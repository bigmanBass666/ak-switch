#!/bin/bash
# WSL2 Acceptance Test for Docker Compose Full Stack
# This script is run inside WSL2 at /mnt/d/Test/Alvus-fork

set -uo pipefail

ROOT="$(cd "$(dirname "$0")" && pwd)"
cd "$ROOT"

PASS=0
FAIL=0

cleanup() {
  docker compose down -v > /dev/null 2>&1 || true
}
trap cleanup EXIT

assert() {
  local desc="$1"
  shift
  if eval "$@"; then
    echo "  ✅ $desc"
    ((PASS++))
  else
    echo "  ❌ $desc"
    ((FAIL++))
  fi
}

echo "=== Docker Compose Acceptance Tests ==="

# Step A: Compose config
echo "--- A: Config ---"
assert "docker compose config valid" \
  "docker compose config > /dev/null 2>&1"

# Step B: Start stack
echo "--- B: Start ---"
assert "docker compose up" \
  "docker compose up -d > /tmp/compose-up.log 2>&1"

# Step C: Wait for services
echo "--- C: Wait (15s) ---"
sleep 15

# Step D: Alvus health
echo "--- D: Alvus ---"
ALVUS_PORT="$(grep -E '^PORT=' .env 2>/dev/null | cut -d= -f2 | tr -d '[:space:]')"
ALVUS_PORT="${ALVUS_PORT:-3000}"
echo "  Alvus host port: $ALVUS_PORT"

assert "Alvus /health returns status ok" \
  "curl -sf http://localhost:${ALVUS_PORT}/health 2>/dev/null | grep -q 'status.*ok'"

# Step E: Prometheus targets
echo "--- E: Prometheus ---"
assert "Prometheus has alvus target" \
  "curl -sf http://localhost:9090/api/v1/targets 2>/dev/null | grep -q 'alvus:3000'"

# Step F: Prometheus scraping
echo "--- F: Prometheus scrape ---"
sleep 15
assert "Prometheus alvus_keypool_keys" \
  "curl -sf 'http://localhost:9090/api/v1/query?query=alvus_keypool_keys' 2>/dev/null | grep -q 'alvus_keypool_keys'"
assert "Prometheus go_info" \
  "curl -sf 'http://localhost:9090/api/v1/query?query=go_info' 2>/dev/null | grep -q 'go_info'"

# Step G: Grafana health
echo "--- G: Grafana ---"
assert "Grafana api/health" \
  "curl -sf http://localhost:3001/api/health 2>/dev/null | grep -q 'database.*ok'"

# Step H: Grafana datasource
echo "--- H: Grafana datasource ---"
DS="$(curl -sf http://localhost:3001/api/datasources 2>/dev/null)"
if [ -z "$DS" ]; then
  DS="$(curl -sf -u admin:admin http://localhost:3001/api/datasources 2>/dev/null)"
fi
assert "Grafana Prometheus datasource" \
  "echo '$DS' | grep -q 'Prometheus'"

# Step I: Grafana dashboard
echo "--- I: Grafana dashboard ---"
DASH="$(curl -sf 'http://localhost:3001/api/search?query=Alvus' 2>/dev/null)"
if [ -z "$DASH" ]; then
  DASH="$(curl -sf -u admin:admin 'http://localhost:3001/api/search?query=Alvus' 2>/dev/null)"
fi
assert "Grafana Alvus dashboard" \
  "echo '$DASH' | grep -q 'Alvus Overview'"

# Step J: Container state
echo "--- J: Containers ---"
SERVICES="$(docker compose ps --services 2>/dev/null)"
for s in alvus prometheus grafana; do
  assert "$s container running" \
    "echo '$SERVICES' | grep -q '^${s}$'"
done

# Summary
echo ""
echo "=== $PASS passed, $FAIL failed ==="
if [ "$FAIL" -gt 0 ]; then
  exit 1
fi