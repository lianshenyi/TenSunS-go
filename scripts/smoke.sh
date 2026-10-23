#!/usr/bin/env bash
# Run appendix-B smoke checks against a temporary in-memory Consul fixture.
# This does not touch a real Consul or JumpServer.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP="$(mktemp -d)"
CONSUL_ADDR="${SMOKE_CONSUL_ADDR:-127.0.0.1:18500}"
BACKEND_ADDR="${SMOKE_BACKEND_ADDR:-127.0.0.1:12026}"
PASSWORD="${SMOKE_PASSWORD:-smoke-pass}"
CONSUL_PID=""
BACKEND_PID=""

cleanup() {
  if [[ -n "$BACKEND_PID" ]]; then kill "$BACKEND_PID" 2>/dev/null || true; fi
  if [[ -n "$CONSUL_PID" ]]; then kill "$CONSUL_PID" 2>/dev/null || true; fi
  if [[ "${SMOKE_KEEP_LOGS:-0}" != "1" ]]; then rm -rf "$TMP"; fi
}
trap cleanup EXIT

echo "==> building binaries"
cd "$ROOT"
go build -o "$TMP/consulmock" ./cmd/consulmock
go build -o "$TMP/tensuns" ./cmd/tensuns

echo "==> starting consul mock on $CONSUL_ADDR"
"$TMP/consulmock" -addr "$CONSUL_ADDR" >"$TMP/consul.log" 2>&1 &
CONSUL_PID=$!
for _ in $(seq 1 100); do
  if curl -fsS "http://$CONSUL_ADDR/v1/operator/autopilot/state" >/dev/null 2>&1; then break; fi
  sleep 0.1
done
curl -fsS "http://$CONSUL_ADDR/v1/operator/autopilot/state" >/dev/null

echo "==> starting Go backend on $BACKEND_ADDR"
consul_url="http://$CONSUL_ADDR/v1" \
listen_addr="$BACKEND_ADDR" \
admin_passwd="$PASSWORD" \
"$TMP/tensuns" >"$TMP/tensuns.log" 2>&1 &
BACKEND_PID=$!

for _ in $(seq 1 100); do
  if curl -fsS -X POST "http://$BACKEND_ADDR/api/user/login" \
    -H 'Content-Type: application/json' \
    -d "{\"username\":\"admin\",\"password\":\"$PASSWORD\"}" >/dev/null 2>&1; then
    break
  fi
  sleep 0.1
done

echo "==> running appendix-B checks"
python3 "$ROOT/scripts/smoke.py" "http://$BACKEND_ADDR" "$PASSWORD"

echo "==> smoke test complete"
