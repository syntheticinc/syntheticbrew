import { useCallback, useEffect, useMemo, useState } from 'react';
import { Link, useNavigate, useParams, useSearchParams } from 'react-router-dom';
import DelegationTree, { computeEntryAgent } from '../components/DelegationTree';
import { api } from '../api/client';
import { useApi } from '../hooks/useApi';
import type { AgentDetail, Schema } from '../types';
import type { TreeAgent, TreeRelation } from '../mocks/schemas';

type TabKey = 'canvas' | 'settings';

// ─── Type adapters ───────────────────────────────────────────────────────────
// DelegationTree expects TreeAgent/TreeRelation (prototype mock shapes).
// We adapt real API types to these shapes here at the boundary.

function agentDetailToTreeAgent(a: AgentDetail, modelNameById?: Map<string, string>): TreeAgent {
  const initials = a.name
    .split(/[-_\s]/)
    .map((p) => p[0] ?? '')
    .join('')
    .slice(0, 2)
    .toUpperCase() || a.name.slice(0, 2).toUpperCase();
  const modelId = a.model_id ?? '';
  const modelDisplay = modelNameById?.get(modelId) ?? modelId.slice(0, 8);
  return {
    id: a.name,
    name: a.name,
    model: modelDisplay,
    description: a.description,
    avatarInitials: initials,
    lifecycle: a.lifecycle ?? 'persistent',
    toolsCount: a.tools_count ?? 0,
    knowledgeCount: 0,
    flowsCount: 0,
    activeSessions: 0,
    state: 'idle',
  };
}

function apiRelationToTreeRelation(r: { id: string; schema_id: string; source: string; target: string }): TreeRelation {
  return {
    id: r.id,
    sourceAgentId: r.source,
    targetAgentId: r.target,
  };
}

// ─── AddAgentPanel ────────────────────────────────────────────────────────────

function AddAgentPanel({
  schemaAgentNames,
  parentAgentName,
  onAdd,
  onClose,
}: {
  schemaAgentNames: string[];
  parentAgentName?: string;
  onAdd: (agentName: string) => void;
  onClose: () => void;
}) {
  const { data: allAgents, loading } = useApi(() => api.listAgents());
  const available = (allAgents ?? []).filter((a) => !schemaAgentNames.includes(a.name));

  return (
    <div className="absolute top-4 right-4 z-20 w-[320px] bg-brand-dark-surface border border-brand-shade3/30 rounded-card shadow-2xl">
      <div className="px-4 py-3 border-b border-brand-shade3/15 flex items-center justify-between">
        <div>
          <div className="text-[12px] font-semibold text-brand-light">
            {parentAgentName ? 'Add delegate' : 'Add agent'}
          </div>
          {parentAgentName && (
            <div className="text-[10px] text-brand-shade3 mt-0.5">
              Under <span className="text-brand-accent">{parentAgentName}</span>
            </div>
          )}
        </div>
        <button onClick={onClose} className="text-brand-shade3 hover:text-brand-light text-[14px]">✕</button>
      </div>
      <div className="max-h-[340px] overflow-y-auto">
        {loading && (
          <div className="px-4 py-4 text-center text-[11px] text-brand-shade3">Loading agents…</div>
        )}
        {!loading && available.length === 0 && (
          <div className="px-4 py-4 text-center text-[11px] text-brand-shade3">
            All agents already added. Create a new one from Agents page.
          </div>
        )}
        {available.map((a) => {
          const initials = a.name
            .split(/[-_\s]/)
            .map((p) => p[0] ?? '')
            .join('')
            .slice(0, 2)
            .toUpperCase() || a.name.slice(0, 2).toUpperCase();
          return (
            <button
              key={a.name}
              onClick={() => onAdd(a.name)}
              className="w-full text-left flex items-center gap-3 px-4 py-2.5 hover:bg-brand-shade3/5 border-b border-brand-shade3/5 last:border-b-0 transition-colors"
            >
              <div className="shrink-0 w-8 h-8 rounded-full bg-gradient-to-br from-brand-shade3/30 to-brand-shade3/10 flex items-center justify-center text-[11px] font-semibold text-brand-light border border-brand-shade3/20">
                {initials}
              </div>
              <div className="min-w-0 flex-1">
                <div className="text-[12px] text-brand-light truncate">{a.name}</div>
                <div className="text-[10px] text-brand-shade3 truncate">{a.description ?? ''}</div>
              </div>
            </button>
          );
        })}
      </div>
      <div className="px-4 py-2 border-t border-brand-shade3/15 bg-brand-dark/50">
        <Link to="/agents" className="text-[11px] text-brand-accent hover:underline">
          + Create new agent
        </Link>
      </div>
    </div>
  );
}

// ─── Chat endpoint info panel ────────────────────────────────────────────────

function ChatEndpointPanel({ schemaName }: { schemaName: string }) {
  const [copied, setCopied] = useState(false);
  const url = `POST /api/v1/schemas/${schemaName}/chat`;

  function copy() {
    navigator.clipboard
      .writeText(url)
      .then(() => {
        setCopied(true);
        setTimeout(() => setCopied(false), 1500);
      })
      .catch(() => {});
  }

  return (
    <div className="bg-brand-dark border border-brand-shade3/20 rounded-card p-3 space-y-2">
      <div className="text-[10px] uppercase tracking-wider text-brand-shade3">Chat endpoint</div>
      <div className="flex items-center gap-2">
        <code className="flex-1 bg-brand-dark-alt border border-brand-shade3/20 rounded-btn px-2 py-1.5 text-[11px] font-mono text-brand-light break-all">
          {url}
        </code>
        <button
          onClick={copy}
          className="px-3 py-1.5 text-[11px] bg-brand-accent text-white rounded-btn hover:bg-brand-accent/90 transition-colors shrink-0"
        >
          {copied ? 'Copied' : 'Copy'}
        </button>
      </div>
      <p className="text-[10px] text-brand-shade3 leading-relaxed">
        Send <code className="text-brand-shade2">{`{ "message": "...", "session_id": "..." }`}</code> to this
        endpoint. The chat is dispatched to the schema's entry orchestrator.
      </p>
    </div>
  );
}

// ─── Main page ────────────────────────────────────────────────────────────────

export default function SchemaDetailPage() {
  const { schemaName = '' } = useParams<{ schemaName: string }>();
  const navigate = useNavigate();
  const [searchParams] = useSearchParams();
  const validTabs: TabKey[] = ['canvas', 'settings'];
  const rawTab = searchParams.get('tab') as TabKey | null;
  const [tab, setTab] = useState<TabKey>(
    rawTab && validTabs.includes(rawTab) ? rawTab : 'canvas',
  );
  const [showAddAgent, setShowAddAgent] = useState(false);
  const [addChildParentName, setAddChildParentName] = useState<string | null>(null);

  // ─── Data fetching ───────────────────────────────────────────────────────────
  const { data: schema, loading: schemaLoading, refetch: refetchSchema } = useApi(
    () => api.getSchema(schemaName),
    [schemaName],
  );

  const { data: agentNames, loading: agentNamesLoading, refetch: refetchAgentNames } = useApi(
    () => api.listSchemaAgents(schemaName),
    [schemaName],
  );

  const { data: rawRelations, loading: relationsLoading, refetch: refetchRelations } = useApi(
    () => api.listAgentRelations(schemaName),
    [schemaName],
  );

  // Local optimistic state for chat_enabled toggle
  const [chatEnabledLocal, setChatEnabledLocal] = useState<boolean | null>(null);
  const [chatEnabledSaving, setChatEnabledSaving] = useState(false);
  const [chatEnabledError, setChatEnabledError] = useState<string | null>(null);

  // Restore to factory defaults — only applicable on system (builder) schema.
  // Mirrors the same affordance on AgentDrillInPage for builder-assistant; the
  // button was previously on the schemas list which was asymmetric — editing
  // an agent is per-agent on its detail page, editing the builder schema is
  // per-schema on its canvas, so the restore control belongs here too.
  const [restoring, setRestoring] = useState(false);
  const [restoreError, setRestoreError] = useState<string | null>(null);
  const handleRestoreDefaults = useCallback(async () => {
    const confirmed = window.confirm(
      'Restore the builder schema to factory defaults? The system agent, schema, and chat flag are reset — user schemas are untouched.',
    );
    if (!confirmed) return;
    setRestoring(true);
    setRestoreError(null);
    try {
      await api.restoreBuilderAssistant();
      refetchSchema();
      refetchAgentNames();
      refetchRelations();
    } catch (err) {
      setRestoreError(err instanceof Error ? err.message : String(err));
    } finally {
      setRestoring(false);
    }
  }, [refetchSchema, refetchAgentNames, refetchRelations]);
  useEffect(() => {
    if (schema) setChatEnabledLocal(schema.chat_enabled ?? false);
  }, [schema]);

  // Load full agent details by name
  const [agents, setAgents] = useState<AgentDetail[]>([]);
  const [agentsLoading, setAgentsLoading] = useState(false);

  useEffect(() => {
    if (!agentNames || agentNames.length === 0) {
      setAgents([]);
      return;
    }
    let cancelled = false;
    setAgentsLoading(true);
    Promise.all(agentNames.map((name) => api.getAgent(name)))
      .then((result) => {
        if (!cancelled) setAgents(result);
      })
      .catch(() => {
        if (!cancelled) setAgents([]);
      })
      .finally(() => {
        if (!cancelled) setAgentsLoading(false);
      });
    return () => { cancelled = true; };
  }, [agentNames]);

  const { data: models } = useApi(() => api.listModels());
  const modelNameById = useMemo<Map<string, string>>(() => {
    const m = new Map<string, string>();
    for (const model of models ?? []) m.set(model.id, model.name);
    return m;
  }, [models]);

  // Adapt API shapes to Tree* types expected by DelegationTree
  const treeAgents = useMemo<TreeAgent[]>(
    () => agents.map((a) => agentDetailToTreeAgent(a, modelNameById)),
    [agents, modelNameById],
  );

  const treeRelations = useMemo<TreeRelation[]>(
    () => (rawRelations ?? []).map(apiRelationToTreeRelation),
    [rawRelations],
  );

  // Entry agent name: prefer explicit entry_agent_name from schema, then
  // detect from the relation graph via the shared `computeEntryAgent` helper
  // (agent with outgoing relations but no incoming ones). Returns an empty
  // string if neither source can pick one — the canvas surfaces a clear
  // placeholder in that case rather than silently picking the first agent
  // in the array, which would hide missing-entry-agent bugs.
  //
  // Uses agentNames (available before full agent details load) to build the
  // graph input so the heuristic is available even before the full agent
  // details resolve.
  const entryAgentId = useMemo(() => {
    if (schema?.entry_agent_name) return schema.entry_agent_name;
    const names = agentNames ?? [];
    if (names.length === 0) return '';
    // Feed agent names as both id and name — SchemaDetailPage projects
    // agents by name at this point in the pipeline (the full-detail fetch
    // carrying UUIDs happens later).
    const graphAgents = names.map((n) => ({ id: n, name: n }));
    const graphRelations = treeRelations.map((r) => ({
      sourceAgentId: r.sourceAgentId,
      targetAgentId: r.targetAgentId,
    }));
    return computeEntryAgent(graphAgents, graphRelations) ?? '';
  }, [schema, treeRelations, agentNames]);

  const isLoading = schemaLoading || agentNamesLoading || relationsLoading || agentsLoading;

  // ─── Handlers ────────────────────────────────────────────────────────────────

  const onAgentOpen = useCallback(
    (agentId: string) => navigate(`/agents/${agentId}`),
    [navigate],
  );

  const onAddChildRequest = useCallback((parentAgentId: string) => {
    setAddChildParentName(parentAgentId);
    setShowAddAgent(true);
  }, []);

  const handleAddAgent = useCallback(
    async (agentName: string) => {
      const parent = addChildParentName ?? entryAgentId;
      if (!parent) {
        // Empty schema: no entry agent yet — set this agent as the entry orchestrator.
        try {
          await api.updateSchema(schemaName, { entry_agent_id: agentName });
          refetchSchema();
          refetchAgentNames();
        } catch {
          // silently ignore
        }
        setShowAddAgent(false);
        setAddChildParentName(null);
        return;
      }
      try {
        await api.createAgentRelation(schemaName, parent, agentName);
        refetchRelations();
        refetchAgentNames();
      } catch {
        // silently ignore — user sees stale state
      }
      setShowAddAgent(false);
      setAddChildParentName(null);
    },
    [schemaName, addChildParentName, entryAgentId, refetchRelations, refetchAgentNames, refetchSchema],
  );

  const handleRemoveDelegation = useCallback(
    async (agentId: string) => {
      if (agentId === entryAgentId) return;
      // Find all relations involving this agent as target and delete them
      const toDelete = (rawRelations ?? []).filter((r) => r.target === agentId);
      try {
        await Promise.all(toDelete.map((r) => api.deleteAgentRelation(schemaName, r.id)));
        refetchRelations();
        refetchAgentNames();
      } catch {
        // silently ignore
      }
    },
    [schemaName, entryAgentId, rawRelations, refetchRelations, refetchAgentNames],
  );

  const handleToggleChatEnabled = useCallback(
    async (next: boolean) => {
      if (!schema || chatEnabledSaving) return;
      setChatEnabledLocal(next); // optimistic
      setChatEnabledSaving(true);
      setChatEnabledError(null);
      try {
        await api.updateSchema(schemaName, { chat_enabled: next });
        refetchSchema();
      } catch (err) {
        // Revert on error
        setChatEnabledLocal(!next);
        setChatEnabledError(err instanceof Error ? err.message : String(err));
      } finally {
        setChatEnabledSaving(false);
      }
    },
    [schema, schemaName, chatEnabledSaving, refetchSchema],
  );

  // ─── Render guards ───────────────────────────────────────────────────────────

  if (!schemaLoading && schema === null) {
    return (
      <div className="max-w-[800px] mx-auto text-center py-12">
        <p className="text-brand-shade3">Schema not found.</p>
        <Link to="/schemas" className="text-brand-accent text-sm mt-4 inline-block">
          ← Back to schemas
        </Link>
      </div>
    );
  }

  const canvasEmpty = treeAgents.length === 0 && !isLoading;
  // Distinguish "no agents at all" (canvasEmpty above) from "agents exist
  // but nobody is the entry orchestrator" — e.g. a schema with one lone
  // agent and no relations, or a cycle. Surface an explicit placeholder so
  // the operator knows to add the first agent as entry rather than seeing
  // a random pick.
  const entryAgentMissing = !isLoading && treeAgents.length > 0 && !entryAgentId;
  const schemaAgentNames = agentNames ?? [];

  const chatEnabled = chatEnabledLocal ?? schema?.chat_enabled ?? false;

  return (
    <div className="h-full flex flex-col">
      {/* Breadcrumb + title */}
      <div className="px-6 pt-4 pb-3 border-b border-brand-shade3/10">
        <Link to="/schemas" className="text-[11px] text-brand-shade3 hover:text-brand-accent transition-colors">
          ← Schemas
        </Link>
        <div className="flex items-center gap-3 mt-2">
          <h1 className="text-xl font-semibold text-brand-light">
            {schema?.name ?? schemaName}
          </h1>
          {schema?.is_system && (
            <span className="text-[10px] px-1.5 py-0.5 rounded uppercase tracking-wider bg-brand-shade3/10 text-brand-shade3 border border-brand-shade3/25 font-mono">
              System
            </span>
          )}
          {chatEnabled && (
            <span className="text-[10px] px-1.5 py-0.5 rounded uppercase tracking-wider bg-emerald-500/10 text-emerald-400 border border-emerald-500/30">
              chat enabled
            </span>
          )}
          <div className="flex-1" />
          {schema?.is_system && (
            <button
              type="button"
              onClick={handleRestoreDefaults}
              disabled={restoring}
              className="px-4 py-1.5 border border-amber-500/40 text-amber-400 rounded-btn text-sm font-medium font-mono hover:bg-amber-500/10 disabled:opacity-50 transition-colors"
              title="Restore builder-assistant, builder-schema, and chat flag to factory defaults"
            >
              {restoring ? 'Restoring…' : 'Restore defaults'}
            </button>
          )}
        </div>
        {restoreError && (
          <div className="mt-2 text-[11px] text-rose-400">Restore failed: {restoreError}</div>
        )}

        {/* Tabs */}
        <div className="flex items-center gap-1 mt-3">
          {([
            ['canvas', 'Canvas'],
            ['settings', 'Settings'],
          ] as const).map(([key, label]) => (
            <button
              key={key}
              onClick={() => setTab(key as TabKey)}
              className={`px-3 py-1.5 text-[12px] rounded-btn transition-colors ${
                tab === key
                  ? 'bg-brand-dark-alt text-brand-light border border-brand-shade3/25'
                  : 'text-brand-shade3 hover:text-brand-light border border-transparent'
              }`}
            >
              {label}
            </button>
          ))}
        </div>
      </div>

      {/* Body */}
      <div className="flex-1 min-h-0 relative">
        {tab === 'canvas' && (
          <div className="absolute inset-0">
            {isLoading ? (
              <div className="h-full flex items-center justify-center">
                <span className="text-[13px] text-brand-shade3">Loading…</span>
              </div>
            ) : canvasEmpty ? (
              <div className="h-full flex items-center justify-center p-6">
                <div className="bg-brand-dark-surface border border-dashed border-brand-shade3/25 rounded-card p-8 max-w-md text-center">
                  <h3 className="text-[14px] font-semibold text-brand-light mb-2">Empty schema</h3>
                  <p className="text-[12px] text-brand-shade3 mb-4">
                    Add your entry orchestrator, then connect it to delegates. You can also import agents from
                    the global library.
                  </p>
                  <button
                    onClick={() => setShowAddAgent(true)}
                    className="px-4 py-2 text-[12px] text-white bg-brand-accent rounded-btn"
                  >
                    + Add first agent
                  </button>
                </div>
              </div>
            ) : entryAgentMissing ? (
              <div className="h-full flex items-center justify-center p-6">
                <div className="bg-brand-dark-surface border border-dashed border-brand-shade3/25 rounded-card p-8 max-w-md text-center">
                  <h3 className="text-[14px] font-semibold text-brand-light mb-2">No entry agent set</h3>
                  <p className="text-[12px] text-brand-shade3 mb-4">
                    The first agent you add will become the orchestrator. Pick which agent should receive
                    incoming chat requests and delegate to the rest.
                  </p>
                  <div className="flex flex-col gap-2">
                    {schemaAgentNames.map((name) => (
                      <button
                        key={name}
                        onClick={async () => {
                          try {
                            await api.updateSchema(schemaName, { entry_agent_id: name });
                            refetchSchema();
                          } catch {
                            // silently ignore — user sees stale state
                          }
                        }}
                        className="px-3 py-2 text-[12px] bg-brand-dark-alt border border-brand-shade3/25 rounded-btn text-brand-light hover:border-brand-accent/50 transition-colors"
                      >
                        Set <span className="text-brand-accent font-semibold">{name}</span> as entry
                      </button>
                    ))}
                  </div>
                </div>
              </div>
            ) : (
              <>
                <DelegationTree
                  agents={treeAgents}
                  relations={treeRelations}
                  entryAgentId={entryAgentId}
                  onAgentOpen={onAgentOpen}
                  onAddChild={onAddChildRequest}
                  onRemoveDelegation={handleRemoveDelegation}
                />

                {/* Canvas toolbar overlay */}
                <div className="absolute top-4 right-4 flex items-center gap-2 z-10">
                  <button
                    onClick={() => {
                      setAddChildParentName(entryAgentId);
                      setShowAddAgent(true);
                    }}
                    className="px-3 py-1.5 text-[11px] font-medium bg-brand-dark-surface/95 backdrop-blur border border-brand-shade3/25 rounded-btn text-brand-light hover:border-brand-shade3/50 transition-colors"
                    title="Add delegate under entry orchestrator"
                  >
                    + Delegate
                  </button>
                  <div
                    className="text-[10px] text-brand-shade3/70 max-w-[180px] leading-tight"
                    title="Hover any agent card to see its + button"
                  >
                    Hover a card to add a delegate under that agent
                  </div>
                </div>
              </>
            )}

            {showAddAgent && (
              <AddAgentPanel
                schemaAgentNames={schemaAgentNames}
                parentAgentName={addChildParentName ?? undefined}
                onAdd={handleAddAgent}
                onClose={() => {
                  setShowAddAgent(false);
                  setAddChildParentName(null);
                }}
              />
            )}
          </div>
        )}

        {tab === 'settings' && (
          <div className="p-6 max-w-[720px] mx-auto space-y-5">
            {/* Engine 1.1.0+: schema name is the operator-facing canonical
                handle (URL param, GitOps key) — immutable post-create.
                A PATCH that changes name returns 409 Conflict. */}
            <div>
              <label className="block text-[11px] uppercase tracking-wider text-brand-shade3 mb-1.5">Name</label>
              <input
                readOnly
                value={schema?.name ?? ''}
                className="w-full bg-brand-dark border border-brand-shade3/20 rounded-btn px-3 py-2 text-[13px] text-brand-light"
              />
              <p className="text-xs text-brand-shade3 mt-1">
                Schema name is immutable post-create — recreate with a new name and migrate
                consumers if a rename is needed (engine 1.1.0+).
              </p>
            </div>
            <div>
              <label className="block text-[11px] uppercase tracking-wider text-brand-shade3 mb-1.5">
                Description
              </label>
              <textarea
                readOnly
                value={schema?.description ?? ''}
                rows={2}
                className="w-full bg-brand-dark border border-brand-shade3/20 rounded-btn px-3 py-2 text-[13px] text-brand-light"
              />
            </div>
            {entryAgentId && (
              <div>
                <label className="block text-[11px] uppercase tracking-wider text-brand-shade3 mb-1.5">
                  Entry Orchestrator
                </label>
                <div className="bg-brand-dark border border-brand-shade3/20 rounded-btn px-3 py-2 text-[13px] text-brand-light">
                  {entryAgentId}{' '}
                  <span className="text-brand-shade3">— chat requests dispatch here</span>
                </div>
              </div>
            )}

            {/* Chat enabled toggle */}
            <div className="border-t border-brand-shade3/10 pt-5">
              <div className="flex items-start gap-4">
                <button
                  role="switch"
                  aria-checked={chatEnabled}
                  onClick={() => handleToggleChatEnabled(!chatEnabled)}
                  disabled={chatEnabledSaving}
                  className={`relative inline-flex h-6 w-11 shrink-0 mt-0.5 items-center rounded-full border transition-colors disabled:opacity-50 ${
                    chatEnabled
                      ? 'bg-emerald-500/70 border-emerald-400/50'
                      : 'bg-brand-dark-alt border-brand-shade3/30'
                  }`}
                >
                  <span
                    className={`inline-block h-4 w-4 rounded-full bg-white transition-transform ${
                      chatEnabled ? 'translate-x-6' : 'translate-x-1'
                    }`}
                  />
                </button>
                <div className="flex-1">
                  <div className="text-[13px] font-medium text-brand-light">
                    Accept chat requests
                  </div>
                  <p className="text-[11px] text-brand-shade3 leading-relaxed mt-1">
                    When enabled, this schema accepts <code className="text-brand-shade2">POST /api/v1/schemas/{schemaName}/chat</code> requests.
                  </p>
                  {chatEnabledError && (
                    <div className="mt-2 text-[11px] text-rose-400">Failed to save: {chatEnabledError}</div>
                  )}
                </div>
              </div>

              {chatEnabled && (
                <div className="mt-4">
                  <ChatEndpointPanel schemaName={schemaName} />
                </div>
              )}
            </div>

            {/* Chat last fired at — read-only telemetry */}
            {schema?.chat_last_fired_at && (
              <div>
                <label className="block text-[11px] uppercase tracking-wider text-brand-shade3 mb-1.5">
                  Last chat request
                </label>
                <div className="bg-brand-dark border border-brand-shade3/20 rounded-btn px-3 py-2 text-[13px] text-brand-light">
                  {new Date(schema.chat_last_fired_at).toLocaleString()}
                </div>
              </div>
            )}
          </div>
        )}
      </div>
    </div>
  );
}

// Unused type re-exports removed — the triggers tab is gone and DelegationTree
// no longer accepts trigger props.
export type { Schema };
