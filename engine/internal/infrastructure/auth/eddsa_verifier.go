// Package auth holds the engine's default JWT verifier and signer.
//
// EdDSA (Ed25519) is the single JWT algorithm supported across all deployment
// modes (CE local-admin + externally issued). No HS256 shared-secret path
// exists — keeping a single algorithm removes the "alg confusion" attack
// surface and lets the same verifier accept tokens regardless of issuer.
package auth

import (
	"crypto/ed25519"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"

	"github.com/golang-jwt/jwt/v5"

	"github.com/syntheticinc/syntheticbrew/internal/authprim"
	"github.com/syntheticinc/syntheticbrew/pkg/plugin"
)

// EdDSAVerifier verifies JWTs signed with an Ed25519 key.
//
// Used by:
//   - External mode: publicKey provisioned out-of-band (the issuer's key),
//     tokens carry tenant_id + sub claims for per-tenant routing.
//   - Local mode: publicKey is the local keypair's public half (generated on
//     first boot by KeypairManager), tokens issued by local_session_handler
//     with sub="local-admin", tenant_id="".
type EdDSAVerifier struct {
	publicKey        ed25519.PublicKey
	expectedAudience string
}

// NewEdDSAVerifier creates a verifier from a raw Ed25519 public key (32 bytes).
// expectedAudience is the canonical URI this deployment accepts in a JWT `aud`
// claim; when empty, every audience-bound token is rejected (see Verify).
func NewEdDSAVerifier(publicKey ed25519.PublicKey, expectedAudience string) (*EdDSAVerifier, error) {
	if len(publicKey) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("invalid public key length: %d (want %d)", len(publicKey), ed25519.PublicKeySize)
	}
	return &EdDSAVerifier{publicKey: publicKey, expectedAudience: expectedAudience}, nil
}

// NewEdDSAVerifierFromHex accepts a hex-encoded Ed25519 public key (64 hex chars).
// Used by external mode where the public key is provided via config env var.
func NewEdDSAVerifierFromHex(publicKeyHex, expectedAudience string) (*EdDSAVerifier, error) {
	keyBytes, err := hex.DecodeString(publicKeyHex)
	if err != nil {
		return nil, fmt.Errorf("decode public key: %w", err)
	}
	return NewEdDSAVerifier(ed25519.PublicKey(keyBytes), expectedAudience)
}

// Verify parses and verifies a JWT string, returning the decoded claims.
//
// Rejection reasons:
//   - Signing alg is not EdDSA (prevents alg-confusion attacks).
//   - Token has no `exp` claim (prevents non-expiring tokens).
//   - Signature invalid or token expired.
//
// tenant_id, when present, must be a non-empty string with no NUL bytes.
// Local-admin tokens from local_session_handler carry tenant_id="" which
// is rendered as no-tenant scope (valid for CE single-tenant mode).
//
// Scopes depend on the `aud` claim (see resolveScopes): session/local tokens
// without one get plugin.ScopeAdmin — JWT holders are trusted admin/system
// actors. Audience-bound tokens carry their authority in the `scope` claim
// and are only accepted by the deployment whose expected audience matches.
// API tokens (bb_*) carry scoped permissions in their ScopesMask column,
// checked separately by the auth middleware.
func (v *EdDSAVerifier) Verify(tokenStr string) (plugin.Claims, error) {
	token, err := jwt.Parse(tokenStr, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodEd25519); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return v.publicKey, nil
	}, jwt.WithExpirationRequired())
	if err != nil {
		return plugin.Claims{}, fmt.Errorf("verify token: %w", err)
	}

	mc, ok := token.Claims.(jwt.MapClaims)
	if !ok || !token.Valid {
		return plugin.Claims{}, fmt.Errorf("invalid token claims")
	}

	subject, _ := mc.GetSubject()

	var tenantID string
	if raw, exists := mc["tenant_id"]; exists && raw != nil {
		s, ok := raw.(string)
		if !ok {
			return plugin.Claims{}, fmt.Errorf("tenant_id claim has unexpected type: %T", raw)
		}
		if strings.ContainsRune(s, 0) {
			return plugin.Claims{}, fmt.Errorf("tenant_id claim contains invalid characters")
		}
		tenantID = s
	}

	scopes, err := v.resolveScopes(mc)
	if err != nil {
		return plugin.Claims{}, err
	}

	return plugin.Claims{
		Subject:  subject,
		TenantID: tenantID,
		Scopes:   scopes,
	}, nil
}

// resolveScopes derives the scope bitmask from the aud/scope claims.
//
// No aud claim → session/local-admin token: full admin, the pre-audience
// behavior. An aud claim marks the token audience-bound (issued for exactly
// one deployment) and fails closed: this deployment must have an expected
// audience configured, it must be a member of aud (string or []string per
// RFC 7519 §4.1.3), and the space-delimited scope claim must parse to known
// scope names — otherwise the token is rejected, never widened to admin.
func (v *EdDSAVerifier) resolveScopes(mc jwt.MapClaims) (int, error) {
	rawAud, hasAud := mc["aud"]
	if !hasAud || rawAud == nil {
		return plugin.ScopeAdmin, nil
	}

	aud, err := mc.GetAudience()
	if err != nil || len(aud) == 0 {
		return 0, fmt.Errorf("invalid aud claim")
	}
	if v.expectedAudience == "" {
		return 0, fmt.Errorf("audience-bound token rejected: no expected audience configured")
	}
	if !slices.Contains(aud, v.expectedAudience) {
		return 0, fmt.Errorf("audience-bound token rejected: audience mismatch")
	}

	rawScope, hasScope := mc["scope"]
	if !hasScope || rawScope == nil {
		return 0, fmt.Errorf("audience-bound token has no scope claim")
	}
	scopeStr, ok := rawScope.(string)
	if !ok {
		return 0, fmt.Errorf("scope claim has unexpected type: %T", rawScope)
	}
	mask, err := authprim.ParseScopeClaim(scopeStr)
	if err != nil {
		return 0, fmt.Errorf("scope claim: %w", err)
	}
	return mask, nil
}
