package tools

// ContentRiskLevel represents the risk level of content returned by a tool.
type ContentRiskLevel int

const (
	// RiskNone — internal tools (manage_tasks, spawn_agent) — no wrapping.
	RiskNone ContentRiskLevel = iota
	// RiskLow — structural/metadata tools — light prefix.
	RiskLow
	// RiskHigh — content tools that may contain injections (MCP responses, memory, knowledge).
	RiskHigh
	// RiskCritical — external command execution — strong markers.
	RiskCritical
)

// GetContentRiskLevel returns the risk level for a given tool name.
// After self-hosted tools were parked, the server-side runtime exposes only
// internal coordination tools (RiskNone) and capability/MCP tools that
// return external content. Unknown tools default to RiskHigh so any new
// MCP tool is wrapped by default.
func GetContentRiskLevel(toolName string) ContentRiskLevel {
	switch toolName {
	// Internal tools that don't return untrusted content.
	case "manage_tasks", "spawn_agent", "show_structured_output":
		return RiskNone
	default:
		return RiskHigh
	}
}
