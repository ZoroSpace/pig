# post-login 模型目录刷新 goroutine 裸改 UI 容器（生产级 race）

- **日期**: 2026-09-26 23:50（2026-09-26 全量回归首抓）
- **状态**: 已修复
- **涉及文件**: `codingagent/modes/interactive/interactive_mode.go`、`codingagent/modes/interactive/interactive_mode_login_test.go`

## 现象

全量 `go test ./... -race` 在 `TestPostLoginDiscoveryBoundsRefreshTo15Seconds` 报 DATA RACE（隔离复跑 PASS 的假绿 flake）：

- **读**（测试主 goroutine）：`renderAllChildren(m.chatContainer)`（interactive_mode_status_test.go:46）
- **写**（refresh goroutine）：`completeProviderAuthentication` 内 `go func()` → `ShowWarning` → `tui.(*Container).AddChild`

## 根因

`completeProviderAuthentication` 尾部的模型目录刷新 goroutine（interactive_mode.go:7665 起）在 `modelRuntimeRefreshFn` 返回后直接调 `m.ShowWarning(...)`（裸 `chatContainer.AddChild`）、`m.updateAvailableProviderCount()`、`m.footer.Invalidate()`——**没有走 `m.mutateUI`**。生产面上 TUI 渲染循环并发读容器树，同样是 race。

pi 无此问题面：这段异步收尾在 TS 里是单线程事件循环上的 continuation，与渲染结构性串行。pig 的 goroutine 是它的等价物，但 T5b-3 立下的「跨 goroutine UI 突变必须经 mutateUI（RunExclusive）」纪律漏盖了这条 2026-09-21（S6-3c 7027 测试批）新增的路——该 goroutine 在彼批从 showStatus 内联逻辑改为独立 `go func()` 时带出了裸调用。

**测试侧的同步纪律同时失效**：Bounds 测试的 `waitFor(counter.requests > baseline)` 里 baseline 在 `completeProviderAuthentication` 之前取，而 `showStatus("...Refreshing model catalog…")`（同步段）已经 bump 了计数器 → waitFor 立即放行 → `renderAllChildren` 与 goroutine 的 AddChild 竞写。同族另两个测试（Selects / Reports）的 baseline 虽然取在 goroutine parked 之后，但 `finishAuthentication` 内 `showStatus` 的内部 RequestRender 会在尾部未完成时提前放行 waitFor——同族窟窿一并修。

## 修复

1. **生产**（interactive_mode.go）：goroutine 尾部全部 UI 突变（两个 ShowWarning 分支、`deferSelection` 判定内的 `finishAuthentication()`、`updateAvailableProviderCount`、`footer.Invalidate`）包进 `m.mutateUI(func(){...})`，与渲染循环串行化（模式与文件内既有 30 处一致）。最终 `m.ui.RequestRender(false)` 保持在块外，作为「goroutine 完成」信号供既有测试纪律消费。`finishAuthentication`/`updateAvailableProviderCount` 内无嵌套 mutateUI，无死锁面（真实 `TuiBase.lock` 本就可重入）。
2. **测试基建**：`countingRenderTUI` 增加 `RunExclusive`（普通 mutex），使测试环境里 `m.mutateUI` 真正串行化（此前 `stubRequestRenderTUI`/`countingRenderTUI` 都不实现该接口，`mutateUI` 在测试里直通）。goroutine 块内无嵌套 mutateUI，不可重入 mutex 安全（文件头注释声明与真实 TuiBase 的差异）。
3. **三个测试收尾**改为「mutateUI 内轮询渲染直至目标内容出现」（Selects 轮询 "Credentials saved"、Reports 轮询 "no models are available"、Bounds 轮询 "timed out; using cached models"）——不再依赖计数器时序，渲染与 goroutine 突变串行，内容断言也不再受提前放行影响。Preserves 测试尾部无中途 RequestRender、纪律本自洽，不动。

## 回归验证

- **红**（stash 生产修复、保留测试改动）：`go test ./codingagent/modes/interactive/ -race -count=5 -run 'TestPostLoginDiscoveryBoundsRefreshTo15Seconds'` → 5 轮 14 份 `WARNING: DATA RACE` + FAIL（轮询渲染反复与 AddChild 相撞，红是高概率确定性）。
- **绿**（恢复修复）：`TestPostLoginDiscovery` 家族 `-count=3 -race` 全绿；全量 `go test ./... -race -count=1 -timeout 600s` 见 `/tmp/pig-test-out-20260926-interactive-fix.log`。
- 本批改动与本文件所述 bug 无关的失败（bash 节流器 flake）另见 `2026-09-26-bash-update-throttle-onupdate-overlap.md`。
