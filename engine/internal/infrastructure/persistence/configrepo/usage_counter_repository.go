package configrepo

import (
	"context"
	"errors"
	"fmt"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/models"
	"gorm.io/gorm"
)

// GORMUsageCounterRepository is the tenant-scoped CounterStore implementation.
// It reads rolling usage counters and advances them with an atomic upsert that
// applies the rolling reset in the same statement.
type GORMUsageCounterRepository struct {
	db *gorm.DB
}

// NewGORMUsageCounterRepository creates a new GORMUsageCounterRepository.
func NewGORMUsageCounterRepository(db *gorm.DB) *GORMUsageCounterRepository {
	return &GORMUsageCounterRepository{db: db}
}

// Read returns the counter for (tenant, userSub, rolling window), or (nil, nil)
// when the row does not exist yet.
func (r *GORMUsageCounterRepository) Read(ctx context.Context, userSub string) (*domain.UsageCounter, error) {
	var m models.UsageCounterModel
	err := r.db.WithContext(ctx).
		Scopes(tenantScope(ctx)).
		Where("user_sub = ? AND window_kind = ?", userSub, domain.WindowRolling).
		First(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read usage counter: %w", err)
	}
	return &domain.UsageCounter{
		Scope:       "",
		UserSub:     m.UserSub,
		WindowKind:  m.WindowKind,
		PeriodStart: m.PeriodStart.Unix(),
		TurnsCount:  m.TurnsCount,
		StepsCount:  m.StepsCount,
	}, nil
}

// incrementSQL is the atomic upsert that advances the rolling counter.
//
// On insert (no row yet) the counts start at :turnsDelta turns / :steps steps
// and period_start = now(). On conflict it either resets (when the window has
// elapsed: now - period_start > interval → counts become :turnsDelta/:steps and
// period_start = now()) or increments (turns += :turnsDelta, steps += :steps),
// all in a single statement so concurrent turns cannot race a check-then-write.
//
// Both counts advance regardless of the enforced unit, so the operator can
// switch the enforced unit mid-period without a recount. turnsDelta is 1 for a
// new turn and 0 for a HITL resume (which continues the same turn but still
// consumes step budget).
const incrementSQL = `
INSERT INTO usage_counters
    (tenant_id, user_sub, window_kind, period_start, turns_count, steps_count)
VALUES (?, ?, ?, now(), ?, ?)
ON CONFLICT (tenant_id, user_sub, window_kind) DO UPDATE SET
    period_start = CASE
        WHEN EXTRACT(EPOCH FROM (now() - usage_counters.period_start)) > ?
        THEN now()
        ELSE usage_counters.period_start
    END,
    turns_count = CASE
        WHEN EXTRACT(EPOCH FROM (now() - usage_counters.period_start)) > ?
        THEN ?
        ELSE usage_counters.turns_count + ?
    END,
    steps_count = CASE
        WHEN EXTRACT(EPOCH FROM (now() - usage_counters.period_start)) > ?
        THEN ?
        ELSE usage_counters.steps_count + ?
    END,
    updated_at = now()
`

// Increment atomically advances the counter for (tenant, userSub, rolling
// window), bumping turns by turnsDelta and steps by steps, applying the rolling
// reset when the window has elapsed — all in one statement.
func (r *GORMUsageCounterRepository) Increment(ctx context.Context, userSub string, intervalSeconds, turnsDelta int64, steps int) error {
	tenantID := tenantIDFromCtx(ctx)
	err := r.db.WithContext(ctx).Exec(incrementSQL,
		tenantID, userSub, domain.WindowRolling, turnsDelta, steps, // INSERT values
		intervalSeconds, // period_start CASE predicate
		intervalSeconds, // turns_count CASE predicate
		turnsDelta,      // turns_count reset value
		turnsDelta,      // turns_count increment value
		intervalSeconds, // steps_count CASE predicate
		steps,           // steps_count reset value
		steps,           // steps_count increment value
	).Error
	if err != nil {
		return fmt.Errorf("increment usage counter: %w", err)
	}
	return nil
}
