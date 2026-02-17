# Event Contract v2

This document defines the canonical event envelope emitted by Bridge.

## Schema

`schema/event.v2.schema.json`

## Required fields

1. `run_id`
2. `seq`
3. `ts`
4. `schema_version`
5. `type`
6. `channel`
7. `format`
8. `role`
9. `backend`

## Enums

1. `type`: `token | tool_call | tool_result | patch | status | done | error`
2. `channel`: `final | working | system`
3. `format`: `markdown | plain | json | diff`
4. `role`: `assistant | system`
5. `schema_version`: `v1 | v2` (Bridge currently emits `v2`)

## Backward-compatible fields

`compat` object is provided for clients that only need simplified rendering:

1. `compat.text`: plain fallback text
2. `compat.status`: normalized status for status/done lanes
3. `compat.is_error`: boolean error marker

## Rendering guidance

1. `channel=final`: primary assistant answer region
2. `channel=working`: progress/process panel (typically collapsible)
3. `channel=system`: status/error system lane

## Compatibility

1. Adapter or driver may emit partial fields.
2. Bridge normalizes defaults before persistence and fan-out.
3. All persisted and streamed events must pass contract validation.
4. New additive fields should keep `schema_version=v2`; breaking changes require a major schema bump.

## Negotiation

Use `GET /api/v3/backends` to discover:

1. `schema_versions`
2. `preferred_schema_version`
3. `compat_fields`

At run submission time (`POST /api/v3/runs`):

1. client may request `options.schema_version`
2. Bridge validates requested version against backend capabilities
3. if omitted, Bridge selects backend `preferred_schema_version`
