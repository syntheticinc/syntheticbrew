# syntheticbrew-engine Helm Chart

Helm chart for deploying the **SyntheticBrew AI Agent Engine** (Community Edition) on Kubernetes.

## Stability

Per OSS testing transparency convention (Kubernetes feature gates / Helm Artifact
Hub maturity / Apache Incubator pattern), each chart feature is labelled with how
it has been validated. Pick what your environment supports and treat
`Beta`/`Experimental` paths as community-feedback territory.

| Feature                                | Tier         | Tested how |
|----------------------------------------|--------------|------------|
| Default install (single-shot)          | **Stable**   | CI gate (kind v1.28/1.30/1.31, install + upgrade + rollback) + production canary |
| External Postgres + ESO + Vault        | **Stable**   | CI gate + production canary |
| Bootstrap admin token + configApply    | **Stable**   | CI gate (full single-shot flow) |
| Schema/KB name-keyed URLs (engine 1.1.0+) | **Stable** | Engine unit + integration suite; chart-test integration-knowledge end-to-end |
| Declarative Knowledge / RAG ingest (`knowledgeLoader`) | **Stable** | CI gate (kind v1.30 install — asserts loader Job uploaded N files into KB) |
| Migrations Job (Liquibase)             | **Stable**   | CI gate asserts `databasechangelog` populated |
| HTTPRoute (Gateway API v1)             | **Stable**   | CI render-validated + production canary against Envoy Gateway |
| `containerSecurityContext.readOnlyRootFilesystem: true` (auto /tmp emptyDir) | **Stable** | CI render-validated; smoke runs render-only on kind |
| `replicaCount=1` enforcement (`auth.mode=local`) | **Stable** | CI gate (template `fail` on `replicaCount > 1`) |
| `config.auth.existingKeysSecret` (keypair via Secret) | **Beta** | CI render-validated |
| `config.knowledge.storage` (`none`/`local`) | **Beta** | CI render-validated |
| AWS IRSA annotations                   | **Beta**     | CI render-validated only — no AWS account in CI; community feedback welcome |
| GCP Workload Identity annotations      | **Beta**     | CI render-validated only — no GCP account in CI |
| NetworkPolicy enabled                  | **Beta**     | CI render-validated only — kind default CNI does NOT enforce NetworkPolicy |
| Argo CD pull GitOps                    | **Experimental** | Example-only (`examples/argocd-application.yaml`); not exercised in CI |
| Flux CD HelmRelease                    | **Experimental** | Not exercised in CI |
| Multi-replica HA (`auth.mode=external`)| **Out of CE** | EE feature — requires external JWT IdP, not in this chart |

**Known limitations (v0.4.2):**

- **ServiceAccount as hook.** Rendered as a Helm pre-install/pre-upgrade hook
  so it exists in time for the migrations Job. Side effects: SA is not deleted
  on `helm uninstall` (orphan; clean up via `kubectl delete sa
  <release>-syntheticbrew-engine` or by deleting the namespace), and during
  `helm upgrade` there is a sub-second window during SA recreation where new
  pods cannot schedule. Both will be removed in v0.5.0 by relocating
  migrations from a hook to a Deployment init container.

- **`helm rollback` does NOT downgrade the database schema.** Liquibase
  `update` is forward-only. After rolling back to an older chart revision,
  the engine pod will still see the newer schema applied by the previous
  upgrade. If the older engine image references columns/tables that no
  longer exist (or expects narrower schema), it will crash on first DB read.
  **To roll back the engine version safely, also restore the database
  from a pre-upgrade snapshot** (e.g. `pg_dump` taken before `helm upgrade`).
  The chart cannot do this for you because rollback semantics are
  application-specific.

- **HPA + `auth.mode=local` is rejected at template time.** Local auth
  persists the JWT keypair on a single-writer PVC; multi-replica races and
  produces intermittent auth failures. The chart `fail`s at render if
  `autoscaling.enabled=true` while `autoscaling.maxReplicas > 1`. Use
  `auth.mode=external` for HA.

## Quick Install

```bash
helm install syntheticbrew-engine oci://ghcr.io/syntheticinc/charts/syntheticbrew-engine \
  --version 0.9.5 \
  --set image.tag=1.7.0 \
  --set postgresql.external.host=my-postgres \
  --set postgresql.external.password=secret
```

## Configuration

| Parameter | Description | Default |
|-----------|-------------|---------|
| `image.tag` | Engine image tag (pin to specific version) | `latest` |
| `replicaCount` | Number of replicas (local auth mode: 1 only) | `1` |
| `config.auth.mode` | Auth mode: `local` or `external` | `local` |
| `postgresql.external.host` | PostgreSQL host | `""` |
| `postgresql.external.existingSecret` | Existing Secret with `DATABASE_URL` key | `""` |
| `migrations.enabled` | Run Liquibase migrations Job on install/upgrade | `true` |
| `configApply.enabled` | Run brewctl config-apply Job on install/upgrade | `false` |
| `serviceAccount.create` | Create a ServiceAccount | `true` |
| `serviceAccount.annotations` | ServiceAccount annotations (IRSA, WI) | `{}` |
| `httpRoute.enabled` | Create Gateway API HTTPRoute | `false` |
| `networkPolicy.enabled` | Create NetworkPolicy | `false` |
| `podSecurityContext` | Pod-level security context | see values.yaml |
| `containerSecurityContext` | Container-level security context | see values.yaml |

See `values.yaml` for the full list of parameters.

## Securing self-hosted

`auth.mode=local` (default) has **no real authentication** — engine signs its
own Ed25519 keypair and any request reaching the listen address can mint an
admin session via `POST /api/v1/auth/local-session`. This is by design for
CE single-node dev / CI / single-developer port-forward setups, where the
network perimeter (loopback, ClusterIP, VPN, firewall) is the actual trust
boundary.

A startup `WARN` is emitted when `auth.mode=local` and the HTTP listener
binds to anything other than `127.0.0.1` / `::1` / `localhost`, so a public
exposure does not go unnoticed. The recommended deployment patterns:

| Scenario | Recommended setup |
|---|---|
| Local dev, CI, single-developer | `auth.mode=local`, keep bind on loopback or behind ClusterIP / VPN. The WARN can be ignored when you control the perimeter. |
| Headless automation (brewctl, curl) | `BOOTSTRAP_ADMIN_TOKEN` — long-lived `bb_<hex>` API token, sent as `Authorization: Bearer <token>`. Independent of session JWTs. |
| Production self-hosted | `auth.mode=external` + a reverse proxy that enforces identity (oauth2-proxy, Authelia, Cloudflare Access, nginx `basic_auth`, Tailscale serve, etc.). Engine validates pre-provisioned EdDSA JWTs but does not itself authenticate users. |

There is intentionally no built-in admin password / login form. A single
`admin/<password>` from `values.yaml` would be cleartext in git or k8s
secrets — equivalent to no auth against any attacker that already reached
the cluster, and it would not buy real defence-in-depth. Use a battle-tested
identity proxy instead.

## Security prerequisites for production (self-hosted)

Install prerequisites the engine assumes — verify before exposing it to
untrusted clients:

- **Rate limiting + request-size limits live at the edge.** The engine
  delegates per-IP rate limiting to a reverse proxy by design and has no global
  in-process throttle. Put a proxy (nginx / Caddy / Envoy) in front with a
  request rate limit and a body-size cap. (The chat endpoint additionally bounds
  its own body at 1 MB as a backstop, but per-IP throttling is the edge's job.)
- **Secrets are stored at rest in the engine database.** Provider API keys —
  and, when the chart-managed Secret is used, the PostgreSQL password — are
  stored plaintext-at-rest in the DB / k8s Secrets. Enable encryption-at-rest
  for the database volume and etcd, and prefer
  `postgresql.external.existingSecret` + `config.auth.existingKeysSecret` over
  chart-generated secrets.
- **Model `base_url` is operator-controlled.** A model's `base_url` is validated
  for URL format only; an operator with model-write scope can point it at any
  reachable host (intended — on-prem gateways, ollama on localhost). Restrict
  who holds model-write scope.

## Availability on node churn / autoscaling clusters

A single-replica engine (`auth.mode=local`) can wedge for a long time when its
node is drained, cordoned, or churned by the cluster-autoscaler. The cause is
**two ReadWriteOnce PVCs on the pod's startup critical path**:

1. the **JWT keypair PVC** (`persistence.keys`, written by local auth), and
2. the **knowledge raw-files PVC** (`persistence.knowledge`).

RWO is a block volume bound to one node at a time. When the pod moves, the CSI
driver must detach from the old node before attaching to the new one — and on
some drivers (e.g. Hetzner CSE) that detach can lock for a long time. The
rescheduled pod sits in `ContainerCreating` (→ 502) until the volume frees.

This chart lets you take **both** PVCs off the critical path so the single pod
reschedules to any node in seconds:

- **`config.auth.existingKeysSecret`** — mount the Ed25519 keypair READ-ONLY
  from a Secret instead of writing it to a PVC. A Secret is replicated through
  the API server and mounts on any node with no block-volume attach/detach, so
  no keys PVC is created. See the RUNBOOK for how to provision the Secret.
- **`config.knowledge.storage: none`** — keep live knowledge in PostgreSQL only
  and write no raw files, so no knowledge PVC is created and the pod is
  stateless. (`local` keeps the raw-files PVC; empty derives the mode from
  `persistence.knowledge.enabled` for back-compat.)

When neither PVC is mounted the chart auto-selects `strategy: RollingUpdate`
(near-zero redeploy downtime); with any RWO PVC it stays `Recreate` to avoid the
upgrade deadlock. `clusterAutoscaler.safeToEvict` (default `false`) also renders
`cluster-autoscaler.kubernetes.io/safe-to-evict` on the pod so the autoscaler
does not proactively evict the single stateful pod during node optimisation —
set `true` once both PVCs are off the path and you want the pod freely movable.

```yaml
config:
  auth:
    mode: local
    existingKeysSecret: syntheticbrew-jwt-keys   # keypair from a read-only Secret
  knowledge:
    storage: none                                # stateless — Postgres-only
persistence:
  keys:
    enabled: false                               # no keys PVC
clusterAutoscaler:
  safeToEvict: true                              # allow the autoscaler to move the pod
```

This is **single-replica resilience, NOT high availability** — there is still
one pod. A graceful node drain reschedules it quickly; a hard node crash
mid-request loses the in-flight turn (the conversation history lives in
PostgreSQL, so the client simply retries). For real HA (multiple replicas) use
`auth.mode=external` with an external JWT IdP, which is out of scope for CE.

## Integrations

### Helmfile (similar GitOps stacks)

```yaml
repositories:
  - name: syntheticbrew
    url: oci://ghcr.io/syntheticinc/charts

releases:
  - name: syntheticbrew-engine
    chart: oci://ghcr.io/syntheticinc/charts/syntheticbrew-engine
    version: 0.4.0
    namespace: {{ .Environment.Name }}
    values:
      - ./values/{{ .Environment.Name }}/syntheticbrew-engine.yaml
    needs:
      - postgresql-syntheticbrew
```

### External Secrets Operator + Vault

Use `existingSecret` to skip the chart-managed Secret and pull `DATABASE_URL`
from your ESO-managed Secret:

```yaml
postgresql:
  external:
    existingSecret: syntheticbrew-config
    existingSecretKey: DATABASE_URL
```

### AWS IRSA

Bind an IAM Role to the engine pod via ServiceAccount annotation:

```yaml
serviceAccount:
  annotations:
    eks.amazonaws.com/role-arn: arn:aws:iam::123456789012:role/syntheticbrew-engine
```

### GCP Workload Identity

```yaml
serviceAccount:
  annotations:
    iam.gke.io/gcp-service-account: syntheticbrew-engine@my-project.iam.gserviceaccount.com
```

### Argo CD

See `examples/argocd-application.yaml` for both Git-based and OCI-based source variants.

### Read-only root filesystem (opt-in security best practice)

Engine writes temp files to `/tmp`. To enable a read-only root filesystem:

```yaml
containerSecurityContext:
  readOnlyRootFilesystem: true

extraVolumes:
  - name: tmp
    emptyDir: {}

extraVolumeMounts:
  - name: tmp
    mountPath: /tmp
```

### Gateway API HTTPRoute (Envoy Gateway / Cilium / Istio)

```yaml
ingress:
  enabled: false

httpRoute:
  enabled: true
  routes:
    - nameSuffix: api
      hostnames:
        - syntheticbrew.dev.example.com
      parentRefs:
        - name: internal
          namespace: envoy-gateway-system
          sectionName: https-dev-example-com
      rules:
        - matches:
            - path: /
              pathType: PathPrefix
          servicePort: 8443
```

### NetworkPolicy

```yaml
networkPolicy:
  enabled: true
  ingressFrom:
    - podSelector: {}
    - namespaceSelector:
        matchLabels:
          kubernetes.io/metadata.name: envoy-gateway-system
```

### Single-shot deployment with `bootstrapAdminToken`

For fully-automated GitOps deploys (no manual Admin UI step), pre-mint
an admin token and reference it from both `bootstrapAdminToken.existingSecret`
(engine) and `configApply.tokenSecret` (brewctl Job):

```yaml
bootstrapAdminToken:
  enabled: true
  existingSecret: syntheticbrew-config

configApply:
  enabled: true
  tokenSecret: syntheticbrew-config
  apiKeysSecret: syntheticbrew-config
```

Engine seeds the token into `api_tokens` on first boot (idempotent — safe
to re-apply). Token format: `bb_<64-hex>` — generate via
`echo "bb_$(openssl rand -hex 32)"`.

Requires engine image **v1.0.1 or later** for `SYNTHETICBREW_BOOTSTRAP_ADMIN_TOKEN`
soft seeding (invalid format → WARN log + skip seed). Engine **v1.0.2+** adds
fail-fast on invalid token format — process exits with a clear cause logged,
producing CrashLoopBackOff visible in `kubectl describe pod` (chart `appVersion`
1.0.2 reflects this).

### Declarative Knowledge / RAG ingest (`knowledgeLoader`)

Chart 0.5.0+ ships a post-install Helm hook that uploads files from a
ConfigMap into a knowledge base declared in your `configApply` bundle —
no hand-rolled bash on top of `helmfile sync`.

**Pieces:**

1. **Embedding model + KB + agent linkage** in the brewctl bundle:

    ```yaml
    configApply:
      enabled: true
      tokenSecret: syntheticbrew-config
      apiKeysSecret: syntheticbrew-config              # holds OPENROUTER_API_KEY
      config: |
        models:
          - name: chat-default
            type: openai_compatible
            model_kind: chat
            base_url: https://openrouter.ai/api/v1
            model_name: openai/gpt-4o-mini
            api_key: ${OPENROUTER_API_KEY}
            is_default: true
          - name: embed-default
            type: openai_compatible
            model_kind: embedding
            base_url: https://api.openai.com/v1
            model_name: text-embedding-3-small
            api_key: ${OPENROUTER_API_KEY}
            embedding_dim: 1536
        knowledge_bases:
          - name: handbook
            description: "Company handbook"
            embedding_model: embed-default
        agents:
          - name: support
            model: chat-default
            knowledge_bases: [handbook]
            lifecycle: persistent
            system_prompt: "Answer using the handbook."
        schemas:
          - name: support-chat
            entry_agent: support
            chat_enabled: true
    ```

2. **ConfigMap with the actual document files.** Either render via
   helmfile/Kustomize, or build it as a CI step before sync:

    ```bash
    kubectl create configmap handbook-files \
      --from-file=./knowledge/policy.md \
      --from-file=./knowledge/faq.md \
      --dry-run=client -o yaml | kubectl apply -f -
    ```

3. **Loader values.** Enable the hook + the writable PVC engine needs to
   store uploaded files:

    ```yaml
    persistence:
      knowledge:
        enabled: true                             # required (engine writes
        size: 1Gi                                 # to {DATA_DIR}/knowledge/)
        storageClass: ""                          # cluster default

    knowledgeLoader:
      enabled: true
      kb: handbook                                # MUST match knowledge_bases[].name
      embeddingModel: embed-default               # MUST match models[].name (kind: embedding).
                                                  # Workaround for brewctl 0.1.0 — see note below.
      existingConfigMap: handbook-files           # ConfigMap from step 2
      mode: skip-existing                         # or replace / always (see below)
      prune: false                                # delete remote files not in CM
      # tokenSecret/tokenSecretKey default to configApply's
    ```

    > **Why `embeddingModel` is a separate field.** brewctl 0.1.0 has a
    > limitation: when models and knowledge_bases are declared in the same
    > bundle, brewctl resolves `embedding_model: <name>` against pre-apply
    > state — the embedding model isn't yet in `current.Models`, so the KB
    > is created with empty `embedding_model_id`. The loader Job probes
    > the KB after `configApply` runs, and if the link is missing it
    > resolves `knowledgeLoader.embeddingModel` to a model UUID and
    > `PATCH`es the KB. Self-healing — becomes a no-op once brewctl 0.1.1+
    > ships a two-pass apply.

**What runs:**

- `syntheticbrew-engine-config-apply` (Helm hook weight **10**) — brewctl applies
  the bundle, creates KB row in DB.
- `syntheticbrew-engine-knowledge-loader` (Helm hook weight **15**) — alpine pod
  installs `curl`+`jq` via apk (~3s), resolves `kb` to UUID, walks
  `/etc/syntheticbrew/knowledge-files/`, uploads each file via `POST
  /api/v1/knowledge-bases/{id}/files`. Idempotent across re-syncs.

**Mode trade-offs:**

| `mode`          | Behavior                                         | Embedding cost on no-op |
| --------------- | ------------------------------------------------ | ----------------------- |
| `skip-existing` | Skip if filename already in KB (default)         | $0                      |
| `replace`       | Skip if name+size match, else DELETE+POST        | $0 on size match        |
| `always`        | DELETE all KB files + re-upload everything       | full re-embedding       |

Until engine exposes `file_hash` on `GET /files` (planned 1.0.4),
`replace` mode uses `file_size` as a content-drift proxy. Length-preserving
edits (typo fixes of identical-length words) won't trigger a re-upload —
either rename the file or use `mode: always`.

**Prune (default off).** When `prune: true`, the loader DELETEs files in
the KB that are NOT present in the ConfigMap. Off by default because
operators may upload files via the Admin UI outside the GitOps loop —
`prune: true` would silently nuke them. Turn on only when the ConfigMap
is the single source of truth for the KB.

**Required envelope:**

- `configApply.enabled=true` with a matching `knowledge_bases:` entry
- `persistence.knowledge.enabled=true` (chart fail-fasts at render
  otherwise — engine writes to `{DATA_DIR}/knowledge/{tenant}/{kb}/`,
  no PVC means HTTP 500 on every upload)
- `configApply.apiKeysSecret` with a real key bound to the embedding
  model. Embeddings run async after upload — placeholder keys leave
  files in `status: error` (visible in Admin UI / `GET /files`).

**ConfigMap size limit.** Stock k8s caps a ConfigMap at ~1 MB of total
data. For larger corpora, switch `existingConfigMap` to a CSI-driver
projection (S3/GCS-backed) via `extraVolumes` — the loader script just
walks `/etc/syntheticbrew/knowledge-files/`, so the volume source is opaque.

**Security note.** The loader Job overrides chart's `runAsNonRoot=true`
to `runAsUser=0` because alpine `apk add` requires root for
`/var/cache/apk/`. Job is short-lived (seconds), only talks to the
in-cluster engine REST API, holds no persistent state. To keep
non-root, override `knowledgeLoader.image` to a pre-built variant with
`curl`+`jq` baked in (e.g. `your-registry/kb-loader:tag`) and re-add
`runAsNonRoot: true`.

## Rollback runbook

Recovery paths if a 1.1.0 / chart 0.6.0 deploy hits a critical issue
post-migration. The chart's Liquibase changeset has a clean rollback
clause; engine is forward-compatible with the CHECK constraint (a 1.0.x
engine talking to a 1.1.0 schema does not violate the new format on
inserts/updates of valid names).

### Engine pod CrashLoopBackOff after migration applied

```bash
# 1) Revert pod to the previous image (pre-1.1.0).
kubectl rollout undo deployment/<release>-syntheticbrew-engine

# 2) Drop the CHECK constraint via Liquibase rollback.
#    The migration changeset has a <rollback> clause documented in
#    engine/migrations/add-resource-name-format-check.yaml.
liquibase --changeLogFile=migrations/db.changelog-master.yaml \
  --url="$DATABASE_URL" rollbackCount 2

# 3) Helm rollback to chart 0.5.x (chart history is preserved by Helm).
helm rollback <release> <previous-revision>
```

### Migration pre-flight HALT mid-deploy

The migration's pre-flight changeset asserts every existing
`schemas.name` and `knowledge_bases.name` matches the new regex
(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`, max 100 chars). If any row violates,
Liquibase HALTs **before** applying the CHECK constraint — the DB stays
in pristine state.

```bash
# Inspect violating rows:
psql "$DATABASE_URL" <<'SQL'
SELECT 'schemas' AS table, name FROM schemas
  WHERE name !~ '^[a-z0-9]([-a-z0-9]*[a-z0-9])?$' OR length(name) > 100
UNION ALL
SELECT 'knowledge_bases', name FROM knowledge_bases
  WHERE name !~ '^[a-z0-9]([-a-z0-9]*[a-z0-9])?$' OR length(name) > 100;
SQL

# Normalize OR delete offending rows, then re-run helmfile sync.
```

### Post-deploy bug discovered in name validation

The DB CHECK constraint can be dropped without data loss — engine HTTP
layer continues to enforce the same regex (defense-in-depth survives
single-layer rollback). Hotfix as a 1.1.x patch with the corrected
validator.

```bash
psql "$DATABASE_URL" <<'SQL'
ALTER TABLE schemas DROP CONSTRAINT IF EXISTS chk_schemas_name_format;
ALTER TABLE knowledge_bases DROP CONSTRAINT IF EXISTS chk_knowledge_bases_name_format;
SQL
```

### No-go conditions (DO NOT migrate)

- Existing `schemas` / `knowledge_bases` rows with **uppercase** names,
  **underscores**, **dots**, or **length > 100** — preflight will HALT
  with offending names logged. Resolve these before bumping chart
  0.6.0.
- Custom EE / Cloud plugin overrides (`syntheticbrew-ee --mode ee|cloud`)
  not yet rebuilt against engine 1.1.0 — pin the EE binary's engine
  dependency to 1.1.0 + redeploy in lockstep. The shared engine binary
  serves both the chart's CE pod and the EE plugin loader.

### Manual verification checklist (post-deploy)

```text
[ ] kubectl rollout status deploy/<release>-syntheticbrew-engine — Available
[ ] kubectl logs job/<release>-syntheticbrew-engine-migrations — "Update has been successful"
[ ] curl /api/v1/health → 200
[ ] curl /api/v1/schemas/<name>/chat (valid name) → 200 SSE
[ ] curl /api/v1/schemas/<bogus-name>/chat → 404 {"error":"schema not found"}
[ ] curl /api/v1/schemas/Bad/Name/chat → 400 (path validation)
[ ] curl PATCH /api/v1/schemas/<name> {"name":"renamed"} → 409 + body "immutable"
[ ] Admin SPA loads at /admin/, schema URL paths show names not UUIDs
[ ] Embed widget on test HTML page with data-schema="<name>" — chat works
[ ] No client-name / UUID leaks in tracked source: git grep validation clean
```
