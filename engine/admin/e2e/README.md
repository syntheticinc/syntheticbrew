# engine/admin E2E (Playwright)

## Stack requirements

Tests run against the full Cloud stack via Caddy at `http://localhost:18082`.

```bash
cd local-dev
docker compose -f docker-compose-cloud-full.yml -p cloud-full up -d --wait
```

## Auth model

Admin SPA at `/admin/*` uses an engine-issued Ed25519 JWT. In Cloud mode the flow is:

1. Register user via cloud-api (`POST /api/v1/auth/register`)
2. Verify email (requires SMTP — see limitation below)
3. Login via cloud-api (`POST /api/v1/auth/login`) → `access_token`
4. Mint engine token via cloud-api (`POST /api/v1/auth/engine-token` with Bearer) → `engine_token`
5. Store `engine_token` in `localStorage.jwt` for admin SPA

The `adminSession` worker-fixture does steps 1-4 once per worker. Any test using `adminToken` or `authenticatedAdmin` is **skipped** if step 2 fails (EMAIL_NOT_VERIFIED).

## Known limitations

**~112 of 312 tests skipped** due to missing SMTP mock in the stack. To enable:

- Add `mailhog` to `docker-compose-cloud-full.yml` + update fixtures to fetch verification token from its API
- OR add `SYNTHETICBREW_TEST_AUTO_VERIFY_EMAIL=true` to cloud-api env
- OR add admin-only `POST /api/v1/internal/test/verify-email` handler behind the existing `SYNTHETICBREW_TEST_AUTO_SUBSCRIPTION` flag

**EE-only tests** (license activate/refresh/revoke) are skipped by default — enable `SYNTHETICBREW_EE=true` in compose to include them.

**Metering fail-closed tests** require docker-level manipulation (`docker stop landing`) and are marked manual-only.

## Commands

```bash
npm run test:e2e              # headless, all specs
npm run test:e2e:headed       # headed
npm run test:e2e:ui           # UI mode
npx playwright test --list
npx playwright test -g "SCC-"
```

## Layout

- `fixtures.ts` — `adminSession` (worker-scoped), `adminToken`, `authenticatedAdmin`, `apiFetch` helper
- `e2e/onboarding/` — wizard steps
- `e2e/navigation/` — sidebar, empty states, handoff
- `e2e/crud/` — agents, schemas, models, mcp, knowledge, api-keys, settings, capabilities, relations
- `e2e/widget/` — embed snippet, origins, XSS
- `e2e/mcp/` — catalog, transports, auth modes
- `e2e/inspect/` — sessions pagination, audit, tool-call log
- `e2e/resilience/` — circuit breakers, heartbeats, dead letter
- `e2e/apikey/` — bb_* tokens, revoke, scopes
- `e2e/license-ee/` — EE-flag gated
- `e2e/metering/` — Cloud quota, HMAC
- `e2e/security/` — SCC-01..06 per-endpoint
- `e2e/concurrency/` — multi-admin race
- `e2e/a11y/` — tab order, layout consistency
- `e2e/docs-site/` — Starlight 27 pages
- `e2e/headers/` — widget.js cache, /vite.svg
- `e2e/sse/` — SSE replay
- `e2e/regression/` — BUG-001..004 guards, known bug fixes from playwright-plan.md
