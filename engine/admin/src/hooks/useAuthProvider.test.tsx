// Pins the PRE-RENDER half of the handoff fix: the fragment token must be
// applied inside useAuthProvider's useState initializer, before the first
// child render. Child effects run before parent effects, so with a stale
// cached token present the route tree mounts and fetches with the stale token
// unless the swap happens synchronously — an effect-based bootstrap alone
// keeps every other test green while the reported bug is back. This probe
// reads the token in a child's RENDER BODY on first render, which an act()
// flush cannot mask.
import { describe, it, expect, vi } from 'vitest';
import { StrictMode } from 'react';
import { render } from '@testing-library/react';

vi.hoisted(() => {
  vi.stubEnv('VITE_AUTH_MODE', 'external');
  vi.stubEnv('VITE_LANDING_URL', 'http://landing.test');
});

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

import { api } from '../api/client';
import { useAuthProvider } from './useAuth';

const mockApi = api as typeof api & { __store: { token: string | null } };

function Harness({ seen }: { seen: (string | null)[] }) {
  useAuthProvider();
  seen.push(mockApi.__store.token);
  return null;
}

describe('useAuthProvider (external mode, pre-render handoff)', () => {
  it('applies the fragment token before the first render with a stale cache present', () => {
    mockApi.__store.token = 'stale-expired-token';
    window.history.replaceState(null, '', '/admin/api-keys#at=fresh-handoff-token');
    const seen: (string | null)[] = [];

    render(
      <StrictMode>
        <Harness seen={seen} />
      </StrictMode>,
    );

    expect(seen.length).toBeGreaterThan(0);
    expect(seen[0]).toBe('fresh-handoff-token');
    expect(window.location.hash).toBe('');
  });
});
