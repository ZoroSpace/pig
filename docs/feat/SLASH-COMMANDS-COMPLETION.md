# SLASH-COMMANDS-COMPLETION：交互 slash 命令补全批（用户插入批，2026-09-18）

## 背景

用户在交互模式输入 `/session` 触发 deferred 拦截警告，追问「还有哪些 / 命令有问题或没迁移」。盘点（对照 pi 22 条 BUILTIN_SLASH_COMMANDS + onSubmit 全部分支）结论：

- ✅ 已接线 14 个（T5b/T6-1/T6-2 交付）；
- 🚨 **`/reload` 真漏接线**（不在 submit switch 也不在 deferred 清单 → 原文发给模型，handler 却早已存在，见 docs/fix/2026-09-18-reload-command-leaks-to-model.md）；
- 🚨 `--resume` CLI 门禁过时（仍报 "TUI lands in a later phase"，但 T6-2 已交付选择器）；
- ❌ deferred 拦截 10 个中 5 个底层件全就绪、3 个基建在、2 个真被外部依赖卡住（/export 等 P8 export-html、/share 等 GitHub gist API）。

用户拍板：除 /export /share 外全部本批做完。

## 交付内容

### 1. `/reload` submit 分支补接线（bug 修复）
`interactive_mode.go` processSubmitText 补 case：清编辑器 → `m.handleReloadCommand(ctx)`（pi: `editor.setText(""); await handleReloadCommand()`）。handler 本体 T5b-3 已有，零新逻辑。回归测试红→绿验证见 fix 文档。

### 2. 八个命令接线（pi 1:1，声明偏差随附）

| 命令 | handler（pi 对应） | 依赖件来源 |
|---|---|---|
| `/copy` | handleCopyCommand(true, false)（pi 默认参：flash=true, preferSelection=false） | T5b-2 已有，仅接 submit 分支 |
| `/session` | handleSessionCommand：会话名/文件/ID + 消息计数 + token 明细（cache 命中率/写入拆分）+ cost 明细（usage breakdown 多于 1 项时逐行列出）+ Cache Re-billed | GetSessionStats + ComputeCacheWaste + GetUsageCostBreakdown（footer 修复批已有） |
| `/changelog` | handleChangelogCommand：ParseChangelog 逆序 + NormalizeChangelogLinks + Markdown 渲染；空表回落 "No changelog entries found." | utils/changelog.go（T5b-2） |
| `/hotkeys` | handleHotkeysCommand：Navigation/Editing/Other 三张 markdown 快捷键表（键位经 KeyDisplayText 实时解析）+ Extensions 表 | coretools.KeyDisplayText + GetShortcuts（T5b-3） |
| `/import` | handleImportCommand：getPathCommandArgument 取参 → 确认框 → ImportFromJsonl；MissingSessionCwdError → promptForMissingSessionCwd 重试；SessionImportFileNotFoundError → 报错 | AgentSessionRuntime.ImportFromJsonl（第 6 批已有） |
| `/clone` | handleCloneCommand：GetLeafID → Fork(position "at") → 状态提示 | AgentSessionRuntime.Fork |
| `/debug` | handleDebugCommand：mutateUI 内快照当前帧（逐行 visibleWidth + JSON 转义）+ 会话消息 JSONL dump → GetDebugLogPath() 落盘 → transcript 显示路径 | codingagent.GetDebugLogPath（config.go 已有） |
| 彩蛋 `/arminsayshi` `/dementedelves` | ArminComponent（XBM 半块像素画 + 7 种随机动效）+ EarendilAnnouncementComponent | 新移植 components/armin.ts、earendil-announcement.ts |

新移植组件（`codingagent/modes/interactive/components/`）：
- `armin.go`（← armin.ts 全量：typewriter/scanline/rain/fade/crt/glitch/dissolve 七动效，30fps（glitch 60fps）；pig 并发面：setInterval → ticker goroutine + Guard 收容，Render 缓存 mutex 化；BITS 144 字节经脚本逐字节比对一致）
- `earendil_announcement.go`（← earendil-announcement.ts；**声明缺口**：pi 的 Kitty Image 子项（clankolas.png）随 Phase 8 Image 组件落地，暂走 pi 的无图回退路径）

`getPathCommandArgument`（pi 同名，引号/空格解析）随 /import 移植，/export 接线时直接复用。

### 3. deferred 清单收窄 + warning 文案分话
`isDeferredInteractiveCommand` 只剩 `/export`（裸 + 带参形态）与 `/share`；新 `deferredCommandReason` 按命令给理由（export → "HTML export lands in a later batch"；share → "GitHub gist sharing lands in a later batch"）。旧共用文案 "interactive selectors land in a later batch" 是 T6 之前的过时话术（选择器 T6 已交付），一并消灭。

### 4. `--resume` CLI 门禁解封
新 `codingagent/cli/session_picker.go`（← cli/session-picker.ts 55 行 1:1）：CreateStartupTui + SessionSelectorComponent（ShowRenameHint=false、fresh keybindings、无 current-session path）→ 返回选中路径。main.go `--resume` 分支从报错改为调 SelectSession → openSessionOrExit；取消 → "No session selected" exit 0；onExit → ui.Stop + os.Exit(0)（pi 同语义）。**pig 并发面**：mutator 传 `ui.RunExclusive` 包装（interactive 侧传 m.mutateUI，startup TUI 无 InteractiveMode 外壳，直接对 TuiBase 串行化选择器后台 mutation 与渲染 goroutine）。

## 声明偏差（新增）

1. `/hotkeys` Extensions 表排序：pi 遍历 JS Map（插入序）；Go map 无序 → 按键名排序输出（确定性）。
2. `/session` `/hotkeys` `/changelog` 的数值格式化：toLocaleString → formatLocaleInt（en-US 千分位手写）；formatTokens 复用 components.FormatTokens。
3. `/debug` 写盘失败：pi 任其抛未处理 rejection；pig ShowError 显示（panic-resilience 同风格）。
4. `/import` 二次尝试（missing-cwd 重试后）出错：pi 会逃逸出 onSubmit promise；pig 路由到 handleFatalRuntimeError（missing-cwd 带显式 override 不会复发，防死循环）。
5. Earendil Image 子项缺（Phase 8）。
6. main.go --resume：pi 的 selectSession 在 TUI stop 后 resolve；pig finish(channel) 后 Stop——settled 标志等价，顺序无观测差异。

## 测试

新增 `interactive_mode_commands_test.go`（4 测试函数）：
- `TestWiredSubmitCommandsReachTheirHandlers`（7 子测：copy/session/changelog/hotkeys/clone + 两彩蛋——输入门空 + 渲染内容断言）
- `TestImportCommandWithoutPathShowsUsage`
- `TestDebugCommandWritesDebugLog`（PIG_CODING_AGENT_DIR 指向临时目录，断言日志结构：帧行 visibleWidth/JSON 转义 + JSONL 消息段；宽字符 w=10 锁定 rune→列宽换算）
- `TestReloadCommandWhileStreamingShowsWaitWarning`（/reload 漏接线回归，红→绿已验证；streaming 态经可控 streamFn 构造，结束经 release channel 收敛，无 goroutine 泄漏）

更新 `interactive_mode_submit_test.go`：TestDeferredInteractiveCommandsStillWarn 收窄到 export/share + 新文案断言；TestIsDeferredInteractiveCommandBoundaries yes/no 清单同步（8 个已接线命令显式进 no）。测试主题补 `mdLink` token（彩蛋 URL 需要）。

## 真 TTY 冒烟（tmux + 本地 mock 端点，2026-09-18 实测）

- `/session`：空会话与 1 轮对话后两次渲染，消息计数/token/usage 全对，footer 同步 ↑100 ↓500
- `/hotkeys`：三表渲染，键位实时解析（Up/Down/Left/Ctrl+B 等实际绑定值）
- `/changelog`：What's New + 空表回落文案
- `/copy`：Copied last agent message to clipboard（OSC 52 → tmux buffer）
- `/debug`：73KB 日志落盘，帧 dump + JSONL 消息段结构完整
- `/arminsayshi`：动效收敛至 "ARMIN SAYS HI" 半块像素画
- `/dementedelves`：Earendil 卡片（无图回退路径）
- `/reload`：流式 idle 下完整重载，"Reloaded keybindings, extensions, skills, prompts, themes, and context files"
- `/clone`：Cloned to new session（fork at 当前位置）
- `/export` `/share`：新文案警告，未发给模型
- `--resume`：选择器渲染（scope/sort 提示、会话列表）→ Enter 选中 → 会话历史完整恢复进交互模式

**冒烟观察（非本批回归，记录待查）**：tmux send-keys 瞬时连发 `/hotkeys`+Enter 出现一次文本拼坏（`/hhotkeys` 落普通消息，autocomplete 接受路径在按键洪峰下的行为）；慢速输入/真人键入无此现象。本批未触碰 editor/autocomplete 接受逻辑，若是 bug 属 T2/T5b-2 存量，待后续批次定论。

## 明确不做

- `/export`（P8 export-html；.jsonl 路径的 ExportSessionToJsonl 已有，接线时顺手）
- `/share`（GitHub gist API，未动工）
