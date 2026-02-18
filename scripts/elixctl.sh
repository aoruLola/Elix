#!/usr/bin/env bash
set -euo pipefail

SERVICE_NAME="${SERVICE_NAME:-elix-bridge}"
ENV_FILE="${ENV_FILE:-/etc/echohelix/elix-bridge.env}"
BASE_URL="${BASE_URL:-http://127.0.0.1:8765}"

usage() {
  cat <<'EOF'
Usage: scripts/elixctl.sh <command> [args]

Commands:
  status                 Show systemd service status
  logs [N]               Tail service logs (default 200 lines)
  restart                Restart service
  health                 Query /healthz
  backends               Query /api/v3/backends (requires token)
  pair-start             Start pairing and print response
EOF
}

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "missing required command: $1" >&2
    exit 2
  fi
}

read_token() {
  if [[ ! -f "$ENV_FILE" ]]; then
    return 1
  fi
  grep -E '^BRIDGE_AUTH_TOKEN=' "$ENV_FILE" | tail -n1 | cut -d= -f2-
}

cmd="${1:-}"
if [[ -z "$cmd" ]]; then
  usage
  exit 2
fi
shift || true

case "$cmd" in
  status)
    require_cmd systemctl
    systemctl status "$SERVICE_NAME" --no-pager
    ;;
  logs)
    require_cmd journalctl
    lines="${1:-200}"
    journalctl -u "$SERVICE_NAME" -n "$lines" -f
    ;;
  restart)
    require_cmd systemctl
    systemctl restart "$SERVICE_NAME"
    systemctl status "$SERVICE_NAME" --no-pager
    ;;
  health)
    require_cmd curl
    curl -fsS "$BASE_URL/healthz"
    echo
    ;;
  backends)
    require_cmd curl
    token="$(read_token || true)"
    if [[ -z "$token" ]]; then
      echo "cannot read BRIDGE_AUTH_TOKEN from $ENV_FILE" >&2
      exit 1
    fi
    curl -fsS "$BASE_URL/api/v3/backends" -H "Authorization: Bearer $token"
    echo
    ;;
  pair-start)
    require_cmd curl
    token="$(read_token || true)"
    if [[ -z "$token" ]]; then
      echo "cannot read BRIDGE_AUTH_TOKEN from $ENV_FILE" >&2
      exit 1
    fi
    payload='{"permissions":["runs:submit","runs:read","runs:cancel","backends:read","devices:read","devices:write"]}'
    curl -fsS -X POST "$BASE_URL/api/v3/pair/start" \
      -H "Authorization: Bearer $token" \
      -H "Content-Type: application/json" \
      -d "$payload"
    echo
    ;;
  *)
    usage
    exit 2
    ;;
esac

