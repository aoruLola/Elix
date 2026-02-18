# EchoHelix Bridge v3 (Fresh Start)

Go-based control plane for coding-agent runs.

## Components

1. `cmd/bridge`: HTTP + WebSocket API, run state machine, SQLite ledger.
2. `cmd/codex-adapter`: gRPC adapter that launches Codex CLI per run.
3. `cmd/gemini-adapter`: gRPC adapter that launches Gemini CLI per run.
4. `cmd/claude-adapter`: gRPC adapter that launches Claude CLI per run.
5. `cmd/elix-wallet`: local wallet tool (mnemonic/keypair/signature).
6. `proto/adapter.proto`: canonical Bridge↔Adapter contract.

## Build

```bash
go build -o codex-adapter ./cmd/codex-adapter
go build -o gemini-adapter ./cmd/gemini-adapter
go build -o claude-adapter ./cmd/claude-adapter
go build -o elix-wallet ./cmd/elix-wallet
go build -o elix-bridge ./cmd/bridge
```

## Run

```bash
export BRIDGE_AUTH_TOKEN='echohelix-dev-token'
export WORKSPACE_ROOTS='/tmp,/home'
export CODEX_CLI_BIN='codex'
export GEMINI_CLI_BIN='gemini'
export CLAUDE_CLI_BIN='claude'
export CODEX_SESSION_ENABLED=1
export CODEX_APP_SERVER_ARGS=''
export CODEX_SESSION_START_TIMEOUT_SECONDS=20
export CODEX_SESSION_REQUEST_TIMEOUT_SECONDS=30
export AUTH_ACCESS_TOKEN_TTL_SECONDS=900
export AUTH_REFRESH_TOKEN_TTL_SECONDS=86400
export AUTH_PAIR_CODE_TTL_SECONDS=60
export AUTH_PAIR_START_RATE_LIMIT=6
export AUTH_PAIR_START_RATE_WINDOW_SECONDS=60
export AUTH_REFRESH_FAIL_ALERT_THRESHOLD=5
export AUTH_REFRESH_FAIL_ALERT_WINDOW_SECONDS=120
export AUTH_AUTH_FAIL_ALERT_THRESHOLD=8
export AUTH_AUTH_FAIL_ALERT_WINDOW_SECONDS=120
export AUTH_PAIR_COMPLETE_FAIL_ALERT_THRESHOLD=5
export AUTH_PAIR_COMPLETE_FAIL_ALERT_WINDOW_SECONDS=120
export BRIDGE_FILE_STORE_DIR='./files'
export BRIDGE_MAX_UPLOAD_BYTES=20971520
./elix-bridge
```

Adapter binary path defaults are now resolved from the `elix-bridge` executable directory.
Example: if bridge runs from `/opt/echohelix/bin/elix-bridge`, defaults become:

1. `/opt/echohelix/bin/codex-adapter`
2. `/opt/echohelix/bin/gemini-adapter`
3. `/opt/echohelix/bin/claude-adapter`

So you no longer need to `cd` into the repo before starting bridge.

## Daemon Mode (systemd)

Install bridge + adapters as a managed service:

```bash
make install-systemd
```

Or run installer directly:

```bash
sudo bash ./scripts/install_systemd_bridge.sh
```

First-time bootstrap (recommended, root):

```bash
make bootstrap
```

This does: install service, initialize `/etc/echohelix/elix-bridge.env`, generate token if placeholder, restart service, run health check, and print pairing command.

Preflight config check:

```bash
make preflight
```

Ops helper:

```bash
./scripts/elixctl.sh status
./scripts/elixctl.sh logs
./scripts/elixctl.sh health
./scripts/elixctl.sh backends
./scripts/elixctl.sh pair-start
```

Built-in web console:

1. start bridge (`elix-bridge` service or local binary)
2. open `http://127.0.0.1:8765/ui/`

After install:

1. service unit: `/etc/systemd/system/elix-bridge.service`
2. env file: `/etc/echohelix/elix-bridge.env`
3. status: `systemctl status elix-bridge`
4. logs: `journalctl -u elix-bridge -f`

Backend adapter switches:

1. `CODEX_ADAPTER_ENABLED=1|0` (default: `1`)
2. `GEMINI_ADAPTER_ENABLED=1|0` (default: `1`)
3. `CLAUDE_ADAPTER_ENABLED=1|0` (default: `0`)
4. `CODEX_ADAPTER_ADDR` (default: `127.0.0.1:50051`)
5. `GEMINI_ADAPTER_ADDR` (default: `127.0.0.1:50052`)
6. `CLAUDE_ADAPTER_ADDR` (default: `127.0.0.1:50053`)

Gemini CLI option flags (optional):

1. `GEMINI_CLI_MODEL_FLAG` (default: `--model`)
2. `GEMINI_CLI_PROFILE_FLAG` (default: empty, disabled)
3. `GEMINI_CLI_SANDBOX_FLAG` (default: empty, disabled)
4. `GEMINI_CLI_PROMPT_FLAG` (default: `-p`, headless mode; set `none` to use positional prompt)
5. `GEMINI_CLI_ARGS` (default: `--output-format stream-json`)
6. `GEMINI_STREAM_INCLUDE_USER_MESSAGES` (default: `false`, suppress `role=user` message events)

Claude CLI option flags (optional):

1. `CLAUDE_CLI_MODEL_FLAG` (default: `--model`)
2. `CLAUDE_CLI_PROFILE_FLAG` (default: empty, disabled)
3. `CLAUDE_CLI_SANDBOX_FLAG` (default: empty, disabled)
4. `CLAUDE_CLI_PROMPT_FLAG` (default: positional prompt; set e.g. `-p` if CLI supports)
5. `CLAUDE_CLI_ARGS` (default: `--print --verbose --output-format stream-json`)

Claude API mode (recommended for headless bridge deployments):

1. `ANTHROPIC_API_KEY` (or `ANTHROPIC_AUTH_TOKEN`) for auth
2. `ANTHROPIC_BASE_URL` for OpenAI-compatible proxy/base URL
3. Keep secrets in environment only; do not commit keys into repo files

Example:

```bash
export CLAUDE_ADAPTER_ENABLED=1
export CLAUDE_CLI_BIN='claude'
export ANTHROPIC_API_KEY='<redacted>'
export ANTHROPIC_BASE_URL='http://127.0.0.1:3000/'
./elix-bridge
```

Codex interactive session mode (`codex app-server`) switches:

1. `CODEX_SESSION_ENABLED=1|0` (default: `1`)
2. `CODEX_APP_SERVER_ARGS` (default: empty; appended after `codex app-server --listen stdio://`)
3. `CODEX_SESSION_START_TIMEOUT_SECONDS` (default: `20`)
4. `CODEX_SESSION_REQUEST_TIMEOUT_SECONDS` (default: `30`)
5. `BACKEND_CALL_READ_METHODS` (default: `status`)
6. `BACKEND_CALL_CANCEL_METHODS` (default: `turn/interrupt`)
7. `BACKEND_CALL_BLOCKED_METHODS` (default: `initialize,initialized`)

## API

1. `POST /api/v3/pair/start` (requires bootstrap static token)
2. `POST /api/v3/pair/complete` (public; one-time code + signature)
3. `POST /api/v3/session/refresh` (public; refresh token rotation)
4. `GET /api/v3/devices`
5. `POST /api/v3/devices/{address}/rename`
6. `POST /api/v3/devices/{address}/revoke`
7. `POST /api/v3/runs`
8. `GET /api/v3/runs/{run_id}`
9. `POST /api/v3/runs/{run_id}/cancel`
10. `GET /api/v3/runs/{run_id}/events` (WebSocket)
11. `POST /api/v3/sessions`
12. `GET /api/v3/sessions`
13. `GET /api/v3/sessions/{session_id}`
14. `DELETE /api/v3/sessions/{session_id}`
15. `POST /api/v3/sessions/{session_id}/turns`
16. `POST /api/v3/sessions/{session_id}/interrupt`
17. `GET /api/v3/sessions/{session_id}/backend/status`
18. `POST /api/v3/sessions/{session_id}/backend/call`
19. `GET /api/v3/sessions/{session_id}/events` (WebSocket)
20. `GET /api/v3/sessions/{session_id}/requests`
21. `POST /api/v3/sessions/{session_id}/requests/{request_id}`
22. `GET /api/v3/sessions/{session_id}/approvals`
23. `POST /api/v3/sessions/{session_id}/approvals/{request_id}`
24. `GET /api/v3/backends`
25. `POST /api/v3/files` (multipart upload, field `file`)
26. `GET /api/v3/files/{file_id}`
27. `GET /api/v3/usage/tokens`
28. `GET /api/v3/usage/quota`
29. `POST /api/v3/emergency/stop` (requires bootstrap static token)
30. `POST /api/v3/emergency/resume` (requires bootstrap static token)
31. `GET /api/v3/emergency/status` (requires bootstrap static token)

All `/api/v3/*` endpoints require:

```text
Authorization: Bearer <access_token|BRIDGE_AUTH_TOKEN>
```

`BRIDGE_AUTH_TOKEN` is now bootstrap-only (pairing/ops scopes, no run scopes).  
Workload APIs (`/api/v3/runs*`) should use short-lived `access_token`.

Emergency switch usage:

```bash
curl -X POST http://127.0.0.1:8765/api/v3/emergency/stop \
  -H 'Authorization: Bearer echohelix-dev-token' \
  -H 'Content-Type: application/json' \
  -d '{"reason":"ops maintenance"}'

curl -X GET http://127.0.0.1:8765/api/v3/emergency/status \
  -H 'Authorization: Bearer echohelix-dev-token'

curl -X POST http://127.0.0.1:8765/api/v3/emergency/resume \
  -H 'Authorization: Bearer echohelix-dev-token'
```

Attachment usage in run submit:

```json
{
  "workspace_id": "ws-1",
  "workspace_path": "/tmp",
  "backend": "codex",
  "prompt": "review @spec.md",
  "context": {
    "attachments": [
      { "file_id": "<uploaded-file-id>", "alias": "spec.md" }
    ]
  }
}
```

API contract (OpenAPI):

1. `docs/API_V3_OPENAPI.yaml`

`POST /api/v3/pair/start` has built-in IP rate limiting and returns `429` when exceeded.

When `CODEX_SESSION_ENABLED=0`, all `/api/v3/sessions*` endpoints return `503`.

## Secure Pairing (elix://)

1. Admin creates one-time pair code:

```bash
curl -X POST http://127.0.0.1:8765/api/v3/pair/start \
  -H 'Authorization: Bearer echohelix-dev-token' \
  -H 'Content-Type: application/json' \
  -d '{"permissions":["runs:submit","runs:read","runs:cancel","backends:read","devices:read","devices:write"]}'
```

2. Client uses wallet keypair to sign the returned `challenge`, then completes pairing:

```bash
curl -X POST http://127.0.0.1:8765/api/v3/pair/complete \
  -H 'Content-Type: application/json' \
  -d '{"pair_code":"ABCD-EFGH","public_key":"<base64>","signature":"<base64>","device_name":"macbook"}'
```

3. Bridge returns `access_token` + `refresh_token`; refresh via `/api/v3/session/refresh`.

4. `pair/start` response includes `elix_uri`:
   `elix://host/pair#code=...&challenge=...`

Wallet helper examples:

```bash
./elix-wallet generate
./elix-wallet recover --mnemonic "..."
./elix-wallet sign --private-key <base64> --challenge <challenge>
```

One-click pairing demo (bridge must be running):

```bash
make pair-demo
```

The demo executes:

1. `pair/start`
2. wallet `generate` + `sign`
3. `pair/complete`
4. run submit + poll terminal status
5. `session/refresh` rotation
6. device revoke + token invalidation check

`GET /api/v3/runs/{run_id}` now includes standardized terminal metadata for frontend rendering:

1. `terminal.is_terminal`: whether the run reached terminal state
2. `terminal.outcome`: `completed | failed | cancelled`
3. `terminal.reason_code`: normalized code (`success | backend_error | timeout | policy_denied | cancelled_by_user | ...`)
4. `terminal.reason`: human-readable summary

## Submit Run Example

```bash
curl -X POST http://127.0.0.1:8765/api/v3/runs \
  -H 'Authorization: Bearer <access_token>' \
  -H 'Content-Type: application/json' \
  -d '{
    "workspace_id":"demo",
    "workspace_path":"/tmp/demo",
    "backend":"gemini",
    "prompt":"explain this repository",
    "context":{"user":"local"},
    "options":{
      "model":"gpt-5",
      "profile":"default",
      "sandbox":"workspace-write",
      "schema_version":"v2"
    }
  }'
```

`options` whitelist:

1. `model`: `[A-Za-z0-9._:-]{1,128}`
2. `profile`: `[A-Za-z0-9._:-]{1,128}`
3. `sandbox`: `read-only | workspace-write | danger-full-access`
4. `schema_version`: `v1 | v2` (optional; defaults to backend preferred version)

## Codex Interactive Session Example

Create session:

```bash
curl -X POST http://127.0.0.1:8765/api/v3/sessions \
  -H 'Authorization: Bearer <access_token>' \
  -H 'Content-Type: application/json' \
  -d '{
    "workspace_id":"demo",
    "workspace_path":"/tmp/demo",
    "backend":"codex",
    "model":"gpt-5",
    "approval_policy":"on-request",
    "sandbox":"workspace-write"
  }'
```

Start turn:

```bash
curl -X POST http://127.0.0.1:8765/api/v3/sessions/<session_id>/turns \
  -H 'Authorization: Bearer <access_token>' \
  -H 'Content-Type: application/json' \
  -d '{"prompt":"scan this repo and list TODOs"}'
```

Stream events:

```bash
wscat -c ws://127.0.0.1:8765/api/v3/sessions/<session_id>/events \
  -H "Authorization: Bearer <access_token>"
```

Handle pending approvals:

```bash
curl -X GET http://127.0.0.1:8765/api/v3/sessions/<session_id>/approvals \
  -H 'Authorization: Bearer <access_token>'

curl -X POST http://127.0.0.1:8765/api/v3/sessions/<session_id>/approvals/<request_id> \
  -H 'Authorization: Bearer <access_token>' \
  -H 'Content-Type: application/json' \
  -d '{"decision":"accept","for_session":false}'
```

Read Codex backend status passthrough:

```bash
curl -X GET http://127.0.0.1:8765/api/v3/sessions/<session_id>/backend/status \
  -H 'Authorization: Bearer <access_token>'
```

Call generic Codex backend RPC passthrough:

```bash
curl -X POST http://127.0.0.1:8765/api/v3/sessions/<session_id>/backend/call \
  -H 'Authorization: Bearer <access_token>' \
  -H 'Content-Type: application/json' \
  -d '{
    "method":"status",
    "params":{},
    "timeout_ms":3000
  }'
```

`backend/call` scope mapping (configurable via env):

1. `status` -> `runs:read`
2. `turn/interrupt` -> `runs:cancel`
3. all other methods -> `runs:submit`

## Event Mapping (CLI JSONL -> Bridge Event)

1. `thread.started` / `turn.started` -> `status`
2. `item.completed` + `agent_message` -> `token`
3. `item.completed` + `tool_call` -> `tool_call`
4. `item.completed` + `tool_result` -> `tool_result`
5. `turn.completed` -> `done`
6. parse failure/plain line -> `token`

Gemini `--output-format stream-json` mapping:

1. `init` -> `status` (`phase=init`, includes `session_id/model`)
2. `message` + `role=assistant` -> `token` (`channel=final`, `format=markdown`)
3. `message` + non-assistant role -> `token` (`channel=working`, `format=json`)
4. `result` + `status=success` -> `done` (includes `usage` from `stats`)
5. `result` + non-success status -> `error`
6. `tool.call`/`tool-call`/`tool_invocation` -> `tool_call`
7. `tool.result`/`tool-result`/`tool_response` -> `tool_result`

Claude adapter mapping (template):

1. `message_start`/`thread.started` -> `status`
2. `content_block_delta`/`message_delta` -> `token(final/markdown)`
3. `tool_use` + aliases -> `tool_call`
4. `tool_result` + aliases -> `tool_result`
5. `message_stop`/`result` -> `done`

Event envelope also carries rendering metadata:

1. `channel`: `final | working | system`
2. `format`: `markdown | plain | json | diff`
3. `role`: `assistant | system`
4. `schema_version`: `v2` (current producer default)
5. `compat`: backward-compatible summary fields (`text/status/is_error`)

For markdown code blocks, adapter applies a fence-aware assembler:

1. unclosed fenced blocks are buffered
2. event is emitted when fences close (or flushed at run end)

`GET /api/v3/backends` capabilities include protocol negotiation fields:

1. `schema_versions`: supported event schema versions
2. `preferred_schema_version`: adapter default version
3. `compat_fields`: backward-compatible helper fields

## Cross-Backend E2E

Run one-click e2e for selected backends:

```bash
make e2e-backends
```

`e2e-backends` now runs the secure pair flow automatically and then executes runs with session token.

Common env overrides:

1. `E2E_BACKENDS=codex,gemini,claude`
2. `E2E_PROMPT='reply exactly ok'`
3. `E2E_TIMEOUT_SECONDS=120`
4. `BRIDGE_BASE_URL=http://127.0.0.1:8765`
5. `ANTHROPIC_API_KEY` + `ANTHROPIC_BASE_URL` (for Claude API mode)
