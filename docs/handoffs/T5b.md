# Handoff：T5b（interactive-mode 外壳，冲 M4）→ T5b-2（外壳补全）

> 2026-09-09 收批。规则引用 AGENTS.md §3.4/§3.5。pi 基线 v0.84.3。
> 状态：✅ 已完成并被下一批交接取代。

## 一、本批完成

**新建/改动**（pi 侧合计 ≈5.3k 行源码+测试）：

- `codingagent/core/keybindings.go`（505 行）+ 测试 — keybindings.ts 全量：TUI_KEYBINDINGS 合并表（tui.* 平台覆盖 + 43 个 app.* id）、`UseWindowsKeybindings`、legacy 名迁移 `MigrateKeybindingsConfig`（keybindingsOrder 显式记录表序，Go map 无序）、keybindings.json 装载 `CreateKeybindingsManager/Reload`。测试 = keybindings.test.ts 全量 + migration 的内存装载用例（runMigrations 文件重写两例随 P8）
- `codingagent/core/cache_stats.go`（216 行）+ 测试 — cache-stats.ts 全量（CACHE_TTL_MS/detectMiss/scan/ComputeCacheWaste/CollectCacheMisses/DetectCacheMiss）；`ModelPriceSource` 接口 + Func 适配器（ModelRuntime 注入）
- `codingagent/utils/fs_watch.go`（94 行）— fs-watch.ts：PollWatcher（mtime/size 轮询，interval 可调）+ `WatchWithErrorHandlerWithInterval`；`theme/fs_watch.go` 改别名
- `codingagent/core/footer_data_provider.go`（456 行）+ 测试 — footer-data-provider.ts 全量（FindGitPaths 复用 resource_loader_default 既有实现并修其绝对 gitdir bug、HEAD/reftable/tables.list 三路 watcher、WSL 轮询、500ms 去抖刷新、in-flight/pending 合并）；child_process 落 `footerResolveBranchSync/Async` 函数 var（测试 seam）；fs.watch → PollWatcher
- `codingagent/modes/interactive/components/footer.go`（413 行）+ `footer_width_test.go` — footer.ts 全量（formatTokens/formatCwdForFooter/usage 汇总/订阅标记/右对齐模型名/扩展状态行）；`footerSessionView` 函数字段 seam（Go 无法对具体 struct 做 duck-typing stub）；`ReadonlyFooterDataProvider` 接口
- `codingagent/modes/interactive/components/custom_editor.go`（116 行）+ 测试 — custom-editor.ts 全量（OnEscape/OnCtrlD/OnPasteImage/OnAction/OnExtensionShortcut 优先级链与 pi 逐分支一致）；测试 = custom-editor-history-keybindings.test.ts
- `codingagent/modes/interactive/components/{extension_selector,extension_input,first_time_setup}.go`（409 行）— startup-ui 依赖三组件全量
- `codingagent/cli/startup_ui.go`（287 行）— startup-ui.ts 全量（createStartupTui/startStartupTui/applyDetectedStartupTheme/ShouldRunFirstTimeSetup/ShowStartupSelector/ShowFirstTimeSetup/ShowStartupInput）；`loadStartupThemes` 走 `ResolveSettingsResourcePaths`（packageManager.resolve 的 settings 半区已存在，包源随 P7）；theme 包补导出 `TuiThemeDetector`
- `codingagent/modes/interactive/interactive_mode.go`（2455 行）+ `interactive_mode_test.go` — **外壳**：InteractiveModeOptions/构造/Init/Run/updateTerminalTitle、startup 头（ExpandableText logo+快捷键说明）、提交链（/quit /new /compact /name + 延后命令显式警告 + `!`/`!!` bash + compaction 队列 + steer + inputGate→Run 循环）、handleEvent 全事件面、transcript 全渲染（addMessageToChat/renderSessionItems/renderSessionEntries/renderInitialMessages/compaction cost/cache-miss notice/trust 警告）、pending/queue 全套（getAllQueued/clearAllQueues/restoreQueued/flushCompactionQueue）、key handlers（escape 三态/cycle thinking/model/tools.expand/thinking.toggle/followUp/dequeue）、Ctrl+C/D、SIGTERM/SIGHUP、shutdown+resume 提示、handleBashCommand（user_bash 拦截段随 P7）
- `cmd/pig/main.go` — interactive 分支装配（替换 "not yet available" 报错）
- 小补：`core/extensions_runner.go` 补 `GetEntryRenderer/GetMessageRenderer`（无扩展路径 nil）；`components/status_indicator.go` 补 `IndicatorKind()`

**测试**：全仓 917 个测试函数（+10），26 包 `go build`/`go vet`/`gofmt` 干净，`go test ./... -race` 全绿。**M4 冒烟（tmux 真实 TTY + 本地 mock OpenAI SSE 端点）**：启动头/footer/多轮流式回复/三工具面渲染/`--session` 恢复渲染 3 轮历史/Ctrl+D 干净退出，全部通过。

**本批新增偏差与决策**（详见各文件头）：

1. **interactive_mode.go 头部集中声明延后段**：selectors（model/session/tree/trust/login/settings/thinking/fork/clone 及其命令）、showLoadedResources+路径标签、扩展 UI 面（widgets/header/footer/selector/input/editor/notify/shortcuts）、fullscreen（TuiAltScreen/switchTuiMode）、changelog/version/package/tmux 检查、external editor/clipboard、suspend(Ctrl+Z)、autocomplete provider、EditorFactory/TUI reference proxy —— 全部落 T5b-2/T6。
2. **并发模型（pig 添加）**：pi 单事件循环 → 提交体跑在串行 submit-worker goroutine（不阻塞 TUI 输入）；Run 循环经 `userInputGate`（= pendingUserInputs+onInputCallback 等价物）取输入；escape-handler 换入换出用 abortMu。
3. **修复 TUI 层三个死锁/渲染 bug（pi 单线程无对应物，均为 pig T1/T5a 遗留）**：① `tui/terminal.go` ABBA——input dispatch 持 t.mu→b.mu 与 render 持 b.mu→t.mu 互锁，OnData/negotiation-flush 改为**不持 t.mu 分发**（`dispatchInputSequence`；`readKeyboardProtocolNegotiationSequenceLocked` 改返回 bufferedToForward）；② `tui/stdin_buffer.go` 自死锁——emitDataSequence 持 b.mu 调 dataHandler，handler 内 Destroy→Clear 再抢 b.mu，handler 移出锁外；③ `components/assistant_message.go`——adapters 产**值类型** Content，组件指针断言失败渲染为空，加 `asTextOrThinking` 归一（T5a-1 测试 fixture 同用指针未暴露）。
4. **装配序坑**：`SetFocus` 必须在 `AddChild` 之后（未挂载组件焦点被忽略）；pi 的七容器是 TUI **兄弟**子节点（mountInteractiveTui），不是 documentContainer 的孩子；`startSubmitWorker` 必须显式启动（漏调 = 提交进缓冲 channel 后无人消费，表现为"打字无回显无提交"）。
5. `FormatCwdForFooter`：`filepath.Rel` 同路径返回 `"."` 而 node 返回 `""`，需归一。
6. `findGitPaths`（resource_loader_default.go）：worktree 的 `.git` 文件内容是**绝对** gitdir，`filepath.Join(dir, abs)` 语义错，需 `filepath.IsAbs` 分支（pi 的 `resolve(dir, x)` 语义）。
7. 无 `KeybindingsManager` 名字冲突问题：core 版嵌入 `*tui.KeybindingsManager`；`tui.SetKeybindings` 需传内嵌字段。
8. footer-data-provider 测试：容器 fs（ext2）mtime 粒度粗，写入前 sleep 50ms 错开时间桶（PollWatcher 既有偏差的测试面体现）；watcher-retry 用 real timers（5s）。
9. `UseWindowsKeybindings(platform string, env func)`（pig 可注入，测试用）+ GOOS 名映射（"windows"）。
10. interactive_mode_test.go 仅覆盖 inputGate（pi 的 prototype-stub 三用例中另两个需 session seam，随 T5b-2）。

## 二、下一批定义：T5b-2（外壳补全，收完 M4 交互面）

**要读的 pi 源文件**（`~/Projects/pi/packages/coding-agent/src/`）：

- `modes/interactive/interactive-mode.ts` 指定段：`showLoadedResources`（1687–1902）+ 路径标签 helpers（1314–1686，getShortPath/getCompact*/buildScopeGroups/formatScopeGroups/formatPathWithSource/formatDiagnostics）；`setupAutocompleteProvider`+`createBaseAutocompleteProvider`（627–792，含 model/thinking/login 参数补全与 skillCommands）；扩展 UI context（2127–2848：setExtensionStatus/showStatusIndicator 系/widgets/header/footer/selector/input/editor/notify/error/createExtensionUIContext）；`rebindCurrentSession`/`switchTuiMode`/`mountInteractiveTui`/`stopInteractiveTui`（823–890, 2008–2042，fullscreen 链）；`handleCtrlZ`（4058–4095）；clipboard/external editor（2917–2956, 4216–4237）；`handleReloadCommand`（5910–5999）；`showNewVersionNotification`/`showPackageUpdateNotification`（4255–4305）
- `components/mermaid.ts`（mermaid transformer，getMarkdownTransformers 的第一项）
- `utils/changelog.ts` + `utils/version-check.ts` + `utils/clipboard.ts` + `utils/clipboard-image.ts` + `utils/open-browser.ts`（startup 检查与粘贴链）
- `core/agent-session-runtime.go` 的 SetRebindSession/SetBeforeSessionInvalidate 消费面（pig 已有，对接用）

**要移植的 pi 测试**：

- `test/interactive-mode-status.test.ts`（1251 行）——本批可测子集（showStatus/showManagedToolStatus/setToolsExpanded/createBaseAutocompleteProvider），其余随扩展 UI；VirtualTerminal 在 tui 包已有等价物（editor 测试在用）

**预期产出**：`interactive_mode.go` 扩展 + `components/mermaid.go` + `codingagent/utils/{changelog,version_check,clipboard,clipboard_image,open_browser}.go` + 对应测试；fullscreen 若过大可再拆 T5b-3（声明即可）。

**依赖与顺序**：本批产出的 footerSessionView/submit-worker/inputGate 是 T5b-2 的直接消费面；autocomplete 需要 `tui.CombinedAutocompleteProvider`（已交付）+ `core.BUILTIN_SLASH_COMMANDS`（已有）；扩展 UI context 需要 `core.ExtensionUIContext`（已有接口，noOp 实现在 extensions_types.go）。

## 三、风险与坑（本批实证）

1. **tmux 是共享设施**：agent 与用户会话在同一 server——只用专属命名 session，**严禁 kill-server**；`pkill -f` 模式会匹配自身命令行，用 `^/absolute/path` 锚定（以上已写进 AGENTS.md 仓库机制节）。
2. 新增组件/外壳代码时**先想锁序**：t.mu → b.mu 是合法方向，反向必死锁；任何在 input-dispatch 回调里同步调用的路径都不得持 ProcessTerminal.mu/StdinBuffer.mu。
3. Content 值/指针：adapter 产出的 Message.Content 是值类型；所有 `.(types.X)` 断言都要双形态处理（已有 `asAssistantMessage`/`asTextOrThinking` 先例）。
4. theme 测试资产 TestMain（PI_PACKAGE_DIR→theme/testdata）照旧；footer 类 watcher 测试记得 mtime 粒度 sleep。
5. `forwardInputSequenceToLocked` 名字已名不副实（现无锁调用），T5b-2 顺手改名。

## 四、明确不要读的

- pi 侧：`src/experimental/`、`src/modes/rpc/`、`packages/{protocol,client,server,evals}/`、interactive-mode.ts 的 login/oauth/session-selector/tree-selector 段（T6）
- pig 侧：`docs/batches/2026-09.md`（历史归档）；`ai/`、`agent/` 内部（无改动需求）

## 给人类的建议

- M4 手感验收建议真机 tmux 里人工跑一轮（复用 /tmp/pig_t5b 的 mock 端点方案），自动化覆盖不了编辑器手感。
- T5b-2 后 T6 收尾 selectors + fullscreen；若 T5b-2 装不下，fullscreen 单独成 T5b-3（批内声明即可，不必先改计划）。
