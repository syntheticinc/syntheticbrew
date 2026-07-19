package plugin

import (
	"bytes"
	"errors"
	"net"
	"net/url"
	"strings"
)

// EgressPolicy decides which outbound LLM destinations a deployment permits.
// It governs the operator/stored-model egress path (models configured by the
// deployment operator). The untrusted end-user BYOK header path always applies
// an engine-owned deny-private baseline on top of this policy — a plugin may
// only tighten, never relax, that baseline.
//
// CE / bare-metal deployments return PermissiveEgressPolicy (all destinations
// allowed) so a self-hosted operator can point a stored model at an internal
// host (e.g. a LAN ollama or vLLM). Managed / multi-tenant deployments return
// DenyPrivateEgressPolicy (private ranges + metadata hosts blocked), because in
// those deployments the operator is not trusted with the engine host's network.
type EgressPolicy interface {
	// CheckAddr validates a resolved ip:port right before connect. It is wired
	// into net.Dialer.Control, so it sees the address AFTER DNS resolution —
	// the defence against DNS-rebinding. Returns nil to allow.
	CheckAddr(network, address string) error

	// CheckURL validates a target URL's scheme and host before dial (and on
	// every redirect hop). It rejects non-http(s) schemes and cloud-metadata
	// hostnames that a post-DNS ip:port check cannot see. Returns nil to allow.
	CheckURL(rawURL string) error
}

// ErrEgressBlocked is the single opaque sentinel every egress rejection
// returns. It carries no address so a caller relaying it cannot use the error
// text as a port/host scan oracle.
var ErrEgressBlocked = errors.New("destination not permitted by egress policy")

// PermissiveEgressPolicy allows every destination. It is the CE / bare-metal
// default returned by Noop.EgressPolicy(): a self-hosted operator's stored
// models may legitimately target internal hosts.
type PermissiveEgressPolicy struct{}

// CheckAddr always returns nil — all destinations are permitted in CE mode.
func (PermissiveEgressPolicy) CheckAddr(string, string) error { return nil }

// CheckURL always returns nil — all destinations are permitted in CE mode.
func (PermissiveEgressPolicy) CheckURL(string) error { return nil }

// DenyPrivateEgressPolicy rejects loopback / private / link-local / CGNAT /
// unspecified / IPv4-mapped-private resolved addresses and cloud-metadata
// hostnames. It is both the engine-owned baseline for the untrusted BYOK path
// (CE and managed identical) and the tightened policy a managed deployment
// returns for its operator/stored-model path.
type DenyPrivateEgressPolicy struct{}

// CheckAddr rejects a resolved ip:port that points at a non-routable or
// internal-only address.
func (DenyPrivateEgressPolicy) CheckAddr(_, address string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return ErrEgressBlocked
	}
	ip := net.ParseIP(host)
	if ip == nil || isBlockedIP(ip) {
		return ErrEgressBlocked
	}
	return nil
}

// CheckURL rejects non-http(s) schemes and cloud-metadata hostnames. Hostname
// defence lives here because net.Dialer.Control only sees the post-DNS ip:port
// and a metadata hostname may resolve to a routable address.
func (DenyPrivateEgressPolicy) CheckURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ErrEgressBlocked
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return ErrEgressBlocked
	}
	host := strings.ToLower(u.Hostname())
	if host == "" || isMetadataHostname(host) {
		return ErrEgressBlocked
	}
	// A literal private IP in the URL is caught at dial time too, but reject it
	// here as well so the failure is deterministic regardless of resolution.
	if ip := net.ParseIP(host); ip != nil && isBlockedIP(ip) {
		return ErrEgressBlocked
	}
	return nil
}

// isBlockedIP reports whether ip is loopback, private, link-local, CGNAT,
// unspecified, or otherwise not a safe public destination. Go's IsPrivate()
// misses CGNAT (100.64.0.0/10), so it is checked explicitly.
func isBlockedIP(ip net.IP) bool {
	// Normalise IPv4-mapped IPv6 (::ffff:a.b.c.d) to its IPv4 form so the
	// checks below see the real address.
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	// Unwrap IPv4 addresses embedded in IPv6 that To4 does not fold: NAT64
	// (64:ff9b::/96) and the deprecated IPv4-compatible (::/96) form. A literal
	// like 64:ff9b::7f00:1 (NAT64-embedded 127.0.0.1) is otherwise a
	// global-unicast IPv6 that would slip through — classify the embedded v4.
	if v4 := embeddedIPv4(ip); v4 != nil {
		return isBlockedIP(v4)
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() {
		return true
	}
	return isCGNAT(ip)
}

// nat64Prefix is the well-known NAT64 prefix 0064:ff9b::/96 (RFC 6052).
var nat64Prefix = []byte{0x00, 0x64, 0xff, 0x9b}

// embeddedIPv4 returns the IPv4 address embedded in a NAT64 (64:ff9b::/96) or
// IPv4-compatible (::/96, excluding ::/128, ::1 and IPv4-mapped) IPv6 address,
// or nil when ip carries no such embedding.
func embeddedIPv4(ip net.IP) net.IP {
	v16 := ip.To16()
	if v16 == nil || ip.To4() != nil {
		return nil
	}
	// NAT64 0064:ff9b::/96 — the 4-byte prefix followed by 8 zero bytes.
	if bytes.Equal(v16[:4], nat64Prefix) && isZero(v16[4:12]) {
		return net.IPv4(v16[12], v16[13], v16[14], v16[15])
	}
	// IPv4-compatible ::/96 (first 12 bytes zero). Skip ::/128 (unspecified,
	// already caught) and ::1 (loopback) — only treat it as embedded when the
	// last four bytes are a non-trivial address.
	if isZero(v16[:12]) && (v16[12] != 0 || v16[13] != 0) {
		return net.IPv4(v16[12], v16[13], v16[14], v16[15])
	}
	return nil
}

// isZero reports whether every byte in b is zero.
func isZero(b []byte) bool {
	for _, x := range b {
		if x != 0 {
			return false
		}
	}
	return true
}

// isCGNAT reports whether ip is in the carrier-grade NAT range 100.64.0.0/10
// (RFC 6598), which net.IP.IsPrivate does not classify as private.
func isCGNAT(ip net.IP) bool {
	v4 := ip.To4()
	if v4 == nil {
		return false
	}
	return v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127
}

// isMetadataHostname reports whether host is a well-known cloud instance
// metadata endpoint reachable by name.
func isMetadataHostname(host string) bool {
	switch host {
	case "metadata.google.internal", "metadata", "instance-data":
		return true
	}
	return false
}
