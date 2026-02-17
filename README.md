# EchoHelix Bridge v3 (Fresh Start)

Go-based control plane for coding-agent runs.

## Components

1. `cmd/bridge`: HTTP + WebSocket API, run state machine, SQLite ledger.
2. `cmd/codex-adapter`: gRPC adapter that launches Codex CLI per run.
3. `cmd/gemini-adapter`: gRPC adapter that launches Gemini CLI per run.
4. `cmd/claude-adapter`: gRPC adapter that launches Claude CLI per run.
5. `proto/adapter.proto`: canonical Bridge↔Adapter contract.

## Build

```bash
go build -o codex-adapter ./cmd/codex-adapter
go build -o gemini-adapter ./cmd/gemini-adapter
go build -o claude-adapter ./cmd/claude-adapter
go build -o bridge ./cmd/bridge
```

## Run

```bash
export BRIDGE_AUTH_TOKEN='echohelix-dev-token'
export WORKSPACE_ROOTS='/tmp,/home'
export CODEX_ADAPTER_BIN='./codex-adapter'
export GEMINI_ADAPTER_BIN='./gemini-adapter'
export CLAUDE_ADAPTER_BIN='./claude-adapter'
export CODEX_CLI_BIN='codex'
export GEMINI_CLI_BIN='gemini'
export CLAUDE_CLI_BIN='claude'
./bridge
```

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
./bridge
```

## API

1. `POST /api/v3/runs`
2. `GET /api/v3/runs/{run_id}`
3. `POST /api/v3/runs/{run_id}/cancel`
4. `GET /api/v3/runs/{run_id}/events` (WebSocket)
5. `GET /api/v3/backends`

All `/api/v3/*` endpoints require:

```text
Authorization: Bearer <BRIDGE_AUTH_TOKEN>
```

## Submit Run Example

```bash
curl -X POST http://127.0.0.1:8765/api/v3/runs \
  -H 'Authorization: Bearer echohelix-dev-token' \
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
