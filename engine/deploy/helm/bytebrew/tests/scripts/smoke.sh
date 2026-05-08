#!/usr/bin/env bash
# Chart smoke — runs after `helm install` against an in-cluster engine
# reachable via kubectl port-forward.
#
# NOTE: tests/values/*.yaml pin engine image.tag to "1.0.1". Chart appVersion is
# 1.0.3 (engine fail-fast on invalid bootstrap token + PATCH alias normalize).
# Valid-token / canonical-type happy path is identical between 1.0.1 / 1.0.2 /
# 1.0.3, so kind smoke runs identically. The new PATCH normalization in v1.0.3
# is covered by go unit test (TestModelHandler_Patch_NormalizesAlias) — kind
# smoke does NOT exercise it because fixtures use canonical type already.
# Bump pins to "1.0.3" in a follow-up after the cloud-web deploy workflow has
# published bytebrew/engine:1.0.3 + bytebrew/engine-migrations:1.0.3, and at
# that point flipping single-shot.yaml to `type: openrouter` would also
# exercise the v1.0.3 fix end-to-end in CI.
#
# Required env:
#   ADMIN_TOKEN   bb_<64-hex> Bearer token for engine REST
#   ENGINE_URL    typically http://localhost:18443 (port-forward target)
#
# Optional env:
#   NAMESPACE     k8s namespace (default: default)
#   RELEASE       Helm release name (default: bb)
#
# Exit non-zero on any failure.
set -euo pipefail

NAMESPACE=${NAMESPACE:-default}
RELEASE=${RELEASE:-bb}
TOKEN=${ADMIN_TOKEN:?ADMIN_TOKEN env required}
ENGINE_URL=${ENGINE_URL:-http://localhost:18443}

echo "==> Wait for engine deployment ready"
kubectl -n "$NAMESPACE" rollout status \
  "deploy/${RELEASE}-bytebrew-engine" --timeout=300s

echo "==> GET /api/v1/health"
curl -fsS "$ENGINE_URL/api/v1/health" | jq -e '.status == "ok" or .status == "healthy"'

# REST endpoints — engine returns either a plain array or {data: [...]}.
# Smoke accepts both to stay neutral on the response envelope.
for endpoint in agents schemas models; do
  echo "==> GET /api/v1/$endpoint"
  curl -fsS "$ENGINE_URL/api/v1/$endpoint" \
    -H "Authorization: Bearer $TOKEN" \
    | jq -e 'type == "array" or has("data")'
done

# configApply Job runs as post-install Helm hook — when the scenario enables
# it the Job should already be Complete by the time `helm install --wait`
# returned. Guard with `--ignore-not-found` for scenarios without it.
echo "==> Verify configApply Job (if scenario enabled it)"
if kubectl -n "$NAMESPACE" get \
    "job/${RELEASE}-bytebrew-engine-config-apply" --ignore-not-found \
    -o name | grep -q job; then
  kubectl -n "$NAMESPACE" wait --for=condition=complete \
    "job/${RELEASE}-bytebrew-engine-config-apply" --timeout=120s

  # Catch v0.4.2 false-positive: brewctl `apply -f <dir>` walks subdirs only,
  # missed top-level ConfigMap-mounted bytebrew.yaml → "No changes" → Job
  # Completed → looked successful but ZERO resources created. Assert the
  # smoke bundle actually landed in engine.
  #
  # Gated behind EXPECT_BREWCTL_RESOURCES=true so scenarios that intentionally
  # ship an empty `models: []` bundle (e.g. restricted-security, where the
  # focus is engine-boot-under-readOnlyRootFilesystem, not brewctl flow)
  # don't trip on missing resources.
  if [ "${EXPECT_BREWCTL_RESOURCES:-}" = "true" ]; then
    echo "==> Assert configApply created the smoke resources"
    # Single-shot scenario uses kind-smoke-{model,agent,schema}; knowledge
    # scenario uses kind-smoke-{chat,agent,schema} + a KB. Match either.
    models=$(curl -fsS "$ENGINE_URL/api/v1/models" \
      -H "Authorization: Bearer $TOKEN" | jq -e 'map(select(.name | startswith("kind-smoke-"))) | length')
    agents=$(curl -fsS "$ENGINE_URL/api/v1/agents" \
      -H "Authorization: Bearer $TOKEN" | jq -e 'map(select(.name == "kind-smoke-agent")) | length')
    schemas=$(curl -fsS "$ENGINE_URL/api/v1/schemas" \
      -H "Authorization: Bearer $TOKEN" | jq -e 'map(select(.name == "kind-smoke-schema")) | length')
    if [ "$models" -lt 1 ] || [ "$agents" != "1" ] || [ "$schemas" != "1" ]; then
      echo "FAIL: brewctl reported success but smoke resources missing — models=$models agents=$agents schemas=$schemas"
      exit 1
    fi
    echo "OK: brewctl created kind-smoke-* (models=$models agents=$agents schemas=$schemas)"

    # Regression guard for chirp 1.1.2 dev-rollout bug #1: brewctl 0.2.2 +
    # engine 1.1.2 left schemas.entry_agent_id NULL on a fresh-DB single
    # apply. Engine 1.1.3 + brewctl 0.2.3 ship the fix; this assertion
    # ensures the FK is populated post-apply, not deferred to a follow-up
    # PATCH. If this fails, chat would 400 with INVALID_INPUT: schema has
    # no entry agent.
    SCHEMA_FIXTURE_NAME="kind-smoke-schema"
    schema_entry_agent=$(curl -fsS "$ENGINE_URL/api/v1/schemas/$SCHEMA_FIXTURE_NAME" \
      -H "Authorization: Bearer $TOKEN" | jq -re '.entry_agent_name // ""')
    if [ -z "$schema_entry_agent" ]; then
      echo "FAIL: schema $SCHEMA_FIXTURE_NAME has no entry_agent_name (was the 1.1.2 NULL bug)"
      exit 1
    fi
    echo "OK: schema $SCHEMA_FIXTURE_NAME entry_agent=$schema_entry_agent"
  fi
fi

# Knowledge loader scenario assertions — gated separately so single-shot
# doesn't fail when no KB exists.
if [ "${EXPECT_KB_FILES:-}" = "true" ]; then
  echo "==> Assert knowledgeLoader uploaded files into KB"
  KB_NAME="${KB_NAME:-kind-smoke-kb}"
  # Engine 1.1.0+: REST URLs are name-keyed. Pre-1.1.0 this script
  # resolved the KB's UUID first and built /knowledge-bases/{uuid}/files
  # — modern engines reject UUID-shaped strings in the {name} URL slot
  # (ValidateResourceName), so use the canonical name directly.
  if ! curl -fsS "$ENGINE_URL/api/v1/knowledge-bases" \
    -H "Authorization: Bearer $TOKEN" \
    | jq -er --arg n "$KB_NAME" '.[] | select(.name==$n) | .name' >/dev/null; then
    echo "FAIL: KB '$KB_NAME' not found in /api/v1/knowledge-bases"
    exit 1
  fi
  files=$(curl -fsS "$ENGINE_URL/api/v1/knowledge-bases/$KB_NAME/files" \
    -H "Authorization: Bearer $TOKEN" | jq -e 'length')
  expected="${EXPECT_KB_FILE_COUNT:-2}"
  if [ "$files" != "$expected" ]; then
    echo "FAIL: KB '$KB_NAME' has $files files, expected $expected"
    exit 1
  fi
  echo "OK: knowledgeLoader uploaded $files files into '$KB_NAME'"
fi

echo "✅ Smoke pass"
