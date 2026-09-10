# S5b：interactive-mode + 组件 + theme 对齐 v0.85.1 + 回归测试族

**状态**：✅ 完成（2026-09-10）。下一批：S5 全部收口，基线同步剩余项见本文件第五节「给人类的建议」；批次定义回归 `docs/handoffs/sync_v0_85_1.md`。

## 一、本批完成

### Src 改动（全部对齐 pi v0.85.1 `git diff 4e58f324f..d981de122 -- src/modes/interactive/`）

**tui 层（3 处 seam，供组件消费）**
- `tui/terminal_image.go`：`TerminalCapabilityOverrides` 类型（规范定义落 tui，core 起别名）+ `SetCapabilityOverrides` + `GetCapabilities` 应用覆盖（pi: setCapabilityOverrides/getCapabilities spread；pig 检测机制仍 Phase 8，覆盖值直接生效）
- `tui/editor.go`：`TopBorderOverride func(width, hiddenLineCount int) (string, bool)` 回调 seam（pig 替代 pi 的 protected renderTopBorder 继承 override；返回 false = super 调用）
- `tui/loader.go`：导出 `GetRenderedIndicator()`（pi: protected getRenderedIndicator → WorkingStatusIndicator.renderSpinnerInBorder）
- `codingagent/core/settings_manager.go`：TerminalCapabilityOverrides 改为 tui 别名

**theme 包**
- `theme_json.go`（新建）：validator seam（`ValidateThemeJson` / `SetThemeJsonValidator` / `ThemeJsonValidator`），validateThemeJson 全文自 theme.go 搬入 + "/" 名校验（pi theme-json.ts 拆分；pig 手写校验器替代 TypeBox，seam 形状 1:1）
- `theme.go`：`parseThemeJson` 走 seam（无 validator = 最小 "colors" 结构检查、跳过 "/" 名校验，逐语义对齐 pi 的 `json as ThemeJson`）；**scrollbarThumb 从 bg token 挪到 fg token + 新增 scrollbarTrack**（fallback：track→muted、thumb→text）；OptionalThemeColor/Bg 更新；`withThemeColorFallbacks`/`NewTheme`/`createTheme` 三处同步
- `codingagent/assets/theme/{dark,light}.json` + `theme/testdata/theme/{dark,light}.json`：scrollbarTrack 新增（darkGray/lightGray）、scrollbarThumb 改 "text"
- `theme_controller.go`：`Dispose()`（停 autoSync + 退订 color-scheme；pi 不在 interactive-mode 调它，只有 experimental/client-tui 调，pig 同样不接线）

**components**
- `status_indicator.go`：`WorkingStatusIndicator` 独立 struct（value 嵌入 StatusIndicator，同 Retry 先例）+ `colorFn` 参数（同时着色 spinner 与 message）+ `RenderInBorder` / `RenderSpinnerInBorder`
- `custom_editor.go`：`CustomEditorOptions`（EditorOptions + EmbedWorkingStatus）+ `SetWorkingStatusIndicator` + `renderTopBorder` 三分支 override（overflow label 居中 / spinner 降级 / 纯 spinner），经 TopBorderOverride seam 注入
- `assistant_message.go`：thinking run 点击折叠——`thinkingVisibilityOverrides map[int]bool` + MouseRegion click 切换 + `SetHideThinkingBlock` 清空 overrides（v0.85.1）
- `thinking_selector.go`：当前档 `✓` 标记进 label、hint 走 `KeyDisplayText`（`app.thinking.save` / `tui.select.*`）、Ctrl+S 硬编码改 kb.Matches、fuzzyFilter 改按 value 匹配（防 ✓ 污染评分）
- `model_selector.go`：行渲染改 cursor + 持久 currentMarker（`✓` 在 id 前，accent 色）+ hint 键位化 + `app.models.save` 查表
- `scoped_models_selector.go`：**toggle 语义反转**（nil 首次 toggle = 关掉该 id；re-enable 最后一个 = 归还 nil，pi normalizeEnabled）+ 渲染标记前移（status 在 id 前）+ unavailable 划线（theme.Strikethrough）+ hint KeyDisplayText 化
- `settings_selector.go`：`FullscreenCopyOnSelect` config + `OnFullscreenCopyOnSelectChange` callback + "fullscreen-copy-on-select" 条目；themeItems/单模式主题 ✓ 标记；per-model thinking override ✓ 标记 + "(clear override)" 补位；cache-miss-notices 文案更新

**interactive 包**
- `chat_viewport.go`（新建）：`createChatViewport`（transcript ScrollView follow=end/primary/overscroll=chain + scrollbar track/thumb 样式经 theme.fg("scrollbarTrack"/"scrollbarThumb") + dock VStack 含可选 widget 槽）；Init 内联布局改走它
- `tui_renderer.go`（新建）：`createInteractiveTui` / `tuiReference` 自 interactive_mode.go 整体搬出（v0.85.1 tui-renderer.ts 抽取）+ 新增 `terminal` 注入（测试）、`fullscreenCopyOnSelect`、SearchNavigationButtonStyle、ScrollToEndIndicator（`tui.altScreen.bottom` 键位提示）、CopyOnSelect 传递；`interactiveCopyToClipboard` 包级 seam（pi vi.mock 对应物）
- `interactive_mode.go`（十余处）：
  - `defaultWorkingMessage = "Working"`（v0.85.1 去省略号；**顺带修复 pig 从未赋值的潜伏 bug**——此前 workingMessage 为空时 indicator 显示空串）
  - working status 内嵌机制：`activeWorkingIndicatorEmbedded` + `setEditorWorkingStatusIndicator` + `showStatusIndicator`（working 优先内嵌 editor 顶边框）+ `clearStatusIndicator`（embedded 不补 idleStatus）+ `showWorkingStatusIndicator`（colorFn = editor.BorderColor ?? thinkingBorder）+ setCustomEditorComponent 尾部 re-embed + updateEditorBorderColor invalidate working indicator
  - **agent_start / turn_start 拆分**（v0.85.1）：SetProgress + working indicator 移到 turn_start（compaction 恢复后同 agent run 也拿到指示器）
  - `maybeShowAssistantDiagnostics`（anthropic_input_transformations → thinking_dropped 逐条转文案，受 showCacheMissNotices 门控）+ 两处调用（streaming 完成块 + 非 partial message_start）
  - `updateThinkingBlockVisibility`（就地更新，不再 rebuildChat——修复 #8611 thinking 切换销毁运行中 bash 组件）+ toggle/settings 回调双接线
  - `handleCopyCommand(flash, preferSelection)` 补齐 + `app.message.copy` action 注册（**pig T5b 外壳漏移植，本批补齐**）+ `app.editor.external` action 注册（同样漏移植）；preferSelection 分支走 HasActiveSelection/CopyActiveSelectionToClipboard/GetCopyOnSelect（S4 已有原语）
  - `applyRuntimeSettings`：SetCapabilityOverrides + alt-screen SetCopyOnSelect 同步；handleReloadCommand 中 applyRuntimeSettings ↔ themeController.ApplyFromSettings **顺序对调**（v0.85.1）
  - selectModel 后 `updateAvailableProviderCount()`（v0.85.1）
  - settings selector config/callbacks 接线 fullscreenCopyOnSelect；两处 createInteractiveTui 调用传 fullscreenCopyOnSelect
  - 构造函数：SetCapabilityOverrides + defaultWorkingMessage 赋值

### 测试（+30 函数；全仓 1051 → **1081**，26 包 -race 全绿）
- 新文件（10）：`thinking_selector_test.go`（2）、`trust_selector_test.go`（3，基线文件首移植含 v0.85.1 标记位变更）、`scoped_models_selector_test.go`（4，toggle 语义锁定）、`chat_viewport_test.go`（1）、`scrollbar_theme_test.go`（4，基线文件首移植）、`interactive_mode_assistant_diagnostics_test.go`（1）、`interactive_mode_turn_start_test.go`（1）、`interactive_mode_8611_test.go`（1）、`interactive_tui_test.go`（4：jump-to-end 键位提示 / copy-on-select disabled 选区优先 / enabled 走 assistant 文本 / fullscreen Copied! flash）、`tui_renderer.go` 的 `copyToClipboardForTest` seam
- 增量：assistant_message_test（thinking 点击折叠）、model_selector_test（浏览保持 ✓ 标记 + 可配置保存键 2 例）、settings_selector_test（fullscreen copy-on-select 循环 + 固定主题 ✓ + per-model thinking ✓）、status_indicator_test（边框不内嵌默认 / 内嵌后 "── ⠋ Working ───────" 精确断言 + 5 段 thinkingHigh 着色）

### 批内决策与偏差（新增）
- **测试基建新增 seam**：`interactiveCopyToClipboard` 包级 var（pi vi.mock clipboard 的等价物）；`copyToClipboardForTest(t, fn)` 助手；turn-start/8611 测试用 `newSeamTestMode` + `m.isInitialized = true` 跳过懒 Init（对应 pi fakeThis）
- **-race 修复（测试侧）**：model-selector 新测试与 refresh goroutine 共享 mutex（mutator 即测试锁），并 `<-selector.refreshDone` 等待后再 Dispose——pi 单线程不存在此竞争
- **跳过（pi v0.85.1 增量、pig 无对应物）**：session-share.ts mkdtemp 重构（pig 无该文件，exportToHtml 属 Phase 8）、package-distribution.test（npm 打包）、startup-session-name/stdout-cleanliness 的 source-resolver 改动（TS 打包专属）、6596 taskkill 行为测试（S5a 已声明：runtime.GOOS 不可 mock）、image-processing.test（依赖 photon 运行时，pig Phase 8）

## 二、下一批建议

S5（coding-agent 同步）至此全部完成。§4 Phase 7（扩展系统）未开始；Phase 8 杂项未开始。建议下一批由人类决策，候选：
1. **v1.0 收口检查**：AGENTS.md §4 Phase 6/同步策略剩余消费点核对（Phase 8 的 terminal-image 检测、photon/wazero、export-html、migrations、goreleaser 仍缺）
2. **pi 基线再核对**（若 pi 发新 tag）：本次同步后 v0.85.1 消化完毕，排除区仅剩 harness runtime/（S2 冻结）与 experimental/
3. **回归测试 harness 移植**（见下）

## 三、风险与坑（下批注意）

- `tui.Editor.TopBorderOverride` 是 pig 发明的 seam（Go 无虚方法派发）；后续 pi 对 renderTopBorder/renderBottomBorder 的任何再改都要先查 seam 语义
- `TerminalCapabilityOverrides` 规范所有权在 `tui` 包，core 是别名——不要再往 core 加字段
- model-selector/scoped-selector 渲染断言对空格敏感（"→ ✓ " / "→   "）；改行渲染时测试全组一起动
- `interactiveCopyToClipboard` seam 只覆盖 fullscreen CopySelection 与 handleCopyCommand；新代码需要可 mock 剪贴板时统一走它
- footer-data-provider 的 fake-timer 测试改造（pi +33）未移植：pig PollWatcher 结构不同，若移植需先给 fs_watch.go 加事件注入 seam

## 四、明确不要读的

沿 S5a 第四节全部有效：`src/experimental/`、`src/cli/experimental/`、`src/{client,server}/`、`src/modes/rpc/`、`src/bun/`、pi `packages/agent/src/harness/runtime/`、`packages/chord/`、pi `docs/`、`test/experimental*`、`test/client/*`、`test/server/*`、`test/rpc-*`。

## 五、给人类的建议（不改计划，仅列未消化项）

1. **pi suite harness（createHarness）从未移植**：pi 的 `test/suite/regressions/` 基础文件（4167/3217/7209/6949/7253/8537/8724/8935 等，共 ~9 个文件）依赖 response-queue 式 mock streamFn + 事件录制 + extensionFactories 的 harness，pig 的 createAgentTestSession 只有 StreamFn/SettingsOverrides。v0.85.1 这些测试的 delta（勾选标记解析、数值参数）已在本次核对中确认无新语义缺口（7048/8537 语义已由 S5a pendingCustomMessages 覆盖、8989 已移植）。若将来要整体移植 suite/regressions，需先建 harness（约 150 行 helper）。
2. **llama.cpp 扩展**（pi `src/extensions/llama/`，~1.4k 行 + 72 行测试）：pig 从未移植，随 Phase 7 扩展系统决策（原生 provider 扩展形态）。
3. **footer-data-provider fake-timer 测试重构**：pi 行为无变、仅测试驱动方式，pig 可在任一空档补。
