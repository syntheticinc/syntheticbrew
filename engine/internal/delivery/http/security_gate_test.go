// Package http_test contains black-box tests that treat the delivery/http
// package as an opaque boundary. These tests exercise the SCC-01 security
// gate: every tenant-data API must return 401 when the caller omits the
// Authorization header.
//
// The CE engine mounts dozens of routes behind AuthMiddleware.Authenticate;
// this suite verifies the middleware itself rejects unauthenticated traffic
// for the three endpoints most commonly used in multi-tenancy acceptance
// testing (TC-AUTH-04 / SCC-01). It is intentionally narrow — deeper authz
// is covered by RequireScope tests in the white-box auth_middleware_test.go.
package http_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	eehttp "github.com/syntheticinc/syntheticbrew/internal/delivery/http"
	"github.com/syntheticinc/syntheticbrew/pkg/plugin"
)

// noTokenVerifier always fails token lookup. The security-gate suite never
// supplies a token, so VerifyToken should not be reached — this stub exists
// only to satisfy the AuthMiddleware dependency.
type noTokenVerifier struct{}

func (n *noTokenVerifier) VerifyToken(_ context.Context, _ string) (eehttp.APITokenInfo, error) {
	return eehttp.APITokenInfo{}, errors.New("token lookup not expected in security-gate test")
}

// rejectingVerifier is a plugin.JWTVerifier that rejects every token. The
// security-gate suite never sends an Authorization header, so the middleware
// rejects the request before calling Verify. This stub is plumbed only to
// satisfy the constructor signature — reaching it means the gate failed.
type rejectingVerifier struct{}

func (rejectingVerifier) Verify(string) (plugin.Claims, error) {
	return plugin.Claims{}, errors.New("rejecting verifier: gate failure")
}

func newRejectingVerifier() plugin.JWTVerifier { return rejectingVerifier{} }

// TestSecurityGate_NoAuthorization asserts SCC-01: any call without an
// Authorization header to a protected endpoint returns 401.
//
// We wire the real AuthMiddleware in front of a sink handler that would
// return 200 if reached. The middleware must short-circuit with 401 before
// the sink runs — we assert both the status code and that the sink was not
// invoked.
func TestSecurityGate_NoAuthorization(t *testing.T) {
	cases := []struct {
		name   string
		method string
		path   string
		body   io.Reader
	}{
		{
			name:   "GET_agents",
			method: nethttp.MethodGet,
			path:   "/api/v1/agents",
			body:   nil,
		},
		{
			name:   "GET_schemas",
			method: nethttp.MethodGet,
			path:   "/api/v1/schemas",
			body:   nil,
		},
		{
			name:   "POST_schemas",
			method: nethttp.MethodPost,
			path:   "/api/v1/schemas",
			body:   bytes.NewReader([]byte(`{"name":"attempt"}`)),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mw := eehttp.NewAuthMiddlewareWithVerifier(
				newRejectingVerifier(),
				&noTokenVerifier{},
			)

			reached := false
			sink := nethttp.HandlerFunc(func(w nethttp.ResponseWriter, _ *nethttp.Request) {
				reached = true
				w.WriteHeader(nethttp.StatusOK)
			})

			server := httptest.NewServer(mw.Authenticate(sink))
			defer server.Close()

			req, err := nethttp.NewRequest(tc.method, server.URL+tc.path, tc.body)
			require.NoError(t, err)
			if tc.body != nil {
				req.Header.Set("Content-Type", "application/json")
			}
			// Explicitly: no Authorization header set.

			resp, err := nethttp.DefaultClient.Do(req)
			require.NoError(t, err)
			defer func() { _ = resp.Body.Close() }()

			assert.Equal(t, nethttp.StatusUnauthorized, resp.StatusCode,
				"SCC-01 gate: %s %s without Authorization header must return 401",
				tc.method, tc.path,
			)
			assert.False(t, reached,
				"SCC-01 gate: protected handler must never execute for unauthenticated requests",
			)

			bodyBytes, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			assert.True(t, strings.Contains(string(bodyBytes), "unauthorized"),
				"SCC-01 gate: body should surface an 'unauthorized' marker; got %q",
				string(bodyBytes),
			)
		})
	}
}

// TestSecurityGate_BearerPrefixRequired verifies that a malformed
// Authorization header (missing the Bearer prefix) also yields 401 — the
// middleware must not accept alternative schemes.
func TestSecurityGate_BearerPrefixRequired(t *testing.T) {
	mw := eehttp.NewAuthMiddlewareWithVerifier(
		newRejectingVerifier(),
		&noTokenVerifier{},
	)

	sink := nethttp.HandlerFunc(func(w nethttp.ResponseWriter, _ *nethttp.Request) {
		w.WriteHeader(nethttp.StatusOK)
	})
	server := httptest.NewServer(mw.Authenticate(sink))
	defer server.Close()

	req, err := nethttp.NewRequest(nethttp.MethodGet, server.URL+"/api/v1/agents", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Basic dXNlcjpwYXNz")

	resp, err := nethttp.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, nethttp.StatusUnauthorized, resp.StatusCode,
		"SCC-01 gate: non-Bearer auth schemes must be rejected",
	)
}
