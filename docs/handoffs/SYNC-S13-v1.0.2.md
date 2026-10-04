# S13：pi v1.0.1 → v1.0.2 基线同步（单实移批）

**状态**：✅ 完成（2026-10-04）。上游区间 `v1.0.1..v1.0.2`（tag cd32f7725，2026-10-04）：6 commits / 61 files / +760/−166，生产面仅 1 个 commit。**pig 基线现跟踪 pi v1.0.2（Version 已 bump，TestVersionTracksPiBaseline 绿）。**
**性质**：无 wire 结构形态变更（sampling params 是请求参数级）、无 catalog 数据变更（data/*.json 未动）、无新依赖、无 jsonl/session 格式改动。

## 0. 逐 commit 归类

| commit | 内容 | pig 处置 |
|---|---|---|
| `76dfb88f6` | **feat(ai): per-thinking-level sampling parameters（#9776，+249/−30）** | **唯一实移面**，见 §1 |
| `70eceaade` | fix(durable): persist provider session identities（#10424，prompt-cache affinity） | **零操作**——durable 在不做清单（S2 取消，2026-09-21 拍板；harness 上半身冻结） |
| `83692682f` | fix: Nix workflow stable 分支 fast-forward 前拉全量历史 | 零操作（打包面） |
| `750105c80` / `4c6fb7cfe` | changelog 条目搬运 / [Unreleased] 段 | 零操作（pi 自家文档） |
| `cd32f7725` | Release v1.0.2 | → 收尾 Version bump 1.0.2（已落地） |

## 1. 实移面：#9776 per-thinking-level sampling params

语义：OpenAI 兼容三 adapter（openai-completions / openai-responses / azure-openai-responses）支持 `Model.samplingParamsByThinkingLevel`——按 **pi thinking level**（off/minimal/low/medium/high/xhigh/max，非 thinkingLevelMap 的 provider 值）选采样参数覆盖。合并序：先 clamp unsupported level（`clampThinkingLevel`）→ `model.samplingParams` → 该 level 的覆盖 → 请求级 `samplingParams`，逐键后者胜。

pig 落点（全部现成对应物）：

1. `ai/types/types.go`：Model 增 `SamplingParamsByThinkingLevel`（json `samplingParamsByThinkingLevel,omitempty`）+ 命名类型 `SamplingParamsByThinkingLevel map[ModelThinkingLevel]map[string]any`（供 core 引用）。
2. `ai/api/simple_options.go`：新 `ResolveSamplingParams(model, thinkingLevel, requestParams)`（pi `resolveSamplingParams` 1:1：clamp → 三层合并，全空返回 nil）；`BuildBaseOptions` 的 samplingParams 改为 `ResolveSamplingParams(model, options.Reasoning(""→off), options.SamplingParams)`（pi buildBaseOptions 同步改；与 adapter buildParams 的再合并幂等，同 pi）。
3. `ai/api/openai_completions_stream.go` buildParams：两段 merge 循环改 `ResolveSamplingParams(model, options.ReasoningEffort(""→off), options.SamplingParams)`。
4. `ai/api/openai_responses.go` / `azure_openai_responses.go` buildParams：**先算** `reasoningEffort = options.ReasoningEffort ?? (ReasoningSummary != "" ? "medium" : "")`（pi 同构；model.Reasoning 块的现有 effort 逻辑语义不变），merge 改 `ResolveSamplingParams(model, reasoningEffort(""→off), ...)`——summary-only 请求按 medium level 取参（pi 新测试钉死）。
5. `codingagent/core/model_config.go`：`ModelsJsonModel` + `ModelsJsonModelOverride` 增字段（定义/覆盖两 schema，pi 同步加）+ 新 JSON 类型 `ModelsJsonSamplingParamsByThinkingLevel map[string]map[string]any`。
6. `codingagent/core/provider_composer.go`：`ProviderConfigInputModel` 增字段；新 `mergeSamplingParamsByThinkingLevel`（七 level 逐级 {...base, ...override}）+ `samplingParamsByThinkingLevelFromJSON` 接进 applyModelOverride；modelFromJSON 与 ProviderConfigInput.Models 两处透传。
7. 测试：`ai/api/sampling_options_test.go` 家族扩展；`codingagent/core/model_registry_test.go` 存量 sampling e2e 子测升级。

跳过声明：`packages/coding-agent/docs/models.md` 新章节（pi 自家文档，pig 文档面不随动）。

## 2. 实际交付（终稿回填）

### 2.1 改动文件（9）

| 文件 | 改动 |
|---|---|
| `ai/types/types.go` | Model 增 `SamplingParamsByThinkingLevel` 字段 + 命名类型（含语义注释：pi thinking level 而非 thinkingLevelMap 值，仅 OpenAI 兼容 adapter 消费） |
| `ai/api/simple_options.go` | 新 `ResolveSamplingParams`（clamp → model → level → request 逐键后者胜，全空返回 nil）；`BuildBaseOptions` 接入并删除 v0.99.1 的「避免双算」注释 |
| `ai/api/openai_completions_stream.go` | buildParams 两段 merge 循环 → `ResolveSamplingParams(model, options.ReasoningEffort, …)` |
| `ai/api/openai_responses.go` | buildParams 前置解析 `reasoningEffort`（summary-only ⇒ medium）→ 走 ResolveSamplingParams |
| `ai/api/azure_openai_responses.go` | 同 responses（azure 同构） |
| `codingagent/core/model_config.go` | `ModelsJsonModel`/`ModelsJsonModelOverride` 增字段 + `ModelsJsonSamplingParamsByThinkingLevel` |
| `codingagent/core/provider_composer.go` | `ProviderConfigInputModel` 增字段 + `mergeSamplingParamsByThinkingLevel` + `samplingParamsByThinkingLevelFromJSON` + applyModelOverride 接线 + modelFromJSON / ProviderConfigInput.Models 两处透传 |
| `codingagent/config.go` | Version `1.0.1` → `1.0.2` |
| `codingagent/config_test.go` | 基线常量同步（`TestVersionTracksPiBaseline` 绿） |

`Version` 已由 S12-3 的 1.0.0→1.0.1 状态再 bump 至 1.0.2（stash 红→绿已验证）。

### 2.2 测试

- `ai/api/sampling_options_test.go`：新增 5 子测——level 覆盖 model 默认 / reasoning 关闭落 off 参 / stream-option 键胜 level 键 / 三 adapter（completions·responses·azure）各一遍「level 夹在 model 与 request 之间」/ summary-only 请求落 medium 参。
- `codingagent/core/model_registry_test.go`：`TestModelRegistryCustomModelAndModelOverrideCarrySamplingParams` 升级——override 的 per-level 条目与 base 逐键合并（low 混合 base+override、high 只覆盖指定键、max 仅 override），未配置模型 `SamplingParamsByThinkingLevel` 保持 nil。

### 2.3 验收门

- `go build ./...` / `go vet ./...` / `gofmt -l` 全干净。
- 全量 `go test ./... -race -count=1 -timeout 600s` **单次 rc=0，31 包全 ok、零 FAIL**（日志 `/tmp/pig-test-out-s13.log`）。

### 2.4 tmux 冒烟（env -i 清空凭证 + mock provider，零真实 API）

环境：`/tmp/pig-s13-smoke`，agent 目录挂 mock provider（baseUrl `http://127.0.0.1:8941/v1`、`api openai-completions`、`mock-1`），settings 钉 defaultProvider/defaultModel，trust.json 写 `/private/tmp/pig-s13-smoke/work`（ProcessCwd symlink 解析后路径），mock SSE 服务复用 S12-3 脚本改端口。pig 以 `env -i HOME=… PATH=… TERM=… PIG_CODING_AGENT_DIR=…` 启动（凭证环境变量全清）。

四项全通：

1. **header** `▀▀█ █▀▀ v1.0.2`（footer `mock-1`）。
2. **一轮对话**：发 `hello smoke` → mock 回 `pong`，footer `↑1 ↓1`。
3. **退出干净**：`ctrl+d` 退出，进程结束、shell 回归、打印 `To resume this session: pig --session …`。（注：`/exit` 不是 pi/pig 内置命令——pi 仅处理 `/quit`，`/exit` 会被当普通消息发给模型；冒烟以 `ctrl+d` 验证干净退出。）
4. **jsonl 抽查**：assistant usage `input 1 / output 1 / cost.total 0`，且全程请求只落 `127.0.0.1:8941` mock — **零真实 API**。

**附加实证（S13 特性端到端）**：models.json 配 `samplingParams {temperature:0.4, top_p:0.9}` + `samplingParamsByThinkingLevel.high {temperature:1.1, top_p:0.5}`；thinking level `high` 时 wire 实收 `temperature=1.1 top_p=0.5`（level 覆盖生效），默认 off 时实收 `temperature=0.4`（model 默认生效）。

### 2.5 声明（沿用）

- **双向兼容**：本批无 jsonl / wire 结构改动（sampling params 是请求参数级），v3 门 1/2 沿用 S12-3 实测结论、v4 门沿用 S11 收尾批结论；未重跑双向兼容实测。
- **跳过面**：durable provider-session-identity fix（不做清单）、Nix workflow、changelog ×2 均零操作；pi `docs/models.md` 新章节不随动。

## 3. 收尾（已完成）

- Version 1.0.1 → 1.0.2（`codingagent/config.go` + `config_test.go`，`TestVersionTracksPiBaseline` 绿）。
- 验收门：全量 `-race` 单次 rc=0（31 包）；build/vet/gofmt 干净；tmux 冒烟四项全通（§2.4）。
- 文档：本稿回填终稿 + AGENTS.md S13 条目 + 基线表述更新。
- 提交：S13 代码/测试 9 文件 + 本 handoff + AGENTS.md，单一 commit 合入 master。
