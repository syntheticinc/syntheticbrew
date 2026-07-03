package domain

import "testing"

func TestUsageLimit_Validate(t *testing.T) {
	tests := []struct {
		name    string
		limit   UsageLimit
		wantErr bool
	}{
		{"valid tenant turns", UsageLimit{Scope: ScopeTenant, Unit: UnitTurns, LimitValue: 10, IntervalSeconds: 3600, Enabled: true}, false},
		{"valid per_user steps", UsageLimit{Scope: ScopeUser, Unit: UnitSteps, LimitValue: 1, IntervalSeconds: 1, Enabled: false}, false},
		{"bad scope", UsageLimit{Scope: "org", Unit: UnitTurns, LimitValue: 10, IntervalSeconds: 3600}, true},
		{"empty scope", UsageLimit{Scope: "", Unit: UnitTurns, LimitValue: 10, IntervalSeconds: 3600}, true},
		{"bad unit", UsageLimit{Scope: ScopeTenant, Unit: "tokens", LimitValue: 10, IntervalSeconds: 3600}, true},
		{"zero limit", UsageLimit{Scope: ScopeTenant, Unit: UnitTurns, LimitValue: 0, IntervalSeconds: 3600}, true},
		{"negative limit", UsageLimit{Scope: ScopeTenant, Unit: UnitTurns, LimitValue: -5, IntervalSeconds: 3600}, true},
		{"zero interval", UsageLimit{Scope: ScopeTenant, Unit: UnitTurns, LimitValue: 10, IntervalSeconds: 0}, true},
		{"negative interval", UsageLimit{Scope: ScopeTenant, Unit: UnitTurns, LimitValue: 10, IntervalSeconds: -1}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.limit.Validate()
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}
