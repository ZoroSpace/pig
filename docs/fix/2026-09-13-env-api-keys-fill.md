# 2026-09-14 11:38 — StreamSimple 缺 env key 回填，真机探针全数"无 key"失败（env-api-keys 补齐）

- **状态**：已修复收口
- **涉及文件**：`ai/env_api_keys.go`（新，全量移植）、`ai/compat_registry.go`（withEnvApiKey + StreamSimple 接线）、`ai/env_api_keys_test.go`（新）、`codingagent/core/compaction.go`（过时注释更新）
- **触发**：用户拿到第一个真机 key（GEMINI_API_KEY，AI Studio 免费 key）跑 `docs/handoffs/verify-matrix.md` 的 thinking-disable 探针时。

## 现象

`GEMINI_API_KEY=... go test ./ai/api/ -run TestGoogleThinkingDisableGemini` 三个用例全部失败：

```
stopReason = error (No API key for provider: google), want stop
```

skip 门（读 `GEMINI_API_KEY`）与实际请求取 key 走的不是同一条路：探针直调 `ai.StreamSimple`，adapter 层只认 `options.APIKey`，不查 env。

## 根因

pi 的 `compat.ts` 在 `stream`/`streamSimple` 入口有 `withEnvApiKey`：调用方未显式给 key 时按 `env-api-keys.ts` 的 per-provider 映射（google → `GEMINI_API_KEY` 等 36 项）回填 `options.apiKey`。pig 移植时该段被声明延后（`compat_registry.go` 旧注释：「arrives with the env-api-keys batch」），但**没有任何后续批次把它带进来**——一个静默的结构缺口，直到第一个真 key 才暴露。探针此前全部 skip，CI 永远绿，属于「无真机数据就测不到」的典型。

## 修复

1. `ai/env_api_keys.go` 全量移植 `env-api-keys.ts`：`getApiKeyEnvVars`（github-copilot / anthropic 特判 + 36 项映射表）、`FindEnvKeys`、`GetEnvApiKey`（anthropic 跳过 `ANTHROPIC_AUTH_TOKEN`、Vertex ADC `<authenticated>` 标记、Bedrock 六种 ambient 凭证）。ADC 默认路径探测带缓存（pi 浏览器分支无 Go 对应物，注释声明）；显式 `GOOGLE_APPLICATION_CREDENTIALS` 路径**只查 scoped env 不查进程 env** 的 pi 不对称语义逐字保留。
2. `ai/compat_registry.go` 增 `withEnvApiKey` 并接入 `StreamSimple`：空白 key 视为未设（`hasExplicitApiKey`）、ambient 标记不动 options（vertex/bedrock adapter 自行解析）、回填走浅拷贝（对齐 pi 的 `{...options, apiKey}` 不改调用方状态）。pig 无顶层 `Stream()` 派发器，其孪生接线随该派发器落地。
3. 过时注释同步（`compat_registry.go`、`codingagent/core/compaction.go`）。

## 过程中顺带抓到的移植偏差（修复前被测试逮住）

初版把 pi 的 `envKeys.find(key => key !== ANTHROPIC_AUTH_TOKEN)` 写成「找不到就回落 `envKeys[0]`」——只在 `ANTHROPIC_AUTH_TOKEN` 设置时，pi 返回**无 key**、初版错误返回 auth-token。`TestEnvApiKeysAnthropicAuthTokenNotAnAPIKey` 修复过程中当场红，改写后绿。

## 回归测试

- `ai/env_api_keys_test.go`：pi `env-api-keys.test.ts` 7 用例整体移植 + scoped env 优先级 + Vertex ADC/Bedrock ambient + `TestStreamSimpleWithEnvApiKey` 四子测试（env 回填且不改调用方结构体 / 显式 key 优先 / ambient 标记透传 / 无 key 保持空）。
- **stash 验证**：`git stash push ai/compat_registry.go` 后 `TestStreamSimpleWithEnvApiKey/fills_from_provider_env_var` FAIL（captured APIKey 空）→ pop 后 PASS。
- 全仓 `go test ./... -race -count=1` 28 包 rc=0（2026-09-14，输出 `/tmp/pig-test-out.log`）。

## 真机验证附带发现（非 pig bug，记录知会）

真 key 打通后 pig CLI 全链路（google adapter → SSE → 事件流）真机工作正常：

- `gemini-3.8-flash` 默认档：`pong`；`--mode json` 事件流正常。
- `--thinking off`（3.8-flash）400 `Thinking level MINIMAL is not supported`——**pi 0.85.1 同题同错**（差分实锤，pi 也经 clamp off→minimal→MINIMAL）。根因是 pi catalog 元数据只标 `off: null`，未表达 3.8-flash 不认 MINIMAL；上游模型行为变化，pig 与 pi 一致，不修不追。
- `gemini-3-flash-preview` 探针 PASS（thinking 全关 + pong 计数，`TestGoogleThinkingDisableGemini3x`）；`--thinking minimal` 真机通。
- `gemini-2.5-flash`/`2.5-flash-lite` 404「no longer available to new users」、`gemini-3.1-pro-preview` 429 且免费档配额 `limit: 0`——均为账号档位限制，销账条件记入 verify-matrix。
- 环境注记：Gemini 3.x flash 族存在按请求来源 IP 的地区限制（`User location is not supported`），与账号/凭证无关；同一凭证更换合规来源网络后即消失，非 pig 线格式问题。
