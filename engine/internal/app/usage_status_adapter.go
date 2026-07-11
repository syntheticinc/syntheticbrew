package app

import (
	"context"
	"fmt"
	"strconv"
	"time"

	deliveryhttp "github.com/syntheticinc/syntheticbrew/internal/delivery/http"
	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/usecase/usagelimit"
)

// usageStatusPolicyReader reads protected per-tenant policy values by key.
type usageStatusPolicyReader interface {
	GetMany(ctx context.Context, keys []string) (map[string]string, error)
}

// activeUserCounter counts distinct users active in the current rolling
// window. Satisfied by *activeusers.Gate.
type activeUserCounter interface {
	CountActive(ctx context.Context) (int64, error)
}

// turnLimitReader reads the operator-declared usage limit for a scope.
type turnLimitReader interface {
	Get(ctx context.Context, scope string) (*domain.UsageLimit, error)
}

// turnCounterReader reads the rolling usage counter for a user key.
type turnCounterReader interface {
	Read(ctx context.Context, userSub string) (*domain.UsageCounter, error)
}

// userSchemaCounter counts user-created schemas scoped by `tenant_id` from the
// context. Engine-managed system schemas (e.g. builder-schema) are excluded so
// they do not inflate the tenant's reported schema usage.
type userSchemaCounter interface {
	CountUserSchemas(ctx context.Context) (int64, error)
}

// usageStatusAdapter composes the tenant-scoped stores into the read-only
// usage aggregates served by GET /api/v1/usage-status.
type usageStatusAdapter struct {
	policies    usageStatusPolicyReader
	activeUsers activeUserCounter
	schemas     userSchemaCounter
	knowledge   KnowledgeDocumentCounterByTenant
	limits      turnLimitReader
	counters    turnCounterReader
	now         func() time.Time
}

// newUsageStatusAdapter creates a usageStatusAdapter.
func newUsageStatusAdapter(
	policies usageStatusPolicyReader,
	activeUsers activeUserCounter,
	schemas userSchemaCounter,
	knowledge KnowledgeDocumentCounterByTenant,
	limits turnLimitReader,
	counters turnCounterReader,
) *usageStatusAdapter {
	return &usageStatusAdapter{
		policies:    policies,
		activeUsers: activeUsers,
		schemas:     schemas,
		knowledge:   knowledge,
		limits:      limits,
		counters:    counters,
		now:         time.Now,
	}
}

// UsageStatus assembles the tenant's usage aggregates. All stores are
// tenant-scoped (resolve the tenant from context).
func (a *usageStatusAdapter) UsageStatus(ctx context.Context) (deliveryhttp.UsageStatusResponse, error) {
	policies, err := a.policies.GetMany(ctx, []string{
		domain.PolicyActiveUsersLimit,
		domain.PolicySchemasLimit,
		domain.PolicyKnowledgeDocumentsLimit,
	})
	if err != nil {
		return deliveryhttp.UsageStatusResponse{}, fmt.Errorf("read usage policies: %w", err)
	}

	activeUsed, err := a.activeUsers.CountActive(ctx)
	if err != nil {
		return deliveryhttp.UsageStatusResponse{}, fmt.Errorf("count active users: %w", err)
	}

	schemasUsed, err := a.schemas.CountUserSchemas(ctx)
	if err != nil {
		return deliveryhttp.UsageStatusResponse{}, fmt.Errorf("count schemas: %w", err)
	}

	documentsUsed, err := a.knowledge.CountDocuments(ctx)
	if err != nil {
		return deliveryhttp.UsageStatusResponse{}, fmt.Errorf("count knowledge documents: %w", err)
	}

	turns, err := a.turnsMetric(ctx)
	if err != nil {
		return deliveryhttp.UsageStatusResponse{}, err
	}

	return deliveryhttp.UsageStatusResponse{
		ActiveUsers: deliveryhttp.UsageStatusMetric{
			Used:  activeUsed,
			Limit: policyLimit(policies, domain.PolicyActiveUsersLimit),
		},
		Schemas: deliveryhttp.UsageStatusMetric{
			Used:  schemasUsed,
			Limit: policyLimit(policies, domain.PolicySchemasLimit),
		},
		KnowledgeDocuments: deliveryhttp.UsageStatusMetric{
			Used:  documentsUsed,
			Limit: policyLimit(policies, domain.PolicyKnowledgeDocumentsLimit),
		},
		Turns: turns,
	}, nil
}

// turnsMetric reports the tenant-scope turn usage. Used is the effective
// in-window turn count (same number the pre-turn gate compares); the limit is
// exposed only when the configured limit is enabled and enforces turns.
func (a *usageStatusAdapter) turnsMetric(ctx context.Context) (deliveryhttp.UsageStatusMetric, error) {
	cfg, err := a.limits.Get(ctx, domain.ScopeTenant)
	if err != nil {
		return deliveryhttp.UsageStatusMetric{}, fmt.Errorf("get tenant usage limit: %w", err)
	}
	if cfg == nil {
		// No configured limit → no rolling window to count within.
		return deliveryhttp.UsageStatusMetric{}, nil
	}

	counter, err := a.counters.Read(ctx, "")
	if err != nil {
		return deliveryhttp.UsageStatusMetric{}, fmt.Errorf("read tenant usage counter: %w", err)
	}

	metric := deliveryhttp.UsageStatusMetric{
		Used: usagelimit.EffectiveCount(counter, domain.UnitTurns, cfg.IntervalSeconds, a.now()),
	}
	if cfg.Enabled && cfg.Unit == domain.UnitTurns {
		limit := cfg.LimitValue
		metric.Limit = &limit
	}
	return metric, nil
}

// policyLimit parses a policy value as a positive int64 limit. Absent or
// malformed values (mirroring the activeusers gate: non-integer or <= 0)
// resolve to nil — "no limit configured".
func policyLimit(policies map[string]string, key string) *int64 {
	raw, ok := policies[key]
	if !ok {
		return nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value <= 0 {
		return nil
	}
	return &value
}
