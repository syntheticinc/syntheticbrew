package auth

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/golang-jwt/jwt/v5"

	"github.com/syntheticinc/syntheticbrew/internal/authprim"
	"github.com/syntheticinc/syntheticbrew/pkg/plugin"
)

// asScopeNameToMask is the ONLY scope vocabulary an authorization-server access
// token may carry. It is deliberately narrow: "provision" and "manage" and
// nothing else. Any other scope name (including "admin", "config", or any other
// canonical authprim name) must be rejected, so the AS path can never mint an
// admin-privileged token. This is why strictASVerifier does NOT reuse
// authprim.ParseScopeClaim, which accepts the full vocabulary including admin.
var asScopeNameToMask = map[string]int{
	"provision": authprim.ScopeProvisionMask,
	"manage":    authprim.ScopeManageMask,
}

// CompositeVerifier routes an incoming JWT to the right verifier by its header
// `kid`. Authorization-server access tokens carry a kid matching a registered
// AS keypair and are verified under the strict, admin-denying rules. Every
// other token (no kid) is a session/externally-issued token and is delegated
// unchanged to the base verifier.
//
// Fail-closed: a token whose kid is present but unrecognized is rejected. A
// forged kid never widens authority because the strict verifier re-verifies
// the signature against the pinned public key for that kid.
type CompositeVerifier struct {
	asVerifiers map[string]*strictASVerifier
	base        plugin.JWTVerifier
}

// NewCompositeVerifier builds a CompositeVerifier. asKeys maps each kid to the
// Ed25519 public key that signed AS tokens under that kid (a map, not a single
// key, so ≥2 kids can be trusted simultaneously during key rotation).
// expectedAudience is the canonical audience AS access tokens must be bound to.
func NewCompositeVerifier(base plugin.JWTVerifier, asKeys map[string]ed25519.PublicKey, expectedAudience string) *CompositeVerifier {
	verifiers := make(map[string]*strictASVerifier, len(asKeys))
	for kid, pub := range asKeys {
		verifiers[kid] = &strictASVerifier{
			publicKey:        pub,
			expectedAudience: expectedAudience,
		}
	}
	return &CompositeVerifier{asVerifiers: verifiers, base: base}
}

// Verify implements plugin.JWTVerifier. It reads the (unverified) header kid
// only to choose a verifier — the chosen verifier re-verifies the signature,
// so a spoofed header cannot bypass cryptographic validation.
func (v *CompositeVerifier) Verify(tokenStr string) (plugin.Claims, error) {
	kid, err := kidFromHeader(tokenStr)
	if err != nil {
		return plugin.Claims{}, fmt.Errorf("read token header: %w", err)
	}
	if kid == "" {
		return v.base.Verify(tokenStr)
	}
	strict, ok := v.asVerifiers[kid]
	if !ok {
		return plugin.Claims{}, fmt.Errorf("unknown key id %q", kid)
	}
	return strict.Verify(tokenStr)
}

// kidFromHeader base64url-decodes and JSON-parses ONLY the header segment of a
// JWT to extract its `kid`. The result is used solely for routing; it is never
// trusted for authorization. An empty kid means the header omits it.
func kidFromHeader(tokenStr string) (string, error) {
	parts := strings.Split(tokenStr, ".")
	if len(parts) != 3 {
		return "", fmt.Errorf("malformed token")
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", fmt.Errorf("decode header: %w", err)
	}
	var header struct {
		Kid string `json:"kid"`
	}
	if err := json.Unmarshal(raw, &header); err != nil {
		return "", fmt.Errorf("parse header json: %w", err)
	}
	return header.Kid, nil
}

// strictASVerifier verifies an authorization-server-issued access token under
// the narrowest possible rules. It NEVER returns plugin.ScopeAdmin and has no
// no-audience fallback — an AS token that fails any check is rejected outright.
type strictASVerifier struct {
	publicKey        ed25519.PublicKey
	expectedAudience string
}

// Verify parses and verifies an AS access token. Requirements (all mandatory):
//   - EdDSA signature, exp present.
//   - aud present and containing expectedAudience; if expectedAudience is empty
//     the token is rejected (fail closed).
//   - scope present and drawn ONLY from {provision, manage}; any other name
//     (including admin) is rejected.
//   - no typ claim (belt-and-suspenders: AS access tokens are untagged, only
//     the stateless blobs carry typ).
func (v *strictASVerifier) Verify(tokenStr string) (plugin.Claims, error) {
	token, err := jwt.Parse(tokenStr, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodEd25519); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return v.publicKey, nil
	}, jwt.WithExpirationRequired())
	if err != nil {
		return plugin.Claims{}, fmt.Errorf("verify as token: %w", err)
	}

	mc, ok := token.Claims.(jwt.MapClaims)
	if !ok || !token.Valid {
		return plugin.Claims{}, fmt.Errorf("invalid as token claims")
	}

	if _, hasTyp := mc["typ"]; hasTyp {
		return plugin.Claims{}, fmt.Errorf("as access token must not carry a typ claim")
	}

	if v.expectedAudience == "" {
		return plugin.Claims{}, fmt.Errorf("as token rejected: no expected audience configured")
	}
	aud, err := mc.GetAudience()
	if err != nil || len(aud) == 0 {
		return plugin.Claims{}, fmt.Errorf("as token has no aud claim")
	}
	if !slices.Contains(aud, v.expectedAudience) {
		return plugin.Claims{}, fmt.Errorf("as token rejected: audience mismatch")
	}

	scopes, err := v.resolveScopes(mc)
	if err != nil {
		return plugin.Claims{}, err
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
		Scopes:   scopes,
	}, nil
}

// resolveScopes converts the AS scope claim to a bitmask using the narrow
// {provision, manage} whitelist. An empty scope claim or any unlisted name is
// an error — never widened, never defaulted to admin.
func (v *strictASVerifier) resolveScopes(mc jwt.MapClaims) (int, error) {
	rawScope, hasScope := mc["scope"]
	if !hasScope || rawScope == nil {
		return 0, fmt.Errorf("as token has no scope claim")
	}
	scopeStr, ok := rawScope.(string)
	if !ok {
		return 0, fmt.Errorf("scope claim has unexpected type: %T", rawScope)
	}
	names := strings.Fields(scopeStr)
	if len(names) == 0 {
		return 0, fmt.Errorf("as token has empty scope claim")
	}
	mask := 0
	for _, name := range names {
		bit, ok := asScopeNameToMask[name]
		if !ok {
			return 0, fmt.Errorf("as token has disallowed scope %q", name)
		}
		mask |= bit
	}
	return mask, nil
}
