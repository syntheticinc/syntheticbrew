import { createContext, useContext, useState, useCallback, useEffect } from 'react';
import { api } from '../api/client';

// Wave 1+7 auth model.
//
// Admin is a pure client SPA — it never POSTs credentials. Two build-time
// modes decide how the token is obtained:
//
//   VITE_AUTH_MODE=local (default)
//     Engine exposes `POST /api/v1/auth/local-session` (no auth, no body)
//     which mints a short-lived JWT for the single local admin user. The
//     SPA calls it once on mount if no token is cached in localStorage.
//     This is the dev / self-hosted flow — there is no login form.
//
//   VITE_AUTH_MODE=external
//     Token is produced by an external identity service (Cloud landing,
//     SSO, etc.) and delivered to the admin as a URL hash fragment
//     `#at=<token>&rt=<refresh>`. If neither localStorage nor the hash
//     carries a token, the SPA redirects to `VITE_LANDING_URL/login?return_to=<current>`.
//
// The existing axios/fetch client keeps using the token the same way
// (Authorization: Bearer <token>). Expiry → 401 → clearToken + re-bootstrap.

export type AuthMode = 'local' | 'external';

export const AUTH_MODE: AuthMode =
  (import.meta.env.VITE_AUTH_MODE as AuthMode | undefined) === 'external' ? 'external' : 'local';

const LANDING_URL = import.meta.env.VITE_LANDING_URL as string | undefined;

export interface AuthContextType {
  isAuthenticated: boolean;
  logout: () => void;
}

export const AuthContext = createContext<AuthContextType | null>(null);

export function useAuth(): AuthContextType {
  const ctx = useContext(AuthContext);
  if (!ctx) throw new Error('useAuth must be used within AuthProvider');
  return ctx;
}

// parseHashToken consumes `#at=...&rt=...` fragment (if present) and clears
// it from the URL bar so the token never leaks via copy/paste or logs.
// Returns the access token or null.
function parseHashToken(): string | null {
  const hash = window.location.hash.startsWith('#')
    ? window.location.hash.slice(1)
    : window.location.hash;
  if (!hash) return null;
  const params = new URLSearchParams(hash);
  const at = params.get('at');
  if (!at) return null;
  // Scrub fragment so the token doesn't linger in location.href.
  window.history.replaceState(
    null,
    '',
    window.location.pathname + window.location.search,
  );
  return at;
}

// Exchange a same-origin cloud session for an engine token; null on miss.
// 5s timeout caps the SPA at ~5s of "Authenticating…" before falling through
// to redirectToLanding(); without it a hung engine pinned the SPA indefinitely.
async function mintFromCloudSession(): Promise<string | null> {
  let cloudToken: string | null = null;
  try {
    cloudToken = localStorage.getItem('syntheticbrew_access_token');
  } catch {
    return null;
  }
  if (!cloudToken) return null;

  const ctl = new AbortController();
  const timer = setTimeout(() => ctl.abort(), 5000);
  try {
    const resp = await fetch('/api/v1/auth/engine-token', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        Authorization: `Bearer ${cloudToken}`,
      },
      body: '{}',
      signal: ctl.signal,
    });
    if (!resp.ok) return null;
    const json = await resp.json();
    return json?.data?.token ?? json?.token ?? null;
  } catch {
    return null;
  } finally {
    clearTimeout(timer);
  }
}

// redirectToLanding sends the user to the external login flow, passing
// the current URL as `return_to` so the IdP can bounce back after auth.
function redirectToLanding(): void {
  if (!LANDING_URL) {
    throw new Error(
      'VITE_AUTH_MODE=external requires VITE_LANDING_URL to be set at build time',
    );
  }
  const returnTo = encodeURIComponent(window.location.href);
  window.location.href = `${LANDING_URL}/login?return_to=${returnTo}`;
}

// consumeHandoffToken synchronously applies a fragment handoff token when
// present. A fresh handoff token always wins over a cached session — the
// cached one may be expired or belong to another user, and trusting it first
// bounced the handoff to the login page. It must run BEFORE the first render:
// with a (stale) cached token present the route tree mounts immediately and
// its data fetches would otherwise race the async bootstrap, 401 and redirect.
export function consumeHandoffToken(): boolean {
  if (AUTH_MODE !== 'external') return false;
  const hashToken = parseHashToken();
  if (!hashToken) return false;
  api.setToken(hashToken);
  return true;
}

// bootstrapAuth runs the correct acquisition flow for the active mode.
// External mode consumes a fragment handoff token first (it always wins over
// a cached session); local mode is a no-op when a token is already cached.
// Exported so the api client can re-run it on 401 without the React tree.
export async function bootstrapAuth(): Promise<boolean> {
  if (AUTH_MODE === 'external') {
    if (consumeHandoffToken()) return true;
    if (api.isAuthenticated()) return true;
    const minted = await mintFromCloudSession();
    if (minted) {
      api.setToken(minted);
      try { localStorage.setItem('jwt', minted); } catch {}
      return true;
    }
    redirectToLanding();
    return false;
  }

  if (api.isAuthenticated()) return true;

  // local mode: ask the engine for a fresh session token.
  const res = await api.localSession();
  api.setToken(res.access_token);
  return true;
}

export function useAuthProvider(): AuthContextType {
  const [isAuthenticated, setIsAuthenticated] = useState(() => {
    consumeHandoffToken();
    return api.isAuthenticated();
  });

  useEffect(() => {
    let cancelled = false;
    bootstrapAuth()
      .then((ok) => {
        if (!cancelled && ok) setIsAuthenticated(true);
      })
      .catch((err) => {
        // In local mode this surfaces as a logged error and leaves the
        // SPA in an unauthenticated state — the 401 handler will retry
        // on the next API call.
        console.error('auth bootstrap failed', err);
      });
    return () => {
      cancelled = true;
    };
  }, []);

  const logout = useCallback(() => {
    api.clearToken();
    setIsAuthenticated(false);
    // Re-run bootstrap to either mint a new local session or bounce to
    // the external landing page.
    void bootstrapAuth().then((ok) => {
      if (ok) setIsAuthenticated(true);
    });
  }, []);

  return { isAuthenticated, logout };
}
