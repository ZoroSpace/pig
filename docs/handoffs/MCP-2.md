# MCP-2：/mcp 交互面板 + 启停持久化（已调研、待开工）

> 状态：**调研完成，代码未动**。两个范围决策已用户拍板（§1，不要翻案）；参考调研、pig 现状缺口（含 file:line）、实施方案、测试/冒烟计划全部就绪，下一个 agent 按 §5 直接开工。前置：MCP-1 已收口（commit 355aa37，handoff：docs/handoffs/MCP-1.md，feat：docs/feat/MCP.md——开工前先读）。

## 1. 批次定义与已拍板决策

- **交付物**：交互模式 `/mcp` 命令打开 MCP 服务器面板——状态展示 + 过滤 + 展开看工具 + 每服务器动作（重连/启停/复制错误）。
- **决策 A（用户拍板）**：启停**持久化到 mcp.json**——`ServerConfig` 加 `disabled` 字段（pig-only 字段），面板写回来源文件，配置链天然生效。
- **决策 B（用户拍板）**：工具列表**展开时才连接**（pig 无 metadata cache，列工具 = 触发连接；展开某 server 时才懒连它，面板打开本身零连接副作用）。
- **明确不做（留 MCP-3+）**：OAuth、directTools 模式、metadata cache（离线工具列表）、resources/prompts、config importer、sampling/elicitation。

## 2. 参考调研：pi-mcp-adapter 面板语义（线上源码，本地无）

来源：`github.com/nicobailon/pi-mcp-adapter`（MIT，~1.5k stars，flat 布局：mcp-panel.ts ~1100 行 / panel-keys.ts / mcp-panel-theme.ts）。已核实 ~/Projects/pi、~/.pi 均无本地副本，只能线上参考。

**面板 UX**（顶到底）：边框「MCP Servers」→ 常开过滤行（`◎` 图标 + 查询串，`desc:` 前缀切描述搜索）→ notice 行（auth/重连/导入结果）→ **服务器行**：展开符 `▾/▸`、direct-tools 切换图标、名称 + `(importKind)`、工具数 `2/5`、`~N tokens` 估算、状态后缀（authenticating / blocked by project trust / disabled / needs auth / connecting / failed / connected / idle），failed 时错误消息红字挂在行下 → **工具行**（展开后）：光标符 + direct 切换 + 名称 + 截断描述 → 滚动窗口 MAX_VISIBLE=12 + 进度点 → footer（direct 数/token 估算/`(unsaved)`）→ 键位提示行 → 未保存丢弃确认弹窗；60s 无操作自动关。

**动作键**：`↑↓` 导航；`space` 切 direct（server 级=全开/全关）；`enter` 展开/收起（needs auth 时触发 OAuth）；`ctrl+a` OAuth；`ctrl+r` 重连（先翻 connecting，成功刷缓存，失败显错误 + 提示重试）；`ctrl+d` 启停；`ctrl+y` 复制错误；`ctrl+s` 保存——**面板自己不写文件**，构建 changes/disabledChanges 两张 map 交 caller；禁用持久化在项目本地 `.pi/mcp-adapter.json` 加 `disabled` 字段（**从不改写源配置文件**）；`/reload` 后生效。

**pig 取舍**：取 服务器行/工具行/状态后缀/enter·ctrl+r·ctrl+d·ctrl+y·esc/活过滤；direct-tools、OAuth、metadata cache、保存弹窗、setup/importer 全部不要。pig 的启停语义与 adapter 不同——**直接写回来源 mcp.json 的 `disabled` 字段**（用户拍板），无独立状态文件。

## 3. pig 现状与缺口（已核实，file:line 为 355aa37 时点）

**数据面够用**：`mcp/manager.go` `Status() []ServerStatus`（:215；`ServerStatus{Name, Transport("stdio"|"http"), Lifecycle, Status("connected"|"disconnected"|"connecting"|"error"), ToolCount, PID, LastError, IdleMs}`，注释明说为 /mcp UI 预留）。LastError 在连接失败时已含 stderr tail（`stderrSuffix`）。死锁已修（Status 漏 Unlock + ABBA，见 MCP-1 §4）。

**缺口（本批要补）**：
1. **无导出的重连/断开**：`connectLocked`/`closeSessionLocked` 未导出且需持 st.mu；`Close()` 是全量关停。需要导出 `Reconnect`（+内部组合即可）。
2. **无 per-server 工具列举导出**：`listServerTools`（manager.go:399）未导出；`SearchTools` 会连**全部** server，面板不能用。
3. **无启停**：`ServerConfig` 无 `disabled` 字段；Manager 无启停语义。
4. **无来源追溯**：`LoadConfigFiles`（config.go:127）later-wins 合并后不知道每个 server 来自哪个文件——写回需要它。
5. **core 无桥**：`AgentSession.mcpManager` 未导出（agent_session.go:586），无 getter；`codingagent/modes/interactive/**` 对 mcp 零引用。
6. **命令未注册**：`BUILTIN_SLASH_COMMANDS`（core/slash_commands.go:33）无 /mcp。

**交互面装配点**（已核实）：
- 命令分发：`interactive_mode.go` `processSubmitText`（:1082）大 switch；case 加在 deferred 判断（isDeferredInteractiveCommand :1318，现仅剩 `/share`）之前。未知 `/...` 会原文发给模型（:1292 注释），case 必须真实存在。
- 面板装配：`showSelector(create func(done func()) selectorComponent)`（:6128）——单实例、mount/unmount、焦点管理全现成；参照 `showTrustSelector`（:6453）/`showSettingsSelector`（:6161）。
- 组件骨架：`components/thinking_selector.go` 最干净——embed `tui.Container` + Focusable、`NewDynamicBorder` + searchInput（OnSubmit 重触发列表）+ `tui.SelectList`（`tui/select_list.go`：OnSelect :280 / OnCancel :286 / FuzzyFilter）+ `coretools.KeyDisplayText` 提示行。层级行渲染参照 `components/tree_selector.go`。
- 活刷新：footer resource poller 同型（`components/footer.go:206` + `components/resource_stats.go:184`）——组件自持 ticker、组件自身 mu 保护快照、`RequestRender(false)`，**不与 TUI 锁嵌套**；跨 goroutine 动 UI 才走 `m.mutateUI`（interactive_mode.go:5595，RunExclusive 包装）。
- keybinding：若加 `app.mcp` action，仿 `AppKeybindingSessionTree`（core/keybindings.go defs :155 起 + interactive_mode.go OnAction 绑定块 :1018）——**本批可只做命令不加快捷键**（pi-mcp-adapter 也无默认全局键）。

## 4. 关键设计约束（踩坑预防）

1. **`.mcp.json` 是共享文件**（Claude Code/Cursor 同格式）：写回 `disabled` **绝不能按 struct marshal 往返**（会丢 pig 不建模的未知字段）。必须 JSON 手术：读文件 → `map[string]any` 解码 → 只改 `mcpServers[name].disabled`（false 时**删键**而非写 false，保持文件干净）→ 2 空格缩进 marshal + 尾换行写回，保留原文件 mode。声明偏差：JSON 归一化（缩进/键序）。
2. **来源胜者**：同名 server 在多文件定义时 later-wins，`Sources[name]` 记胜者文件；启停写胜者文件。
3. **写项目文件 = 信任敏感**：未信任项目的配置根本进不了 manager（MCP-1 信任门控），面板无从展示/操作其 server——trust-blocked 状态不可见，声明边界即可，不做合成行。
4. **mcp 包保持叶子**：文件手术放 mcp 包（它已有 config 读面）还是 core？——放 **mcp 包**（`SetServerDisabled` 一并做内存+文件），它只依赖 stdlib，不破坏分层；core 只传语义。
5. 面板组件数据面走**接口**（footer 的 ReadonlyFooterDataProvider 先例），不要直接依赖 `*mcp.Manager` 具体类型——components 测试要 fake。

## 5. 实施方案（按序）

### 5.1 mcp 包（~150 行 + 测试）

1. `ServerConfig.Disabled bool`（`json:"disabled,omitempty"`，pig-only 字段注释）。校验不变（disabled 条目仍需合法 command/url）。
2. `Config.Sources map[string]string`（`json:"-"`）：`LoadConfigFiles` 填 server → 胜者文件路径；`NewManager` 存进 serverState（新字段 `sourcePath`）；`ServerStatus` 增加 `Disabled bool`、`SourcePath string`。
3. `statusDisabled = "disabled"` 语义：disabled server 在 lazy/eager/reap 全路径不连接（connectLocked 直接报 "server is disabled"）；`SearchTools`/`ListServerTools` 跳过（不出工具）；`CallTool` 报错；`Status()` 报 disabled。`SetServerDisabled(false)` 回 disconnected（下次用懒连）。
4. 新导出 API：
   - `ListServerTools(ctx, name) ([]ToolInfo, error)` —— `listServerTools` 导出化（懒连接不变）。
   - `Reconnect(ctx, name) error` —— st.mu 下 closeSessionLocked + connectLocked，连接错误原样上抛（面板显示）。
   - `SetServerDisabled(name string, disabled bool) error` —— 内存更新（disable 时 closeSessionLocked；enable 时 status=disconnected）+ `setServerDisabledInFile(st.sourcePath, name, disabled)`（sourcePath 为空只改内存）。
5. `setServerDisabledInFile`：§4.1 的 JSON 手术。

### 5.2 core（~30 行）

1. `func (s *AgentSession) MCPManager() *mcp.Manager` 导出 getter（nil 安全）。
2. `BUILTIN_SLASH_COMMANDS` 追加 `{Name: "mcp", Description: "MCP servers and tools"}`（autocomplete 自动收编，无需 ArgumentHint）。

### 5.3 components/mcp_panel.go（新组件 ~400 行 + 测试）

```go
type MCPPanelProvider interface {
    Status() []pigmcp.ServerStatus
    ListServerTools(ctx context.Context, server string) ([]pigmcp.ToolInfo, error)
    Reconnect(ctx context.Context, server string) error
    SetServerDisabled(server string, disabled bool) error
}
```
`*mcp.Manager` 天然实现（接口放 components 包）。

- 骨架照 thinking_selector：DynamicBorder + 标题「MCP Servers」+ searchInput（FuzzyFilter 常开过滤）+ SelectList + KeyDisplayText 提示行 + esc OnCancel。
- 行渲染：`▾/▸` + 状态符 + 名称 + `(transport·lifecycle)` + `N tools` + 状态后缀（disabled / connecting / failed / connected + idle Xs）；failed 时 LastError 挂行下；展开后缩进工具行（`名称 — 描述截断`，参照 tree_selector 的层级行）。SelectList 条目按展开态重建（server + 其工具行）。
- 动作：enter 展开/收起（懒 `ListServerTools`，连接中 loading 占位行，失败错误行）；`ctrl+r` Reconnect（先翻 connecting 再调，结果刷新）；`ctrl+d` SetServerDisabled 取反（失败 notice 行）；`ctrl+y` 复制 LastError（交互模式既有剪贴板路径，interactiveCopyToClipboard 先例）；esc 关。
- 活刷新：2s ticker 轮询 `Status()`，组件 mu + 快照 + `RequestRender(false)`（§3 footer 同型）；构造参数收 `requestRender func()`。

### 5.4 interactive 接线（~40 行）

`processSubmitText` 加 `case text == "/mcp": m.editor.SetText(""); m.showMcpPanel(); return`；`showMcpPanel()` 照 showSelector 装配：`session.MCPManager()` 为 nil → `showStatus("No MCP servers configured")`；否则构造组件（requestRender 传 `func(){ m.ui.RequestRender(false) }`，剪贴板回调传既有实现），OnClose 走 `done()`。

### 5.5 验收门（铁律沿用）

- build/vet/gofmt 干净。
- 全量 `go test ./... -race -count=1 -timeout 600s` **只跑一次**：`out=$(…); rc=$?`，失败详情/ok 计数从同一份输出取，落盘 `/tmp/pig-test-out-mcp2.log`。
- tmux 真 TTY 冒烟（mock LLM + `mcp.test -test.run=^$` helper server 手法，见 MCP-1 §5）：/mcp 面板打开 → 状态行 → 展开列工具 → ctrl+d 禁用（**验证来源 mcp.json 落盘 `"disabled": true` 且面板翻转**）→ ctrl+d 重新启用（键被删除）→ ctrl+r 重连 → esc 退出。

### 5.6 文档收批

`docs/feat/MCP.md` 追加 MCP-2 节（§4 的四条约束 + JSON 归一化偏差 + trust-blocked 边界）+ `docs/handoffs/MCP-2.md` 改终稿（对齐 MCP-1 终稿格式：收口摘要/修复清单/偏差/验收/冒烟）+ AGENTS.md「已交付」段补行（MCP-1 行上方插）。

## 6. 测试清单

- **mcp 包**：disabled 跳过全路径（lazy connect 拒绝 / SearchTools 无其工具 / CallTool 报错 / Status=disabled）；`Reconnect`（断开重连 + 失败上抛 + keep-alive 也可重连）；`SetServerDisabled`（内存翻转 + 文件手术：临时目录验证兄弟 server 与未知字段保留、false 删键、文件 mode 保留、sourcePath 空不写盘）；`ListServerTools`（懒连接 + disabled 报错）。
- **components**：fake provider——状态后缀渲染（connected/error/disabled/connecting/idle）、错误行、展开懒连接（loading→工具行/失败行）、过滤、ctrl+r/ctrl+d/ctrl+y/esc 回调、ticker 刷新快照。
- **存量回归**：mcp 包现有 17 函数 + core 桥接 5 函数不动、必须保持绿。

## 7. 已声明边界（写进 feat 文档的素材）

- trust-blocked server 不可见（未信任项目配置进不了 manager，面板无合成行）。
- `.mcp.json` 手术回写会归一化 JSON 格式（缩进/键序）；无注释损失（JSON 无注释），未知字段保留。
- 启停写「胜者文件」：同名 server 多文件定义时只改生效的那份，另一份的定义仍在（next 启动若调整链序会复活——语义上正确，文档说明）。
- 面板工具列表非缓存：每次展开重连重列（no metadata cache）；对 keep-alive server 无额外开销，对 lazy server 每次展开可能触发一次连接。
- OAuth / directTools / metadata cache / resources / prompts 全部留 MCP-3。
