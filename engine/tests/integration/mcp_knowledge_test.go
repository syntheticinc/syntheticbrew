//go:build integration

package integration

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	gormpostgres "gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/auth"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/models"
	pluginpkg "github.com/syntheticinc/syntheticbrew/pkg/plugin"
	ceserver "github.com/syntheticinc/syntheticbrew/pkg/server"
)

// --- MCP JSON-RPC helpers (scoped to the KB tool tests) ---

// mcpRPCError mirrors the JSON-RPC 2.0 error object the MCP endpoint returns
// for protocol-level failures (parse/method/scope).
type mcpRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// mcpRPCEnvelope is the JSON-RPC 2.0 response envelope.
type mcpRPCEnvelope struct {
	Result json.RawMessage `json:"result"`
	Error  *mcpRPCError    `json:"error"`
}

// mcpToolCallResult is the tools/call result: one or more text content blocks
// plus the tool-level isError flag.
type mcpToolCallResult struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	IsError bool `json:"isError"`
}

// mcpPost issues one POST /api/v1/mcp/rpc against base with the given bearer
// token and JSON body. base lets the same helper drive both the shared suite
// engine and a self-contained engine booted with a custom plugin.
func mcpPost(t *testing.T, base, token string, body io.Reader) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, base+"/api/v1/mcp/rpc", body)
	require.NoError(t, err, "build mcp request")
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := httpClient.Do(req)
	require.NoError(t, err, "mcp http do")
	return resp
}

// mcpToolCall invokes a tools/call against base and returns the joined tool text,
// the tool-level isError flag, and the HTTP status. It fails the test on a
// JSON-RPC protocol error (a well-formed tool call must never produce one for
// the flows here — tool-level failures come back as isError:true inside a 200).
func mcpToolCall(t *testing.T, base, token, toolName string, args map[string]any) (string, bool, int) {
	t.Helper()
	reqBody := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params":  map[string]any{"name": toolName, "arguments": args},
	}
	resp := mcpPost(t, base, token, mustJSON(reqBody))
	raw := readBody(t, resp)

	require.NotEqual(t, http.StatusInternalServerError, resp.StatusCode,
		"tools/call %s must never 500; body=%s", toolName, raw)

	var env mcpRPCEnvelope
	require.NoError(t, json.Unmarshal(raw, &env), "decode rpc envelope: %s", raw)
	require.Nil(t, env.Error, "tools/call %s protocol error: %+v", toolName, env.Error)

	var result mcpToolCallResult
	require.NoError(t, json.Unmarshal(env.Result, &result), "decode tool result: %s", env.Result)

	var text strings.Builder
	for _, c := range result.Content {
		text.WriteString(c.Text)
	}
	return text.String(), result.IsError, resp.StatusCode
}

// signAdminToken mints an EdDSA JWT (no aud, no tenant) that the engine's
// verifier grants ScopeAdmin to — the standard local-admin credential. key is
// the private half of the engine's own local keypair.
func signAdminToken(key ed25519.PrivateKey, sub string) string {
	return signToken(key, jwt.MapClaims{
		"sub": sub,
		"exp": time.Now().Add(time.Hour).Unix(),
		"iat": time.Now().Unix(),
	})
}

// mcpTokenForTenant signs an EdDSA JWT carrying a tenant_id claim, so the request
// runs scoped to that tenant. Signed with the shared suite engine's key.
func mcpTokenForTenant(sub, tenantID string) string {
	return signToken(localSessionPrivKey, jwt.MapClaims{
		"sub":       sub,
		"tenant_id": tenantID,
		"exp":       time.Now().Add(time.Hour).Unix(),
		"iat":       time.Now().Unix(),
	})
}

// signToken signs claims with an Ed25519 key.
func signToken(key ed25519.PrivateKey, claims jwt.MapClaims) string {
	tok := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	signed, err := tok.SignedString(key)
	if err != nil {
		panic(fmt.Sprintf("signToken: %v", err))
	}
	return signed
}

// seedEmbeddingModelDirect inserts an embedding-kind model row for a specific
// tenant straight through GORM (bypassing REST) so a KB in that tenant can be
// created and pass the embedding-model guard. The base_url is never reached
// synchronously — async indexing failure is irrelevant to the storage/link/
// isolation assertions here.
func seedEmbeddingModelDirect(t *testing.T, db *gorm.DB, name, tenantID string) {
	t.Helper()
	m := models.LLMProviderModel{
		Name:      name,
		Type:      "openai_compatible",
		Kind:      "embedding",
		ModelName: "text-embedding-3-small",
		BaseURL:   "https://api.openai.com/v1",
		Config:    `{"embedding_dim":1536}`,
		TenantID:  tenantID,
	}
	require.NoError(t, db.Create(&m).Error, "seed embedding model %q", name)
}

// --- Test 1: full MCP knowledge-base flow (happy path) ---

// TestMCPKnowledgeFlow drives the whole KB tool chain over the real
// /api/v1/mcp/rpc endpoint on the shared suite engine:
//
//	provision_agent → admin_create_knowledge_base → admin_add_document →
//	admin_list_documents (poll) → admin_link_knowledge_base → get_embed_snippet
//
// It asserts each step succeeds (no [ERROR]), the document is stored under the
// KB, the KB is linked to the agent, and a paste-ready embed snippet is returned.
//
// Embeddings: the suite has no reachable embedding backend, so async indexing
// never reaches 'ready'. Per the harness contract this test asserts up to
// storage + link + snippet; grounded knowledge_search over real vectors is
// covered by the admin knowledge-tool unit tests
// (internal/infrastructure/tools/admin/knowledge_tool_test.go) and the
// upload-service tests.
func TestMCPKnowledgeFlow(t *testing.T) {
	requireSuite(t)
	t.Cleanup(func() {
		truncateTables(t)
		// llm_provider_models is not in the shared truncate list — clean the
		// embedding model this test seeded so it can't perturb sibling tests.
		_ = testDB.Exec("DELETE FROM llm_provider_models WHERE name = ?", "mcpflow-embed").Error
	})

	base := baseURL
	token := adminToken

	// Embedding model the KB will reference (seeded under the sentinel tenant,
	// which is where the local-admin token operates).
	seedEmbeddingModelDirect(t, testDB, "mcpflow-embed", ceTenantID)

	// Step 1: provision an agent (creates schema + agent + entry binding).
	provText, provErr, _ := mcpToolCall(t, base, token, "provision_agent", map[string]any{
		"name":          "mcpflow-agent",
		"system_prompt": "You are a support assistant for the ACME checkout product. Answer only from provided knowledge.",
	})
	require.False(t, provErr, "provision_agent must succeed: %s", provText)
	assert.NotContains(t, provText, "[ERROR]")

	var prov struct {
		AgentName  string `json:"agent_name"`
		SchemaName string `json:"schema_name"`
	}
	require.NoError(t, json.Unmarshal([]byte(provText), &prov), "parse provision result: %s", provText)
	require.Equal(t, "mcpflow-agent", prov.AgentName)
	require.NotEmpty(t, prov.SchemaName)

	// Step 2: create the knowledge base.
	kbText, kbErr, _ := mcpToolCall(t, base, token, "admin_create_knowledge_base", map[string]any{
		"name":            "mcpflow-kb",
		"description":     "Checkout FAQ",
		"embedding_model": "mcpflow-embed",
	})
	require.False(t, kbErr, "admin_create_knowledge_base must succeed: %s", kbText)

	var kb struct {
		KnowledgeBaseID string `json:"knowledge_base_id"`
		Name            string `json:"name"`
	}
	require.NoError(t, json.Unmarshal([]byte(kbText), &kb), "parse kb result: %s", kbText)
	require.NotEmpty(t, kb.KnowledgeBaseID, "KB create must return an id")
	require.Equal(t, "mcpflow-kb", kb.Name)

	// Step 3: add a markdown document.
	const docContent = "# Refund policy\n\nRefunds are issued within 14 days of purchase.\n"
	addText, addErr, _ := mcpToolCall(t, base, token, "admin_add_document", map[string]any{
		"kb_name":   "mcpflow-kb",
		"file_name": "refunds.md",
		"content":   docContent,
		"file_type": "md",
	})
	require.False(t, addErr, "admin_add_document must succeed: %s", addText)

	var added struct {
		DocumentID string `json:"document_id"`
		FileName   string `json:"file_name"`
		Status     string `json:"status"`
	}
	require.NoError(t, json.Unmarshal([]byte(addText), &added), "parse add result: %s", addText)
	require.NotEmpty(t, added.DocumentID, "add_document must return a document id")
	assert.Equal(t, "refunds.md", added.FileName)

	// Step 4: poll admin_list_documents until the document is visibly stored
	// under the KB. Status may be 'indexing' or 'error' (no reachable embedder)
	// — storage is what this step proves, not readiness.
	status := pollDocumentStored(t, base, token, "mcpflow-kb", added.DocumentID)
	t.Logf("document %s reached status %q (readiness needs a real embedder; not required here)", added.DocumentID, status)

	// Step 5: link the KB to the agent.
	linkText, linkErr, _ := mcpToolCall(t, base, token, "admin_link_knowledge_base", map[string]any{
		"kb_name":    "mcpflow-kb",
		"agent_name": "mcpflow-agent",
	})
	require.False(t, linkErr, "admin_link_knowledge_base must succeed: %s", linkText)

	var linked struct {
		Linked bool `json:"linked"`
	}
	require.NoError(t, json.Unmarshal([]byte(linkText), &linked), "parse link result: %s", linkText)
	assert.True(t, linked.Linked, "KB must report as linked to the agent")

	// The link must be durable in the join table (tenant-scoped).
	var linkCount int64
	require.NoError(t, testDB.
		Table("knowledge_base_agents").
		Where("knowledge_base_id = ?", kb.KnowledgeBaseID).
		Count(&linkCount).Error)
	assert.Equal(t, int64(1), linkCount, "exactly one KB↔agent link row must exist")

	// Step 6: get a paste-ready embed snippet for the schema.
	snippetText, snippetErr, _ := mcpToolCall(t, base, token, "get_embed_snippet", map[string]any{
		"schema_name": prov.SchemaName,
		"endpoint":    "https://engine.example.com",
	})
	require.False(t, snippetErr, "get_embed_snippet must succeed: %s", snippetText)
	assert.Contains(t, snippetText, "<script", "snippet must be an HTML script tag")
	assert.Contains(t, snippetText, "widget.js", "snippet must reference widget.js")
	assert.Contains(t, snippetText, `data-schema="`+prov.SchemaName+`"`, "snippet must carry the schema")
}

// pollDocumentStored calls admin_list_documents until docID appears under kbName
// or a short deadline elapses, returning the last-seen status. The row is saved
// synchronously by the upload service, so it should appear on the first poll.
func pollDocumentStored(t *testing.T, base, token, kbName, docID string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		text, isErr, _ := mcpToolCall(t, base, token, "admin_list_documents", map[string]any{
			"kb_name": kbName,
		})
		require.False(t, isErr, "admin_list_documents must succeed: %s", text)

		var list struct {
			Documents []struct {
				DocumentID string `json:"document_id"`
				Status     string `json:"status"`
			} `json:"documents"`
		}
		require.NoError(t, json.Unmarshal([]byte(text), &list), "parse list result: %s", text)
		for _, d := range list.Documents {
			if d.DocumentID == docID {
				return d.Status
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("document %s never appeared under KB %q within deadline", docID, kbName)
		}
		time.Sleep(150 * time.Millisecond)
	}
}

// --- Test 2: document-quota seam on the MCP path (RED→GREEN) ---

// quotaTestPlugin embeds the CE Noop and overrides only OnDocumentCreate to
// enforce a small document limit. It is the test double for the EE/Cloud
// document-quota seam: it proves the MCP admin_add_document path is gated by
// OnDocumentCreate (the bypass this seam was introduced to close).
type quotaTestPlugin struct {
	pluginpkg.Noop
	limit    int32
	admitted atomic.Int32 // successfully admitted documents
	calls    atomic.Int32 // total OnDocumentCreate invocations (spy)
}

// OnDocumentCreate admits up to `limit` documents, then rejects with
// ErrDocumentQuotaExceeded. A rejected attempt does not consume a slot.
func (p *quotaTestPlugin) OnDocumentCreate(_ context.Context, tenantID string, n int) error {
	p.calls.Add(1)
	if tenantID == "" {
		// The engine always resolves a concrete tenant (sentinel in CE) before
		// consulting the guard; an empty tenant here would mean the ingest path
		// dropped tenant scope.
		return fmt.Errorf("quota guard received empty tenant id")
	}
	for {
		cur := p.admitted.Load()
		if cur+int32(n) > p.limit {
			return pluginpkg.ErrDocumentQuotaExceeded
		}
		if p.admitted.CompareAndSwap(cur, cur+int32(n)) {
			return nil
		}
	}
}

// TestMCPDocumentQuotaSeam boots a self-contained engine wired with a plugin
// that caps documents at 2, then adds documents through the MCP
// admin_add_document tool. The first two succeed; the third is rejected. This
// is the regression guard proving the MCP ingest path passes through
// Plugin.OnDocumentCreate (spy: the guard is invoked once per add attempt).
func TestMCPDocumentQuotaSeam(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	plug := &quotaTestPlugin{limit: 2}
	base, db, priv := bootEngineWithPlugin(t, ctx, plug)
	token := signAdminToken(priv, "local-admin")

	// Seed an embedding model (sentinel tenant — where the local-admin token
	// operates) and create a KB via the MCP tool.
	seedEmbeddingModelDirect(t, db, "quota-embed", ceTenantID)

	kbText, kbErr, _ := mcpToolCall(t, base, token, "admin_create_knowledge_base", map[string]any{
		"name":            "quota-kb",
		"embedding_model": "quota-embed",
	})
	require.False(t, kbErr, "admin_create_knowledge_base must succeed: %s", kbText)

	addDoc := func(n int) (string, bool, int) {
		return mcpToolCall(t, base, token, "admin_add_document", map[string]any{
			"kb_name":   "quota-kb",
			"file_name": fmt.Sprintf("doc-%d.md", n),
			"content":   fmt.Sprintf("# Doc %d\n\nbody %d\n", n, n),
			"file_type": "md",
		})
	}

	// First two documents are within the cap.
	for i := 1; i <= 2; i++ {
		text, isErr, status := addDoc(i)
		require.Equal(t, http.StatusOK, status)
		require.False(t, isErr, "document %d must be admitted: %s", i, text)
	}

	// Third document exceeds the cap → tool-level rejection (isError), not a 500.
	text, isErr, status := addDoc(3)
	assert.Equal(t, http.StatusOK, status, "quota rejection must be a 200 JSON-RPC tool error, not a transport 500")
	assert.True(t, isErr, "third document must be rejected by the quota seam: %s", text)
	assert.Contains(t, text, "[ERROR]", "rejection must carry the tool [ERROR] marker")
	assert.Contains(t, text, "Failed to add document", "rejection must come from the add-document path")

	// Spy: the guard was consulted on every ingest attempt (2 admitted + 1 rejected).
	assert.Equal(t, int32(3), plug.calls.Load(), "OnDocumentCreate must be invoked once per MCP add-document call")
	assert.Equal(t, int32(2), plug.admitted.Load(), "exactly two documents must have consumed a slot")
}

// --- Test 3: SCC-02 cross-tenant isolation over the MCP path ---

// TestMCPKnowledgeCrossTenantIsolation proves tenant B cannot reach into tenant
// A's knowledge base through the MCP tools. B's calls to admin_add_document,
// admin_delete_document, and admin_link_knowledge_base against A's KB name all
// resolve to a generic not-found tool error (never a 500, never a leak of A's
// KB existence).
//
// Tenancy: the shared suite runs in CE local mode (RequireTenant=false) but the
// EdDSA verifier honours a tenant_id claim, so two tokens with distinct
// tenant_id claims drive two isolated tenant scopes over the same engine. Repos
// filter every read/write by the context tenant, so this exercises the real
// isolation boundary rather than a CE-only degenerate case.
func TestMCPKnowledgeCrossTenantIsolation(t *testing.T) {
	requireSuite(t)

	const (
		tenantA = "00000000-0000-0000-0000-00000000000a"
		tenantB = "00000000-0000-0000-0000-00000000000b"
	)
	t.Cleanup(func() {
		truncateTables(t)
		_ = testDB.Exec("DELETE FROM llm_provider_models WHERE name IN (?, ?)", "xtenant-embed-a", "xtenant-embed-b").Error
	})

	base := baseURL
	tokenA := mcpTokenForTenant("admin-a", tenantA)
	tokenB := mcpTokenForTenant("admin-b", tenantB)

	// Tenant A: embedding model + KB + an agent to link against.
	seedEmbeddingModelDirect(t, testDB, "xtenant-embed-a", tenantA)

	kbText, kbErr, _ := mcpToolCall(t, base, tokenA, "admin_create_knowledge_base", map[string]any{
		"name":            "tenant-a-kb",
		"embedding_model": "xtenant-embed-a",
	})
	require.False(t, kbErr, "tenant A must create its KB: %s", kbText)

	provText, provErr, _ := mcpToolCall(t, base, tokenA, "provision_agent", map[string]any{
		"name":          "tenant-a-agent",
		"system_prompt": "Tenant A support assistant.",
	})
	require.False(t, provErr, "tenant A must provision its agent: %s", provText)

	// Add a document under tenant A so there is a real doc id to attempt to delete.
	addText, addErr, _ := mcpToolCall(t, base, tokenA, "admin_add_document", map[string]any{
		"kb_name":   "tenant-a-kb",
		"file_name": "a-doc.md",
		"content":   "# A\n\ntenant A private content\n",
		"file_type": "md",
	})
	require.False(t, addErr, "tenant A must add its document: %s", addText)
	var aDoc struct {
		DocumentID string `json:"document_id"`
	}
	require.NoError(t, json.Unmarshal([]byte(addText), &aDoc))
	require.NotEmpty(t, aDoc.DocumentID)

	// Tenant B seeds its own embedding model (so any resolution difference is
	// tenant scope, not a missing-model artifact) but must not see A's KB.
	seedEmbeddingModelDirect(t, testDB, "xtenant-embed-b", tenantB)

	notFound := func(op string, args map[string]any) {
		text, isErr, status := mcpToolCall(t, base, tokenB, op, args)
		assert.Equal(t, http.StatusOK, status, "%s cross-tenant must be a 200 tool error, not a 500", op)
		assert.True(t, isErr, "%s into another tenant's KB must be a tool error: %s", op, text)
		assert.Contains(t, text, "not found", "%s must report a generic not-found (no existence leak): %s", op, text)
		assert.NotContains(t, text, aDoc.DocumentID, "%s must not echo tenant A's private ids", op)
	}

	// B cannot add into A's KB (resolved by name → not found in B's tenant).
	notFound("admin_add_document", map[string]any{
		"kb_name":   "tenant-a-kb",
		"file_name": "evil.md",
		"content":   "# B\n\ninjected\n",
		"file_type": "md",
	})

	// B cannot delete A's document via A's KB name.
	notFound("admin_delete_document", map[string]any{
		"kb_name": "tenant-a-kb",
		"file_id": aDoc.DocumentID,
	})

	// B cannot link A's KB to any agent.
	notFound("admin_link_knowledge_base", map[string]any{
		"kb_name":    "tenant-a-kb",
		"agent_name": "tenant-a-agent",
	})

	// A's document is untouched.
	listText, listErr, _ := mcpToolCall(t, base, tokenA, "admin_list_documents", map[string]any{
		"kb_name": "tenant-a-kb",
	})
	require.False(t, listErr, "tenant A list must succeed: %s", listText)
	assert.Contains(t, listText, aDoc.DocumentID, "tenant A's document must survive B's attempts")
}

// --- self-contained engine boot with a custom plugin ---

// bootEngineWithPlugin starts a fully isolated engine (own pgvector container,
// own port, own config + keypair) wired with plug, and returns its base URL, a
// direct GORM handle for seeding, and the engine's local Ed25519 private key
// (for minting admin tokens the engine's verifier trusts). It mirrors the boot
// pattern of TestBYOKEnvReconcileOnBoot; on missing Docker it skips.
func bootEngineWithPlugin(t *testing.T, ctx context.Context, plug pluginpkg.Plugin) (string, *gorm.DB, ed25519.PrivateKey) {
	t.Helper()

	pg, err := tcpostgres.Run(ctx,
		"pgvector/pgvector:pg16",
		tcpostgres.WithDatabase("syntheticbrew_ce_test"),
		tcpostgres.WithUsername("syntheticbrew"),
		tcpostgres.WithPassword("syntheticbrew_ce_test_pass"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		t.Skipf("mcp document-quota test skipped — cannot start postgres container: %v", err)
	}
	t.Cleanup(func() { _ = pg.Terminate(context.Background()) })

	connStr, err := pg.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err, "postgres connection string")

	migrationsDir, err := filepath.Abs(filepath.Join("..", "..", "migrations"))
	require.NoError(t, err, "resolve migrations dir")
	_, statErr := os.Stat(migrationsDir)
	require.NoError(t, statErr, "migrations dir not found")
	require.NoError(t, applyLiquibaseMigrations(ctx, pg, migrationsDir), "apply liquibase migrations")

	httpPort, err := pickFreePort()
	require.NoError(t, err, "pick free port")

	dataDir, err := os.MkdirTemp("", "syntheticbrew-mcp-quota-")
	require.NoError(t, err, "mkdir data")
	t.Cleanup(func() { _ = os.RemoveAll(dataDir) })

	configPath := filepath.Join(dataDir, "config.yaml")
	require.NoError(t, writeBootstrapConfig(configPath, connStr, httpPort), "write bootstrap config")

	restoreEnv := setEnvIsolated(dataDir)
	t.Cleanup(restoreEnv)

	serverCtx, serverCancel := context.WithCancel(context.Background())
	t.Cleanup(serverCancel)

	go func() {
		_ = ceserver.Run(ceserver.Config{
			ConfigPath:     configPath,
			ConfigExplicit: true,
			RequireTenant:  false,
			Plugin:         plug,
			Version:        "ce-mcp-quota-test",
			Commit:         "none",
			Date:           "none",
		})
		_ = serverCtx
	}()

	base := fmt.Sprintf("http://127.0.0.1:%d", httpPort)
	require.NoError(t, waitForHealthy(ctx, base, 60*time.Second), "wait for engine healthy")

	keysDir := filepath.Join(dataDir, "keys")
	kp, err := auth.LoadOrGenerateKeypair(keysDir)
	require.NoError(t, err, "load engine keypair")

	db, err := gorm.Open(gormpostgres.Open(connStr), &gorm.Config{Logger: gormlogger.Discard})
	require.NoError(t, err, "open assertion gorm connection")

	return base, db, kp.Private
}
