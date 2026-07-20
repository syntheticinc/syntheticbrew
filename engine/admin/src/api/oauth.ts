import { api } from './client';

const BASE_URL = '/api/v1';

// OAuth endpoints speak RFC 6749-style JSON (top-level fields, errors as
// {"error","error_description"}), not the admin's {data}/{error} envelope — so
// they bypass APIClient.request(). Both calls carry the admin session bearer:
// authorize-info needs it so the engine can bind an anti-CSRF consent_nonce to
// the session subject, and approve is the authenticated consent decision.

export interface AuthorizeQuery {
  client_id: string;
  redirect_uri: string;
  scope: string;
  state: string;
  code_challenge: string;
  code_challenge_method: string;
  resource: string;
}

export interface AuthorizeInfo {
  client_name: string;
  scopes: string[];
  redirect_uri_valid: boolean;
  consent_nonce: string;
}

export interface ApproveRequest extends AuthorizeQuery {
  approved_scopes: string[];
  consent_nonce: string;
  deny: boolean;
}

interface OAuthErrorBody {
  error?: string | { code?: string; message?: string };
  error_description?: string;
}

async function unwrapOAuth<T>(res: Response): Promise<T> {
  const json = (await res.json().catch(() => null)) as T | OAuthErrorBody | null;
  if (!res.ok) {
    const body = (json ?? {}) as OAuthErrorBody;
    // The auth middleware in front of /approve still answers with the admin
    // envelope, so accept both the RFC {error,error_description} shape and the
    // {error:{code,message}} one.
    const message =
      body.error_description ??
      (typeof body.error === 'object' ? body.error?.message : body.error) ??
      'Failed to validate the authorization request.';
    throw new Error(message);
  }
  return json as T;
}

function authHeaders(): Record<string, string> {
  const token = api.getToken();
  return token ? { Authorization: `Bearer ${token}` } : {};
}

export async function getAuthorizeInfo(params: AuthorizeQuery): Promise<AuthorizeInfo> {
  const qs = new URLSearchParams({
    client_id: params.client_id,
    redirect_uri: params.redirect_uri,
    scope: params.scope,
    code_challenge: params.code_challenge,
    code_challenge_method: params.code_challenge_method,
  }).toString();
  const res = await fetch(`${BASE_URL}/oauth/authorize-info?${qs}`, {
    headers: authHeaders(),
  });
  return unwrapOAuth<AuthorizeInfo>(res);
}

export async function approveAuthorization(
  body: ApproveRequest,
): Promise<{ redirect_url: string }> {
  const res = await fetch(`${BASE_URL}/oauth/approve`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', ...authHeaders() },
    body: JSON.stringify(body),
  });
  return unwrapOAuth<{ redirect_url: string }>(res);
}
