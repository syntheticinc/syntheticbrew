package http

import (
	"context"
	"net/http"
	"strings"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

const (
	headerXContentTypeOptions = "X-Content-Type-Options"
	headerXFrameOptions       = "X-Frame-Options"
	headerCSP                 = "Content-Security-Policy"
	headerReferrerPolicy      = "Referrer-Policy"
	headerHSTS                = "Strict-Transport-Security"

	valueNoSniff        = "nosniff"
	valueDeny           = "DENY"
	valueReferrerPolicy = "strict-origin-when-cross-origin"
	valueHSTS           = "max-age=31536000; includeSubDomains"
	// 'unsafe-inline' restricted to style-src; admin SPA bundle emits inline styles + uses Google Fonts + data: SVGs.
	valueCSPDefault = "default-src 'self'; " +
		"script-src 'self'; " +
		"style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; " +
		"font-src 'self' https://fonts.gstatic.com data:; " +
		"img-src 'self' data: https:; " +
		"connect-src 'self'; " +
		"frame-ancestors 'none'"
)

// WidgetEmbedOriginsLookup returns the allowed frame-ancestors for a tenant.
// Returns an empty slice when no origins are configured — callers render CSP
// as frame-ancestors 'none' in that case (safe default: blocks all embedding).
type WidgetEmbedOriginsLookup interface {
	GetWidgetEmbedOrigins(ctx context.Context, tenantID string) []string
}

// SecurityHeadersMiddleware returns an http.Handler middleware that sets the
// standard security headers for API and admin routes:
//
//   - X-Content-Type-Options: nosniff
//   - X-Frame-Options: DENY
//   - Content-Security-Policy: default-src 'self'; frame-ancestors 'none'
//   - Referrer-Policy: strict-origin-when-cross-origin
//   - Strict-Transport-Security: max-age=31536000; includeSubDomains
//     (only when the request arrived over TLS or via X-Forwarded-Proto: https)
//
// It does not override a CSP that a downstream handler has already set.
func SecurityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setCommonSecurityHeaders(w, r)
		if w.Header().Get(headerCSP) == "" {
			w.Header().Set(headerCSP, valueCSPDefault)
		}
		w.Header().Set(headerXFrameOptions, valueDeny)
		next.ServeHTTP(w, r)
	})
}

// WidgetSecurityHeadersMiddleware returns middleware for widget routes.
// It looks up widget_embed_origins per-request via lookup, using the tenant_id
// stored in ctx by the tenant middleware. Without a tenant in ctx, the
// middleware defaults to frame-ancestors 'none' (safe default: blocks embedding).
//
// X-Frame-Options is intentionally omitted — the widget is designed to be
// embedded in third-party pages. The frame-ancestors CSP directive enforces
// the per-tenant allow-list instead.
func WidgetSecurityHeadersMiddleware(lookup WidgetEmbedOriginsLookup) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			setCommonSecurityHeaders(w, r)
			// Widget routes: no X-Frame-Options — intentional embedding.
			w.Header().Del(headerXFrameOptions)
			var origins []string
			if lookup != nil {
				if tid := domain.TenantIDFromContext(r.Context()); tid != "" {
					origins = lookup.GetWidgetEmbedOrigins(r.Context(), tid)
				}
			}
			// Always overwrite CSP for widget routes so the per-tenant origins
			// take precedence over any previously-set global default-src header.
			w.Header().Set(headerCSP, buildWidgetCSP(origins))
			next.ServeHTTP(w, r)
		})
	}
}

// setCommonSecurityHeaders applies headers shared by all route groups.
func setCommonSecurityHeaders(w http.ResponseWriter, r *http.Request) {
	w.Header().Set(headerXContentTypeOptions, valueNoSniff)
	w.Header().Set(headerReferrerPolicy, valueReferrerPolicy)
	if isTLS(r) {
		w.Header().Set(headerHSTS, valueHSTS)
	}
}

// isTLS returns true when the connection is TLS or the request was forwarded
// from an upstream TLS proxy (X-Forwarded-Proto: https).
func isTLS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// buildWidgetCSP returns the CSP for widget routes; same relaxations as
// valueCSPDefault, only frame-ancestors differs (per-tenant allow-list).
func buildWidgetCSP(origins []string) string {
	frameAncestors := "'none'"
	if len(origins) > 0 {
		frameAncestors = strings.Join(origins, " ")
	}
	return "default-src 'self'; " +
		"script-src 'self'; " +
		"style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; " +
		"font-src 'self' https://fonts.gstatic.com data:; " +
		"img-src 'self' data: https:; " +
		"connect-src 'self'; " +
		"frame-ancestors " + frameAncestors
}
