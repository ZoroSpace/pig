# /resume 之后 footer 模型名冻结（会话替换未重绑 footer）

- **日期**: 2026-09-29 23:37
- **状态**: 已修复
- **涉及文件**: `codingagent/modes/interactive/components/footer.go`、`codingagent/modes/interactive/interactive_mode.go`、`codingagent/modes/interactive/interactive_mode_rebind_footer_test.go`（新增）

## 现象

用户报：`/resume` 之后，切换模型（`/model`）实际生效了（请求发到新模型、状态行也显示 "Model: …"），但 footer 右下角的模型名一直停在旧值（例：切到 deepseek，仍显示 `glm-5.3-flash`）。另确认：新建会话、重启都不触发，只有 `/resume` 之后会。

真机复现（tmux + mock 端点）：会话 A 用 `deepseek-v4-flash` → `/new` → `/resume` 回到 A → `/model` 选 `deepseek-v4-flash`。修复前 footer 恒显旧值，而 mock 请求日志证明请求已走新模型。

## 根因（两个缺口叠加）

**缺口 1：`rebindCurrentSession` 漏了 pi 的 `applyRuntimeSettings()` 调用。**

pi `interactive-mode.ts:2020`：

```ts
private async rebindCurrentSession(options = {}): Promise<void> {
    const session = this.session;
    this.unsubscribe?.();
    this.applyRuntimeSettings();   // ← pi 的第一步；footer.setSession 在这里
    ...
```

pig 的 `rebindCurrentSession` 端口时漏了这一步，而 `applyRuntimeSettings` 是唯一调用 `footer.SetSession` 的地方（另一 caller 是 `/reload`）。于是会话替换（`/resume`、`/new`、`/fork`、`/import`）后，footer 从未被指向新会话。

**缺口 2：`FooterComponent.SetSession` 对渲染是空操作。**

pi 的 footer 存 `this.session`，`render` 直接读 `this.session.state`，所以 `setSession` 换字段即完成重绑。pig 为测试 seam 把读取封装成 `footerSessionView`（构造器里建一次），`Render` 走 `view.state()`：

```go
func (f *FooterComponent) SetSession(session *core.AgentSession) {
    f.session = session   // Render 根本不读这个字段
}
```

所以即便调了 `SetSession`，视图仍指向旧会话。

两者叠加 → 会话替换后 footer 永久读旧会话的 model / token 统计 / context window / cwd。因为 `/model` 改的是**新**会话的 agent state，footer 读的是**旧**会话，看起来就是"切换不成功"。

## 处置

1. `FooterComponent.SetSession` 同步重建 view：

```go
f.session = session
if session != nil {
    f.view = newFooterSessionView(session)
}
```

（注释声明：pi 读 `this.session` 不需要这一步，这是 pig 的 seam 偏差；同时提醒 `NewFooterComponentWithView` 的 stub 视图调用者不要再传真实 session。）

2. `rebindCurrentSession` 补回 `m.applyRuntimeSettings()`（pi 的位置：unsubscribe 之后、renderBeforeBind 之前）。它同时修好会话替换后 cwd / 终端能力 / 编辑器设置的重读（`footerDataProviderView.SetCwd` 等），不只模型名。

## 回归测试

`codingagent/modes/interactive/interactive_mode_rebind_footer_test.go`：

- `TestRebindCurrentSessionRebindsFooterAfterSessionReplacement`——两个会话（model-a / model-b）+ 真实 footer，把 runtime 换成 session B 后调 `rebindCurrentSession(false)`，断言 footer 渲染出 `model-b` 且不含 `model-a`。该测试同时锁住两个缺口（缺任何一个都失败）。
- 新增测试 TUI stub `rebindTestTUI`（补 `SetShowHardwareCursor` / `SetClearOnShrink` / `Terminal`，`applyRuntimeSettings` 与 `updateTerminalTitle` 需要）。

验证：把两处修复一起回退（`SetSession` 去掉 view 重建 + 删掉 `applyRuntimeSettings()` 调用）后跑测试 →

```
--- FAIL: TestRebindCurrentSessionRebindsFooterAfterSessionReplacement
    footer after rebind = "... model-a", want model-b (stale view keeps the old session)
```

恢复后全绿；全量 `go test ./... -race` 单次 rc=0（30 包）。

## 真机验证（tmux + mock 端点）

修复后同一流程：会话 A `deepseek-v4-flash` → `/new`（footer 立即显示新会话默认模型 `doubao-seed-evolving`）→ `/resume` 回 A（footer 显示 `deepseek-v4-flash`）→ `/model` 切 `glm-5-3-flash`（footer 显示 `glm-5-3-flash`，请求日志同步为 `glm-5-3-flash`）。

## 边界

- `/resume`、`/new`、`/fork`、`/import` 都会替换会话，全部走同一重绑路径，一并修好。
- 这是端口偏差（pi 无此 bug），不是 2026-09-29 两个 fix 批引入的（CPU 阈值 / settings 空格均不触及此路径）。
