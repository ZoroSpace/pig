# 2026-10-02 showAuthSelect 挂载/恢复不触发渲染——/login 认证方式选择器画面冻结

- **日期**：2026-10-02（修复落盘时刻，见文件 mtime）
- **状态**：✅ 已修复收口（S11 收尾批 tmux 冒烟中发现，当批修复）
- **涉及文件**：
  - `codingagent/modes/interactive/interactive_mode.go`（showAuthSelect：挂载 selector 与 restoreDialog 两处补 `m.ui.RequestRender(false)`）
  - `codingagent/modes/interactive/interactive_mode_login_test.go`（回归 `TestShowAuthSelectRequestsRender` + `focusRecordingTUI` stub）

## 现象

tmux 真 TTY 冒烟 `/login` → Anthropic：选完 provider 后画面永久停在只有标题的 "Login to Anthropic" 对话框上，认证方式选择（"Browser login (default)" / "Copy code login (headless)"）永不出现；fullscreen 与 regular 两模式一致复现。SIGQUIT goroutine dump 实证：login goroutine 正常走到 `showAuthSelect` 的 `<-resultCh` 等待，且 `mutateUI`（挂载 selector + SetFocus）已完成——**组件树是对的，画面是旧的**。

## 根因

移植掉线。pi `interactive-mode.ts` 的 `showAuthSelect` 在两处调 `this.ui.requestRender()`：① 挂载 selector 之后；② `restoreDialog` 内部。pig 移植时两处都丢了。pig 的事件驱动渲染管线里，无动画 tick、无输入时组件树变更不自动重绘——于是空闲 TUI 永不重画，屏幕保留旧帧（对话框），而焦点已经在看不见的 selector 上，按键实际打进 selector（行为与画面脱节，比单纯"没画出来"更糟）。

对话框自身的 `showAuth`/`showPrompt`/`showManualInput` 等方法都有 `requestRender`，所以后续 prompt 类型（manual_code 等）不受影响——只有 editorContainer 级换装的两处丢渲染。

## 修法

`showAuthSelect` 两处补 `m.ui.RequestRender(false)`（与 pi 逐点对齐：挂载后一次、restoreDialog 内一次）。

## 回归测试

- `TestShowAuthSelectRequestsRender`（`interactive_mode_login_test.go`）：`focusRecordingTUI`（计数 RequestRender + 记录 SetFocus）驱动真实 `showAuthSelect`——① 挂载后 selector 必须已聚焦且渲染请求计数 > 0（修复前恒 0，红）；② 对聚焦 selector 发 `\x1b` 取消，restore 后焦点回对话框且计数再增（restoreDialog 的渲染请求一并锁定）。
- **stash 验证**：还原两处 RequestRender 后单跑 FAIL（"mounted the selector without requesting a render"），修复后 PASS。

## 附注

- 定位手段存档：`sample` 对 Go 进程拿不到 goroutine 栈（macOS 采样器解不开 Go 帧）；SIGQUIT dump 可用但 `pgrep -f` 首个命中是 tmux/zsh 包装进程，必须经 `tmux display-message #{pane_pid}` 再取其子进程才是 pig 本体（否则"dump 无输出"是假象——第一回 kill 的是 tmux 包装进程）。
