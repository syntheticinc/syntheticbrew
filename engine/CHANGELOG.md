# Changelog

## [1.8.4] — 2026-06-16

### Fixed

- **The built-in `show_structured_output` HITL widget halts the agent turn on the
  first widget again.** When the engine took ownership of the ReAct loop (1.7.0),
  the loop's halt-after-tool ("return-directly") signal began to be driven by a
  tool-name set carried on the loop's own state. The built-in widget, however,
  still tried to halt through the previous loop's state type — which the owned loop
  does not carry — so the halt call failed on every widget emit and the turn never
  stopped. Having shown a widget but not ended its turn, the model would re-emit the
  same widget repeatedly (observed up to 14× in one turn), narrate the prompt as
  plain text, or run to the turn budget. The built-in human-in-the-loop tools are
  now part of the loop's return-directly set, so the turn ends the moment the widget
  fires (one widget per turn) — the same route MCP return-directly tools already
  use. This is independent of the model. The dead halt call, which logged an error
  on every widget emit, has been removed.

## [1.8.3] — 2026-06-16

### Fixed

- **Prompt cache no longer collapses every few steps, and now grows with the
  conversation, on explicit-cache providers (Qwen/DashScope via OpenRouter).** Two
  independent causes, both verified live on qwen3.7-plus:
  - The environment-context reminder embedded the wall-clock time **to the minute**
    and re-read it on every model call, so when the minute rolled mid-turn (≈ every
    3–4 reasoning steps) an already-sent prefix message changed value — which makes
    explicit-cache providers discard the **entire** prefix cache. The reminder now
    stamps the time once at turn start (it is built per turn) and stays byte-identical
    across the turn's steps.
  - The history cache breakpoint was placed on the **moving last message**, so the
    previous tail lost its `cache_control` marker every step; on Qwen/DashScope that
    byte change stops the prior cache write from being reused, pinning cached tokens
    at the system prompt. Breakpoints now anchor to **fixed stride boundaries** that
    accumulate and never move, so the cached prefix grows in a staircase as the
    conversation extends (live: cached climbed `2k → 8.8k → 16.4k` across a 20-step
    tool loop where it previously stayed flat at ~2k), with no per-step collapse. Short
    turns below the first boundary keep marking the tail, unchanged.

## [1.8.2] — 2026-06-15

### Fixed

- **Cached prompt tokens are now visible in the engine response and admin chat for
  OpenRouter models.** Prompt caching already worked, but OpenRouter omits
  `usage.prompt_tokens_details.cached_tokens` from its response unless the request
  body carries the `usage: {"include": true}` flag — which the engine never sent, so
  `cached_prompt_tokens` always read as zero even on cache hits. The engine now adds
  that flag for OpenRouter base URLs (only — it is an OpenRouter extension, and real
  OpenAI / strict gateways reject unknown body keys; an operator-supplied `usage` via
  `extra_body` still wins). The cached count flows through to the `processing_stopped`
  SSE event (`cached_prompt_tokens`, alongside `prompt_tokens` / `completion_tokens`)
  and renders in the admin chat context bar. Verified live on qwen3.7-plus over the
  streaming path: cached tokens surface and climb across steps.

## [1.8.1] — 2026-06-15

### Fixed

- **Prompt caching now grows with the conversation on explicit-cache providers
  instead of collapsing mid-turn.** 1.7.3 moved the cache breakpoint ahead of the
  dynamic trailing reminders, but explicit-cache providers (Alibaba Qwen /
  DashScope) discard the **entire** prefix cache the moment any already-sent
  content changes or shifts between steps — not just content after the breakpoint.
  The per-call reminders (tool-call history, environment time, task state,
  finalize/urgency directives) were re-emitted in a trailing block that was
  rewritten and shifted as the conversation grew, so from the step a dynamic
  reminder appeared the cached-token count dropped to zero and the whole prefix was
  re-billed. The engine now builds each turn's messages **append-only**: a reminder
  is appended as a new message (interleaved at the tail it was added) only when its
  value changes, prior reminders and turns are never rewritten or shifted, and the
  cache breakpoint moves to that append-only tail (every message is canonicalized to
  array form so a former breakpoint stays byte-stable). Each request is therefore a
  clean prefix-extension of the previous one — the explicit-cache prefix grows while
  reminders keep their live, per-step values (e.g. a step countdown). Verified live
  on qwen3.7-plus: cached prompt tokens climb across steps where they previously
  stayed at zero.

### Changed

- **`cache_control` is now on by default for explicit-cache providers**
  (`openai_compatible`, `anthropic`). A model with no `cache_control` config caches
  its stable prefix automatically; set `cache_control.enabled: false` to opt out.
  Automatic-cache providers (OpenAI, Azure, Google) are unaffected — they ignore
  the marker. The marker stays gated by `min_prefix_tokens`, so small requests are
  untouched. A few strict OpenAI-compatible gateways may reject the in-content
  marker — opt out there.

## [1.8.0] — 2026-06-15

### Added

- **MCP tools can self-declare "return-directly" (terminal) via tool metadata.** A
  tool whose `tools/list` entry carries `_meta: {"syntheticbrew.ai/return-directly":
  true}` ends the ReAct turn the moment it runs: its result is the final answer,
  with no follow-up model call and no trailing assistant message. This lets a tool
  meant to render the final answer (e.g. one that returns a formatted
  recommendation) stop the loop without the engine hard-coding third-party tool
  names. On reasoning models this removes a spurious trailing self-narration turn
  that a non-terminal "final answer" tool would otherwise force. The key is
  namespaced per the MCP `_meta` spec; only a boolean `true` enables it, and any
  other shape (missing/malformed) leaves behaviour unchanged. The existing global
  `agent.tool_return_directly` list still works and is unioned with self-declared
  tools.

### Fixed

- **Two data races eliminated (`go test -race ./...` is now clean).** Both were real
  concurrency defects, not test artifacts: `SSEWriter` wrote the same
  `http.ResponseWriter` from the request and heartbeat goroutines without a lock
  (and its heartbeat stop returned before the goroutine had exited), and
  `SessionEventBus.Publish` could send on `eventCh` while `Close` closed it — a
  send racing a channel close, which panics ("send on closed channel"). Writes are
  now mutex-serialized, heartbeat stop blocks until the goroutine exits, and
  Publish/Close are mutually exclusive via a mutex + closed flag.

## [1.7.3] — 2026-06-14

### Fixed

- **Prompt caching: history breakpoint no longer lands on dynamic trailing
  content, so the growing conversation prefix actually caches.** On explicit-cache
  providers the cached-token count could freeze at the system-prompt-plus-tools
  size (the static head) while the rest of the conversation was re-billed on every
  call. Two causes, both fixed:
  - The `cache_control` history breakpoint was placed on the absolute last message.
    In the live request that is a per-call dynamic reminder (tool-call history,
    environment time) injected after the conversation, so its cache block was never
    re-read and only the static head re-hit. The breakpoint now lands on the last
    **stable** message (the last non-system turn), before the trailing reminders.
  - Per-step directives (task focus, finalize, urgency) were concatenated into the
    head system message, changing its bytes from one model call to the next and
    breaking the head's own cache. They are now emitted as a single trailing system
    message — the model sees the same text, positioned last for recency, while the
    head stays byte-identical and cacheable across the turn.

  Behaviour is unchanged (same text reaches the model); this is a billing/transport
  fix. Most effective on explicit-cache providers (Anthropic; explicit Qwen models
  such as `qwen-plus` / `qwen3.x-plus`).

## [1.7.2] — 2026-06-14

### Added

- **Prompt caching: provider-agnostic `cache_control` breakpoints.** A new opt-in
  per-model `cache_control` config marks the stable prefix (system prompt + tool
  definitions + frozen history) as cacheable, so explicit-cache providers
  (Anthropic Claude; explicit Qwen models) serve it from cache instead of
  re-billing it on every ReAct step. Default off — when off, the request shape is
  byte-identical to before. Automatic-cache providers ignore the marker
  (harmless). Cached tokens are surfaced in the per-turn `token_usage` event as
  `cached_prompt_tokens` and in a debug log line.
- **Prompt caching: OpenRouter sticky-routing via `x-session-id`.** The engine now
  sends the conversation's session id as an `x-session-id` header on
  OpenAI-compatible requests (on by default). OpenRouter uses it to pin every step
  and turn of a conversation to the same upstream provider, keeping that provider's
  automatic prefix cache warm — the only effective lever for auto-cache models such
  as `qwen3-coder-next`, which ignore `cache_control`. Harmless for non-OpenRouter
  providers (the header is ignored). See `deployment/prompt-caching` in the docs.

## [1.7.1] — 2026-06-11

### Fixed

- **`max_context_size` is now enforced in real tokens, not a fixed `chars/4`
  guess.** The compression guard previously estimated tokens as characters ÷ 4.
  For tool- and JSON-heavy, multilingual traffic the real ratio is closer to
  ~2.7 chars/token, so the estimate undercounted by ~46% and the guard skipped
  compression exactly when it was needed — a flow configured for 128k tokens
  could ship ~187k. The decision now runs off an empirical chars-per-token ratio
  calibrated from the provider's real `prompt_tokens` (with a conservative
  cold-start default and a safety headroom), so the budget tracks true
  tokenization.
- **The budget now covers the whole request.** The system prompt and the tool
  schemas — both injected after the rewriter runs and previously invisible to it —
  are counted toward the context budget, not just the conversation messages.
- **The budget is a hard ceiling.** When the system prompt plus user turns still
  exceed the limit, the oldest user turns are evicted (keeping the live turn)
  instead of warning and sending an over-limit request. The forced-summary call
  at the budget wall is compressed too, so it cannot overflow on a long turn.
- **Compression keeps parallel tool calls well-formed.** An assistant message that
  issued several tool calls in one step is now kept together with ALL of its tool
  results, or dropped as a whole. Previously compression could keep the assistant
  while evicting one of its tool results, leaving a `tool_call` with no matching
  result — which OpenAI-strict providers reject with a 400. This was latent while
  the chars/4 under-count rarely triggered compression; enforcing the real budget
  makes compression fire regularly, so the atomic-group rule is now required.
- **Data race on the streaming answer-start flag.** The per-call streaming
  goroutines could overlap across sequential model calls in a turn and write the
  shared `answerStarted` flag without synchronization (caught by `go test -race`).
  The flag is now guarded by the same lock as the rest of the streamed-answer
  state.

### Security

- **`max_context_size` is bounded at the API layer.** A value above a sane ceiling
  (100M tokens) is now rejected with 400 on create / update / patch / config-import,
  instead of overflowing the budget arithmetic into silent degenerate compression
  (SCC-03: invalid input must be 400, not a silent wrong result).

## [1.7.0] — 2026-06-10

### Changed

- **The ReAct execution loop is now owned by the engine** instead of delegating to
  a prebuilt agent black box. The turn runs on a hand-built graph with explicit
  `chat` / `tools` / `finalize` nodes and budget-, loop-, and HITL-aware routing.
  This is what makes the fix below possible; existing turn behaviour (event
  stream, tool handling, HITL, context compression, the step watchdog) is
  preserved.

### Fixed

- **A budget-exhausted turn now produces a real summary instead of a canned
  apology.** When a turn reaches its `max_turn_duration` or `max_steps` wall, the
  engine makes one final model call with the tools removed, fed the full context
  the turn already gathered, so the model writes its best answer from what it
  found. The hardcoded graceful message remains only as a fallback for when that
  final call yields nothing. Previously the turn ended with a fixed apology and
  discarded the gathered context.
- **A live partial answer is no longer retracted at the budget wall.** The earlier
  behaviour scrubbed an in-progress streamed answer when a budget tripped; the
  owned loop streams the final summary as a normal answer and never retracts a
  substantive partial.
- **Non-productive tool loops are corrected before they are terminated.** A model
  that repeats a failing tool, or repeats a byte-identical call without progress,
  first receives a single corrective instruction and is allowed to continue;
  only if it keeps looping past a small correction budget is the turn finalised
  with a summary. Identical-call detection is guarded against false positives:
  a deliberately paced repeat (for example polling a status on a timer, with a
  wait between checks) does not count as a loop.

### Security

- **`max_turn_duration` is now bounded at every layer.** The value is validated on
  every write path (create / update / patch / config import), clamped to the
  engine default at agent construction if a persisted row is out of range, and —
  new in this release — enforced by a database `CHECK` (migration 013), mirroring
  the existing `max_step_duration` constraint. This closes a path where an
  out-of-range value written straight to the table could overflow into a negative
  or effectively-infinite turn deadline.
- **A tool name can no longer smuggle instructions into a model prompt.** Tool
  names interpolated into a loop-correction message are matched against an
  allowlist (the function-name charset plus the dotted/colon MCP convention) and
  replaced with a neutral placeholder otherwise, so a tenant- or MCP-controlled
  name carrying control characters or inline text cannot reach the model as
  high-authority guidance.

### Removed

- Internal workarounds that existed only to compensate for the prebuilt loop —
  streaming content recovery and the shadow-state termination path — are gone;
  the owned loop holds this state directly.

## [1.6.0] — 2026-06-10

### Added

- **`max_step_duration` agent field (per-step watchdog)** — a new per-agent
  setting (seconds; `0` = disabled, the default) that force-stops a turn with a
  graceful answer when a single step (a model call or a tool) produces no
  activity for longer than the limit. Catches a hung step faster than the
  whole-turn budget, and surfaces it as an agent answer rather than a dropped
  stream. Configurable via the API, the Admin dashboard, and GitOps
  (config import/export + brewctl).

### Fixed

- **Budget-exhausted turns now end with a graceful answer instead of a bare
  error** — previously a turn that hit `max_turn_duration` or `max_steps` (Eino
  `ErrExceedMaxSteps`) terminated the SSE stream with a raw error and no `done`
  event, leaving the client on a stuck spinner. Both budgets now emit a clear
  final assistant message and finish the turn cleanly (`answer` + `done`),
  matching the tool-error-loop breaker. A turn-time deadline on the internal
  stream context is distinguished from a genuine client cancellation, so the
  graceful answer fires only for the former.
- **Soft-landing before the budget wall** — as a turn approaches ~90% of
  `max_turn_duration` or its step budget, the model is instructed to stop calling
  tools and produce its best final answer from what it already gathered, so a
  well-behaved model finishes inside the budget with its own summary. The
  hardcoded graceful message remains the backstop when the model ignores the
  directive.
- **Identical-argument tool loops are hard-stopped** — when a model repeats a
  byte-identical tool call (same name + arguments) three times in a row, the turn
  is force-stopped with a graceful answer, regardless of result content. This
  catches the degenerate loop where a tool returns successful-but-empty results,
  which the error-loop breaker (which only counts `[ERROR]` results) never saw. A
  call with different arguments (e.g. pagination) resets the streak, so
  legitimate iteration is unaffected.

## [1.5.0] — 2026-06-07

### Added

- **Typed error codes on SSE error events** — `ERROR` events now carry the most
  specific machine-readable code from the error chain (`errors.DeepestCode`)
  instead of a hardcoded `internal`, and the content is the curated user-facing
  message (`errors.UserMessage`) rather than the raw technical chain. Circuit
  breaker rejections surface as a typed `UNAVAILABLE`. Clients can switch on a
  stable code (`INTERNAL_ERROR`, `UNAVAILABLE`, `RATE_LIMITED`, …) instead of
  string-matching the message. **Contract change:** the `code` field on SSE
  error events changes from the literal `internal` to the typed codes; consumers
  that matched `code == "internal"` should switch to the typed values.

### Fixed

- **Runaway tool-error loops are now hard-stopped with a graceful answer** — when
  a single tool returns an `[ERROR]` result four times in a row within one turn,
  the agent loop is force-stopped (deterministically, without relying on the
  model heeding the advisory warning) and a clear final assistant message naming
  the failing tool is emitted, instead of looping until `MaxSteps` / client
  timeout. Works in both the streaming and non-streaming paths.
- **Empty final answer no longer renders a blank assistant bubble** — streaming
  turns emitted a trailing empty-content `ANSWER` event (turn completion is
  signalled separately by `PROCESSING_STOPPED`); it is now suppressed at the
  projection layer, removing the blank-bubble flash on clients (notably on HITL
  turns where accumulated prose is intentionally suppressed).

## [1.4.1] — 2026-05-29

### Fixed

- **KG bundle re-apply self-collision (HTTP 409)** — re-applying the same
  bundle to an engine that had already cached its tools in the in-memory
  registry (after at least one chat session warmed the cache) raised
  `[ALREADY_EXISTS] tool name collision: [...]` listing the bundle's own
  tools. Root cause: `RegistryToolNames.ToolNamesForTenant` ignored the
  `excludeBundle` argument the collision detector passed in, so the
  bundle's cached tools registered as pre-existing during its own re-apply.
  The `DBSchemaToolNames` source already excluded correctly; the in-memory
  registry source did not. Added `Registry.AllToolNamesForTenantExceptBundle`
  and wired the detector through it. Apply is now genuinely idempotent
  for the same `bundle_name` across CI re-runs without manual delete or
  engine restart, matching the documented contract. Regression covered by
  three unit tests in `kgtools` package.

## [1.4.0] — 2026-05-28

Knowledge Graphs query API ergonomics. Five LLM-tool / REST additions driven
by first design-partner pilot feedback after the 1.3.0 launch. No schema
changes, no migrations — all features layer on the existing `kg_entity`
JSONB storage with the generic GIN index.

### Added

- **Batch `get_<entity>(ids[])`** auto MCP tool — agents fetch many entities
  in one round-trip, response shape `{entities, not_found}` with input-order
  preservation via `ORDER BY array_position($input, entity_id)`. Hard cap
  500 ids per call. Partial success — missing ids surface in `not_found`,
  the call never fails on misses.
- **REST POST `/api/v1/knowledge-graphs/{bundle}/entities/{entity_type}/batch-get`**
  for symmetry with the auto MCP tool. Same body and response shape.
- **Range filter operators**: `filter[X][gte|gt|lte|lt]=N` on numeric and
  date / date-time `x-index` properties. Type-aware validation rejects
  range on string / enum / boolean.
- **`filter[X][in]=a,b,c`** multi-value equality, capped at 500 values.
- **`x-summary-fields: [...]`** schema annotation. When set, the
  `list_<entity>_ids` tool returns `{items, total}` with each item carrying
  the id field plus the declared summary fields, instead of the bare
  `{ids, total}` shape. Default (annotation absent) preserves 1.3.x bare-ids
  semantics.
- **Server-side `sort: [{field, order}]`** on `list_<entity>` and
  `list_<entity>_ids`. Enum properties sort by *declaration order* via
  `array_position(ARRAY[...], data->>'field')` — NOT alphabetical. Missing
  values appear last regardless of direction (NULLS LAST). Sort fields must
  be `x-index`.
- **`list_<entity>_ids` MCP tool** is now actually built. 1.3.0 declared the
  tool name in `domain.ToolNames()` but the BuildTool switch missed the
  `_ids` suffix case — names were silently dropped. Closed.

### Breaking

- `get_<entity>` MCP tool signature changed from single-id to array-of-ids.
  Migration is a one-line prompt edit: `get_X(id="A")` → `get_X(ids=["A"])`.
  REST single-id endpoint `GET /entities/{type}/{id}` is unchanged.

### Hardened

- **Sort field injection** (KG14-SEC-01): repo-layer `validIdentifier`
  whitelist plus usecase x-index check — defence-in-depth.
- **IN-list DoS cap** (KG14-SEC-04): `MaxFilterInSize = 500` mirrors batch
  get cap.
- **Query timeout** (KG14-SEC-05): `KGQueryTimeout = 5s` wraps every list /
  batch get call via context cancellation.
- **Sort `array_position` values** (KG14-SEC-07): enum values come from the
  parsed schema, never from caller input. Repo escapes single quotes before
  emitting the SQL literal as defence-in-depth.
- **`x-summary-fields` path injection** (KG14-SEC-06): validation at apply
  time (`pkg/jsonschema.normaliseSummaryFields`) rejects dot-notation,
  unknown properties, empty entries, and duplicates.
- **Sort query-string size cap** (KG14-SEC-08): parser rejects strings
  longer than 2KB before splitting.

### Compatibility

- 1.3.x bundles continue to work without re-import — all new annotations
  are opt-in; bare equality filters and existing tool signatures behave
  identically to 1.3.0.

## [1.3.0] — 2026-05-27

Knowledge Graphs — third capability primitive alongside `memory` and
`knowledge`. Declarative entity taxonomies with auto-generated `list_X` /
`get_X` MCP tools per entity type, bound to agents via the standard
capability mechanism.

### Added

- **Knowledge Graphs domain**: `kg_bundle`, `kg_entity_schema`, `kg_entity`
  tables (migration `010_knowledge_graphs.yaml`); generic GIN index on
  entity JSONB; partial GIN on `capabilities.config -> 'bundles'`.
- **Capability**: `capabilities.type = 'knowledge_graphs'` accepted
  (migration `011_capabilities_kg_constraint.yaml` relaxes the CHECK).
  Strategy registry pattern (`internal/domain/capabilities/`) replaces
  prior switch-based capability dispatch — adding a new capability is now
  a one-file change, enforced by an architectural test.
- **REST API**: 12 endpoints under `/api/v1/knowledge-graphs/{bundle}/...`
  (read + bulk import + granular CRUD + schema upsert + bundle delete).
  Granular entity bodies are flat JSON mirroring `bulk-import.items[]`.
- **Auto MCP tools**: per-tenant lazy registry; `list_<entity_type>`
  accepts `filter[<x-index-field>]=<value>`, `limit` (1..500), `offset`.
- **GitOps**: `/config/import` and `/config/export` round-trip
  `knowledge_graphs` alongside agents/models/MCP servers.

### Hardened

- Cross-bundle tool-name collision detection now reads the persistence
  layer (`DBSchemaToolNames`) instead of relying on the in-memory
  registry that was only hydrated by chat traffic.
- Bundle-level data cap: 10 MB (`MaxBundleDataBytes`); per-entity 100 KB.
- Pagination: `limit > 500` returns HTTP 400 (was silently clamped).
- Cross-tenant isolation: tenant B GET on tenant A's bundle → 404 (no
  existence leak); capability bound to a phantom bundle resolves to
  empty tools (no info leak).
- JSON Schema validator blocks external `$ref` (SSRF guard).

### Cloud (bytebrew-ee)

- `Plugin.KGEnforcer()` and `Plugin.KGCounter()` implemented; entitlements
  extended with KG quota fields. Free=1/200/50KB/5MB; Personal=3/2000/
  100KB/10MB; Pro=∞/20000/100KB/10MB; Enterprise=∞ across the board.

### Known follow-ups

- L3 e2e tests in `bytebrew-ee/tests/e2e/` exercising the agent-chat-uses-
  tool path (TestKGCloud01-08) — wiring + entitlements shipped, mock-llm
  scenarios pending.
- Per-bundle creation hook (`OnBundleCreate`) to enforce KGBundlesLimit
  cap at apply time — currently only entity-level writes are gated.

## [1.2.4] — 2026-05-26

Recovery classifier hardening + log-noise cleanup.

### Fixed

- Chat turns no longer abort with `INTERNAL_ERROR` when an MCP tool
  returns `isError: true` with a content payload containing phrases
  like `"permission denied"`. The agent recovery classifier previously
  did substring matching over the lowercased error text against a
  non-recoverable list — a leaky abstraction that let tool-author-
  controlled content steer the platform's control flow.
- `WARN persist chat session failed` (duplicate key on `pk_sessions`)
  no longer fires on every chat request to an existing session after
  engine restart or in-memory registry eviction. Session-row creation
  is now idempotent via `INSERT ... ON CONFLICT DO NOTHING`.
- `ERROR failed to create session log directory` no longer fires on
  every turn when `ContextLogPath` points at a read-only filesystem.
  The context logger now performs a one-shot sticky disable: a single
  `INFO context logging disabled` line per process lifetime, then all
  subsequent log attempts return silently.

### Changed (internal)

- New `pkg/errors` codes: `CodeRateLimited`, `CodeLLMAuth`,
  `CodeTransient`, `CodeAgentBudgetExhausted`. Additive only.
- New `internal/infrastructure/llm/classify_error.go` — the single
  chokepoint for substring matching against HTTP-status / provider
  error strings; produces typed `DomainError` wraps.
- `RetryWrapper` is now applied to every production LLM client (13
  construction sites in `llm_factory`, `model_cache`,
  `azure_openai_client`, `byok`). Defaults: 3 attempts, 500 ms base
  backoff, 5 min per-attempt timeout. `Stream` calls remain pass-
  through. `isRetriable` rewritten to use `errors.Is` on the typed
  codes.
- `react/agent.go classifyRecovery` replaces `isRateLimitError` +
  `isRecoverableAgentError`. Pure `errors.Is` dispatch over
  `context.Canceled`, `context.DeadlineExceeded`,
  `compose.ErrExceedMaxSteps`, and the new `pkgerrors` codes. Zero
  substring matching in the agent layer.
- MCP `tool_adapter.InvokableRun` returns `("[ERROR] " + content,
  nil)` for application-level errors (`isError: true`) instead of
  bubbling them as Go errors. Transport-level Go errors still
  surface as before. `MCPToolError` type removed.
- `spawn_tool` migrated to the same `[ERROR]` convention for its
  five application-level error branches.

### Behavioural guarantees preserved

- SSE event shape unchanged.
- `pkg/errors` public surface additive only.
- Retry counts, backoff durations and final wrapping codes in the
  `agent.go` retry loops identical to prior behaviour for every
  error class that crosses the classifier.

## [1.2.3] — 2026-05-22

Admin SPA hotfix.

### Fixed
- Test Flow tab in the admin SPA stopped rendering chat UI when the schema
  page was reached by deep-link or reload (URL of the form
  `/admin/schemas/{name}`). The early-return empty-state branch only checked
  the BottomPanel context (`selectedSchema`) and missed the
  `lockedSchemaId` prop populated from the URL, so the body kept showing
  "Select a schema in the panel above…" while the lock indicator already
  displayed the schema name. Now the empty state requires both signals to
  be absent.

### No engine code changes
- All Go packages identical to 1.2.2. Admin SPA is rebuilt and baked into
  the engine image, so an upgrade to the 1.2.3 image is required to pick
  up the fix.

## [1.2.2] — 2026-05-22

`show_structured_output` API hardening. Sub-frontier LLMs routinely emit
widget args in shapes that diverge from the tool's JSON Schema (invented
fields, stringified nested arrays, omitted required ids). The previous
implementation accepted these silently and emitted a degenerate widget;
the agent received `Structured output displayed to user.` and continued
as if the call had succeeded. This release switches every input-validation
path to fail-loud, surfaces nested stringified arrays through a recursive
lenient parser, and removes one source of model-busywork on single-question
forms.

### Added
- Fail-loud validation on `show_structured_output` arguments. Invalid input
  returns `[ERROR] …` to the agent instead of silently emitting a degenerate
  widget.
  - Unknown top-level fields and unknown fields inside `questions[]` /
    `options[]` return `[ERROR] json: unknown field "X"`. The decoder uses
    `DisallowUnknownFields` at every level via the new `decodeStrict[T]`
    helper.
  - `output_type` is validated against the closed set
    (`summary_table | form | info`) before any other processing; unknown
    values return `[ERROR] unknown output_type "X". Supported: …`.
- Recursive lenient parsing: `questions[i].options` accepts stringified
  JSON arrays via a new `Question.UnmarshalJSON` on the domain type,
  matching the PR #75 behaviour for top-level `rows` / `actions` /
  `questions`. Malformed stringified values fail loud rather than being
  silently dropped.
- Auto-generated `question.id` for single-question forms when the model
  omits it. Synthetic id (`q-<8 hex chars>` derived from the
  server-issued `interrupt_id`) surfaces in the resume payload as
  `answers[0].question_id`. Multi-question forms still require explicit
  ids — they carry semantic meaning for cross-answer correlation.
- JSON Schema `description` of the tool expanded with literal + stringified
  examples for all three `output_type` values, explicit closed-set
  declaration, and a "STOP on tool error" instruction at the end.
- New CE docs page `docs/architecture/tools-show-structured-output.md`
  with the full schema reference, examples, recommended prompt pattern for
  tool-error handling, and the known sub-frontier-model pitfalls.

### Changed
- `maxQuestions` raised from 5 to 10. Full configurability (per-tenant /
  per-agent override) deferred to 1.3.0.

### Notes
- No protocol changes. SSE wire format, POST body shape, and DB schema are
  unchanged from 1.2.0.
- Clients on 1.2.0 keep working without changes. The only observable diff
  is that agents using `show_structured_output` against sub-frontier
  models now see explicit `[ERROR]` responses on malformed input where
  they previously saw `Structured output displayed to user.` followed by
  a degenerate empty widget on the client.

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
- **Engine: startup `WARN` when `SYNTHETICBREW_AUTH_MODE=local` and the HTTP
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
SyntheticBrew tenant. Accepted on `POST /api/v1/sessions` and
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
- Multi-tenant + multi-user JWT regression tests live in `syntheticbrew-ee/tests/integration/`
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
- `SYNTHETICBREW_BOOTSTRAP_ADMIN_TOKEN` env support: when set, engine seeds an admin
  API token in `api_tokens` on first boot (idempotent — skipped when
  `name="bootstrap-admin"` already exists). Enables automated declarative
  GitOps reconcile via `brewctl config-apply` in k8s deployments without
  manual Admin UI token generation.
  Format: `bb_<64-hex>`. Generate: `echo "bb_$(openssl rand -hex 32)"`.
  Scope: admin (mask=16). Name: `bootstrap-admin`.

## Architecture — CE/EE/Cloud Unification (pre-release)

Initial canonical architecture for SyntheticBrew Engine. Frozen pre-release — no
prior production clients, no upgrade path.

### Identity
- End-user identity is external — JWT `sub` claim — persisted as varchar
  (`sessions.user_sub`, `memories.user_sub`, `audit_logs.actor_sub`,
  `api_tokens.user_sub`). No `users` table; no UUID FKs to a user record.

### Auth
- EdDSA (Ed25519) is the only JWT algorithm. No HS256 shared-secret path.
- `SYNTHETICBREW_AUTH_MODE=local`: engine auto-generates an Ed25519 keypair under
  `SYNTHETICBREW_JWT_KEYS_DIR` on first boot; admin sessions minted via
  `POST /api/v1/auth/local-session` (sub=`local-admin`, tenant_id empty).
  Single-replica use only.
- `SYNTHETICBREW_AUTH_MODE=external`: engine loads the issuer's public key from
  `SYNTHETICBREW_JWT_PUBLIC_KEY_PATH`; no local-session route. Multi-replica safe.
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

