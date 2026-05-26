package react

import (
	"context"
	"encoding/json"
	stdErrors "errors"
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"
	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/agents"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/agents/callbacks"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/llm"
	"github.com/syntheticinc/syntheticbrew/pkg/errors"
)

// openAIToolNamePattern is the regex OpenAI applies to function tool names.
const openAIToolNamePattern = `^[a-zA-Z0-9_-]+$`

var openAIToolNameRegex = regexp.MustCompile(openAIToolNamePattern)

// Agent wraps Eino ReAct agent with additional functionality
type Agent struct {
	agent            *react.Agent
	contextLogger    *agents.ContextLogger
	modelName        string
	historyMessages  []*schema.Message
	stepContentStore *agents.StepContentStore
	sessionID        string
	agentID          string   // "supervisor" or "code-agent-{uuid}"
	toolNames        []string // List of available tool names for system prompt injection
	messageModifier  *MessageModifier
	toolCallRecorder ToolCallRecorder
	maxTurnDuration  int           // seconds, max time for a single LLM stream turn (0 = default 120s)
	contextTokens    *atomic.Int64 // last context size reported by ContextRewriter (agent-scoped, survives across turns)
}

// NewAgent creates a new ReAct agent
func NewAgent(ctx context.Context, config AgentConfig) (*Agent, error) {
	if config.ChatModel == nil {
		return nil, errors.New(errors.CodeInvalidInput, "chat model is required")
	}

	slog.InfoContext(ctx, "creating ReAct agent", "tools_count", len(config.Tools))

	// Set max steps
	// 0 means "unlimited" — use a very large number since Eino requires MaxStep >= 1
	maxSteps := config.MaxSteps
	if maxSteps <= 0 {
		maxSteps = 10000
		slog.InfoContext(ctx, "maxSteps is 0 (unlimited), using 10000")
	} else {
		slog.InfoContext(ctx, "maxSteps set", "maxSteps", maxSteps)
	}

	// Create ReAct agent with configuration
	// Use ToolCallingModel instead of deprecated Model for proper native tool calls
	agentConfig := &react.AgentConfig{
		ToolCallingModel: config.ChatModel,
		MaxStep:          maxSteps,
	}

	// Add tools if provided and collect tool names for error messages
	var toolNames []string
	if len(config.Tools) > 0 {
		slog.InfoContext(ctx, "adding tools to ReAct agent", "tools_count", len(config.Tools))
		agentConfig.ToolsConfig.Tools = config.Tools
		agentConfig.ToolsConfig.ExecuteSequentially = config.SequentialTools
		// Collect tool names for error messages
		for _, t := range config.Tools {
			info, err := t.Info(ctx)
			if err == nil && info != nil {
				toolNames = append(toolNames, info.Name)
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
		agentConfig.ToolsConfig.UnknownToolsHandler = func(ctx context.Context, name, input string) (string, error) {
			slog.WarnContext(ctx, "model called non-existent tool", "tool_name", name)
			return fmt.Sprintf("[ERROR] Tool '%s' does not exist. Available tools: %s. Use ONLY these tools.", name, availableTools), nil
		}

		// Handle malformed tool arguments (JSON parsing errors, XML tags)
		agentConfig.ToolsConfig.ToolArgumentsHandler = func(ctx context.Context, name, arguments string) (string, error) {
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

	// Create shared store for accumulated content
	// This is used to recover content that gets lost in eino's streaming mode
	stepContentStore := agents.NewStepContentStore()

	// modifier is declared outside the if block so it's accessible in the return statement
	var modifier *MessageModifier
	// contextTokensCounter is agent-scoped: created before react.NewAgent(), captured by
	// ContextRewriter closure, stored in our Agent struct. Survives across turns and retries.
	var contextTokensCounter *atomic.Int64

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
				MaxSteps:          maxSteps,
				StepContentStore:  stepContentStore,
				ContextLogger:     contextLogger,
				ReminderProviders: reminderProviders,
				SessionID:         config.SessionID,
				ToolNames:         toolNames,
			})

			agentConfig.MessageModifier = modifier.BuildModifierFunc()
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
			agentConfig.MessageRewriter = agents.NewContextRewriterWithLogging(
				config.AgentConfig.MaxContextSize,
				contextLogger,
				func(n int) { contextTokensCounter.Store(int64(n + systemPromptTokens)) },
			)
		}

		// Add StreamToolCallChecker (EnhancedStreamToolCallChecker) if enabled
		if config.AgentConfig.EnableEnhancedToolCallChecker {
			agentConfig.StreamToolCallChecker = agents.NewEnhancedStreamToolCallChecker()
		}

		// Add ToolReturnDirectly if configured
		if len(config.AgentConfig.ToolReturnDirectly) > 0 {
			agentConfig.ToolReturnDirectly = config.AgentConfig.ToolReturnDirectly
		}
	}

	agent, err := react.NewAgent(ctx, agentConfig)
	if err != nil {
		return nil, errors.Wrap(err, errors.CodeInternal, "failed to create react agent")
	}

	agentID := config.AgentID
	if agentID == "" {
		agentID = "supervisor"
	}

	// Extract maxTurnDuration from config (0 = use default 120s)
	maxTurnDuration := 0
	if config.AgentConfig != nil {
		maxTurnDuration = config.AgentConfig.MaxTurnDuration
	}

	return &Agent{
		agent:            agent,
		contextLogger:    contextLogger,
		modelName:        config.ModelName,
		historyMessages:  config.HistoryMessages,
		stepContentStore: stepContentStore,
		sessionID:        config.SessionID,
		agentID:          agentID,
		toolNames:        toolNames,
		messageModifier:  modifier,
		toolCallRecorder: config.ToolCallRecorder,
		maxTurnDuration:  maxTurnDuration,
		contextTokens:    contextTokensCounter,
	}, nil
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

// Run executes the agent with given input
func (a *Agent) Run(ctx context.Context, input string) (string, error) {
	// Build messages with history
	messages := a.buildMessagesWithHistory(input)

	// Log initial context
	if a.contextLogger != nil {
		a.contextLogger.LogContext(ctx, messages, 0)
	}

	// Run agent
	result, err := a.agent.Generate(ctx, messages)
	if err != nil {
		return "", errors.Wrap(err, errors.CodeInternal, "agent execution failed")
	}

	// Add final answer to messages for context logging
	if result.Content != "" {
		finalMessage := &schema.Message{
			Role:    schema.Assistant,
			Content: result.Content,
		}
		messages = append(messages, finalMessage)
	}

	// Log final context with the complete conversation including agent's answer
	if a.contextLogger != nil {
		a.contextLogger.LogContextSummary(ctx, messages)
	}

	return result.Content, nil
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

// RunWithCallbacks executes the agent with given input and event callbacks.
// Single-pass: runs one Eino REACT loop and returns.
func (a *Agent) RunWithCallbacks(ctx context.Context, input string, eventCallback func(event *domain.AgentEvent) error) (string, error) {
	messages := a.buildMessagesWithHistory(input)

	if a.contextLogger != nil {
		a.contextLogger.LogContext(ctx, messages, 0)
	}

	cb := callbacks.NewBuilder(callbacks.BuilderConfig{
		EventCallback:    eventCallback,
		Store:            a.stepContentStore,
		SessionID:        a.sessionID,
		AgentID:          a.agentID,
		ToolCallRecorder: a.toolCallRecorder,
	})
	callbackOpt := cb.BuildCallbackOption()
	slog.InfoContext(ctx, "[RUN] calling agent.Generate...")

	const maxErrorRetries = 2
	const maxRateLimitRetries = 5
	var result *schema.Message
	var err error
	var rateLimitCount int

	for retryCount := 0; retryCount <= maxErrorRetries; retryCount++ {
		result, err = a.agent.Generate(ctx, messages, callbackOpt)
		if err == nil {
			break
		}

		switch classifyRecovery(err) {
		case recoveryBackoff:
			rateLimitCount++
			if rateLimitCount > maxRateLimitRetries {
				slog.ErrorContext(ctx, "[RUN] rate limit persists after max retries",
					"error", err, "retries", rateLimitCount)
				return "", errors.Wrap(err, errors.CodeInternal, "rate limit exceeded after retries")
			}

			backoff := rateLimitBackoff(rateLimitCount - 1)
			slog.WarnContext(ctx, "[RUN] rate limit hit, backing off",
				"error", err, "attempt", rateLimitCount, "backoff", backoff)

			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return "", errors.Wrap(ctx.Err(), errors.CodeInternal, "context cancelled during rate limit backoff")
			}

			retryCount-- // Don't count rate limit retries against regular retry budget
			continue

		case recoveryAbortCancelled, recoveryAbortNonRecoverable:
			slog.ErrorContext(ctx, "[RUN] agent.Generate failed with non-recoverable error", "error", err)
			return "", errors.Wrap(err, errors.CodeInternal, "agent execution failed")

		case recoveryFeedback:
			if retryCount >= maxErrorRetries {
				slog.ErrorContext(ctx, "[RUN] agent.Generate failed after retries", "error", err, "retries", retryCount)
				return "", errors.Wrap(err, errors.CodeInternal, "agent execution failed after retries")
			}

			slog.WarnContext(ctx, "[RUN] recoverable error, adding feedback and retrying",
				"error", err, "attempt", retryCount+1)

			errorFeedback := formatAgentErrorFeedback(err)
			messages = append(messages, &schema.Message{
				Role:    schema.System,
				Content: errorFeedback,
			})

			cb = callbacks.NewBuilder(callbacks.BuilderConfig{
				EventCallback:    eventCallback,
				Store:            a.stepContentStore,
				SessionID:        a.sessionID,
				AgentID:          a.agentID,
				ToolCallRecorder: a.toolCallRecorder,
			})
			callbackOpt = cb.BuildCallbackOption()
		}
	}

	// Drop residual content on HITL turns — the widget is the message.
	if cb.HITLSeen() && result.Content != "" {
		slog.InfoContext(ctx, "[RUN] suppressing assistant content on HITL turn",
			"dropped_length", len(result.Content))
		result.Content = ""
	}

	if result.Content != "" {
		messages = append(messages, &schema.Message{
			Role:    schema.Assistant,
			Content: result.Content,
		})
	}

	if result.Content != "" && eventCallback != nil {
		eventCallback(&domain.AgentEvent{
			Type:       domain.EventTypeAnswer,
			Timestamp:  time.Now(),
			Step:       cb.GetStep(),
			Content:    result.Content,
			IsComplete: true,
		})
	}

	// Emit cumulative token usage for this turn (with context window size from rewriter)
	cb.EmitTokenUsage(ctx, a.lastContextTokens())

	if a.contextLogger != nil {
		a.contextLogger.LogContextSummary(ctx, messages)
	}

	slog.InfoContext(ctx, "[RUN] completed successfully")
	return result.Content, nil
}

// Stream executes the agent with streaming output using typed callbacks.
// Single-pass: runs one Eino REACT loop and returns.
// The Orchestrator handles continuation logic externally via events.
func (a *Agent) Stream(ctx context.Context, input string, callback func(chunk string) error, eventCallback func(event *domain.AgentEvent) error) error {
	slog.InfoContext(ctx, "[STREAM] Starting Stream method", "input_length", len(input))

	messages := a.buildMessagesWithHistory(input)
	slog.InfoContext(ctx, "[STREAM] Built messages with history", "message_count", len(messages))

	if a.contextLogger != nil {
		a.contextLogger.LogContext(ctx, messages, 0)
	}

	// Accumulate final answer content
	var finalContent string

	wrappedCallback := func(chunk string) error {
		finalContent += chunk
		slog.DebugContext(ctx, "[STREAM] accumulated chunk", "chunk_length", len(chunk), "total_length", len(finalContent))
		if callback != nil {
			return callback(chunk)
		}
		return nil
	}

	cb := callbacks.NewBuilder(callbacks.BuilderConfig{
		EventCallback:    eventCallback,
		ChunkCallback:    wrappedCallback,
		Store:            a.stepContentStore,
		SessionID:        a.sessionID,
		AgentID:          a.agentID,
		ToolCallRecorder: a.toolCallRecorder,
	})
	callbackOpt := cb.BuildCallbackOption()
	slog.InfoContext(ctx, "[STREAM] Created callback handler, calling agent.Stream...")

	// Retry on recoverable errors
	const maxErrorRetries = 2
	const maxRateLimitRetries = 5
	// Use per-agent max_turn_duration from config; fall back to 120s default
	streamTimeout := 120 * time.Second
	if a.maxTurnDuration > 0 {
		streamTimeout = time.Duration(a.maxTurnDuration) * time.Second
	}
	var reader *schema.StreamReader[*schema.Message]
	var err error
	var rateLimitCount int

	var activeStreamCancel context.CancelFunc // cancel for successful stream (deferred cleanup)
	for retryCount := 0; retryCount <= maxErrorRetries; retryCount++ {
		// Per-attempt timeout prevents hanging on unresponsive LLM providers.
		// If the provider hangs after an EOF or during retry, we fail fast
		// instead of blocking the SSE connection indefinitely.
		var streamCtx context.Context
		var streamCancel context.CancelFunc
		streamCtx, streamCancel = context.WithTimeout(ctx, streamTimeout)
		reader, err = a.agent.Stream(streamCtx, messages, callbackOpt)
		if err == nil {
			// Keep context alive for drain loop; cancel deferred at function exit
			activeStreamCancel = streamCancel
			break
		}
		streamCancel() // cancel failed attempt immediately

		switch classifyRecovery(err) {
		case recoveryBackoff:
			rateLimitCount++
			if rateLimitCount > maxRateLimitRetries {
				slog.ErrorContext(ctx, "[STREAM] rate limit persists after max retries",
					"error", err, "retries", rateLimitCount)
				return errors.Wrap(err, errors.CodeInternal, "rate limit exceeded after retries")
			}

			backoff := rateLimitBackoff(rateLimitCount - 1)
			slog.WarnContext(ctx, "[STREAM] rate limit hit, backing off",
				"error", err, "attempt", rateLimitCount, "backoff", backoff)

			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return errors.Wrap(ctx.Err(), errors.CodeInternal, "context cancelled during rate limit backoff")
			}

			retryCount-- // Don't count rate limit retries against regular retry budget
			continue

		case recoveryAbortCancelled:
			// Client cancel / deadline — return unwrapped so callers see
			// canonical context errors via errors.Is.
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err

		case recoveryAbortNonRecoverable:
			slog.ErrorContext(ctx, "[STREAM] agent.Stream failed with non-recoverable error", "error", err)
			return errors.Wrap(err, errors.CodeInternal, "agent stream failed")

		case recoveryFeedback:
			if retryCount >= maxErrorRetries {
				slog.ErrorContext(ctx, "[STREAM] agent.Stream failed after retries", "error", err, "retries", retryCount)
				return errors.Wrap(err, errors.CodeInternal, "agent stream failed after retries")
			}

			slog.WarnContext(ctx, "[STREAM] recoverable error, adding feedback and retrying",
				"error", err, "attempt", retryCount+1)

			errorFeedback := formatAgentErrorFeedback(err)
			messages = append(messages, &schema.Message{
				Role:    schema.System,
				Content: errorFeedback,
			})

			cb = callbacks.NewBuilder(callbacks.BuilderConfig{
				EventCallback:    eventCallback,
				ChunkCallback:    wrappedCallback,
				Store:            a.stepContentStore,
				SessionID:        a.sessionID,
				AgentID:          a.agentID,
				ToolCallRecorder: a.toolCallRecorder,
			})
			callbackOpt = cb.BuildCallbackOption()
		}
	}
	if activeStreamCancel != nil {
		defer activeStreamCancel()
	}
	slog.InfoContext(ctx, "[STREAM] agent.Stream returned reader, starting drain loop...")

	// Drain the main stream — all events are handled by callbacks
	recvCount := 0
	for {
		// Check context before blocking on Recv
		if ctx.Err() != nil {
			slog.InfoContext(ctx, "[STREAM] context cancelled, stopping drain loop", "recv_count", recvCount)
			break
		}
		slog.DebugContext(ctx, "[STREAM] Waiting for reader.Recv...", "recv_count", recvCount)
		_, err := reader.Recv()
		recvCount++
		if err != nil {
			if err == io.EOF {
				slog.InfoContext(ctx, "[STREAM] reader.Recv got EOF, breaking loop", "total_recv", recvCount)
				break
			}
			// Context canceled during Recv — expected on client cancel, not an error
			if ctx.Err() != nil {
				slog.InfoContext(ctx, "[STREAM] context cancelled during recv", "recv_count", recvCount)
				break
			}
			slog.ErrorContext(ctx, "[STREAM] reader.Recv failed", "error", err, "recv_count", recvCount)
			return errors.Wrap(err, errors.CodeInternal, "stream read failed")
		}
		slog.DebugContext(ctx, "[STREAM] reader.Recv successful", "recv_count", recvCount)
	}

	slog.InfoContext(ctx, "[STREAM] Drain loop completed", "total_recv", recvCount)

	// Wait for callback streaming goroutine to finish delivering all chunks.
	// The drain loop reads the main Eino reader fast, but the callback goroutine
	// reads its own tee'd copy and delivers chunks via chunkCb. Without this wait,
	// ProcessingStopped fires before all chunks reach the SSE client.
	cb.WaitStreamDone()
	slog.InfoContext(ctx, "[STREAM] Callback goroutine completed")

	// Finalize any accumulated text that wasn't flushed by onToolStart
	cb.FinalizeAccumulatedText(ctx)

	// Emit cumulative token usage for this turn (consumed by EventStream for the done event)
	cb.EmitTokenUsage(ctx, a.lastContextTokens())

	// Drop streamed content from history on HITL turns (live chunks may
	// have already reached the client; history and final-answer event must not).
	if cb.HITLSeen() && finalContent != "" {
		slog.InfoContext(ctx, "[STREAM] suppressing assistant content on HITL turn",
			"dropped_length", len(finalContent))
		finalContent = ""
	}

	if finalContent != "" {
		messages = append(messages, &schema.Message{
			Role:    schema.Assistant,
			Content: finalContent,
		})
		slog.InfoContext(ctx, "[STREAM] Added final answer to messages", "content_length", len(finalContent))
	}

	if a.contextLogger != nil {
		a.contextLogger.LogContextSummary(ctx, messages)
	}

	slog.InfoContext(ctx, "[STREAM] Stream method completed successfully")
	return nil
}

// recoveryAction describes how the agent retry loop should respond to an
// error returned by agent.Generate / agent.Stream.
type recoveryAction int

const (
	// recoveryAbortCancelled — context was cancelled (user / deadline).
	// Return immediately without wrapping; callers check ctx.Err().
	recoveryAbortCancelled recoveryAction = iota
	// recoveryAbortNonRecoverable — LLM-provider auth failure or Eino
	// step budget exhaustion. Retry will not help; fail fast.
	recoveryAbortNonRecoverable
	// recoveryBackoff — LLM provider rate-limit / quota. Wait with
	// exponential backoff and retry the same request.
	recoveryBackoff
	// recoveryFeedback — recoverable error (parse failure, transient
	// transport hiccup, unknown shape). Inject a system-level hint into
	// the conversation and retry within the regular retry budget.
	recoveryFeedback
)

// classifyRecovery decides the retry strategy for an error surfacing out
// of the Eino ReAct agent. It consults only typed predicates — no
// substring matching on opaque error text — so tool-result content
// (controlled by tool authors / partners) cannot influence the platform's
// recovery decisions.
//
// The single concession to substring matching lives one layer down in
// internal/infrastructure/llm/classify_error.go, where Eino's
// chat-model SDKs return opaque HTTP error strings and there is no
// alternative path to status-code information. Anything that crosses
// this function is already typed.
func classifyRecovery(err error) recoveryAction {
	if err == nil {
		// Defensive — callers should only invoke when err != nil.
		return recoveryFeedback
	}

	// Context cancellation — user cancelled or deadline hit.
	if stdErrors.Is(err, context.Canceled) ||
		stdErrors.Is(err, context.DeadlineExceeded) {
		return recoveryAbortCancelled
	}

	// Eino's own step-budget sentinel — retry is pointless.
	if stdErrors.Is(err, compose.ErrExceedMaxSteps) {
		return recoveryAbortNonRecoverable
	}

	// Engine-level step-budget marker (in case any of our code wraps
	// max-steps semantics in our own typed error).
	if errors.Is(err, errors.CodeAgentBudgetExhausted) {
		return recoveryAbortNonRecoverable
	}

	// LLM provider rate-limit — backoff + retry, no feedback (the LLM
	// has not done anything wrong; the platform is being throttled).
	if errors.Is(err, errors.CodeRateLimited) {
		return recoveryBackoff
	}

	// LLM provider auth failure — retry will not help.
	if errors.Is(err, errors.CodeLLMAuth) {
		return recoveryAbortNonRecoverable
	}

	// Everything else — transient transport hiccups, parse errors,
	// caller-side LLM bugs the model can be coached to fix — gets a
	// feedback retry within the budget.
	return recoveryFeedback
}

// rateLimitBackoff calculates exponential backoff duration for rate limit retries.
// Formula: 2^attempt * 2 seconds (2s, 4s, 8s, 16s, 32s)
func rateLimitBackoff(attempt int) time.Duration {
	return time.Duration(1<<uint(attempt)) * 2 * time.Second
}

// formatAgentErrorFeedback creates a user-friendly error message for the agent
func formatAgentErrorFeedback(err error) string {
	if err == nil {
		return ""
	}

	errStr := err.Error()
	errLower := strings.ToLower(errStr)

	// XML/parsing errors
	if strings.Contains(errLower, "xml") || strings.Contains(errStr, "element") && strings.Contains(errStr, "closed by") {
		return "[SYSTEM ERROR] Your previous response had invalid XML format. Please ensure proper XML tag formatting for tool calls."
	}

	// JSON parsing errors
	if strings.Contains(errLower, "json") || strings.Contains(errLower, "unmarshal") {
		return "[SYSTEM ERROR] Your previous response had invalid JSON format. Please ensure proper JSON formatting in your tool call arguments."
	}

	// Tool not found
	if strings.Contains(errLower, "tool not found") || strings.Contains(errLower, "unknown tool") {
		return "[SYSTEM ERROR] The tool you tried to call does not exist. Please use only the tools listed in your available tools."
	}

	// Generic error
	return "[SYSTEM ERROR] An error occurred processing your previous response: " + errStr + ". Please try a different approach."
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
