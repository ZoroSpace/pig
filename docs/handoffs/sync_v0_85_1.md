# SYNC-0：基线同步批 v0.84.3 → v0.85.1 总计划

**状态**：计划已定稿（2026-09-10），未开工。本文件是同步批的总纲 + 第一批（S1）的开工定义。
**基线**：pig pin 于 pi v0.84.3（commit 4e58f324f）。同步目标 **v0.85.1**（2026-09-05 Release，commit d981de122）。规格书目录：`~/Projects/latest_pi/pi`（工作区已停在 v0.85.1）。
**注意**：pi 仓库里的 v0.9.x tag 是老版本号方案，**v0.85.1 就是最新正式版**，勿被 tag 排序误导。

## 排除区（不迁移，偏差逐条归档到收尾清单）

| 排除区 | 体量 | 理由与处置 |
|---|---|---|
| `packages/chord`（新包） | +10.8k | 应用组合运行时（facets/services/远程边界），服务 experimental/远程场景。但 3 个非 experimental agent 文件 type-only 引它：`agent-harness.ts` 的 `JsonRepresentation`、`context.ts` 的 `Context/ContextKey`、`session/types.ts` 的 `JsonValue` → pig 用少量本地类型等价物替代，注释声明 |
| `agent/src/harness/runtime/` 全套 | src +7.5k、test ~10k | 新多进程编排子系统（lane/structural/drive 族），消费者仅 agent index 再导出 + experimental。**连带决策**：① pi 把 `reducer.ts` 挪进 runtime → pig 保持 `agent/harness/reducer/` 原位不动；② `agent-harness.ts` 改为 re-export `runtime/harness.ts` → pig 不跟随，注释声明；③ 新增 `hooks.ts` +533 与 `execution/` +449 → **S2 开工第一件事确认其消费者**，若仅 runtime 用则正式划入排除区 |
| `coding-agent/src/experimental/` + protocol/client/server/session-backends 增量 | ~+7k/-9k | RPC/SDK/sqlite = Phase 5 延后区 |
| 散项 | 小 | cloudflare provider（pig 无此 provider）、anthropic-messages adapter 变更 +164/-66（pig 无此 adapter，随将来 adapter 批）、evals/benchmark/docs、lock 文件 |

## 批次总览（每批 ≤8k 行 TS，源码+测试合计）

| 批 | 内容 | 估量 | 依赖 |
|---|---|---|---|
| **S1** | session/jsonl 重写 + 会话线格式核对（硬约束） | ≈8k | 无，最先做 |
| **S2** | agent harness 杂项收尾（agent-harness/events/env/types/tools/utils/skills/telemetry/result）+ ai 小增量（openai-completions +55/-7 等） | ≈6k | S1 |
| **S4** | TUI 增量（tui-alt-screen +452、alt-screen-search +219、tui.ts +207、tui-main-screen +119、editor +115、mouse-region 鼠标支持等） | ≈6k | S1（无硬依赖，可并行） |
| **S5** | coding-agent core + interactive（工具渲染器拆分 renderers/* ~1.1k、agent-session ±127、interactive-mode ±296、theme-json +146、回归测试族） | ≈6k | S2 + S4 |

S5 若超 8k 拆两半：S5a = 工具渲染器 + agent-session + core；S5b = interactive-mode + 组件 + 回归测试。
每批收口跑全仓 `go test ./... -race` 回归；S1 额外要求双向兼容实测（见下）。

## 下一批 = S1：session/jsonl 重写 + 会话线格式核对

### 要读的 pi 源文件（规格书，均在 `~/Projects/latest_pi/pi/packages/agent/src/harness/` 下）

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

## 风险与坑

- pi 的 `session/testing/` 在 src 里（不是 test/），pig 移植时注意归属：conformance 是验收基建，pig 放测试侧
- `in-memory-storage-state.ts` 是新概念：内存存储与文件存储共享状态层，pig 的 InMemory 后端要跟着重构，别只改 File 后端
- `reducer.ts` 在 pi 已删除（挪进 runtime）——pig 保留原位是**声明过的偏差**，勿当遗漏"补"成 runtime
- chord 的 type-only 引用出现在 S1 就会撞到的 `session/types.ts`（`JsonValue`）——开工时先落本地等价类型

## 明确不要读的

`packages/agent/src/harness/runtime/`、`packages/agent/src/harness/{hooks,execution}`（S2 再确认）、`packages/chord/`、`packages/{protocol,client,server,session-backends}/`、`packages/coding-agent/src/experimental/`、pi 的 `docs/`（mobile-handoff 等与移植无关）。

## 收尾动作（S5 完成后）

AGENTS.md 重新 pin v0.85.1 + 偏差清单归档（不跟进清单：chord/runtime/experimental/RPC 系、anthropic-messages adapter、cloudflare provider，各附一句理由）。
