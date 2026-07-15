package admin

import "context"

// KnowledgeCapabilityEnsurer idempotently guarantees the knowledge capability
// is enabled for an agent. knowledge_search is capability-injected (Tier-2), so
// a knowledge base linked to an agent that lacks the enabled capability is inert
// — the agent cannot search it. Linking a KB is the point where grounding intent
// is expressed, so the link tool ensures the capability is on.
type KnowledgeCapabilityEnsurer interface {
	EnsureKnowledgeEnabled(ctx context.Context, agentName string) error
}

// capabilityEnsurer implements KnowledgeCapabilityEnsurer over the admin
// CapabilityRepository. It reuses the same repo + registry reloader as
// admin_add_capability so the enabled capability takes effect on the next turn.
type capabilityEnsurer struct {
	repo   CapabilityRepository
	reload func(context.Context)
}

// NewCapabilityEnsurer builds a KnowledgeCapabilityEnsurer. reload may be nil
// (no registry reload after the write).
func NewCapabilityEnsurer(repo CapabilityRepository, reload func(context.Context)) KnowledgeCapabilityEnsurer {
	return &capabilityEnsurer{repo: repo, reload: reload}
}

func (e *capabilityEnsurer) EnsureKnowledgeEnabled(ctx context.Context, agentName string) error {
	caps, err := e.repo.ListByAgent(ctx, agentName)
	if err != nil {
		return err
	}
	for i := range caps {
		if caps[i].Type != "knowledge" {
			continue
		}
		if caps[i].Enabled {
			return nil // already enabled — idempotent no-op
		}
		caps[i].Enabled = true
		if err := e.repo.Update(ctx, caps[i].ID, &caps[i]); err != nil {
			return err
		}
		e.triggerReload(ctx)
		return nil
	}

	if err := e.repo.Create(ctx, &CapabilityRecord{AgentName: agentName, Type: "knowledge", Enabled: true}); err != nil {
		return err
	}
	e.triggerReload(ctx)
	return nil
}

func (e *capabilityEnsurer) triggerReload(ctx context.Context) {
	if e.reload != nil {
		e.reload(ctx)
	}
}
