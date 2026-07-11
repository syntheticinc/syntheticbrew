package domain

import (
	"strings"
	"testing"
)

func TestTenantPolicyValidate(t *testing.T) {
	tests := []struct {
		name    string
		policy  TenantPolicy
		wantErr bool
	}{
		{"valid well-known key", TenantPolicy{Key: PolicyActiveUsersLimit, Value: "25"}, false},
		{"valid custom key", TenantPolicy{Key: "some_key_9", Value: "x"}, false},
		{"valid empty value", TenantPolicy{Key: PolicyWidgetAttribution, Value: ""}, false},
		{"valid max-length key", TenantPolicy{Key: strings.Repeat("k", 100), Value: "x"}, false},
		{"valid max-length value", TenantPolicy{Key: "k", Value: strings.Repeat("v", 8192)}, false},
		{"reject uppercase key", TenantPolicy{Key: "Bad_Key", Value: "x"}, true},
		{"reject dash in key", TenantPolicy{Key: "bad-key", Value: "x"}, true},
		{"reject 101-char key", TenantPolicy{Key: strings.Repeat("k", 101), Value: "x"}, true},
		{"reject empty key", TenantPolicy{Key: "", Value: "x"}, true},
		{"reject 8193-byte value", TenantPolicy{Key: "k", Value: strings.Repeat("v", 8193)}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.policy.Validate()
			if tt.wantErr && err == nil {
				t.Fatalf("expected error for %+v", tt.policy)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error for %+v: %v", tt.policy, err)
			}
		})
	}
}
