package admin

import (
	"context"
	"strings"
	"testing"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	pkgerrors "github.com/syntheticinc/syntheticbrew/pkg/errors"
)

// --- stubs ---

type stubAgentRepo struct {
	created   *AgentRecord
	createErr error
}

func (s *stubAgentRepo) List(context.Context) ([]AgentRecord, error) { return nil, nil }
func (s *stubAgentRepo) GetByName(_ context.Context, name string) (*AgentRecord, error) {
	if s.created != nil && s.created.Name == name {
		return s.created, nil
	}
	return nil, nil
}
func (s *stubAgentRepo) Create(_ context.Context, r *AgentRecord) error {
	if s.createErr != nil {
		return s.createErr
	}
	r.ID = "agent-id-1"
	s.created = r
	return nil
}
func (s *stubAgentRepo) Update(context.Context, string, *AgentRecord) error { return nil }
func (s *stubAgentRepo) Delete(context.Context, string) error               { return nil }

type stubSchemaRepo struct {
	created   *SchemaRecord
	updated   *SchemaRecord
	schemas   []SchemaRecord
	createErr error
}

func (s *stubSchemaRepo) List(context.Context) ([]SchemaRecord, error) { return s.schemas, nil }
func (s *stubSchemaRepo) GetByID(context.Context, string) (*SchemaRecord, error) {
	return nil, nil
}
func (s *stubSchemaRepo) Create(_ context.Context, r *SchemaRecord) error {
	if s.createErr != nil {
		return s.createErr
	}
	r.ID = "schema-id-1"
	s.created = r
	return nil
}
func (s *stubSchemaRepo) Update(_ context.Context, _ string, r *SchemaRecord) error {
	s.updated = r
	return nil
}

// stubCreator adapts a stubSchemaRepo to the SchemaCreator seam so the
// provisioning tests keep asserting through sr.created. err short-circuits
// the creation like a guard rejection would.
type stubCreator struct {
	repo *stubSchemaRepo
	err  error
}

func (c *stubCreator) CreateSchema(ctx context.Context, name, description string) (*SchemaRecord, error) {
	if c.err != nil {
		return nil, c.err
	}
	rec := &SchemaRecord{Name: name, Description: description}
	if err := c.repo.Create(ctx, rec); err != nil {
		return nil, err
	}
	return rec, nil
}
func (s *stubSchemaRepo) Delete(context.Context, string) error { return nil }

type stubMinter struct{ token string }

func (m *stubMinter) MintChatToken(context.Context, string) (string, error) {
	if m.token == "" {
		return "bb_testtoken", nil
	}
	return m.token, nil
}

// --- provision_agent ---

func TestProvisionAgent_HappyPath(t *testing.T) {
	ar, sr := &stubAgentRepo{}, &stubSchemaRepo{}
	reloaded := false
	tool := &provisionAgentTool{agentRepo: ar, schemaRepo: sr, schemaCreator: &stubCreator{repo: sr}, reloader: func(context.Context) { reloaded = true }}

	out, err := tool.InvokableRun(context.Background(),
		`{"name":"support","system_prompt":"You are a support agent."}`)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if strings.Contains(out, "[ERROR]") {
		t.Fatalf("expected success, got: %s", out)
	}
	if sr.created == nil || sr.created.Name != "support" {
		t.Fatalf("schema not created")
	}
	if ar.created == nil || ar.created.Name != "support" {
		t.Fatalf("agent not created")
	}
	// entry binding + chat enabled
	if sr.updated == nil || sr.updated.EntryAgentID == nil || sr.updated.ChatEnabled == nil || !*sr.updated.ChatEnabled {
		t.Fatalf("schema not bound/chat-enabled: %+v", sr.updated)
	}
	if !reloaded {
		t.Fatalf("reloader not called")
	}
}

// TestProvisionAgent_ThreadsTenantCtxToReloader guards the Fable #1 fix: the
// admin/provisioning tools must hand the request ctx (carrying tenant_id) to the
// reloader so agent-registry invalidation is tenant-scoped, never a cross-tenant
// InvalidateAll broadcast. Pre-fix the reloader was func() and could only broadcast.
func TestProvisionAgent_ThreadsTenantCtxToReloader(t *testing.T) {
	const tenant = "22222222-2222-2222-2222-222222222222"
	var gotTenant string
	sr := &stubSchemaRepo{}
	tool := &provisionAgentTool{
		agentRepo:     &stubAgentRepo{},
		schemaRepo:    sr,
		schemaCreator: &stubCreator{repo: sr},
		reloader:      func(ctx context.Context) { gotTenant = domain.TenantIDFromContext(ctx) },
	}

	ctx := domain.WithTenantID(context.Background(), tenant)
	out, err := tool.InvokableRun(ctx, `{"name":"support","system_prompt":"x"}`)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if strings.Contains(out, "[ERROR]") {
		t.Fatalf("expected success, got: %s", out)
	}
	if gotTenant != tenant {
		t.Fatalf("reloader must receive the request tenant ctx; got %q want %q", gotTenant, tenant)
	}
}

func TestProvisionAgent_RejectsManagementTool(t *testing.T) {
	ar, sr := &stubAgentRepo{}, &stubSchemaRepo{}
	tool := &provisionAgentTool{agentRepo: ar, schemaRepo: sr, schemaCreator: &stubCreator{repo: sr}, reloader: func(context.Context) {}}

	out, err := tool.InvokableRun(context.Background(),
		`{"name":"pwn","system_prompt":"x","tools":["admin_delete_agent"]}`)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !strings.Contains(out, "admin_delete_agent") || !strings.Contains(strings.ToLower(out), "cannot be granted") {
		t.Fatalf("expected management-tool rejection, got: %s", out)
	}
	// Nothing must be persisted when a dangerous tool is requested.
	if sr.created != nil || ar.created != nil {
		t.Fatalf("privilege-escalation config was persisted: schema=%v agent=%v", sr.created, ar.created)
	}
}

// TestProvisionAgent_QuotaRejection pins the guarded creation path: when the
// creator rejects with a usage-limited DomainError (tenant over its schema
// cap), the tool surfaces the machine-readable quota sentinel and persists
// nothing — the MCP provisioning path is gated exactly like REST.
func TestProvisionAgent_QuotaRejection(t *testing.T) {
	ar, sr := &stubAgentRepo{}, &stubSchemaRepo{}
	tool := &provisionAgentTool{
		agentRepo:     ar,
		schemaRepo:    sr,
		schemaCreator: &stubCreator{repo: sr, err: pkgerrors.UsageLimited("schema limit reached")},
		reloader:      func(context.Context) {},
	}

	out, err := tool.InvokableRun(context.Background(),
		`{"name":"support","system_prompt":"You are a support agent."}`)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !strings.Contains(out, "[quota:schema_limit_reached]") {
		t.Fatalf("expected quota sentinel in tool result, got: %s", out)
	}
	if sr.created != nil || ar.created != nil {
		t.Fatalf("nothing must be persisted on quota rejection: schema=%v agent=%v", sr.created, ar.created)
	}
}

func TestProvisionAgent_RequiresNameAndPrompt(t *testing.T) {
	tool := &provisionAgentTool{agentRepo: &stubAgentRepo{}, schemaRepo: &stubSchemaRepo{}, schemaCreator: &stubCreator{repo: &stubSchemaRepo{}}, reloader: func(context.Context) {}}
	for _, tc := range []struct{ args, want string }{
		{`{"system_prompt":"x"}`, "name is required"},
		{`{"name":"a"}`, "system_prompt is required"},
		{`{"name":"BadName","system_prompt":"x"}`, "Invalid agent name"},
	} {
		out, _ := tool.InvokableRun(context.Background(), tc.args)
		if !strings.Contains(out, tc.want) {
			t.Fatalf("args %s: want %q, got %s", tc.args, tc.want, out)
		}
	}
}

// --- get_embed_snippet ---

func TestGetEmbedSnippet_HappyPath(t *testing.T) {
	sr := &stubSchemaRepo{schemas: []SchemaRecord{{ID: "s1", Name: "support"}}}
	tool := &getEmbedSnippetTool{schemaRepo: sr, minter: &stubMinter{token: "bb_widgetkey"}}

	out, err := tool.InvokableRun(context.Background(),
		`{"schema_name":"support","endpoint":"https://engine.example.com"}`)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	for _, want := range []string{`data-schema="support"`, `data-api-key="bb_widgetkey"`, "https://engine.example.com/widget.js"} {
		if !strings.Contains(out, want) {
			t.Fatalf("snippet missing %q; got: %s", want, out)
		}
	}
}

func TestGetEmbedSnippet_SchemaNotFound(t *testing.T) {
	tool := &getEmbedSnippetTool{schemaRepo: &stubSchemaRepo{}, minter: &stubMinter{}}
	out, _ := tool.InvokableRun(context.Background(), `{"schema_name":"missing"}`)
	if !strings.Contains(out, "not found") {
		t.Fatalf("expected not-found, got: %s", out)
	}
}

func TestGetEmbedSnippet_RejectsBadEndpoint(t *testing.T) {
	sr := &stubSchemaRepo{schemas: []SchemaRecord{{ID: "s1", Name: "support"}}}
	tool := &getEmbedSnippetTool{schemaRepo: sr, minter: &stubMinter{}}
	// (An HTML-breaking value like "><script> can't be embedded as a raw JSON
	// string here; its output is separately covered by html.EscapeString.)
	for _, bad := range []string{`javascript:alert(1)`, `not a url`, `ftp://x`} {
		out, _ := tool.InvokableRun(context.Background(),
			`{"schema_name":"support","endpoint":"`+bad+`"}`)
		if !strings.Contains(out, "Invalid endpoint") {
			t.Fatalf("endpoint %q should be rejected, got: %s", bad, out)
		}
	}
}

func TestValidateEndpoint(t *testing.T) {
	ok := []string{"https://a.com", "http://localhost:9555", "https://x.example.com/"}
	for _, e := range ok {
		if _, msg := validateEndpoint(e); msg != "" {
			t.Fatalf("valid endpoint %q rejected: %s", e, msg)
		}
	}
	bad := []string{"", "javascript:alert(1)", "ftp://x", "not-a-url", "//no-scheme"}
	for _, e := range bad {
		if _, msg := validateEndpoint(e); msg == "" {
			t.Fatalf("invalid endpoint %q accepted", e)
		}
	}
}
