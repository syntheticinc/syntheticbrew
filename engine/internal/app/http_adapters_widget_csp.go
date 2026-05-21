package app

import (
	"context"
	"encoding/json"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/configrepo"
)

// widgetEmbedOriginsAdapter implements deliveryhttp.WidgetEmbedOriginsLookup.
// It reads the "widget_embed_origins" setting from the tenant-scoped settings
// table and returns the parsed origin strings. Returns nil (empty slice) on
// any error or when the key is not configured, causing the caller to fall back
// to frame-ancestors 'none' (safe default).
type widgetEmbedOriginsAdapter struct {
	repo *configrepo.GORMSettingRepository
}

// GetWidgetEmbedOrigins returns the allowed frame-ancestors for the given
// tenant. tenantID is injected into ctx so the repo's tenantScope picks it up.
func (a *widgetEmbedOriginsAdapter) GetWidgetEmbedOrigins(ctx context.Context, tenantID string) []string {
	// The repo reads tenant_id from ctx via tenantScope; inject it explicitly
	// so this method works regardless of whether the caller already set it.
	ctx = domain.WithTenantID(ctx, tenantID)

	setting, err := a.repo.Get(ctx, "widget_embed_origins")
	if err != nil || setting == nil {
		return nil
	}

	// settings.value is jsonb. Two shapes are accepted:
	//   direct array:  ["https://a.example", "https://b.example"]
	//   wrapped string: "[\"https://a.example\",\"https://b.example\"]"
	// The settings API currently stores the value as a JSON string (the second
	// shape) because UpdateSettingRequest.Value is typed string. Handle both
	// so future schema changes don't require an adapter rewrite.
	var origins []string
	if err := json.Unmarshal(setting.Value, &origins); err == nil {
		return origins
	}
	var wrapped string
	if err := json.Unmarshal(setting.Value, &wrapped); err != nil {
		return nil
	}
	if err := json.Unmarshal([]byte(wrapped), &origins); err != nil {
		return nil
	}
	return origins
}
