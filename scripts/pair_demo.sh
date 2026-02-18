#!/usr/bin/env bash
set -euo pipefail

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "missing required command: $1" >&2
    exit 2
  fi
}

require_cmd curl
require_cmd jq

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ELIX_WALLET_BIN="${ELIX_WALLET_BIN:-$ROOT_DIR/elix-wallet}"
BRIDGE_BASE_URL="${BRIDGE_BASE_URL:-http://127.0.0.1:8765}"
BRIDGE_AUTH_TOKEN="${BRIDGE_AUTH_TOKEN:-echohelix-dev-token}"
BRIDGE_BIN="${BRIDGE_BIN:-$ROOT_DIR/elix-bridge}"
DEMO_BACKEND="${DEMO_BACKEND:-codex}"
DEMO_WORKSPACE="${DEMO_WORKSPACE:-/tmp/echohelix-demo}"
DEMO_PROMPT="${DEMO_PROMPT:-reply exactly ok}"
DEMO_POLL_SECONDS="${DEMO_POLL_SECONDS:-1}"
DEMO_TIMEOUT_SECONDS="${DEMO_TIMEOUT_SECONDS:-120}"

if [[ "$ELIX_WALLET_BIN" == */* ]]; then
  if [[ ! -x "$ELIX_WALLET_BIN" ]]; then
    echo "missing executable elix-wallet at: $ELIX_WALLET_BIN" >&2
    exit 2
  fi
else
  require_cmd "$ELIX_WALLET_BIN"
fi

mkdir -p "$DEMO_WORKSPACE"

http_req() {
  local method="$1"
  local url="$2"
  local bearer="${3:-}"
  local payload="${4:-}"
  local resp
  if [[ -n "$payload" ]]; then
    if [[ -n "$bearer" ]]; then
      resp="$(curl -sS -w $'\n%{http_code}' -X "$method" "$url" -H "Authorization: Bearer $bearer" -H "Content-Type: application/json" -d "$payload")"
    else
      resp="$(curl -sS -w $'\n%{http_code}' -X "$method" "$url" -H "Content-Type: application/json" -d "$payload")"
    fi
  else
    if [[ -n "$bearer" ]]; then
      resp="$(curl -sS -w $'\n%{http_code}' -X "$method" "$url" -H "Authorization: Bearer $bearer")"
    else
      resp="$(curl -sS -w $'\n%{http_code}' -X "$method" "$url")"
    fi
  fi
  HTTP_STATUS="${resp##*$'\n'}"
  HTTP_BODY="${resp%$'\n'*}"
}

echo "checking bridge health..."
if [[ ! -x "$BRIDGE_BIN" ]]; then
  echo "warning: bridge binary not found at $BRIDGE_BIN (ensure service is running or run make build)"
fi
curl -fsS "$BRIDGE_BASE_URL/healthz" >/dev/null

echo "1/8 start secure pairing"
pair_start_payload="$(jq -n '{permissions:["runs:submit","runs:read","runs:cancel","backends:read","devices:read","devices:write"]}')"
http_req POST "$BRIDGE_BASE_URL/api/v3/pair/start" "$BRIDGE_AUTH_TOKEN" "$pair_start_payload"
if [[ "$HTTP_STATUS" != "200" ]]; then
  echo "pair/start failed status=$HTTP_STATUS body=$HTTP_BODY" >&2
  exit 1
fi
pair_code="$(echo "$HTTP_BODY" | jq -r '.pair_code // empty')"
challenge="$(echo "$HTTP_BODY" | jq -r '.challenge // empty')"
if [[ -z "$pair_code" || -z "$challenge" ]]; then
  echo "pair/start missing pair_code/challenge: $HTTP_BODY" >&2
  exit 1
fi

echo "2/8 generate wallet identity"
wallet_json="$("$ELIX_WALLET_BIN" generate)"
public_key="$(echo "$wallet_json" | jq -r '.public_key // empty')"
private_key="$(echo "$wallet_json" | jq -r '.private_key // empty')"
if [[ -z "$public_key" || -z "$private_key" ]]; then
  echo "wallet generate failed: $wallet_json" >&2
  exit 1
fi

echo "3/8 sign challenge"
sign_json="$("$ELIX_WALLET_BIN" sign --private-key "$private_key" --challenge "$challenge")"
signature="$(echo "$sign_json" | jq -r '.signature // empty')"
if [[ -z "$signature" ]]; then
  echo "wallet sign failed: $sign_json" >&2
  exit 1
fi

echo "4/8 complete pairing"
pair_complete_payload="$(jq -n --arg code "$pair_code" --arg pk "$public_key" --arg sig "$signature" --arg name "pair-demo" \
  '{pair_code:$code,public_key:$pk,signature:$sig,device_name:$name}')"
http_req POST "$BRIDGE_BASE_URL/api/v3/pair/complete" "" "$pair_complete_payload"
if [[ "$HTTP_STATUS" != "200" ]]; then
  echo "pair/complete failed status=$HTTP_STATUS body=$HTTP_BODY" >&2
  exit 1
fi
address="$(echo "$HTTP_BODY" | jq -r '.address // empty')"
access_token="$(echo "$HTTP_BODY" | jq -r '.access_token // empty')"
refresh_token="$(echo "$HTTP_BODY" | jq -r '.refresh_token // empty')"
if [[ -z "$address" || -z "$access_token" || -z "$refresh_token" ]]; then
  echo "pair/complete missing token fields: $HTTP_BODY" >&2
  exit 1
fi

echo "5/8 submit run with session token"
submit_payload="$(jq -n --arg ws "pair-demo" --arg wsp "$DEMO_WORKSPACE" --arg b "$DEMO_BACKEND" --arg p "$DEMO_PROMPT" \
  '{workspace_id:$ws,workspace_path:$wsp,backend:$b,prompt:$p,options:{schema_version:"v2"}}')"
http_req POST "$BRIDGE_BASE_URL/api/v3/runs" "$access_token" "$submit_payload"
if [[ "$HTTP_STATUS" != "202" ]]; then
  echo "run submit failed status=$HTTP_STATUS body=$HTTP_BODY" >&2
  exit 1
fi
run_id="$(echo "$HTTP_BODY" | jq -r '.run_id // empty')"
if [[ -z "$run_id" ]]; then
  echo "run_id missing: $HTTP_BODY" >&2
  exit 1
fi

echo "6/8 poll run terminal status"
deadline=$((SECONDS + DEMO_TIMEOUT_SECONDS))
status=""
run_json=""
while (( SECONDS < deadline )); do
  http_req GET "$BRIDGE_BASE_URL/api/v3/runs/$run_id" "$access_token" ""
  if [[ "$HTTP_STATUS" != "200" ]]; then
    echo "get run failed status=$HTTP_STATUS body=$HTTP_BODY" >&2
    exit 1
  fi
  run_json="$HTTP_BODY"
  status="$(echo "$run_json" | jq -r '.status // empty')"
  case "$status" in
    completed|failed|cancelled)
      break
      ;;
  esac
  sleep "$DEMO_POLL_SECONDS"
done
if [[ "$status" != "completed" && "$status" != "failed" && "$status" != "cancelled" ]]; then
  echo "run did not reach terminal status within timeout: $run_json" >&2
  exit 1
fi

echo "7/8 rotate session token"
refresh_payload="$(jq -n --arg rt "$refresh_token" '{refresh_token:$rt}')"
http_req POST "$BRIDGE_BASE_URL/api/v3/session/refresh" "" "$refresh_payload"
if [[ "$HTTP_STATUS" != "200" ]]; then
  echo "session/refresh failed status=$HTTP_STATUS body=$HTTP_BODY" >&2
  exit 1
fi
new_access_token="$(echo "$HTTP_BODY" | jq -r '.access_token // empty')"
if [[ -z "$new_access_token" ]]; then
  echo "refresh response missing access_token: $HTTP_BODY" >&2
  exit 1
fi

http_req GET "$BRIDGE_BASE_URL/api/v3/backends" "$access_token" ""
if [[ "$HTTP_STATUS" != "401" ]]; then
  echo "expected old access token to be invalid after refresh, got status=$HTTP_STATUS body=$HTTP_BODY" >&2
  exit 1
fi

echo "8/8 revoke paired device"
revoke_payload='{"reason":"pair_demo_cleanup"}'
http_req POST "$BRIDGE_BASE_URL/api/v3/devices/$address/revoke" "$new_access_token" "$revoke_payload"
if [[ "$HTTP_STATUS" != "200" ]]; then
  echo "device revoke failed status=$HTTP_STATUS body=$HTTP_BODY" >&2
  exit 1
fi
http_req GET "$BRIDGE_BASE_URL/api/v3/backends" "$new_access_token" ""
if [[ "$HTTP_STATUS" != "401" ]]; then
  echo "expected revoked token to be invalid, got status=$HTTP_STATUS body=$HTTP_BODY" >&2
  exit 1
fi

terminal_outcome="$(echo "$run_json" | jq -r '.terminal.outcome // ""')"
terminal_reason_code="$(echo "$run_json" | jq -r '.terminal.reason_code // ""')"
echo
echo "pair demo completed"
echo "- address: $address"
echo "- run_id: $run_id"
echo "- run_status: $status"
echo "- terminal_outcome: $terminal_outcome"
echo "- terminal_reason_code: $terminal_reason_code"
