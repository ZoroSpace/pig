# MCP-1：pig 原生 MCP 客户端层（✅ 收口终稿，2026-09-27）

> 状态：**完成**。mcp/ 纯客户端包 + core 集成面 + 桥接测试 + 全量 -race + print mock 冒烟全部落地；偏差声明与 MCP-2 定义见 §7/§8。feat 文档：`docs/feat/MCP.md`。

## 0. 收口摘要

- **死锁悬案破案**：半成品卡在 `TestLazyConnectAndIdleReap`（两个等待者、栈上无持锁者），真相是 `Status()` 循环里 `st.mu.Lock()` 后**从未 Unlock**——持有者带着锁返回，自然不在任何栈上。handoff 里 TryLock/调度/starvation 猜想全部作废，修复为一行 `defer st.mu.Unlock()`。
- 修复后全量重跑暴露并修复 **4 个初版测试/实现缺口**（§6），mcp 包 12 测试 + core 桥接 5 测试全绿。
- core 集成面：`mcp_integration.go`（配置链/信任门控/代理工具）+ config/trust_manager/agent_session/services/sdk 五处小改。
- 验收门：build/vet/gofmt 干净；**全量 `go test ./... -race -count=1 -timeout 600s` 只跑一次 rc=0，30 包全 ok**（`/tmp/pig-test-out-mcp1.log`）；print 模式 mock 冒烟 search→call 全链路通过（§5）。
- 一处计划简化（机制核实后不需要）：原计划「buildRuntime 默认 active 追加 mcp」经核实**不需要**——`NewAgentSession`/`Reload` 的 buildRuntime 均带 `includeAllExtensionTools: true`，customTools 里的代理工具自动进 active 集，有回归测试锁定。

## 1. 批次目标与已批准的计划

用户决策：pig 原生实现 MCP（Model Context Protocol）客户端层（pi-mcp-adapter 是 pi 的 npm 扩展，pig 扩展系统在不做清单，故走原生 feat 批）。**完整计划已经用户批准**（ExitPlanMode 过）：

- 依赖：官方 SDK `github.com/modelcontextprotocol/go-sdk` v1.8.0（已进 go.mod，`go mod tidy` 已跑）
- 新建顶层纯客户端包 `mcp/`（只依赖 SDK + stdlib）
- 新建 `codingagent/core/mcp_integration.go`（配置文件链 + 信任门控 + 代理 ToolDefinition）
- 小改挂接：`config.go`（GetMCPPath）、`trust_manager.go`（mcp.json 进 trustRequiringProjectResources）、`agent_session.go`（MCPManager 字段照 cacheWarmer 同构 + Dispose 关停 + 默认 active）、`sdk.go`（构造 manager）
- 明确不做（MCP-2+）：/mcp 面板、OAuth、resources/prompts、directTools、sampling/elicitation/Tasks/mcpScript/config importer

## 2. 关键设计决策（已定，不要翻案）

1. **SDK 用法**（API 已用 go doc 逐个确认过，v1.8.0）：
   - `mcp.NewClient(&mcp.Implementation{Name:"pig", Version:…}, nil)` → `client.Connect(ctx, transport, nil)` → `*mcp.ClientSession`
   - stdio：`&mcp.CommandTransport{Command: exec.Cmd, TerminateDuration: 3s}`（自己建 exec.Cmd，方便设 Setpgid + stderr 捕获）
   - HTTP：`&mcp.StreamableClientTransport{Endpoint, HTTPClient}`；per-request header（bearer 等）SDK 不支持，用自定义 `http.RoundTripper` 注入（`headerTransport`，manager.go 底部）
   - 工具列举：`session.Tools(ctx, nil)` 迭代器（自带分页）；**返回按字母序**
   - 工具调用：`session.CallTool(ctx, &mcp.CallToolParams{Name, Arguments})` → `*mcp.CallToolResult{Content, StructuredContent, IsError}`
   - **重要语义**：tool handler 的业务错误（如 "boom"）SDK 按 MCP 规范返回 `IsError=true` 的正常结果而非 Go error（error 只用于协议级失败）。core 侧 Execute 处理 IsError 时把文本内容转成 Go error 返回，让 agent loop 走标准 IsError 工具结果路（已实现，回归测试锁定）
   - 测试：`mcp.NewInMemoryTransports()` 一对 transport，client 侧给 manager，server 侧 `go srv.Run(ctx, serverTransport)`
2. **TransportFactory seam**（ManagerOptions 注入，pig-only seam）：测试注入 in-memory transport；将来 unix socket 也走这。
3. **Manager 并发模型**：`Manager.mu` 管 server map + closed；每个 `serverState` 自带 mutex。**锁序：持 `st.mu` 可拿 `m.mu`（connectLocked 查 closed）；`Status()` 反例已修——先在 `m.mu` 下快照列表，放锁后再逐个拿 `st.mu`**。`Close`/`reapOnce` 先放 `m.mu` 再拿 `st.mu`。
4. **配置链**（core 侧，later-wins per-server 整条覆盖）：项目根 `.mcp.json`（**信任门控**）→ 项目 `.pig/mcp.json`（**信任门控**）→ 用户 `agentDir/mcp.json` → 全局 `~/.config/mcp/mcp.json`。Header 的 `!command`/`$env` 经 `ManagerOptions.ResolveHeaders` → `core.ResolveHeadersOrThrow`，**连接时执行**（非加载时）。
5. **生命周期**：`NewManager` 懒连接；eager/keep-alive 由 core 会话装配后 `go StartEager()`；idle 回收默认 5min、keep-alive 豁免；`Close()` 幂等、关 session + 进程组 kill。
6. **进程管理**：`mcp/proc_unix.go`（Setpgid + `kill(-pid, SIGKILL)`）/ `proc_windows.go`（taskkill /T /F），镜像 `codingagent/utils/shell_kill_*.go`。
7. **代理工具名 `mcp`**：schema 五字段 `status/search/describe/tool/args`（对齐 pi-mcp-adapter 输入形状）；工具全名 `server__tool`（CallTool 接受全名/display/raw 三形态）；裸名跨 server 歧义报错列候选（manager.resolveToolName）。MCP 图片内容映射 `types.ImageContent`；audio/resource 映射为描述文本（MCP-2 素材）。
8. **默认 active**：代理工具走 customTools 注册路，`includeAllExtensionTools: true` 使其自动进 active 集；PromptSnippet「先 search 再 call」自动进系统提示（冒烟实证）。

## 3. 最终文件清单

| 文件 | 状态 |
|---|---|
| `mcp/config.go` | ✅ Config/ServerConfig/Diagnostic + ParseConfigBytes/ParseConfigFile/LoadConfigFiles（校验失败的 server 丢弃并报诊断）|
| `mcp/manager.go` | ✅ Manager 全量（懒连接/idle 回收/include-exclude glob/toolPrefix/请求超时 60s 默认/Close 幂等/stderr ring buffer 64KB/headerTransport/mergeEnv）+ TransportFactory seam |
| `mcp/search.go` | ✅ 打分（exact 1000/prefix 700/词边界 600/含 500/全词 450/描述 200/120）|
| `mcp/proc_unix.go` / `proc_windows.go` | ✅ 进程组 kill |
| `mcp/config_test.go` / `manager_test.go` / `proc_*_test.go` | ✅ 8 + 12 + 1 测试函数全绿 |
| `codingagent/core/mcp_integration.go` | ✅ createMCPManager（文件链+信任门控+诊断）+ createMCPProxyToolDefinition（五 action 分派/IsError→Go error/内容映射）+ mcpGlobalConfigPathFn seam |
| `codingagent/config.go` | ✅ `GetMCPPath()` |
| `codingagent/core/trust_manager.go` | ✅ `.pig/mcp.json` 进 trustRequiringProjectConfigResources + 根 `.mcp.json` 单独 stat |
| `codingagent/core/agent_session.go` | ✅ `AgentSessionConfig.MCPManager` + customTools 注入（跨 Reload 存活）+ `Dispose()` 关停 |
| `codingagent/core/agent_session_services.go` | ✅ services 建 manager（Reload 后、trust 终局处）+ 诊断并入 `services.Diagnostics` + FromServices 透传 |
| `codingagent/core/sdk.go` | ✅ `CreateAgentSessionOptions.MCPManager` + 直连路自动建 + `go StartEager()` + 传入 AgentSessionConfig |
| `codingagent/core/mcp_integration_test.go` | ✅ 5 测试函数（桥接面） |
| `go.mod`/`go.sum` | ✅ +SDK v1.8.0（tidy 过） |

## 4. 死锁破案记录（原 §4，存档）

半成品现象：测试配置 `IdleTimeoutMs: 80`、`ReapInterval: 20ms`，`Status()`（disconnected）通过后 `SearchTools` 永久阻塞；dump 显示两个等待者锁地址相同、**全部 goroutine 栈上没有任何持锁者**。已排查排除：无 ABBA（当时的认知）、reapOnce 三条路径显式 Unlock、dump 无 SDK 痕迹。

**真相**：`Status()` 的循环体里 `st.mu.Lock()` 之后没有任何 `st.mu.Unlock()`（函数只有 `defer m.mu.Unlock()`）。测试第一步 `Status()` 把 st.mu 永久带走并返回——持锁者不在任何栈上，因为持锁者已经不存在了。「锁的持有人在栈上不可见」的最朴素解释：持有人已带着锁退出作用域。

**教训**：栈上无持锁者时，优先怀疑「持锁者已正常返回但漏 Unlock」（lock/unlock 不配对），其次才是调度玄学；`grep -A15 'func.*Status'` 级别的肉眼审查比 goroutine dump 分析更快。

**连带修复**：修掉漏 Unlock 后暴露第二层真 ABBA——`Status()` 持 `m.mu` 拿 `st.mu` vs `connectLocked` 持 `st.mu` 拿 `m.mu`（TestStartEagerConnects 卡死现形）。锁序铁律已写入 manager.go 注释与 feat 文档。

## 5. 验收

- `go build ./... && go vet ./... && gofmt` 干净。
- 全量 `go test ./... -race -count=1 -timeout 600s` **单次** rc=0，30 包全 ok（AGENTS.md 铁律：`out=$(…); rc=$?`，日志 `/tmp/pig-test-out-mcp1.log`）。
- **print 模式 mock 冒烟（全链路）**：`go test -c ./mcp/ -o mcp.test` 的测试二进制以 `PIG_MCP_HELPER_PROCESS=1 -test.run=^$` 作真实 stdio MCP server（helper-process 手法）+ Python mock OpenAI SSE 端点（三回合脚本：search → call → final text）+ 项目 `.mcp.json` 指向该二进制 + `--approve` 信任。输出实证：
  - `tool_execution_start {"search": ""}` → `tool_execution_end isError=false`：`1 MCP tool(s): - helper_echo (server "helper") echo helper`
  - `tool_execution_start {"tool": "helper_echo", "args": {"message": "smoke"}}` → `helper: smoke` isError=false
  - 系统提示 `<tools>` 段含 `mcp` 条目、描述与 PromptSnippet；agent_end 正常收尾（rc=0）。
  - 过程细节：`--mode json` 事件流在 **stderr**（stdout 留空）；trust-requiring 项目必须 `--approve`（或预置 trust.json），否则 `.mcp.json` 被门控跳过——冒烟顺带实证了信任门控的生产行为。

## 6. 修复清单（半成品 → 收口）

| # | 问题 | 修复 | 红→绿证据 |
|---|---|---|---|
| 1 | `Status()` 漏 `st.mu.Unlock()`（§4 悬案）| `defer st.mu.Unlock()` | TestLazyConnectAndIdleReap 卡死 → 全绿 |
| 2 | `Status()` 持 `m.mu` 拿 `st.mu` 与 connectLocked 反序（真 ABBA）| m.mu 下快照、放锁后逐个拿 st.mu | TestStartEagerConnects 120s 卡死 → 全绿 |
| 3 | `CallTool` 不剥 `server__` 全名前缀 | `strings.TrimPrefix(tool, server+"__")` | TestSearchAndCallInMemory 红 → 绿 |
| 4 | 测试给 "broken" server 也建了可用 fake（意图是连接失败）| factory 对 broken 返回 error | TestBrokenServerDoesNotHideOthers 红 → 绿 |
| 5 | `newTestManager` 防护 factory 拦截真 HTTP 测试且返回 nil transport（SDK 段错误）| 防护收窄到 stdio 配置 + 返回 error | TestStreamableHTTPE2EWithHeaders 段错误 → 绿 |
| 6 | 测试里 `StreamableHTTPHandler` 每请求 new 一次（session store 丢失 → "session not found"）| handler 提到 handler func 外 | 同上（同批修复）|
| 7 | header 解析失败测试注入 factory 走不到 HTTP header 路 | 去掉 factory，走真 HTTP transport 构建路（解析失败先于拨号）| TestHeaderTransportRejectFailure 红 → 绿 |
| 8 | 空 config manager `Close()` 永阻（reapLoop 未启动但 Close 等 reapDone）| 恒启动 reaper | core 链测试卡死 → 绿 |

## 7. 已知偏差声明（pig-only 面，无 pi 对齐物）

- `mcpServers` 配置键兼容生态（Claude Code/Cursor `.mcp.json` 直接复用）；server 级字段名 pig 自定（`idleTimeoutMs`/`requestTimeoutMs`/`lifecycle` 取值对齐 pi-mcp-adapter 语义但命名 snake_case 自定）。
- `!command` header 解析沿用 `core/resolve_config_value.go` 的**进程级缓存**语义（命令结果缓存）——token 轮换类 header 有过期窗口。
- 工具搜索打分是 pig-only 简版（不 import tui/fuzzy，mcp 包保持纯叶子）。
- call 的裸工具名歧义时报错列出候选（manager.resolveToolName），不做静默选择。
- 代理工具的 MCP server 列举**触发懒连接**（与 pi-mcp-adapter 同语义：发现即连接）。
- 代理工具渲染 nil，走 tool_execution 既有 fallback（pi-mcp-adapter 有自带渲染器，pig 声明偏差）。
- 直连 `CreateAgentSession` 自动建 manager 时诊断无处落（该路仅测试消费；生产走 services 路，诊断并入 services.Diagnostics）。
- audio/resource MCP 内容映射为描述性文本；图片正常映射 `types.ImageContent`。

## 8. MCP-2 定义（下一批素材，全部现成）

- **/mcp 面板**：`Manager.Status()` 数据面已备好（含 PID/IdleMs/LastError/ToolCount）。
- **resources/prompts**：SDK `ClientSession.GetPrompt/ListResources/ReadResource/Complete` 现成；代理工具可扩 describe 之外的 action 或独立工具。
- **OAuth**：SDK 的 OAuth 在 `internal/authutil` 不导出，需自写（pig 已有 ai/auth OAuth 先例）。
- **directTools 模式**：MCP 工具平铺注册（不做 proxy），注意工具名冲突与 include/exclude 语义。
- **sampling/elicitation/Tasks**：SDK 客户端侧有对应 handler 注册点。
- **config importer**：从 Claude Code/Cursor 配置导入（键已兼容，差路径发现）。
- **服务端**：`mcp.NewStreamableHTTPHandler` 可把 pig 暴露为 MCP server（注意 §6#6 的 session store 陷阱）。

## 9. 给下一个 agent 的备忘

- mcp 包的锁序铁律改动前先读 manager.go 顶部注释；任何「持 m.mu 拿 st.mu」的新代码都是 ABBA 候选。
- `mcp.test -test.run=^$` + `PIG_MCP_HELPER_PROCESS=1` 是现成的 stdio fake server，可复用于任何 pig MCP 冒烟。
- darwin 无 `timeout` 命令；go test 用 `-timeout` 硬超时（卡住会 dump goroutine）。
- 冒烟时 mock server 生命周期要干净（pkill 后确认端口释放），否则脚本化回合数会被残留进程吃掉（本次实证）。
