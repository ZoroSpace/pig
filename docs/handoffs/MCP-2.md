# MCP-2：/mcp 交互面板 + 启停持久化（✅ 收口终稿，2026-09-27）

> 状态：**完成**。（2026-09-27 追记：本文所述 `mcp/` 包已迁至 `integrations/mcp/`，准入规则见 AGENTS.md 仓库机制；路径按写作时点记录。）mcp 包启停面 + core 桥接 + components 面板 + interactive 接线 + 全量 -race + tmux 真 TTY 冒烟全部落地。feat 文档：`docs/feat/MCP.md` MCP-2 节。前置：MCP-1 已收口（8e9d76c）。

## 0. 收口摘要

- 按本 handoff 调研稿直接开工，两个已拍板决策未翻案：启停**持久化到来源 mcp.json**（`disabled` 字段）；工具列表**展开时才连接**（面板打开零连接副作用，冒烟实证）。
- 实施过程无返工；过程中沉淀三个测试基建教训（§4：TUI 锁可重入与组件 mutate 模式、组件测试禁用 ticker、MatchesKey 只认原始字节序列）。
- 验收门：build/vet/gofmt 干净；**全量 `go test ./... -race -count=1 -timeout 600s` 只跑一次 rc=0，30 包全 ok**（`/tmp/pig-test-out-mcp2.log`）；tmux 真 TTY 冒烟七步全通（§5）。
- 新增测试 17 函数（mcp 7 + components 10），存量 mcp 17 函数 + core 桥接 5 函数零改动保持绿。

## 1. 最终文件清单

| 文件 | 内容 |
|---|---|
| `mcp/config.go` | `ServerConfig.Disabled`（pig-only 字段）+ `Config.Sources`（`json:"-"`，`LoadConfigFiles` 填胜者文件）|
| `mcp/manager.go` | `statusDisabled` 常量 + `serverState.sourcePath` + `ServerStatus.Disabled/SourcePath` + StartEager/SearchTools/connectLocked/Status 的 disabled 跳过 + 新导出 API `ListServerTools`/`Reconnect`/`SetServerDisabled` + `isDisabled` |
| `mcp/disable.go`（新）| `setServerDisabledInFile` JSON 手术（generic map + UseNumber + EscapeHTML off + 2 空格缩进 + 尾换行 + 保留 mode；enable 删键）|
| `mcp/disable_test.go`（新）| 7 测试函数 |
| `codingagent/core/agent_session.go` | `MCPManager()` 导出 getter |
| `codingagent/core/slash_commands.go` | `/mcp` 追加进 `BuiltinSlashCommands` |
| `codingagent/core/keybindings.go` | `app.mcp.reconnect/toggleDisabled/copyError` 三 defs（ctrl+r/ctrl+d/ctrl+y）+ 常量 + keybindingsOrder |
| `codingagent/modes/interactive/components/mcp_panel.go`（新）| 面板组件 ~460 行（`MCPPanelProvider` 接口 + 行渲染 + 动作 + 2s ticker）|
| `codingagent/modes/interactive/components/mcp_panel_test.go`（新）| 10 测试函数（fake provider）|
| `codingagent/modes/interactive/interactive_mode.go` | `case "/mcp"`（deferred 判断之前）+ `showMcpPanel()` |

## 2. 与调研稿的偏差

1. **`Config.Sources` 落在 `LoadConfigFiles`**（原计划一致），但实现时把 `setServerDisabledInFile` 独立成 `mcp/disable.go`（原计划放 manager.go 底部附近）——手术逻辑独立成文件便于测试与注释。
2. **`Encoder.SetEscapeHTML(false)`**：调研稿只说「2 空格缩进 marshal」；实现补了 HTML 转义关闭，否则 URL 里的 `&` 会被写成 `\u0026`（语义等价但文件脏）。
3. **面板过滤**：取 pi-mcp-adapter 的活过滤但未做 `desc:` 前缀切描述搜索——FuzzyFilter 作用于 server 名，工具行跟随展开的 server（调研稿取舍清单本就未含 desc: 前缀，此处显式声明）。
4. **panel keys 走 keybinding 系统**（`app.mcp.*` defs + KeyDisplayText 提示行）而非裸字节匹配——与 tree_selector 的 pig-only 键位先例一致，/hotkeys 自动收编。
5. **busy 防重入**：reconnect 与启停在 in-flight 时拒绝重复触发（调研稿未提，防连按）。

## 3. 修复清单

本批无「半成品返工」级修复；调试期测试失败全部是测试基建问题（键序列写法、测试环境无 core keybindings 表、ticker 竞态），实现代码本身零 bug 返工。教训沉淀见 §4。

## 4. 关键设计决策与教训

1. **组件并发模型（踩坑预防的核心）**：TUI 锁是**可重入**的（goroutine-local `lockOwner`），`HandleInput` 被调用时已持锁——因此面板组件把所有状态入口（HandleInput 整体 / refreshNow / 后台 goroutine 结果落地）统一走 `p.mutate`（interactive 传 `m.mutateUI` = RunExclusive），内部方法全部假设已持锁（`*Locked` 后缀），组件自身零 mutex。HandleInput 内重入是零成本路径，后台 goroutine 新取锁。**若在持锁方法里再调 p.mutate 且 Mutate 是非重入 mutex（测试形态），即死锁**——内部方法一律不再调 mutate。
2. **组件测试必须禁用 ticker**：`RefreshInterval: -1` 选项（0 = 默认 2s）。测试环境没有 TUI 锁，ticker goroutine 的 refreshNow 与测试主 goroutine 的 Render/HandleInput 竞态（-race 必红）。刷新路径经 `refreshNow()` 直接测，ticker 循环本身 10 行 select 不值得为其开竞态窗口。
3. **`MatchesKey` 只认原始字节序列**：`"ctrl+r"`/`"down"`/`"escape"` 字符串**不匹配**；必须 `\x12`/`\x1b[B`/`\x1b`/`\r`。探针实证（tui/keys.go：ctrl+letter 匹配 `rawCtrlChar` 的控制字节）。仓库既有组件测试全部如此，新测试照抄。
4. **测试环境要装完整 keybindings 表**：`tui.GetKeybindings()` 全局默认只有 TUIKeyBindings（tui.*），`app.mcp.*`（以及一切 app.*）在组件测试里不匹配，须 `tui.SetKeybindings(core.CreateKeybindingsManager("").KeybindingsManager)`（scoped_models_selector_test 先例）。
5. **导航预期**：面板行按字母序排 + failed server 的错误行占一个导航位——测试里「选到某个 server」要按实际行序按 down，不能假设行数。

## 5. 验收

- `go build ./... && go vet ./... && gofmt -l` 干净。
- 全量 `go test ./... -race -count=1 -timeout 600s` **单次** rc=0，30 包全 ok（AGENTS.md 铁律：`out=$(…); rc=$?`，日志 `/tmp/pig-test-out-mcp2.log`）。
- **tmux 真 TTY 冒烟**（`go build ./cmd/pig` + `go test -c ./mcp/` 的测试二进制以 `PIG_MCP_HELPER_PROCESS=1` 作 stdio server，项目 `.mcp.json` 指向它，`--approve` 信任，独立 `PIG_CODING_AGENT_DIR=/tmp/pig-mcp2/agent`）：
  1. `/mcp` → 面板打开：`▸ ○ helper (stdio)  disconnected`——**打开零连接副作用**（决策 B 实证）；
  2. enter 展开 → 懒连 + 列工具：`▾ ● helper (stdio) · 1 tools  connected · idle 6s` + `helper_echo — echo helper`；
  3. ctrl+d → 面板翻转 `· helper  disabled` + notice `disabled helper (saved to mcp.json)`，**`.mcp.json` 落盘 `"disabled": true`**（env 键保持、缩进干净）；
  4. ctrl+d → `enabled helper`，文件中键被删除，状态回 disconnected；
  5. ctrl+r → `reconnected helper` + `● connected`；
  6. esc → 面板关闭、编辑器复位；
  7. 收尾：kill tmux session，无 helper 进程残留。

## 6. 已声明边界（feat 文档同步）

- trust-blocked server 不可见（未信任项目配置进不了 manager，面板无合成行）。
- `.mcp.json` 手术回写归一化整文件 JSON 格式（缩进/键序）；JSON 无注释故无注释损失，未知字段保留。
- 启停写「胜者文件」：同名 server 多文件定义时只改生效那份，另一份定义仍在（链序调整会复活，语义上正确）。
- 面板工具列表非缓存：每次展开重列；keep-alive server 无额外开销，lazy server 每次展开可能触发一次连接（reap 后）。
- 过滤不做 `desc:` 前缀描述搜索（server 名 FuzzyFilter）。
- OAuth / directTools / metadata cache / resources / prompts 全部留 MCP-3。

## 7. 给下一个 agent 的备忘

- **MCP-3 素材**（MCP-1 §8 清单仍有效）：OAuth、directTools、resources/prompts、metadata cache、config importer、服务端（StreamableHTTPHandler session store 陷阱见 MCP-1 过程坑 3）。
- 面板组件的 mutate 模式（§4.1）可直接抄给任何需要「组件内 ticker + 用户输入双入口」的新组件。
- `mcp.test -test.run=^$` + `PIG_MCP_HELPER_PROCESS=1` stdio fake server 复用不变（MCP-1 §9）。
- 组件测试三件套：`RefreshInterval: -1`（或等效禁后台 goroutine）+ `SetKeybindings(core.CreateKeybindingsManager(""))` + 原始字节键序列。
