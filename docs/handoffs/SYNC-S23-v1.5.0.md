# S23 — pi v1.4.0（S22 pre-tag 已在 main@d5b3de22d 消化 67 commit）→ pi v1.5.0

- **日期**：2026-11-03
- **状态**：🚧 进行中（计划稿 + 分执行记录）
- **基线**：`d5b3de22d0e24a3e9c38a9410d3329b099717283..v1.5.0`（241 commit；其中触及已移植源码面 60 个，其余为 chord/durable/env/evals/docs/scripts/examples）
- **策略**：durable/chord/env/evals/telemetry/experiments/examples/scripts/package-manager/TS extension loader 一律跳过；catalog 结构性重构（`catalog/` 目录、TypeBox→pi-schema 类型面）不逐文件搬，按「行为规格级对齐」保留 pig 生成器架构并同步规则与产物。
- **收口**：Version 1.4.0 → 1.5.0，README/verify-matrix 同步，打 annotated tag v1.5.0。

## 0. commit 分流（d5b3de22d..v1.5.0，仅列触及移植面者）

### S23-1 — ai 核心修复

| pi commit | 内容 | pig 落点 |
|---|---|---|
| `c719d5b08` | Zhipu/Z.ai 用量上限错误码 1308 不重试 | `ai/utils/retry.go` 非重试 pattern |
| `d3398f826` | LiteLLM `ContextWindowExceededError` 识别为上下文溢出 | `ai/utils/overflow.go` pattern |
| `842c73170` | Bedrock strict "prefer" 工具超限回退一次（与 Anthropic 对齐） | `ai/api/constrained_sampling.go` 共享 helper、`ai/api/bedrock_converse_stream.go` 请求循环、`ai/api/anthropic_messages_convert.go` 改用共享 helper |
| `89c921d8b`（ai 部分） | OAuth 登录只允许 http/https（Radius/Copilot/Kimi/Meta 共享校验）；auth 文件 0600 | 新 `ai/auth/trusted_url.go`、`ai/auth/oauth_*.go`、`ai/auth/auth_storage.go`（按 pig 存储路径） |
| `6536f8012` / `6291b9bcc` | TypeBox → pi-schema 校验/类型面 | **跳过**：pig 无 TypeBox，`ai/utils/validation.go` 已对 plain JSON Schema 走合并 coercion（文件头已声明）；TS 类型面零运行时对应物。仅当新消息文案有行为差异时不追（pi-schema 依赖不引入） |

### S23-2 — ai catalog（models.dev v2 + 规则）

| pi commit | 内容 | pig 落点 |
|---|---|---|
| `2e0f175bd` | 目录改为 models.dev v2（`/experimental/v2.0/api.json`）+ 每 provider 规则文件 | `ai/scripts/generatemodels/fetch.go`（v2 读取与 normalize）、`types.go`（v2 结构）、`process.go` 既有 per-provider 规则沿用；不搬 `catalog/*.ts` 目录结构 |
| `44f6851b7` | OpenRouter `~anthropic/claude-*-latest` 走 Anthropic Messages | `ai/scripts/generatemodels/openrouter_catalog.go`、`metadata.go` |
| `81d6759fc` | GPT-5.4/5.5 工具顶层声明（不再 additional_tools）/ tool search | `ai/scripts/generatemodels/openai_models.go`、`metadata.go`、`process.go` 对应 compat |
| `177e2ad7e` | OpenCode Zen/Go 按协议分端点 | `ai/scripts/generatemodels/process.go`（openCode 段） |
| `363ad78bf` | GPT-6.1 Terra（OpenAI/Azure Responses/Codex，off 支持，Codex 500K） | 目录数据（models.dev v2）+ `openai_models.go`/`codex_models.go` 规则 |
| `d3398f826`/`81d6759fc` 等 catalog 测试 | 生成器回归 | `ai/scripts/generatemodels/*_test.go`、`ai/models_catalog_test.go` |

### S23-3 — tui

| pi commit | 内容 | pig 落点 |
|---|---|---|
| `0f21a3319`（tui 部分） | Markdown 顶层块复用、Text.setText 未变文本保留 wrap | `tui/markdown.go`、`tui/text.go` |
| `f09d01b85` | Orca 终端识别（Kitty images/truecolor/hyperlinks） | `tui/terminal_image.go`、`terminal.go` |
| `2ce9d527d` | 换行边界 styled word 后空格、换行 padding 的 strikethrough 复位 | `tui/utils.go` |
| `86dc686cf` | ESC+Return + native Shift → Shift+Enter | `tui/terminal.go` |
| `19ce8059c` | regular mode 不写 OSC 133 标记 | `tui/tui_main_screen.go` |
| `deb12d78e` | Kitty F1-F12 识别、忽略 F13+ | `tui/keys.go` |
| `fa1685152` | 裸 URL 在 CJK 标点处停止 | `tui/marked*.go` / `tui/markdown.go` |
| `cd1ea342a` | display math 宽于内容宽时回退原始 LaTeX | `tui/markdown.go` |
| `89c921d8b`（tui 部分） | `replaceControlCharacters`、terminal title 去控制字符 | `tui/utils.go`、`tui/terminal.go` |
| `3bae85274`（tui 部分） | `capturesViewportInput`、`getSelectPageIndex`、SelectList/SettingsList/Editor 翻页键 | `tui/select_list.go`、`settings_list.go`、`editor.go`、`keybindings.go` |

### S23-4 — coding-agent 核心

| pi commit | 内容 | pig 落点 |
|---|---|---|
| `d0fe21308` | branch summary 应用 context edits | `codingagent/core/branch_summarization.go`、`session_manager.go` |
| `269afc66a` | branch summary 传 thinking level | `codingagent/core/branch_summarization.go` |
| `807a5c051` | 写 session 失败时内存与文件一致 | `codingagent/core/session_manager.go` |
| `fecc19e4e` | grep 单独 CR 时上下文行对齐 | `codingagent/core/tools/grep.go` |
| `b9e3eef6c` | 同一工具集合不同顺序不再触发 system prompt 更新 | `codingagent/core/agent_session.go` |
| `7391dbb0f` | model scope 提示移入 fullscreen header | `codingagent/modes/interactive/interactive_mode.go` |
| `88ffcdc73` | 工作目录被删除时友好报错 | `cmd/pig/main.go` / `codingagent` 启动路径 |
| `f14813e96` | skill 诊断标签 `[Skill warnings]` | `codingagent/modes/interactive/interactive_mode.go` |
| `e7a0df27c` | 图片缩放取 PNG/JPEG 较小者 | `codingagent/utils/image_process.go` |
| `b5fc6dc84` | stripAnsi 去除 private-parameter CSI | `codingagent/utils/ansi.go` |
| `31ade0e0d` | 已保存 trust 决策优先于自动信任 | `codingagent/core/project_trust.go`、main |
| `6454d07ec` | WSL helper 从 `/` 启动 + 外部编辑器失败提示 | `codingagent/utils/{clipboard_command,open_browser}.go`、`modes/interactive/external_editor.go` |
| `6fed3cfb9` | SDK summary 流走真实 auth | `codingagent/core/{agent_session,sdk}.go` |
| `a6a7bac86` | hidden thinking 空标签不渲染空行 | `codingagent/modes/interactive/components/assistant_message.go` |
| `7853186d1` | 替换 extension editor 时保留对话框 | `codingagent/modes/interactive/interactive_mode.go` |
| `c1810bf70` | inline `ctx.ui.custom()` 可报 blocked program status | `codingagent/core/extensions_types.go`、`interactive_mode.go` |
| `705d047cb` | 收紧 system prompt | `codingagent/core/system_prompt.go`、`skills.go`、`tools/edit.go` |
| `1be312b46` | Windows 路径大小写不敏感比较 | `codingagent/utils/paths.go` + 各比较点 |
| `3bae85274`（coding-agent 部分） | 各选择器 PageUp/PageDown | `codingagent/modes/interactive/components/*selector*.go`、`mcp/ui` 对应面 |

### S23-5 — MCP + 扩展面

| pi commit | 内容 | pig 落点 |
|---|---|---|
| `908aa7b74` | MCP OAuth Client ID Metadata Document 优先、DCR 回退、`clientRegistration` auto/dcr | `integrations/mcp/oauth.go`、`oauth_signin.go`、`config.go`、`core/mcp_integration.go` |
| `be15fe3c1` | `pi mcp login/logout -l` 项目服务器 | pig 无 `pi mcp` CLI（`/mcp` 面板）→ **跳过**，面板已有项目/全局语义 |
| `0f21a3319`（coding-agent 部分） | 工具行/助手消息复用高亮与渲染块 | `codingagent/modes/interactive/components/{assistant_message,tool_execution}.go`、`codingagent/utils/syntax_highlight.go` |
| `9376c8d17` | 扩展渲染器注入 keybinding hints | pig 无 TS 扩展渲染器 → **跳过** |
| `b4b72a351`/`8bc94b19f`/package-manager 族 | 扩展 changelog、self-update、npm/git 包刷新 | pig 无 package-manager / TS 扩展装载 → **跳过** |
| `79006f061`/`e033f255f`/`cc44fa216`/`ae823bc0b`/`f6ee9ce9c` | tool_search/codemode | 不做清单（codemode+tool_search）→ **跳过** |
| `c466a3595` | `pi.exec` 输出上限/SIGKILL | pig 无 `pi.exec` 扩展 API → **跳过** |
| `0662...`/`66c2fa3d7`/`b4c2e92ac`/`531340f10`/`068e972c7` | 依赖升级、根 shim、删测试 | **跳过** |

## 1. 验收

- 每子批：`go build ./... && go vet ./... && gofmt -l` 干净；相关包 `go test -race -timeout 120s`。
- 收口：全量 `go test ./... -race -count=1 -timeout 600s` 一次全绿。
- `ai/models_generated.go` 只由生成器产出；生成器需可在 `--strict` 下跑通（网络可用）。
- Version 1.5.0 + `TestVersionTracksPiBaseline` 更新；README 用户可见面同步。

## 2. 风险与坑

- **catalog v2**：v2 schema（`capabilities.tools.supported`、`cost.text`、`api` 映射）与 v1 不同；保留 v1 形状的 `ModelsDevModel`，新增 v2 原始结构 + normalize 适配层，避免大改 1600 行 `process.go`。
- **v2 chat 过滤**：v2 显式 `type === "chat"`；realtime/image 不再进 chat。
- **生成器网络漂移**：catalog 随时更新，生成后与 pi v1.5.0 的 ai 测试期望（Terra/端点/顶层工具）对齐，不追当日新增模型。
- **Bedrock 重试**：只在流开始前重试；`OnPayload` 每轮重算；诊断 `bedrock_strict_tools_fallback`。绝不能重放已开始的流。
- **OAuth trusted URL**：校验必须发生在调用系统打开器之前；`http`/`https` 大小写不敏感；空 URL/相对 URL 拒绝。
- **Windows 路径**：不能全局 `strings.ToLower`（Unix 区分大小写）；抽 `pathEqual`/`pathContains` 按 `GOOS=windows` 分支，测试用注入变量。
- **PageUp/PageDown**：`capturesViewportInput` 需与 fullscreen overlay 优先级一致；regular mode 不改变滚动。
- **Markdown 复用**：缓存键必须含 theme/highlight 状态；`invalidate()` 在 theme 变更时调用，否则旧高亮残留。
- **大 diff 不整搬**：catalog `catalog/*.ts`、pi-schema、package-manager、TS 扩展 loader 均不逐文件移植。

## 3. 明确不要读的

`packages/durable/`、`packages/chord/`、`packages/env/`、`packages/evals/`、`packages/telemetry/`、`packages/schema/`、`packages/quickstart/`、`packages/coding-agent/examples/`、`scripts/*.mjs`、`packages/ai/src/api/{mistral-conversations,pi-messages}.ts`、`packages/ai/catalog/{chord,sources}/`（仅按需读 `sources/models-dev.ts` 与 provider 规则）。

## 4. 执行记录

- 2026-11-03：计划稿落盘，开始 S23-1。
- 2026-11-04：S23-2 完成。models.dev v2 读取/normalize（`models_dev_v2.go`，仅保留 `type == "chat"`）、OpenCode 按 `apis[].protocol` 选端点、OpenRouter `~anthropic/*` 走 Messages、GPT-6.1 Terra 全规则、GPT-5.4/5.5 顶层工具与 mid-convo 集合拆分、长上下文定价 tier 改为替换。生成器与 pi v1.5.0 新生成目录逐字段比对仅剩未移植的 mistral 与 pi 环境自身拉取失败的 OpenRouter；5 个 golden 已重同步（OpenRouter golden 因 pi 侧拉取为空，按 pig 当前生成快照记录）。新增 `models_dev_v2_test.go`（v2 normalize/缓存 TTL/协议选择/长上下文 tier）；`go test ./... -race` 全绿。
