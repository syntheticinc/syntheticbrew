# Authentication Flow — EdDSA JWT Architecture

## Overview

SyntheticBrew Engine uses **Ed25519 (EdDSA)** for all JWT signing as of Wave 1+7 (2026-04-21). The authentication architecture supports two modes: **local** (single-node, default) and **external** (multi-replica, pre-provisioned keys).

```
┌──────────────────────────────────────────────────────────────┐
│ Admin SPA (browser)                                           │
│ ┌────────────────────────────────────────────────────────┐  │
│ │ VITE_AUTH_MODE=local or external                       │  │
│ │ local: calls /api/v1/auth/local-session                │  │
│ │ external: parses at=<token> from URL fragment         │  │
│ └────────────────────────────────────────────────────────┘  │
└──────────────────────────────────────────────────────────────┘
                              │
                              │ JWT (Ed25519 signed)
                              │
┌──────────────────────────────────────────────────────────────┐
│ SyntheticBrew Engine                                               │
│ ┌────────────────────────────────────────────────────────┐  │
│ │ Auth Mode: local (default) or external                │  │
│ │                                                        │  │
│ │ local:                                                │  │
│ │  • Generates Ed25519 keypair on first boot            │  │
│ │  • Stores at SYNTHETICBREW_JWT_KEYS_DIR                   │  │
│ │  • Exposes POST /api/v1/auth/local-session           │  │
│ │  • Single-node deployments only                       │  │
│ │                                                        │  │
│ │ external:                                              │  │
│ │  • Loads public key from SYNTHETICBREW_JWT_PUBLIC_KEY_PATH │  │
│ │  • Verifies tokens issued elsewhere                   │  │
│ │  • Multi-replica deployments                          │  │
│ │  • /api/v1/auth/local-session NOT registered         │  │
│ └────────────────────────────────────────────────────────┘  │
└──────────────────────────────────────────────────────────────┘
```

---

## Local Mode (Default)

### How It Works

1. **Keypair Generation** (first boot):
   - Engine generates Ed25519 keypair under `<SYNTHETICBREW_JWT_KEYS_DIR>/jwt_ed25519.priv` and `.pub`
   - Atomic creation via O_EXCL flag prevents races in multi-pod scenarios
   - Keypair is ~64 bytes total

2. **Session Creation**:
   ```bash
   curl -X POST http://localhost:8443/api/v1/auth/local-session \
     -H "Content-Type: application/json" \
     -d '{
       "username": "admin",
       "password": "your-secure-password"
     }'
   ```

3. **JWT Response**:
   ```json
   {
     "token": "eyJhbGciOiJFZERTQSIsInR5cCI6IkpXVCJ9...",
     "expires_in": 86400,
     "user_sub": "admin-user-id-or-name"
   }
   ```

4. **Token Structure** (Ed25519-signed):
   ```
   Header: {"alg": "EdDSA", "typ": "JWT"}
   Payload: {
     "sub": "admin",
     "exp": 1713877200,
     "iat": 1713790800,
     "auth_mode": "local"
   }
   Signature: <Ed25519 signature>
   ```

### Configuration

**Environment variables:**

| Variable | Required | Default | Description |
|---|---|---|---|
| `SYNTHETICBREW_AUTH_MODE` | yes | `local` | Must be `local` for this mode |
| `SYNTHETICBREW_JWT_KEYS_DIR` | no | `/var/lib/syntheticbrew/keys` | Directory where keypair is stored (must be writable) |
| `ENGINE_PORT` | no | `8443` | HTTP listening port |

**Docker Compose:**

```yaml
services:
  engine:
    image: syntheticinc/syntheticbrew:latest
    ports:
      - "8443:8443"
    environment:
      - DATABASE_URL=postgresql://user:pass@db:5432/syntheticbrew
      - SYNTHETICBREW_AUTH_MODE=local
      - SYNTHETICBREW_JWT_KEYS_DIR=/var/lib/syntheticbrew/keys
    volumes:
      - keys-data:/var/lib/syntheticbrew/keys
    restart: unless-stopped

volumes:
  keys-data:
```

### Admin SPA Configuration

**Build time (Vite env):**

```
VITE_AUTH_MODE=local
VITE_API_TARGET=http://localhost:8443
```

When `VITE_AUTH_MODE=local`, the SPA:
1. Shows a login form (username + password)
2. POSTs credentials to `/api/v1/auth/local-session`
3. Receives JWT token and stores in `localStorage`
4. Automatically includes token in `Authorization: Bearer` header for all API requests

### Key Rotation

To rotate the keypair in local mode (invalidates all existing sessions):

1. **Stop the engine**:
   ```bash
   docker compose stop engine
   ```

2. **Delete the old keypair**:
   ```bash
   rm /var/lib/syntheticbrew/keys/jwt_ed25519.priv /var/lib/syntheticbrew/keys/jwt_ed25519.pub
   ```

3. **Start the engine** (generates new keypair):
   ```bash
   docker compose start engine
   ```

All admin users must log in again. Previous tokens are now invalid.

---

## External Mode (Multi-Replica)

### How It Works

1. **Pre-provisioned Public Key**:
   - Operator generates Ed25519 keypair **outside the engine** (e.g., on a landing server)
   - Only the public key is mounted into engine pods via ConfigMap or Secret
   - Engine never generates keys in external mode

2. **Token Verification**:
   ```
   Cloud Landing (private key) ──(signs JWT)──> JWT token
                                                      │
                                                      │
                                                      ▼
   Engine (public key)         ←────(verifies)─── Application
   ```

3. **Token Structure**:
   Same as local mode (Ed25519-signed), but issued by external service:
   ```
   {
     "sub": "user@cloud.ai",
     "exp": 1713877200,
     "iat": 1713790800,
     "auth_mode": "external"
   }
   ```

### Configuration

**Environment variables:**

| Variable | Required | Default | Description |
|---|---|---|---|
| `SYNTHETICBREW_AUTH_MODE` | yes | — | Must be `external` for this mode |
| `SYNTHETICBREW_JWT_PUBLIC_KEY_PATH` | yes | — | Path to hex-encoded Ed25519 public key file |
| `ENGINE_PORT` | no | `8443` | HTTP listening port |

**Docker Compose (with ConfigMap):**

```yaml
services:
  engine:
    image: syntheticinc/syntheticbrew:latest
    ports:
      - "8443:8443"
    environment:
      - DATABASE_URL=postgresql://user:pass@db:5432/syntheticbrew
      - SYNTHETICBREW_AUTH_MODE=external
      - SYNTHETICBREW_JWT_PUBLIC_KEY_PATH=/etc/syntheticbrew/jwt_ed25519.pub
    volumes:
      - ./jwt_ed25519.pub:/etc/syntheticbrew/jwt_ed25519.pub:ro
    restart: unless-stopped
```

**Kubernetes (with ConfigMap):**

```bash
# Create ConfigMap from public key file
kubectl create configmap syntheticbrew-pubkey \
  --from-file=jwt_ed25519.pub=/path/to/jwt_ed25519.pub

# Then mount in Helm values or patch deployment
kubectl patch deployment syntheticbrew-engine --type json -p '[
  {"op": "add", "path": "/spec/template/spec/containers/0/volumeMounts/-", 
   "value": {"name": "pubkey", "mountPath": "/etc/syntheticbrew/keys", "readOnly": true}},
  {"op": "add", "path": "/spec/template/spec/volumes/-",
   "value": {"name": "pubkey", "configMap": {"name": "syntheticbrew-pubkey"}}}
]'
```

### Admin SPA Configuration

**Build time (Vite env):**

```
VITE_AUTH_MODE=external
VITE_LANDING_URL=https://syntheticbrew.ai
```

When `VITE_AUTH_MODE=external`, the SPA:
1. Checks URL fragment for `at=<token>` (e.g. `#at=eyJ...`)
2. If found: parses token, stores in `localStorage`, initializes API client
3. If not found: redirects to `VITE_LANDING_URL/login` to obtain token from cloud landing
4. Verifies token `exp` claim before using (client-side safety check)

### Generating Public Keys

**On the landing server (or offline):**

```bash
# Generate Ed25519 keypair
openssl genpkey -algorithm ed25519 -out jwt_ed25519.priv

# Extract public key
openssl pkey -in jwt_ed25519.priv -pubout -out jwt_ed25519.pub

# Convert to hex for engine (if required by your JWT library)
openssl pkey -in jwt_ed25519.pub -text -noout | grep -A1 "pub:" | tail -1 | xxd -r -p | xxd -p
```

### Key Rotation in External Mode

To rotate the public key without downtime:

1. **Generate new keypair** on the landing server (keep private key secure)
2. **Extract new public key** and push to all engine deployments:
   - Docker: update ConfigMap, restart pods
   - Kubernetes: update ConfigMap, rolling restart
   - Bare metal: update `/etc/syntheticbrew/jwt_ed25519.pub`, restart service
3. **Update the landing server** to sign tokens with the new private key
4. **Wait for old tokens to expire** (typically 24 hours) or force logout

During the rotation window, the engine accepts both old and new public keys. Once all old tokens have expired, you can remove the old public key from the rotation.

---

## API Tokens (Non-Interactive)

In addition to session JWTs, the engine supports **API tokens** for programmatic access. These are long-lived tokens created via the Admin Dashboard or API, tied to a specific user (`user_sub`).

```bash
# Create API token (returns base64-encoded secret)
curl -X POST http://localhost:8443/api/v1/api-tokens \
  -H "Authorization: Bearer <admin-jwt>" \
  -H "Content-Type: application/json" \
  -d '{"name": "CI/CD pipeline", "expires_in": 7776000}'

# Use API token in requests
curl -X POST http://localhost:8443/api/v1/agents/my-agent/chat \
  -H "Authorization: Bearer bb_pk_your_api_token_secret" \
  -H "Content-Type: application/json" \
  -d '{"message": "Hello", "session_id": "sess_123"}'
```

API tokens are verified the same way as session JWTs — Ed25519 signature validation — but carry a `token_type: "api"` claim.

---

## Breaking Changes (Wave 1+7)

### Removed

- `JWT_SECRET` environment variable (HS256 signing) — **REMOVED**
- `security.jwt_secret` config key — **REMOVED**
- `POST /api/v1/auth/login` endpoint (username/password via HS256) — **REMOVED** (returns 404)
- `syntheticbrew-ce admin` CLI subcommand (create, list, reset-password) — **REMOVED**
- HS256 JWT algorithm support (Engine only accepts EdDSA) — **REMOVED**

### Replaced With

- `SYNTHETICBREW_AUTH_MODE` (local or external)
- `SYNTHETICBREW_JWT_KEYS_DIR` (local mode keypair storage)
- `SYNTHETICBREW_JWT_PUBLIC_KEY_PATH` (external mode public key)
- `POST /api/v1/auth/local-session` endpoint (EdDSA-signed tokens)
- Admin user management via Admin Dashboard UI
- Ed25519 (EdDSA) JWT signing (all tokens)

### Migration Path

**From v1 to v2 (no in-place upgrade):**

1. Fresh v2 install: `docker compose down -v && docker compose up -d`
2. Or migrate via snapshot (backup v1 database, restore to v2 schema with Liquibase migration `002_drop_users_unify_identity.yaml`)
3. Create initial admin user via Admin Dashboard login flow (local mode) or cloud landing (external mode)

---

## Security Considerations

### Local Mode

- **Single-node only**: Multiple pods sharing the same keypair directory can race; use external mode for HA
- **Keypair protection**: Store `SYNTHETICBREW_JWT_KEYS_DIR` on a filesystem with restricted permissions (mode 0700)
- **Session expiration**: Tokens expire after 24 hours; consider re-login flows for long-running operations
- **No rotation**: To rotate, delete keypair (all sessions invalidated); plan rotation windows

### External Mode

- **Private key security**: The private key NEVER reaches the engine; keep it secure on the issuing service (landing server, SSO provider)
- **Public key integrity**: Distribute via ConfigMap Secret or bind-mount; consider HTTPS pinning
- **Rotation strategy**: Can rotate public keys with a brief window of dual-key acceptance (see key rotation section above)
- **Token validation**: Engine validates `exp` claim; ensure clock skew is < 60 seconds between issuer and engine

### General

- **Use HTTPS**: Always deploy behind a TLS reverse proxy (Caddy, nginx) in production
- **Token storage**: Admin SPA stores tokens in `localStorage`; consider SameSite cookies for web-only deployments
- **Audit logging**: All token validations (success and failure) are logged with `slog` to facilitate security audits

---

## Troubleshooting

| Issue | Symptom | Solution |
|---|---|---|
| **Local mode: keys not generated** | Engine logs "permission denied" on startup | Check `SYNTHETICBREW_JWT_KEYS_DIR` is writable by engine process |
| **External mode: token rejected** | 401 response with "invalid signature" | Verify public key matches the private key used to sign token |
| **SPA shows login loop** | Admin SPA redirects to landing repeatedly | Ensure `VITE_AUTH_MODE` matches engine mode (`local` or `external`) |
| **Token expired after 24 hours** | 401 after inactivity | Implement automatic token refresh or re-login flow in frontend |
| **Multi-pod local mode racing** | Inconsistent startup, pods failing | Switch to external mode for multi-replica deployments |

---

## See also

- [`auth-scopes.md`](./auth-scopes.md) — per-endpoint scope bitmask
  reference, actor matrix (admin JWT / api_token / end-user JWT), and
  pinned by-design behaviours (anti-impersonation guard on `/chat`,
  trusted-proxy session creation on `POST /sessions`).
- [`api-contracts.md`](./api-contracts.md) — endpoint contracts +
  request/response shapes.
- [`security-hardening.md`](./security-hardening.md) — deployment-time
  hardening checklist.
- [`models-kind.md`](./models-kind.md) — model kind invariants (chat vs
  embedding) and how they interact with KB linkage.

