# S5a：coding-agent core 层（工具渲染器拆分 + agent-session + core 杂项）

**状态**：✅ 完成（2026-09-10）。下一批 = **S5b**（定义见本文件第二节；总定义见 `docs/handoffs/S3.md` 第二节与 `docs/handoffs/sync_v0_85_1.md`）。

## 一、本批完成

### 改动文件

**渲染器拆分（S5a 主体）**：
- `codingagent/core/tools/renderers_index.go`（新建）：`ToolRenderers`（renderShell + pig-addition `NewRenderState` + renderCall/renderResult）+ `renderersByToolName`（`createAllToolRenderers`）+ `WithBuiltInRenderers`（fill renderCall/renderResult/NewRenderState，nil 定义返回 render-only 壳）
- `codingagent/core/tools/renderers_{bash,edit,read,write,grep,find,ls}.go`（新建 7 文件）：渲染段自七工具文件整体搬入（v0.85.1 拆分是纯搬移，无行为变化）；`CreateShellRenderers(prompt)` 工厂（bash/powershell 共用）；`bashUpdateThrottleMs`/`bashPreviewLines` 常量随渲染器落位
- **结构决策（本批最重要的偏差，需向后同步）**：pi 的 `core/tools/renderers/` 子目录在 Go 下必成 import cycle（renderers 依赖 tools 包的 render-utils/edit-diff/keybinding-hints/详情类型，而工具文件又要展开渲染器进定义）。pig 落为 **package tools 同包的 `renderers_*.go` 文件**（grep 映射：pi `renderers/bash.ts` ↔ `renderers_bash.go`），定义内嵌渲染器（`RenderCall: EditRenderers.RenderCall` 等）保持与 pi 的 spread 语义一致，export-html 的 `toolDef.renderCall` 直读路径不受影响。文件头均有说明
- 七工具文件瘦身（bash/edit/read/write/grep/find/ls）；write.go 成功文案改 `"Successfully wrote to <path>"`（v0.85.1 去掉字节数）
- **ctx.cwd 穿透**（v0.85.1 七工具全部支持）：`ToolExecuteContext` 增 `GetCwd() string`（"" = undefined）+ `executeCwd(toolCtx, cwd)` helper；七工具路径解析全部接入；`ExtensionContext.GetCwd()` 桥接

**tool-execution 组件（v0.85.1 形态）**：
- `components/tool_execution.go`：内置 fallback 移除（`builtInToolDefinition` 字段删除，lookup 归调用方）；`createResultRegion`（MouseRegion 点击展开，每个渲染子节点包裹）；`selfRenderHeight` + `HandleMouse` 覆盖（self-shell 鼠标路由，首行空行不计入）；`convertedImages` 改 `{data, mimeType}` 结构；rendererState 仅从传入定义解析（`ToolRenderers.NewRenderState` 承接 pig-addition）
- `interactive_mode.go`：`getRegisteredToolDefinition` → `tools.WithBuiltInRenderers(toolName, session.GetToolDefinition(name))`（与 pi interactive-mode.ts:2021 对齐；扩展工具同名时获得内置渲染器）

**agent-session（±126 全量）**：
- `_pendingCustomMessages`：`SendCustomMessage` 第四分支（streaming + triggerTurn=false → defer 到 turn 末，防消息插进 tool call/result 之间）；`appendCustomMessage` 抽取；turn_end 冲刷（在扩展/监听派发后）；`runAgentPrompt` defer 与 `Prompt` 新 prompt 前冲刷
- `_compactBeforeNextAssistantResponse` + `installAgentNextTurnRefresh` 集成（压缩先于 previous prepareNextTurn）
- `IsIdle` = !runActive && !IsCompacting（v0.85.1 门控）；`resolveIdleWaitIfIdle` 同步收紧；`Abort` 增 `AbortCompaction`+`AbortBranchSummary`；Compact/branch-summary/auto-compaction 的 defer 全部补 `resolveIdleWaitIfIdle`
- **修复 pig 潜伏 bug**：`runAutoCompaction` 原在函数开头清 `autoCompactionCancel` 而非结束时，完成后 IsCompacting 恒 true——被新 isIdle 语义暴露（pi: finally 语义）
- `addPersistedDefaultToNonEmptyScope`：persist 模型加入非空 scope + enabledModels 追加（SetModel/CycleModel×2 三处 persist 点）

**core 杂项**：
- `agent_session_runtime.go`：fork 非持久路径 teardown 先于 newSession/createBranchedSession，parentSession 用 previousSessionFile；`ImportFromJsonl` 唯一后缀循环 + `O_EXCL` 写入（COPYFILE_EXCL 语义）
- `session_manager.go`：`LoadEntriesFromFile` 尾部半行修复（header 校验后 append "\n"，`sessionLineReader.hadTrailing`）；`loadEntriesLocked` 重构（`_loadEntries`）；构造器 preloaded-entries 分支；`InMemorySession(cwd, options, entries...)` 变参收 entries；`CreateBranchedSession` compaction `firstKeptEntryId` 对被删 label 的重映射（#8989）
- `settings_manager.go`：`TerminalSettings` 增 hyperlinks/images/trueColor（doc-shape，raw map 存储）；`GetTerminalCapabilityOverrides`（pi images:null → pig Images=false）；`fullscreenCopyOnSelect` get/set + doc
- `extensions_types.go`：`UIPromptKind` + `UIPromptStartEvent`/`UIPromptEndEvent`（Title *string）；`extensions_runner.go`：`SetUIContext` 包装 `wrapUIPromptContext`（五个阻塞对话框方法）+ `withUIPrompt` 深度计数（mutex 守卫）+ `emitUIPromptEvent`（goroutine ≙ queueMicrotask）；**修复 Emit 偏差**：pi 的 emit 把最后一个非 nil handler result 传回（session_before_compact 的非 cancel compaction 生效），pig 原先只透传 cancel——`Emit` 改为跟踪 lastResult
- `compaction.go`：`GetSummarizationFailure`（error/length 两种不可持久化停止）；`GenerateSummaryWithUsage`/`GenerateTurnPrefixSummary` 改用之；`CompleteSummarization` 去 `toolChoice:"none"`；`branch_summarization.go`：maxTokens = min(4096, model.maxTokens) + GetSummarizationFailure
- `agent/harness/messages.go`：`BranchSummaryMessage.FromID` → `*string`（v0.85.1 string|null）；`CreateBranchSummaryMessage` 签名跟随；core 四处调用点 lo.ToPtr 或直传
- `model_config.go`：compat 校验器增 `vllmPriority`(number)/`supportsMaxOutputTokens`/`supportsMidConvoEffort`
- `model_runtime.go`：`StreamDeferred`（lazyStream 原样暴露）+ `FetchDeferred` 改薄包装
- `provider_composer.go`：`findModelDefaults`（id 精确 → 同 api → openai-completions → 首个）
- `keybindings.go`：`app.thinking.save`（ctrl+s）
- `skills.go`：`FormatSkillsForPrompt(skills, fileReadTool...)`（bash 变体文案；pig 用变参实现默认值，pi 是显式默认参数）；`system_prompt.go`：`skillFileReadTool`（read 优先、bash 次之）双分支
- `http_dispatcher.go`：仅注释（undici proxyTunnel 无 Go 对应物）
- `utils/tools_manager.go`：fd/rg linux 资产改 musl；`GetLatestVersion` 改 release 页 302 重定向解析（`FetchRetryOptions.NoRedirect` 新增，绕开 api.github.com 匿名配额）；下载失败信息含 URL；`EnsureTool` 错误 cause 链拼接（深度 5 去重）；fd darwin/x64 pin 跳过版本查询
- `utils/shell.go`：Windows taskkill 用 System32 绝对路径（`systemRoot()`，#6596）
- `config.go` `isBundledNode`/extensions loader 的 isBundledNode 化、`index.ts` +5：**跳过**（Go 无 bundled-node 概念；pig codingagent 无 index.go——S3 先例）

**测试（+20 函数，全绿）**：
- 新文件：`tools_manager_test.go`（fetchDo seam + GetLatestVersion 5 例 + EnsureTool cause 链/offline，PATH 置空强制下载路径）、`session_manager_load_entries_test.go`（inMemory+preloaded entries 13 子测试）
- 增量：tools_test（write 文案精确断言 + `TestToolCwdResolution` 7 子测试）；tool_execution_test（3 处 WithBuiltInRenderers 包装 + click-expand 新例）；session_manager_file_operations_test（尾行修复 3 例）；settings_manager_test（终端能力覆盖 + fullscreenCopyOnSelect 持久化）；model_resolver_test（persisted-scoping 3 例）；agent_session_runtime_test（import 同名冲突，harness 抽取为 `newRuntimeHostForTest`）；agent_session_compaction_e2e_test（7048 + compaction-before-next-response 大场景）；branch_summarization_test（clamp/length 2 例 + toolChoice 断言翻转）；compaction_summary_reasoning_test（length 2 例 + toolChoice 断言翻转）；session_manager_labels_test（8989）
- **测试基建**：`createAgentTestSession` 补装 `ConvertToLlm: harness.ConvertToLlm`（pi 的 createHarness 走 SDK 装配；defaultConvertToLlm 会丢 compaction/custom 角色——与 pi defaultConvertToLlm 行为一致，故 harness 必须显式装）
- 既有 tools_test chatty-updates 用例补 mutex（throttle 定时器 goroutine 与主 goroutine 的读竞争，-race 抓出）

**验收**：`go build && go vet && gofmt -l` 干净；全仓 `go test ./... -race -count=1` 全绿（26 包）；测试函数 1031 → **1051**（+20，另有 ~30 子测试）。

**批内取舍声明（归 S5b 或跳过）**：
- 归 S5b：tool-execution/assistant-message 的剩余 mouse 测试面、interactive-mode/interactive-tui/selectors/theme/chat-viewport/status-indicator/footer-data-provider（仅测试驱动方式重构，行为无变）/image-processing（JPEG APP1 探测，src 无变）/llama-extension
- 跳过：model-registry +9（github-copilot 专用，pig 无该 provider）、http-dispatcher.test +60（undici 专属）、7537 文件（确认不在 v0.85.1 tag 树中，区间内加又删；其语义已由 pendingCustomMessages 实现 + 现有 concurrent 测试覆盖）

## 二、下一批定义 = S5b（S5 后半）

范围：interactive-mode + 组件 + theme + 回归测试族。

### 要读的 pi 源文件（`~/Projects/latest_pi/pi/packages/coding-agent/src/`）
- `modes/interactive/interactive-mode.ts`（±296：footer debounce、fullscreen transcript jump-to-end 指示器等；对照 pig `modes/interactive/interactive_mode.go` ~6700 行）
- `modes/interactive/components/assistant-message.ts`（±31：thinking run 点击折叠，v0.84.3 无 mouse）+ `assistant_message.go`
- `modes/interactive/theme/theme.ts`（-160 重构）+ `theme-json.ts`（+146 新）+ `theme-schema.json` → pig `modes/interactive/theme/`
- 其余 components 逐个对照 `git diff 4e58f324f..d981de122 -- packages/coding-agent/src/modes/interactive/`（多数无变化）

### 要移植的 pi 测试（`packages/coding-agent/test/`）
- assistant-message.test.ts（+36 thinking 点击折叠）、chat-viewport +26、footer-data-provider +33（fake-timer 驱动，pig PollWatcher 需适配）、interactive-tui +176、interactive-mode-assistant-diagnostics +67、interactive-mode-compaction +31、image-processing +33、llama-extension +72、model-selector +62、scoped-models-selector +126、scrollbar-theme +24、settings-selector +94、status-indicator +53、theme-controller +18、thinking-selector +55、trust-selector +16、suite/regressions/{167,3217,6949,7253,8611}、package-distribution +10、startup-session-name/stdout-cleanliness ±4

### 依赖与顺序
- S5b 消费 S5a 的 `WithBuiltInRenderers`（interactive 已接线）、UIPrompt 事件族（settings/theme selector 的 ui_prompt 包装）、`GetTerminalCapabilityOverrides`（TUI 能力门控消费点在 S5b 接线）
- `GetFullscreenCopyOnSelect`/`fullscreenScrollbar` 消费点在 tui-alt-screen（S4 已移植）——S5b 检查接线

## 三、风险与坑

- **渲染器拆分的 grep 映射已变**：`renderers/X.ts` ↔ `renderers_X.go`（同包）；后续 sync 批不要再按子目录找
- **`Emit` 返回值语义已修**：非 cancel result 现在会传播。凡 emitXxx 依赖「非 cancel 一律 nil」的旧假设都要复查（已查：EmitMessageEnd/EmitContext 独立实现不受影响）
- **`createAgentTestSession` 现装 ConvertToLlm**：新 session 级测试若断言「custom 角色被过滤」会反转——pi 语义是 custom 角色投给 LLM（user 消息）
- **`InMemorySession` 变参 entries**：旧调用 `InMemorySession(cwd, nil)` 不受影响；需要 entries 时传第三参 `[]FileEntry`（SessionEntry 需 asFileEntries 显式拉宽）
- **compaction 数值平衡**：compaction-before-next-response 测试对 estimateTokens/keepRecentTokens 的数值敏感（firstKept 必须落在 turn 边界避免 split-turn）；调整阈值数字时整组一起调
- 6596 taskkill 的 Go 侧只有 SystemRoot 路径拼接可测（runtime.GOOS 不可 mock），行为测试只能在 Windows 跑——pig 未移植该用例，声明的偏差

## 四、明确不要读的

沿 S3 handoff 第四节全部有效：`src/experimental/`、`src/cli/experimental/`、`src/{client,server}/`、`src/modes/rpc/`、`src/bun/`、pi 的 `packages/agent/src/harness/runtime/`、`packages/chord/`、pi `docs/`、`test/experimental*`、`test/client/*`、`test/server/*`、`test/rpc-*`。
