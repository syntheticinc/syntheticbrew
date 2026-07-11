import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { ChatClient, endUserHttpMessage, type ChatCallbacks } from './chat';

function baseCallbacks(): ChatCallbacks {
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

/** In-memory localStorage stub for the node test environment. */
function makeLocalStorage(initial: Record<string, string> = {}) {
  const store = new Map(Object.entries(initial));
  return {
    getItem: (key: string) => store.get(key) ?? null,
    setItem: (key: string, value: string) => {
      store.set(key, String(value));
    },
    removeItem: (key: string) => {
      store.delete(key);
    },
    clear: () => store.clear(),
  };
}

/** Replace global fetch with a recorder that fails the request (non-ok) —
 *  enough to capture the outgoing request without mocking an SSE stream. */
function captureFetch(): { url: string; init: RequestInit }[] {
  const calls: { url: string; init: RequestInit }[] = [];
  globalThis.fetch = vi.fn(async (url: RequestInfo | URL, init?: RequestInit) => {
    calls.push({ url: String(url), init: init ?? {} });
    return { ok: false, status: 500, text: async () => '' } as unknown as Response;
  }) as unknown as typeof fetch;
  return calls;
}

const VISITOR_ID_PATTERN = /^[A-Za-z0-9._-]{1,64}$/;
const baseConfig = { schemaName: 's', endpoint: 'http://engine.test', apiKey: null };

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

// Visitor id feeds the engine's distinct-active-users metric: one id per
// origin (NOT per schema), persisted across page loads, conforming to the
// server-side charset/length contract.
describe('ChatClient — persistent visitor id', () => {
  const realFetch = globalThis.fetch;

  afterEach(() => {
    globalThis.fetch = realFetch;
    vi.unstubAllGlobals();
  });

  it('generates a conforming id once and persists it under bb_widget_visitor', () => {
    const ls = makeLocalStorage();
    vi.stubGlobal('localStorage', ls);

    new ChatClient(baseConfig);

    const stored = ls.getItem('bb_widget_visitor');
    expect(stored).not.toBeNull();
    expect(stored).toMatch(VISITOR_ID_PATTERN);
  });

  it('a second client instance reuses the stored id', async () => {
    const ls = makeLocalStorage();
    vi.stubGlobal('localStorage', ls);
    const calls = captureFetch();

    new ChatClient(baseConfig);
    const first = ls.getItem('bb_widget_visitor');

    // A different schema on the same origin still shares the visitor id —
    // one person chatting with two schemas of one deployment = one user.
    const second = new ChatClient({ ...baseConfig, schemaName: 'other' });
    expect(ls.getItem('bb_widget_visitor')).toBe(first);

    await second.send('hi', baseCallbacks());
    const body = JSON.parse(String(calls[0].init.body));
    expect(body.user_sub).toBe(first);
  });

  it('regenerates when the stored value is garbage', () => {
    for (const garbage of ['<script>alert(1)</script>', '', 'a'.repeat(65), 'has space', 'юникод']) {
      const ls = makeLocalStorage({ bb_widget_visitor: garbage });
      vi.stubGlobal('localStorage', ls);

      new ChatClient(baseConfig);

      const stored = ls.getItem('bb_widget_visitor');
      expect(stored).not.toBe(garbage);
      expect(stored).toMatch(VISITOR_ID_PATTERN);
    }
  });

  it('still yields a usable id when localStorage throws', async () => {
    vi.stubGlobal('localStorage', {
      getItem: () => {
        throw new Error('denied');
      },
      setItem: () => {
        throw new Error('denied');
      },
    });
    const calls = captureFetch();

    const client = new ChatClient(baseConfig);
    await client.send('hi', baseCallbacks());

    const body = JSON.parse(String(calls[0].init.body));
    expect(body.user_sub).toMatch(VISITOR_ID_PATTERN);
  });

  it('falls back to a Math.random id when crypto.randomUUID is unavailable', () => {
    vi.stubGlobal('crypto', {});
    const ls = makeLocalStorage();
    vi.stubGlobal('localStorage', ls);

    new ChatClient(baseConfig);

    expect(ls.getItem('bb_widget_visitor')).toMatch(VISITOR_ID_PATTERN);
  });
});

describe('ChatClient — dispatch body carries user_sub', () => {
  const realFetch = globalThis.fetch;

  afterEach(() => {
    globalThis.fetch = realFetch;
    vi.unstubAllGlobals();
  });

  it('always sends user_sub with the visitor id alongside the message', async () => {
    const ls = makeLocalStorage();
    vi.stubGlobal('localStorage', ls);
    const calls = captureFetch();

    const client = new ChatClient(baseConfig);
    await client.send('hello', baseCallbacks());

    expect(calls).toHaveLength(1);
    const body = JSON.parse(String(calls[0].init.body));
    expect(body.message).toBe('hello');
    expect(body.user_sub).toBe(ls.getItem('bb_widget_visitor'));
  });
});

// Contract: GET {endpoint}/api/v1/widget-config → {"attribution": true|false}.
// Fail-quiet — the config endpoint must never break chat.
describe('ChatClient.fetchWidgetConfig', () => {
  const realFetch = globalThis.fetch;

  afterEach(() => {
    globalThis.fetch = realFetch;
    vi.unstubAllGlobals();
  });

  function newClient(apiKey: string | null = null): ChatClient {
    vi.stubGlobal('localStorage', makeLocalStorage());
    return new ChatClient({ ...baseConfig, apiKey });
  }

  it('resolves attribution: true on 200 {"attribution": true}', async () => {
    globalThis.fetch = vi.fn(async () => ({
      ok: true,
      status: 200,
      json: async () => ({ attribution: true }),
    })) as unknown as typeof fetch;

    await expect(newClient().fetchWidgetConfig()).resolves.toEqual({ attribution: true });
  });

  it('resolves attribution: false on non-200', async () => {
    globalThis.fetch = vi.fn(async () => ({
      ok: false,
      status: 500,
      json: async () => ({ attribution: true }),
    })) as unknown as typeof fetch;

    await expect(newClient().fetchWidgetConfig()).resolves.toEqual({ attribution: false });
  });

  it('resolves attribution: false on network error', async () => {
    globalThis.fetch = vi.fn(async () => {
      throw new TypeError('network down');
    }) as unknown as typeof fetch;

    await expect(newClient().fetchWidgetConfig()).resolves.toEqual({ attribution: false });
  });

  it('resolves attribution: false on malformed JSON', async () => {
    globalThis.fetch = vi.fn(async () => ({
      ok: true,
      status: 200,
      json: async () => {
        throw new SyntaxError('unexpected token');
      },
    })) as unknown as typeof fetch;

    await expect(newClient().fetchWidgetConfig()).resolves.toEqual({ attribution: false });
  });

  it('resolves attribution: false when the value is not the boolean true', async () => {
    globalThis.fetch = vi.fn(async () => ({
      ok: true,
      status: 200,
      json: async () => ({ attribution: 'yes' }),
    })) as unknown as typeof fetch;

    await expect(newClient().fetchWidgetConfig()).resolves.toEqual({ attribution: false });
  });

  it('hits /api/v1/widget-config and sends Authorization when apiKey is configured', async () => {
    const calls: { url: string; init: RequestInit }[] = [];
    globalThis.fetch = vi.fn(async (url: RequestInfo | URL, init?: RequestInit) => {
      calls.push({ url: String(url), init: init ?? {} });
      return { ok: true, status: 200, json: async () => ({ attribution: true }) } as unknown as Response;
    }) as unknown as typeof fetch;

    await newClient('bb_pk_widget_abc').fetchWidgetConfig();

    expect(calls).toHaveLength(1);
    expect(calls[0].url).toBe('http://engine.test/api/v1/widget-config');
    const headers = calls[0].init.headers as Record<string, string>;
    expect(headers['Authorization']).toBe('Bearer bb_pk_widget_abc');
  });
});
