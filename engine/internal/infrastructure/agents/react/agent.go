package react

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/agents"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/agents/callbacks"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/llm"
	"github.com/syntheticinc/syntheticbrew/pkg/config"
	"github.com/syntheticinc/syntheticbrew/pkg/errors"
)

// openAIToolNamePattern is the regex OpenAI applies to function tool names.
const openAIToolNamePattern = `^[a-zA-Z0-9_-]+$`

// maxTurnDurationCeiling is the runtime upper bound (seconds) for a turn's
// wall-clock budget — mirrors the API-layer validation (validateMaxTurnDuration)
// and keeps the value far below time.Duration int64-nanosecond overflow.
const maxTurnDurationCeiling = 86400

var openAIToolNameRegex = regexp.MustCompile(openAIToolNamePattern)

// Agent runs one ReAct turn through our owned compose.Graph (built in NewAgent).
type Agent struct {
	contextLogger    *agents.ContextLogger
	modelName        string
	historyMessages  []*schema.Message
	sessionID        string
	agentID          string   // "supervisor" or "code-agent-{uuid}"
	toolNames        []string // List of available tool names for system prompt injection
	messageModifier  *MessageModifier
	toolCallRecorder ToolCallRecorder
	maxTurnDuration  int           // seconds, max time for a single LLM stream turn (0 = default 120s)
	maxStepDuration  int           // seconds, per-step watchdog timeout (0 = disabled)
	contextTokens    *atomic.Int64 // last context size reported by ContextRewriter (agent-scoped, survives across turns)

	// ownedRun is our hand-built ReAct graph. It routes budget/loop terminations
	// into a tool-less finalize node so the model summarises instead of emitting a
	// hardcoded apology.
	ownedRun compose.Runnable[[]*schema.Message, *schema.Message]
}

// NewAgent creates a new ReAct agent
func NewAgent(ctx context.Context, config AgentConfig) (*Agent, error) {
	if config.ChatModel == nil {
		return nil, errors.New(errors.CodeInvalidInput, "chat model is required")
	}

	// MaxSteps: 0 means "unlimited". The owned graph derives its own step budget
	// (ownedStepBudget) and the eino hard-wall backstop (ownedBackstopSteps) from
	// this directly; nothing else needs the value here.
	slog.InfoContext(ctx, "creating ReAct agent", "tools_count", len(config.Tools), "max_steps", config.MaxSteps)

	// Closures the owned graph is wired from. Declared up front because they are
	// populated inside the tools / AgentConfig blocks below and consumed when the
	// graph is built.
	var (
		unknownToolsHandler  func(ctx context.Context, name, input string) (string, error)
		toolArgumentsHandler func(ctx context.Context, name, arguments string) (string, error)
		messageModifierFunc  func(ctx context.Context, input []*schema.Message) []*schema.Message
		messageRewriterFunc  func(ctx context.Context, input []*schema.Message) []*schema.Message
		streamToolChecker    func(ctx context.Context, sr *schema.StreamReader[*schema.Message]) (bool, error)
	)

	// Add tools if provided and collect tool names for error messages
	var toolNames []string
	var toolInfos []*schema.ToolInfo // owned-graph needs the full ToolInfo, not just names
	if len(config.Tools) > 0 {
		slog.InfoContext(ctx, "adding tools to ReAct agent", "tools_count", len(config.Tools))
		// Collect tool names for error messages
		for _, t := range config.Tools {
			info, err := t.Info(ctx)
			if err == nil && info != nil {
				toolNames = append(toolNames, info.Name)
				toolInfos = append(toolInfos, info)
			}
		}

		// Early-reject tool names that OpenAI's regex would 400 on.
		if llm.IsOpenAIStrictRoute(config.ProviderType, config.ModelName, config.ProviderBaseURL) {
			for _, name := range toolNames {
				if !openAIToolNameRegex.MatchString(name) {
					return nil, errors.New(errors.CodeInvalidInput,
						fmt.Sprintf("tool name %q is not OpenAI-compatible (must match %s); rename the tool in its MCP source or attach a non-OpenAI model",
							name, openAIToolNamePattern))
				}
			}
		}

		// Handle hallucinated tool calls gracefully — return error as tool result (not hard error)
		// This preserves conversation structure: assistant(tool_calls) → tool(result) → assistant
		availableTools := strings.Join(toolNames, ", ")
		unknownToolsHandler = func(ctx context.Context, name, input string) (string, error) {
			slog.WarnContext(ctx, "model called non-existent tool", "tool_name", name)
			return fmt.Sprintf("[ERROR] Tool '%s' does not exist. Available tools: %s. Use ONLY these tools.", name, availableTools), nil
		}

		// Handle malformed tool arguments (JSON parsing errors, XML tags)
		toolArgumentsHandler = func(ctx context.Context, name, arguments string) (string, error) {
			if json.Valid([]byte(arguments)) {
				return arguments, nil
			}
			sanitized := sanitizeToolArguments(arguments)
			slog.WarnContext(ctx, "sanitized malformed tool arguments",
				"tool", name, "original_length", len(arguments), "sanitized_length", len(sanitized))
			return sanitized, nil
		}
	} else {
		slog.WarnContext(ctx, "no tools provided to ReAct agent")
	}

	// Create context logger with configured path, session ID, and token limit
	var contextLogger *agents.ContextLogger
	if config.AgentConfig != nil && config.AgentConfig.ContextLogPath != "" && config.SessionID != "" {
		maxContextTokens := config.AgentConfig.MaxContextSize
		if maxContextTokens <= 0 {
			maxContextTokens = 16000 // default
		}
		agentID := config.AgentID
		if agentID == "" {
			agentID = "supervisor"
		}
		if config.ParentAgentID != "" || config.SubtaskID != "" {
			// Code Agent — use agent-specific logger
			contextLogger = agents.NewContextLoggerForAgent(
				config.AgentConfig.ContextLogPath, config.SessionID,
				agentID, config.ParentAgentID, config.SubtaskID,
				maxContextTokens,
			)
		} else {
			// Supervisor — use standard logger
			contextLogger = agents.NewContextLoggerWithLimit(config.AgentConfig.ContextLogPath, config.SessionID, maxContextTokens)
		}
		// Share session directory name if provided by parent
		if config.SessionDirName != "" {
			contextLogger.SetSessionDirName(config.SessionDirName)
		}
		slog.InfoContext(ctx, "context logger created",
			"path", config.AgentConfig.ContextLogPath,
			"session_id", config.SessionID,
			"agent_id", agentID,
			"max_context_tokens", maxContextTokens)
	} else {
		slog.WarnContext(ctx, "context logger not created",
			"agent_config_nil", config.AgentConfig == nil,
			"context_log_path_empty", config.AgentConfig != nil && config.AgentConfig.ContextLogPath == "",
			"session_id_empty", config.SessionID == "")
	}

	// modifier is declared outside the if block so it's accessible in the return statement
	var modifier *MessageModifier
	// contextTokensCounter is agent-scoped: created before react.NewAgent(), captured by
	// ContextRewriter closure, stored in our Agent struct. Survives across turns and retries.
	var contextTokensCounter *atomic.Int64

	// Extract per-turn timing budgets from config and clamp BEFORE anything consumes
	// them (the MessageModifier soft-landing reads maxTurnDuration too).
	// maxTurnDuration: 0 = use default 120s. maxStepDuration: 0 = watchdog disabled.
	maxTurnDuration := 0
	maxStepDuration := 0
	if config.AgentConfig != nil {
		maxTurnDuration = config.AgentConfig.MaxTurnDuration
		maxStepDuration = config.AgentConfig.MaxStepDuration
	}
	// Defense-in-depth runtime clamp: max_turn_duration has API-layer validation
	// but (unlike max_step_duration) no DB CHECK, so a stale or non-API-written
	// row could carry an out-of-range value that, multiplied into a time.Duration,
	// overflows int64-ns to a negative (already-expired) or effectively-infinite
	// deadline — or trips the modifier's soft-landing from t≈0. Reset anything
	// outside [0, 86400] to 0 (engine default) rather than trust the persisted value.
	if maxTurnDuration < 0 || maxTurnDuration > maxTurnDurationCeiling {
		slog.WarnContext(ctx, "max_turn_duration out of range; using engine default",
			"value", maxTurnDuration, "ceiling", maxTurnDurationCeiling)
		maxTurnDuration = 0
	}

	// Add AgentConfig if provided
	if config.AgentConfig != nil {
		promptLen := 0
		if config.AgentConfig.Prompts != nil {
			promptLen = len(config.AgentConfig.Prompts.SystemPrompt)
		}
		slog.InfoContext(ctx, "processing AgentConfig",
			"system_prompt_length", promptLen,
			"max_context_size", config.AgentConfig.MaxContextSize,
			"max_turn_duration", config.AgentConfig.MaxTurnDuration,
			"enable_tool_call_checker", config.AgentConfig.EnableEnhancedToolCallChecker)

		// Add MessageModifier (AgentPrompts) if system prompt is provided
		systemPrompt := ""
		urgencyWarning := ""
		if config.AgentConfig.Prompts != nil {
			systemPrompt = config.AgentConfig.Prompts.SystemPrompt
			urgencyWarning = config.AgentConfig.Prompts.UrgencyWarning
		}
		if systemPrompt != "" {
			slog.InfoContext(ctx, "adding MessageModifier with system prompt")

			// Build list of context reminder providers
			var reminderProviders []ContextReminderProvider
			// Add external providers (e.g., WorkContextReminder)
			reminderProviders = append(reminderProviders, config.ContextReminderProviders...)

			// Create MessageModifier with reminder providers instead of direct PlanManager dependency
			modifier = NewMessageModifier(MessageModifierConfig{
				SystemPrompt:      systemPrompt,
				UrgencyWarning:    urgencyWarning,
				MaxSteps:          config.MaxSteps,
				MaxTurnDuration:   maxTurnDuration,
				ContextLogger:     contextLogger,
				ReminderProviders: reminderProviders,
				SessionID:         config.SessionID,
				ToolNames:         toolNames,
			})

			messageModifierFunc = modifier.BuildModifierFunc()
		}

		// Add MessageRewriter (ContextRewriter) if max context size is set
		if config.AgentConfig.MaxContextSize > 0 {
			// Agent-scoped counter: created before react.NewAgent(), captured by rewriter closure,
			// stored in our Agent struct. Survives across per-turn NewBuilder calls and error-retries.
			contextTokensCounter = &atomic.Int64{}
			// Eino's MessageRewriter receives conversation messages WITHOUT system prompt
			// (system prompt is injected separately by MessageModifier).
			// Estimate system prompt tokens so context_tokens reflects the full context window usage.
			systemPromptTokens := 0
			if config.AgentConfig.Prompts != nil {
				systemPromptTokens = len(config.AgentConfig.Prompts.SystemPrompt) / 4 // ~4 chars/token
			}
			messageRewriterFunc = agents.NewContextRewriterWithLogging(
				config.AgentConfig.MaxContextSize,
				contextLogger,
				func(n int) { contextTokensCounter.Store(int64(n + systemPromptTokens)) },
			)
		}

		// Add StreamToolCallChecker (EnhancedStreamToolCallChecker) if enabled
		if config.AgentConfig.EnableEnhancedToolCallChecker {
			streamToolChecker = agents.NewEnhancedStreamToolCallChecker()
		}
	}

	agentID := config.AgentID
	if agentID == "" {
		agentID = "supervisor"
	}

	// Build the owned ReAct graph: our hand-built loop with budget/loop routing
	// into a tool-less finalize node.
	ownedRun, err := buildOwnedGraph(ctx, ownedGraphConfig{
		model:                config.ChatModel,
		tools:                config.Tools,
		toolInfos:            toolInfos,
		maxStep:              ownedBackstopSteps(config.MaxSteps),
		stepBudget:           ownedStepBudget(config.MaxSteps),
		maxTurnDuration:      time.Duration(maxTurnDuration) * time.Second,
		messageModifier:      messageModifierFunc,
		messageRewriter:      messageRewriterFunc,
		toolReturnDirectly:   ownedReturnDirectlyMap(config.AgentConfig),
		streamToolChecker:    streamToolChecker,
		unknownToolsHandler:  unknownToolsHandler,
		toolArgumentsHandler: toolArgumentsHandler,
		executeSequentially:  config.SequentialTools,
		onTerminal: func(runCtx context.Context, reason callbacks.TerminalReason, tool, detail string) {
			if h := terminalHolderFrom(runCtx); h != nil {
				h.set(reason, tool, detail)
			}
			slog.InfoContext(runCtx, "[OWNED] turn diverting to finalize", "reason", reason.String(), "tool", tool)
		},
	})
	if err != nil {
		return nil, errors.Wrap(err, errors.CodeInternal, "failed to build owned react graph")
	}

	return &Agent{
		contextLogger:    contextLogger,
		modelName:        config.ModelName,
		historyMessages:  config.HistoryMessages,
		sessionID:        config.SessionID,
		agentID:          agentID,
		toolNames:        toolNames,
		messageModifier:  modifier,
		toolCallRecorder: config.ToolCallRecorder,
		maxTurnDuration:  maxTurnDuration,
		maxStepDuration:  maxStepDuration,
		contextTokens:    contextTokensCounter,
		ownedRun:         ownedRun,
	}, nil
}

// ownedStepBudget maps the configured MaxSteps (tool-round budget; 0 = unlimited)
// to the owned graph's finalize threshold.
func ownedStepBudget(maxSteps int) int {
	if maxSteps <= 0 {
		return 0 // unlimited (effectiveStepBudget treats 0 as no wall)
	}
	return maxSteps
}

// ownedBackstopSteps sizes eino's hard WithMaxRunSteps wall ABOVE our step
// budget (in node-transition terms ~3 per tool round) so OUR routing reaches the
// finalize node first and the hard wall is only a runaway backstop.
func ownedBackstopSteps(maxSteps int) int {
	if maxSteps <= 0 {
		return 10000
	}
	return maxSteps*3 + 10
}

// ownedReturnDirectlyMap extracts the return-directly tool set from config.
func ownedReturnDirectlyMap(cfg *config.AgentConfig) map[string]struct{} {
	if cfg == nil || len(cfg.ToolReturnDirectly) == 0 {
		return nil
	}
	return cfg.ToolReturnDirectly
}

// lastContextTokens returns the last context size reported by the ContextRewriter.
// Returns 0 if no rewriter is configured or it hasn't fired yet.
func (a *Agent) lastContextTokens() int {
	if a.contextTokens == nil {
		return 0
	}
	return int(a.contextTokens.Load())
}

// GetSessionDirName returns the session directory name from the context logger
func (a *Agent) GetSessionDirName() string {
	if a.contextLogger == nil {
		return ""
	}
	return a.contextLogger.GetSessionDirName()
}

// buildMessagesWithHistory creates message list with conversation history and current input
func (a *Agent) buildMessagesWithHistory(input string) []*schema.Message {
	var messages []*schema.Message

	// Add history messages first (if any)
	if len(a.historyMessages) > 0 {
		messages = append(messages, a.historyMessages...)
		slog.InfoContext(context.Background(), "added history messages to context", "count", len(a.historyMessages))
	}

	// Add current user message
	messages = append(messages, &schema.Message{
		Role:    schema.User,
		Content: input,
	})

	return messages
}

// RunWithCallbacks executes the agent with given input and event callbacks
// through the owned graph (non-streaming). Budget/loop terminations are routed to
// the finalize node so the model summarises; see runOwned.
func (a *Agent) RunWithCallbacks(ctx context.Context, input string, eventCallback func(event *domain.AgentEvent) error) (string, error) {
	return a.runOwned(ctx, input, eventCallback)
}

// Stream executes the agent with streaming output through the owned graph.
// Budget/loop terminations are routed to the finalize node so the model
// summarises from gathered context; see streamOwned.
func (a *Agent) Stream(ctx context.Context, input string, callback func(chunk string) error, eventCallback func(event *domain.AgentEvent) error) error {
	return a.streamOwned(ctx, input, callback, eventCallback)
}

// stepWatchdogDuration converts the configured per-step timeout (seconds) into a
// duration; non-positive disables the watchdog.
func (a *Agent) stepWatchdogDuration() time.Duration {
	if a.maxStepDuration <= 0 {
		return 0
	}
	return time.Duration(a.maxStepDuration) * time.Second
}

// sanitizeToolArguments attempts to extract valid JSON from malformed tool arguments.
// Handles common issues: XML tags wrapping JSON, extra whitespace, mixed content.
// Returns sanitized JSON if possible, otherwise returns original input.
func sanitizeToolArguments(arguments string) string {
	// Remove XML tags (e.g., <parameter>{...}</parameter>)
	xmlTagPattern := regexp.MustCompile(`<[^>]+>`)
	cleaned := xmlTagPattern.ReplaceAllString(arguments, "")
	cleaned = strings.TrimSpace(cleaned)

	// Check if result is already valid JSON after tag removal
	if json.Valid([]byte(cleaned)) {
		return cleaned
	}

	// Try to extract JSON object from mixed content (e.g., "text {...} text")
	jsonObjectPattern := regexp.MustCompile(`\{[^{}]*\}`)
	matches := jsonObjectPattern.FindAllString(cleaned, -1)
	for _, match := range matches {
		if json.Valid([]byte(match)) {
			return match
		}
	}

	// No valid JSON found — return original input (tool will handle the error)
	return arguments
}
