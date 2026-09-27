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

## 边界（MCP-2+ 素材，本批不做）

- /mcp 面板（`Manager.Status()` 已备好数据面）、OAuth、resources/prompts（SDK `GetPrompt/ListResources/ReadResource/Complete` 现成）、directTools 模式、sampling/elicitation/Tasks/mcpScript/config importer。
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
