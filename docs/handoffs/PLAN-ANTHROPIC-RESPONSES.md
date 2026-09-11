# PLAN：anthropic-messages + openai-responses 双 adapter 批（2026-09-11）

- **状态链**：A1 ✅ 2026-09-11（见 `A1.md`）；A2 ✅ 2026-09-11（见 `A2.md`）；B1/B2 待执行（下一批 B1）。执行顺序 A1 → A2 → B1 → B2。
- **收批动作**：每个子批完成后按 §3.5 写 `docs/handoffs/A1.md` / `A2.md` / `B1.md` / `B2.md`，并在本文件头部状态链标注。本文件自身保留为两批的公共设计输入（DeepSeek 靶子矩阵 + 批次划分），不随收批删除。
- **基线**：pi v0.85.1（已核实 2026-09-11 main == tag `d981de122`，无未发版 commit；pig 已完成 S1-S5 同步）。

## 0. 触发与测试靶子（DeepSeek 双端点）

Phase 2 收口时两个 adapter 延后，卡点是「无真机验证渠道」（只能 mock SSE 单测）。2026-09-11 发现 DeepSeek 新增两个兼容端点，**两个 adapter 的真机冒烟都可用现有 DeepSeek key 完成**（此前以为必须 Anthropic/OpenAI 官方 key）：

- **Anthropic 兼容端点**：`base_url = https://api.deepseek.com/anthropic`
  文档：https://api-docs.deepseek.com/zh-cn/guides/anthropic_api
- **Responses 兼容端点**：`base_url = https://api.deepseek.com`
  文档：https://api-docs.deepseek.com/zh-cn/guides/responses_api

已核实：pi v0.85.1 未内置这两个端点（全仓 grep `deepseek.com/anthropic` 无结果），pig 走 **models.json 自定义 provider** 消费，零规格漂移、无需改模型生成脚本即可冒烟。

### 0.1 DeepSeek Anthropic 端点兼容矩阵（2026-09-11 文档快照）

**可真机验证的 adapter 语义**：

| 语义 | 支持度 |
|---|---|
| SSE 流式 | 完全支持 |
| tools / tool_choice（none/auto/any/tool） | 完全支持（`disable_parallel_tool_use` 忽略） |
| thinking + thinking 块 | 支持（`budget_tokens` 被忽略；`output_config` 仅 effort） |
| 图片（base64 jpeg/png/gif/webp、url、file+beta 头） | 支持 |
| system / stop_sequences / temperature[0,2] / max_tokens | 完全支持 |
| server_tool_use / web_search_tool_result 回传 | 支持 |

**只能 mock 验证的语义**（端点测不到，勿因冒烟通过误判正确）：

- `cache_control` 全忽略 → prompt caching 注入/计费语义纯 mock（anthropic-cache-write-1h-cost、long-cache-retention 两测）
- `redacted_thinking`、document、search_result 块不支持
- `anthropic-version` / `top_k` 忽略；`top_p` 仅思考模式生效（下限 0.95），非思考恒 1.0
- 模型映射：`claude-opus*` → deepseek-v4-pro，`claude-haiku/sonnet*` → deepseek-flash，未知模型名 → deepseek-flash（冒烟直接填 `deepseek-flash` 最干净）
- OAuth（该端点走 `x-api-key`）→ anthropic-oauth 测试 mock

### 0.2 DeepSeek Responses 端点兼容矩阵（2026-09-11 文档快照）

**可真机验证**：

- 语义化 SSE 事件流：`response.created` / `response.in_progress` / `response.output_item.added|done`（reasoning/message/function_call/custom_tool_call）/ `response.content_part.added|done` / `response.reasoning_text.delta|done` / `response.output_text.delta|done` / `response.function_call_arguments.delta|done` / `response.completed|incomplete|failed`（终止事件）；事件带 `event` 字段 + 递增 `sequence_number`；**没有 `data: [DONE]`**，终止靠 terminal event——与 pi adapter 既有语义一致（pi 有专门的 openai-responses-terminal-event.test.ts）
- function tools / tool_choice（none/auto/required/指定工具）/ `reasoning.effort` / `instructions`（作首条 system）/ input items（message / function_call / function_call_output / reasoning 明文归并相邻 assistant）/ `input_image`（user/developer 消息 + function_call_output、custom_tool_call_output 输出中）/ usage（`input_tokens_details.cached_tokens`、`output_tokens_details.reasoning_tokens`）/ max_output_tokens / top_logprobs / text.format / user
- 图片限制与对话补全一致（内联单张 32 MiB 等）

**只能 mock 验证**：

- `previous_response_id` / `conversation` / `store`（无状态，响应恒 `store: false`）——pi agent 每轮全量发历史，无碍
- `reasoning.summary` 可传不生成；`encrypted_content` 不支持（reasoning replay 的 encrypted 路径纯 mock）
- custom tool 仅 `{"type":"custom","name":"apply_patch"}`；web_search/file_search/code_interpreter/mcp 等内置工具忽略
- `parallel_tool_calls` 恒开（忽略）；`stream_options` / `truncation` 不支持（超上下文直接 400）
- `system` / `assistant` 消息中的图片 → 400（convertResponsesMessages 的图片路由只允许 user/developer + 工具输出）

## 1. 批次 A：anthropic-messages（拆 A1 / A2）

### A1：adapter 本体 + 核心语义测试

**要读的 pi 源文件（规格书）**：

| 路径 | 行数 | 职责 |
|---|---|---|
| `packages/ai/src/api/anthropic-messages.ts` | 1489 | adapter 本体；基于 `@anthropic-ai/sdk` 的 **beta messages 命名空间**，Go 侧 net/http 原生重写（先例：openai_completions 对 openai SDK 的处理）；SSE 为 `event:`+`data:` 双行事件格式，解析器按 event 行分派 |
| `packages/ai/src/utils/deferred-tools.ts` | 39 | `splitDeferredTools`——deferred 工具与前缀工具拆分（**新前置**） |
| `packages/ai/src/utils/diagnostics.ts` | 45 | `appendAssistantMessageDiagnostic` 等 assistant 诊断收集（**新前置**） |
| `packages/ai/src/api/transform-messages.ts` | — | 参照核对（pig 已有 `ai/api/transform_messages.go`） |
| `packages/ai/src/api/simple-options.ts` | — | 参照核对（pig 已有全套：AdjustMaxTokensForThinking 等） |
| `packages/ai/src/api/anthropic-messages.lazy.ts` | 8 | lazy 注册壳（Go 无 lazy 装载，对照其注册语义落 compat_registry 即可） |

**要移植的 pi 测试（第一批，共 1388 行）**：

- `test/anthropic-sse-parsing.test.ts`（585）——SSE 解析核心
- `test/anthropic-tool-name-normalization.test.ts`（204）
- `test/anthropic-thinking-disable.test.ts`（172）
- `test/anthropic-eager-tool-input-compat.test.ts`（166）+ `test/anthropic-eager-tool-input-e2e.test.ts`（155）
- `test/anthropic-empty-thinking-signature-compat.test.ts`（108）
- `test/anthropic-temperature-compat.test.ts`（103）

**预期 Go 产出**：

- `ai/api/anthropic_messages.go`（adapter 本体；URL 拼接 `baseURL + /v1/messages`，DeepSeek 的 base 直接可用）
- `ai/utils/deferred_tools.go`、`ai/utils/diagnostics.go`（新前置小件）
- 测试贴 `ai/api/anthropic_messages_test.go`（可按主题拆多份 `_test.go`）

**pig 侧已就绪（勿重做）**：`ParseStreamingJSON` / `ParseJSONWithRepair`（`ai/utils/json_parse.go`）、sanitize_unicode / provider_env / provider_retry / error_body / headers / pi_user_agent、`ai/api/simple_options.go` 全套、constrained_sampling、github_copilot_headers、transform_messages；types 面：`ApiAnthropicMessages` 常量（types.go:24）、`AnthropicMessagesCompat`（types.go:1188，含 S3 的 supportsMidConvoEffort）、`Model.UnmarshalJSON` 按 api 分发（types.go:1350）。

### A2：provider 铺开 + OAuth + 剩余测试

**要读的 pi 源文件**：

| 路径 | 职责 |
|---|---|
| `packages/ai/src/providers/anthropic.ts` | Anthropic provider 全量（含 Claude Pro/Max OAuth login flow；pig `ai/providers/anthropic.go` 骨架已留位注释） |
| `packages/ai/src/providers/kimi-coding.ts` / `minimax.ts` / `minimax-cn.ts` / `vercel-ai-gateway.ts` | Anthropic 兼容 coding-plan 系 provider |
| `packages/ai/src/providers/fireworks.ts` | 双协议 provider（completions + anthropic-messages 按 baseUrl 分流） |
| `packages/ai/src/auth/resolve.ts` | OAuth 解析半区（参照） |

**剩余测试（共 1518 行）**：anthropic-auth-token 235、anthropic-mid-conversation-effort 212、transform-messages-copilot-openai-to-anthropic 191、anthropic-oauth 144、anthropic-long-cache-retention-e2e 127、github-copilot-anthropic 125、anthropic-force-adaptive-thinking 123、xiaomi-token-plan-ams-anthropic-empty-signature-smoke 114、anthropic-cache-write-1h-cost 86、anthropic-opus-4-8-smoke 71、anthropic-thinking-binding-e2e 59、anthropic-adaptive-thinking-models 40。

**预期 Go 产出**：`ai/providers/{anthropic(补全),kimi_coding,minimax,minimax_cn,vercel_ai_gateway,fireworks}.go` + `BuiltinProviders()` 扩容（pi alphabetical 顺序）+ ai/auth OAuth 流程补齐。

### 冒烟（A1 收尾即可做，不触真实 Anthropic）

`~/.pig/agent/models.json` 自定义 provider（字段名以 `codingagent/core/model_config.go` 的 ModelsJsonProvider 为准）：

```json
{
  "providers": {
    "deepseek-anthropic": {
      "name": "DeepSeek (Anthropic API)",
      "api": "anthropic-messages",
      "baseUrl": "https://api.deepseek.com/anthropic",
      "apiKey": "<DEEPSEEK_API_KEY>",
      "models": [{ "id": "deepseek-flash", "name": "DeepSeek Flash", "reasoning": [], "input": ["text", "image"], "cost": { "input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0 }, "outputLimit": 8192, "contextWindow": 128000 }]
    }
  }
}
```

验证：`pig -p` 单轮文本 + thinking 开启 + 工具调用（read/write 文件）+ `--mode json` 事件流与 pi 逐键对照。

## 2. 批次 B：openai-responses（拆 B1 / B2）

### B1：adapter 本体 + 核心测试

**要读的 pi 源文件**：

| 路径 | 行数 | 职责 |
|---|---|---|
| `packages/ai/src/api/openai-responses.ts` | 390 | openai-responses adapter 顶层（请求构造 + stream 装配 + copilot/grammar/prompt-cache 钩子） |
| `packages/ai/src/api/openai-responses-shared.ts` | ~750 | `convertResponsesMessages` / `convertResponsesTools` / `processResponsesStream`（input items 双向转换 + SSE 语义事件 → AssistantMessageEvent 归约） |
| `packages/ai/src/api/openai-prompt-cache.ts` | — | 参照核对（pig 已有 openai_prompt_cache.go） |

**要移植的 pi 测试（第一批，共 1779 行）**：openai-responses-compat 599、openai-responses-terminal-event 356、openai-responses-reasoning-replay-e2e 291、openai-responses-namespace 233、openai-responses-tool-result-images 194、openai-responses-partial-json-cleanup 106。

**预期 Go 产出**：`ai/api/openai_responses.go` + `ai/api/openai_responses_shared.go` + 测试。

### B2：provider + catalog 再生成 + 剩余测试

**要读的 pi 源文件**：

| 路径 | 职责 |
|---|---|
| `packages/ai/src/providers/openai.ts` + `openai.models.ts` | OpenAI provider（走 responses；pig `ai/providers/openai.go` 骨架已留位；OPENAI_MODELS 目前被生成脚本跳过） |
| `packages/ai/src/providers/xai.ts` + `xai.models.ts` | xAI（v0.85.1 起 route through Responses） |
| `packages/ai/src/api/azure-openai-responses.ts` | Azure responses 变体（~300 行；随 B2 顺手，或按届时体量单列） |
| `ai/scripts/generatemodels` | catalog 再生成：pig `ai/models_generated.go` 产自 Phase 2（v0.84.3 时点，0 条 gpt-6-astra），B2 需按 v0.85.1 再生成——**AGENTS.md Phase 2 延后项第 4 条（GPT-6 Astra / supportsExplicitPromptCacheMode ttl "30m" / xhigh-max thinking 规则 / missingOpenAiModels+CODEX 列表）在此兑现** |

**剩余测试（共 556 行）**：xai-responses 273、azure-openai-responses-reasoning-replay 136、openai-responses-foreign-toolcall-id 66、openai-responses-empty-tool-result 58、openai-responses-message-id 48、openai-responses-cache-affinity-e2e 31。

**冒烟**：同 A 的 models.json 方式，`api: "openai-responses"` + `baseUrl: "https://api.deepseek.com"`，model `deepseek-flash`。

## 3. 顺序与体量

A1 → A2 → B1 → B2（B1 不依赖 A，可与 A 并行；catalog 再生成固定在 B2）。

| 子批 | pi 侧（源+测试） | 备注 |
|---|---|---|
| A1 | 1489+39+45 源 + 1388 测试 ≈ **2961 行** | Go 膨胀 1.5-2x（openai-completions 先例），Go 侧 ≈ 5-6k |
| A2 | 5-6 provider + OAuth + 1518 测试 | OAuth 若大可再拆 A3 |
| B1 | 390+750 源 + 1779 测试 ≈ **2919 行** | |
| B2 | provider + catalog 再生成 + 556 测试 | catalog 再生成需跑 generatemodels 并逐字段比对 |

各子批均 ≤6000 行上限（§4 批次体量控制）。

## 4. 风险与坑

1. **byte/rune 索引族**（docs/fix/ 三例复发）：SSE 事件拼接、UTF-16 → rune 偏移换算（thinking signature / JSON delta 定位）——凡 strings.Index 下标跨体系使用先想一遍。
2. **值/指针双形态消息族**（docs/fix/ 四例复发）：流式 Partial 快照——openai-completions 曾因 pi 的 `blocks` 与 `output.content` 共享数组引用语义翻车（`2026-09-10-tui-streaming-empty-partial-content.md`），anthropic 的 content_block 增量生长是同一模式，**每次 push 前快照**。
3. **Anthropic SSE 是 `event:` + `data:` 双行**（非 OpenAI 纯 `data:`），按 event 行分派；原生重写不搬 SDK 的重试/重连面（pi 未用），retry 走既有 provider_retry。
4. `input_json_delta` 部分参数累积 + `ParseStreamingJSON`（已有）+ partial-json-cleanup 的清尾语义。
5. thinking signature 透传与 empty-signature compat：DeepSeek 不支持 redacted_thinking，signature 语义只能 mock 验。
6. anthropic beta 头族由 config（`anthropic-beta` header）或 OAuth 态（`claude-code-20250219` + `oauth-2025-04-20`）拼装（anthropic-messages.ts:987/1003）——线格式细节，测试锁定。
7. cache_control 在 DeepSeek 端点恒被忽略——A1 冒烟通过不代表缓存注入逻辑正确，以 mock 测试为准。
8. responses 侧 foreign toolcall id / message id / cache affinity 语义（DeepSeek 端点未必复现，mock 为主）。
9. TUI/AgentSession 消费面预期零改动：新 adapter 走同一 AssistantMessageEvent 协议；assistant_message 的 thinking 折叠/run 已就绪（S5b）。
10. 会话线格式不受影响（adapter 只改 LLM 请求/事件层，jsonl 不动），S1 双向兼容不受扰。

## 5. 验收门（每子批）

- `go build && go vet && gofmt -l` 干净 + `go test ./... -race` 全绿
- 冒烟：DeepSeek 双端点（样例见 §1/§2）+ 既有 deepseek completions 回归不破
- 收批：`docs/handoffs/A1.md`…`B2.md` + AGENTS.md 快照更新 + 本文件头部状态链

## 6. 明确不要读的

沿 S5b handoff 第四节全部有效，另加：

- `packages/ai/src/api/openai-codex-responses.ts`（54KB）+ `providers/openai-codex.ts`——**单列第三批，本双批禁止顺手移植**
- `packages/ai/src/api/{bedrock-converse-stream,google-generative-ai,google-vertex,google-shared,mistral-conversations}.ts`——各自 adapter 到时再说
- `src/experimental/`、`src/{client,server}/`、`src/modes/rpc/`、`src/bun/`

## 7. 给人类的建议（不改计划，仅列出）

1. codex-responses（openai-codex-responses.ts + openai-codex provider + GPT-6 Astra 的 codex 侧）建议单列 C 批，排在 B 之后。
2. opencode / opencode-go / cloudflare-ai-gateway / github-copilot 四家 provider 依赖 catalog 未覆盖的模型数据源，仍按 Phase 2 延后项第 3 条处理，不在 A2/B2 强行铺。
3. 若冒烟中发现 DeepSeek 端点实际行为与 §0 矩阵不符，以实测为准更新本文件并知会——矩阵来自官方文档快照，未经全量实测。
