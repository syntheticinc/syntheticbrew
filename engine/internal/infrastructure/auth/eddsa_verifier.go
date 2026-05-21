// Package auth holds the engine's default JWT verifier and signer.
//
// EdDSA (Ed25519) is the single JWT algorithm supported across all deployment
// modes (CE local-admin + Cloud landing-issued). No HS256 shared-secret path
// exists — keeping a single algorithm removes the "alg confusion" attack
// surface and lets the same verifier accept tokens regardless of issuer.
package auth

import (
	"crypto/ed25519"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/golang-jwt/jwt/v5"

	"github.com/syntheticinc/syntheticbrew/pkg/plugin"
)

// EdDSAVerifier verifies JWTs signed with an Ed25519 key.
//
// Used by:
//   - Cloud mode: publicKey provisioned out-of-band (landing's issuing key),
//     tokens carry tenant_id + sub claims for per-tenant routing.
//   - Local mode: publicKey is the local keypair's public half (generated on
//     first boot by KeypairManager), tokens issued by local_session_handler
//     with sub="local-admin", tenant_id="".
type EdDSAVerifier struct {
	publicKey ed25519.PublicKey
}

// NewEdDSAVerifier creates a verifier from a raw Ed25519 public key (32 bytes).
func NewEdDSAVerifier(publicKey ed25519.PublicKey) (*EdDSAVerifier, error) {
	if len(publicKey) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("invalid public key length: %d (want %d)", len(publicKey), ed25519.PublicKeySize)
	}
	return &EdDSAVerifier{publicKey: publicKey}, nil
}

// NewEdDSAVerifierFromHex accepts a hex-encoded Ed25519 public key (64 hex chars).
// Used by Cloud mode where the public key is provided via config env var.
func NewEdDSAVerifierFromHex(publicKeyHex string) (*EdDSAVerifier, error) {
	keyBytes, err := hex.DecodeString(publicKeyHex)
	if err != nil {
		return nil, fmt.Errorf("decode public key: %w", err)
	}
	return NewEdDSAVerifier(ed25519.PublicKey(keyBytes))
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
// Scopes are set to plugin.ScopeAdmin for all successfully-verified JWTs —
// JWT holders are trusted admin/system actors, not API consumers with
// limited permissions. API tokens (bb_*) carry scoped permissions in their
// ScopesMask column, checked separately by the auth middleware.
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

	return plugin.Claims{
		Subject:  subject,
		TenantID: tenantID,
		Scopes:   plugin.ScopeAdmin,
	}, nil
}
