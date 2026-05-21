package agent

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/service/orchestrator"
)

func (p *AgentPool) markCompleted(agentID, subtaskID, result string) {
	// 1. Update agent state under lock, but DON'T signal completion yet.
	//    signalCompletion() unblocks WaitForAllSessionAgents → supervisor resumes →
	//    session may close before lifecycle event reaches the client.
	p.mu.Lock()
	subtaskTitle := ""
	sessionID := ""
	var agent *RunningAgent
	if a, ok := p.agents[agentID]; ok {
		agent = a
		agent.Status = "completed"
		agent.Result = result
		subtaskTitle = agent.SubtaskTitle
		sessionID = agent.SessionID
	}
	bus := p.eventBus
	agentRunStorage := p.agentRunStorage
	p.mu.Unlock()

	ctx := context.Background()
	if subtaskID != "" {
		if subtaskUUID, err := uuid.Parse(subtaskID); err != nil {
			slog.ErrorContext(context.Background(), "[AgentPool] invalid subtask id on complete", "task_id", subtaskID, "error", err)
		} else if err := p.subtaskManager.CompleteTask(ctx, subtaskUUID, result); err != nil {
			slog.ErrorContext(context.Background(), "[AgentPool] failed to complete task", "task_id", subtaskID, "error", err)
		}
	}

	// Update agent run in DB (if storage available)
	if agentRunStorage != nil {
		go func() {
			updateCtx := context.Background()
			run, err := agentRunStorage.GetByID(updateCtx, agentID)
			if err != nil {
				slog.ErrorContext(context.Background(), "[AgentPool] failed to get agent run for update", "agent_id", agentID, "error", err)
				return
			}
			if run != nil {
				run.Complete(result)
				if err := agentRunStorage.Update(updateCtx, run); err != nil {
					slog.ErrorContext(context.Background(), "[AgentPool] failed to update agent run", "agent_id", agentID, "error", err)
				}
			}
		}()
	}

	// Content for lifecycle event must be single-line (client parses via regex).
	// Use subtask title; full result is available via subtask storage.
	contentText := "Completed: " + subtaskTitle
	if subtaskTitle == "" {
		contentText = result
		if len(contentText) > 200 {
			contentText = contentText[:200] + "..."
		}
	}

	// 2. Send lifecycle event to client BEFORE signaling completion.
	//    This prevents a race where the supervisor resumes, closes the session,
	//    and the callback is removed before the event is sent.
	p.emitEventForSession(sessionID, &domain.AgentEvent{
		Type:      domain.EventTypeAgentCompleted,
		Timestamp: time.Now(),
		AgentID:   agentID,
		Content:   contentText,
		Metadata: map[string]interface{}{
			"subtask_id":    subtaskID,
			"subtask_title": subtaskTitle,
		},
	})

	// 3. NOW signal completion — unblocks WaitForAllSessionAgents.
	//    signalCompletion uses sync.Once, safe to call outside mutex.
	if agent != nil {
		agent.signalCompletion()
	}

	// 4. Publish to EventBus so Orchestrator updates active work status.
	// For blocking agents, tool result is delivered via WaitForAllSessionAgents,
	// but Orchestrator still needs to know the agent is done (HasActiveWork check).
	if bus != nil {
		_ = bus.Publish(orchestrator.OrchestratorEvent{
			Type:      orchestrator.EventAgentCompleted,
			AgentID:   agentID,
			SubtaskID: subtaskID,
			Content:   result,
		})
	}
}

func (p *AgentPool) markFailed(agentID, subtaskID, reason string) {
	// 1. Update agent state under lock, but DON'T signal completion yet.
	p.mu.Lock()
	agent, ok := p.agents[agentID]
	if !ok {
		p.mu.Unlock()
		return
	}
	agent.Status = "failed"
	agent.Error = reason
	subtaskTitle := agent.SubtaskTitle
	sessionID := agent.SessionID
	bus := p.eventBus
	agentRunStorage := p.agentRunStorage
	p.mu.Unlock()

	ctx := context.Background()
	if subtaskID != "" {
		if subtaskUUID, err := uuid.Parse(subtaskID); err != nil {
			slog.ErrorContext(context.Background(), "[AgentPool] invalid subtask id on fail", "task_id", subtaskID, "error", err)
		} else if err := p.subtaskManager.FailTask(ctx, subtaskUUID, reason); err != nil {
			slog.ErrorContext(context.Background(), "[AgentPool] failed to mark task as failed", "task_id", subtaskID, "error", err)
		}
	}

	// Update agent run in DB (if storage available)
	if agentRunStorage != nil {
		go func() {
			updateCtx := context.Background()
			run, err := agentRunStorage.GetByID(updateCtx, agentID)
			if err != nil {
				slog.ErrorContext(context.Background(), "[AgentPool] failed to get agent run for update", "agent_id", agentID, "error", err)
				return
			}
			if run != nil {
				run.Fail(reason)
				if err := agentRunStorage.Update(updateCtx, run); err != nil {
					slog.ErrorContext(context.Background(), "[AgentPool] failed to update agent run", "agent_id", agentID, "error", err)
				}
			}
		}()
	}

	contentText := reason
	if subtaskTitle != "" {
		contentText = fmt.Sprintf("Failed: %s\nReason: %s", subtaskTitle, reason)
	}

	// 2. Send lifecycle event to client BEFORE signaling completion.
	p.emitEventForSession(sessionID, &domain.AgentEvent{
		Type:      domain.EventTypeAgentFailed,
		Timestamp: time.Now(),
		AgentID:   agentID,
		Content:   contentText,
		Metadata: map[string]interface{}{
			"subtask_id":    subtaskID,
			"subtask_title": subtaskTitle,
		},
	})

	// 3. Signal completion AFTER event is sent.
	agent.signalCompletion()

	if bus != nil {
		_ = bus.Publish(orchestrator.OrchestratorEvent{
			Type:      orchestrator.EventAgentFailed,
			AgentID:   agentID,
			SubtaskID: subtaskID,
			Content:   reason,
		})
	}
}

func (p *AgentPool) emitEventForSession(sessionID string, event *domain.AgentEvent) {
	p.mu.RLock()
	cb := p.sessionEventCallbacks[sessionID]
	p.mu.RUnlock()

	if cb == nil {
		return
	}
	if err := cb(event); err != nil {
		slog.ErrorContext(context.Background(), "[AgentPool] failed to emit event", "type", event.Type, "error", err)
	}
}
