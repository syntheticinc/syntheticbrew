package http

import (
	"strings"
	"testing"
)

// TestValidateModelBaseURL pins the SCC-03 base_url format check: malformed
// URLs are rejected (so the handler returns 400, not a deferred 500 or an
// outbound request to a garbage target), while well-formed http/https URLs —
// INCLUDING private/localhost hosts that self-hosted CE legitimately uses —
// pass.
func TestValidateModelBaseURL(t *testing.T) {
	cases := []struct {
		in      string
		wantErr string // substring; "" = must be accepted
	}{
		{"https://openrouter.ai/api/v1", ""},
		{"http://localhost:11434/v1", ""},               // CE ollama — must stay allowed
		{"https://myresource.openai.azure.com", ""},     // azure private endpoint — allowed
		{"http://10.0.0.5:8080", ""},                    // on-prem gateway — allowed (no egress block)
		{"not-a-url", "valid absolute URL"},             // no scheme
		{"ftp://example.com", "scheme must be"},         // wrong scheme
		{"file:///etc/passwd", "scheme must be"},        // dangerous scheme rejected
		{"https://", "must include a host"},             // empty host
	}
	for _, c := range cases {
		got := validateModelBaseURL(c.in)
		if c.wantErr == "" {
			if got != "" {
				t.Errorf("%q: want accepted, got error %q", c.in, got)
			}
			continue
		}
		if !strings.Contains(got, c.wantErr) {
			t.Errorf("%q: want error containing %q, got %q", c.in, c.wantErr, got)
		}
	}
}
