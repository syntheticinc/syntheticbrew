package tools

import "github.com/syntheticinc/syntheticbrew/internal/domain"

// ToolEventEmitter sends agent events from tools (e.g. structured output).
type ToolEventEmitter interface {
	Send(event *domain.AgentEvent) error
}

// ToolDependencies holds all dependencies needed by tools at runtime.
type ToolDependencies struct {
	SessionID         string
	AgentName         string
	// IsSystem marks the resolving agent as a built-in system agent. Only
	// system agents may resolve management-plane tools (admin_*, provision_agent,
	// get_embed_snippet) via the legacy Resolve path.
	IsSystem          bool
	ProjectKey        string
	BackgroundMode    bool // true for cron/webhook/API tasks (no user interaction)
	Proxy             ClientOperationsProxy
	AgentPool         AgentPoolForTool
	EngineTaskManager EngineTaskManager // unified task manager (EngineTask-based)
	EventEmitter      ToolEventEmitter  // event stream for tools that emit events
	MCPServers        []string          // MCP server names for legacy Resolve path
	CanSpawn          []string          // target agent names this agent can spawn (legacy Resolve path)
	// Memory capability deps (US-001: injected when agent has Memory capability)
	SchemaID         string         // agent's schema ID for memory scoping
	UserID           string         // end-user ID for memory scoping
	MemoryRecaller   MemoryRecaller // nil → memory_recall disabled
	MemoryStorer     MemoryStorer   // nil → memory_store disabled
	MemoryMaxEntries int            // 0 → unlimited
	ConfirmBefore    []string              // tools requiring user confirmation before execution
	ConfirmRequester ConfirmationRequester // confirmation handler for confirm_before tools (nil = no wrapping)
}
