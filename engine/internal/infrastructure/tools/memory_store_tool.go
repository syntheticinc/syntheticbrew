package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

// MemoryStorer persists memory entries.
type MemoryStorer interface {
	Store(ctx context.Context, mem *domain.Memory, maxEntries int) error
}

// memoryStoreArgs represents arguments for the memory_store tool.
type memoryStoreArgs struct {
	Content  string            `json:"content"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

// MemoryStoreTool allows the agent to store important information for future sessions.
type MemoryStoreTool struct {
	schemaID   string
	userSub    string
	storer     MemoryStorer
	maxEntries int
}

// NewMemoryStoreTool creates a new memory_store tool scoped to the given
// (schema, user_sub) pair. Memories isolate by this tuple — a non-empty
// user_sub is required because memories require an authenticated end-user
// in V2 (the anonymous sentinel was removed).
func NewMemoryStoreTool(schemaID, userSub string, storer MemoryStorer, maxEntries int) tool.InvokableTool {
	return &MemoryStoreTool{
		schemaID:   schemaID,
		userSub:    userSub,
		storer:     storer,
		maxEntries: maxEntries,
	}
}

// Info returns tool information for LLM.
func (t *MemoryStoreTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "memory_store",
		Desc: `Stores a durable fact about the user for future sessions.
CALL THIS EAGERLY, without being asked: whenever the user reveals any lasting detail about
themselves or their context — name, role, company, team size, plan, goals, preferences,
constraints, or a decision — store it immediately, even if they mention it only in passing
while asking about something else. Prefer storing a fact over forgetting it.
Memory is per-user and cross-session — stored facts are available in every future session with this user.`,
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"content": {
				Type:     schema.String,
				Desc:     "The information to remember. Be concise and factual.",
				Required: true,
			},
			"metadata": {
				Type:     schema.Object,
				Desc:     "Optional key-value metadata (e.g. source, category)",
				Required: false,
			},
		}),
	}, nil
}

// InvokableRun executes the memory store.
func (t *MemoryStoreTool) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
	var args memoryStoreArgs
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		slog.ErrorContext(ctx, "[MemoryStoreTool] failed to parse arguments", "error", err)
		return fmt.Sprintf("[ERROR] Invalid arguments: %v", err), nil
	}

	if args.Content == "" {
		return "[ERROR] content is required.", nil
	}

	mem, err := domain.NewMemory(t.schemaID, t.userSub, args.Content)
	if err != nil {
		return fmt.Sprintf("[ERROR] Invalid memory: %v", err), nil
	}

	for k, v := range args.Metadata {
		mem.AddMetadata(k, v)
	}

	if err := t.storer.Store(ctx, mem, t.maxEntries); err != nil {
		slog.ErrorContext(ctx, "[MemoryStoreTool] store failed", "error", err)
		return fmt.Sprintf("[ERROR] Failed to store memory: %v", err), nil
	}

	slog.InfoContext(ctx, "[MemoryStoreTool] stored",
		"schema_id", t.schemaID, "user_sub", t.userSub, "content_len", len(args.Content))

	return "Memory stored successfully.", nil
}
