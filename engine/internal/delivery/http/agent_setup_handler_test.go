package http

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAgentSetupPromptDerivesOriginFromRequest(t *testing.T) {
	h := NewAgentSetupPromptHandler("")
	req := httptest.NewRequest("GET", "http://engine.local:9555/agent-setup/prompt.md", nil)
	rec := httptest.NewRecorder()

	h.Get(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/markdown") {
		t.Fatalf("content-type = %q, want text/markdown", ct)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "http://engine.local:9555/api/v1/mcp/rpc") {
		t.Fatalf("body does not carry the request-derived MCP URL:\n%s", body[:200])
	}
	if !strings.Contains(body, "provision_agent") || !strings.Contains(body, "get_embed_snippet") {
		t.Fatal("body is missing the setup tool flow")
	}
	if strings.Contains(body, "syntheticbrew.ai") {
		t.Fatal("instructions must be self-contained — no external SyntheticBrew URLs")
	}
}

func TestAgentSetupPromptPrefersConfiguredBaseURL(t *testing.T) {
	h := NewAgentSetupPromptHandler("https://public.example.com/")
	req := httptest.NewRequest("GET", "http://internal-host/agent-setup/prompt.md", nil)
	rec := httptest.NewRecorder()

	h.Get(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, "https://public.example.com/api/v1/mcp/rpc") {
		t.Fatal("configured public base URL not used")
	}
	if strings.Contains(body, "internal-host") {
		t.Fatal("request host leaked despite configured base URL")
	}
}

func TestAgentSetupPromptHonorsForwardedProto(t *testing.T) {
	h := NewAgentSetupPromptHandler("")
	req := httptest.NewRequest("GET", "http://engine.behind.proxy/agent-setup/prompt.md", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	rec := httptest.NewRecorder()

	h.Get(rec, req)

	if !strings.Contains(rec.Body.String(), "https://engine.behind.proxy/api/v1/mcp/rpc") {
		t.Fatal("X-Forwarded-Proto not honored")
	}
}
