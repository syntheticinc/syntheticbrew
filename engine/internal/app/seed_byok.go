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

// reconcileBYOKConfig overwrites the BYOK settings rows with operator-declared
// values when supplied via env/chart (BootstrapBYOK.Managed*). Unlike
// seedBYOKConfig (write-only-if-missing), this is the GitOps "declared state
// wins" path: it runs on every boot and supersedes prior Admin-UI edits. Only
// the keys the operator actually set are overwritten. Values land as the same
// jsonb shapes the middleware reads (boolean / string[]).
func reconcileBYOKConfig(ctx context.Context, db *gorm.DB, b config.BootstrapBYOK) {
	if db == nil || (!b.ManagedEnabled && !b.ManagedProviders) {
		return
	}
	repo := configrepo.NewGORMSettingRepository(db)
	if b.ManagedEnabled {
		enabledJSON, _ := json.Marshal(b.Enabled)
		writeBYOKSetting(ctx, repo, settingKeyBYOKEnabled, enabledJSON, b.Enabled)
	}
	if b.ManagedProviders {
		reconcileBYOKProviders(ctx, repo, b.AllowedProviders)
	}
}

// reconcileBYOKProviders writes the operator-declared allowlist as a jsonb
// array — nil collapses to [] so the row is always a JSON array.
func reconcileBYOKProviders(ctx context.Context, repo *configrepo.GORMSettingRepository, providers []string) {
	if providers == nil {
		providers = []string{}
	}
	providersJSON, err := json.Marshal(providers)
	if err != nil {
		slog.WarnContext(ctx, "reconcile byok: encode allowed_providers failed", "error", err)
		return
	}
	writeBYOKSetting(ctx, repo, settingKeyBYOKAllowedProviders, providersJSON, providers)
}

// writeBYOKSetting upserts a reconciled BYOK setting row and logs the outcome.
func writeBYOKSetting(ctx context.Context, repo *configrepo.GORMSettingRepository, key string, value []byte, logValue any) {
	if err := repo.SetJSON(ctx, key, value); err != nil {
		slog.WarnContext(ctx, "reconcile byok: write failed", "key", key, "error", err)
		return
	}
	slog.InfoContext(ctx, "reconciled byok setting from env", "key", key, "value", logValue)
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
		} else {
			// HTTP Settings API stores the value as a jsonb string. Accept both a
			// JSON-array string ("[\"openai\"]") and a CSV string ("openai,anthropic").
			var s string
			if jsonErr2 := json.Unmarshal(rec.Value, &s); jsonErr2 == nil {
				out.AllowedProviders = parseProviderListString(s)
			}
		}
	}

	return out
}

// parseProviderListString parses an allowlist supplied through the Settings API
// as a string: either a JSON array ("[\"openai\",\"anthropic\"]") or a CSV
// ("openai, anthropic"). Whitespace is trimmed and empty elements dropped.
func parseProviderListString(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return []string{}
	}
	if arr, ok := parseJSONProviderArray(s); ok {
		return arr
	}
	return trimNonEmpty(strings.Split(s, ","))
}

// parseJSONProviderArray parses a JSON-array string ("[\"openai\"]") into a
// trimmed provider list. Returns ok=false when s is not a parseable JSON array.
func parseJSONProviderArray(s string) ([]string, bool) {
	if !strings.HasPrefix(s, "[") {
		return nil, false
	}
	var arr []string
	if err := json.Unmarshal([]byte(s), &arr); err != nil {
		return nil, false
	}
	return trimNonEmpty(arr), true
}

// trimNonEmpty trims whitespace from each element and drops empty ones.
func trimNonEmpty(in []string) []string {
	out := make([]string, 0, len(in))
	for _, p := range in {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
