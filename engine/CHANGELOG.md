# Changelog

## [1.2.0] — 2026-05-20

HITL Interrupt Primitive + Bug 1 wrap-LLM-only refactor (Chirp 2026-05-20 report).

### Breaking changes
- **`show_structured_output` no longer emits `tool_call` / `tool_result` SSE
  events to the client.** Engine 1.1.x clients that rendered widgets from the
  tool_call event arguments will see the widget never appear. Replaced by the
  new `interrupt_request` / `interrupt_resume` SSE events — see migration
  guide `docs/migration/v1.2-hitl-interrupt.md`. LLM-context flow is preserved
  (eino still sees the tool result for accumulated messages), so the agent's
  internal reasoning is unchanged; only the wire format to chat clients moved.
- Two new `SessionEventType` enum values added to `flow_service.pb.go`:
  `SESSION_EVENT_INTERRUPT_REQUEST = 12`, `SESSION_EVENT_INTERRUPT_RESUME = 13`.
  Existing clients silently ignore them (verified — admin SPA, embed widget,
  and chirp's parser all have no-default switches on unknown event types).
- POST `/api/v1/schemas/{name}/chat` accepts a new optional `resume_interrupt`
  field as a mutually-exclusive alternative to `message`. Sending both → 400.
  Sending neither → 400. Existing clients sending only `message` keep working.

### Added
- **HITL Interrupt Primitive** as a first-class concept (server-issued halt
  point with explicit resume). New domain entities in
  `internal/domain/interrupt.go`:
  - `Interrupt` — pure state-tracker row (id, tenant_id, request_event_id,
    status, resolve_event_id, created_at); kind/schema/payload live in linked
    session_event_log rows
  - `InterruptStatus` (pending / resolved / abandoned)
  - `InterruptKind` discriminator (currently only `structured_output`;
    extensible to file_pick / voice / wizard without protocol changes)
  - `InterruptRequestPayload` / `InterruptResumePayload` wire envelopes
- **New `interrupts` table** (Liquibase changeset 008) — 6-column pure state
  tracker with FK linkage to `session_event_log(id)` for request and resolve
  events. Indexed on `(tenant_id, status) WHERE status='pending'` for fast
  resume-validation lookup and on `request_event_id` for the JOIN that
  recovers kind/schema. Cloud-first scoping enforced via per-tenant column +
  `tenantScope` helper.
- `GORMInterruptRepository` (`internal/infrastructure/persistence/configrepo/interrupt_repository.go`)
  with `Create`, `Get`, `LoadWithRequestEvent` (single-JOIN lookup that returns
  Interrupt + its request event row in one round-trip), `MarkResolved`
  (atomic conditional UPDATE on status='pending' to safely handle concurrent
  resume races → 409), `MarkAbandonedForSession`.
- `chatServiceHTTPAdapter.ResumeInterrupt` — validates tenant + session +
  status, persists the `interrupt_resume` event row, atomically marks the
  interrupts row resolved, reconstructs a Q+A user message from the original
  widget schema + answers (preferring human labels over raw codes), and
  resumes the React loop by injecting that message.
- `wrapContentForLLMContext` helper in `internal/service/engine/llm_content_wrap.go` —
  applies prompt-injection markers ONLY when assembling
  `schema.Message{Role:Tool}` for the next LLM iteration. SSE / history /
  audit consumers receive raw content from the tool. Closes Chirp Bug 1
  ("SafeToolWrapper markers leak into the client SSE stream").
- `EventStream.persistAndPublish` auto-creates the `interrupts` state-tracker
  row when persisting an `interrupt_request` event, wiring `request_event_id`
  FK in the same transaction as the event row insert.
- Admin SPA `InterruptWidget.tsx` reference renderer (form / summary_table /
  info modes). Embeddable widget bundle (`engine/widget/`) ships matching DOM
  rendering — both swallow widget-submit through `POST resume_interrupt` and
  surface the just-emitted `interrupt_resume` SSE event as an "answered"
  visual state, never a duplicate chat bubble. Closes Chirp Bug 2 ("widget
  submit duplicates value in chat").
- AI Builder Assistant agent now has `show_structured_output` in its
  available tools and a prompt section guiding when to use it (bounded
  confirmation, capability tier selection, model preset picking) vs plain
  text (open discovery questions, free-form names).

### Removed
- `SafeToolWrapper` struct + `NewSafeToolWrapper` + `wrapContent` from
  `internal/infrastructure/tools/safe_tool_wrapper.go`. Its only sibling,
  `CancellableToolWrapper`, remains. The 8 wrapping behaviours from the old
  test file are now exercised by
  `internal/service/engine/llm_content_wrap_test.go` at the new layer.
- `internal/service/engine/message_collector.go::stripToolOutputMarkers` —
  no longer needed since tool callbacks now receive raw content directly.
- `domain.EventTypeStructuredOutput` removed; replaced by
  `EventTypeInterruptRequest`. Persisted 1.1.x rows with this type fall
  through `convertEvent` default (returns nil).

### Changed
- `NewEventStream` and `sessionprocessor.New` now accept an `InterruptCreator`
  (nil-safe — passes through when no DB is wired). Test callers updated to
  pass nil.
- `ChatService` interface gains a second method `ResumeInterrupt`. The
  in-tree `chatServiceHTTPAdapter` and the BYOK test fake both implement it.

### Migration
See `docs/migration/v1.2-hitl-interrupt.md` for downstream client guidance —
the suppression of `tool_call`/`tool_result` SSE events for
`show_structured_output` requires clients to render widgets from the new
`interrupt_request` event instead, and submit via
`POST {resume_interrupt: {interrupt_id, payload}}` instead of forging a
synthetic user message.

## [1.1.11] — 2026-05-18

Admin SPA session-expiry recovery + local-mode bind-exposure warning.

### Fixed
- **Admin SPA: stale-JWT 401 no longer redirects to a non-existent `/login`
  route** (`engine/admin/src/api/client.ts`). The previous handler hard-coded
  `window.location.href = '/login?reason=session_expired'` regardless of
  basename, so after a 1h session expired the SPA bounced to `/login` —
  outside the `/admin/` mount — and the host's Caddy/edge fell through to a
  404. There is no `/login` route inside the admin SPA; the comment claiming
  otherwise was stale.

  Replaced `redirectToLoginOn401` with `handleUnauthorized` which routes by
  active auth mode:
  - `VITE_AUTH_MODE=local` (self-hosted): dynamically imports
    `bootstrapAuth` and re-mints a fresh token via
    `POST /api/v1/auth/local-session` inline — no page reload, no redirect.
    A module-scoped `recovering` flag de-duplicates simultaneous 401s from
    parallel in-flight requests.
  - `VITE_AUTH_MODE=external` with `VITE_LANDING_URL`: redirects to
    `${VITE_LANDING_URL}/login?return_to=<current>&reason=session_expired`
    (unchanged from previous behaviour).
  - `VITE_AUTH_MODE=external` without `VITE_LANDING_URL`: throws a
    build-config error so the misconfiguration is loud rather than silently
    routing to nowhere.

  Vitest coverage (`engine/admin/src/api/client.test.ts`) adds four cases —
  local-mode re-mint without redirect, external+landing redirect shape,
  external-without-landing throw, and idempotency across parallel 401s.

### Added
- **Engine: startup `WARN` when `BYTEBREW_AUTH_MODE=local` and the HTTP
  listener is bound to a non-loopback address**
  (`engine/internal/app/server.go`). Local auth mode has no real
  authentication — any request reaching the listen address can mint an
  admin session — so a public bind silently exposes the admin API. The
  warning surfaces at startup with the offending host/port so operators
  catch the misconfiguration before the next bug report. Loopback binds
  (`127.0.0.1` / `::1` / `localhost`) and any `AUTH_MODE=external` setup
  remain silent. Behaviour-only; no API or DB change. Drop-in upgrade from
  1.1.10.

## [1.1.10] — 2026-05-18

Admin SPA UX: clarify Display Name validation when adding a model.

### Fixed
- **Models page: "Add Model" Display Name now teaches the rule up front and
  surfaces precise inline errors** (`engine/admin/src/pages/ModelsPage.tsx`).
  The Display Name field is the URL slug — validated server-side against
  the DNS-label regex `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$` enforced for every
  operator-facing resource (`name_validation.go`). Previously the form
  shipped an unconstrained input with placeholder `my-llama` only, and any
  violation surfaced as a generic toast `invalid resource name` from the
  backend after submit. Users hitting the rule with `My-Llama`, `my llama`,
  `my_llama`, or `-bad` had no way to learn what the constraint actually
  was; uppercase rejection in particular was not discoverable.

  Now mirrors `AgentsPage.tsx`: a permanent hint under the field shows the
  rule (`URL slug: lowercase letters, digits, hyphens. Must start and end
  with a letter or digit (e.g. "my-llama", "glm-4").`), and a client-side
  validator (`validateModelDisplayName`) renders a distinct inline error
  for each failure shape — uppercase, spaces, other forbidden characters,
  leading/trailing hyphens, length cap, UUID-shape, reserved name. Submit
  short-circuits with the same toast if the user bypasses the inline error,
  so the backend is never asked to validate a known-bad value.

  Behaviour-only fix; no API or DB change. Engine binary embeds the rebuilt
  admin bundle. Drop-in upgrade from 1.1.9.

## [1.1.9] — 2026-05-15

Multi-tenant correctness for the MCP subsystem. The `MCP ClientRegistry` was
process-global keyed by server name; in multi-tenant deployments tenant A
calling `/api/v1/config/reload` would `CloseAll()` every tenant's MCP
clients before reconnecting only its own — a long-standing cross-tenant
side-effect that's been silent because no Cloud customer hit the exact
collision. This release rebuilds the boundary as `mcp.Manager` (mirroring
`agentregistry.Manager`), adds auto-reconnect on MCP server CRUD, and adds
an optional per-server `tools/list` refresh interval so downstream renames
or description changes propagate without `kubectl rollout restart`.

DB: changeset 007 adds nullable `mcp_servers.catalog_refresh_interval_seconds`
+ CHECK 30..86400. Additive — drop-in upgrade from 1.1.8.

### Added
- **Tenant-aware `mcp.Manager`** (`internal/infrastructure/mcp/manager.go`).
  Process-global `ClientRegistry` is gone; the boundary is now a Manager
  that holds either a singleton (`perTenant=false`, CE) or a lazy
  per-tenant map (`perTenant=true`, Cloud) of `*ClientRegistry` instances.
  `GetForContext(ctx)` resolves the registry from the request's tenant_id
  with a double-check lock, identical pattern to `agentregistry.Manager`.
  Tenant A's `/config/reload` (or any CRUD) can no longer affect tenant
  B's MCP clients.

- **Per-tenant `ForwardHeadersStore`**
  (`internal/infrastructure/mcp/forward_headers.go`). The previous
  process-global `atomic.Value` carrying the union of MCP `forward_headers`
  was rewritten as a per-tenant slot. ChatHandler reads via
  `GetForContext(r.Context())` so each request sees only its own tenant's
  whitelist. CE behaviour is unchanged (`isPerTenant=false` collapses to
  a single slot).

- **Auto-reconnect after MCP server CRUD**
  (`internal/app/http_adapters_extra.go`). Successful POST/PUT/PATCH on
  `/api/v1/mcp-servers/{name}` triggers
  `Manager.ReconnectServer(ctx, tenantID, name)` — close stale per-server
  client, redial, swap. DELETE triggers `DisconnectServer`. Per-server
  granularity (PATCH on `chirp-tools` does NOT bounce `slack-bot`).
  Failure to reconnect is logged at WARN but does not fail the CRUD
  response — DB is source of truth, runtime catches up at next
  reconnect/refresh. **Closes Admin SPA "Save not applied" UX bug** —
  `MCPPage.tsx` no longer needs to call `/api/v1/config/reload` after
  save (it never did).

- **Optional per-server periodic `tools/list` refresh**
  (`internal/infrastructure/mcp/refresher.go`). New nullable column
  `mcp_servers.catalog_refresh_interval_seconds INTEGER` (range 30..86400
  enforced via DB CHECK + API validation). Refresher schedules one
  goroutine per `(tenantID, serverName)`, bound to engine-process root
  context. Each tick re-issues `tools/list` and atomically swaps the
  cached tools under `Client.mu`; diff (added/removed) is logged at
  INFO. NULL = disabled (default). Closes the chirp use case where a
  downstream MCP image rolls with a renamed/added/described tool but
  the engine pod kept serving the boot-time catalog. NULL-default means
  zero behaviour change for existing rows.

- **Tenant-scoped reload path**
  (`internal/app/http_adapters.go`). `configReloaderHTTPAdapter.Reload`
  now calls `ReconnectTenant(ctx, tenantIDFromCtx(ctx))` instead of the
  legacy global `CloseAll() + ConnectAll()`. Reload by tenant A no
  longer reaches tenant B.

- **`POST /api/v1/mcp-servers/{name}/refresh` endpoint** + Admin SPA
  "Refresh" action (`internal/delivery/http/mcp_handler.go`,
  `admin/src/pages/MCPPage.tsx`). Lightweight on-demand re-fetch of
  one server's `tools/list` without recreating the transport — cheaper
  than the full `ReconnectServer` (close + redial) path for the case
  "downstream rolled out renamed tools but session is alive". Returns
  `{name, tools_count}` on 200, 404 when the server is not registered
  in the runtime registry. The Admin SPA's MCP detail panel surfaces
  it as a Refresh button next to Edit / Reset Breaker, with a toast on
  success/error. Form gains a `Catalog refresh interval (seconds)`
  input that wires `catalog_refresh_interval_seconds` end-to-end.

### Changed
- `MCPClientProvider.GetMCPTools` signature: `(name string)` →
  `(ctx context.Context, name string)`. The two production call sites
  in `builtin_tool_store.go` (legacy `Resolve` and `resolveMCPTools`)
  were updated; the latter required threading `Ctx context.Context`
  into `ResolveContext`. Tools now resolve through the per-tenant
  registry returned by `Manager.GetForContext(ctx)`.
- `forwardHeadersFn` signature: `func() []string` →
  `func(context.Context) []string`. ChatHandler and admin assistant
  pass `r.Context()`.
- `mcpServerRepo.List(ctx)` is now a thin wrapper over the new
  `ListForTenant(ctx, tenantID)` (called by Manager from background
  paths that don't carry an HTTP request context).
- Internal: removed dead `Handler.Routes()` methods, tests now mirror
  production routing 1:1.

### Operational notes
- **Drop-in upgrade from 1.1.8.** No env vars added (per-server config
  lives in DB). No metrics added. The 007 changeset is additive — pre-
  existing rows get `catalog_refresh_interval_seconds = NULL` (no
  refresh) and require no backfill.
- **CE behaviour unchanged at runtime.** Single-tenant CE deployments
  continue to use the sentinel tenant; the Manager's `Init()` eagerly
  loads the singleton at boot exactly like 1.1.8 did.
- **Cloud lazy-load.** First chat request from a cold tenant triggers
  Manager to dial that tenant's MCP servers (same lazy pattern that
  `agentregistry.Manager` already uses). Connect timeout in connector
  caps worst-case latency at 10s.

## [1.1.8] — 2026-05-14

Hardening for `openai_compatible` LLM routes — observability on upstream
errors, schema normalisation, tool-name validation, and HITL halt-point
enforcement. No DB schema changes, drop-in upgrade from 1.1.7.

### Added
- **HTTP transport: log raw provider response body on 4xx/5xx**
  (`internal/infrastructure/llm/response_logging_transport.go`). Wraps the
  outermost openai/openai_compatible HTTP client; on any non-2xx response,
  reads up to 16 KiB of the body and logs it at ERROR with `status`,
  redacted `url`, and a `truncated` flag, then restores the body for
  downstream parsers. Eino-ext otherwise collapses rich upstream error
  payloads (e.g. OpenRouter's `error.metadata.raw`) into opaque
  "Provider returned error" strings; operators can now diagnose
  schema/payload issues from inside the cluster.

- **HTTP transport: normalize empty `properties` for OpenAI tool schemas**
  (`internal/infrastructure/llm/properties_normalizing_transport.go`).
  Walks outgoing `tools[*].function.parameters` JSON; when a schema has
  `type: object` but no `properties` key, inserts `properties: {}`.
  OpenAI rejects bare `{"type":"object"}` with 400
  `object schema missing properties`; other providers tolerate it.
  Gated on `IsOpenAIStrictRoute` so non-OpenAI flows pay no per-request
  JSON re-marshal cost.

- **Early validation of tool names for OpenAI-strict routes**
  (`internal/infrastructure/agents/react/agent.go`). When the resolved
  route is OpenAI-strict — provider type `openai`, or `openai_compatible`
  with a base URL on `api.openai.com` / `*.openai.azure.com` or a model
  slug matching OpenAI families (`openai/`, `azure/`, `gpt-`, `o1`, `o3`,
  `o4`, `chatgpt-`, `text-davinci-`) — tool names not matching OpenAI's
  `^[a-zA-Z0-9_-]+$` regex produce a clear `INVALID_INPUT` error naming
  the offending tool BEFORE any upstream call. Non-OpenAI flows (qwen,
  glm, anthropic-via-OpenRouter, Ollama, Google, vLLM, llama.cpp) are
  unaffected and continue to accept the dotted MCP convention
  (`device.list`, `alarm.definition.create`).

- **`llm.IsOpenAIStrictRoute(providerType, modelName, baseURL)`** —
  shared route-classification helper used by tool-name validation and
  the properties-normalising transport gate. Single definition + test
  matrix (`internal/infrastructure/llm/openai_strict_test.go`) covers
  direct OpenAI, Azure, OpenRouter routing slugs, bare GPT/o-series
  names, non-OpenAI flows, and empty inputs.

- **HITL halt-point semantics** for `show_structured_output`:
  - System-prompt directive injected by `MessageModifier` when an agent
    has any HITL tool, instructing the model to emit ONLY the tool call.
  - `tools/structured_output_tool.go` calls `react.SetReturnDirectly`
    so Eino halts the loop instead of feeding the tool_result back to
    the LLM. Without this, the model would otherwise produce a
    follow-up assistant message claiming actions no tool has run.
  - `callbacks.ModelEventHandler.MarkHITLSeen` / `HITLSeen` track the
    flag; `FinalizeAccumulatedText` drops accumulated prose on a HITL
    turn; `Agent.Stream` / `Agent.RunWithCallbacks` skip the final
    `EventTypeAnswer` event so history is clean.
  - New `EventTypeRetractAssistant` SSE event so streaming consumers
    can scrub already-delivered chunks. Non-streaming aggregator in
    `chat_handler.handleNonStreaming` resets `message` on the event
    and as a belt-and-suspenders on the HITL tool_call event itself.
  - Strict-boundary models (Claude, GPT-4) are unaffected — they
    already emit empty `content` alongside `tool_calls`.

### Changed
- `internal/infrastructure/llm/model_cache.go` openai/openai_compatible
  branch builds a layered transport chain:
  `http.DefaultTransport → propertiesNormalizingTransport (OpenAI-strict
  routes only) → extraBodyTransport → responseLoggingTransport`. Outermost
  logging so it observes the final wire response.
- `internal/infrastructure/llm/model_cache.go` adds `GetWithType` method
  returning the model's provider type and base URL alongside client+name.
  `Get` is preserved as a back-compat shim that discards the new fields.
- `react.AgentConfig` carries `ProviderType` + `ProviderBaseURL`; threaded
  through `model_cache.GetWithType` → factory → engine adapter →
  `engine.ExecutionConfig` → `react.AgentConfig`.

### Notes
- Eino's ReAct manual warns prompt-engineering mitigations should be
  "verified in actual use" — defense for the HITL path is layered
  (prompt directive + content suppression + retract event + react-loop
  halt) so a single layer failing does not unblock fabricated
  destructive claims.

## [1.1.7] — 2026-05-13

### Fixed
- **`GET/PUT/PATCH/DELETE /api/v1/agents/{ref}` now accept UUID or name.**
  Previously the path parameter was treated strictly as `name`, so external
  consumers using the agent UUID returned by `GET /api/v1/agents` received
  404 NOT_FOUND on the very next PATCH. 1.1.7 adds `resolveAgentName` in
  `agentManagerHTTPAdapter` (mirrors the 1.1.5 `resolveSchemaRef` pattern):
  explicit tenant scoping in the resolver, info-hiding 404 on miss, no SQL
  leakage. Admin SPA continues to use names — fully back-compat. PR #56.

### Added
- **`models[*].extra_body` passthrough for `openai_compatible` providers.**
  Operators can now inject upstream-specific JSON fields (e.g. OpenRouter
  `provider: {order: ["zai", "google"], allow_fallbacks: false}`) on a
  per-model basis. The field is stored in the existing `LLMProviderModel.Config`
  JSONB column (no migration), exposed in HTTP DTOs (CreateModelRequest /
  UpdateModelRequest / ModelResponse) and YAML import/export. At LLM call
  time, an `extraBodyTransport` http.RoundTripper merges the map into each
  request body. Reserved keys (`messages`, `tools`, `stream`, `model`) cannot
  be overwritten so the engine's wire contract stays predictable. Works
  generically — unblocks OpenRouter sub-provider pinning, transforms,
  reasoning effort, and any future passthrough without engine code changes.

## [1.1.6] — 2026-05-13

### Fixed
- **`tool_result` rows persist `payload.is_error` for failed tool calls.**
  The live SSE stream already carried `tool_has_error` via `AgentEvent.Error`
  (set in `OnToolEnd` for `[ERROR]`-prefixed responses and `OnToolError` for
  MCP `isError`, circuit-breaker open, and Eino-side Go errors). The
  persistence path through `MessageCollector` dropped that signal — reloaded
  history rendered failed and successful tool calls identically. 1.1.6 adds
  `ToolResultPayload.IsError bool` (`json:"is_error,omitempty"`) and threads
  `event.Error != nil` through `NewToolResultEvent`. `omitempty` + bool zero
  value keeps existing rows binary-compatible; happy-path JSON still emits
  `{"tool":"...","content":"..."}` with no `is_error` key. External proxy
  consumers (e.g. ai-assistant) that branch on `payload.is_error` can drop
  their `[UNAVAILABLE]`-prefix content heuristics. PR #54.

## [1.1.5] — 2026-05-11

### Fixed
- **`POST /api/v1/sessions` now accepts `schema_id` as UUID-or-name.**
  Previously the field was passed raw into the `sessions.schema_id` UUID
  column, so sending the operator-declared schema name (e.g.
  `{"schema_id":"chirp"}`) produced a 500 SQLSTATE 22P02 instead of a clean
  resolve. Clients had to pre-call `GET /api/v1/schemas` on every cold start
  to translate name → UUID. 1.1.5 mirrors the `resolveAgentModel` /
  `resolveEntryAgentRef` pattern in `sessionServiceHTTPAdapter`: explicit
  tenant scoping in both UUID and name branches, `pkgerrors.InvalidInput`
  on miss (→ 400, not 500 SQL leakage), backwards-compatible UUID path
  preserved. Symmetric with the 1.1.3 CreateSchema `entry_agent` fix.

- **`POST /api/v1/knowledge-bases` (`embedding_model_id`) now accepts
  UUID-or-name.** Same shape as schema_id. `kbStoreAdapter.Create/Update/Patch`
  go through the new `resolveEmbeddingModelRef`, which preserves the
  kind=embedding check from the pre-1.1.5 `validateEmbeddingModelKind`.

### Security
- **Tenant scoping bug closed in embedding model lookup.** The pre-1.1.5
  `validateEmbeddingModelKind` used a raw `Where("id = ?", modelID)` with
  **no tenant filter** — a crafted request with a cross-tenant embedding
  model UUID would pass the kind check (the actual FK insert would still
  fail due to row-level tenant isolation, but the lookup itself was a
  side-channel). 1.1.5 adds `AND tenant_id = ?` to both UUID and name
  branches of `resolveEmbeddingModelRef`. No known exploitation; preemptive
  hardening.

### Added
- **`PaginatedSessionResponse.per_page_max`** — server-enforced upper bound
  on `?per_page` (currently 100) surfaced in the response so clients can
  detect runaway pagination loops without out-of-band knowledge. Additive
  JSON field; existing parsers unaffected.

- **`engine/docs/architecture/auth-scopes.md`** — full scope table, actor matrix,
  anti-impersonation guard documented as by-design (chat endpoint ignores
  body `user_sub` for authenticated actors — 1.1.4 fix; regression-guarded
  by integration test `TestSEC24`). POST /sessions trusted-proxy session
  creation explicitly preserved as the chosen pattern for ai-assistant
  style proxies. Deferred from the 1.1.4 plan, written now alongside the
  chirp #2(a) documentation ask.

### Known limitation
- Model names are not currently immutable (unlike schema and KB names). If
  an operator renames an embedding model via direct DB update after KBs
  reference it by name, subsequent KB creates with the old name will 400.
  Model name immutability mirror (PATCH model name → 409) deferred to
  1.1.6 if it becomes an actual issue.

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

