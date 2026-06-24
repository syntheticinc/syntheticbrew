package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/pgvector/pgvector-go"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/models"
)

// KnowledgeSearcher performs vector similarity search on knowledge chunks.
type KnowledgeSearcher interface {
	SearchSimilarByKBs(ctx context.Context, kbIDs []string, embedding pgvector.Vector, limit int, similarityThreshold float64) ([]models.KnowledgeChunk, error)
	SearchByKeywordKBs(ctx context.Context, kbIDs []string, keyword string, limit int) ([]models.KnowledgeChunk, error)
}

// KnowledgeEmbedder generates embeddings for search queries.
type KnowledgeEmbedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}

// knowledgeSearchArgs represents arguments for the knowledge_search tool.
type knowledgeSearchArgs struct {
	Query string `json:"query"`
	Limit int    `json:"limit,omitempty"`
}

// KnowledgeSearchTool searches an agent's knowledge base using semantic similarity.
type KnowledgeSearchTool struct {
	agentName           string
	kbIDs               []string // linked KB IDs (many-to-many)
	repo                KnowledgeSearcher
	embeddings          KnowledgeEmbedder
	defaultLimit        int
	similarityThreshold float64
}

// NewKnowledgeSearchTool creates a new knowledge_search tool for the given agent.
func NewKnowledgeSearchTool(agentName string, kbIDs []string, repo KnowledgeSearcher, embeddings KnowledgeEmbedder, defaultLimit int, similarityThreshold float64) tool.InvokableTool {
	if defaultLimit <= 0 {
		defaultLimit = 5
	}
	return &KnowledgeSearchTool{
		agentName:           agentName,
		kbIDs:               kbIDs,
		repo:                repo,
		embeddings:          embeddings,
		defaultLimit:        defaultLimit,
		similarityThreshold: similarityThreshold,
	}
}

// Info returns tool information for LLM.
func (t *KnowledgeSearchTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "knowledge_search",
		Desc: `Searches the agent's knowledge base for relevant information using semantic similarity.

Use this tool to find answers from the agent's configured knowledge documents (markdown, text files).
The search is based on meaning, not exact keyword matching — so natural language queries work well.`,
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"query": {
				Type:     schema.String,
				Desc:     "Natural language search query describing what you're looking for",
				Required: true,
			},
			"limit": {
				Type:     schema.Integer,
				Desc:     "Maximum number of results to return (default: 5, max: 20)",
				Required: false,
			},
		}),
	}, nil
}

// InvokableRun executes the knowledge search.
func (t *KnowledgeSearchTool) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
	var args knowledgeSearchArgs
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		slog.ErrorContext(ctx, "[KnowledgeSearchTool] failed to parse arguments",
			"error", err, "raw", argumentsInJSON)
		return fmt.Sprintf("[ERROR] Invalid arguments: %v. Please provide a query.", err), nil
	}

	if args.Query == "" {
		return "[ERROR] query is required.", nil
	}

	if args.Limit <= 0 {
		args.Limit = t.defaultLimit
	}
	if args.Limit > 20 {
		args.Limit = 20
	}

	if len(t.kbIDs) == 0 {
		return fmt.Sprintf("No results found in knowledge base for: \"%s\". No knowledge bases linked to agent.", args.Query), nil
	}

	slog.InfoContext(ctx, "[KnowledgeSearchTool] searching",
		"agent", t.agentName, "kb_ids", t.kbIDs, "query", args.Query, "limit", args.Limit)

	embedding, err := t.embeddings.Embed(ctx, args.Query)
	if err != nil {
		slog.ErrorContext(ctx, "[KnowledgeSearchTool] embed query failed", "error", err)
		return fmt.Sprintf("[ERROR] Failed to embed query: %v", err), nil
	}

	chunks, err := t.repo.SearchSimilarByKBs(ctx, t.kbIDs, pgvector.NewVector(embedding), args.Limit, t.similarityThreshold)
	if err != nil {
		slog.ErrorContext(ctx, "[KnowledgeSearchTool] search failed", "error", err)
		return fmt.Sprintf("[ERROR] Search failed: %v", err), nil
	}

	// Hybrid: merge keyword results
	queryWords := strings.Fields(args.Query)
	for _, word := range queryWords {
		if len(word) < 4 {
			continue
		}
		kwChunks, kwErr := t.repo.SearchByKeywordKBs(ctx, t.kbIDs, word, 3)
		if kwErr != nil || len(kwChunks) == 0 {
			continue
		}
		seen := make(map[string]bool, len(chunks))
		for _, c := range chunks {
			seen[c.ID] = true
		}
		for _, c := range kwChunks {
			if !seen[c.ID] {
				chunks = append(chunks, c)
				seen[c.ID] = true
			}
		}
	}

	if len(chunks) == 0 {
		return fmt.Sprintf("No results found in knowledge base for: \"%s\". Try different search terms.", args.Query), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## Knowledge search results for \"%s\"\n\n", args.Query))

	for i, chunk := range chunks {
		source := t.agentName
		if fn := chunk.Document.FileName(); fn != "" {
			source = fn
		}
		sb.WriteString(fmt.Sprintf("### Result %d (Source: %s)\n", i+1, source))
		sb.WriteString(chunk.Content)
		sb.WriteString("\n\n---\n\n")
	}

	slog.InfoContext(ctx, "[KnowledgeSearchTool] returning results", "count", len(chunks))
	return sb.String(), nil
}
