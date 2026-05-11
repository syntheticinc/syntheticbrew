package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"

	deliveryhttp "github.com/syntheticinc/bytebrew/engine/internal/delivery/http"
	"github.com/syntheticinc/bytebrew/engine/internal/domain"
	"github.com/syntheticinc/bytebrew/engine/internal/infrastructure/persistence/configrepo"
	"github.com/syntheticinc/bytebrew/engine/internal/infrastructure/persistence/models"
	"github.com/syntheticinc/bytebrew/engine/internal/infrastructure/tools"
	pkgerrors "github.com/syntheticinc/bytebrew/engine/pkg/errors"
	"github.com/syntheticinc/bytebrew/engine/pkg/config"
)

// mcpServiceHTTPAdapter bridges GORMMCPServerRepository to the http.MCPService interface.
type mcpServiceHTTPAdapter struct {
	repo *configrepo.GORMMCPServerRepository
}

func (a *mcpServiceHTTPAdapter) ListMCPServers(ctx context.Context) ([]deliveryhttp.MCPServerResponse, error) {
	servers, err := a.repo.List(ctx)
	if err != nil {
		return nil, err
	}

	// Batch-load agent names for all servers (single query, no N+1)
	serverIDs := make([]string, 0, len(servers))
	for _, s := range servers {
		serverIDs = append(serverIDs, s.ID)
	}
	agentsByServer, err := a.repo.GetAgentNamesByServerIDs(ctx, serverIDs)
	if err != nil {
		return nil, fmt.Errorf("load agents for mcp servers: %w", err)
	}

	result := make([]deliveryhttp.MCPServerResponse, 0, len(servers))
	for _, s := range servers {
		agents := agentsByServer[s.ID]
		if agents == nil {
			agents = []string{}
		}
		resp := deliveryhttp.MCPServerResponse{
			ID:           s.ID,
			Name:         s.Name,
			Type:         s.Type,
			Command:      s.Command,
			URL:          s.URL,
			AuthType:     s.AuthType,
			AuthKeyEnv:   s.AuthKeyEnv,
			AuthTokenEnv: s.AuthTokenEnv,
			AuthClientID: s.AuthClientID,
			Agents:       agents,
		}
		if s.Args != nil && *s.Args != "" {
			_ = json.Unmarshal([]byte(*s.Args), &resp.Args)
		}
		if s.EnvVars != nil && *s.EnvVars != "" {
			_ = json.Unmarshal([]byte(*s.EnvVars), &resp.EnvVars)
		}
		if s.ForwardHeaders != nil && *s.ForwardHeaders != "" {
			_ = json.Unmarshal([]byte(*s.ForwardHeaders), &resp.ForwardHeaders)
		}
		// V2 Commit Group C (§5.6): connection status is no longer persisted
		// — callers query the live MCP client registry separately.
		result = append(result, resp)
	}
	return result, nil
}

func (a *mcpServiceHTTPAdapter) CreateMCPServer(ctx context.Context, req deliveryhttp.CreateMCPServerRequest) (*deliveryhttp.MCPServerResponse, error) {
	model := &models.MCPServerModel{
		Name:         req.Name,
		Type:         req.Type,
		Command:      req.Command,
		URL:          req.URL,
		AuthType:     req.AuthType,
		AuthKeyEnv:   req.AuthKeyEnv,
		AuthTokenEnv: req.AuthTokenEnv,
		AuthClientID: req.AuthClientID,
	}
	if len(req.Args) > 0 {
		data, _ := json.Marshal(req.Args)
		s := string(data)
		model.Args = &s
	}
	if len(req.EnvVars) > 0 {
		data, _ := json.Marshal(req.EnvVars)
		s := string(data)
		model.EnvVars = &s
	}
	if len(req.ForwardHeaders) > 0 {
		data, _ := json.Marshal(req.ForwardHeaders)
		s := string(data)
		model.ForwardHeaders = &s
	}
	if err := a.repo.Create(ctx, model); err != nil {
		if strings.Contains(err.Error(), "duplicate key") || strings.Contains(err.Error(), "unique constraint") || strings.Contains(err.Error(), "UNIQUE constraint") {
			return nil, pkgerrors.AlreadyExists(fmt.Sprintf("mcp server with name %q already exists", req.Name))
		}
		return nil, err
	}
	return &deliveryhttp.MCPServerResponse{
		ID:             model.ID,
		Name:           model.Name,
		Type:           model.Type,
		Command:        model.Command,
		URL:            model.URL,
		AuthType:       model.AuthType,
		AuthKeyEnv:     model.AuthKeyEnv,
		AuthTokenEnv:   model.AuthTokenEnv,
		AuthClientID:   model.AuthClientID,
		Args:           req.Args,
		EnvVars:        req.EnvVars,
		ForwardHeaders: req.ForwardHeaders,
		Agents:         []string{},
	}, nil
}

func (a *mcpServiceHTTPAdapter) UpdateMCPServer(ctx context.Context, name string, req deliveryhttp.CreateMCPServerRequest) (*deliveryhttp.MCPServerResponse, error) {
	servers, err := a.repo.List(ctx)
	if err != nil {
		return nil, err
	}
	var targetID string
	for _, s := range servers {
		if s.Name == name {
			targetID = s.ID
			break
		}
	}
	if targetID == "" {
		return nil, pkgerrors.NotFound(fmt.Sprintf("mcp server not found: %s", name))
	}

	model := &models.MCPServerModel{
		Name:         req.Name,
		Type:         req.Type,
		Command:      req.Command,
		URL:          req.URL,
		AuthType:     req.AuthType,
		AuthKeyEnv:   req.AuthKeyEnv,
		AuthTokenEnv: req.AuthTokenEnv,
		AuthClientID: req.AuthClientID,
	}
	if len(req.Args) > 0 {
		data, _ := json.Marshal(req.Args)
		s := string(data)
		model.Args = &s
	}
	if len(req.EnvVars) > 0 {
		data, _ := json.Marshal(req.EnvVars)
		s := string(data)
		model.EnvVars = &s
	}
	if len(req.ForwardHeaders) > 0 {
		data, _ := json.Marshal(req.ForwardHeaders)
		s := string(data)
		model.ForwardHeaders = &s
	}
	if err := a.repo.Update(ctx, targetID, model); err != nil {
		return nil, err
	}

	updated, err := a.repo.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, s := range updated {
		if s.ID == targetID {
			agents, err := a.repo.GetAgentNamesForServer(ctx, targetID)
			if err != nil {
				return nil, fmt.Errorf("load agents for mcp server: %w", err)
			}
			if agents == nil {
				agents = []string{}
			}
			resp := &deliveryhttp.MCPServerResponse{
				ID:           s.ID,
				Name:         s.Name,
				Type:         s.Type,
				Command:      s.Command,
				URL:          s.URL,
				AuthType:     s.AuthType,
				AuthKeyEnv:   s.AuthKeyEnv,
				AuthTokenEnv: s.AuthTokenEnv,
				AuthClientID: s.AuthClientID,
				Agents:       agents,
			}
			if s.Args != nil && *s.Args != "" {
				_ = json.Unmarshal([]byte(*s.Args), &resp.Args)
			}
			if s.EnvVars != nil && *s.EnvVars != "" {
				_ = json.Unmarshal([]byte(*s.EnvVars), &resp.EnvVars)
			}
			if s.ForwardHeaders != nil && *s.ForwardHeaders != "" {
				_ = json.Unmarshal([]byte(*s.ForwardHeaders), &resp.ForwardHeaders)
			}
			// V2 Commit Group C (§5.6): live status no longer persisted.
			return resp, nil
		}
	}
	return nil, pkgerrors.NotFound(fmt.Sprintf("mcp server not found after update: %s", name))
}

// PatchMCPServer applies only the non-nil fields in req to the existing MCP server.
func (a *mcpServiceHTTPAdapter) PatchMCPServer(ctx context.Context, name string, req deliveryhttp.UpdateMCPServerRequest) (*deliveryhttp.MCPServerResponse, error) {
	servers, err := a.repo.List(ctx)
	if err != nil {
		return nil, err
	}
	var existing *models.MCPServerModel
	for i := range servers {
		if servers[i].Name == name {
			existing = &servers[i]
			break
		}
	}
	if existing == nil {
		return nil, pkgerrors.NotFound(fmt.Sprintf("mcp server not found: %s", name))
	}

	// Build update struct starting from existing values.
	update := &models.MCPServerModel{
		Name:         existing.Name,
		Type:         existing.Type,
		Command:      existing.Command,
		URL:          existing.URL,
		AuthType:     existing.AuthType,
		AuthKeyEnv:   existing.AuthKeyEnv,
		AuthTokenEnv: existing.AuthTokenEnv,
		AuthClientID: existing.AuthClientID,
		Args:         existing.Args,
		EnvVars:      existing.EnvVars,
		ForwardHeaders: existing.ForwardHeaders,
	}

	// Apply non-nil fields.
	if req.Name != nil {
		update.Name = *req.Name
	}
	if req.Type != nil {
		update.Type = *req.Type
	}
	if req.Command != nil {
		update.Command = *req.Command
	}
	if req.URL != nil {
		update.URL = *req.URL
	}
	if req.AuthType != nil {
		update.AuthType = *req.AuthType
	}
	if req.AuthKeyEnv != nil {
		update.AuthKeyEnv = *req.AuthKeyEnv
	}
	if req.AuthTokenEnv != nil {
		update.AuthTokenEnv = *req.AuthTokenEnv
	}
	if req.AuthClientID != nil {
		update.AuthClientID = *req.AuthClientID
	}
	if req.Args != nil {
		data, _ := json.Marshal(*req.Args)
		s := string(data)
		update.Args = &s
	}
	if req.EnvVars != nil {
		data, _ := json.Marshal(*req.EnvVars)
		s := string(data)
		update.EnvVars = &s
	}
	if req.ForwardHeaders != nil {
		data, _ := json.Marshal(*req.ForwardHeaders)
		s := string(data)
		update.ForwardHeaders = &s
	}

	if err := a.repo.Update(ctx, existing.ID, update); err != nil {
		return nil, err
	}

	agents, err := a.repo.GetAgentNamesForServer(ctx, existing.ID)
	if err != nil {
		return nil, fmt.Errorf("load agents for mcp server: %w", err)
	}
	if agents == nil {
		agents = []string{}
	}
	resp := &deliveryhttp.MCPServerResponse{
		ID:           existing.ID,
		Name:         update.Name,
		Type:         update.Type,
		Command:      update.Command,
		URL:          update.URL,
		AuthType:     update.AuthType,
		AuthKeyEnv:   update.AuthKeyEnv,
		AuthTokenEnv: update.AuthTokenEnv,
		AuthClientID: update.AuthClientID,
		Agents:       agents,
	}
	if update.Args != nil && *update.Args != "" {
		_ = json.Unmarshal([]byte(*update.Args), &resp.Args)
	}
	if update.EnvVars != nil && *update.EnvVars != "" {
		_ = json.Unmarshal([]byte(*update.EnvVars), &resp.EnvVars)
	}
	if update.ForwardHeaders != nil && *update.ForwardHeaders != "" {
		_ = json.Unmarshal([]byte(*update.ForwardHeaders), &resp.ForwardHeaders)
	}
	return resp, nil
}

func (a *mcpServiceHTTPAdapter) DeleteMCPServer(ctx context.Context, name string) error {
	servers, err := a.repo.List(ctx)
	if err != nil {
		return err
	}
	for _, s := range servers {
		if s.Name == name {
			return a.repo.Delete(ctx, s.ID)
		}
	}
	return pkgerrors.NotFound(fmt.Sprintf("mcp server not found: %s", name))
}

// ptrString converts a string to *string; returns nil when v == "" (no reference).
func ptrString(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}

// derefString dereferences a *string; returns "" when p is nil.
func derefString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// settingServiceHTTPAdapter bridges GORMSettingRepository to the http.SettingService interface.
// byokMW, db, and fallback are optional — when set, any write to a byok.*
// key triggers a live SetConfig so the middleware hot-swaps without restart.
type settingServiceHTTPAdapter struct {
	repo        *configrepo.GORMSettingRepository
	byokMW      *deliveryhttp.BYOKMiddleware
	db          *gorm.DB
	byokFallback config.BYOKConfig
}

// settingValueAsString renders a jsonb value for the HTTP layer:
//   - jsonb string ("foo") → unwrapped Go string foo
//   - any other jsonb (number, bool, array, object) → raw JSON text
//
// This keeps the wire shape stable for the existing
// SettingResponse.Value:string contract while allowing structured values
// (byok.allowed_providers as a real array) to round-trip as JSON text.
func settingValueAsString(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return string(raw)
}

func (a *settingServiceHTTPAdapter) ListSettings(ctx context.Context) ([]deliveryhttp.SettingResponse, error) {
	settings, err := a.repo.List(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]deliveryhttp.SettingResponse, 0, len(settings))
	for _, s := range settings {
		result = append(result, deliveryhttp.SettingResponse{
			Key:       s.Key,
			Value:     settingValueAsString(s.Value),
			UpdatedAt: s.UpdatedAt.Format(time.RFC3339),
		})
	}
	return result, nil
}

func (a *settingServiceHTTPAdapter) UpdateSetting(ctx context.Context, key, value string) (*deliveryhttp.SettingResponse, error) {
	if err := a.repo.Set(ctx, key, value); err != nil {
		return nil, err
	}
	setting, err := a.repo.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	if a.byokMW != nil && strings.HasPrefix(key, "byok.") {
		cfg := loadBYOKConfig(ctx, a.db, a.byokFallback)
		a.byokMW.SetConfig(deliveryhttp.BYOKConfig{
			Enabled:          cfg.Enabled,
			AllowedProviders: cfg.AllowedProviders,
		})
	}
	return &deliveryhttp.SettingResponse{
		Key:       setting.Key,
		Value:     settingValueAsString(setting.Value),
		UpdatedAt: setting.UpdatedAt.Format(time.RFC3339),
	}, nil
}

// sessionServiceHTTPAdapter bridges GORMSessionRepository to the http.SessionService interface.
type sessionServiceHTTPAdapter struct {
	repo        *configrepo.GORMSessionRepository
	messageRepo *configrepo.GORMEventRepository
	// db is used by resolveSchemaRef to translate operator-declared schema
	// names into UUIDs on POST /api/v1/sessions. The handler accepts both
	// schema UUID and schema name in the `schema_id` body field — engine
	// resolves on its side so GitOps consumers don't have to pre-call
	// GET /schemas on cold start (1.1.5 fix, symmetric with the 1.1.3
	// CreateSchema entry_agent resolver).
	db *gorm.DB
}

// resolveSchemaRef returns the schema UUID for a tenant-scoped reference.
// Accepts either a UUID (validated against tenant ownership) or a name
// (resolved via the per-tenant unique-name index). Mirrors the
// resolveAgentModel pattern from agent_manager_http_adapter.go: explicit
// tenant filter in WHERE, InvalidInput on miss (mapped to 400 by
// writeDomainError, not 500 SQL leakage). Empty input returns "" — caller
// decides whether NULL FK is acceptable; sessions.schema_id is NOT NULL so
// the DB constraint will catch empty inserts.
func (a *sessionServiceHTTPAdapter) resolveSchemaRef(ctx context.Context, ref string) (string, error) {
	if ref == "" {
		return "", nil
	}
	tenantID := domain.TenantIDFromContext(ctx)
	if tenantID == "" {
		tenantID = domain.CETenantID
	}
	if isUUID(ref) {
		// Verify tenant ownership; cross-tenant UUID guess returns 400
		// (info hiding — same response as truly-not-found name).
		var found string
		err := a.db.WithContext(ctx).
			Raw("SELECT id FROM schemas WHERE id = ? AND tenant_id = ? LIMIT 1", ref, tenantID).
			Scan(&found).Error
		if err != nil || found == "" {
			return "", pkgerrors.InvalidInput(fmt.Sprintf("schema not found: %s", ref))
		}
		return ref, nil
	}
	var id string
	err := a.db.WithContext(ctx).
		Raw("SELECT id FROM schemas WHERE tenant_id = ? AND name = ? LIMIT 1", tenantID, ref).
		Scan(&id).Error
	if err != nil || id == "" {
		return "", pkgerrors.InvalidInput(fmt.Sprintf("schema not found: %s", ref))
	}
	return id, nil
}

// sessionToResponse maps the persistence model into the API DTO. Metadata is
// returned as-is when non-empty so clients receive their own opaque blob;
// the column default `'{}'::jsonb` round-trips as `{}` JSON.
func sessionToResponse(s *models.SessionModel) deliveryhttp.SessionResponse {
	resp := deliveryhttp.SessionResponse{
		ID:        s.ID,
		Title:     s.Title,
		SchemaID:  s.SchemaID,
		UserSub:   s.UserSub,
		Status:    s.Status,
		CreatedAt: s.CreatedAt.Format(time.RFC3339),
		UpdatedAt: s.UpdatedAt.Format(time.RFC3339),
	}
	if len(s.Metadata) > 0 {
		resp.Metadata = json.RawMessage(s.Metadata)
	}
	return resp
}

func (a *sessionServiceHTTPAdapter) ListSessions(ctx context.Context, agentName, userSub, status, from, to string, page, perPage int) ([]deliveryhttp.SessionResponse, int64, error) {
	sessions, total, err := a.repo.List(ctx, agentName, userSub, status, from, to, page, perPage)
	if err != nil {
		return nil, 0, err
	}
	result := make([]deliveryhttp.SessionResponse, 0, len(sessions))
	for i := range sessions {
		result = append(result, sessionToResponse(&sessions[i]))
	}
	return result, total, nil
}

func (a *sessionServiceHTTPAdapter) GetSession(ctx context.Context, id string) (*deliveryhttp.SessionResponse, error) {
	s, err := a.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if s == nil {
		return nil, nil
	}
	resp := sessionToResponse(s)
	return &resp, nil
}

func (a *sessionServiceHTTPAdapter) CreateSession(ctx context.Context, req deliveryhttp.CreateSessionRequest) (*deliveryhttp.SessionResponse, error) {
	id := req.ID
	if id == "" {
		id = uuid.New().String()
	}
	schemaID, err := a.resolveSchemaRef(ctx, req.SchemaID)
	if err != nil {
		return nil, err
	}
	session := &models.SessionModel{
		ID:       id,
		Title:    req.Title,
		SchemaID: schemaID,
		UserSub:  req.UserSub,
		Status:   "active",
	}
	if len(req.Metadata) > 0 {
		session.Metadata = datatypes.JSON(req.Metadata)
	}
	if err := a.repo.Create(ctx, session); err != nil {
		return nil, err
	}
	resp := sessionToResponse(session)
	return &resp, nil
}

func (a *sessionServiceHTTPAdapter) UpdateSession(ctx context.Context, id string, req deliveryhttp.UpdateSessionRequest) (*deliveryhttp.SessionResponse, error) {
	updates := make(map[string]interface{})
	if req.Title != nil {
		updates["title"] = *req.Title
	}
	if req.Status != nil {
		updates["status"] = *req.Status
	}
	if len(req.Metadata) > 0 {
		updates["metadata"] = datatypes.JSON(req.Metadata)
	}
	if len(updates) == 0 {
		return a.GetSession(ctx, id)
	}
	if err := a.repo.Update(ctx, id, updates); err != nil {
		return nil, err
	}
	return a.GetSession(ctx, id)
}

func (a *sessionServiceHTTPAdapter) DeleteSession(ctx context.Context, id string) error {
	if a.messageRepo != nil {
		_ = a.messageRepo.DeleteBySession(ctx, id)
	}
	return a.repo.Delete(ctx, id)
}

// eventServiceHTTPAdapter bridges GORMEventRepository to the http.EventService interface.
type eventServiceHTTPAdapter struct {
	repo *configrepo.GORMEventRepository
}

func (a *eventServiceHTTPAdapter) ListEvents(ctx context.Context, sessionID string) ([]deliveryhttp.EventResponse, error) {
	events, err := a.repo.ListBySession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	result := make([]deliveryhttp.EventResponse, 0, len(events))
	for _, ev := range events {
		agentID := ""
		if ev.AgentID != nil {
			agentID = *ev.AgentID
		}
		result = append(result, deliveryhttp.EventResponse{
			ID:        ev.ID,
			EventType: ev.EventType,
			AgentID:   agentID,
			CallID:    ev.CallID,
			Payload:   ev.Payload,
			CreatedAt: ev.CreatedAt.Format(time.RFC3339),
		})
	}
	return result, nil
}

// toolMetadataHTTPAdapter bridges tools.GetAllToolMetadata to the http.ToolMetadataProvider interface.
type toolMetadataHTTPAdapter struct{}

func (a *toolMetadataHTTPAdapter) GetAllToolMetadata() []deliveryhttp.ToolMetadataResponse {
	all := tools.GetAllToolMetadata()
	result := make([]deliveryhttp.ToolMetadataResponse, len(all))
	for i, m := range all {
		result[i] = deliveryhttp.ToolMetadataResponse{
			Name:         m.Name,
			Description:  m.Description,
			SecurityZone: string(m.SecurityZone),
			RiskWarning:  m.RiskWarning,
		}
	}
	return result
}

