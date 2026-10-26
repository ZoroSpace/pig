# S22 — pi v1.4.0 → pi main@d5b3de22d (pre-v1.5.0)

- **日期**：2026-10-25
- **状态**：✅ 完成（Version 维持 1.4.0；待 pi v1.5.0 正式 tag 再收口 bump）
- **基线**：`v1.4.0..d5b3de22d0e24a3e9c38a9410d3329b099717283`（67 commit；v1.4.0 之后尚无新 tag，故本批按 main 预告版同步，`Version` 维持 1.4.0，不 bump）
- **策略**：durable/chord/env/evals/telemetry/examples/scripts/preview-packages/changelog/docs-only 一律跳过；pi 无对应物的面（package-manager 安装链、pi-messages、faux provider、TS extension loader）跳过。按 S20 先例，不追 main 的长期承诺不变——本批只消化到用户指定的最新 commit，下个正式 tag 再收口。

## 0. commit 分流

| 子批 | pi commit | 内容 | pig 落点 |
|---|---|---|---|
| S22-1 | `7c22a93d0` | Usage.cost.totalFromProvider 标志；catalog 估算置 false | `ai/types/types.go` Cost、`ai/models.go` CalculateCost、`ai/api/openrouter_images.go` |
| S22-1 | `cae8d69e5` | OpenRouter 上报总价（Chat Completions + Anthropic），BYOK 加上游成本 | 新 `ai/utils/openrouter_cost.go`、`ai/api/openai_completions_stream.go`、`ai/api/anthropic_messages.go` |
| S22-1 | `3bc6eebac` | Anthropic 延迟 system 更新必须越过被丢弃的 assistant 消息 | `ai/api/anthropic_messages_convert.go` |
| S22-1 | `e3bc05e87` | per-message effort 时顶层 effort 固定 `low`（组织可封顶） | `ai/api/anthropic_messages.go` |
| S22-1 | `7314819f0` + `a7b94a03f` | Gemini 工具 schema：string/number `const` → 单值 `enum`，只重写 schema 位置 | `ai/api/google_shared.go` |
| S22-1 | `1de2d7ade` | Kimi K3 家族按 ID 正则识别、Kimi Coding K3 风格按 effort options、K3.1 订阅成本 | `ai/scripts/generatemodels/process.go`（目录数据源无静态 diff） |
| S22-1 | `f715a15c1` | 增量解析流式工具参数 `StreamingJsonParser` / `ToolCallArgumentsStream`；所有 adapter/agent proxy 每 delta 只读一次 | 新 `ai/utils/streaming_json.go`；`ai/api/{anthropic_messages,bedrock_converse_stream,openai_completions_stream,openai_responses_shared}.go`、`agent/proxy.go`、`ai/api/constrained_sampling.go` |
| S22-1 | `f715a15c1` 工具卡片 | 每帧只渲染一次流式参数 | `codingagent/modes/interactive/components/tool_execution.go` |
| S22-2 | `27cc6bfc0` | macOS 先 SIGSTOP 进程组再 SIGKILL | `codingagent/utils/shell_kill_unix.go` |
| S22-2 | `c60de42e9` + `36be9c612` | exec spawn 错误保留 message；stdout/stderr 跨 UTF-8 chunk 解码 | `codingagent/core/exec.go`（若为 utils 同名文件按落点） |
| S22-2 | `8ddc44ec8` | forkFrom 单 fd 写头 + 全条目 | `codingagent/core/session_manager.go` |
| S22-2 | `c8ec752cf` | fd/rg 仅 x64/arm64 自动下载；其他架构告警并跳过 | `codingagent/utils/tools_manager.go` |
| S22-2 | `984cf6643` | provider id 含 `/` 时 model 引用最长前缀匹配 | `codingagent/core/model_resolver.go` |
| S22-2 | `64627838b` | Markdown transformer context 增加 messageId/timestamp | `codingagent/core/extensions_types.go`、`modes/interactive/{interactive_mode.go,components/{assistant_message,user_message,markdown_transform}.go}` |
| S22-2 | `606af8662` | /model 与 /scoped-models 行显示有信息量的 name | `codingagent/modes/interactive/model_search.go`、`components/{model_selector,scoped_models_selector}.go` |
| S22-2 | `7151099d0` | device-code 登录确认后打开浏览器/复制 code | `ai/auth/types.go`、`ai/extension_oauth_types.go`、`codingagent/modes/interactive/components/login_dialog.go` |
| S22-2 | `9ffd27cfa` | MCP OAuth 只接受 http/https URL | `integrations/mcp/oauth.go`（发现/端点校验） |
| S22-3 | `9359adf0c` + `1c720d285` + `cae8d69e5` 导出面 | HTML export：system 消息渲染、超深会话迭代遍历、总价用 cost.total | `codingagent/core/exporthtml/templates/{template.js,template.css}` |
| S22-3 | `8a2ebd385` | 嵌套 blockquote 宽度耗尽时不再逐层重排（防指数增长） | `tui/markdown.go` |
| S22-3 | `ff6896471` | 全屏双击选词使用 iTerm2 word characters | `tui/alt_screen*.go` |
| S22-3 | `4464442f9` + `1c8cb1781` | 全屏视口上方图片裁剪：跳过 padding/边界行，搜索上限=最高图片 | `tui/layout.go`、`tui/terminal_image.go` |
| S22-3 | `8c03fef4d` | fuzzy 连续命中评分；/resume 分字段匹配（name/cwd fuzzy，id/messages 精确） | `tui/fuzzy.go`、`codingagent/modes/interactive/components/session_selector_search.go` |
| 跳过 | durable/chord/env/evals/examples/scripts/preview-packages/changelog/docs | 不做清单/无对应物 | — |
| 跳过 | `476a303c1` `5f960a0e2` `6dfb54678` | npm/package 安装链；pig 无 package-manager 实现 | — |
| 跳过 | `14c811841` | pi-messages adapter；pig 未实现该 adapter | — |
| 跳过 | `f3fa8bd92` | faux provider；pig 无 faux provider（冒烟用 mock 注入） | — |

## 1. 批次目标与验收

- **S22-1**：新增增量 JSON 解析器，替换所有 `ParseStreamingJSON` 全量重解析点；验证大参数流式场景耗时线性、partial 值递增、结束后 `argumentsError` 语义不变。
- **S22-2**：coding-agent 行为修复与用户可见面（登录确认、选择器名称、/resume 搜索、fork 性能、exec 编码）。
- **S22-3**：TUI 渲染修复 + HTML export。
- 每子批 `go build && go vet && gofmt -l` 干净，相关包 `-race` 通过；收尾全量一次 `go test ./... -race -count=1 -timeout 600s`。
- 不 bump `codingagent.Version` / `ai/utils/version.go`（基线仍是 v1.4.0 tag；等 pi 出 v1.5.0 正式 tag 再收口）。README 只更新用户可见的行为描述，不写版本号变化。

## 2. 风险与坑

- **StreamingJsonParser 语义必须与 pi 逐条一致**：未闭合字符串保留已读字符；未闭合转义暂扣；未闭合容器展示已读成员；未开始的 key/value 省略；literal 前缀可显示；number 未完成时按已读数字显示（指数无数字忽略）。非法输入宽松读取、永不 panic。Go 侧需处理 rune/byte：直接按字节读取，多字节 UTF-8 原样透传（只需 `"`/`\` 为 ASCII）。
- **快照不可变**：pig 的值形态 content 会整体替换，但仍须保证已返回的 `arguments` map 不被后续 append 就地改写；实现按 pi 复制 open path，完成容器共享。
- **`__proto__` 特例**：Go map 无原型污染，但测试数据里有该键，按普通 key 处理即可；不要在注释里假装实现 JS 语义。
- **adapter scratch 状态**：pig 已把 `partialJSON` 放在 `anthropicBlock`/`bedrockBlock`/`openai*Slot` 而非 content 上；本批把 string 换成 `*ToolCallArgumentsStream`，结束后 `finish()` 并清空指针，禁止把 raw text 写进最终 ToolCall。
- **OpenRouter cost**：`totalFromProvider` 是可选 bool，线格式只在 true 时输出？pi 的 `totalFromProvider?: boolean` 在 JSON 中 false 也会序列化（显式赋值时）。Go 用 `*bool` + `omitempty` 会丢 false；为线兼容改用 `bool` + `json:"totalFromProvider,omitempty"` 会丢 false 但缺失语义等价（false=缺省），可由 combine 逻辑接受。优先 `*bool` 以保留显式 false 与 pi 序列化一致；读取处 nil 视为 false。
- **HTML template.js 是内嵌静态资源**：pig 的 Go 导出器直接把 template.js 打包；改动需确保 `go:embed` 生效，且避免引入只有浏览器端才有的现代语法（项目已有基线）。
- **TUI 图片布局**：`maxKittyImageRows` 只增不减与 pi 一致；单元测试需注册图片后验证边界。
- **fuzzy 评分**：修改后 `/model`、`/resume`、自动补全共用 `FuzzyMatch`；用现有测试 + pi 新增用例核对顺序，不要只改 session search。
- **device-code 确认**：`copyToClipboard` 异步，不得阻塞轮询；只允许 http/https 打开浏览器；Enter 消费一次防重复。

## 3. 明确不要读的

`packages/durable/`、`packages/chord/`、`packages/env/`、`packages/evals/`、`packages/telemetry/`、`packages/coding-agent/examples/`、`scripts/*.mjs`、`packages/ai/src/api/{mistral-conversations,pi-messages}.ts`、`packages/ai/src/providers/faux.ts`。

## 4. 执行记录

- 2026-10-25：方案落盘；开始 S22-1。
- 2026-10-25：S22-1 完成。`ai/utils/streaming_json.go` 增量解析器替换全部流式参数重解析点（anthropic / bedrock / openai completions / responses / agent proxy / constrained sampling）；`ai/utils/openrouter_cost.go` 接入 Chat Completions 与 Anthropic 上报总价，`types.Cost.TotalFromProvider` 全链；Google `const`→单值 `enum`；Kimi K3 家族生成器识别；Anthropic per-message effort 顶层固定 `low`；工具卡片每帧只渲染一次流式参数。新增 `streaming_json_test.go`、`openrouter_cost_test.go`、adapter cost/effort/schema 回归；`ai/... ./agent` 全绿。
- 2026-10-25：S22-2 完成。macOS 先 SIGSTOP 进程组再 SIGKILL（`shell_kill_unix.go` + seam 测试）；fd/rg 仅 x64/arm64 自动下载（`getReleaseTarget` + 平台/架构 seam + 不支持架构告警）；provider id 含 `/` 时最长前缀匹配（`splitProviderPrefix` + catalog 回归）；Markdown transform context 增 `messageID`/`timestamp`（`extensions_types.go` + user/assistant components + interactive 元数据）；`/model`、`/scoped-models` 显示有信息量的模型 name；device-code 登录确认后打开浏览器/复制 code（`OpenBrowserOnConfirm`/`CopyCodeOnConfirm` 默认 true，UI 线程与后台状态写分离）；MCP OAuth metadata URL 仅 http/https。`c60de42e9`/`36be9c612` 跳过：pig 无 `pi.exec` 扩展 API，bash 已有独立 UTF-8 流式解码；`8ddc44ec8` 零操作：pig forkFrom 已单 fd 写头+全条目。
- 2026-10-25：S22-3 完成。HTML export 用 system entries 在时间线内渲染 prompt/tools（更新用 System Prompt Update/Tools Added/Removed），header 仅在旧会话无 system entries 时回退；树遍历迭代化防深会话栈溢出；总价优先 `cost.total`（旧会话回退分量和）。TUI：嵌套 blockquote 宽度耗尽时不再逐层重排；全屏双击选词补 iTerm2 默认单词字符 `+ \ ~ _ .`；视口上方图片裁剪跳过 padding/SGR/边框/并排文本行，反向搜索上限=最高已注册图片行数；fuzzy 匹配同时评分连续命中取最优，`/resume` 按字段分匹配（name/cwd fuzzy，id/messages 精确）+ 三级 tier 排序。新增/更新回归：`template.js` canaries、`TestMarkdownDeeplyNestedBlockquotesDoNotExplode`、`TestTuiAltScreenWordSelectionKeepsITerm2WordCharactersWhole`、两个 Kitty crop 测试、`tui/fuzzy_test.go`、session search 6 个用例。
- 验证：`go build ./...`、`go vet`、`gofmt -l` 干净；相关包 `-race` 通过后全量 `go test ./... -race -count=1 -timeout 600s` 复跑（首次抓出 S22-1 `CombineUsage` 的 `TotalFromProvider=false` 测试期望遗漏，已修正为 DeepEqual）。
- README 同步：`/resume` 搜索规则、全屏双击选词、设备码登录确认行为。
