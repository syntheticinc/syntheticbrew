import { useState, useEffect, useRef } from 'react';
import { useParams, useNavigate } from 'react-router-dom';
import { api } from '../api/client';
import FormField from '../components/FormField';
import CapabilityBlock, { capabilityIcon, getCapabilityDefaultConfig } from '../components/builder/CapabilityBlock';
import ConfirmDialog from '../components/ConfirmDialog';
import { ToastProvider, useToast } from '../components/builder/Toast';
import type { AgentDetail, CapabilityConfig, CapabilityType, Model, MCPServer } from '../types';
import { CAPABILITY_META } from '../types';
import { usePrototype } from '../hooks/usePrototype';
import { MOCK_AGENTS, MOCK_MODELS } from '../mocks/agents';

const ALL_CAPABILITY_TYPES = Object.keys(CAPABILITY_META) as CapabilityType[];

type Zone = 'safe' | 'caution' | 'dangerous';

const ZONE_CONFIG: Record<Zone, { label: string; labelClass: string; borderClass: string }> = {
  safe:      { label: 'Safe',      labelClass: 'text-brand-shade3',  borderClass: 'border-brand-shade3/30' },
  caution:   { label: 'Caution',   labelClass: 'text-amber-400',     borderClass: 'border-amber-500/30' },
  dangerous: { label: 'Dangerous', labelClass: 'text-brand-accent',  borderClass: 'border-brand-accent/50' },
};

// Tool tiers for display
type ToolTier = 'core' | 'auto' | 'mcp';

const TOOL_TIERS: Record<ToolTier, { label: string; description: string; tools: string[]; labelClass: string; borderClass: string; alwaysOn?: boolean }> = {
  core: {
    label: 'Core',
    description: 'Essential tools always available to every agent',
    tools: ['manage_tasks', 'show_structured_output', 'spawn_agent'],
    labelClass: 'text-brand-shade3',
    borderClass: 'border-brand-shade3/30',
  },
  auto: {
    label: 'Auto-injected',
    description: 'Added automatically when capability is enabled (Memory, Knowledge)',
    tools: ['memory_recall', 'memory_store', 'knowledge_search'],
    labelClass: 'text-purple-400',
    borderClass: 'border-purple-500/30',
  },
  mcp: {
    label: 'MCP',
    description: 'External tools from connected MCP servers (web search, APIs, integrations)',
    tools: [],  // filled dynamically from agent.mcp_servers
    labelClass: 'text-brand-accent',
    borderClass: 'border-brand-accent/30',
  },
};

function AgentDrillInInner() {
  const { addToast } = useToast();
  const { schema, agent: agentName } = useParams<{ schema: string; agent: string }>();
  const navigate = useNavigate();
  const { isPrototype } = usePrototype();

  const [agent, setAgent] = useState<AgentDetail | null>(null);
  const [capabilities, setCapabilities] = useState<CapabilityConfig[]>([]);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [showCapDropdown, setShowCapDropdown] = useState(false);
  const [enabledTools, setEnabledTools] = useState<string[]>([]);
  const [canSpawn, setCanSpawn] = useState<string[]>([]);
  const [allAgentNames, setAllAgentNames] = useState<string[]>([]);
  const [models, setModels] = useState<Model[]>([]);
  const [confirmInput, setConfirmInput] = useState('');
  const [mcpServers, setMcpServers] = useState<MCPServer[]>([]);
  const [enabledMCP, setEnabledMCP] = useState<string[]>([]);
  const dropdownRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!agentName) { setLoading(false); return; }

    if (isPrototype) {
      const mockAgent = MOCK_AGENTS[agentName] ?? MOCK_AGENTS['support-agent'];
      if (mockAgent) {
        setAgent(mockAgent);
        setEnabledTools(mockAgent.tools ?? []);
        setCanSpawn(mockAgent.can_spawn ?? []);
      }
      // Pre-populate capabilities for prototype (matching original prototype)
      const protoCaps: CapabilityConfig[] = [
        { type: 'memory', enabled: true, config: { unlimited_retention: true, unlimited_entries: false, max_entries: 500 } },
        { type: 'knowledge', enabled: true, config: { sources: ['support-docs.pdf'], chunks: 2341, top_k: 5, similarity_threshold: 0.75 } },
      ];
      setCapabilities(protoCaps);
      setInitialCapabilities(protoCaps.map((c) => ({ ...c })));
      setModels(MOCK_MODELS as Model[]);
      setAllAgentNames(Object.keys(MOCK_AGENTS));
      setLoading(false);
      return;
    }

    Promise.all([
      api.getAgent(agentName),
      api.listCapabilities(agentName),
    ])
      .then(([data, caps]) => {
        setAgent(data);
        setEnabledTools(data.tools ?? []);
        setCanSpawn(data.can_spawn ?? []);
        setEnabledMCP(data.mcp_servers ?? []);
        const mapped: CapabilityConfig[] = caps.map((c) => {
          const defaults = getCapabilityDefaultConfig(c.type);
          const config = Object.keys(c.config ?? {}).length > 0 ? c.config : defaults;
          return {
            id: c.id,
            agent_name: c.agent_name,
            type: c.type as CapabilityType,
            config,
            enabled: c.enabled,
          };
        });
        setCapabilities(mapped);
        setInitialCapabilities(mapped.map((c) => ({ ...c })));
      })
      .catch(() => { /* fallback to empty */ })
      .finally(() => setLoading(false));
  }, [agentName, isPrototype]);

  // Fetch models and all agent names for connections (production only)
  useEffect(() => {
    if (isPrototype) return;
    // Wave 5: agents require a chat-kind model (backend enforces
    // `model_id must reference a chat model`). Filter server-side.
    api.listModels({ kind: 'chat' }).then(setModels).catch(() => {});
    api.listAgents().then((agents: Array<{ name: string }>) => setAllAgentNames(agents.map((a) => a.name))).catch(() => {});
    api.listMCPServers().then(setMcpServers).catch(() => {});
  }, [isPrototype]);

  // Close dropdown on outside click
  useEffect(() => {
    function handleClick(e: MouseEvent) {
      if (dropdownRef.current && !dropdownRef.current.contains(e.target as Node)) {
        setShowCapDropdown(false);
      }
    }
    document.addEventListener('mousedown', handleClick);
    return () => document.removeEventListener('mousedown', handleClick);
  }, []);

  function addCapability(type: CapabilityType) {
    if (capabilities.some((c) => c.type === type)) return;
    setCapabilities((prev) => [...prev, { type, enabled: true, config: getCapabilityDefaultConfig(type) }]);
    setShowCapDropdown(false);
  }

  function updateCapability(index: number, updated: CapabilityConfig) {
    setCapabilities((prev) => prev.map((c, i) => (i === index ? updated : c)));
  }

  function removeCapability(index: number) {
    setCapabilities((prev) => prev.filter((_, i) => i !== index));
  }

  function toggleTool(name: string) {
    setEnabledTools((prev) =>
      prev.includes(name) ? prev.filter((t) => t !== name) : [...prev, name],
    );
  }

  // Track initial capabilities loaded from API for diffing on save
  const [initialCapabilities, setInitialCapabilities] = useState<CapabilityConfig[]>([]);

  async function handleSave() {
    if (!agentName || !agent) return;
    setSaving(true);
    try {
      // Save agent config
      await api.updateAgent(agentName, {
        system_prompt: agent.system_prompt,
        model_id: agent.model_id,
        lifecycle: agent.lifecycle,
        tool_execution: agent.tool_execution,
        max_steps: agent.max_steps,
        max_context_size: agent.max_context_size,
        max_turn_duration: agent.max_turn_duration,
        temperature: agent.temperature,
        top_p: agent.top_p,
        max_tokens: agent.max_tokens,
        stop_sequences: agent.stop_sequences,
        confirm_before: agent.confirm_before,
        tools: enabledTools,
        can_spawn: canSpawn,
        mcp_servers: enabledMCP,
      });

      // Save capabilities — diff against initial state
      const initialTypes = new Set(initialCapabilities.map((c) => c.type));
      const currentTypes = new Set(capabilities.map((c) => c.type));

      // Removed capabilities
      for (const cap of initialCapabilities) {
        if (!currentTypes.has(cap.type) && cap.id) {
          await api.removeCapability(agentName, cap.id);
        }
      }

      // Added or updated capabilities
      for (const cap of capabilities) {
        if (!initialTypes.has(cap.type)) {
          // New capability
          await api.addCapability(agentName, { type: cap.type, config: cap.config, enabled: cap.enabled });
        } else if (cap.id) {
          // Existing — update
          await api.updateCapability(agentName, cap.id, { config: cap.config, enabled: cap.enabled });
        }
      }

      // Refresh initial state after save
      setInitialCapabilities(capabilities.map((c) => ({ ...c })));

      addToast('Agent saved successfully', 'success');
    } catch (err) {
      addToast(err instanceof Error ? err.message : 'Save failed', 'error');
    } finally {
      setSaving(false);
    }
  }

  const [showDeleteConfirm, setShowDeleteConfirm] = useState(false);
  const [showRestoreConfirm, setShowRestoreConfirm] = useState(false);
  const [restoring, setRestoring] = useState(false);

  async function handleRestore() {
    setRestoring(true);
    try {
      await api.restoreBuilderAssistant();
      addToast('Builder Assistant restored to factory defaults', 'success');
      // Reload agent data
      if (agentName && !isPrototype) {
        const data = await api.getAgent(agentName);
        setAgent(data);
        setEnabledTools(data.tools ?? []);
        setCanSpawn(data.can_spawn ?? []);
      }
    } catch (err) {
      addToast(err instanceof Error ? err.message : 'Restore failed', 'error');
    } finally {
      setRestoring(false);
    }
  }

  async function handleDeleteConfirmed() {
    if (!agentName) return;
    try {
      await api.deleteAgent(agentName);
      navigate('/schemas');
    } catch (err) {
      addToast(err instanceof Error ? err.message : 'Delete failed', 'error');
    }
  }

  function updateAgentField<K extends keyof AgentDetail>(key: K, value: AgentDetail[K]) {
    setAgent((prev) => (prev ? { ...prev, [key]: value } : prev));
  }

  const usedCapabilityTypes = new Set(capabilities.map((c) => c.type));
  const availableCapTypes = ALL_CAPABILITY_TYPES.filter((t) => !usedCapabilityTypes.has(t));

  if (loading) {
    return (
      <div className="flex items-center justify-center h-64 text-brand-shade3 text-sm font-mono">
        Loading…
      </div>
    );
  }

  return (
    <div className="flex flex-col h-full min-h-0">
      {/* Breadcrumb header */}
      <div className="flex items-center justify-between px-6 py-3 border-b border-brand-shade3/10 bg-brand-dark-surface flex-shrink-0">
        <div className="flex items-center gap-2">
          <button
            type="button"
            onClick={() => navigate('/schemas')}
            className="flex items-center gap-2 text-brand-shade3 hover:text-brand-light transition-colors text-sm font-mono px-2 py-1 -ml-2 rounded-btn hover:bg-brand-dark-alt"
          >
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
              <path d="M19 12H5M12 19l-7-7 7-7" />
            </svg>
            {schema}
          </button>
          <span className="text-brand-shade3/40 text-sm">/</span>
          <span className="text-brand-light text-sm font-mono font-semibold">{agentName}</span>
          {agent?.is_system && (
            <span className="text-[10px] text-brand-shade3 bg-brand-shade3/10 px-1.5 py-0.5 rounded border border-brand-shade3/20 font-mono">
              System
            </span>
          )}
        </div>
        <div className="flex items-center gap-2">
          <button
            type="button"
            onClick={handleSave}
            disabled={saving}
            className="px-4 py-1.5 bg-brand-accent text-brand-light rounded-btn text-sm font-medium font-mono hover:bg-brand-accent/90 disabled:opacity-50 transition-colors"
          >
            {saving ? 'Saving…' : 'Save'}
          </button>
          {agentName === 'builder-assistant' && (
            <button
              type="button"
              onClick={() => setShowRestoreConfirm(true)}
              disabled={restoring}
              className="px-4 py-1.5 border border-amber-500/40 text-amber-400 rounded-btn text-sm font-medium font-mono hover:bg-amber-500/10 disabled:opacity-50 transition-colors"
            >
              {restoring ? 'Restoring…' : 'Restore defaults'}
            </button>
          )}
          <button
            type="button"
            onClick={() => setShowDeleteConfirm(true)}
            className="px-4 py-1.5 border border-brand-accent/40 text-brand-accent rounded-btn text-sm font-medium font-mono hover:bg-brand-accent/10 transition-colors"
          >
            Delete
          </button>
        </div>
      </div>

      {/* Global entity banner */}
      <div className="px-6 py-2 bg-blue-500/5 border-b border-blue-500/20">
        <div className="flex items-center gap-2">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" className="text-blue-400 shrink-0">
            <circle cx="12" cy="12" r="10" /><line x1="2" y1="12" x2="22" y2="12" /><path d="M12 2a15.3 15.3 0 014 10 15.3 15.3 0 01-4 10 15.3 15.3 0 01-4-10 15.3 15.3 0 014-10z" />
          </svg>
          <span className="text-xs text-blue-400 font-mono">This agent is global — changes affect all schemas using it</span>
        </div>
        <p className="text-[11px] text-brand-shade3/70 font-mono ml-[22px] mt-0.5">Used in: Support Schema, Sales Schema</p>
      </div>

      {/* Delete confirmation */}
      <ConfirmDialog
        open={showDeleteConfirm}
        onClose={() => setShowDeleteConfirm(false)}
        onConfirm={() => { setShowDeleteConfirm(false); handleDeleteConfirmed(); }}
        title={`Delete "${agentName}"?`}
        message="This action cannot be undone."
        confirmLabel="Delete"
        variant="danger"
      />

      {/* Restore confirmation */}
      <ConfirmDialog
        open={showRestoreConfirm}
        onClose={() => setShowRestoreConfirm(false)}
        onConfirm={() => { setShowRestoreConfirm(false); handleRestore(); }}
        title="Restore to factory defaults?"
        message="This will reset the agent's system prompt, tools, and configuration to factory defaults. Your customizations will be lost."
        confirmLabel="Restore"
        variant="warning"
      />

      {/* Scrollable body */}
      <div className="flex-1 overflow-y-auto px-6 py-6 space-y-4">

        {/* Model + Lifecycle card */}
        <div className="bg-brand-dark-surface border border-brand-shade3/10 rounded-card p-4">
          <h2 className="flex items-center gap-2 text-xs font-semibold text-brand-shade3 uppercase tracking-widest mb-3 font-mono">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5"><path d="M21 16V8a2 2 0 00-1-1.73l-7-4a2 2 0 00-2 0l-7 4A2 2 0 003 8v8a2 2 0 001 1.73l7 4a2 2 0 002 0l7-4A2 2 0 0021 16z" /><path d="M3.27 6.96L12 12.01l8.73-5.05M12 22.08V12" /></svg>
            Model & Lifecycle
          </h2>
          <div className="grid grid-cols-2 gap-4">
            <div>
              <label className="block text-sm font-medium text-brand-light mb-1">Model</label>
              <select
                value={agent?.model_id ?? ''}
                onChange={(e) => updateAgentField('model_id', e.target.value || undefined)}
                className="w-full px-3 py-2 bg-brand-dark-alt border border-brand-shade3/50 rounded-card text-sm text-brand-light font-mono focus:outline-none focus:border-brand-accent focus:ring-1 focus:ring-brand-accent transition-colors"
              >
                <option value="">Select model...</option>
                {models.map((m) => (
                  <option key={m.id} value={m.id}>{m.name} ({m.model_name})</option>
                ))}
              </select>
              <p className="mt-1 text-xs text-brand-shade3">LLM model used for agent reasoning</p>
            </div>
            <FormField
              label="Lifecycle"
              type="select"
              value={agent?.lifecycle ?? 'persistent'}
              onChange={(v) => updateAgentField('lifecycle', v as AgentDetail['lifecycle'])}
              options={[
                { value: 'persistent', label: 'Persistent' },
                { value: 'spawn', label: 'Spawn' },
              ]}
              hint="Persistent: always running. Spawn: created on-demand by other agents"
            />
          </div>
        </div>

        {/* System Prompt card */}
        <div className="bg-brand-dark-surface border border-brand-shade3/10 rounded-card p-4">
          <h2 className="flex items-center gap-2 text-xs font-semibold text-brand-shade3 uppercase tracking-widest mb-3 font-mono">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5"><path d="M21 15a2 2 0 01-2 2H7l-4 4V5a2 2 0 012-2h14a2 2 0 012 2z" /></svg>
            System Prompt
          </h2>
          <textarea
            value={agent?.system_prompt ?? ''}
            onChange={(e) => updateAgentField('system_prompt', e.target.value)}
            rows={6}
            className="w-full px-3 py-2 bg-brand-dark-alt border border-brand-shade3/50 rounded-card text-sm text-brand-light font-mono focus:outline-none focus:border-brand-accent focus:ring-1 focus:ring-brand-accent transition-colors resize-y"
            style={{ minHeight: '120px' }}
          />
          <p className="mt-1 text-xs text-brand-shade3">Instructions that define agent behavior, personality, and constraints</p>
        </div>

        {/* Parameters card */}
        <div className="bg-brand-dark-surface border border-brand-shade3/10 rounded-card p-4">
          <h2 className="flex items-center gap-2 text-xs font-semibold text-brand-shade3 uppercase tracking-widest mb-3 font-mono">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5"><line x1="4" y1="21" x2="4" y2="14" /><line x1="4" y1="10" x2="4" y2="3" /><line x1="12" y1="21" x2="12" y2="12" /><line x1="12" y1="8" x2="12" y2="3" /><line x1="20" y1="21" x2="20" y2="16" /><line x1="20" y1="12" x2="20" y2="3" /><line x1="1" y1="14" x2="7" y2="14" /><line x1="9" y1="8" x2="15" y2="8" /><line x1="17" y1="16" x2="23" y2="16" /></svg>
            Parameters
          </h2>
          <div className="grid grid-cols-3 gap-4">
            <FormField
              label="Max Turn Steps"
              type="number"
              value={agent?.max_steps ?? 50}
              onChange={(v) => updateAgentField('max_steps', Number(v))}
              min={1}
              max={500}
              hint="Max actions per turn (tool calls, reasoning, responses). Prevents infinite loops within a single interaction"
            />
            <FormField
              label="Context Size"
              type="number"
              value={agent?.max_context_size ?? 16000}
              onChange={(v) => updateAgentField('max_context_size', Number(v))}
              min={1000}
              max={200000}
              step={1000}
              hint="Token window for conversation history (larger = more memory, higher cost)"
            />
            <FormField
              label="Max Turn Duration (seconds)"
              type="number"
              value={agent?.max_turn_duration ?? 120}
              onChange={(v) => updateAgentField('max_turn_duration', Number(v))}
              min={30}
              max={600}
              step={10}
              hint="Maximum time in seconds for a single LLM stream turn"
            />
            <FormField
              label="Execution"
              type="select"
              value={agent?.tool_execution ?? 'sequential'}
              onChange={(v) => updateAgentField('tool_execution', v as AgentDetail['tool_execution'])}
              options={[
                { value: 'sequential', label: 'Sequential' },
                { value: 'parallel', label: 'Parallel' },
              ]}
              hint="Sequential: tools one by one. Parallel: multiple tools simultaneously"
            />
          </div>

          {/* Confirm Before */}
          <div className="border-t border-brand-shade3/10 mt-4 pt-4">
            <p className="text-xs font-semibold text-brand-shade3 uppercase tracking-widest mb-2 font-mono">Confirm Before</p>
            <p className="text-xs text-brand-shade3/60 mb-2">Tools requiring user confirmation before execution</p>
            <div className="flex gap-2 mb-2">
              <input
                type="text"
                value={confirmInput}
                onChange={(e) => setConfirmInput(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === 'Enter') {
                    e.preventDefault();
                    const trimmed = confirmInput.trim();
                    if (trimmed && !(agent?.confirm_before ?? []).includes(trimmed)) {
                      updateAgentField('confirm_before', [...(agent?.confirm_before ?? []), trimmed]);
                      setConfirmInput('');
                    }
                  }
                }}
                placeholder="Tool name..."
                className="flex-1 px-3 py-1.5 bg-brand-dark-alt border border-brand-shade3/50 rounded-card text-sm text-brand-light font-mono focus:outline-none focus:border-brand-accent focus:ring-1 focus:ring-brand-accent transition-colors"
              />
              <button
                type="button"
                onClick={() => {
                  const trimmed = confirmInput.trim();
                  if (trimmed && !(agent?.confirm_before ?? []).includes(trimmed)) {
                    updateAgentField('confirm_before', [...(agent?.confirm_before ?? []), trimmed]);
                    setConfirmInput('');
                  }
                }}
                className="px-3 py-1.5 border border-brand-shade3/30 rounded-btn text-xs text-brand-shade2 font-mono hover:text-brand-light hover:border-brand-shade3/60 transition-colors"
              >
                Add
              </button>
            </div>
            {(agent?.confirm_before ?? []).length > 0 && (
              <div className="flex flex-wrap gap-1.5">
                {(agent?.confirm_before ?? []).map((t) => (
                  <span key={t} className="inline-flex items-center gap-1 px-2 py-1 bg-amber-500/10 border border-amber-500/30 rounded-btn text-xs text-amber-400 font-mono">
                    {t}
                    <button
                      type="button"
                      onClick={() => updateAgentField('confirm_before', (agent?.confirm_before ?? []).filter((x) => x !== t))}
                      className="text-amber-400/60 hover:text-amber-400 ml-0.5"
                    >
                      x
                    </button>
                  </span>
                ))}
              </div>
            )}
          </div>

          {/* Model Parameters */}
          <div className="border-t border-brand-shade3/10 mt-4 pt-4">
            <p className="text-xs font-semibold text-brand-shade3 uppercase tracking-widest mb-3 font-mono">Model Parameters</p>
            <div className="grid grid-cols-3 gap-4">
              <FormField
                label="Temperature"
                type="number"
                value={agent?.temperature ?? 0.7}
                onChange={(v) => updateAgentField('temperature', Number(v))}
                min={0}
                max={2}
                step={0.1}
                hint="Controls randomness. 0 = deterministic, 2 = maximum creativity. Most tasks work best at 0.3-0.7"
              />
              <FormField
                label="Top P"
                type="number"
                value={agent?.top_p ?? 1.0}
                onChange={(v) => updateAgentField('top_p', Number(v))}
                min={0}
                max={1}
                step={0.05}
                hint="Nucleus sampling: considers tokens with top P probability mass. Alternative to temperature — usually change one, not both"
              />
              <FormField
                label="Max Tokens"
                type="number"
                value={agent?.max_tokens ?? 4096}
                onChange={(v) => updateAgentField('max_tokens', Number(v))}
                min={1}
                max={128000}
                step={256}
                hint="Maximum length of model response in tokens (~4 chars per token). Higher = longer responses, higher cost"
              />
              <FormField
                label="Stop Sequences"
                type="text"
                value={agent?.stop_sequences?.join(', ') ?? ''}
                onChange={(v) => updateAgentField('stop_sequences', v ? String(v).split(',').map(s => s.trim()).filter(Boolean) : [])}
                placeholder="e.g. \n\n, END, ---"
                hint="Comma-separated strings that stop generation. Model stops when it outputs any of these sequences"
              />
            </div>
          </div>
        </div>

        {/* Capabilities card */}
        <div className="bg-brand-dark-surface border border-brand-shade3/10 rounded-card p-4">
          <div className="flex items-center justify-between mb-3">
            <h2 className="flex items-center gap-2 text-xs font-semibold text-brand-shade3 uppercase tracking-widest font-mono">
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5"><rect x="3" y="3" width="7" height="7" rx="1" /><rect x="14" y="3" width="7" height="7" rx="1" /><rect x="3" y="14" width="7" height="7" rx="1" /><rect x="14" y="14" width="7" height="7" rx="1" /></svg>
              Capabilities
            </h2>
            <div className="relative" ref={dropdownRef}>
              <button
                type="button"
                onClick={() => setShowCapDropdown((v) => !v)}
                className="px-3 py-1 border border-brand-shade3/30 rounded-btn text-xs text-brand-shade2 font-mono hover:text-brand-light hover:border-brand-shade3/60 transition-colors"
              >
                + Add
              </button>
              {showCapDropdown && availableCapTypes.length > 0 && (
                <div className="absolute right-0 top-full mt-1 z-50 bg-brand-dark-alt border border-brand-shade3/20 rounded-card shadow-lg min-w-[180px]">
                  {availableCapTypes.map((type) => (
                    <button
                      key={type}
                      type="button"
                      onClick={() => addCapability(type)}
                      className="w-full flex items-center gap-2 px-3 py-2 text-left text-sm text-brand-shade2 hover:bg-brand-dark hover:text-brand-light font-mono transition-colors"
                    >
                      <span className="text-brand-shade3 shrink-0">{capabilityIcon(CAPABILITY_META[type].icon)}</span>
                      <span>{CAPABILITY_META[type].label}</span>
                    </button>
                  ))}
                </div>
              )}
              {showCapDropdown && availableCapTypes.length === 0 && (
                <div className="absolute right-0 top-full mt-1 z-50 bg-brand-dark-alt border border-brand-shade3/20 rounded-card shadow-lg px-3 py-2 text-xs text-brand-shade3 font-mono">
                  All capabilities added
                </div>
              )}
            </div>
          </div>
          {capabilities.length === 0 ? (
            <p className="text-sm text-brand-shade3 font-mono">No capabilities configured. Click + Add to extend agent with memory or knowledge.</p>
          ) : (
            <div className="space-y-2">
              {capabilities.map((cap, i) => (
                <div key={cap.type} className="animate-slide-down overflow-hidden">
                  <CapabilityBlock
                    capability={cap}
                    onChange={(updated) => updateCapability(i, updated)}
                    onRemove={() => removeCapability(i)}
                    agentName={agentName}
                    models={models as { id: string; name: string; model_name: string }[]}
                  />
                </div>
              ))}
            </div>
          )}
        </div>

        {/* Tools card */}
        <div className="bg-brand-dark-surface border border-brand-shade3/10 rounded-card p-4">
          <h2 className="flex items-center gap-2 text-xs font-semibold text-brand-shade3 uppercase tracking-widest mb-1 font-mono">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5"><path d="M14.7 6.3a1 1 0 000 1.4l1.6 1.6a1 1 0 001.4 0l3.77-3.77a6 6 0 01-7.94 7.94l-6.91 6.91a2.12 2.12 0 01-3-3l6.91-6.91a6 6 0 017.94-7.94l-3.76 3.76z" /></svg>
            Tools
          </h2>
          <p className="text-xs text-brand-shade3 mb-3">Tools are organized by availability tier. External integrations connect via MCP servers.</p>
          <div className="space-y-3">
            {/* Core — on by default, toggleable */}
            <div className={`border ${TOOL_TIERS.core.borderClass} rounded-card p-3`}>
              <p className={`text-xs font-semibold ${TOOL_TIERS.core.labelClass} mb-1 font-mono`}>
                {TOOL_TIERS.core.label} <span className="font-normal text-brand-shade3/60">— {TOOL_TIERS.core.description}</span>
              </p>
              <div className="flex flex-wrap gap-2">
                {TOOL_TIERS.core.tools.map((tool) => (
                  <ToolChip key={tool} name={tool} zone="safe" enabled={enabledTools.includes(tool)} onToggle={toggleTool} />
                ))}
              </div>
            </div>

            {/* Auto-injected — from capabilities, toggleable */}
            {capabilities.length > 0 && (() => {
              const autoTools: string[] = [];
              if (capabilities.some(c => c.type === 'memory')) autoTools.push('memory_recall', 'memory_store');
              if (capabilities.some(c => c.type === 'knowledge')) autoTools.push('knowledge_search');
              if (autoTools.length === 0) return null;
              return (
                <div className={`border ${TOOL_TIERS.auto.borderClass} rounded-card p-3`}>
                  <p className={`text-xs font-semibold ${TOOL_TIERS.auto.labelClass} mb-1 font-mono`}>
                    {TOOL_TIERS.auto.label} <span className="font-normal text-brand-shade3/60">— {TOOL_TIERS.auto.description}</span>
                  </p>
                  <div className="flex flex-wrap gap-2">
                    {autoTools.map((tool) => (
                      <span key={tool} className="px-2 py-0.5 bg-purple-500/10 border border-purple-500/20 rounded text-xs text-purple-300 font-mono">
                        {tool}
                      </span>
                    ))}
                  </div>
                </div>
              );
            })()}

            {/* MCP — from connected servers */}
            <div className={`border ${TOOL_TIERS.mcp.borderClass} rounded-card p-3`}>
              <p className={`text-xs font-semibold ${TOOL_TIERS.mcp.labelClass} mb-1 font-mono`}>
                {TOOL_TIERS.mcp.label} <span className="font-normal text-brand-shade3/60">— {TOOL_TIERS.mcp.description}</span>
              </p>
              {mcpServers.length > 0 ? (
                <div className="flex flex-wrap gap-2">
                  {mcpServers.map((srv) => {
                    const isSelected = enabledMCP.includes(srv.name);
                    return (
                      <label
                        key={srv.name}
                        className={`inline-flex items-center gap-1.5 px-3 py-1.5 rounded-btn border text-sm cursor-pointer transition-colors ${
                          isSelected
                            ? 'border-brand-accent bg-brand-accent/15 text-brand-accent'
                            : 'border-brand-shade3/30 bg-brand-dark-alt text-brand-shade2 hover:bg-brand-shade3/10'
                        }`}
                      >
                        <input
                          type="checkbox"
                          checked={isSelected}
                          onChange={() => setEnabledMCP((prev) =>
                            prev.includes(srv.name) ? prev.filter((n) => n !== srv.name) : [...prev, srv.name]
                          )}
                          className="sr-only"
                        />
                        {srv.name}
                      </label>
                    );
                  })}
                </div>
              ) : (
                <p className="text-xs text-brand-shade3/50 font-mono italic">No MCP servers configured. Add via Settings → MCP Servers</p>
              )}
            </div>
          </div>
        </div>

        {/* Connections card */}
        <div className="bg-brand-dark-surface border border-brand-shade3/10 rounded-card p-4">
          <h2 className="flex items-center gap-2 text-xs font-semibold text-brand-shade3 uppercase tracking-widest mb-3 font-mono">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5"><circle cx="5" cy="6" r="2" /><circle cx="19" cy="6" r="2" /><circle cx="12" cy="18" r="2" /><line x1="5" y1="8" x2="12" y2="16" /><line x1="19" y1="8" x2="12" y2="16" /></svg>
            Connections</h2>
          <div className="grid grid-cols-2 gap-4">
            <div className="border border-brand-shade3/10 rounded-card p-3">
              <p className="text-xs text-brand-shade3 font-mono mb-2">Receives from</p>
              <p className="text-xs text-brand-shade3/50 font-mono italic">
                Determined by other agents' can_spawn config (read-only)
              </p>
            </div>
            <div className="border border-brand-shade3/10 rounded-card p-3">
              <p className="text-xs text-brand-shade3 font-mono mb-2">Can spawn</p>
              {allAgentNames.filter((n) => n !== agentName).length === 0 ? (
                <p className="text-xs text-brand-shade3/50 font-mono">No other agents available</p>
              ) : (
                <div className="space-y-1">
                  {allAgentNames.filter((n) => n !== agentName).map((name) => (
                    <label key={name} className="flex items-center gap-2 cursor-pointer">
                      <input
                        type="checkbox"
                        checked={canSpawn.includes(name)}
                        onChange={() =>
                          setCanSpawn((prev) =>
                            prev.includes(name) ? prev.filter((n) => n !== name) : [...prev, name],
                          )
                        }
                        className="accent-brand-accent"
                      />
                      <span className="text-xs text-brand-shade2 font-mono">{name}</span>
                    </label>
                  ))}
                </div>
              )}
            </div>
          </div>
        </div>

      </div>
    </div>
  );
}

// ─── ToolChip ─────────────────────────────────────────────────────────────────

interface ToolChipProps {
  name: string;
  zone: Zone;
  enabled: boolean;
  onToggle: (name: string) => void;
}

const ZONE_ACTIVE: Record<Zone, string> = {
  safe:      'border-status-active bg-status-active/15 text-status-active',
  caution:   'border-amber-500 bg-amber-500/15 text-amber-400',
  dangerous: 'border-brand-accent bg-brand-accent/15 text-brand-accent',
};

function ToolChip({ name, zone, enabled, onToggle }: ToolChipProps) {
  return (
    <label
      className={`inline-flex items-center gap-1.5 px-3 py-1.5 rounded-btn border text-sm font-mono cursor-pointer transition-colors ${
        enabled
          ? ZONE_ACTIVE[zone]
          : `${ZONE_CONFIG[zone].borderClass} bg-brand-dark-alt text-brand-shade2 hover:text-brand-light`
      }`}
    >
      <input type="checkbox" checked={enabled} onChange={() => onToggle(name)} className="sr-only" />
      {name}
    </label>
  );
}

// ─── Wrapper with ToastProvider ──────────────────────────────────────────────

export default function AgentDrillInPage() {
  return (
    <ToastProvider>
      <AgentDrillInInner />
    </ToastProvider>
  );
}
