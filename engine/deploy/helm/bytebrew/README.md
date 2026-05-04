# bytebrew-engine Helm Chart

Helm chart for deploying the **ByteBrew AI Agent Engine** (Community Edition) on Kubernetes.

## Stability

Per OSS testing transparency convention (Kubernetes feature gates / Helm Artifact
Hub maturity / Apache Incubator pattern), each chart feature is labelled with how
it has been validated. Pick what your environment supports and treat
`Beta`/`Experimental` paths as community-feedback territory.

| Feature                                | Tier         | Tested how |
|----------------------------------------|--------------|------------|
| Default install (single-shot)          | **Stable**   | CI gate (kind v1.28/1.30/1.31, install + upgrade + rollback) + chirp-mono2 dev canary |
| External Postgres + ESO + Vault        | **Stable**   | CI gate + chirp-mono2 dev canary |
| Bootstrap admin token + configApply    | **Stable**   | CI gate (full single-shot flow) |
| Declarative Knowledge / RAG ingest (`knowledgeLoader`) | **Stable** | CI gate (kind v1.30 install — asserts loader Job uploaded N files into KB) |
| Migrations Job (Liquibase)             | **Stable**   | CI gate asserts `databasechangelog` populated |
| HTTPRoute (Gateway API v1)             | **Stable**   | CI render-validated + chirp-mono2 dev canary against Envoy Gateway |
| `containerSecurityContext.readOnlyRootFilesystem: true` (auto /tmp emptyDir) | **Stable** | CI render-validated; smoke runs render-only on kind |
| `replicaCount=1` enforcement (`auth.mode=local`) | **Stable** | CI gate (template `fail` on `replicaCount > 1`) |
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
  <release>-bytebrew-engine` or by deleting the namespace), and during
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
helm install bytebrew-engine oci://ghcr.io/syntheticinc/charts/bytebrew-engine \
  --version 0.4.2 \
  --set image.tag=1.0.2 \
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

## Integrations

### Helmfile (chirp-mono2 / similar GitOps stacks)

```yaml
repositories:
  - name: bytebrew
    url: oci://ghcr.io/syntheticinc/charts

releases:
  - name: bytebrew-engine
    chart: oci://ghcr.io/syntheticinc/charts/bytebrew-engine
    version: 0.4.0
    namespace: {{ .Environment.Name }}
    values:
      - ./values/{{ .Environment.Name }}/bytebrew-engine.yaml
    needs:
      - postgresql-bytebrew
```

### External Secrets Operator + Vault

Use `existingSecret` to skip the chart-managed Secret and pull `DATABASE_URL`
from your ESO-managed Secret:

```yaml
postgresql:
  external:
    existingSecret: bytebrew-config
    existingSecretKey: DATABASE_URL
```

### AWS IRSA

Bind an IAM Role to the engine pod via ServiceAccount annotation:

```yaml
serviceAccount:
  annotations:
    eks.amazonaws.com/role-arn: arn:aws:iam::123456789012:role/bytebrew-engine
```

### GCP Workload Identity

```yaml
serviceAccount:
  annotations:
    iam.gke.io/gcp-service-account: bytebrew-engine@my-project.iam.gserviceaccount.com
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
        - bytebrew.dev.example.com
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
  existingSecret: bytebrew-config

configApply:
  enabled: true
  tokenSecret: bytebrew-config
  apiKeysSecret: bytebrew-config
```

Engine seeds the token into `api_tokens` on first boot (idempotent — safe
to re-apply). Token format: `bb_<64-hex>` — generate via
`echo "bb_$(openssl rand -hex 32)"`.

Requires engine image **v1.0.1 or later** for `BYTEBREW_BOOTSTRAP_ADMIN_TOKEN`
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
      tokenSecret: bytebrew-config
      apiKeysSecret: bytebrew-config              # holds OPENROUTER_API_KEY
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
      existingConfigMap: handbook-files           # ConfigMap from step 2
      mode: skip-existing                         # or replace / always (see below)
      prune: false                                # delete remote files not in CM
      # tokenSecret/tokenSecretKey default to configApply's
    ```

**What runs:**

- `bytebrew-engine-config-apply` (Helm hook weight **10**) — brewctl applies
  the bundle, creates KB row in DB.
- `bytebrew-engine-knowledge-loader` (Helm hook weight **15**) — alpine pod
  installs `curl`+`jq` via apk (~3s), resolves `kb` to UUID, walks
  `/etc/bytebrew/knowledge-files/`, uploads each file via `POST
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
walks `/etc/bytebrew/knowledge-files/`, so the volume source is opaque.

**Security note.** The loader Job overrides chart's `runAsNonRoot=true`
to `runAsUser=0` because alpine `apk add` requires root for
`/var/cache/apk/`. Job is short-lived (seconds), only talks to the
in-cluster engine REST API, holds no persistent state. To keep
non-root, override `knowledgeLoader.image` to a pre-built variant with
`curl`+`jq` baked in (e.g. `your-registry/kb-loader:tag`) and re-add
`runAsNonRoot: true`.
