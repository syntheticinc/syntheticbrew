# auth.md

Agent authentication guide for SyntheticBrew Cloud. This document is self-contained: everything an automated client needs to obtain and use credentials is described below. There is no OAuth authorization server — see the OAuth section at the end.

## Audience

AI agents and automated clients that want to call the SyntheticBrew Cloud API or a SyntheticBrew Engine deployment programmatically.

## Endpoints

- Cloud API: `https://api.syntheticbrew.ai/api/v1`
- Engine API: `https://app.syntheticbrew.ai/api/v1`
- Health (no authentication): `GET https://api.syntheticbrew.ai/health`
- Docs: https://syntheticbrew.ai/docs/

## Registration and provisioning

1. Create an account: `POST /api/v1/auth/register` with `{ "email", "password" }`. Interactive alternative: https://syntheticbrew.ai/register (email/password or Google account).
2. Log in: `POST /api/v1/auth/login` with `{ "email", "password" }` — returns an access token and a refresh token.

## Supported authentication methods

- **Bearer JWT** (Cloud): Ed25519-signed access tokens issued by the login endpoint. Short-lived; refresh with `POST /api/v1/auth/refresh` using the refresh token.
- **Scoped API tokens** (self-hosted Engine): long-lived tokens created in the admin dashboard, sent as Bearer tokens.

## Using credentials

Send `Authorization: Bearer <access_token>` on every API request. When the access token expires, refresh it with the refresh token; re-login if the refresh token is rejected.

## OAuth

SyntheticBrew intentionally does not operate a public OAuth/OIDC authorization server, so no OAuth discovery metadata (`/.well-known/oauth-authorization-server`, `/.well-known/oauth-protected-resource`) is published. The JWT flow above is the complete, supported path. Self-hosted Community Edition can run without any SyntheticBrew Cloud dependency.
