# 2026-09-24 并行工具批 fatal: concurrent map read and map write（pendingTools 跨 goroutine 裸奔）

- **日期**：2026-09-24 12:21（修复落盘时间）
- **状态**：已修复，回归测试锁定
- **涉及文件**：
  - `codingagent/modes/interactive/interactive_mode.go`（新增 `pendingToolsMu`，12 个访问点全部加锁）
  - `codingagent/modes/interactive/interactive_mode_pending_tools_race_test.go`（新增，回归测试）

## 现象

dogfooding 中模型（volcengine-plan glm-5-3-flash）正常对话运行时进程直接死亡：

```
fatal error: concurrent map read and map write
goroutine 1618 [running]:
internal/runtime/maps.fatal(...)
interactive.(*InteractiveMode).handleEvent(...) interactive_mode.go:2057
interactive.(*InteractiveMode).subscribeToAgent.func2(...) interactive_mode.go:1767
core.(*AgentSession).emit.func1() agent_session.go:1364
```

2057 行 = `tool_execution_update` 分支里 `m.pendingTools[event.ToolCallID]` 的**读**。这是 Go runtime 的 fatal error，panic-resilience 三层收容**接不住**（AGENTS.md PANIC-RESILIENCE 已声明的边界：fatal error 不可 recover），进程即死。

## 根因

**pi 单线程事件循环 vs pig 真 goroutine 的并发面差异，T5b 移植时未补锁。**

事件链 goroutine 归属（`agent/agent_loop.go`）：

- `ToolExecutionStart`/`ToolExecutionEnd` 事件全部从**主循环 goroutine** 发出（`executeToolCallsParallel` 的 for 循环里 emit start；结果收齐后 emit end）。
- `ToolExecutionUpdate` 事件从**工具自己的 goroutine** 发出——`executePreparedToolCall` 的 `onUpdate` 闭包在工具 `Execute` 内被调（bash 流式输出等），`agent_loop.go:797-799` 的注释自己就承认：*"settled tools' onUpdate may be invoked from another goroutine (pi relies on the single-threaded event loop here)"*。

于是本次 fatal 的确切形状（并行工具批，bash 多命令并行是日常触发器）：

> 主循环逐个收齐工具结果、emit `tool_execution_end`（**写**：`delete(m.pendingTools, id)` @2067）的同一时刻，另一个还没跑完的工具 goroutine 正在 emit `tool_execution_update`（**读**：`m.pendingTools[id]` @2057）。

`m.pendingTools` 自 T5b 交付起就是无锁裸 map；同文件的 `streamingComponent`/`streamingMessage` 早在 T5b 就有 `streamingMu` 先例，但 `pendingTools` 的访问点漏了——12 个访问点分布：

| 位置 | goroutine | 操作 |
|---|---|---|
| `AgentStart` 分支 | 主循环 | 换新 map |
| `message_update` 分支 | 主循环（streamingMu 内） | 读/写 |
| `message_end` 分支（×2 段） | 主循环（streamingMu 内） | 迭代/换新 |
| `tool_execution_start` | 主循环 | 读/写 |
| `tool_execution_update` | **工具 goroutine** | 读 ← crash 点 |
| `tool_execution_end` | 主循环 | 读/删 ← 对撞点 |
| `agent_end` 分支 | 主循环（**streamingMu 外**） | 换新 |
| `renderSessionItems`（开头+结尾） | 会话重建路径 | 换新/回填 |
| `renderCurrentSessionState` | 会话切换路径 | 换新 |

相邻的 `chatContainer.AddChild`（`tui.Container` 裸 slice append）**无并发窗口**：start/end 全在主循环，update 分支不 AddChild，message_update（流式期）与工具批（流式后）不同期——本次修复面据此收敛为 map 本身，不动容器。

## 修复

pig-only 最小修（与 `streamingMu` 先例同型）：

1. 新增 `pendingToolsMu sync.Mutex` 字段，注释声明锁序 `streamingMu → pendingToolsMu` 恒定不反向。
2. 12 个访问点全部入临界区；锁只护**字段读写与 map 操作**，组件方法（`UpdateResult`/`UpdateArgs`/`MarkExecutionStarted` 等）在锁外（组件自有 mutex，且真实流程中同组件 start→update 有因果序：工具 goroutine 在 emit start 同步返回后才启动，同组件永不并发）。
3. `tool_execution_start` 的组件构造+`AddChild` 留在临界区内（start 全主循环串行，无竞争者，临界区微秒级）。

## 回归测试

`interactive_mode_pending_tools_race_test.go` / `TestHandleEventPendingToolsConcurrentAccess`：

- 两个 goroutine 直接驱动 `handleEvent`：主循环替身对偶数 id 发 `tool_execution_start`+`end`（写/删 map），settled-tool goroutine 替身对奇数 id 发 `tool_execution_update`（读 map，组件恒 nil）。
- **偶/奇 id 分半是关键设计**：Go map 并发检测是 map 对象级的（不同 key 的 mapassign×mapaccess 同样报 race），map race 本体保留；同时避免两个 goroutine 打同一组件——组件不是 goroutine-safe 的（pi 单线程设计，真实流程同组件有因果序永不并发），测试若制造同组件重叠会误报测试构造出的假 race。
- **验证**：stash 修复（只撤 `interactive_mode.go`）→ `-race` FAIL（DATA RACE 精确落在 2051/2067 写 × 2057 读，与线上 fatal 同点）→ 恢复修复 → `-race -count=3` PASS。全量 `go test ./... -race -count=1 -timeout 600s` 29 包 rc=0（`/tmp/pig-test-out-pendingtools.log`）。

## 教训

- **移植 pi 单线程代码时，pig 的并发面审计要跟着「谁在哪个 goroutine emit」走**，不能只看函数内部。`agent_loop.go` 里那句 "may be invoked from another goroutine" 注释就是信号——消费端（interactive UI）没跟上。
- `streamingMu` 先例（T5b）覆盖了 `streamingComponent`，但同族共享状态 `pendingTools` 漏网——同类字段要成组审计。
- 宿主进程级 fatal（map/OOM）是 panic-resilience 的已知盲区，这类 bug 只能靠 `-race` 回归测试提前抓。
