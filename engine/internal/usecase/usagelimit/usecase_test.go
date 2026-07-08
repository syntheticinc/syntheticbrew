package usagelimit

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

// fakeConfigStore is an in-memory ConfigStore keyed by scope.
type fakeConfigStore struct {
	limits map[string]domain.UsageLimit
	getErr error
}

func newFakeConfigStore() *fakeConfigStore {
	return &fakeConfigStore{limits: map[string]domain.UsageLimit{}}
}

func (s *fakeConfigStore) Get(_ context.Context, scope string) (*domain.UsageLimit, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	l, ok := s.limits[scope]
	if !ok {
		return nil, nil
	}
	return &l, nil
}

func (s *fakeConfigStore) Set(_ context.Context, limit domain.UsageLimit) error {
	s.limits[limit.Scope] = limit
	return nil
}

func (s *fakeConfigStore) Delete(_ context.Context, scope string) error {
	delete(s.limits, scope)
	return nil
}

func (s *fakeConfigStore) List(_ context.Context) ([]domain.UsageLimit, error) {
	out := make([]domain.UsageLimit, 0, len(s.limits))
	for _, l := range s.limits {
		out = append(out, l)
	}
	return out, nil
}

// fakeCounterStore is an in-memory CounterStore keyed by user_sub. It mirrors
// the SQL rolling-reset semantics so the usecase logic is exercised faithfully.
type fakeCounterStore struct {
	rows map[string]*domain.UsageCounter
	now  func() time.Time
}

func newFakeCounterStore(now func() time.Time) *fakeCounterStore {
	return &fakeCounterStore{rows: map[string]*domain.UsageCounter{}, now: now}
}

func (s *fakeCounterStore) Read(_ context.Context, userSub string) (*domain.UsageCounter, error) {
	r, ok := s.rows[userSub]
	if !ok {
		return nil, nil
	}
	cp := *r
	return &cp, nil
}

func (s *fakeCounterStore) Increment(_ context.Context, userSub string, intervalSeconds, turnsDelta int64, steps int) error {
	now := s.now().Unix()
	r, ok := s.rows[userSub]
	if !ok {
		s.rows[userSub] = &domain.UsageCounter{
			UserSub:     userSub,
			WindowKind:  domain.WindowRolling,
			PeriodStart: now,
			TurnsCount:  turnsDelta,
			StepsCount:  int64(steps),
		}
		return nil
	}
	if now-r.PeriodStart > intervalSeconds {
		r.PeriodStart = now
		r.TurnsCount = turnsDelta
		r.StepsCount = int64(steps)
		return nil
	}
	r.TurnsCount += turnsDelta
	r.StepsCount += int64(steps)
	return nil
}

func fixedClock(t time.Time) Clock {
	return func() time.Time { return t }
}

func TestCheckAllowed_NoConfig_Allows(t *testing.T) {
	e := New(newFakeConfigStore(), newFakeCounterStore(time.Now), nil)

	dec, err := e.CheckAllowed(context.Background(), "user-1")
	require.NoError(t, err)
	assert.True(t, dec.Allowed)
}

func TestRecordTurn_NoConfig_NoOp(t *testing.T) {
	counters := newFakeCounterStore(time.Now)
	e := New(newFakeConfigStore(), counters, nil)

	require.NoError(t, e.RecordTurn(context.Background(), "user-1", 3))
	assert.Empty(t, counters.rows, "no config → RecordTurn must not write any counter")
}

func TestCheckAllowed_UnderLimit_Allows(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	cfg := newFakeConfigStore()
	require.NoError(t, cfg.Set(context.Background(), domain.UsageLimit{
		Scope: domain.ScopeTenant, Unit: domain.UnitTurns, LimitValue: 5, IntervalSeconds: 3600, Enabled: true,
	}))
	counters := newFakeCounterStore(func() time.Time { return now })
	e := New(cfg, counters, fixedClock(now))
	ctx := context.Background()

	// 4 turns recorded, limit is 5 → still allowed.
	for i := 0; i < 4; i++ {
		require.NoError(t, e.RecordTurn(ctx, "user-1", 1))
	}
	dec, err := e.CheckAllowed(ctx, "user-1")
	require.NoError(t, err)
	assert.True(t, dec.Allowed)
}

func TestCheckAllowed_AtLimit_Blocks(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	cfg := newFakeConfigStore()
	require.NoError(t, cfg.Set(context.Background(), domain.UsageLimit{
		Scope: domain.ScopeTenant, Unit: domain.UnitTurns, LimitValue: 3, IntervalSeconds: 3600, Enabled: true,
	}))
	counters := newFakeCounterStore(func() time.Time { return now })
	e := New(cfg, counters, fixedClock(now))
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		require.NoError(t, e.RecordTurn(ctx, "user-1", 1))
	}
	dec, err := e.CheckAllowed(ctx, "user-1")
	require.NoError(t, err)
	assert.False(t, dec.Allowed)
	assert.Equal(t, domain.ScopeTenant, dec.BlockedScope)
	assert.Equal(t, domain.UnitTurns, dec.Unit)
	assert.Equal(t, int64(3), dec.Limit)
	assert.Equal(t, int64(3), dec.Used)
}

func TestCheckAllowed_DisabledConfig_Allows(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	cfg := newFakeConfigStore()
	require.NoError(t, cfg.Set(context.Background(), domain.UsageLimit{
		Scope: domain.ScopeTenant, Unit: domain.UnitTurns, LimitValue: 1, IntervalSeconds: 3600, Enabled: false,
	}))
	counters := newFakeCounterStore(func() time.Time { return now })
	e := New(cfg, counters, fixedClock(now))
	ctx := context.Background()

	require.NoError(t, e.RecordTurn(ctx, "user-1", 1))
	dec, err := e.CheckAllowed(ctx, "user-1")
	require.NoError(t, err)
	assert.True(t, dec.Allowed, "a disabled config must not gate")
}

func TestCheckAllowed_RollingReset_AllowsAfterExpiry(t *testing.T) {
	start := time.Unix(1_000_000, 0)
	cfg := newFakeConfigStore()
	require.NoError(t, cfg.Set(context.Background(), domain.UsageLimit{
		Scope: domain.ScopeTenant, Unit: domain.UnitTurns, LimitValue: 2, IntervalSeconds: 100, Enabled: true,
	}))

	clockNow := start
	counters := newFakeCounterStore(func() time.Time { return clockNow })
	e := New(cfg, counters, func() time.Time { return clockNow })
	ctx := context.Background()

	require.NoError(t, e.RecordTurn(ctx, "user-1", 1))
	require.NoError(t, e.RecordTurn(ctx, "user-1", 1))

	dec, err := e.CheckAllowed(ctx, "user-1")
	require.NoError(t, err)
	assert.False(t, dec.Allowed, "at limit before window elapses")

	// Advance past the interval → the window is expired, effective count is 0.
	clockNow = start.Add(101 * time.Second)
	dec, err = e.CheckAllowed(ctx, "user-1")
	require.NoError(t, err)
	assert.True(t, dec.Allowed, "expired window must allow again")
}

func TestRecordTurn_DualUnit_BumpsBothCounts(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	cfg := newFakeConfigStore()
	// Only turns is enforced, but steps must still be counted.
	require.NoError(t, cfg.Set(context.Background(), domain.UsageLimit{
		Scope: domain.ScopeTenant, Unit: domain.UnitTurns, LimitValue: 100, IntervalSeconds: 3600, Enabled: true,
	}))
	counters := newFakeCounterStore(func() time.Time { return now })
	e := New(cfg, counters, fixedClock(now))
	ctx := context.Background()

	require.NoError(t, e.RecordTurn(ctx, "user-1", 5))

	row, err := counters.Read(ctx, "") // tenant scope → user_sub ""
	require.NoError(t, err)
	require.NotNil(t, row)
	assert.Equal(t, int64(1), row.TurnsCount, "one turn recorded")
	assert.Equal(t, int64(5), row.StepsCount, "steps must advance even though turns is enforced")
}

func TestCheckAllowed_MidPeriodUnitSwitch_ComparesEnforcedUnit(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	ctx := context.Background()

	// Seed a counter with turns=10, steps=200 by recording 10 turns of 20 steps
	// while enforcing turns with a high limit (so nothing blocks during seeding).
	seed := func() (*fakeConfigStore, *fakeCounterStore, *Enforcer) {
		cfg := newFakeConfigStore()
		require.NoError(t, cfg.Set(ctx, domain.UsageLimit{
			Scope: domain.ScopeTenant, Unit: domain.UnitTurns, LimitValue: 1000, IntervalSeconds: 3600, Enabled: true,
		}))
		counters := newFakeCounterStore(func() time.Time { return now })
		e := New(cfg, counters, fixedClock(now))
		for i := 0; i < 10; i++ {
			require.NoError(t, e.RecordTurn(ctx, "user-1", 20))
		}
		row, err := counters.Read(ctx, "")
		require.NoError(t, err)
		require.Equal(t, int64(10), row.TurnsCount)
		require.Equal(t, int64(200), row.StepsCount)
		return cfg, counters, e
	}

	t.Run("switch to steps → gate compares steps (200)", func(t *testing.T) {
		cfg, _, e := seed()
		// Switch the enforced unit to steps with a limit at 200 → must block on steps.
		require.NoError(t, cfg.Set(ctx, domain.UsageLimit{
			Scope: domain.ScopeTenant, Unit: domain.UnitSteps, LimitValue: 200, IntervalSeconds: 3600, Enabled: true,
		}))
		dec, err := e.CheckAllowed(ctx, "user-1")
		require.NoError(t, err)
		assert.False(t, dec.Allowed, "steps=200 >= limit 200 must block")
		assert.Equal(t, domain.UnitSteps, dec.Unit)
		assert.Equal(t, int64(200), dec.Used)

		// A steps limit of 201 with the same counts must allow (turns=10 would
		// wrongly block if the gate still compared turns).
		require.NoError(t, cfg.Set(ctx, domain.UsageLimit{
			Scope: domain.ScopeTenant, Unit: domain.UnitSteps, LimitValue: 201, IntervalSeconds: 3600, Enabled: true,
		}))
		dec, err = e.CheckAllowed(ctx, "user-1")
		require.NoError(t, err)
		assert.True(t, dec.Allowed, "steps=200 < limit 201 must allow")
	})

	t.Run("stay on turns → gate compares turns (10)", func(t *testing.T) {
		cfg, _, e := seed()
		// Enforce turns at 10 → block on turns even though steps is 200.
		require.NoError(t, cfg.Set(ctx, domain.UsageLimit{
			Scope: domain.ScopeTenant, Unit: domain.UnitTurns, LimitValue: 10, IntervalSeconds: 3600, Enabled: true,
		}))
		dec, err := e.CheckAllowed(ctx, "user-1")
		require.NoError(t, err)
		assert.False(t, dec.Allowed, "turns=10 >= limit 10 must block")
		assert.Equal(t, domain.UnitTurns, dec.Unit)
		assert.Equal(t, int64(10), dec.Used)
	})
}

func TestCheckAllowed_PerUserScope_IsolatesUsers(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	cfg := newFakeConfigStore()
	require.NoError(t, cfg.Set(context.Background(), domain.UsageLimit{
		Scope: domain.ScopeUser, Unit: domain.UnitTurns, LimitValue: 2, IntervalSeconds: 3600, Enabled: true,
	}))
	counters := newFakeCounterStore(func() time.Time { return now })
	e := New(cfg, counters, fixedClock(now))
	ctx := context.Background()

	// user-a exhausts its own limit.
	require.NoError(t, e.RecordTurn(ctx, "user-a", 1))
	require.NoError(t, e.RecordTurn(ctx, "user-a", 1))

	decA, err := e.CheckAllowed(ctx, "user-a")
	require.NoError(t, err)
	assert.False(t, decA.Allowed, "user-a is at its per-user limit")

	// user-b is unaffected.
	decB, err := e.CheckAllowed(ctx, "user-b")
	require.NoError(t, err)
	assert.True(t, decB.Allowed, "user-b must not be gated by user-a's usage")
}

func TestSetLimit_Validation(t *testing.T) {
	tests := []struct {
		name            string
		scope           string
		unit            string
		limitValue      int64
		intervalSeconds int64
		wantErr         bool
	}{
		{"valid tenant turns", domain.ScopeTenant, domain.UnitTurns, 10, 3600, false},
		{"valid per_user steps", domain.ScopeUser, domain.UnitSteps, 100, 60, false},
		{"bad scope", "org", domain.UnitTurns, 10, 3600, true},
		{"bad unit", domain.ScopeTenant, "tokens", 10, 3600, true},
		{"zero limit", domain.ScopeTenant, domain.UnitTurns, 0, 3600, true},
		{"negative limit", domain.ScopeTenant, domain.UnitTurns, -1, 3600, true},
		{"zero interval", domain.ScopeTenant, domain.UnitTurns, 10, 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := New(newFakeConfigStore(), newFakeCounterStore(time.Now), nil)
			err := e.SetLimit(context.Background(), tt.scope, tt.unit, tt.limitValue, tt.intervalSeconds, true)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestGetLimits_ReturnsConfigured(t *testing.T) {
	e := New(newFakeConfigStore(), newFakeCounterStore(time.Now), nil)
	ctx := context.Background()

	require.NoError(t, e.SetLimit(ctx, domain.ScopeTenant, domain.UnitTurns, 10, 3600, true))
	require.NoError(t, e.SetLimit(ctx, domain.ScopeUser, domain.UnitSteps, 50, 60, true))

	limits, err := e.GetLimits(ctx)
	require.NoError(t, err)
	assert.Len(t, limits, 2)
}

func TestDeleteLimit(t *testing.T) {
	e := New(newFakeConfigStore(), newFakeCounterStore(time.Now), nil)
	ctx := context.Background()

	require.NoError(t, e.SetLimit(ctx, domain.ScopeTenant, domain.UnitTurns, 10, 3600, true))
	require.NoError(t, e.DeleteLimit(ctx, domain.ScopeTenant))

	limits, err := e.GetLimits(ctx)
	require.NoError(t, err)
	assert.Empty(t, limits)

	// Deleting an absent limit is not an error.
	require.NoError(t, e.DeleteLimit(ctx, domain.ScopeTenant))

	// Invalid scope is rejected before touching the store.
	require.Error(t, e.DeleteLimit(ctx, "org"))
}

func TestRecordSteps_BumpsStepsNotTurns(t *testing.T) {
	cfg := newFakeConfigStore()
	require.NoError(t, cfg.Set(context.Background(), domain.UsageLimit{Scope: domain.ScopeTenant, Unit: domain.UnitTurns, LimitValue: 100, IntervalSeconds: 3600, Enabled: true}))
	counters := newFakeCounterStore(time.Now)
	e := New(cfg, counters, time.Now)

	// One real turn (turns=1, steps=4), then a resume (steps+=6, turns+0).
	require.NoError(t, e.RecordTurn(context.Background(), "", 4))
	require.NoError(t, e.RecordSteps(context.Background(), "", 6))

	row := counters.rows[""]
	require.NotNil(t, row)
	assert.Equal(t, int64(1), row.TurnsCount, "resume must NOT add a turn")
	assert.Equal(t, int64(10), row.StepsCount, "resume steps must accrue (4+6)")
}

func TestEffectiveCount(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	tests := []struct {
		name            string
		counter         *domain.UsageCounter
		unit            string
		intervalSeconds int64
		want            int64
	}{
		{
			name:            "nil counter is zero",
			counter:         nil,
			unit:            domain.UnitTurns,
			intervalSeconds: 3600,
			want:            0,
		},
		{
			name:            "in-window turns",
			counter:         &domain.UsageCounter{PeriodStart: now.Unix() - 100, TurnsCount: 7, StepsCount: 42},
			unit:            domain.UnitTurns,
			intervalSeconds: 3600,
			want:            7,
		},
		{
			name:            "in-window steps",
			counter:         &domain.UsageCounter{PeriodStart: now.Unix() - 100, TurnsCount: 7, StepsCount: 42},
			unit:            domain.UnitSteps,
			intervalSeconds: 3600,
			want:            42,
		},
		{
			name:            "rolled-over window is zero",
			counter:         &domain.UsageCounter{PeriodStart: now.Unix() - 7200, TurnsCount: 7, StepsCount: 42},
			unit:            domain.UnitTurns,
			intervalSeconds: 3600,
			want:            0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EffectiveCount(tt.counter, tt.unit, tt.intervalSeconds, now)
			assert.Equal(t, tt.want, got)
		})
	}
}
