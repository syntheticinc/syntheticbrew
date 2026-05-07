package http

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"gorm.io/gorm"
)

// SchemaNameResolver is the consumer-side interface for resolving a schema
// name to its UUID. Implemented by GORMSchemaRepository.GetSchemaIDByName.
//
// Distinct from SchemaRefRepo (resolvers.go) which accepts both UUID and
// name — engine 1.1.0 URL handlers strictly accept name only, so we use
// the narrow consumer-side interface here.
type SchemaNameResolver interface {
	GetSchemaIDByName(ctx context.Context, name string) (string, error)
}

// KBNameResolver is the consumer-side interface for resolving a knowledge
// base name to its UUID. Implemented by
// GORMKnowledgeBaseRepository.GetKBIDByName.
type KBNameResolver interface {
	GetKBIDByName(ctx context.Context, name string) (string, error)
}

// resolveSchemaNameToUUID validates a URL `{name}` parameter and resolves it
// to the schema UUID within the caller's tenant. Strict name-only — does
// NOT accept UUID-shaped inputs (those are explicitly rejected by
// ValidateResourceName).
//
// Returns:
//   - ErrInvalidName / ErrUUIDShapedName / ErrReservedName / ErrNameTooLong / ErrNameEmpty
//     for client-supplied bad input (handler maps to 400)
//   - ErrRefNotFound when validation passes but no row matches in tenant
//     (handler maps to 404 — does not leak existence across tenants)
//   - other repo errors wrapped (handler maps to 500)
func resolveSchemaNameToUUID(ctx context.Context, repo SchemaNameResolver, name string) (string, error) {
	if err := ValidateResourceName(name); err != nil {
		return "", err
	}
	id, err := repo.GetSchemaIDByName(ctx, name)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", ErrRefNotFound
		}
		return "", fmt.Errorf("get schema id by name: %w", err)
	}
	return id, nil
}

// resolveKBNameToUUID — symmetric for knowledge bases.
func resolveKBNameToUUID(ctx context.Context, repo KBNameResolver, name string) (string, error) {
	if err := ValidateResourceName(name); err != nil {
		return "", err
	}
	id, err := repo.GetKBIDByName(ctx, name)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", ErrRefNotFound
		}
		return "", fmt.Errorf("get knowledge base id by name: %w", err)
	}
	return id, nil
}

// writeNameLookupError maps a name-resolution error to a JSON response and
// returns true. Validation sentinels → 400 with the cause string. ErrRefNotFound
// → 404 with `{resource} not found`. Anything else is logged at error level
// and returned as 500 so a repo outage never leaks via 404.
//
// resourceLabel is the human-readable name used in the 404 body — "schema"
// or "knowledge base". The 400 body always carries the validation cause so
// operators can self-correct (e.g. "invalid schema name: resource name is
// reserved (collides with route segment)").
func writeNameLookupError(ctx context.Context, w http.ResponseWriter, resourceLabel, name string, err error) {
	switch {
	case errors.Is(err, ErrInvalidName), errors.Is(err, ErrUUIDShapedName),
		errors.Is(err, ErrReservedName), errors.Is(err, ErrNameTooLong),
		errors.Is(err, ErrNameEmpty):
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "invalid " + resourceLabel + " name: " + err.Error(),
		})
	case errors.Is(err, ErrRefNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{
			"error": resourceLabel + " not found",
		})
	default:
		slog.ErrorContext(ctx, "resolve resource name failed",
			"resource", resourceLabel, "name", name, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "internal error",
		})
	}
}
