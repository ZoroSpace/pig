# MCP：pig 原生 MCP 客户端层（feat 批 MCP-1，2026-09-27 收口）

- **日期**：2026-09-27
- **状态**：✅ MCP-1 完成。mcp 包 12 测试 + core 桥接 5 测试全绿；全量 `-race` 单次 rc=0（30 包，`/tmp/pig-test-out-mcp1.log`）；print 模式 mock 冒烟 search→call 全链路通过
- **动机**：用户决策 pig 原生实现 MCP（Model Context Protocol）客户端层。pi 无原生 MCP（pi-mcp-adapter 是 npm 扩展，pig 扩展系统在「不做清单」），故为 pig-only 面，无 pi 对齐物。配置文件键兼容生态（`mcpServers`），Claude Code / Cursor 的 `.mcp.json` 可直接复用
- **依赖**：官方 SDK `github.com/modelcontextprotocol/go-sdk` v1.8.0
- **涉及文件**：`mcp/`（新顶层纯客户端包：config/manager/search/proc_*）、`codingagent/core/mcp_integration.go`（配置链+信任门控+代理工具）、`codingagent/config.go`（GetMCPPath）、`core/trust_manager.go`（mcp.json 门控）、`core/agent_session.go`（MCPManager 字段+Dispose）、`core/agent_session_services.go`（manager 装配+诊断）、`core/sdk.go`（MCPManager option）

## 设计要点（用户已批准的计划，详见 handoff 终稿 §2）

1. **`mcp/` 包是叶子**：只依赖 SDK + stdlib。config 路径、信任门控、`!command` header 解析全部留在 core。
2. **Manager 并发模型**：`Manager.mu` 管 server map + closed；每个 `serverState` 自带 mutex 串行化该 server 的 connect/close/reap/call。**锁序铁律：持 `st.mu` 期间可拿 `m.mu`（connectLocked 查 closed），反之必须先放 `m.mu` 再拿 `st.mu`**——`Status()` 曾因此真死锁（见过程坑 1）。
3. **懒连接 + idle 回收**：`NewManager` 不连任何东西；eager/keep-alive 由 core 在会话装配后 `go StartEager()`；idle 回收默认 5min、keep-alive 豁免；`Close()` 幂等、关 session + 进程组 SIGKILL（`proc_unix.go` Setpgid + kill(-pid)，`proc_windows.go` taskkill /T /F，镜像 `codingagent/utils/shell_kill_*.go`）。
4. **传输**：stdio = `mcp.CommandTransport`（自建 exec.Cmd，Setpgid + stderr 捕获进 64KB ring buffer）；HTTP = `mcp.StreamableClientTransport` + 自定义 `headerTransport`（RoundTripper 注入 per-request headers，SDK 不直接支持）。测试注入 `TransportFactory`（options 注入，pig-only seam；in-memory transport / 未来 unix socket 都走这里）。
5. **代理工具 `mcp`**：schema 五字段 `status/search/describe/tool/args`（对齐 pi-mcp-adapter 输入形状）；工具全名约定 `server__tool`（CallTool 接受全名/display/raw 三形态）；裸名跨 server 歧义时报错列候选，不静默选择。**发现即连接**（search 触发懒连接，与 pi-mcp-adapter 同语义）。渲染走 tool_execution 既有 fallback（nil renderer），pig-only 偏差声明。
6. **默认 active**：代理工具走 customTools 注册路——`NewAgentSession` 与 `Reload` 的 `buildRuntime` 都带 `includeAllExtensionsTools: true`，custom 工具自动进 active 集，**无需改 defaultActiveToolNames**（handoff 原计划的 buildRuntime 追加一步经机制核实后不需要）。PromptSnippet「先 search 再 call」经 `rebuildSystemPrompt` 自动进系统提示（冒烟实证）。
7. **IsError 语义**：MCP 规范里工具业务错误（如 "boom"）是 `IsError=true` 的正常结果；core Execute 处把它转成 Go error 返回，让 agent loop 走标准 IsError 工具结果路。

## 配置链与信任门控

- 顺序（later-wins，per-server 整条覆盖）：项目根 `.mcp.json` → 项目 `.pig/mcp.json` → 用户 `agentDir/mcp.json` → 全局 `~/.config/mcp/mcp.json`（`os.UserConfigDir`）。
- **信任门控**：`.pig/mcp.json` 追加进 `trustRequiringProjectConfigResources`；根 `.mcp.json` 在 `HasTrustRequiringProjectResources` 单独 stat（根级文件，不在 `.pig/` 下）——两者存在即令项目成为 trust-requiring；未信任项目跳过加载 + warning 诊断，用户级/全局文件不受信任影响。
- Header 的 `!command`/`$env` 经 `ManagerOptions.ResolveHeaders` 接 `core.ResolveHeadersOrThrow`，**连接时执行**（非加载时）；沿用 resolve_config_value 的进程级缓存语义——token 轮换类 header 有缓存过期窗口，声明偏差。

## 装配与生命周期

- services 路（print/interactive 生产路）：`CreateAgentSessionServices` 在 `Reload` 之后（trust 已终局）建 manager，诊断并入 `services.Diagnostics`；`CreateAgentSessionFromServices` 透传。
- 直连路（tests）：`CreateAgentSessionOptions.MCPManager == nil` 时自动建（诊断无处落，丢弃，声明边界）。
- 生命周期：manager 每 session 一个，**跨 Reload 存活**（同一 session 的 buildRuntime 重建不重建 manager），`Dispose()` 关停（runtime teardown 覆盖 /new /fork /resume）。

## 边界（MCP-3+ 素材，本批不做；/mcp 面板已于 MCP-2 交付，见下节）

- OAuth、resources/prompts（SDK `GetPrompt/ListResources/ReadResource/Complete` 现成）、directTools 模式、sampling/elicitation/Tasks/mcpScript/config importer。
- 代理工具渲染 nil（fallback 兜底）；audio/resource 内容映射为描述文本。

## 回归测试

- `mcp/config_test.go`（8 函数）+ `mcp/manager_test.go`（12 函数，含 stdio helper-process e2e、HTTP httptest e2e + header 注入断言、header 解析失败路、idle 回收、broken server 隔离）+ `mcp/proc_*_test.go`。
- `core/mcp_integration_test.go`（5 函数）：配置链 later-wins + 未信任跳过与 warning、信任门控红绿（`.mcp.json`/`.pig/mcp.json` 令项目 trust-requiring）、代理工具注册+默认 active+Reload 存活、Execute 四 action 分派（status/search/describe/call + 全名/裸名/IsError→Go error）、Dispose 关停（Dispose 后 CallTool 报 closed）。
- **红→绿实证**（初版全量跑既有失败，修复后转绿，非 stash 而是天然 test-first）：`Status()` 漏 Unlock（TestLazyConnectAndIdleReap 卡死）→ 修复绿；`Status` ABBA（TestStartEagerConnects 卡死）→ 修复绿；`CallTool` 不剥 `server__` 前缀（TestSearchAndCallInMemory 红）→ 修复绿；空 config manager `Close()` 永阻（core 链测试卡死）→ 修复绿。
- **print 模式 mock 冒烟**（全链路）：`go test -c ./mcp/` 的测试二进制以 `PIG_MCP_HELPER_PROCESS=1 -test.run=^$` 作真实 stdio MCP server + Python mock OpenAI SSE 端点脚本化三回合 → `pig --mode json --approve -p` 输出实证：`mcp {"search": ""}` 发现 helper_echo（含描述）→ `mcp {"tool": "helper_echo", "args": {"message": "smoke"}}` 真子进程调用 → toolResult `helper: smoke` isError=false → 模型收尾。系统提示 `<tools>` 段含 `mcp` 条目与 PromptSnippet。

## 过程坑

1. **`Status()` 漏 `st.mu.Unlock()`（handoff 悬案真相）**：MCP-1 半成品卡在「TestLazyConnectAndIdleReap 死锁：两个等待者、栈上无持锁者」——真相朴素得惊人：`Status()` 在循环里 `st.mu.Lock()` 后**从未 Unlock**（只有 `defer m.mu.Unlock()`），测试第一步 Status 把锁永久带走，持有者早已带着锁返回，自然不在任何栈上。handoff 里的 TryLock/调度猜想全部不需要。
2. **`Status()` 真 ABBA**：修掉漏 Unlock 后暴露第二层——Status 持 `m.mu` 再拿 `st.mu`，而 `connectLocked`（StartEager 后台 goroutine / listServerTools 路径）持 `st.mu` 再拿 `m.mu`，TestStartEagerConnects 在 120s 包超时 dump 里现形。修法：Status 先在 `m.mu` 下快照 server 列表，放锁后再逐个拿 `st.mu`。
3. **SDK StreamableHTTPHandler 每请求 new 一次会丢 session**：handler 实例各持 session store，per-request 构造导致 initialize 之后全部 "session not found"——测试里的低级坑，但对将来 MCP-2 写 server 侧有普适提醒。
4. **空 config 的 manager `Close()` 永阻**：`NewManager` 只在非空 config 时启动 reapLoop，`Close()` 却无条件等 `reapDone`——core 测试用空 config manager 时卡死。改为恒启动 reaper（零 server 的 30s ticker 成本为零）。
5. **`os.UserConfigDir` 在 darwin 不认 XDG_CONFIG_HOME**：全局 mcp.json 测试隔离不能走 env，改包级 var seam `mcpGlobalConfigPathFn`；用户级路径以显式 agentDir 参数优先（`GetMCPPath()` 仅回落），避免测试读真机 `~/.pig`。
6. **`--mode json` 事件流在 stderr**：print 模式 JSON 事件走 stderr（stdout 留空），冒烟解析时别看错管道。

---

# MCP-2：/mcp 交互面板 + 启停持久化（✅ 2026-09-27）

> handoff 终稿：`docs/handoffs/MCP-2.md`。两个范围决策用户拍板：启停**持久化到来源 mcp.json**（`ServerConfig.Disabled` pig-only 字段）；工具列表**展开时才连接**（无 metadata cache，列工具 = 触发懒连接；面板打开本身零连接副作用）。

## 交付面

- **mcp 包**：`ServerConfig.Disabled`（`disabled,omitempty`，校验不变）+ `Config.Sources`（`LoadConfigFiles` 记录每 server 胜者文件）+ `ServerStatus.Disabled/SourcePath` + `statusDisabled = "disabled"`；disabled 全路径跳过（StartEager 不连、`SearchTools` 不出其工具、`CallTool`/`ListServerTools`/`Reconnect` 报错、`Status()` 报 disabled）；新导出 API `ListServerTools(ctx, name)`（`listServerTools` 导出化，懒连接语义不变）、`Reconnect(ctx, name)`（close + connect，连接错误原样上抛）、`SetServerDisabled(name, disabled)`（内存翻转 + 文件手术；disable 时关 session、enable 回 disconnected 懒连）。
- **JSON 手术（`mcp/disable.go` `setServerDisabledInFile`）**：`.mcp.json` 是共享文件（Claude Code/Cursor 同格式），**绝不能 struct marshal 往返**（会丢 pig 不建模的未知字段）——generic map 解码（`json.Decoder.UseNumber` 保数字字面量）→ 只改 `mcpServers[name].disabled`（enable **删键**而非写 false）→ `Encoder.SetEscapeHTML(false) + SetIndent 2 空格` 写回 + 保留原文件 mode。声明偏差：整文件 JSON 归一化（缩进/键序）；JSON 无注释故无注释损失。
- **core**：`AgentSession.MCPManager()` 导出 getter（nil 安全）；`BuiltinSlashCommands` 追加 `/mcp`（pig-only，autocomplete 自动收编）；面板键位 defs `app.mcp.reconnect/toggleDisabled/copyError`（ctrl+r/ctrl+d/ctrl+y，pig-only 追加进 keybindingsOrder）。
- **components/mcp_panel.go（~460 行，pig-only）**：`MCPPanelProvider` 接口（`*mcp.Manager` 天然实现，components 测试用 fake）；骨架照 thinking_selector（DynamicBorder + 过滤输入 + SelectList + 提示行 + esc）；行渲染 ▾/▸ + 状态符（● connected / ◌ connecting / ✗ failed / · disabled / ○ disconnected）+ 名称 + (transport·lifecycle) + 工具数 + 彩色状态后缀（connected · idle Xs / connecting / failed / disabled），failed 的 LastError 红字挂行下；enter 展开（后台 `ListServerTools`，loading 占位行/失败错误行）；2s ticker 轮询 `Status()` 刷新快照；notice 行反馈动作结果。
- **interactive 接线**：`processSubmitText` 加 `case "/mcp"`（在 deferred 判断之前）；`showMcpPanel()` 照 showSelector 装配（`Mutate: m.mutateUI` 串行化跨 goroutine UI 变更、剪贴板走 `interactiveCopyToClipboard` seam、`dispose: panel.Dispose` 停 ticker）；manager nil/空 → `showStatus("No MCP servers configured")`。

## 关键设计决策

1. **并发模型（组件侧）**：TUI 锁可重入（goroutine-local `lockOwner`），`HandleInput` 本就持锁——组件所有入口（HandleInput/refreshNow/后台回调落地）统一走 `p.mutate`（interactive 传 `m.mutateUI`=RunExclusive；HandleInput 内重入零成本，后台 goroutine 新取锁），内部方法全部假设已持锁（`*Locked` 命名），无组件级 mutex。ticker 回调只 `RequestRender(false)` 不嵌 TUI 锁（footer resource poller 同型）。
2. **测试与 ticker**：组件测试传 `RefreshInterval: -1` 禁用 ticker goroutine（无 TUI 锁的测试环境里 ticker 与测试主 goroutine 竞态）；刷新路径经 `refreshNow()` 直接测。测试须 `tui.SetKeybindings(core.CreateKeybindingsManager("").KeybindingsManager)` 安装完整表——全局默认表只有 tui.*，app.mcp.* 键在纯 tui 表下不匹配（探针实证）。
3. **测试键序列**：`MatchesKey` 匹配原始字节序列（``=ctrl+r、`[B`=down、``=enter），不是 `"ctrl+r"` 字符串——组件测试全部用原始序列。
4. **启停写胜者文件**：同名 server 多文件定义 later-wins，`Sources[name]` 记胜者，启停只写生效那份；另一份定义仍在（调整链序会复活——语义正确，文档声明）。
5. **trust-blocked 边界**：未信任项目的配置根本进不了 manager（MCP-1 信任门控），面板无从展示其 server——不做合成行，声明边界。

## 回归测试（新增 17 函数）

- `mcp/disable_test.go`（7 函数）：disabled 全路径跳过（search/call/list/reconnect/status + eager 不连）；Reconnect（keep-alive 重连 + factory 失败上抛 + unknown server）；SetServerDisabled 文件手术（enable 删键/兄弟 server 与未知字段/大整数/preserved mode/尾换行）；无 sourcePath 仅内存翻转（disable 关 session + enable 懒连复活）；Sources later-wins。
- `components/mcp_panel_test.go`（10 函数）：状态后缀渲染（connected+idle/connecting/failed+错误行/disabled）、展开懒连接（tools 落地/失败错误行/收起）、活过滤、ctrl+r（成功+失败 notice）、ctrl+d（disable+enable 往返）、ctrl+y（copy+无错 notice）、esc 回调、refresh 快照、刷新后选中保持；接口满足性 pin `var _ MCPPanelProvider = (*pigmcp.Manager)(nil)`。
- 存量：mcp 包原 17 函数 + core 桥接 5 函数零改动保持绿。

## 验收

- build/vet/gofmt 干净；全量 `go test ./... -race -count=1 -timeout 600s` **只跑一次** rc=0，30 包全 ok（`/tmp/pig-test-out-mcp2.log`）。
- **tmux 真 TTY 冒烟**（`mcp.test` helper-process 手法 + `--approve` 信任 + 独立 `PIG_CODING_AGENT_DIR`）：/mcp 面板打开（helper disconnected，**打开零连接副作用**）→ enter 展开懒连（`▾ ● helper (stdio) · 1 tools  connected · idle 6s` + `helper_echo — echo helper` 工具行）→ ctrl+d 禁用（面板翻转 `disabled` + **`.mcp.json` 落盘 `"disabled": true`**，未知结构零损伤）→ ctrl+d 重新启用（键删除、回 disconnected）→ ctrl+r 重连（`reconnected helper` + connected）→ esc 关面板。
