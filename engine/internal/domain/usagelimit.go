package domain

import "fmt"

// Usage limit scopes. A "tenant" limit gates the whole tenant; a "per_user"
// limit gates each end user independently. The two share the same machinery
// and differ only by which counter row they key.
const (
	ScopeTenant = "tenant"
	ScopeUser   = "per_user"
)

// Usage limit units. A limit is enforced on one of these; both are always
// counted so the enforced unit can switch mid-period without a recount.
const (
	UnitTurns = "turns"
	UnitSteps = "steps"
)

// Usage counter windows. Only a rolling window exists today; the constant keeps
// callers from spelling the literal and leaves room for additive windows.
const WindowRolling = "rolling"

// UsageLimit is an operator-declared usage limit. One limit exists per
// (tenant, scope): Scope selects tenant-wide vs per-end-user gating, Unit
// selects which counter is compared against LimitValue, and IntervalSeconds is
// the rolling window after which the counter resets.
type UsageLimit struct {
	Scope           string
	Unit            string
	LimitValue      int64
	IntervalSeconds int64
	Enabled         bool
}

// Validate checks that a UsageLimit is well-formed. The DB CHECK constraints
// are a backstop; this is the primary gate so callers get a typed error before
// a round-trip.
func (l UsageLimit) Validate() error {
	if l.Scope != ScopeTenant && l.Scope != ScopeUser {
		return fmt.Errorf("scope must be %q or %q, got %q", ScopeTenant, ScopeUser, l.Scope)
	}
	if l.Unit != UnitTurns && l.Unit != UnitSteps {
		return fmt.Errorf("unit must be %q or %q, got %q", UnitTurns, UnitSteps, l.Unit)
	}
	if l.LimitValue <= 0 {
		return fmt.Errorf("limit_value must be > 0, got %d", l.LimitValue)
	}
	if l.IntervalSeconds <= 0 {
		return fmt.Errorf("interval_seconds must be > 0, got %d", l.IntervalSeconds)
	}
	return nil
}

// UsageCounter is a rolling usage counter for one (tenant, user_sub, window).
// UserSub "" is the tenant-wide counter; a real sub is a per-end-user counter.
// Both counts advance every turn; PeriodStart is when the current window began.
type UsageCounter struct {
	Scope       string
	UserSub     string
	WindowKind  string
	PeriodStart int64
	TurnsCount  int64
	StepsCount  int64
}

// UsageDecision is the result of the pre-turn gate. When Allowed is false the
// remaining fields describe the config that blocked: which scope, the enforced
// unit, its limit, and the effective count already used.
type UsageDecision struct {
	Allowed      bool
	BlockedScope string
	Unit         string
	Limit        int64
	Used         int64
}
