# S11 收尾批：Version bump 1.0.0 + 双向兼容四门 + tmux 冒烟（✅ 收口终稿，2026-10-02）

S11 总计划：`docs/handoffs/SYNC-S11-v1.0.0.md`。S11-1/2/3 源码面已全部收口（pi v1.0.0 的 8 个 tui+interactive commit 全部消化），本批是 S11 的关账批。**前置拍板项**：§0.2-1 MCP OAuth 整族是否回填（S11-4）——若拍板做，S11-4 在本批之前开工（候选清单见 §4）；若拍板不做，本批直接开工。

## 1. Version bump

- `codingagent/config.go:186`：`const Version = "0.99.1"` → `"1.0.0"`。
- `codingagent/config_test.go:21` `TestVersionTracksPiBaseline`：wantPiBaseline 改 `"1.0.0"`。
- pi v1.0.0 tag a13d35a74。pig 基线就此跟踪 pi v1.0.0。

## 2. 双向兼容 v3 四门

本批（S11 全系）**无线格式改动**、无新 entry 类型、无 catalog 变更——v4 门预期零 diff（声明核对即可），v3 四门预期全绿但必须实测（照 S8 收尾先例，`docs/handoffs/SYNC-S8-v0.87.0.md` §0.1；S10-4 的四向实测流程可整套复用）：

1. 隔离目录 + 本地 mock OpenAI 端点 + `npm install @earendil-works/pi-coding-agent@1.0.0` dist 独立跑。
2. 四门：① pig 读 pi 真跑会话；② pi API+CLI `--continue` 读 pig 会话并把历史带进模型上下文；③ pi 追加条目 pig 回读；④ context_edit 专项双向投影逐字段一致。
3. **实验必须与被续会话同 cwd**（continueRecent 按 cwd 找最近会话，S8 教训）；探针不留文件。
4. 本批新增的 `quietStartup:"header"`（settings.json 里 string 值）与 `tuiMode` 翻转都不进会话线格式，预期对四门无影响——如实测有意外，先查 settings 读取面而非 jsonl。

## 3. tmux 真 TTY 冒烟清单（S11 系全部积压项一次清）

1. **fullscreen 默认**（88ff80b98，S11-3）：无 `tuiMode` 键的 settings.json 启动 → 直接进 fullscreen；`--tui-mode regular` → regular；/settings 里 TUI mode 描述已改为 regular mode 使用终端 scrollback。
2. **quietStartup "header"**（f29ea3deb，S11-3）：settings.json `"quietStartup": "header"` 启动 → header（logo/版本/键位提示）可见、模型行与资源列表隐藏、compact onboarding 文案无 "and loaded resources" 尾巴；`true` 仍全隐；/settings Quiet startup 三值循环（true/header/false）。
3. **system theme pastel**（409e808f5，S11-3）：Catppuccin Frappe 类 palette 终端下 system theme 的 accent 不再比 palette 粉色更艳（顺带复验 S10-3 遗留的 light palette dogfooding 项，见 SYNC-S11 §5-3）。
4. **MCP 结果预览**（0582d9c11，S11-3）：一条超长单行 JSON 的 MCP 结果，收起态预览按折行截断为 5 行 + hint，不再占满整屏。
5. **user message 渲染**（e792ba131，S11-3）：对话正常显示、背景色/内边距与改前一致（纯重构，输出不变）。
6. **copy code 登录**（S11-1 挂账）：`/login` anthropic → 选 copy_code → 粘 `code#state` 全链路（需有效凭证或人工完成浏览器步）。
7. 正常退出 / resume 回归。

## 4. S11-4 候选清单（✅ 2026-10-04 销账：用户拍板开工，交付为 S12-4，终稿 docs/handoffs/S12-4.md）

> 本清单即 S12-4 的施工范围，已全部落地（pig 原生实现，见 S12-4 终稿的偏差声明）。

- `integrations/mcp/` 新建 oauth*.go：d850edee9（authServerMetadataUrl + RFC 9207 iss）→ 5806068c2（凭证按 server name+URL 存）→ e529a82c9（step-up 保留 scopes）→ 8ce69e9d2（空/null 可选字段容忍）→ 91f9f3b5d（`"auth":{"provider":...}` 复用 /login token）→ 7c9fe6645（oauth.clientName）。
- `components/mcp_panel.go`：3e9451238+41169ba2a 登录 URL 可点击（terminal hyperlink）。
- a4715ec9b（官方 conformance suite）可选；e029c3ed0 零操作（pig 懒连接天然具备）；c662ec7e3 开工时核对 resume × MCP 工具恢复路径。

## 5. 验收门

build / vet / gofmt 干净；全量 `go test ./... -race -count=1 -timeout 600s` **单次**（输出落盘 `/tmp/pig-test-out-s11-final.log`）；双向兼容四门实测记录回填本文件；AGENTS.md S11 段补收尾 ✅ 行并更新「pig 基线现跟踪 pi v1.0.0」。

## 6. 进度快照（2026-10-02 中断续作 handoff，token 用尽）

S11-4 未拍板 → 按开工稿分支「不做」处理，直接开工收尾批。已完成：

1. **Version bump ✅**：`codingagent/config.go:186` → `"1.0.0"`，`config_test.go:22` wantPiBaseline 同步，`TestVersionTracksPiBaseline` 绿。
2. **全量 -race 单次 rc=0，31 包 ✅**（/tmp/pig-test-out-s11-final.log）。
3. **双向兼容 v3 四门 ✅ 全通**（实验环境 /tmp/pig-s11-final/：mock OpenAI-completions SSE 端点 :8931〔已按端口 kill 清理〕、共享 agent 目录 `$exp/agent`（models.json 挂 mock provider + trust.json seed work 目录）、pi@1.0.0 npm dist 装 /tmp/pig-s11-final/pi-dist、两侧同 cwd `$exp/work`）：
   - 门 1 pi 写 → pig 读：pi `-p` 真跑落盘 → pig `--continue` 续跑，mock 捕获 n=4（system+pi user/assistant+新 user），历史完整进上下文。cwd 编码 `--private-tmp-…` 证明 ProcessCwd 修复双向生效。
   - 门 2 pig 写 → pi 读：pig `-p` 落盘 → pi CLI `--continue -p` n=4；pi 再追加 → pig `--continue` n=6；再闭环 n=8，逐条 user content 全对。
   - 门 3a pi 写 context_edit → pig 读：null omit 整条省略 + `{content:"…"}` string 归一 text block，pig 投影与 pi 自读**语义逐字段一致**（diff 仅 JSON 缩进 + model 键大小写 + usage cost 序列化差异）。
   - 门 3b pig 写 → pi 读：null omit + blocks 数组替换，pi 投影与 pig 自读 `JSON.stringify` **逐字节一致**。
   - 探针：/tmp/pig-s11-final/probe（Go，go.mod replace 指向本仓）+ gate3-pi.js（node）；**教训：探针 AppendMessage 必须传值类型**——`hasConversationLocked` 的类型 switch 只认 `types.UserMessage/AssistantMessage` 值形态，指针形态永不 flush（真实调用方均为值形态，无存量 bug，已核实 agent loop emit 与 agent_session 两处调用点）。
   - 门 4 v4 声明核对 ✅（结论变更见下）。
4. **门 4 结论（重要）**：pi v1.0.0 **整体删除了 `packages/agent/src/harness/session/jsonl/`（8 文件 -1894 行）**，v4 runtime 存储随 durable 包收编（`packages/durable/src/storage/jsonl/`，属「durable 不做」裁决区，v0.99.1→v1.0.0 该处仅 sidecar task-record 校验一处小改，与 pig 无关）。因此 v4 门声明更新为：**pi v1.0.0 无 agent 侧 v4 写入器残留，frozen 区对照对象消失；pig v4 写入器（S1 冻结区）本批零改动 ⇒ v4 互操作结论对 v1.0.0 成立（声明核对，非实测）**。S1 的真实代码互操作结论不变。

### 剩余工作（下个会话接手）

1. **tmux 冒烟 7 项**（本文件 §3 清单，全部未跑）。
2. AGENTS.md S11 段补收尾 ✅ 行 + 「pig 基线现跟踪 pi v1.0.0」。
3. 收口 commit（改动：config.go/config_test.go + 本文件回填 + AGENTS.md）。

## 7. tmux 冒烟结果（§3 清单 7 项，2026-10-02 续作会话全部执行）

实验环境 `/tmp/pig-s11-smoke/`（与 §3 门实验隔离）：mock OpenAI-completions SSE :8931（ping / callmcp→tool_call / tool 回包后收尾三模式，日志 mock-requests.log）+ MCP stdio helper（`mcp-stdio.js`，node 绝对路径——tmux server 环境无 nvm PATH）+ 各场景独立 agent dir（settings 均钉 `defaultProvider/defaultModel = mock/mock-1`，用户拍板验证不用 deepseek-v4-pro；本次全程零真实 API 调用）。trust.json 必须 seed **`/private/tmp/...` 解析后路径**（ProcessCwd symlink 语义，只写 /tmp 会弹 trust 缺失告警）。各场景专属 tmux session 名 s11*，收口全部 kill-session；mock 按端口 lsof 定位 kill。

1. **fullscreen 默认 ✅**：无 `tuiMode` 键启动 → `tmux display-message #{alternate_on}` = 1（alt-screen）；footer 显示 (mock) mock-1。/settings 搜 "tui"：「TUI mode  fullscreen」+ 新描述 "Interface layout; regular mode uses the terminal's normal scrollback" ✅。`--tui-mode regular` → alternate_on=0 ✅。
2. **quietStartup 三态 ✅**：`"header"` 启动 → header（blocks logo + v1.0.0 + 键位提示 + onboarding）可见、模型行/资源列表隐藏、compact onboarding **无** " and loaded resources" 尾巴；`true` → 全隐（只剩编辑器框/cwd/footer）；/settings "Quiet startup" Enter 三值循环 header→false→true→header，持久化保真（true/false 写 boolean、"header" 写 string）✅。
3. **system theme ✅（部分）**：`theme:"system"` tmux 启动渲染正常（回落路径无回归，logo 真彩序列正常）；pastel 算法由 `TestSystemTheme/keeps_pastel_...`（#10255 Frappe 16 色）锁定且绿。**Frappe 类 palette 真终端下 accent 不再更艳**属 tmux 原理性无法复现（tmux 不答 OSC 4 调色板查询），与 S10-3 light palette dogfooding 项一并留真机验证。
4. **MCP 结果预览 ✅**：mock 回 tool_call（`{"tool":"smoke__bigjson","args":{}}`）→ mcp 代理工具执行 → 卡片调用行 `mcp bigjson (smoke)`（MCP 渲染器可见性）+ 收起态预览**恰好 5 视觉行** JSON + `... (19 more lines, ctrl+o to expand)` hint，不再占满整屏 ✅。
5. **user message 渲染 ✅**：对话往返正常（用户消息 1 列 padding + 文本完整 + OSC 133 zone；默认 dark 主题无 userMessageBg token → 无 bg 序列 = 预期输出）。
6. **copy code 登录 ✅（无凭证可达链路全通）**：/login → Sign in with an account → provider 选择器（"not configured" 文案）→ Anthropic → **认证方式选择器渲染**（Browser login / Copy code login (headless)）→ 选 copy code → PKCE 授权 URL（redirect_uri=platform.claude.com/oauth/code/callback）+ "Paste the code Anthropic shows..." 输入框；浏览器启动走 fake `open`（fakebin）拦截未弹真浏览器；负路径：粘 `bogus-code#wrong-state` → "Error: Failed to login to Anthropic: OAuth state mismatch"，auth.json 保持 `{}` 无凭证落盘。完整浏览器步留真机凭证 dogfooding（verify-matrix）。
7. **正常退出 / resume ✅**：ctrl+d 退出 EXIT=0、alt-screen 退出、fullscreen exit output=transcript 默认把对话全文落到 scrollback + "To resume this session" 提示；`--continue` 恢复 → fullscreen 下完整 transcript（用户消息+pong）还原。

**冒烟发现并当批修复 2 个问题**（回归测试均 stash 红→绿，详见 docs/fix/）：

- **`showAuthSelect` 挂载/恢复不触发渲染**（docs/fix/2026-10-02-auth-select-stale-frame.md）——pi interactive-mode.ts 的两个 `ui.requestRender()` 在移植时丢失：选择器挂进 editorContainer 且焦点已切换，但空闲 TUI 永不重绘，画面冻结在旧帧（/login → Anthropic 后看似卡死，实际按键打进看不见的 selector）。SIGQUIT dump（经 pane_pid 取真 pig pid）实证 login goroutine 停在 `<-resultCh` 且挂载已完成。修：两处补 `RequestRender(false)`；回归 `TestShowAuthSelectRequestsRender`（focusRecordingTUI）。**修后 item 6 全链路真机通**。
- **TestThemeStyles 受宿主 COLORFGBG 影响**（docs/fix/2026-10-02-theme-style-colorfgbg-env.md）——本会话 shell 新增 `COLORFGBG=0;15`（light 分类）使 palette-only 子测翻转；pi 同样潜在敏感（CI 环境恰好无此变量）。测试加固 `t.Setenv("COLORFGBG", "")`，产品代码零改动。

**基建教训（入档）**：① MCP 官方 go-sdk v1.8.0 客户端在 initialize 前发 `server/discover` 探针，自建 stdio server 对未知 method 带 id 的请求必须回包（哪怕空 result），否则客户端永阻到 connect 超时；② tmux server 的 PATH 不含登录 shell 后挂的路径（nvm），MCP stdio server 的 command 需绝对路径；③ `pgrep -f` 首个命中是 tmux/zsh 包装进程，SIGQUIT 取 dump 必须经 `tmux display-message #{pane_pid}` 再取子进程；④ macOS `sample` 解不开 Go goroutine 栈，Go 侧栈只有 SIGQUIT 一路；⑤ 全量回归对环境变量敏感（COLORFGBG），跨会话接手先 `env` 对照。

## 8. 验收门终态

- go build / vet / gofmt 干净（含本批两处修复）。
- 全量 `go test ./... -race -count=1 -timeout 600s` **单次** rc=0 31 包（/tmp/pig-test-out-s11-final2.log；中间一次 rc=1 为上条 COLORFGBG 环境漂移，已加固复跑全绿）。
- Version 1.0.0（TestVersionTracksPiBaseline 绿）；pig 基线跟踪 pi v1.0.0。
- S11 系（S11-1/2/3 + 收尾批）全部关账；S11-4（MCP OAuth）未拍板不开工，候选清单保留在 §4。
