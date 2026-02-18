# Session 多后端实现计划

> **给 Claude：** 必须使用 `superpowers:executing-plans` 子技能，按任务逐步执行本计划。

**目标：** 让 `/api/v3/sessions*` 从仅支持 `codex`，扩展为支持 `codex`、`gemini`、`claude` 三种会话后端。

**架构：** 保持现有 `session.Service` 的 JSON-RPC/会话生命周期不变，但把进程启动配置从硬编码的 codex 路径中解耦。新增“按后端配置启动参数（`bin + args`）”以及 `Create()` 中的后端选择逻辑。通过默认保留 codex 当前行为（`codex app-server --listen stdio://`）来保证向后兼容；Gemini/Claude 通过显式配置按需启用。

**技术栈：** Go 1.22，现有 session JSON-RPC 客户端（`internal/session/codex_client.go`），HTTP API 层（`internal/api/server.go`），`go test` 单元/集成测试。

---

## 执行状态（2026-02-18）

- 当前阶段：`已完成`
- 当前批次：`Batch 1-2`（任务 1 至任务 5）
- 当前焦点：`回归通过，等待评审/提交`

| 任务 | 状态 |
| --- | --- |
| 任务 1：失败测试锁定目标行为 | 已完成 |
| 任务 2：Service 支持多后端启动 | 已完成 |
| 任务 3：API 层多后端覆盖 | 已完成 |
| 任务 4：运行时配置与文档 | 已完成 |
| 任务 5：最终回归验证 | 已完成 |

已执行验证：
- `go test ./internal/session -run "TestSessionCreateSupportsGeminiBackend|TestSessionCreateSupportsClaudeBackend" -count=1`
- `go test ./internal/session -count=1`
- `go test ./internal/api -run "TestSessionCreateSupportsGeminiBackendAPI|TestSessionCreateSupportsClaudeBackendAPI|TestSessionCreateRejectsUnknownBackendAPI" -count=1`
- `go test ./internal/api -count=1`
- `go test ./internal/config -count=1`
- `go test ./internal/api ./internal/session -count=1`
- `go test ./... -count=1`

---

### 任务 1：先用失败测试锁定目标行为

**文件：**
- 修改：`internal/session/service_integration_test.go`
- 测试：`internal/session/service_integration_test.go`

**步骤 1：为非 codex 后端编写失败测试**

新增两个集成测试，预期以下场景都能成功创建 session：
- backend=`gemini`
- backend=`claude`

每个测试应：
- 创建临时 workspace
- 构建假的 app-server 可执行文件（复用现有 fake binary helper 模式）
- 用后端专属二进制配置初始化 `session.NewService`
- 调用 `Create(..., CreateRequest{Backend: "<backend>"})`
- 断言 session 状态为 `ready` 且 backend 与请求一致

**步骤 2：运行测试，验证 RED**

运行：
```bash
go test ./internal/session -run "TestSessionCreateSupportsGeminiBackend|TestSessionCreateSupportsClaudeBackend" -count=1
```

预期：
- 失败，错误信息包含 `only codex backend supports interactive sessions now`

**步骤 3：提交仅含失败测试的改动**

```bash
git add internal/session/service_integration_test.go
git commit -m "test(session): add failing tests for gemini/claude interactive session create"
```

---

### 任务 2：在 Service 中实现后端感知的会话启动

**文件：**
- 修改：`internal/session/model.go`
- 修改：`internal/session/service.go`
- 测试：`internal/session/service_integration_test.go`

**步骤 1：增加后端常量与配置结构**

在 `internal/session/model.go`：
- 新增 `BackendGemini = "gemini"`
- 新增 `BackendClaude = "claude"`

在 `internal/session/service.go`：
- 扩展 `Config`：
  - `GeminiBin string`
  - `GeminiArgs []string`
  - `ClaudeBin string`
  - `ClaudeArgs []string`
- 增加内部启动器结构体：
  - `type backendLaunch struct { bin string; args []string }`
- 增加 `Service.launchers map[string]backendLaunch`

**步骤 2：在 `NewService` 中构建后端启动映射**

规则：
- `codex` 默认行为不变：bin 默认 `codex`，args 为 `buildCodexArgs(cfg.CodexArgs)`
- `gemini` 默认 bin 为 `gemini`，args 为 `cfg.GeminiArgs`
- `claude` 默认 bin 为 `claude`，args 为 `cfg.ClaudeArgs`

键名统一可复用现有 `normalizeMethod` 风格 helper，或新增 backend normalize helper。

**步骤 3：更新 `Create()` 按请求后端选择启动器**

在 `Create()` 中：
- 规范化请求 backend（为空时默认 `codex`）
- 对未知 backend 返回明确错误（`unsupported backend`）
- 用选中的启动器调用 `newAppServerClient(...)`
- 保持会话流转逻辑（`initialize`、`thread/start`、approval 等）不变

**步骤 4：运行测试，验证 GREEN**

运行：
```bash
go test ./internal/session -count=1
```

预期：
- 所有 session 测试通过，包括新增的 gemini/claude 创建测试

**步骤 5：提交实现**

```bash
git add internal/session/model.go internal/session/service.go internal/session/service_integration_test.go
git commit -m "feat(session): support codex gemini claude interactive backend selection"
```

---

### 任务 3：补齐 API 层的多后端会话测试覆盖

**文件：**
- 修改：`internal/api/server_auth_test.go`
- 测试：`internal/api/server_auth_test.go`

**步骤 1：为后端选择增加 API 失败/成功测试**

新增 API 级测试：
- `POST /api/v3/sessions` 且 `backend=gemini` 返回 `201`
- `POST /api/v3/sessions` 且 `backend=claude` 返回 `201`
- 不支持的 backend 返回 `400`

使用 fake backend 二进制，并确保将按后端配置传给 `session.NewService(...)`。

**步骤 2：运行测试，验证 RED/GREEN 边界**

运行：
```bash
go test ./internal/api -run "TestSessionCreateSupportsGeminiBackendAPI|TestSessionCreateSupportsClaudeBackendAPI|TestSessionCreateRejectsUnknownBackendAPI" -count=1
```

预期：
- 若 helper/config 连接不完整，初次会失败
- 完成 helper/config 更新后通过

**步骤 3：重构 API 测试 helper**

更新 `newTestServerWithSession(...)`，使其支持更完整的 session 配置（或显式传入各后端 bin），让 codex/gemini/claude 场景不依赖脆弱的全局默认值。

**步骤 4：验证完整 API 包**

运行：
```bash
go test ./internal/api -count=1
```

预期：
- 通过

**步骤 5：提交 API 覆盖改动**

```bash
git add internal/api/server_auth_test.go
git commit -m "test(api): add interactive session backend coverage for gemini and claude"
```

---

### 任务 4：接入运行时配置并更新文档

**文件：**
- 修改：`internal/config/config.go`
- 修改：`internal/config/config_test.go`
- 修改：`README.md`
- 修改：`docs/API_V3_OPENAPI.yaml`

**步骤 1：增加 session 后端 bin/args 配置字段**

在 `internal/config/config.go` 增加：
- `GeminiSessionBin string`
- `GeminiSessionArgs []string`
- `ClaudeSessionBin string`
- `ClaudeSessionArgs []string`

从环境变量读取：
- `GEMINI_CLI_BIN`（默认 `gemini`）
- `GEMINI_SESSION_ARGS`（默认空）
- `CLAUDE_CLI_BIN`（默认 `claude`）
- `CLAUDE_SESSION_ARGS`（默认空）

保留现有 `CODEX_CLI_BIN` + `CODEX_APP_SERVER_ARGS` 行为不变。

**步骤 2：补充配置解析测试**

在 `internal/config/config_test.go` 新增测试，确保：
- session bin 默认值正确
- 环境变量覆盖后 args 解析正确

**步骤 3：更新对外文档**

在 `README.md`：
- 更新 session 章节，说明多后端支持与新增环境变量
- 明确 codex 有默认 app-server args；gemini/claude 需提供兼容的 session 命令参数

在 `docs/API_V3_OPENAPI.yaml`：
- session 接口描述不再暗示“仅 codex”
- 说明 backend 字段可选值：`codex | gemini | claude`

**步骤 4：验证文档/配置及相关测试**

运行：
```bash
go test ./internal/config -count=1
go test ./internal/api ./internal/session -count=1
```

预期：
- 通过

**步骤 5：提交配置与文档改动**

```bash
git add internal/config/config.go internal/config/config_test.go README.md docs/API_V3_OPENAPI.yaml
git commit -m "chore(config): add session backend envs and docs for multi-backend sessions"
```

---

### 任务 5：最终回归验证

**文件：**
- 修改：无
- 测试：全仓库

**步骤 1：运行完整测试集**

```bash
go test ./... -count=1
```

预期：
- 全部包通过

**步骤 2：检查 diff 健康度**

```bash
git status --short
git diff --stat
```

预期：
- 仅包含预期文件改动

**步骤 3：最终提交检查（若需要压缩提交）**

```bash
git log --oneline -n 5
```

如无特别要求，保留前面任务生成的细粒度提交（更利于审阅）。
