# PANIC-RESILIENCE：panic 记录 + 不退出（用户插入批，2026-09-11）

- **日期**：2026-09-11（晚）
- **状态**：✅ 完成，全仓 `go test ./... -race` 全绿
- **动机**：dogfooding 中 pig 一旦 panic（如 `docs/fix/2026-09-11-latex-escaped-byte-rune-index-panic.md` 的 CJK 行内公式渲染 panic）进程直接死、什么都没留下——用户既丢会话也没法报 bug。需求：① panic 落盘成日志；② 交互模式不被 panic 杀掉。
- **涉及文件**：`utils/panic_log.go`（新建）、`utils/panic_log_test.go`（新建）、`tui/tui.go`、`tui/terminal.go`、`tui/loader.go`、`tui/editor.go`、`tui/tui_alt_screen.go`、`tui/tui_panic_test.go`（新建）、`codingagent/core/agent_session.go`、`codingagent/core/agent_session_panic_test.go`（新建）、`codingagent/modes/interactive/interactive_mode.go`、`codingagent/modes/interactive/components/{countdown_timer,model_selector,session_selector}.go`、`codingagent/core/tools/{renderers_bash,renderers_edit}.go`、`cmd/pig/main.go`

## 与 pi 的关系

**pig 并发面新增，pi 无对应物**（pi 是单线程 JS，未捕获 throw 崩进程；Go 的 panic 模型不同，需要显式收容）。与 `TuiBase.RunExclusive`/`mutateUI`/`LoginDialog.Mutator` 同属 pig 并发面先例序列。既有 pi 对齐物保持不动：渲染超宽行的 `pi-tui-crash.log` dump（tui_main_screen.go，pi parity）原样保留。

## 设计：三层收容

### 第 1 层：panic 记录（`utils/panic_log.go`，顶层叶子包）

- `LogPanic(component, r) path`：写报告到 `$PIG_CODING_AGENT_DIR/logs/panic-<ts>.log`（回落 `~/.pig/agent/logs/`，再回落 os.TempDir；env 解析镜像 config.go，与 tui.go 同一同步义务）。内容：组件名/时间/pid/Go 版本/module version + vcs revision/panic 值与类型/当前 goroutine 栈/全 goroutine dump（pprof goroutine profile——收容后的死锁上下文）。
- 保留最新 20 份，超出按 mtime 剪枝（多进程并发剪枝无害）。
- `RecoverPanic(component)`：defer 用的吞掉式收容（日志落盘后继续）；`Guard(component, fn) (path, panicked)`：同步分派点用，需要向调用方回传"发生了 panic + 日志路径"。
- 记录本身 best-effort：写失败返回空路径，绝不二次 panic。

### 第 2 层：热路径收容（交互模式不退出）

| 收容点 | 组件名 | 说明 |
|---|---|---|
| `TuiBase.lock()` 改 defer 释放 | — | **关键前置**：锁内 panic 原本会把 `b.mu` + `lockOwner` 永久锁死（比崩溃更糟的全局死锁）；defer 化后 panic 干净 unwind，锁状态恢复 |
| 渲染 goroutine ×2（immediate render / renderTimer） | `tui.immediateRender` / `tui.renderTimer` | `doRenderOrPanic` 的 panic 被收容，下次 RequestRender 重试（pi 语义：timer 回调里 throw 崩进程） |
| stdin readLoop 的 `ProcessString` | `tui.stdin` | 按键/粘贴分派（editor、focus、keybinding handler）panic 不再杀 readLoop |
| loader 动画 ticker / editor autocomplete 续体 / alt-screen 选中自动滚动 | `tui.loader` / `tui.editor.autocomplete` / `tui.altScreen.autoScroll` | 长寿命 ticker/worker goroutine |
| **`AgentSession.emit()` 逐 listener 收容** | `core.agentSession.listener` | **用户内容 panic 的实际入口**（latex 案例：message_end → handleEvent → rebuildChat → markdown 渲染）。一个 listener panic 被收容，事件泵与其它 listener 存活 |
| `SetListenerPanicObserver` 钩子 | — | emit 收容后回调（本身也 Guard），interactive 注册为 `mutateUI(ShowError("Internal error (recovered) — panic log: <path>"))`——用户在 UI 里直接看到日志路径 |
| interactive 全部后台 goroutine（catalog 刷新/tmux 检查/submit worker/信号/fork/setModel/模型刷新/resume/logout/provider 刷新/compact/shutdown×4/SIGCONT/input gate/install report） | `interactive.*` | 一次性动作 goroutine 全部 defer 收容；shutdown 一行式收拢为 `spawnShutdown(fatal)` 辅助方法 |
| 组件/渲染器：countdown timer、model selector 刷新、session selector 装载、bash elapsed ticker、edit 异步 diff preview | `components.*` / `tools.*` | 渲染用户文件内容的后台 goroutine |

### 第 2.5 层：abort-on-panic（用户插入小批，2026-09-11 深夜）

listener panic 除了收容 + 弹 Error 行，现在还会**中止当前 run**（方案 A，会话不 halt）：`subscribeToAgent` 的 panic observer 里 `go session.Abort(ctx 5s 超时)`，与手动 Esc 同语义——模型不再对着坏渲染器继续流式输出，会话本身存活、下一条消息照常。设计要点：

- **必须 `go` 起来调**：observer 在 agent 事件分发的同步链上（agent 等 awaited listener settle 才 idle），同步 Abort → WaitForIdle 会死锁；`abortInFlight`（atomic.Bool CAS）把 panic 爆发合并成一次 abort，abort 结算后复位，后续 run 的 panic 可再触发。
- **abort 先于 ShowError 派发**，mutateUI 队列再慢也不拖延中止。
- 未做：会话级 halt 标志（方案 B）、渲染熔断（方案 C）、panic 摘要注入下轮 context——均为独立候选批。

回归测试：`TestSubscribeToAgentAbortsRunOnListenerPanic` / `TestSubscribeToAgentSessionStaysUsableAfterPanicAbort`（`codingagent/modes/interactive/interactive_mode_panic_abort_test.go`，真实流式 run + panicking listener；stash 验证：移除 go Abort → 两测超时 FAIL；同步化 Abort → 同样死锁超时 FAIL）。

### 第 3 层：进程兜底（`cmd/pig/main.go`）

`main()` 用 `Guard("main")` 包住整个 `Main()`：主 goroutine panic（Init/shutdown/print 路径）→ `core.RestoreStdout()`（终端回 sane 模式，不留烂屏）→ stderr 提示 panic log 路径 → exit 1。print 模式无法"继续"（输出契约已破），但日志已落盘。

## 已知边界（文档化）

- **fatal error 不可收容**：Go 的 concurrent map writes / OOM / 栈溢出不可 recover，进程仍会死——这类只能靠 `-race` 测试与代码纪律预防。
- **收容后半更新状态**：panic 所在单元（如半渲染帧、半更新容器）可能不一致；日志是诊断真相源。选择"带病运行"优于"全丢"。
- 锁纪律：所有收容点确认不持非 defer 释放的锁（emit 在锁外调 listener；lock() defer 化；readLoop 的 Guard 在自身锁外）。

## 回归测试与验证

| 测试 | 文件 | 验证方式 |
|---|---|---|
| `TestTuiLockReleasesMutexOnPanic` / `TestTuiLockNestedReleaseOnPanic` | `tui/tui_panic_test.go` | stash 验证：lock() 回退非 defer 释放 → 测试 2s 死锁 FAIL；恢复 → PASS |
| `TestEmitContainsListenerPanic` / `TestEmitSurvivesMultiplePanickingListeners` | `codingagent/core/agent_session_panic_test.go` | stash 验证：emit 收容回退 → 测试 panic FAIL；恢复 → PASS |
| `TestLogPanicWritesReport` / `TestLogPanicPrunesOldReports` / `TestGuardContainsPanic` / `TestRecoverPanicSwallows` / `TestPanicLogDirectoryFallback` | `utils/panic_log_test.go` | utils 自身为本批新建，直接锁定（写入/剪枝/Guard/回退目录） |

全仓验收：`go build && go vet && gofmt -l` 干净 + `go test ./... -race` 全绿。
