# SYNC-0：基线同步批 v0.84.3 → v0.85.1 总计划

**状态**：计划定稿（2026-09-10）后执行中：S1 ✅ 完成（见 docs/handoffs/SYNC-S1-v0.85.1.md）；**S2 ❌ 取消（2026-09-10 用户决策：harness 上半身冻结，决策归档 docs/handoffs/SYNC-S2-v0.85.1.md）**，同步工作改道为 S3 → S4 → S5。本文件保留总纲 + 排除区裁决 + 现行 S3 定义。
**基线**：pig pin 于 pi v0.84.3（commit 4e58f324f）。同步目标 **v0.85.1**（2026-09-05 Release，commit d981de122）。规格书目录：`~/Projects/latest_pi/pi`（工作区已停在 v0.85.1）。
**注意**：pi 仓库里的 v0.9.x tag 是老版本号方案，**v0.85.1 就是最新正式版**，勿被 tag 排序误导。

## 排除区（不迁移，偏差逐条归档到收尾清单）

| 排除区 | 体量 | 理由与处置 |
|---|---|---|
| `packages/chord`（新包） | +10.8k | 应用组合运行时（facets/services/远程边界），服务 experimental/远程场景。但 3 个非 experimental agent 文件 type-only 引它：`agent-harness.ts` 的 `JsonRepresentation`、`context.ts` 的 `Context/ContextKey`、`session/types.ts` 的 `JsonValue` → pig 用少量本地类型等价物替代，注释声明 |
| `agent/src/harness/runtime/` 全套 | src +7.5k、test ~10k | 新多进程编排子系统（lane/structural/drive 族），消费者仅 agent index 再导出 + experimental。**连带决策**：① pi 把 `reducer.ts` 挪进 runtime → pig 保持 `agent/harness/reducer/` 原位不动；② `agent-harness.ts` 改为 re-export `runtime/harness.ts` → pig 不跟随，注释声明；③ ~~hooks.ts + execution/ S2 再确认~~ **已确认（2026-09-10，SYNC-S2-v0.85.1.md）**：hooks.ts（533 行）实现消费者仅 runtime/{drive,harness,lane,types}.ts，execution/（449 行）唯一 src 消费者是 hooks.ts 的 type-only `Gate` → **正式划入排除区** |
| `agent/src/harness/agent-harness.ts` v0.85.1 门面 + `events.ts` v0.85.1 重写 | +976/-65 | **（2026-09-10 追加，SYNC-S2-v0.85.1.md）** v0.85.1 的 agent-harness.ts 只剩纯类型门面（实现在排除区 runtime/），events.ts 的 watch/emitBatch 机槴消费者也仅 runtime/。pig 不迁移此门面：`agent/harness/agentharness/` 保持 S1 时的最小适配面（不可用脚手架），原 v0.84.3 时代的事件总线 `agent/harness/events.go` 冻结保留。解冻条件：pi harness 收敛（WP08 完成 / Storage format 4 定稿 / watchSession 实现，证据链见 `packages/agent/docs/harness.md` 评审，归档于 SYNC-S2-v0.85.1.md）或 pig 实际产生消费方 |
| `coding-agent/src/experimental/` + protocol/client/server/session-backends 增量 | ~+7k/-9k | RPC/SDK/sqlite = Phase 5 延后区 |
| 散项 | 小 | cloudflare provider（pig 无此 provider）、anthropic-messages adapter 变更 +164/-66（pig 无此 adapter，随将来 adapter 批）、evals/benchmark/docs、lock 文件 |

## 批次总览（每批 ≤8k 行 TS，源码+测试合计）

| 批 | 内容 | 估量 | 依赖 |
|---|---|---|---|
| **S1** | session/jsonl 重写 + 会话线格式核对（硬约束） | ≈8k | 无，最先做 |
| ~~**S2**~~ | ~~agent harness 杂项收尾~~ **❌ 取消（2026-09-10，harness 上半身冻结）**：主体（agent-harness 门面/events/hooks/execution）划入排除区；有价值的残余拆入 S3（ai 增量 + agent 根）与冻结区（env/output-capture/tools 增量、telemetry schema、jsonl 迁移余量测试，见下） | — | — |
| **S3（下一批）** | print 路保真小批：ai 增量（openai-completions +types +models）+ agent 根小改（agent-loop/proxy/types） | ≈2k | S1 |
| **S4** | TUI 增量（鼠标支持/alt-screen/搜索/滚动条等 21 文件 +2.2k） | ≈6k | ✅ **完成**（feat/s4 并行开发，已合流 master 6b91ec6，-race 全绿；见 docs/handoffs/S4.md） |
| **S5** | coding-agent core + interactive（工具渲染器拆分 renderers/* ~1.1k、agent-session ±127、interactive-mode ±296、theme-json +146、回归测试族） | ≈6k | S3 + S4 |

**冻结区（随 harness 解冻再议，不占同步批次）**：env/nodejs exec 重写（OutputCapture/AdaptivePublisher/spill ±368）、utils/{output-capture +238, adaptive-publisher +87, shell-output ±239}、tools/* 渲染增量（±314）、types.ts 的 Shell* 家族、telemetry.ts schema 更新（±80）、skills/prompt-templates ctx 穿透（±86）、messages.ts `fromId: string\|null`（±4）、config.ts（+35）、jsonl-v3-migration.test.ts 余量 ~30 用例（session 层，helpers 已就绪，可随 S5 或单独小批处理）。print/TUI 两条路对以上均零依赖（messages/compaction/tools 的既有实现已够用）。

S5 若超 8k 拆两半：S5a = 工具渲染器 + agent-session + core；S5b = interactive-mode + 组件 + 回归测试。
每批收口跑全仓 `go test ./... -race` 回归；S1 额外要求双向兼容实测（已完成，4 门全过）。

## ✅ 已完成归档：S1 定义（session/jsonl 重写 + 会话线格式核对）

> 以下为 S1 的开工定义，历史档案；S1 已交付（见 `SYNC-S1-v0.85.1.md`）。现行开工定义见本文件 §S3。

### 当时要读的 pi 源文件（规格书，均在 `~/Projects/latest_pi/pi/packages/agent/src/harness/` 下）

- `session/jsonl/repo.ts`（+317/-196）与 `session/jsonl/storage.ts`（+309/-210）：读写层重构主体
- `session/jsonl/codec.ts`（重构，-269 级）与 `session/jsonl/types.ts`（+60）、`session/jsonl/errors.ts`（-27）、`session/jsonl/index.ts`
- `session/jsonl/legacy-v3.ts`（**新增 539**）：v1→v3 迁移的新归属，pig 的迁移逻辑要对齐
- `session/types.ts`（+493/-285）：会话类型面变更，**线格式核心**
- `session/session.ts`（+385/-209）、`session/memory.ts`（+403/-131）、`session/state.ts`（+344）、`session/in-memory-storage-state.ts`（**新增 264**）
- `session/testing/conformance.ts`（1016）+ `session/testing/conformance/storage.ts`（920）+ `session/testing/conformance/session-repo.ts`（846）：conformance 套件被搬进 src，pig 对齐其验收面

### 要移植的 pi 测试文件（`packages/agent/test/harness/` 下）

- `jsonl-v3-migration.test.ts`（**新增 2013**，S1 最大的单文件）
- `session/jsonl.test.ts`（766）、`session/jsonl-storage.test.ts`（497）、`session/jsonl-codec.test.ts`（182）
- coding-agent 侧：`test/session-manager/load-entries.test.ts`（新增 182）

### 预期产出（Go 目标路径）

- `agent/harness/session/`（types/memory/session/state 增量）与 `agent/harness/session/jsonl/`（repo/storage/codec/errors/types 重构 + legacy_v3.go 新增）
- codingagent 侧 `core/session_manager.go` 消费面适配
- 会话格式若有 schema 变化：迁移逻辑进 pig 的 jsonl 层，**不做静默不兼容**

### 收口门（S1 特有，硬约束）

1. 移植测试全绿 + 全仓 `go test ./... -race` 回归
2. **双向兼容实测**：pig 新写的会话文件用 pi v0.85.1 能读；pi v0.85.1 产生的会话 pig 能读（含 v1/v3 旧文件迁移路径）
3. 用真实 `~/.pig/agent/sessions` 冒烟一次（S1 动会话存储，勿拖到 S5）

## 下一批 = S3：print 路保真小批（2026-09-10 新定义）

> **✅ 已完成（2026-09-10）**，本节转为历史档案；完成报告与下一批 S5a 定义见 `docs/handoffs/S3.md`。

S1/S2 决策后重排的第一批：只动 print/TUI 两条路共同依赖的 ai 流式面与 agent 根，不碰 harness。体量 ≈2k 行。

### 要读的 pi 源文件（规格书）

- `packages/ai/src/api/openai-completions.ts`（+62：`appendOpenAIReasoningDetail` 增量合并——OpenRouter 把 reasoning_details 流成 delta，相邻 text/summary 合并为逻辑条目、encrypted 保持离散；`applyStreamedReasoningDetails` 把 details 缓存到 thinking_end/收尾时才序列化进 thinkingSignature；compat 新增 `vllmPriority` → 请求体顶层 `priority` 字段）
- `packages/ai/src/types.ts`（±42：`AssistantMessage.providerThinkingLevel?`、`OpenAICompletionsCompat.vllmPriority?`、`OpenAIResponsesCompat.supportsMaxOutputTokens?`、`AnthropicMessagesCompat.supportsMidConvoEffort?`、`JsonValue` 成员序重排（无运行时差）、SimpleStreamOptions/StreamFunction/事件协议注释收敛）
- `packages/ai/src/models.ts`（±19：新增 `streamDeferred`（stream 形态的 fetchDeferred），`fetchDeferred` 改为其 `.result()` 薄包装）
- `packages/ai/src/utils/retry.ts`（±3：retry sleep 被 abort 时返回值必须删除 errorMessage 键，不仅是 undefined）
- `packages/ai/src/providers/faux.ts`（±6：可选字段 undefined 时省键）
- `packages/agent/src/agent-loop.ts`（±61：prepareNextTurn 从 turn_end 后移到下一轮开始前；准备期间补拉 steering（仅首轮轮空时）；executeToolCallsParallel 提交前 signal.aborted 检查 → 直接落 error 工具结果）
- `packages/agent/src/types.ts`（±7：`AgentTool.replay?: "never"\|"safe"` 字段；prepareNextTurn/shouldStopAfterTurn 文档对调）
- `packages/agent/src/proxy.ts`（±52：SSE 终止事件检测——干净 EOF 无 done/error 时补发 error 事件（Connection closed by proxy server…）；最后事件可能无换行 → flush decoder + 残余 buffer；done/error 透传 `providerThinkingLevel`）
- `packages/agent/src/index.ts`（±21：再导出面对齐）

### 要移植的 pi 测试

- `packages/agent/test/agent-loop.test.ts`（+3）与 `packages/agent/test/proxy.test.ts`（+50）
- openai-completions 的 reasoning-details 合并用例若在 `packages/ai/test/` 有专项（搜 reasoning_details delta），随文件移植；否则 pig 侧补 pig-覆盖

### 预期产出（Go 目标路径）

- `ai/api/openai_completions*.go` 增量、`ai/types/` + `ai/index.go` 增量、`ai/models.go` streamDeferred、`ai/utils/retry.go` aborted 语义、`ai/providers/faux.go`
- `agent/agent_loop.go`、`agent/types.go`、`agent/proxy.go`、`agent/index.go`

### 明确不做（S3 内声明）

- `packages/ai/src/utils/assistant-message-frame.ts`（+490）：消费者为排除区 runtime/ + agent-harness 门面 + S1 的 pending-frames 值地址；MessageUpdateEvent.frame 在 pig 无对应物，等消费方出现再移植
- `packages/ai/src/utils/node-http-proxy.ts` 增量：pig 从未移植该文件（Node 专属 proxy env 处理），不随小批补
- openrouter 双 api（anthropic-messages）：pig 无该 adapter，随将来 adapter 批
- `packages/ai/src/api/anthropic-messages.ts`、cloudflare-*：既有排除区

## 风险与坑

- pi 的 `session/testing/` 在 src 里（不是 test/），pig 移植时注意归属：conformance 是验收基建，pig 放测试侧
- `in-memory-storage-state.ts` 是新概念：内存存储与文件存储共享状态层，pig 的 InMemory 后端要跟着重构，别只改 File 后端
- `reducer.ts` 在 pi 已删除（挪进 runtime）——pig 保留原位是**声明过的偏差**，勿当遗漏"补"成 runtime
- chord 的 type-only 引用出现在 S1 就会撞到的 `session/types.ts`（`JsonValue`）——开工时先落本地等价类型

## 明确不要读的

`packages/agent/src/harness/runtime/`、`packages/agent/src/harness/{hooks,execution}`（已裁决划入排除区）、`packages/agent/src/harness/agent-harness.ts`（门面，不迁移）、`packages/chord/`、`packages/{protocol,client,server,session-backends}/`、`packages/coding-agent/src/experimental/`、pi 的 `docs/`（mobile-handoff 等与移植无关）。

## 收尾动作（S5 完成后）

AGENTS.md 重新 pin v0.85.1 + 偏差清单归档（不跟进清单：chord/runtime/experimental/RPC 系、**agent-harness 门面 + hooks/execution（2026-09-10 冻结，见 docs/handoffs/SYNC-S2-v0.85.1.md）**、anthropic-messages adapter、cloudflare provider，各附一句理由）。
