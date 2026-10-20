# S20 — pi v1.3.0 → main@73f8de80b（pre-v1.4.0）同步（开工稿 / pre-digest）

- **日期**：2026-10-20
- **状态**：✅ 完成（2026-10-20；收口稿 `docs/handoffs/SYNC-S20-pre-v1.4.0.md`）
- **基线**：pig 当前 = pi v1.3.0（S19 收口）。目标 = **pi main `73f8de80b`**（2026-10-19，`fix(ai): recover from Copilot request-size overflow`），最新正式 tag 仍为 `v1.3.0`。
- **范围**：`git -C ~/Projects/pi log --oneline v1.3.0..73f8de80b`（43 commit）。其中 30 个落不做清单（durable/chord/env 整族、telemetry 新族、`scripts/*.mjs` 发布管线、changelog/ignore/docs 生成），实移 13 个，落 **ai / coding-agent / tui** 三区。
- **版本**：不 bump。pi 未发新 tag，`codingagent.Version` 保持 `1.3.0`（与 `TestVersionTracksPiBaseline` 一致）。
- **线格式**：本版 `core/session.ts`、`core/messages.ts`、`core/transcript.ts`、RPC 面零 diff → 双向兼容门沿用 S18/S19 声明核对制。

## 0. 决策：模型目录处理（沿用 S14/S16-1/S18-1/S19）

pi 的目录数据由 `scripts/generate-models.ts` 在构建时从 models.dev 生成（`src/providers/data/*.json` 不入库），本版 pi 的 `models.generated.ts` 与 `providers/*.models.ts` **零 diff**，所以目录类 commit 只改生成器源码/测试期望。

pig 策略：
1. **生成器源码照 pi 改**（保证未来 regen 行为一致）。
2. `ai/models_generated.go` 只补「新默认值/新机位必须存在」的条目：**MiniMax-M3.1-Flash（minimax + minimax-cn）**、**nvidia/nemotron-3.5-super-vl-120b-a12b**。用 pig 生成器对当前 models.dev 跑一次、仅抽取这两个 model block（跑前备份、跑后还原整文件），不整体重生成（S19 实测漂移 +5113/−1832 行）。
3. GLM-4.6V cacheRead 0→0.05 仅是 pi 测试对齐 models.dev 数据漂移，pig 无对应测试，**零操作**。

## 1. 实移 commit 表（逐 commit 勾销）

### S20-1 ai 层

| commit | 内容 | pig 落点 | 动作 |
|---|---|---|---|
| `0fbdbd3f8` | retry 增 node-fetch `request to … failed, reason:`（Google Vertex ADC token 刷新） | `ai/utils/retry.go`（`retryableProviderErrorPattern`） | 实移一行 + `ai/utils/retry_test.go` 两例 |
| `698a2d23b` | retry 增 `upstream.?request.?failed` / `upstream_stream_read_error` / `stream was interrupted` / `http/?2 stream failed` | 同上 | 实移四行 + 测试四例 |
| `4dd2b149d` | 纯空白 tool result 视为空（openai-completions/openai-responses/google 三处 `hasText` 用 `strings.TrimSpace`） | `ai/api/openai_completions_convert.go`、`ai/api/openai_responses_shared.go`、`ai/api/google_shared.go` | 实移三处 + 测试（completions/responses/google 各一例） |
| `73f8de80b` | 任意 HTTP 413 视为 request-size overflow（provider 无关；前缀/裸状态码两种形态；仅 error 态） | `ai/utils/overflow.go`（`overflowPatterns` 增 `^(?:[\w -]+ API error \(413\):|413(?:\s|:|$))`）；`ai/utils/overflow_test.go` | 实移 + 正反例测试；Cerebras bodyless 测试收窄为只测 400 |
| `d2e7d8b7c` | MiniMax 直连：allowlist → denylist（隐藏 M2/M2.1/M2.5/M2.5-highspeed）；effort 模型走 adaptive thinking（`thinkingLevelMap` + `compat.forceAdaptiveThinking`） | `ai/scripts/generatemodels/process.go`（`processMinimaxModels`）、`ai/scripts/generatemodels/main.go`（删 allowlist 过滤） | 实移；`ai/models_generated.go` 补 M3.1-Flash 两条；测试 `ai/api/anthropic_adaptive_thinking_models_test.go` 增两例、`ai/providers/xiaomi_models_test.go` 等 |
| `99fe14897` | Xiaomi 测试模型 `mimo-v2.5*` → `mimo-v2.6*` | `ai/api/*_test.go`、`ai/providers/xiaomi_models_test.go`、`ai/utils/overflow_test.go` 中同款引用 | 实移（仅测试常量） |
| `90d863f7c` | coding-agent 默认模型 Xiaomi → `mimo-v2.6-pro` | `codingagent/core/model_resolver.go` 四行 | 归 S20-2 |
| `95364a54e` | NVIDIA 默认模型 → `nvidia/nemotron-3.5-super-vl-120b-a12b` | `codingagent/core/model_resolver.go` 一行 + 目录条目 | 归 S20-2 |
| `482c1381a` | GLM-4.6V cacheRead 测试期望 0.05 | pig 无该测试；目录为数据快照 | **零操作声明** |
| `e86b05475` | OpenRouter 测试改用 `deepseek-r1-0528` | 检查 pig 对应测试；无该引用则零操作 | 核对后声明 |

### S20-2 coding-agent

| commit | 内容 | pig 落点 | 动作 |
|---|---|---|---|
| `6fcb48670` | compaction 摘要移除固定输出上限（不再按 `reserveTokens` 算 maxTokens；`generateSummary*` 参数保留但忽略；turn-prefix 删除参数；branch summary 删 4096 cap） | `codingagent/core/compaction.go`、`codingagent/core/branch_summarization.go` | 实移 + 改测试（`compaction_summary_reasoning_test.go` 删 maxTokens 断言、`branch_summarization_test.go` 删两例 cap 断言） |
| `28bea006b` | Windows Store alias：`existsSync` → `access(F_OK)`（`shellPathExists`） | `codingagent/utils/shell.go`（`os.Stat` → `os.OpenFile`/`unix.Access` 等价；统一走 helper） | 实移 + 新增 `codingagent/utils/shell_path_exists_test.go` |
| `61060ddf8` | PowerShell 安装目录发现（`ProgramFiles\PowerShell\7\pwsh.exe`、`SystemRoot\System32\WindowsPowerShell\v1.0\powershell.exe`）；legacy WSL 按 `SystemRoot` 判定；`SystemRoot` 空串回退 `C:\Windows` | `codingagent/utils/shell.go`（`isLegacyWslBashPath`、`GetPowerShellConfig`、`killProcessTree` 对应处）、`codingagent/utils/tools_manager.go`（`SystemRoot ?? WINDIR` → 仅 `SystemRoot`） | 实移 + 新增 `powershell_config_test.go` / `windows_shell_config_test.go` 等价测试 |
| `90d863f7c` | 默认 Xiaomi → `mimo-v2.6-pro` | `codingagent/core/model_resolver.go` | 实移 |
| `95364a54e` | 默认 NVIDIA → Nemotron 3.5 Super VL | `codingagent/core/model_resolver.go` + `ai/models_generated.go` 条目 | 实移 |

### S20-3 tui

| commit | 内容 | pig 落点 | 动作 |
|---|---|---|---|
| `0ed1b0431` | Rex（`TERM_PROGRAM=rex`）启用 OSC 8 + truecolor，images 关；在 outer-terminal 检测前判 | `tui/terminal_image.go`（`detectTrueColorCapability`、`detectHyperlinksCapability`） | 实移两分支 + `tui/terminal_image_test.go` 等价测试 |

## 2. 跳过声明（不做清单/未移植面）

- durable `6085986e4`/`c4cf7957b`/`790271cd0`/`5c1736f82`/`7ec1f648e`/`2331785c3`/`f75b3240a`/`ea7c64843`/`de47e488f`/`26b5535a5`/`ec1be23eb`/`00bf7193d`/`e17e2e105`/`a30c2a81d`/`440a5fcdb`/`5ba1cbaf3`/`91b33cab3`/`a30c2a81d` — durable 整体不做。
- chord `c268154ca`、`d9025a5f5`（SQLite telemetry store）— chord/telemetry 不做。
- env `14ff455a0` — pi-env 不做。
- telemetry 族 `48909d987`/`1402973a9`/`2ec55d808`（含 ai `telemetry.ts`、`lazyStream` observer、options 改名 `telemetryContext → telemetry`）— Phase 8 telemetry 待用户拍板；pig 的 `lazyStream` 无 observer、`ProviderRequestOptions` 无 telemetry 字段，整体跳过。
- `4954d7605`（catalog publish 校验脚本）、`8efd314fa`（models.json reference 生成 + schema 描述/default 注解）— 发布/schema 管线未移植；`CompatSchemasByApi` 增 azure/codex 两项对 pig 的三选一 compat 联合校验等价（两者复用 responses schema），零操作。
- `28bea006b` 文档段、`0b102787c`（扩展 guide）、`ae7b16d43`（changelog）、`e28e70804`（ignore `.pi/settings.json`）— 文档/仓库杂项，跳过。

## 3. 风险与坑

- **MiniMax catalog 条目**：`getEffortThinkingLevelMap(["low","medium","high","xhigh","max"])` → `off:nil / minimal:nil / low..max` 全有值；`compat.forceAdaptiveThinking=true` 必须带。跑生成器抽取 block 时先改生成器源码。
- **`recordModelsDevReasoningOptions`**：pi 在 minimax 块删掉了该调用，改显式 map；pig 照做，避免后续 merge 再覆盖。
- **`shellPathExists`**：Go 没有 Node 的 Windows stat EACCES 特例；用 `os.OpenFile(path, O_RDONLY, 0)` 后 Close 等价于 access(F_OK)（目录也可打开）。保留 `os.Stat` 行为对 Unix 无差异。
- **PowerShell 测试**：pig 测试在 Linux 跑，`runtime.GOOS` 不可 stub；把解析逻辑抽成接收 platform/env/pathExists/searcher 的内部函数以便单测（pi 用 `vi.mock` 等价）。
- **compaction 签名**：`GenerateSummary`/`GenerateSummaryWithUsage` 的 `reserveTokens` 保留（外部调用点不变），注释标 unused；`Compact` preparation 的 `Settings` 字段仍需保留（prepare 阶段用），只删 compact 内局部解构。
- **413 正则**：RE2 下 `[\w -]` 合法；`^` 锚定保证“正文里提 413”不误判。`IsContextOverflow` 已限定 `StopReasonError`，天然满足 pi 的 “only failed responses”。
- **默认模型**：NVIDIA 新 id 若不在目录中会导致 provider 无默认可选模型；必须同步目录条目，并抽查 `--list-models` 输出。

## 4. 明确不要读的

`packages/durable/`、`packages/chord/`、`packages/env/`、`packages/telemetry/`、`packages/ai/src/telemetry.ts`、`packages/coding-agent/docs/extensions.md`、`scripts/*.mjs`、`packages/coding-agent/test/suite/**`（除本表明确引用的 harness 改动）。

## 5. 验收与收口

- 每批：`go build ./... && go vet ./... && gofmt -l` 干净 + 相关包 `go test ./... -race -timeout 600s`。
- 全部完成：一次全量 `go test ./... -race -timeout 600s -count=1`。
- 收口稿 `docs/handoffs/SYNC-S20-pre-v1.4.0.md` + `AGENTS.md` 批次索引补一行 + 当前状态改为「v1.3.0 + main@73f8de80b」。
- 不打 tag（未到正式版本）；commit 用 `feat(sync): S20-x — …`。
