package capabilities

import "context"

// MemoryCapability injects memory_recall + memory_store tools into the agent.
// Static: ignores ctx, agentID, and config; always returns the same two tools.
type MemoryCapability struct{}

func (MemoryCapability) Type() string { return "memory" }

func (MemoryCapability) Validate(_ map[string]any) error { return nil }

func (MemoryCapability) Tools(_ context.Context, _ string, _ map[string]any) ([]string, error) {
	return []string{"memory_recall", "memory_store"}, nil
}
