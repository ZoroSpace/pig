# fix：`/` 开头的普通文本被当作斜杠命令吞掉（粘贴图片路径必触发）

- **日期**：2026-09-16 22:07
- **状态**：已修复收口，回归测试锁定
- **涉及文件**：`codingagent/modes/interactive/interactive_mode.go`（`processSubmitText` + 新增 `isDeferredInteractiveCommand`）、`codingagent/modes/interactive/interactive_mode_submit_test.go`（新增）

## 现象

用户在交互模式粘贴图片（Ctrl+V）后编辑器里是 macOS 临时文件路径（`/var/folders/.../T/pi-clipboard-*.png`），补一句「你好」回车，pig 弹出：

```
Warning: /var/folders/.../pi-clipboard-....png is not yet available (interactive selectors land in a later batch)
```

消息没有发给模型。同样输入 pi 正常回答。

## 根因

`processSubmitText`（pi `setupEditorSubmitHandler` onSubmit 的移植）里有一个兜底分支：

```go
case strings.HasPrefix(text, "/"):
    m.ShowWarning("/" + commandName + " is not yet available ...")
```

意图是给 T6 未移植的延迟命令（export/import/share/copy/session/changelog/hotkeys/clone/debug + 彩蛋）占位，但 pi 的 onSubmit **根本没有「未知命令」分支**——已知命令逐个精确匹配后，其余 `/` 开头文本一律走普通消息提交（扩展命令/技能/模板在 `AgentSession.prompt` 内部解析，`AgentSession.prompt` 的 `_tryExecuteExtensionCommand` 与 skill 展开对不匹配的 `/...` 文本原样放行）。pig 的兜底把所有「以 `/` 开头但不是命令」的输入全部拦下，而 macOS 粘贴图片插入的路径恰以 `/` 开头 → 必触发。

对照：pi `packages/coding-agent/src/modes/interactive/interactive-mode.ts` onSubmit 分支列表（settings/scoped-models/model/thinking/export/import/share/copy/name/session/changelog/hotkeys/fork/clone/tree/login/logout/compact/debug/arminsayshi/dementedelves/resume）之后直接落到 `onInputCallback(text)`，无 unknown-command 告警。

## 修复

兜底分支收窄为显式的延迟命令清单（与 pi onSubmit 未移植分支一一对应，匹配形状也照抄：带参命令 `/export`、`/import` 匹配裸命令与 `"<cmd> " ` 前缀，其余精确匹配）：

- 新增 `isDeferredInteractiveCommand(text)`：export/import/share/copy/session/changelog/hotkeys/clone/debug/arminsayshi/dementedelves。
- `processSubmitText` 的兜底分支改为 `case isDeferredInteractiveCommand(text):`；其余 `/...` 文本落到普通消息提交（扩展命令、技能、模板由 `AgentSession.prompt` 解析，行为与 pi 一致）。
- 顺带语义修正：`/exports folder` 这类「更长单词」不再被 `/export` 误吞（pi 同样不吞）。

同批顺带实现（用户插入需求，opencode 风格占位符）：粘贴图片改为插入 `[Image N]` 占位符，提交时展开为真实路径，详见 `docs/feat/CLIPBOARD-IMAGE-PLACEHOLDER.md`。该功能上线后粘贴路径不再出现在编辑器里，但手打/文本粘贴的 `/...` 路径仍会以普通消息提交——本修复独立成立。

## 回归测试

`codingagent/modes/interactive/interactive_mode_submit_test.go`（全部新增）：

- `TestClipboardPathSubmissionReachesModel`：5 个子测试（用户原话路径 + 你好、裸路径、`/etc/hosts`、`/foo bar`、`/exports folder`）——未知 `/...` 文本必须进 inputGate 且无 Warning 渲染；
- `TestDeferredInteractiveCommandsStillWarn`：12 个子测试——延迟命令仍显示告警且不进 inputGate；
- `TestIsDeferredInteractiveCommandBoundaries`：匹配形状边界（`/exportx`、`/exports x` 不算命令；已实现命令不算延迟命令）。

**验证方式**：临时把三处修复注回原 bug（catch-all 恢复 / 插入原始路径 / 展开退化恒等）→ `TestClipboardPathSubmissionReachesModel`、`TestClipboardPasteInsertsImagePlaceholder`、`TestClipboardPlaceholderExpandsToPathOnSubmit`、`TestTranscriptRendersImagePlaceholderLabel` 各自 FAIL；还原修复后同批测试 PASS + 整包 `go test -count=1` 全绿。（直接 `git stash` 不可用：测试文件引用了新 seam，stash 后编译失败，故采用注回 bug 的等价验证。）

## 备注

- `inputGate.wait()` 阻塞语义下，「命令被吞」类回归在测试里表现为 2s 超时超时 Fatal——catch-all 注回时该测试 10s（5 子测试 × 2s），行为特征吻合原 bug。
- 延迟命令清单是 T6 未完成项的显式化；后续批次实现各命令时应把对应条目从 `isDeferredInteractiveCommand` 挪进 `processSubmitText` 的 switch 分支。
