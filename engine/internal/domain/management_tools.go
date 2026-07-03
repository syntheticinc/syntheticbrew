package domain

import "strings"

// managementToolPrefix marks the management-plane tool family. Every tool whose
// name starts with this prefix administers engine configuration (agents,
// schemas, models, MCP servers, capabilities, sessions).
const managementToolPrefix = "admin_"

// extraManagementTools are management-plane tools that do not share the
// admin_ prefix but carry the same configuration authority: provision_agent
// creates agents+schemas+bindings, get_embed_snippet mints chat tokens.
var extraManagementTools = map[string]bool{
	"provision_agent":   true,
	"get_embed_snippet": true,
}

// IsManagementTool reports whether name is a management-plane tool. These tools
// mutate engine configuration or mint credentials and must never be granted to
// a user-provisioned (non-system) agent — only system agents such as the
// builder-assistant may resolve them. Used both to gate tool resolution
// (ResolveForAgent) and to reject dangerous agent configs at create/update.
func IsManagementTool(name string) bool {
	if strings.HasPrefix(name, managementToolPrefix) {
		return true
	}
	return extraManagementTools[name]
}
