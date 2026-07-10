package authprim

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseScopeClaim(t *testing.T) {
	tests := []struct {
		name     string
		scope    string
		wantMask int
		wantErr  bool
	}{
		{"single provision", "provision", ScopeProvisionMask, false},
		{"provision and manage", "provision manage", ScopeProvisionMask | ScopeManageMask, false},
		{"manage alone", "manage", ScopeManageMask, false},
		{"admin", "admin", ScopeAdmin, false},
		{"granular names", "chat tasks agents:read", ScopeChat | ScopeTasks | ScopeAgentsRead, false},
		{"extra whitespace tolerated", "  provision   manage  ", ScopeProvisionMask | ScopeManageMask, false},
		{"empty string", "", 0, true},
		{"whitespace only", "   ", 0, true},
		{"unknown name", "provision deploy", 0, true},
		{"case sensitive", "Provision", 0, true},
		{"offline_access not an engine scope", "provision offline_access", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mask, err := ParseScopeClaim(tt.scope)
			if tt.wantErr {
				require.Error(t, err)
				assert.Zero(t, mask)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantMask, mask)
		})
	}
}

func TestScopesToMask_DropsUnknownSilently(t *testing.T) {
	assert.Equal(t, ScopeChat, ScopesToMask([]string{"chat", "no-such-scope"}))
	assert.Zero(t, ScopesToMask(nil))
	assert.Zero(t, ScopesToMask([]string{"bogus"}))
}

// TestManageMaskSupersetOfProvision pins the invariant the MCP per-tool scope
// table relies on: a manage token can do everything a provision token can.
func TestManageMaskSupersetOfProvision(t *testing.T) {
	assert.Equal(t, ScopeProvisionMask, ScopeManageMask&ScopeProvisionMask)
	assert.NotZero(t, ScopeManageMask&ScopeManage)
	assert.Zero(t, ScopeProvisionMask&ScopeManage)
}
