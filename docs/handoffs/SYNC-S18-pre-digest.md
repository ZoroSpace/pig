# S18 — pi v1.1.0 → v1.2.0 同步（系列开工稿 / pre-digest）

- **日期**：2026-10-16
- **状态**：🚧 进行中（2026-10-16 用户确认「都按你说的来」：S18-5 取 **lean**、批次与串并行按本稿执行）
- **进度日志**（每项完成即追加，收口时汇总进 `SYNC-S18-v1.2.0.md`）：
  - `2026-10-16` S18-1 · `7bd01fb92` retry pattern：`ai/utils/retry.go` 正则已改（`provider.?returned.?(?:an.?)?error`）；回归测试 `TestIsRetryableAssistantErrorMatchesUpstreamProviderReturnedError` 已补并窄跑通过
  - `2026-10-16` S18-1 · `d4d53612b` anthropic strict fallback：`buildAnthropicParams` 4 参/3 返、`convertAnthropicTools` 加 `disablePreferredStrictTools`、`streamAnthropicMessages` 请求改重试循环、诊断 `anthropic_strict_tools_fallback`、错误分类器 `isAnthropicStrictToolsLimitError`；测试 `ai/api/anthropic_strict_tool_fallback_test.go` 9 例全绿（含 required 保留 / 非 prefer 组不重试 / 非目标错误不降级 / 二次拒绝 / 取消 / SSE 错误 / 跨请求不泄漏 / 内联 addition 降级）。pi 的 hook-throw 例与 pi-messages 例不适用（pig OnPayload 不抛错；pi-messages adapter 延后）
  - `2026-10-16` S18-1 · `ee409a85f` OpenRouter 个性化目录（**ai 半区**，coding-agent 接线留 S18-2）：新文件 `ai/providers/openrouter_api.go`（`OpenRouterPricing`/`Override`/`TopProvider`/`Model` + `OpenRouterCost` 共享换算，负价/非法/缺失归 fallback）；`ai/providers/openrouter.go` 加 `PersonalizeOpenRouterCatalog` + `openRouterFetch`（测试可替换）+ `openRouterCredentialKey`；`ai/models.go` `RefreshModelsContext.BaseURL` + `runProviderRefreshPhase` 传 `provider.BaseURL()`；生成器 `openrouter_catalog.go`/`types.go`/`fetch.go` 改用 `providers.OpenRouterCost`（价格换算共享、负价归 0）。测试：`ai/providers/openrouter_api_test.go`（pi `openrouter-user-models`）+ `ai/models_test.go` 增 `TestModelsRefreshPassesRegisteredBaseURL`（pi models-runtime 增量）
  - `2026-10-16` S18-1 · `ce950d78f` 删生成器 opus/sonnet/haiku-5-5 硬编码 fallback（models.dev 已收录）；`ai/models_generated.go` 手改 Sonnet 5.5 cacheRead 0.2→0.1；`ai/supports_xhigh_test.go` 期望同步
  - `2026-10-16` S18-1 声明：`0f4b1465a`（codex zstd Uint8Array 拷贝）Go 零操作；`6b07b4e57`（mistral header 超时）pig 无 mistral adapter 跳过；`2a33bbee8`（radius e2e）TS-only 跳过
  - `2026-10-16` S18-2 · `f1b2e77f5` 嵌套 worktree 软链 AGENTS.md：`resource_loader_default.go` `LoadProjectContextFiles` 去重改比对「目录 realpath + 文件名」；移植 pi 整块 11 例 nested-worktree 测试（新文件 `resource_loader_context_files_test.go`）。`e6fb2581d` Windows EPERM：`auth_storage.go` 加 `retryableLockErrorCode`（EPERM 仅 Windows）+ `maxEpermRetries=5` 独立上限 + `authLockIsWindows` 测试缝；4 子测移植。`dbc7ff87c` `NewCompactionStatusIndicator` 加 `colorFn` 覆写参数（interactive 两处传 `embeddedStatusColorFn`，顺抽 `editorEmbedsStatus` 助手）；回归测试 `TestCompactionStatusIndicatorUsesColorFnOverride`。`1cedd3272`（coding-agent 半区）settings 文案改 "Use the terminal cursor instead of Pig's drawn cursor"。`codingagent/... -race` 10 包全绿
  - `2026-10-16` S18-2 · `3e477eab5` `PI_CLIPBOARD_OSC52`：`codingagent/utils/clipboard.go` `isRemoteSession` 增 `PI_CLIPBOARD_OSC52=1` 强制 OSC52；测试 `TestCopyToClipboardForcedOsc52AfterSuccessfulCopy` + `clipboardTextEnv` 补清该变量。`441f5767c` print 模式截断告警：`print_mode.go` stopReason=length 写 stderr 并 exitCode=1（部分文本仍出 stdout）；测试新增子测 + `capturePrintStderr`。`15e7bdc14` 摘要去 thinking：`agent/harness/compaction/utils.go` `SerializeConversation` 删 `[Assistant thinking]`；测试 `TestSerializeConversationOmitsThinking`。`6a51a39d4` 设置模式 `~` 展开：`package_manager_paths.go` `matchesAnyPattern`/`normalizeExactPattern` 走 `utils.NormalizePath`；**顺带修复 Minimatch 参数顺序反置**（八处调用照抄 pi 的 `minimatch(value,pattern)` 与 pig `Minimatch(pattern,text)` 不符，见 `docs/fix/2026-10-16-minimatch-argument-order.md`）
  - `2026-10-16` S18-1 收口：`go test ./ai/... -race -timeout 600s` 7 包全绿；`gofmt -l ai/` 干净
  - `2026-10-16` S18-2 · `ee409a85f` coding-agent 接线：`remote_catalog_provider.go` 抽出 `readCatalog`/`catalogIsNewer`/`requestCatalog` 共享助手，新增 `PersonalizeCatalog` 类型 + `WithPersonalizedCatalog`（`personalizedCatalogProvider`：saved 列表优先、空则回退 builtin、跳过 TTL/etag）；`model_runtime.go` openrouter 分支走个性化目录（radius 原样）。测试 `remote_catalog_provider_test.go` 增 2 例（失败保 saved / 离线还原与空回退）+ 阶段助手 `refreshProviderPhase`。`go test ./codingagent/core/` 绿
  - `2026-10-16` **S18-2 · `a447ba11f` 完成**（磁铁，独占串行）：`BeforeAgentStartTrigger` 新类型（`extensions_runner.go`）+ `BeforeAgentStartEvent` 加 `MessageType`/`Message`（`extensions_types.go`，Pig 保留单结构判别式）；`EmitBeforeAgentStart` 改收 trigger；`agent_session.go` 抽出 `startRun`（顶部置 active/abort → prepareRun → flush → onStarted → 跑循环）+ `prepareRun`（compaction → emit → loadout → starting message/nextTurn/handler 消息/system 补丁）；`Prompt` 尾部改调 `startRun({user,...}, started)`；`SendCustomMessage` triggerTurn 改 `startRun({custom,...})`、deferred 改重派发 `SendCustomMessage`；`handleAgentEvent` `agent_start` 时 `agentRunAbortRequested` → `Agent.Abort()`。**顺带修复 `docs/fix/2026-10-16-system-prompt-sections-dropped-on-next-turn-refresh.md`**：下一轮刷新 `merged` 漏 `Sections`（pi 是 `...runOptions` 展开）致同 run 工具调用后 handler 注入段丢失（影响所有 run）；补一行。测试新文件 `agent_session_custom_message_run_test.go`（用户 prompt 保段 + 自定义 run 5 例）。`go test ./codingagent/... -race` 12 包全绿。**S18-2 全部完成**
  - `2026-10-16` S18-3 · `edb2eb966` stdin buffer 延迟终端回复：`tui/stdin_buffer.go` 加 `terminalReplyHeaderRegex`（OSC 10/11/4/7501、DCS `P>\|`、kitty `_G` 头）+ `isControlString`；`processLocked` 识别到回复头不排超时，超时 flush 把控制串拆成 Alt 前缀 + 重解析；`extractCompleteSequences` 处理 CAN/SUB/ESC 中止。7 例新测试（含每边界切分/跨超时缓冲/裸 introducer 仍有界/legacy Alt 恢复/CAN 丢弃）。stash 验证 5/7 红→绿
  - `2026-10-16` S18-3 · `11f991145`（#10769）：`tui_alt_screen.go` 滚轮分支删 `shouldDeferViewportInputToOverlay` 守卫，未处理的滚轮滚 transcript；键盘分支守卫保留。测试改名 `TestTuiAltScreenGivesViewportKeysToAFocusedOverlay`（只留 viewport keys）+ 新增 `TestTuiAltScreenScrollsTranscriptOnWheelInputAFocusedOverlayDoesNotHandle`
  - `2026-10-16` S18-3 · `78a3c10ba`（#10774）：`tui/terminal_image.go` 加 `detectImagesCapability()`（herdr → `"kitty"`，其余家族仍归 Phase 8 → nil），`GetCapabilities` 接线；herdr 测试期望改 `"kitty"`
  - `2026-10-16` S18-3 · `1cedd3272`（**tui 半区**，与 S18-2 coding-agent 半区同批）：`tui.go` 加 `FakeCursorStart`/`FakeCursorEnd`/`focusedFakeCursor`/`RenderFakeCursor` + `TuiBase.resolveFakeCursors`（硬件光标显示时丢弃紧邻 CursorMarker 的 fake cursor，否则解析为 reverse video）；editor/input 光标改用 `RenderFakeCursor`；alt-screen/main-screen/overlay render 路径接 `resolveFakeCursors`；`utils.go` `ExtractSegments` 边界保留 `\x1b_` APC（CursorMarker）。测试：editor 两断言更新 + `TestTuiFakeCursor` 4 子测 + `TestTuiAltScreenKeepsTheSelectionHighlightAfterAFakeCursorCell`。`go test ./tui/ -race` 全绿
  - `2026-10-16` S18-3 · `7f9e1198f`（**tui 半区**）：`tui/keys.go` 加 `shiftedSymbolKeys`/`modifierNames` + `getLogicalShiftedSymbolCandidate`；`matchesKittySequence` 拆物理/逻辑匹配（`+` 作 shift+`=`，保留 ctrl/alt/super）；`matchesPrintableModifyOtherKeys` 去 `expectedModifier==0` 早退并加逻辑匹配；`parseKeyId` 改前缀循环剥修饰符（支持 `+` 作键名、重复修饰符拒绝、含 `+` 非法）；`MatchesKey` 单字符分支加 modifyOtherKeys；`ParseKey` kitty/modifyOtherKeys 两路加逻辑符号。测试：keys 三处（shifted symbol / ctrl++ / `+`）。`go test ./tui/ -race` 全绿。**S18-3 全部完成**
  - `2026-10-16` S18-4 · `c5f5b3282`（RFC 9207）：`integrations/mcp/oauth.go` `oauthEndpoints` 加 `HasMetadata`（`endpointsFromMeta`→true，2025-03-26 fallback→false）；`completeExchange` 改：无 metadata 时任何一个 `iss` 一律拒绝（`expected=""`），加 `formatIssuer` 助手（空→`none`）。测试 `TestSignInRejectsIssWithoutMetadata`（stash 验证红：旧码放行）/ `TestSignInAllowsOmittedIssWithoutMetadata`（无 metadata 无 iss 放行）+ mock 加 `omitAuthServerMeta`/`omitIss`。`integrations/mcp/ -race` 全绿
  - `2026-10-16` S18-4 声明：`67996ab75`（structuredContent 任意 JSON）go-sdk v1.8.0 `CallToolResult.StructuredContent any` 已宽松 + pig `ToolCallOutput.StructuredContent any` 无二次校验 → **零操作**；`da0eadf04`（刷新取消不重试）传输层在 go-sdk 内 pig 无 seam → **零操作**（记 verify-matrix 待 go-sdk 升级）；`f4cca2dc2`（connect 不列资源）pig manager 本就不列 → **零操作**。**S18-4 完成**
  - `2026-10-16` S18-5 · `7f9e1198f` theme 严格校验：`theme_json.go` 顶层/`colors`/`export` 三处 `additionalProperties:false`（未知键报 `/xxx`），新增 `knownThemeColorKeys` 允许表（含 pig-only `imagePlaceholder`）；测试 `theme_json_test.go` 5 例（未知顶层/色标/export 被拒 + `imagePlaceholder` 放行 + name 含 `/` 拒）。stash 验证顶层用例红→绿
  - `2026-10-16` S18-5 · `7f9e1198f` keybinding 值校验：`tui/keys.go` 新 `IsValidKeyId`（`baseKeyNames` 集合 + 变体修饰符剥离 + 重复修饰符拒绝，对齐 `KeyIdSchema` 语法）；`codingagent/core/keybindings.go` `toKeybindingsConfig` 改用 `IsValidKeyId` 逐值校验（非法静默丢弃）、跳过 `$schema`。测试 `tui/keys_test.go` `TestIsValidKeyId`（合法/非法各一批）+ `codingagent/core/keybindings_test.go` `TestKeybindingsConfigValueValidation`（数字/非法串/重复修饰符/混合数组丢弃、`$schema` 跳过）。stash 验证 keybindings 用例红→绿
  - `2026-10-16` S18-5 声明：`settings-defaults.ts`（`SETTINGS_DEFAULTS` 新表）为纯重构（值不变，仅集中供 schema 复用）→ pig 无对应物、**零操作**；`model-config.ts` 校验（`PositiveTokenCountSchema` 等）+ `settings-manager.ts` 类型搬迁均 schema 侧，pig 运行时无对应校验（lean 跳过）→ **零操作**；`96abe9a5a`（schema URL 改 pi.dev）纯发布管线 → **跳过**；`codemode/index.ts` 与 `settings-selector.ts` 改动为引用共享默认值的行为等价重构 → **零操作**。**S18-5 完成**
- **基线**：pig 当前 = pi v1.1.0（S17 收口）。目标 = **v1.2.0**（`06a466935`）。
  - 注意：pi 仓库 main 已到 **v1.3.0**（`71023aec1`）。按「钉正式 tag、不追 main」策略，本系列只吃 v1.1.0→v1.2.0；v1.3.0 另开。
  - 范围：`git -C ~/Projects/pi log --oneline v1.1.0..v1.2.0`（约 120 commit），**绝大多数落不做清单**（durable `77332f7a3`/`eba849739` 等、chord `0104fef1e`、codemode、pi-env/environment、tool_search、experimental server stack 删除 `7472cda0d`、release/docs/build/issues 工具）。真正要消化 ≈ 30 commit。
- **本版无会话 JSONL / RPC 线格式变更**（`core/session.ts`、`core/messages.ts`、`core/transcript.ts` 零 diff）——风险序第一区（线格式核对）为空，双向兼容门沿用声明核对制。

## 0. 唯一重大决策：配置 schema（#9880 `7f9e1198f` + #10751 `96abe9a5a`）

pi 用 **TypeBox** 把 settings/models/theme/keybindings 的校验 + 默认值 + TS 类型统一成 schema 源，再由 `scripts/generate-schemas.ts` 编译成 `schemas/*.schema.json` 发布到 `https://pi.dev/schemas/*.json`（编辑器补全用）。

- **推荐（lean，默认）**：**不移植** schema 源化与发布管线；只回填**行为 delta**（S18-5）：严格 theme 校验、keybinding 解析、compat/默认值一致性。理由：pig 无 pi.dev 托管、无 TS 消费方、无 TypeBox；全量需自建 struct→JSON Schema 生成器 + 运行时数据驱动校验，~5000 行低 dogfooding 价值。
- **备选（full）**：S18-5 扩为独立大批（schema 定义 + 生成器 + 生成物 + 运行时 schema 校验）。若选此项，S18-5 体量需另拆子批。
- **本稿以 lean 为准**；末尾「给人类的建议」列该决策点，用户可翻转。

---

## S18-1 ai 层

**要读的 pi 源文件**（精确路径）：
- `packages/ai/src/utils/retry.ts`
- `packages/ai/src/api/anthropic-messages.ts`、`packages/ai/test/anthropic-strict-tool-fallback.test.ts`
- `packages/ai/src/models.ts`、`packages/ai/src/providers/openrouter.ts`、`packages/ai/src/providers/openrouter-api.ts`（新）、`packages/ai/scripts/openrouter-catalog.ts`
- `packages/ai/test/openrouter-user-models.test.ts`、`packages/ai/test/models-runtime.test.ts`
- `packages/ai/src/api/openai-codex-responses.ts`
- `packages/ai/scripts/generate-models.ts`、`packages/ai/test/supports-xhigh.test.ts`

**要移植的 pi 测试文件**：`anthropic-strict-tool-fallback.test.ts`、`openrouter-user-models.test.ts`、`models-runtime.test.ts`（增量）、`supports-xhigh.test.ts`（改期望）

| commit | 内容 | pig 落点 | 动作 |
|---|---|---|---|
| `7bd01fb92` | `RETRYABLE_PROVIDER_ERROR_PATTERN` 正则 `provider.?returned.?error` → `provider.?returned.?(?:an.?)?error`（#10842 网关语 "The upstream provider returned an error"） | `ai/utils/retry.go` | 实移（一行）+ 回归断言 |
| `0f4b1465a` | codex zstd body 改 `new Uint8Array(compressed)` 拷贝，保证 plain ArrayBuffer | Go 无对应（`bytes` 即拷贝） | **零操作声明** |
| `d4d53612b` | anthropic strict tool 容量拒绝（400 invalid_request_error + 三条 message 串）→ 关掉 `strict:"prefer"` 工具重试一次；`buildParams` 返回 `{params, hasPreferredStrictTools}`、`convertAnthropicTools` 加 `disablePreferredStrictTools`；诊断 `anthropic_strict_tools_fallback`；已开始的流不重试 | `ai/api/anthropic_messages.go`（`buildParams` 附近行 ~1501/1565/1584）、`ai/api/anthropic_messages_convert.go`（`convertAnthropicTools`:336，加第 6 参）；`ResolveJSONSchemaStrictSampling` 已有 | 实移 + 测试移植（约 288 行 TS 全例）|
| `ee409a85f` | OpenRouter `GET /models/user` 个性化目录：`personalizeOpenRouterCatalog`、`RefreshModelsContext.baseUrl`、价格换算 `openRouterCost` 与生成器共享、负价（router 模型）归 0 | `ai/models.go`（`RefreshModelsContext` 补 `BaseURL` 字段，行 29；refresh 调用点行 508）、`ai/providers/openrouter.go`、**新文件** `ai/providers/openrouter_api.go`、生成器 `ai/scripts/generatemodels/openrouter_catalog.go` | 实移 ai 半区（coding-agent 接线在 S18-2）+ 测试 |
| `ce950d78f` | 删 generate-models 里 opus-5-5/sonnet-5-5/haiku-5-5 硬编码 fallback（models.dev 已收录）；Sonnet 5.5 cacheRead 期望 0.2→0.1 | `ai/scripts/generatemodels/main.go`（126/148/171 三段 fallback + 215 特判）、`ai/models_generated.go`（若重生成；否则手改 Sonnet 5.5 cacheRead）| 实移生成器；生成物按 S14/S16-1 先例手插或整文件重生成（注意 models.dev 漂移）|
| `6b07b4e57` | mistral 超时只用于 header | pig **无 mistral adapter** | **跳过**（适配器按需延后） |
| `2a33bbee8` | radius 思考重放 e2e 测试 | TS-only | **跳过**（测试基建不迁） |

---

## S18-2 agent + coding-agent core

**要读的 pi 源文件**：
- `packages/coding-agent/src/core/agent-session.ts`、`packages/coding-agent/src/core/extensions/runner.ts`、`packages/coding-agent/src/core/extensions/types.ts`、`packages/coding-agent/src/index.ts`
- `packages/coding-agent/test/suite/agent-session-prompt.test.ts`、`test/agent-session-concurrent.test.ts`、`test/extensions-runner.test.ts`
- `packages/coding-agent/src/core/remote-catalog-provider.ts`、`src/core/model-runtime.ts`、`test/remote-catalog-provider.test.ts`
- `packages/coding-agent/src/core/compaction/utils.ts`、`test/compaction-serialization.test.ts`
- `packages/coding-agent/src/core/auth-storage.ts`、`test/auth-storage.test.ts`
- `packages/coding-agent/src/modes/print-mode.ts`、`test/print-mode.test.ts`
- `packages/coding-agent/src/core/package-manager.ts`、`test/package-manager.test.ts`
- `packages/coding-agent/src/core/resource-loader.ts`、`test/resource-loader.test.ts`
- `packages/coding-agent/src/utils/clipboard.ts`、`test/clipboard.test.ts`
- `packages/coding-agent/src/modes/interactive/components/status-indicator.ts`、`src/modes/interactive/interactive-mode.ts`

| commit | 内容 | pig 落点 | 动作 |
|---|---|---|---|
| `a447ba11f` | **自定义消息触发的 run 也走 `before_agent_start`**：`_runAgentPrompt` 拆成 `_startRun`/`_prepareRun`；`BeforeAgentStartEvent` 改 union（`messageType: "user"\|"custom"`，custom 带 `message`）；session 从 setup 起即 busy；starting 期间 abort 在 `agent_start` 生效 | **磁铁** `codingagent/core/agent_session.go`（`runAgentPrompt`:2327、prompt 路径行 2674、`SendCustomMessage`:3201 行 3239/3245、`emitAgentSettled` 附近）、`codingagent/core/extensions_runner.go`（`EmitBeforeAgentStart`:940 签名改 trigger）、`codingagent/core/extensions_types.go`（`BeforeAgentStartEvent`:1158 拆 union）| 实移 + 测试移植（312 行）。**独占串行**（agent_session 磁铁）|
| `ee409a85f` | coding-agent 接线：`withPersonalizedCatalog` + `model-runtime` 对 openrouter 走个性化目录 | `codingagent/core/remote_catalog_provider.go`（仿 `withRemoteCatalog`）、`codingagent/core/model_runtime.go`（builtinProviders map 分支）| 实移 + 测试（92 行）|
| `15e7bdc14` | compaction 摘要去 thinking | `codingagent/core/compaction_utils.go`（pi 源 `compaction/utils.ts`）| 实移 + 测试 |
| `e6fb2581d` | Windows `EPERM`（刚删的 lock 目录 delete-pending）加入可重试码，独立计数上限 5；`ELOCKED` 语义不变 | `codingagent/core/auth_storage.go`（`acquireLockSyncWithRetry`、async 循环）| 实移 + 测试（76 行）。注意 pig lockfile 是自研（`O_EXCL` + mtime stale），Windows 分支需 `runtime.GOOS` 判定 |
| `441f5767c` | print 模式末条 `stopReason==="length"` → stderr 警告 + exit 1（partial stdout 仍写） | `codingagent/modes/print_mode.go`（exitCode 逻辑行 225~274）| 实移 + 测试 |
| `6a51a39d4` | 设置里 `!~/`、`+/`、`-/` 模式先 `normalizePath`（展开 `~`）再 match | `codingagent/core/package_manager.go`（`matchesAnyPattern`、`normalizeExactPattern`）| 实移 + 测试 |
| `f1b2e77f5` | 嵌套 worktree 中软链的 AGENTS.md 被误判 shadow 跳过：改比较「目录 realpath + 文件名」 | `codingagent/core/resource_loader.go`（`loadProjectContextFiles`）| 实移 + 测试 |
| `3e477eab5` | `PI_CLIPBOARD_OSC52=1` 强制走 OSC 52（覆盖无 SSH 标记的远程客户端） | `codingagent/utils/clipboard.go`（`isRemoteSession`）| 实移 + 测试 |
| `dbc7ff87c` | CompactionStatusIndicator 支持 override `colorFn` | `codingagent/modes/interactive/components/status_indicator.go`、`codingagent/modes/interactive/interactive_mode.go` | 实移 |
| `1cedd3272`（coding-agent 半区）| `showHardwareCursor` 语义改为「只用终端光标」（settings 文案 + selector）| `codingagent/modes/interactive/components/settings_selector` 族、README/docs | 实移（TUI 半区见 S18-3）|
| `014873d7d` | codemode 行显示 Elapsed/Took，timer 逻辑移入 `render-utils`；bash renderer 改调用共享 | codemode **不做**；bash 已是 pig 自实现 | **跳过/可选重构**（pig bash 计时 S16-2 已实现，无行为变化）|

---

## S18-3 TUI + keys

**要读的 pi 源文件**：
- `packages/tui/src/stdin-buffer.ts`、`packages/tui/test/stdin-buffer.test.ts`
- `packages/tui/src/tui-alt-screen.ts`、`packages/tui/src/tui-main-screen.ts`、`packages/tui/src/tui.ts`
- `packages/tui/src/terminal-image.ts`
- `packages/tui/src/components/editor.ts`、`packages/tui/src/components/input.ts`、`packages/tui/src/utils.ts`
- `packages/tui/src/keys.ts`、`packages/tui/test/keys.test.ts`

| commit | 内容 | pig 落点 | 动作 |
|---|---|---|---|
| `edb2eb966` | **延迟终端回复串不入键输入**：新增 `TERMINAL_REPLY_HEADER_PATTERN`（OSC 10/11/4/7501、DCS `P>\|`、kitty `_G` 头）；识别到回复头则等到终止符（BEL/ST/CAN/SUB），超时也不 flush；裸 introducer / 歧义 Alt 输入保留有界恢复 | `tui/stdin_buffer.go` | 实移 + 测试移植（131 行，含 fake timer 场景）**重点**|
| `11f991145` | overlay 未处理的滚轮事件改为滚动 transcript，不再当 raw input 给 focused overlay | `tui/tui_alt_screen.go`（`shouldDeferViewportInputToOverlay` 删除点）| 实移 + 测试 |
| `78a3c10ba` | herdr 启用 kitty 图片协议（`images: "kitty"`）| `tui/terminal_image.go`（`detectCapabilitiesFromEnvironment` 的 herdr 分支）| 实移 + 测试改期望 |
| `1cedd3272`（tui 半区）| `showHardwareCursor` 时只用终端光标；`tui.ts`/editor/input 光标绘制分支 | `tui/tui.go`、`tui/components/editor.go`、`tui/components/input.go`、`tui/utils.go` | 实移 + 测试（tui-render/alt-screen/editor）|
| `7f9e1198f`（tui 半区）| kitty/modifyOtherKeys **shifted symbol** 逻辑匹配（`+` 作 shift+`=`）；`parseKeyId` 支持 `+` 作键名、重复修饰符拒绝 | `tui/keys.go` | 实移 + 测试（keys 14 行）|

---

## S18-4 MCP 语义映射（pig 自研路线）

**要读的 pi 源文件**：`packages/mcp/src/oauth/flow.ts`、`errors.ts`、`packages/mcp/src/client.ts`、`packages/mcp/src/transports/streamable-http.ts`、`transport.ts`、`packages/coding-agent/src/extensions/mcp/runtime.ts`、`resources.ts`、`test/oauth.test.ts`、`test/client.test.ts`

| commit | 内容 | pig 落点 / 判断 | 动作 |
|---|---|---|---|
| `c5f5b3282` | RFC 9207：无 authorization server metadata 时，带 `iss` 的授权响应一律拒绝（原逻辑跳过校验）| `integrations/mcp/oauth.go`（自研 flow）| 实移 + 测试 |
| `67996ab75` | `structuredContent` 接受任意 JSON 值（数组/null），不再强制 object；无 output schema 的工具声明为 unknown | `integrations/mcp` 用 go-sdk v1.8.0 的 `CallToolResult`；核对 pig 桥接是否二次校验 | **先核对 go-sdk 版本行为**：若 go-sdk 已宽松则声明，否则 `codingagent/core/mcp_integration.go` 补 |
| `da0eadf04` | 授权刷新期间被取消的请求不重试：per-request send signal，dispatch 前检查 | 传输在 go-sdk `StreamableClientTransport` 内，pig 无此层 | **零操作/声明**（除非 go-sdk 暴露 seam；记入 verify-matrix 待 go-sdk 升级）|
| `f4cca2dc2` | connect 时不再 `resources/list`（避免慢列举阻塞首个 prompt），仅报 `hasResources` | pig `integrations/mcp/manager.go` **本就不在 connect 列资源** | **零操作声明** |

---

## S18-5 配置 schema（lean 版）

**要读的 pi 源文件**：`packages/coding-agent/src/modes/interactive/theme/theme-schema.ts`、`theme-tokens.ts`、`theme.ts`、`packages/coding-agent/src/core/keybindings.ts`、`settings-schema.ts`、`settings-defaults.ts`、`model-config.ts`、`packages/coding-agent/src/main.ts`

| 面 | 内容 | pig 落点 | 动作 |
|---|---|---|---|
| theme 严格校验 | 未知顶层字段/colors/export 键被拒 | `codingagent/modes/interactive/theme/theme_json.go`（当前 `json.Unmarshal` 不设 `DisallowUnknownFields`）、`theme.go` | 实移（`Decoder.DisallowUnknownFields` + 保留 `vars`/自定义色）；确认不误伤 pig-only token（BLOCK-BORDERS/FgOptional）|
| keybinding 校验 | `KeybindingValueSchema` 语法校验 + keys 解析（tui 半区见 S18-3）| `codingagent/core/keybindings.go` | 实移校验 |
| compat/models 校验松紧 | `ProviderCompatSchema` 由 union 改 merged superset + `additionalProperties:true`；模型 schema 集中 | `codingagent/core/model_config.go`（pig 手写校验）| 核对差异，仅回填行为差异 |
| settings 默认值集中 | `SETTINGS_DEFAULTS` 表 | `codingagent/core/settings_manager.go`（pig 已有默认值）| 核对，预期零/极小 |
| `showHardwareCursor` 语义 | 见 S18-2/S18-3 | — | — |
| **不移植** | `schemas/*.schema.json` 生成物、`generate-schemas.ts`、`$schema` URL、TypeBox 源化 | — | 声明跳过 |

---

## 风险与坑

- **S18-1 anthropic**：`convertAnthropicTools` 加参会波及 `anthropic_messages.go` 全部 3 个调用点（1501/1565/1584），漏一处编译即红；`onPayload` 在重试循环内每次都要重跑；诊断类型需在 diagnostics 枚举登记。
- **S18-2 agent_session**：`_startRun`/`_prepareRun` 重构是行为等价迁移 + 新 union，最易漏的是「setup 期间消息入队而非二次 run」与「abort during setup 在 agent_start 生效」两条；pig 并发模型下注意 `_isAgentRunActive` 与锁。收口前必须跑 `TestCompactsAfterToolResultBeforeNextAssistantRequest`（S16 曾在此假红）。
- **S18-3 stdin_buffer**：pig 的 stdin buffer 是终端查询分发核心（见 `docs/fix/2026-09-19-terminal-query-dispatch-deadlock.md`），新回复头 pattern 必须与 `terminal.go` 的查询头对齐；fake timer 测试务必带 `-timeout`，防永阻（见仓库机制）。
- **S18-4**：go-sdk v1.8.0 边界 `structuredContent`/transport 行为未定，先核对再决定实移/声明；不要为了对齐而 fork go-sdk。
- **生成物**：`ai/models_generated.go`、`schemas/` 若整文件重生成会撞 models.dev 实时漂移 → 沿用 S14/S16-1 手插先例，或重生成后 diff 人工核。
- **批次体量**：S18-2 逼近上限（agent_session + remote_catalog + 6 个小 fix + 测试），若超 6000 行先拆。

## 明确不要读的

- `packages/durable/`、`packages/chord/`、`packages/codemode/`、`packages/env/`、`packages/mcp`（pig 自研，只看上表 4 个 commit 的 diff）、`packages/coding-agent/src/experimental/`、`scripts/issues/`、`scripts/*.mjs`、`apps/`
- 旧 `docs/handoffs/S16-*`、S15 及更早（除非核对偏差沿革）

## 收尾动作（系列全绿后）

- Version `1.1.0` → `1.2.0`（`TestVersionTracksPiBaseline` 守卫）
- 双向兼容门：本版无线格式变更，沿用 S17 声明核对制
- 全量 `go test ./... -race -count=1`（单次、`-timeout 600s`）+ build/vet/gofmt
- 冒烟：mock provider / 清凭证（tmux `-e` 传隔离目录，pane 内 `!echo` 抽查）—— 见仓库机制 tmux 条
- `docs/handoffs/SYNC-S18-v1.2.0.md` 收口稿 + AGENTS.md 批次索引/状态行

## 给人类的建议（单列，不擅自改计划）

1. **确认 S18-5 范围**：lean（推荐，如上）还是 full（另拆子批）。这决定系列是 5 批还是 6+ 批。
2. S18 是「先 ai → agent/core → TUI → MCP → schema」的串行顺序；MCP 与 schema 可与前两批并行（文件不相交），但 agent_session 磁铁批必须独占串行，且 S18-1 与 S18-2 因 `ee409a85f` 同源建议相邻做。
3. 若想省事，可把 S18-4（MCP）与 S18-5（schema）合并为「收尾批」，但两者上下文差异大，不建议。
