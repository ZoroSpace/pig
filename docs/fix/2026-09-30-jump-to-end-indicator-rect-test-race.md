# 2026-09-30 jump-to-end indicator rect 测试裸读数据竞态（CI flake）

- **日期**：2026-09-30（GitHub Actions CI `TestTuiAltScreenKeepsTheJumpToEndIndicatorCenteredAsTheAutoScrollbarHidesAndReappears` 假红，run 36671106041）
- **状态**：已修复收口（测试基建 bug，生产代码无缺陷）
- **涉及文件**：`tui/tui_alt_screen.go`（新 `ScrollToEndIndicatorRect()` 访问器）、`tui/tui_alt_screen_new_test.go`（改走访问器）

## 现象

CI 全量 `-race` 中该测试两种失败形态同时出现：

1. `revealed: indicator rect = <nil>, want column 24`——`assertColumn` 的 viewport 检查（屏幕上确实有 `↓` 且列位正确）先通过，紧随其后的 rect 检查却读到 nil；
2. `WARNING: DATA RACE`：测试 goroutine 在 `tui_alt_screen_new_test.go:464` 裸读 `tui.scrollToEndIndicatorRect`，与渲染 goroutine 在 `tui_alt_screen.go:2370` 的写入并发。

## 根因

`scrollToEndIndicatorRect` 由渲染 goroutine 在 `TuiBase.lock()` 持锁下维护，但每次渲染帧的开头（`compositeScrollToEndIndicator` 首行）先重置为 nil、帧中段才重新赋值。测试侧 `VirtualTerminal.WaitForRender()` 只是 35ms sleep，无任何同步，随后直接裸读该字段：

- **竞态**：读与写分属两个 goroutine 且无 happens-before，`-race` 必报；
- **transient nil**：读恰好落在「本帧已重置 nil、尚未重新赋值」的窗口时，上一帧画面仍留在终端上（viewport 检查照常通过），rect 却是 nil——本地跑窗口极窄难复现，CI 慢机 + `-race` 下渲染 goroutine 与测试 goroutine 交叠概率放大。

生产代码不受影响：唯一消费点 `handleScrollToEndIndicatorMouseEvent` 在输入分发路径上，本就在锁内（`tui.go:1525` 的 `b.lock`）。

## 修复

按包内既有访问器先例（`HasActiveSelection`/`ViewportTop` 的 `t.lock` 包裹 + 可重入注释）新增 `TuiAltScreen.ScrollToEndIndicatorRect()`：持锁读取并返回结构体副本（nil 透传），测试 `assertColumn` 改走访问器。锁语义同时消掉两个问题：与渲染 goroutine 互斥消竞态；不可能观察到帧中段的 transient nil。

## 验证

- 单测 `-race -count=5` 通过；
- 全量 `go test ./... -race -count=1 -timeout 600s` 单次 rc=0，30 包全绿（日志 `/tmp/pig-test-out-fix.log`）；
- `go vet` / `gofmt` 干净。
