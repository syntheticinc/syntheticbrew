package admin

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/tool"
)

// --- fakes for the narrow consumer-side seams ---

type fakeKBCreator struct {
	info   *KnowledgeBaseInfo
	err    error
	called bool

	gotName   string
	gotDesc   string
	gotEmbRef string
}

func (f *fakeKBCreator) CreateKB(_ context.Context, name, description, embeddingModelRef string) (*KnowledgeBaseInfo, error) {
	f.called = true
	f.gotName, f.gotDesc, f.gotEmbRef = name, description, embeddingModelRef
	if f.err != nil {
		return nil, f.err
	}
	if f.info != nil {
		return f.info, nil
	}
	return &KnowledgeBaseInfo{ID: "kb-1", Name: name, Description: description, EmbeddingModelID: embeddingModelRef}, nil
}

type fakeKBResolver struct {
	idByName map[string]string
	kbByID   map[string]*KnowledgeBaseInfo
	idErr    error
}

func (f *fakeKBResolver) GetKBIDByName(_ context.Context, name string) (string, error) {
	if f.idErr != nil {
		return "", f.idErr
	}
	return f.idByName[name], nil // "" when absent → resolveKBID renders "not found"
}

func (f *fakeKBResolver) GetKBByID(_ context.Context, id string) (*KnowledgeBaseInfo, error) {
	return f.kbByID[id], nil
}

type fakeUploader struct {
	doc    *DocumentInfo
	err    error
	called bool

	gotFileType string
	gotKBID     string
	gotEmbID    string
}

func (f *fakeUploader) UploadDocument(_ context.Context, kbID, embeddingModelID, fileName, fileType string, _ int64, _ string, _ []byte) (*DocumentInfo, error) {
	f.called = true
	f.gotFileType, f.gotKBID, f.gotEmbID = fileType, kbID, embeddingModelID
	if f.err != nil {
		return nil, f.err
	}
	if f.doc != nil {
		return f.doc, nil
	}
	return &DocumentInfo{ID: "doc-1", FileName: fileName, FileType: fileType, Status: "indexing"}, nil
}

type fakeDeleter struct {
	err    error
	called bool
}

func (f *fakeDeleter) DeleteDocument(_ context.Context, _, _ string) error {
	f.called = true
	return f.err
}

type fakeLinker struct {
	err    error
	called bool
}

func (f *fakeLinker) LinkAgent(_ context.Context, _, _ string) error {
	f.called = true
	return f.err
}

type fakeCapEnsurer struct {
	err       error
	agentName string
}

func (f *fakeCapEnsurer) EnsureKnowledgeEnabled(_ context.Context, agentName string) error {
	f.agentName = agentName
	return f.err
}

type fakeLister struct {
	docs []DocumentInfo
	err  error
}

func (f *fakeLister) ListDocuments(_ context.Context, _ string) ([]DocumentInfo, error) {
	return f.docs, f.err
}

type fakeEmbResolver struct {
	id     string
	err    error
	called bool
}

func (f *fakeEmbResolver) ResolveSingleEmbeddingModel(_ context.Context) (string, error) {
	f.called = true
	return f.id, f.err
}

// kbWithEmbedding is a resolver wired for the add-document happy path: kb_name
// "kb" resolves to a KB with an embedding model, so a valid file_type reaches
// the uploader.
func kbWithEmbedding() *fakeKBResolver {
	return &fakeKBResolver{
		idByName: map[string]string{"kb": "kb-1"},
		kbByID:   map[string]*KnowledgeBaseInfo{"kb-1": {ID: "kb-1", Name: "kb", EmbeddingModelID: "emb-1"}},
	}
}

// --- admin_add_document: file_type allowlist (security fix guard) ---

func TestAddDocument_FileTypeAllowlist(t *testing.T) {
	tests := []struct {
		name         string
		fileType     string
		wantAccepted bool
	}{
		{"md accepted", "md", true},
		{"txt accepted", "txt", true},
		{"csv accepted", "csv", true},
		{"uppercase normalized", "MD", true},
		{"pdf rejected", "pdf", false},
		{"docx rejected", "docx", false},
		{"exe rejected", "exe", false},
		{"empty rejected", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			uploader := &fakeUploader{}
			tool := NewAddDocumentTool(kbWithEmbedding(), uploader)

			args := `{"kb_name":"kb","file_name":"f.md","content":"hello","file_type":"` + tt.fileType + `"}`
			out, err := tool.InvokableRun(context.Background(), args)
			if err != nil {
				t.Fatalf("unexpected Go error: %v", err)
			}

			gotErr := strings.Contains(out, "[ERROR]")
			if !tt.wantAccepted {
				rejected := gotErr && strings.Contains(out, "Unsupported file_type")
				if !rejected {
					t.Fatalf("file_type %q must be rejected with an [ERROR], got: %s", tt.fileType, out)
				}
				if uploader.called {
					t.Fatalf("a rejected file_type %q must never reach the binary-capable uploader", tt.fileType)
				}
				return
			}
			if gotErr {
				t.Fatalf("file_type %q must be accepted, got: %s", tt.fileType, out)
			}
			if !uploader.called {
				t.Fatalf("uploader must run for accepted file_type %q", tt.fileType)
			}
			if !strings.EqualFold(uploader.gotFileType, tt.fileType) {
				t.Fatalf("uploader received file_type %q, want normalized %q", uploader.gotFileType, tt.fileType)
			}
		})
	}
}

func TestAddDocument_MissingRequiredArgs(t *testing.T) {
	tests := []struct {
		name string
		args string
		want string
	}{
		{"missing file_name", `{"kb_name":"kb","content":"x","file_type":"md"}`, "file_name is required"},
		{"missing content", `{"kb_name":"kb","file_name":"f.md","file_type":"md"}`, "content is required"},
		{"invalid json", `{`, "Invalid arguments"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			uploader := &fakeUploader{}
			tool := NewAddDocumentTool(kbWithEmbedding(), uploader)
			out, err := tool.InvokableRun(context.Background(), tt.args)
			if err != nil {
				t.Fatalf("unexpected Go error: %v", err)
			}
			if !strings.Contains(out, "[ERROR]") || !strings.Contains(out, tt.want) {
				t.Fatalf("expected [ERROR] containing %q, got: %s", tt.want, out)
			}
			if uploader.called {
				t.Fatal("uploader must not run when args are invalid")
			}
		})
	}
}

func TestAddDocument_HappyPath(t *testing.T) {
	uploader := &fakeUploader{}
	tool := NewAddDocumentTool(kbWithEmbedding(), uploader)

	out, err := tool.InvokableRun(context.Background(), `{"kb_name":"kb","file_name":"faq.md","content":"hello","file_type":"md"}`)
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if strings.Contains(out, "[ERROR]") {
		t.Fatalf("expected success, got: %s", out)
	}
	if !strings.Contains(out, "document_id") {
		t.Fatalf("expected document_id in result, got: %s", out)
	}
	if uploader.gotKBID != "kb-1" || uploader.gotEmbID != "emb-1" {
		t.Fatalf("uploader got kbID=%q embID=%q, want kb-1/emb-1", uploader.gotKBID, uploader.gotEmbID)
	}
}

// TestAddDocument_KBNotFound pins SCC-02/03: an unresolvable (or cross-tenant)
// kb_name yields a generic "not found", not a 500 and no existence leak.
func TestAddDocument_KBNotFound(t *testing.T) {
	uploader := &fakeUploader{}
	// resolver knows no KBs → GetKBIDByName returns "".
	tool := NewAddDocumentTool(&fakeKBResolver{idErr: errors.New("boom")}, uploader)

	out, err := tool.InvokableRun(context.Background(), `{"kb_name":"secret","file_name":"f.md","content":"x","file_type":"md"}`)
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !strings.Contains(out, "[ERROR]") || !strings.Contains(out, "not found") {
		t.Fatalf("expected generic not-found [ERROR], got: %s", out)
	}
	if uploader.called {
		t.Fatal("uploader must not run when the KB cannot be resolved")
	}
}

// --- admin_create_knowledge_base ---

func TestCreateKnowledgeBase_ValidCreate(t *testing.T) {
	creator := &fakeKBCreator{}
	embResolver := &fakeEmbResolver{}
	tool := NewCreateKnowledgeBaseTool(creator, embResolver)

	out, err := tool.InvokableRun(context.Background(), `{"name":"docs","description":"d","embedding_model":"emb-x"}`)
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if strings.Contains(out, "[ERROR]") {
		t.Fatalf("expected success, got: %s", out)
	}
	if !strings.Contains(out, "knowledge_base_id") {
		t.Fatalf("expected knowledge_base_id in result, got: %s", out)
	}
	if creator.gotName != "docs" || creator.gotEmbRef != "emb-x" {
		t.Fatalf("creator got name=%q embRef=%q, want docs/emb-x", creator.gotName, creator.gotEmbRef)
	}
	if embResolver.called {
		t.Fatal("single-embedding resolver must NOT run when embedding_model is supplied")
	}
}

// TestCreateKnowledgeBase_EmptyEmbeddingResolvesSingle pins the delegation: an
// omitted embedding_model resolves the tenant's single embedding model.
func TestCreateKnowledgeBase_EmptyEmbeddingResolvesSingle(t *testing.T) {
	creator := &fakeKBCreator{}
	embResolver := &fakeEmbResolver{id: "resolved-emb"}
	tool := NewCreateKnowledgeBaseTool(creator, embResolver)

	out, err := tool.InvokableRun(context.Background(), `{"name":"docs"}`)
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if strings.Contains(out, "[ERROR]") {
		t.Fatalf("expected success, got: %s", out)
	}
	if !embResolver.called {
		t.Fatal("single-embedding resolver must run when embedding_model is omitted")
	}
	if creator.gotEmbRef != "resolved-emb" {
		t.Fatalf("creator got embRef=%q, want the resolved model resolved-emb", creator.gotEmbRef)
	}
}

func TestCreateKnowledgeBase_EmbeddingResolutionError(t *testing.T) {
	creator := &fakeKBCreator{}
	embResolver := &fakeEmbResolver{err: errors.New("multiple embedding models configured: specify the embedding model explicitly")}
	tool := NewCreateKnowledgeBaseTool(creator, embResolver)

	out, err := tool.InvokableRun(context.Background(), `{"name":"docs"}`)
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !strings.Contains(out, "[ERROR]") || !strings.Contains(out, "specify the embedding model explicitly") {
		t.Fatalf("expected embedding-ambiguity [ERROR], got: %s", out)
	}
	if creator.called {
		t.Fatal("KB must not be created when the embedding model cannot be resolved")
	}
}

func TestCreateKnowledgeBase_MissingName(t *testing.T) {
	creator := &fakeKBCreator{}
	tool := NewCreateKnowledgeBaseTool(creator, &fakeEmbResolver{})
	out, err := tool.InvokableRun(context.Background(), `{"description":"no name"}`)
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !strings.Contains(out, "[ERROR]") || !strings.Contains(out, "name is required") {
		t.Fatalf("expected name-required [ERROR], got: %s", out)
	}
	if creator.called {
		t.Fatal("creator must not run without a name")
	}
}

// --- admin_delete_document ---

func TestDeleteDocument_HappyPath(t *testing.T) {
	resolver := &fakeKBResolver{idByName: map[string]string{"kb": "kb-1"}}
	deleter := &fakeDeleter{}
	tool := NewDeleteDocumentTool(resolver, deleter)

	out, err := tool.InvokableRun(context.Background(), `{"kb_name":"kb","file_id":"doc-9"}`)
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if strings.Contains(out, "[ERROR]") {
		t.Fatalf("expected success, got: %s", out)
	}
	if !strings.Contains(out, `"deleted": true`) {
		t.Fatalf("expected deleted:true, got: %s", out)
	}
	if !deleter.called {
		t.Fatal("deleter must run on the happy path")
	}
}

func TestDeleteDocument_MissingFileID(t *testing.T) {
	deleter := &fakeDeleter{}
	tool := NewDeleteDocumentTool(&fakeKBResolver{}, deleter)
	out, _ := tool.InvokableRun(context.Background(), `{"kb_name":"kb"}`)
	if !strings.Contains(out, "[ERROR]") || !strings.Contains(out, "file_id is required") {
		t.Fatalf("expected file_id-required [ERROR], got: %s", out)
	}
	if deleter.called {
		t.Fatal("deleter must not run without a file_id")
	}
}

func TestDeleteDocument_KBNotFound(t *testing.T) {
	deleter := &fakeDeleter{}
	tool := NewDeleteDocumentTool(&fakeKBResolver{}, deleter) // unknown kb → ""
	out, err := tool.InvokableRun(context.Background(), `{"kb_name":"ghost","file_id":"doc-9"}`)
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !strings.Contains(out, "[ERROR]") || !strings.Contains(out, "not found") {
		t.Fatalf("expected generic not-found [ERROR], got: %s", out)
	}
	if deleter.called {
		t.Fatal("deleter must not run when the KB cannot be resolved")
	}
}

// --- admin_link_knowledge_base ---

func TestLinkKnowledgeBase_HappyPath(t *testing.T) {
	resolver := &fakeKBResolver{idByName: map[string]string{"kb": "kb-1"}}
	linker := &fakeLinker{}
	ensurer := &fakeCapEnsurer{}
	tool := NewLinkKnowledgeBaseTool(resolver, linker, ensurer)

	out, err := tool.InvokableRun(context.Background(), `{"kb_name":"kb","agent_name":"support"}`)
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if strings.Contains(out, "[ERROR]") {
		t.Fatalf("expected success, got: %s", out)
	}
	if !strings.Contains(out, `"linked": true`) {
		t.Fatalf("expected linked:true, got: %s", out)
	}
	if !linker.called {
		t.Fatal("linker must run on the happy path")
	}
	// A linked KB is inert without the knowledge capability — linking must
	// auto-enable it so the one-prompt grounded flow works.
	if ensurer.agentName != "support" {
		t.Fatalf("expected knowledge capability auto-enabled for agent %q, got %q", "support", ensurer.agentName)
	}
	if !strings.Contains(out, `"knowledge_capability_enabled": true`) {
		t.Fatalf("expected knowledge_capability_enabled:true, got: %s", out)
	}
}

func TestLinkKnowledgeBase_CapabilityEnsureFailureIsNonFatal(t *testing.T) {
	resolver := &fakeKBResolver{idByName: map[string]string{"kb": "kb-1"}}
	linker := &fakeLinker{}
	ensurer := &fakeCapEnsurer{err: errors.New("db down")}
	tool := NewLinkKnowledgeBaseTool(resolver, linker, ensurer)

	out, err := tool.InvokableRun(context.Background(), `{"kb_name":"kb","agent_name":"support"}`)
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	// The link itself succeeded; the capability failure is surfaced, not fatal.
	if !strings.Contains(out, `"linked": true`) {
		t.Fatalf("expected linked:true despite capability failure, got: %s", out)
	}
	if !strings.Contains(out, `"knowledge_capability_enabled": false`) {
		t.Fatalf("expected knowledge_capability_enabled:false, got: %s", out)
	}
	if !strings.Contains(out, "warning") {
		t.Fatalf("expected a warning telling the caller to enable the capability, got: %s", out)
	}
}

func TestLinkKnowledgeBase_NilEnsurerStillLinks(t *testing.T) {
	resolver := &fakeKBResolver{idByName: map[string]string{"kb": "kb-1"}}
	linker := &fakeLinker{}
	tool := NewLinkKnowledgeBaseTool(resolver, linker, nil)

	out, err := tool.InvokableRun(context.Background(), `{"kb_name":"kb","agent_name":"support"}`)
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !strings.Contains(out, `"linked": true`) {
		t.Fatalf("expected linked:true with a nil ensurer, got: %s", out)
	}
}

func TestLinkKnowledgeBase_MissingAgentName(t *testing.T) {
	linker := &fakeLinker{}
	tool := NewLinkKnowledgeBaseTool(&fakeKBResolver{}, linker, nil)
	out, _ := tool.InvokableRun(context.Background(), `{"kb_name":"kb"}`)
	if !strings.Contains(out, "[ERROR]") || !strings.Contains(out, "agent_name is required") {
		t.Fatalf("expected agent_name-required [ERROR], got: %s", out)
	}
	if linker.called {
		t.Fatal("linker must not run without an agent_name")
	}
}

func TestLinkKnowledgeBase_KBNotFound(t *testing.T) {
	linker := &fakeLinker{}
	tool := NewLinkKnowledgeBaseTool(&fakeKBResolver{}, linker, nil)
	out, err := tool.InvokableRun(context.Background(), `{"kb_name":"ghost","agent_name":"support"}`)
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !strings.Contains(out, "[ERROR]") || !strings.Contains(out, "not found") {
		t.Fatalf("expected generic not-found [ERROR], got: %s", out)
	}
	if linker.called {
		t.Fatal("linker must not run when the KB cannot be resolved")
	}
}

// --- admin_list_documents ---

func TestListDocuments_HappyPath(t *testing.T) {
	resolver := &fakeKBResolver{idByName: map[string]string{"kb": "kb-1"}}
	lister := &fakeLister{docs: []DocumentInfo{
		{ID: "d1", FileName: "faq.md", FileType: "md", Status: "ready", ChunkCount: 3},
	}}
	tool := NewListDocumentsTool(resolver, lister)

	out, err := tool.InvokableRun(context.Background(), `{"kb_name":"kb"}`)
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if strings.Contains(out, "[ERROR]") {
		t.Fatalf("expected success, got: %s", out)
	}
	if !strings.Contains(out, "faq.md") || !strings.Contains(out, "ready") {
		t.Fatalf("expected document listing, got: %s", out)
	}
}

func TestListDocuments_KBNotFound(t *testing.T) {
	lister := &fakeLister{}
	tool := NewListDocumentsTool(&fakeKBResolver{}, lister)
	out, err := tool.InvokableRun(context.Background(), `{"kb_name":"ghost"}`)
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !strings.Contains(out, "[ERROR]") || !strings.Contains(out, "not found") {
		t.Fatalf("expected generic not-found [ERROR], got: %s", out)
	}
}

// --- Info() schema validity for all five tools ---

func TestKnowledgeTools_Info(t *testing.T) {
	tests := []struct {
		name     string
		tool     tool.InvokableTool
		wantName string
		required []string
	}{
		{
			"create_knowledge_base",
			NewCreateKnowledgeBaseTool(&fakeKBCreator{}, &fakeEmbResolver{}),
			"admin_create_knowledge_base",
			[]string{"name"},
		},
		{
			"add_document",
			NewAddDocumentTool(&fakeKBResolver{}, &fakeUploader{}),
			"admin_add_document",
			[]string{"kb_name", "file_name", "content", "file_type"},
		},
		{
			"delete_document",
			NewDeleteDocumentTool(&fakeKBResolver{}, &fakeDeleter{}),
			"admin_delete_document",
			[]string{"kb_name", "file_id"},
		},
		{
			"link_knowledge_base",
			NewLinkKnowledgeBaseTool(&fakeKBResolver{}, &fakeLinker{}, nil),
			"admin_link_knowledge_base",
			[]string{"kb_name", "agent_name"},
		},
		{
			"list_documents",
			NewListDocumentsTool(&fakeKBResolver{}, &fakeLister{}),
			"admin_list_documents",
			[]string{"kb_name"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := tt.tool.Info(context.Background())
			if err != nil {
				t.Fatalf("Info: unexpected error: %v", err)
			}
			if info.Name != tt.wantName {
				t.Fatalf("name = %q, want %q", info.Name, tt.wantName)
			}
			if strings.TrimSpace(info.Desc) == "" {
				t.Fatal("description must be non-empty")
			}
			js, err := info.ToJSONSchema()
			if err != nil {
				t.Fatalf("ToJSONSchema: %v", err)
			}
			for _, want := range tt.required {
				if !contains(js.Required, want) {
					t.Fatalf("required params %v missing %q", js.Required, want)
				}
			}
		})
	}
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
