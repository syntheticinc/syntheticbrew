# Changelog

All notable changes to the `syntheticbrew-engine` Helm chart will be documented here.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this chart adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.12.1] - 2026-07-08

### Changed

- Bump `appVersion` to `1.12.0` to track the engine release that adds the
  `UsageLimitWriter` provisioning extension point. No template or values
  changes — CE behaviour is unchanged (the extension point is inert in CE).

## [0.12.0] - 2026-07-04

### Changed
- Bump `appVersion` to **1.11.0** — engine adds the MCP provisioning endpoint,
  generic operator-configurable usage limits, and the `SetDefault` model-selector
  extension point. All additive and default-off in CE; no new chart values are
  required, so existing installs upgrade without values changes.

### Fixed
- Migration Job now waits for Postgres to accept TCP connections before running
  Liquibase, via an initContainer that reuses the migrations image (bash
  `/dev/tcp`, no extra tooling or image). Prevents the pre-install/pre-upgrade
  hook — which runs with `backoffLimit: 0` — from failing a release when it races
  a not-yet-ready database. Tunable via
  `migrations.waitForDB.{enabled,retries,intervalSeconds}` (default on; no values
  change required to adopt).

## [0.11.2] - 2026-07-01

### Changed
- Bump `appVersion` to **1.10.2** — engine prompt-cache fixes: the per-turn
  volatile head now sits at the tail so the whole conversation history caches
  across turns, and the history cache breakpoint follows the conversation to full
  depth (replacing the fixed-stride scheme that stopped caching past ~48 messages).
  No chart template or values changes.

## [0.11.1] - 2026-06-27

### Changed
- Bump `appVersion` to **1.10.1** — engine fixes: HITL widget answers are now
  persisted into the agent context snapshot, and cross-turn prompt caching is
  restored (frozen head split into a cache-marked stable head + a per-turn volatile
  head). No chart template or values changes.

## [0.11.0] - 2026-06-24

### Added
- **Declarative BYOK enablement via chart values.** New optional `config.byok`
  block lets operators manage Bring-Your-Own-Key per environment through GitOps
  instead of the imperative Admin Settings API:
  - `config.byok.enabled` (bool) → `SYNTHETICBREW_BYOK_ENABLED`.
  - `config.byok.allowedProviders` (list) → `SYNTHETICBREW_BYOK_ALLOWED_PROVIDERS`
    (comma-separated; empty/omitted = all supported providers: openai, anthropic,
    openrouter, openai_compatible, ollama).
  When the block is set the engine **reconciles** these settings on every boot —
  the declared state supersedes Admin-UI edits (remove the block to hand control
  back to the Admin Dashboard). The env vars are emitted only when `config.byok`
  is present, so existing releases that don't set it are unaffected. Requires
  engine appVersion ≥ 1.10.0.

## [0.10.0] - 2026-06-22

### Added
- **Node-churn stability — keep the only ReadWriteOnce PVC off the pod's
  startup critical path.** A single-replica engine could wedge for hours when a
  node was drained or churned: the JWT keypair PVC is an RWO block volume, and a
  CSI detach can lock for a long time, leaving the rescheduled pod stuck in
  `ContainerCreating` (→ 502). This release lets operators take that PVC off the
  path so the single pod reschedules to any node in seconds. This is
  single-replica resilience, **NOT HA** (replicaCount stays 1; for HA use
  `auth.mode=external`, out of CE).
  - `config.auth.existingKeysSecret` — mount the Ed25519 keypair READ-ONLY from
    a Secret (`jwt_ed25519.priv` + `jwt_ed25519.pub`) instead of generating it
    onto a PVC. When set, no keys PVC is created. Mutually exclusive with
    `persistence.keys`.
  - `clusterAutoscaler.safeToEvict` — renders the
    `cluster-autoscaler.kubernetes.io/safe-to-evict` pod annotation (local mode).
    `false` (default) keeps the cluster-autoscaler from proactively evicting the
    single stateful pod during node optimisation.
  - Deployment strategy auto-selects `RollingUpdate` when no RWO PVC is mounted
    (keypair from a Secret), giving near-zero redeploy downtime; with the keys
    PVC mounted it stays `Recreate` (avoids the RWO upgrade deadlock).

### Removed
- **Knowledge raw-file storage machinery.** The engine is now always stateless
  for knowledge — live data (chunks + embeddings) lives in PostgreSQL/pgvector
  and raw upload bytes are not persisted. Dropped `config.knowledge.storage`, the
  knowledge PVC (`persistence.knowledge`), the `DATA_DIR` env, and the
  `SYNTHETICBREW_KNOWLEDGE_STORAGE` env. `knowledgeLoader` no longer requires
  `persistence.knowledge` — it uploads files over the REST API, which the engine
  chunks into pgvector; re-indexing requires a re-upload.

### Changed
- Bumps `appVersion` to **1.9.0** — the engine version that drops raw-file
  knowledge persistence and is always stateless for knowledge.

## [0.9.6] - 2026-06-11

### Changed
- Bumps `appVersion` to **1.7.1**. Engine 1.7.1 enforces `max_context_size` in real
  tokens (the previous chars/4 estimate undercounted tool-/JSON-heavy traffic by
  ~46% and skipped compression), counts the system prompt and tool schemas toward
  the budget, and makes the budget a hard ceiling. Also bounds `max_context_size` at
  the API layer and keeps parallel tool calls well-formed under compression. No
  chart template change.

## [0.9.5] - 2026-06-11

### Changed
- Bumps `appVersion` to **1.7.0**. Engine 1.7.0 owns the ReAct execution loop and,
  at a budget wall (`max_turn_duration` / `max_steps`), makes a tool-less final
  model call so the turn ends with a real summary of the gathered context instead
  of a canned apology; a live partial answer is no longer retracted. It also bounds
  `max_turn_duration` at the database layer (CHECK via migration 013) on top of the
  existing API validation and runtime clamp. No chart template change.

## [0.9.4] - 2026-06-10

### Changed
- Version hygiene (no engine behaviour change; `appVersion` stays **1.6.0**).
  Bumps the default `configApply.image.tag` to **0.5.0** (the brewctl release
  that adds `max_step_duration` GitOps pass-through) and refreshes the
  `tests/values/*` chart-test fixtures to the current published pair
  (engine 1.6.0 + brewctl 0.5.0), clearing long-stale 1.2.x/0.2.x pins.

## [0.9.3] - 2026-06-10

### Changed
- Bumps `appVersion` to **1.6.0**. Engine 1.6.0 ends budget-exhausted turns
  (`max_turn_duration` / `max_steps`) with a graceful answer instead of a dropped
  stream, adds an identical-argument tool-loop breaker, and adds the per-agent
  `max_step_duration` watchdog field (DB migration 012). No chart template
  changes; pulling 0.9.3 picks up the engine changes.

## [0.9.2] - 2026-06-07

### Changed
- Bumps `appVersion` to **1.5.0**. Engine 1.5.0 adds typed error codes on SSE
  error events, hard-stops runaway tool-error loops with a graceful final
  answer, and suppresses the empty final-answer blank bubble. No chart template
  changes; pulling 0.9.2 picks up the engine changes.

## [0.9.1] - 2026-05-29

### Changed
- Bumps `appVersion` to **1.4.1**. Engine 1.4.1 fixes the KG bundle
  re-apply self-collision (HTTP 409) caused by the in-memory tool
  registry source not excluding the bundle being re-applied. No chart
  template changes; pulling 0.9.1 picks up the engine fix.

## [0.9.0] - 2026-05-28

### Changed
- Bumps `appVersion` to **1.4.0**. Engine 1.4.0 ships KG query API
  ergonomics: batch `get_<entity>(ids[])` (BREAKING tool signature, REST
  single-id unchanged), range / multi-value filter operators, server-side
  sort with enum declaration-order semantics, `x-summary-fields` schema
  annotation for `list_<entity>_ids` projection mode. Bundles authored
  for 1.3.x continue to work without changes. See engine CHANGELOG 1.4.0
  for the full list + migration notes.

### Added
- New REST endpoint exposed by the chart's read-path readiness check:
  `POST /api/v1/knowledge-graphs/{bundle}/entities/{entity_type}/batch-get`.

## [0.7.4] - 2026-05-26

### Changed
- Bumps `appVersion` to **1.2.4**. Engine 1.2.4 fixes a recovery-
  classifier regression where chat turns aborted with `INTERNAL_ERROR`
  when an MCP tool returned `isError: true` with content containing
  certain phrases (e.g. `"permission denied"`), and cleans up two
  log-noise sources (`WARN persist chat session failed` after engine
  restart, and `ERROR failed to create session log directory` on
  read-only `logs/` mounts). No chart template changes; pull the
  new chart only to pick up the engine version bump.

## [0.7.3] - 2026-05-22

### Changed
- Bumps `appVersion` to **1.2.3**. Engine 1.2.3 is an admin SPA hotfix —
  the Test Flow tab now renders the chat UI when the schema page is
  reached by URL deep-link / reload (previously the empty-state guard
  shadowed the lockedSchemaId prop). No engine code changes; no chart
  template changes.

## [0.7.2] - 2026-05-22

### Changed
- Bumps `appVersion` to **1.2.2**. Engine 1.2.2 hardens the
  `show_structured_output` tool input contract (fail-loud on unknown
  fields and unknown `output_type` values, recursive lenient parser for
  stringified nested arrays, auto-id on single-question forms,
  `maxQuestions` raised 5 → 10). No chart template changes.
- No protocol changes — chart 0.7.x clients keep working unchanged.

## [0.7.0] - 2026-05-20

### Changed (breaking)
- Bumps `appVersion` to **1.2.0**. Engine 1.2.0 introduces the HITL Interrupt
  Primitive — a breaking change to the SSE contract for downstream chat
  clients that render widgets from `show_structured_output`. `tool_call` /
  `tool_result` events for that tool are no longer emitted; clients now
  receive `interrupt_request` / `interrupt_resume` events instead, and
  submit user choices via a new optional `resume_interrupt` field on the
  chat POST body. See the engine migration guide
  `docs/migration/v1.2-hitl-interrupt.md` for the client diff.

  The chart-level minor bump (0.6.x → 0.7.0) reflects the breaking semantic
  change in the bundled engine API even though no chart-template values are
  added or removed. Self-hosted operators upgrading from 0.6.x must
  coordinate with any custom chat-client integrations they ship alongside
  engine before adopting this chart version. Auto-applies a forward-only
  Liquibase changeset (`008_add_interrupts_table`) on first boot to create
  the per-tenant `interrupts` state-tracker table.

## [0.6.13] - 2026-05-18

### Changed
- Bumps `appVersion` to **1.1.11**. Engine 1.1.11 fixes the admin SPA's
  `/login` 404 after session expiry — local-mode 401 now re-mints a fresh
  token inline via `/api/v1/auth/local-session` without page reload, and
  external-mode redirect to the landing's `/login?return_to=` is preserved.
  Engine also emits a startup `WARN` when `auth.mode=local` and the HTTP
  listener binds to a non-loopback address, so operators can spot
  unauthenticated admin-API exposure during deployment.

### Added
- README **Securing self-hosted** section documents the trust model for
  `auth.mode=local` (dev / CI / VPN), recommends `auth.mode=external`
  fronted by an identity proxy (oauth2-proxy, Authelia, Cloudflare Access,
  etc.) for production self-hosted, and points headless automation at the
  long-lived `BOOTSTRAP_ADMIN_TOKEN` API token.

### Compatibility
- Chart-only metadata bump (`appVersion` sync + README). Default
  `image.tag` remains `"latest"` (operators are expected to pin
  explicitly per chart convention). Pin to `1.1.11` post-publish to pick
  up the admin SPA fix and the new startup warning. Drop-in upgrade from
  0.6.12.

## [0.6.12] - 2026-05-18

### Changed
- Bumps `appVersion` to **1.1.10**. Engine 1.1.10 is an admin SPA UX fix —
  the "Add Model" Display Name field now shows the URL-slug rule up front
  and renders precise inline errors (uppercase, spaces, other forbidden
  characters, leading/trailing hyphens) instead of a generic backend
  `invalid resource name` toast after submit. Behaviour-only; no template
  / values / DB change.

### Compatibility
- Chart-only metadata bump (`appVersion` sync only — no template / values
  changes). Default `image.tag` remains `"latest"` (operators are expected
  to pin explicitly per chart convention). Pin to `1.1.10` post-publish to
  pick up the admin SPA fix. Drop-in upgrade from 0.6.11.

## [0.6.11] - 2026-05-16

### Fixed
- **Release pipeline no longer overrides `appVersion` with chart version.**
  `.github/workflows/release-helm.yaml` previously passed
  `--app-version=${{ chart tag }}` to `helm package`, forcing both versions
  equal at publish time and shipping wrong `appVersion` metadata in every
  chart released since 0.6.x (0.6.10 shipped as `appVersion: 0.6.10` instead
  of `1.1.9`; 0.6.9 had the same bug; cosmetic only — `helm show chart` and
  `helm list` reported a non-existent engine version, but image pulls were
  unaffected because operators always set `image.tag` explicitly). The
  workflow now reads `appVersion` from `Chart.yaml`, and a follow-up
  verification step fails the release if the packaged tarball's `appVersion`
  diverges from the committed `Chart.yaml`.

### Compatibility
- Chart-only release. `appVersion` stays at **1.1.9**. No image / template
  changes vs 0.6.10 — only the published metadata is now correct.

## [0.6.10] - 2026-05-16

### Changed
- Bumps default `configApply.image.tag` to **0.2.5** (brewctl). brewctl
  0.2.5 propagates `mcp_servers[*].catalog_refresh_interval_seconds` from
  YAML config to the engine PATCH/CREATE body — before 0.2.5 the field was
  silently dropped at YAML decode (the desired struct lacked it), so
  operators driving engine 1.1.9's per-server TTL refresh from GitOps got
  zero effect from their YAML. Drop-in upgrade — existing values that pin
  `configApply.image.tag` to 0.2.4 continue to work, just bump them
  manually to 0.2.5 when ready.

### Compatibility
- Chart-only release. `appVersion` stays at **1.1.9** (engine unchanged).
- Removing the YAML line for `catalog_refresh_interval_seconds` after
  having set it is intentionally a no-op (`brewctl 0.2.5` preserves the
  engine value when the field is absent). To clear, use a direct curl
  PATCH with `null` or the Admin SPA. See brewctl CHANGELOG for the
  rationale.

## [0.6.9] - 2026-05-15

### Changed
- Bumps `appVersion` to **1.1.9** with multi-tenant correctness for the
  MCP subsystem: `MCP ClientRegistry` becomes per-tenant via
  `mcp.Manager`, `forward_headers` store is per-tenant, MCP server CRUD
  auto-reconnects the affected client (no more "Save not applied" via
  Admin SPA), and an optional per-server
  `catalog_refresh_interval_seconds` (30..86400) drives periodic
  `tools/list` refresh so downstream MCP rollouts (renamed/added tools,
  description changes) propagate without `kubectl rollout restart`.

### Database
- Engine 1.1.9 ships changeset **007_add_mcp_catalog_refresh_interval**
  (nullable `INTEGER` column on `mcp_servers` + `chk_mcp_refresh_range`
  CHECK 30..86400). Additive — pre-existing rows default to NULL (no
  background refresh). No backfill, no DB wipe.

Drop-in upgrade from chart 0.6.8. No breaking template / values
changes.

## [0.6.8] - 2026-05-14

### Changed
- Bumps `appVersion` to **1.1.8** with `openai_compatible` route
  hardening: transport-level logging of raw 4xx/5xx provider response
  bodies (operators can read full provider diagnostics from engine
  logs), outgoing JSON normalisation for bare `{"type":"object"}`
  schemas (OpenAI no longer 400s on no-arg MCP tools), early
  engine-side tool-name validation on OpenAI-strict routes (clear
  `INVALID_INPUT` replaces opaque upstream 400), and HITL halt-point
  enforcement for `show_structured_output` (prompt directive + react
  loop halt + content suppression + retract event) so models with
  weak tool-use boundaries cannot emit fabricated post-confirmation
  claims.
- Bumps default `configApply.image.tag` from `0.2.3` to `0.2.4`.
  brewctl 0.2.4 propagates `agents[].max_turn_duration` and
  `models[*].extra_body` YAML pass-through.

No DB schema changes, no breaking template / values changes, no DB
wipe. Drop-in upgrade from chart 0.6.7. Non-OpenAI flows (qwen, glm,
anthropic-via-OpenRouter, Ollama, Google, vLLM, llama.cpp) see no
behaviour change — only OpenAI-strict routes (direct OpenAI / Azure
OpenAI / OpenRouter routing to OpenAI families) are gated.

## [0.6.7] - 2026-05-13

### Changed
- Bumps `appVersion` to **1.1.7**: agent endpoints accept UUID-or-name on
  GET/PUT/PATCH/DELETE (closes external-consumer 404 when using UUIDs from
  the list response), and `models[*].extra_body` passthrough for
  `openai_compatible` providers (OpenRouter provider routing, etc.).

No DB schema changes, no breaking template / values changes, no DB wipe.
Drop-in upgrade from chart 0.6.6. brewctl default tag stays at `0.2.3` —
brewctl 0.2.4 (with `agents[].max_turn_duration` propagation and
`models[*].extra_body` YAML pass-through) ships separately; the next chart
release will bump the default. Operators wanting the new GitOps surface
before then can set `configApply.image.tag: "0.2.4"` once it's published.

## [0.6.6] - 2026-05-13

### Changed
- Bumps `appVersion` to **1.1.6** with the `tool_result.is_error` persistence
  fix. `GET /api/v1/sessions/{id}/messages` now surfaces `payload.is_error:
  true` for failed tool calls (MCP `isError`, circuit-breaker open, `[ERROR]`
  prefix, Eino tool error) — matching what the live SSE stream already
  carried via `tool_has_error`. Happy-path rows still omit the field
  (`omitempty`), so existing JSON parsers are unaffected.

No DB schema changes, no breaking template / values changes, no DB wipe.
Drop-in upgrade from chart 0.6.5. brewctl default tag remains `0.2.3`.

## [0.6.5] - 2026-05-11

### Changed
- Bumps `appVersion` to **1.1.5** with the symmetric UUID-or-name body-field
  resolvers (`POST /sessions schema_id`, `POST /knowledge-bases
  embedding_model_id`), the embedding-model lookup tenant-scoping fix
  (preemptive hardening), the new `per_page_max` pagination response field,
  and the `docs/api/auth-scopes.md` reference document.

No DB schema changes, no breaking template / values changes, no DB wipe.
Drop-in upgrade from chart 0.6.4. brewctl default tag remains `0.2.3`.

## [0.6.4] - 2026-05-10

### Changed
- Bumps `appVersion` to **1.1.4** with engine auth scope sweep, session
  per-user ACL hardening, the chat impersonation security fix, and the new
  opaque `sessions.metadata` JSONB column.

  Operator-visible behaviour:
  - `/api/v1/sessions/...`, `/api/v1/audit`, `/api/v1/settings`,
    `/api/v1/tools/metadata`, `/api/v1/resilience/circuit-breakers` now
    accept narrow-scope `api_token` (e.g. `scopes: ["sessions:read"]`)
    instead of requiring an admin JWT cookie. Existing admin tooling is
    unaffected — `ScopeAdmin` remains the superscope.
  - End-user JWT actors that previously had no `/sessions` access now see
    a per-user-scoped view: `?user_sub=other` is silently ignored, and
    cross-user GET by UUID returns 404. Trusted proxy `api_token` actors
    keep tenant-wide reads — chirp's ai-assistant pattern unchanged.
  - Chat impersonation via `POST /api/v1/schemas/{name}/chat` body
    `user_sub` field is closed. The api_token's name is the canonical
    identity for sessions/memories — clients that previously relied on
    body-level `user_sub` for tagging must re-architect.
  - New `metadata` JSONB column on `sessions` (additive migration 006).
    Empty-default `{}`, capped to 16KB on writes, returned in GET
    responses. Engine treats as opaque blob.

  No DB wipe required. Migration `006_add_sessions_metadata.yaml` is an
  additive `ALTER TABLE ADD COLUMN ... DEFAULT '{}'::jsonb` with a clean
  rollback that drops the column. Engine 1.1.3 sessions retain
  `metadata = {}` after upgrade.

### Tests
- Engine integration tests `TestSEC20`–`TestSEC28` cover scope enforcement
  on the new mounts, the impersonation guard on `POST /sessions`, and
  metadata round-trip.
- Multi-tenant + multi-user cross-user ACL tests live in `syntheticbrew-ee/tests/integration/`
  (paired PR / release).

## [0.6.3] - 2026-05-08

### Fixed
- Bumps `appVersion` to **1.1.3** + raises the bundled
  `configApply.image.tag` default from `0.2.1` to `0.2.3`. Pairs the
  engine-side fix for `CreateSchema` (now resolves `entry_agent_id`
  UUID-or-name, mirroring `UpdateSchema`) with the brewctl-side fix that
  passes the agent NAME on the wire instead of pre-resolving from a
  potentially empty `current.Agents` snapshot. Without the pair, a
  fresh-DB single `helm install` left `schemas.entry_agent_id` NULL and
  chat returned `INVALID_INPUT: schema has no entry agent`. Operators
  worked around it by re-running `configApply` once after install.
- Same fix shape applied to `agents[].model_id`: brewctl now sends the
  model NAME, engine `resolveAgentModel` / `PatchAgent` resolve. Catches
  the latent "agent silently inherits tenant default" path that didn't
  surface as a chat failure but produced wrong-model behaviour.
- Engine 1.1.3 also corrects `tool_tier.go.CoreToolNames()` to mirror the
  actual builtin registry (`manage_tasks`, `show_structured_output`,
  `spawn_agent`). `manage_subtasks` is a synonym for
  `manage_tasks(action=create_subtask, parent_task_id=...)`; `wait` is
  exposed as `spawn_agent(action="wait")`. Operators no longer see
  `unknown builtin tool` at agent runtime when listing the legacy names.

### Tests
- Engine integration test `TestSCH10_CreateSchemaWithEntryAgentName`
  (engine repo) covers the regression at the API layer — fresh-DB single
  apply with the agent NAME in `entry_agent_id` must populate the FK.
- chart-test fixtures (`tests/values/single-shot.yaml`,
  `knowledge.yaml`) pinned to `image.tag: "1.1.1"` +
  `configApply.image.tag: "0.2.2"` (last published pair) so the chart-test
  workflow runs without the chicken-and-egg of pulling unpublished
  binaries. A follow-up PR will bump both fixtures to 1.1.3 + 0.2.3 once
  those are pullable, and at that time the `smoke.sh` `entry_agent_name`
  assertion will be re-introduced as a second-layer guard.

## [0.6.2] - 2026-05-08

### Fixed
- Bumps `appVersion` to **1.1.2** with the engine name-validation gap
  closure for `models`, `agents`, `mcp_servers`. Engine 1.1.0 added
  `ValidateResourceName` + CHECK constraints to `schemas` +
  `knowledge_bases` but missed the other name-keyed tables — POST
  accepted invalid names, and the resulting rows were unreachable via
  the canonical URL once chi met `%2F`. Same preflight HALT semantics
  apply: operator must rename or delete violating rows before the new
  migration applies, no silent data loss.

## [0.6.1] - 2026-05-08

### Fixed
- Bumps `appVersion` to **1.1.1** with the engine hotfix for tenant
  provisioning. Engine 1.1.0 ships a CHECK constraint that the engine's
  own `SeedTenant` violates by hardcoding `"My Workspace"` as the default
  schema name — every signup returned 500 from EE provisioning and left
  the user without a default workspace. Pulling chart 0.6.1 with
  `image.tag` floating to `latest` (or pinned to `1.1.1`) is required
  for a working signup flow on Cloud / EE deployments.

## [0.6.0] - 2026-05-07

### Breaking
- **Schema and knowledge-base REST URLs are now name-keyed.** Bumps engine
  `appVersion` to **1.1.0**. All operator-facing endpoints under
  `/api/v1/schemas/{name}/...` and `/api/v1/knowledge-bases/{name}/...`
  now use the resource name in the URL segment (was UUID in 0.5.x). UUIDs
  remain internal storage detail (PK, FK integrity, audit, sessions).
  See engine/CHANGELOG.md 1.1.0 for the full endpoint list and migration
  notes.
- **Schema and knowledge-base names are immutable post-create.** PATCH/PUT
  with a different `name` field returns `409 Conflict`. The URL segment is
  now the stable consumer-facing handle for GitOps deploys.
- **Resource name format enforced at DB layer** via Liquibase CHECK
  constraint (regex `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`, max 100 chars).
  Migration runs a preflight that HALTs with a loud failure if any
  existing row violates the new format — operator must rename or delete
  offending rows before re-running `helmfile sync`. No silent data loss.

### Added
- **README "Stability matrix"** new row: `Schema/KB name-keyed URLs —
  Stable`.
- **Rollback runbook** in chart README documenting the recovery path if
  a 1.1.0 deploy needs to be rolled back to 1.0.x (kubectl rollout undo
  + Liquibase rollback + helm rollback semantics).

### Why this matters
Pre-0.6.0 GitOps consumers had to discover schema/KB UUIDs after each
`configApply` (UUIDs are auto-generated, env-specific, regenerated on DB
reset). The chart's `knowledgeLoader` Job already worked around this for
KBs by resolving name→UUID at runtime; consumers of the chat endpoint
had no equivalent. Engine 1.1.0 makes the operator-declared name the
canonical handle everywhere — `helmfile.yaml` references `support`,
consumers reference `support`, no UUID round-trips.

### Required values change
None. Existing values files continue to work — chart bundles updated
admin SPA + widget that use the new URLs internally. Only impact: any
external consumer hardcoding `/api/v1/schemas/<uuid>/chat` URLs must
switch to the schema name (declared in `configApply.config`).

## [0.5.0] - 2026-05-04

### Added
- **`knowledgeLoader` Job** — declarative Knowledge / RAG file ingest. Pairs
  with `configApply.config` bundle: declare KB metadata + agent linkage
  (`knowledge_bases:[{name, embedding_model}]` + `agents:[{knowledge_bases:[name]}]`),
  put document files in a ConfigMap, and the loader Job uploads each file
  via `POST /api/v1/knowledge-bases/{id}/files` on every `helm install` /
  `helm upgrade`. Idempotent across syncs (configurable mode):
  - `skip-existing` (default) — skip files whose name exists in KB
  - `replace` — skip if name+size match, else DELETE+POST
  - `always` — DELETE all KB files + re-upload everything
  - Optional `prune: true` — delete remote files not in ConfigMap

  Out-of-the-box GitOps for the most common case ("ship 7 markdown files
  with the chart") that previously required a hand-rolled bash Job in
  the operator's CI. Helm hook weight 15 — runs after configApply (10).
  Image: `alpine:3.19` + apk-installed curl + jq (~3s startup).

  See README "Integrations — Knowledge / RAG ingest" for the wiring example.

### Why now (a production deploy feedback)
A production deployment asked: "we have 7 markdown files, what's the
canonical way to load them?" Pre-0.5.0 answer was "post-deploy bash with
~30 lines of REST upload + SHA dedup". That pushes boilerplate to every CE
operator. 0.5.0 ships the Job natively — declare it in values, files land
on `helmfile sync`, no bash glue.

### Fixed
- **`DATA_DIR` env wired when `persistence.knowledge.enabled=true`.** Engine
  writes uploaded KB files to `{DATA_DIR}/knowledge/{tenant}/{kb}/`. Without
  `DATA_DIR` set, engine fell back to `os.UserConfigDir()` — empty in
  containers with no `HOME` — which collapsed to relative `./data` and
  failed every upload with `HTTP 500: create knowledge directory: mkdir
  data: permission denied` under `runAsNonRoot=true` (chart default). Chart
  now pins `DATA_DIR=/etc/syntheticbrew` so writes land inside the mounted
  knowledge PVC. **Render-time guard:** `knowledgeLoader.enabled=true`
  fails fast unless `persistence.knowledge.enabled=true` — pre-0.5.0
  combinations would render but every upload would 500.
- **knowledgeLoader Job: `restartPolicy: Never` + `backoffLimit: 0`.**
  Default `OnFailure + backoffLimit:1` deletes the failed pod after backoff
  exceeded → operators lose access to the actual error logs. Single-shot
  with `Never` keeps the failed pod in `Failed` state for `kubectl logs`
  inspection.
- **`knowledgeLoader.embeddingModel` workaround for brewctl 0.1.0.**
  brewctl 0.1.0 resolves `embedding_model: <name>` against pre-apply
  state when models and `knowledge_bases` are declared in the same
  bundle — the embedding model is not yet in `current.Models`, so
  brewctl creates the KB with empty `embedding_model_id` and every
  `POST /files` call returns `400 no embedding model configured`. The
  loader now probes the KB after `configApply` runs; if the link is
  missing, it resolves `knowledgeLoader.embeddingModel` to a model UUID
  and `PATCH`es the KB. Self-healing — becomes a no-op once brewctl
  0.1.1+ ships a two-pass apply.
- **Idempotent file skip with UUID prefix awareness.** Engine stores
  uploaded files on disk as `<uuid>_<original>`. The loader now strips
  this prefix when matching local filenames against remote, so
  `skip-existing` mode actually skips on re-runs instead of re-uploading
  every file on every `helm upgrade` (KB grew linearly per sync before).
- **Prune loop counter + DELETE error visibility.** Replaced
  `| while read` (which runs in a busybox-sh subshell — `PRUNED` never
  escaped, and `set -e` couldn't trip on `curl` failures inside the
  pipeline) with a tempfile-backed `while; done <` so the prune count
  surfaces in the summary line and any `DELETE /files/{id}` failure
  fails the Job loudly instead of silently swallowing.
- **Failed knowledgeLoader Jobs persist for log inspection.** Hook
  delete-policy switched from `before-hook-creation` to
  `before-hook-creation,hook-succeeded`. Successful runs are still
  reaped (no namespace bloat), but failed Jobs survive past the next
  `helm upgrade` so operators can run `kubectl logs job/...-knowledge-loader`
  even after retrying.

### Future (chart 0.5.x / engine 1.0.4+)
Engine currently does not expose `file_hash` on `GET /files` (DB has it,
API response struct doesn't). Once engine 1.0.4 surfaces the hash field,
loader will switch to content-perfect idempotency (skip if `sha256(local)
== file_hash(remote)`). Until then, `replace` mode uses `file_size` as a
proxy for content drift — catches most edits except length-preserving ones.

## [0.4.4] - 2026-05-04

### Fixed
- **Deployment update strategy deadlocked single-replica + RWO PVC.** Chart
  did not set `spec.strategy`, so k8s used the default RollingUpdate
  (maxSurge=25%, maxUnavailable=25%). For `auth.mode=local`, the chart pins
  replicaCount=1 with a ReadWriteOnce PVC for the JWT keypair. RollingUpdate
  creates the new pod *before* deleting the old one — but the new pod
  deadlocks Pending because the old pod still holds the RWO PVC attachment.
  `helm upgrade --atomic` times out and rolls back to the previous chart
  version. Caught by a production canary when bumping 0.4.2 → 0.4.3 — atomic
  rollback fired ~10 min in.
- Now defaults to `strategy.type: Recreate` whenever `auth.mode=local`. Old
  pod is killed first → PVC released → new pod attaches and starts.
  `auth.mode=external` (HA, no PVC contention) keeps the k8s default
  RollingUpdate.

### Added
- `deploymentStrategy:` value for explicit override. Empty (default) →
  the auto-rule above kicks in. Set to a full strategy block (e.g.
  `{type: RollingUpdate, rollingUpdate: {maxSurge: 25%, maxUnavailable: 25%}}`)
  to force RollingUpdate on RWX storage or other non-RWO setups.

### Changed
- Bumped chart `version` to `0.4.4`. `appVersion` stays at `1.0.3` — no
  engine code change in this release.

## [0.4.3] - 2026-05-03

### Fixed (engine v1.0.3)
- **PATCH /models did not normalize type aliases.** POST /models accepts
  `type: openrouter` and canonicalizes to `openai_compatible` (chk_models_type
  enum: ollama, openai_compatible, anthropic, azure_openai). PATCH had no
  matching normalization, so brewctl reconcile after a Create-with-alias hit
  API 500 → Job BackoffLimitExceeded → Helm upgrade failed. Patch now mirrors
  Create's validation + alias rewrite. Bumped engine appVersion 1.0.2 → 1.0.3.

### Fixed (chart)
- **configApply Job silently no-op'd on real deploys** — chart pointed brewctl
  at `-f /etc/syntheticbrew/config` (a ConfigMap-mounted directory). brewctl's
  loader walks subdirectories `models/`, `agents/`, `schemas/` only and
  ignores top-level files in the dir. The ConfigMap renders the inline
  `configApply.config` value as a single file `syntheticbrew.yaml` at the dir
  root, so brewctl found zero subdirs → empty desired state → "No changes"
  → Job Completed → false success. Caught by production canary deploy:
  Job logs said `No changes.` but engine had no smoke model/agent/schema.
  Now points brewctl at `-f /etc/syntheticbrew/config/syntheticbrew.yaml` (explicit
  file path).
- **`configApply.config` example removed `apiVersion + kind: Config` wrapper**
  — brewctl's loader treats any file with a `kind` field as a single-resource
  manifest (Model | MCPServer | KnowledgeBase | Agent | Schema). `Config` is
  not in that list, so the loader would have errored if it had reached the
  file. Bundle format uses top-level `models:`, `agents:`, `schemas:` etc
  arrays only — no `apiVersion`/`kind` wrapper at the top.

### Changed
- `tests/values/single-shot.yaml` now applies a real bundle (1 model + 1
  agent + 1 schema) instead of `models: []`. The empty-array form passed
  v0.4.2 chart-test as a false positive.
- `tests/scripts/smoke.sh` now asserts the smoke resources actually landed
  in engine (`/api/v1/{models,agents,schemas}` each contains one
  `kind-smoke-*` row) — guards against the regression class above.
- `tests/fixtures/postgres-pgvector.yaml` Secret carries a placeholder
  `OPENROUTER_API_KEY` so `${OPENROUTER_API_KEY}` substitution in the
  smoke model definition resolves. Smoke does not invoke real LLM.

### Why this matters for a production deploy
The production install:dev pipeline reported success (engine 1/1 Running, Job
Completed), but `/api/v1/models` came back empty — brewctl had silently
no-op'd. Bumping the operator's `helmfile.yaml.gotmpl` to `version: 0.4.3` and
re-running `helmfile -e dev sync` reconciles via `helm upgrade` →
configApply Job re-runs with the file path fix → brewctl actually creates
the smoke bundle.

### Compatibility matrix
- chart 0.4.3 + engine 1.0.3 (matching `appVersion`) — recommended; full
  fix coverage including PATCH alias normalization (Patch handler now
  mirrors Create).
- chart 0.4.3 + engine 1.0.2 — works as long as `configApply.config`
  uses canonical types (`openai_compatible`, not the `openrouter`
  alias). With the alias, first install succeeds (Create normalizes)
  but the second `helm upgrade` reconcile triggers PATCH, which engine
  1.0.2 rejects on `chk_models_type`. Stay on canonical types or bump
  the engine.
- chart 0.4.3 + engine 1.0.1 — same constraint as 1.0.2 plus loses
  fail-fast on invalid bootstrap admin token (silent skip-seed instead).

### Operator constraint — `configApply.existingConfigMap`
If you bring your own ConfigMap via `configApply.existingConfigMap`, the
data key MUST be `syntheticbrew.yaml` — the Job invokes brewctl with the
explicit file path `/etc/syntheticbrew/config/syntheticbrew.yaml`. Configurable
filename is tracked for chart v0.5.x.

## [0.4.2] - 2026-04-30

### Fixed
- **ServiceAccount hook ordering** — SA was a regular resource, but the
  pre-install migrations Job depends on it. On first install Helm tried to
  run the Job before creating the SA → `serviceaccount "..." not found`.
  SA template now declares `helm.sh/hook: pre-install,pre-upgrade` with
  weight `-10` and `before-hook-creation` delete policy so it always
  exists in time for hook Jobs.
- **Engine `HOME` not set** — engine writes its `server.port` discovery
  file to `~/.local/share/syntheticbrew/`. Under `runAsUser: 1000` without
  `HOME` set, the path resolved to `/.local`, which is not writable →
  `mkdir /.local: permission denied` → CrashLoopBackOff. Deployment
  template now sets `HOME=/tmp` explicitly.
- **Migrations Job no args** — the `syntheticinc/syntheticbrew-migrations` image is
  stock liquibase (entrypoint `/liquibase/docker-entrypoint.sh`, default
  Cmd `--help`). The chart Job did not pass any args → migrations never
  ran, the Job exited 0 after printing liquibase help, engine then crashed
  on `relation "agents" does not exist`. Job now overrides `command` with
  a POSIX shell wrapper that parses libpq `DATABASE_URL` into JDBC URL +
  URL-decoded username/password, then `exec`s the entrypoint with
  `--changeLogFile=migrations/db.changelog-master.yaml update`.
- **brewctl image tag** — chart default was `v0.1.0`, but the published
  GHCR tag is `0.1.0` (no `v` prefix). Default values now use `"0.1.0"`.
- **brewctl `command` override** — chart used `command: ["brewctl", ...]`
  but the brewctl image entrypoint is `/brewctl` (binary at root, not in
  PATH) → `executable file not found in $PATH`. Job now uses `args: [...]`
  so the entrypoint stays in effect.

### Security
- **No password leak via process argv** — migrations Job previously passed
  `--password=$DB_PASS` on liquibase argv, exposing the DB password to
  anyone with `pods/exec` or `ps -ef` rights inside the container. Now
  passed via `LIQUIBASE_COMMAND_PASSWORD` env var which never appears in
  argv. Same for username (`LIQUIBASE_COMMAND_USERNAME`).
- **DSN URL-decoding** — `DATABASE_URL` containing URL-encoded characters
  (e.g. password with `@` → `%40`) is now decoded before handoff to
  Liquibase. JDBC PostgreSQL driver does NOT URL-decode credentials, so
  passing the encoded form would have failed authentication on real-world
  managed Postgres credentials. Decoder is POSIX `printf '%b' / sed`.

### Added
- **Auto `/tmp` emptyDir when `readOnlyRootFilesystem: true`** — Deployment,
  migrations Job, and configApply Job now automatically mount an in-memory
  `/tmp` when the security context enables read-only root. Previously users
  had to manually wire `extraVolumes` + `extraVolumeMounts` and engine /
  Liquibase / brewctl would CrashLoop on first temp file write.
- **`replicaCount=1` guard for `auth.mode=local`** — chart now `fail`s at
  template time with a clear message if `replicaCount > 1` while
  `auth.mode=local`. Local mode persists JWT keypair on a single-writer
  PVC; multi-replica would race.
- `tests/` directory with kind-based smoke fixtures (excluded from
  `helm package` artefact via `.helmignore`):
  - `tests/values/default.yaml` — vanilla install with in-kind
    postgres-pgvector, no bootstrap token, no configApply
  - `tests/values/single-shot.yaml` — GitOps-style flow with
    bootstrap admin token + configApply Helm hook
  - `tests/fixtures/postgres-pgvector.yaml` — Secret + ConfigMap +
    StatefulSet + Service for an in-kind pgvector Postgres. Init script
    is idempotent (re-runnable across smoke iterations) and the
    readiness probe verifies the `syntheticbrew` DB exists before signalling
    ready, eliminating a init-race flake on slow CI runners.
  - `tests/scripts/smoke.sh` — `/health` + admin REST endpoints +
    configApply Job completion check
- GitHub Actions workflow `.github/workflows/chart-test.yml` — static
  helm lint + render matrix with **regression-pinned greps** for each
  fixed bug, plus a kind v1.30 integration job that explicitly verifies
  the migrations Job ran Liquibase (not the transitive symptom of
  engine boot success). Triggered on PRs and pushes touching the chart.
- `.helmignore` — excludes `tests/` from the deployable chart artefact
  (test admin-token + smoke scripts must not leak into the OCI package).

### Changed
- Bumped chart `version` to `0.4.2`.
- Bumped `appVersion` to `1.0.2` (engine fail-fast on invalid bootstrap
  admin token format — see engine v1.0.2 release).

### Known limitations
- The ServiceAccount is rendered as a Helm hook (so it exists before
  pre-install Jobs). Hook resources are not tracked as release resources,
  so `helm uninstall` does NOT delete the SA — it is orphaned in the
  namespace until the namespace itself is deleted or the SA is manually
  removed (`kubectl delete sa <release>-syntheticbrew-engine`). On `helm
  upgrade` there is a brief sub-second window during pre-upgrade hook
  re-creation where the SA does not exist; existing pods retain their
  cached token, but new pods scheduled in that window will retry
  creation. Both limitations will be removed in v0.5.0 by moving
  migrations from a pre-install Helm hook to a Deployment init container.
- `helm rollback` does NOT downgrade the database schema. Liquibase
  `update` is forward-only. If you roll back to a chart revision whose
  engine image expects an older schema, the engine will crash on first
  DB read. Take a `pg_dump` before any upgrade and restore alongside the
  chart rollback. Documented in README "Known limitations".

## [0.4.1] - 2026-04-30

### Added
- `bootstrapAdminToken` section: when enabled, engine pod receives
  `SYNTHETICBREW_BOOTSTRAP_ADMIN_TOKEN` env from a Secret. Engine v1.0.1+ seeds
  an admin API token in `api_tokens` on first boot, enabling single-shot
  GitOps deploy with `configApply.enabled=true` (no manual Admin UI token
  generation).
- Pair with `configApply.tokenSecret` pointing at the same Secret/key for
  DRY config: one Vault entry, two consumers (engine boot + brewctl Job).

### Changed
- Bumped `appVersion` to `1.0.1` (BOOTSTRAP_ADMIN_TOKEN feature requires it).
- Bumped chart `version` to `0.4.1`.

## [0.4.0] - 2026-04-30

### Added
- HTTPRoute template (Gateway API support) — opt-in via `httpRoute.enabled`. Tested with Envoy Gateway, Cilium Gateway API, and Istio Gateway.
- ServiceAccount template with configurable annotations for AWS IRSA and GCP Workload Identity. Toggle via `serviceAccount.create` (default: `true`).
- NetworkPolicy template — opt-in via `networkPolicy.enabled`. Configurable `ingressFrom` selectors; egress unrestricted by default (DNS, Postgres, LLM API).
- Escape hatches: `extraEnv`, `extraEnvFrom`, `extraVolumes`, `extraVolumeMounts`, `extraInitContainers`, `podAnnotations`, `podLabels` — applied to Deployment and Job pods.
- `podSecurityContext` (defaults: `fsGroup: 1000`, `runAsNonRoot: true`, `runAsUser: 1000`) and `containerSecurityContext` (defaults: `allowPrivilegeEscalation: false`, `readOnlyRootFilesystem: false`, `capabilities.drop: [ALL]`, `seccompProfile: RuntimeDefault`). `readOnlyRootFilesystem` is `false` by default to avoid CrashLoopBackOff from engine `/tmp` writes; opt-in pattern documented in README.
- `service.annotations` for cloud load-balancer hints (e.g. `service.beta.kubernetes.io/aws-load-balancer-internal`).
- README "Integrations" section with copy-paste examples for helmfile, External Secrets Operator + Vault, AWS IRSA, GCP Workload Identity, Argo CD, and read-only root filesystem opt-in.

## [0.3.0] - 2026-04-29

### Added
- Liquibase migrations Job (`pre-install,pre-upgrade` Helm hook). Runs the
  `syntheticinc/syntheticbrew-migrations` image against `DATABASE_URL` before every
  install or upgrade. Toggle via `migrations.enabled` (default: `true`).
- `brewctl` config-apply Job (`post-install,post-upgrade` Helm hook) for
  declarative GitOps reconcile via the `brewctl` CLI. Waits for engine
  readiness via an init container before applying. Optional via
  `configApply.enabled` (default: `false`).
  **Prerequisites:** brewctl Docker image (`ghcr.io/syntheticinc/brewctl:v0.1.0`
  or later) must be published before enabling `configApply.enabled=true`. See
  [syntheticbrew-brewctl releases](https://github.com/syntheticinc/syntheticbrew-brewctl/releases).
- ConfigMap template (`configmap-syntheticbrew.yaml`) for inline `syntheticbrew.yaml`
  config-as-code. Rendered only when `configApply.enabled=true` and
  `configApply.config` is non-empty and `configApply.existingConfigMap` is unset.
- Argo CD Application example (`examples/argocd-application.yaml`) with both
  Git-based and OCI-based source variants.
- GitHub Actions workflow `release-helm.yaml` publishing chart to
  `ghcr.io/syntheticinc/charts/syntheticbrew-engine` on `helm/v*.*.*` tags.

### Changed
- Bumped chart version to `0.3.0`.
- `NOTES.txt` updated with config-apply bootstrap instructions (step 3).

## [0.2.0] - earlier

Initial CE chart with engine Deployment, Service, Ingress (sticky sessions for
SSE), PVC for JWT keys, HPA, ServiceMonitor, and ConfigMap for `agents.yaml`.
