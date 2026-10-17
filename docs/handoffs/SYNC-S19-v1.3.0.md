# S19 收口：pi v1.2.0 → v1.3.0 基线同步

- **状态**：✅ 完成（2026-10-17）
- **基线**：pi `v1.2.0` → `v1.3.0`（`71023aec1`）。
- **计划稿/逐 commit 消化**：`docs/handoffs/SYNC-S19-pre-digest.md`（S19-1..S19-3 全表 + 执行进度节）。
- **本版无会话 JSONL / RPC 线格式变更**（`core/session.ts`、`core/messages.ts`、`core/transcript.ts` 零 diff）→ 双向兼容门沿用 S18 声明核对制。
- **版本**：`codingagent.Version` `1.2.0` → `1.3.0`（`TestVersionTracksPiBaseline` 同步 1.2.0→1.3.0）。

## 分批复盘（详见计划稿进度表）

- **S19-1 ai**（收口 commit `89e6367`）：json-parse 去 `"u"` 转义（`4b994d5e5`）、完成态 tool call 参数严格解析 `parseToolCallArguments`/`ArgumentsError`（`87f9a3641`，mistral 适配器未移植处跳过）、retry 两模式 `unable to forward upstream`（`7efebff18`）+ `service.?unavailable` 放宽（`550f1f3ec`）、错误 tool result 图片拆分（`5ab7fe74c`）、跨模型 thinking 直接丢弃（`911309429`）、anthropic `off` thinking 三返 `providerThinkingLevel`（`dc939913f`）、openai-completions reasoning 先于 content（`63035734f`）、gemini-4+ thinking level（`1692bba63`）、openai-responses GPT-6 mid-convo effort `configuration_update`（`60ac3bcc3`）、opencode baseUrl `inference` + opencode-go google（`195120a91`）、kimi-coding 改 openai-completions（`a3f265b1c`）、xiaomi MiMo thinking 开关（`6374c2baf`）、codex gpt-6.1-sol 500K（`5712b134e`）。零操作声明：`b39979799`（AssistantMessageFrame，pig 无此物）。
- **S19-2 coding-agent core 小修**（commit `ef12700`）：resume 恢复 transcript 记录的 default tools loadout（`6f2f164ab`）、prompt templates 保留空引号参数（`9fe3f3716`）、read 行范围标签跨 TUI/导出统一 `FormatReadLineRange`（`910149912`）。跳过声明：`cea257439`（`sessionShareDestination` 依赖未实现的 /share 上传链路）、`a4f67eb65`（扩展 overlay，Phase 7 不做）、`3daa20280`（codemode 不做）。
  - 顺带修 pig-only 测试 flake：`TestMCPPanelSignInAndCancel` 在 `signInSelected` 同步渲染登录屏、后台 goroutine 才调 `SignIn` 的窗口内立即读 `calls` → 改为 `waitFor` 轮询（commit `7e293a5`，`docs/fix/2026-10-17-mcp-panel-signin-test-race.md`）。
- **S19-3 model-resolver 统一解析器（磁铁）**（commit `e6bebc3`，`d7f7e0dfe`）：新 `resolveModelReference(request, models, usable)`（先收集全部候选再按 usable/provider 选，杜绝 catalog 顺序决定 provider、thinking 后缀换 provider、歧义 exact 退化为 fuzzy）；`model_resolver.go` 整文件重写（新 `ModelReferenceRequest/Target/Result`、`providerPrefix`/`splitProviderPrefix`、`splitThinkingSuffix`、`selectReference`/`selectFuzzy`/`fuzzyMatchIndices` 等；`FindExactModelReferenceMatch` 保留为包装；删 pi 已无的 `ParseModelPattern`/`tryMatchModel`/`ParsedModelResult`）；scope 解析增 `ambiguous` 诊断；`resolveCliModel` 增 `InlineApiKey`（`--api-key` 不借存储凭证选 provider）；`findInitialModel` 去 `cliProvider/cliModel`（pig 无调用点，签名对齐保持编译）；credential-print 改用 `resolveModelReference`（且删死 helper `containsCustomModelIdNote`）。

## 本批 pig-only 偏差新增

- 无新增 pig-only 行为偏差；`model_resolver.go` 全量重写属可 grep/行为对齐的实现替换（pi TS `object` 断言 → pig `ModelResolverRuntime` stub）。

## 测试与验收

- 全量 `go test ./... -race -timeout 600s -count=1`：**全绿，rc=0**（收口基准）。
- `gofmt -l` 干净、`go vet ./...` 干净、`go build ./...` 通过。
- 新/重写回归测试：`ai/utils/utils_test.go`、`ai/utils/validation_test.go`、`ai/utils/retry_test.go`、`ai/api/anthropic_*_test.go`、`ai/api/transcript_tool_changes_test.go`、`ai/api/xiaomi_models_test.go`（新）、`ai/providers/xiaomi_models_test.go`、`ai/supports_xhigh_test.go`、`codingagent/core/settings_default_tools_test.go`、`codingagent/core/prompt_templates_test.go`、`codingagent/core/tools/render_utils_test.go`（新）、`codingagent/modes/interactive/components/tree_selector_test.go`、`codingagent/core/exporthtml/index_test.go`、`codingagent/core/model_resolver_test.go`（整文件重写，reference catalog 40 例 × 正反序）、`codingagent/cli/credential_print_test.go`（新）。
- 生成物策略（沿用 S14/S16-1/S18-1）：生成器源码照 pi 1.3.0 改 + `ai/models_generated.go` 定点手改，**不整体重生成**（今日 models.dev 漂移 +5113/−1832 行）。

## 下一批候选

- pi main 若出 v1.4.0，按「钉 tag」策略开 S20-pre-digest。
- verify-matrix：`da0eadf04`（go-sdk 升级后核对 MCP 刷新取消语义）仍挂账。
