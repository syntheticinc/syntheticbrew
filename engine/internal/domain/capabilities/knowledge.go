package capabilities

import "context"

// KnowledgeCapability injects knowledge_search tool into the agent.
// Static: ignores ctx, agentID, and config; always returns the same tool.
type KnowledgeCapability struct{}

func (KnowledgeCapability) Type() string { return "knowledge" }

func (KnowledgeCapability) Validate(_ map[string]any) error { return nil }

func (KnowledgeCapability) Tools(_ context.Context, _ string, _ map[string]any) ([]string, error) {
	return []string{"knowledge_search"}, nil
}
