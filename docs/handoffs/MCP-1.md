# MCP-1：pig 原生 MCP 客户端层（进行中，未收批）

> 状态：**源码半成品，工作树未提交**。mcp 纯客户端包已写完且大部分测试绿；卡在一个诡异的 mutex 死锁（见 §4），core 集成面尚未开始。本文给下一个 agent 接手用。

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
   - 工具调用：`session.CallTool(ctx, &mcp.CallToolParams{Name, Arguments})` → `*mcp.CallToolResult{Content []mcp.Content, StructuredContent, IsError}`
   - **重要语义**：tool handler 的业务错误（如 "boom"）SDK 按 MCP 规范返回 `IsError=true` 的正常结果而非 Go error（error 只用于协议级失败）。core 侧 Execute 处理 IsError 时应把文本内容转成 Go error 返回，让 agent loop 走标准 IsError 工具结果路
   - 测试：`mcp.NewInMemoryTransports()` 一对 transport，client 侧给 manager，server 侧 `go srv.Run(ctx, serverTransport)`
2. **TransportFactory seam**（manager.go，`ManagerOptions.TransportFactory func(server string, cfg ServerConfig) (mcp.Transport, error)`）：测试注入 in-memory transport；将来 unix socket 也走这。pig 测试 seam 惯例（包级 var + t.Cleanup）在此不适用，用的是 options 注入，已声明是 pig-only seam。
3. **Manager 并发模型**：`Manager.mu` 管 server map + closed；每个 `serverState` 自带 mutex 串行化该 server 的 connect/close/reap/call。锁序：`listServerTools` 是 st.mu 内套 m.mu（connectLocked 里查 closed）；`reapOnce`/`Close` 都是先放 m.mu 再拿 st.mu，无 ABBA。
4. **配置链**（core 侧做，later-wins 整条覆盖）：项目根 `.mcp.json`（**必须信任门控**）→ 项目 `.pig/mcp.json` → 用户 `~/.pig/agent/mcp.json` → 全局 `~/.config/mcp/mcp.json`。Header 的 `!command`/`$env` 解析用 `core.ResolveHeadersOrThrow`，**连接时执行**（不是加载时），通过 `ManagerOptions.ResolveHeaders` 注入。
5. **生命周期**：`NewManager` 不连接任何东西（lazy）；eager/keep-alive 用 `manager.StartEager()`（core 在会话装配后开 goroutine 调）；idle 回收默认 5min、keep-alive 豁免；`Close()` 幂等、关 session + 进程组 kill。
6. **进程管理**：`mcp/proc_unix.go`（Setpgid + `kill(-pid, SIGKILL)`）/ `proc_windows.go`（taskkill /T /F），镜像 `codingagent/utils/shell_kill_*.go`。SDK Close 自己会 close stdin→SIGTERM，我们补进程组 SIGKILL 防孙进程。
7. **代理工具名 `mcp`**（core 侧）：schema 五字段 `status/search/describe/tool/args`（对齐 pi-mcp-adapter 输入形状）；工具全名约定 `server__tool`（双下划线分隔，search 返回的显示名带 toolPrefix）；裸名若跨 server 唯一也可直接用（manager 已实现 resolve）。MCP 图片内容映射 `types.ImageContent`。
8. **默认 active**：配置非空时 `buildRuntime` 默认 active 工具集追加 `"mcp"`；PromptSnippet 写「先 search 再 call」。

## 3. 当前文件清单（工作树全部未提交）

| 文件 | 状态 |
|---|---|
| `mcp/config.go` | ✅ 完整：Config/ServerConfig/Diagnostic + ParseConfigBytes/ParseConfigFile/LoadConfigFiles（校验失败的 server 丢弃并报诊断）|
| `mcp/manager.go` | ✅ 完整：Manager 全量（懒连接/idle 回收/include-exclude glob/toolPrefix/请求超时 60s 默认/Close 幂等/stderr ring buffer 64KB/headerTransport/mergeEnv）+ `TransportFactory` seam |
| `mcp/search.go` | ✅ 完整：打分（exact 1000/prefix 700/词边界 600/含 500/全词 450/描述 200/120）|
| `mcp/proc_unix.go` / `proc_windows.go` | ✅ |
| `mcp/config_test.go` | ✅ 8 个测试全绿 |
| `mcp/manager_test.go` | 🔶 12 个测试：in-memory 系全绿；**`TestLazyConnectAndIdleReap` 卡死锁（§4）；`TestStdioTransportE2E`/`TestStreamableHTTPE2EWithHeaders`/`TestHeaderTransportRejectFailure`/`TestStartEagerConnects`/`TestKeepAliveNotReaped`/`TestRequestTimeout`/`TestBrokenServerDoesNotHideOthers`/`TestUnknownServerAndManagerClose` 未跑完验证（被死锁测试堵住）** |
| `mcp/proc_unix_test.go` / `proc_windows_test.go` | ✅ processAliveOnce |
| `go.mod`/`go.sum` | +SDK v1.8.0（tidy 过）|
| `core/mcp_integration.go` 及 core 挂接 | ❌ 未开始 |

跑过且绿的命令：`go build ./mcp/ && go vet ./mcp/ && gofmt -l mcp/` 干净。
注意 macOS 无 `timeout` 命令，用 go test 自带 `-timeout` 做硬超时（卡住会 dump goroutine）。

## 4. 当前卡点：TestLazyConnectAndIdleReap 死锁（先修这个）

**现象**：测试配置 `IdleTimeoutMs: 80`、`ReapInterval: 20ms`，第一步 `Status()`（disconnected）通过，随后 `SearchTools` 永久阻塞，go test 10s 超时 dump：

- goroutine 5（测试）：`listServerTools` 的 `st.mu.Lock()`（manager.go:399）**等待中**
- goroutine 6（reaper）：`reapOnce` 的 `st.mu.Lock()`（manager.go:654）**等待中**
- **两个等待者锁地址相同（0xc0000005c8），但全部 4 个 goroutine 的栈上没有任何持锁者**——即锁的持有人在栈上不可见或错过唤醒

完整 dump 已存 `/tmp/mcp-reap-dump.log`。

**已排查排除**：无 ABBA（锁序如 §2.3）；reapOnce 三条路径都显式 Unlock；`closeSessionLocked` 若阻塞栈上会显示；不是 Status/Close 的嵌套。dump 里只有 4 个 goroutine（TestMain/测试/reaper/alarm），**没有 SDK 内部 goroutine 持锁的痕迹**。

**下一个 agent 的排查建议（按性价比排序）**：
1. 最可疑：in-memory transport 下 `client.Connect` 或某 SDK 调用在**持 st.mu 期间**同步回调进入 SDK，SDK 内部又等我们的什么（比如等待 `srv.Run(ctx, serverTransport)` 那个 goroutine 起来），而该 goroutine 因调度/starvation 没跑起来（-race 下 20ms 的 reaper tick 高频抢锁加剧）。先试**最小改动**：把 `listToolsLocked` 里的 `session.Tools()` 移出 st.mu？不行——要先解决连接本身。**建议先做实验**：把 ReapInterval 改大（比如 1s）看测试是否直接全绿（若是，就是持锁期间与 reaper 高频争锁的调度问题，而不是严格死锁）；再试 reaper 完全不碰 st.mu（改用 `st.mu.TryLock`，拿不到就跳过本轮）——**TryLock 跳过是合理终态**，回收不急这一轮。
2. 次可疑：`m.logf` / ring buffer 竞争（可能性低）。
3. 用 `-race` + 缩小 ReapInterval/IdleTimeout 参数做二分，或给 reapOnce 换 `TryLock` + 失败时 `runtime.Stack` 打点确认持锁者。
4. 若是 SDK 内部在持锁期间需要"并发推进"（如 in-memory Connect 等对端 goroutine），终态修法可能是：**connect 时不要在持 st.mu 状态下同步等 Connect 完成**（先 TryLock 语义不适用则把 connect 的等待拆出去），或 reaper 用 TryLock。**倾向终态：reapOnce 改 `st.mu.TryLock()`（拿不到直接跳过，下轮再收）**——简单、语义正确、消除争抢窗口。

修好后按 §5 继续。

## 5. 下一步清单（按序）

1. **修 §4 死锁**，`go test ./mcp/ -race -count=1 -timeout 120s` 全绿（12 个测试全过，含三个 e2e：stdio helper-process、HTTP httptest + header 解析注入断言、header 解析失败路）
2. `core/mcp_integration.go`：
   - `loadMCPConfigFiles(cwd, agentDir)` 文件链（§2.4）+ 信任门控（未信任项目跳过 `.mcp.json` + warning 诊断）→ 返回 `*mcp.Manager` + `[]AgentSessionRuntimeDiagnostic`
   - `createMCPProxyToolDefinition(mgr *mcp.Manager) *tools.ToolDefinition`：name `mcp`，五 action 分派；`IsError` 结果转 Go error；图片内容映射 `types.ImageContent`；轻量 RenderCall/RenderResult（tui.NewText 摘要 + 文本预览即可，或先 nil 走 fallback——fallback 渲染已存在，tool_execution.go createCallFallback/createResultFallback）
   - `AgentToolResult` 构造参考 `core/tools/ls.go`（`resultOf`/`asTextContent` helper、`decodeToolParams[T]`）
3. **core 挂接小改**（<100 行）：
   - `codingagent/config.go`：`GetMCPPath()`（挨着 GetSettingsPath :248）
   - `core/trust_manager.go:61`：`trustRequiringProjectResources` 追加 `"mcp.json"`
   - `core/agent_session.go`：`AgentSessionConfig` + `MCPManager` 字段（cacheWarmer 同构 :618）；`NewAgentSession` 把代理工具塞 `s.customTools`（**manager 本体跨 Reload 存活，customTools 走 refreshToolRegistry 自动重挂**）；`Dispose()`（:1891）调 `mgr.Close()`；`buildRuntime` 默认 active（:4878）配置非空追加 `"mcp"`
   - `core/sdk.go` CreateAgentSession：cwd/trusted 可得处构造（信任状态在 main.go createRuntimeFactory 的 resolveProjectTrusted 缓存里，确认穿进来；若 sdk.go 拿不到 trust，就在 services 层（`agent_session_services.go:159`）加参数）；诊断并入 services.Diagnostics
   - 注意：`CreateAgentSession`（sdk.go :148）与 `CreateAgentSessionServices` 是两条装配路，别只接一条
4. **core 桥接测试**（fake manager 注入）：工具注册/默认 active/system prompt snippet（PromptSnippet 会经 rebuildSystemPrompt :2183 自动进系统提示）/Dispose 关停/信任门控红绿（未信任项目 .mcp.json 不加载且出 warning）
5. **验收门**：build/vet/gofmt 干净 + 全量 `go test ./... -race -count=1 -timeout 600s` **只跑一次**（AGENTS.md 铁律：输出存 `out=$(…); rc=$?` 后多处消费，严禁重跑提取）+ print 模式 mock 冒烟（本地起 fake MCP server，走 `mcp search → call` 全链路；可用 mcp 包的 helper-process 手法）
6. **文档**：`docs/feat/MCP.md`（house 结构照 MAC-CLIPBOARD-IMAGE.md：日期/状态/动机/涉及文件 → 实现 → 边界 → 回归测试含 stash 验证 → 过程坑）+ 收批 `docs/handoffs/MCP-1.md` 终稿（表格 + 偏差声明 + 验收 + MCP-2 定义）+ AGENTS.md「已交付」段补一行 + 若改了 AGENTS.md 机制类规则才更新

## 6. 已知偏差声明（写进 feat 文档的素材）

- `mcpServers` 配置键兼容生态（Claude Code/Cursor .mcp.json 可直接复用）；但 server 级字段名是 pig 自定（`idleTimeoutMs`/`requestTimeoutMs`/`lifecycle` 取值对齐 pi-mcp-adapter 语义但命名 snake_case 自定）——与 pi 无对齐物，属 pig-only 面
- `!command` header 解析沿用 `core/resolve_config_value.go` 的**进程级缓存**语义（命令结果缓存）——对 token 轮换类 header 有过期语义，文档要写明
- 工具搜索打分是 pig-only 简版（不 import tui/fuzzy，mcp 包保持纯叶子）
- call 的裸工具名歧义时报错列出候选（manager.resolveToolName），不做静默选择
- proxy tool 的 MCP server 列举会**触发懒连接**（与 pi-mcp-adapter 同语义：发现即连接）

## 7. 探索期情报（接手者不必重查）

- 工具注册面/执行面/渲染面完整地图：`ToolDefinition` 在 `codingagent/core/tools/tool_context.go:159`（Parameters 是 `json.RawMessage`）；`refreshToolRegistry` 在 agent_session.go:4669（customTools 插入序：base 先、custom 同名覆盖）；渲染 fallback 在 `components/tool_execution.go`（nil renderer 有兜底）；`WithBuiltInRenderers` 只认 8 个内置名
- 配置/生命周期地图：`GetAgentDir()` config.go:219（PIG_CODING_AGENT_DIR）；`ResolveHeadersOrThrow` resolve_config_value.go:410；Dispose 链 `AgentSessionRuntime.teardownCurrent` 覆盖 /new /fork /resume /reload
- 测试惯例：包级 var seam（t.Cleanup 恢复）、httptest 假后端（`ai/api/openai_completions_test.go:42` newMockSSEServer、bedrock e2e）、helper-process 先例（clipboard 五文件）
- SDK 另有的能力（MCP-2 素材）：`ClientSession.GetPrompt/ListResources/ReadResource/Complete`、`mcp.NewStreamableHTTPHandler`（服务端）、OAuth 在 `internal/authutil`（不导出，MCP-2 OAuth 要自己写）
