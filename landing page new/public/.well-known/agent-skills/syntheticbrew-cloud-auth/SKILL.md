---
name: syntheticbrew-cloud-auth
description: Register, log in, and call the SyntheticBrew Cloud API as an automated client using the JWT Bearer flow. Covers token refresh and self-hosted Engine API tokens. No OAuth server exists.
license: CC-BY-4.0
---

# Authenticating with the SyntheticBrew Cloud API

Canonical reference: https://syntheticbrew.ai/auth.md

## Flow

1. Register an account: `POST https://api.syntheticbrew.ai/api/v1/auth/register` with `{ "email", "password" }`.
2. Log in: `POST https://api.syntheticbrew.ai/api/v1/auth/login` with `{ "email", "password" }` — the response contains an access token (Ed25519-signed JWT, short-lived) and a refresh token.
3. Call the API with the header `Authorization: Bearer <access_token>`.
4. When the access token expires: `POST https://api.syntheticbrew.ai/api/v1/auth/refresh` with the refresh token. Re-login if the refresh token is rejected.

## Endpoints

- Cloud API base: `https://api.syntheticbrew.ai/api/v1`
- Engine API base: `https://app.syntheticbrew.ai/api/v1`
- Health check (no auth): `GET https://api.syntheticbrew.ai/health`

## Self-hosted Engine deployments

Create a long-lived scoped API token in the admin dashboard and send it as a Bearer token. Self-hosted Community Edition runs without any SyntheticBrew Cloud dependency.

## No OAuth

SyntheticBrew does not operate a public OAuth/OIDC authorization server; no OAuth discovery metadata is published. The JWT flow above is the complete, supported path.
