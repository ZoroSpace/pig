# 真机验证矩阵（A1/A2/B1/B2 交付面 × 实测状态）

- **设立**：2026-09-11，落在 `docs/handoffs/`（随批次交接流转；AGENTS.md Dogfooding 约定 ③ 指向本文件）。**背景**：A1/A2/B1/B2 四批的真机冒烟全部依托 DeepSeek 两个兼容端点（`/anthropic` shim + Responses 端点，`deepseek-flash`）；目标 provider 的真实端点、OAuth 流程、provider 特有语义**均未实测**（当前无对应 key/订阅）。
- **用途**：每拿到一个 key / 订阅，按本表跑对应 probe 销账；发现偏差走 `docs/fix/` 登记 + 回归测试锁定（与 dogfooding bug 同流程）。
- **状态图例**：
  - ✅ 真机已验（打的是该 provider 的真实端点）
  - 🔶 shim 已验（DeepSeek 兼容端点覆盖了**协议层**语义，非目标 provider）
  - 🧪 mock 已验（单测锁定线格式/结构，无真实网络）
  - ❌ 未验

## 0. 已验证通道（销账基线）

| 通道 | 覆盖 | 局限 |
|---|---|---|
| DeepSeek `/anthropic`（A1） | anthropic-messages 协议语义：SSE 双行流、thinking 块、tools/tool_choice、图片输入、usage | 非 Anthropic：cache_control 全忽略、无 redacted_thinking、无真 Claude adaptive thinking、beta 头无协商 |
| DeepSeek Responses（B1/B2） | openai-responses 协议语义：语义化 SSE、function tools、reasoning.effort、input items、usage、input_image 在 function_call_output | 无 `previous_response_id`/`store`、无 `encrypted_content`（加密重放路径恰被绕过）、`prompt_cache_options` 无 5.6 分代行为 |
| DeepSeek 主端点 completions（Phase 2 起） | openai-completions 协议 + deepseekCompat | 不覆盖其他 completions 系 provider 的 compat 分族 |

## 1. Provider 矩阵

| Provider | 协议 | 模型数 | 协议语义 | Provider 特有语义 | Auth 真机 | 解锁条件 |
|---|---|---|---|---|---|---|
| anthropic | anthropic-messages | 14 | 🔶 shim | ❌ cache_control 1h 计费 / redacted_thinking / adaptive thinking（opus-4.6+）/ 5 beta 头协商 / server-side-fallback / tool_reference | ❌ | `ANTHROPIC_API_KEY` 或 Claude Pro/Max OAuth |
| google | google-generative-ai | 22 | 🔶→✅ 部分（2026-09-14：`TestGoogleThinkingDisableGemini3x` 真机 PASS（3-flash-preview thinking 全关 + pong 计数）；CLI 全链路 3.8/3.5-flash pong、`--thinking minimal` 通、`--mode json` 事件流正常、thoughtSignature 真端点观察（pi 差分逐字一致）） | ❌ 3.1-pro 探针（免费档配额 limit:0，等付费；2.5 系探针不再追——新账号 404 deprecated）、thinkingBudget 分代真机、functionResponse 图片嵌套真机；上游知会：3.8-flash 拒 MINIMAL（pi 同样 400，catalog 元数据未表达，不追） | 🔶 AI Studio 免费 key（2026-09-14）已销协议半账；付费档可解锁剩余 | `GEMINI_API_KEY`（已到）/ 付费档 |
| google-vertex（C4） | google-vertex | 14 | 🧪 mock（C4 adapter：SDK 构造配置 10 例 + URL 组合 8 形态（node 探针对 @google/genai 1.52.0 实证）+ tModel/SAFETY/MAX_TOKENS/STOP/thinking 映射；本地 mock CLI 全链路：`/v1/publishers/google/models/{id}:streamGenerateContent`、x-goog-api-key、vertex 字段提升） | ❌ 真 Vertex 端点行为（regional/global/multi-region URL 实路由、ADC 真凭证链 authorized_user/service_account/metadata server、express 限流形态） | ❌（apiKey 或 ADC；ADC 授权链为 pig 原生实现） | `GOOGLE_CLOUD_API_KEY`，或 ADC（`gcloud auth application-default login`）+ `GOOGLE_CLOUD_PROJECT`+`GOOGLE_CLOUD_LOCATION` |
| kimi-coding | anthropic-messages | 4 | 🔶 shim | ❌（kimi 特有 header/行为） | ❌ 设备流 | `KIMI_API_KEY` 或 Kimi 订阅 OAuth |
| minimax | anthropic-messages | 3 | 🔶 shim | ❌ | ❌（纯 apiKey） | `MINIMAX_API_KEY` |
| minimax-cn | anthropic-messages | 3 | 🔶 shim | ❌ | ❌（纯 apiKey） | `MINIMAX_CN_API_KEY` |
| vercel-ai-gateway | anthropic-messages | 236 | 🔶 shim | ❌（网关路由/计费头） | ❌（纯 apiKey） | `AI_GATEWAY_API_KEY` |
| fireworks（anthropic 半区） | anthropic-messages | — | 🔶 shim | ❌（双协议 baseUrl 分流实测） | ❌（纯 apiKey） | `FIREWORKS_API_KEY` |
| fireworks（completions 半区） | openai-completions | — | ✅（协议已被 deepseek 真验） | ❌ detectCompat 分族 | ❌ | `FIREWORKS_API_KEY` |
| openai | openai-responses | 39 | 🔶 shim | ❌ `prompt_cache_options`（仅 5.6 家族接受）/ 官方端点 affinity 头 / grammar tools 穿透 | ❌（纯 apiKey） | `OPENAI_API_KEY` |
| xai | openai-responses | 3 | 🔶 shim | ❌ grok effort 映射 / include encrypted_content 重放 | ❌ 设备流 | `XAI_API_KEY` 或 SuperGrok/X Premium OAuth |
| openai-codex（C2） | openai-codex-responses | 8 | 🧪 mock（C1 adapter 30 函数 + C2 本地 Go mock CLI 全链路：zstd 请求体/Bearer JWT/chatgpt-account-id/prompt_cache_key/WS→SSE fallback） | ❌ 真 ChatGPT backend 行为（WS 传输真机、account 隔离头、end_turn 语义、限流 Retry-After 形态） | ❌ OAuth（设备流+浏览器路 mock 全锁） | ChatGPT Plus/Pro 订阅（`/login` → openai-codex） |
| azure-openai-responses（adapter+39 模型） | azure 变体 | 39 | 🧪 mock | ❌ **整个 URL/deployment/api-version/api-key 头约定**（`normalizeAzureBaseURL`、`/responses?api-version=v1`） | ❌（纯 apiKey） | `AZURE_OPENAI_BASE_URL`/`AZURE_OPENAI_RESOURCE_NAME` + key |

注：openai-completions 系其余 provider（groq/cerebras/together/baseten/nvidia/moonshot/zai/antling/huggingface/openrouter 等，Phase 2 交付）同样只有协议级真验（deepseek）+ detectCompat 单测，provider 真机未打——不在本矩阵范围（它们早于 A/B 批），销账时顺带登记即可。

## 2. OAuth 流程（三家，全部 🧪 mock）

| 流程 | 机制 | 结构锁 | 未验项 |
|---|---|---|---|
| anthropic（Claude Pro/Max） | PKCE + `localhost:53692` 回调服务器；请求携带 `claude-code-20250219` + `oauth-2025-04-20` beta，`Authorization: Bearer` | anthropic-oauth / auth-token 测试 | 真登录：授权页跳转、回调时序、token 端点字段、刷新窗口（5min/15s）、凭证落盘→请求携带全链路 |
| kimi-coding | 设备流（auth.kimi.com，JSON） | oauth-kimi 测试 | 同上（设备码获取/轮询/令牌交换/刷新不轮换语义） |
| xai（SuperGrok/X Premium） | 设备流（auth.x.ai，RFC 8628；`verification_uri` 强制 https、refresh 不轮换沿用旧 token） | oauth_xai.go 结构 + mock | 同上 |
| openai-codex（ChatGPT Plus/Pro，C2） | 双路登录（browser：PKCE + `localhost:1455/auth/callback` 回调服务器 + manual code 兜底 + state 校验；device：usercode 端点 → 轮询 → authorization_code 再走 PKCE exchange，interval 支持数字/字符串）；refresh；toAuth=access 即 apiKey | openai-codex-oauth 测试 12 函数（fake-timer 时钟 seam 驱动轮询节奏/取消/超时/slow_down 无关的 pending/403·404 pending/失败 body 原文） | 真登录：auth.openai.com 授权页、404 禁用文案真值、轮询真节奏、accountId 提取真 JWT、凭证落盘→请求携带全链路 |

**OAuth 真机 probe（手工，任一家）**：`pig` 交互模式 → `/login` → 选目标 provider → 完成流程 → 检查 `~/.pig/agent/auth.json` 新增凭证 → 发一轮真实对话 → 重启 pig 验证刷新路径。

## 3. 现成 probe 清单（key 到手即跑）

| Key | Probe | 命令 | 状态 |
|---|---|---|---|
| `DEEPSEEK_API_KEY` | anthropic shim 三项 | `go test -tags smoke ./ai/api/ -run 'TestAnthropicDeepSeekSmoke' -v` | ✅ 已跑（A1） |
| `DEEPSEEK_API_KEY` | responses 四项 | `go test -tags smoke ./ai/api/ -run 'TestResponsesDeepSeekSmoke' -v` | ✅ 已跑（B2） |
| `OPENAI_API_KEY` | cache-affinity e2e | `go test ./ai/api/ -run 'TestOpenAIResponsesCacheAffinityE2E' -v` | ⏳ 待 key |
| `OPENAI_API_KEY` | reasoning-replay e2e（含 orphaned reasoning、同 provider 异模型交接） | `go test ./ai/api/ -run 'TestOpenAIResponsesReasoningReplayE2E' -v` | ⏳ 待 key |
| ChatGPT Plus/Pro（openai-codex） | codex cache-affinity e2e（存储 OAuth 凭证 → gpt-5.5 SSE） | `go test ./ai/api/ -run 'TestOpenAICodexCacheAffinityE2E' -v` | ⏳ 待订阅 |
| `GEMINI_API_KEY` | thinking-disable e2e（3-flash / 3.1-pro，thinking 全关 + pong 计数；**2.5-flash / 2.5-flash-lite 不再追**——2026-09-14 用户决策：新账号 404 deprecated，模型生命周期问题不属 pig 验证范围） | `go test ./ai/api/ -run 'TestGoogleThinkingDisable' -v` | 🔶 部分（2026-09-14：3-flash-preview ✅；3.1-pro 429 免费档 limit:0 等付费档；顺带修出 env-api-keys 缺口，见 docs/fix/2026-09-13-env-api-keys-fill.md；环境注记：3.x flash 族存在按请求来源 IP 的地区限制（与账号无关）） |
| `GOOGLE_CLOUD_API_KEY` 或 ADC+project/location | thinking-disable vertex e2e 两例（2.5-flash / 3-flash-preview）+ `pig -p --model google-vertex/gemini-2.5-flash` 真端点一轮 | `GOOGLE_CLOUD_API_KEY=... go test ./ai/api/ -run 'TestGoogleVertexThinkingDisable' -v` | ⏳ 待 key/ADC |
| `ANTHROPIC_API_KEY` | 逐模型 probe 族 | `go test ./ai/api/ -run 'TestAnthropicEagerToolInputE2EProbe|TestAnthropicLongCacheRetentionE2EProbe|TestAnthropicThinkingBindingE2E|TestAnthropicOpus48Smoke' -v` | ⏳ 待 key |
| `KIMI_API_KEY` | kimi 模型 probe（eager e2e 的 kimi 分支） | 同上（getE2EAPIKey 按 provider 取 key） | ⏳ 待 key |
| 上述任一 | reasoning 语义 | anthropic thinking 家族单测全部 mock；真机 thinking 观察并入对应 probe | ⏳ |

**缺口（key 到手后可补 probe）**：openai live tool-result-images（pi 有 OpenAI/Azure 分支，pig 目前只有 mock 单测 + DeepSeek smoke）；azure 全链路 probe（待 Azure 资源）。

## 4. 销账流程

1. 跑上表 probe（或 OAuth 手工流程），记录日期 + key 类型（控制台 key / 订阅 OAuth）。
2. 通过 → 本文件对应格改 ✅ 并注明日期。
3. 发现偏差 → `docs/fix/YYYY-MM-DD-<主题>.md` 登记 + 回归测试锁定（stash 验证 FAIL→PASS），本文件该格改 ✅ 并链 fix 文档。
4. pi 与 pig 实测行为不一致且确认 pig 错 → 修 pig；确认 pi 错/文档错 → 记录知会，不追。
5. **README 同步义务（2026-09-14 立）**：销账/降级/不再追若影响 README「Provider：实现 ≠ 验证」分层表的归属，同批更新 README（含顶部 WARNING 与概览表的真实验证行）。
6. 仓库惯例：凡批次或 fix 改动了用户可见面（provider 验证状态、CLI 行为、配置格式等），同批同步 README 对应段；AGENTS.md 台账与 README 二者都要落地。

## 5. 风险备注

- 线格式抄自 pi（有真实使用历史），主要风险在 **pig 重写的 HTTP 细节**（URL 拼接、header 组装、表单编码、轮询状态机）——mock 锁了结构，真机才能暴露端点的实际容忍度。
- **最高风险三件**：① anthropic OAuth 回调全链路（纯 pig 新写 HTTP 服务器）；② openai 5.6 家族 `prompt_cache_options` 分代（发错即 400）；③ azure URL 约定（零真机）。
- 加密 reasoning 重放（`encrypted_content`）是 openai-responses 多轮无状态的关键路径，DeepSeek 端点无法覆盖——拿到 `OPENAI_API_KEY` 后 reasoning-replay e2e 是第一优先。
