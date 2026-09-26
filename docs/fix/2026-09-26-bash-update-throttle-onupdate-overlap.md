# bash 输出节流器 onUpdate 并发重叠（S8-4/S9 存量 flake 收口）

- **日期**: 2026-09-26 23:50（CI 首次暴露于 S8-4 批，S8-4/S9 两次以「隔离复跑绿，记录在案未修」挂账）
- **状态**: 已修复（存量 flake 就此收口）
- **涉及文件**: `agent/harness/tools/bash.go`、`agent/harness/tools/tools_test.go`、`agent/harness/tools/bash.go` 参照 `~/Projects/pi/packages/coding-agent/src/core/tools/bash.ts`

## 现象

CI 全量 `-race` 偶发（S8-4、S9 各一次，本地难以复现）：

```
WARNING: DATA RACE
Read  ... TestBashCoalescesUpdatesAndPersistsTruncatedFullOutput.func1() tools_test.go:836
       ← (*bashUpdateThrottle).emitOutputUpdate() bash.go:128
       ← CreateBashTool.func1() bash.go:260            ← Execute 主 goroutine（最终 emit）
Previous write ... 同一回调闭包
       ← emitOutputUpdate() bash.go:128
       ← (*bashUpdateThrottle).scheduleOutputUpdate.func1() bash.go:181  ← time.AfterFunc goroutine
```

## 根因

`emitOutputUpdate` 在锁内取 `latest()` 快照后**解锁再调 `onUpdate`**。该回调有三个触发点：

1. `OnChunk` → `scheduleOutputUpdate`（delay ≤ 0 时直接 emit，chunk/Execute goroutine）
2. `time.AfterFunc` 定时器回调（bash.go:181）
3. 命令收尾的最终 emit（Execute goroutine，bash.go:260）

点 2/3 并发时两个 goroutine 同时进入消费者的 `onUpdate` 回调 → 测试回调裸 `append(updates, ...)` 被 race detector 逮住。生产面同理：agent 把 onUpdate 转发为工具更新事件，消费者从不该假设并发调用。

**pi 为什么没有这个 bug**：pi 所有 emit 点（直接调用 + setTimeout 回调）都跑在单线程事件循环上，`onUpdate` 结构性不可能与自身重叠；pig 的 timer goroutine 是 setTimeout 等价物，必须自行补上这条串行化语义。

## 修复

`agent/harness/tools/bash.go` 的 `bashUpdateThrottle`：

1. **锁跨回调持有**：`emitOutputUpdate` 拆出 `emitLocked`（调用方持锁），回调在 `t.mu` 内调用——三个 emit 点串行化，对齐 pi 事件循环语义。无死锁风险：throttle 是 Execute 局部结构，消费者回调不可能回调进 throttle（RunExclusive/lock 本就可重入，双保险）。
2. **closed 门**：`clearUpdateTimer` 置 `closed = true`，定时器回调在锁内检查后再 emit。pi 的 `clearTimeout` 语义是「已排定的回调永不再跑」，而 `time.AfterFunc` 无法撤销已到期的回调（Stop 对已触发定时器返回 false），必须用门补齐——否则存在 Execute 返回后定时器补发一帧、测试读 `updates` 与之竞写的残余窗口。原声明未用的 `timeCleared` 死字段更名为 `closed` 承接。

## 回归验证

- **新增 `TestBashUpdateThrottleNeverOverlapsOnUpdate`**（tools_test.go）：直接驱动 throttle——定时路 emission 进入 `onUpdate`（sleep 50ms 拉宽窗口，原子计数追踪最大并发）后，主路 `emitOutputUpdate` 直入；断言最大并发 == 1。**语义断言，不依赖 race detector**。
  - 红（stash 修复）：`onUpdate overlapped itself: max concurrency = 2, want 1`
  - 绿（恢复修复）：×10 全绿
- 存量 `TestBashCoalescesUpdatesAndPersistsTruncatedFullOutput` 保留为 e2e pin。
- 全量 `go test ./... -race -count=1 -timeout 600s`：agent/harness/tools 绿（全仓日志 `/tmp/pig-test-out-20260926-throttle-fix.log`；该轮 interactive 包有一个与本修复无关的独立 race，见 `2026-09-26-provider-refresh-goroutine-mutates-ui.md`）。

## 教训

pi 靠事件循环免费获得的串行化语义（回调不与自身并发、clearTimeout 后回调不跑），pig 移植成 goroutine 后都必须显式补齐；JS 里「看起来只是状态翻转」的闭包，在 Go 里往往是一把锁加一个完成门。
