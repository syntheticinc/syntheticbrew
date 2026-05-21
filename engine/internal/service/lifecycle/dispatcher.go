package lifecycle

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

// Dispatcher manages task dispatch between parent and child agents.
type Dispatcher struct {
	mu      sync.RWMutex
	tasks   map[string]*domain.TaskPacket // taskID -> packet
	manager *Manager
}

// NewDispatcher creates a new task Dispatcher.
func NewDispatcher(manager *Manager) *Dispatcher {
	return &Dispatcher{
		tasks:   make(map[string]*domain.TaskPacket),
		manager: manager,
	}
}

// Dispatch creates a task, assigns it to a child agent, and executes it.
// Returns the result when the child completes (blocking).
func (d *Dispatcher) Dispatch(ctx context.Context, taskID, parentAgent, childAgent, sessionID, input string,
	childMode domain.LifecycleMode, maxContext int, timeout time.Duration,
	eventStream domain.AgentEventStream) (*domain.TaskPacket, error) {

	packet, err := domain.NewTaskPacket(taskID, parentAgent, childAgent, sessionID, input, timeout)
	if err != nil {
		return nil, fmt.Errorf("create task packet: %w", err)
	}

	d.mu.Lock()
	d.tasks[taskID] = packet
	d.mu.Unlock()

	// Emit task.dispatched event
	if eventStream != nil {
		eventStream.Send(domain.NewTaskDispatchedEvent(taskID, parentAgent, childAgent))
	}

	slog.InfoContext(ctx, "lifecycle: dispatching task", "task_id", taskID, "parent", parentAgent, "child", childAgent)

	// Start the task
	if err := packet.Start(); err != nil {
		return packet, fmt.Errorf("start task: %w", err)
	}

	// Apply timeout if configured
	execCtx := ctx
	if timeout > 0 {
		var cancel context.CancelFunc
		execCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	// Execute the child agent via the lifecycle manager
	output, err := d.manager.ExecuteTask(execCtx, childAgent, taskID, input, childMode, maxContext, eventStream)
	if err != nil {
		// Check if it's a timeout
		if execCtx.Err() == context.DeadlineExceeded {
			if markErr := packet.MarkTimeout(); markErr != nil {
				slog.ErrorContext(ctx, "lifecycle: failed to mark timeout", "error", markErr)
			}
			if eventStream != nil {
				eventStream.Send(&domain.AgentEvent{
					Type:          domain.EventTypeTaskTimeout,
					SchemaVersion: domain.EventSchemaVersion,
					Timestamp:     time.Now(),
					AgentID:       childAgent,
					Metadata: map[string]interface{}{
						"task_id":     taskID,
						"child_agent": childAgent,
					},
				})
			}
			return packet, fmt.Errorf("task %q timed out", taskID)
		}

		if failErr := packet.Fail(err.Error()); failErr != nil {
			slog.ErrorContext(ctx, "lifecycle: failed to mark failure", "error", failErr)
		}
		if eventStream != nil {
			eventStream.Send(&domain.AgentEvent{
				Type:          domain.EventTypeTaskFailed,
				SchemaVersion: domain.EventSchemaVersion,
				Timestamp:     time.Now(),
				AgentID:       childAgent,
				Content:       err.Error(),
				Metadata: map[string]interface{}{
					"task_id":     taskID,
					"child_agent": childAgent,
				},
			})
		}
		return packet, fmt.Errorf("task %q failed: %w", taskID, err)
	}

	// Complete the task
	if err := packet.Complete(output); err != nil {
		return packet, fmt.Errorf("complete task: %w", err)
	}

	// Emit task.completed event
	if eventStream != nil {
		eventStream.Send(domain.NewTaskCompletedEvent(taskID, childAgent))
	}

	slog.InfoContext(ctx, "lifecycle: task completed", "task_id", taskID, "child", childAgent)

	return packet, nil
}

// GetTask returns a task by ID.
func (d *Dispatcher) GetTask(taskID string) (*domain.TaskPacket, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	tp, ok := d.tasks[taskID]
	return tp, ok
}

// ListTasks returns all tasks for a parent agent.
func (d *Dispatcher) ListTasks(parentAgent string) []*domain.TaskPacket {
	d.mu.RLock()
	defer d.mu.RUnlock()

	var result []*domain.TaskPacket
	for _, tp := range d.tasks {
		if tp.ParentAgent == parentAgent {
			result = append(result, tp)
		}
	}
	return result
}

// ListTasksBySession returns all tasks for a given session.
func (d *Dispatcher) ListTasksBySession(sessionID string) []*domain.TaskPacket {
	d.mu.RLock()
	defer d.mu.RUnlock()

	var result []*domain.TaskPacket
	for _, tp := range d.tasks {
		if tp.SessionID == sessionID {
			result = append(result, tp)
		}
	}
	return result
}
