# S18 收口：pi v1.1.0 → v1.2.0 基线同步

- **状态**：✅ 完成（2026-10-16）
- **基线**：pi `v1.1.0` → `v1.2.0`（`06a466935`）。main v1.3.0（`71023aec1`）另开，不追。
- **计划稿/逐 commit 消化**：`docs/handoffs/SYNC-S18-pre-digest.md`（含 S18-1..S18-5 全表与进度日志）
- **无会话 JSONL / RPC 线格式变更**（`core/session.ts`、`core/messages.ts`、`core/transcript.ts` 零 diff）→ 双向兼容门沿用 S17 声明核对制。
- **版本**：`codingagent.Version` `1.1.0` → `1.2.0`（`TestVersionTracksPiBaseline` 同步）。

## 分批复盘（详见计划稿进度日志）

- **S18-1 ai**：retry 正则（`7bd01fb92`）、anthropic strict tool 容量降级重试（`d4d53612b`，9 例测试）、OpenRouter 个性化目录 ai 半区（`ee409a85f`，新 `ai/providers/openrouter_api.go`）、生成器清理 + Sonnet 5.5 cacheRead（`ce950d78f`）。声明零操作：codex zstd 拷贝（`0f4b1465a`）；跳过：mistral（无适配器）、radius e2e（TS-only）。
- **S18-2 agent + coding-agent core**：`a447ba11f` **磁铁**（`startRun`/`prepareRun` 拆分 + `BeforeAgentStartTrigger` union + custom message run + abort-during-setup），顺带修 system-prompt Sections 丢失；OpenRouter 接线（`ee409a85f`）；compaction 去 thinking（`15e7bdc14`）；Windows EPERM lock 重试（`e6fb2581d`）；print 截断告警（`441f5767c`）；`~` 展开 + **顺带修 Minimatch 参数顺序**（`6a51a39d4`）；嵌套 worktree AGENTS.md（`f1b2e77f5`）；`PI_CLIPBOARD_OSC52`（`3e477eab5`）；status-indicator colorFn（`dbc7ff87c`）；settings 光标文案（`1cedd3272` coding-agent 半区）。跳过 codemode 计时（`014873d7d`）。
- **S18-3 tui + keys**：stdin buffer 延迟终端回复（`edb2eb966`）、overlay 滚轮滚 transcript（`11f991145`）、herdr kitty 图片（`78a3c10ba`）、fake-cursor 解析全链（`1cedd3272` tui 半区）、kitty/modifyOtherKeys shifted symbol + `parseKeyId` 重写（`7f9e1198f` tui 半区）。
- **S18-4 MCP（pig 自研路线）**：RFC 9207 issuer 无 metadata 一律拒绝（`c5f5b3282`，2 例测试）。声明零操作：`67996ab75`（structuredContent any）、`da0eadf04`（refresh 取消传输层在 go-sdk 无 seam，记 verify-matrix 待升级）、`f4cca2dc2`（connect 不列资源）。
- **S18-5 配置 schema（lean 决策）**：只回填行为 delta——theme 严格校验（`theme_json.go` 顶层/colors/export `additionalProperties:false` + `knownThemeColorKeys` 含 pig-only `imagePlaceholder`）、keybinding 值校验（`tui.IsValidKeyId` + `toKeybindingsConfig` 逐值校验/跳 `$schema`）。声明零操作：`SETTINGS_DEFAULTS`/`model-config` 校验/类型搬迁均为 schema 侧或纯重构；`96abe9a5a` schema URL 发布管线**跳过**。

## 本批 pig-only 偏差新增

- `theme_json.go` 严格校验的允许表 `knownThemeColorKeys` 必须含 pig-only token `imagePlaceholder`（BLOCK-BORDERS 族）——后续新增 pig-only 颜色 token 时同处登记，否则主题文件被拒。
- `tui.IsValidKeyId` 的 `baseKeyNames` 是 pi `Object.values(Key)` 字符串成员的 Go 镜像；新增 base key 常量时须同步补集合。

## 测试与验收

- 全量 `go test ./... -race -timeout 600s -count=1`：**32 包全绿，rc=0**（收口基准）。
- `gofmt -l` 干净、`go vet ./...` 干净、`go build ./...` 通过；`pig --version` = `1.2.0`。
- 新回归测试（stash 验证红→绿）：`ai/api/anthropic_strict_tool_fallback_test.go`、`ai/providers/openrouter_api_test.go`、`tui/stdin_buffer_test.go`、`tui/keys_test.go TestIsValidKeyId`、`theme_json_test.go TestValidateThemeJsonStrictUnknownKeys`、`codingagent/core/keybindings_test.go TestKeybindingsConfigValueValidation`、`agent_session_custom_message_run_test.go`、`resource_loader_context_files_test.go` 等。

## 下一批候选

- 计划稿「给人类的建议」中 S18-5 若改 full（schema 源化）需另拆；当前按 lean 收口，无遗留。
- pi main v1.3.0（`71023aec1`）建议下一同步批（tag 已出，按「钉 tag」策略可开 S19-pre-digest）。
- verify-matrix：`da0eadf04`（go-sdk 升级后核对 MCP 刷新取消语义）挂账。
