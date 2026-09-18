# 2026-09-18 `/reload` 命令漏接线，输入后原文发给模型

- **日期**：2026-09-18（发现于 SLASH-COMMANDS-COMPLETION 批盘点，随该批修复；文件 mtime 为准）
- **状态**：已修复收口
- **涉及文件**：`codingagent/modes/interactive/interactive_mode.go`（processSubmitText submit switch、isDeferredInteractiveCommand）、`codingagent/modes/interactive/interactive_mode_commands_test.go`（回归测试）

## 现象

交互模式输入 `/reload` 不触发重载，而是**作为普通消息原样发给模型**（模型收到 "/reload" 文本并回复）。autocomplete 里有 `/reload` 提示（core/slash_commands.go 的 BUILTIN_SLASH_COMMANDS 全量 22 条），用户可正常选中提交，所以必现。

## 根因

pi 的 onSubmit（interactive-mode.ts setupEditorSubmitHandler）有 24 个显式命令分支，其中 `/reload` 分支调用 handleReloadCommand。pig 移植时的 submit switch 漏了这个 case；2026-09-16 收窄 deferred 拦截清单（docs/fix/2026-09-16-clipboard-path-eaten-by-slash-command.md）时，`isDeferredInteractiveCommand` 从 pi 的 24 个分支抄了 10 个进拦截清单（export/import/share/copy/session/changelog/hotkeys/clone/debug + 两彩蛋），**`/reload` 两头都没接**：

- 不在 submit switch → 无 handler 执行；
- 不在 isDeferredInteractiveCommand 清单 → 无拦截警告；

于是 `/reload` 穿过全部命令分支落到 `session.prompt`，AgentSession 解析不到对应 extension command，按普通文本提交给模型。讽刺的是 handler 本体早已存在——T5b-3 交付的 `handleReloadCommand`（extension `pi.reload()` 的 command context 也绑定着它），只差用户输入侧这一条分支。

## 修复

submit switch 补 `case text == "/reload":`（清空编辑器 → `m.handleReloadCommand(ctx)`，与 pi 的 `editor.setText(""); await this.handleReloadCommand();` 逐字对齐）。同批顺带把 deferred 清单里其余 8 个「handler 底层已就绪」的命令全部接线（/import /copy /session /changelog /hotkeys /clone /debug + 彩蛋），deferred 清单收窄到外部依赖卡住的 /export /share 两项——见 docs/feat/SLASH-COMMANDS-COMPLETION.md。

## 回归测试

- `TestReloadCommandWhileStreamingShowsWaitWarning`（`codingagent/modes/interactive/interactive_mode_commands_test.go`）：构造真实 streaming 态的 seam 会话（可控 streamFn 阻塞），提交 `/reload`，断言 ① 不落到输入门（未发给模型）② 渲染出 "Wait for the current response to finish before reloading." 警告（流式中 guard 提前返回，避开 stub UI 上跑完整 reload）。
- **stash 验证**：临时摘除 `/reload` case 后该测试 FAIL（无警告、走漏），恢复后 PASS——红→绿确认。
- `TestIsDeferredInteractiveCommandBoundaries` 同步把 `/reload` 从「无分支」态改为显式 no 断言（已接线命令永不进 deferred 清单）。
