#!/usr/bin/env bash
set -euo pipefail

ENV_FILE="${ENV_FILE:-/etc/echohelix/elix-bridge.env}"

if [[ ! -f "$ENV_FILE" ]]; then
  echo "WARN: env file not found: $ENV_FILE"
  exit 0
fi

get_env() {
  local key="$1"
  grep -E "^${key}=" "$ENV_FILE" | tail -n1 | cut -d= -f2-
}

warn=0

token="$(get_env BRIDGE_AUTH_TOKEN || true)"
if [[ -z "$token" || "$token" == "change-me" ]]; then
  echo "WARN: BRIDGE_AUTH_TOKEN is unset or default placeholder."
  warn=1
fi

roots="$(get_env WORKSPACE_ROOTS || true)"
if [[ "$roots" == *"/"* && "$roots" =~ (^|,)/($|,) ]]; then
  echo "WARN: WORKSPACE_ROOTS includes '/'. This is too broad."
  warn=1
fi
if [[ "$roots" == *"/home"* ]]; then
  echo "INFO: WORKSPACE_ROOTS includes /home. Confirm this is intended."
fi

http_addr="$(get_env BRIDGE_HTTP_ADDR || true)"
if [[ "$http_addr" == 0.0.0.0:* || "$http_addr" == *":8765" ]]; then
  echo "INFO: bridge listens on $http_addr (or default). Ensure HTTPS/TLS termination upstream."
fi

if [[ "$warn" -eq 0 ]]; then
  echo "OK: preflight checks passed."
else
  echo "WARN: preflight checks found issues."
fi

