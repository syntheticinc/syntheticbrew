import { useEffect, useState } from 'react';
import { Navigate, useLocation } from 'react-router-dom';
import { api } from '../api/client';

// Runs AFTER ProtectedRoute. Blocks the admin surface until the tenant has
// at least one LLM. `error` state fails open — surfacing /models 5xx here
// would permanently strand the user.
type GateState = 'checking' | 'has-models' | 'no-models' | 'error';

// Session-scoped warm-cache flag. The wizard sets it on Step 1 success
// before navigating to /schemas; the gate honors it for a short window
// (RACE_WINDOW_MS) to bridge the read-after-write race against the
// just-created models row. Beyond that window the API is the source of
// truth — earlier the flag was sticky-forever and prevented recovery
// when the tenant's models dropped to zero (admin delete / DB wipe).
const ONBOARDED_FLAG = 'bb_onboarded';
const RACE_WINDOW_MS = 5_000;

// Flag value format: `1:<unix_ms_set_at>`. Legacy `'1'` (no timestamp)
// is treated as expired so old sessions don't keep the wizard locked
// out after this fix lands.
function readOnboardedFlag(): { trusted: boolean } {
  try {
    const raw = sessionStorage.getItem(ONBOARDED_FLAG);
    if (!raw) return { trusted: false };
    const [, ts] = raw.split(':');
    if (!ts) return { trusted: false };
    const setAt = Number(ts);
    if (!Number.isFinite(setAt)) return { trusted: false };
    return { trusted: Date.now() - setAt < RACE_WINDOW_MS };
  } catch {
    return { trusted: false };
  }
}

function setOnboardedFlag() {
  try { sessionStorage.setItem(ONBOARDED_FLAG, `1:${Date.now()}`); } catch { /* no-op */ }
}

function clearOnboardedFlag() {
  try { sessionStorage.removeItem(ONBOARDED_FLAG); } catch { /* no-op */ }
}

export default function OnboardingGate({ children }: { children: React.ReactNode }) {
  // Initial render: trust the flag inside the race window so the wizard's
  // Step 1 → /schemas navigation doesn't flicker through 'checking' before
  // the API replies. Outside the window we always start from 'checking'
  // and let the API call below decide.
  const [state, setState] = useState<GateState>(() =>
    readOnboardedFlag().trusted ? 'has-models' : 'checking',
  );
  const location = useLocation();

  useEffect(() => {
    // Always re-check via API on each mount / path change. The flag is a
    // short-lived race-window cache, never the source of truth. Without
    // this, deleting / wiping models leaves the gate happily rendering
    // admin surface and the user stranded with an unbound builder-assistant
    // (no path back into the wizard).
    let cancelled = false;
    api
      .listModels()
      .then((models) => {
        if (cancelled) return;
        const hasModels = !!models && models.length > 0;
        if (hasModels) {
          setOnboardedFlag();
          setState('has-models');
          return;
        }
        // API is authoritative: tenant has zero models. Drop the cached
        // flag (it may be stale from a prior session) and surface the
        // wizard. The 'no-models' branch below handles a tail race by
        // checking the flag once more synchronously before navigating.
        clearOnboardedFlag();
        setState('no-models');
      })
      .catch(() => {
        if (cancelled) return;
        setState('error');
      });
    return () => {
      cancelled = true;
    };
    // Re-run on path change so finishing the wizard (navigate elsewhere)
    // re-checks and unlocks the normal surface without a full reload.
  }, [location.pathname]);

  if (state === 'checking') {
    return (
      <div className="fixed inset-0 bg-brand-dark flex items-center justify-center">
        <div className="text-sm text-brand-shade3 font-mono">Loading workspace…</div>
      </div>
    );
  }

  if (state === 'no-models') {
    // Synchronous re-check of the session flag before redirecting. The
    // useEffect that flips state from 'no-models' → 'has-models' runs AFTER
    // this render commits, so when the wizard's Step 1 sets the flag and
    // immediately calls navigate('/schemas'), the very next render here
    // still has stale state='no-models'. Without this check we'd fire
    // <Navigate to="/onboarding" replace /> before the effect can update
    // state, sending the user back into the wizard in a loop. We trust
    // the flag only inside RACE_WINDOW_MS — outside that the API is the
    // source of truth and a stale flag must not gate the redirect.
    if (readOnboardedFlag().trusted) {
      return <>{children}</>;
    }
    // Guard against loops: the wizard itself must not be gated.
    if (location.pathname === '/onboarding') {
      return <>{children}</>;
    }
    return <Navigate to="/onboarding" replace />;
  }

  return <>{children}</>;
}
