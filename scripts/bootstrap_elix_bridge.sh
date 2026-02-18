#!/usr/bin/env bash
set -euo pipefail

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "missing required command: $1" >&2
    exit 2
  fi
}

if [[ "${EUID:-$(id -u)}" -ne 0 ]]; then
  echo "this script must run as root (use sudo)" >&2
  exit 2
fi

require_cmd systemctl
require_cmd sed
require_cmd curl
require_cmd install

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ENV_FILE="${ENV_FILE:-/etc/echohelix/elix-bridge.env}"
SERVICE_NAME="${SERVICE_NAME:-elix-bridge}"

echo "1/5 install and enable service"
"$ROOT_DIR/scripts/install_systemd_bridge.sh"

echo "2/5 ensure env defaults"
install -d -m 0755 "$(dirname "$ENV_FILE")"
if [[ ! -f "$ENV_FILE" ]]; then
  install -m 0644 "$ROOT_DIR/deploy/systemd/elix-bridge.env.example" "$ENV_FILE"
fi

if grep -q '^BRIDGE_AUTH_TOKEN=change-me$' "$ENV_FILE"; then
  token="$(od -An -N24 -tx1 /dev/urandom | tr -d ' \n')"
  sed -i "s/^BRIDGE_AUTH_TOKEN=change-me$/BRIDGE_AUTH_TOKEN=${token}/" "$ENV_FILE"
  echo "generated BRIDGE_AUTH_TOKEN in $ENV_FILE"
fi

echo "3/5 dependency checks"
for bin in codex gemini claude; do
  if command -v "$bin" >/dev/null 2>&1; then
    echo "- found $bin"
  else
    echo "- missing $bin (optional, needed if corresponding adapter enabled)"
  fi
done

echo "4/5 restart service with latest env"
systemctl daemon-reload
systemctl restart "$SERVICE_NAME"

echo "5/5 health check and pairing hint"
bridge_addr="$(grep -E '^BRIDGE_HTTP_ADDR=' "$ENV_FILE" | tail -n1 | cut -d= -f2- || true)"
if [[ -z "$bridge_addr" ]]; then
  bridge_addr="0.0.0.0:8765"
fi
if [[ "$bridge_addr" == *:* ]]; then
  bridge_port="${bridge_addr##*:}"
else
  bridge_port="8765"
fi
health_url="http://127.0.0.1:${bridge_port}/healthz"
if curl -fsS "$health_url" >/dev/null 2>&1; then
  echo "- health ok: $health_url"
else
  echo "- health check failed: $health_url"
fi

echo
echo "next command (pair start):"
echo "export BRIDGE_AUTH_TOKEN='<read from ${ENV_FILE}>'"
echo "curl -X POST http://127.0.0.1:${bridge_port}/api/v3/pair/start \\"
echo "  -H 'Authorization: Bearer \$BRIDGE_AUTH_TOKEN' \\"
echo "  -H 'Content-Type: application/json' \\"
echo "  -d '{\"permissions\":[\"runs:submit\",\"runs:read\",\"runs:cancel\",\"backends:read\",\"devices:read\",\"devices:write\"]}'"
