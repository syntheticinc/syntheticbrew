# Security Hardening

This document describes the security headers, CORS policy, and observability
controls applied by the SyntheticBrew engine HTTP layer.

## Security Gate Mapping (SCC-01..SCC-06)

The table below maps canonical Security Check Codes to the engine endpoints
and middleware that satisfy each gate. Used by QA when running
`docs/testing/security-checklist.md` against a live deployment.

| Check | Gate type | Description | Engine mechanism |
|-------|-----------|-------------|-----------------|
| **SCC-01** | GATE | Unauthenticated → 401 | `JWTAuthMiddleware` on all `/api/v1/*` protected routes. Absent or invalid `Authorization: Bearer` → `401 Unauthorized`. |
| **SCC-02** | GATE | Cross-tenant → 403/404 | `TenantMiddleware` resolves `tenant_id` from JWT `sub` + `kid`. All repository queries are scoped by `tenant_id`. A valid JWT for tenant A cannot read resources of tenant B — returns 404 (resource not found for that tenant). |
| **SCC-03** | GATE | Invalid input → 400 not 500 | Handler-level input validation before any business logic. Malformed JSON, missing required fields, oversized bodies (1 MB cap via `maxBodySize` middleware) → `400 Bad Request`. |
| **SCC-04** | ADVISORY | File/shell tools blocked (Cloud) | EE binary: `deriveRuntimeTools` excludes `file_*`, `shell_exec` tool registrations. Cloud mode enforced via `SYNTHETICBREW_AUTH_MODE=external` build path — no file-system tools exposed through MCP or native tool registry. |
| **SCC-05** | ADVISORY | Expired JWT → 401 + WWW-Authenticate | EdDSA verifier checks `exp` claim; expired token → `401` with `WWW-Authenticate: Bearer error="invalid_token"` header. |
| **SCC-06** | ADVISORY | Rate limit → 429 | `httprate.LimitByIP(100, time.Minute)` on `/api/v1/*` group (cloud-api). Engine: configurable via `engine.rate_limit` config key (default 200 req/min per IP). |

### Fail-Closed Policy

The quota check on each chat step is **fail-closed**: if the internal metering
endpoint is unreachable, the engine rejects the request rather than allowing
unlimited usage. Rationale: a silent billing gap is worse than a brief outage.

```
quota check → 503 metering unavailable → engine returns 503 to client
```

This is intentional and documented in the run-book. Operators must ensure the
cloud-api `/api/v1/internal/quota/{tenant_id}` endpoint is reachable from the
engine pod/container.

### JWT Attack Defences

| Attack | Defence |
|--------|---------|
| `alg=none` | EdDSA verifier hardcodes `EdDSA` algorithm — any token with `alg=none` or an unexpected algorithm is rejected before signature check. |
| Algorithm confusion (RS256 vs EdDSA) | Single key type (Ed25519) registered; no RSA/HMAC keys present — confusion attacks have no valid target. |
| Tampered payload | Signature covers the full header+payload; any byte change invalidates the Ed25519 signature. |
| Key enumeration | Public key served only at `/api/v1/auth/keys` (JWKS). Private key never leaves the keys directory. |
| Replay after expiry | `exp` claim mandatory; verifier rejects expired tokens even with valid signature. |

---

## Security Headers

### API and Admin Routes (`/api/*`, `/admin/*`)

Applied by `SecurityHeadersMiddleware` in `internal/delivery/http/security_middleware.go`:

| Header | Value |
|--------|-------|
| `X-Content-Type-Options` | `nosniff` |
| `X-Frame-Options` | `DENY` |
| `Content-Security-Policy` | `default-src 'self'; frame-ancestors 'none'` |
| `Referrer-Policy` | `strict-origin-when-cross-origin` |
| `Strict-Transport-Security` | `max-age=31536000; includeSubDomains` *(TLS only)* |

HSTS is set only when `r.TLS != nil` or `X-Forwarded-Proto: https` is present.
Operators running behind a cleartext reverse proxy must enable HSTS at the proxy
level rather than relying on the engine to emit it.

### Widget Routes (`/widget.js`, widget-embedded paths)

Applied by `WidgetSecurityHeadersMiddleware` in the same file:

| Header | Value |
|--------|-------|
| `X-Content-Type-Options` | `nosniff` |
| `Content-Security-Policy` | `default-src 'self'; frame-ancestors <origins>` |
| `Referrer-Policy` | `strict-origin-when-cross-origin` |
| `Strict-Transport-Security` | `max-age=31536000; includeSubDomains` *(TLS only)* |

`X-Frame-Options` is intentionally omitted for widget routes because the widget
is designed to be embedded in third-party pages. The `frame-ancestors` directive
in CSP enforces the embedding allow-list instead.

`<origins>` comes from the per-tenant `settings.widget_embed_origins` key (a
JSON array of origin strings, read at request time). An empty list produces
`frame-ancestors 'none'`, which blocks all embedding.

### Overriding CSP

A downstream handler can set `Content-Security-Policy` before calling
`next.ServeHTTP`. The middleware checks `w.Header().Get("Content-Security-Policy")`
and skips writing if it is already non-empty.

## CORS Policy

### Default (no configured origins)

When `CORSOrigins` is empty or nil, the server uses a same-origin policy:
cross-origin requests receive no `Access-Control-Allow-Origin` header.
There is **no wildcard fallback** (`*`). Same-origin requests are unaffected.

### Configured Origins

`BootstrapEngine.CORSOrigins` (config key: `engine.cors_origins`, environment
variable: `ENGINE_CORS_ORIGINS` as a comma-separated list) provides the explicit
allow-list. Only listed origins receive CORS headers.

```yaml
# config.yaml
engine:
  cors_origins:
    - https://app.example.com
    - https://staging.example.com
```

```env
ENGINE_CORS_ORIGINS=https://app.example.com,https://staging.example.com
```

### Widget Origin Union

Widget embed origins (from `settings.widget_embed_origins`) are union-merged
into the CORS allow-list for widget routes, so embedded pages can make
authenticated requests to the widget chat endpoint.

## Strict-Transport-Security

HSTS is applied only when one of the following is true:

- The TCP connection was established over TLS (`r.TLS != nil`).
- The `X-Forwarded-Proto: https` header is present (set by the upstream proxy).

**Operators running behind a cleartext proxy** (e.g. nginx → engine over HTTP)
must configure HSTS at the proxy layer. The engine will not emit HSTS for
cleartext connections even if the public endpoint is HTTPS.

## Observability — Structured Logging

All `slog` calls in production code use the `Context` variant
(`slog.InfoContext`, `slog.WarnContext`, `slog.ErrorContext`, `slog.DebugContext`)
so that request-scoped attributes (`tenant_id`, `user_sub`, `trace_id`) injected
by middleware are automatically included in every log line.

The `scripts/slog-lint.sh` script enforces this at CI time. It fails if any
bare `slog.Info(...)` / `slog.Warn(...)` / `slog.Error(...)` / `slog.Debug(...)`
call is found outside `_test.go` files.

When no request context is in scope (e.g. startup, background goroutines),
`context.Background()` is used as a safe fallback.

## Applying Middleware

`SecurityHeadersMiddleware` must be registered on router groups before route
handlers are mounted. In `internal/delivery/http/server.go`:

```go
// API + admin routes
r.Group(func(r chi.Router) {
    r.Use(SecurityHeadersMiddleware)
    // ... route registrations
})

// Widget routes
r.Group(func(r chi.Router) {
    r.Use(WidgetSecurityHeadersMiddleware(embedOrigins))
    r.Get("/widget.js", widgetFileHandler)
})
```

## Metering HMAC Key Rotation

The engine↔landing metering boundary is protected with HMAC-SHA256. Both sides
support two secrets simultaneously so that a 90-day rotation can happen without
downtime.

### Env vars

**Engine (EE/Cloud binary — producer)**

| Variable | Required | Description |
|---|---|---|
| `SYNTHETICBREW_METERING_HMAC_CURRENT` | yes (Cloud mode) | Active signing secret. Engine always signs with this key. |
| `SYNTHETICBREW_METERING_HMAC_PREVIOUS` | rotation only | Accepted by the verifier during the rotation window. The engine ignores this variable. |

**Landing (cloud-api — verifier)**

| Variable | Required | Description |
|---|---|---|
| `METERING_HMAC_CURRENT` | yes (if internal API enabled) | Active secret — checked first on every request. |
| `METERING_HMAC_PREVIOUS` | rotation only | Previous secret — checked only when `CURRENT` fails, and a warning is logged. |

### Zero-downtime rotation procedure

1. **Generate** a new secret on the operator workstation:
   ```bash
   S2=$(openssl rand -hex 32)
   ```

2. **Roll landing first** — set both secrets, redeploy cloud-api:
   ```
   METERING_HMAC_CURRENT=<S2>
   METERING_HMAC_PREVIOUS=<S1>   # S1 = current active secret before rotation
   ```
   Landing now accepts requests signed with either S1 or S2.

3. **Roll engine** — set only the new current secret, redeploy:
   ```
   SYNTHETICBREW_METERING_HMAC_CURRENT=<S2>
   ```
   Engine signs all new requests with S2. Landing validates them via `CURRENT`.

4. **Wait** — allow at least 24 hours for any in-flight events signed with S1
   to drain through the metering pipeline.

5. **Remove previous** — clear `METERING_HMAC_PREVIOUS` on landing, redeploy:
   ```
   METERING_HMAC_CURRENT=<S2>
   # METERING_HMAC_PREVIOUS removed
   ```
   Rotation complete. S1 is no longer accepted.

> **Note:** Step 4 is optional in low-traffic environments; the 24-hour window
> is a conservative bound. Any in-flight event that arrives at landing after
> step 5 and was signed with S1 will be rejected 401 — the metering client
> will log a warning and drop it (fail-open design).

## Widget Embed Origins

The `/widget.js` route must be embeddable inside third-party pages. Embedding
is controlled per-tenant via the `widget_embed_origins` setting key.

### Setting key

| Field | Value |
|-------|-------|
| Key | `widget_embed_origins` |
| Type | JSON array of origin strings |
| Scope | Per-tenant (one row per `tenant_id` in the `settings` table) |
| Default | *(not set)* → `frame-ancestors 'none'` — widget cannot be embedded |

### How it works at runtime

On every request to `/widget.js`, `WidgetSecurityHeadersMiddleware` reads the
tenant ID from the request context (injected by the tenant middleware), calls
`GetWidgetEmbedOrigins`, and sets:

```
Content-Security-Policy: default-src 'self'; frame-ancestors <origin1> <origin2> …
```

If the setting is absent or the tenant cannot be resolved, the safe default
`frame-ancestors 'none'` is used instead — no embedding is permitted.

`X-Frame-Options` is intentionally **omitted** on widget routes (legacy header
would block embedding in browsers that honour it over CSP).

### How to configure

**Via Admin UI:** Settings page → key `widget_embed_origins` → value (JSON array).

**Via API:**

```bash
curl -X PUT https://<engine>/api/v1/settings/widget_embed_origins \
  -H "Authorization: Bearer <admin-token>" \
  -H "Content-Type: application/json" \
  -d '{"value": "[\"https://partner.example.com\",\"https://staging.example.com\"]"}'
```

**Example value stored in the database:**

```json
["https://partner.example.com", "https://staging.example.com"]
```

### Security notes

- Only origins explicitly listed in `widget_embed_origins` can embed the widget.
- An attacker-controlled origin not in the list receives `frame-ancestors 'none'`
  and the browser will refuse to render the widget inside the attacker's `<iframe>`.
- Changes take effect on the next request — no engine restart required.
