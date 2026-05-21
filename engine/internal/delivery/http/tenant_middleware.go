package http

import (
	"log/slog"
	"net/http"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

// TenantExtractor extracts tenant_id from a request (e.g., from JWT claims).
type TenantExtractor interface {
	ExtractTenantID(r *http.Request) (string, error)
}

// JWTTenantExtractor extracts tenant_id from JWT claims in the Authorization header.
type JWTTenantExtractor struct {
	claimKey string // JWT claim key containing tenant_id
}

// NewJWTTenantExtractor creates a new JWTTenantExtractor.
func NewJWTTenantExtractor(claimKey string) *JWTTenantExtractor {
	if claimKey == "" {
		claimKey = "tenant_id"
	}
	return &JWTTenantExtractor{claimKey: claimKey}
}

// ExtractTenantID extracts tenant_id from the request context.
// The auth middleware should have already parsed the JWT and stored claims in context.
func (e *JWTTenantExtractor) ExtractTenantID(r *http.Request) (string, error) {
	// In Cloud mode, tenant_id comes from JWT claims set by auth middleware.
	// Check the context for tenant_id (set by upstream auth middleware).
	tenantID := domain.TenantIDFromContext(r.Context())
	if tenantID != "" {
		return tenantID, nil
	}

	// Fallback: check header (for internal/service-to-service calls)
	tenantID = r.Header.Get("X-Tenant-ID")
	return tenantID, nil
}

// TenantMiddleware injects tenant_id into the request context.
// In CE mode (no extractor or empty tenant_id), requests pass through unscoped.
type TenantMiddleware struct {
	extractor TenantExtractor
	required  bool // if true, reject requests without tenant_id
}

// NewTenantMiddleware creates a new TenantMiddleware.
// If required is true, requests without tenant_id will be rejected (Cloud mode).
// If required is false, requests without tenant_id pass through (CE mode).
func NewTenantMiddleware(extractor TenantExtractor, required bool) *TenantMiddleware {
	return &TenantMiddleware{extractor: extractor, required: required}
}

// Handler returns the middleware handler.
func (m *TenantMiddleware) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tenantID, err := m.extractor.ExtractTenantID(r)
		if err != nil {
			slog.ErrorContext(r.Context(), "tenant extraction failed", "error", err)
			writeJSONError(w, http.StatusUnauthorized, "failed to extract tenant")
			return
		}

		if tenantID == "" && m.required {
			writeJSONError(w, http.StatusForbidden, "tenant_id is required")
			return
		}

		// Set tenant_id in context for downstream use
		ctx := domain.WithTenantID(r.Context(), tenantID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
