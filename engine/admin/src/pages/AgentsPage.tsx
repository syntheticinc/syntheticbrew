import { useEffect, useMemo, useRef, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { useApi } from '../hooks/useApi';
import { useAdminRefresh } from '../hooks/useAdminRefresh';
import { api } from '../api/client';
import PageContainer from '../components/PageContainer';
import Button from '../components/Button';
import OnboardCodingAgentButton from '../components/OnboardCodingAgentButton';
import type { AgentInfo } from '../types';

function AgentRow({ agent, onClick }: { agent: AgentInfo; onClick: () => void }) {
  const schemas = (agent as any).used_in_schemas ?? (agent as any).schemas ?? [];
  const lifecycle = (agent as any).lifecycle as string | undefined;
  return (
    <tr
      onClick={onClick}
      className="border-b border-brand-shade3/5 hover:bg-brand-dark-alt/50 cursor-pointer transition-colors"
    >
      <td className="px-4 py-3">
        <span className="text-brand-light font-medium font-mono">{agent.name}</span>
        {agent.is_system && (
          <span className="ml-2 text-[10px] text-brand-accent/70 bg-brand-accent/10 px-1.5 py-0.5 rounded border border-brand-accent/20">
            System
          </span>
        )}
      </td>
      <td className="px-4 py-3 text-brand-shade2 font-mono text-xs">{(agent as any).model ?? <span className="text-brand-shade3/50">—</span>}</td>
      <td className="px-4 py-3">
        {lifecycle ? (
          <span className={`text-xs px-2 py-0.5 rounded-full font-mono ${
            lifecycle === 'persistent'
              ? 'bg-green-500/10 text-green-400 border border-green-500/20'
              : 'bg-brand-dark text-brand-shade3 border border-brand-shade3/20'
          }`}>
            {lifecycle}
          </span>
        ) : <span className="text-xs text-brand-shade3/50">—</span>}
      </td>
      <td className="px-4 py-3 text-brand-shade2 text-xs">{agent.tools_count}</td>
      <td className="px-4 py-3">
        <span className="text-xs text-brand-shade3/50">—</span>
      </td>
      <td className="px-4 py-3">
        <div className="flex gap-1 flex-wrap">
          {schemas.map((s: string) => (
            <span key={s} className="text-[10px] text-blue-400 bg-blue-500/10 px-1.5 py-0.5 rounded border border-blue-500/20">
              {s}
            </span>
          ))}
          {schemas.length === 0 && <span className="text-xs text-brand-shade3/50">—</span>}
        </div>
      </td>
    </tr>
  );
}

const TABLE_HEADERS = ['Name', 'Model', 'Lifecycle', 'Tools', 'Capabilities', 'Used in Schemas'];

// Mirrors engine `nameRegex` + reserved list in name_validation.go. Keeping
// these in sync is enforceable via the API (POST 400 confirms) — the client
// copy is a UX-only fast path so users see the error inline before submit.
const NAME_REGEX = /^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/;
const UUID_REGEX = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
const RESERVED_NAMES = new Set([
  'chat', 'agents', 'agent-relations', 'memory', 'files', 'health', 'auth',
  'tasks', 'models', 'knowledge-bases', 'schemas', 'mcp-servers', 'tokens',
  'sessions', 'metrics',
]);
const MAX_NAME_LENGTH = 100;

function slugify(display: string): string {
  return display
    .toLowerCase()
    .replace(/[^a-z0-9-]+/g, '-')
    .replace(/-+/g, '-')
    .replace(/^-|-$/g, '')
    .slice(0, MAX_NAME_LENGTH);
}

function validateName(name: string, existing: Set<string>): string | null {
  if (!name) return 'Name is required.';
  if (name.length > MAX_NAME_LENGTH) return `Name must be at most ${MAX_NAME_LENGTH} characters.`;
  if (!NAME_REGEX.test(name)) return 'Use lowercase letters, digits, and hyphens. Must start and end with a letter or digit.';
  if (UUID_REGEX.test(name)) return 'Name cannot be UUID-shaped.';
  if (RESERVED_NAMES.has(name)) return `"${name}" is reserved (collides with API route segment).`;
  if (existing.has(name)) return `An agent named "${name}" already exists. Choose a different name.`;
  return null;
}

function NewAgentModal({
  existingNames,
  onClose,
  onCreated,
}: {
  existingNames: Set<string>;
  onClose: () => void;
  onCreated: (name: string) => void;
}) {
  const [displayName, setDisplayName] = useState('');
  const [name, setName] = useState('');
  const [nameTouched, setNameTouched] = useState(false);
  const [systemPrompt, setSystemPrompt] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [formError, setFormError] = useState<string | null>(null);
  const firstFieldRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    firstFieldRef.current?.focus();
  }, []);

  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (e.key === 'Escape' && !submitting) onClose();
    }
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [onClose, submitting]);

  function handleDisplayChange(v: string) {
    setDisplayName(v);
    // Auto-derive slug until the user explicitly types into the name field.
    if (!nameTouched) setName(slugify(v));
  }

  const nameError = validateName(name, existingNames);
  const promptError = !systemPrompt.trim() ? 'System prompt is required.' : null;
  const canSubmit = !submitting && !nameError && !promptError;
  // Surface validation as soon as the user expresses intent (typed anything
  // into either field), not only on explicit blur. Otherwise the auto-slug
  // path leaves the user staring at a disabled Submit button with no clue.
  const showNameError = !!nameError && (nameTouched || displayName.length > 0);

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    if (!canSubmit) return;
    setSubmitting(true);
    setFormError(null);
    try {
      await api.createAgent({ name, system_prompt: systemPrompt });
      onCreated(name);
    } catch (err) {
      const message = err instanceof Error ? err.message : 'Failed to create agent.';
      // Engine returns 409 ALREADY_EXISTS when the (tenant_id, name) row
      // already exists — map back to the field-level error so the user can
      // see why their submit was rejected without scanning a raw API string.
      const lower = message.toLowerCase();
      if (lower.includes('already exists') || lower.includes('already_exists') || lower.includes('conflict')) {
        existingNames.add(name);
        setFormError(`An agent named "${name}" already exists. Choose a different name.`);
      } else {
        setFormError(message);
      }
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 backdrop-blur-sm" onClick={onClose}>
      <div
        className="w-full max-w-lg bg-brand-dark-surface border border-brand-shade3/20 rounded-card shadow-2xl"
        onClick={(e) => e.stopPropagation()}
      >
        <form onSubmit={handleSubmit}>
          <div className="px-5 py-4 border-b border-brand-shade3/10">
            <h2 className="text-base font-semibold text-brand-light">New agent</h2>
            <p className="text-xs text-brand-shade3 mt-0.5">
              Create a standalone agent. Wire it into a schema afterwards.
            </p>
          </div>

          <div className="px-5 py-4 space-y-4">
            <div>
              <label className="block text-xs font-medium text-brand-shade2 mb-1">Display name</label>
              <input
                ref={firstFieldRef}
                type="text"
                value={displayName}
                onChange={(e) => handleDisplayChange(e.target.value)}
                placeholder="Support Triage Agent"
                disabled={submitting}
                className="w-full bg-brand-dark-alt border border-brand-shade3/30 rounded-btn text-sm text-brand-light px-3 py-2 focus:outline-none focus:border-brand-accent"
              />
            </div>

            <div>
              <label className="block text-xs font-medium text-brand-shade2 mb-1">
                Name <span className="text-brand-shade3 font-normal">(used in URLs &amp; references)</span>
              </label>
              <input
                type="text"
                value={name}
                onChange={(e) => { setName(e.target.value); setNameTouched(true); }}
                onBlur={() => setNameTouched(true)}
                placeholder="support-triage"
                disabled={submitting}
                className={`w-full bg-brand-dark-alt border rounded-btn text-sm text-brand-light px-3 py-2 font-mono focus:outline-none transition-colors ${
                  showNameError
                    ? 'border-red-500/60 focus:border-red-500'
                    : 'border-brand-shade3/30 focus:border-brand-accent'
                }`}
              />
              {showNameError && (
                <p className="mt-1 text-[11px] text-red-400">{nameError}</p>
              )}
              {!nameError && name && (
                <p className="mt-1 text-[11px] text-brand-shade3">
                  POST /api/v1/agents/<span className="font-mono text-brand-shade2">{name}</span>
                </p>
              )}
            </div>

            <div>
              <label className="block text-xs font-medium text-brand-shade2 mb-1">System prompt</label>
              <textarea
                value={systemPrompt}
                onChange={(e) => setSystemPrompt(e.target.value)}
                placeholder="You are a helpful assistant..."
                rows={6}
                disabled={submitting}
                className="w-full bg-brand-dark-alt border border-brand-shade3/30 rounded-btn text-sm text-brand-light px-3 py-2 focus:outline-none focus:border-brand-accent resize-y"
              />
            </div>

            {formError && (
              <div className="text-xs text-red-400 bg-red-500/10 border border-red-500/20 rounded-btn px-3 py-2">
                {formError}
              </div>
            )}
          </div>

          <div className="px-5 py-3 border-t border-brand-shade3/10 flex items-center justify-end gap-2">
            <button
              type="button"
              onClick={onClose}
              disabled={submitting}
              className="px-3 py-1.5 text-sm text-brand-shade2 hover:text-brand-light transition-colors disabled:opacity-40"
            >
              Cancel
            </button>
            <button
              type="submit"
              disabled={!canSubmit}
              className="px-4 py-1.5 bg-brand-accent text-white text-sm font-medium rounded-btn hover:bg-brand-accent/80 transition-colors disabled:opacity-40 disabled:cursor-not-allowed"
            >
              {submitting ? 'Creating…' : 'Create agent'}
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}

export default function AgentsPage() {
  const navigate = useNavigate();
  const [search, setSearch] = useState('');
  const [systemExpanded, setSystemExpanded] = useState(false);
  const [showCreate, setShowCreate] = useState(false);

  const { data: apiAgents, refetch } = useApi(() => api.listAgents());
  useAdminRefresh(refetch);
  const agents = apiAgents ?? [];
  const allFiltered = agents.filter(a => a.name.toLowerCase().includes(search.toLowerCase()));
  const userAgents = allFiltered.filter(a => !a.is_system);
  const systemAgents = allFiltered.filter(a => a.is_system);
  // Existing-name set drives both the inline UX guard ("name already exists")
  // and recovery if the engine 409s — keeps the client mirror up to date.
  const existingNames = useMemo(() => new Set(agents.map(a => a.name)), [agents]);

  function handleAgentClick(agent: AgentInfo) {
    const schemas = (agent as any).used_in_schemas ?? (agent as any).schemas ?? [];
    if (schemas[0]) {
      navigate(`/schemas/${schemas[0]}/${agent.name}`);
    } else {
      navigate(`/agents/${agent.name}`);
    }
  }

  function handleCreated(name: string) {
    setShowCreate(false);
    refetch();
    navigate(`/agents/${name}`);
  }

  return (
    <PageContainer>
      <div className="flex items-center justify-between mb-6">
        <div>
          <h1 className="text-xl font-semibold text-brand-light">Agents</h1>
          <p className="text-sm text-brand-shade3 mt-1">Global agent configurations. Changes affect all schemas using the agent.</p>
        </div>
        <Button onClick={() => setShowCreate(true)}>+ New Agent</Button>
      </div>

      {/* Search */}
      <div className="mb-4">
        <input
          type="text"
          placeholder="Search agents..."
          value={search}
          onChange={e => setSearch(e.target.value)}
          className="w-full max-w-sm bg-brand-dark-alt border border-brand-shade3/50 rounded-btn text-sm text-brand-light px-3 py-2 focus:outline-none focus:border-brand-accent placeholder-brand-shade3"
        />
      </div>

      {/* User agents table */}
      <div className="bg-brand-dark-surface rounded-card border border-brand-shade3/10 overflow-hidden">
        <table className="w-full text-sm">
          <thead>
            <tr className="border-b border-brand-shade3/10">
              {TABLE_HEADERS.map(h => (
                <th key={h} className="text-left px-4 py-3 text-xs font-semibold text-brand-shade3 uppercase tracking-wider">{h}</th>
              ))}
            </tr>
          </thead>
          <tbody>
            {userAgents.map(agent => (
              <AgentRow key={agent.name} agent={agent} onClick={() => handleAgentClick(agent)} />
            ))}
          </tbody>
        </table>
        {userAgents.length === 0 && (
          <div className="px-4 py-8 text-center text-brand-shade3 text-sm">
            {search ? (
              'No agents match your search.'
            ) : (
              <>
                <p>No agents configured. Connect a coding agent and it builds one for you.</p>
                <p className="mt-3">
                  <OnboardCodingAgentButton />
                </p>
                <p className="mt-3 text-xs text-brand-shade3/70">
                  If SyntheticBrew helps you, consider{' '}
                  <a
                    href="https://github.com/syntheticinc/syntheticbrew"
                    target="_blank"
                    rel="noopener noreferrer"
                    className="text-brand-accent hover:underline"
                  >
                    starring us on GitHub
                  </a>
                  .
                </p>
              </>
            )}
          </div>
        )}
      </div>

      {/* System agents — collapsible */}
      {systemAgents.length > 0 && (
        <div className="mt-4">
          <button
            onClick={() => setSystemExpanded(e => !e)}
            className="flex items-center gap-2 text-xs text-brand-shade3 hover:text-brand-shade2 transition-colors mb-2"
          >
            <svg
              width="12" height="12" viewBox="0 0 14 14" fill="none"
              className={`transition-transform ${systemExpanded ? 'rotate-180' : ''}`}
            >
              <path d="M3 5L7 9L11 5" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" />
            </svg>
            <span className="uppercase tracking-wider font-semibold">System Agents</span>
            <span className="text-brand-shade3/50">({systemAgents.length})</span>
          </button>

          {systemExpanded && (
            <div className="bg-brand-dark-surface rounded-card border border-brand-accent/15 overflow-hidden">
              <table className="w-full text-sm">
                <thead>
                  <tr className="border-b border-brand-shade3/10">
                    {TABLE_HEADERS.map(h => (
                      <th key={h} className="text-left px-4 py-3 text-xs font-semibold text-brand-shade3 uppercase tracking-wider">{h}</th>
                    ))}
                  </tr>
                </thead>
                <tbody>
                  {systemAgents.map(agent => (
                    <AgentRow key={agent.name} agent={agent} onClick={() => handleAgentClick(agent)} />
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>
      )}

      <p className="text-xs text-brand-shade3/50 mt-4">
        {userAgents.length} agent{userAgents.length !== 1 ? 's' : ''} total.
        {systemAgents.length > 0 && ` ${systemAgents.length} system agent${systemAgents.length !== 1 ? 's' : ''} hidden.`}
        {' '}Click to edit configuration.
      </p>

      {showCreate && (
        <NewAgentModal
          existingNames={existingNames}
          onClose={() => setShowCreate(false)}
          onCreated={handleCreated}
        />
      )}
    </PageContainer>
  );
}
