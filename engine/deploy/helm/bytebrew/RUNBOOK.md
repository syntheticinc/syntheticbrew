# bytebrew-engine — Operations RUNBOOK

Operational handbook for engine deployments via the bytebrew-engine Helm
chart. Targeted at platform / DevOps owners who run the chart day-to-day.

---

## Daily operations

### Quick health snapshot

```bash
NS=dev   # or your namespace
kubectl -n $NS get pods,jobs -l app.kubernetes.io/name=bytebrew-engine
kubectl -n $NS get externalsecret,secret bytebrew-config 2>/dev/null
helm -n $NS status <release-name>
```

Expected steady-state:
- engine deployment 1/1 Running (or replicaCount/maxReplicas in HA)
- migrations Job Completed (one-shot per release)
- config-apply Job Completed (one-shot per release, only when configApply.enabled)

### Reach engine REST (internal — no public Ingress by default)

```bash
kubectl -n $NS port-forward svc/<release-name>-bytebrew-engine 18443:8443

# In another terminal — admin Bearer token from ESO-managed Secret
TOKEN=$(kubectl -n $NS get secret bytebrew-config \
  -o jsonpath='{.data.token}' | base64 -d)

curl -fsS http://localhost:18443/api/v1/health | jq
curl -fsS http://localhost:18443/api/v1/agents \
  -H "Authorization: Bearer $TOKEN" | jq
```

`/api/v1/health` is public (no auth). All other endpoints require Bearer.

---

## Token rotation

Admin token (`bb_<64-hex>`) is **service credential**, not a human secret.
Lives only in Vault `kubernetes/<env>/bytebrew/ADMIN_TOKEN`. brewctl Job uses
it for GitOps reconcile. Engine seed reads it on first boot, hashes
SHA-256, persists hash in `api_tokens` table.

### When to rotate

- Quarterly (90 days) recommended baseline
- Immediately on suspected compromise
- On personnel change with `secrets/get` RBAC

### Rotation flow

```bash
NS=dev
NEW_TOKEN="bb_$(openssl rand -hex 32)"

# 1. Update Vault (this is the source of truth)
vault kv patch kubernetes/$NS/bytebrew ADMIN_TOKEN="$NEW_TOKEN"

# 2. Force ExternalSecret resync (do not wait for refreshInterval)
kubectl -n $NS annotate externalsecret bytebrew-config \
  force-sync=$(date +%s) --overwrite

# 3. Confirm Secret has new token (compare digests)
kubectl -n $NS get secret bytebrew-config \
  -o jsonpath='{.data.token}' | base64 -d | sha256sum

# 4. Rolling-restart engine pod so seed picks up new token
kubectl -n $NS rollout restart deploy/<release>-bytebrew-engine
kubectl -n $NS rollout status deploy/<release>-bytebrew-engine --timeout=300s

# 5. Verify new token works
curl -fsS http://localhost:18443/api/v1/agents \
  -H "Authorization: Bearer $NEW_TOKEN" | jq

# 6. (Optional) Revoke the old hash via API. Find row id in
#    /api/v1/auth/tokens, then:
curl -X DELETE http://localhost:18443/api/v1/auth/tokens/<id> \
  -H "Authorization: Bearer $NEW_TOKEN"
```

The seed function is **idempotent** — it does not re-seed if a token named
`bootstrap-admin` already exists. After rotation, the row stays with the
old hash; the new token coexists alongside until you DELETE the old via API.
This is intentional: zero-downtime rotation. Step 6 cleans the orphan.

---

## Upgrade flow

```bash
# 1. Bump chart version in your helmfile / Argo Application / etc.
#    helmfile.yaml.gotmpl:
#      version: 0.4.4   # was 0.4.3

# 2. Bump engine image tags in values (only when engine appVersion changed):
#    values/<env>/bytebrew-engine.yaml:
#      image.tag: "1.0.3"
#      migrations.image.tag: "1.0.3"

# 3. Apply
helmfile -e <env> -l name=bytebrew-engine sync
# OR plain helm:
helm upgrade <release> oci://ghcr.io/syntheticinc/charts/bytebrew-engine \
  --version 0.4.4 -f values.yaml --wait --atomic --timeout 10m
```

What happens during `helm upgrade`:

1. SA hook re-creates (pre-upgrade)
2. migrations Job runs (idempotent — Liquibase skips already-applied changesets)
3. Engine pod rolls — under `auth.mode=local` chart pins `strategy: Recreate`,
   so OLD pod is killed → JWT keypair PVC released → NEW pod attaches and
   starts. Brief downtime (10-30s typical).
4. configApply Job runs (idempotent — brewctl reports "No changes" unless
   bundle drifted)

**Idempotency note:** running `helmfile sync` repeatedly without changes is safe.
Migrations Job re-runs but Liquibase no-ops. configApply Job re-runs but
brewctl no-ops. Cost: a few k8s API calls.

### Why `strategy: Recreate` (chart 0.4.4+)

`auth.mode=local` persists JWT keypair on a single-replica RWO PVC.
Default RollingUpdate creates the new pod *before* deleting the old one →
new pod deadlocks Pending on PVC contention → atomic timeout. Recreate
avoids this. `auth.mode=external` (HA) keeps RollingUpdate.

If you genuinely need RollingUpdate (e.g. RWX storage), override:
```yaml
deploymentStrategy:
  type: RollingUpdate
  rollingUpdate:
    maxSurge: 25%
    maxUnavailable: 25%
```

---

## Rollback

```bash
helm -n $NS history <release>
helm -n $NS rollback <release> <revision>
```

⚠️ **`helm rollback` does NOT downgrade the database schema.** Liquibase
`update` is forward-only. If you roll back to an older chart whose engine
image references columns that the *current* schema does not have, engine
boot fails (typically `column "X" does not exist`).

For chart-only rollbacks (template fix, values typo) → safe.
For engine-version rollbacks → also restore Postgres from a `pg_dump`
taken **before** the upgrade. There is no built-in chart support for
schema downgrade; that is application-specific.

---

## Troubleshoot CrashLoopBackOff

```bash
NS=dev
POD=$(kubectl -n $NS get pod -l app.kubernetes.io/name=bytebrew-engine -o name | head -1)
kubectl -n $NS describe $POD
kubectl -n $NS logs $POD --previous --tail=200
```

Common patterns and fixes:

### `bootstrap admin token: invalid format`

Engine v1.0.2+ fails fast on malformed token. Format MUST be `bb_<64-hex>`
(67 chars total). Fix:

```bash
vault kv patch kubernetes/$NS/bytebrew ADMIN_TOKEN="bb_$(openssl rand -hex 32)"
kubectl -n $NS annotate externalsecret bytebrew-config force-sync=$(date +%s) --overwrite
kubectl -n $NS rollout restart deploy/<release>-bytebrew-engine
```

### `secret "bytebrew-config" not found`

ExternalSecret has not materialized the Secret yet. Causes:
- ESO controller down → `kubectl get pods -n external-secrets-system`
- Vault unreachable → check ClusterSecretStore status:
  `kubectl describe clustersecretstore <name>`
- Vault path wrong → confirm `vault kv get kubernetes/$NS/bytebrew` works
  with the same auth ESO uses

### `failed to attach volume ... already attached`

PVC contention on RWO storage during a rolling restart. Chart 0.4.4+
defaults to `strategy: Recreate` for `auth.mode=local` → fixed.
If you are pre-0.4.4 OR have overridden to RollingUpdate:
```bash
kubectl -n $NS delete pod <stuck-pending-pod>
# Old pod releases PVC. New pod attaches.
```

### `connection refused` to Postgres / `password authentication failed`

DSN unreachable or credentials wrong. Verify:
```bash
# 1. ExternalSecret materialized current creds
kubectl -n $NS get secret bytebrew-config -o jsonpath='{.data.DATABASE_URL}' \
  | base64 -d

# 2. Connectivity from a debug pod in the same namespace
kubectl -n $NS run psql --rm -it --image=pgvector/pgvector:pg16 -- \
  psql "$(kubectl -n $NS get secret bytebrew-config -o jsonpath='{.data.DATABASE_URL}' | base64 -d)" -c '\l'

# 3. pgvector extension installed (engine migration 001 requires it)
kubectl -n $NS exec ... -- psql ... -c "SELECT extname FROM pg_extension WHERE extname='vector'"
```

If pgvector missing → enable extension on managed Postgres (provider-specific
GUI or `CREATE EXTENSION vector;` as superuser) then re-run `helm upgrade`
to restart migrations Job.

### `mkdir /.local: permission denied`

Engine pre-1.0.2 wrote `server.port` discovery file under `~/.local/share`
without HOME set. Chart 0.4.2+ pins `HOME=/tmp` explicitly → fixed. If you
are running an older chart with a newer engine, override via `extraEnv`:
```yaml
extraEnv:
  - name: HOME
    value: "/tmp"
```

### configApply Job `BackoffLimitExceeded`

```bash
kubectl -n $NS logs job/<release>-bytebrew-engine-config-apply
```

Common causes:

- **brewctl `apply -f <directory>` walks subdirs only** — fixed in chart
  0.4.3 (Job points at the explicit file `/etc/bytebrew/config/bytebrew.yaml`).
  If pre-0.4.3 → bump.
- **`apiVersion + kind: Config` wrapper** — bundle format MUST be top-level
  `models:`/`agents:`/`schemas:` arrays only. Drop the wrapper.
- **`type: openrouter` on engine pre-1.0.3** — POST normalizes to canonical
  `openai_compatible`, but PATCH did not. Reconcile fails on chk_models_type
  constraint. Fixed in engine 1.0.3. Workaround on older engines: pin
  `type: openai_compatible` directly.
- **`api_key: ${OPENROUTER_API_KEY}` resolving to empty** — env var not in
  `apiKeysSecret`. Add the key to Vault path → ESO syncs → restart Job.

---

## Database backup / restore

The chart does NOT manage Postgres backup. Use either managed-Postgres
provider snapshots (Scaleway / RDS / Cloud SQL all support point-in-time
recovery) or run a CronJob with `pg_dump`:

```yaml
# crontab pattern — adapt to your namespace + Vault setup
apiVersion: batch/v1
kind: CronJob
metadata:
  name: bytebrew-pgdump
spec:
  schedule: "0 2 * * *"   # nightly
  jobTemplate:
    spec:
      template:
        spec:
          restartPolicy: OnFailure
          containers:
            - name: dump
              image: pgvector/pgvector:pg16
              env:
                - name: DATABASE_URL
                  valueFrom:
                    secretKeyRef:
                      name: bytebrew-config
                      key: DATABASE_URL
              command:
                - sh
                - -c
                - pg_dump --format=custom "$DATABASE_URL" > /backup/bytebrew-$(date +%F).dump
              volumeMounts:
                - { name: backup, mountPath: /backup }
          volumes:
            - name: backup
              persistentVolumeClaim:
                claimName: bytebrew-backup-pvc
```

Restore:
```bash
pg_restore --clean --if-exists --no-owner -d "$DATABASE_URL" \
  /backup/bytebrew-YYYY-MM-DD.dump
```

**Take a `pg_dump` BEFORE every chart upgrade that bumps the engine
appVersion** — that is your only path to recover from a forward-only
Liquibase migration that goes wrong.

---

## ExternalSecret refresh

```bash
# Force a fresh sync from Vault (do not wait for refreshInterval, default 1h)
kubectl -n $NS annotate externalsecret bytebrew-config \
  force-sync=$(date +%s) --overwrite

# Verify Secret was rewritten
kubectl -n $NS get secret bytebrew-config -o yaml | grep resourceVersion
```

Cascading restart of pods that consume the Secret:
- engine: `kubectl -n $NS rollout restart deploy/<release>-bytebrew-engine`
- configApply Job: re-runs only on next `helm upgrade` (post-install hook)

---

## Scaling considerations

| Setting | When | Notes |
|---------|------|-------|
| `replicaCount: 1` (default) | always for `auth.mode=local` | Chart fails template at >1 |
| `autoscaling.enabled: true` + `maxReplicas: 1` | OK | HPA stays at 1 |
| `autoscaling.enabled: true` + `maxReplicas > 1` | only `auth.mode=external` | Chart fails template otherwise |
| Multi-replica HA | requires `auth.mode=external` + external JWT IdP | EE/Cloud feature, not CE |

CE single-replica is rarely a bottleneck for self-hosted teams. SSE chat
streams are long-lived but I/O-bound; one engine pod handles dozens of
concurrent sessions on modest CPU/memory. For HA → bytebrew-ee.

---

## When to escalate to upstream (ByteBrew)

- Migration failure on a published engine version (suggests bug in
  changeset, not your DB) → file issue with Liquibase log + Postgres
  version.
- brewctl idempotency violation (resource diff between consecutive
  `helmfile sync` with no values change) → file issue with brewctl logs
  before/after.
- Engine 5xx on routine REST → captrue `/var/log/bytebrew-engine` + repro
  steps.
- Schema drift between `target-schema.dbml` and live DB after migration
  → block deploy, file urgent issue.

Otherwise (env config, Vault setup, k8s cluster specifics) → keep in-house.
This RUNBOOK and chart README cover the operational surface.
