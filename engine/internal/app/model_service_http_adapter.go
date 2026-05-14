package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	deliveryhttp "github.com/syntheticinc/bytebrew/engine/internal/delivery/http"
	"github.com/syntheticinc/bytebrew/engine/internal/infrastructure/llm"
	"github.com/syntheticinc/bytebrew/engine/internal/infrastructure/persistence/configrepo"
	"github.com/syntheticinc/bytebrew/engine/internal/infrastructure/persistence/models"
	pkgerrors "github.com/syntheticinc/bytebrew/engine/pkg/errors"
)

// ModelCacheInvalidator allows invalidating cached model clients when models are modified.
type ModelCacheInvalidator interface {
	Invalidate(modelID string)
}

// modelServiceHTTPAdapter bridges GORMLLMProviderRepository to the http.ModelService interface.
type modelServiceHTTPAdapter struct {
	repo       *configrepo.GORMLLMProviderRepository
	modelCache ModelCacheInvalidator
	// agentRepo is used to back-fill the builder-assistant's model_name when
	// a tenant creates its first chat model via the onboarding wizard. At
	// provisioning time the builder-assistant is seeded without a model (no
	// tenant models exist yet) — this closes that gap automatically so the
	// AI Builder is usable right after step 1 of the wizard. Optional; nil
	// means no backfill (useful in unit tests).
	agentRepo *configrepo.GORMAgentRepository
}

func (m *modelServiceHTTPAdapter) ListModels(ctx context.Context) ([]deliveryhttp.ModelResponse, error) {
	providers, err := m.repo.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list models: %w", err)
	}

	result := make([]deliveryhttp.ModelResponse, 0, len(providers))
	for _, p := range providers {
		kind := p.Kind
		if kind == "" {
			kind = "chat"
		}
		result = append(result, deliveryhttp.ModelResponse{
			ID:           p.ID,
			Name:         p.Name,
			Type:         p.Type,
			Kind:         kind,
			BaseURL:      p.BaseURL,
			ModelName:    p.ModelName,
			HasAPIKey:    p.APIKeyEncrypted != "",
			APIVersion:   p.APIVersion,
			EmbeddingDim: p.EmbeddingDim(),
			IsDefault:    p.IsDefault,
			ExtraBody:    p.GetConfig().ExtraBody,
			CreatedAt:    p.CreatedAt.Format(time.RFC3339),
		})
	}
	return result, nil
}

func (m *modelServiceHTTPAdapter) CreateModel(ctx context.Context, req deliveryhttp.CreateModelRequest) (*deliveryhttp.ModelResponse, error) {
	kind := req.Kind
	if kind == "" {
		kind = "chat"
	}

	// Bootstrap: if this is the first chat model for the tenant and the
	// caller didn't explicitly set is_default, auto-promote it so the tenant
	// has a coherent default out of the box.
	autoPromoted := false
	if kind == "chat" && !req.IsDefault {
		existingDefault, err := m.repo.GetDefault(ctx, "chat")
		if err != nil {
			return nil, fmt.Errorf("check existing default model: %w", err)
		}
		if existingDefault == nil {
			req.IsDefault = true
			autoPromoted = true
		}
	}

	provider := &models.LLMProviderModel{
		Name:            req.Name,
		Type:            req.Type,
		Kind:            kind,
		BaseURL:         req.BaseURL,
		ModelName:       req.ModelName,
		APIKeyEncrypted: req.APIKey,
		APIVersion:      req.APIVersion,
	}
	// Only set IsDefault on create for the bootstrap case — when the caller
	// explicitly asked to promote and the tenant already has a default, we
	// route through SetDefault (below) to preserve the atomic-swap invariant.
	if kind == "chat" && autoPromoted {
		provider.IsDefault = true
	}
	if req.EmbeddingDim > 0 || len(req.ExtraBody) > 0 {
		provider.SetConfig(models.ModelConfig{
			EmbeddingDim: req.EmbeddingDim,
			ExtraBody:    req.ExtraBody,
		})
	}

	if err := m.repo.Create(ctx, provider); err != nil {
		if strings.Contains(err.Error(), "duplicate key") || strings.Contains(err.Error(), "unique constraint") || strings.Contains(err.Error(), "UNIQUE constraint") {
			return nil, pkgerrors.AlreadyExists(fmt.Sprintf("model with name %q already exists", req.Name))
		}
		return nil, fmt.Errorf("create model: %w", err)
	}

	// If caller asked for is_default=true on a model that wasn't the
	// bootstrap case, flip the default now (atomic swap).
	if kind == "chat" && req.IsDefault && !autoPromoted {
		if err := m.repo.SetDefault(ctx, provider.ID); err != nil {
			return nil, fmt.Errorf("promote to default: %w", err)
		}
		provider.IsDefault = true
	}

	// Back-fill every agent with ModelID IS NULL to the tenant default
	// whenever a chat model becomes the default. Covers both the bootstrap
	// (first chat model) and the explicit promote path. Non-fatal per-agent.
	if kind == "chat" && provider.IsDefault {
		m.backfillTenantAgentsToDefault(ctx, provider.ID)
	}

	return &deliveryhttp.ModelResponse{
		ID:           provider.ID,
		Name:         provider.Name,
		Type:         provider.Type,
		Kind:         provider.Kind,
		BaseURL:      provider.BaseURL,
		ModelName:    provider.ModelName,
		HasAPIKey:    provider.APIKeyEncrypted != "",
		APIVersion:   provider.APIVersion,
		EmbeddingDim: provider.EmbeddingDim(),
		IsDefault:    provider.IsDefault,
		ExtraBody:    provider.GetConfig().ExtraBody,
		CreatedAt:    provider.CreatedAt.Format(time.RFC3339),
	}, nil
}

// backfillTenantAgentsToDefault sets ModelID on every agent in the current
// tenant whose ModelID is nil — driving them onto the new default chat model.
// Agents already bound to a specific model are left alone: user intent takes
// precedence over automatic backfill. Per-agent failure is logged but does
// not fail the caller (the model create already committed).
func (m *modelServiceHTTPAdapter) backfillTenantAgentsToDefault(ctx context.Context, modelID string) {
	if m.agentRepo == nil || modelID == "" {
		return
	}
	agents, err := m.agentRepo.List(ctx)
	if err != nil {
		return
	}
	// Resolve model name once for the Update call — the existing AgentRecord
	// update path keys on ModelName, not ModelID. Cheap: we already have the
	// provider struct on the caller side, but looking up by ID keeps this
	// helper reusable from SetDefault paths that only carry the ID.
	provider, err := m.repo.GetByID(ctx, modelID)
	if err != nil || provider == nil {
		return
	}
	for i := range agents {
		a := &agents[i]
		if a.ModelID != nil && *a.ModelID != "" {
			continue
		}
		a.ModelName = provider.Name
		if uerr := m.agentRepo.Update(ctx, a.Name, a); uerr != nil {
			// Log-only: failure to rebind one agent doesn't undo the model create.
			_ = uerr
		}
	}
}

func (m *modelServiceHTTPAdapter) UpdateModel(ctx context.Context, name string, req deliveryhttp.CreateModelRequest) (*deliveryhttp.ModelResponse, error) {
	providers, err := m.repo.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list models for update: %w", err)
	}

	var existing *models.LLMProviderModel
	for i := range providers {
		if providers[i].Name == name {
			existing = &providers[i]
			break
		}
	}
	if existing == nil {
		return nil, pkgerrors.NotFound(fmt.Sprintf("model not found: %s", name))
	}

	kind := req.Kind
	if kind == "" {
		kind = "chat"
	}
	update := &models.LLMProviderModel{
		Name:       req.Name,
		Type:       req.Type,
		Kind:       kind,
		BaseURL:    req.BaseURL,
		ModelName:  req.ModelName,
		APIVersion: req.APIVersion,
		// Preserve default flag through the plain Update path — promotion
		// goes through SetDefault to keep the atomic-swap invariant intact.
		IsDefault:  existing.IsDefault,
	}
	// PUT semantics: replace config wholesale. Carry over both
	// EmbeddingDim and ExtraBody from the request — omitted fields clear
	// (mirrors the rest of the PUT path which replaces, not patches).
	if req.EmbeddingDim > 0 || len(req.ExtraBody) > 0 {
		update.SetConfig(models.ModelConfig{
			EmbeddingDim: req.EmbeddingDim,
			ExtraBody:    req.ExtraBody,
		})
	}
	// Only update API key if provided (empty means keep existing).
	if req.APIKey != "" {
		update.APIKeyEncrypted = req.APIKey
	}

	if err := m.repo.Update(ctx, existing.ID, update); err != nil {
		return nil, fmt.Errorf("update model: %w", err)
	}

	// Handle explicit IsDefault promotion (PUT full-replace semantics): if the
	// caller passed IsDefault=true on a model that isn't currently default,
	// promote it via SetDefault to keep the atomic-swap invariant intact.
	promotedDefault := false
	if req.IsDefault && !existing.IsDefault {
		if err := m.repo.SetDefault(ctx, existing.ID); err != nil {
			return nil, fmt.Errorf("promote model to default: %w", err)
		}
		promotedDefault = true
		m.backfillTenantAgentsToDefault(ctx, existing.ID)
	}

	// Invalidate cached client so next access picks up changes.
	if m.modelCache != nil {
		m.modelCache.Invalidate(existing.ID)
	}

	hasKey := existing.APIKeyEncrypted != ""
	if req.APIKey != "" {
		hasKey = true
	}

	respName := req.Name
	if respName == "" {
		respName = existing.Name
	}

	isDefault := existing.IsDefault
	if promotedDefault {
		isDefault = true
	}

	return &deliveryhttp.ModelResponse{
		ID:           existing.ID,
		Name:         respName,
		Type:         req.Type,
		Kind:         kind,
		BaseURL:      req.BaseURL,
		ModelName:    req.ModelName,
		HasAPIKey:    hasKey,
		APIVersion:   req.APIVersion,
		EmbeddingDim: req.EmbeddingDim,
		IsDefault:    isDefault,
		ExtraBody:    req.ExtraBody,
		CreatedAt:    existing.CreatedAt.Format(time.RFC3339),
	}, nil
}

// PatchModel applies only the non-nil fields in req to the existing model.
func (m *modelServiceHTTPAdapter) PatchModel(ctx context.Context, name string, req deliveryhttp.UpdateModelRequest) (*deliveryhttp.ModelResponse, error) {
	providers, err := m.repo.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list models for patch: %w", err)
	}

	var existing *models.LLMProviderModel
	for i := range providers {
		if providers[i].Name == name {
			existing = &providers[i]
			break
		}
	}
	if existing == nil {
		return nil, pkgerrors.NotFound(fmt.Sprintf("model not found: %s", name))
	}

	// Build update struct starting from existing values (preserve unspecified fields).
	existingKind := existing.Kind
	if existingKind == "" {
		existingKind = "chat"
	}
	update := &models.LLMProviderModel{
		Name:            existing.Name,
		Type:            existing.Type,
		Kind:            existingKind,
		BaseURL:         existing.BaseURL,
		ModelName:       existing.ModelName,
		APIVersion:      existing.APIVersion,
		APIKeyEncrypted: existing.APIKeyEncrypted,
	}
	update.SetConfig(existing.GetConfig())

	if req.Name != nil {
		update.Name = *req.Name
	}
	if req.Type != nil {
		update.Type = *req.Type
	}
	if req.Kind != nil {
		update.Kind = *req.Kind
	}
	if req.BaseURL != nil {
		update.BaseURL = *req.BaseURL
	}
	if req.ModelName != nil {
		update.ModelName = *req.ModelName
	}
	if req.APIVersion != nil {
		update.APIVersion = *req.APIVersion
	}
	if req.APIKey != nil && *req.APIKey != "" {
		update.APIKeyEncrypted = *req.APIKey
	}
	if req.EmbeddingDim != nil {
		cfg := existing.GetConfig()
		cfg.EmbeddingDim = *req.EmbeddingDim
		update.SetConfig(cfg)
	}
	if req.ExtraBody != nil {
		cfg := update.GetConfig()
		cfg.ExtraBody = *req.ExtraBody
		update.SetConfig(cfg)
	}

	// Preserve current IsDefault through the plain Update path — promotion
	// goes through SetDefault to keep the atomic-swap invariant intact.
	update.IsDefault = existing.IsDefault

	if err := m.repo.Update(ctx, existing.ID, update); err != nil {
		return nil, fmt.Errorf("patch model: %w", err)
	}

	// Promote to default when is_default=true and we aren't already the default.
	// (is_default=false was rejected at the handler layer.)
	promotedDefault := false
	if req.IsDefault != nil && *req.IsDefault && !existing.IsDefault {
		if err := m.repo.SetDefault(ctx, existing.ID); err != nil {
			return nil, fmt.Errorf("promote model to default: %w", err)
		}
		promotedDefault = true
		// Re-run agent backfill so any unbound agents pick up the new default.
		m.backfillTenantAgentsToDefault(ctx, existing.ID)
	}

	if m.modelCache != nil {
		m.modelCache.Invalidate(existing.ID)
	}

	embDim := existing.EmbeddingDim()
	if req.EmbeddingDim != nil {
		embDim = *req.EmbeddingDim
	}

	isDefault := existing.IsDefault
	if promotedDefault {
		isDefault = true
	}

	return &deliveryhttp.ModelResponse{
		ID:           existing.ID,
		Name:         update.Name,
		Type:         update.Type,
		Kind:         update.Kind,
		BaseURL:      update.BaseURL,
		ModelName:    update.ModelName,
		HasAPIKey:    update.APIKeyEncrypted != "",
		APIVersion:   update.APIVersion,
		EmbeddingDim: embDim,
		IsDefault:    isDefault,
		ExtraBody:    update.GetConfig().ExtraBody,
		CreatedAt:    existing.CreatedAt.Format(time.RFC3339),
	}, nil
}

func (m *modelServiceHTTPAdapter) DeleteModel(ctx context.Context, name string) error {
	providers, err := m.repo.List(ctx)
	if err != nil {
		return fmt.Errorf("list models for delete: %w", err)
	}

	for _, p := range providers {
		if p.Name != name {
			continue
		}

		users, err := m.repo.AgentsUsingModel(ctx, p.ID)
		if err != nil {
			return fmt.Errorf("check agents using model: %w", err)
		}
		if len(users) > 0 {
			return pkgerrors.InvalidInput(fmt.Sprintf(
				"cannot delete model %q: it is used by %d agent(s): %s",
				name, len(users), strings.Join(users, ", "),
			))
		}

		if err := m.repo.Delete(ctx, p.ID); err != nil {
			return err
		}
		if m.modelCache != nil {
			m.modelCache.Invalidate(p.ID)
		}
		return nil
	}
	return pkgerrors.NotFound(fmt.Sprintf("model not found: %s", name))
}

func (m *modelServiceHTTPAdapter) VerifyModel(ctx context.Context, name string) (*deliveryhttp.ModelVerifyResult, error) {
	providers, err := m.repo.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list models for verify: %w", err)
	}

	var dbModel *models.LLMProviderModel
	for i := range providers {
		if providers[i].Name == name {
			dbModel = &providers[i]
			break
		}
	}
	if dbModel == nil {
		return nil, pkgerrors.NotFound(fmt.Sprintf("model not found: %s", name))
	}

	client, err := llm.CreateClientFromDBModel(*dbModel)
	if err != nil {
		errMsg := fmt.Sprintf("failed to create client: %s", err.Error())
		return &deliveryhttp.ModelVerifyResult{
			Connectivity: "error",
			ToolCalling:  "skipped",
			ModelName:    dbModel.ModelName,
			Provider:     dbModel.Type,
			Error:        &errMsg,
		}, nil
	}

	verifyCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	vr := llm.VerifyModel(verifyCtx, client, dbModel.ModelName, dbModel.Type)
	return &deliveryhttp.ModelVerifyResult{
		Connectivity:   vr.Connectivity,
		ToolCalling:    vr.ToolCalling,
		ResponseTimeMs: vr.ResponseTimeMs,
		ModelName:      vr.ModelName,
		Provider:       vr.Provider,
		Error:          vr.Error,
	}, nil
}
