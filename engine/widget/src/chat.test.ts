import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { ChatClient, endUserHttpMessage } from './chat';

// Guards the end-user error surface: a non-2xx server body (which may carry
// server-side operator or error detail) must never leak into the chat bubble —
// only a mapped, safe message is shown. See chat.ts dispatch() non-ok branch.
describe('endUserHttpMessage', () => {
  it('maps limit / rate-limit statuses to a usage-limit message', () => {
    expect(endUserHttpMessage(402)).toMatch(/usage limit/i);
    expect(endUserHttpMessage(429)).toMatch(/usage limit/i);
  });

  it('maps auth / permission statuses to a not-available message', () => {
    expect(endUserHttpMessage(401)).toMatch(/not available/i);
    expect(endUserHttpMessage(403)).toMatch(/not available/i);
  });

  it('maps server errors to a temporarily-unavailable message', () => {
    expect(endUserHttpMessage(500)).toMatch(/temporarily unavailable/i);
    expect(endUserHttpMessage(503)).toMatch(/temporarily unavailable/i);
  });

  it('never leaks the raw status code or JSON body markers into the message', () => {
    for (const status of [400, 401, 402, 403, 404, 429, 500, 503]) {
      const msg = endUserHttpMessage(status);
      expect(msg).not.toContain(String(status));
      expect(msg).not.toMatch(/[{}]|Server error/);
    }
  });
});

// Wiring guard: the dispatch() non-ok branch must route the raw body to the
// operator console channel and surface ONLY the mapped message. Reverting
// dispatch() to onError(`Server error ${status}: ${body}`) must fail this.
describe('ChatClient — a non-2xx response never leaks the raw body to the user', () => {
  const realFetch = globalThis.fetch;
  let warnSpy: ReturnType<typeof vi.spyOn>;

  beforeEach(() => {
    warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {});
  });

  afterEach(() => {
    warnSpy.mockRestore();
    globalThis.fetch = realFetch;
  });

  function baseCallbacks() {
    return {
      onDelta: () => {},
      onToolCallStart: () => {},
      onToolCallResult: () => {},
      onInterruptRequest: () => {},
      onInterruptResume: () => {},
      onDone: () => {},
      onError: () => {},
    };
  }

  it('shows the mapped message and logs the raw body for operators', async () => {
    const rawBody = '{"error":"internal 402 over-limit detail"}';
    globalThis.fetch = vi.fn(async () => ({
      ok: false,
      status: 402,
      text: async () => rawBody,
    })) as unknown as typeof fetch;

    const client = new ChatClient({ schemaName: 's', endpoint: 'http://engine.test', apiKey: null });
    let surfaced = '';
    await client.send('hello', { ...baseCallbacks(), onError: (e) => { surfaced = e; } });

    // End user sees only the mapped, safe copy — not the raw body or status.
    expect(surfaced).toBe(endUserHttpMessage(402));
    expect(surfaced).not.toContain('internal');
    expect(surfaced).not.toContain('402');
    // The raw body is confined to the operator console channel.
    const logged = warnSpy.mock.calls.map((c) => String(c[0])).join(' ');
    expect(logged).toContain('internal 402 over-limit detail');
  });
});
