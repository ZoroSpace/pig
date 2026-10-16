# S19 — pi v1.2.0 → v1.3.0 同步（系列开工稿 / pre-digest）

- **日期**：2026-10-16
- **状态**：🚧 进行中（用户确认「定好方案后写 md，然后执行到底，自己 commit push」）
- **进度**：见下「执行进度」节，逐 commit 勾销，全部完成后再写 `docs/handoffs/SYNC-S19-v1.3.0.md` 收口稿
- **基线**：pig 当前 = pi v1.2.0（S18 收口）。目标 = **v1.3.0**（`71023aec1`）。
- 范围：`git -C ~/Projects/pi log --oneline v1.2.0..v1.3.0`（约 70 commit），**绝大多数落不做清单**（durable `f7e3cb543`/`b04299717` 等、chord 整族、env、codemode、Nix/flake、发布脚本 `scripts/*.mjs`、npm 版本钉子、docs）。真正要消化 ≈ 20 commit，全部落在 **ai + coding-agent** 两区；**TUI / MCP 本版 src 零 diff**（仅 package.json 版本号），故不设 TUI/MCP 批。
- **本版无会话 JSONL / RPC 线格式变更**（`core/session.ts`、`core/messages.ts`、`core/transcript.ts` 零 diff）——风险序第一区（线格式核对）为空，双向兼容门沿用 S18 声明核对制。
- **不做清单命中并跳过**：codemode（`3daa20280`）、chord/durable/env 全部、`AssistantMessageFrame`（`b39979799`，pig 无此物，见 S2 取消）、`sessionShareDestination`（`cea257439`，依赖 `/share` 上传链路，pig /share 仍在 deferred-command 清单，见下）、扩展 overlay（`a4f67eb65`，Phase 7 不做）。

## 0. 唯一重大决策：模型目录生成物（models_generated.go）处理方式

S19 的 ai 目录面有 5 个 commit 落在**生成器**（`generate-models.ts`）：opencode baseUrl 迁 `inference`、opencode-go 增 google-generative-ai 支持、xiaomi MiMo thinking 开关、codex gpt-6.1-sol 500K、openai GPT-6 mid-convo effort compat、anthropic off-thinking 模型表。

- **实测**：直接跑 pig 生成器（`go run ./ai/scripts/generatemodels`）对今日 models.dev 重生成，diff = **+5113/−1832 行**（远超 pi v1.3.0 的目录面语义，全为上游多日漂移）→ **整文件重生成不可行**。
- **决策（沿用 S14/S16-1/S18-1 先例）**：**不整体重生成**。① 生成器源码照 pi 1.3.0 改（保证未来 regen 正确）；② `ai/models_generated.go` 按 commit 语义**定点手改**（具体条目见 S19-1 表）；③ golden（`ai/testdata/*.json`）只在被覆盖 provider 的语义确实变化时同步，若上游漂移导致无法逐字节对齐则按 C2 先例记「重构后重生成 golden」决策。
- **golden 覆盖范围提示**：现有 golden 仅 `google / google-vertex / amazon-bedrock / meta / openrouter` 五家。opencode/xiaomi/codex 条目不进 golden，定点手改不影响 golden 测试；openrouter 目录本版无 commit 触碰，预期不动。

---

## 执行进度（2026-10-16，逐 commit 勾销）

已提交 `71eeeaa`（S19-1 前半）：

| commit | 状态 | 落点 |
|---|---|---|
| `4b994d5e5` | ✅ | `ai/utils/json_parse.go` `isValidJSONEscape` 去 `"u"`；`ai/utils/utils_test.go`（`TestRepairJSON`、`TestParseStreamingJSONKeepsInvalidUnicodeEscapes`）|
| `87f9a3641` | ✅ | `ai/utils/json_parse.go` `ParseToolCallArguments`；`ai/types/types.go` ToolCall 加 `ArgumentsError`；`ai/utils/validation.go`；4 处 finalize 接入（anthropic/bedrock/openai-completions/openai-responses-shared）；测试 `ai/utils/utils_test.go`/`validation_test.go`。mistral adapter 未移植跳过 |
| `7efebff18` | ✅ | `ai/utils/retry.go` 加 `unable to forward upstream`；`ai/utils/retry_test.go` |
| `550f1f3ec` | ✅ | `ai/utils/retry.go` `service.?(?:is.?)?(?:temporarily.?)?unavailable`；同测试 |
| `5ab7fe74c` | ✅（实现） | `ai/api/transform_messages.go` `SplitErrorToolResultImages` + `errorToolImagePlaceholder`；`anthropic_messages_convert.go`（convertImageBlock 抽出 + toolResult 收集拆图）；`bedrock_converse_stream.go`（Claude 才拆，其余模型原位保留） |
| `911309429` | ✅（实现） | `ai/api/transform_messages.go` 跨模型 thinking 直接丢弃（不再转 text） |
| `dc939913f` | ✅（实现） | `ai/api/anthropic_messages.go` `buildAnthropicParams` 三返 + `thinkingDisabled`/`effortMessages` 分支 + `managedEffortProvider(model, effortMessages)`；providerThinkingLevel 移出 output 初始化改 built 回填。**生成器 + catalog 未做**（并入 catalog 批） |

**遗留（未提交部分）**：
- 两个旧测试期望待按 pi 同 commit 改：`TestTransformMessagesCopilotOpenAIToAnthropic/converts_thinking_blocks_to_plain_text_when_source_model_differs`（现为丢弃，删/改）、`TestOpenAIResponsesMessageIDConversion/generates_unique_fallback_message_IDs_for_multiple_text_blocks_in_one_assistant_turn`（pi 把首块 thinking 改 text，期望 `[msg_pi_1 msg_pi_1_1]`）。
- 新增测试待补：anthropic off-thinking 两例（disabled 无 effort / off 不支持保持 adaptive）、错误 tool result 图片（anthropic/bedrock）、跨模型 thinking 丢弃。
- 未动 commit：`63035734f`（completions 顺序）、`1692bba63`（gemini-4）、`60ac3bcc3`（responses mid-convo）、`195120a91`/`a3f265b1c`/`6374c2baf`/`5712b134e`（目录/生成器）。

---

## S19-1 ai 层

**要读的 pi 源文件**（精确路径）：
- `packages/ai/src/utils/retry.ts`、`packages/ai/test/retry.test.ts`
- `packages/ai/src/utils/json-parse.ts`、`packages/ai/test/json-parse.test.ts`、`packages/ai/test/validation.test.ts`
- `packages/ai/src/utils/validation.ts`、`packages/ai/src/types.ts`（ToolCall）
- `packages/ai/src/api/anthropic-messages.ts`、`packages/ai/test/anthropic-mid-conversation-effort.test.ts`、`test/anthropic-error-tool-result-images.test.ts`、`test/anthropic-adaptive-thinking-models.test.ts`
- `packages/ai/src/api/bedrock-converse-stream.ts`、`packages/ai/test/bedrock-convert-messages.test.ts`
- `packages/ai/src/api/transform-messages.ts`、`packages/ai/test/transform-messages-cross-model-thinking.test.ts`
- `packages/ai/src/api/openai-completions.ts`、`packages/ai/test/openai-completions-reasoning-details.test.ts`、`openai-completions-tool-choice.test.ts`
- `packages/ai/src/api/google-shared.ts`、`packages/ai/test/google-thinking-level-map.test.ts`
- `packages/ai/src/api/openai-responses.ts`、`openai-responses-shared.ts`、`packages/ai/test/openai-responses-mid-convo-effort.test.ts`、`openai-responses-partial-json-cleanup.test.ts`
- `packages/ai/src/providers/compat-schema.ts`、`providers/kimi-coding.ts`、`providers/opencode-go.ts`
- `packages/ai/scripts/generate-models.ts`、`packages/ai/test/xiaomi-models.test.ts`、`test/supports-xhigh.test.ts`

| commit | 内容 | pig 落点 | 动作 |
|---|---|---|---|
| `4b994d5e5` | json-parse：`VALID_JSON_ESCAPES` 去掉 `"u"`，`\usepackage` 类非法 `\u` 不再当合法转义（partial fallback 不再丢字段） | `ai/utils/json_parse.go`（`isValidJSONEscape`/`RepairJSON`） | 实移 + 测试 |
| `87f9a3641` | 完成的 tool call 参数严格解析：新 `parseToolCallArguments` → `{arguments, argumentsError}`；非法则 `arguments={}` + `argumentsError=原因+原文`；`validateToolArguments` 见到 `argumentsError` 抛错；4 adapter（anthropic/bedrock/mistral/openai-completions/openai-responses-shared）改用 `Object.assign(block, …)` | `ai/utils/json_parse.go`、`ai/utils/validation.go`、`ai/types/types.go`（ToolCall 加 `ArgumentsError`）、`ai/api/anthropic_messages*.go`、`bedrock_converse_stream.go`、`openai_completions*.go`、`openai_responses_shared.go` | 实移 + 测试。**mistral adapter pig 未移植 → 该处跳过** |
| `7efebff18` | retry pattern 加 `unable to forward upstream` | `ai/utils/retry.go` | 实移一行 + 断言 |
| `550f1f3ec` | retry pattern `service.?unavailable` → `service.?(?:is.?)?(?:temporarily.?)?unavailable` | `ai/utils/retry.go` | 实移一行 + 断言 |
| `63035734f` | openai-completions：一个 delta 同含 reasoning+content 时先处理 reasoning（thinking 块先于 text），`reasoning_details` 一并提前 | `ai/api/openai_completions_stream.go` | 实移 + 测试 |
| `1692bba63` | `usesGoogleThinkingLevel` 增 Gemini 4+（`gemini-(?:[4-9]|[1-9]\d)`） | `ai/api/google_shared.go` | 实移 + 测试 |
| `dc939913f` | anthropic `off` thinking：`buildParams` 返 `providerThinkingLevel`；`thinkingDisabled` 分支发 `{type:"disabled"}` 且无 effort marker/block_binding；providerThinkingLevel 移出 output 初始化、改由 built 回填；生成器 `ANTHROPIC_DISABLED_THINKING_MODELS`（opus-5/haiku-5.5）不设 `off:null` | `ai/api/anthropic_messages.go`、`ai/scripts/generatemodels/metadata.go`、`ai/models_generated.go`（对应模型 thinkingLevelMap） | 实移 + 测试。**依赖已存在的 SupportsMidConvoEffort** |
| `5ab7fe74c`（net；取代 `34a8bb38b`） | anthropic/bedrock 错误 tool result 图片：拆出图片，tool result 内替换占位、图片在整轮 tool results 之后以带 caption 的 text+image 追加 | `ai/api/transform_messages.go`（新 `SplitErrorToolResultImages` + `ERROR_TOOL_IMAGE_PLACEHOLDER`）、`anthropic_messages*.go`、`bedrock_converse_stream.go` | 实移 + 测试 |
| `911309429` | transform-messages：跨模型 thinking 块**直接丢弃**（不再转纯文本）；同模型保留 redacted/signed；空文本丢弃 | `ai/api/transform_messages.go` | 实移 + 测试 |
| `60ac3bcc3` | openai-responses GPT-6 mid-convo effort：`supportsMidConvoEffort` 时把 thinking level 作为 `configuration_update` 输入项（保持 reasoning.effort 固定 `medium`，缓存不失效）；ChatGPT Sign-In 跳过；每条回复盖 `providerThinkingLevel`；compat schema 增字段；生成器 GPT-6 家族表 | `ai/api/openai_responses.go`、`openai_responses_shared.go`、`ai/types/types.go`（compat）、`ai/scripts/generatemodels/metadata.go`、`ai/models_generated.go`（openai gpt-6 家族 `supportsMidConvoEffort`） | 实移 + 测试（pi 204 行）|
| `195120a91` | opencode 系 baseUrl `zen`→`inference`（`opencode.ai/inference`、`/inference/go`）；opencode-go 增 `google-generative-ai`；minimax-m2.7 改走 anthropic-messages；classifier Jev baseUrl | `ai/providers/opencode.go`、`opencode_go.go`、`ai/scripts/generatemodels/*`（basePath）、`ai/models_generated.go`（opencode/opencode-go 条目 baseURL + minimax api + classifier Jev） | 实移 + 测试 |
| `a3f265b1c`（net；`b06c1f2cf`→`6a1f799b6`→`a3f265b1c`） | kimi-coding provider：anthropic-messages → **openai-completions**，baseUrl 加 `/v1` | `ai/providers/kimi_coding.go`、`ai/scripts/generatemodels/*`、`ai/models_generated.go`（kimi-coding 条目 api/baseUrl） | 实移 + 测试 |
| `6374c2baf` | xiaomi MiMo：compat `supportsReasoningEffort:false`，thinkingLevelMap 仅 `off`/`high`，移除 `reasoning_effort` 生成 | `ai/scripts/generatemodels/metadata.go`、`ai/models_generated.go`（xiaomi 系条目 compat/thinkingLevelMap） | 实移 + 测试（`xiaomi-models.test.ts`）|
| `5712b134e` | codex `gpt-6.1-sol` contextWindow 272000→500000 | `ai/scripts/generatemodels/codex_models.go`、`ai/models_generated.go`（openai-codex gpt-6.1-sol） | 实移 + 测试 |
| `b39979799` | 删 `AssistantMessageFrame` API | pig 无该文件（S2 已取消 harness） | **零操作声明** |

**要移植的 pi 测试**：`json-parse.test.ts`、`validation.test.ts`（增量）、`retry.test.ts`（增量）、`openai-completions-reasoning-details.test.ts`、`google-thinking-level-map.test.ts`、`openai-responses-mid-convo-effort.test.ts`、`openai-responses-partial-json-cleanup.test.ts`、`anthropic-error-tool-result-images.test.ts`、`transform-messages-cross-model-thinking.test.ts`、`bedrock-convert-messages.test.ts`（增量）、`anthropic-adaptive-thinking-models.test.ts`（改期望）、`anthropic-mid-conversation-effort.test.ts`（改）、`openai-completions-tool-choice.test.ts`（改）、`supports-xhigh.test.ts`（改）、`xiaomi-models.test.ts`（改）

---

## S19-2 coding-agent core 小修

**要读的 pi 源文件**：
- `packages/coding-agent/src/core/agent-session.ts`、`test/default-tools-setting.test.ts`
- `packages/coding-agent/src/core/prompt-templates.ts`、`test/prompt-templates.test.ts`
- `packages/coding-agent/src/core/tools/render-utils.ts`、`tools/renderers/read.ts`、`modes/interactive/components/tree-selector.ts`、`core/export-html/template.js`、`test/read-line-range-display.test.ts`

| commit | 内容 | pig 落点 | 动作 |
|---|---|---|---|
| `6f2f164ab` | resume 恢复 transcript 记录的 loadout：构造器改 `if usesDefaultTools → restoreToolsFromTranscript(defaultToolModifiers)`；`restoreToolsFromTranscript(modifiers)` 对 names 走 `applyToolModifiers` | `codingagent/core/agent_session.go`（`restoreToolsFromTranscript`、构造调用点、`activate...` 5122 附近） | 实移 + 测试 |
| `9fe3f3716` | prompt templates 保留空引号参数（`""`/`''` 不再被丢，位置默认值不偏移）：引入 `hasToken` | `codingagent/core/prompt_templates.go`（`ParseCommandArgs`） | 实移 + 测试 |
| `910149912` | read 行范围标签跨 TUI/导出/示例统一：新 `FormatReadLineRange(offset,limit)`（容忍数字串/null，非法范围省略）；read renderer、tree-selector、export-html 模板共用 | `codingagent/core/tools/render_utils.go`、`tools/renderers_read.go`、`components/tree_selector.go`、`core/export_html/*`（模板资源）| 实移 + 测试 |
| `cea257439` | `sessionShareDestination` 设置（auto/gist/radius）+ /share 目标 | pig `/share` 上传链路（`shareSession`/`tryShareViaRadius`）在 deferred-command 清单、未实现 | **跳过声明**（无 target，设 `sessionShareDestination` 为死键；与 /share 同批再迁）|
| `a4f67eb65` | 扩展 `ctx.ui.custom({overlay})` done() 关自己的 overlay | Phase 7 扩展系统不做 | **跳过**（无 pig 面）|
| `3daa20280` | codemode 取消 | codemode 不做 | **跳过** |

---

## S19-3 model-resolver 统一解析器（磁铁）

**要读的 pi 源文件**：`packages/coding-agent/src/core/model-resolver.ts`、`cli/credential-print.ts`、`main.ts`、`test/model-resolver.test.ts`、`test/credential-print.test.ts`

| commit | 内容 | pig 落点 | 动作 |
|---|---|---|---|
| `d7f7e0dfe` | 单一解析器：新 `resolveModelReference(request, models, usable)`（先收集全部候选，再按 usable/provider 选，杜绝 catalog 顺序决定 provider、thinking 后缀换 provider、歧义 exact 退化为 fuzzy）；`findExactModelReferenceMatch`/`parseModelPattern`/`tryMatchModel` 重写；scope 解析增 `ambiguous` 诊断；`resolveCliModel` 增 `inlineApiKey`；`findInitialModel` 去 `cliProvider/cliModel`；credential-print 改用 `resolveModelReference` | `codingagent/core/model_resolver.go`（磁铁，899 行）、`codingagent/cli/credential_print.go`、`cmd/pig/main.go`（对应 pi main.ts）| 实移 + 全量测试重写（pi 674 行）+ `add` 去重/reasoning`CustomModelID` |

**风险**：`model_resolver_test.go` pi 侧几乎整文件重写；pig 调用点（`interactive_mode.go` 3 处、`cmd/pig/main.go`）需对齐新签名。**独占串行**。

---

## 风险与坑

- **生成物 hand-edit**：`ai/models_generated.go` 44k 行，定点手改务必锁定唯一锚点（provider 段 + 模型 id）；改完 `grep -c` 复核条数，勿整体重生成（见 §0 实测漂移 5113 行）。
- **anthropic `buildParams` 重构**（`dc939913f`）：pig 的 `buildAnthropicParams` 与 pi TS 结构可能不同（S18-1 加过 strict fallback 第 4/3 返），落点需先读实际签名；`providerThinkingLevel` 回填要接到 pig 的 output 构造。
- **openai-responses `configuration_update`**：pig 的 `convertResponsesMessages` 是 ported 函数，需加 `configurationUpdateEffort` 选项 + `providerThinkingLevel` 记录；注意 `buildParams` 签名级联。
- **argumentsError 家族**：新字段 `ArgumentsError` 必须带 `json:"argumentsError,omitempty"`（线格式）；4 处 `Object.assign(block, parseToolCallArguments(...))` 对应 pig 需按值赋 `Arguments` + `ArgumentsError`，注意双形态（ToolCall 在 pig 为值/指针）。
- **kimi-coding/opencode**：baseUrl/api 变更会改 `ai/providers/*.go` 与生成器 basePath；kimi 的 `/coding/v1/v1/messages` 坑（pi 曾 revert）保证只改一次到位。
- **S19-3 磁铁**：`findInitialModel` 在 pig 可能无调用点（dead port），签名改动仍需保持编译；credential-print 测试基建。
- **批次体量**：S19-1 逼近上限（14 commit + 生成器 + catalog + 大量测试），若超 6000 行按「ai 行为 vs 目录生成」再拆。

## 明确不要读的

- `packages/durable/`、`packages/chord/`、`packages/codemode/`、`packages/env/`、`packages/mcp/src`（零 diff）、`packages/tui/src`（零 diff）、`scripts/*.mjs`、`packages/coding-agent/src/experimental/`、Nix/flake、`docs/`
- 旧 handoff S18 及更早（除非核对偏差沿革）

## 收尾动作（系列全绿后）

- Version `1.2.0` → `1.3.0`（`TestVersionTracksPiBaseline` 守卫）
- 双向兼容门：本版无线格式变更，沿用 S18 声明核对制
- 全量 `go test ./... -race -count=1 -timeout 600s`（单次）+ build/vet/gofmt
- 冒烟：mock provider / 清凭证（tmux `-e` 传隔离目录，pane 内 `!echo` 抽查）—— 见仓库机制 tmux 条
- `docs/handoffs/SYNC-S19-v1.3.0.md` 收口稿 + AGENTS.md 状态行/批次索引
- **打 annotated tag `v1.3.0` 并 push**（AGENTS 收尾动作条：v1.0.1/v1.1.0/v1.2.0 曾漏打）

## 给人类的建议（单列，不擅自改计划）

1. **生成物策略**（§0）默认走「不整体重生成 + 定点手改」，与 S14/S16-1/S18-1 一致。
2. **`sessionShareDestination` 跳过**：pig `/share` 上传链路 endors deferred。若想同时把 `/share` 从 deferred 拉起，可另开 feat 批，S19 不捆绑。
3. 顺序：S19-1（ai）→ S19-2（小修）→ S19-3（resolver 磁铁）。S19-2 与 S19-3 文件不相交，可并行（但 resolver 独占串行）。
