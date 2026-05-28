package capabilities

import (
	"context"
	"fmt"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

// KGToolResolver is the consumer-side dependency for KnowledgeGraphsCapability.
// Implemented by infrastructure/kgtools (separate etap, requires DB schema).
//
// Given an agent_id and a list of bundle names the agent declared in its
// capability config, returns the set of auto-generated tool names that the
// agent should see (list_<type>, get_<type>, list_<type>_ids per schema).
// Tenant scope is read from ctx via domain.TenantIDFromContext inside the
// resolver implementation; capability does not propagate tenant explicitly.
type KGToolResolver interface {
	ResolveToolsForBundles(ctx context.Context, agentID string, bundles []string) ([]string, error)
}

// KnowledgeGraphsCapability is the dynamic capability that injects KG tools
// based on per-agent bundle selection. Unlike MemoryCapability /
// KnowledgeCapability (static, constant tool lists), this capability reads
// config.bundles and calls KGToolResolver to compute the agent's tool surface
// from current DB state.
//
// Config shape:
//
//	{ "bundles": ["bundle-a", "bundle-b"] }
type KnowledgeGraphsCapability struct {
	resolver KGToolResolver
}

// NewKnowledgeGraphsCapability constructs the capability bound to a resolver.
// resolver may be nil at construction time for testing Validate in isolation,
// but Tools() will reject nil-resolver calls at runtime.
func NewKnowledgeGraphsCapability(r KGToolResolver) KnowledgeGraphsCapability {
	return KnowledgeGraphsCapability{resolver: r}
}

// Type returns the stable identifier persisted in the capabilities table.
func (KnowledgeGraphsCapability) Type() string { return "knowledge_graphs" }

// Validate enforces the config shape: bundles required, non-empty array of
// valid bundle-name strings.
func (KnowledgeGraphsCapability) Validate(config map[string]any) error {
	if config == nil {
		return fmt.Errorf("knowledge_graphs config is required")
	}
	raw, ok := config["bundles"]
	if !ok {
		return fmt.Errorf("knowledge_graphs config.bundles is required")
	}
	arr, ok := raw.([]any)
	if !ok {
		return fmt.Errorf("knowledge_graphs config.bundles must be an array of strings")
	}
	if len(arr) == 0 {
		return fmt.Errorf("knowledge_graphs config.bundles must be non-empty")
	}
	for i, item := range arr {
		s, ok := item.(string)
		if !ok {
			return fmt.Errorf("knowledge_graphs config.bundles[%d] must be a string", i)
		}
		if !domain.ValidKGBundleName(s) {
			return fmt.Errorf("knowledge_graphs config.bundles[%d] invalid bundle name %q", i, s)
		}
	}
	return nil
}

// Tools validates config, extracts bundle names, and delegates to the resolver.
// Returns the resolver's tool list verbatim (resolver is responsible for
// deterministic ordering).
func (c KnowledgeGraphsCapability) Tools(ctx context.Context, agentID string, config map[string]any) ([]string, error) {
	if c.resolver == nil {
		return nil, fmt.Errorf("knowledge_graphs capability has no resolver configured")
	}
	if err := (KnowledgeGraphsCapability{}).Validate(config); err != nil {
		return nil, err
	}
	bundles := extractBundleNames(config)
	return c.resolver.ResolveToolsForBundles(ctx, agentID, bundles)
}

// extractBundleNames pulls validated bundle-name strings out of config.
// Caller must have run Validate first; this skips defensive checks.
func extractBundleNames(config map[string]any) []string {
	arr, _ := config["bundles"].([]any)
	out := make([]string, 0, len(arr))
	for _, item := range arr {
		s, _ := item.(string)
		out = append(out, s)
	}
	return out
}
