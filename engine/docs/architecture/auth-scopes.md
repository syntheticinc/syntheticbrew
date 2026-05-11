# Engine API — Authentication, Scopes & Actor Matrix

Engine 1.1.5+ reference for the dual authentication scheme (admin JWT +
api_token), the bitmask scope system that gates per-endpoint access, and
the actor-type semantics that determine cross-user behaviour. Audience:
operators issuing tokens, integrators building thin proxies, security
reviewers.

## TL;DR

- Engine accepts two Bearer-token shapes on every authenticated route:
  **EdDSA-signed JWT** (admin SPA, cloud-api, etc.) and **api_token**
  (`bb_…` prefix, `POST /api/v1/auth/tokens`-issued).
- Most endpoints gate access via `RequireScope(ScopeX)`. **ScopeAdmin (=16)
  acts as a superscope** that satisfies any `RequireScope` check.
- The api_token's **name** is the canonical identity for that actor type.
  Engine stamps it into `ctx.UserSub`, so any per-user filter / FK uses
  the token name as the operator-facing "user".
- Per-user ACL on `/sessions` and `/dispatch` is **actor-aware**:
  - api_token (trusted proxy) → tenant-wide read, `?user_sub` query
    honoured (chirp ai-assistant pattern).
  - ScopeAdmin actor → same.
  - regular end-user JWT → `?user_sub` URL parameter is silently ignored;
    results force-scoped to caller's own `ctx.UserSub`. Cross-user GET by
    UUID returns 404 (info hiding).
- **Anti-impersonation guard on `/chat` is by design** — see "Sticky
  by-design behaviours" below.

## Scope bitmask reference (1.1.5)

| Const                 | Bitmask  | Name aliases                | Endpoints gated                                       |
|-----------------------|---------:|-----------------------------|-------------------------------------------------------|
| `ScopeChat`           |        1 | `chat`                      | `POST /api/v1/schemas/{name}/chat`                    |
| `ScopeTasks`          |        2 | `tasks`                     | `/api/v1/tasks/*`, `/api/v1/dispatch/*`               |
| `ScopeAgentsRead`     |        4 | `agents:read`, `agents`     | `GET /api/v1/agents{,/...}`                           |
| `ScopeConfig`         |        8 | `config`                    | `POST /api/v1/config/{reload,import}`, `GET /export`  |
| `ScopeAdmin`          |       16 | `admin`                     | **Superscope** — satisfies any `RequireScope` check   |
| `ScopeAgentsWrite`    |       32 | `agents:write`              | `POST/PUT/PATCH/DELETE /api/v1/agents{,/...}`         |
| `ScopeModelsRead`     |       64 | `models:read`, `models`     | `GET /api/v1/models{,/...}`                           |
| `ScopeModelsWrite`    |      128 | `models:write`              | `POST/PUT/PATCH/DELETE /api/v1/models{,/...}`         |
| `ScopeMCPRead`        |      256 | `mcp:read`, `mcp`           | `GET /api/v1/mcp-servers{,/...}`                      |
| `ScopeMCPWrite`       |      512 | `mcp:write`                 | `POST/PUT/PATCH/DELETE /api/v1/mcp-servers{,/...}`    |
| `ScopeTriggersRead`   |     1024 | (deprecated)                | (V2 triggers removed)                                 |
| `ScopeTriggersWrite`  |     2048 | (deprecated)                | (V2 triggers removed)                                 |
| `ScopeSchemasRead`    |     4096 | `schemas:read`, `schemas`   | `GET /api/v1/schemas{,/...}`                          |
| `ScopeSchemasWrite`   |     8192 | `schemas:write`             | `POST/PUT/PATCH/DELETE /api/v1/schemas{,/...}`        |
| `ScopeSessionsRead`   |    16384 | `sessions:read`, `sessions` | `GET /api/v1/sessions{,/...}`                         |
| `ScopeSessionsWrite`  |    32768 | `sessions:write`            | `POST/PUT/DELETE /api/v1/sessions{,/...}`             |
| `ScopeSettingsRead`   |    65536 | `settings:read`, `settings` | `GET /api/v1/settings`                                |
| `ScopeSettingsWrite`  |   131072 | `settings:write`            | `PUT /api/v1/settings/{key}`                          |
| `ScopeAuditRead`      |   262144 | `audit:read`, `audit`       | `GET /api/v1/audit`                                   |
| `ScopeResilienceRead` |   524288 | `resilience:read`, `resilience` | `GET /api/v1/resilience/circuit-breakers`         |
| `ScopeResilienceWrite`|  1048576 | `resilience:write`          | `POST /api/v1/resilience/circuit-breakers/.../reset`  |
| `ScopeToolsRead`      |  2097152 | `tools:read`, `tools`       | `GET /api/v1/tools/metadata`                          |

### Composite `api` scope

Issuing `{"scopes":["api"]}` expands to:
```
ScopeChat | ScopeTasks | ScopeAgentsRead | ScopeModelsRead | ScopeMCPRead
  | ScopeTriggersRead | ScopeSchemasRead | ScopeSessionsRead
  | ScopeSettingsRead | ScopeAuditRead | ScopeResilienceRead | ScopeToolsRead
```

I.e. read-only access plus chat + tasks. **No write scopes.** Convenience
default for read-mostly integrations.

### Endpoints not gated by `RequireScope`

Two routes remain under `RequireAdminSession` (admin JWT only — api_token
rejected by design):

| Endpoint                                          | Why admin-only                                              |
|---------------------------------------------------|-------------------------------------------------------------|
| `POST/GET/DELETE /api/v1/auth/tokens{,/...}`      | Token-escalation guard — api_token must not be able to mint or delete other api_tokens |
| `POST /api/v1/admin/builder-assistant/restore`    | Low-frequency recovery flow; restricted to interactive admin |

## Actor matrix

Engine recognises three actor types via the `Authorization: Bearer …`
header:

| Actor type        | How recognised                          | Identity (`ctx.UserSub`)        | Default scopes                              |
|-------------------|------------------------------------------|---------------------------------|---------------------------------------------|
| **admin JWT**     | EdDSA-signed JWT, `iss=engine`           | `claims.Subject`                | ScopeAdmin (granted uniformly by EdDSA verifier) |
| **api_token**     | `bb_…` prefix, verified via SHA-256 hash | `info.Name` (token row's name) | `info.ScopesMask` (set at `POST /auth/tokens`) |
| **end-user JWT**  | EdDSA-signed JWT, cloud-side             | `claims.Subject`                | depends on cloud-api signer config          |

### Cross-cutting behaviours

| Concern                                  | admin JWT          | api_token (trusted proxy) | end-user JWT          |
|------------------------------------------|--------------------|---------------------------|-----------------------|
| Token-mint capability (`/auth/tokens`)   | yes                | **no** (RequireAdminSession) | **no**                |
| `GET /sessions?user_sub=alice`           | full tenant-wide   | full tenant-wide          | **silently scoped to caller's `ctx.UserSub`** |
| `GET /sessions/{other_user_session_id}`  | 200                | 200                       | **404 (info hiding)** |
| `POST /chat` with `body.user_sub=other`  | body **ignored**   | body **ignored**          | body **ignored**      |
| `POST /sessions` with `body.user_sub=X`  | body honoured (admin override) | **body honoured (trusted proxy)** | body silently overridden to caller's `ctx.UserSub` |
| `POST /sessions` with `schema_id=name`   | resolved to UUID   | resolved to UUID          | resolved to UUID      |
| `POST /knowledge-bases` with `embedding_model_id=name` | resolved | resolved        | resolved              |

## Sticky by-design behaviours

These behaviours are **load-bearing** — clients architect around them.
Regressions need explicit roadmap announcement.

### Anti-impersonation guard on `/chat` (1.1.4 fix, pinned by design)

**Behaviour:** for any authenticated actor (admin JWT OR api_token),
`POST /api/v1/schemas/{name}/chat` reads `user_sub` from `ctx`, never
from the request body. `body.user_sub` is silently ignored for these
actors.

**Why:** before 1.1.4, the api_token branch in `auth_middleware.go` did
not stamp `domain.WithUserSub(ctx)`, and `chat_handler.resolveUserSub`
fell back to body. An api_token with `ScopeChat` could create sessions
and write memories under any user_sub — full impersonation without admin
scope.

**Regression guard:** `engine/tests/integration/session_acl_test.go`
`TestSEC24_SessionCreate_APITokenWriteOwnUserSub` (HTTP-level) plus
`engine/internal/delivery/http/chat_handler.go::resolveUserSub` source
comments.

**Roadmap promise:** this guard does not change without an explicit
release note flagging it.

### Trusted-proxy session creation preserved on `POST /sessions`

**Behaviour:** unlike `/chat`, `POST /api/v1/sessions` **honours**
`body.user_sub` when the actor is `api_token` (trusted proxy) or has
`ScopeAdmin`. End-user JWT actors still get their `body.user_sub`
silently overridden.

**Why:** ai-assistant style proxies (chirp's pattern) sit between many
end-users and the engine. They use one service-level api_token for the
entire fleet of users; the per-user identity is supplied per call via
`body.user_sub`. Locking this down would require either per-end-user JWTs
direct to engine (RFC 8693 token-exchange flow) or a new opt-in scope
like `ScopeImpersonate`.

**Roadmap promise:** if `body.user_sub` ever becomes opt-in for
api_token actors (e.g. behind a separate scope bit), this is announced
in advance — clients can migrate before the change ships. Current plan:
**no changes**.

## Programmatic client examples

### Issuing a narrow-scope token

```bash
curl -X POST $ENGINE_URL/api/v1/auth/tokens \
  -H "Authorization: Bearer $ADMIN_JWT" \
  -d '{
        "name": "ai-assistant-proxy",
        "scopes": ["chat", "sessions:read", "sessions:write"]
      }'
# →
# {
#   "id": "...",
#   "name": "ai-assistant-proxy",
#   "scopes_mask": 49153,   // chat(1) | sessions:read(16384) | sessions:write(32768)
#   "token": "bb_…"          // shown ONCE; store securely
# }
```

This token cannot:
- mint other tokens (no `RequireAdminSession` bypass)
- write/delete schemas, models, agents
- read audit logs, settings, resilience admin
- access tools/metadata

…but it can:
- create chat messages on any schema (`ScopeChat`)
- list / read / create / update sessions tenant-wide (`ScopeSessionsRead/Write`)
- when chatting or creating sessions, set `body.user_sub` to any end-user
  (trusted-proxy pattern preserved)

### Creating a session referencing a schema by name (1.1.5+)

```bash
curl -X POST $ENGINE_URL/api/v1/sessions \
  -H "Authorization: Bearer bb_…" \
  -H "Content-Type: application/json" \
  -d '{
        "schema_id": "chirp",
        "user_sub":  "alice@chirp-org-1",
        "metadata":  {"org_id": "chirp-org-1"}
      }'
# Engine resolves schema_id "chirp" → schemas.id UUID via tenant-scoped
# index lookup. Returns 201 with the created session (including
# `metadata` round-trip).
```

Unknown name → 400 `InvalidInput`. Cross-tenant UUID → 400 `InvalidInput`
(same response code as truly-not-found, to prevent existence enumeration
across tenants).

### Reading sessions tenant-wide (trusted proxy)

```bash
curl -G $ENGINE_URL/api/v1/sessions \
  -H "Authorization: Bearer bb_…" \
  --data-urlencode "user_sub=alice@chirp-org-1" \
  --data-urlencode "per_page=100"
# api_token actor → URL ?user_sub filter honoured.
# Response includes `per_page_max: 100` for safe pagination math.
```

Same call as an end-user JWT (e.g. Alice signed in via cloud-api): the
`?user_sub` filter is silently dropped and the result is force-scoped to
Alice's own sessions.

## Migration & compatibility notes

- Existing **admin-token-only** consumers continue to work unchanged.
  `ScopeAdmin` remains the superscope.
- New scope names (`sessions:read`, `audit:read`, etc.) are additive —
  existing tokens issued with `scopes:["api"]` automatically gain the new
  read scopes via the composite mask expansion.
- New response field `per_page_max` on `GET /sessions` is **additive**
  JSON. Existing parsers ignore unknown fields by default.

## See also

- `docs/testing/security-checklist.md` — SCC-01..06 GATE checks (must
  pass for every authenticated TC).
- `engine/internal/delivery/http/auth_middleware.go` — canonical
  implementation of Bearer parsing, scope bitmask, and `RequireScope`.
- `engine/internal/delivery/http/session_handler.go::extractSessionACL`
  — actor-aware per-user ACL on the `/sessions` mount.
- `engine/CHANGELOG.md` — release-by-release changes to the scope set and
  the chat / session impersonation behaviours.
