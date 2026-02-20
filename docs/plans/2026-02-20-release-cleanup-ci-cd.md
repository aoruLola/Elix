# Release Cleanup and CI/CD Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Clean repository noise, add GitHub CI/CD workflows, and publish the latest backend updates to GitHub.

**Architecture:** Keep runtime behavior unchanged; only add repository hygiene rules and automation workflows. CI validates formatting, vet, and tests. CD runs on version tags and creates GitHub Releases.

**Tech Stack:** Git, GitHub Actions, Go 1.22.

---

### Task 1: Clean repository noise

**Files:**
- Modify: `.gitignore`

Steps:
1. Add local-only workspace noise ignores (`.research/`, `.vscode/`, `Elix-Web/`).
2. Verify ignored status via `git status --short`.

### Task 2: Add CI workflow

**Files:**
- Create: `.github/workflows/ci.yml`

Steps:
1. Trigger on push/PR to `main`.
2. Run `gofmt` check on tracked Go files.
3. Run `go vet ./...`.
4. Run `go test ./... -count=1`.

### Task 3: Add CD workflow

**Files:**
- Create: `.github/workflows/cd.yml`

Steps:
1. Trigger on tag pushes `v*` and manual dispatch.
2. Re-run quality gates (`go vet`, `go test`).
3. Package source snapshot as artifact.
4. Publish GitHub Release with generated notes and attached artifact.

### Task 4: Verify and publish

Steps:
1. Run full verification command set locally.
2. Commit all intended changes.
3. Push to `origin/main`.
4. Report release readiness status with evidence.

