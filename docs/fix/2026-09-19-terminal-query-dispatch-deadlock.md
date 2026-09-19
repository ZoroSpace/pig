# 2026-09-19 设置页 Automatic Theme 的 Apply 卡死（TUI 输入分发 × 终端查询双死锁）

- **日期**：2026-09-19 16:14（修复落盘时间，取自文件 mtime）
- **状态**：已修复收口（全量 `go test ./... -race -count=1` 28 包 rc=0；tmux 真 TTY 冒烟 Enter/Space 双路径全通）
- **涉及文件**：
  - `tui/tui.go`（修复：`pendingOsc11BackgroundQuery.settled` → `atomic.Bool` CAS；`OnTerminalColorSchemeChange`/`removeTerminalColorSchemeListener` 改走可重入 `b.lock`）
  - `tui/terminal_query_deadlock_test.go`（新增回归测试 3 个）
- **报告人**：用户（dogfooding，/settings → Theme → Automatic → Apply 按回车或空格必现卡死）

## 现象

交互模式 `/settings` → Theme → Automatic 进入自动主题页，选好 Light/Dark theme 后在 **Apply** 上按 Enter 或 Space，整个 TUI 永久冻结（按键无响应，只能杀进程）。单主题页选主题、Apply 之外的其它项（Light/Dark theme 子菜单、Change mode）均正常。

## 根因（两处叠加死锁，pig-only 并发面；pi 无此问题）

pi 的 `theme-controller.applyFromSettings` 是 async（`interactive-mode.ts` 里 `void themeController.setThemeSetting(...)` fire-and-forget），await 终端查询 Promise 期间事件循环继续处理 stdin——终端回复能被消费。pig 的调用链是**同步**的，且整条链运行在**持有 `b.mu` 的输入分发路径**上（`handleTerminalInput` → `b.lock` → 组件 `HandleInput` → settings 回调链）：

```
输入 goroutine（持有 b.mu）
  → SettingsList.activateItem → ThemeSubmenu.apply → onDone
  → 外层 SettingsList onChange("theme") → OnThemeChange
  → themeController.SetThemeSetting("light/dark") → ApplyFromSettings（auto 分支）
  → DetectTerminalThemeForAuto
    ① QueryTerminalColorScheme(100ms)
       → OnTerminalColorSchemeChange(settle) → b.mu.Lock() ← 死锁点 A：自锁
    ② QueryTerminalBackgroundColor(100ms)（① 修掉后才会走到）
       → < -query.resolve 阻塞（仍持 b.mu）
       → 100ms 超时 timer 触发 → b.mu.Lock() ← 死锁点 B：timer 等不到锁
  终端真实回复到达 → 读取 goroutine → handleTerminalInput → b.mu.Lock() ← 同样卡死
```

- **死锁点 A（用户实际撞到的第一处）**：`OnTerminalColorSchemeChange` / `removeTerminalColorSchemeListener` 用 `b.mu.Lock()` 直接加锁，不经可重入的 `b.lock()`。从 dispatch 内调用（`QueryTerminalColorScheme` 第一行注册 settle listener）即自死锁。
- **死锁点 B**：`QueryTerminalBackgroundColor` 的超时 timer 在 timer goroutine 里 `b.mu.Lock()` 后才写 resolve；查询方持有 b.mu 等 resolve → 循环等待。终端不支持 DSR 996 / OSC 11 时靠超时兜底，而超时路径自身死锁。

只有 Automatic 页的 Apply 走 `ParseAutoThemeSetting("light/dark") != nil` 的检测分支；单主题 apply、预览、Change mode 都不触发终端查询，故只有该入口必现。

## 修复

1. `pendingOsc11BackgroundQuery.settled` 改 `atomic.Bool`：超时 timer 与回复消费路径（consume，持 b.mu）都以 CAS 抢 settle，timer 不再碰 `b.mu`。
2. `OnTerminalColorSchemeChange` / `removeTerminalColorSchemeListener` 改走 `b.lock(func(){...})`（可重入：dispatch 内联执行；外部 goroutine 正常加锁），与同文件 `SetTerminalColorSchemeNotifications`/`SetFocus` 一致。
3. `QueryTerminalBackgroundColor` doc comment 声明 dispatch-safe 契约：从 dispatch 内调用时**有界停顿 ≤timeoutMs**（终端回复稍后由后续 dispatch 消费，settle 过后为无害 no-op）。修复后 Apply 最坏 ~200ms 停顿（DSR 100ms + OSC 11 100ms），与 pi 在终端不支持 996/11 协议时的 await 超时同量级；终端支持时 pi 更快（ms 级），属已声明行为差异，如需完全对齐可把 `OnThemeChange` 改为 goroutine fire-and-forget（需先给 theme controller 字段面补锁，未做，见「已知项」）。

## 回归测试（`tui/terminal_query_deadlock_test.go`，3 个函数）

- `TestTerminalQueryTimeoutSurvivesInputDispatch`：dispatch goroutine 持 b.mu 时组件 HandleInput 内同步发 OSC 11 查询，超时路径须能 settle（锁死锁点 B）。stash 验证：修复前 5s 守卫 FAIL（死锁），修复后 PASS（~50ms）。
- `TestColorSchemeQuerySurvivesInputDispatch`：同形态发 DSR 996 查询，listener 注册不得自锁（锁死锁点 A，即用户撞到的首杀点）。stash 验证：FAIL → PASS。
- `TestTerminalQueryRealReplyWinsOverTimeout`：真实回复与超时竞争时 consume 路径胜出并交付解析色（CAS 语义防过修）。

## 已知项与顺带修复

- **同型隐患未动**：`AddInputListener`/`RemoveInputListener` 仍用直接 `b.mu.Lock()`——当前无 dispatch 内调用路径，不顺手扩大改动面；将来若从 key handler 里注册 input listener 会撞同一颗雷。
- **顺手修复（同日同批，独立事件）**：`components/armin.go` 的 `ArminComponent` 动画 ticker goroutine 在锁外写 `currentGrid`/`effectState`（`tickEffect` + `gridVersion++`），与持锁的 `Render()` 数据竞争，全量 `-race` 逮到（`TestWiredSubmitCommandsReachTheirHandlers/dementedelves`）。修：tick 分支纳入 `a.mu`，`updateDisplay` 内联进循环（gridVersion 递增与网格写同锁）。09-18 SLASH-COMMANDS-COMPLETION 批遗留。
- **事故记录（流程，非代码）**：冒烟诊断时误用 `pgrep -x pig | head -1` 定位测试进程，SIGQUIT 打到了用户的日常 pig 会话（goroutine dump 到用户终端 + 会话终止）。教训入规：**对 tmux 测试实例操作必须用 `tmux list-panes -t <session> -F '#{pane_pid}'` 取精确 PID，禁止 `pgrep | head` 猜**。
