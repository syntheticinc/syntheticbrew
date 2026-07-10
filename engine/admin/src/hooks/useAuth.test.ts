// Regression guard for the external-mode handoff: a fresh `#at=` fragment
// token must win over any cached localStorage session. A cached token may be
// expired or belong to another user; trusting it first bounced every first
// handoff click to the landing login (observed as session_expired).
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';

vi.mock('../api/client', () => {
  const store: { token: string | null } = { token: null };
  return {
    api: {
      isAuthenticated: () => store.token !== null,
      setToken: (t: string) => { store.token = t; },
      clearToken: () => { store.token = null; },
      localSession: vi.fn(async () => ({ access_token: 'local-minted' })),
      __store: store,
    },
  };
});

type Store = { token: string | null };

async function loadExternalMode(): Promise<{
  bootstrapAuth: () => Promise<boolean>;
  consumeHandoffToken: () => boolean;
  store: Store;
}> {
  vi.resetModules();
  vi.stubEnv('VITE_AUTH_MODE', 'external');
  vi.stubEnv('VITE_LANDING_URL', 'http://landing.test');
  const client = (await import('../api/client')) as unknown as {
    api: { __store: Store };
  };
  const { bootstrapAuth, consumeHandoffToken } = await import('./useAuth');
  client.api.__store.token = null;
  return { bootstrapAuth, consumeHandoffToken, store: client.api.__store };
}

function setHash(hash: string) {
  window.history.replaceState(null, '', '/admin/api-keys' + hash);
}

describe('bootstrapAuth (external mode handoff)', () => {
  beforeEach(() => {
    localStorage.clear();
    setHash('');
  });

  afterEach(() => {
    vi.unstubAllEnvs();
  });

  it('prefers a fresh #at fragment token over a cached (possibly stale) session', async () => {
    const { bootstrapAuth, store } = await loadExternalMode();
    store.token = 'stale-expired-token';
    setHash('#at=fresh-handoff-token');

    const ok = await bootstrapAuth();

    expect(ok).toBe(true);
    expect(store.token).toBe('fresh-handoff-token');
  });

  it('scrubs the fragment after consuming the token', async () => {
    const { bootstrapAuth } = await loadExternalMode();
    setHash('#at=fresh-handoff-token');
    await bootstrapAuth();
    expect(window.location.hash).toBe('');
  });

  it('takes the first at= value when the fragment carries duplicates', async () => {
    // A login return_to that already contained a fragment can produce
    // `#at=<new>&at=<old>`; the first (newest) one must win.
    const { bootstrapAuth, store } = await loadExternalMode();
    setHash('#at=new-token&at=old-token');
    await bootstrapAuth();
    expect(store.token).toBe('new-token');
  });

  it('consumeHandoffToken applies the fragment token synchronously (pre-render)', async () => {
    // Route components mount and fetch as soon as a cached token makes
    // isAuthenticated() true — the handoff swap must not await anything.
    const { consumeHandoffToken, store } = await loadExternalMode();
    store.token = 'stale-expired-token';
    setHash('#at=fresh-handoff-token');

    const consumed = consumeHandoffToken();

    expect(consumed).toBe(true);
    expect(store.token).toBe('fresh-handoff-token');
    expect(window.location.hash).toBe('');
  });

  it('falls back to the cached session when no fragment is present', async () => {
    const { bootstrapAuth, store } = await loadExternalMode();
    store.token = 'cached-valid-token';
    const ok = await bootstrapAuth();
    expect(ok).toBe(true);
    expect(store.token).toBe('cached-valid-token');
  });
});
