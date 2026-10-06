# S15：pi v1.0.3 → v1.0.4 同步（开工稿预写于 S14 收口后，2026-10-06 收口）

**状态**：**终稿（2026-10-06 收口）**。pi diff 范围 `v1.0.3..v1.0.4`，共 28 commit，实移 5 项 + Version + 零操作 22 commit（含 c30840c2e 整 commit 定论），单批收口。

## 0. 终稿追加（开工稿 → 实际执行）

- **验收门**：build/vet/gofmt 干净；全量 `-race` 单次 rc=0 31 包（`/tmp/pig-test-out-s15.log`）；tmux 冒烟四项全通（复用 S13 mock 环境 `/tmp/pig-s13-smoke`：header `v1.0.4`、`hello smoke`→`pong` ↑1↓1、ctrl+d 干净退出、jsonl usage input 1/output 1/cost.total 0，mock-requests.log 仅 1 条 127.0.0.1:8941 → 零真实 API）。tmux 备忘：本次用户默认 socket 为残留死 socket，冒烟改用 `-S /tmp/pig-s15-tmux.sock` 独立 socket + `-f /dev/null`（用户 tmux 配置插件会让无 TTY 的 server 立即退出）；mock 进程/独立 socket 已清理。
- **Version** 1.0.3→1.0.4（config.go + config_test.go）。
- **1.6 skills indirect 定论为零操作**：pig 无 hiddenDeclarations（全仓零命中），`declaredTools == selectedTools` 恒成立，pi 的 `?? "indirect"` 分支不可达——skills.go 未改动。
- **1.5 实现细节**：`serverState` 新增 `connectMu/connectCancel`（独立小锁，原因同 challengeMu——connect 持 mu 全程）；`connectLocked` 在 `client.Connect` 前注册 cancel 柄 + `m.closed` 重查关闭注册竞态窗口，重查命中时走 connect-failure 同款清理（closeProcessLocked，防 stdio 进程泄漏）；`Close()` 先 cancel 再等 `st.mu`。测试实证：ConnectTimeout 60s 下 Close 5s 内返回（`TestCloseAbortsInFlightConnect`，pig 语义断言 per-server 状态为 disconnected 而非 pi 的 closed）。
- **1.4 实现细节**：matcher 落在新文件 `codingagent/core/tool_name_matcher.go`（`toolPatternRegExp` 用 `strings.Split(pattern, "*")` + `regexp.QuoteMeta`，与 pi 的字符类转义等价）；agent_session 两字段 map→matcher 函数形态，isAllowedTool 闭包按 pi 顺序（excluded 先判）重写并注释声明 MCP 偏差；sdk.go 的 `excludedToolNameSet` 同步换 matcher；help 文案 pig 化（无 mcp__ 表述），examples 加一条模式示例（Printf 实参同步 +1 个 AppName）。
- 新测试：`ai/utils/retry_test.go` +1（pending stream canceled 两形态）、`codingagent/modes/interactive/theme/syntax_highlight_test.go` +3 子测（多行逐行染色与 pi 期望逐字节一致、subst scope 映射、markdown 双路）、`integrations/mcp/oauth_test.go` +1（stub 注册端点断 application_type 推导 5 例）、`codingagent/core/tool_name_matcher_test.go`（5 子测含 pi #5109 元字符字面量回归）、`codingagent/core/tool_patterns_test.go`（3 子测 session 级通配过滤/激活/空 allowlist）、`codingagent/cli/args_test.go` +1（--no-mcp）、`integrations/mcp/manager_test.go` +1（Close 中断 in-flight connect）。
- README 工具权限段补 `*` 通配与 `--no-mcp`；verify-matrix 无新 provider 面，不动。

## 0.0 用户拍板记录（2026-10-06）

- **pi-env 进不做清单**：v1.0.4 新包 `@earendil-works/pi-env`（SSH 远程执行环境 + Rust 远端 daemon，Durable 的 ExecutionEnv 实现）→ durable 同族，随 durable 延后，本批写入 AGENTS.md 不做清单。
- **第 4 项 MCP allowlist 语义 pig 化**已确认按代理工具形态裁剪（见 1.4 偏差）。

## 1. 实移项（按施工顺序）

### 1.1 ai retry 补 pending stream canceled —— pi `5b6c792b4`

`retryableProviderErrorPattern`（`ai/utils/retry.go:51`）在 `"http2 request did not get a response"` 后加 `"pending stream has been canceled"`（含 pi 的注释：Bedrock HTTP/2 session 5 分钟超时，请求未发出可安全重试，#10379/#10379）。pi 测试 `retry.test.ts` +8 行 → `ai/utils/retry_test.go` 补子测。

### 1.2 多行 token 语法高亮保色 + subst —— pi `b9ab918c6`

- `codingagent/utils/syntax_highlight.go:336` `RenderHighlightedHtml` 的 `flushText`：formatter 命中时按 `\n` 逐行染色再拼回（跨行 span 整段掉色，python docstring 场景 #10143）。
- subst → text：pi highlight.js `subst` scope（字符串插值）映射到正文色。pig 走 chroma：`chromaScopeNames`（syntax_highlight.go:46）补 `chroma.LiteralStringInterpol: "subst"`，`theme.go:1806` `buildCliHighlightTheme` 表补 `"subst": t.Fg(ColorText, …)`（核对 ColorText 常量名）。开工已核对 chroma 无逐字 `subst` scope，`LiteralStringInterpol` 是最近对应物，注释声明。
- pi 测试 `syntax-highlight.test.ts` +19 → pig 对应测试文件补多行 docstring 用例。

### 1.3 MCP OAuth 动态注册带 application_type —— pi `147b50281`（SEP-837）

pi 在 registerClient body 里补 `application_type`：未显式设置时从 redirect_uris 推导——任一 URI 是自定义 scheme（非 http/https）或 loopback host（localhost/127.0.0.1/[::1]/::1）→ `native`，否则 `web`（OpenID Connect 缺省当 web，拒绝 http loopback 回调 #10493）。

**pig 落点**：go-sdk `oauthex.ClientRegistrationMetadata` 已有 `ApplicationType` 字段（v1.8.0 dcr.go:96）。pig 构造点唯一：`integrations/mcp/oauth.go:836`（browser flow），在构造后按 redirectURIs 推导填入（`ApplicationType == ""` 才填）；`loopback` 判定 pig 已有对应物则复用（oauth.go 内搜），无则补。pi 测试 `oauth.test.ts` +22 → `integrations/mcp/oauth_test.go` 补注册 body 断言子测（stub registration endpoint 收 body 断 application_type）。

### 1.4 `--tools`/`--exclude-tools` 通配模式 + `--no-mcp` —— pi `04b97ef00`（大头）

pi 语义：① 两旗标条目支持 `*` 通配（`createToolNameMatcher`：无 `*` 条目进 Set，含 `*` 的编译 `^…$` 正则，`*`→`.*`、其余正则元字符转义）；② allowlist 不含 `mcp__` 条目时保 MCP 工具注册（供 codemode/tool_search）；③ 新旗标 `--no-mcp`（禁内建 MCP：不连 server、无 MCP 工具）；④ naming/matching a tool activates it。

**pig 落点**：
- matcher：pig 无 `core/mcp_servers.go`，`createToolNameMatcher`/`toolPatternRegExp` 放 `codingagent/core/agent_session.go` 顶部或独立小文件（按命名规则 `mcp_servers.go` 不存在则新建 `tool_name_matcher.go`）。
- `agent_session.go`：`allowedToolNames`/`excludedToolNames`（:612 map 形态）改 matcher 函数形态；:686-695 构造点换 `createToolNameMatcher`；:4721 `isAllowedTool` 闭包换 matcher；:4833 activation 循环 `allowedToolNames[toolName]` 换 matcher 调用。
- `sdk.go`：`excludedToolNameSet`（:325-331，过滤 initialActiveToolNames）同步换 matcher（excluded patterns 也要挡 initial active）。
- `--no-mcp`：`cli/args.go` 加 `--no-mcp` 解析 + help 文案 + `AgentSessionOptions`/CLI Options 穿线（`cmd/pig/main.go:606` 一带）；`sdk.go:225` `createMCPManager` 调用点与 `agent_session.go:682` 代理工具注册点按旗标短路（pig-only MCP 面，pi 对应物是 disabledBuiltinExtensions ["mcp"]）。

**偏差声明（pig MCP = 代理工具形态）**：pig 的 MCP 是单个 `mcp` 代理工具（status/search/describe/tool 动作），无 `mcp__server__tool` 逐工具声明、无 codemode/tool_search——pi 的「allowlist 保 MCP 工具注册 + `_isActivatable` tool_search 门」在 pig 零对应物：`mcp` 代理工具就是普通声明工具，`--tools` 不点名即照常排除，用户可用 `mcp` 精确名或 `mcp*` 通配过滤。help 文案按 pig 语义写（--tools 支持 `*` 模式；--no-mcp 禁内建 MCP）。pi 测试移植取语义子集：`args.test.ts` +8（旗标解析）、`5109-exclude-tools` 回归 +18（通配排除）、`agent-session-mcp.test.ts` +118 中与代理工具/manager 相关的子集。

### 1.5 shutdown 中断仍在连接中的 MCP —— pi `8c911797c`（形状适配）

pi：`close()` abort per-connection signal——打断 in-flight connect、取消重试等待、等 settle 后才返回（#10249 关闭挂死）。pig 形状不同：`manager.go` 无重试循环，connect 在 `st.mu` 下跑，`Close()`（:261）等 `st.mu` 到 connect 自然结束（≤ConnectTimeout 默认 10s）。

**pig 方案**：`serverState` 加 in-flight connect 取消柄（`connectCancel`，专用小锁或 atomic 存 `context.CancelFunc`——不能放 st.mu，connect 持 st.mu 全程）；`connectLocked`（:618）把 connectCtx 的 cancel 登记进去，退出时清除；`Close()` 先 cancel 再等 `st.mu`/closeSessionLocked。取消后 connectLocked 走既有错误清理（closeProcessLocked + status 置位），Close 随后置 closed，顺序无竞争。pi 测试 `mcp-extension.test.ts` +49 的语义（连接中 Close 不等满超时）→ `manager_test.go` 补 TransportFactory 挂起式子测。

### 1.6 skills hint indirect —— pi `c30840c2e`（终稿定论：整 commit 零操作）

pi 主体（hiddenTools 进 system prompt options、ToolLoadout.getPromptGuidelines）依赖 `hiddenDeclarations` 机器——pig 全仓零命中（codemode/toolExposure 面，不做）。唯一的候选子项 `indirect` skills 提示同样不可达：pi 的触发条件是 read/bash 在 selectedTools 但在 hiddenTools（被 codemode prepareLoadout 隐藏），pig 无 hidden 环节 → `declaredTools == selectedTools` 恒成立，pi 的 `?? "indirect"` 分支不可达；read/bash 不在 selectedTools 时 pig 与 pi 原逻辑一致地不设 skills 段。skills.go 未改动。

### 1.7 Version 1.0.4 —— pi `7c10bd433`

`codingagent/config.go` Version → `"1.0.4"` + config_test 基线常量。

## 2. 零操作声明（22 commit，后续批不再重判）

- **c30840c2e**（hidden tools 不进 prompt 规则/skills 提示）——见 1.6 终稿定论

- **pi-env 全族 13**：`ba03e03f2`（新包本体）+ `1cbc7a0e8`/`b78e6a908`/`ed94330a2`/`46d0ff936`/`97a600395`/`4bf5a6bc5`/`6fa21f2a3`/`9a193ff7e`/`b7dfc049e`/`031b24aa6`/`956e81504`/`5b3189647`——不做清单（durable 同族，用户 2026-10-06 拍板）
- durable 2：`cd60a5b99`、`68c22123b`（powershell tool over argv exec）——不做清单整体延后
- codemode 2：`b223082bb`（sandbox 防 patch built-ins）、`021eae60a`（read structuredContent 供 codemode image()，read.ts 改动纯 codemode 消费）——codemode 不做
- docs/release 4：`997d31f28`、`98d2e1947`、`b5b490601`、`3cdfbec51`

## 3. 风险与坑

1. **1.4 的 `excludedToolNameSet` 有两处消费**：sdk.go initial active 过滤 + agent_session isAllowedTool，两处都要换 matcher，漏一处则 `--exclude-tools 'mcp__x__*'` 语义分裂。
2. **1.4 matcher 语义**：空 allowlist（`--no-tools`）仍走 `allowedToolNames = []string{}` 非 nil 路径（sdk.go 现有注释），matcher 空条目 = 全 false，不得变 nil。
3. **1.5 取消柄不能放 st.mu 后面**：Close 在 st.mu 上等，connect 持 st.mu 全程——用独立锁/atomic。
4. **1.3 pig 只有一处 metadata 构造点**（oauth.go:836），但 `registerClient` 也被 refresh 路复用与否需确认（grep registerClient 调用点），推导逻辑放构造点或 registerClient 内二选一，放 registerClient 内更贴 pi（pi 在 flow.ts registerClient 里推导）。
5. README/verify-matrix：--no-mcp 与 --tools 通配是用户可见面，收口同批更新。

## 4. 明确不要读的

- `packages/env/**` 全部（pi-env 13 commit 零操作，勿翻 Rust daemon/SSH 实现）
- `packages/durable/**`（2 commit 零操作）
- codemode：`packages/codemode/**`、`extensions/codemode/**`、`core/tools/read.ts` 的 structuredContent 面

## 5. 收口动作

照旧：AGENTS.md 批次索引一行 + pi-env 入不做清单 + 本文件回填终稿 + Version bump + 全量 `-race` 单次（`-timeout 600s`，落 `/tmp/pig-test-out-s15.log`）+ tmux 冒烟四项（清凭证 + mock，抽查 jsonl 零真实调用）+ README/verify-matrix 更新。双向兼容：无 jsonl/wire 结构改动，v3/v4 门按声明沿用。

## 6. 下一批候选 + 已知落点（下一同步目标未定，按仓库机制预写）

- 下一同步目标：等 pi 新正式 tag，照例 `git diff v1.0.4..<tag>` 逐 commit 分拣。pig 基线钉 v1.0.4，永不追 main。
- 已知悬置面：
  - pi v1.0.4 的语法高亮改动让 chroma `LiteralStringInterpol` → `subst` → text 色；如 dogfooding 中发现 chroma 各语言对插值的 token 化与 hljs 语义漂移明显的场景，按 fix 流程登记。
  - `--no-mcp` 只短路 sdk.go 直建路径的 `createMCPManager`；services 路径（`CreateAgentSessionServices`，SDK 嵌入用）无工具旗标透传，如后续 SDK 面需要同语义，在 services options 加对应字段。
  - pig-only 偏差清单无新增（1.4 的 MCP allowlist 裁剪与 1.5 的 connectMu 均为本批一次性执行细节，不构成活偏差；活偏差已计入 AGENTS.md 不做清单 pi-env 条目）。
- pig 侧候选插批（等用户拍板，不自行开工）：`v1.0.4` 附注 tag 打在收口 commit；Phase 8 余量（photon/wazero、telemetry、goreleaser、/share、terminal-image）仍待拍板。
