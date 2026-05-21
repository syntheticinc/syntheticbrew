package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/configrepo"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/models"
	admintools "github.com/syntheticinc/syntheticbrew/internal/infrastructure/tools/admin"
	"gorm.io/gorm"
)

// --- Agent adapter ---

type adminAgentRepoAdapter struct {
	repo *configrepo.GORMAgentRepository
}

func newAdminAgentRepoAdapter(repo *configrepo.GORMAgentRepository) *adminAgentRepoAdapter {
	return &adminAgentRepoAdapter{repo: repo}
}

func (a *adminAgentRepoAdapter) List(ctx context.Context) ([]admintools.AgentRecord, error) {
	records, err := a.repo.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]admintools.AgentRecord, 0, len(records))
	for _, r := range records {
		out = append(out, toAdminAgentRecord(r))
	}
	return out, nil
}

func (a *adminAgentRepoAdapter) GetByName(ctx context.Context, name string) (*admintools.AgentRecord, error) {
	rec, err := a.repo.GetByName(ctx, name)
	if err != nil {
		return nil, err
	}
	out := toAdminAgentRecord(*rec)
	return &out, nil
}

func (a *adminAgentRepoAdapter) Create(ctx context.Context, record *admintools.AgentRecord) error {
	cr := fromAdminAgentRecord(record)
	return a.repo.Create(ctx, &cr)
}

func (a *adminAgentRepoAdapter) Update(ctx context.Context, name string, record *admintools.AgentRecord) error {
	cr := fromAdminAgentRecord(record)
	return a.repo.Update(ctx, name, &cr)
}

func (a *adminAgentRepoAdapter) Delete(ctx context.Context, name string) error {
	return a.repo.Delete(ctx, name)
}

func toAdminAgentRecord(r configrepo.AgentRecord) admintools.AgentRecord {
	return admintools.AgentRecord{
		ID:            r.ID,
		Name:          r.Name,
		SystemPrompt:  r.SystemPrompt,
		ModelName:     r.ModelName,
		Lifecycle:     r.Lifecycle,
		ToolExecution: r.ToolExecution,
		MaxSteps:      r.MaxSteps,
		BuiltinTools:  r.BuiltinTools,
		MCPServers:    r.MCPServers,
		CanSpawn:      r.CanSpawn,
		IsSystem:      r.IsSystem,
	}
}

func fromAdminAgentRecord(r *admintools.AgentRecord) configrepo.AgentRecord {
	return configrepo.AgentRecord{
		Name:          r.Name,
		SystemPrompt:  r.SystemPrompt,
		ModelName:     r.ModelName,
		Lifecycle:     r.Lifecycle,
		ToolExecution: r.ToolExecution,
		MaxSteps:      r.MaxSteps,
		BuiltinTools:  r.BuiltinTools,
		MCPServers:    r.MCPServers,
		CanSpawn:      r.CanSpawn,
		IsSystem:      r.IsSystem,
	}
}

// --- Schema adapter ---

type adminSchemaRepoAdapter struct {
	repo *configrepo.GORMSchemaRepository
}

func newAdminSchemaRepoAdapter(repo *configrepo.GORMSchemaRepository) *adminSchemaRepoAdapter {
	return &adminSchemaRepoAdapter{repo: repo}
}

func (a *adminSchemaRepoAdapter) List(ctx context.Context) ([]admintools.SchemaRecord, error) {
	records, err := a.repo.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]admintools.SchemaRecord, 0, len(records))
	for _, r := range records {
		out = append(out, admintools.SchemaRecord{
			ID:          r.ID,
			Name:        r.Name,
			Description: r.Description,
			AgentNames:  r.AgentNames,
		})
	}
	return out, nil
}

func (a *adminSchemaRepoAdapter) GetByID(ctx context.Context, id string) (*admintools.SchemaRecord, error) {
	rec, err := a.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	return &admintools.SchemaRecord{
		ID:          rec.ID,
		Name:        rec.Name,
		Description: rec.Description,
		AgentNames:  rec.AgentNames,
	}, nil
}

func (a *adminSchemaRepoAdapter) Create(ctx context.Context, record *admintools.SchemaRecord) error {
	cr := &configrepo.SchemaRecord{
		Name:        record.Name,
		Description: record.Description,
	}
	if err := a.repo.Create(ctx, cr); err != nil {
		return err
	}
	record.ID = cr.ID
	return nil
}

// Update applies the name/description/entry_agent_id/chat_enabled overrides
// from the admin tool. Optional pointer fields on the admin record preserve
// existing values when nil — the concrete SchemaRecord the GORM repo consumes
// has value-typed fields that map directly to UPDATE columns, so we have to
// merge by first loading the current row.
func (a *adminSchemaRepoAdapter) Update(ctx context.Context, id string, record *admintools.SchemaRecord) error {
	existing, err := a.repo.GetByID(ctx, id)
	if err != nil {
		return err
	}

	cr := &configrepo.SchemaRecord{
		Name:         existing.Name,
		Description:  existing.Description,
		EntryAgentID: existing.EntryAgentID,
		ChatEnabled:  existing.ChatEnabled,
	}
	if record.Name != "" {
		cr.Name = record.Name
	}
	if record.Description != "" {
		cr.Description = record.Description
	}
	if record.EntryAgentID != nil {
		if *record.EntryAgentID == "" {
			cr.EntryAgentID = nil
		} else {
			resolved := *record.EntryAgentID
			cr.EntryAgentID = &resolved
		}
	}
	if record.ChatEnabled != nil {
		cr.ChatEnabled = *record.ChatEnabled
	}
	return a.repo.Update(ctx, id, cr)
}

func (a *adminSchemaRepoAdapter) Delete(ctx context.Context, id string) error {
	return a.repo.Delete(ctx, id)
}

// --- MCP Server adapter ---

type adminMCPServerRepoAdapter struct {
	repo *configrepo.GORMMCPServerRepository
}

func newAdminMCPServerRepoAdapter(repo *configrepo.GORMMCPServerRepository) *adminMCPServerRepoAdapter {
	return &adminMCPServerRepoAdapter{repo: repo}
}

func (a *adminMCPServerRepoAdapter) List(ctx context.Context) ([]admintools.MCPServerRecord, error) {
	servers, err := a.repo.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]admintools.MCPServerRecord, 0, len(servers))
	for _, s := range servers {
		out = append(out, toAdminMCPServerRecord(s))
	}
	return out, nil
}

func (a *adminMCPServerRepoAdapter) GetByID(ctx context.Context, id string) (*admintools.MCPServerRecord, error) {
	s, err := a.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	rec := toAdminMCPServerRecord(*s)
	return &rec, nil
}

func (a *adminMCPServerRepoAdapter) Create(ctx context.Context, record *admintools.MCPServerRecord) error {
	m := &models.MCPServerModel{
		Name:    record.Name,
		Type:    record.Type,
		Command: record.Command,
		URL:     record.URL,
		Args:    marshalJSONPtr(record.Args),
		EnvVars: marshalJSONPtr(record.EnvVars),
		Enabled: record.Enabled,
	}
	if err := a.repo.Create(ctx, m); err != nil {
		return err
	}
	record.ID = m.ID
	return nil
}

func (a *adminMCPServerRepoAdapter) Update(ctx context.Context, id string, record *admintools.MCPServerRecord) error {
	m := &models.MCPServerModel{
		Name:    record.Name,
		Type:    record.Type,
		Command: record.Command,
		URL:     record.URL,
		Args:    marshalJSONPtr(record.Args),
		EnvVars: marshalJSONPtr(record.EnvVars),
		Enabled: record.Enabled,
	}
	return a.repo.Update(ctx, id, m)
}

func (a *adminMCPServerRepoAdapter) Delete(ctx context.Context, id string) error {
	return a.repo.Delete(ctx, id)
}

func toAdminMCPServerRecord(s models.MCPServerModel) admintools.MCPServerRecord {
	var args []string
	if s.Args != nil && *s.Args != "" {
		_ = json.Unmarshal([]byte(*s.Args), &args)
	}
	var envVars map[string]string
	if s.EnvVars != nil && *s.EnvVars != "" {
		_ = json.Unmarshal([]byte(*s.EnvVars), &envVars)
	}
	return admintools.MCPServerRecord{
		ID:      s.ID,
		Name:    s.Name,
		Type:    s.Type,
		Command: s.Command,
		URL:     s.URL,
		Args:    args,
		EnvVars: envVars,
		Enabled: s.Enabled,
	}
}

// marshalJSONPtr marshals v to a JSON string pointer; returns nil if v is nil or empty slice/map.
func marshalJSONPtr(v interface{}) *string {
	if v == nil {
		return nil
	}
	data, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	s := string(data)
	if s == "null" || s == "[]" || s == "{}" {
		return nil
	}
	return &s
}

// --- Model (LLM Provider) adapter ---

type adminModelRepoAdapter struct {
	repo *configrepo.GORMLLMProviderRepository
}

func newAdminModelRepoAdapter(repo *configrepo.GORMLLMProviderRepository) *adminModelRepoAdapter {
	return &adminModelRepoAdapter{repo: repo}
}

func (a *adminModelRepoAdapter) List(ctx context.Context) ([]admintools.ModelRecord, error) {
	providers, err := a.repo.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]admintools.ModelRecord, 0, len(providers))
	for _, p := range providers {
		out = append(out, toAdminModelRecord(p))
	}
	return out, nil
}

func (a *adminModelRepoAdapter) GetByID(ctx context.Context, id string) (*admintools.ModelRecord, error) {
	p, err := a.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	rec := toAdminModelRecord(*p)
	return &rec, nil
}

func (a *adminModelRepoAdapter) Create(ctx context.Context, record *admintools.ModelRecord) error {
	m := &models.LLMProviderModel{
		Name:            record.Name,
		Type:            record.Type,
		BaseURL:         record.BaseURL,
		ModelName:       record.ModelName,
		APIKeyEncrypted: record.APIKey,
		IsDefault:       record.IsDefault,
	}
	if err := a.repo.Create(ctx, m); err != nil {
		return err
	}
	record.ID = m.ID
	return nil
}

func (a *adminModelRepoAdapter) Update(ctx context.Context, id string, record *admintools.ModelRecord) error {
	m := &models.LLMProviderModel{
		Name:      record.Name,
		Type:      record.Type,
		BaseURL:   record.BaseURL,
		ModelName: record.ModelName,
		IsDefault: record.IsDefault,
	}
	if record.APIKey != "" {
		m.APIKeyEncrypted = record.APIKey
	}
	return a.repo.Update(ctx, id, m)
}

func (a *adminModelRepoAdapter) Delete(ctx context.Context, id string) error {
	return a.repo.Delete(ctx, id)
}

// GetDefault exposes the tenant's default chat model to admin tools.
func (a *adminModelRepoAdapter) GetDefault(ctx context.Context) (*admintools.ModelRecord, error) {
	p, err := a.repo.GetDefault(ctx, "chat")
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, nil
	}
	rec := toAdminModelRecord(*p)
	return &rec, nil
}

// SetDefault promotes the given model ID to the tenant's default chat model
// — atomic swap, enforced by a partial unique index at the DB level.
func (a *adminModelRepoAdapter) SetDefault(ctx context.Context, id string) error {
	return a.repo.SetDefault(ctx, id)
}

func toAdminModelRecord(p models.LLMProviderModel) admintools.ModelRecord {
	apiKey := ""
	if p.APIKeyEncrypted != "" {
		apiKey = "***"
	}
	return admintools.ModelRecord{
		ID:        p.ID,
		Name:      p.Name,
		Type:      p.Type,
		BaseURL:   p.BaseURL,
		ModelName: p.ModelName,
		APIKey:    apiKey,
		IsDefault: p.IsDefault,
	}
}

// --- AgentRelation adapter ---

type adminAgentRelationRepoAdapter struct {
	repo      *configrepo.GORMAgentRelationRepository
	agentRepo *configrepo.GORMAgentRepository
}

func newAdminAgentRelationRepoAdapter(repo *configrepo.GORMAgentRelationRepository, agentRepo *configrepo.GORMAgentRepository) *adminAgentRelationRepoAdapter {
	return &adminAgentRelationRepoAdapter{repo: repo, agentRepo: agentRepo}
}

// resolveAgentRef accepts either an agent UUID or an agent name and returns
// the agent UUID. The admin tool surface (admin_create_agent_relation)
// exposes names to the LLM; the DB column is a uuid FK, so we must translate.
func (a *adminAgentRelationRepoAdapter) resolveAgentRef(ctx context.Context, ref string) (string, error) {
	if ref == "" {
		return "", fmt.Errorf("empty agent reference")
	}
	// Heuristic: UUIDs are 36 chars with dashes. Anything else must be a name.
	if len(ref) == 36 && strings.Count(ref, "-") == 4 {
		return ref, nil
	}
	rec, err := a.agentRepo.GetByName(ctx, ref)
	if err != nil {
		return "", fmt.Errorf("agent %q not found: %w", ref, err)
	}
	return rec.ID, nil
}

// agentNameByID resolves a UUID to a name for display. Falls back to the ID
// string when the agent row has been deleted but relations still reference it.
func (a *adminAgentRelationRepoAdapter) agentNameByID(ctx context.Context, id string, cache map[string]string) string {
	if n, ok := cache[id]; ok {
		return n
	}
	agents, err := a.agentRepo.List(ctx)
	if err != nil {
		cache[id] = id
		return id
	}
	for _, ag := range agents {
		cache[ag.ID] = ag.Name
	}
	if n, ok := cache[id]; ok {
		return n
	}
	cache[id] = id
	return id
}

func (a *adminAgentRelationRepoAdapter) List(ctx context.Context, schemaID string) ([]admintools.AgentRelationRecord, error) {
	records, err := a.repo.List(ctx, schemaID)
	if err != nil {
		return nil, err
	}
	nameCache := map[string]string{}
	out := make([]admintools.AgentRelationRecord, 0, len(records))
	for _, r := range records {
		label, _ := r.Config["label"].(string)
		out = append(out, admintools.AgentRelationRecord{
			ID:        r.ID,
			SchemaID:  r.SchemaID,
			FromAgent: a.agentNameByID(ctx, r.SourceAgentID, nameCache),
			ToAgent:   a.agentNameByID(ctx, r.TargetAgentID, nameCache),
			Label:     label,
		})
	}
	return out, nil
}

func (a *adminAgentRelationRepoAdapter) Create(ctx context.Context, record *admintools.AgentRelationRecord) error {
	sourceID, err := a.resolveAgentRef(ctx, record.FromAgent)
	if err != nil {
		return fmt.Errorf("resolve from_agent: %w", err)
	}
	targetID, err := a.resolveAgentRef(ctx, record.ToAgent)
	if err != nil {
		return fmt.Errorf("resolve to_agent: %w", err)
	}
	config := map[string]interface{}{}
	if record.Label != "" {
		config["label"] = record.Label
	}
	cr := &configrepo.AgentRelationRecord{
		SchemaID:      record.SchemaID,
		SourceAgentID: sourceID,
		TargetAgentID: targetID,
		Config:        config,
	}
	if err := a.repo.Create(ctx, cr); err != nil {
		return err
	}
	record.ID = cr.ID
	return nil
}

func (a *adminAgentRelationRepoAdapter) Delete(ctx context.Context, id string) error {
	return a.repo.Delete(ctx, id)
}

// --- Session adapter ---

type adminSessionRepoAdapter struct {
	repo *configrepo.GORMSessionRepository
}

func newAdminSessionRepoAdapter(repo *configrepo.GORMSessionRepository) *adminSessionRepoAdapter {
	return &adminSessionRepoAdapter{repo: repo}
}

func (a *adminSessionRepoAdapter) List(ctx context.Context) ([]admintools.SessionRecord, error) {
	sessions, _, err := a.repo.List(ctx, "", "", "", "", "", 1, 100)
	if err != nil {
		return nil, err
	}
	out := make([]admintools.SessionRecord, 0, len(sessions))
	for _, s := range sessions {
		out = append(out, admintools.SessionRecord{
			ID:        s.ID,
			UserID:    s.UserSub,
			StartedAt: s.CreatedAt.Format("2006-01-02T15:04:05Z"),
			Status:    s.Status,
		})
	}
	return out, nil
}

func (a *adminSessionRepoAdapter) GetByID(ctx context.Context, id string) (*admintools.SessionRecord, error) {
	s, err := a.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if s == nil {
		return nil, fmt.Errorf("session %q not found", id)
	}
	return &admintools.SessionRecord{
		ID:        s.ID,
		UserID:    s.UserSub,
		StartedAt: s.CreatedAt.Format("2006-01-02T15:04:05Z"),
		Status:    s.Status,
	}, nil
}

// --- Capability adapter ---

type adminCapabilityRepoAdapter struct {
	repo *configrepo.GORMCapabilityRepository
}

func newAdminCapabilityRepoAdapter(repo *configrepo.GORMCapabilityRepository) *adminCapabilityRepoAdapter {
	return &adminCapabilityRepoAdapter{repo: repo}
}

func (a *adminCapabilityRepoAdapter) ListByAgent(ctx context.Context, agentName string) ([]admintools.CapabilityRecord, error) {
	records, err := a.repo.ListByAgent(ctx, agentName)
	if err != nil {
		return nil, err
	}
	out := make([]admintools.CapabilityRecord, 0, len(records))
	for _, r := range records {
		out = append(out, admintools.CapabilityRecord{
			ID:        r.ID,
			AgentName: r.AgentName,
			Type:      r.Type,
			Config:    r.Config,
			Enabled:   r.Enabled,
		})
	}
	return out, nil
}

func (a *adminCapabilityRepoAdapter) Create(ctx context.Context, record *admintools.CapabilityRecord) error {
	cr := &configrepo.CapabilityRecord{
		AgentName: record.AgentName,
		Type:      record.Type,
		Config:    record.Config,
		Enabled:   record.Enabled,
	}
	if err := a.repo.Create(ctx, cr); err != nil {
		return err
	}
	record.ID = cr.ID
	return nil
}

func (a *adminCapabilityRepoAdapter) Update(ctx context.Context, id string, record *admintools.CapabilityRecord) error {
	cr := &configrepo.CapabilityRecord{
		AgentName: record.AgentName,
		Type:      record.Type,
		Config:    record.Config,
		Enabled:   record.Enabled,
	}
	return a.repo.Update(ctx, id, cr)
}

func (a *adminCapabilityRepoAdapter) Delete(ctx context.Context, id string) error {
	return a.repo.Delete(ctx, id)
}

// --- Builder-assistant restorer adapter ---

type builderAssistantRestorerAdapter struct {
	db       *gorm.DB
	registry interface{ Reload(ctx context.Context) error }
}

func (a *builderAssistantRestorerAdapter) RestoreBuilderAssistant(ctx context.Context) error {
	if err := restoreBuilderSchema(ctx, a.db); err != nil {
		return err
	}
	// Reload in-memory agent registry so restored tools are available at runtime.
	if a.registry != nil {
		if err := a.registry.Reload(ctx); err != nil {
			slog.WarnContext(ctx, "failed to reload registry after restore", "error", err)
		}
	}
	return nil
}

// --- Helpers ---

func resolveAgentID(ctx context.Context, db *gorm.DB, agentName string) (string, error) {
	var agent models.AgentModel
	if err := db.WithContext(ctx).Where("name = ?", agentName).First(&agent).Error; err != nil {
		return "", fmt.Errorf("find agent %q: %w", agentName, err)
	}
	return agent.ID, nil
}
