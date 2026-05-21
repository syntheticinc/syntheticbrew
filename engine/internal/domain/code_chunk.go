package domain

import (
	"time"

	"github.com/syntheticinc/syntheticbrew/pkg/errors"
)

// CodeChunk represents a chunk of code with metadata
type CodeChunk struct {
	ChunkID    string
	ProjectKey string
	UserID     string
	FilePath   string
	Content    string
	StartLine  int
	EndLine    int
	Language   string
	ChunkType  string
	Name       string
	Embedding  []float32
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// NewCodeChunk creates a new CodeChunk with validation
func NewCodeChunk(
	chunkID, projectKey, userID, filePath, content string,
	startLine, endLine int,
	language, chunkType, name string,
) (*CodeChunk, error) {
	now := time.Now()
	chunk := &CodeChunk{
		ChunkID:    chunkID,
		ProjectKey: projectKey,
		UserID:     userID,
		FilePath:   filePath,
		Content:    content,
		StartLine:  startLine,
		EndLine:    endLine,
		Language:   language,
		ChunkType:  chunkType,
		Name:       name,
		CreatedAt:  now,
		UpdatedAt:  now,
	}

	if err := chunk.Validate(); err != nil {
		return nil, err
	}

	return chunk, nil
}

// Validate validates the CodeChunk
func (c *CodeChunk) Validate() error {
	if c.ChunkID == "" {
		return errors.New(errors.CodeInvalidInput, "chunk_id is required")
	}
	if c.ProjectKey == "" {
		return errors.New(errors.CodeInvalidInput, "project_key is required")
	}
	if c.UserID == "" {
		return errors.New(errors.CodeInvalidInput, "user_id is required")
	}
	if c.FilePath == "" {
		return errors.New(errors.CodeInvalidInput, "file_path is required")
	}
	if c.Content == "" {
		return errors.New(errors.CodeInvalidInput, "content is required")
	}
	if c.StartLine < 0 {
		return errors.New(errors.CodeInvalidInput, "start_line must be >= 0")
	}
	if c.EndLine < c.StartLine {
		return errors.New(errors.CodeInvalidInput, "end_line must be >= start_line")
	}
	if c.Language == "" {
		return errors.New(errors.CodeInvalidInput, "language is required")
	}

	// Business rule: max chunk size
	const maxChunkSize = 10000 // 10KB
	if len(c.Content) > maxChunkSize {
		return errors.New(errors.CodeInvalidInput, "chunk content exceeds max size")
	}

	return nil
}

// LineCount returns the number of lines in the chunk
func (c *CodeChunk) LineCount() int {
	return c.EndLine - c.StartLine + 1
}

// SetEmbedding sets the embedding vector for the chunk
func (c *CodeChunk) SetEmbedding(embedding []float32) error {
	if len(embedding) == 0 {
		return errors.New(errors.CodeInvalidInput, "embedding cannot be empty")
	}
	c.Embedding = embedding
	c.UpdatedAt = time.Now()
	return nil
}
