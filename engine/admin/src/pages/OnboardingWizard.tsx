import { useEffect, useState, type FormEvent } from 'react';
import { useNavigate } from 'react-router-dom';
import { api } from '../api/client';
import type { CreateModelRequest } from '../types';

// Mandatory BYOK wizard. Full-page route (not modal) because the admin
// surface is blocked until a model is configured — see OnboardingGate.

// Mirror of engine's ValidateResourceName regex (DNS-label format, max 100
// chars). Engine rejects POST /api/v1/models with HTTP 400 on mismatch since
// 1.1.0 — name-keyed URLs require a slug-shaped identifier. We block the
// wizard's "Next" button on invalid input so the user gets an inline error
// instead of a 400 round-trip with the toast hidden behind the modal.
const SLUG_RE = /^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/;

type Provider = {
  id: string;
  label: string;
  description: string;
  baseUrl?: string;
  defaultModel: string;
  modelHint: string;
  apiKeyHint: string;
  requiresBaseUrl?: boolean;
};

const PROVIDERS: Provider[] = [
  {
    id: 'openai_compatible',
    label: 'OpenAI',
    description: 'GPT-4, GPT-4o, GPT-3.5 via official OpenAI API',
    baseUrl: 'https://api.openai.com/v1',
    defaultModel: 'gpt-4o-mini',
    modelHint: 'e.g. gpt-4o, gpt-4o-mini, gpt-4-turbo',
    apiKeyHint: 'Starts with sk-...',
  },
  {
    id: 'anthropic',
    label: 'Anthropic',
    description: 'Claude Opus, Sonnet, Haiku',
    defaultModel: 'claude-3-5-sonnet-latest',
    modelHint: 'e.g. claude-3-5-sonnet-latest, claude-3-5-haiku-latest',
    apiKeyHint: 'Starts with sk-ant-...',
  },
  {
    id: 'openrouter',
    label: 'OpenRouter',
    description: 'Unified gateway — 200+ models from many providers',
    baseUrl: 'https://openrouter.ai/api/v1',
    defaultModel: 'anthropic/claude-3.5-sonnet',
    modelHint: 'e.g. anthropic/claude-3.5-sonnet, openai/gpt-4o',
    apiKeyHint: 'Starts with sk-or-...',
  },
  {
    id: 'azure_openai',
    label: 'Azure OpenAI',
    description: 'Azure-hosted OpenAI deployments',
    defaultModel: '',
    modelHint: 'Your deployment name (not the underlying model)',
    apiKeyHint: 'Azure resource key',
    requiresBaseUrl: true,
  },
  {
    id: 'openai_compatible_custom',
    label: 'Custom',
    description: 'Any OpenAI-compatible endpoint (LM Studio, vLLM, etc.)',
    defaultModel: '',
    modelHint: 'Your model identifier',
    apiKeyHint: 'API key (optional for local endpoints)',
    requiresBaseUrl: true,
  },
];

type TemplateId = 'support' | 'sales' | 'blank';

// Catalog templates (support/sales) use the backend fork endpoint
// (POST /api/v1/schema-templates/:name/fork) which creates schema +
// agents + relations + capabilities atomically. Client-side relation
// synthesis is rejected as a self-loop (source must differ from target).
// `blank` uses the per-entity API and creates no relations — a single
// entry agent is a valid schema.
type Template = {
  id: TemplateId;
  label: string;
  description: string;
  schemaName: string;
  // Backend schema-templates.yaml catalog name (undefined for `blank`).
  catalogName?: string;
  // Fields below are used only for the `blank` path.
  agentName?: string;
  systemPrompt?: string;
};

const TEMPLATES: Template[] = [
  {
    id: 'support',
    label: 'Support Bot',
    description:
      'A polite, fact-driven customer support agent. Answers from your docs, escalates when unsure.',
    schemaName: 'Support Bot',
    catalogName: 'customer-support-basic',
  },
  {
    id: 'sales',
    label: 'Sales Assistant',
    description:
      'A proactive sales assistant. Qualifies leads, answers pricing questions, books demos.',
    schemaName: 'Sales Assistant',
    catalogName: 'sales-qualifier-basic',
  },
  {
    id: 'blank',
    label: 'Blank canvas',
    description: 'Start from scratch. A single empty agent ready for you to shape.',
    schemaName: 'My First Workspace',
    agentName: 'assistant',
    systemPrompt: 'You are a helpful AI assistant.',
  },
];

// SVG icons inline — lucide-react is not in package.json.
function CheckIcon({ className = 'w-5 h-5' }: { className?: string }) {
  return (
    <svg xmlns="http://www.w3.org/2000/svg" className={className} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <polyline points="20 6 9 17 4 12" />
    </svg>
  );
}

function KeyIcon({ className = 'w-4 h-4' }: { className?: string }) {
  return (
    <svg xmlns="http://www.w3.org/2000/svg" className={className} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d="M21 2l-2 2m-7.61 7.61a5.5 5.5 0 1 1-7.778 7.778 5.5 5.5 0 0 1 7.777-7.777zm0 0L15.5 7.5m0 0l3 3L22 7l-3-3m-3.5 3.5L19 4" />
    </svg>
  );
}

function SpinnerIcon({ className = 'w-4 h-4' }: { className?: string }) {
  return (
    <svg xmlns="http://www.w3.org/2000/svg" className={`${className} animate-spin`} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" aria-hidden="true">
      <circle cx="12" cy="12" r="10" strokeOpacity="0.25" />
      <path d="M22 12a10 10 0 0 1-10 10" strokeLinecap="round" />
    </svg>
  );
}

function XIcon({ className = 'w-4 h-4' }: { className?: string }) {
  return (
    <svg xmlns="http://www.w3.org/2000/svg" className={className} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <line x1="18" y1="6" x2="6" y2="18" />
      <line x1="6" y1="6" x2="18" y2="18" />
    </svg>
  );
}

function ProgressHeader({ step }: { step: 1 | 2 }) {
  const labels = ['Connect LLM', 'Starter template'];
  return (
    <div className="w-full max-w-3xl mx-auto mb-8">
      <div className="flex items-center justify-between mb-3 text-xs text-brand-shade3 font-mono">
        <span>Step {step} of 2</span>
        <span>{Math.round((step / 2) * 100)}%</span>
      </div>
      <div className="flex items-center gap-2">
        {labels.map((label, idx) => {
          const n = (idx + 1) as 1 | 2;
          const done = n < step;
          const active = n === step;
          return (
            <div key={label} className="flex-1 flex items-center gap-2">
              <div
                className={`flex items-center justify-center w-7 h-7 rounded-full text-xs font-semibold shrink-0 transition-colors ${
                  done
                    ? 'bg-brand-accent text-white'
                    : active
                    ? 'bg-brand-accent text-white ring-2 ring-brand-accent/30'
                    : 'bg-brand-dark-alt text-brand-shade3 border border-brand-shade3/30'
                }`}
              >
                {done ? <CheckIcon className="w-4 h-4" /> : n}
              </div>
              <span
                className={`text-sm truncate ${
                  active ? 'text-brand-light font-medium' : done ? 'text-brand-shade2' : 'text-brand-shade3'
                }`}
              >
                {label}
              </span>
              {idx < labels.length - 1 && (
                <div
                  className={`flex-1 h-px mx-1 ${
                    done ? 'bg-brand-accent' : 'bg-brand-shade3/20'
                  }`}
                />
              )}
            </div>
          );
        })}
      </div>
    </div>
  );
}

type TestStatus =
  | { kind: 'idle' }
  | { kind: 'testing' }
  | { kind: 'success'; modelName: string }
  | { kind: 'error'; message: string };

function Step1ConnectLLM({
  onSuccess,
  platformDefault,
}: {
  onSuccess: () => void;
  // When the deployment provides a process-wide default model, bringing a key
  // is optional — the user can continue without one.
  platformDefault: boolean;
}) {
  const [providerId, setProviderId] = useState<string>(PROVIDERS[0]!.id);
  const [apiKey, setApiKey] = useState('');
  const [modelName, setModelName] = useState(PROVIDERS[0]!.defaultModel);
  const [baseUrl, setBaseUrl] = useState(PROVIDERS[0]!.baseUrl ?? '');
  const [displayName, setDisplayName] = useState('default');
  const [status, setStatus] = useState<TestStatus>({ kind: 'idle' });

  const provider = PROVIDERS.find((p) => p.id === providerId)!;

  function selectProvider(id: string) {
    const next = PROVIDERS.find((p) => p.id === id)!;
    setProviderId(id);
    setModelName(next.defaultModel);
    setBaseUrl(next.baseUrl ?? '');
    setStatus({ kind: 'idle' });
  }

  // "openai_compatible_custom" is an onboarding-only alias for the Custom
  // provider card; backend's enum only knows "openai_compatible".
  function backendType(id: string): string {
    if (id === 'openai_compatible_custom') return 'openai_compatible';
    return id;
  }

  async function handleNext(e: FormEvent) {
    e.preventDefault();
    if (status.kind === 'testing') return;

    if (!apiKey.trim() && providerId !== 'openai_compatible_custom') {
      setStatus({ kind: 'error', message: 'API key is required.' });
      return;
    }
    if (!modelName.trim()) {
      setStatus({ kind: 'error', message: 'Model name is required.' });
      return;
    }
    if (provider.requiresBaseUrl && !baseUrl.trim()) {
      setStatus({ kind: 'error', message: 'Base URL is required for this provider.' });
      return;
    }
    if (!displayName.trim()) {
      setStatus({ kind: 'error', message: 'Display name is required.' });
      return;
    }
    if (!SLUG_RE.test(displayName.trim()) || displayName.trim().length > 100) {
      setStatus({
        kind: 'error',
        message:
          'Display name must be a URL slug: lowercase letters, digits, hyphens (e.g. "default", "glm-4"). The provider model identifier goes in "Model name" below.',
      });
      return;
    }

    setStatus({ kind: 'testing' });

    const payload: CreateModelRequest = {
      // `kind` is required by the server. Embedding models are configured
      // on a separate admin surface.
      kind: 'chat',
      name: displayName.trim(),
      type: backendType(providerId),
      model_name: modelName.trim(),
      api_key: apiKey.trim() || undefined,
      base_url: baseUrl.trim() || undefined,
    };

    try {
      // Backend only validates payload shape; bad API keys surface later on
      // the first real chat call.
      await api.createModel(payload);
      // Sticky flag for OnboardingGate — the gate re-mounts when the route
      // group changes (/onboarding wrapper vs /* wrapper); a read-after-write
      // race against POST /models would otherwise return an empty list and
      // bounce the user back into the wizard.
      // Timestamped value (`1:<unix_ms>`) is the new gate-cache contract —
      // OnboardingGate honors it for ~5s to bridge the read-after-write
      // race; outside that window the API is the source of truth, so a
      // tenant that lost all models recovers via the wizard automatically.
      try { sessionStorage.setItem('bb_onboarded', `1:${Date.now()}`); } catch { /* no-op */ }
      setStatus({ kind: 'success', modelName: payload.name });
      onSuccess();
    } catch (err) {
      const message = err instanceof Error ? err.message : 'Connection failed.';
      // Treat ALREADY_EXISTS as success — re-entering onboarding (e.g. after
      // Skip) must not force users to invent a new display name.
      if (/already exists|ALREADY_EXISTS/i.test(message)) {
        // Timestamped value (`1:<unix_ms>`) is the new gate-cache contract —
      // OnboardingGate honors it for ~5s to bridge the read-after-write
      // race; outside that window the API is the source of truth, so a
      // tenant that lost all models recovers via the wizard automatically.
      try { sessionStorage.setItem('bb_onboarded', `1:${Date.now()}`); } catch { /* no-op */ }
        setStatus({ kind: 'success', modelName: payload.name });
        onSuccess();
        return;
      }
      setStatus({ kind: 'error', message });
    }
  }

  return (
    <form onSubmit={handleNext} className="w-full max-w-3xl mx-auto">
      <div className="mb-6">
        <h1 className="text-2xl font-bold text-brand-light mb-1">Connect your LLM</h1>
        <p className="text-sm text-brand-shade2">
          SyntheticBrew is bring-your-own-key. Paste an API key from your LLM provider to continue —
          keys live on your Engine, not in our cloud.
        </p>
      </div>

      {/* Provider cards */}
      <div className="grid grid-cols-1 sm:grid-cols-2 gap-3 mb-6">
        {PROVIDERS.map((p) => {
          const selected = providerId === p.id;
          return (
            <button
              type="button"
              key={p.id}
              onClick={() => selectProvider(p.id)}
              className={`text-left p-4 rounded-card border transition-colors ${
                selected
                  ? 'border-brand-accent bg-brand-accent/5 ring-1 ring-brand-accent/40'
                  : 'border-brand-shade3/20 bg-brand-dark-alt hover:border-brand-shade3/40'
              }`}
            >
              <div className="flex items-center justify-between mb-1">
                <span className="font-semibold text-brand-light text-sm">{p.label}</span>
                {selected && <CheckIcon className="w-4 h-4 text-brand-accent" />}
              </div>
              <p className="text-xs text-brand-shade2 leading-relaxed">{p.description}</p>
            </button>
          );
        })}
      </div>

      {/* Credentials */}
      <div className="bg-brand-dark-alt rounded-card border border-brand-shade3/15 p-5 space-y-4">
        <div>
          <label className="block text-sm font-medium text-brand-light mb-1">
            Display name <span className="text-brand-shade3 font-normal">(URL slug)</span>
          </label>
          <input
            type="text"
            value={displayName}
            onChange={(e) => {
              setDisplayName(e.target.value);
              setStatus({ kind: 'idle' });
            }}
            placeholder="default"
            className={`w-full px-3 py-2 bg-brand-dark border rounded-btn text-sm text-brand-light focus:outline-none focus:ring-1 ${
              displayName.trim() && !SLUG_RE.test(displayName.trim())
                ? 'border-red-500/50 focus:border-red-500 focus:ring-red-500'
                : 'border-brand-shade3/30 focus:border-brand-accent focus:ring-brand-accent'
            }`}
          />
          {displayName.trim() && !SLUG_RE.test(displayName.trim()) ? (
            <p className="mt-1 text-xs text-red-400">
              Use lowercase letters, digits, hyphens (e.g. <code>default</code>, <code>glm-4</code>).
              No slashes or spaces — that's why your model identifier goes in <em>Model name</em> below.
            </p>
          ) : (
            <p className="mt-1 text-xs text-brand-shade3">
              URL-safe identifier. Lowercase letters, digits, hyphens. The model identifier (with <code>/</code>) goes in <em>Model name</em>.
            </p>
          )}
        </div>

        <div>
          <label className="block text-sm font-medium text-brand-light mb-1">
            Model name
          </label>
          <input
            type="text"
            value={modelName}
            onChange={(e) => {
              setModelName(e.target.value);
              setStatus({ kind: 'idle' });
            }}
            placeholder={provider.defaultModel || provider.modelHint}
            className="w-full px-3 py-2 bg-brand-dark border border-brand-shade3/30 rounded-btn text-sm text-brand-light font-mono focus:outline-none focus:border-brand-accent focus:ring-1 focus:ring-brand-accent"
          />
          <p className="mt-1 text-xs text-brand-shade3">{provider.modelHint}</p>
        </div>

        {(provider.requiresBaseUrl || provider.baseUrl) && (
          <div>
            <label className="block text-sm font-medium text-brand-light mb-1">Base URL</label>
            <input
              type="text"
              value={baseUrl}
              onChange={(e) => {
                setBaseUrl(e.target.value);
                setStatus({ kind: 'idle' });
              }}
              placeholder="https://api.example.com/v1"
              disabled={!provider.requiresBaseUrl}
              className="w-full px-3 py-2 bg-brand-dark border border-brand-shade3/30 rounded-btn text-sm text-brand-light font-mono focus:outline-none focus:border-brand-accent focus:ring-1 focus:ring-brand-accent disabled:opacity-60"
            />
            {!provider.requiresBaseUrl && (
              <p className="mt-1 text-xs text-brand-shade3">Auto-configured for {provider.label}.</p>
            )}
          </div>
        )}

        <div>
          <label className="flex items-center gap-1.5 text-sm font-medium text-brand-light mb-1">
            <KeyIcon className="w-4 h-4" />
            API key
          </label>
          <input
            type="password"
            value={apiKey}
            onChange={(e) => {
              setApiKey(e.target.value);
              setStatus({ kind: 'idle' });
            }}
            placeholder={provider.apiKeyHint}
            className="w-full px-3 py-2 bg-brand-dark border border-brand-shade3/30 rounded-btn text-sm text-brand-light font-mono focus:outline-none focus:border-brand-accent focus:ring-1 focus:ring-brand-accent"
            autoComplete="off"
          />
          <p className="mt-1 text-xs text-brand-shade3">{provider.apiKeyHint}</p>
        </div>
      </div>

      {/* Status */}
      {status.kind === 'success' && (
        <div className="mt-4 flex items-center gap-2 p-3 bg-green-500/10 border border-green-500/30 rounded-btn text-sm text-green-400">
          <CheckIcon className="w-4 h-4 shrink-0" />
          <span>
            Connected. Model <strong className="font-mono">{status.modelName}</strong> is ready.
          </span>
        </div>
      )}
      {status.kind === 'error' && (
        <div className="mt-4 flex items-start gap-2 p-3 bg-red-500/10 border border-red-500/30 rounded-btn text-sm text-red-400">
          <XIcon className="w-4 h-4 mt-0.5 shrink-0" />
          <span className="break-words">{status.message}</span>
        </div>
      )}

      {/* Actions */}
      <div className="mt-6 flex items-center justify-between">
        <p className="text-xs text-brand-shade3">
          Your key is stored on your Engine's database, never transmitted to syntheticbrew.ai.
        </p>
        <div className="flex items-center gap-3">
          {platformDefault && (
            <button
              type="button"
              onClick={onSuccess}
              disabled={status.kind === 'testing'}
              className="px-4 py-2 text-sm font-medium text-brand-shade2 hover:text-brand-light transition-colors disabled:opacity-60 disabled:cursor-not-allowed"
            >
              Continue without a key
            </button>
          )}
          <button
            type="submit"
            disabled={status.kind === 'testing'}
            className="flex items-center gap-2 px-5 py-2 bg-brand-accent text-white rounded-btn text-sm font-medium hover:bg-brand-accent-hover transition-colors disabled:opacity-60 disabled:cursor-not-allowed"
          >
            {status.kind === 'testing' ? (
              <>
                <SpinnerIcon className="w-4 h-4" />
                Connecting…
              </>
            ) : (
              'Next'
            )}
          </button>
        </div>
      </div>
    </form>
  );
}

function Step2Template({
  onDone,
}: {
  onDone: (schemaName?: string) => void;
}) {
  const [selected, setSelected] = useState<TemplateId | null>(null);
  const [creating, setCreating] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Backend fork endpoint creates schema+agents+relations+capabilities in
  // a single transaction and sets entry_agent_id + chat_enabled. Returns
  // both schema_id (UUID) and schema_name; engine 1.1.0+ URLs are name-keyed.
  async function createFromCatalog(template: Template): Promise<string> {
    if (!template.catalogName) {
      throw new Error(`template ${template.id} has no catalogName`);
    }
    const forked = await api.forkSchemaTemplate(template.catalogName, template.schemaName);
    return forked.schema_name;
  }

  // Blank-canvas path: schema + entry agent + PATCH entry_agent_id (the
  // schema API accepts the agent *name*, not its UUID). No agent_relation
  // — the domain rejects self-loops and a lone entry agent is valid.
  async function createBlankSchema(template: Template): Promise<string> {
    if (!template.agentName || !template.systemPrompt) {
      throw new Error('blank template missing agent definition');
    }

    const schema = await api.createSchema({
      name: template.schemaName,
      description: `Created from ${template.label} template during onboarding`,
    });

    // Idempotent: re-running onboarding must not fail on existing agent.
    try {
      await api.createAgent({
        name: template.agentName,
        system_prompt: template.systemPrompt,
      });
    } catch (err) {
      const message = err instanceof Error ? err.message.toLowerCase() : '';
      const benign =
        message.includes('exists') ||
        message.includes('duplicate') ||
        message.includes('conflict') ||
        message.includes('already');
      if (!benign) throw err;
    }

    await api.updateSchema(schema.name, { entry_agent_id: template.agentName });

    return schema.name;
  }

  async function createFromTemplate(template: Template) {
    setCreating(true);
    setError(null);
    try {
      const schemaName = template.catalogName
        ? await createFromCatalog(template)
        : await createBlankSchema(template);
      onDone(schemaName);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to create template.');
    } finally {
      setCreating(false);
    }
  }

  async function handleContinue() {
    if (!selected) return;
    const template = TEMPLATES.find((t) => t.id === selected);
    if (!template) return;
    await createFromTemplate(template);
  }

  return (
    <div className="w-full max-w-3xl mx-auto">
      <div className="mb-6">
        <h1 className="text-2xl font-bold text-brand-light mb-1">Pick a starter (optional)</h1>
        <p className="text-sm text-brand-shade2">
          Start from a pre-built workspace or skip to a blank canvas. You can always add more
          later.
        </p>
      </div>

      <div className="grid grid-cols-1 md:grid-cols-3 gap-3 mb-6">
        {TEMPLATES.map((t) => {
          const active = selected === t.id;
          return (
            <button
              key={t.id}
              type="button"
              data-testid={`template-${t.id}`}
              onClick={() => setSelected(t.id)}
              disabled={creating}
              className={`text-left p-4 rounded-card border transition-colors ${
                active
                  ? 'border-brand-accent bg-brand-accent/5 ring-1 ring-brand-accent/40'
                  : 'border-brand-shade3/20 bg-brand-dark-alt hover:border-brand-shade3/40'
              } disabled:opacity-60`}
            >
              <div className="flex items-center justify-between mb-2">
                <span className="font-semibold text-brand-light text-sm">{t.label}</span>
                {active && <CheckIcon className="w-4 h-4 text-brand-accent" />}
              </div>
              <p className="text-xs text-brand-shade2 leading-relaxed">{t.description}</p>
            </button>
          );
        })}
      </div>

      {selected && (() => {
        const t = TEMPLATES.find((x) => x.id === selected);
        if (!t) return null;
        const isCatalog = !!t.catalogName;
        return (
          <div className="mb-6 p-4 bg-brand-dark-alt border border-brand-shade3/15 rounded-card">
            <p className="text-xs text-brand-shade3 mb-1">You'll get:</p>
            <p className="text-sm text-brand-light">
              A schema named{' '}
              <strong className="font-mono">{t.schemaName}</strong>{' '}
              {isCatalog
                ? 'with a preconfigured multi-agent flow and chat enabled — ready to test immediately.'
                : (
                    <>
                      with a single entry agent (
                      <span className="font-mono text-brand-shade2">{t.agentName}</span>
                      ).
                    </>
                  )}
            </p>
          </div>
        );
      })()}

      {error && (
        <div className="mb-4 flex items-start gap-2 p-3 bg-red-500/10 border border-red-500/30 rounded-btn text-sm text-red-400">
          <XIcon className="w-4 h-4 mt-0.5 shrink-0" />
          <span className="break-words">{error}</span>
        </div>
      )}

      <div className="flex items-center justify-end gap-3">
        <button
          type="button"
          // Wrapper required — bare onClick={onDone} would forward the
          // MouseEvent as the schemaId arg.
          onClick={() => onDone()}
          disabled={creating}
          className="px-4 py-2 bg-brand-dark border border-brand-shade3/30 text-brand-light rounded-btn text-sm font-medium hover:border-brand-shade3/60 transition-colors disabled:opacity-60"
        >
          Skip
        </button>
        <button
          type="button"
          onClick={handleContinue}
          disabled={!selected || creating}
          className="flex items-center gap-2 px-5 py-2 bg-brand-accent text-white rounded-btn text-sm font-medium hover:bg-brand-accent-hover transition-colors disabled:opacity-40 disabled:cursor-not-allowed"
        >
          {creating ? (
            <>
              <SpinnerIcon className="w-4 h-4" />
              Creating…
            </>
          ) : (
            'Create & continue'
          )}
        </button>
      </div>
    </div>
  );
}

export default function OnboardingWizard() {
  const [step, setStep] = useState<1 | 2>(1);
  // Deployments that provide a default model let the user continue keyless.
  const [platformDefault, setPlatformDefault] = useState(false);
  const navigate = useNavigate();

  useEffect(() => {
    let cancelled = false;
    api
      .health()
      .then((h) => {
        if (!cancelled) setPlatformDefault(!!h?.platform_default_model);
      })
      .catch(() => {
        /* fail closed: no free-plan affordance, mandatory key setup remains */
      });
    return () => {
      cancelled = true;
    };
  }, []);

  // Drop into the new schema's canvas when we know its name; fall back to
  // the schemas list (Skip path). Engine 1.1.0+ URLs are name-keyed.
  const finish = (schemaName?: string) => {
    if (schemaName) {
      navigate(`/schemas/${encodeURIComponent(schemaName)}`);
      return;
    }
    navigate('/schemas');
  };

  return (
    <div className="fixed inset-0 z-50 bg-brand-dark overflow-auto">
      <div className="min-h-full flex flex-col">
        <div className="px-6 py-4 border-b border-brand-shade3/10 bg-brand-dark-surface">
          <div className="max-w-3xl mx-auto flex items-center justify-between">
            <div className="text-sm font-semibold text-brand-light">SyntheticBrew setup</div>
            <div className="text-xs text-brand-shade3">
              {platformDefault ? 'Platform default model — bring your own key for full control' : 'BYOK — bring your own key'}
            </div>
          </div>
        </div>

        <div className="flex-1 px-6 py-10">
          <ProgressHeader step={step} />
          {step === 1 && <Step1ConnectLLM onSuccess={() => setStep(2)} platformDefault={platformDefault} />}
          {step === 2 && <Step2Template onDone={finish} />}
        </div>
      </div>
    </div>
  );
}
