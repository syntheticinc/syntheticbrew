package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

// MemoryRecaller retrieves memory entries.
type MemoryRecaller interface {
	ListBySchemaAndUser(ctx context.Context, schemaID, userSub string) ([]*domain.Memory, error)
}

// memoryRecallArgs represents arguments for the memory_recall tool.
type memoryRecallArgs struct {
	Query string `json:"query,omitempty"`
	Limit int    `json:"limit,omitempty"`
}

// MemoryRecallTool retrieves relevant memories from previous sessions
// scoped to (schema, user_sub).
type MemoryRecallTool struct {
	schemaID string
	userSub  string
	recaller MemoryRecaller
}

// NewMemoryRecallTool creates a new memory_recall tool scoped to the given
// (schema, user_sub) pair.
func NewMemoryRecallTool(schemaID, userSub string, recaller MemoryRecaller) tool.InvokableTool {
	return &MemoryRecallTool{
		schemaID: schemaID,
		userSub:  userSub,
		recaller: recaller,
	}
}

// Info returns tool information for LLM.
func (t *MemoryRecallTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "memory_recall",
		Desc: `Retrieves stored memories from previous sessions with this user.
Use this tool at the start of a session or when you need to recall past interactions.
Memory is per-schema and cross-session.`,
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"query": {
				Type:     schema.String,
				Desc:     "Optional keyword to filter memories. If empty, returns most recent memories.",
				Required: false,
			},
			"limit": {
				Type:     schema.Integer,
				Desc:     "Maximum number of memories to return (default: 10, max: 50)",
				Required: false,
			},
		}),
	}, nil
}

// InvokableRun executes the memory recall.
func (t *MemoryRecallTool) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
	var args memoryRecallArgs
	if argumentsInJSON != "" {
		if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
			slog.ErrorContext(ctx, "[MemoryRecallTool] failed to parse arguments", "error", err)
			// Non-fatal: use defaults
		}
	}

	if args.Limit <= 0 {
		args.Limit = 10
	}
	if args.Limit > 50 {
		args.Limit = 50
	}

	slog.InfoContext(ctx, "[MemoryRecallTool] recalling",
		"schema_id", t.schemaID, "user_sub", t.userSub, "query", args.Query, "limit", args.Limit)

	memories, err := t.recaller.ListBySchemaAndUser(ctx, t.schemaID, t.userSub)
	if err != nil {
		slog.ErrorContext(ctx, "[MemoryRecallTool] recall failed", "error", err)
		return fmt.Sprintf("[ERROR] Failed to recall memories: %v", err), nil
	}

	if len(memories) == 0 {
		return "No memories found for this user in this schema.", nil
	}

	// Keyword filtering: all query words must appear in content (AND logic)
	if args.Query != "" {
		queryWords := strings.Fields(strings.ToLower(args.Query))
		filtered := make([]*domain.Memory, 0)
		for _, m := range memories {
			contentLower := strings.ToLower(m.Content)
			match := true
			for _, w := range queryWords {
				if !strings.Contains(contentLower, w) {
					match = false
					break
				}
			}
			if match {
				filtered = append(filtered, m)
			}
		}
		memories = filtered
	}

	// Apply limit
	if len(memories) > args.Limit {
		memories = memories[:args.Limit]
	}

	if len(memories) == 0 {
		return fmt.Sprintf("No memories matching \"%s\" found.", args.Query), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## Recalled %d memories\n\n", len(memories)))
	for i, m := range memories {
		sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, m.Content))
		if len(m.Metadata) > 0 {
			sb.WriteString("   Metadata: ")
			parts := make([]string, 0, len(m.Metadata))
			for k, v := range m.Metadata {
				parts = append(parts, k+"="+v)
			}
			sb.WriteString(strings.Join(parts, ", "))
			sb.WriteString("\n")
		}
	}

	slog.InfoContext(ctx, "[MemoryRecallTool] returning memories", "count", len(memories))
	return sb.String(), nil
}
