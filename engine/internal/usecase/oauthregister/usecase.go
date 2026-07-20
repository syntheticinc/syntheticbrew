// Package oauthregister implements RFC 7591 dynamic client registration for
// public OAuth clients. Registration is stateless: the client_id is a signed
// JWT carrying the client metadata, so no client table exists.
package oauthregister

import (
	"time"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

// BlobSigner signs typ-tagged OAuth JWTs.
type BlobSigner interface {
	SignOAuthBlob(typ string, claims map[string]any, ttl time.Duration) (string, error)
}

// Input is the client registration request.
type Input struct {
	RedirectURIs []string
	ClientName   string
}

// Output is the registered client metadata.
type Output struct {
	ClientID       string
	ClientIDIssued time.Time
	RedirectURIs   []string
	ClientName     string
}

// Usecase registers public OAuth clients as stateless signed client_ids.
type Usecase struct {
	signer BlobSigner
}

// New creates a new OAuthRegister usecase.
func New(signer BlobSigner) *Usecase {
	return &Usecase{signer: signer}
}

// Execute validates the redirect URIs and mints a signed client_id JWT.
func (u *Usecase) Execute(input Input) (*Output, error) {
	if len(input.RedirectURIs) == 0 {
		return nil, domain.NewOAuthError("invalid_redirect_uri", "at least one redirect_uri is required")
	}
	for _, uri := range input.RedirectURIs {
		if err := domain.ValidateRedirectURI(uri); err != nil {
			return nil, domain.NewOAuthError("invalid_redirect_uri", err.Error())
		}
	}

	clientID, err := u.signer.SignOAuthBlob("oauth_client", map[string]any{
		"client_name":   input.ClientName,
		"redirect_uris": input.RedirectURIs,
	}, 0)
	if err != nil {
		return nil, domain.NewOAuthError("server_error", "failed to issue client_id")
	}

	return &Output{
		ClientID:       clientID,
		ClientIDIssued: time.Now(),
		RedirectURIs:   input.RedirectURIs,
		ClientName:     input.ClientName,
	}, nil
}
