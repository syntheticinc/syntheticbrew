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

// ExtractTenantID returns the tenant_id the auth middleware stamped into the
// request context from the VERIFIED JWT claim.
//
// There is deliberately no X-Tenant-ID header fallback: a client-controlled
// header could otherwise let a validly-signed but tenant-less credential target
// another tenant's registry / MCP clients (cross-tenant side-effect via
// /config/reload and admin CRUD) — a cross-tenant-isolation violation. Tenant identity must
// come only from the verified credential. (No caller sets X-Tenant-ID anywhere
// in the codebase; removing the fallback changes no legitimate flow.)
func (e *JWTTenantExtractor) ExtractTenantID(r *http.Request) (string, error) {
	return domain.TenantIDFromContext(r.Context()), nil
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
