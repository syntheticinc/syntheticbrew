package http

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// Test routers below mirror the production wiring in
// internal/app/routes_register.go (one Get/Post/etc per prod path) so test
// requests exercise the same chi route shapes the server registers at boot.
// Auth/scope middleware is intentionally omitted — these helpers are for
// handler-layer tests that want to skip the auth boilerplate.

func newAgentTestRouter(h *AgentHandler) http.Handler {
	r := chi.NewRouter()
	r.Get("/api/v1/agents", h.List)
	r.Post("/api/v1/agents", h.Create)
	r.Get("/api/v1/agents/{name}", h.Get)
	r.Put("/api/v1/agents/{name}", h.Update)
	r.Patch("/api/v1/agents/{name}", h.Patch)
	r.Delete("/api/v1/agents/{name}", h.Delete)
	return r
}

func newModelTestRouter(h *ModelHandler) http.Handler {
	r := chi.NewRouter()
	r.Get("/api/v1/models", h.List)
	r.Post("/api/v1/models", h.Create)
	r.Put("/api/v1/models/{name}", h.Update)
	r.Patch("/api/v1/models/{name}", h.Patch)
	r.Delete("/api/v1/models/{name}", h.Delete)
	r.Post("/api/v1/models/{name}/verify", h.Verify)
	return r
}

func newMCPTestRouterFull(h *MCPHandler) http.Handler {
	r := chi.NewRouter()
	r.Get("/api/v1/mcp-servers", h.List)
	r.Post("/api/v1/mcp-servers", h.Create)
	r.Put("/api/v1/mcp-servers/{name}", h.Update)
	r.Patch("/api/v1/mcp-servers/{name}", h.Patch)
	r.Delete("/api/v1/mcp-servers/{name}", h.Delete)
	r.Post("/api/v1/mcp-servers/{name}/refresh", h.Refresh)
	return r
}

func newSchemaTestRouter(h *SchemaHandler) http.Handler {
	r := chi.NewRouter()
	r.Get("/api/v1/schemas", h.ListSchemas)
	r.Post("/api/v1/schemas", h.CreateSchema)
	r.Get("/api/v1/schemas/{name}", h.GetSchema)
	r.Put("/api/v1/schemas/{name}", h.UpdateSchema)
	r.Patch("/api/v1/schemas/{name}", h.PatchSchema)
	r.Delete("/api/v1/schemas/{name}", h.DeleteSchema)
	r.Get("/api/v1/schemas/{name}/agents", h.ListSchemaAgents)
	r.Get("/api/v1/schemas/{name}/agent-relations", h.ListAgentRelations)
	r.Post("/api/v1/schemas/{name}/agent-relations", h.CreateAgentRelation)
	r.Get("/api/v1/schemas/{name}/agent-relations/{relationId}", h.GetAgentRelation)
	r.Put("/api/v1/schemas/{name}/agent-relations/{relationId}", h.UpdateAgentRelation)
	r.Delete("/api/v1/schemas/{name}/agent-relations/{relationId}", h.DeleteAgentRelation)
	return r
}

func newSessionTestRouter(h *SessionHandler) http.Handler {
	r := chi.NewRouter()
	r.Get("/api/v1/sessions", h.List)
	r.Post("/api/v1/sessions", h.Create)
	r.Get("/api/v1/sessions/{id}", h.Get)
	r.Get("/api/v1/sessions/{id}/messages", h.ListMessages)
	r.Put("/api/v1/sessions/{id}", h.Update)
	r.Delete("/api/v1/sessions/{id}", h.Delete)
	return r
}

func newTaskTestRouter(h *TaskHandler) http.Handler {
	r := chi.NewRouter()
	r.Post("/api/v1/tasks", h.Create)
	r.Get("/api/v1/tasks", h.List)
	r.Get("/api/v1/tasks/{id}", h.Get)
	r.Delete("/api/v1/tasks/{id}", h.Cancel)
	r.Get("/api/v1/tasks/{id}/subtasks", h.ListSubtasks)
	r.Post("/api/v1/tasks/{id}/approve", h.Approve)
	r.Post("/api/v1/tasks/{id}/start", h.Start)
	r.Post("/api/v1/tasks/{id}/complete", h.Complete)
	r.Post("/api/v1/tasks/{id}/fail", h.Fail)
	r.Post("/api/v1/tasks/{id}/priority", h.SetPriority)
	return r
}
