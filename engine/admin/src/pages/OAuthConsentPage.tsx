import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import Button from '../components/Button';
import {
  getAuthorizeInfo,
  approveAuthorization,
  type AuthorizeInfo,
  type AuthorizeQuery,
} from '../api/oauth';

const SCOPE_PROVISION = 'provision';
const SCOPE_MANAGE = 'manage';

function readAuthorizeQuery(search: string): AuthorizeQuery {
  const params = new URLSearchParams(search);
  const get = (key: string): string => params.get(key) ?? '';
  return {
    client_id: get('client_id'),
    redirect_uri: get('redirect_uri'),
    scope: get('scope'),
    state: get('state'),
    code_challenge: get('code_challenge'),
    code_challenge_method: get('code_challenge_method'),
    resource: get('resource'),
  };
}

// redirectHost surfaces where the authorization code will be delivered so the
// user can spot a phishing redirect. A malformed redirect_uri returns the raw
// string rather than hiding it.
function redirectHost(uri: string): string {
  try {
    return new URL(uri).host;
  } catch {
    return uri;
  }
}

export default function OAuthConsentPage() {
  // Read once per mount: the query comes from the client's authorization
  // redirect and never changes while the page is open.
  const [query] = useState(() => readAuthorizeQuery(window.location.search));
  const [info, setInfo] = useState<AuthorizeInfo | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  // manage grants destructive delete/overwrite — default OFF so a hijacked
  // single click can never hand over destructive power (T12).
  const [manageAllowed, setManageAllowed] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [submitError, setSubmitError] = useState('');

  const hasRequiredParams = query.client_id !== '' && query.redirect_uri !== '';

  useEffect(() => {
    if (!hasRequiredParams) {
      setError('Missing client_id or redirect_uri in the authorization request.');
      setLoading(false);
      return;
    }
    let cancelled = false;
    getAuthorizeInfo(query)
      .then((data) => {
        if (cancelled) return;
        if (!data.redirect_uri_valid) {
          setError('The client is asking to send the code to an unregistered address.');
          return;
        }
        setInfo(data);
      })
      .catch((err: unknown) => {
        if (cancelled) return;
        setError(
          err instanceof Error && err.message
            ? err.message
            : 'Failed to validate the authorization request.',
        );
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [hasRequiredParams, query]);

  const manageRequested = info?.scopes.includes(SCOPE_MANAGE) ?? false;

  const submit = async (deny: boolean) => {
    if (!info) return;
    setSubmitError('');
    setSubmitting(true);
    try {
      const approvedScopes = deny
        ? []
        : manageRequested && manageAllowed
          ? [SCOPE_PROVISION, SCOPE_MANAGE]
          : [SCOPE_PROVISION];
      const { redirect_url } = await approveAuthorization({
        ...query,
        approved_scopes: approvedScopes,
        consent_nonce: info.consent_nonce,
        deny,
      });
      // Keep `submitting` on — the browser is leaving for the client's
      // redirect_uri and the buttons must stay disabled until it does.
      window.location.href = redirect_url;
    } catch (err) {
      setSubmitError(
        err instanceof Error && err.message
          ? err.message
          : 'Authorization failed. Please try again.',
      );
      setSubmitting(false);
    }
  };

  if (loading) {
    return (
      <div className="flex min-h-screen items-center justify-center bg-brand-dark text-sm text-brand-shade2">
        Loading…
      </div>
    );
  }

  if (error || !info) {
    return <ConsentError message={error || 'Failed to validate the authorization request.'} />;
  }

  return (
    <div className="flex min-h-screen items-center justify-center bg-brand-dark px-4">
      <div
        className="w-full max-w-md rounded-card border border-brand-shade3/30 bg-brand-dark-surface p-6"
        data-testid="consent-card"
      >
        <h1 className="text-xl font-bold text-brand-light">Authorize access</h1>

        <div className="mt-4 rounded-btn border border-brand-shade3/30 bg-brand-dark p-3">
          <p className="text-[11px] uppercase tracking-wide text-brand-shade3">
            Requesting application
          </p>
          <p
            className="mt-1 text-sm font-medium text-brand-light break-words"
            data-testid="client-name"
          >
            {info.client_name || 'Unknown application'}
          </p>
          <p className="mt-1 text-[11px] text-brand-shade3" data-testid="client-unverified">
            Unverified — this name is self-reported by the application and has not
            been confirmed. Only continue if you started this connection.
          </p>
        </div>

        <div className="mt-3 rounded-btn border border-brand-shade3/30 bg-brand-dark p-3">
          <p className="text-[11px] uppercase tracking-wide text-brand-shade3">
            The authorization code will be sent to
          </p>
          <p
            className="mt-1 font-mono text-sm font-semibold text-brand-light break-all"
            data-testid="redirect-host"
          >
            {redirectHost(query.redirect_uri)}
          </p>
        </div>

        <p className="mt-4 text-sm font-medium text-brand-shade2">This will allow it to:</p>
        <ul className="mt-2 space-y-2">
          <li
            className="flex items-start gap-2 text-sm text-brand-light"
            data-testid="scope-provision"
          >
            <span className="text-brand-accent">&bull;</span>
            <span>Create and configure agents, schemas, models and MCP servers</span>
          </li>
        </ul>

        {manageRequested && (
          <label className="mt-3 flex cursor-pointer items-start gap-2 text-sm text-brand-light">
            <input
              type="checkbox"
              checked={manageAllowed}
              onChange={(e) => setManageAllowed(e.target.checked)}
              className="mt-0.5 cursor-pointer accent-brand-accent"
              data-testid="manage-checkbox"
            />
            <span>
              Also allow destructive operations (delete or overwrite existing agents,
              schemas, models and MCP servers)
            </span>
          </label>
        )}

        {submitError && (
          <div
            className="mt-4 rounded-btn border border-red-500/20 bg-red-500/10 p-3 text-sm text-red-400"
            data-testid="submit-error"
          >
            {submitError}
          </div>
        )}

        <div className="mt-6 flex gap-3">
          <Button
            variant="primary"
            className="flex-1"
            onClick={() => submit(false)}
            disabled={submitting}
            data-testid="allow-button"
          >
            {submitting ? 'Redirecting…' : 'Allow'}
          </Button>
          <Button
            variant="secondary"
            className="flex-1"
            onClick={() => submit(true)}
            disabled={submitting}
            data-testid="deny-button"
          >
            Deny
          </Button>
        </div>
      </div>
    </div>
  );
}

function ConsentError({ message }: { message: string }) {
  return (
    <div className="flex min-h-screen items-center justify-center bg-brand-dark px-4">
      <div className="w-full max-w-md">
        <div
          className="rounded-card border border-red-500/20 bg-red-500/10 p-4 text-sm text-red-400"
          data-testid="consent-error"
        >
          <p className="font-medium">Authorization request rejected</p>
          <p className="mt-1">{message}</p>
        </div>
        <p className="mt-3 text-center text-xs text-brand-shade3">
          <Link to="/" className="text-brand-accent hover:underline">
            Return to the dashboard
          </Link>
        </p>
      </div>
    </div>
  );
}
