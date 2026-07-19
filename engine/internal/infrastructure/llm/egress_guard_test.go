package llm

import (
	"errors"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syntheticinc/syntheticbrew/pkg/plugin"
)

// TestGuardedBaseTransport_Proxy is B2: on an enforcing (deny-private) policy
// the transport must zero Proxy — a proxied CONNECT would make the Control
// check see the proxy address instead of the real target (a full bypass). On
// the permissive CE path the proxy must be PRESERVED: the Control check is a
// no-op there, and a self-hosted operator may reach the internet only via an
// egress proxy.
func TestGuardedBaseTransport_Proxy(t *testing.T) {
	denyPriv := guardedBaseTransport(plugin.DenyPrivateEgressPolicy{})
	require.NotNil(t, denyPriv)
	assert.Nil(t, denyPriv.Proxy, "deny-private transport must not honour HTTP(S)_PROXY (SSRF bypass)")
	assert.NotNil(t, denyPriv.DialContext, "guarded transport must install the Control dialer")

	// The untrusted composition also enforces — proxy zeroed.
	untrusted := guardedBaseTransport(untrustedEgress(plugin.PermissiveEgressPolicy{}))
	assert.Nil(t, untrusted.Proxy, "untrusted path composes deny-private — proxy must be zeroed")

	// Permissive operator path preserves the env proxy.
	permissive := guardedBaseTransport(plugin.PermissiveEgressPolicy{})
	assert.NotNil(t, permissive.Proxy, "permissive CE path must preserve HTTP(S)_PROXY")
	assert.NotNil(t, permissive.DialContext)
}

// TestEgressRedirectGuard is B3: the redirect handler caps hop count, rejects
// an https→http downgrade, and re-validates each hop URL (so a 3xx cannot
// bounce to a metadata host the first hop passed).
func TestEgressRedirectGuard(t *testing.T) {
	guard := egressRedirectGuard(untrustedEgress(plugin.PermissiveEgressPolicy{}))

	mustReq := func(raw string) *http.Request {
		u, err := url.Parse(raw)
		require.NoError(t, err)
		return &http.Request{URL: u}
	}

	// Hop cap.
	via := make([]*http.Request, maxEgressRedirects)
	assert.ErrorIs(t, guard(mustReq("https://ok.example.com/x"), via), errEgressBlocked, "hop cap")

	// https → http downgrade.
	down := []*http.Request{mustReq("https://ok.example.com/a")}
	assert.ErrorIs(t, guard(mustReq("http://ok.example.com/b"), down), errEgressBlocked, "downgrade")

	// Redirect to a metadata host.
	one := []*http.Request{mustReq("https://ok.example.com/a")}
	assert.ErrorIs(t, guard(mustReq("http://169.254.169.254/x"), one), errEgressBlocked, "metadata hop")

	// A legitimate same-scheme hop to a public host is allowed.
	assert.NoError(t, guard(mustReq("https://api.openai.com/v1"), one))
}

// TestNormalizeEgressError is N1: an egress rejection maps to a single opaque,
// address-free message regardless of target — no scan oracle. Other errors are
// left untouched so real model-connectivity debugging keeps its detail.
func TestNormalizeEgressError(t *testing.T) {
	// Wrapping preserves identification.
	wrapped := errors.New("dial tcp 10.0.0.5:22: " + errEgressBlocked.Error())
	_ = wrapped
	msgA, okA := normalizeEgressError(errEgressBlocked)
	require.True(t, okA)
	assert.Equal(t, errEgressBlocked.Error(), msgA)
	assert.NotContains(t, msgA, "10.0.0", "opaque message must not leak an address")

	// A url.Error wrapping the sentinel (the shape http.Client returns).
	urlErr := &url.Error{Op: "Get", URL: "http://10.0.0.5:22", Err: errEgressBlocked}
	msgB, okB := normalizeEgressError(urlErr)
	require.True(t, okB)
	assert.Equal(t, errEgressBlocked.Error(), msgB)

	// Two different private targets produce the SAME opaque message.
	other := &url.Error{Op: "Get", URL: "http://192.168.1.9:3306", Err: errEgressBlocked}
	msgC, _ := normalizeEgressError(other)
	assert.Equal(t, msgB, msgC, "different targets must yield identical error text")

	// A non-egress error is passed through untouched.
	_, ok := normalizeEgressError(errors.New("model returned 500"))
	assert.False(t, ok)
}

// TestUntrustedEgress_BaselineNonRelaxable proves a permissive injected policy
// cannot relax the deny-private baseline on the untrusted path.
func TestUntrustedEgress_BaselineNonRelaxable(t *testing.T) {
	eff := untrustedEgress(plugin.PermissiveEgressPolicy{})
	assert.ErrorIs(t, eff.CheckAddr("tcp", "127.0.0.1:80"), errEgressBlocked)
	assert.ErrorIs(t, eff.CheckURL("http://169.254.169.254/x"), errEgressBlocked)

	// The operator path, by contrast, honours the permissive policy (loopback
	// allowed — a self-hosted operator may target internal hosts).
	op := operatorEgress(plugin.PermissiveEgressPolicy{})
	assert.NoError(t, op.CheckAddr("tcp", "127.0.0.1:80"))
}
