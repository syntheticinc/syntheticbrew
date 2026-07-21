// Package oauthauthorizeinfo validates an authorization request so the consent
// page can render client name + requested scopes before the user approves.
package oauthauthorizeinfo

import (
	"time"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

// ConsentNonceTTL bounds how long a rendered consent page stays valid for a
// subsequent approve. It is the lifetime of the anti-CSRF consent nonce.
const ConsentNonceTTL = 10 * time.Minute

// BlobVerifier verifies typ-tagged OAuth JWTs.
type BlobVerifier interface {
	VerifyOAuthBlob(tokenString, wantTyp string) (map[string]any, error)
}

// BlobSigner signs typ-tagged OAuth JWTs.
type BlobSigner interface {
	SignOAuthBlob(typ string, claims map[string]any, ttl time.Duration) (string, error)
}

// Input is the authorization request to validate. SessionSub, when non-empty,
// is the authenticated subject loading the consent page; it triggers minting a
// signed anti-CSRF consent nonce bound to that subject.
type Input struct {
	ClientID            string
	RedirectURI         string
	Scope               string
	CodeChallenge       string
	CodeChallengeMethod string
	SessionSub          string
}

// Output is the validated request info for the consent page.
type Output struct {
	ClientName       string
	Scopes           []string
	RedirectURIValid bool
	// ConsentNonce is a signed anti-CSRF token bound to SessionSub, empty when
	// the request carried no authenticated session subject.
	ConsentNonce string
}

// Usecase validates authorization requests for the consent page.
type Usecase struct {
	verifier BlobVerifier
	signer   BlobSigner
}

// New creates a new OAuthAuthorizeInfo usecase.
func New(verifier BlobVerifier, signer BlobSigner) *Usecase {
	return &Usecase{verifier: verifier, signer: signer}
}

// Execute verifies the client_id signature and validates redirect_uri, PKCE
// method and scope. When Input.SessionSub is set it also mints a consent nonce
// binding the eventual approve to this authenticated consent-page load. All
// failures are RFC 6749 errors (delivery maps them to 400 with the raw OAuth
// error JSON).
func (u *Usecase) Execute(input Input) (*Output, error) {
	claims, err := u.verifier.VerifyOAuthBlob(input.ClientID, "oauth_client")
	if err != nil {
		return nil, domain.NewOAuthError("invalid_client", "invalid client_id")
	}
	client, err := domain.OAuthClientFromClaims(claims)
	if err != nil {
		return nil, domain.NewOAuthError("invalid_client", "invalid client_id metadata")
	}

	scopes, oerr := domain.ValidateAuthorizeRequest(client, input.RedirectURI, input.Scope, input.CodeChallenge, input.CodeChallengeMethod)
	if oerr != nil {
		return nil, oerr
	}

	out := &Output{
		ClientName:       client.Name,
		Scopes:           scopes,
		RedirectURIValid: true,
	}

	if input.SessionSub == "" {
		return out, nil
	}

	nonce, err := u.signer.SignOAuthBlob("oauth_consent", map[string]any{
		"sub": input.SessionSub,
		// Bind the nonce to this exact authorization request so it cannot be
		// replayed to approve a different client, redirect target, or scope.
		"req": domain.ConsentRequestHash(input.ClientID, input.RedirectURI, input.Scope, input.CodeChallenge),
	}, ConsentNonceTTL)
	if err != nil {
		return nil, domain.NewOAuthError("server_error", "failed to issue consent nonce")
	}
	out.ConsentNonce = nonce
	return out, nil
}
