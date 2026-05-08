# Changelog

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

