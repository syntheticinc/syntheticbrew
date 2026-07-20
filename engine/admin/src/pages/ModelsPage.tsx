import { useState, useMemo, useEffect, type FormEvent } from 'react';
import { api } from '../api/client';
import { useApi } from '../hooks/useApi';
import { useModelRegistry } from '../hooks/useModelRegistry';
import { useAdminRefresh } from '../hooks/useAdminRefresh';
import DataTable from '../components/DataTable';
import { emptyIcons } from '../components/EmptyState';
import DetailPanel, { DetailRow, DetailSection } from '../components/DetailPanel';
import FormModal from '../components/FormModal';
import FormField from '../components/FormField';
import ConfirmDialog from '../components/ConfirmDialog';
import PageContainer from '../components/PageContainer';
import TierBadge, { CustomModelBadge } from '../components/TierBadge';
import { ToastProvider, useToast } from '../components/builder/Toast';
import type { Model, ModelKind, CreateModelRequest, ModelRegistryEntry } from '../types';

const PROVIDER_TYPES = [
  { value: 'ollama', label: 'Ollama (local)' },
  { value: 'openai_compatible', label: 'OpenAI Compatible' },
  { value: 'openrouter', label: 'OpenRouter' },
  { value: 'anthropic', label: 'Anthropic' },
  { value: 'azure_openai', label: 'Azure OpenAI' },
  { value: 'google', label: 'Google (Gemini)' },
  { value: 'embedding', label: 'Embedding Provider' },
];

// Kind filter persisted in localStorage so operators keep their chosen view
// across page navigations.
type KindFilter = 'all' | 'chat' | 'embedding';
const KIND_FILTER_KEY = 'syntheticbrew_models_kind_filter';

function readKindFilter(): KindFilter {
  const v = localStorage.getItem(KIND_FILTER_KEY);
  if (v === 'chat' || v === 'embedding' || v === 'all') return v;
  return 'all';
}

const PROVIDER_BASE_URLS: Record<string, string> = {
  openrouter: 'https://openrouter.ai/api/v1',
};

const PROVIDER_HINTS: Record<string, string> = {
  azure_openai: 'Use your Azure resource endpoint as Base URL (e.g. https://my-company.openai.azure.com). Model Name = deployment name.',
  google: 'Uses native Gemini API (generateContent). No Base URL needed — just API key and model name.',
  embedding: 'OpenAI-compatible embedding API (POST /embeddings). Used for document vectorization in Knowledge capability. Recommended: text-embedding-3-small (1536 dim, $0.02/1M tokens).',
};

// Display Name on the wire is the URL slug — same DNS-label format the
// engine enforces for every operator-facing resource (see name_validation.go).
// Mirror the rules client-side so users see the precise rule that just got
// violated instead of a generic backend 400.
const NAME_REGEX = /^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/;
const UUID_REGEX = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
const RESERVED_NAMES = new Set([
  'chat', 'agents', 'agent-relations', 'memory', 'files', 'health', 'auth',
  'tasks', 'models', 'knowledge-bases', 'schemas', 'mcp-servers', 'tokens',
  'sessions', 'metrics',
]);
const MAX_NAME_LENGTH = 100;
const NAME_HINT =
  'URL slug: lowercase letters, digits, hyphens. Must start and end with a letter or digit (e.g. "my-llama", "glm-4").';

function validateModelDisplayName(name: string): string | null {
  if (!name) return 'Display name is required.';
  if (name.length > MAX_NAME_LENGTH) return `Display name must be at most ${MAX_NAME_LENGTH} characters.`;
  if (/[A-Z]/.test(name)) return 'Uppercase letters are not allowed — use lowercase only (e.g. "my-llama", not "My-Llama").';
  if (/\s/.test(name)) return 'Spaces are not allowed — use hyphens instead (e.g. "my-llama").';
  if (/[^a-z0-9-]/.test(name)) return 'Only lowercase letters, digits, and hyphens are allowed.';
  if (!NAME_REGEX.test(name)) return 'Must start and end with a letter or digit (no leading/trailing hyphens).';
  if (UUID_REGEX.test(name)) return 'Display name cannot be UUID-shaped.';
  if (RESERVED_NAMES.has(name)) return `"${name}" is reserved (collides with API route segment).`;
  return null;
}

function providerTypeForRegistry(provider: string): string {
  if (provider === 'openrouter') return 'openrouter';
  return provider;
}

const emptyForm: CreateModelRequest = {
  name: '',
  type: 'ollama',
  kind: 'chat',
  base_url: '',
  model_name: '',
  api_key: '',
};

export default function ModelsPage() {
  return (
    <ToastProvider>
      <ModelsPageInner />
    </ToastProvider>
  );
}

function ModelsPageInner() {
  const { addToast } = useToast();
  const [kindFilter, setKindFilter] = useState<KindFilter>(() => readKindFilter());

  useEffect(() => {
    localStorage.setItem(KIND_FILTER_KEY, kindFilter);
  }, [kindFilter]);

  // Call the server with the active filter so the table only shows the slice
  // the operator asked for. The `all` branch omits the param so the backend
  // returns both kinds.
  const { data: models, loading, error, refetch } = useApi(
    () => api.listModels(kindFilter === 'all' ? undefined : { kind: kindFilter }),
    [kindFilter],
  );
  useAdminRefresh(refetch);
  const { registry, registryByModelName } = useModelRegistry();

  const [selected, setSelected] = useState<Model | null>(null);
  const [showForm, setShowForm] = useState(false);
  const [editTarget, setEditTarget] = useState<Model | null>(null);
  const [form, setForm] = useState<CreateModelRequest>({ ...emptyForm });
  const [saving, setSaving] = useState(false);
  const [deleteTarget, setDeleteTarget] = useState<string | null>(null);

  // Filter registry models by selected provider for the model picker
  const registryModelsForProvider = useMemo(() => {
    if (!registry.length) return [];
    const providerType = providerTypeForRegistry(form.type);
    return registry.filter((entry) => entry.provider === providerType);
  }, [registry, form.type]);

  function findRegistryEntry(modelName: string): ModelRegistryEntry | undefined {
    return registryByModelName.get(modelName);
  }

  function openCreate() {
    setForm({ ...emptyForm });
    setEditTarget(null);
    setShowForm(true);
  }

  function openEdit(model: Model) {
    setForm({
      name: model.name,
      type: model.type,
      kind: model.kind ?? 'chat',
      base_url: model.base_url ?? '',
      model_name: model.model_name,
      api_key: '',
      embedding_dim: model.embedding_dim,
      api_version: model.api_version,
    });
    setEditTarget(model);
    setShowForm(true);
  }

  function closeForm() {
    setShowForm(false);
    setEditTarget(null);
    setForm({ ...emptyForm });
  }

  function handleProviderChange(providerType: string) {
    const autoUrl = PROVIDER_BASE_URLS[providerType];
    setForm((prev) => ({
      ...prev,
      type: providerType,
      // Legacy `type: embedding` stayed as a shorthand for embedding-provider
      // config. With Wave 5 the canonical split is `kind`, but we keep the
      // implicit sync so picking the Embedding provider auto-flips the kind
      // radio below — the operator can still override it either way.
      kind: providerType === 'embedding' ? 'embedding' : prev.kind ?? 'chat',
      base_url: autoUrl ?? (providerType === prev.type ? prev.base_url : ''),
      model_name: '',
      embedding_dim: providerType === 'embedding' ? (prev.embedding_dim || 1536) : prev.embedding_dim,
    }));
  }

  function handleKindChange(kind: ModelKind) {
    setForm((prev) => ({
      ...prev,
      kind,
      // When flipping to embedding make sure the embedding_dim hint is set so
      // the operator sees a sensible default.
      embedding_dim: kind === 'embedding' ? prev.embedding_dim ?? 1536 : prev.embedding_dim,
    }));
  }

  function handleRegistryModelSelect(registryId: string) {
    if (!registryId) return;
    const entry = registryByModelName.get(registryId);
    if (entry) {
      setForm((prev) => ({ ...prev, model_name: entry.id }));
    }
  }

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    if (!editTarget) {
      const nameErr = validateModelDisplayName(form.name);
      if (nameErr) {
        addToast(nameErr, 'error');
        return;
      }
    }
    setSaving(true);
    try {
      if (editTarget) {
        await api.updateModel(editTarget.name, form);
      } else {
        await api.createModel(form);
      }
      closeForm();
      setSelected(null);
      refetch();
    } catch (err) {
      addToast(err instanceof Error ? err.message : 'Failed to save model', 'error');
    } finally {
      setSaving(false);
    }
  }

  async function handleDelete() {
    if (!deleteTarget) return;
    try {
      await api.deleteModel(deleteTarget);
      setDeleteTarget(null);
      setSelected(null);
      refetch();
    } catch (err) {
      setDeleteTarget(null);
      addToast(err instanceof Error ? err.message : 'Failed to delete model', 'error');
    }
  }

  // handleSetDefault promotes a chat model to default. The backend atomically
  // clears the previous default and flips the target — we just refetch so the
  // table reflects the swap. No-op for already-default rows (defensive — the
  // UI hides the button in that case, but this keeps the call site safe if
  // someone invokes it programmatically).
  async function handleSetDefault(model: Model) {
    if (model.is_default) return;
    try {
      await api.setDefaultModel(model.name);
      refetch();
      addToast(`"${model.name}" is now the default chat model`, 'success');
    } catch (err) {
      addToast(err instanceof Error ? err.message : 'Failed to set default model', 'error');
    }
  }

  const isEdit = editTarget !== null;
  const isBaseUrlReadOnly = form.type in PROVIDER_BASE_URLS;
  const providerHint = PROVIDER_HINTS[form.type];
  // Inline validation only fires once the user has typed something — empty
  // field shouldn't shout "required" before they've started.
  const nameValidationError = !isEdit && form.name.length > 0
    ? validateModelDisplayName(form.name)
    : null;

  const columns = [
    {
      key: 'name',
      header: 'Name',
      render: (row: Model) => {
        const entry = findRegistryEntry(row.model_name);
        const isDefault = row.is_default === true && row.kind === 'chat';
        return (
          <div className="flex items-center gap-2">
            <span>{row.name}</span>
            {entry ? (
              <TierBadge tier={entry.tier} />
            ) : (
              <CustomModelBadge />
            )}
            {isDefault && (
              <span
                title="Default chat model — used by agents that don't specify one explicitly"
                className="px-1.5 py-0.5 bg-brand-accent/15 border border-brand-accent/40 rounded text-[10px] text-brand-accent font-semibold uppercase tracking-wider"
              >
                Default
              </span>
            )}
          </div>
        );
      },
    },
    {
      key: 'kind',
      header: 'Kind',
      render: (row: Model) => (
        <span
          className={
            row.kind === 'embedding'
              ? 'px-1.5 py-0.5 bg-purple-500/20 border border-purple-500/30 rounded text-xs text-purple-400 font-medium'
              : 'px-1.5 py-0.5 bg-blue-500/20 border border-blue-500/30 rounded text-xs text-blue-400 font-medium'
          }
        >
          {row.kind === 'embedding' ? 'Embedding' : 'Chat'}
        </span>
      ),
    },
    {
      key: 'type',
      header: 'Provider',
      render: (row: Model) => (
        <span className="flex items-center gap-1.5">
          <span className="px-2 py-0.5 bg-brand-shade3/15 rounded text-xs text-brand-shade2 font-medium">
            {row.type}
          </span>
        </span>
      ),
    },
    { key: 'model_name', header: 'Model' },
    {
      key: 'base_url',
      header: 'URL',
      render: (row: Model) => (
        <span className="font-mono text-xs text-brand-shade3">{row.base_url || '--'}</span>
      ),
    },
    {
      key: 'has_api_key',
      header: 'API Key',
      render: (row: Model) =>
        row.has_api_key ? (
          <span className="text-xs text-status-active font-medium">Configured</span>
        ) : (
          <span className="text-xs text-brand-shade3">--</span>
        ),
    },
  ];

  if (loading) return <div className="text-brand-shade3">Loading models...</div>;
  if (error) return <div className="text-red-400">Error: {error}</div>;

  const selectedRegistryEntry = selected ? findRegistryEntry(selected.model_name) : undefined;

  return (
    <PageContainer>
      <div className="flex items-center justify-between mb-6">
        <h1 className="text-2xl font-bold text-brand-light">Models</h1>
        <button
          onClick={openCreate}
          className="px-4 py-2 bg-brand-accent text-white rounded-btn text-sm font-medium hover:bg-brand-accent-hover transition-colors"
        >
          Add Model
        </button>
      </div>

      {/* Kind filter — persisted in localStorage so operators keep their view
          across navigations. Toggles map to server-side ?kind= filter. */}
      <div className="mb-4 flex items-center gap-1" role="tablist" aria-label="Filter by model kind">
        {(
          [
            { value: 'all', label: 'All' },
            { value: 'chat', label: 'Chat' },
            { value: 'embedding', label: 'Embedding' },
          ] as { value: KindFilter; label: string }[]
        ).map((opt) => (
          <button
            key={opt.value}
            role="tab"
            aria-selected={kindFilter === opt.value}
            onClick={() => setKindFilter(opt.value)}
            className={
              kindFilter === opt.value
                ? 'px-3 py-1.5 bg-brand-accent text-white rounded-btn text-xs font-medium'
                : 'px-3 py-1.5 bg-brand-dark-alt border border-brand-shade3/30 text-brand-shade2 rounded-btn text-xs font-medium hover:bg-brand-dark transition-colors'
            }
          >
            {opt.label}
          </button>
        ))}
      </div>

      <div className="bg-brand-dark-alt rounded-card border border-brand-shade3/15">
        <DataTable
          columns={columns}
          data={models ?? []}
          keyField="id"
          onRowClick={setSelected}
          activeKey={selected?.id}
          emptyMessage="No models configured"
          emptyIcon={emptyIcons.models}
          emptyAction={{ label: 'Add Model', onClick: openCreate }}
        />
      </div>

      {/* Detail Panel */}
      <DetailPanel
        open={selected !== null}
        onClose={() => setSelected(null)}
        title={selected?.name ?? ''}
        actions={
          selected ? (
            <>
              <button
                onClick={() => openEdit(selected)}
                className="flex-1 px-4 py-2 bg-brand-accent text-white rounded-btn text-sm font-medium hover:bg-brand-accent-hover transition-colors"
              >
                Edit
              </button>
              {selected.kind === 'chat' && !selected.is_default && (
                <button
                  onClick={() => handleSetDefault(selected)}
                  title="Promote this model to default for new agents"
                  className="px-4 py-2 text-brand-accent border border-brand-accent/40 rounded-btn text-sm font-medium hover:bg-brand-accent/10 transition-colors"
                >
                  Set as default
                </button>
              )}
              <button
                onClick={() => setDeleteTarget(selected.name)}
                disabled={selected.is_default === true && selected.kind === 'chat'}
                title={
                  selected.is_default === true && selected.kind === 'chat'
                    ? 'Promote another chat model to default before removing this one'
                    : undefined
                }
                className="px-4 py-2 text-red-400 border border-red-500/30 rounded-btn text-sm font-medium hover:bg-red-500/10 disabled:opacity-40 disabled:cursor-not-allowed disabled:hover:bg-transparent transition-colors"
              >
                Remove
              </button>
            </>
          ) : undefined
        }
      >
        {selected && (
          <>
            <DetailSection title="Provider">
              <DetailRow label="Kind">
                <span
                  className={
                    selected.kind === 'embedding'
                      ? 'px-1.5 py-0.5 bg-purple-500/20 border border-purple-500/30 rounded text-xs text-purple-400 font-medium'
                      : 'px-1.5 py-0.5 bg-blue-500/20 border border-blue-500/30 rounded text-xs text-blue-400 font-medium'
                  }
                >
                  {selected.kind === 'embedding' ? 'Embedding' : 'Chat'}
                </span>
              </DetailRow>
              <DetailRow label="Type">
                <span className="px-2 py-0.5 bg-brand-shade3/15 rounded text-xs text-brand-shade2 font-medium">
                  {selected.type}
                </span>
              </DetailRow>
              <DetailRow label="Model Name">{selected.model_name}</DetailRow>
              <DetailRow label="Tier">
                {selectedRegistryEntry ? (
                  <TierBadge tier={selectedRegistryEntry.tier} />
                ) : (
                  <CustomModelBadge />
                )}
              </DetailRow>
              {selected.base_url && <DetailRow label="Base URL"><code className="font-mono text-xs">{selected.base_url}</code></DetailRow>}
              <DetailRow label="API Key">
                {selected.has_api_key ? (
                  <span className="text-status-active font-medium text-xs">Configured</span>
                ) : (
                  <span className="text-brand-shade3 text-xs">Not set</span>
                )}
              </DetailRow>
              {selected.kind === 'chat' && (
                <DetailRow label="Default">
                  {selected.is_default ? (
                    <span className="px-1.5 py-0.5 bg-brand-accent/15 border border-brand-accent/40 rounded text-[10px] text-brand-accent font-semibold uppercase tracking-wider">
                      Default
                    </span>
                  ) : (
                    <span className="text-brand-shade3 text-xs">No</span>
                  )}
                </DetailRow>
              )}
            </DetailSection>

            {selectedRegistryEntry && (
              <DetailSection title="Registry Info">
                <DetailRow label="Display Name">{selectedRegistryEntry.display_name}</DetailRow>
                <DetailRow label="Context Window">{selectedRegistryEntry.context_window.toLocaleString()} tokens</DetailRow>
                <DetailRow label="Supports Tools">
                  {selectedRegistryEntry.supports_tools ? (
                    <span className="text-status-active font-medium text-xs">Yes</span>
                  ) : (
                    <span className="text-brand-shade3 text-xs">No</span>
                  )}
                </DetailRow>
                {selectedRegistryEntry.description && (
                  <DetailRow label="Description">
                    <span className="text-xs text-brand-shade2">{selectedRegistryEntry.description}</span>
                  </DetailRow>
                )}
                {selectedRegistryEntry.recommended_for?.length > 0 && (
                  <DetailRow label="Recommended For">
                    <div className="flex flex-wrap gap-1">
                      {selectedRegistryEntry.recommended_for.map((use) => (
                        <span key={use} className="px-1.5 py-0.5 bg-brand-dark border border-brand-shade3/30 rounded text-xs text-brand-shade2">
                          {use}
                        </span>
                      ))}
                    </div>
                  </DetailRow>
                )}
              </DetailSection>
            )}

            <DetailSection title="Timestamps">
              <DetailRow label="Created">{new Date(selected.created_at).toLocaleString()}</DetailRow>
            </DetailSection>
          </>
        )}
      </DetailPanel>

      {/* Create / Edit Form Modal */}
      <FormModal
        open={showForm}
        onClose={closeForm}
        title={isEdit ? 'Edit Model' : 'Add Model'}
        onSubmit={handleSubmit}
        submitLabel={isEdit ? 'Save Changes' : 'Add Model'}
        loading={saving}
      >
        <FormField
          label="Display Name"
          value={form.name}
          onChange={(v) => setForm({ ...form, name: v })}
          required
          disabled={isEdit}
          placeholder="my-llama"
          hint={isEdit ? 'Name cannot be changed.' : NAME_HINT}
          error={nameValidationError ?? undefined}
        />

        {/* Kind selector. Wave 5: agents require chat, KBs require embedding.
            Rendered as radio pair rather than a dropdown so the choice stays
            visible — picking the wrong kind at create time is a common
            mistake that would later fail agent/KB wiring. */}
        <div>
          <label className="block text-sm font-medium text-brand-light mb-1">
            Kind<span className="text-brand-accent ml-0.5">*</span>
          </label>
          <div className="flex gap-2" role="radiogroup" aria-label="Model kind">
            {(
              [
                { value: 'chat', label: 'Chat Model', hint: 'Used by agents for completions' },
                { value: 'embedding', label: 'Embedding Model', hint: 'Used by Knowledge Bases for vectorization' },
              ] as { value: ModelKind; label: string; hint: string }[]
            ).map((opt) => {
              const selected = (form.kind ?? 'chat') === opt.value;
              return (
                <label
                  key={opt.value}
                  className={
                    selected
                      ? 'flex-1 px-3 py-2 rounded-card border border-brand-accent bg-brand-accent/10 cursor-pointer'
                      : 'flex-1 px-3 py-2 rounded-card border border-brand-shade3/30 bg-brand-dark-alt cursor-pointer hover:border-brand-shade3/60'
                  }
                >
                  <input
                    type="radio"
                    name="model-kind"
                    value={opt.value}
                    checked={selected}
                    onChange={() => handleKindChange(opt.value)}
                    disabled={isEdit}
                    className="sr-only"
                  />
                  <div className={`text-sm font-medium ${selected ? 'text-brand-accent' : 'text-brand-light'}`}>
                    {opt.label}
                  </div>
                  <div className="text-xs text-brand-shade3 mt-0.5">{opt.hint}</div>
                </label>
              );
            })}
          </div>
          {isEdit && (
            <p className="mt-1 text-xs text-brand-shade3">Kind cannot be changed after creation.</p>
          )}
        </div>

        <FormField
          label="Provider"
          type="select"
          value={form.type}
          onChange={handleProviderChange}
          options={PROVIDER_TYPES}
        />

        {providerHint && (
          <div className="p-3 bg-blue-500/10 border border-blue-500/30 rounded-btn text-xs text-blue-400 leading-relaxed">
            {providerHint}
          </div>
        )}

        {/* Registry model picker */}
        {registryModelsForProvider.length > 0 && (
          <div>
            <label className="block text-sm font-medium text-brand-light mb-1">
              Select from Registry
            </label>
            <select
              value={form.model_name}
              onChange={(e) => handleRegistryModelSelect(e.target.value)}
              className="w-full px-3 py-2 bg-brand-dark border border-brand-shade3/30 rounded-card text-sm text-brand-light focus:outline-none focus:border-brand-accent focus:ring-1 focus:ring-brand-accent transition-colors"
            >
              <option value="">-- Or type model name below --</option>
              {registryModelsForProvider.map((entry) => (
                <option key={entry.id} value={entry.id}>
                  {entry.display_name} (Tier {entry.tier})
                </option>
              ))}
            </select>
            <p className="mt-1 text-xs text-brand-shade3">
              Pick a known model from the registry or enter a custom model name.
            </p>
          </div>
        )}

        <FormField
          label="Model Name"
          value={form.model_name}
          onChange={(v) => setForm({ ...form, model_name: v })}
          required
          placeholder="llama-4-scout"
        />
        <FormField
          label="Base URL"
          value={form.base_url ?? ''}
          onChange={(v) => setForm({ ...form, base_url: v })}
          placeholder="http://localhost:11434"
          disabled={isBaseUrlReadOnly}
          hint={
            isBaseUrlReadOnly
              ? 'Auto-configured for this provider.'
              : 'Required for Ollama and OpenAI-compatible providers.'
          }
        />
        {form.type !== 'ollama' && (
          <FormField
            label="API Key"
            type="password"
            value={form.api_key ?? ''}
            onChange={(v) => setForm({ ...form, api_key: v })}
            placeholder={isEdit ? '(unchanged if empty)' : 'sk-...'}
            hint={isEdit ? 'Leave empty to keep the existing key.' : undefined}
          />
        )}
        {form.type === 'azure_openai' && (
          <FormField
            label="API Version"
            value={form.api_version ?? '2024-10-21'}
            onChange={(v) => setForm({ ...form, api_version: v })}
            placeholder="2024-10-21"
            hint="Azure OpenAI API version (default: 2024-10-21)"
          />
        )}
        {form.kind === 'embedding' && (
          <FormField
            label="Embedding Dimension"
            value={String(form.embedding_dim ?? 1536)}
            onChange={(v) => setForm({ ...form, embedding_dim: parseInt(v) || 0 })}
            placeholder="1536"
            hint="Vector dimension (e.g. 1536 for text-embedding-3-small, 3072 for text-embedding-3-large, 768 for nomic-embed-text)"
          />
        )}
      </FormModal>

      <ConfirmDialog
        open={deleteTarget !== null}
        onClose={() => setDeleteTarget(null)}
        onConfirm={handleDelete}
        title="Remove Model"
        message={
          <>
            Remove model <strong className="text-brand-light">{deleteTarget}</strong>?
          </>
        }
        confirmLabel="Remove"
        variant="danger"
      />
    </PageContainer>
  );
}
