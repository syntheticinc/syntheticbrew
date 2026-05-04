# Changelog

All notable changes to the `bytebrew-engine` Helm chart will be documented here.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this chart adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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

### Why now (chirp-mono2 dev feedback)
chirp-mono2 dev integration asked: "we have 7 markdown files, what's the
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
  now pins `DATA_DIR=/etc/bytebrew` so writes land inside the mounted
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
  version. Caught by chirp-mono2 dev when bumping 0.4.2 → 0.4.3 — atomic
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
  at `-f /etc/bytebrew/config` (a ConfigMap-mounted directory). brewctl's
  loader walks subdirectories `models/`, `agents/`, `schemas/` only and
  ignores top-level files in the dir. The ConfigMap renders the inline
  `configApply.config` value as a single file `bytebrew.yaml` at the dir
  root, so brewctl found zero subdirs → empty desired state → "No changes"
  → Job Completed → false success. Caught by chirp-mono2 dev canary deploy:
  Job logs said `No changes.` but engine had no smoke model/agent/schema.
  Now points brewctl at `-f /etc/bytebrew/config/bytebrew.yaml` (explicit
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

### Why this matters for chirp-mono2 dev
The chirp install:dev pipeline reported success (engine 1/1 Running, Job
Completed), but `/api/v1/models` came back empty — brewctl had silently
no-op'd. Bumping chirp's `helmfile.yaml.gotmpl` to `version: 0.4.3` and
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
data key MUST be `bytebrew.yaml` — the Job invokes brewctl with the
explicit file path `/etc/bytebrew/config/bytebrew.yaml`. Configurable
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
  file to `~/.local/share/bytebrew/`. Under `runAsUser: 1000` without
  `HOME` set, the path resolved to `/.local`, which is not writable →
  `mkdir /.local: permission denied` → CrashLoopBackOff. Deployment
  template now sets `HOME=/tmp` explicitly.
- **Migrations Job no args** — the `bytebrew/engine-migrations` image is
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
  - `tests/values/single-shot.yaml` — chirp-mono2-style flow with
    bootstrap admin token + configApply Helm hook
  - `tests/fixtures/postgres-pgvector.yaml` — Secret + ConfigMap +
    StatefulSet + Service for an in-kind pgvector Postgres. Init script
    is idempotent (re-runnable across smoke iterations) and the
    readiness probe verifies the `bytebrew` DB exists before signalling
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
  removed (`kubectl delete sa <release>-bytebrew-engine`). On `helm
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
  `BYTEBREW_BOOTSTRAP_ADMIN_TOKEN` env from a Secret. Engine v1.0.1+ seeds
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
  `bytebrew/engine-migrations` image against `DATABASE_URL` before every
  install or upgrade. Toggle via `migrations.enabled` (default: `true`).
- `brewctl` config-apply Job (`post-install,post-upgrade` Helm hook) for
  declarative GitOps reconcile via the `brewctl` CLI. Waits for engine
  readiness via an init container before applying. Optional via
  `configApply.enabled` (default: `false`).
  **Prerequisites:** brewctl Docker image (`ghcr.io/syntheticinc/brewctl:v0.1.0`
  or later) must be published before enabling `configApply.enabled=true`. See
  [bytebrew-brewctl releases](https://github.com/syntheticinc/bytebrew-brewctl/releases).
- ConfigMap template (`configmap-bytebrew.yaml`) for inline `bytebrew.yaml`
  config-as-code. Rendered only when `configApply.enabled=true` and
  `configApply.config` is non-empty and `configApply.existingConfigMap` is unset.
- Argo CD Application example (`examples/argocd-application.yaml`) with both
  Git-based and OCI-based source variants.
- GitHub Actions workflow `release-helm.yaml` publishing chart to
  `ghcr.io/syntheticinc/charts/bytebrew-engine` on `helm/v*.*.*` tags.

### Changed
- Bumped chart version to `0.3.0`.
- `NOTES.txt` updated with config-apply bootstrap instructions (step 3).

## [0.2.0] - earlier

Initial CE chart with engine Deployment, Service, Ingress (sticky sessions for
SSE), PVC for JWT keys, HPA, ServiceMonitor, and ConfigMap for `agents.yaml`.
