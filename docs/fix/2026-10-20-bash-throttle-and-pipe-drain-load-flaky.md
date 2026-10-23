# 全量 -race 下 bash 节流测试 flaky + 进程管道尾丢失（负载下两连挂）

日期：2026-10-20（状态：已修，全量回归 -race 全绿）
涉及文件：`codingagent/core/tools/bash.go`、`codingagent/core/tools/tools_test.go`、`agent/harness/env/nodejs.go`、`agent/harness/env/pipe_has_data_unix.go`、`agent/harness/env/pipe_has_data_windows.go`（新增）

同一轮 `go test ./... -race` 满负载下暴露两处独立问题：一个 flaky 测试断言、一个真实数据丢失竞态。两者根因同为「墙钟/负载敏感」，但机制不同，分别修复。

## 一、`TestBashTool/should_coalesce_streaming_updates_for_chatty_output`（codingagent/core/tools）

**现象**：`update count = 69`（断言 `< 25`），子测试耗时 7.46s。单跑/轻载必过，全量 -race + 满负载必挂（复现：4 个 -race 大包并行时 34 次更新）。

**根因**：测试是**依赖墙钟的 flaky 断言**，不是节流器 bug。`shellUpdateThrottle` 与 pi 的 `scheduleOutputUpdate` 语义逐行等价（100ms 窗口内突发合并且至多每 100ms 发一次）。但 5000 次 `OnData` 同步循环在 -race + CPU 饱和下要跑 ~7s，节流器按墙钟每 100ms 发一次 → 69 次。断言 `<25` 隐含「循环须在 2.5s 内跑完」，负载下一碰就碎。累加器本身在隔离 -race 下仅 ~97ms，非瓶颈。

**修复**：给 `shellUpdateThrottle` 加时钟/定时器测试缝（`shellThrottleNow` / `shellThrottleAfterFunc` 包级 var，默认 `time.Now`/`time.AfterFunc`，与既有 `writePreviewClock` 同模式）。测试冻结假时钟 + 捕获（不真发）定时器，5000 次突发坍缩到同一瞬间 → 立即 1 次 + 收尾 1 次，输出完整性照验，与负载彻底解耦。另补 `TestShellUpdateThrottleCoalescesBursts` 单测，用假时钟锁死「突发→1 立即+1 待发、窗口内仍合并且、边界立即发」三拍语义。

## 二、`TestBashCoalescesUpdatesAndPersistsTruncatedFullOutput`（agent/harness/tools）

**现象**：全量 -race 下 `truncation = &{Content:line-988...}`（3000 行只捕获 ~2987）；更重负载下只捕到 586 行。单跑必过（0.08s），负载下约 1/3 概率挂。

**根因**：**真实数据丢失竞态**（非测试问题）。`processWaiter` 在子进程退出后起 100ms 宽限定时器；定时器到时无条件 `finalize`（`wait()` 返回），`Exec` 随即关读端、按已累加的 `stdoutAcc` 建结果。负载下 stdout 读 goroutine 被 CPU 饿死，尚未排空管道缓冲就触发定时器 → 关读端 → 缓冲尾部整段丢失。pi 此处更稳：其 `waitForChildProcess` 的 idle 定时器回调先查 `spillIsDraining()`，仍在排空就**重新起定时器**而非 finalize（`packages/durable/src/env/node.ts` armIdleTimer）。pig 移植丢了这层检查。

**修复**：按 pi 语义回填。`processWaiter` 增加 `drainActive func() bool`（对应 pi 的 `spillIsDraining` 参数），`armIdleTimerLocked` 定时器回调改为：仍在排空 → 重新起定时器，空闲 → finalize。`Exec` 侧 wire `drainActive = pipeHasData(stdoutRead) || pipeHasData(stderrRead)`。`pipeHasData` 用 `unix.Poll` 零超时非阻塞探测管道是否有 POLLIN 数据（不扰动同 fd 上阻塞读的 goroutine），unix 实现 + Windows 回退（匿名管道无可移植 peek，维持原行为并注释）。孙进程挂住空管道（无数据）→ poll 无 POLLIN → 100ms 后照常 finalize，不挂死。

## 验证

- 回归测试函数：`TestBashTool/should_coalesce_streaming_updates_for_chatty_output`（改确定性）、`TestShellUpdateThrottleCoalescesBursts`（新增单测）、`TestBashCoalescesUpdatesAndPersistsTruncatedFullOutput`（原测试即回归锁）。
- 红→绿：修复前在 4 个 -race 大包并行负载下分别复现 34 次更新 / 2987 行丢失（红）；修复后同一负载 8/8 通过（绿）。单测在假时钟下确定性验证。
- 边界：孙进程挂 stdout（`echo hello; sleep 5 &`）0.10s 内正常返回，不挂死，`hello` 完整捕获。
- 全量 `go test ./... -race -count=1 -timeout 600s` rc=0，33 包全绿；`GOOS=windows go build` 通过。
