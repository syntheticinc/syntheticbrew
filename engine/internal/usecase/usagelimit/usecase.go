// Package usagelimit implements a generic, operator-configurable usage-limiting
// feature. It gates a "turn" (one user message → full answer) before it runs
// and settles the counters once the turn completes.
//
// Two applications share the same machinery and differ only by scope:
//   - scope "tenant"   → one counter for the whole tenant (user_sub "").
//   - scope "per_user" → one counter per end user (user_sub = the real sub).
//
// Enforcement is a pre-turn gate plus a settle-increment, with no rollback: a
// turn that never completes simply never calls RecordTurn.
package usagelimit

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

// ConfigStore persists operator-declared usage limits, one row per
// (tenant, scope). It is tenant-scoped: the implementation resolves the tenant
// from the context.
type ConfigStore interface {
	// Get returns the limit for a scope, or (nil, nil) when none is configured.
	Get(ctx context.Context, scope string) (*domain.UsageLimit, error)
	// Set upserts the limit for its scope.
	Set(ctx context.Context, limit domain.UsageLimit) error
	// Delete removes the limit for a scope. Removing an absent limit is not an error.
	Delete(ctx context.Context, scope string) error
	// List returns all configured limits for the tenant.
	List(ctx context.Context) ([]domain.UsageLimit, error)
}

// CounterStore reads and advances rolling usage counters. It is tenant-scoped:
// the implementation resolves the tenant from the context. userSub "" selects
// the tenant-wide counter; a real sub selects that end user's counter.
type CounterStore interface {
	// Read returns the counter for (scope's user key, rolling window), or
	// (nil, nil) when the row does not exist yet.
	Read(ctx context.Context, userSub string) (*domain.UsageCounter, error)
	// Increment atomically advances the counter for (userSub, rolling window),
	// bumping turns by turnsDelta and steps by steps, applying the rolling reset
	// (when now - period_start > interval) in the same statement.
	Increment(ctx context.Context, userSub string, intervalSeconds, turnsDelta int64, steps int) error
}

// Clock returns the current time. Injected so tests can drive rolling windows
// deterministically.
type Clock func() time.Time

// Enforcer gates and settles turns against the configured usage limits and
// owns limit configuration management. It holds no mutable state.
type Enforcer struct {
	configs  ConfigStore
	counters CounterStore
	now      Clock
}

// New creates an Enforcer. clock may be nil, in which case time.Now is used.
func New(configs ConfigStore, counters CounterStore, clock Clock) *Enforcer {
	if clock == nil {
		clock = time.Now
	}
	return &Enforcer{configs: configs, counters: counters, now: clock}
}

// applicableScopes returns the scope→user_sub key pairs to gate/settle for a
// request. The tenant scope always keys user_sub ""; the per_user scope keys
// the real sub.
type scopeKey struct {
	scope   string
	userSub string
}

func applicableScopes(userSub string) []scopeKey {
	return []scopeKey{
		{scope: domain.ScopeTenant, userSub: ""},
		{scope: domain.ScopeUser, userSub: userSub},
	}
}

// CheckAllowed is the pre-turn gate. For each configured+enabled limit
// applicable to this request it computes the effective count for that limit's
// unit and blocks when any is at or over its limit. A request with no
// configured limits is always allowed.
func (e *Enforcer) CheckAllowed(ctx context.Context, userSub string) (domain.UsageDecision, error) {
	now := e.now()
	for _, sk := range applicableScopes(userSub) {
		cfg, err := e.configs.Get(ctx, sk.scope)
		if err != nil {
			return domain.UsageDecision{}, fmt.Errorf("get %s usage limit: %w", sk.scope, err)
		}
		if cfg == nil || !cfg.Enabled {
			continue
		}

		counter, err := e.counters.Read(ctx, sk.userSub)
		if err != nil {
			return domain.UsageDecision{}, fmt.Errorf("read %s usage counter: %w", sk.scope, err)
		}

		used := effectiveCount(counter, cfg.Unit, cfg.IntervalSeconds, now)
		if used >= cfg.LimitValue {
			slog.InfoContext(ctx, "usage limit reached",
				"scope", sk.scope, "unit", cfg.Unit, "limit", cfg.LimitValue, "used", used)
			return domain.UsageDecision{
				Allowed:      false,
				BlockedScope: sk.scope,
				Unit:         cfg.Unit,
				Limit:        cfg.LimitValue,
				Used:         used,
			}, nil
		}
	}
	return domain.UsageDecision{Allowed: true}, nil
}

// effectiveCount returns the count that counts against the limit for unit,
// treating a window that has rolled over (or a missing counter) as 0.
func effectiveCount(counter *domain.UsageCounter, unit string, intervalSeconds int64, now time.Time) int64 {
	if counter == nil {
		return 0
	}
	if now.Unix()-counter.PeriodStart > intervalSeconds {
		return 0
	}
	if unit == domain.UnitSteps {
		return counter.StepsCount
	}
	return counter.TurnsCount
}

// RecordTurn is the settle for a completed billable turn. It advances the
// matching counter for each configured scope, bumping turns by 1 and steps by
// steps so the enforced unit can switch mid-period without a recount. With no
// configured limits it is a no-op.
func (e *Enforcer) RecordTurn(ctx context.Context, userSub string, steps int) error {
	return e.record(ctx, userSub, 1, steps)
}

// RecordSteps settles work that is NOT a new turn — specifically a HITL resume,
// which continues the same turn. It advances only the step counter (turnsDelta
// 0) so resume work consumes step budget (and keeps the dual-unit counters
// accurate) without counting the wizard as more than one turn.
func (e *Enforcer) RecordSteps(ctx context.Context, userSub string, steps int) error {
	return e.record(ctx, userSub, 0, steps)
}

// record advances the counter for every configured scope by (turnsDelta, steps).
// With no configured limits it is a no-op.
func (e *Enforcer) record(ctx context.Context, userSub string, turnsDelta int64, steps int) error {
	for _, sk := range applicableScopes(userSub) {
		cfg, err := e.configs.Get(ctx, sk.scope)
		if err != nil {
			return fmt.Errorf("get %s usage limit: %w", sk.scope, err)
		}
		if cfg == nil {
			continue
		}
		if err := e.counters.Increment(ctx, sk.userSub, cfg.IntervalSeconds, turnsDelta, steps); err != nil {
			return fmt.Errorf("increment %s usage counter: %w", sk.scope, err)
		}
	}
	return nil
}

// SetLimit validates and upserts an operator-declared usage limit.
func (e *Enforcer) SetLimit(ctx context.Context, scope, unit string, limitValue, intervalSeconds int64, enabled bool) error {
	limit := domain.UsageLimit{
		Scope:           scope,
		Unit:            unit,
		LimitValue:      limitValue,
		IntervalSeconds: intervalSeconds,
		Enabled:         enabled,
	}
	if err := limit.Validate(); err != nil {
		return fmt.Errorf("validate usage limit: %w", err)
	}
	if err := e.configs.Set(ctx, limit); err != nil {
		return fmt.Errorf("set usage limit: %w", err)
	}
	return nil
}

// GetLimits returns all configured usage limits for the tenant.
func (e *Enforcer) GetLimits(ctx context.Context) ([]domain.UsageLimit, error) {
	limits, err := e.configs.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list usage limits: %w", err)
	}
	return limits, nil
}

// DeleteLimit removes the usage limit for a scope.
func (e *Enforcer) DeleteLimit(ctx context.Context, scope string) error {
	if scope != domain.ScopeTenant && scope != domain.ScopeUser {
		return fmt.Errorf("scope must be %q or %q, got %q", domain.ScopeTenant, domain.ScopeUser, scope)
	}
	if err := e.configs.Delete(ctx, scope); err != nil {
		return fmt.Errorf("delete usage limit: %w", err)
	}
	return nil
}
