# EchoHelix 重启计划（2026-02-17）

## 1. 目标
将 EchoHelix 重构为“Bridge 控制平面 + 多 CLI Adapter 执行平面”的可扩展 Agent 平台，优先支持：Gemini CLI、Codex CLI、Claude Code、Aider。

## 2. 核心架构决策
1. Bridge 只负责网关、会话、路由、安全、审计，不承载 Agent 推理语义。
2. 每个 CLI 通过独立 Adapter 接入，Bridge 统一调度 Adapter。
3. Adapter 与 CLI 通信采用 `stdio 优先，pty 兜底`。
4. Bridge 与 Adapter 使用统一事件协议（A2A-Lite），统一事件类型：
   - `token`
   - `tool_call`
   - `tool_result`
   - `patch`
   - `done`
   - `error`

## 3. Bridge 必做改造
1. 引入 `BackendDriver` 抽象：`run / stream / cancel / health / capabilities`。
2. 引入 Adapter Supervisor：按需拉起/重启/健康检查/超时终止。
3. 建立 Run 状态机：`queued -> running -> streaming -> completed|failed|cancelled`。
4. API 分层：保留 `/api/v2/chat/proxy`，新增 `/api/v2/agent/run|stream|cancel|backends`。
5. 统一治理：目录沙箱、命令白名单、并发限制、超时、输出限制。
6. 建立 Run Ledger：输入、事件、工具调用、补丁、错误全量可追溯。

## 4. 迭代顺序
1. M1：先将 Gemini/Aider 接入统一 Driver，不改 App 协议。
2. M2：接入 Codex Adapter，打通完整 run/stream/cancel。
3. M3：接入 Claude Adapter，补齐 capabilities 路由。
4. M4：完善恢复、重放、观测和压测，进入稳定阶段。

## 5. 风险与约束
1. 各 CLI 输出格式变化风险高，必须在 Adapter 层做版本隔离。
2. 工具权限风险高，必须统一在 Bridge 策略层收口。
3. 长任务恢复复杂，Run Ledger 从第一天强制落地。

## 6. 重启后的执行原则
1. 先完成 Bridge 控制平面，再扩 CLI 数量。
2. 先保证可观测与安全，再追求功能覆盖。
3. 所有新增后端必须实现统一 Driver 契约后方可上线。

## 7. 后续特性（已记录）
### 7.1 细粒度消息流（Token/片段级）
1. 当前状态：暂不在 M1 实现，保持“按行流式 + 结构化事件”。
2. 触发条件：Bridge 与 Adapter 稳定后再开启（建议 M2/M3）。
3. 目标收益：
   - 更低首字延迟
   - 更平滑的实时输出体验
4. 主要代价：
   - 事件量与落库量显著上升
   - 背压与重放复杂度增加
5. 未来实现要点：
   - 在 Adapter 增加“细粒度流开关”（默认关闭）
   - Bridge 侧增加事件限流与批处理
   - Run Ledger 支持分片聚合存储策略

## 8. 当前落地状态（2026-02-18）
1. 已完成双后端模板：`codex` + `gemini`，统一 Bridge API 与事件契约。
2. 已完成后端能力协商：`schema_version` 支持 `v1|v2`，按 backend capabilities 进行协商。
3. 已完成向后兼容字段：事件统一携带 `compat.text/status/is_error`。
4. 已完成多 Adapter 启动与注册：Bridge 支持独立地址、独立二进制、独立开关。
5. 已补充回归测试：包含多 backend 列表与契约校验路径。

## 9. Runtime 抽象进展（2026-02-18）
1. 已新增通用 `CLI Adapter Runtime`，统一 run/stream/cancel/进程执行/事件发布。
2. `codex-adapter` 与 `gemini-adapter` 已迁移到 Runtime，保留各自的参数和事件映射函数。
3. Gemini 专用 stream-json 映射已完成（`init/message/result` + tool 别名归一）。
4. 默认启用用户消息降噪（可通过开关恢复）。
5. 已新增 `claude-adapter` 与 `claude driver`（默认关闭，模板映射已接入 Runtime）。
6. 已验证 Claude API 模式可用：`ANTHROPIC_API_KEY/ANTHROPIC_AUTH_TOKEN + ANTHROPIC_BASE_URL` 可直接驱动 `claude` CLI 并通过 bridge 完成 run。
