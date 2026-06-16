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

	openaiext "github.com/cloudwego/eino-ext/components/model/openai"
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
	maxTurnDuration  int                     // seconds, max time for a single LLM stream turn (0 = default 120s)
	maxStepDuration  int                     // seconds, per-step watchdog timeout (0 = disabled)
	contextTokens    *atomic.Int64           // last context size reported by ContextRewriter (agent-scoped, survives across turns)
	tokenCalibrator  *agents.TokenCalibrator // empirical chars/token from provider usage; feeds the rewriter budget (nil when no max context)

	// ownedRun is our hand-built ReAct graph. It routes budget/loop terminations
	// into a tool-less finalize node so the model summarises instead of emitting a
	// hardcoded apology.
	ownedRun compose.Runnable[[]*schema.Message, *schema.Message]

	// chatCallOptions are extra per-run options designated to the chat node —
	// currently the prompt-cache payload modifier. Empty when caching is off.
	chatCallOptions []compose.Option
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
	// tokenCalibrator is agent-scoped likewise: the rewriter records each request's
	// char size and the model callback records the provider's prompt_tokens, so the
	// budget decision tracks real tokenization instead of a fixed chars/token guess.
	var tokenCalibrator *agents.TokenCalibrator

	// Extract per-turn timing budgets from config and clamp BEFORE anything consumes
	// them (the MessageModifier soft-landing reads maxTurnDuration too).
	// maxTurnDuration: 0 = use default 120s. maxStepDuration: 0 = watchdog disabled.
	maxTurnDuration := 0
	maxStepDuration := 0
	if config.AgentConfig != nil {
		maxTurnDuration = config.AgentConfig.MaxTurnDuration
		maxStepDuration = config.AgentConfig.MaxStepDuration
	}
	// Defense-in-depth runtime clamp: max_turn_duration is bounded by API-layer
	// validation and a DB CHECK (migration 013), but this guards the in-process
	// path regardless — a stale or non-API-written row carrying an out-of-range
	// value would, multiplied into a time.Duration, overflow int64-ns to a negative
	// (already-expired) or effectively-infinite deadline — or trip the modifier's
	// soft-landing from t≈0. Reset anything outside [0, 86400] to 0 (engine
	// default) rather than trust the persisted value.
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
			// Agent-scoped state: created before the graph is built, captured by the
			// rewriter closure, stored on the Agent. Survives across per-turn
			// NewBuilder calls and error-retries.
			contextTokensCounter = &atomic.Int64{}
			tokenCalibrator = agents.NewTokenCalibrator()

			// Defense-in-depth runtime clamp: max_context_size is bounded by API-layer
			// validation, but a stale or non-API-written row carrying an absurd value
			// would overflow the rewriter's token×ratio char arithmetic. Cap it at the
			// ceiling (not 0 — that would mean "unlimited" and skip compression).
			maxContextSize := config.AgentConfig.MaxContextSize
			if maxContextSize > domain.MaxContextSizeCeiling {
				slog.WarnContext(ctx, "max_context_size above ceiling; clamping",
					"value", maxContextSize, "ceiling", domain.MaxContextSizeCeiling)
				maxContextSize = domain.MaxContextSizeCeiling
			}

			// The rewriter sees conversation messages only; the system prompt is
			// injected by the MessageModifier and the tool schemas by the model
			// binding, both AFTER the rewriter runs. Feed their sizes in as fixed
			// overhead so the budget covers the whole request, not just messages.
			systemPromptChars := 0
			if config.AgentConfig.Prompts != nil {
				systemPromptChars = len(config.AgentConfig.Prompts.SystemPrompt)
			}
			messageRewriterFunc = agents.NewContextRewriterFromConfig(agents.ContextRewriterConfig{
				MaxContextTokens:  maxContextSize,
				SystemPromptChars: systemPromptChars,
				ToolSchemaChars:   estimateToolSchemaChars(toolInfos),
				Calibrator:        tokenCalibrator,
				ContextLogger:     contextLogger,
				OnContextSize:     func(n int) { contextTokensCounter.Store(int64(n)) },
			})
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
		toolReturnDirectly:   ownedReturnDirectlyMap(config.AgentConfig, toolInfos),
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
		tokenCalibrator:  tokenCalibrator,
		ownedRun:         ownedRun,
		chatCallOptions:  buildChatCallOptions(config.RequestPayloadModifier, config.SessionID),
	}, nil
}

// buildChatCallOptions assembles the chat-node call options: the prompt-cache
// payload modifier (explicit-cache providers) and the x-session-id header
// (sticky-routing for auto-cache providers). Either may be absent; an empty slice
// leaves the request shape unchanged.
func buildChatCallOptions(modifier func([]byte) ([]byte, error), sessionID string) []compose.Option {
	var opts []compose.Option
	if modifier != nil {
		opts = append(opts, compose.WithChatModelOption(
			openaiext.WithRequestPayloadModifier(
				func(_ context.Context, _ []*schema.Message, rawBody []byte) ([]byte, error) {
					return modifier(rawBody)
				},
			),
		).DesignateNode(ownedNodeChat))
	}
	// A stable per-conversation session id lets OpenRouter sticky-route every step
	// and turn to the same upstream provider, keeping its automatic prefix cache
	// warm (the only lever for auto-cache providers like Qwen, which ignore
	// cache_control). Plain header — non-OpenRouter providers ignore it, and the
	// request body is untouched.
	if headerSafeSessionID(sessionID) {
		opts = append(opts, compose.WithChatModelOption(
			openaiext.WithExtraHeader(map[string]string{"x-session-id": sessionID}),
		).DesignateNode(ownedNodeChat))
	}
	return opts
}

// headerSafeSessionID reports whether the session id is safe to send as an HTTP
// header value and within OpenRouter's 256-char limit. The session id is
// client-supplied (chat request body) and unvalidated upstream; Go's transport
// rejects control characters in header values, so a malformed id would otherwise
// fail the turn at send time. We skip the sticky header for unsafe ids instead —
// graceful degradation, never a broken turn.
func headerSafeSessionID(s string) bool {
	if s == "" || len(s) > 256 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] > 0x7e { // printable ASCII only
			return false
		}
	}
	return true
}

// promptTokensRecorder returns the calibrator's prompt_tokens sink, or nil when no
// context budget is configured (so the callback builder skips the wiring).
func (a *Agent) promptTokensRecorder() func(int) {
	if a.tokenCalibrator == nil {
		return nil
	}
	return a.tokenCalibrator.RecordPromptTokens
}

// estimateToolSchemaChars approximates the byte size of the tool/function schemas
// sent to the provider on every request. It is a fixed per-request overhead the
// context budget must account for; the TokenCalibrator later absorbs any residual
// estimate error from the real prompt_tokens.
func estimateToolSchemaChars(toolInfos []*schema.ToolInfo) int {
	total := 0
	for _, info := range toolInfos {
		if info == nil {
			continue
		}
		if b, err := json.Marshal(info); err == nil {
			total += len(b)
			continue
		}
		total += len(info.Name) + len(info.Desc)
	}
	return total
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

// ownedReturnDirectlyMap builds the return-directly tool set: the built-in HITL
// tools (which halt the loop and wait for the user by definition), unioned with
// names from the agent config (the global/baked tool_return_directly) and tools
// that self-declare it via ToolInfo.Extra (e.g. an MCP tool carrying the
// return-directly _meta). A fresh map is returned so the shared config is never
// mutated; nil when empty so the loop's default (no early return) is unchanged.
func ownedReturnDirectlyMap(cfg *config.AgentConfig, toolInfos []*schema.ToolInfo) map[string]struct{} {
	var set map[string]struct{}
	add := func(name string) {
		if name == "" {
			return
		}
		if set == nil {
			set = make(map[string]struct{})
		}
		set[name] = struct{}{}
	}
	// Built-in HITL tools (show_structured_output) drive the same
	// tools→direct_return→END route as config/Extra-declared tools; the HITL
	// surfacing (drop prose, no answer event) is applied downstream by HITLSeen.
	// Routing them here is the single seam that ties the HITL classification to
	// the loop halt, so a new HITL tool cannot forget to stop the loop.
	for _, name := range domain.HITLToolNames() {
		add(name)
	}
	if cfg != nil {
		for name := range cfg.ToolReturnDirectly {
			add(name)
		}
	}
	for _, info := range toolInfos {
		if info == nil || info.Extra == nil {
			continue
		}
		if v, _ := info.Extra[domain.ToolExtraReturnDirectly].(bool); v {
			add(info.Name)
		}
	}
	return set
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
