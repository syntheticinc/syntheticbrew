package auth

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/syntheticinc/syntheticbrew/internal/authprim"
)

// MCPAccessTokenTTL is the lifetime of authorization-server access tokens
// minted for the MCP client flow.
const MCPAccessTokenTTL = 30 * time.Minute

// OAuthTokenSigner mints OAuth 2.1 credentials for the MCP client flow:
// audience-bound access tokens plus the typ-tagged blobs used as stateless
// authorization codes and client identifiers.
//
// It signs with a dedicated Ed25519 authorization-server keypair. Every token
// it produces carries a JWT header `kid` derived from the public key so a
// verifier can route the token to the strict AS verifier (and support key
// rotation by keying multiple public keys by kid).
type OAuthTokenSigner struct {
	privateKey ed25519.PrivateKey
	publicKey  ed25519.PublicKey
	issuer     string
	kid        string
}

// NewOAuthTokenSigner creates an OAuthTokenSigner. privateKey must be a full
// 64-byte ed25519.PrivateKey; issuer is the authorization-server base URL.
// kid is computed as hex(sha256(publicKey))[:16] and stamped into every token
// header this signer produces.
func NewOAuthTokenSigner(priv ed25519.PrivateKey, issuer string) *OAuthTokenSigner {
	pub, _ := priv.Public().(ed25519.PublicKey)
	return &OAuthTokenSigner{
		privateKey: priv,
		publicKey:  pub,
		issuer:     issuer,
		kid:        KeyID(pub),
	}
}

// KeyID derives the stable key identifier advertised in JWT headers and JWKS
// for an Ed25519 public key: hex(sha256(pub))[:16].
func KeyID(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:])[:16]
}

// SignMCPAccessToken creates the audience-bound access token handed to MCP
// clients. It carries aud + scope and NO role or typ claim; the header kid
// routes it to the strict AS verifier on the engine side.
func (s *OAuthTokenSigner) SignMCPAccessToken(sub, tenantID string, scopes []string, audience string) (string, error) {
	now := time.Now()
	claims := jwt.MapClaims{
		"iss":       s.issuer,
		"sub":       sub,
		"tenant_id": tenantID,
		"scope":     strings.Join(scopes, " "),
		"aud":       audience,
		"exp":       now.Add(MCPAccessTokenTTL).Unix(),
		"iat":       now.Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	token.Header["kid"] = s.kid
	return token.SignedString(s.privateKey)
}

// SignOAuthBlob signs a typ-tagged JWT used as a self-encoded OAuth artifact.
// It sets typ and iat; exp is added only when ttl != 0 (client_id blobs are
// unexpiring, authorization codes are short-lived). The header kid is stamped
// so the blob is verified with the AS keypair.
func (s *OAuthTokenSigner) SignOAuthBlob(typ string, claims map[string]any, ttl time.Duration) (string, error) {
	if typ == "" {
		return "", fmt.Errorf("oauth blob typ is required")
	}
	now := time.Now()
	mc := jwt.MapClaims{}
	for k, v := range claims {
		mc[k] = v
	}
	mc["typ"] = typ
	mc["iat"] = now.Unix()
	if ttl != 0 {
		mc["exp"] = now.Add(ttl).Unix()
	}
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, mc)
	token.Header["kid"] = s.kid
	return token.SignedString(s.privateKey)
}

// VerifyOAuthBlob validates an EdDSA-signed typ-tagged blob and returns its
// claims. A blob whose typ does not equal wantTyp is rejected, so a client_id
// can never be replayed as an authorization code (or vice versa). exp, when
// present, is enforced by the parser.
func (s *OAuthTokenSigner) VerifyOAuthBlob(tokenString, wantTyp string) (map[string]any, error) {
	token, err := jwt.ParseWithClaims(tokenString, jwt.MapClaims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodEd25519); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return s.publicKey, nil
	})
	if err != nil {
		return nil, fmt.Errorf("parse oauth blob: %w", err)
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("invalid oauth blob claims")
	}
	typ, _ := claims["typ"].(string)
	if typ != wantTyp {
		return nil, fmt.Errorf("oauth blob typ mismatch: got %q, want %q", typ, wantTyp)
	}
	return claims, nil
}

// NewOpaqueRefreshToken mints an opaque OAuth refresh token: "rt_" plus
// 32 random bytes base64url-encoded. Returns the token and its SHA-256 hex
// hash (the only form ever persisted).
func (s *OAuthTokenSigner) NewOpaqueRefreshToken() (token, hash string, err error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", "", fmt.Errorf("generate refresh token: %w", err)
	}
	token = "rt_" + base64.RawURLEncoding.EncodeToString(raw)
	return token, authprim.Hash(token), nil
}

// KID returns the key identifier stamped into every token this signer produces.
func (s *OAuthTokenSigner) KID() string { return s.kid }

// PublicKey returns the Ed25519 public half of the signing key.
func (s *OAuthTokenSigner) PublicKey() ed25519.PublicKey { return s.publicKey }
