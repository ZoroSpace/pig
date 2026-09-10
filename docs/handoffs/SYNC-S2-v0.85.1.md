# SYNC-S2：取消记录 + harness 冻结决策（基线同步批第 2 批）

**状态**：❌ 取消（2026-09-10，用户决策）。S2 未产出代码：开工当日完成消费者确认后即改道，已写半成品整体回退至 `3de631c`（S1 收口态，回退后 build + agent/telemetry 测试全绿）。取代 `SYNC-S1-v0.85.1.md` 第 2 节的 S2 定义；现行下一批 = **S3**，定义见 `sync_v0_85_1.md` §S3。（**补充**：S4 已与本决策并行完成——另一 agent 在 `~/Projects/S4/pig` 开发 feat/s4，仅动 `tui/`，已合流 master（6b91ec6）并 force-push；批次序中的 S4 就此出清。）
**基线**：不变（pig pin v0.84.3 → 对齐 v0.85.1，规格书 `~/Projects/latest_pi/pi`）。

## 1. 决策：harness 暂不迁移，先把 TUI 路跑通（用户结论）

核心判断：**pig 的 Go 重写暂时不迁移 harness，先把正式 TUI 跑通**。等 harness 把 WP08 做完、Storage format 4 定稿、watchSession 实现、contract 稳定后再回头迁移——那时 TUI 已在跑，加 harness 是叠加能力，不是从零开始。

### 1.1 决策理由（用户评审结论，四条）

**① harness 本身是 WIP，架构还在变。** pi 的 `packages/agent/docs/harness.md:158` 白纸黑字：*"Storage format 4 is still WIP (pre-stabilization): shapes may change in place without migrations; do not invent migration obligations for them."* 另有一批已知缺失与 contract debt（pi `packages/agent/docs/harness.md` 评审，行号为该文件实际行号）：

| 标记 | 状态 | 出处 |
|---|---|---|
| WP08 命名分支 / streaming forks | **in progress — Slice A**（JSONL/SQLite bounded transfer 未完） | harness.md:151, :1298 |
| R12 watchSession | 直接 `throw new SliceNotImplemented("watchSession")`，唯一 stub 方法 | harness.md:147, :1080 |
| C1 raw RemoteSession | 规格与已交付的 process-local 产品矛盾，implement-or-retire 未决策 | harness.md:146, :564 |
| SQLite branch divergence | 压缩有界算法在未压缩分支上可 O(history) 复制，目标未达成 | harness.md:152, :526 |
| H1 contract/test closure | public `OperationStatus` 声明了 `"running"` 但实测只产出 `"open"/"aborting"`；abort 信号顺序与 gate 收窄类型待清理 | harness.md:153, :155 |
| J1 / S3(search) / T1(telemetry) 等 | snapshot 重写、搜索 API、遥测 schema 对齐——全部未实现，汇总见 :1303 | harness.md:1303 |

（源码侧旁证，S2 开工当日核实：`agent-harness.ts:147` 确有 `OperationStatus = "running" | "open" | "aborting"`，`SliceNotImplemented` 从 runtime/types.ts re-export。）Go 重写一版，harness 下个版本又改 schema，等于从零再来。

> 注意：pi harness 文档里的工单号 T1/S3 与 pig 的同步批次号 T1/S3 无关，仅是撞名。

**② harness 与正式 TUI 是两条完全独立的产品线。**

```
正式 TUI  →  顶层 Agent 类     →  纯内存 loop + SessionManager(JSONL)
mini TUI  →  AgentHarness      →  持久化 Lane + Drive 状态机
```

正式 TUI 链路（`interactive-mode.ts` → `AgentSession` → `Agent`）没有一行代码依赖 harness：它的"持久化"就是 SessionManager 写 JSONL，"恢复"就是读回来塞回 `agent.state.messages`——简单粗暴但够用。pig 侧同构实证（S2 消费者确认，见 §2）：`agent/harness/agentharness` 在 pig 全仓的唯一引用是一条注释；print 模式与 interactive mode 消费的是 harness 下半身（compaction/messages/utils/tools/session），全部不在冻结范围。

**③ harness 复杂度极高，迁移 ROI 目前为负。** 内部依赖深度全仓最夸张（session 事务/values/fork/jsonl 全家、runtime drive 12 个 procedure + lane 1200+ 行、restore/transcript/progress、execution effect-gate、compaction、tools、utils）；核心抽象——`OperationState` 状态机、`LaneCommand` 三种 outcome、`Drive` 与 `activeDrive` 的 claims 竞争、intent/settlement 两阶段提交——全部为"崩溃恢复"这一个目标设计。TUI 先跑通的阶段，崩溃恢复不是刚需。全套搬过去的结果：一套还在演进的复杂架构 + TUI 依然没跑起来。

**④ 务实路线是两阶段。**

```
Phase 1（现在）: 跑通 TUI
├── 顶层 Agent 类（纯内存 agent loop）        ✅ pig 已交付（Phase 3）
├── SessionManager（JSONL 持久化）            ✅ pig 已交付（Phase 4 第 2 批）
├── coding-agent tools（bash/read/write/...） ✅ pig 已交付（Phase 4 第 1 批）
├── interactive-mode UI 层                    ✅ pig 已交付（T5/T6，M4 达成）
└── 跳过：harness 全套、extensions sidecar（Phase 7）
Phase 2（harness 稳定后）: 叠加 harness 能力
├── Session 层（事务、Entry Tree、Values）    ◑ S1 已完成 v0.85.1 对齐
├── Lane + Drive 状态机                       ⏸ 冻结区
├── Watch/Snapshot 事件流                     ⏸ 冻结区
└── experimental mini TUI 的 RPC 架构         ⏸ 随 Phase 5 再议
```

Phase 1 代码量少、边界清晰、测试好写。Agent 类是 harness 之前就有的**老架构**，且是正式 TUI 实际在用的那条路；harness 是给 mini TUI 准备的未来之路。

## 2. 决策内容（落档动作）

1. **harness「上半身」冻结**：`agentharness`（agent-harness.ts 门面）、`hooks.ts`、`execution/`、`runtime/`、events v0.85.1 重写全部划入排除区，**等 pi 架构稳定再议**（解冻参考条件：WP08 完成、Storage format 4 定稿、watchSession 实现、pi 把 runtime/ 转正，或 pig 实际产生消费方）。
2. **不加沉没成本**：当日已写的 S2 半成品（agentharness v0.85.1 门面重写 + events 总线 + messages/compaction/skills/prompt-templates/telemetry 小件 + telemetry InMemory 后端）不做收口修补，直接 `git reset --hard 3de631c` 丢弃。
3. **同步工作改道**：S3（print 路保真小批）→ S4（TUI 增量）→ S5（coding-agent），harness 剩余增量进冻结区（清单见 `sync_v0_85_1.md`）。

## 3. 消费者确认结论（开工当日实证，排除区裁决依据）

- **v0.85.1 把 AgentHarness 实现全量挪进了 `runtime/`**：`agent-harness.ts`（622 行）只剩纯类型门面（Result 别名族 / OperationRequest / LaneSnapshot / HookMap / HarnessEventPayload / AgentLane / AgentHarness 接口）+ `export { createAgentHarness } from "./runtime/harness.ts"`。原计划「agent-harness.ts 全量重写 +976 行」实为「实现在排除区长出了新面」——印证理由①（架构仍在搬家）。
- **`hooks.ts`（533 行）**：实现消费者仅 `runtime/{drive,harness,lane,types}.ts`；`agent-harness.ts`/`types.ts` 自带 HookMap 类型定义，不 import hooks。
- **`execution/`（449 行，assistant/effect-gate/tools）**：唯一 src 消费者是 `hooks.ts`（type-only `Gate`），其余在 runtime/。
- **`events.ts` v0.85.1 重写（watch/emitBatch/deliveryTail）**：唯一 src 消费者是 `runtime/harness.ts`；其上游测试 `events.test.ts` 被删除（覆盖移入排除区 `runtime/watch.test.ts`）。
- **print/TUI 两条路对以上全部零依赖**：pig 全仓对 `agent/harness/agentharness` 的唯一引用是 `codingagent/core/compaction.go` 的一条注释；core/compaction、core/tools、interactive 组件消费的是 harness 下半身（compaction/messages/utils/tools/session），这些不在冻结范围。

## 4. 给后续 session 的提醒

- 下一批从 `sync_v0_85_1.md` §S3 开工，**勿读** `agent/src/harness/{runtime,hooks,execution,agent-harness.ts}`（已在排除区清单）。
- S2 曾盘点过的冻结区内容（env exec 重写、output-capture 族、tools 渲染增量、telemetry schema、skills/prompt-templates ctx 穿透、messages `fromId: string|null`、config.ts、jsonl-v3-migration 余量 ~30 用例）在 `sync_v0_85_1.md` 冻结区清单里列明了 pi 文件与行数，解冻时按清单直接开工，无需重新盘点。
