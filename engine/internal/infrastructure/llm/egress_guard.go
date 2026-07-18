package llm

import (
	"errors"
	"net"
	"net/http"
	"syscall"
	"time"

	"github.com/syntheticinc/syntheticbrew/pkg/plugin"
)

// egress_guard wraps outbound LLM HTTP clients so a resolved destination is
// validated right before connect. It closes the BYOK SSRF surface: an
// end-user base-URL override (or an operator stored-model base URL in a
// managed deployment) can no longer make the engine dial an internal address.
//
// Two layers apply, in order:
//   - deny-private baseline (plugin.DenyPrivateEgressPolicy) — engine-owned,
//     non-relaxable on the untrusted end-user BYOK path (composed via
//     untrustedEgress). It rejects loopback / private / link-local / CGNAT /
//     unspecified resolved IPs and cloud-metadata hostnames.
//   - injected plugin.EgressPolicy — the deployment policy. Permissive in CE
//     (operator trusted), tightened in managed deployments (deny-private).
//
// The check runs in net.Dialer.Control, which fires AFTER DNS resolution with
// the concrete ip:port — this is what makes it DNS-rebinding safe (a hostname
// that resolves to a private IP is caught even if a string check would pass).

// errEgressBlocked is the opaque sentinel every egress rejection returns
// (re-exported from pkg/plugin so callers may errors.Is against a local name).
var errEgressBlocked = plugin.ErrEgressBlocked

const maxEgressRedirects = 5

// composeEgress runs a then b; the first rejection wins. Used to layer the
// hardcoded deny-private baseline under the injected deployment policy on the
// untrusted end-user path.
type composeEgress struct {
	a, b plugin.EgressPolicy
}

func (c composeEgress) CheckAddr(network, address string) error {
	if err := c.a.CheckAddr(network, address); err != nil {
		return err
	}
	return c.b.CheckAddr(network, address)
}

func (c composeEgress) CheckURL(rawURL string) error {
	if err := c.a.CheckURL(rawURL); err != nil {
		return err
	}
	return c.b.CheckURL(rawURL)
}

// untrustedEgress returns the effective policy for the untrusted end-user BYOK
// path: the hardcoded deny-private baseline plus the injected deployment
// policy (which may only tighten). A nil injected policy collapses to the
// baseline alone.
func untrustedEgress(injected plugin.EgressPolicy) plugin.EgressPolicy {
	if injected == nil {
		return plugin.DenyPrivateEgressPolicy{}
	}
	return composeEgress{a: plugin.DenyPrivateEgressPolicy{}, b: injected}
}

// operatorEgress returns the effective policy for the operator/stored-model
// path: the injected deployment policy alone. CE returns Permissive (operator
// trusted); managed deployments return a tightened policy. A nil policy
// collapses to Permissive so a wiring miss fails open only in CE (managed mode
// boot-asserts a non-nil policy).
func operatorEgress(injected plugin.EgressPolicy) plugin.EgressPolicy {
	if injected == nil {
		return plugin.PermissiveEgressPolicy{}
	}
	return injected
}

// guardedBaseTransport returns the innermost *http.Transport enforcing policy
// at connect time. It clones DefaultTransport. On any enforcing (non-permissive)
// policy it zeroes Proxy: with an HTTP(S)_PROXY set, requests would CONNECT to
// the proxy and the Control check would see the proxy's ip:port, not the real
// target — a full bypass. On the permissive CE path the Control check is a
// no-op, so the proxy buys nothing and must be preserved — a self-hosted
// operator may reach the internet only through an egress proxy. Provider-
// specific request transforms wrap this base, never replace it.
func guardedBaseTransport(policy plugin.EgressPolicy) *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	if _, permissive := policy.(plugin.PermissiveEgressPolicy); !permissive {
		t.Proxy = nil
	}
	dialer := &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
		Control: func(network, address string, _ syscall.RawConn) error {
			return policy.CheckAddr(network, address)
		},
	}
	t.DialContext = dialer.DialContext
	return t
}

// egressURLCheckTransport validates the request URL (scheme, host, metadata
// hostname) before the request is dialled. It is the outermost wrapper so the
// check runs pre-DNS — a metadata hostname is rejected without a lookup, and a
// literal private IP is rejected without a dial. The Control dialer underneath
// still catches a hostname that only resolves to a private IP at connect time.
type egressURLCheckTransport struct {
	base   http.RoundTripper
	policy plugin.EgressPolicy
}

func (t *egressURLCheckTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := t.policy.CheckURL(req.URL.String()); err != nil {
		return nil, err
	}
	return t.base.RoundTrip(req)
}

// newGuardedHTTPClient builds an *http.Client whose transport enforces policy
// at the URL layer (pre-DNS) and at connect time (post-DNS), and whose redirect
// handler re-validates every hop URL, caps hop count, and forbids an https→http
// downgrade. transform optionally wraps the guarded base with a provider-
// specific request transform (e.g. the anthropic version header); pass nil.
func newGuardedHTTPClient(policy plugin.EgressPolicy, transform func(base http.RoundTripper) http.RoundTripper) *http.Client {
	var rt http.RoundTripper = guardedBaseTransport(policy)
	if transform != nil {
		rt = transform(rt)
	}
	rt = &egressURLCheckTransport{base: rt, policy: policy}
	return &http.Client{
		Transport:     rt,
		CheckRedirect: egressRedirectGuard(policy),
	}
}

// egressRedirectGuard validates each redirect hop: it caps the chain, rejects
// an https→http downgrade, and re-runs the URL check so a 3xx cannot bounce
// the request to a metadata hostname or private target the first hop passed.
func egressRedirectGuard(policy plugin.EgressPolicy) func(req *http.Request, via []*http.Request) error {
	return func(req *http.Request, via []*http.Request) error {
		if len(via) >= maxEgressRedirects {
			return errEgressBlocked
		}
		if len(via) > 0 {
			prev := via[len(via)-1]
			if prev.URL.Scheme == "https" && req.URL.Scheme == "http" {
				return errEgressBlocked
			}
		}
		return policy.CheckURL(req.URL.String())
	}
}

// normalizeEgressError maps an error to an opaque, address-free message when it
// stems from an egress rejection, so a caller relaying it (e.g. VerifyModel)
// cannot use the text as a scan oracle. Returns ("", false) for other errors,
// leaving the caller's legitimate error text intact for real model debugging.
func normalizeEgressError(err error) (string, bool) {
	if err == nil {
		return "", false
	}
	if errors.Is(err, errEgressBlocked) {
		return errEgressBlocked.Error(), true
	}
	return "", false
}
