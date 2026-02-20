# Backend Release Readiness Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Increase backend release confidence by adding missing tests for core infrastructure packages and fixing any defects discovered by those tests.

**Architecture:** Keep runtime behavior unchanged unless tests expose concrete defects. Add focused unit tests around policy validation, JSON codec behavior, gRPC adapter transport wrappers, and supervisor process lifecycle safety. Apply minimal code changes required to satisfy failing tests.

**Tech Stack:** Go 1.22, `go test`, gRPC (`google.golang.org/grpc`), standard library process/runtime primitives.

---

### Task 1: Add policy validation tests

**Files:**
- Create: `internal/policy/policy_test.go`
- Test: `internal/policy/policy_test.go`

**Step 1: Write failing tests for workspace validation and option validation**

Cover:
- empty workspace path rejected
- path inside allowed root accepted
- path outside allowed root rejected
- valid run options accepted
- invalid `model/profile/sandbox/schema_version` rejected

**Step 2: Run tests to verify RED/GREEN boundary**

Run: `go test ./internal/policy -count=1`
Expected: If behavior differs from assumptions, test fails first.

**Step 3: Apply minimal implementation change only if needed**

Modify only when tests reveal mismatch.

**Step 4: Re-run package tests**

Run: `go test ./internal/policy -count=1`
Expected: PASS.

### Task 2: Add JSON codec tests

**Files:**
- Create: `internal/rpc/codec/jsoncodec_test.go`
- Test: `internal/rpc/codec/jsoncodec_test.go`

**Step 1: Write tests**

Cover:
- marshal/unmarshal round trip
- invalid JSON returns error
- codec name constant is stable (`json`)
- register path callable without panic

**Step 2: Run tests**

Run: `go test ./internal/rpc/codec -count=1`
Expected: PASS after minimal fixes if required.

### Task 3: Add adapter RPC wrapper tests

**Files:**
- Create: `internal/rpc/adapter/adapter_test.go`
- Test: `internal/rpc/adapter/adapter_test.go`

**Step 1: Write tests for transport wrappers**

Cover:
- method constants match expected service paths
- `adapterStreamEventsServer.Send` forwards message through `SendMsg`
- `adapterStreamEventsClient.Recv` maps received message correctly and propagates stream errors

Use lightweight fake stream structs implementing required gRPC interfaces.

**Step 2: Run tests**

Run: `go test ./internal/rpc/adapter -count=1`
Expected: PASS after minimal fixes if required.

### Task 4: Add supervisor safety tests and fix defects

**Files:**
- Create: `internal/adapter/supervisor/supervisor_test.go`
- Modify (if required): `internal/adapter/supervisor/supervisor.go`
- Test: `internal/adapter/supervisor/supervisor_test.go`

**Step 1: Write tests for deterministic safety behavior**

Cover:
- missing binary returns clear error
- stopping without process is no-op
- repeat stop remains safe

Add lifecycle test for start/stop only if portable helper process setup is stable.

**Step 2: Run tests**

Run: `go test ./internal/adapter/supervisor -count=1`
Expected: initial failures if lifecycle handling is unsafe.

**Step 3: Implement minimal fix (if tests fail)**

Prefer small lifecycle hardening (e.g., state cleanup after stop/wait) without expanding behavior scope.

**Step 4: Re-run tests**

Run: `go test ./internal/adapter/supervisor -count=1`
Expected: PASS.

### Task 5: Full backend verification gate (release decision input)

**Files:**
- Modify: none (unless defects found during verification)

**Step 1: Run focused regression suite**

Run:
- `go test ./internal/policy ./internal/rpc/codec ./internal/rpc/adapter ./internal/adapter/supervisor -count=1`

Expected: PASS.

**Step 2: Run full repository tests**

Run:
- `go test ./... -count=1`

Expected: PASS.

**Step 3: Collect release evidence**

Run:
- `git status --short`
- `git diff --stat`

Expected:
- only intended files changed
- all tests green

