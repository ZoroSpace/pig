# S21 — pi main@73f8de80b (pre-v1.4.0) → pi v1.4.0

- **日期**：2026-10-22
- **状态**：进行中
- **基线**：`73f8de80bc87021375f8b525292ac07492253002..v1.4.0`（39 commit）
- **策略**：按 S20 收口点直接对齐正式 tag；durable/chord/env/telemetry/evals/experimental/codemode/扩展 TS loader 面一律跳过。

## 0. commit 分流

| 批次 | pi commit | 内容 |
|---|---|---|
| S21-1 | `adc5fa9f8` + `04a93bad8` | ClientIdentity 下沉到 pi-ai、provider attribution 统一（OpenRouter/NVIDIA/OpenCode）、agent 默认身份 |
| S21-1 | `6e491cfa9` | retry 状态码只匹配独立数字 |
| S21-1 | `29057ece3` | 代理转发的 Codex CLI stream disconnect 重试 |
| S21-1 | `cedefc532` | ChatGPT Business workspace usage limit 错误 |
| S21-2 | `caa6967b6` | Responses `additional_tools` 全量就地声明 + prompt cache 跨工具变更 |
| S21-2 | `530015d93` | 增量工具通知只回述本轮之前已宣布的移除 |
| S21-2 | `e5fe9777b` | 上游退役模型清理 + Copilot 默认模型 `gpt-6.1-sol` |
| S21-2 | `fdce48eb4` | Codex 目录移除 ChatGPT 账号不接受的模型 |
| S21-2 | `fbc456d82` / `18e56a069` | 目录测试对齐（xhigh Grok、OpenRouter decision compat） |
| S21-3 | `02726086d` | 恢复省略截断 tool call 时必须连带其 tool result |
| S21-3 | `8c4e87389` | grep 工具传 `--no-config` |
| S21-3 | `bd3c9b823` | resume/tree 恢复 loadout 时激活分支未记录的新扩展工具 |
| S21-3 | `adfc14461` | `/compact` 自定义指令应用到 split-turn 前缀摘要 |
| S21-3 | `bdd9f0399` | mcp 配置接受 UTF-8 BOM（pig 自研 MCP 同面） |
| S21-3 | `f8ab362cc` | 剪贴板子进程脱离进程组（Ctrl+Z 不杀剪贴板守护） |
| S21-3 | `38847563f` | 扩展对话框叠栈，关闭后恢复上一个 |
| S21-3 | `26293e74f` | HTML export CJK emphasis（vendor marked-cjk-friendly） |
| S21-4 | `612a172e6` | `@` 单段 fd query 转义正则元字符 |
| S21-4 | `9df819de3` | 进程直接退出（未 Stop）时同步恢复终端模式 |
| S21-4 | `2ed3118b0` | 未闭合 `$$` 不吞 Markdown 表格 |
| S21-4 | `396ecd7b8` | Rex 启用 Kitty images |
| S21-4 | `b2d0da6fe` | CJK 标点旁 emphasis（核对 pig 既有修正） |
| 跳过 | `7f225f25d` | System One classifier 图片输入：pig 无 classifier 运行时（仅目录元数据） |
| 跳过 | `e5fe...` 内 Baseten/Together 测试面 | pig 无对应 live 测试；仅同步目录与默认值 |
| 跳过 | `22dc6c26f` | 扩展 loader 暴露 `pi-ai/models`：pig 无 TS 扩展运行时 |
| 跳过 | `885a564b3` | codemode worker 限量：pig 不做 codemode |
| 跳过 | `ea25697e9` | managed-install.json BOM：pig 无 managed install 自更新链 |
| 跳过 | `f76b248ca` `82432219a` `1d01b3b1f` `a731ba8b8` | changelog/docs |
| 跳过 | `16f5f0af4` `4c25119f5` `cae9fd28a` `48c8f3228` `4759f09fb` `89e309581` | durable/chord |
| 跳过 | `4a080252f` | Release commit（版本号由 S21-final bump） |

## 1. 批次定义

### S21-1 ai 身份 + retry
- 目标文件：`ai/utils/identity.go`（新）、`ai/utils/version.go`（新）、`ai/types/types.go`、`ai/models.go`、`ai/agent` 回调链、`ai/api/*` 十处 User-Agent 来源、`agent/agent.go`、`agent/agent_loop.go`、`codingagent/core/client_identity.go`（新）、`codingagent/core/model_runtime.go`、`codingagent/core/sdk.go`；删除 `codingagent/core/provider_attribution.go` 与 `ai/utils/pi_user_agent.go`（管理端点用的 `codingagent/utils/pi_user_agent.go` 保留）。
- retry：`ai/utils/retry.go` 状态码边界 + Codex disconnect + workspace limit；`ai/api/openai_responses.go`。
- 测试：identity 合并/UA/attribution/OpenCode session、agent 默认身份、retry 新正反例。

### S21-2 ai Responses 工具变更缓存 + 目录
- `ai/utils/transcript.go`、`ai/api/openai_responses_shared.go`、`openai_responses.go`、`azure_openai_responses.go`、`openai_codex_responses.go`、`openai_completions.go`；既有 `transcript_tool_changes_test.go` 扩展。
- 目录：`ai/scripts/generatemodels/process.go` + `ai/models_generated.go` 定点删除 codex `gpt-5.5`/`gpt-5.3-codex-spark`、Copilot 退役模型；`codingagent/core/model_resolver.go` 默认 `gpt-6.1-sol`；相关目录测试。

### S21-3 coding-agent
- `agent_session.go` 恢复省略、loadout；`compaction/compaction.go`；`tools/grep.go`；`integrations/mcp/config.go`；`utils/clipboard_command.go`；`modes/interactive/interactive_mode.go`；`core/exporthtml/`（模板 + vendor）。
- 测试：每项回归单测（Go 形状适配，见各 fix 文档）。

### S21-4 tui
- `autocomplete.go`、`terminal.go`、`tui.go`、`alt_screen*.go`、`markdown.go`、`terminal_image.go`。
- 测试：fd query 元字符、exit restore、math/table、Rex、CJK emphasis。

### S21-final
- `codingagent.Version` + `ai/utils/version.go` → `1.4.0`；`TestVersionTracksPiBaseline` 同步；AGENTS pin；README 用户可见面；全量 `go test ./... -race -timeout 600s -count=1`；打 tag `v1.4.0`。

## 2. 风险

- Identity 是线格式外行为：`User-Agent` 形态从 `pi (<os> <kernel>; <arch>)` 变为 `pi/<version> (<goos>; go/<version>; <arch>)` 的 pig 等价物；保留 `PI_*`/`pi` 协议值。
- OpenCode session header 从 coding-agent transformHeaders 移入 ai models/runtime，避免重复注入。
- Responses 变更影响 prompt cache 与工具声明位置，必须逐字对照 pi 文案（模型对工具可用性敏感）。
- 目录只做定点删除，不整文件重生成（S14/S16/S18/S19/S20 先例）。
- 终端 exit 恢复在 Go 中需 `os.Exit` 前同步 `os.Stdout.Write`，不引入 process-global 泄漏。

## 3. 明确不要读的

`packages/durable/`、`packages/chord/`、`packages/env/`、`packages/telemetry/`、`packages/evals/`、`packages/coding-agent/docs/extensions.md`、`scripts/*.mjs`、`packages/ai/src/telemetry.ts`。
