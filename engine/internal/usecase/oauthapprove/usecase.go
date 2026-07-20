// Package oauthapprove turns a user's consent decision into an authorization
// code redirect. The code is a self-encoded Ed25519-signed JWT bound to the
// client (cid_hash), redirect_uri and PKCE challenge.
package oauthapprove

import (
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

// CodeTTL is the lifetime of authorization codes.
const CodeTTL = 60 * time.Second

// BlobVerifier verifies typ-tagged OAuth JWTs.
type BlobVerifier interface {
	VerifyOAuthBlob(tokenString, wantTyp string) (map[string]any, error)
}

// BlobSigner signs typ-tagged OAuth JWTs.
type BlobSigner interface {
	SignOAuthBlob(typ string, claims map[string]any, ttl time.Duration) (string, error)
}

// Input is the consent decision. UserID/TenantID come from the authenticated
// session, everything else from the consent page form. ConsentNonce is the
// anti-CSRF token minted when the consent page was loaded.
type Input struct {
	UserID              string
	TenantID            string
	ConsentNonce        string
	ClientID            string
	RedirectURI         string
	Scope               string
	State               string
	CodeChallenge       string
	CodeChallengeMethod string
	Resource            string
	ApprovedScopes      []string
	Deny                bool
}

// Output carries the redirect URL the consent page sends the browser to.
type Output struct {
	RedirectURL string
}

// Usecase issues authorization codes from consent decisions.
type Usecase struct {
	verifier BlobVerifier
	signer   BlobSigner
}

// New creates a new OAuthApprove usecase.
func New(verifier BlobVerifier, signer BlobSigner) *Usecase {
	return &Usecase{verifier: verifier, signer: signer}
}

// Execute re-validates the authorization request (never trust the consent
// page alone) and the anti-CSRF consent nonce, then either redirects with
// error=access_denied or mints a single-use authorization code.
func (u *Usecase) Execute(input Input) (*Output, error) {
	if err := u.verifyConsentNonce(input); err != nil {
		return nil, err
	}

	claims, err := u.verifier.VerifyOAuthBlob(input.ClientID, "oauth_client")
	if err != nil {
		return nil, domain.NewOAuthError("invalid_client", "invalid client_id")
	}
	client, err := domain.OAuthClientFromClaims(claims)
	if err != nil {
		return nil, domain.NewOAuthError("invalid_client", "invalid client_id metadata")
	}

	requested, oerr := domain.ValidateAuthorizeRequest(client, input.RedirectURI, input.Scope, input.CodeChallenge, input.CodeChallengeMethod)
	if oerr != nil {
		return nil, oerr
	}

	if input.Deny {
		redirectURL, err := buildRedirectURL(input.RedirectURI, map[string]string{
			"error": "access_denied",
			"state": input.State,
		})
		if err != nil {
			return nil, domain.NewOAuthError("invalid_request", "invalid redirect_uri")
		}
		return &Output{RedirectURL: redirectURL}, nil
	}

	granted, oerr := grantedScopes(input.ApprovedScopes, requested)
	if oerr != nil {
		return nil, oerr
	}

	cidHash := sha256.Sum256([]byte(input.ClientID))
	code, err := u.signer.SignOAuthBlob("oauth_code", map[string]any{
		"cid_hash":              hex.EncodeToString(cidHash[:]),
		"redirect_uri":          input.RedirectURI,
		"scope":                 strings.Join(granted, " "),
		"resource":              input.Resource,
		"sub":                   input.UserID,
		"tenant_id":             input.TenantID,
		"code_challenge":        input.CodeChallenge,
		"code_challenge_method": "S256",
		"jti":                   uuid.New().String(),
	}, CodeTTL)
	if err != nil {
		return nil, domain.NewOAuthError("server_error", "failed to issue authorization code")
	}

	redirectURL, err := buildRedirectURL(input.RedirectURI, map[string]string{
		"code":  code,
		"state": input.State,
	})
	if err != nil {
		return nil, domain.NewOAuthError("invalid_request", "invalid redirect_uri")
	}
	return &Output{RedirectURL: redirectURL}, nil
}

// verifyConsentNonce validates the anti-CSRF consent nonce and asserts it was
// minted for the same authenticated subject AND the same authorization request
// now being approved. Binding to the subject defeats cross-site request
// forgery; binding to the request (client_id + redirect_uri + code_challenge)
// stops a nonce from one client's consent page being replayed to approve a
// different client or redirect target.
func (u *Usecase) verifyConsentNonce(input Input) error {
	if input.ConsentNonce == "" {
		return domain.NewOAuthError("invalid_request", "consent session expired or invalid")
	}
	claims, err := u.verifier.VerifyOAuthBlob(input.ConsentNonce, "oauth_consent")
	if err != nil {
		return domain.NewOAuthError("invalid_request", "consent session expired or invalid")
	}
	sub, _ := claims["sub"].(string)
	if sub == "" || sub != input.UserID {
		return domain.NewOAuthError("invalid_request", "consent session expired or invalid")
	}
	req, _ := claims["req"].(string)
	if req == "" || req != domain.ConsentRequestHash(input.ClientID, input.RedirectURI, input.Scope, input.CodeChallenge) {
		return domain.NewOAuthError("invalid_request", "consent nonce does not match this authorization request")
	}
	return nil
}

// grantedScopes derives the granted scope set: approved_scopes must be a
// subset of the requested scopes; provision is always granted; manage only
// when explicitly approved.
func grantedScopes(approved, requested []string) ([]string, *domain.OAuthError) {
	if !domain.ScopeSubset(approved, requested) {
		return nil, domain.NewOAuthError("invalid_scope", "approved_scopes must be a subset of the requested scope")
	}
	granted := []string{domain.OAuthScopeProvision}
	for _, s := range approved {
		if s == domain.OAuthScopeManage {
			granted = append(granted, domain.OAuthScopeManage)
			break
		}
	}
	return granted, nil
}

// buildRedirectURL appends query parameters to the redirect URI, preserving
// any existing query. Empty values (e.g. absent state) are omitted.
func buildRedirectURL(redirectURI string, params map[string]string) (string, error) {
	u, err := url.Parse(redirectURI)
	if err != nil {
		return "", err
	}
	q := u.Query()
	for k, v := range params {
		if v != "" {
			q.Set(k, v)
		}
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}
