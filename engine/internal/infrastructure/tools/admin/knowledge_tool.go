package admin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/tools"
)

// allowedDocFileTypes is the text-only allowlist for MCP document uploads. The
// MCP path bypasses the REST layer's MIME validation, so the allowlist is
// enforced here — binary parsers (PDF/docx) are intentionally out of reach for
// v1 so a malformed binary can never reach them through this surface.
var allowedDocFileTypes = map[string]bool{
	"md":  true,
	"txt": true,
	"csv": true,
}

// resolveKBID resolves a KB name to its UUID within the caller's tenant. A
// non-empty errMsg is the user-facing rejection (tool-result error). Any
// resolution failure — including a cross-tenant name — surfaces as "not found"
// so the tool never leaks another tenant's KB existence.
func resolveKBID(ctx context.Context, resolver KBRefResolver, kbName string) (string, string) {
	if kbName == "" {
		return "", "[ERROR] kb_name is required"
	}
	kbID, err := resolver.GetKBIDByName(ctx, kbName)
	if err != nil || kbID == "" {
		return "", fmt.Sprintf("[ERROR] Knowledge base %q not found.", kbName)
	}
	return kbID, ""
}

// --- admin_create_knowledge_base ---

type createKnowledgeBaseTool struct {
	creator     KBCreator
	embResolver SingleEmbeddingResolver
}

// NewCreateKnowledgeBaseTool wires the KB-creation tool. When embedding_model is
// omitted it resolves the tenant's single embedding model; a KB with no
// embedding model cannot ingest documents, so ambiguity (0 or >1 models) is a
// hard error rather than a silent no-model KB.
func NewCreateKnowledgeBaseTool(creator KBCreator, embResolver SingleEmbeddingResolver) tool.InvokableTool {
	return &createKnowledgeBaseTool{creator: creator, embResolver: embResolver}
}

func (t *createKnowledgeBaseTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "admin_create_knowledge_base",
		Desc: strings.TrimSpace(`
Creates a knowledge base the agent can answer from. Add documents with admin_add_document, then link it to an agent with admin_link_knowledge_base.

If embedding_model is omitted, the tenant's single configured embedding model is used automatically; if none or several exist, name one explicitly.`),
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"name":            {Type: schema.String, Desc: "Unique knowledge base name.", Required: true},
			"description":     {Type: schema.String, Desc: "Short description of the knowledge base contents.", Required: false},
			"embedding_model": {Type: schema.String, Desc: "Embedding model name or UUID. If omitted, the tenant's single embedding model is used.", Required: false},
		}),
	}, nil
}

type createKnowledgeBaseArgs struct {
	Name           string `json:"name"`
	Description    string `json:"description"`
	EmbeddingModel string `json:"embedding_model"`
}

func (t *createKnowledgeBaseTool) InvokableRun(ctx context.Context, argsJSON string, _ ...tool.Option) (string, error) {
	var args createKnowledgeBaseArgs
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return fmt.Sprintf("[ERROR] Invalid arguments: %v", err), nil
	}
	if args.Name == "" {
		return "[ERROR] name is required", nil
	}

	embeddingRef := args.EmbeddingModel
	if embeddingRef == "" {
		resolved, err := t.embResolver.ResolveSingleEmbeddingModel(ctx)
		if err != nil {
			return fmt.Sprintf("[ERROR] %s", err.Error()), nil
		}
		embeddingRef = resolved
	}

	info, err := t.creator.CreateKB(ctx, args.Name, args.Description, embeddingRef)
	if err != nil {
		return fmt.Sprintf("[ERROR] Failed to create knowledge base: %s", tools.SanitizeDBError(err)), nil
	}

	slog.InfoContext(ctx, "[Knowledge] created knowledge base", "name", info.Name, "kb_id", info.ID)
	return renderJSON(map[string]any{
		"knowledge_base_id":  info.ID,
		"name":               info.Name,
		"embedding_model_id": info.EmbeddingModelID,
		"next_steps": []string{
			"Add documents with admin_add_document (kb_name=" + info.Name + ").",
			"Poll admin_list_documents until each document status is 'ready'.",
			"Link the knowledge base to an agent with admin_link_knowledge_base.",
		},
	})
}

// --- admin_add_document ---

type addDocumentTool struct {
	resolver KBRefResolver
	uploader DocumentUploader
}

// NewAddDocumentTool wires the document-ingest tool. It resolves kb_name to a
// tenant-scoped UUID (the sole cross-tenant guard) and loads the KB's embedding
// model before handing the content to the shared upload service, whose
// admission gate enforces the document quota on this path too.
func NewAddDocumentTool(resolver KBRefResolver, uploader DocumentUploader) tool.InvokableTool {
	return &addDocumentTool{resolver: resolver, uploader: uploader}
}

func (t *addDocumentTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "admin_add_document",
		Desc: strings.TrimSpace(`
Adds a text document to a knowledge base and indexes it for retrieval. Only text formats are accepted: md, txt, csv.

Indexing is asynchronous — the document starts in status 'indexing'. Poll admin_list_documents until it reports status 'ready' before relying on it in answers.`),
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"kb_name":   {Type: schema.String, Desc: "Name of the target knowledge base.", Required: true},
			"file_name": {Type: schema.String, Desc: "Document file name (e.g. faq.md).", Required: true},
			"content":   {Type: schema.String, Desc: "Full text content of the document.", Required: true},
			"file_type": {Type: schema.String, Desc: "Document format: one of md, txt, csv.", Required: true},
		}),
	}, nil
}

type addDocumentArgs struct {
	KBName   string `json:"kb_name"`
	FileName string `json:"file_name"`
	Content  string `json:"content"`
	FileType string `json:"file_type"`
}

func (t *addDocumentTool) InvokableRun(ctx context.Context, argsJSON string, _ ...tool.Option) (string, error) {
	var args addDocumentArgs
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return fmt.Sprintf("[ERROR] Invalid arguments: %v", err), nil
	}
	if args.FileName == "" {
		return "[ERROR] file_name is required", nil
	}
	if args.Content == "" {
		return "[ERROR] content is required", nil
	}
	fileType := strings.ToLower(strings.TrimSpace(args.FileType))
	if !allowedDocFileTypes[fileType] {
		return fmt.Sprintf("[ERROR] Unsupported file_type %q. Allowed: md, txt, csv.", args.FileType), nil
	}

	kbID, errMsg := resolveKBID(ctx, t.resolver, args.KBName)
	if errMsg != "" {
		return errMsg, nil
	}
	kb, err := t.resolver.GetKBByID(ctx, kbID)
	if err != nil || kb == nil {
		return fmt.Sprintf("[ERROR] Knowledge base %q not found.", args.KBName), nil
	}
	if kb.EmbeddingModelID == "" {
		return fmt.Sprintf("[ERROR] Knowledge base %q has no embedding model; recreate it with an embedding model before adding documents.", args.KBName), nil
	}

	content := []byte(args.Content)
	sum := sha256.Sum256(content)
	fileHash := hex.EncodeToString(sum[:])

	doc, err := t.uploader.UploadDocument(ctx, kbID, kb.EmbeddingModelID, args.FileName, fileType, int64(len(content)), fileHash, content)
	if err != nil {
		return fmt.Sprintf("[ERROR] Failed to add document: %s", tools.SanitizeDBError(err)), nil
	}

	slog.InfoContext(ctx, "[Knowledge] added document", "kb_id", kbID, "doc_id", doc.ID, "file", doc.FileName)
	return renderJSON(map[string]any{
		"document_id": doc.ID,
		"file_name":   doc.FileName,
		"status":      doc.Status,
		"note":        "Indexing is asynchronous. Poll admin_list_documents until status is 'ready'.",
	})
}

// --- admin_delete_document ---

type deleteDocumentTool struct {
	resolver KBRefResolver
	deleter  DocumentDeleter
}

// NewDeleteDocumentTool wires the document-delete tool. Deleting is symmetric
// with adding (same knowledge-base lifecycle scope), so it runs under the
// provision scope rather than the broad manage scope.
func NewDeleteDocumentTool(resolver KBRefResolver, deleter DocumentDeleter) tool.InvokableTool {
	return &deleteDocumentTool{resolver: resolver, deleter: deleter}
}

func (t *deleteDocumentTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "admin_delete_document",
		Desc: strings.TrimSpace(`
Deletes a document (and its indexed chunks) from a knowledge base, freeing a document slot. Use admin_list_documents to find the file_id.`),
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"kb_name": {Type: schema.String, Desc: "Name of the knowledge base owning the document.", Required: true},
			"file_id": {Type: schema.String, Desc: "UUID of the document to delete (from admin_list_documents).", Required: true},
		}),
	}, nil
}

type deleteDocumentArgs struct {
	KBName string `json:"kb_name"`
	FileID string `json:"file_id"`
}

func (t *deleteDocumentTool) InvokableRun(ctx context.Context, argsJSON string, _ ...tool.Option) (string, error) {
	var args deleteDocumentArgs
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return fmt.Sprintf("[ERROR] Invalid arguments: %v", err), nil
	}
	if args.FileID == "" {
		return "[ERROR] file_id is required", nil
	}

	kbID, errMsg := resolveKBID(ctx, t.resolver, args.KBName)
	if errMsg != "" {
		return errMsg, nil
	}

	if err := t.deleter.DeleteDocument(ctx, kbID, args.FileID); err != nil {
		return fmt.Sprintf("[ERROR] Failed to delete document %q: %s", args.FileID, tools.SanitizeDBError(err)), nil
	}

	slog.InfoContext(ctx, "[Knowledge] deleted document", "kb_id", kbID, "doc_id", args.FileID)
	return renderJSON(map[string]any{
		"deleted":     true,
		"document_id": args.FileID,
	})
}

// --- admin_link_knowledge_base ---

type linkKnowledgeBaseTool struct {
	resolver KBRefResolver
	linker   KBAgentLinker
}

// NewLinkKnowledgeBaseTool wires the KB↔agent link tool. Both sides are
// tenant-scoped: the KB is resolved by name in-tenant and the link write
// re-verifies both KB and agent belong to the caller's tenant.
func NewLinkKnowledgeBaseTool(resolver KBRefResolver, linker KBAgentLinker) tool.InvokableTool {
	return &linkKnowledgeBaseTool{resolver: resolver, linker: linker}
}

func (t *linkKnowledgeBaseTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "admin_link_knowledge_base",
		Desc: strings.TrimSpace(`
Links a knowledge base to an agent so the agent answers from its documents. The agent must already exist (create it with provision_agent).`),
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"kb_name":    {Type: schema.String, Desc: "Name of the knowledge base to link.", Required: true},
			"agent_name": {Type: schema.String, Desc: "Name of the agent to link the knowledge base to.", Required: true},
		}),
	}, nil
}

type linkKnowledgeBaseArgs struct {
	KBName    string `json:"kb_name"`
	AgentName string `json:"agent_name"`
}

func (t *linkKnowledgeBaseTool) InvokableRun(ctx context.Context, argsJSON string, _ ...tool.Option) (string, error) {
	var args linkKnowledgeBaseArgs
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return fmt.Sprintf("[ERROR] Invalid arguments: %v", err), nil
	}
	if args.AgentName == "" {
		return "[ERROR] agent_name is required", nil
	}

	kbID, errMsg := resolveKBID(ctx, t.resolver, args.KBName)
	if errMsg != "" {
		return errMsg, nil
	}

	if err := t.linker.LinkAgent(ctx, kbID, args.AgentName); err != nil {
		return fmt.Sprintf("[ERROR] Failed to link knowledge base %q to agent %q: %s", args.KBName, args.AgentName, tools.SanitizeDBError(err)), nil
	}

	slog.InfoContext(ctx, "[Knowledge] linked knowledge base to agent", "kb", args.KBName, "agent", args.AgentName)
	return renderJSON(map[string]any{
		"linked":     true,
		"kb_name":    args.KBName,
		"agent_name": args.AgentName,
	})
}

// --- admin_list_documents ---

type listDocumentsTool struct {
	resolver KBRefResolver
	lister   DocumentLister
}

// NewListDocumentsTool wires the document-status tool. Indexing is async, so
// callers poll this until every document reports status 'ready' before relying
// on the knowledge base in answers.
func NewListDocumentsTool(resolver KBRefResolver, lister DocumentLister) tool.InvokableTool {
	return &listDocumentsTool{resolver: resolver, lister: lister}
}

func (t *listDocumentsTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "admin_list_documents",
		Desc: strings.TrimSpace(`
Lists the documents in a knowledge base with their indexing status and chunk count. Poll until every document status is 'ready' before relying on the knowledge base in answers.`),
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"kb_name": {Type: schema.String, Desc: "Name of the knowledge base to list documents for.", Required: true},
		}),
	}, nil
}

type listDocumentsArgs struct {
	KBName string `json:"kb_name"`
}

func (t *listDocumentsTool) InvokableRun(ctx context.Context, argsJSON string, _ ...tool.Option) (string, error) {
	var args listDocumentsArgs
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return fmt.Sprintf("[ERROR] Invalid arguments: %v", err), nil
	}

	kbID, errMsg := resolveKBID(ctx, t.resolver, args.KBName)
	if errMsg != "" {
		return errMsg, nil
	}

	docs, err := t.lister.ListDocuments(ctx, kbID)
	if err != nil {
		return fmt.Sprintf("[ERROR] Failed to list documents: %s", tools.SanitizeDBError(err)), nil
	}

	out := make([]map[string]any, 0, len(docs))
	for _, d := range docs {
		out = append(out, map[string]any{
			"document_id": d.ID,
			"file_name":   d.FileName,
			"file_type":   d.FileType,
			"status":      d.Status,
			"chunk_count": d.ChunkCount,
		})
	}
	return renderJSON(map[string]any{
		"kb_name":   args.KBName,
		"documents": out,
	})
}

// renderJSON marshals a tool result to indented JSON, mapping a marshal failure
// to the same [ERROR] convention every other tool uses.
func renderJSON(v any) (string, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprintf("[ERROR] failed to serialize result: %v", err), nil
	}
	return string(data), nil
}
