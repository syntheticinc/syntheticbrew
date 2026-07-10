package app

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

func int64Ptr(v int64) *int64 { return &v }

type fakeUsagePolicyReader struct {
	values map[string]string
}

func (f *fakeUsagePolicyReader) GetMany(ctx context.Context, keys []string) (map[string]string, error) {
	out := map[string]string{}
	for _, k := range keys {
		if v, ok := f.values[k]; ok {
			out[k] = v
		}
	}
	return out, nil
}

type fakeActiveUserCounter struct{ count int64 }

func (f fakeActiveUserCounter) CountActive(ctx context.Context) (int64, error) {
	return f.count, nil
}

type fakeUserSchemaCounter struct{ count int64 }

func (f fakeUserSchemaCounter) CountUserSchemas(ctx context.Context) (int64, error) {
	return f.count, nil
}

type fakeKnowledgeCounter struct{ count int64 }

func (f fakeKnowledgeCounter) CountDocuments(ctx context.Context) (int64, error) {
	return f.count, nil
}

type fakeTurnLimitReader struct{ cfg *domain.UsageLimit }

func (f fakeTurnLimitReader) Get(ctx context.Context, scope string) (*domain.UsageLimit, error) {
	return f.cfg, nil
}

type fakeTurnCounterReader struct{ counter *domain.UsageCounter }

func (f fakeTurnCounterReader) Read(ctx context.Context, userSub string) (*domain.UsageCounter, error) {
	return f.counter, nil
}

func TestUsageStatusAdapter_Compose(t *testing.T) {
	now := time.Unix(1_000_000, 0)

	tests := []struct {
		name           string
		policies       map[string]string
		limitCfg       *domain.UsageLimit
		counter        *domain.UsageCounter
		wantActiveLim  *int64
		wantSchemasLim *int64
		wantDocsLim    *int64
		wantTurnsUsed  int64
		wantTurnsLim   *int64
	}{
		{
			name: "all policies present, turns limit enabled",
			policies: map[string]string{
				domain.PolicyActiveUsersLimit:        "10",
				domain.PolicySchemasLimit:            "5",
				domain.PolicyKnowledgeDocumentsLimit: "100",
			},
			limitCfg:       &domain.UsageLimit{Scope: domain.ScopeTenant, Unit: domain.UnitTurns, LimitValue: 50, IntervalSeconds: 3600, Enabled: true},
			counter:        &domain.UsageCounter{PeriodStart: now.Unix() - 100, TurnsCount: 12, StepsCount: 30},
			wantActiveLim:  int64Ptr(10),
			wantSchemasLim: int64Ptr(5),
			wantDocsLim:    int64Ptr(100),
			wantTurnsUsed:  12,
			wantTurnsLim:   int64Ptr(50),
		},
		{
			name:           "policies absent → nil limits; no turn config → zero turns",
			policies:       map[string]string{},
			limitCfg:       nil,
			counter:        nil,
			wantActiveLim:  nil,
			wantSchemasLim: nil,
			wantDocsLim:    nil,
			wantTurnsUsed:  0,
			wantTurnsLim:   nil,
		},
		{
			name: "turn config disabled → nil turn limit, used still counted",
			policies: map[string]string{
				domain.PolicyActiveUsersLimit: "bogus", // malformed → nil
			},
			limitCfg:      &domain.UsageLimit{Scope: domain.ScopeTenant, Unit: domain.UnitTurns, LimitValue: 50, IntervalSeconds: 3600, Enabled: false},
			counter:       &domain.UsageCounter{PeriodStart: now.Unix() - 100, TurnsCount: 4, StepsCount: 9},
			wantActiveLim: nil,
			wantTurnsUsed: 4,
			wantTurnsLim:  nil,
		},
		{
			name:     "turn config enforces steps not turns → nil turn limit",
			policies: map[string]string{},
			limitCfg: &domain.UsageLimit{Scope: domain.ScopeTenant, Unit: domain.UnitSteps, LimitValue: 200, IntervalSeconds: 3600, Enabled: true},
			counter:  &domain.UsageCounter{PeriodStart: now.Unix() - 100, TurnsCount: 6, StepsCount: 40},
			// turns metric limit is only surfaced for the turns unit.
			wantTurnsUsed: 6,
			wantTurnsLim:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adapter := newUsageStatusAdapter(
				&fakeUsagePolicyReader{values: tt.policies},
				fakeActiveUserCounter{count: 3},
				fakeUserSchemaCounter{count: 2},
				fakeKnowledgeCounter{count: 7},
				fakeTurnLimitReader{cfg: tt.limitCfg},
				fakeTurnCounterReader{counter: tt.counter},
			)
			adapter.now = func() time.Time { return now }

			got, err := adapter.UsageStatus(context.Background())
			require.NoError(t, err)

			assert.Equal(t, int64(3), got.ActiveUsers.Used)
			assert.Equal(t, tt.wantActiveLim, got.ActiveUsers.Limit)

			assert.Equal(t, int64(2), got.Schemas.Used, "schemas Used = len(List)")
			assert.Equal(t, tt.wantSchemasLim, got.Schemas.Limit)

			assert.Equal(t, int64(7), got.KnowledgeDocuments.Used)
			assert.Equal(t, tt.wantDocsLim, got.KnowledgeDocuments.Limit)

			assert.Equal(t, tt.wantTurnsUsed, got.Turns.Used)
			assert.Equal(t, tt.wantTurnsLim, got.Turns.Limit)
		})
	}
}
