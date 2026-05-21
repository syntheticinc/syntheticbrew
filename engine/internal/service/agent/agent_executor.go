package agent

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/cloudwego/eino/components/model"
	einotool "github.com/cloudwego/eino/components/tool"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/agents"
	"github.com/syntheticinc/syntheticbrew/internal/service/engine"
)

// runAgentWithEngine is the generic execution method for any agent type.
// Used by both coder (via runCodeAgentWithEngine) and researcher/reviewer agents.
func (p *AgentPool) runAgentWithEngine(
	ctx context.Context,
	sessionID, projectKey, agentID string,
	agentType string,
	subtaskID string,
	input string,
) (string, error) {
	p.mu.RLock()
	eng := p.engine
	flowProvider := p.flowProvider
	toolResolver := p.toolResolver
	toolDeps := p.toolDeps
	sessionDir := p.sessionDirName
	reminders := p.contextReminders
	p.mu.RUnlock()

	if eng == nil || flowProvider == nil || toolResolver == nil || toolDeps == nil {
		return "", fmt.Errorf("engine dependencies not configured")
	}

	flow, err := flowProvider.GetFlow(ctx, agentType)
	if err != nil {
		return "", fmt.Errorf("get %s flow: %w", agentType, err)
	}

	deps := toolDeps.GetDependencies(sessionID, projectKey)
	deps.AgentName = flow.Name
	deps.MCPServers = flow.MCPServers
	deps.CanSpawn = flow.Spawn.AllowedFlows

	p.mu.RLock()
	sessionProxy, ok := p.sessionProxies[sessionID]
	p.mu.RUnlock()
	if !ok {
		return "", fmt.Errorf("no proxy for session: %s", sessionID)
	}
	deps.Proxy = sessionProxy

	resolvedTools, err := toolResolver.Resolve(ctx, flow.ToolNames, deps)
	if err != nil {
		return "", fmt.Errorf("resolve tools: %w", err)
	}

	baseTools := make([]einotool.BaseTool, len(resolvedTools))
	for i, t := range resolvedTools {
		baseTools[i] = t
	}

	eventCb := func(event *domain.AgentEvent) error {
		event.AgentID = agentID
		p.mu.RLock()
		cb := p.sessionEventCallbacks[sessionID]
		p.mu.RUnlock()
		if cb == nil {
			return nil
		}
		return cb(event)
	}

	var compressor engine.MessageCompressor
	if flow.MaxContextSize > 0 {
		compressor = engine.MessageCompressor(agents.NewContextRewriter(flow.MaxContextSize))
	}

	// Resolve model from DB (per-agent config) or fallback to static ModelSelector
	chatModel, modelName := p.resolveModel(ctx, flow.Name)

	execCfg := engine.ExecutionConfig{
		SessionID:         sessionID,
		AgentID:           agentID,
		Flow:              flow,
		Tools:             baseTools,
		Input:             input,
		ChatModel:         chatModel,
		Streaming:         false,
		EventCallback:     eventCb,
		ContextReminders:  reminders,
		ModelName:         modelName,
		AgentConfig:       p.agentConfig,
		ParentAgentID:     "supervisor",
		SubtaskID:         subtaskID,
		SessionDirName:    sessionDir,
		MessageCompressor: compressor,
	}

	result, err := eng.Execute(ctx, execCfg)
	if err != nil {
		return "", fmt.Errorf("execute engine: %w", err)
	}

	return result.Answer, nil
}

// runCodeAgentWithEngine executes a coder agent for a specific subtask (EngineTask with ParentTaskID).
// Delegates to the generic runAgentWithEngine with coder-specific input.
func (p *AgentPool) runCodeAgentWithEngine(
	ctx context.Context,
	sessionID, projectKey, agentID string,
	subtask *domain.EngineTask,
) (string, error) {
	input := buildCodeAgentInput(subtask)
	return p.runAgentWithEngine(ctx, sessionID, projectKey, agentID, "coder", subtask.ID.String(), input)
}

// resolveModel returns the LLM client and model name for the given agent.
// Tries per-agent DB model (via modelIDResolver + modelCache) first,
// then falls back to the static ModelSelector.
func (p *AgentPool) resolveModel(ctx context.Context, agentName string) (model.ToolCallingChatModel, string) {
	p.mu.RLock()
	resolver := p.modelIDResolver
	cache := p.modelCache
	p.mu.RUnlock()

	if resolver != nil && cache != nil {
		if modelID := resolver.ResolveModelID(ctx, agentName); modelID != nil {
			client, name, err := cache.Get(ctx, *modelID)
			if err != nil {
				slog.ErrorContext(ctx, "failed to resolve model from cache, falling back to selector",
					"agent", agentName, "model_id", *modelID, "error", err)
			} else {
				return client, name
			}
		}
	}

	return p.modelSelector.Select(agentName), p.modelSelector.ModelName(agentName)
}

func buildCodeAgentInput(subtask *domain.EngineTask) string {
	input := fmt.Sprintf("Subtask: %s\n\nDescription: %s", subtask.Title, subtask.Description)
	if len(subtask.AcceptanceCriteria) > 0 {
		input += "\n\nAcceptance criteria:"
		for _, c := range subtask.AcceptanceCriteria {
			input += fmt.Sprintf("\n- %s", c)
		}
	}
	return input
}
