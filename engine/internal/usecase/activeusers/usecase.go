// Package activeusers implements the distinct-user gate: an operator-declared
// cap on how many DISTINCT end users may be active per rolling window
// (domain.ActiveUsersWindowSeconds). Unlike the turn/step usage limits it
// counts users existing, not work performed, so it applies to BYOK turns too.
//
// Enforcement is a pre-turn check plus a post-turn activity touch. Users
// already active inside the window are never locked out — only a NEW user
// arriving at/over the limit is rejected (or merely logged in monitor mode).
package activeusers

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

// PolicyReader reads protected per-tenant policy values by key. It is
// tenant-scoped: the implementation resolves the tenant from the context.
// Keys with no configured policy are simply absent from the map.
type PolicyReader interface {
	GetMany(ctx context.Context, keys []string) (map[string]string, error)
}

// ActivityStore records and queries per-user last-seen activity. It is
// tenant-scoped: the implementation resolves the tenant from the context.
type ActivityStore interface {
	// Touch marks userSub as active now, creating the row on first sight.
	Touch(ctx context.Context, userSub string) error
	// CountActiveSince returns the number of users active after since.
	CountActiveSince(ctx context.Context, since time.Time) (int64, error)
	// IsActiveSince reports whether userSub has been active after since.
	IsActiveSince(ctx context.Context, userSub string, since time.Time) (bool, error)
}

// Clock returns the current time. Injected so tests can drive the rolling
// window deterministically.
type Clock func() time.Time

// Gate checks a user against the configured active-users limit and records
// activity once a turn completes. It holds no mutable state.
type Gate struct {
	policies PolicyReader
	activity ActivityStore
	now      Clock
}

// New creates a Gate. clock may be nil, in which case time.Now is used.
func New(policies PolicyReader, activity ActivityStore, clock Clock) *Gate {
	if clock == nil {
		clock = time.Now
	}
	return &Gate{policies: policies, activity: activity, now: clock}
}

// windowStart returns the start of the rolling activity window.
func (g *Gate) windowStart() time.Time {
	return g.now().Add(-time.Duration(domain.ActiveUsersWindowSeconds) * time.Second)
}

// Check is the pre-turn gate. With no limit configured (or a malformed one —
// operator misconfig must never break chat) every user is allowed. A user
// already active inside the window is always allowed: the limit caps NEW
// users, existing ones are never locked out. A new user is allowed while the
// distinct-active count is below the limit; at/over it, monitor mode logs and
// allows, enforce mode rejects with the limit and current count.
func (g *Gate) Check(ctx context.Context, userSub string) (domain.ActiveUsersDecision, error) {
	policies, err := g.policies.GetMany(ctx, []string{domain.PolicyActiveUsersLimit, domain.PolicyActiveUsersMode})
	if err != nil {
		return domain.ActiveUsersDecision{}, fmt.Errorf("get active-users policies: %w", err)
	}
	raw, ok := policies[domain.PolicyActiveUsersLimit]
	if !ok {
		return domain.ActiveUsersDecision{Allowed: true}, nil
	}
	limit, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || limit <= 0 {
		slog.WarnContext(ctx, "active-users limit is not a positive integer — gate disabled", "value", raw)
		return domain.ActiveUsersDecision{Allowed: true}, nil
	}

	since := g.windowStart()
	active, err := g.activity.IsActiveSince(ctx, userSub, since)
	if err != nil {
		return domain.ActiveUsersDecision{}, fmt.Errorf("check user activity: %w", err)
	}
	if active {
		return domain.ActiveUsersDecision{Allowed: true}, nil
	}

	used, err := g.activity.CountActiveSince(ctx, since)
	if err != nil {
		return domain.ActiveUsersDecision{}, fmt.Errorf("count active users: %w", err)
	}
	if used < limit {
		return domain.ActiveUsersDecision{Allowed: true}, nil
	}
	if policies[domain.PolicyActiveUsersMode] == domain.ActiveUsersModeMonitor {
		slog.WarnContext(ctx, "user limit exceeded (monitor)", "used", used, "limit", limit)
		return domain.ActiveUsersDecision{Allowed: true}, nil
	}
	return domain.ActiveUsersDecision{Allowed: false, Limit: limit, Used: used}, nil
}

// RecordActivity marks userSub as active now. Called once a turn completes
// with real output, so a failed turn never mints an active user.
func (g *Gate) RecordActivity(ctx context.Context, userSub string) error {
	if err := g.activity.Touch(ctx, userSub); err != nil {
		return fmt.Errorf("record user activity: %w", err)
	}
	return nil
}

// CountActive returns the number of distinct users active in the current
// rolling window.
func (g *Gate) CountActive(ctx context.Context) (int64, error) {
	count, err := g.activity.CountActiveSince(ctx, g.windowStart())
	if err != nil {
		return 0, fmt.Errorf("count active users: %w", err)
	}
	return count, nil
}
