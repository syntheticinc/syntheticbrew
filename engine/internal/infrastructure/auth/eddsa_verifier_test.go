package auth

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syntheticinc/syntheticbrew/internal/authprim"
	"github.com/syntheticinc/syntheticbrew/pkg/plugin"
)

const testAudience = "https://engine.example.test/api/v1/mcp/rpc"

func newVerifierKeypair(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	return pub, priv
}

func signClaims(t *testing.T, priv ed25519.PrivateKey, claims jwt.MapClaims) string {
	t.Helper()
	if _, ok := claims["exp"]; !ok {
		claims["exp"] = time.Now().Add(time.Hour).Unix()
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims).SignedString(priv)
	require.NoError(t, err)
	return signed
}

func TestEdDSAVerifier_Verify_AudienceAndScopes(t *testing.T) {
	pub, priv := newVerifierKeypair(t)

	tests := []struct {
		name             string
		expectedAudience string
		claims           jwt.MapClaims
		wantScopes       int
		wantErr          bool
	}{
		{
			name:             "no aud claim keeps full admin",
			expectedAudience: testAudience,
			claims:           jwt.MapClaims{"sub": "local-admin"},
			wantScopes:       plugin.ScopeAdmin,
		},
		{
			name:             "no aud and no expected audience keeps full admin",
			expectedAudience: "",
			claims:           jwt.MapClaims{"sub": "local-admin"},
			wantScopes:       plugin.ScopeAdmin,
		},
		{
			name:             "aud match with provision scope",
			expectedAudience: testAudience,
			claims:           jwt.MapClaims{"sub": "u1", "aud": testAudience, "scope": "provision"},
			wantScopes:       authprim.ScopeProvisionMask,
		},
		{
			name:             "aud match with provision manage scope",
			expectedAudience: testAudience,
			claims:           jwt.MapClaims{"sub": "u1", "aud": testAudience, "scope": "provision manage"},
			wantScopes:       authprim.ScopeProvisionMask | authprim.ScopeManageMask,
		},
		{
			name:             "aud as string array containing expected",
			expectedAudience: testAudience,
			claims:           jwt.MapClaims{"sub": "u1", "aud": []string{"https://other.example", testAudience}, "scope": "provision"},
			wantScopes:       authprim.ScopeProvisionMask,
		},
		{
			name:             "aud mismatch rejected",
			expectedAudience: testAudience,
			claims:           jwt.MapClaims{"sub": "u1", "aud": "https://evil.example/api", "scope": "provision"},
			wantErr:          true,
		},
		{
			name:             "aud present but no expected audience configured rejected",
			expectedAudience: "",
			claims:           jwt.MapClaims{"sub": "u1", "aud": testAudience, "scope": "provision"},
			wantErr:          true,
		},
		{
			name:             "unknown scope name rejected",
			expectedAudience: testAudience,
			claims:           jwt.MapClaims{"sub": "u1", "aud": testAudience, "scope": "provision superuser"},
			wantErr:          true,
		},
		{
			name:             "empty scope rejected",
			expectedAudience: testAudience,
			claims:           jwt.MapClaims{"sub": "u1", "aud": testAudience, "scope": ""},
			wantErr:          true,
		},
		{
			// Present-but-empty aud must reject, never widen to the no-aud
			// admin path (mirrors the EE verifier's guard).
			name:             "aud present but empty array rejected",
			expectedAudience: testAudience,
			claims:           jwt.MapClaims{"sub": "u1", "aud": []interface{}{}, "scope": "provision"},
			wantErr:          true,
		},
		{
			name:             "missing scope claim rejected",
			expectedAudience: testAudience,
			claims:           jwt.MapClaims{"sub": "u1", "aud": testAudience},
			wantErr:          true,
		},
		{
			name:             "non-string scope claim rejected",
			expectedAudience: testAudience,
			claims:           jwt.MapClaims{"sub": "u1", "aud": testAudience, "scope": []string{"provision"}},
			wantErr:          true,
		},
		{
			name:             "malformed aud claim rejected not widened to admin",
			expectedAudience: testAudience,
			claims:           jwt.MapClaims{"sub": "u1", "aud": 12345, "scope": "provision"},
			wantErr:          true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			verifier, err := NewEdDSAVerifier(pub, tt.expectedAudience)
			require.NoError(t, err)

			claims, err := verifier.Verify(signClaims(t, priv, tt.claims))
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantScopes, claims.Scopes)
			assert.Equal(t, tt.claims["sub"], claims.Subject)
		})
	}
}

func TestEdDSAVerifier_Verify_TenantAndBasics(t *testing.T) {
	pub, priv := newVerifierKeypair(t)
	verifier, err := NewEdDSAVerifier(pub, testAudience)
	require.NoError(t, err)

	t.Run("tenant_id carried through on audience-bound token", func(t *testing.T) {
		token := signClaims(t, priv, jwt.MapClaims{
			"sub":       "user-1",
			"tenant_id": "8b47d1f0-0000-4000-8000-000000000001",
			"aud":       testAudience,
			"scope":     "provision",
		})
		claims, err := verifier.Verify(token)
		require.NoError(t, err)
		assert.Equal(t, "8b47d1f0-0000-4000-8000-000000000001", claims.TenantID)
		assert.Equal(t, authprim.ScopeProvisionMask, claims.Scopes)
	})

	t.Run("missing exp rejected", func(t *testing.T) {
		signed, err := jwt.NewWithClaims(jwt.SigningMethodEdDSA, jwt.MapClaims{"sub": "u1"}).SignedString(priv)
		require.NoError(t, err)
		_, err = verifier.Verify(signed)
		require.Error(t, err)
	})

	t.Run("wrong key rejected", func(t *testing.T) {
		_, otherPriv := newVerifierKeypair(t)
		_, err := verifier.Verify(signClaims(t, otherPriv, jwt.MapClaims{"sub": "u1"}))
		require.Error(t, err)
	})
}
