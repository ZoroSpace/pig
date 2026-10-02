# S11 收尾批：Version bump 1.0.0 + 双向兼容四门 + tmux 冒烟（📋 开工稿，2026-10-02 由 S11-3 批预写）

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

## 4. S11-4 候选清单（仅当拍板回填 MCP OAuth 时开工；全清单见 SYNC-S11 §2 MCP 表）

- `integrations/mcp/` 新建 oauth*.go：d850edee9（authServerMetadataUrl + RFC 9207 iss）→ 5806068c2（凭证按 server name+URL 存）→ e529a82c9（step-up 保留 scopes）→ 8ce69e9d2（空/null 可选字段容忍）→ 91f9f3b5d（`"auth":{"provider":...}` 复用 /login token）→ 7c9fe6645（oauth.clientName）。
- `components/mcp_panel.go`：3e9451238+41169ba2a 登录 URL 可点击（terminal hyperlink）。
- a4715ec9b（官方 conformance suite）可选；e029c3ed0 零操作（pig 懒连接天然具备）；c662ec7e3 开工时核对 resume × MCP 工具恢复路径。

## 5. 验收门

build / vet / gofmt 干净；全量 `go test ./... -race -count=1 -timeout 600s` **单次**（输出落盘 `/tmp/pig-test-out-s11-final.log`）；双向兼容四门实测记录回填本文件；AGENTS.md S11 段补收尾 ✅ 行并更新「pig 基线现跟踪 pi v1.0.0」。
