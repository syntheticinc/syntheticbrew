import { useState, useEffect } from 'react';
import { api } from '../api/client';
import { useApi } from '../hooks/useApi';
import UsageDashboard from '../components/UsageDashboard';

// V2 §5.8 "Settings + BYOK": admin toggles for per-end-user BYOK. The
// values are stored in the `settings` table and read by the BYOK middleware
// on every request — flips here take effect without a restart.
//
// The engine reads exactly two keys:
//   - byok.enabled            "true" / "false"
//   - byok.allowed_providers  allowlist; we WRITE a CSV string, the engine
//                             accepts either a CSV string or a JSON array.
// An empty allowlist means every supported provider is allowed.
const BYOK_ENABLED_KEY = 'byok.enabled';
const BYOK_ALLOWED_PROVIDERS_KEY = 'byok.allowed_providers';

// The full set of providers the engine recognises for BYOK. Identifiers are
// the lowercase values the engine matches against; labels are display-only.
const BYOK_PROVIDERS = [
  { id: 'openai', label: 'OpenAI' },
  { id: 'anthropic', label: 'Anthropic' },
  { id: 'openrouter', label: 'OpenRouter' },
  { id: 'openai_compatible', label: 'OpenAI-compatible' },
  { id: 'ollama', label: 'Ollama' },
] as const;

// parseAllowedProviders normalises the stored value into a lowercase set.
// The setting may arrive as a JSON-array text (env/seed) or a CSV string
// (written by this UI), so both shapes are accepted.
function parseAllowedProviders(raw: string | undefined): Set<string> {
  const result = new Set<string>();
  if (!raw) return result;
  const trimmed = raw.trim();
  if (!trimmed) return result;

  let parts: string[];
  if (trimmed.startsWith('[')) {
    try {
      const arr = JSON.parse(trimmed) as unknown;
      parts = Array.isArray(arr) ? arr.map((v) => String(v)) : [];
    } catch {
      parts = [];
    }
  } else {
    parts = trimmed.split(',');
  }

  for (const p of parts) {
    const id = p.trim().toLowerCase();
    if (id) result.add(id);
  }
  return result;
}

type SettingsTab = 'general' | 'usage';

export default function SettingsPage() {
  const [activeTab, setActiveTab] = useState<SettingsTab>('general');
  const { data: settings, loading, error, refetch } = useApi(() => api.listSettings());
  const [savingKey, setSavingKey] = useState<string | null>(null);
  const [localSettings, setLocalSettings] = useState<Record<string, string>>({});
  const [allowedProviders, setAllowedProviders] = useState<Set<string>>(new Set());

  useEffect(() => {
    if (!settings) return;
    const map: Record<string, string> = {};
    if (Array.isArray(settings)) {
      for (const s of settings) {
        map[s.key] = s.value;
      }
    } else if (typeof settings === 'object') {
      // Handle flat object response from stub API
      const obj = settings as Record<string, unknown>;
      if (obj.byok_enabled !== undefined) map[BYOK_ENABLED_KEY] = String(obj.byok_enabled);
      if (Array.isArray(obj.byok_allowed_providers)) {
        map[BYOK_ALLOWED_PROVIDERS_KEY] = (obj.byok_allowed_providers as unknown[])
          .map((v) => String(v))
          .join(',');
      }
    }
    setLocalSettings(map);
    setAllowedProviders(parseAllowedProviders(map[BYOK_ALLOWED_PROVIDERS_KEY]));
  }, [settings]);

  async function handleToggle(key: string) {
    const current = localSettings[key] === 'true';
    const newValue = (!current).toString();
    setSavingKey(key);
    try {
      await api.updateSetting(key, newValue);
      setLocalSettings((prev) => ({ ...prev, [key]: newValue }));
      refetch();
    } catch {
      // visible in console
    } finally {
      setSavingKey(null);
    }
  }

  // handleProviderToggle flips a single provider in the allowlist and persists
  // the full set as a CSV string under byok.allowed_providers — the only key
  // the engine reads. We optimistically update local state then refetch,
  // mirroring handleToggle.
  async function handleProviderToggle(providerId: string) {
    const next = new Set(allowedProviders);
    if (next.has(providerId)) {
      next.delete(providerId);
    } else {
      next.add(providerId);
    }
    // Preserve the canonical provider order for a stable, readable CSV.
    const csv = BYOK_PROVIDERS.filter((p) => next.has(p.id))
      .map((p) => p.id)
      .join(',');

    setSavingKey(BYOK_ALLOWED_PROVIDERS_KEY);
    try {
      await api.updateSetting(BYOK_ALLOWED_PROVIDERS_KEY, csv);
      setAllowedProviders(next);
      setLocalSettings((prev) => ({ ...prev, [BYOK_ALLOWED_PROVIDERS_KEY]: csv }));
      refetch();
    } catch {
      // visible in console
    } finally {
      setSavingKey(null);
    }
  }

  if (loading) return <div className="text-brand-shade3">Loading settings...</div>;
  if (error) return <div className="text-red-400">Error: {error}</div>;

  return (
    <div className="max-w-3xl">
      <h1 className="text-2xl font-bold text-brand-light mb-4">Settings</h1>

      {/* Tabs */}
      <div className="flex items-center gap-1 mb-6 border-b border-brand-shade3/15">
        {(['general', 'usage'] as const).map((tab) => (
          <button
            key={tab}
            onClick={() => setActiveTab(tab)}
            className={[
              'px-4 py-2 text-sm font-medium font-mono transition-colors capitalize',
              activeTab === tab
                ? 'text-brand-light border-b-2 border-brand-accent'
                : 'text-brand-shade3 hover:text-brand-shade2',
            ].join(' ')}
          >
            {tab}
          </button>
        ))}
      </div>

      {/* Usage tab */}
      {activeTab === 'usage' && <UsageDashboard />}

      {/* General tab */}
      {activeTab === 'general' && <>

      {/* BYOK Configuration — wired into the request path (V2 §5.8). */}
      <section className="mb-8">
        <h2 className="text-lg font-semibold text-brand-light mb-4">BYOK (Bring Your Own Key)</h2>
        <p className="text-sm text-brand-shade3 mb-3">
          When enabled, end users can override the tenant model with their own credentials by sending
          <code className="mx-1 px-1 bg-brand-dark rounded">X-BYOK-Provider</code>,
          <code className="mx-1 px-1 bg-brand-dark rounded">X-BYOK-API-Key</code>,
          <code className="mx-1 px-1 bg-brand-dark rounded">X-BYOK-Model</code>
          and, for <code className="mx-1 px-1 bg-brand-dark rounded">openai_compatible</code> / <code className="mx-1 px-1 bg-brand-dark rounded">ollama</code> only,
          <code className="mx-1 px-1 bg-brand-dark rounded">X-BYOK-Base-URL</code>
          headers on the chat endpoint. The hosted providers use fixed endpoints, and a base URL pointing at a
          private/internal address is refused. Selecting no providers allows the hosted ones; the custom-base-URL
          providers must be selected explicitly.
        </p>

        {/* Enable toggle — writes byok.enabled "true"/"false". */}
        <div className="bg-brand-dark-alt rounded-card border border-brand-shade3/15">
          {(() => {
            const enabled = localSettings[BYOK_ENABLED_KEY] === 'true';
            return (
              <div className="flex items-center justify-between px-4 py-3">
                <div>
                  <div className="text-sm font-medium text-brand-light">BYOK Enabled</div>
                  <div className="text-xs text-brand-shade3">Allow users to bring their own API keys</div>
                </div>
                <button
                  onClick={() => handleToggle(BYOK_ENABLED_KEY)}
                  disabled={savingKey === BYOK_ENABLED_KEY}
                  aria-pressed={enabled}
                  aria-label="BYOK Enabled"
                  className={`relative inline-flex h-6 w-11 items-center rounded-full transition-colors ${
                    enabled ? 'bg-brand-accent' : 'bg-brand-shade3/30'
                  } ${savingKey === BYOK_ENABLED_KEY ? 'opacity-50' : ''}`}
                >
                  <span
                    className={`inline-block h-4 w-4 transform rounded-full bg-white transition-transform ${
                      enabled ? 'translate-x-6' : 'translate-x-1'
                    }`}
                  />
                </button>
              </div>
            );
          })()}
        </div>

        {/* Provider allowlist — writes byok.allowed_providers as CSV. */}
        <h3 className="text-sm font-semibold text-brand-light mt-6 mb-2">Allowed providers</h3>
        <div className="bg-brand-dark-alt rounded-card border border-brand-shade3/15 divide-y divide-brand-shade3/10">
          {BYOK_PROVIDERS.map((provider) => {
            const enabled = allowedProviders.has(provider.id);
            const saving = savingKey === BYOK_ALLOWED_PROVIDERS_KEY;
            return (
              <div key={provider.id} className="flex items-center justify-between px-4 py-3">
                <div>
                  <div className="text-sm font-medium text-brand-light">{provider.label}</div>
                  <div className="text-xs text-brand-shade3">
                    <code className="px-1 bg-brand-dark rounded">{provider.id}</code>
                  </div>
                </div>
                <button
                  onClick={() => handleProviderToggle(provider.id)}
                  disabled={saving}
                  aria-pressed={enabled}
                  aria-label={`Allow ${provider.label}`}
                  className={`relative inline-flex h-6 w-11 items-center rounded-full transition-colors ${
                    enabled ? 'bg-brand-accent' : 'bg-brand-shade3/30'
                  } ${saving ? 'opacity-50' : ''}`}
                >
                  <span
                    className={`inline-block h-4 w-4 transform rounded-full bg-white transition-transform ${
                      enabled ? 'translate-x-6' : 'translate-x-1'
                    }`}
                  />
                </button>
              </div>
            );
          })}
        </div>
        <p className="text-xs text-brand-shade3 mt-2">
          No providers selected = all supported providers are allowed when BYOK is enabled.
        </p>
      </section>
      </>}
    </div>
  );
}
