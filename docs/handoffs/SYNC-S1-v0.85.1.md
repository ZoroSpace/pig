# S1：session/jsonl 重写 + 会话线格式核对（基线同步批第 1 批）

**状态**：✅ 完成（2026-09-10）。取代 `sync_v0_85_1.md` 中 S1 的开工定义；~~下一批 = S2（本文件第 2 节）~~ **S2 已取消（2026-09-10 用户决策：harness 上半身冻结），本文件第 2 节的 S2 定义作废**；现行定义见 `docs/handoffs/sync_v0_85_1.md` §S3，决策归档见 `docs/handoffs/SYNC-S2-v0.85.1.md`。
**基线**：pig pin pi v0.84.3 → 对齐 **v0.85.1**（d981de122）。规格书 `~/Projects/latest_pi/pi`。

## 1. 本批完成

### 1.1 会话层全量重写（v0.85.1 架构）

新建/重写（`agent/harness/session/`）：

| 文件 | 行数 | 内容 |
|---|---|---|
| `types.go` | ~700 | v0.85.1 四类 Entry（message/compaction/branch_summary/custom，**model_change/thinking_level_change/active_tools_change/LaneRecord 全族删除**，仅存于 legacy-v3 迁移）、Value/List 写、Storage/Session/Branch/SessionRepo 接口、13 叶 OperationState 族 |
| `values.go` | ~300 | 存储地址（Value/ValueList）、value 写、`ResolveListReadOptions`（limit 1000/上限 10000）、全部保留地址 helper（branchTip/laneConfig/pi.op.*/pi.pending.*/…） |
| `commit.go` | ~230 | CommittedWrite 六类（**wire 带 kind/op**，EntryWrite MarshalJSON 扁平化 entry+kind）、PrepareStorageCommit/ValidateCommittedWrites |
| `mutation_line.go` | ~90 | sync.Cond 串行线；**Seal 返回 drained channel**（对应 pi 的 promise tail） |
| `fork.go` + `fork_policy.go` | ~380 | CreateForkSnapshot/ForkSnapshotWrites/ClassifyForkAddress（copy/exclude/reconstruct 策略） |
| `in_memory_storage_state.go` | ~380 | Memory/Jsonl 共享物化状态 |
| `session.go` | ~700 | StorageBackedSession、Branch 视图、mutation barrier、5 个会话错误类型、`TypedMetadata()` 逃逸口 |
| `memory.go` | ~700 | MemoryStorage（FIFO commitGate）+ MemorySessionRepo + Facade（admit/inFlight 语义对齐 pi） |
| `context.go` | ~120 | v0.85.1 版 buildSessionContext（**model/thinking/tools 状态提取已删除**，只剩消息投影 + EntryProjector） |
| `jsonl/{types,codec,legacy_v3,storage,repo}.go` | ~1900 | format-4 存储：header 解析（v4+v3 双识别）、事务逐行/torn-tail 修复、**legacy-v3 懒升级**（首次 commit 原地重写+补零值 usage adjustment）、repo 生命周期（cwd 目录编码/原子发布/pendingCreates 并发去重/fork 快照） |

前置件：`agent/harness/context.go`（chord Context 等价物：GetTelemetryContext/WithTelemetryContext）、`agent/harness/utils/usage.go`（EmptyUsage/AddUsage）、`ai/utils/uuid.go` 加 `UUIDv7WithTime`。

### 1.2 消费面适配

- **`agent/harness/compaction/{compaction,branch_summarization}.go`**：移植 v0.85.1 版。`CollectEntriesForBranchSummary` 改签名 `(BranchEntryReader, SessionEntryReader, oldTipID, targetID, ctx)`（pi 的 `Pick<Branch,"findEntries">` + `Pick<Session,"getEntry">`，窄接口定义在 compaction 包）。
- **`agent/harness/agentharness`**：最小适配（完整 v0.85.1 重写属 S2）——`SessionTree()`→`Session() session.Session`、`GetLeafID`→main branch tip、restore 检测 FindRecords→FindEntries limit 1。
- **`agent/harness/reducer`**：**冻结为自包含 legacy 包**——`legacy_types.go`（v0.84.3 record 词汇表逐字冻结，取自 b4a0efe），`session.` 前缀去除。声明过的偏差：pi 把 reducer 挪进排除的 runtime/，pig 保留原位。
- **`agent/search`**：收缩为 v0.85.1 的 `index.ts`（上游删除了 scanning.ts，pig 的 `scanning.go` 一并删除，无消费者）。

### 1.3 SessionRepo 泛型接口（本批关键决策，用户确认）

`SessionRepo[TMetadata, TCreate, TList any]` 泛型接口 1:1 对应 pi 泛型：memory 实例化 `[SessionMetadata, SessionCreateOptions, any]`、jsonl 实例化 `[JsonlSessionMetadata, JsonlSessionCreateOptions, *JsonlSessionListOptions]`，编译期接口断言固定在两包内。中间曾用 `any`+运行时断言，经用户质疑后重构为泛型方案。`Session` 接口保持非泛型（级联成本）；具体元数据经 `(*StorageBackedSession).TypedMetadata() any` 逃逸。

### 1.4 测试（992 函数全仓 -race 全绿）

- **conformance 双套件**（pig 放 test 侧 `session/testing` 包，pi 在 src）：`storage.ts` 21 用例 + `session-repo.ts` 13 用例（lifecycle/ownership/messages/forks/fork coordination），Memory/Jsonl 双后端各跑一遍；适配器 `ConformanceRepo[TMetadata]`（pi 的 Pick + 工厂注入 cwd 模式）。
- **专项移植**：values（4）、mutation-line（3）、session-context（6，含 projector 错误传播）、storage-backed-session（~17）、branch（create-branch 相关并入）、branch-summarization（2）、memory（3）、jsonl-storage（11：快照发布/持久化/快照边界/torn-tail 7 例）、jsonl-repo（8：cwd 过滤/原子发布/显式 mutation/版本拒绝/fork 占用/并发 create/跨 cwd 同 id）、**legacy-v3 迁移 13 用例**。
- **未移植**（跟随 S2，helpers 全就绪）：`jsonl-v3-migration.test.ts` 剩余 ~30 用例（forking legacy、升级失败原子性、配置重建的边界变体等）；`types.test.ts`（纯 expectTypeOf，无 Go 对应物）；coding-agent `load-entries.test.ts`（属 S5 的 coding-agent session-manager 面）。

### 1.5 双向兼容实测（S1 收口门，4 项全过）

| 门 | 验证 |
|---|---|
| pig harness jsonl 读 pi 真实 v3 | 133 个真实会话（`~/.pi/agent/sessions`）List/Open/entries/usage 聚合/branch tip 全通，源文件零改动 |
| pig 写 v4 → pi 读 | pi 全局安装的 `pi-agent-core/dist` 真实代码 open+scanEntries+getValue+scanUsage+scanBranch+getStats 全对 |
| pi 写 v4 → pig 读 | 同上反向全对（node 脚本用 pi dist 写，pig `OpenJsonlStorage` 读） |
| pig core/session_manager(v3) ↔ pi | pig 发现 133 个 pi 会话并解析 729 entries；全局 pi CLI `--continue` 打开 pig 写的 v3 会话并输出 pig 的 assistant 消息 |

**关键澄清**：pi 日常会话（coding-agent/core/session-manager）**仍是 v3 格式**（v0.85.1 新建会话亦然）——harness jsonl 的 v4 是 runtime/experimental 层格式，`legacy-v3.ts` 的意义是 harness 可导入 coding-agent 历史文件。两层格式互不影响，S5 处理 coding-agent 会话时勿混淆。

## 2. 下一批 = S2（❌ 已作废，2026-09-10）

> 本节保留仅供历史追溯，勿按此开工。S2 开工当日即改道：v0.85.1 实证 agent-harness 实现已全量迁入排除区 runtime/，hooks/execution 仅被 runtime 消费 → harness 上半身整体冻结划入排除区；当日半成品回退至 3de631c。消费者确认结论与新批次序见 `docs/handoffs/SYNC-S2-v0.85.1.md`，现行下一批 = S3（见 `docs/handoffs/sync_v0_85_1.md`）。

### 原定义（作废）

### 要读的 pi 源文件（`~/Projects/latest_pi/pi/packages/agent/src/harness/`）

- `agent-harness.ts`（**v0.85.1 全量重写 +976 行**：AgentLane 全新方法面 getResult/accept/drive/requestAbort/inspectExecution、AgentHarness.lane/lanes、hooks.ts 契合）——本批主体
- `hooks.ts`（新增 +533）与 `execution/`（+449）——**开工第一件事确认消费者**：若仅被排除的 runtime/ 使用则正式划入排除区（handoff `sync_v0_85_1.md` 既定动作）
- `compaction/compaction.ts` 余量（±261 中的非 session 面）、`utils/`（truncate/shell_output 增量）、`events.ts`、`types.ts`（WatchHandle/LaneSnapshot 族）
- `env/nodejs.ts` 增量、`skills.ts`、`system-prompt.ts`、`telemetry.ts`、`result.ts` 的 diff（`git diff 4e58f324f..v0.85.1 -- <path>`）
- `ai` 侧：`packages/ai` 的 openai-completions 增量（+55/-7）与其余 provider/类型面小改

### 要移植的 pi 测试

- `test/harness/{agent-harness*,hooks*,execution*,events*,telemetry*,types*,skills*,system-prompt*}.test.ts`（按实际 diff 取舍）
- `jsonl-v3-migration.test.ts` 剩余用例（helpers 已在 `jsonl/legacy_v3_test.go`）
- `test/harness/tools.test.ts` 的 +340 行增量

### 预期产出

- `agent/harness/agentharness/` 全量重写（替代 S1 的最小适配面）
- `agent/harness/{hooks,execution,events,types,skills,telemetry,result}` 增量
- reducer 冻结维持不动

### 依赖

- S1 的 `session.Session`（新接口）与 `session/testing` conformance 基建直接被 S2 消费
- agentharness 现挂 `legacy_types.go`（reducer）——S2 若动 OperationKind 别名注意指向

## 3. 风险与坑

- **agentharness 的 v0.85.1 面完全不同**（lane/accept/drive 族）——S1 只做了编译级最小适配，S2 重写时 `Session()/GetTip()/GetLeafID()` 等过渡面全部推倒，勿当既成事实保留。
- **`hooks.ts`/`execution/` 消费者确认**是 S2 第一动作（sync handoff 既定）；若划入排除区，agent-harness.ts 重写时相应方法保持 unavailable 面。
- **MemorySessionFacade 的 admit 语义**：pi 在 facade mutate 回调内部复查 open 状态（close 与排队回调竞态的仲裁点）——pig 已按此实现，改并发结构时勿丢。
- **FIFO commitGate**：存储层用 channel（cap 1）而非 mutex 保证提交排序（Go mutex 不保 FIFO），conformance 的 serialization/fork-coordination 用例依赖此；改锁策略会挂测试。
- **CommittedWrite 的 wire 带 `kind`/`op`**（entry 扁平化 + kind 成员）：torn-tail/legacy 升级回放按此解析，改动即破坏 pig↔pi v4 互读（已有 4 门实测锁住）。
- **`ListReadOptions.Limit` > 2^53 拒绝**（pi 的 Number.isSafeInteger）：conformance 锁定。
- pig 写 v4 时 **branch tip 值形状是 `string|nil`**（fork.go `derefOrNil`），pi 端按 `value: string|null` 断言——勿存 `*string`。

## 4. 明确不要读的

`packages/agent/src/harness/runtime/`（排除区，reducer 冻结已处理）、`packages/chord/`、`packages/coding-agent/src/experimental/`、`packages/{protocol,client,server,session-backends}/`、pi 的 `docs/`、`packages/ai/src/api/` 非 openai-completions 的 adapter 目录。

## 5. 给人类的建议

- S1 实际体量 ≈9.5k 行（session 重写 4.2k + jsonl 2.9k + 测试 4.5k − 删除旧件），超出 handoff 预估的 8k，主要因为 v0.85.1 会话层是结构性重设计而非增量。S2 若同样超限，按 agent-harness 主体 / hooks+execution 确认后的余量拆两半。
- 收尾清单（sync handoff 既定，S5 后执行）：AGENTS.md 重 pin + 不跟进清单（chord/runtime/experimental/RPC、anthropic-messages、cloudflare provider）。
