package plugin

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestDenyPrivateEgress_CheckAddr is the resolved-ip:port classifier — the
// DNS-rebinding-safe core: whatever a hostname resolves to, this runs at
// connect with the concrete address. A private / loopback / link-local /
// CGNAT / unspecified / IPv4-mapped-private address is rejected.
func TestDenyPrivateEgress_CheckAddr(t *testing.T) {
	blocked := []string{
		"127.0.0.1:443", "10.0.0.5:80", "172.16.0.1:80", "192.168.1.1:80",
		"169.254.169.254:80", "[::1]:443", "0.0.0.0:80", "[fe80::1]:80",
		"100.64.0.1:80",           // CGNAT — Go IsPrivate misses this
		"[::ffff:10.0.0.1]:80",    // IPv4-mapped private
		"[64:ff9b::7f00:1]:80",    // NAT64-embedded 127.0.0.1
		"[64:ff9b::a00:1]:80",     // NAT64-embedded 10.0.0.1
		"[64:ff9b::a9fe:a9fe]:80", // NAT64-embedded 169.254.169.254 (metadata)
		"[::7f00:1]:80",           // IPv4-compatible 127.0.0.1 (deprecated)
	}
	// A NAT64-embedded PUBLIC address stays allowed (8.8.8.8).
	allowed := []string{"8.8.8.8:443", "1.1.1.1:443", "203.0.113.10:443", "[64:ff9b::808:808]:443"}

	p := DenyPrivateEgressPolicy{}
	for _, addr := range blocked {
		assert.ErrorIs(t, p.CheckAddr("tcp", addr), ErrEgressBlocked, "should block %s", addr)
	}
	for _, addr := range allowed {
		assert.NoError(t, p.CheckAddr("tcp", addr), "should allow %s", addr)
	}
}

// TestDenyPrivateEgress_CheckURL rejects non-http(s) schemes, metadata
// hostnames, and literal private IPs at the URL-parse layer (before DNS).
func TestDenyPrivateEgress_CheckURL(t *testing.T) {
	blocked := []string{
		"file:///etc/passwd", "gopher://x", "ftp://host/x",
		"http://metadata.google.internal/x", "http://metadata/x",
		"http://169.254.169.254/latest/meta-data", "http://10.0.0.1/x",
		"", "http://",
	}
	allowed := []string{"https://api.openai.com/v1", "http://example.com/v1", "https://openrouter.ai/api/v1"}

	p := DenyPrivateEgressPolicy{}
	for _, u := range blocked {
		assert.ErrorIs(t, p.CheckURL(u), ErrEgressBlocked, "should block %q", u)
	}
	for _, u := range allowed {
		assert.NoError(t, p.CheckURL(u), "should allow %q", u)
	}
}

// TestPermissiveEgress allows everything — the CE default.
func TestPermissiveEgress(t *testing.T) {
	p := PermissiveEgressPolicy{}
	assert.NoError(t, p.CheckAddr("tcp", "127.0.0.1:80"))
	assert.NoError(t, p.CheckURL("http://10.0.0.1/x"))
}
