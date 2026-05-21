package app

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	"gorm.io/gorm"

	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/persistence/configrepo"
	"github.com/syntheticinc/syntheticbrew/pkg/config"
)

// settings keys for per-end-user BYOK (V2 §5.8). The middleware reads
// these from the `settings` table on every request via loadBYOKConfig
// so admin UI changes propagate without restart.
const (
	settingKeyBYOKEnabled          = "byok.enabled"
	settingKeyBYOKAllowedProviders = "byok.allowed_providers"
)

// seedBYOKConfig writes the YAML BYOK bootstrap into the `settings` table
// (jsonb). Idempotent — re-runs overwrite the rows with the YAML values
// only when they are missing. If admin changes them via the API the
// existing rows are preserved across restarts.
//
// The values land as real jsonb shapes:
//   - byok.enabled              → jsonb boolean   (true / false)
//   - byok.allowed_providers    → jsonb string[]  (e.g. ["openai","anthropic"])
//
// See `docs/architecture/agent-first-runtime.md §5.8` and
// `docs/plan/v2-cleanup-checklist.md` "Commit Group G".
func seedBYOKConfig(ctx context.Context, db *gorm.DB, cfg config.BYOKConfig) {
	if db == nil {
		return
	}

	repo := configrepo.NewGORMSettingRepository(db)

	// 1) byok.enabled — write only if missing. Honours admin-side toggles
	//    that may have been flipped after the first boot.
	if existing, err := repo.Get(ctx, settingKeyBYOKEnabled); err != nil {
		slog.WarnContext(ctx, "seed byok: read enabled failed", "error", err)
	} else if existing == nil {
		enabledJSON, _ := json.Marshal(cfg.Enabled)
		if err := repo.SetJSON(ctx, settingKeyBYOKEnabled, enabledJSON); err != nil {
			slog.WarnContext(ctx, "seed byok: write enabled failed", "error", err)
		} else {
			slog.InfoContext(ctx, "seeded byok.enabled", "value", cfg.Enabled)
		}
	}

	// 2) byok.allowed_providers — same idempotent rule. nil collapses to []
	//    so the row shape is always a JSON array.
	if existing, err := repo.Get(ctx, settingKeyBYOKAllowedProviders); err != nil {
		slog.WarnContext(ctx, "seed byok: read allowed_providers failed", "error", err)
	} else if existing == nil {
		providers := cfg.AllowedProviders
		if providers == nil {
			providers = []string{}
		}
		providersJSON, err := json.Marshal(providers)
		if err != nil {
			slog.WarnContext(ctx, "seed byok: encode allowed_providers failed", "error", err)
			return
		}
		if err := repo.SetJSON(ctx, settingKeyBYOKAllowedProviders, providersJSON); err != nil {
			slog.WarnContext(ctx, "seed byok: write allowed_providers failed", "error", err)
		} else {
			slog.InfoContext(ctx, "seeded byok.allowed_providers", "providers", providers)
		}
	}
}

// loadBYOKConfig reads the current BYOK configuration from the `settings`
// table. Returns the bootstrap fallback when the table is unavailable so
// callers always get a usable config — disabled by default keeps the
// strictest stance.
func loadBYOKConfig(ctx context.Context, db *gorm.DB, fallback config.BYOKConfig) config.BYOKConfig {
	if db == nil {
		return fallback
	}

	repo := configrepo.NewGORMSettingRepository(db)
	out := config.BYOKConfig{
		Enabled:          fallback.Enabled,
		AllowedProviders: fallback.AllowedProviders,
	}

	if rec, err := repo.Get(ctx, settingKeyBYOKEnabled); err == nil && rec != nil && len(rec.Value) > 0 {
		var enabled bool
		if jsonErr := json.Unmarshal(rec.Value, &enabled); jsonErr == nil {
			out.Enabled = enabled
		} else {
			// HTTP API writes the value as a JSON string ("true"/"false").
			var s string
			if jsonErr2 := json.Unmarshal(rec.Value, &s); jsonErr2 == nil {
				out.Enabled = strings.EqualFold(s, "true")
			}
		}
	}

	if rec, err := repo.Get(ctx, settingKeyBYOKAllowedProviders); err == nil && rec != nil && len(rec.Value) > 0 {
		var providers []string
		if jsonErr := json.Unmarshal(rec.Value, &providers); jsonErr == nil {
			out.AllowedProviders = providers
		}
	}

	return out
}
