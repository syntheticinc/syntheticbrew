import { useEffect, useState } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { api } from '../api/client';
import { useApi } from '../hooks/useApi';
import PageContainer from '../components/PageContainer';
import type { Schema, SchemaTemplate } from '../types';

function formatRelativeTime(iso: string) {
  const diff = Date.now() - new Date(iso).getTime();
  if (diff < 60_000) return 'just now';
  if (diff < 3_600_000) return `${Math.floor(diff / 60_000)}m ago`;
  if (diff < 86_400_000) return `${Math.floor(diff / 3_600_000)}h ago`;
  return `${Math.floor(diff / 86_400_000)}d ago`;
}

// sanitizeSchemaName makes a human-entered schema name safe for the DB's
// unique constraint — lowercase, alphanumerics + hyphens, collapsed
// whitespace. Returns empty on empty input.
function sanitizeSchemaName(raw: string): string {
  return raw
    .toLowerCase()
    .trim()
    .replace(/[^a-z0-9\s-]/g, '')
    .replace(/\s+/g, '-')
    .replace(/-+/g, '-');
}

interface TemplatePickerProps {
  onClose: () => void;
  onForked: (schemaName: string) => void;
}

function TemplatePicker({ onClose, onForked }: TemplatePickerProps) {
  const [templates, setTemplates] = useState<SchemaTemplate[]>([]);
  const [loading, setLoading] = useState(true);
  const [listError, setListError] = useState<string | null>(null);
  const [selected, setSelected] = useState<SchemaTemplate | null>(null);
  const [schemaName, setSchemaName] = useState('');
  const [forking, setForking] = useState(false);
  const [forkError, setForkError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setListError(null);
    api
      .listSchemaTemplates()
      .then((resp) => {
        if (!cancelled) setTemplates(resp.templates);
      })
      .catch((err) => {
        if (!cancelled) setListError(err instanceof Error ? err.message : String(err));
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, []);

  async function handleFork() {
    if (!selected) return;
    const clean = sanitizeSchemaName(schemaName);
    if (!clean) {
      setForkError('Schema name is required (letters, digits, hyphens).');
      return;
    }
    setForking(true);
    setForkError(null);
    try {
      const resp = await api.forkSchemaTemplate(selected.name, clean);
      // Engine 1.1.0+: navigate by schema_name (operator-facing handle),
      // not schema_id (UUID — internal-only after the URL migration).
      onForked(resp.schema_name);
    } catch (err) {
      setForkError(err instanceof Error ? err.message : String(err));
    } finally {
      setForking(false);
    }
  }

  return (
    <div className="fixed inset-0 z-40 bg-black/60 flex items-center justify-center p-6">
      <div className="bg-brand-dark-surface border border-brand-shade3/25 rounded-card max-w-[840px] w-full max-h-[88vh] overflow-hidden shadow-2xl flex flex-col">
        <div className="px-5 py-4 border-b border-brand-shade3/15 flex items-center justify-between">
          <h2 className="text-[15px] font-semibold text-brand-light">Create Schema</h2>
          <button onClick={onClose} className="text-brand-shade3 hover:text-brand-light" aria-label="Close">
            ✕
          </button>
        </div>
        <div className="px-5 py-3 text-[12px] text-brand-shade3 border-b border-brand-shade3/10">
          Pick a starter template. The fork operation creates a new schema with its agents, delegations, and triggers — independent of the catalog.
        </div>

        {/* Template grid */}
        <div className="p-5 grid grid-cols-2 gap-3 overflow-y-auto flex-1">
          {loading && <div className="col-span-2 text-[12px] text-brand-shade3">Loading templates…</div>}
          {listError && (
            <div className="col-span-2 text-[12px] text-rose-400">
              Failed to load templates: {listError}
            </div>
          )}
          {!loading && !listError && templates.length === 0 && (
            <div className="col-span-2 text-[12px] text-brand-shade3">
              No templates match the current filter.
            </div>
          )}
          {templates.map((tpl) => {
            const isSelected = selected?.name === tpl.name;
            return (
              <button
                key={tpl.name}
                onClick={() => setSelected(tpl)}
                className={`text-left bg-brand-dark border rounded-card p-4 transition-colors ${
                  isSelected ? 'border-brand-accent' : 'border-brand-shade3/20 hover:border-brand-accent/40'
                }`}
              >
                <div className="flex items-center gap-2 mb-2">
                  <span className="text-[13px] font-semibold text-brand-light">{tpl.display}</span>
                  <span className="text-[9px] uppercase tracking-wider px-1.5 py-0.5 rounded bg-brand-shade3/15 text-brand-shade3">
                    {tpl.category}
                  </span>
                </div>
                <p className="text-[11px] text-brand-shade3 leading-relaxed mb-3">{tpl.description}</p>
                <div className="flex items-center gap-3 text-[10px] text-brand-shade3">
                  <span>{tpl.definition.agents.length} agents</span>
                  <span>·</span>
                  <span>
                    {tpl.definition.chat_enabled ? 'chat enabled' : 'chat disabled'}
                  </span>
                  <span>·</span>
                  <span>v{tpl.version}</span>
                </div>
              </button>
            );
          })}
        </div>

        {/* Fork footer */}
        {selected && (
          <div className="px-5 py-4 border-t border-brand-shade3/15 bg-brand-dark/40">
            <div className="flex items-center gap-3">
              <div className="flex-1">
                <label className="block text-[10px] uppercase tracking-wider text-brand-shade3 mb-1">
                  New schema name
                </label>
                <input
                  type="text"
                  value={schemaName}
                  placeholder={`e.g. ${selected.name}-${Date.now().toString(36)}`}
                  onChange={(e) => {
                    setSchemaName(e.target.value);
                    setForkError(null);
                  }}
                  disabled={forking}
                  className="w-full bg-brand-dark border border-brand-shade3/25 rounded-btn px-3 py-2 text-[12px] text-brand-light placeholder:text-brand-shade3 focus:outline-none focus:border-brand-accent/60 disabled:opacity-60"
                />
                {forkError && <div className="mt-1 text-[11px] text-rose-400">{forkError}</div>}
              </div>
              <button
                onClick={handleFork}
                disabled={forking || schemaName.trim() === ''}
                className="px-4 py-2 text-[12px] font-medium bg-brand-accent text-white rounded-btn hover:bg-brand-accent/90 transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
              >
                {forking ? 'Forking…' : 'Use template'}
              </button>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}

function SchemaCard({ schema }: { schema: Schema }) {
  return (
    <Link
      to={`/schemas/${encodeURIComponent(schema.name)}`}
      className="block bg-brand-dark-surface border border-brand-shade3/15 rounded-card hover:border-brand-shade3/35 transition-all group"
    >
      <div className="px-5 py-4 border-b border-brand-shade3/10">
        <div className="flex items-start justify-between gap-3">
          <div className="min-w-0 flex-1">
            <div className="text-base font-semibold text-brand-light truncate">{schema.name}</div>
            <div className="text-[12px] text-brand-shade3 mt-1 line-clamp-2">{schema.description ?? ''}</div>
          </div>
        </div>
      </div>

      <div className="px-5 py-3 flex items-center gap-4">
        {schema.entry_agent_name && (
          <div className="flex items-center gap-2 min-w-0">
            <span className="shrink-0 w-7 h-7 rounded-full bg-gradient-to-br from-brand-shade3/30 to-brand-shade3/10 flex items-center justify-center text-[10px] font-semibold text-brand-light border border-brand-shade3/20">
              {schema.entry_agent_name.slice(0, 2).toUpperCase()}
            </span>
            <div className="min-w-0">
              <div className="text-[10px] uppercase tracking-wider text-brand-shade3">Entry</div>
              <div className="text-[12px] font-medium text-brand-light truncate">
                {schema.entry_agent_name}
              </div>
            </div>
          </div>
        )}
        <div className="flex-1" />
        <div className="flex items-center gap-4 text-[11px] text-brand-shade3">
          <span>
            <span className="text-brand-light font-medium">{schema.agents_count}</span> agents
          </span>
        </div>
      </div>

      <div className="px-5 py-2 border-t border-brand-shade3/10 flex items-center justify-between">
        <span className="text-[10px] text-brand-shade3">
          Created {formatRelativeTime(schema.created_at)}
        </span>
        <span className="text-[11px] text-brand-shade3 group-hover:text-brand-accent transition-colors">
          Open →
        </span>
      </div>
    </Link>
  );
}

export default function SchemasPage() {
  const [picking, setPicking] = useState(false);
  const [systemExpanded, setSystemExpanded] = useState(false);
  const navigate = useNavigate();
  const { data: schemas, loading, error } = useApi(() => api.listSchemas());

  function handleForked(schemaId: string) {
    setPicking(false);
    navigate(`/schemas/${schemaId}`);
  }

  // "Restore to factory defaults" lives on the builder-schema detail page
  // (SchemaDetailPage) and the builder-assistant agent detail page
  // (AgentDrillInPage). Symmetric with agent editing — the restore affordance
  // is tied to the entity being edited, not the list. Keeping it off the list
  // avoids the surprise "why is there a reset here but not on Agents list".

  const all = schemas ?? [];
  const userSchemas = all.filter((s) => !s.is_system);
  const systemSchemas = all.filter((s) => s.is_system);

  return (
    <PageContainer>
      <div className="flex items-center justify-between mb-6">
        <div>
          <h1 className="text-2xl font-semibold text-brand-light">Schemas</h1>
          <p className="text-sm text-brand-shade3 mt-1">
            Each schema has one entry orchestrator and its delegation tree.
          </p>
        </div>
        <button
          onClick={() => setPicking(true)}
          className="px-4 py-2 text-[12px] font-medium bg-brand-accent text-white rounded-btn hover:bg-brand-accent/90 transition-colors"
        >
          + New Schema
        </button>
      </div>

      {loading && (
        <div className="text-[13px] text-brand-shade3">Loading schemas…</div>
      )}

      {error && (
        <div className="text-[13px] text-rose-400">Failed to load schemas: {error}</div>
      )}

      {!loading && !error && userSchemas.length === 0 && (
        <div className="bg-brand-dark-surface border border-dashed border-brand-shade3/25 rounded-card p-10 text-center">
          <h3 className="text-base font-semibold text-brand-light mb-2">No schemas yet</h3>
          <p className="text-[13px] text-brand-shade3 max-w-md mx-auto mb-4">
            A schema binds triggers to an entry orchestrator and its delegates. Pick a template to scaffold one.
          </p>
          <button
            onClick={() => setPicking(true)}
            className="px-4 py-2 text-[12px] text-white bg-brand-accent rounded-btn"
          >
            Create first schema
          </button>
          <p className="mt-5 text-[11px] text-brand-shade3/70">
            If ByteBrew helps you, consider{' '}
            <a
              href="https://github.com/syntheticinc/bytebrew"
              target="_blank"
              rel="noopener noreferrer"
              className="text-brand-accent hover:underline"
            >
              starring us on GitHub
            </a>
            .
          </p>
        </div>
      )}

      {userSchemas.length > 0 && (
        <div className="grid grid-cols-2 gap-4">
          {userSchemas.map((s) => (
            <SchemaCard key={s.id} schema={s} />
          ))}
        </div>
      )}

      {systemSchemas.length > 0 && (
        <div className="mt-10 pt-6 border-t border-brand-shade3/10">
          <button
            onClick={() => setSystemExpanded((e) => !e)}
            className="flex items-center gap-2 text-xs text-brand-shade3 hover:text-brand-shade2 transition-colors mb-3"
          >
            <svg
              width="12" height="12" viewBox="0 0 14 14" fill="none"
              className={`transition-transform ${systemExpanded ? 'rotate-180' : ''}`}
            >
              <path d="M3 5L7 9L11 5" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" />
            </svg>
            <span className="uppercase tracking-wider font-semibold">System Schemas</span>
            <span className="text-brand-shade3/50">({systemSchemas.length})</span>
          </button>
          {systemExpanded && (
            <>
              <p className="text-[11px] text-brand-shade3 mb-3">
                Reserved for platform internals. Cannot be deleted. Open a system schema to restore it to factory defaults.
              </p>
              <div className="grid grid-cols-2 gap-4">
                {systemSchemas.map((s) => (
                  <SchemaCard key={s.id} schema={s} />
                ))}
              </div>
            </>
          )}
        </div>
      )}

      {picking && <TemplatePicker onClose={() => setPicking(false)} onForked={handleForked} />}
    </PageContainer>
  );
}
