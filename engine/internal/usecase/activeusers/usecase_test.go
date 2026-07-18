package activeusers

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

// fixedNow anchors the rolling window so tests are deterministic.
var fixedNow = time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC)

func fixedClock() time.Time { return fixedNow }

type fakePolicyReader struct {
	values   map[string]string
	err      error
	getCalls int
	lastKeys []string
}

func (f *fakePolicyReader) GetMany(_ context.Context, keys []string) (map[string]string, error) {
	f.getCalls++
	f.lastKeys = keys
	if f.err != nil {
		return nil, f.err
	}
	return f.values, nil
}

type fakeActivityStore struct {
	activeSubs map[string]bool
	count      int64

	touchErr error
	countErr error
	checkErr error

	touchedSubs []string
	countCalls  int
	checkCalls  int
	lastSince   time.Time
}

func (f *fakeActivityStore) Touch(_ context.Context, userSub string) error {
	f.touchedSubs = append(f.touchedSubs, userSub)
	return f.touchErr
}

func (f *fakeActivityStore) CountActiveSince(_ context.Context, since time.Time) (int64, error) {
	f.countCalls++
	f.lastSince = since
	if f.countErr != nil {
		return 0, f.countErr
	}
	return f.count, nil
}

func (f *fakeActivityStore) IsActiveSince(_ context.Context, userSub string, since time.Time) (bool, error) {
	f.checkCalls++
	f.lastSince = since
	if f.checkErr != nil {
		return false, f.checkErr
	}
	return f.activeSubs[userSub], nil
}

// fakeFloor is a static FloorProvider: it returns a fixed (limit, enforce) pair
// with no I/O, mirroring the plugin's contract.
type fakeFloor struct {
	limit   int64
	enforce bool
}

func (f fakeFloor) Floor(_ context.Context) (int64, bool) { return f.limit, f.enforce }

func TestGate_Check(t *testing.T) {
	tests := []struct {
		name        string
		policies    map[string]string
		floor       FloorProvider
		activeSubs  map[string]bool
		count       int64
		userSub     string
		wantAllowed bool
		wantLimit   int64
		wantUsed    int64
	}{
		{
			// BUG-12 (a): no policy row, an enforcing floor of 100, count at the
			// floor. A NEW user must be rejected — the floor closes the
			// fail-open gap where a missing policy meant unlimited.
			name:        "missing policy with enforcing floor rejects new user at floor",
			policies:    map[string]string{},
			floor:       fakeFloor{limit: 100, enforce: true},
			count:       100,
			userSub:     "u-new",
			wantAllowed: false,
			wantLimit:   100,
			wantUsed:    100,
		},
		{
			// BUG-12 (b) — Fable F5: an unlimited plan surfaces as floor
			// limit=-1. The gate MUST treat <=0 as unlimited and allow, NOT
			// reject every user (used < -1 would reject all).
			name:        "unlimited floor (-1) allows despite high count",
			policies:    map[string]string{},
			floor:       fakeFloor{limit: -1, enforce: true},
			count:       9999,
			userSub:     "u-new",
			wantAllowed: true,
		},
		{
			// BUG-12 (c): a non-enforcing floor (a plugin that opts out) leaves
			// the gate unlimited — same as CE.
			name:        "non-enforcing floor leaves gate unlimited",
			policies:    map[string]string{},
			floor:       fakeFloor{limit: 100, enforce: false},
			count:       9999,
			userSub:     "u-new",
			wantAllowed: true,
		},
		{
			// BUG-12 (d): a malformed policy value with an enforcing floor is
			// floored (not fail-open) — operator misconfig cannot disable the cap.
			name:        "malformed policy with enforcing floor is floored",
			policies:    map[string]string{domain.PolicyActiveUsersLimit: "abc"},
			floor:       fakeFloor{limit: 100, enforce: true},
			count:       100,
			userSub:     "u-new",
			wantAllowed: false,
			wantLimit:   100,
			wantUsed:    100,
		},
		{
			name:        "no limit policy allows",
			policies:    map[string]string{},
			count:       1000,
			userSub:     "u-1",
			wantAllowed: true,
		},
		{
			name:        "existing user at limit allows",
			policies:    map[string]string{domain.PolicyActiveUsersLimit: "5"},
			activeSubs:  map[string]bool{"u-1": true},
			count:       5,
			userSub:     "u-1",
			wantAllowed: true,
		},
		{
			name:        "new user at limit enforce rejects with used and limit",
			policies:    map[string]string{domain.PolicyActiveUsersLimit: "5", domain.PolicyActiveUsersMode: domain.ActiveUsersModeEnforce},
			count:       5,
			userSub:     "u-new",
			wantAllowed: false,
			wantLimit:   5,
			wantUsed:    5,
		},
		{
			name:        "new user over limit without mode defaults to enforce",
			policies:    map[string]string{domain.PolicyActiveUsersLimit: "5"},
			count:       7,
			userSub:     "u-new",
			wantAllowed: false,
			wantLimit:   5,
			wantUsed:    7,
		},
		{
			name:        "new user at limit monitor allows",
			policies:    map[string]string{domain.PolicyActiveUsersLimit: "5", domain.PolicyActiveUsersMode: domain.ActiveUsersModeMonitor},
			count:       5,
			userSub:     "u-new",
			wantAllowed: true,
		},
		{
			name:        "unparseable limit allows",
			policies:    map[string]string{domain.PolicyActiveUsersLimit: "abc"},
			count:       1000,
			userSub:     "u-1",
			wantAllowed: true,
		},
		{
			name:        "negative limit allows",
			policies:    map[string]string{domain.PolicyActiveUsersLimit: "-5"},
			count:       1000,
			userSub:     "u-1",
			wantAllowed: true,
		},
		{
			name:        "zero limit allows",
			policies:    map[string]string{domain.PolicyActiveUsersLimit: "0"},
			count:       1000,
			userSub:     "u-1",
			wantAllowed: true,
		},
		{
			name:        "count below limit allows new user",
			policies:    map[string]string{domain.PolicyActiveUsersLimit: "5"},
			count:       4,
			userSub:     "u-new",
			wantAllowed: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			policies := &fakePolicyReader{values: tt.policies}
			activity := &fakeActivityStore{activeSubs: tt.activeSubs, count: tt.count}
			g := New(policies, activity, fixedClock, tt.floor)

			dec, err := g.Check(context.Background(), tt.userSub)
			if err != nil {
				t.Fatalf("Check: %v", err)
			}
			if dec.Allowed != tt.wantAllowed {
				t.Fatalf("Allowed: want %v, got %v", tt.wantAllowed, dec.Allowed)
			}
			if !tt.wantAllowed {
				if dec.Limit != tt.wantLimit || dec.Used != tt.wantUsed {
					t.Fatalf("want Limit=%d Used=%d, got Limit=%d Used=%d", tt.wantLimit, tt.wantUsed, dec.Limit, dec.Used)
				}
			}
			if policies.getCalls != 1 {
				t.Fatalf("want exactly 1 GetMany, got %d", policies.getCalls)
			}
		})
	}
}

func TestGate_Check_WindowIsThirtyDaysBeforeNow(t *testing.T) {
	policies := &fakePolicyReader{values: map[string]string{domain.PolicyActiveUsersLimit: "5"}}
	activity := &fakeActivityStore{count: 0}
	g := New(policies, activity, fixedClock, nil)

	if _, err := g.Check(context.Background(), "u-1"); err != nil {
		t.Fatalf("Check: %v", err)
	}
	wantSince := fixedNow.Add(-time.Duration(domain.ActiveUsersWindowSeconds) * time.Second)
	if !activity.lastSince.Equal(wantSince) {
		t.Fatalf("window start: want %v, got %v", wantSince, activity.lastSince)
	}
}

func TestGate_Check_NoLimitSkipsActivityQueries(t *testing.T) {
	policies := &fakePolicyReader{values: map[string]string{}}
	activity := &fakeActivityStore{}
	g := New(policies, activity, fixedClock, nil)

	if _, err := g.Check(context.Background(), "u-1"); err != nil {
		t.Fatalf("Check: %v", err)
	}
	if activity.checkCalls != 0 || activity.countCalls != 0 {
		t.Fatalf("no-limit must not query activity, got check=%d count=%d", activity.checkCalls, activity.countCalls)
	}
}

func TestGate_Check_ExistingUserSkipsCount(t *testing.T) {
	policies := &fakePolicyReader{values: map[string]string{domain.PolicyActiveUsersLimit: "5"}}
	activity := &fakeActivityStore{activeSubs: map[string]bool{"u-1": true}, count: 999}
	g := New(policies, activity, fixedClock, nil)

	dec, err := g.Check(context.Background(), "u-1")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !dec.Allowed {
		t.Fatal("existing user must be allowed")
	}
	if activity.countCalls != 0 {
		t.Fatalf("existing user must not trigger a count, got %d", activity.countCalls)
	}
}

func TestGate_Check_ErrorPropagation(t *testing.T) {
	sentinel := errors.New("boom")
	tests := []struct {
		name     string
		policies *fakePolicyReader
		activity *fakeActivityStore
	}{
		{
			name:     "policy read error",
			policies: &fakePolicyReader{err: sentinel},
			activity: &fakeActivityStore{},
		},
		{
			name:     "is-active error",
			policies: &fakePolicyReader{values: map[string]string{domain.PolicyActiveUsersLimit: "5"}},
			activity: &fakeActivityStore{checkErr: sentinel},
		},
		{
			name:     "count error",
			policies: &fakePolicyReader{values: map[string]string{domain.PolicyActiveUsersLimit: "5"}},
			activity: &fakeActivityStore{countErr: sentinel},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := New(tt.policies, tt.activity, fixedClock, nil)
			if _, err := g.Check(context.Background(), "u-1"); !errors.Is(err, sentinel) {
				t.Fatalf("want wrapped sentinel error, got %v", err)
			}
		})
	}
}

func TestGate_RecordActivity_TouchPassthrough(t *testing.T) {
	activity := &fakeActivityStore{}
	g := New(&fakePolicyReader{}, activity, fixedClock, nil)

	if err := g.RecordActivity(context.Background(), "u-1"); err != nil {
		t.Fatalf("RecordActivity: %v", err)
	}
	if len(activity.touchedSubs) != 1 || activity.touchedSubs[0] != "u-1" {
		t.Fatalf("want Touch(u-1), got %v", activity.touchedSubs)
	}
}

func TestGate_RecordActivity_WrapsError(t *testing.T) {
	sentinel := errors.New("boom")
	activity := &fakeActivityStore{touchErr: sentinel}
	g := New(&fakePolicyReader{}, activity, fixedClock, nil)

	if err := g.RecordActivity(context.Background(), "u-1"); !errors.Is(err, sentinel) {
		t.Fatalf("want wrapped sentinel error, got %v", err)
	}
}

func TestGate_CountActive(t *testing.T) {
	activity := &fakeActivityStore{count: 42}
	g := New(&fakePolicyReader{}, activity, fixedClock, nil)

	count, err := g.CountActive(context.Background())
	if err != nil {
		t.Fatalf("CountActive: %v", err)
	}
	if count != 42 {
		t.Fatalf("want 42, got %d", count)
	}
	wantSince := fixedNow.Add(-time.Duration(domain.ActiveUsersWindowSeconds) * time.Second)
	if !activity.lastSince.Equal(wantSince) {
		t.Fatalf("window start: want %v, got %v", wantSince, activity.lastSince)
	}
}

func TestNew_NilClockDefaultsToTimeNow(t *testing.T) {
	g := New(&fakePolicyReader{}, &fakeActivityStore{}, nil, nil)
	if g.now == nil {
		t.Fatal("nil clock must default to time.Now")
	}
	if d := time.Since(g.now()); d < 0 || d > time.Minute {
		t.Fatalf("default clock is not wall time: drift %v", d)
	}
}
