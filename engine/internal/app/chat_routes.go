package app

import (
	"github.com/go-chi/chi/v5"

	deliveryhttp "github.com/syntheticinc/syntheticbrew/internal/delivery/http"
)

// chatRoutesDeps bundles the dependencies needed to mount the chat-facing
// HTTP routes (schema chat, admin assistant, secondary agent list).
type chatRoutesDeps struct {
	AuthMW                *deliveryhttp.AuthMiddleware
	BYOKMW                *deliveryhttp.BYOKMiddleware
	ChatHandler           *deliveryhttp.ChatHandler
	AdminAssistantHandler *deliveryhttp.AdminAssistantHandler
	AgentManagerExt       *agentManagerHTTPAdapter
	WidgetConfigHandler   *deliveryhttp.WidgetConfigHandler
}

// mountChatRoutes registers the schema chat endpoint under auth + BYOK +
// ScopeChat middleware. Per-IP rate limiting is an edge concern — configured
// on the reverse proxy (Caddy/nginx/traefik) in front of the engine.
// See docs/deployment/rate-limiting.md for snippets.
func mountChatRoutes(router chi.Router, deps chatRoutesDeps) {
	router.Group(func(r chi.Router) {
		if deps.AuthMW != nil {
			r.Use(deps.AuthMW.Authenticate)
		}
		// BYOK runs AFTER auth so unauthenticated traffic never reaches
		// the header-parsing path; the LLM factory reads ContextKeyBYOK*
		// from the request context to pick tenant-configured vs
		// user-supplied credentials.
		if deps.BYOKMW != nil {
			r.Use(deps.BYOKMW.InjectBYOK)
		}
		r.Group(func(r chi.Router) {
			if deps.AuthMW != nil {
				r.Use(deliveryhttp.RequireScope(deliveryhttp.ScopeChat))
			}
			r.Post("/api/v1/schemas/{name}/chat", deps.ChatHandler.Chat)
			// Widget bootstrap config — same ScopeChat group as the chat POST so
			// the embedded widget can read its render toggles with the token it
			// already holds. Carries no tenant-identifying data.
			if deps.WidgetConfigHandler != nil {
				r.Get("/api/v1/widget-config", deps.WidgetConfigHandler.Get)
			}
		})
	})
}

// mountAdminAssistantRoutes registers the admin assistant endpoints under
// auth + ScopeAgentsRead. Tenant-aware schema resolution lives in
// builder_assistant.go (`NewBuilderSchemaResolver`).
func mountAdminAssistantRoutes(router chi.Router, deps chatRoutesDeps) {
	router.Group(func(r chi.Router) {
		if deps.AuthMW != nil {
			r.Use(deps.AuthMW.Authenticate)
			r.Use(deliveryhttp.RequireScope(deliveryhttp.ScopeAgentsRead))
		}
		r.Post("/api/v1/admin/assistant/chat", deps.AdminAssistantHandler.Chat)
		r.Get("/api/v1/admin/assistant/last-session", deps.AdminAssistantHandler.LastSession)
	})
}

// mountSecondaryAgentList registers the read-only /api/v1/agents endpoint on
// the external router. Used in two-port mode where the canonical agent CRUD
// lives on the internal admin router, but the external router still needs a
// list endpoint for chat clients (web client / widget) that show an agent
// picker.
func mountSecondaryAgentList(router chi.Router, deps chatRoutesDeps) {
	router.Group(func(r chi.Router) {
		if deps.AuthMW != nil {
			r.Use(deps.AuthMW.Authenticate)
			r.Use(deliveryhttp.RequireScope(deliveryhttp.ScopeAgentsRead))
		}
		r.Get("/api/v1/agents", deliveryhttp.NewAgentHandlerWithManager(deps.AgentManagerExt).List)
	})
}
