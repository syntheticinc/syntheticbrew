package capability

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

// CapabilityReader reads enabled capabilities for an agent.
type CapabilityReader interface {
	ListEnabledByAgent(ctx context.Context, agentName string) ([]CapabilityRecord, error)
}

// CapabilityRecord mirrors the repository record for the service boundary.
type CapabilityRecord struct {
	ID        string
	AgentName string
	Type      string
	Config    map[string]interface{}
	Enabled   bool
}

// Injector reads agent capabilities and returns the tool names that should be auto-injected.
type Injector struct {
	reader CapabilityReader
}

// NewInjector creates a new capability injector.
func NewInjector(reader CapabilityReader) *Injector {
	return &Injector{reader: reader}
}

// InjectedTools returns the tool names that should be added to an agent based on its enabled capabilities.
func (inj *Injector) InjectedTools(ctx context.Context, agentName string) ([]string, error) {
	caps, err := inj.reader.ListEnabledByAgent(ctx, agentName)
	if err != nil {
		return nil, fmt.Errorf("list capabilities for agent %q: %w", agentName, err)
	}

	var tools []string
	seen := make(map[string]bool)

	for _, cap := range caps {
		capType := domain.CapabilityType(cap.Type)
		for _, toolName := range capType.InjectedTools() {
			if !seen[toolName] {
				seen[toolName] = true
				tools = append(tools, toolName)
			}
		}
	}

	if len(tools) > 0 {
		slog.InfoContext(ctx, "capability injector: injecting tools", "agent", agentName, "tools", tools)
	}

	return tools, nil
}
