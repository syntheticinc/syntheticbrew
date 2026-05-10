# Changelog

## [1.1.4] — 2026-05-10

### **SECURITY** — Pre-existing chat impersonation vulnerability closed

The api_token branch in `auth_middleware.go` did not populate
`domain.WithUserSub(ctx)`. `chat_handler.resolveUserSub` then fell back to
the client-controlled `req.UserSub` body field. An api_token holder with
ScopeChat could create sessions and write memories under any user_sub by
setting it in the request body — full impersonation without admin scope.

Engine 1.1.4 stamps `info.Name` (the api_token name, e.g. operator-declared
`"ai-assistant-proxy"`) into ctx as the canonical user_sub, and `resolveUserSub`
no longer falls back to the body field for authenticated actors. JWT actors
were unaffected — admin path always set ctx UserSub from `claims.Subject`.

**Operator action:** existing memories scoped under `AnonymousMemoryUserID`
(empty user_sub) due to api_token chat traffic stay where they are; new
chat sessions use the token name. Audit any prior cross-user data merging
suspicions before relying on memory ACL invariants.

### Added — granular auth scopes (chirp dev-rollout request)

Five legacy admin-JWT-only mounts migrated from `RequireAdminSession` to
`RequireScope(...)`, enabling programmatic clients to access them with a
narrow-scope api_token instead of being forced to issue admin tokens:

- `GET /api/v1/audit` → `ScopeAuditRead` (262144)
- `GET /api/v1/settings` → `ScopeSettingsRead` (65536)
- `PUT /api/v1/settings/{key}` → `ScopeSettingsWrite` (131072)
- `GET /api/v1/sessions[?...]` → `ScopeSessionsRead` (16384)
- `GET /api/v1/sessions/{id}` → `ScopeSessionsRead`
- `GET /api/v1/sessions/{id}/messages` → `ScopeSessionsRead`
- `POST /api/v1/sessions` → `ScopeSessionsWrite` (32768)
- `PUT /api/v1/sessions/{id}` → `ScopeSessionsWrite`
- `DELETE /api/v1/sessions/{id}` → `ScopeSessionsWrite`
- `GET /api/v1/tools/metadata` → `ScopeToolsRead` (2097152)
- `GET /api/v1/resilience/circuit-breakers` → `ScopeResilienceRead` (524288)
- `POST /api/v1/resilience/circuit-breakers/{name}/reset` →
  `ScopeResilienceWrite` (1048576)

`ScopeAdmin` remains the superscope and continues to bypass `RequireScope` —
existing admin tokens work unchanged. New scope name aliases accepted by
`POST /api/v1/auth/tokens` `scopes:[…]`: `sessions[:read|:write]`,
`settings[:read|:write]`, `audit[:read]`, `resilience[:read|:write]`,
`tools[:read]`. The composite `api` mask now also expands to include all
new read-only scopes, so existing `scopes:["api"]` tokens automatically
gain the read paths.

`POST /api/v1/auth/tokens` and `POST /api/v1/admin/builder-assistant/restore`
deliberately stay under `RequireAdminSession` — token-escalation guard
(api_tokens shouldn't mint other api_tokens) and recovery flow.

### Added — session per-user ACL hardening

`session_handler` introduces `extractSessionACL` actor classification and
applies per-user filtering at the HTTP layer:

- **Trusted-proxy (api_token) actors:** read tenant-wide. Optional
  `?user_sub` query honoured. Mirrors chirp's ai-assistant proxy pattern.
- **ScopeAdmin actors:** same as trusted-proxy — admin tooling unchanged.
- **All other authenticated actors:** `?user_sub` URL parameter is silently
  ignored; results force-scoped to the caller's own `ctx.UserSub`. Direct
  GET/PUT/DELETE on a session UUID owned by another user returns 404 (info
  hiding — same response code as truly-not-found).

This was an enforced-by-admin-gate-only invariant on 1.1.3. After the scope
sweep, opening `/sessions` to non-admin clients without ACL hardening would
have allowed cross-user enumeration via `?user_sub=victim`. Hardening
prevents that regression.

### Added — dispatch handler ACL parity

`dispatch_handler.go` (`/api/v1/dispatch/tasks/{taskId}` and
`/api/v1/sessions/{sessionId}/dispatch-tasks`) now extracts actor and
verifies session ownership before returning packets. Cross-user reads
return 404 — mirrors the new `/sessions/{id}` shape so dispatch routes
can't be used to enumerate session UUIDs across users. `NewDispatchHandler`
takes an additional `SessionOwnerReader` (the existing
`configrepo.GORMSessionRepository`); pass `nil` only in unit tests that
already trust the actor context.

### Added — sessions metadata JSONB column

`sessions.metadata jsonb NOT NULL DEFAULT '{}'::jsonb` (Liquibase
changeset `006_add_sessions_metadata.yaml`). Engine never reads or
interprets the contents — opaque storage for clients that maintain their
own multi-tenant layer (org_id, end-user mapping, etc.) on top of one
ByteBrew tenant. Accepted on `POST /api/v1/sessions` and
`PUT /api/v1/sessions/{id}`, capped to 16KB (`SessionMetadataMaxBytes`),
returned in GET responses. Migration is additive — existing rows backfill
to `{}`.

### Tests
- New CE integration tests `TestSEC20`–`TestSEC28` — token issuance with
  exact scope masks, scope enforcement on `/sessions`, impersonation guard
  on `POST /sessions`, audit/tools/resilience scope migration, metadata
  round-trip.
- Existing `dispatch_handler_test.go` updated — passes a trusted-proxy
  actor context via `asTrustedProxy(req)` so the new ACL guard short-
  circuits and the existing tests continue to cover serialization +
  routing rather than ACL.
- Multi-tenant + multi-user JWT regression tests live in `bytebrew-ee/tests/integration/`
  (separate PR, paired release).

## [1.1.3] — 2026-05-08

### Fixed
- **`POST /api/v1/schemas` now resolves `entry_agent_id` UUID-or-name on
  CREATE.** Previously only `PATCH /api/v1/schemas/{name}` ran the resolver
  (`resolveEntryAgentRef`); the CREATE path stored the raw value, so a
  fresh-DB single-apply that sent the agent NAME (or empty string from a
  pre-resolution miss) wrote `entry_agent_id = NULL` and chat-time validation
  later returned `INVALID_INPUT: schema has no entry agent`. Engine 1.1.3
  CreateSchema mirrors UpdateSchema's call to `resolveEntryAgentRef`, so the
  same UUID-or-name semantics apply to both. New integration test
  `TestSCH10_CreateSchemaWithEntryAgentName` is the regression guard; the
  matching brewctl 0.2.3 release switches to name-passthrough on the wire.
- **`tool_tier.go.CoreToolNames()` now mirrors the actual builtin registry.**
  The function previously listed `manage_subtasks` and `wait`, neither of
  which is registered (subtasks were unified into `manage_tasks` via
  `parent_task_id`; `wait` became `spawn_agent` action="wait"). Operators who
  put either name into an agent's `tools` field saw the engine accept the
  config but emit `resolve tools: unknown builtin tool` at agent runtime.
  After 1.1.3 the truth is `manage_tasks`, `show_structured_output`,
  `spawn_agent`. Stale references purged from `tool_metadata`, `classifier`,
  `content_risk_classifier`, `result_summarizer`, embedded + testdata
  `flows.yaml`, and admin-SPA mocks.

## [1.1.2] — 2026-05-08

### Fixed
- **Closed the 1.1.0 name-keyed validation gap on `models`, `agents`,
  `mcp_servers`.** These three tables already serve URL-keyed routes
  (`/api/v1/{models|agents|mcp-servers}/{name}/...`) but the original
  1.1.0 migration only added `ValidateResourceName` + the
  `chk_*_name_format` CHECK constraint to `schemas` + `knowledge_bases`.
  POST on the missed handlers accepted any string, so a Display Name
  like `qwen/qwen3-coder-next` (slash + space + uppercase) could be
  persisted and the row became unreachable through the canonical
  name-keyed URL — chi's router can't round-trip `/` (`%2F`) inside a
  path segment, and `DELETE /api/v1/models/qwen%2Fqwen3-coder-next`
  404'd with no recovery path through the UI.

  Mirrors the schemas/KB pattern exactly:
  - HTTP layer: `ValidateResourceName(req.Name)` at the top of `Create`
    on `model_handler.go`, `agent_handler.go`, `mcp_handler.go`.
  - DB layer: new Liquibase migration `add-extra-resource-name-format-check`
    with the same preflight HALT semantics — operator must rename or
    delete violating rows before the migration applies, no silent data
    loss. Defense-in-depth so a raw INSERT / GORM AutoMigrate / future
    bug cannot land an invalid name.
  - No compat shim: rejected an admin-side fallback `DELETE
    /api/v1/models/by-id/{id}` because it would lock a transient
    legacy-data condition into the API surface forever. Existing bad
    rows are surfaced by the preflight, the operator cleans them up
    once.

## [1.1.1] — 2026-05-08

### Fixed
- **Tenant provisioning regression on fresh signups.** `SeedTenant` hardcoded
  `"My Workspace"` as the default schema name, which violates the new name
  format CHECK constraint shipped in 1.1.0 (`chk_schemas_name_format` —
  `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`). Every new tenant signup against engine
  1.1.0 returned 500 from EE provisioning and left the user without a default
  workspace, surfacing as "builder schema not ready" in the admin UI. Default
  name normalised to `my-workspace`. Added a guard test that round-trips the
  seeded name through `ValidateResourceName` so any future CHECK addition
  surfaces locally instead of breaking signups in prod.

## [1.1.0] — 2026-05-06

### Breaking
- **Schema and knowledge-base REST URLs are now name-keyed.** All
  `/api/v1/schemas/{id}/...` and `/api/v1/knowledge-bases/{id}/...`
  endpoints now use the resource `name` in the URL segment. UUIDs remain
  internal storage detail (PK, FK integrity, audit, sessions). Endpoint
  list:
  - `POST /api/v1/schemas/{name}/chat`
  - `GET|PUT|PATCH|DELETE /api/v1/schemas/{name}`
  - `GET /api/v1/schemas/{name}/agents`
  - `GET|POST|DELETE /api/v1/schemas/{name}/agent-relations`,
    `GET|PUT|DELETE /api/v1/schemas/{name}/agent-relations/{relationId}`
    (relationId stays UUID — internal join-table key)
  - `GET|DELETE /api/v1/schemas/{name}/memory`,
    `DELETE /api/v1/schemas/{name}/memory/{entry_id}` (entry_id stays UUID)
  - `GET|PATCH|DELETE /api/v1/knowledge-bases/{name}`
  - `GET|POST /api/v1/knowledge-bases/{name}/files`,
    `GET|DELETE /api/v1/knowledge-bases/{name}/files/{file_id}`,
    `POST /api/v1/knowledge-bases/{name}/files/{file_id}/reindex`
    (file_id stays UUID)
  - `POST|DELETE /api/v1/knowledge-bases/{name}/agents/{agent_name}`
- **Schema and knowledge-base names are immutable post-create.** PUT/PATCH
  with a different `name` field returns `409 Conflict`
  (`name is immutable; recreate with new name and migrate consumers`).
  Other fields (`description`, `entry_agent_id`, `chat_enabled`,
  `embedding_model_id`) remain mutable. This is required for the
  GitOps-friendly URL contract — operator-declared names are the stable
  consumer-facing handle.

### Added
- **Resource name validation** at HTTP layer: regex
  `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`, max 100 chars, reject UUID-shaped
  strings, reject reserved tokens (`chat`, `agents`, `agent-relations`,
  `memory`, `files`, `health`, `auth`, `tasks`, `models`,
  `knowledge-bases`, `schemas`, `mcp-servers`, `tokens`, `sessions`,
  `metrics`). Reserved list prevents URL-segment collision with route
  patterns. Applied at CREATE request body and at every `{name}` URL
  param.
- **Liquibase CHECK constraint** on `schemas.name` and
  `knowledge_bases.name` — defense-in-depth so a bad insert via raw SQL
  cannot land an invalid name. Migration includes preflight gate that
  HALTs with a loud failure if any existing row violates the new format
  (no silent data loss).
- **Audit middleware route-pattern dispatch.** Audit action resolution
  now reads chi's matched route pattern (e.g.
  `/api/v1/schemas/{name}/agent-relations`) instead of the raw request
  path. A schema named `agent-relations-test` no longer shadows the
  `schema.agent_relation.delete` action — defense-in-depth alongside
  reserved-name validation.

### Internal-only (unchanged)
- `sessions.schema_id` FK → `schemas.id` (UUID)
- `memories.schema_id` FK → `schemas.id` (UUID)
- `audit_logs.resource_id` (UUID)
- `agent_relations.relation_id` (UUID — exposed in URL segment as inner
  param)
- `kb_files.file_id` (UUID — exposed in URL segment as inner param)

### Migration notes
- Operators upgrading from 1.0.x: the Liquibase preflight will HALT if
  any existing schema/KB has a name violating the new regex (uppercase,
  underscores, dots, length > 100). Inspect via:
  ```sql
  SELECT 'schemas' AS table, name FROM schemas
    WHERE name !~ '^[a-z0-9]([-a-z0-9]*[a-z0-9])?$' OR length(name) > 100
  UNION ALL
  SELECT 'knowledge_bases', name FROM knowledge_bases
    WHERE name !~ '^[a-z0-9]([-a-z0-9]*[a-z0-9])?$' OR length(name) > 100;
  ```
  Rename or delete violating rows before re-running `helmfile sync`.

## [Unreleased] — 2026-04-28

### Added
- `BYTEBREW_BOOTSTRAP_ADMIN_TOKEN` env support: when set, engine seeds an admin
  API token in `api_tokens` on first boot (idempotent — skipped when
  `name="bootstrap-admin"` already exists). Enables automated declarative
  GitOps reconcile via `brewctl config-apply` in k8s deployments without
  manual Admin UI token generation.
  Format: `bb_<64-hex>`. Generate: `echo "bb_$(openssl rand -hex 32)"`.
  Scope: admin (mask=16). Name: `bootstrap-admin`.

## Architecture — CE/EE/Cloud Unification (pre-release)

Initial canonical architecture for ByteBrew Engine. Frozen pre-release — no
prior production clients, no upgrade path.

### Identity
- End-user identity is external — JWT `sub` claim — persisted as varchar
  (`sessions.user_sub`, `memories.user_sub`, `audit_logs.actor_sub`,
  `api_tokens.user_sub`). No `users` table; no UUID FKs to a user record.

### Auth
- EdDSA (Ed25519) is the only JWT algorithm. No HS256 shared-secret path.
- `BYTEBREW_AUTH_MODE=local`: engine auto-generates an Ed25519 keypair under
  `BYTEBREW_JWT_KEYS_DIR` on first boot; admin sessions minted via
  `POST /api/v1/auth/local-session` (sub=`local-admin`, tenant_id empty).
  Single-replica use only.
- `BYTEBREW_AUTH_MODE=external`: engine loads the issuer's public key from
  `BYTEBREW_JWT_PUBLIC_KEY_PATH`; no local-session route. Multi-replica safe.
- Admin SPA selects flow at build time via `VITE_AUTH_MODE` (and
  `VITE_LANDING_URL` for external handoff).

### API Contract
- PATCH is the partial-update verb on every resource (agents, schemas,
  models, knowledge-bases, mcp-servers). PUT is strict full-replace and
  returns 400 when required fields are missing.
- Resource references accept UUID **or** name; a single resolver layer
  (`ResolveAgentRef`, `ResolveModelRef`, …) canonicalises before DB reach.
- `models.kind` ∈ {`chat`, `embedding`} — application-layer validation
  rejects kind-mismatches on agent/KB assignment.

### Multi-tenancy
- Every tenant-scoped table carries `tenant_id` (not nullable, default
  installs to `00000000-0000-0000-0000-000000000001`). Cross-tenant reads
  return 404; writes respect the JWT tenant claim.
- MCP transport policy is DI-injected per deployment: permissive (CE) or
  restricted (Cloud — stdio/shell transports rejected at `400`).

### Observability + Security
- Security headers applied to every HTTP response (nosniff, frame-ancestors,
  CSP, referrer-policy; HSTS when TLS/X-Forwarded-Proto https).
- CORS is whitelist-only — empty config means same-origin; no wildcard.
- Widget routes use a schema-scoped CSP with per-tenant `widget_embed_origins`
  read from the `settings` table.
- All `slog` calls use the `*Context` variant; ctx-lint + slog-lint enforce
  in CI.

