import React, { useState, useEffect } from 'react';
import type { CapabilityConfig } from '../../types';
import { CAPABILITY_META } from '../../types';
import { api } from '../../api/client';

interface CapabilityBlockProps {
  capability: CapabilityConfig;
  onChange: (updated: CapabilityConfig) => void;
  onRemove: () => void;
  agentName?: string;
  models?: { id: string; name: string; model_name: string }[];
}

const inputCls =
  'w-full bg-brand-dark-alt border border-brand-shade3/50 rounded-card text-sm text-brand-light px-3 py-2 focus:outline-none focus:border-brand-accent placeholder-brand-shade3';

const labelCls = 'block text-xs text-brand-shade3 mb-1 font-mono';
const descCls = 'text-xs text-brand-shade3 mb-3';
const hintCls = 'text-[11px] text-brand-shade3/70 mt-1';

// ---------------------------------------------------------------------------
// B.1: Capability SVG icons
// ---------------------------------------------------------------------------

/**
 * Returns default config for a capability type. Currently both supported
 * capability types (memory, knowledge) initialize with an empty config and
 * pull UI defaults lazily via getKey() fallback values.
 */
export function getCapabilityDefaultConfig(_type: string): Record<string, unknown> {
  return {};
}

export function capabilityIcon(name: string): React.ReactElement {
  const props = { width: 18, height: 18, viewBox: '0 0 24 24', fill: 'none', stroke: 'currentColor', strokeWidth: 1.5, strokeLinecap: 'round' as const, strokeLinejoin: 'round' as const };
  switch (name) {
    case 'brain':
      return <svg {...props}><circle cx="12" cy="12" r="9" /><path d="M9 9c0-1 1-2 3-2s3 1 3 2-1 2-3 2v2" /><circle cx="12" cy="17" r=".5" fill="currentColor" /></svg>;
    case 'book-open':
      return <svg {...props}><path d="M2 3h6a4 4 0 014 4v14a3 3 0 00-3-3H2z" /><path d="M22 3h-6a4 4 0 00-4 4v14a3 3 0 013-3h7z" /></svg>;
    case 'graph':
      // Taxonomy hierarchy — parent entity → 2 child entities. Same icon as
      // Sidebar.knowledgeGraphs + KnowledgeGraphsPage empty state for visual
      // continuity across all KG touchpoints.
      return <svg {...props}><rect x="9" y="2.5" width="6" height="5" rx="1" /><rect x="2.5" y="14" width="6" height="5" rx="1" /><rect x="15.5" y="14" width="6" height="5" rx="1" /><path d="M12 7.5 V11" /><path d="M12 11 H5.5 V14" /><path d="M12 11 H18.5 V14" /></svg>;
    case 'file-json':
      return <svg {...props}><path d="M14 2H6a2 2 0 00-2 2v16a2 2 0 002 2h12a2 2 0 002-2V8z" /><path d="M14 2v6h6" /><path d="M10 13l-1 3 1 3" /><path d="M14 13l1 3-1 3" /></svg>;
    case 'arrow-up-right':
      return <svg {...props}><line x1="7" y1="17" x2="17" y2="7" /><polyline points="7 7 17 7 17 17" /></svg>;
    default:
      return <span className="text-[10px] font-semibold">{name}</span>;
  }
}


function setKey(cap: CapabilityConfig, key: string, value: unknown): CapabilityConfig {
  return { ...cap, config: { ...cap.config, [key]: value } };
}

function getKey<T>(cap: CapabilityConfig, key: string, fallback: T): T {
  const v = cap.config[key];
  return v !== undefined ? (v as T) : fallback;
}

// ---------------------------------------------------------------------------
// Per-type config panels
// ---------------------------------------------------------------------------

type PanelProps = { cap: CapabilityConfig; onChange: (u: CapabilityConfig) => void; agentName?: string; models?: { id: string; name: string; model_name: string }[] };

function MemoryConfig({ cap, onChange }: PanelProps) {
  const unlimitedRetention = getKey(cap, 'unlimited_retention', false) as boolean;
  const unlimitedEntries = getKey(cap, 'unlimited_entries', false) as boolean;

  return (
    <div className="space-y-3">
      <p className={descCls}>Agent remembers facts across sessions within this schema. Recalled automatically at session start, stored during conversation. Users can also ask the agent to remember things explicitly.</p>
      <div className="bg-brand-dark rounded-card px-3 py-2 space-y-1">
        <span className="text-[11px] text-brand-shade2 font-mono">Scope: per-schema, cross-session</span>
        <p className={hintCls}>Memory is isolated per schema and persists between sessions. Support Schema and Sales Schema have separate memory spaces.</p>
      </div>
      <div className="bg-brand-dark rounded-card px-3 py-2 space-y-1">
        <span className="text-[11px] text-brand-shade2 font-mono">Auto-included tools:</span>
        <div className="flex gap-2 mt-1">
          <span className="text-[10px] px-2 py-0.5 bg-brand-dark-alt border border-brand-shade3/20 rounded-card text-brand-shade2">memory_recall</span>
          <span className="text-[10px] px-2 py-0.5 bg-brand-dark-alt border border-brand-shade3/20 rounded-card text-brand-shade2">memory_store</span>
        </div>
        <p className={hintCls}>These tools are automatically added to agent runtime when Memory is enabled</p>
      </div>
      <div>
        <label className={labelCls}>Retention</label>
        <label className="flex items-center gap-2 text-sm text-brand-shade2 cursor-pointer select-none mb-2">
          <input type="checkbox" className="accent-brand-accent" data-testid="memory-unlimited-retention" checked={unlimitedRetention} onChange={(e) => onChange(setKey(cap, 'unlimited_retention', e.target.checked))} />
          Unlimited
        </label>
        {!unlimitedRetention && (
          <div className="flex items-center gap-2">
            <input type="number" className={inputCls} data-testid="memory-retention-days" min={1} max={365} value={getKey(cap, 'retention_days', 30) as number} onChange={(e) => onChange(setKey(cap, 'retention_days', Number(e.target.value)))} />
            <span className="text-xs text-brand-shade3 shrink-0">days</span>
          </div>
        )}
        <p className={hintCls}>{unlimitedRetention ? 'Memory entries are kept indefinitely' : 'Entries older than this are automatically deleted'}</p>
      </div>
      <div>
        <label className={labelCls}>Max entries</label>
        <label className="flex items-center gap-2 text-sm text-brand-shade2 cursor-pointer select-none mb-2">
          <input type="checkbox" className="accent-brand-accent" data-testid="memory-unlimited-entries" checked={unlimitedEntries} onChange={(e) => onChange(setKey(cap, 'unlimited_entries', e.target.checked))} />
          Unlimited
        </label>
        {!unlimitedEntries && (
          <input type="number" className={inputCls} data-testid="memory-max-entries" min={1} value={getKey(cap, 'max_entries', 500) as number} onChange={(e) => onChange(setKey(cap, 'max_entries', Number(e.target.value)))} />
        )}
        <p className={hintCls}>{unlimitedEntries ? 'No limit on stored entries (bounded by schema storage quota)' : 'Oldest entries removed first (FIFO) when limit reached'}</p>
      </div>
    </div>
  );
}

function KnowledgeGraphsConfig({ cap, onChange }: PanelProps) {
  const bundles = getKey<string[]>(cap, 'bundles', []);
  const [available, setAvailable] = useState<string[]>([]);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    let cancelled = false;
    api
      .listKnowledgeGraphs()
      .then((res) => {
        if (!cancelled) {
          setAvailable(res.map((b) => b.bundle_name));
          setLoading(false);
        }
      })
      .catch(() => {
        if (!cancelled) setLoading(false);
      });
    return () => { cancelled = true; };
  }, []);

  const toggle = (name: string) => {
    const next = bundles.includes(name)
      ? bundles.filter((b) => b !== name)
      : [...bundles, name];
    onChange(setKey(cap, 'bundles', next));
  };

  return (
    <div className="space-y-3">
      <p className={descCls}>
        Agents access customer-defined ontologies via auto-generated MCP tools. For each entity type the engine emits <code>list_X</code>, <code>get_X</code>, optionally <code>list_X_ids</code>. Deterministic structured retrieval — no hallucinated IDs, full recall on filtered queries.
      </p>

      <div className="bg-brand-dark rounded-card px-3 py-2 space-y-1">
        <span className="text-[11px] text-brand-shade2 font-mono">Auto-generated tools per bound bundle:</span>
        <div className="flex flex-wrap gap-2 mt-1">
          <span className="text-[10px] px-2 py-0.5 bg-brand-dark-alt border border-brand-shade3/20 rounded-card text-brand-shade2">list_&lt;entity_type&gt;</span>
          <span className="text-[10px] px-2 py-0.5 bg-brand-dark-alt border border-brand-shade3/20 rounded-card text-brand-shade2">get_&lt;entity_type&gt;</span>
          <span className="text-[10px] px-2 py-0.5 bg-brand-dark-alt border border-brand-shade3/20 rounded-card text-brand-shade2">list_&lt;entity_type&gt;_ids</span>
        </div>
        <p className={hintCls}>Tool names derived from each entity_type in the bundle (e.g. <code>list_category</code> if the schema declares <code>entity_type: category</code>).</p>
      </div>

      <div>
        <label className={labelCls}>Bound bundles</label>
        {loading && <p className="text-xs text-brand-shade3">Loading available bundles…</p>}
        {!loading && available.length === 0 && (
          <div className="bg-brand-dark-alt rounded-card border border-brand-shade3/20 px-3 py-2">
            <p className="text-xs text-brand-shade3 mb-2">No Knowledge Graph bundles in this tenant yet.</p>
            <a href={`${import.meta.env.BASE_URL}knowledge-graphs`} className="text-xs text-brand-accent hover:underline">
              Create or import bundles →
            </a>
          </div>
        )}
        {!loading && available.length > 0 && (
          <div className="space-y-1">
            {available.map((name) => (
              <label key={name} className="flex items-center gap-2 text-sm cursor-pointer hover:bg-brand-dark-alt/30 rounded px-2 py-1">
                <input
                  type="checkbox"
                  checked={bundles.includes(name)}
                  onChange={() => toggle(name)}
                  className="accent-brand-accent"
                />
                <span className="font-mono text-xs text-brand-light">{name}</span>
              </label>
            ))}
          </div>
        )}
        <p className={hintCls}>
          Bundles selected here are exposed to this agent as MCP tools. Agents not bound to a bundle do not see its tools.{' '}
          <a href={`${import.meta.env.BASE_URL}knowledge-graphs`} className="text-brand-accent hover:underline">Manage bundles →</a>
        </p>
      </div>
    </div>
  );
}

function KnowledgeConfig({ cap, onChange }: PanelProps) {
  return (
    <div className="space-y-3">
      <p className={descCls}>RAG: agent searches linked knowledge bases before answering</p>

      <div className="bg-brand-dark rounded-card px-3 py-2 space-y-1">
        <span className="text-[11px] text-brand-shade2 font-mono">Auto-included tools:</span>
        <div className="flex gap-2 mt-1">
          <span className="text-[10px] px-2 py-0.5 bg-brand-dark-alt border border-brand-shade3/20 rounded-card text-brand-shade2">knowledge_search</span>
        </div>
        <p className={hintCls}>Automatically available to agent when Knowledge is enabled</p>
      </div>

      <div className="bg-brand-dark-alt border border-brand-shade3/20 rounded-card px-3 py-3 space-y-2">
        <div className="flex items-start gap-2">
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" className="text-brand-accent shrink-0 mt-0.5"><path d="M2 3h6a4 4 0 014 4v14a3 3 0 00-3-3H2z" /><path d="M22 3h-6a4 4 0 00-4 4v14a3 3 0 013-3h7z" /></svg>
          <div>
            <p className="text-xs text-brand-light font-medium">Knowledge Bases</p>
            <p className="text-[10px] text-brand-shade3 mt-1">
              Documents and files are managed through Knowledge Bases. Link one or more KBs to this agent on the Knowledge page.
            </p>
          </div>
        </div>
        <a
          href={import.meta.env.BASE_URL + 'knowledge'}
          className="inline-flex items-center gap-1 px-3 py-1.5 bg-brand-accent text-brand-light rounded-btn text-xs font-medium hover:bg-brand-accent-hover transition-colors"
        >
          <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><path d="M18 13v6a2 2 0 01-2 2H5a2 2 0 01-2-2V8a2 2 0 012-2h6" /><polyline points="15 3 21 3 21 9" /><line x1="10" y1="14" x2="21" y2="3" /></svg>
          Manage Knowledge Bases
        </a>
      </div>

      <div>
        <label className={labelCls}>Top-K</label>
        <input type="number" className={inputCls} data-testid="knowledge-top-k" min={1} max={20} value={getKey(cap, 'top_k', 5) as number} onChange={(e) => onChange(setKey(cap, 'top_k', Number(e.target.value)))} />
        <p className={hintCls}>Number of most relevant document chunks retrieved per query</p>
      </div>
      <div>
        <label className={labelCls}>Similarity threshold</label>
        <input type="number" className={inputCls} data-testid="knowledge-threshold" min={0} max={1} step={0.05} value={getKey(cap, 'similarity_threshold', 0.75) as number} onChange={(e) => onChange(setKey(cap, 'similarity_threshold', Number(e.target.value)))} />
        <p className={hintCls}>0 = return all chunks, 1 = exact match only. Recommended: 0.7-0.85</p>
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Summary
// ---------------------------------------------------------------------------

function getSummary(cap: CapabilityConfig): string {
  const c = cap.config ?? {} as Record<string, unknown>;
  switch (cap.type) {
    case 'memory': {
      const parts: string[] = ['Per-schema'];
      if (c.unlimited_retention) parts.push('unlimited retention');
      else if (c.retention_days) parts.push(`${c.retention_days}d retention`);
      else parts.push('unlimited retention');
      if (c.unlimited_entries) parts.push('unlimited entries');
      else parts.push(`max ${c.max_entries ?? 500}`);
      return parts.join(', ');
    }
    case 'knowledge': {
      const sources = c.sources as string[] | undefined;
      const parts: string[] = [];
      const first = sources?.[0];
      if (first) parts.push(first);
      const topK = c.top_k as number | undefined;
      if (topK) parts.push(`top-k: ${topK}`);
      return parts.length > 0 ? parts.join(', ') : 'No sources configured';
    }
    case 'knowledge_graphs': {
      const bundles = (c.bundles as string[] | undefined) ?? [];
      if (bundles.length === 0) return 'No bundles bound';
      if (bundles.length === 1) return bundles[0] ?? '';
      return `${bundles.length} bundles: ${bundles.slice(0, 2).join(', ')}${bundles.length > 2 ? '…' : ''}`;
    }
    default: return '';
  }
}

// ---------------------------------------------------------------------------
// Main block
// ---------------------------------------------------------------------------

const configMap: Record<string, React.FC<PanelProps>> = {
  memory: MemoryConfig,
  knowledge: KnowledgeConfig,
  knowledge_graphs: KnowledgeGraphsConfig,
};

export default function CapabilityBlock({ capability, onChange, onRemove, agentName, models }: CapabilityBlockProps) {
  const [open, setOpen] = useState(false);
  const meta = CAPABILITY_META[capability.type] ?? { label: capability.type, icon: 'brain', description: 'Unknown capability type' };
  const summary = getSummary(capability);
  const ConfigPanel = configMap[capability.type];

  return (
    <div className="bg-brand-dark-alt border border-brand-shade3/20 rounded-card font-mono">
      {/* Header — click anywhere to expand/collapse */}
      <div
        className="flex items-center justify-between px-3 py-2.5 cursor-pointer hover:bg-brand-dark-surface/50 transition-colors"
        onClick={() => setOpen((v) => !v)}
      >
        <div className="flex items-center gap-2 text-sm min-w-0">
          <span className="text-brand-shade3 shrink-0">{capabilityIcon(meta.icon)}</span>
          <span className={`shrink-0 ${capability.enabled ? 'text-brand-light' : 'text-brand-shade3'}`}>{meta.label}</span>
          {!open && summary && <span className="text-[11px] text-brand-shade3 truncate ml-1">{summary}</span>}
        </div>
        <div className="flex items-center gap-1.5">
          {/* Chevron indicator */}
          <svg
            width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"
            className={`text-brand-shade3 transition-transform ${open ? 'rotate-180' : ''}`}
          >
            <path d="M6 9l6 6 6-6" />
          </svg>
          {/* Toggle — stopPropagation to prevent expand/collapse */}
          <button
            type="button"
            onClick={(e) => { e.stopPropagation(); onChange({ ...capability, enabled: !capability.enabled }); }}
            className={`relative inline-flex h-4 w-7 items-center rounded-full transition-colors ${capability.enabled ? 'bg-brand-accent' : 'bg-brand-shade3/40'}`}
            title={capability.enabled ? 'Disable' : 'Enable'}
          >
            <span className={`inline-block h-3 w-3 rounded-full bg-white transition-transform ${capability.enabled ? 'translate-x-3.5' : 'translate-x-0.5'}`} />
          </button>
          {/* Remove — stopPropagation */}
          <button
            type="button"
            onClick={(e) => { e.stopPropagation(); onRemove(); }}
            className="p-1 text-brand-shade3 hover:text-brand-light transition-colors"
            title="Remove"
            aria-label="Remove capability"
          >
            <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><path d="M18 6L6 18M6 6l12 12" /></svg>
          </button>
        </div>
      </div>
      {open && ConfigPanel && (
        <div className="px-3 py-3 border-t border-brand-shade3/10">
          <ConfigPanel cap={capability} onChange={onChange} agentName={agentName} models={models} />
        </div>
      )}
    </div>
  );
}
