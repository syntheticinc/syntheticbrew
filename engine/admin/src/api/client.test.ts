import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';

// We need to test the APIClient class behavior.
// Since it's a singleton export, we'll test via module re-import.

function installLocationStub(): () => string {
  let assignedHref = '';
  const stub = { pathname: '/admin/overview' };
  Object.defineProperty(stub, 'href', {
    get() {
      return assignedHref;
    },
    set(value: string) {
      assignedHref = value;
    },
  });
  Object.defineProperty(window, 'location', {
    value: stub,
    writable: true,
    configurable: true,
  });
  return () => assignedHref;
}

describe('APIClient', () => {
  beforeEach(() => {
    localStorage.clear();
    vi.resetModules();
    vi.unstubAllEnvs();
    vi.restoreAllMocks();
  });

  afterEach(() => {
    vi.unstubAllEnvs();
    vi.restoreAllMocks();
  });

  it('stores token in localStorage on setToken', async () => {
    const { api } = await import('./client');
    api.setToken('test-jwt-token');
    expect(localStorage.getItem('jwt')).toBe('test-jwt-token');
    expect(api.isAuthenticated()).toBe(true);
  });

  it('clears token on clearToken', async () => {
    const { api } = await import('./client');
    api.setToken('test-jwt-token');
    api.clearToken();
    expect(localStorage.getItem('jwt')).toBeNull();
    expect(api.isAuthenticated()).toBe(false);
  });

  it('sends Authorization header when token is set', async () => {
    const { api } = await import('./client');
    api.setToken('my-jwt');

    const mockFetch = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      headers: new Headers({ 'Content-Type': 'application/json' }),
      json: () => Promise.resolve([]),
    });
    vi.stubGlobal('fetch', mockFetch);

    await api.listAgents();

    expect(mockFetch).toHaveBeenCalledWith(
      '/api/v1/agents',
      expect.objectContaining({
        headers: expect.objectContaining({
          Authorization: 'Bearer my-jwt',
        }),
      }),
    );
  });

  // 401 recovery — local mode (default).
  // Behaviour: clearToken + re-mint via bootstrapAuth (dynamic-imported from
  // hooks/useAuth). No same-origin /login redirect — the SPA has no such route.
  it('401 in local mode re-mints via bootstrapAuth without redirect', async () => {
    const bootstrapAuth = vi.fn(async () => true);
    vi.doMock('../hooks/useAuth', () => ({ bootstrapAuth }));

    const getHref = installLocationStub();

    const { api } = await import('./client');
    api.setToken('expired-token');

    const mockFetch = vi.fn().mockResolvedValue({
      ok: false,
      status: 401,
      headers: new Headers(),
      text: () => Promise.resolve('Unauthorized'),
    });
    vi.stubGlobal('fetch', mockFetch);

    await expect(api.listAgents()).rejects.toThrow('Unauthorized');

    // handleUnauthorized fires bootstrapAuth via dynamic import — give it a
    // microtask tick to resolve and invoke the spy.
    await vi.waitFor(() => expect(bootstrapAuth).toHaveBeenCalledTimes(1));

    expect(getHref()).toBe('');
    expect(localStorage.getItem('jwt')).toBeNull();
  });

  // 401 recovery — external mode with landing configured.
  // Behaviour: redirect to ${landing}/login with return_to + reason.
  it('401 in external mode redirects to landing /login', async () => {
    vi.stubEnv('VITE_AUTH_MODE', 'external');
    vi.stubEnv('VITE_LANDING_URL', 'https://land.test');

    const getHref = installLocationStub();

    const { api } = await import('./client');
    api.setToken('expired-token');

    const mockFetch = vi.fn().mockResolvedValue({
      ok: false,
      status: 401,
      headers: new Headers(),
      text: () => Promise.resolve('Unauthorized'),
    });
    vi.stubGlobal('fetch', mockFetch);

    await expect(api.listAgents()).rejects.toThrow('Unauthorized');

    const href = getHref();
    expect(href).toContain('https://land.test/login');
    expect(href).toContain('return_to=');
    expect(href).toContain('reason=session_expired');
    expect(localStorage.getItem('jwt')).toBeNull();
  });

  // 401 recovery — external mode missing landing config.
  // Behaviour: throw a build-config error so the misconfiguration is loud.
  it('401 in external mode without VITE_LANDING_URL throws', async () => {
    vi.stubEnv('VITE_AUTH_MODE', 'external');
    // VITE_LANDING_URL deliberately not stubbed.

    installLocationStub();

    const { api } = await import('./client');
    api.setToken('expired-token');

    const mockFetch = vi.fn().mockResolvedValue({
      ok: false,
      status: 401,
      headers: new Headers(),
      text: () => Promise.resolve('Unauthorized'),
    });
    vi.stubGlobal('fetch', mockFetch);

    await expect(api.listAgents()).rejects.toThrow(/VITE_LANDING_URL/);
    expect(localStorage.getItem('jwt')).toBeNull();
  });

  // Idempotency — three parallel in-flight requests all return 401.
  // bootstrapAuth must run exactly once thanks to the recovering guard.
  it('parallel 401s in local mode trigger bootstrapAuth exactly once', async () => {
    const bootstrapAuth = vi.fn(async () => true);
    vi.doMock('../hooks/useAuth', () => ({ bootstrapAuth }));

    installLocationStub();

    const { api } = await import('./client');
    api.setToken('expired-token');

    const mockFetch = vi.fn().mockResolvedValue({
      ok: false,
      status: 401,
      headers: new Headers(),
      text: () => Promise.resolve('Unauthorized'),
    });
    vi.stubGlobal('fetch', mockFetch);

    const results = await Promise.allSettled([
      api.listAgents(),
      api.listAgents(),
      api.listAgents(),
    ]);
    for (const r of results) {
      expect(r.status).toBe('rejected');
    }

    await vi.waitFor(() => expect(bootstrapAuth).toHaveBeenCalledTimes(1));
  });

  it('getUsageStatus in prototype mode returns the tenant usage mock without network', async () => {
    localStorage.setItem('syntheticbrew_prototype_mode', 'true');
    const mockFetch = vi.fn();
    vi.stubGlobal('fetch', mockFetch);

    const { api } = await import('./client');
    const status = await api.getUsageStatus();

    expect(mockFetch).not.toHaveBeenCalled();
    expect(status.active_users).toEqual({ used: 12, limit: 2000 });
    expect(status.schemas).toEqual({ used: 2, limit: 3 });
    expect(status.knowledge_documents).toEqual({ used: 6, limit: 100 });
    expect(status.turns).toEqual({ used: 18, limit: 50 });
  });

  it('getUsageStatus requests GET /usage-status outside prototype mode', async () => {
    const { api } = await import('./client');
    api.setToken('valid');

    const payload = {
      active_users: { used: 3, limit: 100 },
      schemas: { used: 1, limit: null },
      knowledge_documents: { used: 0, limit: null },
      turns: { used: 7, limit: 50 },
    };
    const mockFetch = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      headers: new Headers({ 'Content-Type': 'application/json' }),
      json: () => Promise.resolve(payload),
    });
    vi.stubGlobal('fetch', mockFetch);

    const status = await api.getUsageStatus();

    expect(mockFetch).toHaveBeenCalledWith('/api/v1/usage-status', expect.anything());
    expect(status.schemas.limit).toBeNull();
    expect(status.turns).toEqual({ used: 7, limit: 50 });
  });

  it('throws on non-OK responses', async () => {
    const { api } = await import('./client');
    api.setToken('valid');

    const mockFetch = vi.fn().mockResolvedValue({
      ok: false,
      status: 500,
      headers: new Headers(),
      text: () => Promise.resolve('{"error":"internal server error"}'),
    });
    vi.stubGlobal('fetch', mockFetch);

    await expect(api.health()).rejects.toThrow('internal server error');
  });
});
