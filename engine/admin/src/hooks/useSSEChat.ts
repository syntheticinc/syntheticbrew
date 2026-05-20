import { useState, useRef, useCallback, useEffect } from 'react';
import { parseSSELine, type ToolCall } from '../lib/sse';
import type {
  EventResponse,
  InterruptAnswer,
  InterruptRequestPayload,
  InterruptResumePayload,
  InterruptSchema,
} from '../types';

// ─── Types ──────────────────────────────────────────────────────────────────

export interface WidgetSegmentData {
  interruptId: string;
  schema: InterruptSchema;
  state: 'pending' | 'answered';
  answers?: InterruptAnswer[];
}

export type MessageSegment =
  | { type: 'text'; content: string }
  | { type: 'tool_call'; toolCall: ToolCall }
  | { type: 'widget'; widget: WidgetSegmentData };

export interface SSEMessage {
  id: string;
  role: 'user' | 'assistant';
  content: string;
  toolCalls?: ToolCall[];
  segments?: MessageSegment[];
  streaming?: boolean;
}

export interface UseSSEChatConfig {
  /**
   * Explicit endpoint override. If not set, the hook falls back to
   * `/api/v1/schemas/{schemaName}/chat` using the `schemaName` field below.
   * Callers that target a different endpoint (e.g. the builder assistant's
   * `/api/v1/admin/assistant/chat`) pass the URL directly here.
   */
  endpoint?: string;
  /**
   * Target schema for the chat request. Used to build the default endpoint
   * when `endpoint` is not supplied. May be empty when `endpoint` is set.
   */
  schemaName?: string;
  schemaContext?: string;
  getHeaders?: () => Record<string, string>;
  onToolResult?: (tool: string, output: string) => void;
  /** When set, sessionId is persisted to localStorage under this key. */
  persistenceKey?: string;
  /** Injected fetch function for session event restore (keeps hook api-import-free). */
  fetchMessages?: (sessionId: string) => Promise<EventResponse[]>;
  /** When provided, called on mount instead of reading from localStorage.
   *  Return null to start a fresh session. Mutually exclusive with persistenceKey. */
  resolveSessionId?: () => Promise<string | null>;
}

export interface UseSSEChatReturn {
  messages: SSEMessage[];
  sendMessage: (text: string) => Promise<void>;
  /** Submit a HITL widget answer (engine 1.2.0+). No-op while streaming. */
  sendInterruptResume: (interruptId: string, answers: InterruptAnswer[]) => Promise<void>;
  isStreaming: boolean;
  isRestoring: boolean;
  error: string | null;
  sessionId: string;
  tokenUsage: number | null;
  contextTokens: number | null;
  resetSession: () => void;
  stopStreaming: () => void;
  loadSession: (sessionId: string) => Promise<void>;
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

/** Strip <think>...</think> blocks from streamed LLM output. */
function stripThinkTags(raw: string): string {
  let cleaned = raw.replace(/<think>[\s\S]*?<\/think>/g, '');
  cleaned = cleaned.replace(/<think>[\s\S]*$/, '');
  return cleaned.replace(/^\s+/, '');
}

/** Safe localStorage get — returns null on SecurityError (Safari ITP / iframe). */
function safeGetItem(key: string): string | null {
  try { return localStorage.getItem(key); } catch { return null; }
}

/** Safe localStorage set — no-op on SecurityError. */
function safeSetItem(key: string, value: string): void {
  try { localStorage.setItem(key, value); } catch { /* no-op */ }
}

/** Safe localStorage remove — no-op on SecurityError. */
function safeRemoveItem(key: string): void {
  try { localStorage.removeItem(key); } catch { /* no-op */ }
}

// ─── Events → SSEMessages mapper ─────────────────────────────────────────────

/** Convert EventResponse[] from backend into SSEMessage[] for rendering.
 *  Groups consecutive assistant events + tool calls into one SSEMessage,
 *  preserving chronological order of text <-> tool interleaving via segments. */
function mapEventsToMessages(events: EventResponse[]): SSEMessage[] {
  const messages: SSEMessage[] = [];
  let currentAssistant: SSEMessage | null = null;
  let lastSegmentIsText = false;

  const flushAssistant = () => {
    if (currentAssistant) {
      messages.push(currentAssistant);
      currentAssistant = null;
      lastSegmentIsText = false;
    }
  };

  const ensureAssistant = (id: string) => {
    if (!currentAssistant) {
      currentAssistant = { id, role: 'assistant', content: '', toolCalls: [], segments: [], streaming: false };
      lastSegmentIsText = false;
    }
    return currentAssistant;
  };

  for (const ev of events) {
    const payload = ev.payload ?? {};
    switch (ev.event_type) {
      case 'user_message':
        flushAssistant();
        messages.push({
          id: ev.id,
          role: 'user',
          content: (payload.content as string) ?? '',
          streaming: false,
        });
        break;

      case 'assistant_message': {
        const a = ensureAssistant(ev.id);
        const delta = (payload.content as string) ?? '';
        a.content += delta;
        const segs = a.segments ?? [];
        if (lastSegmentIsText && segs.length > 0) {
          const last = segs[segs.length - 1]!;
          if (last.type === 'text') {
            a.segments = [...segs.slice(0, -1), { type: 'text', content: last.content + delta }];
          } else {
            a.segments = [...segs, { type: 'text', content: delta }];
          }
        } else {
          a.segments = [...segs, { type: 'text', content: delta }];
          lastSegmentIsText = true;
        }
        break;
      }

      case 'tool_call': {
        const toolName = (payload.tool as string) ?? '';
        // HITL widget surfaces via interrupt_request — skip raw tool_call.
        if (toolName === 'show_structured_output') break;
        const a = ensureAssistant(ev.id);
        const args = payload.arguments as Record<string, unknown> | undefined;
        const tc: ToolCall = {
          tool: toolName,
          input: args ? JSON.stringify(args) : '',
        };
        a.toolCalls = [...(a.toolCalls ?? []), tc];
        a.segments = [...(a.segments ?? []), { type: 'tool_call', toolCall: tc }];
        lastSegmentIsText = false;
        break;
      }

      case 'tool_result': {
        const a = currentAssistant as SSEMessage | null;
        if (!a) break;
        const toolName = (payload.tool as string) ?? '';
        const output = (payload.content as string) ?? '';
        // Match against the most-recent still-open tool call (by name).
        const tcs: ToolCall[] = a.toolCalls ?? [];
        let matched = false;
        const updatedTcs: ToolCall[] = tcs.slice();
        for (let i = updatedTcs.length - 1; i >= 0; i--) {
          const existing = updatedTcs[i]!;
          if (existing.tool === toolName && existing.output === undefined) {
            updatedTcs[i] = { ...existing, output };
            matched = true;
            break;
          }
        }
        a.toolCalls = updatedTcs;
        if (matched && a.segments) {
          const segs: MessageSegment[] = a.segments.slice();
          for (let i = segs.length - 1; i >= 0; i--) {
            const s = segs[i]!;
            if (s.type === 'tool_call' && s.toolCall.tool === toolName && s.toolCall.output === undefined) {
              segs[i] = { type: 'tool_call', toolCall: { ...s.toolCall, output } };
              break;
            }
          }
          a.segments = segs;
        }
        break;
      }

      case 'reasoning':
        // Reasoning events are informational — skip for now in chat history
        break;

      case 'system':
        flushAssistant();
        messages.push({
          id: ev.id,
          role: 'assistant',
          content: (payload.content as string) ?? '',
          streaming: false,
        });
        break;

      case 'interrupt_request': {
        const wp = parseInterruptRequestPayload(payload);
        if (!wp) break;
        const a = ensureAssistant(ev.id);
        a.segments = [
          ...(a.segments ?? []),
          {
            type: 'widget',
            widget: { interruptId: wp.interrupt_id, schema: wp.schema, state: 'pending' },
          },
        ];
        lastSegmentIsText = false;
        break;
      }

      case 'interrupt_resume': {
        const rp = parseInterruptResumePayload(payload);
        if (!rp) break;
        markWidgetAnswered(messages, currentAssistant, rp.interrupt_id, rp.payload.answers);
        break;
      }
    }
  }

  flushAssistant();
  return messages;
}

/** Parse the `content` string carried on interrupt_request SSE events. */
function parseInterruptRequestPayload(payload: Record<string, unknown>): InterruptRequestPayload | null {
  const raw = (payload.content as string) ?? '';
  if (!raw) return null;
  try {
    return JSON.parse(raw) as InterruptRequestPayload;
  } catch {
    return null;
  }
}

/** Parse the `content` string carried on interrupt_resume SSE events. */
function parseInterruptResumePayload(payload: Record<string, unknown>): InterruptResumePayload | null {
  const raw = (payload.content as string) ?? '';
  if (!raw) return null;
  try {
    return JSON.parse(raw) as InterruptResumePayload;
  } catch {
    return null;
  }
}

/** Flip a widget segment to answered. Used by history restore and live SSE. */
function markWidgetAnswered(
  messages: SSEMessage[],
  current: SSEMessage | null,
  interruptId: string,
  answers: InterruptAnswer[] | undefined,
): void {
  const apply = (msg: SSEMessage): boolean => {
    if (!msg.segments) return false;
    let changed = false;
    msg.segments = msg.segments.map((seg) => {
      if (seg.type === 'widget' && seg.widget.interruptId === interruptId) {
        changed = true;
        return {
          type: 'widget',
          widget: { ...seg.widget, state: 'answered', answers: answers ?? [] },
        };
      }
      return seg;
    });
    return changed;
  };
  for (let i = messages.length - 1; i >= 0; i--) {
    if (apply(messages[i]!)) return;
  }
  if (current) apply(current);
}

// ─── Hook ────────────────────────────────────────────────────────────────────

export function useSSEChat(config: UseSSEChatConfig): UseSSEChatReturn {
  const { endpoint, schemaName, schemaContext, getHeaders, onToolResult, persistenceKey, fetchMessages, resolveSessionId } = config;

  const [messages, setMessages] = useState<SSEMessage[]>([]);
  const [isStreaming, setIsStreaming] = useState(false);
  const [isRestoring, setIsRestoring] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [sessionId, setSessionId] = useState(() =>
    persistenceKey ? (safeGetItem(persistenceKey) ?? '') : '',
  );
  const [tokenUsage, setTokenUsage] = useState<number | null>(null);
  const [contextTokens, setContextTokens] = useState<number | null>(() => {
    if (!persistenceKey) return null;
    const stored = safeGetItem(persistenceKey + '_ctx');
    return stored ? Number(stored) : null;
  });

  const sessionIdRef = useRef(sessionId);
  const abortRef = useRef<AbortController | null>(null);
  const restoreAbortRef = useRef<AbortController | null>(null);

  // ── Restore session from backend on mount and persistenceKey change ──────
  useEffect(() => {
    const hasResolve = !!resolveSessionId;
    const hasPersistence = !!persistenceKey && !!fetchMessages;
    if (!hasResolve && !hasPersistence) return;
    if (!fetchMessages) return;

    // Abort any active SSE stream on key change
    abortRef.current?.abort();
    setIsStreaming(false);

    // Abort any previous restore fetch
    restoreAbortRef.current?.abort();

    const controller = new AbortController();
    restoreAbortRef.current = controller;

    const doRestore = async () => {
      let sid: string | null = null;

      if (hasResolve) {
        try {
          sid = await resolveSessionId!();
        } catch {
          sid = null;
        }
      } else {
        sid = safeGetItem(persistenceKey!) ?? null;
      }

      if (controller.signal.aborted) return;

      if (!sid) {
        sessionIdRef.current = '';
        setSessionId('');
        setMessages([]);
        return;
      }

      sessionIdRef.current = sid;
      setSessionId(sid);
      if (persistenceKey) safeSetItem(persistenceKey, sid);

      setIsRestoring(true);
      try {
        const raw = await fetchMessages(sid);
        if (controller.signal.aborted) return;
        const restored = mapEventsToMessages(raw);
        setMessages(restored);
        // Do NOT compute contextTokens from event content — that undercounts
        // by 3-4x because it excludes the system prompt, which is what fills
        // most of the context window for AI builder. Leave contextTokens
        // null so ContextUsageBar falls back to baselineTokens (system prompt
        // estimate). The real value arrives via the next SSE `done` event.
      } catch (err) {
        if (controller.signal.aborted) return;
        if ((err as Error).name !== 'AbortError') {
          if (persistenceKey) safeRemoveItem(persistenceKey);
          sessionIdRef.current = '';
          setSessionId('');
          setMessages([]);
        }
      } finally {
        if (!controller.signal.aborted) setIsRestoring(false);
      }
    };

    doRestore();
    return () => { controller.abort(); };
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [persistenceKey, resolveSessionId]);

  const resetSession = useCallback(() => {
    sessionIdRef.current = '';
    setSessionId('');
    setMessages([]);
    abortRef.current?.abort();
    restoreAbortRef.current?.abort();
    setError(null);
    setTokenUsage(null);
    setContextTokens(null);
    if (persistenceKey) {
      safeRemoveItem(persistenceKey);
      safeRemoveItem(persistenceKey + '_ctx');
    }
  }, [persistenceKey]);

  const stopStreaming = useCallback(() => {
    abortRef.current?.abort();
    setIsStreaming(false);
    setMessages((prev) =>
      prev.map((m) => (m.streaming ? { ...m, streaming: false } : m)),
    );
  }, []);

  // Shared dispatcher for sendMessage / sendInterruptResume.
  type Submission =
    | { kind: 'message'; text: string }
    | { kind: 'resume'; interruptId: string; answers: InterruptAnswer[] };

  const internalSubmit = useCallback(async (submission: Submission) => {
    if (isStreaming) return;
    if (submission.kind === 'message' && !submission.text.trim()) return;

    setIsStreaming(true);
    setError(null);
    abortRef.current = new AbortController();

    // Plain message → push user bubble. Resume → widget shows the answer.
    if (submission.kind === 'message') {
      const userMsg: SSEMessage = {
        id: crypto.randomUUID(),
        role: 'user',
        content: submission.text,
      };
      const assistantMsgId = crypto.randomUUID();
      const assistantMsg: SSEMessage = {
        id: assistantMsgId,
        role: 'assistant',
        content: '',
        toolCalls: [],
        streaming: true,
      };
      setMessages((prev) => [...prev, userMsg, assistantMsg]);
      await runSubmitStream(assistantMsgId, submission);
      return;
    }

    const assistantMsgId = crypto.randomUUID();
    const assistantMsg: SSEMessage = {
      id: assistantMsgId,
      role: 'assistant',
      content: '',
      toolCalls: [],
      streaming: true,
    };
    setMessages((prev) => [...prev, assistantMsg]);
    await runSubmitStream(assistantMsgId, submission);
  }, [isStreaming, endpoint, schemaName, getHeaders, persistenceKey]);

  // Shared SSE consumption: configure throttled patches against the assistant
  // bubble we just inserted, POST the chat endpoint with the per-submission
  // body shape, and process the streamed events.
  const runSubmitStream = useCallback(async (assistantMsgId: string, submission: Submission) => {

    // Throttled state updates: accumulate patches, flush at most every 250ms.
    // This prevents re-rendering the entire message list + markdown on every SSE chunk.
    const THROTTLE_MS = 250;
    let pendingPatch: Partial<SSEMessage> | null = null;
    let throttleTimer: ReturnType<typeof setTimeout> | null = null;

    const flushUpdate = () => {
      throttleTimer = null;
      if (pendingPatch) {
        const patch = pendingPatch;
        pendingPatch = null;
        setMessages((prev) =>
          prev.map((m) => (m.id === assistantMsgId ? { ...m, ...patch } : m)),
        );
      }
    };

    const updateAssistant = (patch: Partial<SSEMessage>) => {
      pendingPatch = pendingPatch ? { ...pendingPatch, ...patch } : patch;
      if (throttleTimer === null) {
        throttleTimer = setTimeout(flushUpdate, THROTTLE_MS);
      }
    };

    // Immediate update (bypass throttle — for done/error/tool events)
    const updateAssistantNow = (patch: Partial<SSEMessage>) => {
      if (throttleTimer !== null) { clearTimeout(throttleTimer); throttleTimer = null; }
      pendingPatch = pendingPatch ? { ...pendingPatch, ...patch } : patch;
      flushUpdate();
    };

    try {
      const token = localStorage.getItem('jwt');
      const baseHeaders: Record<string, string> = {
        'Content-Type': 'application/json',
        ...(token ? { Authorization: `Bearer ${token}` } : {}),
      };
      const extraHeaders = getHeaders ? getHeaders() : {};
      const allHeaders = { ...baseHeaders, ...extraHeaders };

      const url = endpoint || (schemaName ? `/api/v1/schemas/${encodeURIComponent(schemaName)}/chat` : '');
      if (!url) {
        updateAssistantNow({ content: 'Error: chat endpoint not configured', streaming: false });
        setError('chat endpoint not configured');
        return;
      }
      const body: Record<string, unknown> = {
        session_id: sessionIdRef.current || undefined,
        ...(schemaContext ? { schema_context: schemaContext } : {}),
      };
      if (submission.kind === 'message') {
        body.message = submission.text;
      } else {
        body.resume_interrupt = {
          interrupt_id: submission.interruptId,
          payload: { answers: submission.answers },
        };
      }
      const res = await fetch(url, {
        method: 'POST',
        headers: allHeaders,
        body: JSON.stringify(body),
        signal: abortRef.current!.signal,
      });

      if (!res.ok || !res.body) {
        const errText = await res.text().catch(() => 'Request failed');
        sessionIdRef.current = '';
        setSessionId('');
        updateAssistantNow({ content: `Error: ${errText}`, streaming: false });
        setError(errText);
        return;
      }

      const reader = res.body.getReader();
      const decoder = new TextDecoder();
      let buffer = '';
      let currentEvent = '';
      let currentContent = '';
      let currentToolCalls: ToolCall[] = [];
      let currentSegments: MessageSegment[] = [];
      // Track whether last segment was text (for interleaving)
      let lastSegmentIsText = false;

      while (true) {
        const { done, value } = await reader.read();
        if (done) break;

        buffer += decoder.decode(value, { stream: true });
        const lines = buffer.split('\n');
        buffer = lines.pop() ?? '';

        for (const line of lines) {
          const { event, data } = parseSSELine(line);
          if (event !== undefined) {
            currentEvent = event;
            continue;
          }
          if (data === undefined) continue;

          let parsed: Record<string, unknown> = {};
          try {
            parsed = JSON.parse(data) as Record<string, unknown>;
          } catch {
            continue;
          }

          switch (currentEvent) {
            case 'message_delta': {
              const delta = (parsed.content as string) ?? '';
              currentContent += delta;
              // Append to last text segment or create new one
              if (lastSegmentIsText && currentSegments.length > 0) {
                const last = currentSegments[currentSegments.length - 1]!;
                if (last.type === 'text') {
                  currentSegments = [...currentSegments.slice(0, -1), { type: 'text' as const, content: last.content + delta }];
                }
              } else {
                currentSegments = [...currentSegments, { type: 'text', content: delta }];
                lastSegmentIsText = true;
              }
              updateAssistant({ content: stripThinkTags(currentContent), segments: currentSegments });
              break;
            }
            case 'message': {
              const full = (parsed.content as string) ?? '';
              if (full) currentContent = full;
              // Full message replaces — rebuild segments as single text
              if (full) {
                currentSegments = [{ type: 'text', content: full }];
                lastSegmentIsText = true;
              }
              updateAssistant({ content: stripThinkTags(currentContent), segments: currentSegments });
              break;
            }
            case 'tool_call': {
              const tc: ToolCall = {
                tool: (parsed.tool as string) ?? '',
                input: (parsed.content as string) ?? '',
              };
              currentToolCalls = [...currentToolCalls, tc];
              currentSegments = [...currentSegments, { type: 'tool_call', toolCall: tc }];
              lastSegmentIsText = false;
              updateAssistantNow({ toolCalls: currentToolCalls, segments: currentSegments });
              break;
            }
            case 'tool_result': {
              const toolName = (parsed.tool as string) ?? '';
              const output = (parsed.content as string) ?? '';
              currentToolCalls = currentToolCalls.map((tc, idx) =>
                idx === currentToolCalls.length - 1 && tc.tool === toolName
                  ? { ...tc, output }
                  : tc,
              );
              // Update matching tool_call segment
              currentSegments = currentSegments.map((seg) =>
                seg.type === 'tool_call' && seg.toolCall.tool === toolName && seg.toolCall.output === undefined
                  ? { ...seg, toolCall: { ...seg.toolCall, output } }
                  : seg,
              );
              updateAssistantNow({ toolCalls: currentToolCalls, segments: currentSegments });
              onToolResult?.(toolName, output);
              break;
            }
            case 'done': {
              const sid = parsed.session_id as string;
              if (sid) {
                sessionIdRef.current = sid;
                setSessionId(sid);
                if (persistenceKey) safeSetItem(persistenceKey, sid);
              }
              const tokens = parsed.total_tokens as number | undefined;
              if (tokens && tokens > 0) {
                setTokenUsage(tokens);
              }
              const ctxTokens = parsed.context_tokens as number | undefined;
              if (ctxTokens && ctxTokens > 0) {
                setContextTokens(ctxTokens);
                if (persistenceKey) safeSetItem(persistenceKey + '_ctx', String(ctxTokens));
              }
              updateAssistantNow({ streaming: false });
              break;
            }
            case 'error': {
              const errContent = (parsed.content as string) || (parsed.message as string) || 'Unknown error';
              sessionIdRef.current = '';
              setSessionId('');
              updateAssistantNow({ content: `Error: ${errContent}`, streaming: false });
              setError(errContent);
              break;
            }
            case 'interrupt_request': {
              const wp = parseInterruptRequestPayload(parsed);
              if (!wp) break;
              currentSegments = [
                ...currentSegments,
                {
                  type: 'widget',
                  widget: { interruptId: wp.interrupt_id, schema: wp.schema, state: 'pending' },
                },
              ];
              lastSegmentIsText = false;
              updateAssistantNow({ segments: currentSegments });
              break;
            }
            case 'interrupt_resume': {
              const rp = parseInterruptResumePayload(parsed);
              if (!rp) break;
              // The matching widget lives in a PREVIOUS assistant message (the
              // turn that emitted the interrupt_request). Update it directly
              // in setMessages — currentSegments belongs to the new assistant
              // turn we just started and never holds the pending widget.
              setMessages((prev) =>
                prev.map((m) => {
                  if (!m.segments) return m;
                  let changed = false;
                  const segs = m.segments.map((seg) => {
                    if (seg.type === 'widget' && seg.widget.interruptId === rp.interrupt_id) {
                      changed = true;
                      return {
                        type: 'widget' as const,
                        widget: { ...seg.widget, state: 'answered' as const, answers: rp.payload.answers },
                      };
                    }
                    return seg;
                  });
                  return changed ? { ...m, segments: segs } : m;
                }),
              );
              break;
            }
          }
          currentEvent = '';
        }
      }

      updateAssistantNow({ streaming: false });
    } catch (err) {
      if ((err as Error).name !== 'AbortError') {
        sessionIdRef.current = '';
        setSessionId('');
        updateAssistantNow({ content: 'Connection error', streaming: false });
        setError('Connection error');
      }
    } finally {
      setIsStreaming(false);
    }
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [endpoint, schemaName, schemaContext, getHeaders, persistenceKey]);

  const sendMessage = useCallback(
    async (text: string) => internalSubmit({ kind: 'message', text }),
    [internalSubmit],
  );

  const sendInterruptResume = useCallback(
    async (interruptId: string, answers: InterruptAnswer[]) =>
      internalSubmit({ kind: 'resume', interruptId, answers }),
    [internalSubmit],
  );

  const loadSession = useCallback(async (targetSessionId: string) => {
    abortRef.current?.abort();
    setIsStreaming(false);
    restoreAbortRef.current?.abort();

    sessionIdRef.current = targetSessionId;
    setSessionId(targetSessionId);
    if (persistenceKey) safeSetItem(persistenceKey, targetSessionId);

    if (!fetchMessages) {
      setMessages([]);
      return;
    }

    const controller = new AbortController();
    restoreAbortRef.current = controller;

    setIsRestoring(true);
    setMessages([]);

    try {
      const raw = await fetchMessages(targetSessionId);
      if (controller.signal.aborted) return;
      setMessages(mapEventsToMessages(raw));
    } catch (err) {
      if (controller.signal.aborted) return;
      if ((err as Error).name !== 'AbortError') {
        if (persistenceKey) safeRemoveItem(persistenceKey);
        sessionIdRef.current = '';
        setSessionId('');
        setMessages([]);
      }
    } finally {
      if (!controller.signal.aborted) setIsRestoring(false);
    }
  }, [persistenceKey, fetchMessages]);

  return { messages, sendMessage, sendInterruptResume, isStreaming, isRestoring, error, sessionId, tokenUsage, contextTokens, resetSession, stopStreaming, loadSession };
}
