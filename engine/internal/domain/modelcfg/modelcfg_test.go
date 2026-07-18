package modelcfg

import (
	"strings"
	"testing"
)

// pins the SCC-03 base_url format check: malformed URLs are rejected (so
// facades return 400, not a deferred 500 or an outbound request to a garbage
// target), while well-formed http/https URLs — INCLUDING private/localhost
// hosts that self-hosted CE legitimately uses — pass.
func TestValidateBaseURL(t *testing.T) {
	cases := []struct {
		in      string
		wantErr string // substring; "" = must be accepted
	}{
		{"https://openrouter.ai/api/v1", ""},
		{"http://localhost:11434/v1", ""},           // CE ollama — must stay allowed
		{"https://myresource.openai.azure.com", ""}, // azure private endpoint — allowed
		{"http://10.0.0.5:8080", ""},                // on-prem gateway — allowed (no egress block here)
		{"not-a-url", "valid absolute URL"},         // no scheme
		{"ftp://example.com", "scheme must be"},     // wrong scheme
		{"file:///etc/passwd", "scheme must be"},    // dangerous scheme rejected
		{"https://", "must include a host"},         // empty host
	}
	for _, c := range cases {
		got := ValidateBaseURL(c.in)
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

func TestIsValidType(t *testing.T) {
	for _, ok := range []string{"ollama", "openai_compatible", "anthropic", "azure_openai", "openrouter"} {
		if !IsValidType(ok) {
			t.Errorf("%q should be valid", ok)
		}
	}
	for _, bad := range []string{"", "openai", "gpt", "totally_bogus", "OpenRouter"} {
		if IsValidType(bad) {
			t.Errorf("%q should be invalid", bad)
		}
	}
}

func TestCanonicalize(t *testing.T) {
	// openrouter alias → openai_compatible + default base URL when unpinned.
	typ, base := Canonicalize("openrouter", "")
	if typ != "openai_compatible" || base != "https://openrouter.ai/api/v1" {
		t.Fatalf("openrouter unpinned: got (%q,%q)", typ, base)
	}
	// openrouter with a pinned base URL keeps the caller's URL.
	typ, base = Canonicalize("openrouter", "https://custom.example.com/v1")
	if typ != "openai_compatible" || base != "https://custom.example.com/v1" {
		t.Fatalf("openrouter pinned: got (%q,%q)", typ, base)
	}
	// non-alias types pass through untouched.
	typ, base = Canonicalize("anthropic", "")
	if typ != "anthropic" || base != "" {
		t.Fatalf("anthropic passthrough: got (%q,%q)", typ, base)
	}
}
