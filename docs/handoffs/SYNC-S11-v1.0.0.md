# S11：pi v0.99.1 → v1.0.0 基线同步计划（📋 计划稿，2026-10-02 立）

**状态**：📋 **计划（未开工，范围裁决三点待用户拍板，见 §0）**。pig 基线现跟踪 pi v0.99.1（S10-4 收口态），目标推进到 v1.0.0（tag a13d35a74，2026-10-01）。
**规模**：88 commits / 213 files / **+21859/−3059**（v0.99.1..v1.0.0 全区间，含 0.99.2 在内）。非 durable 部分 65 commits：ai +916/−89、coding-agent +9376/−5751、mcp +281/−37、telemetry +5/−1。
**与 S10 对比**：coding-agent 的 +9.4k 中约 8k 落在 `src/experimental/**`（durable/vacation demo、mini/micro 删除、pi-logo-animation 彩蛋）与 codemode 扩展，**真正落 pig 映射面的生产面 diff 仅约 1.5k 行 + 测试**——是 S10（~8k）之后罕见的小同步批，唯一例外是 MCP OAuth 若整体回填则另加 ~600 行新建（见 §0-1）。

## 0. 范围裁决

### 0.1 沿用「不做清单」直接跳过（无争议）

1. **durable 全族不迁**（Packages 16–23：`445770e03`/`47f65f1a3`/`2532a0bef`/`03180653c`/`ed0d6b91b`/`b56702ad3`/`49683a364` 等 + coding-agent 侧 `5609b0d6c` durable TUI、`48dd1e2f0` client/server、`395315f48` vacation demo +27.8k 行）——S10 已裁决整体延后，本批 pi 侧又推进了 8 个 Package，继续声明延后等收敛。
2. **codemode 全族不迁**：瘦身 prompt（`6f1072cc0`）、`models.generateImages()`（`aab34df65`）、tool name 对齐（`b29db895c`）、servers 代 tools 进 codemode（`1c7e7df76`）、hidden tools 不进 system prompt（`028c0ec56`，只改 agent-session codemode 分支 +7 行，pig 无此面）、Windows worker（`4a42f8faf`）、image() 校验（`d2931ad3d`）。
3. **`src/experimental/**` 不迁**（mini/micro 删除是 pi 自家清理，pig 无对应物）。
4. **header logo 彩蛋动画不迁**（`b35af04f4`，+1249 行 pi-logo-animation.ts；pig 有自己的 logo 政策：plain/gradient，见 TUI-LOGO）。
5. **Radius `/login` 不迁**（`ed8b3bcc1` +306/−68，radius-login-selector.ts；pig 无 radius provider，S10-2 已声明 radius 不迁）。注意其中「cancel login 回到来源菜单」的通用行为改进可单独甄别。
6. **7fd478a2e（pi-agent-core 删除 experimental harness）零操作**——pig S2 已取消 harness 上半身，pi 此举反向验证了该决策。
7. **7ef68d4f2（mcp fetch 无 receiver）跳过**——TS runtime 特有，pig 用官方 go-sdk。
8. **233f17440 / ca9c92511（OAuth 页彩色 Pi logo / Apple Terminal wordmark）跳过**——pi 品牌面；pig 等价物是自己的 plain/gradient logo，Apple Terminal 渲染缺口若 dogfooding 撞到再单独立项。
9. **ba7d5fbed（pi-ai/models 轻量入口）跳过**——为 durable 去 TypeBox 服务的包结构改动，pig 无对应消费面。

### 0.2 待用户拍板（三点）

1. **MCP OAuth 整族是否本轮回填（S11-4）**：pi 这轮把 MCP OAuth 从「能用」做到「完备」——`oauth.authServerMetadataUrl` + RFC 9207 iss 校验（`d850edee9` +226）、step-up 保留已授 scopes（`e529a82c9`）、空/null 可选字段容忍（`8ce69e9d2`）、**凭证按 server name+URL 存储**（`5806068c2`）、`"auth": {"provider": "<provider>"}` 复用 /login token（`91f9f3b5d`）、`oauth.clientName`（`7c9fe6645`）、登录 URL 可点击（`3e9451238`+`41169ba2a`）。**pig 的 integrations/mcp 目前完全没有 OAuth**（MCP-1/MCP-2 素材清单里挂账项）——这不是「同步」而是新建能力（pi 侧 oauth.ts + index.ts 合计 ~600 行起，另需 /mcp 面板 login 流）。按 codemode 先例：dogfooding 无真实痛点可继续挂账；要做则独立成 S11-4 最后开工。
2. **fullscreen 变默认是否跟随 pi**（`88ff80b98`）：pi v1.0.0 把 `tuiMode` 默认从 regular 翻转为 fullscreen（`"regular"` 才回落 regular，`--tui-mode regular` 兜底）。pig dogfooding 至今默认 regular；翻转后老用户 settings.json 无 `tuiMode` 键的会直接进 fullscreen。技术上是几行的翻转 + selector 文案，但属用户可见默认行为变更，由用户拍板。
3. **S11-1 copy code 登录的 interaction 通道**：`7a11fe1c7` 需要 ProviderAuthInteraction 支持 `prompt(manual_code)`（发 auth URL → 用户在任意浏览器完成登录 → 手工粘贴 code）。pig 现有 auth interaction 面是否已具备待开工时核实；若缺则顺带小扩 LoginDialog/auth 交互层。无争议默认做进去，列为拍板项只为知会。

### 0.3 本批必迁面（生产面 ~1.5k 行 + 测试）

见 §2 批次划分。无线格式改动（session jsonl / v4 门预期零 diff），无生成器/catalog 变更（本批 pi 未动 catalog），无新依赖包。

## 1. 三条观察（先读）

1. **本批无结构性重构**：与 S10 的三硬变化（模型类型统一/OAuth 共享层/System theme）相反，S11 全部是特性增补与修复族，批次间改动面基本不相交，S11-1/2/3 可 worktree 并行（interactive_mode.go 若双碰按磁铁规则串行）。
2. **fullscreen 默认翻转是唯一全局行为变更**：其余全是局部修复或新增 opt-in 设置（quietStartup "header"）。
3. **MCP「首 prompt 不等服务器」（e029c3ed0，+550/−172）pig 天然具备**：pig integrations/mcp 是懒连接 + idle 回收（MCP-1），代理工具 call 时才连，无 eager 阻塞首 prompt 的问题；该 commit 的 system prompt `mcp_servers` 段、describeNamespace 等 codemode 半区随 codemode 跳过。开工时只需核对 pig 的 `StartEager` 路径不会阻塞首 prompt。

## 2. 增量盘点（生产面 commits 按 pig 归属归类 → 批次）

### ai 层（→ S11-1，改动面 `ai/`）

| commit | 主题 | pig 落点 |
|---|---|---|
| a9424cd43 | **Anthropic workload identity federation**（#10242，+443：`ANTHROPIC_FEDERATION_RULE_ID`/`ANTHROPIC_ORGANIZATION_ID`/`ANTHROPIC_IDENTITY_TOKEN_FILE` 三 env → 请求头注入） | `ai/providers/anthropic.go` + `ai/api/anthropic_messages.go`；测试 anthropic-federation 两文件 311 行全量移植 |
| 7a11fe1c7 | **Anthropic copy code 登录**（#10194，redirect_uri 固定 `platform.claude.com/oauth/code/callback`，auth URL 后 manual_code 收粘贴 code） | `ai/auth/oauth_anthropic.go` + interaction 通道（见 §0.2-3） |
| 295cc72b0 | **strict 翻转修复**：schema 含 rejected keywords 时 Anthropic tools 发非 strict（+195/−54） | `ai/api/constrained_sampling.go` + `anthropic_messages.go`；anthropic-strict-tool-schema 测试 124 行 |
| bc2d8dc1c | grammar tool calls 回放丢 mismatched item id（+44） | openai_responses shared 层 + constrained-sampling 测试 34 行 |
| 2bbfcca43 | Retry-After 不可解析 → 指数退避（3 行） | `ai/utils/provider_retry.go` |
| 3dd803d7e | Z.AI CN 端点 overflow 检测（+12） | `ai/utils/overflow.go` 追加 pattern |

### core 层（→ S11-2，改动面 `codingagent/core/` + `cli/`）

| commit | 主题 | pig 落点 |
|---|---|---|
| db6cc71dc | **/reload 激活新加入 defaultTools 的工具**（#10245，+102：`usesDefaultTools` config + reload diff——新增者激活、移除者保持、会话内手动关的除非新加入否则保持关；`--tools`/`--no-tools`/`--no-builtin-tools` 仍覆盖设置） | `core/agent_session.go` reload 路径；pig 的 S10-4 `+name/-name` 语法 + `DefaultToolNames` 是现成基座 |
| fddc968b9 | #10190/#9962 存凭证 native provider 注册时标记 configured（修新会话间歇性忽略已存默认模型/误报无可用模型）（+81/−20） | `core/model_registry.go` / `model_runtime.go` |
| a0660b174 | branch model 选择单次 catalog 查找（#10198，提交随会话长度变慢）（+87/−13） | `core/model_runtime.go` |
| c34f2d6ad | remote catalog 合并二次方 → 线性（3 行） | `core/remote_catalog_provider.go` |
| dc83372f8 | extension command 注册校验（+9） | `core/extensions/loader` 对应物 |
| ee602414c | #10193/#10072 保留内置工具渲染器 example 的 summary/guidance（#10193，+152/−136） | `core/system_prompt.go` 构建半区 |
| 0c453048b | #10236 `--provider` 不带 `--model` fail-closed（+9） | `cli/args.go`（S9 已有 args_test.go 先例） |
| 0f8740bb6 | "unconfigured" → "not configured" 文案 | login/logout 对应物 |

### tui + interactive（→ S11-3，改动面 `tui/` + `modes/interactive/`）

| commit | 主题 | pig 落点 |
|---|---|---|
| 88ff80b98 | **fullscreen 变默认**（§0.2-2 拍板） | `core/settings_manager.go` GetTuiMode 翻转 + settings-selector 文案 + `--tui-mode` |
| f29ea3deb | **`quietStartup: "header"`**：保留 header（版本+键位提示），隐藏模型行与资源列表（+64/−18） | pig 现有 quietStartup bool 升 tri-state（bool\|string 兼容旧值）；`settings_manager.go` + interactive header 装配 |
| 409e808f5 | **#10255/#10293 system theme pastel 保真**：Catppuccin Frappe 类 palette 被 system theme 二次加工得过于鲜艳，palette 色保持自身 chroma（+44/−5） | `tui/system_theme.go`——S10-3 OKLab 引擎的直接补丁 |
| 65117e31f | #10218 slash 补全在前导空白后触发（+52） | `tui/` autocomplete/editor 触发面 |
| 17f3dccbe | #10169 styled token 结束于选区/高亮边界时 ANSI 顺序保持（+26） | `tui/` 截断/切片面 |
| 0582d9c11 | MCP/codemode 结果预览按折行截断（+172/−81；codemode 半区跳过） | `core/tools/visual_truncate.go` + bash renderer + pig-only `mcpRenderCall` |
| 54c19a252 | transcript 每渲染消息内存（长 assistant 消息约省 4/5 堆）（+23） | `tui/` |
| e792ba131 | user message 渲染行去双份全宽拷贝（输出不变）（+8/−6） | interactive user_message 组件 |

### MCP（→ S11-4，若拍板做；见 §0.2-1）

| commit | 主题 | pig 落点 |
|---|---|---|
| d850edee9 | `oauth.authServerMetadataUrl` + RFC 9207 iss 校验（+226/−20） | 新建 `integrations/mcp/oauth*.go` |
| 5806068c2 | OAuth 凭证按 server name+URL 存（#10252，同 URL 多账号；存量按 URL 存的迁移到首个 server）（+112/−29） | 同上 + credential store 键方案 |
| e529a82c9 | step-up（insufficient_scope）保留已授 scopes（+88/−15） | 同上 |
| 8ce69e9d2 | 空/null 可选 OAuth 字段视为缺席（#10266）（+60/−15） | 同上 |
| 91f9f3b5d | `"auth": {"provider": "<provider>"}` 复用 /login token（每请求现读、随 provider 刷新；仅全局 mcp.json + 非 loopback 强制 https）（+103/−8） | `integrations/mcp/config.go` + core 桥接 |
| 7c9fe6645 | `oauth.clientName`（#10226）（+70/−7） | 同上 |
| 3e9451238 + 41169ba2a | /mcp 登录 URL 可点击（terminal hyperlink + Cmd/Ctrl+click 提示行）（#10186） | `components/mcp_panel.go` |
| a4715ec9b | 官方 MCP client conformance suite 进 CI（+365 run.ts + baseline.json） | 择优：pig 用官方 go-sdk，可评估直接跑该 suite 的 Go 等价物；暂列可选 |
| e029c3ed0 | 首 prompt 不等 MCP 服务器 | pig 天然具备（懒连接），零操作，见 §1-3 |
| c662ec7e3 | tool_search 装载的 deferred MCP 工具 resume/reload 后恢复 | tool_search 随 codemode 跳过；开工时核对 pig 的 resume × MCP 工具恢复路径无同类丢工具问题 |

## 3. 批次划分与顺序

- **S11-1 ai 层**（~700 行 pi diff）：federation + copy code + strict 修复 + grammar tool calls + Retry-After + Z.AI overflow + 测试全量移植。
- **S11-2 core 层**：defaultTools reload + #10190 + 性能双修 + extension command 校验 + #10193 + `--provider` fail-closed + 文案。
- **S11-3 tui + interactive**：fullscreen 默认（若拍板跟随）+ quietStartup "header" + pastel 保真 + TUI 三小修 + 折行截断 + 内存两修。
- **S11-4 MCP OAuth 整族**（若拍板做；独立最后开工，依赖面：integrations/mcp + core 桥接 + mcp_panel + credential store）。
- **收尾**：Version bump 1.0.0（`TestVersionTracksPiBaseline`）+ 双向兼容 v3 四门（本批无线格式改动，预期全绿）+ 全量 `-race` 单次 + tmux 冒烟（重点：fullscreen 默认 / quietStartup header / copy code 登录 / pastel palette 真机着色）。

并行性：S11-1（ai/）/ S11-2（core/）/ S11-3（tui/ + interactive/）改动面互不相交，可 worktree 并行；S11-2 与 S11-3 都可能碰 interactive_mode.go，开工前核对磁铁规则。

## 4. 验收门（沿用 S10-4）

build / vet / gofmt 干净；全量 `go test ./... -race -count=1` **只跑一次**（-timeout 600s，输出存变量单次消费）；双向兼容 v3 四向实测 + v4 门声明核对；tmux 真 TTY 冒烟；handoff 终稿落 `docs/handoffs/S11-*.md` 逐批，AGENTS.md 进度段逐批登记。

## 5. 未决项备忘

1. §0.2 三点拍板结果回填本节。
2. `0f8740bb6` 同 commit 内还有「OAuth sign-in 不再一律称 subscription，仅订阅型 provider 称 subscription，其余称 account」（changelog Fixed 项）——pig login_dialog 的文案面开工时一并核对。
3. S10-3 遗留的 light palette dogfooding 复验（handoff §4.4）与本批无关，但若 pastel 修复（409e808f5）改动 system_theme 求解路径，冒烟时顺带复验。
