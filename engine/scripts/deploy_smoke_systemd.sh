#!/usr/bin/env bash
# deploy_smoke_systemd.sh — systemd (bare-metal / VPS) deploy smoke test.
#
# Requires: docker (to spin up a privileged systemd container), curl
# Usage: bash syntheticinc/syntheticbrew/scripts/deploy_smoke_systemd.sh
#
# The script:
#   1. Builds the engine binary (local Go build)
#   2. Spins up a privileged Alpine+openrc container (systemd-in-docker)
#   3. Installs the binary + example unit file
#   4. Starts the service and waits for it to be active
#   5. Checks /api/v1/health → 200
#   6. Tears down the container
#
# NOTE: Full systemd-in-docker requires a Linux host. On macOS/Windows this
# test runs inside Docker Desktop's Linux VM — behavior is equivalent for
# service lifecycle testing. A privileged container is required.
#
# Exit codes: 0 = pass, 1 = prerequisite missing (SKIPPED), 2 = test failed.

set -euo pipefail

CONTAINER_NAME="syntheticbrew-systemd-smoke"
HOST_PORT="18444"
BINARY_PATH="syntheticinc/syntheticbrew/bin/syntheticbrew-engine-smoke"
UNIT_SRC="syntheticinc/syntheticbrew/deploy/systemd/syntheticbrew-engine.service"

# ── Prerequisite checks ──────────────────────────────────────────────────────

missing=()
for cmd in docker curl go; do
  command -v "$cmd" &>/dev/null || missing+=("$cmd")
done

if [[ ${#missing[@]} -gt 0 ]]; then
  echo "SKIPPED: required tools not found: ${missing[*]}"
  exit 1
fi

if [[ ! -f "$UNIT_SRC" ]]; then
  echo "SKIPPED: systemd unit file not found at $UNIT_SRC"
  echo "Create it first: syntheticinc/syntheticbrew/deploy/systemd/syntheticbrew-engine.service"
  exit 1
fi

# ── Helpers ──────────────────────────────────────────────────────────────────

cleanup() {
  echo "--- Cleanup ---"
  docker rm -f "$CONTAINER_NAME" 2>/dev/null || true
  rm -f "$BINARY_PATH"
}
trap cleanup EXIT

# ── 1. Build engine binary for Linux ────────────────────────────────────────

echo "==> Building engine binary (linux/amd64)..."
mkdir -p "$(dirname "$BINARY_PATH")"
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 GOWORK=off \
  go build -C syntheticinc/syntheticbrew \
  -ldflags "-s -w" \
  -o "../../$BINARY_PATH" \
  ./cmd/ce 2>&1 | tail -5

echo "==> Binary built: $BINARY_PATH"

# ── 2. Start privileged systemd container ───────────────────────────────────

echo "==> Starting privileged systemd container..."
docker run -d \
  --name "$CONTAINER_NAME" \
  --privileged \
  --tmpfs /run \
  --tmpfs /run/lock \
  -v /sys/fs/cgroup:/sys/fs/cgroup:ro \
  -p "${HOST_PORT}:8443" \
  jrei/systemd-ubuntu:22.04

sleep 5

# ── 3. Install binary + unit ─────────────────────────────────────────────────

echo "==> Installing binary into container..."
docker cp "$BINARY_PATH" "$CONTAINER_NAME"://usr/local/bin/syntheticbrew-engine
docker exec "$CONTAINER_NAME" chmod +x //usr/local/bin/syntheticbrew-engine

echo "==> Installing systemd unit..."
docker cp "$UNIT_SRC" "$CONTAINER_NAME"://etc/systemd/system/syntheticbrew-engine.service

# Override ExecStart to use our binary path and local auth mode
docker exec "$CONTAINER_NAME" bash -c "
  sed -i 's|ExecStart=.*|ExecStart=/usr/local/bin/syntheticbrew-engine --mode local --port 8443|' \
    /etc/systemd/system/syntheticbrew-engine.service
  mkdir -p /var/lib/syntheticbrew/keys /etc/syntheticbrew
  systemctl daemon-reload
"

# ── 4. Start service + wait ──────────────────────────────────────────────────

echo "==> Starting syntheticbrew-engine service..."
docker exec "$CONTAINER_NAME" systemctl start syntheticbrew-engine

# Poll for up to 30s
for i in $(seq 1 15); do
  STATUS=$(docker exec "$CONTAINER_NAME" systemctl is-active syntheticbrew-engine 2>/dev/null || true)
  if [[ "$STATUS" == "active" ]]; then
    echo "==> Service is active"
    break
  fi
  echo "    Waiting for service... ($i/15, status=$STATUS)"
  sleep 2
done

STATUS=$(docker exec "$CONTAINER_NAME" systemctl is-active syntheticbrew-engine 2>/dev/null || true)
if [[ "$STATUS" != "active" ]]; then
  echo "FAIL: Service failed to start (status=$STATUS)"
  docker exec "$CONTAINER_NAME" journalctl -u syntheticbrew-engine -n 30 --no-pager 2>/dev/null || true
  exit 2
fi

# ── 5. Health check ──────────────────────────────────────────────────────────

sleep 2
echo "==> Checking health endpoint..."
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" \
  "http://localhost:${HOST_PORT}/api/v1/health" || echo "000")

if [[ "$HTTP_CODE" == "200" ]]; then
  echo "PASS: GET /api/v1/health → $HTTP_CODE"
else
  echo "FAIL: GET /api/v1/health → $HTTP_CODE (expected 200)"
  docker exec "$CONTAINER_NAME" journalctl -u syntheticbrew-engine -n 20 --no-pager 2>/dev/null || true
  exit 2
fi

echo ""
echo "==> systemd smoke test PASSED"
