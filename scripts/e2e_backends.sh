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

BRIDGE_BIN="${BRIDGE_BIN:-$ROOT_DIR/elix-bridge}"
CODEX_ADAPTER_BIN="${CODEX_ADAPTER_BIN:-$ROOT_DIR/codex-adapter}"
GEMINI_ADAPTER_BIN="${GEMINI_ADAPTER_BIN:-$ROOT_DIR/gemini-adapter}"
CLAUDE_ADAPTER_BIN="${CLAUDE_ADAPTER_BIN:-$ROOT_DIR/claude-adapter}"
ELIX_WALLET_BIN="${ELIX_WALLET_BIN:-$ROOT_DIR/elix-wallet}"

BRIDGE_BASE_URL="${BRIDGE_BASE_URL:-http://127.0.0.1:8765}"
bridge_http_addr="${BRIDGE_HTTP_ADDR:-${BRIDGE_BASE_URL#http://}}"
bridge_http_addr="${bridge_http_addr#https://}"
bridge_http_addr="${bridge_http_addr%%/*}"
bridge_http_addr="${bridge_http_addr%/}"

BRIDGE_AUTH_TOKEN="${BRIDGE_AUTH_TOKEN:-echohelix-dev-token}"
WORKSPACE_ROOTS="${WORKSPACE_ROOTS:-/tmp,/home}"
E2E_WORKSPACE="${E2E_WORKSPACE:-/tmp/echohelix-e2e}"
E2E_PROMPT="${E2E_PROMPT:-reply exactly ok}"
E2E_SCHEMA_VERSION="${E2E_SCHEMA_VERSION:-v2}"
E2E_TIMEOUT_SECONDS="${E2E_TIMEOUT_SECONDS:-120}"
E2E_POLL_INTERVAL_SECONDS="${E2E_POLL_INTERVAL_SECONDS:-1}"
E2E_BACKENDS="${E2E_BACKENDS:-codex,gemini,claude}"
E2E_KEEP_ARTIFACTS="${E2E_KEEP_ARTIFACTS:-1}"

tmp_dir="$(mktemp -d -t echohelix-e2e.XXXXXX)"
bridge_log="$tmp_dir/bridge.log"
bridge_db="${BRIDGE_SQLITE_PATH:-$tmp_dir/bridge-e2e.db}"
bridge_pid=""

cleanup() {
  if [[ -n "$bridge_pid" ]] && kill -0 "$bridge_pid" >/dev/null 2>&1; then
    kill "$bridge_pid" >/dev/null 2>&1 || true
    wait "$bridge_pid" >/dev/null 2>&1 || true
  fi
  if [[ "$E2E_KEEP_ARTIFACTS" == "0" ]]; then
    rm -rf "$tmp_dir"
  fi
}
trap cleanup EXIT

if [[ "$ELIX_WALLET_BIN" == */* ]]; then
  if [[ ! -x "$ELIX_WALLET_BIN" ]]; then
    echo "missing executable elix-wallet at: $ELIX_WALLET_BIN" >&2
    exit 2
  fi
else
  require_cmd "$ELIX_WALLET_BIN"
fi

normalized_csv="$(echo "$E2E_BACKENDS" | tr '[:upper:]' '[:lower:]' | tr -d ' ')"
IFS=',' read -r -a requested_backends_raw <<<"$normalized_csv"
requested_backends=()
declare -A backend_seen=()
for name in "${requested_backends_raw[@]}"; do
  [[ -z "$name" ]] && continue
  if [[ -z "${backend_seen[$name]:-}" ]]; then
    backend_seen[$name]=1
    requested_backends+=("$name")
  fi
done
if [[ ${#requested_backends[@]} -eq 0 ]]; then
  echo "no backends requested (E2E_BACKENDS is empty)" >&2
  exit 2
fi

is_requested() {
  local target="$1"
  local name
  for name in "${requested_backends[@]}"; do
    if [[ "$name" == "$target" ]]; then
      return 0
    fi
  done
  return 1
}

enable_for() {
  local backend="$1"
  if is_requested "$backend"; then
    echo 1
  else
    echo 0
  fi
}

mkdir -p "$E2E_WORKSPACE"

echo "starting bridge for backends: ${requested_backends[*]}"
(
  export BRIDGE_HTTP_ADDR="$bridge_http_addr"
  export BRIDGE_AUTH_TOKEN="$BRIDGE_AUTH_TOKEN"
  export BRIDGE_SQLITE_PATH="$bridge_db"
  export WORKSPACE_ROOTS="$WORKSPACE_ROOTS"

  export CODEX_ADAPTER_ENABLED="$(enable_for codex)"
  export GEMINI_ADAPTER_ENABLED="$(enable_for gemini)"
  export CLAUDE_ADAPTER_ENABLED="$(enable_for claude)"

  export CODEX_ADAPTER_BIN="$CODEX_ADAPTER_BIN"
  export GEMINI_ADAPTER_BIN="$GEMINI_ADAPTER_BIN"
  export CLAUDE_ADAPTER_BIN="$CLAUDE_ADAPTER_BIN"

  exec "$BRIDGE_BIN"
) >"$bridge_log" 2>&1 &
bridge_pid=$!

for _ in $(seq 1 120); do
  if curl -fsS "$BRIDGE_BASE_URL/healthz" >/dev/null 2>&1; then
    break
  fi
  if ! kill -0 "$bridge_pid" >/dev/null 2>&1; then
    echo "bridge exited early; log: $bridge_log" >&2
    tail -n 120 "$bridge_log" >&2 || true
    exit 1
  fi
  sleep 0.25
done
if ! curl -fsS "$BRIDGE_BASE_URL/healthz" >/dev/null 2>&1; then
  echo "bridge health check timeout; log: $bridge_log" >&2
  tail -n 120 "$bridge_log" >&2 || true
  exit 1
fi

static_auth_header="Authorization: Bearer $BRIDGE_AUTH_TOKEN"
pair_start_payload="$(jq -n '{permissions:["runs:submit","runs:read","runs:cancel","backends:read","devices:read","devices:write"]}')"
pair_start_resp="$(curl -fsS -X POST "$BRIDGE_BASE_URL/api/v3/pair/start" -H "$static_auth_header" -H "Content-Type: application/json" -d "$pair_start_payload")"
pair_code="$(echo "$pair_start_resp" | jq -r '.pair_code // empty')"
challenge="$(echo "$pair_start_resp" | jq -r '.challenge // empty')"
if [[ -z "$pair_code" || -z "$challenge" ]]; then
  echo "failed to start pair flow; response: $pair_start_resp" >&2
  exit 1
fi

wallet_json="$("$ELIX_WALLET_BIN" generate)"
public_key="$(echo "$wallet_json" | jq -r '.public_key // empty')"
private_key="$(echo "$wallet_json" | jq -r '.private_key // empty')"
if [[ -z "$public_key" || -z "$private_key" ]]; then
  echo "wallet generate did not return keypair: $wallet_json" >&2
  exit 1
fi

sign_json="$("$ELIX_WALLET_BIN" sign --private-key "$private_key" --challenge "$challenge")"
signature="$(echo "$sign_json" | jq -r '.signature // empty')"
if [[ -z "$signature" ]]; then
  echo "wallet sign failed: $sign_json" >&2
  exit 1
fi

pair_complete_payload="$(jq -n --arg code "$pair_code" --arg pk "$public_key" --arg sig "$signature" --arg name "e2e-runner" \
  '{pair_code:$code,public_key:$pk,signature:$sig,device_name:$name}')"
pair_complete_resp="$(curl -fsS -X POST "$BRIDGE_BASE_URL/api/v3/pair/complete" -H "Content-Type: application/json" -d "$pair_complete_payload")"
access_token="$(echo "$pair_complete_resp" | jq -r '.access_token // empty')"
if [[ -z "$access_token" ]]; then
  echo "failed to complete pair flow; response: $pair_complete_resp" >&2
  exit 1
fi

auth_header="Authorization: Bearer $access_token"
backends_json="$(curl -fsS "$BRIDGE_BASE_URL/api/v3/backends" -H "$auth_header")"

declare -A backend_registered=()
declare -A backend_health_ok=()
while IFS=$'\t' read -r name ok; do
  [[ -z "$name" ]] && continue
  backend_registered["$name"]=1
  backend_health_ok["$name"]="$ok"
done < <(echo "$backends_json" | jq -r '.backends[] | [.name, ((.health.ok // false) | tostring)] | @tsv')

if command -v sqlite3 >/dev/null 2>&1; then
  has_sqlite3=1
else
  has_sqlite3=0
fi

failures=0
echo
echo "backend results:"
for backend in "${requested_backends[@]}"; do
  if [[ -z "${backend_registered[$backend]:-}" ]]; then
    echo "- $backend: FAIL (backend not registered)"
    failures=$((failures + 1))
    continue
  fi

  if [[ "${backend_health_ok[$backend]}" != "true" ]]; then
    echo "- $backend: FAIL (backend health not ok)"
    failures=$((failures + 1))
    continue
  fi

  submit_payload="$(jq -n \
    --arg wsid "e2e-$backend" \
    --arg wsp "$E2E_WORKSPACE" \
    --arg b "$backend" \
    --arg p "$E2E_PROMPT" \
    --arg sv "$E2E_SCHEMA_VERSION" \
    '{workspace_id:$wsid,workspace_path:$wsp,backend:$b,prompt:$p,options:{schema_version:$sv}}')"

  submit_resp="$(curl -sS -X POST "$BRIDGE_BASE_URL/api/v3/runs" \
    -H "$auth_header" \
    -H "Content-Type: application/json" \
    -d "$submit_payload")"
  run_id="$(echo "$submit_resp" | jq -r '.run_id // empty')"
  if [[ -z "$run_id" ]]; then
    msg="$(echo "$submit_resp" | jq -r '.error // "submit failed"')"
    echo "- $backend: FAIL (submit: $msg)"
    failures=$((failures + 1))
    continue
  fi

  deadline=$((SECONDS + E2E_TIMEOUT_SECONDS))
  run_json=""
  status=""
  while ((SECONDS < deadline)); do
    run_json="$(curl -sS "$BRIDGE_BASE_URL/api/v3/runs/$run_id" -H "$auth_header")"
    status="$(echo "$run_json" | jq -r '.status // empty')"
    case "$status" in
      completed|failed|cancelled)
        break
        ;;
    esac
    sleep "$E2E_POLL_INTERVAL_SECONDS"
  done

  if [[ "$status" != "completed" && "$status" != "failed" && "$status" != "cancelled" ]]; then
    echo "- $backend: FAIL (timeout waiting terminal status, run_id=$run_id)"
    failures=$((failures + 1))
    continue
  fi

  outcome="$(echo "$run_json" | jq -r '.terminal.outcome // ""')"
  reason_code="$(echo "$run_json" | jq -r '.terminal.reason_code // ""')"
  reason="$(echo "$run_json" | jq -r '.terminal.reason // ""')"
  error_text="$(echo "$run_json" | jq -r '.error // ""')"

  events_count="n/a"
  if [[ "$has_sqlite3" == "1" ]]; then
    events_count="$(sqlite3 "$bridge_db" "SELECT COUNT(*) FROM events WHERE run_id='$run_id';" 2>/dev/null || echo "n/a")"
  fi

  if [[ "$status" == "completed" ]]; then
    echo "- $backend: PASS (run_id=$run_id, events=$events_count, outcome=$outcome, reason_code=$reason_code)"
  else
    echo "- $backend: FAIL (run_id=$run_id, status=$status, outcome=$outcome, reason_code=$reason_code, reason=$reason, error=$error_text, events=$events_count)"
    failures=$((failures + 1))
  fi
done

echo
echo "artifacts:"
echo "- bridge_log: $bridge_log"
echo "- bridge_db:  $bridge_db"

if ((failures > 0)); then
  echo
  echo "e2e result: FAIL ($failures backend(s) failed)"
  exit 1
fi

echo
echo "e2e result: PASS"
