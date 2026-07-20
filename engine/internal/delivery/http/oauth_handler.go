package http

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/usecase/oauthapprove"
	"github.com/syntheticinc/syntheticbrew/internal/usecase/oauthauthorizeinfo"
	"github.com/syntheticinc/syntheticbrew/internal/usecase/oauthregister"
	"github.com/syntheticinc/syntheticbrew/internal/usecase/oauthtoken"
)

type oauthRegisterUsecase interface {
	Execute(input oauthregister.Input) (*oauthregister.Output, error)
}

type oauthAuthorizeInfoUsecase interface {
	Execute(input oauthauthorizeinfo.Input) (*oauthauthorizeinfo.Output, error)
}

type oauthApproveUsecase interface {
	Execute(input oauthapprove.Input) (*oauthapprove.Output, error)
}

type oauthTokenUsecase interface {
	Execute(ctx context.Context, input oauthtoken.Input) (*oauthtoken.Output, error)
}

// OAuthHandler serves the OAuth 2.1 authorization server endpoints for the MCP
// client flow. Responses use raw RFC 6749/7591/8414 JSON shapes, not the API
// envelope — external OAuth clients parse them directly.
type OAuthHandler struct {
	metadata        map[string]any
	registerUC      oauthRegisterUsecase
	authorizeInfoUC oauthAuthorizeInfoUsecase
	approveUC       oauthApproveUsecase
	tokenUC         oauthTokenUsecase
}

// NewOAuthHandler creates a new OAuthHandler. issuer is the authorization-server
// base URL; authorizePageURL is the consent page advertised as the
// authorization_endpoint.
func NewOAuthHandler(
	issuer, authorizePageURL string,
	registerUC oauthRegisterUsecase,
	authorizeInfoUC oauthAuthorizeInfoUsecase,
	approveUC oauthApproveUsecase,
	tokenUC oauthTokenUsecase,
) *OAuthHandler {
	base := strings.TrimSuffix(issuer, "/")
	return &OAuthHandler{
		metadata: map[string]any{
			"issuer":                                base,
			"authorization_endpoint":                authorizePageURL,
			"token_endpoint":                        base + "/oauth/token",
			"registration_endpoint":                 base + "/oauth/register",
			"response_types_supported":              []string{"code"},
			"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
			"code_challenge_methods_supported":      []string{"S256"},
			"token_endpoint_auth_methods_supported": []string{"none"},
			"scopes_supported":                      domain.SupportedOAuthScopes(),
		},
		registerUC:      registerUC,
		authorizeInfoUC: authorizeInfoUC,
		approveUC:       approveUC,
		tokenUC:         tokenUC,
	}
}

// Metadata handles GET /.well-known/oauth-authorization-server (RFC 8414).
func (h *OAuthHandler) Metadata(w http.ResponseWriter, _ *http.Request) {
	writeOAuthJSON(w, http.StatusOK, h.metadata)
}

type oauthRegisterRequest struct {
	RedirectURIs []string `json:"redirect_uris"`
	ClientName   string   `json:"client_name"`
}

type oauthRegisterResponse struct {
	ClientID                string   `json:"client_id"`
	ClientIDIssuedAt        int64    `json:"client_id_issued_at"`
	RedirectURIs            []string `json:"redirect_uris"`
	ClientName              string   `json:"client_name,omitempty"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
}

// Register handles POST /oauth/register (RFC 7591 dynamic client registration).
func (h *OAuthHandler) Register(w http.ResponseWriter, r *http.Request) {
	var req oauthRegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_client_metadata", "invalid request body")
		return
	}

	out, err := h.registerUC.Execute(oauthregister.Input{
		RedirectURIs: req.RedirectURIs,
		ClientName:   req.ClientName,
	})
	if err != nil {
		writeOAuthUsecaseError(r.Context(), w, err)
		return
	}

	writeOAuthJSON(w, http.StatusCreated, oauthRegisterResponse{
		ClientID:                out.ClientID,
		ClientIDIssuedAt:        out.ClientIDIssued.Unix(),
		RedirectURIs:            out.RedirectURIs,
		ClientName:              out.ClientName,
		TokenEndpointAuthMethod: "none",
		GrantTypes:              []string{"authorization_code", "refresh_token"},
		ResponseTypes:           []string{"code"},
	})
}

type oauthAuthorizeInfoResponse struct {
	ClientName       string   `json:"client_name"`
	Scopes           []string `json:"scopes"`
	RedirectURIValid bool     `json:"redirect_uri_valid"`
	ConsentNonce     string   `json:"consent_nonce,omitempty"`
}

// AuthorizeInfo handles GET /api/v1/oauth/authorize-info — the consent page
// calls it to validate the authorization request and render client + scopes.
// When the request carries an authenticated session subject it also returns an
// anti-CSRF consent nonce bound to that subject.
func (h *OAuthHandler) AuthorizeInfo(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	out, err := h.authorizeInfoUC.Execute(oauthauthorizeinfo.Input{
		ClientID:            q.Get("client_id"),
		RedirectURI:         q.Get("redirect_uri"),
		Scope:               q.Get("scope"),
		CodeChallenge:       q.Get("code_challenge"),
		CodeChallengeMethod: q.Get("code_challenge_method"),
		SessionSub:          domain.UserSubFromContext(r.Context()),
	})
	if err != nil {
		writeOAuthUsecaseError(r.Context(), w, err)
		return
	}

	writeOAuthJSON(w, http.StatusOK, oauthAuthorizeInfoResponse{
		ClientName:       out.ClientName,
		Scopes:           out.Scopes,
		RedirectURIValid: out.RedirectURIValid,
		ConsentNonce:     out.ConsentNonce,
	})
}

type oauthApproveRequest struct {
	ConsentNonce        string   `json:"consent_nonce"`
	ClientID            string   `json:"client_id"`
	RedirectURI         string   `json:"redirect_uri"`
	Scope               string   `json:"scope"`
	State               string   `json:"state"`
	CodeChallenge       string   `json:"code_challenge"`
	CodeChallengeMethod string   `json:"code_challenge_method"`
	Resource            string   `json:"resource"`
	ApprovedScopes      []string `json:"approved_scopes"`
	Deny                bool     `json:"deny"`
}

// Approve handles POST /api/v1/oauth/approve (behind auth) — converts the
// user's consent decision into an authorization-code redirect URL.
func (h *OAuthHandler) Approve(w http.ResponseWriter, r *http.Request) {
	var req oauthApproveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "invalid request body")
		return
	}

	out, err := h.approveUC.Execute(oauthapprove.Input{
		UserID:              domain.UserSubFromContext(r.Context()),
		TenantID:            domain.TenantIDFromContext(r.Context()),
		ConsentNonce:        req.ConsentNonce,
		ClientID:            req.ClientID,
		RedirectURI:         req.RedirectURI,
		Scope:               req.Scope,
		State:               req.State,
		CodeChallenge:       req.CodeChallenge,
		CodeChallengeMethod: req.CodeChallengeMethod,
		Resource:            req.Resource,
		ApprovedScopes:      req.ApprovedScopes,
		Deny:                req.Deny,
	})
	if err != nil {
		writeOAuthUsecaseError(r.Context(), w, err)
		return
	}

	writeOAuthJSON(w, http.StatusOK, map[string]string{"redirect_url": out.RedirectURL})
}

type oauthTokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
}

// Token handles POST /oauth/token (form-encoded, RFC 6749).
func (h *OAuthHandler) Token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "invalid form body")
		return
	}

	out, err := h.tokenUC.Execute(r.Context(), oauthtoken.Input{
		GrantType:    r.PostFormValue("grant_type"),
		Code:         r.PostFormValue("code"),
		RedirectURI:  r.PostFormValue("redirect_uri"),
		ClientID:     r.PostFormValue("client_id"),
		CodeVerifier: r.PostFormValue("code_verifier"),
		RefreshToken: r.PostFormValue("refresh_token"),
	})
	if err != nil {
		writeOAuthUsecaseError(r.Context(), w, err)
		return
	}

	// RFC 6749 §5.1: token responses must not be cached.
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	writeOAuthJSON(w, http.StatusOK, oauthTokenResponse{
		AccessToken:  out.AccessToken,
		TokenType:    "Bearer",
		ExpiresIn:    out.ExpiresIn,
		RefreshToken: out.RefreshToken,
		Scope:        out.Scope,
	})
}

// writeOAuthJSON writes a raw (non-enveloped) JSON body.
func writeOAuthJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		slog.Error("failed to encode oauth response", "error", err)
	}
}

// writeOAuthUsecaseError maps a usecase error to the RFC 6749 error JSON.
func writeOAuthUsecaseError(ctx context.Context, w http.ResponseWriter, err error) {
	var oerr *domain.OAuthError
	if !errors.As(err, &oerr) {
		slog.ErrorContext(ctx, "oauth internal error", "error", err)
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "internal server error")
		return
	}
	status := http.StatusBadRequest
	if oerr.Code == "server_error" {
		slog.ErrorContext(ctx, "oauth server error", "error", err)
		status = http.StatusInternalServerError
	}
	writeOAuthError(w, status, oerr.Code, oerr.Description)
}

// writeOAuthError writes an RFC 6749 error body.
func writeOAuthError(w http.ResponseWriter, status int, code, description string) {
	writeOAuthJSON(w, status, map[string]string{
		"error":             code,
		"error_description": description,
	})
}
