// Package oauthtoken implements the OAuth 2.1 token endpoint: PKCE-verified
// authorization_code exchange and rotating refresh_token grants, both issuing
// audience-bound MCP access tokens.
//
// Two defences are enforced against grant reuse:
//   - authorization codes are single-use via a persisted per-code uniqueness
//     constraint (a second exchange collides and revokes the token family);
//   - refresh tokens rotate atomically (the revoke is a conditional single-row
//     update; losing the race is treated as reuse and revokes the family).
package oauthtoken

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/syntheticinc/syntheticbrew/internal/authprim"
	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

const (
	// AccessTokenTTLSeconds is the expires_in value returned to clients.
	AccessTokenTTLSeconds = 1800
	// RefreshTokenTTL is the lifetime of a refresh token row.
	RefreshTokenTTL = 30 * 24 * time.Hour
)

// BlobVerifier verifies typ-tagged OAuth JWTs.
type BlobVerifier interface {
	VerifyOAuthBlob(tokenString, wantTyp string) (map[string]any, error)
}

// AccessTokenSigner mints audience-bound MCP access tokens.
type AccessTokenSigner interface {
	SignMCPAccessToken(sub, tenantID string, scopes []string, audience string) (string, error)
}

// RefreshTokenMinter mints opaque refresh tokens and their storage hashes.
type RefreshTokenMinter interface {
	NewOpaqueRefreshToken() (token, hash string, err error)
}

// RefreshTokenRepository manages persisted refresh token lifecycle. Store
// returns domain.ErrOAuthCodeReplayed (via errors.Is) when the record's
// authorization-code JTI collides — a code replay. RotateRevoke revokes a
// single token atomically and reports the affected row count (0 = already
// rotated, i.e. reuse).
type RefreshTokenRepository interface {
	Store(ctx context.Context, t domain.OAuthRefreshToken) error
	GetByHash(ctx context.Context, hash string) (domain.OAuthRefreshToken, error)
	RotateRevoke(ctx context.Context, hash string) (int64, error)
	RevokeFamily(ctx context.Context, familyID string) error
	FindFamilyByCodeJTI(ctx context.Context, codeJTI string) (string, error)
}

// ConnectNotifier reports a successful MCP client connection (authorization-code
// exchange) for the activation funnel. Implementations must return immediately
// and be best-effort: token issuance never waits on or fails because of it.
type ConnectNotifier interface {
	OnMCPConnected(ctx context.Context, tenantID string)
}

// Input is the token endpoint request (form-encoded fields).
type Input struct {
	GrantType    string
	Code         string
	RedirectURI  string
	ClientID     string
	CodeVerifier string
	RefreshToken string
}

// Output is the RFC 6749 token response.
type Output struct {
	AccessToken  string
	RefreshToken string
	Scope        string
	ExpiresIn    int
}

// Usecase exchanges authorization codes and refresh tokens for access tokens.
type Usecase struct {
	verifier    BlobVerifier
	tokenSigner AccessTokenSigner
	minter      RefreshTokenMinter
	tokenRepo   RefreshTokenRepository
	// connectNotifier is optional (nil = analytics disabled).
	connectNotifier ConnectNotifier
	// resource is the canonical MCP resource URI (aud of every access token).
	resource string
}

// New creates a new OAuthToken usecase. resource is the canonical MCP resource
// URI used as the audience of every issued access token. connectNotifier may
// be nil.
func New(
	verifier BlobVerifier,
	tokenSigner AccessTokenSigner,
	minter RefreshTokenMinter,
	tokenRepo RefreshTokenRepository,
	connectNotifier ConnectNotifier,
	resource string,
) *Usecase {
	return &Usecase{
		verifier:        verifier,
		tokenSigner:     tokenSigner,
		minter:          minter,
		tokenRepo:       tokenRepo,
		connectNotifier: connectNotifier,
		resource:        resource,
	}
}

var errInvalidGrant = domain.NewOAuthError("invalid_grant", "invalid, expired or revoked grant")

// Execute dispatches on grant_type.
func (u *Usecase) Execute(ctx context.Context, input Input) (*Output, error) {
	if input.ClientID == "" {
		return nil, domain.NewOAuthError("invalid_request", "client_id is required")
	}
	switch input.GrantType {
	case "authorization_code":
		return u.exchangeCode(ctx, input)
	case "refresh_token":
		return u.rotateRefreshToken(ctx, input)
	default:
		return nil, domain.NewOAuthError("unsupported_grant_type", "grant_type must be authorization_code or refresh_token")
	}
}

// exchangeCode validates an authorization code (signature, expiry, client
// binding, redirect_uri, PKCE) and issues the first token pair. Single use is
// enforced by the persisted code JTI: a replay collides on Store, which
// revokes the family the first exchange already spawned.
func (u *Usecase) exchangeCode(ctx context.Context, input Input) (*Output, error) {
	if input.Code == "" || input.CodeVerifier == "" || input.RedirectURI == "" {
		return nil, domain.NewOAuthError("invalid_request", "code, redirect_uri and code_verifier are required")
	}

	claims, err := u.verifier.VerifyOAuthBlob(input.Code, "oauth_code")
	if err != nil {
		return nil, errInvalidGrant
	}
	code, err := codeClaimsFromMap(claims)
	if err != nil {
		return nil, errInvalidGrant
	}

	if subtle.ConstantTimeCompare([]byte(code.CidHash), []byte(hashClientID(input.ClientID))) != 1 {
		return nil, errInvalidGrant
	}
	if !domain.MatchRedirectURI(code.RedirectURI, input.RedirectURI) {
		return nil, errInvalidGrant
	}
	if !verifyPKCE(input.CodeVerifier, code.CodeChallenge) {
		return nil, errInvalidGrant
	}

	scopes := domain.ParseScope(code.Scope)
	accessToken, err := u.tokenSigner.SignMCPAccessToken(code.Sub, code.TenantID, scopes, u.resource)
	if err != nil {
		return nil, domain.NewOAuthError("server_error", "failed to sign access token")
	}

	refreshToken, err := u.issueRefreshToken(ctx, domain.OAuthRefreshToken{
		TenantID: code.TenantID,
		UserSub:  code.Sub,
		CidHash:  code.CidHash,
		Scope:    code.Scope,
		Resource: u.resource,
		FamilyID: uuid.New().String(),
		CodeJTI:  code.JTI,
	})
	if errors.Is(err, domain.ErrOAuthCodeReplayed) {
		// Second exchange of this code: revoke the family the first exchange
		// spawned and reject as reuse.
		u.revokeFamilyForCode(ctx, code.JTI)
		return nil, errInvalidGrant
	}
	if err != nil {
		return nil, err
	}

	// Activation funnel: a code exchange means a fresh MCP client connection.
	// Refresh rotations are the same client staying connected, so they don't fire.
	if u.connectNotifier != nil && code.TenantID != "" {
		u.connectNotifier.OnMCPConnected(ctx, code.TenantID)
	}

	return &Output{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		Scope:        code.Scope,
		ExpiresIn:    AccessTokenTTLSeconds,
	}, nil
}

// revokeFamilyForCode revokes the refresh-token family spawned by the first
// exchange of an authorization code, located by its jti. Best-effort: a lookup
// miss leaves nothing to revoke and is not surfaced.
func (u *Usecase) revokeFamilyForCode(ctx context.Context, codeJTI string) {
	familyID, err := u.tokenRepo.FindFamilyByCodeJTI(ctx, codeJTI)
	if err != nil {
		return
	}
	_ = u.tokenRepo.RevokeFamily(ctx, familyID)
}

// rotateRefreshToken validates a refresh token, rotates it atomically within
// its family and issues a fresh token pair. Reuse of a revoked token — or
// losing the atomic rotation race — is treated as theft: the entire family is
// revoked.
func (u *Usecase) rotateRefreshToken(ctx context.Context, input Input) (*Output, error) {
	if input.RefreshToken == "" {
		return nil, domain.NewOAuthError("invalid_request", "refresh_token is required")
	}

	stored, err := u.tokenRepo.GetByHash(ctx, authprim.Hash(input.RefreshToken))
	if err != nil {
		return nil, errInvalidGrant
	}
	if stored.RevokedAt != nil {
		// Reuse of a rotated-out token — assume theft, kill the family.
		_ = u.tokenRepo.RevokeFamily(ctx, stored.FamilyID)
		return nil, errInvalidGrant
	}
	if time.Now().After(stored.ExpiresAt) {
		return nil, errInvalidGrant
	}
	if subtle.ConstantTimeCompare([]byte(stored.CidHash), []byte(hashClientID(input.ClientID))) != 1 {
		return nil, errInvalidGrant
	}

	rows, err := u.tokenRepo.RotateRevoke(ctx, stored.TokenHash)
	if err != nil {
		return nil, domain.NewOAuthError("server_error", "failed to rotate refresh token")
	}
	if rows == 0 {
		// A concurrent rotation already revoked this token — the caller is
		// racing a reuse. Kill the family.
		_ = u.tokenRepo.RevokeFamily(ctx, stored.FamilyID)
		return nil, errInvalidGrant
	}

	scopes := domain.ParseScope(stored.Scope)
	accessToken, err := u.tokenSigner.SignMCPAccessToken(stored.UserSub, stored.TenantID, scopes, u.resource)
	if err != nil {
		return nil, domain.NewOAuthError("server_error", "failed to sign access token")
	}

	refreshToken, err := u.issueRefreshToken(ctx, domain.OAuthRefreshToken{
		TenantID: stored.TenantID,
		UserSub:  stored.UserSub,
		CidHash:  stored.CidHash,
		Scope:    stored.Scope,
		Resource: stored.Resource,
		FamilyID: stored.FamilyID,
	})
	if err != nil {
		return nil, err
	}

	return &Output{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		Scope:        stored.Scope,
		ExpiresIn:    AccessTokenTTLSeconds,
	}, nil
}

// issueRefreshToken mints an opaque token and stores its hash. The record
// template carries tenant/user/client/scope/family (and, on first issue, the
// authorization-code JTI); token hash, id and expiry are set here.
func (u *Usecase) issueRefreshToken(ctx context.Context, record domain.OAuthRefreshToken) (string, error) {
	token, hash, err := u.minter.NewOpaqueRefreshToken()
	if err != nil {
		return "", domain.NewOAuthError("server_error", "failed to mint refresh token")
	}
	record.ID = uuid.New().String()
	record.TokenHash = hash
	record.ExpiresAt = time.Now().Add(RefreshTokenTTL)
	if err := u.tokenRepo.Store(ctx, record); err != nil {
		if errors.Is(err, domain.ErrOAuthCodeReplayed) {
			// Surface replay to the caller so it can revoke the family; do not
			// mask it as a generic server_error.
			return "", err
		}
		return "", domain.NewOAuthError("server_error", "failed to store refresh token")
	}
	return token, nil
}

// codeClaims are the fields of a verified authorization-code blob.
type codeClaims struct {
	CidHash       string
	RedirectURI   string
	Scope         string
	Sub           string
	TenantID      string
	CodeChallenge string
	JTI           string
	ExpiresAt     time.Time
}

// codeClaimsFromMap extracts and sanity-checks the authorization-code claims.
// tenant_id is optional (empty in single-tenant deployments); every other
// field is required.
func codeClaimsFromMap(claims map[string]any) (*codeClaims, error) {
	c := &codeClaims{}
	var ok bool
	if c.CidHash, ok = claims["cid_hash"].(string); !ok || c.CidHash == "" {
		return nil, errInvalidGrant
	}
	if c.RedirectURI, ok = claims["redirect_uri"].(string); !ok || c.RedirectURI == "" {
		return nil, errInvalidGrant
	}
	if c.Scope, ok = claims["scope"].(string); !ok || c.Scope == "" {
		return nil, errInvalidGrant
	}
	if c.Sub, ok = claims["sub"].(string); !ok || c.Sub == "" {
		return nil, errInvalidGrant
	}
	// tenant_id may be absent or empty; accept whatever string is present.
	c.TenantID, _ = claims["tenant_id"].(string)
	if c.CodeChallenge, ok = claims["code_challenge"].(string); !ok || c.CodeChallenge == "" {
		return nil, errInvalidGrant
	}
	if c.JTI, ok = claims["jti"].(string); !ok || c.JTI == "" {
		return nil, errInvalidGrant
	}
	exp, ok := claims["exp"].(float64)
	if !ok {
		return nil, errInvalidGrant
	}
	c.ExpiresAt = time.Unix(int64(exp), 0)
	return c, nil
}

// verifyPKCE checks S256: base64url(sha256(code_verifier)) == code_challenge.
func verifyPKCE(verifier, challenge string) bool {
	sum := sha256.Sum256([]byte(verifier))
	computed := base64.RawURLEncoding.EncodeToString(sum[:])
	return subtle.ConstantTimeCompare([]byte(computed), []byte(challenge)) == 1
}

// hashClientID returns the sha256 hex of a client_id string (cid_hash claim).
func hashClientID(clientID string) string {
	sum := sha256.Sum256([]byte(clientID))
	return hex.EncodeToString(sum[:])
}
