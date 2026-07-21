package http

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
)

// widgetPathPrefix is the path under which the embeddable widget bundle is
// served. Requests under this prefix must accept cross-origin access from any
// host (that's the whole point — customers embed the script on their sites),
// regardless of the admin API's CORS allowlist.
const widgetPathPrefix = "/widget/"

// widgetConfigPath is the public widget bootstrap endpoint the embeddable
// widget GETs cross-origin to read its render toggles (attribution badge).
const widgetConfigPath = "/api/v1/widget-config"

// isPublicWidgetAPIPath reports whether the path is a public endpoint invoked
// by the embeddable widget from a third-party origin. The widget (loaded via
// <script src="…/widget.js"> on a customer site) calls both
// POST /api/v1/schemas/{id}/chat and GET /api/v1/widget-config cross-origin,
// each carrying the customer domain's Origin header and a Bearer chat token, so
// the response must include a matching Access-Control-Allow-Origin and allow
// the Authorization header. Tenant isolation is enforced by resolving the token
// → tenant in the handler, not by the CORS policy.
func isPublicWidgetAPIPath(path string) bool {
	if path == widgetConfigPath {
		return true
	}
	// Chat pattern: /api/v1/schemas/{id}/chat — single segment id, no subpaths.
	const prefix = "/api/v1/schemas/"
	const suffix = "/chat"
	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, suffix) {
		return false
	}
	middle := path[len(prefix) : len(path)-len(suffix)]
	return middle != "" && !strings.Contains(middle, "/")
}

// isPublicOAuthPath reports whether the path is an anonymous OAuth
// discovery/token/register endpoint invoked cross-origin by in-browser MCP
// clients with bearer/none auth. These get a wildcard-origin, no-credentials
// CORS policy. The interactive consent endpoints (/api/v1/oauth/authorize-info
// and /api/v1/oauth/approve) are intentionally NOT matched — they ride the
// admin session and stay under the same-origin credentialed policy.
func isPublicOAuthPath(path string) bool {
	if strings.HasPrefix(path, "/.well-known/oauth-") {
		return true
	}
	switch path {
	case "/oauth/token", "/oauth/register",
		"/api/v1/oauth/token", "/api/v1/oauth/register":
		return true
	default:
		return false
	}
}

// Server is the HTTP server that hosts the REST API.
type Server struct {
	router     chi.Router
	httpServer *http.Server
	port       int
}

// NewServer creates a new HTTP server with standard middleware and same-origin CORS policy.
// Use NewServerWithCORS to explicitly allow additional origins.
func NewServer(port int) *Server {
	return NewServerWithCORS(port, nil)
}

// NewServerWithCORS creates a new HTTP server with standard middleware and configurable CORS.
// If allowedOrigins is nil or empty, only same-origin requests are allowed (no wildcard).
func NewServerWithCORS(port int, allowedOrigins []string) *Server {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	// Widget bundle is publicly embeddable — it must accept cross-origin GET
	// (and preflight) from any host so customers can <script src="…/widget.js">
	// on their own domains. The regular admin-API CORS policy (same-origin or
	// the configured allowlist) deliberately does NOT cover it; without this
	// split the preflight returns 200 but without Access-Control-Allow-Origin,
	// which browsers treat as a CORS failure.
	widgetCORS := cors.Handler(cors.Options{
		AllowedOrigins:   []string{"*"},
		AllowedMethods:   []string{"GET", "HEAD", "OPTIONS"},
		AllowedHeaders:   []string{"Content-Type"},
		ExposedHeaders:   []string{"Content-Length", "Content-Type"},
		AllowCredentials: false,
		MaxAge:           86400,
	})

	// Default is same-origin only — no wildcard fallback. The go-chi/cors
	// library treats an empty AllowedOrigins as "*"; explicitly deny all
	// origins via AllowOriginFunc to neutralize that. Same-origin requests
	// don't carry a CORS Origin header, so they pass through regardless.
	var apiCORS func(http.Handler) http.Handler
	if len(allowedOrigins) > 0 {
		apiCORS = cors.Handler(cors.Options{
			AllowedOrigins:   allowedOrigins,
			AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
			AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-BYOK-Provider", "X-BYOK-API-Key", "X-BYOK-Model", "X-BYOK-Base-URL"},
			ExposedHeaders:   []string{"Link", "X-RateLimit-Limit", "X-RateLimit-Remaining", "X-RateLimit-Reset", "Retry-After"},
			AllowCredentials: true,
			MaxAge:           300,
		})
	} else {
		apiCORS = cors.Handler(cors.Options{
			AllowOriginFunc: func(_ *http.Request, _ string) bool { return false },
			AllowedMethods:  []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
			AllowedHeaders:  []string{"Accept", "Authorization", "Content-Type", "X-BYOK-Provider", "X-BYOK-API-Key", "X-BYOK-Model", "X-BYOK-Base-URL"},
			MaxAge:          300,
		})
	}

	// Widget API CORS: same permissive origin policy as widgetCORS, but the
	// widget POSTs JSON + reads an SSE stream (chat) and GETs its bootstrap
	// config, each with a Bearer chat token — so it needs GET+POST, the
	// Authorization header, and the BYOK headers that the static-bundle policy
	// doesn't allow. AllowCredentials stays false (the token is a Bearer header,
	// not a cookie), so the wildcard origin is safe.
	widgetAPICORS := cors.Handler(cors.Options{
		AllowedOrigins:   []string{"*"},
		AllowedMethods:   []string{"GET", "POST", "OPTIONS"},
		AllowedHeaders:   []string{"Authorization", "Content-Type", "Accept", "X-BYOK-Provider", "X-BYOK-API-Key", "X-BYOK-Model", "X-BYOK-Base-URL"},
		ExposedHeaders:   []string{"Content-Type"},
		AllowCredentials: false,
		MaxAge:           86400,
	})

	// OAuth discovery + token/register endpoints are called by in-browser MCP
	// clients from arbitrary origins with bearer/none auth (no cookies), so they
	// need a wildcard origin. AllowCredentials stays false — the wildcard is safe
	// precisely because no ambient credentials ride along. The interactive
	// consent endpoints (authorize-info, approve) are deliberately EXCLUDED: they
	// ride the admin session and must keep the same-origin credentialed apiCORS.
	oauthCORS := cors.Handler(cors.Options{
		AllowedOrigins:   []string{"*"},
		AllowedMethods:   []string{"GET", "POST", "OPTIONS"},
		AllowedHeaders:   []string{"Content-Type", "Accept"},
		AllowCredentials: false,
		MaxAge:           86400,
	})

	// Dispatch CORS by path: widget static bundle and public widget chat
	// endpoint get permissive policies (embeddable on any customer site), the
	// OAuth discovery/token endpoints get a wildcard-no-credentials policy, and
	// everything else gets the admin-API policy.
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if strings.HasPrefix(req.URL.Path, widgetPathPrefix) {
				widgetCORS(next).ServeHTTP(w, req)
				return
			}
			if isPublicWidgetAPIPath(req.URL.Path) {
				widgetAPICORS(next).ServeHTTP(w, req)
				return
			}
			if isPublicOAuthPath(req.URL.Path) {
				oauthCORS(next).ServeHTTP(w, req)
				return
			}
			apiCORS(next).ServeHTTP(w, req)
		})
	})

	return &Server{
		router: r,
		port:   port,
	}
}

// Router returns the chi router for registering routes.
func (s *Server) Router() chi.Router { return s.router }

// Start begins listening and serving HTTP requests. Blocks until shutdown.
func (s *Server) Start() error {
	s.httpServer = &http.Server{
		Addr:         fmt.Sprintf(":%d", s.port),
		Handler:      s.router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}
	slog.InfoContext(context.Background(), "HTTP server starting", "port", s.port)
	return s.httpServer.ListenAndServe()
}

// Shutdown gracefully stops the server.
func (s *Server) Shutdown(ctx context.Context) error {
	if s.httpServer == nil {
		return nil
	}
	return s.httpServer.Shutdown(ctx)
}
