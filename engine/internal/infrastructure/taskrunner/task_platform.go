package taskrunner

import (
	"context"
	"log/slog"

	"github.com/syntheticinc/syntheticbrew/internal/service/task"
)

// StartBackgroundWorker creates a TaskWorker with the given executor + concurrency.
// Returns the started worker so the caller can Stop() it on shutdown.
// Passing a nil executor yields nil — callers must guard against this before Submit.
//
// Cron/webhook trigger fan-out has been removed from V2. Tenants call the
// chat API directly from their own schedulers; native cron/webhook triggers
// are deferred to V3 along with the trigger_subscriptions table.
func StartBackgroundWorker(executor task.TaskExecutor, concurrency int) *task.TaskWorker {
	if executor == nil {
		slog.InfoContext(context.Background(), "background task worker not started (no executor provided)")
		return nil
	}
	if concurrency <= 0 {
		concurrency = 4
	}
	worker := task.NewTaskWorker(executor, concurrency)
	worker.Start()
	return worker
}
