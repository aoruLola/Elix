# EchoHelix Bridge v3

EchoHelix Bridge is a control-plane service for coding-agent workloads.
It exposes HTTP/WebSocket APIs for pairing, run submission, interactive sessions,
backend passthrough calls, attachments, emergency controls, and usage/quota tracking.

## Repository Scope

This repository currently focuses on core backend packages under `internal/`.
The primary artifacts are:

1. API layer: `internal/api`
2. Run orchestration: `internal/run`
3. Interactive sessions: `internal/session`
4. Auth + pairing: `internal/auth`
5. Policy + ledger: `internal/policy`, `internal/ledger`
6. Adapter runtime/driver contracts: `internal/adapter/*`, `internal/driver/*`, `internal/rpc/*`

## Documentation

1. Human-readable API guide: `docs/API_V3.md`
2. OpenAPI contract: `docs/API_V3_OPENAPI.yaml`
3. Event schema notes: `docs/EVENT_CONTRACT_V2.md`
4. Session multi-backend plan: `docs/plans/2026-02-18-session-multi-backend.md`

## Development Quickstart

### 1) Run tests

```bash
go test ./... -count=1
```

### 2) Focused package tests

```bash
go test ./internal/api ./internal/run ./internal/session -count=1
```

## Runtime Configuration

Common environment variables:

1. `BRIDGE_HTTP_ADDR` (default: `:8765`)
2. `BRIDGE_AUTH_TOKEN` (bootstrap static token)
3. `WORKSPACE_ROOTS` (comma-separated allowed roots)
4. `CODEX_SESSION_ENABLED` (`1|0`, default `1`)
5. `CODEX_CLI_BIN`, `GEMINI_CLI_BIN`, `CLAUDE_CLI_BIN`
6. `CODEX_APP_SERVER_ARGS`, `GEMINI_SESSION_ARGS`, `CLAUDE_SESSION_ARGS`
7. `BRIDGE_FILE_STORE_DIR`, `BRIDGE_MAX_UPLOAD_BYTES`
8. `DAILY_TOKEN_QUOTA` (format: `backend:limit,backend:limit`)
9. `TRUSTED_PROXY_CIDRS` (optional, for trusted `X-Forwarded-For`)

For production-style env template, see:

- `deploy/systemd/elix-bridge.env.example`

## Authentication Model

All protected APIs require bearer token auth.

1. Bootstrap token (`BRIDGE_AUTH_TOKEN`): intended for bootstrap/admin operations.
2. Session access token: issued via pairing, used for workload APIs.
3. Refresh token: rotated via `POST /api/v3/session/refresh`.

Scopes:

1. `pair:start`
2. `runs:submit`
3. `runs:read`
4. `runs:cancel`
5. `backends:read`
6. `devices:read`
7. `devices:write`

## API Highlights

Core routes:

1. Pairing: `/api/v3/pair/start`, `/api/v3/pair/complete`, `/api/v3/session/refresh`
2. Runs: `/api/v3/runs`, `/api/v3/runs/{run_id}`, `/api/v3/runs/{run_id}/events`, `/api/v3/runs/{run_id}/cancel`
3. Sessions: `/api/v3/sessions*`
4. Backends: `/api/v3/backends`
5. Usage/Quota: `/api/v3/usage/tokens`, `/api/v3/usage/quota`
6. Emergency switch: `/api/v3/emergency/stop|resume|status`
7. Files: `/api/v3/files`, `/api/v3/files/{file_id}`

WebSocket auth:

1. Preferred: `Authorization: Bearer <access_token>`
2. Browser fallback: query token `?access_token=<token>`
3. Legacy query alias: `?token=<token>`

## CI/CD

GitHub Actions workflows:

1. CI: `.github/workflows/ci.yml`
   - Trigger: push/PR to `main`
   - Steps: `gofmt` check, `go vet ./...`, `go test ./... -count=1`
2. CD: `.github/workflows/cd.yml`
   - Trigger: version tags `v*` or manual dispatch
   - Steps: quality gates, source archive packaging, GitHub Release publish

## Release Process

1. Ensure `go test ./... -count=1` passes.
2. Merge to `main`.
3. Tag a release:

```bash
git tag v0.1.0
git push origin v0.1.0
```

4. CD workflow publishes release assets automatically.

## Notes

1. `POST /api/v3/pair/start` has built-in rate limiting.
2. When sessions are disabled (`CODEX_SESSION_ENABLED=0`), `/api/v3/sessions*` returns `503`.
3. Emergency stop blocks new run submissions until resumed.
