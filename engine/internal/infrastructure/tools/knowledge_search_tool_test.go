package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/pgvector/pgvector-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/models"
)

type mockKnowledgeSearcher struct {
	chunks []models.KnowledgeChunk
	err    error
}

func (m *mockKnowledgeSearcher) SearchSimilarByKBs(_ context.Context, _ []string, _ pgvector.Vector, _ int, _ float64) ([]models.KnowledgeChunk, error) {
	return m.chunks, m.err
}

func (m *mockKnowledgeSearcher) SearchByKeywordKBs(_ context.Context, _ []string, _ string, _ int) ([]models.KnowledgeChunk, error) {
	return m.chunks, m.err
}

type mockKnowledgeEmbedder struct {
	embedding []float32
	err       error
}

func (m *mockKnowledgeEmbedder) Embed(_ context.Context, _ string) ([]float32, error) {
	return m.embedding, m.err
}

func TestKnowledgeSearchTool_Info(t *testing.T) {
	tool := NewKnowledgeSearchTool("test-agent", []string{"kb-1"}, &mockKnowledgeSearcher{}, &mockKnowledgeEmbedder{}, 5, 0)
	info, err := tool.Info(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "knowledge_search", info.Name)
	assert.NotEmpty(t, info.Desc)
}

func TestKnowledgeSearchTool_EmptyQuery(t *testing.T) {
	tool := NewKnowledgeSearchTool("test-agent", []string{"kb-1"}, &mockKnowledgeSearcher{}, &mockKnowledgeEmbedder{}, 5, 0)
	args, _ := json.Marshal(knowledgeSearchArgs{Query: ""})
	result, err := tool.InvokableRun(context.Background(), string(args))
	require.NoError(t, err)
	assert.Contains(t, result, "[ERROR]")
	assert.Contains(t, result, "query is required")
}

func TestKnowledgeSearchTool_InvalidJSON(t *testing.T) {
	tool := NewKnowledgeSearchTool("test-agent", []string{"kb-1"}, &mockKnowledgeSearcher{}, &mockKnowledgeEmbedder{}, 5, 0)
	result, err := tool.InvokableRun(context.Background(), "not json")
	require.NoError(t, err)
	assert.Contains(t, result, "[ERROR]")
}

func TestKnowledgeSearchTool_NoResults(t *testing.T) {
	embedder := &mockKnowledgeEmbedder{embedding: []float32{0.1, 0.2, 0.3}}
	searcher := &mockKnowledgeSearcher{chunks: nil}
	tool := NewKnowledgeSearchTool("test-agent", []string{"kb-1"}, searcher, embedder, 5, 0)

	args, _ := json.Marshal(knowledgeSearchArgs{Query: "test query"})
	result, err := tool.InvokableRun(context.Background(), string(args))
	require.NoError(t, err)
	assert.Contains(t, result, "No results found")
}

func TestKnowledgeSearchTool_ReturnsFormattedResults(t *testing.T) {
	embedder := &mockKnowledgeEmbedder{embedding: []float32{0.1, 0.2, 0.3}}
	searcher := &mockKnowledgeSearcher{
		chunks: []models.KnowledgeChunk{
			{
				Content:  "This is chunk one content.",
				Document: models.KnowledgeDocument{FilePath: "/data/readme.md"},
			},
			{
				Content:  "This is chunk two content.",
				Document: models.KnowledgeDocument{FilePath: "/data/guide.txt"},
			},
		},
	}
	tool := NewKnowledgeSearchTool("test-agent", []string{"kb-1"}, searcher, embedder, 5, 0)

	args, _ := json.Marshal(knowledgeSearchArgs{Query: "test query", Limit: 2})
	result, err := tool.InvokableRun(context.Background(), string(args))
	require.NoError(t, err)

	assert.Contains(t, result, "readme.md")
	assert.Contains(t, result, "guide.txt")
	assert.Contains(t, result, "chunk one content")
	assert.Contains(t, result, "chunk two content")
	assert.Contains(t, result, "Result 1")
	assert.Contains(t, result, "Result 2")
}

func TestKnowledgeSearchTool_DefaultLimit(t *testing.T) {
	embedder := &mockKnowledgeEmbedder{embedding: []float32{0.1, 0.2}}
	searcher := &mockKnowledgeSearcher{chunks: nil}
	tool := NewKnowledgeSearchTool("test-agent", []string{"kb-1"}, searcher, embedder, 5, 0)

	// No limit specified — defaults to 5
	args, _ := json.Marshal(knowledgeSearchArgs{Query: "test"})
	_, err := tool.InvokableRun(context.Background(), string(args))
	require.NoError(t, err)
}

func TestKnowledgeSearchTool_LimitCap(t *testing.T) {
	embedder := &mockKnowledgeEmbedder{embedding: []float32{0.1, 0.2}}
	searcher := &mockKnowledgeSearcher{chunks: nil}
	tool := NewKnowledgeSearchTool("test-agent", []string{"kb-1"}, searcher, embedder, 5, 0)

	// Limit > 20 should be capped
	args, _ := json.Marshal(knowledgeSearchArgs{Query: "test", Limit: 100})
	_, err := tool.InvokableRun(context.Background(), string(args))
	require.NoError(t, err)
}

func TestKnowledgeSearchTool_EmbedError(t *testing.T) {
	embedder := &mockKnowledgeEmbedder{err: assert.AnError}
	searcher := &mockKnowledgeSearcher{}
	tool := NewKnowledgeSearchTool("test-agent", []string{"kb-1"}, searcher, embedder, 5, 0)

	args, _ := json.Marshal(knowledgeSearchArgs{Query: "test"})
	result, err := tool.InvokableRun(context.Background(), string(args))
	require.NoError(t, err)
	assert.Contains(t, result, "[ERROR]")
	assert.Contains(t, result, "embed query")
}

func TestKnowledgeSearchTool_NoKBIDs(t *testing.T) {
	embedder := &mockKnowledgeEmbedder{embedding: []float32{0.1, 0.2}}
	searcher := &mockKnowledgeSearcher{chunks: nil}
	tool := NewKnowledgeSearchTool("test-agent", nil, searcher, embedder, 5, 0)

	args, _ := json.Marshal(knowledgeSearchArgs{Query: "test"})
	result, err := tool.InvokableRun(context.Background(), string(args))
	require.NoError(t, err)
	assert.Contains(t, result, "No results found")
	assert.Contains(t, result, "No knowledge bases linked")
}
