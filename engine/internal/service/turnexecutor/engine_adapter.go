package turnexecutor

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/syntheticinc/bytebrew/engine/internal/domain"
	"github.com/syntheticinc/bytebrew/engine/internal/infrastructure/agents"
	"github.com/syntheticinc/bytebrew/engine/internal/infrastructure/agents/react"
	"github.com/syntheticinc/bytebrew/engine/internal/infrastructure/llm"
	"github.com/syntheticinc/bytebrew/engine/internal/infrastructure/tools"
	"github.com/syntheticinc/bytebrew/engine/internal/service/engine"
	"github.com/syntheticinc/bytebrew/engine/pkg/config"
	"github.com/cloudwego/eino/components/model"
	einotool "github.com/cloudwego/eino/components/tool"
)

// Consumer-side interfaces (defined here where they're used)

// AgentEngine executes agents with persistence
type AgentEngine interface {
	Execute(ctx context.Context, cfg engine.ExecutionConfig) (*engine.ExecutionResult, error)
	HistoryRepo() engine.HistoryRepository
}

// FlowProvider provides flow configurations
type FlowProvider interface {
	GetFlow(ctx context.Context, agentName string) (*domain.Flow, error)
}

// ToolResolver resolves tool names to instances
type ToolResolver interface {
	Resolve(ctx context.Context, toolNames []string, deps tools.ToolDependencies) ([]einotool.InvokableTool, error)
}

// ToolDependenciesProvider creates tool deps for a given session
type ToolDependenciesProvider interface {
	GetDependencies(sessionID, projectKey string) tools.ToolDependencies
}

// ToolCallRecorder records tool calls (pass-through to engine)
type ToolCallRecorder interface {
	RecordToolCall(sessionID, toolName string)
	RecordToolResult(sessionID, toolName, result string)
}

// ContextReminderProvider provides context reminders (pass-through to engine)
type ContextReminderProvider interface {
	GetContextReminder(ctx context.Context, sessionID string) (string, int, bool)
}

// EngineAdapter adapts Engine to TurnExecutor interface (orchestrator.TurnExecutor)
// It bridges the Orchestrator event loop with the new Engine.
//
// V2 note: post-Group A.1 the schema-level multi-agent pipeline executor was
// removed. Multi-agent delegation in V2 is expressed by the agent itself via
// tool calls (see docs/architecture/agent-first-runtime.md §3.1), not by a
// separate flow Executor walking edge types.
type EngineAdapter struct {
	engine           AgentEngine
	flowProvider     FlowProvider
	toolResolver     ToolResolver
	toolDeps         ToolDependenciesProvider
	chatModel        model.ToolCallingChatModel
	agentConfig      *config.AgentConfig
	modelName        string
	providerType     string // e.g. "openai", "openai_compatible"
	providerBaseURL  string
	agentName        string
	agentUUID        string // uuid FK → agents.id (for engine execution context)
	// pass-through deps
	contextReminders []ContextReminderProvider
	toolCallRecorder ToolCallRecorder
	// Schema scope for memory tools (empty = no explicit schema context)
	schemaID string
	// HistoryRepo for HITL interrupt event mirroring into messages table.
	historyRepo HistoryRepository
}

// Config holds configuration for EngineAdapter
type Config struct {
	Engine           AgentEngine
	FlowProvider     FlowProvider
	ToolResolver     ToolResolver
	ToolDeps         ToolDependenciesProvider
	ChatModel        model.ToolCallingChatModel
	AgentConfig      *config.AgentConfig
	ModelName        string
	ProviderType     string // e.g. "openai", "openai_compatible"
	ProviderBaseURL  string
	AgentName        string
	AgentUUID        string // uuid FK → agents.id (for engine execution context)
	ContextReminders []ContextReminderProvider
	ToolCallRecorder ToolCallRecorder
	// Schema scope (empty = no explicit schema context)
	SchemaID string
	HistoryRepo HistoryRepository // mirrors HITL events into messages table; nil disables
}

// NewEngineAdapter creates a new EngineAdapter
func NewEngineAdapter(cfg Config) (*EngineAdapter, error) {
	if cfg.Engine == nil {
		return nil, fmt.Errorf("engine is required")
	}
	if cfg.FlowProvider == nil {
		return nil, fmt.Errorf("flow provider is required")
	}
	if cfg.ToolResolver == nil {
		return nil, fmt.Errorf("tool resolver is required")
	}
	if cfg.ToolDeps == nil {
		return nil, fmt.Errorf("tool dependencies provider is required")
	}
	if cfg.ChatModel == nil {
		return nil, fmt.Errorf("chat model is required")
	}

	return &EngineAdapter{
		engine:           cfg.Engine,
		flowProvider:     cfg.FlowProvider,
		toolResolver:     cfg.ToolResolver,
		toolDeps:         cfg.ToolDeps,
		chatModel:        cfg.ChatModel,
		agentConfig:      cfg.AgentConfig,
		modelName:        cfg.ModelName,
		providerType:     cfg.ProviderType,
		providerBaseURL:  cfg.ProviderBaseURL,
		agentName:        cfg.AgentName,
		agentUUID:        cfg.AgentUUID,
		contextReminders: cfg.ContextReminders,
		toolCallRecorder: cfg.ToolCallRecorder,
		schemaID:         cfg.SchemaID,
		historyRepo:      cfg.HistoryRepo,
	}, nil
}

// ExecuteTurn implements orchestrator.TurnExecutor interface
func (e *EngineAdapter) ExecuteTurn(
	ctx context.Context,
	sessionID, projectKey, question string,
	chunkCallback func(chunk string) error,
	eventCallback func(event *domain.AgentEvent) error,
) error {
	// 1. Get flow config for the agent
	flow, err := e.flowProvider.GetFlow(ctx, e.agentName)
	if err != nil {
		return fmt.Errorf("get flow %q: %w", e.agentName, err)
	}

	// 2. Get tool dependencies
	toolDeps := e.toolDeps.GetDependencies(sessionID, projectKey)
	toolDeps.AgentName = flow.Name
	toolDeps.MCPServers = flow.MCPServers
	// Set schema scope for memory tools (0 = no explicit schema context)
	toolDeps.SchemaID = e.schemaID
	// Wraps per-turn eventCallback so tools can publish session events directly
	// (Eino's MessageCollector chain is downstream and never sees them).
	toolDeps.EventEmitter = &eventCallbackEmitter{
		cb:          eventCallback,
		historyRepo: e.historyRepo,
		sessionID:   sessionID,
		agentID:     e.agentUUID,
	}

	toolDeps.ConfirmBefore = flow.ConfirmBefore

	// Pull ConfirmRequester from proxy if available (set by processor for SSE path)
	if cr, ok := toolDeps.Proxy.(interface{ ConfirmRequester() tools.ConfirmationRequester }); ok {
		toolDeps.ConfirmRequester = cr.ConfirmRequester()
	}

	// Populate spawn targets from flow's SpawnPolicy
	toolDeps.CanSpawn = flow.Spawn.AllowedFlows

	// 3. Resolve tools from flow.ToolNames
	slog.InfoContext(ctx, "[EngineAdapter] resolving tools", "agent", e.agentName, "flow_tool_names_count", len(flow.ToolNames), "flow_tool_names", flow.ToolNames)
	resolvedTools, err := e.toolResolver.Resolve(ctx, flow.ToolNames, toolDeps)
	if err != nil {
		return fmt.Errorf("resolve tools: %w", err)
	}

	// 4. Convert InvokableTool to BaseTool (slice casting)
	baseTools := convertToBaseTools(resolvedTools)

	// 5. Convert context reminders to engine-compatible interface
	engineReminders := convertContextRemindersToEngine(e.contextReminders)

	// 6. Build ExecutionConfig
	var compressor engine.MessageCompressor
	if flow.MaxContextSize > 0 {
		compressor = engine.MessageCompressor(agents.NewContextRewriter(flow.MaxContextSize))
	}

	// Wrap ChatModel with per-agent model parameters (temperature, top_p, etc.)
	chatModel := llm.WrapWithModelParams(e.chatModel, llm.ModelParams{
		Temperature: flow.Temperature,
		TopP:        flow.TopP,
		MaxTokens:   flow.MaxTokens,
		Stop:        flow.StopSequences,
	})

	execCfg := engine.ExecutionConfig{
		SessionID:         sessionID,
		AgentID:           e.agentUUID,
		Flow:              flow,
		Tools:             baseTools,
		Input:             question,
		ChatModel:         chatModel,
		Streaming:         true,
		ChunkCallback:     chunkCallback,
		EventCallback:     eventCallback,
		ContextReminders:  engineReminders,
		ToolCallRecorder:  convertToolCallRecorderToEngine(e.toolCallRecorder),
		ModelName:         e.modelName,
		ProviderType:      e.providerType,
		ProviderBaseURL:   e.providerBaseURL,
		AgentConfig:       e.agentConfig,
		MessageCompressor: compressor,
	}

	// 7. Execute via Engine
	result, err := e.engine.Execute(ctx, execCfg)
	if err != nil {
		return fmt.Errorf("execute engine: %w", err)
	}

	// Log result status
	slog.InfoContext(ctx, "[EngineAdapter] engine execution completed",
		"status", result.Status,
		"suspended_at", result.SuspendedAt)

	// 8. Send final completion signal so the client knows the turn is done.
	// agent.Stream() only emits IsComplete=false; we must emit IsComplete=true
	// after the engine finishes so the gRPC layer sends IsFinal=true to the client.
	if eventCallback != nil {
		eventCallback(&domain.AgentEvent{
			Type:       domain.EventTypeAnswer,
			Timestamp:  time.Now(),
			Content:    result.Answer,
			IsComplete: true,
			AgentID:    e.agentName,
		})
	}

	// V2 (Group A.1): no schema-level pipeline dispatch happens here. Multi-agent
	// delegation is expressed by the agent itself through tool calls (see
	// docs/architecture/agent-first-runtime.md §3.1).

	return nil
}

// convertToBaseTools converts []InvokableTool to []BaseTool
func convertToBaseTools(invokableTools []einotool.InvokableTool) []einotool.BaseTool {
	baseTools := make([]einotool.BaseTool, len(invokableTools))
	for i, t := range invokableTools {
		baseTools[i] = t // InvokableTool embeds BaseTool, so implicit conversion
	}
	return baseTools
}

// Adapters for converting consumer-side interfaces to engine-compatible types

// contextReminderEngineAdapter adapts turnexecutor.ContextReminderProvider to react.ContextReminderProvider
type contextReminderEngineAdapter struct {
	provider ContextReminderProvider
}

func (a *contextReminderEngineAdapter) GetContextReminder(ctx context.Context, sessionID string) (string, int, bool) {
	return a.provider.GetContextReminder(ctx, sessionID)
}

func convertContextRemindersToEngine(providers []ContextReminderProvider) []react.ContextReminderProvider {
	if providers == nil {
		return nil
	}
	result := make([]react.ContextReminderProvider, len(providers))
	for i, p := range providers {
		result[i] = &contextReminderEngineAdapter{provider: p}
	}
	return result
}

// toolCallRecorderEngineAdapter adapts turnexecutor.ToolCallRecorder to react.ToolCallRecorder
type toolCallRecorderEngineAdapter struct {
	recorder ToolCallRecorder
}

func (a *toolCallRecorderEngineAdapter) RecordToolCall(sessionID, toolName string) {
	a.recorder.RecordToolCall(sessionID, toolName)
}

func (a *toolCallRecorderEngineAdapter) RecordToolResult(sessionID, toolName, result string) {
	a.recorder.RecordToolResult(sessionID, toolName, result)
}

func convertToolCallRecorderToEngine(recorder ToolCallRecorder) react.ToolCallRecorder {
	if recorder == nil {
		return nil
	}
	return &toolCallRecorderEngineAdapter{recorder: recorder}
}

// eventCallbackEmitter wraps the per-turn eventCallback so tool-emitted
// events (currently only show_structured_output → interrupt_request) reach
// both the SSE event stream (via cb → session_event_log) and the messages
// table (via historyRepo → reload replay).
type eventCallbackEmitter struct {
	cb          func(event *domain.AgentEvent) error
	historyRepo HistoryRepository
	sessionID   string
	agentID     string
}

type HistoryRepository = engine.HistoryRepository

func (e *eventCallbackEmitter) Send(event *domain.AgentEvent) error {
	// Best-effort history mirror — failures log but never fail Send.
	if e.historyRepo != nil {
		switch event.Type {
		case domain.EventTypeInterruptRequest:
			interruptID, _ := event.Metadata["interrupt_id"].(string)
			msg, err := domain.NewInterruptRequestMessage(e.sessionID, interruptID, event.Content)
			if err == nil {
				msg.AgentID = e.agentID
				_ = e.historyRepo.Create(context.Background(), msg)
			}
		case domain.EventTypeInterruptResume:
			interruptID, _ := event.Metadata["interrupt_id"].(string)
			msg, err := domain.NewInterruptResumeMessage(e.sessionID, interruptID, event.Content)
			if err == nil {
				msg.AgentID = e.agentID
				_ = e.historyRepo.Create(context.Background(), msg)
			}
		}
	}
	if e.cb == nil {
		return nil
	}
	return e.cb(event)
}
