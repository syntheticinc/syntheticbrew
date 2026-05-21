#!/usr/bin/env bash
# deploy_smoke_k8s.sh — Kubernetes (kind) deploy smoke test for SyntheticBrew Engine.
#
# Requires: kind, helm, kubectl, curl
# Usage: bash syntheticinc/syntheticbrew/scripts/deploy_smoke_k8s.sh
#
# The script:
#   1. Creates a kind cluster
#   2. Builds and loads a local engine image
#   3. Installs via Helm with auth.mode=local
#   4. Waits for engine pod to be ready
#   5. Port-forwards and checks /api/v1/health → 200
#   6. Tears down the cluster
#
# Exit codes: 0 = pass, 1 = prerequisite missing (SKIPPED), 2 = test failed.

set -euo pipefail

CLUSTER_NAME="syntheticbrew-smoke"
IMAGE_NAME="syntheticbrew-engine:smoke"
HELM_CHART="syntheticinc/syntheticbrew/deploy/helm/syntheticbrew"
NAMESPACE="default"
PF_PORT="18443"
HEALTH_ENDPOINT="http://localhost:${PF_PORT}/api/v1/health"

# ── Prerequisite checks ──────────────────────────────────────────────────────

missing=()
for cmd in kind helm kubectl curl docker; do
  command -v "$cmd" &>/dev/null || missing+=("$cmd")
done

if [[ ${#missing[@]} -gt 0 ]]; then
  echo "SKIPPED: required tools not found: ${missing[*]}"
  echo "Install them and re-run:"
  echo "  kind   → https://kind.sigs.k8s.io/docs/user/quick-start/#installation"
  echo "  helm   → https://helm.sh/docs/intro/install/"
  echo "  kubectl→ https://kubernetes.io/docs/tasks/tools/"
  exit 1
fi

if [[ ! -d "$HELM_CHART" ]]; then
  echo "SKIPPED: Helm chart not found at $HELM_CHART"
  echo "Expected chart directory relative to workspace root."
  exit 1
fi

# ── Helpers ──────────────────────────────────────────────────────────────────

cleanup() {
  echo "--- Cleanup ---"
  kill "$PF_PID" 2>/dev/null || true
  kind delete cluster --name "$CLUSTER_NAME" 2>/dev/null || true
}
trap cleanup EXIT

# ── 1. Create kind cluster ───────────────────────────────────────────────────

echo "==> Creating kind cluster '$CLUSTER_NAME'..."
kind create cluster --name "$CLUSTER_NAME" --wait 60s

# ── 2. Build + load image ────────────────────────────────────────────────────

echo "==> Building engine image..."
docker build \
  -f syntheticinc/syntheticbrew/Dockerfile \
  -t "$IMAGE_NAME" \
  . 2>&1 | tail -5

echo "==> Loading image into kind cluster..."
kind load docker-image "$IMAGE_NAME" --name "$CLUSTER_NAME"

# ── 3. Helm install ──────────────────────────────────────────────────────────

echo "==> Installing Helm chart..."
helm install syntheticbrew-smoke "$HELM_CHART" \
  --namespace "$NAMESPACE" \
  --set auth.mode=local \
  --set image.repository=syntheticbrew-engine \
  --set image.tag=smoke \
  --set image.pullPolicy=Never \
  --set service.port=8443 \
  --wait --timeout=120s

# ── 4. Wait for pod ──────────────────────────────────────────────────────────

echo "==> Waiting for engine pod to be ready..."
kubectl wait \
  --for=condition=ready pod \
  -l app.kubernetes.io/name=syntheticbrew-engine \
  --timeout=120s \
  --namespace "$NAMESPACE"

# ── 5. Port-forward + health check ──────────────────────────────────────────

echo "==> Port-forwarding engine service to localhost:${PF_PORT}..."
kubectl port-forward \
  svc/syntheticbrew-smoke \
  "${PF_PORT}:8443" \
  --namespace "$NAMESPACE" &
PF_PID=$!

sleep 3

echo "==> Checking health endpoint..."
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" "$HEALTH_ENDPOINT" || echo "000")

if [[ "$HTTP_CODE" == "200" ]]; then
  echo "PASS: GET $HEALTH_ENDPOINT → $HTTP_CODE"
else
  echo "FAIL: GET $HEALTH_ENDPOINT → $HTTP_CODE (expected 200)"
  exit 2
fi

echo ""
echo "==> K8s smoke test PASSED"
