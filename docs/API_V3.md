# EchoHelix Bridge API v3 (Guide)

This guide is a human-readable companion to `docs/API_V3_OPENAPI.yaml`.
For strict request/response contracts, use the OpenAPI file.

## Base URL

- `http://127.0.0.1:8765`

## Auth

Protected endpoints require:

```text
Authorization: Bearer <access_token or BRIDGE_AUTH_TOKEN>
```

Token types:

1. Bootstrap static token (`BRIDGE_AUTH_TOKEN`): bootstrap/admin operations.
2. Session access token: workload operations.
3. Refresh token: rotate via `/api/v3/session/refresh`.

## Health

### `GET /healthz`

Public health check.

Response:

```json
{ "ok": true }
```

## Pairing and Device Management

### `POST /api/v3/pair/start`

Start secure pairing. Requires bootstrap/static privileges.

### `POST /api/v3/pair/complete`

Complete pairing with wallet signature and receive token pair.

### `POST /api/v3/session/refresh`

Rotate access/refresh tokens.

### `GET /api/v3/devices`

List paired devices (`devices:read`).

### `POST /api/v3/devices/{address}/rename`

Rename device (`devices:write`).

### `POST /api/v3/devices/{address}/revoke`

Revoke device and invalidate related sessions (`devices:write`).

## Runs

### `POST /api/v3/runs`

Submit a run (`runs:submit`).

### `GET /api/v3/runs/{run_id}`

Get run status (`runs:read`).

### `POST /api/v3/runs/{run_id}/cancel`

Cancel run (`runs:cancel`).

### `GET /api/v3/runs/{run_id}/events` (WebSocket)

Stream run events (`runs:read`).

Query options:

1. `from_seq` (optional)
2. `access_token` (browser fallback)
3. `token` (legacy alias)

## Interactive Sessions

### `POST /api/v3/sessions`

Create session (`runs:submit`).

### `GET /api/v3/sessions`

List sessions (`runs:read`).

### `GET /api/v3/sessions/{session_id}`

Get session status (`runs:read`).

### `DELETE /api/v3/sessions/{session_id}`

Close session (`runs:cancel`).

### `POST /api/v3/sessions/{session_id}/turns`

Start or steer turn (`runs:submit`).

### `POST /api/v3/sessions/{session_id}/interrupt`

Interrupt active turn (`runs:cancel`).

### `GET /api/v3/sessions/{session_id}/backend/status`

Backend passthrough `status` (`runs:read`).

### `POST /api/v3/sessions/{session_id}/backend/call`

Generic backend passthrough call. Scope depends on method:

1. `status` -> `runs:read`
2. `turn/interrupt` -> `runs:cancel`
3. others -> `runs:submit`

### `GET /api/v3/sessions/{session_id}/events` (WebSocket)

Stream session events (`runs:read`).

Query options:

1. `from_seq` (optional)
2. `access_token` (browser fallback)
3. `token` (legacy alias)

### `GET /api/v3/sessions/{session_id}/requests`

List pending server requests (`runs:read`).

### `POST /api/v3/sessions/{session_id}/requests/{request_id}`

Resolve pending request (`runs:cancel`).

### `GET /api/v3/sessions/{session_id}/approvals`

List pending approvals (`runs:read`).

### `POST /api/v3/sessions/{session_id}/approvals/{request_id}`

Resolve approval (`runs:cancel`).

## Backends and Usage

### `GET /api/v3/backends`

List backend health and capabilities (`backends:read`).

### `GET /api/v3/usage/tokens`

Aggregate token usage (`backends:read`).

Query options:

1. `window` (Go duration, e.g. `24h`)
2. `from` (RFC3339)
3. `to` (RFC3339)
4. `backend`

### `GET /api/v3/usage/quota`

Get token quota usage for current UTC day (`backends:read`).

Query options:

1. `backend`

## Files

### `POST /api/v3/files`

Upload file (`runs:submit`, multipart field name `file`).

### `GET /api/v3/files/{file_id}`

Get uploaded file metadata (`runs:read`).

## Emergency Controls

### `POST /api/v3/emergency/stop`

Activate emergency stop and cancel active runs. Requires bootstrap/static privileges.

### `POST /api/v3/emergency/resume`

Resume run submissions. Requires bootstrap/static privileges.

### `GET /api/v3/emergency/status`

Get emergency state. Requires bootstrap/static privileges.

## Common Errors

1. `400` invalid request payload/params.
2. `401` missing or invalid bearer token.
3. `403` missing scope or forbidden principal.
4. `404` resource not found.
5. `429` pair/start rate-limited (includes `Retry-After`).
6. `503` service unavailable (for example session service disabled).

## Source of Truth

- Contract: `docs/API_V3_OPENAPI.yaml`
- Server implementation: `internal/api/server.go`
- API integration tests: `internal/api/server_auth_test.go`, `internal/api/server_ws_auth_test.go`
