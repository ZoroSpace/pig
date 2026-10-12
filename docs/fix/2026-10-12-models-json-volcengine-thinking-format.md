# models.json 校验器不认 thinkingFormat "volcengine"

- **日期**：2026-10-12 22:48
- **状态**：已修复，回归测试锁定
- **涉及文件**：`codingagent/core/model_config.go`（枚举补一行）、`codingagent/core/model_config_test.go`（回归测试）

## 现象

用户在 models.json 自定义模型里写 `"compat": { "thinkingFormat": "volcengine" }` 时，整个 models.json 被拒绝：

```
Invalid models.json schema:
  - providers.arkproxy.models.0.compat.thinkingFormat: expected a thinking format literal
```

启动后表现为「No API key found for the selected model」（provider 整个没加载出来），报错信息完全看不出是 thinkingFormat 引发的。

## 根因

`modelsJsonThinkingFormatValues`（model_config.go）是 models.json 校验器的 thinkingFormat 枚举白名单，收录了全部 pi 标准格式（openai/zai/qwen/deepseek/…），但漏了 **pig 自有的 `ThinkingFormatVolcengine`**（ai/types/types.go，内置 volcengine-plan provider 专用，pi 无对应物）。解码端（`decodeCompat` → `ai.OpenAICompletionsCompat.ThinkingFormat *ThinkingFormat`，string 类型无枚举拦截）本来就能通，唯独校验器把它拦了。

内置 volcengine-plan provider 不走 models.json 所以不受影响；受害面是「用户自定义 openai-completions 模型想用 volcengine thinking 格式」（典型场景：通过 models.json 接入火山方舟自建模型/代理）。

## 发现路径

2026-10-12 烧钱对照实验（pig vs pi 对 volcengine-plan deepseek-v4.1-flash 的请求体逐字节 diff）：实验需要把 pig 的 volcengine thinking 格式复刻到 models.json 自定义 provider 上，写 `"volcengine"` 即被拒，临时用 `"zai"`（medium + supportsReasoningEffort:false 下发出的 JSON 逐字节相同）绕过。实验结论本身（KV cache 两边一致、thinking 参数非烧钱主因）见会话记录，未改变 pig 行为。

## 修复

枚举补 `"volcengine": true` 一行，附注释说明 pig-only 来源与发现日期。无行为面变化（校验放行后解码链路原样工作）。

## 回归测试

`TestModelConfigAcceptsVolcengineThinkingFormat`（codingagent/core/model_config_test.go）：

1. models.json 含 `compat: { "thinkingFormat": "volcengine", "supportsReasoningEffort": false }` 的 openai-completions 模型 → `LoadModelConfig` 零错误（锁校验放行）；
2. `decodeCompat` 解出 `*ai.OpenAICompletionsCompat` → `ThinkingFormat == ai.ThinkingFormatVolcengine`（锁解码链路真通，不是只放行）。

**stash 验证**：`git stash push codingagent/core/model_config.go`（只留测试）→ 测试红（报 schema 拒绝）→ `git stash pop` → 绿。全量 `go test ./... -race -count=1 -timeout 600s` 32 包全绿。
