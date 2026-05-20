import { useState, useRef, useEffect, useCallback, useMemo } from 'react';
import { useSSEChat, type SSEMessage } from '../hooks/useSSEChat';
import { useBottomPanel } from '../hooks/useBottomPanel';
import { usePrototype } from '../hooks/usePrototype';
import { api } from '../api/client';
import type { AgentDetail, SessionSummary, Schema } from '../types';
import HeadersEditor, { type HeaderEntry } from './HeadersEditor';
import ContextUsageBar from './ContextUsageBar';
import BrewingSpinner from './BrewingSpinner';
import { InterruptWidget } from './InterruptWidget';

// ─── Mock streaming for prototype mode ──────────────────────────────────────

const MOCK_TOOL_CALLS = [
  { tool: 'memory_recall', input: '{"query": "previous interactions"}', output: '{"memories": []}' },
  { tool: 'knowledge_search', input: '{"query": "product FAQ"}', output: '{"results": [{"title": "FAQ", "content": "..."}]}' },
];

const MOCK_RESPONSES = [
  'Based on the knowledge base, here is the answer to your question. The system supports multiple agent configurations with memory, knowledge, and escalation capabilities.',
  'I have processed your request. The agent flow executed successfully through the classifier and support pipeline.',
  'Your test message has been routed through the schema flow. All tools executed correctly and the response was generated.',
];

// ─── Component ──────────────────────────────────────────────────────────────

// TestFlowTab is scoped to a single schema. The schema selector lives in
// BottomPanel (shared with AI Assistant). The entry agent is read-only —
// chat is always dispatched to the schema's entry orchestrator via the
// `/api/v1/schemas/{id}/chat` endpoint.
export default function TestFlowTab({ lockedSchemaId }: { lockedSchemaId?: string } = {}) {
  const { selectedSchema } = useBottomPanel();
  const { isPrototype } = usePrototype();

  const [schema, setSchema] = useState<Schema | null>(null);
  const [entryAgent, setEntryAgent] = useState<AgentDetail | null>(null);
  const [headers, setHeaders] = useState<HeaderEntry[]>([]);
  const [message, setMessage] = useState('');
  const [expandedItems, setExpandedItems] = useState<Record<string, boolean>>({});

  // Prototype mode state
  const [protoMessages, setProtoMessages] = useState<SSEMessage[]>([]);
  const [protoStreaming, setProtoStreaming] = useState(false);
  const [protoSessionId, setProtoSessionId] = useState('');

  // Session management state (production only)
  const [sessions, setSessions] = useState<SessionSummary[]>([]);
  const [sessionDropdownOpen, setSessionDropdownOpen] = useState(false);
  const sessionDropdownRef = useRef<HTMLDivElement>(null);

  // Local session history in localStorage (supplements API list — in-memory chat sessions aren't in DB)
  const localSessionsKey = schema ? `bb_testflow_sessions__${schema.id}` : '';
  const [localSessions, setLocalSessions] = useState<SessionSummary[]>(() => []);

  const messagesEndRef = useRef<HTMLDivElement>(null);
  const headersRef = useRef(headers);
  const inputRef = useRef<HTMLTextAreaElement>(null);
  useEffect(() => { headersRef.current = headers; }, [headers]);

  // Build headers getter for SSE hook
  const getHeaders = useCallback((): Record<string, string> => {
    const result: Record<string, string> = {};
    const blocked = ['authorization', 'host', 'cookie', 'origin', 'referer', 'content-type', 'content-length'];
    for (const h of headersRef.current) {
      const k = h.key.trim();
      const v = h.value.trim();
      if (k && v && !blocked.includes(k.toLowerCase())) result[k] = v;
    }
    return result;
  }, []);

  const testflowPersistenceKey = schema ? `bb_testflow_${schema.id}` : undefined;
  const sseChat = useSSEChat({
    // Engine 1.1.0+ chat URL is name-keyed; localStorage keys above keep
    // schema.id (UUID) — internal SPA state, no operator-facing impact.
    schemaName: schema?.name ?? '',
    getHeaders,
    persistenceKey: testflowPersistenceKey,
    fetchMessages: (sid) => api.getSessionEvents(sid),
  });

  // Use either prototype or production messages
  const messages = isPrototype ? protoMessages : sseChat.messages;
  const isStreaming = isPrototype ? protoStreaming : sseChat.isStreaming;

  // Resolve the schema to a real Schema record, then fetch its entry agent.
  // The canvas URL carries the schema NAME (engine 1.1.0+ name-keyed routes),
  // so we match either by id or by name — whichever lockedSchemaId happens
  // to be. Otherwise fall back to selectedSchema name from BottomPanel context.
  useEffect(() => {
    let cancelled = false;

    async function load() {
      const hasLocked = !!lockedSchemaId;
      const hasSelected = !!selectedSchema;
      if (!hasLocked && !hasSelected) {
        if (!cancelled) { setSchema(null); setEntryAgent(null); }
        return;
      }
      try {
        const list = await api.listSchemas();
        if (cancelled) return;
        const match = hasLocked
          ? (list.find((s) => s.id === lockedSchemaId || s.name === lockedSchemaId) ?? null)
          : (list.find((s) => s.name === selectedSchema) ?? null);
        setSchema(match);
        if (match?.entry_agent_name) {
          const detail = await api.getAgent(match.entry_agent_name);
          if (!cancelled) setEntryAgent(detail);
        } else {
          setEntryAgent(null);
        }
      } catch {
        // ignore
      }
    }

    load();
    return () => { cancelled = true; };
  }, [lockedSchemaId, selectedSchema]);

  // Fetch sessions for selected schema's entry agent (production only)
  useEffect(() => {
    if (!schema || !entryAgent || isPrototype) { setSessions([]); return; }
    let cancelled = false;
    api.listSessions({ agent_name: entryAgent.name, per_page: 20 })
      .then((res) => { if (!cancelled) setSessions(res.sessions); })
      .catch(() => {});
    return () => { cancelled = true; };
  }, [schema, entryAgent, isPrototype]);

  // Refresh session list when a new session is created (sessionId changes)
  useEffect(() => {
    if (!schema || !entryAgent || isPrototype || !sseChat.sessionId) return;
    if (sessions?.some((s) => s.session_id === sseChat.sessionId)) return;
    api.listSessions({ agent_name: entryAgent.name, per_page: 20 })
      .then((res) => setSessions(res.sessions))
      .catch(() => {});
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sseChat.sessionId]);

  // Reload local sessions when schema changes
  useEffect(() => {
    if (!schema) { setLocalSessions([]); return; }
    try { setLocalSessions(JSON.parse(localStorage.getItem(`bb_testflow_sessions__${schema.id}`) ?? '[]')); }
    catch { setLocalSessions([]); }
  }, [schema]);

  // Add newly created session to local list
  useEffect(() => {
    if (!sseChat.sessionId || !localSessionsKey || !entryAgent) return;
    setLocalSessions((prev) => {
      if (prev.some((s) => s.session_id === sseChat.sessionId)) return prev;
      const entry: SessionSummary = {
        session_id: sseChat.sessionId,
        entry_agent: entryAgent.name,
        status: 'running',
        duration_ms: 0,
        total_tokens: 0,
        created_at: new Date().toISOString(),
      };
      const updated = [entry, ...prev].slice(0, 20);
      try { localStorage.setItem(localSessionsKey, JSON.stringify(updated)); } catch { /* no-op */ }
      return updated;
    });
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sseChat.sessionId]);

  // Merge API sessions + local sessions (dedup by session_id, newest first)
  const allSessions = useMemo(() => {
    const map = new Map<string, SessionSummary>();
    for (const s of [...sessions, ...localSessions]) {
      if (!map.has(s.session_id)) map.set(s.session_id, s);
    }
    return [...map.values()].sort((a, b) =>
      new Date(b.created_at).getTime() - new Date(a.created_at).getTime(),
    );
  }, [sessions, localSessions]);

  // Close dropdown on outside click
  useEffect(() => {
    if (!sessionDropdownOpen) return;
    function handleClick(e: MouseEvent) {
      if (sessionDropdownRef.current && !sessionDropdownRef.current.contains(e.target as Node)) {
        setSessionDropdownOpen(false);
      }
    }
    document.addEventListener('mousedown', handleClick);
    return () => document.removeEventListener('mousedown', handleClick);
  }, [sessionDropdownOpen]);

  // Auto-scroll
  useEffect(() => {
    messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' });
  }, [messages]);

  function toggleItem(key: string) {
    setExpandedItems((prev) => ({ ...prev, [key]: !prev[key] }));
  }

  // ── Prototype mock send ──────────────────────────────────────────────────

  function protoSend(text: string) {
    if (!text.trim() || protoStreaming) return;

    const userMsg: SSEMessage = { id: crypto.randomUUID(), role: 'user', content: text };
    const assistantId = crypto.randomUUID();
    const sid = protoSessionId || `session-${Date.now()}`;
    if (!protoSessionId) setProtoSessionId(sid);

    setProtoMessages((prev) => [
      ...prev,
      userMsg,
      { id: assistantId, role: 'assistant', content: '', toolCalls: [], streaming: true },
    ]);
    setProtoStreaming(true);

    // Simulate streaming with tool calls
    const toolCalls = MOCK_TOOL_CALLS.map((tc) => ({ ...tc }));
    const responseText = MOCK_RESPONSES[Math.floor(Math.random() * MOCK_RESPONSES.length)]!;

    // Step 1: show tool calls after 500ms
    setTimeout(() => {
      setProtoMessages((prev) =>
        prev.map((m) => m.id === assistantId ? { ...m, toolCalls } : m),
      );
    }, 500);

    // Step 2: stream response text
    let charIndex = 0;
    const interval = setInterval(() => {
      charIndex += 3;
      if (charIndex >= responseText.length) {
        clearInterval(interval);
        setProtoMessages((prev) =>
          prev.map((m) => m.id === assistantId ? { ...m, content: responseText, streaming: false } : m),
        );
        setProtoStreaming(false);
        return;
      }
      setProtoMessages((prev) =>
        prev.map((m) => m.id === assistantId ? { ...m, content: responseText.slice(0, charIndex) } : m),
      );
    }, 30);
  }

  // ── Send message ─────────────────────────────────────────────────────────

  async function handleSend() {
    const text = message.trim();
    if (!text || !schema || isStreaming) return;
    setMessage('');
    if (inputRef.current) inputRef.current.style.height = 'auto';

    if (isPrototype) {
      protoSend(text);
      return;
    }

    await sseChat.sendMessage(text);
  }

  function handleKeyDown(e: React.KeyboardEvent<HTMLTextAreaElement>) {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault();
      handleSend();
    }
  }

  function handleReset() {
    if (isPrototype) {
      setProtoMessages([]);
      setProtoSessionId('');
    } else {
      sseChat.resetSession();
    }
  }

  function handleStop() {
    if (isPrototype) {
      setProtoStreaming(false);
      setProtoMessages((prev) => prev.map((m) => m.streaming ? { ...m, streaming: false } : m));
    } else {
      sseChat.stopStreaming();
    }
  }

  // ── Session management (production only) ─────────────────────────────────

  async function handleSwitchSession(sid: string) {
    setSessionDropdownOpen(false);
    await sseChat.loadSession(sid);
  }

  async function handleDeleteSession(sid: string, e: React.MouseEvent) {
    e.stopPropagation();
    if (!confirm('Delete this session?')) return;
    try {
      await api.deleteSession(sid);
    } catch { /* session may not be in DB — continue with local removal */ }
    setSessions((prev) => prev.filter((s) => s.session_id !== sid));
    setLocalSessions((prev) => {
      const updated = prev.filter((s) => s.session_id !== sid);
      if (localSessionsKey) {
        try { localStorage.setItem(localSessionsKey, JSON.stringify(updated)); } catch { /* no-op */ }
      }
      return updated;
    });
    if (sseChat.sessionId === sid) {
      sseChat.resetSession();
    }
  }

  // ── Render ────────────────────────────────────────────────────────────────

  const lastMsg = messages.length > 0 ? messages[messages.length - 1] : null;
  const hasError = lastMsg?.role === 'assistant' && lastMsg.content?.startsWith('Error:');

  if (!selectedSchema) {
    return (
      <div className="flex items-center justify-center h-full p-6 text-[12px] text-brand-shade3">
        Select a schema in the panel above to test its flow.
      </div>
    );
  }

  if (!schema) {
    return (
      <div className="flex items-center justify-center h-full p-6 text-[12px] text-brand-shade3">
        Loading schema…
      </div>
    );
  }

  return (
    <div className="flex flex-col h-full">
      {/* Config section */}
      <div className="px-3 py-2 space-y-2 border-b border-brand-shade3/10 flex-shrink-0">
        {/* Schema + entry agent (read-only) */}
        <div className="flex items-center gap-2">
          <label className="text-[10px] text-brand-shade3 uppercase tracking-wide shrink-0">Schema:</label>
          <span className="text-xs text-brand-light truncate">{schema.name}</span>
          <span className="text-[10px] text-brand-shade3/60 shrink-0">→</span>
          <label className="text-[10px] text-brand-shade3 uppercase tracking-wide shrink-0">Entry:</label>
          <span className="text-xs text-brand-light truncate">{entryAgent?.name ?? schema.entry_agent_name ?? '—'}</span>
          {!schema.chat_enabled && (
            <span
              className="text-[10px] px-1.5 py-0.5 rounded border border-amber-500/30 text-amber-400 bg-amber-500/10 shrink-0"
              title="Chat is disabled on this schema. Enable it in the schema Settings tab."
            >
              chat disabled
            </span>
          )}
          {messages.length > 0 && (
            <button
              onClick={handleReset}
              className="ml-auto p-1 text-brand-shade3 hover:text-brand-light transition-colors shrink-0"
              title="New Session"
            >
              <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                <polyline points="1 4 1 10 7 10" />
                <path d="M3.51 15a9 9 0 102.13-9.36L1 10" />
              </svg>
            </button>
          )}
        </div>

        {/* Session selector (production only) */}
        {!isPrototype && (
          <div className="flex items-center gap-2">
            <label className="text-[10px] text-brand-shade3 uppercase tracking-wide shrink-0">Session:</label>
            <div ref={sessionDropdownRef} className="relative flex-1">
              <button
                onClick={() => setSessionDropdownOpen((p) => !p)}
                className="w-full px-2 py-1 bg-brand-dark border border-brand-shade3/30 rounded text-xs text-brand-light text-left flex items-center gap-1 hover:border-brand-shade3/50 transition-colors"
              >
                <span className="truncate flex-1">
                  {sseChat.sessionId ? sseChat.sessionId.slice(0, 12) + '...' : 'New Session'}
                </span>
                <svg width="8" height="8" viewBox="0 0 24 24" fill="currentColor" className={`text-brand-shade3 transition-transform ${sessionDropdownOpen ? 'rotate-180' : ''}`}>
                  <path d="M7 10l5 5 5-5H7z" />
                </svg>
              </button>
              {sessionDropdownOpen && (
                <div className="absolute top-full left-0 mt-1 w-full max-h-48 overflow-y-auto bg-brand-dark border border-brand-shade3/20 rounded shadow-lg z-50">
                  <button
                    onClick={() => { handleReset(); setSessionDropdownOpen(false); }}
                    className="w-full px-2 py-1.5 text-left text-xs text-brand-accent hover:bg-brand-shade3/10 flex items-center gap-1.5 transition-colors"
                  >
                    <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                      <line x1="12" y1="5" x2="12" y2="19" /><line x1="5" y1="12" x2="19" y2="12" />
                    </svg>
                    New Session
                  </button>
                  {allSessions.map((s) => (
                    <div
                      key={s.session_id}
                      onClick={() => handleSwitchSession(s.session_id)}
                      className={`w-full px-2 py-1.5 text-left text-xs flex items-center gap-1.5 hover:bg-brand-shade3/10 cursor-pointer transition-colors ${sseChat.sessionId === s.session_id ? 'text-brand-accent' : 'text-brand-light'}`}
                    >
                      <span className="truncate flex-1">
                        {s.session_id.slice(0, 10)}...
                        <span className="text-brand-shade3 ml-1">
                          {new Date(s.created_at).toLocaleString(undefined, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })}
                        </span>
                      </span>
                      <button
                        onClick={(e) => handleDeleteSession(s.session_id, e)}
                        className="p-0.5 text-brand-shade3 hover:text-red-400 transition-colors shrink-0"
                        title="Delete session"
                      >
                        <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                          <polyline points="3 6 5 6 21 6" /><path d="M19 6l-1 14a2 2 0 01-2 2H8a2 2 0 01-2-2L5 6" /><path d="M10 11v6" /><path d="M14 11v6" />
                        </svg>
                      </button>
                    </div>
                  ))}
                  {allSessions.length === 0 && (
                    <div className="px-2 py-1.5 text-[10px] text-brand-shade3">No sessions yet</div>
                  )}
                </div>
              )}
            </div>
          </div>
        )}

        {/* Headers editor */}
        <HeadersEditor headers={headers} onChange={setHeaders} />
      </div>

      {/* Messages area */}
      <div className="flex-1 overflow-y-auto min-h-0 px-3 py-2 space-y-2">
        {!isPrototype && sseChat.isRestoring && messages.length === 0 ? (
          <div className="flex items-center gap-2 text-[11px] text-brand-shade3 font-mono py-4 justify-center">
            <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" className="animate-spin">
              <path d="M21 12a9 9 0 11-6.219-8.56" />
            </svg>
            Restoring session...
          </div>
        ) : messages.length === 0 ? (
          <p className="text-[11px] text-brand-shade3/50 text-center mt-4">
            Send a message to test this schema's entry agent.
          </p>
        ) : null}

        {messages.map((msg) => (
          <div key={msg.id} className={msg.role === 'user' ? 'flex justify-end' : ''}>
            {msg.role === 'user' ? (
              <div className="max-w-[85%] px-2.5 py-1.5 bg-brand-accent/10 border border-brand-accent/20 rounded-lg text-xs text-brand-light font-mono">
                {msg.content}
              </div>
            ) : (
              <div className="space-y-1">
                {/* Error */}
                {hasError && msg.id === lastMsg?.id && (
                  <div className="px-2 py-1.5 bg-red-900/20 border border-red-500/20 rounded text-[11px] text-red-400">
                    {msg.content.replace(/^Error:\s*/, '')}
                  </div>
                )}

                {/* Render segments in chronological order (tool_calls interleaved with text) */}
                {msg.segments && msg.segments.length > 0 ? (
                  msg.segments.map((seg, i) => {
                    if (seg.type === 'text') {
                      const text = seg.content.replace(/\[thinking\][\s\S]*?\[\/thinking\]/g, '').trim();
                      if (!text && !msg.streaming) return null;
                      return (
                        <div key={i} className="text-xs text-brand-light leading-relaxed whitespace-pre-wrap">
                          {text}
                          {msg.streaming && i === msg.segments!.length - 1 && (
                            <span className="inline-block w-1.5 h-3 bg-brand-accent ml-0.5 animate-pulse" />
                          )}
                        </div>
                      );
                    }
                    if (seg.type === 'widget') {
                      return (
                        <div key={i} className="text-xs">
                          <InterruptWidget
                            interruptId={seg.widget.interruptId}
                            schema={seg.widget.schema}
                            state={seg.widget.state}
                            answers={seg.widget.answers}
                            onSubmit={(id, answers) => {
                              void sseChat.sendInterruptResume(id, answers);
                            }}
                          />
                        </div>
                      );
                    }
                    const tc = seg.toolCall;
                    const key = `${msg.id}-tc-${i}`;
                    const isExpanded = expandedItems[key] ?? false;
                    return (
                      <button
                        key={i}
                        onClick={() => toggleItem(key)}
                        className="w-full text-left px-2 py-1 bg-brand-dark border border-brand-shade3/15 rounded text-[11px] font-mono hover:border-brand-shade3/30 transition-colors"
                      >
                        <div className="flex items-center gap-1.5">
                          <span className="text-blue-400 font-medium">{tc.tool}</span>
                          {tc.output !== undefined && (
                            <span className="text-emerald-400/60 ml-1">done</span>
                          )}
                          <svg
                            width="8" height="8" viewBox="0 0 24 24" fill="currentColor"
                            className={`text-brand-shade3 transition-transform ml-auto ${isExpanded ? 'rotate-90' : ''}`}
                          >
                            <path d="M8 5l10 7-10 7V5z" />
                          </svg>
                        </div>
                        {isExpanded && (
                          <div className="mt-1 space-y-1 text-[10px]">
                            {tc.input && (
                              <div className="text-brand-shade3 whitespace-pre-wrap break-all">
                                <span className="text-brand-shade3/60">Input: </span>{tc.input}
                              </div>
                            )}
                            {tc.output !== undefined && (
                              <div className="text-emerald-400/80 whitespace-pre-wrap break-all">
                                <span className="text-emerald-400/50">Output: </span>{tc.output}
                              </div>
                            )}
                          </div>
                        )}
                      </button>
                    );
                  })
                ) : (
                  <>
                    {/* Fallback for history messages without segments */}
                    {msg.content && !msg.content.startsWith('Error:') && (
                      <div className="text-xs text-brand-light leading-relaxed whitespace-pre-wrap">
                        {msg.content.replace(/\[thinking\][\s\S]*?\[\/thinking\]/g, '').trim()}
                        {msg.streaming && (
                          <span className="inline-block w-1.5 h-3 bg-brand-accent ml-0.5 animate-pulse" />
                        )}
                      </div>
                    )}
                    {msg.toolCalls && msg.toolCalls.length > 0 && (
                      <div className="space-y-1">
                        {msg.toolCalls.map((tc, i) => {
                          const key = `${msg.id}-tc-${i}`;
                          const isExpanded = expandedItems[key] ?? false;
                          return (
                            <button
                              key={i}
                              onClick={() => toggleItem(key)}
                              className="w-full text-left px-2 py-1 bg-brand-dark border border-brand-shade3/15 rounded text-[11px] font-mono hover:border-brand-shade3/30 transition-colors"
                            >
                              <div className="flex items-center gap-1.5">
                                <span className="text-blue-400 font-medium">{tc.tool}</span>
                                {tc.output !== undefined && (
                                  <span className="text-emerald-400/60 ml-1">done</span>
                                )}
                                <svg
                                  width="8" height="8" viewBox="0 0 24 24" fill="currentColor"
                                  className={`text-brand-shade3 transition-transform ml-auto ${isExpanded ? 'rotate-90' : ''}`}
                                >
                                  <path d="M8 5l10 7-10 7V5z" />
                                </svg>
                              </div>
                              {isExpanded && (
                                <div className="mt-1 space-y-1 text-[10px]">
                                  {tc.input && (
                                    <div className="text-brand-shade3 whitespace-pre-wrap break-all">
                                      <span className="text-brand-shade3/60">Input: </span>{tc.input}
                                    </div>
                                  )}
                                  {tc.output !== undefined && (
                                    <div className="text-emerald-400/80 whitespace-pre-wrap break-all">
                                      <span className="text-emerald-400/50">Output: </span>{tc.output}
                                    </div>
                                  )}
                                </div>
                              )}
                            </button>
                          );
                        })}
                      </div>
                    )}
                  </>
                )}

              </div>
            )}
          </div>
        ))}

        {/* Brewing spinner — mirrors AI Assistant logic */}
        {isStreaming && (() => {
          const lastMsg = messages[messages.length - 1];
          if (!lastMsg || lastMsg.role !== 'assistant') return null;
          if (lastMsg.content === '' || (lastMsg.toolCalls && lastMsg.toolCalls.length > 0 && lastMsg.streaming)) {
            return (
              <div className="flex justify-start">
                <BrewingSpinner />
              </div>
            );
          }
          return null;
        })()}

        <div ref={messagesEndRef} />
      </div>

      {/* Context usage bar */}
      <ContextUsageBar maxContextTokens={entryAgent?.max_context_size ?? null} totalTokens={isPrototype ? null : sseChat.tokenUsage} contextTokens={isPrototype ? null : sseChat.contextTokens} />

      {/* Input area */}
      <div className="flex items-center gap-2 px-3 py-2 border-t border-brand-shade3/10 flex-shrink-0">
        <textarea
          ref={inputRef}
          value={message}
          onChange={(e) => {
            setMessage(e.target.value);
            e.target.style.height = 'auto';
            e.target.style.height = Math.min(e.target.scrollHeight, 120) + 'px';
          }}
          onKeyDown={handleKeyDown}
          placeholder="Send test message to entry agent..."
          rows={1}
          className="flex-1 px-2.5 py-1.5 bg-brand-dark-alt border border-brand-shade3/20 rounded-card text-xs text-brand-light placeholder-brand-shade3 font-mono focus:outline-none focus:border-brand-accent resize-none transition-colors"
          style={{ maxHeight: '120px', overflowY: 'auto' }}
        />
        {isStreaming ? (
          <button
            onClick={handleStop}
            className="px-2.5 py-1.5 bg-brand-dark border border-brand-shade3/30 rounded-card text-xs text-brand-shade2 hover:text-brand-light hover:border-brand-shade3 transition-colors flex-shrink-0 inline-flex items-center gap-1"
          >
            <svg width="10" height="10" viewBox="0 0 24 24" fill="currentColor">
              <rect x="4" y="4" width="16" height="16" rx="2" />
            </svg>
            Stop
          </button>
        ) : (
          <button
            onClick={handleSend}
            disabled={!message.trim() || !schema}
            className="px-2.5 py-1.5 bg-brand-accent text-brand-light rounded-card text-xs font-medium hover:bg-brand-accent-hover disabled:opacity-40 transition-colors flex-shrink-0 inline-flex items-center gap-1"
          >
            <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <polygon points="5 3 19 12 5 21 5 3" />
            </svg>
            Run
          </button>
        )}
      </div>
    </div>
  );
}
