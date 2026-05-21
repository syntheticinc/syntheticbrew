package app

import (
	"context"
	"errors"
	"fmt"

	deliveryhttp "github.com/syntheticinc/syntheticbrew/internal/delivery/http"
	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/agentregistry"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/configrepo"
	pkgerrors "github.com/syntheticinc/syntheticbrew/pkg/errors"
	"gorm.io/gorm"
)

// capabilityServiceHTTPAdapter bridges GORMCapabilityRepository to the http.CapabilityService interface.
// registryMgr is optional; when set, every mutation triggers registry invalidation so
// DerivedTools are recomputed on the next request (fixes BUG-K-01).
type capabilityServiceHTTPAdapter struct {
	repo        *configrepo.GORMCapabilityRepository
	registryMgr *agentregistry.Manager
}

// invalidateRegistry refreshes the agent registry cache after a capability mutation.
func (a *capabilityServiceHTTPAdapter) invalidateRegistry(ctx context.Context) {
	if a.registryMgr == nil {
		return
	}
	if tid := domain.TenantIDFromContext(ctx); tid != "" {
		a.registryMgr.InvalidateTenant(tid)
	} else {
		a.registryMgr.InvalidateAll()
	}
}

func (a *capabilityServiceHTTPAdapter) ListCapabilities(ctx context.Context, agentName string) ([]deliveryhttp.CapabilityInfo, error) {
	records, err := a.repo.ListByAgent(ctx, agentName)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, pkgerrors.NotFound(fmt.Sprintf("agent not found: %s", agentName))
		}
		return nil, fmt.Errorf("list capabilities: %w", err)
	}

	result := make([]deliveryhttp.CapabilityInfo, 0, len(records))
	for _, r := range records {
		result = append(result, deliveryhttp.CapabilityInfo{
			ID:      r.ID,
			Type:    r.Type,
			Config:  r.Config,
			Enabled: r.Enabled,
		})
	}
	return result, nil
}

func (a *capabilityServiceHTTPAdapter) AddCapability(ctx context.Context, agentName string, req deliveryhttp.CreateCapabilityRequest) (*deliveryhttp.CapabilityInfo, error) {
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	record := &configrepo.CapabilityRecord{
		AgentName: agentName,
		Type:      req.Type,
		Config:    req.Config,
		Enabled:   enabled,
	}
	if err := a.repo.Create(ctx, record); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, pkgerrors.NotFound(fmt.Sprintf("agent not found: %s", agentName))
		}
		return nil, fmt.Errorf("add capability: %w", err)
	}

	a.invalidateRegistry(ctx)

	return &deliveryhttp.CapabilityInfo{
		ID:      record.ID,
		Type:    record.Type,
		Config:  record.Config,
		Enabled: record.Enabled,
	}, nil
}

func (a *capabilityServiceHTTPAdapter) UpdateCapability(ctx context.Context, id string, req deliveryhttp.UpdateCapabilityRequest) error {
	// First read the existing record to use as default values
	existing, err := a.repo.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return pkgerrors.NotFound(fmt.Sprintf("capability not found: %s", id))
		}
		return fmt.Errorf("get capability: %w", err)
	}

	capType := existing.Type
	if req.Type != "" {
		capType = req.Type
	}

	config := existing.Config
	if req.Config != nil {
		config = req.Config
	}

	enabled := existing.Enabled
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	record := &configrepo.CapabilityRecord{
		Type:    capType,
		Config:  config,
		Enabled: enabled,
	}
	if err := a.repo.Update(ctx, id, record); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return pkgerrors.NotFound(fmt.Sprintf("capability not found: %s", id))
		}
		return fmt.Errorf("update capability: %w", err)
	}

	a.invalidateRegistry(ctx)
	return nil
}

func (a *capabilityServiceHTTPAdapter) RemoveCapability(ctx context.Context, id string) error {
	if err := a.repo.Delete(ctx, id); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return pkgerrors.NotFound(fmt.Sprintf("capability not found: %s", id))
		}
		return fmt.Errorf("remove capability: %w", err)
	}

	a.invalidateRegistry(ctx)
	return nil
}
