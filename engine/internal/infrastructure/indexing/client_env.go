package indexing

import "github.com/syntheticinc/syntheticbrew/pkg/config"

// NewClient constructs an EmbeddingsClient from the bootstrap embeddings config.
// Empty fields fall back to the package defaults:
//
//	URL   == "" → DefaultOllamaURL
//	Model == "" → DefaultEmbedModel
//	Dim   <= 0  → DefaultDimension
func NewClient(cfg config.EmbeddingsConfig) *EmbeddingsClient {
	url := cfg.URL
	if url == "" {
		url = DefaultOllamaURL
	}
	model := cfg.Model
	if model == "" {
		model = DefaultEmbedModel
	}
	dim := cfg.Dim
	if dim <= 0 {
		dim = DefaultDimension
	}
	return NewEmbeddingsClient(url, model, dim)
}
