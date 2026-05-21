package app

import (
	"context"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence"
)

// memoryListerHTTPAdapter bridges MemoryStorage to the http.MemoryLister interface.
type memoryListerHTTPAdapter struct {
	storage *persistence.MemoryStorage
}

func (a *memoryListerHTTPAdapter) Execute(ctx context.Context, schemaID string) ([]*domain.Memory, error) {
	return a.storage.ListBySchema(ctx, schemaID)
}

// memoryClearerHTTPAdapter bridges MemoryStorage to the http.MemoryClearer interface.
type memoryClearerHTTPAdapter struct {
	storage *persistence.MemoryStorage
}

func (a *memoryClearerHTTPAdapter) ClearAll(ctx context.Context, schemaID string) (int64, error) {
	return a.storage.DeleteBySchema(ctx, schemaID)
}

func (a *memoryClearerHTTPAdapter) DeleteOne(ctx context.Context, id string) error {
	return a.storage.DeleteByID(ctx, id)
}
