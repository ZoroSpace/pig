# P8-EH1：export-html + --export + migrations（✅ 收口 2026-09-21，Phase 8 首批）

**状态：✅ 完成。全量 `go test ./... -race -count=1 -timeout 600s` rc=0（29 包，日志 /tmp/pig-test-out-p8eh1.log）。上批 = S6-3c（docs/handoffs/S6-3c.md）。本批定义见 S6-3c handoff 第 3 节。**

## 0. 本批完成

**新建/改动文件**：

| 文件 | 内容 |
|---|---|
| `codingagent/core/exporthtml/`（新子包） | pi `core/export-html` 的模板半区（Go import-cycle 强制拆分，见 1.1）：`ansi_to_html.go`（← ansi-to-html.ts 1:1，jsParseInt10 复刻 JS parseInt 宽容语义）、`index.go`（← index.ts 的 ExportOptions/ToolHtmlRenderer 接口/SessionData/parseColor/getLuminance/adjustBrightness/deriveExportColors/generateThemeVars/generateHtml + **jsStringReplace** + 模板 go:embed）、`tool_renderer.go`（← tool-renderer.ts 1:1，ThemeFunc 懒解析 + recover 兜底）、`templates/`（template.html/css/js + vendor marked/highlight.min.js 自 pi v0.86.0 **逐字节照搬**，sha256 校验，共 266KB 嵌入二进制） |
| `codingagent/core/export_html.go`（新） | pi index.ts 的会话半区：ExportSessionToHtml / ExportFromFile / preRenderCustomTools（typed entries 双形态 switch）+ exportToolsOf（`*[]ToolSummary` 保 `"tools":[]`） |
| `codingagent/core/migrations.go`（新） | pi `src/migrations.ts` 全量：MigrateAuthToAuthJson / MigrateSessionsFromAgentRoot / migrateToolsToBin / migrateKeybindingsConfigFile / migrateExtensionSystem / ShowDeprecationWarnings（raw-mode 按键等待）+ RunMigrations + jsonObjectEntries（token 流保序解析）。位置偏差（src 根 → package core）因 codingagent 根不能 import core，S6-3c handoff 已圈定 |
| `codingagent/core/agent_session.go` | `ExportToHtml(outputPath, themeName string)` 方法（← agent-session.ts exportToHtml：themeName 双候选解析 + CreateToolHtmlRenderer 装配，ThemeFunc 传 theme.Current） |
| `cmd/pig/main.go` | `--export` 转正（pi main.ts 同位：exportFromFile + "Exported to: N"）+ runMigrations 转正（runMigrationsResult 存变量）+ 弃用警告在 interactive 门内显示（pi 同位）+ MigratedProviders 传入 InteractiveModeOptions |
| `codingagent/modes/interactive/interactive_mode.go` | handleExportCommand 补 HTML 分支（非 .jsonl → ExportToHtml(·, theme.Current().GetName())）；isDeferredInteractiveCommand 注释收口；deferredCommandReason 删 "export" case |
| `codingagent/modes/interactive/interactive_mode_submit_test.go` | TestDeferredInteractiveCommandsStillWarn 的三个 export 用例移除；新增 TestExportCommandHtmlExports（显式 .html / .md 保名 / 裸命令写 AppName 默认名三子测）；TestExportCommandJsonlExports 保持 |
| `codingagent/modes/interactive/test_session_seam_test.go` | 抽出 createInteractiveTestSessionWithManager（文件会话注入用） |
| `README.md` | 交互命令表补 `/export`；脚本与 CI 段补 `--export` 用法 |

**测试**：新增 37 个测试函数（exporthtml 19 + core 17 + interactive 1；约 120 个子测/断言节点）。exporthtml 的表驱动含 6 个故意写错后修正的期望（全部为**测试侧**理解偏差，实现均与 pi 算法逐路径核对无误，见 1.3）。stash 验证：jsStringReplace 换回 strings.Replace → TestGenerateHtmlEndToEnd 红（`$&` artifact 计数 1→0），还原后绿。

**Golden 对照（vs 全局 pi 0.85.1 二进制，同一真实 131 行会话）**：
- **payload 语义零差异**（131 entry 逐路径 walk，`{}`）
- **pi 的 `$&` mangling artifact 字节级一致**：highlight.min.js 正则字符类 `[-+*\/?!$&|:...]` 中的 `$&` 被 JS `.replace` 展开为字面 `{{HIGHLIGHT_JS}}`，pi 产物与 pig 产物该处完全相同（`grep -c` 双方 =1，jsStringReplace 复刻成功）
- 骨架差异仅三类、全部已声明：CSS var 排序（Go map 无序 → sorted）、`--imagePlaceholder`（pig-only token）、四个中性灰值（用户自己的 pig 提交 2552fad，非本批引入）
- 手编 fixture 首轮出现的 payload 差异（timestamp / usage.cost 子键）系 fixture 字段不真实 + S1 typed-remarshal 既有声明行为，非本批 bug

**冒烟（tmux 真 TTY + mock SSE 端点）**：① `--export` CLI 双端点对照（上）② 交互 `/export` 全链路：mock 对话 pong → `/export <path>.html` → 状态行 + 286KB HTML（9 entries、leafId、4 活动工具入 payload）③ migrations：agent 目录预置 hooks/+commands/ → 启动打印 "Migrated Global commands/ → prompts/" + 弃用警告 + URL + "Press any key to continue..."，按键后 TUI 正常挂载且按键不泄漏进编辑器，commands/ 目录已改名 ④ 顺手确认 logo 版本号 v0.86.0（codingagent.Version 常量 S6-3b 已更新，旧二进制未编译所致）。

## 1. 过程决策与偏差（仅本批新增）

1. **pi index.ts 拆两半（Go import-cycle 强制）**：package core 必须调用 exporthtml（AgentSession.ExportToHtml），exporthtml 又需要 core 的 SessionManager/SessionEntry 类型 → 环。拆分：模板半区（SessionData/ToolHtmlRenderer 接口/generateHtml/theme 色彩推导）留 exporthtml；会话半区（exportSessionToHtml/exportFromFile/preRenderCustomTools）落 `core/export_html.go`。SessionData.Header/Entries 用 `any` 承载 core 类型（marshal 走各自 pi 兼容 tag）。TEMPLATE_RENDERED_TOOLS 因拆分导出为 IsTemplateRenderedTool（pi 为模块私有）。
2. **jsStringReplace 复刻 ECMA-262 GetSubstitution**：pi 用 `.replace()` 注入模板；替换串中的 `$&`/`` $` ``/`$'`/`$$` 会展开，`$n`（无捕获组）字面。highlight.min.js 含一处 `$&` → pi 导出产物里它是字面 `{{HIGHLIGHT_JS}}`（无害的 pi 固有 quirk）。pig 用 strings.Replace 会产出"更正确但不同"的输出 → 破坏字节对齐。jsStringReplace ~40 行逐语义复刻（node v24.14 实测 pin：含 `$'` 展开后自然尾随再跟一次的双重形态）。
3. **测试期望三轮返工全为测试侧错误**：① style 清空后不开 span（pi hasStyle 判定）；② 截断扩展序列剩余参数按普通 SGR 处理（48;2;1;2 → dim+bold）；③ `$'` 的"双重尾巴"是我对 JS 语义的理解错，node 实测后确认实现正确；④ trimRenderedResultLines 只裁头尾不裁中间；⑤ deriveExportColors g+15 算术；⑥ toolPendingBg 等主题值差异来自用户自己的 2552fad 提交。
4. **ThemeFunc 懒解析**（pi theme Proxy 语义）：pi 的 deps.theme 是 Proxy，未初始化时在**渲染器内部首次访问**才 throw → 被 try/catch 吃掉 → 静默回落结构化渲染。pig 若传 `*Theme` 会在构造期 panic。ThemeFunc 在 recover 守卫的渲染调用内解析，行为等价。
5. **SessionData.Tools 用 `*[]ToolSummary`**：pi `state?.tools?.map()` 对空数组产出 `"tools":[]`，Go omitempty 会把空切片整个省掉。nil = 缺席（state undefined），非 nil（含空）= 在场。
6. **裸 `/export` 在无会话文件时报 "Nothing to export yet"**：pig 与 pi 一致——会话文件在首条 assistant 消息 flush 时才物化（pi 同型 flush 机制）。交互测试 fixture 需先 AppendMessage 一条 assistant 才能导出。
7. **models.json schema 冒烟修正**：正确格式是 `{"providers": {"<id>": {"models": [数组，元素带 id]}}}`（pi ModelConfigSchema），旧冒烟配置的映射形态本就不合规。顺带记录。
8. **`doc.go` 陈旧遗留**：exporthtml/ 下发现 9 月 10 日的 2 行探针遗留（untracked），删除；包文档以 index.go 头注释为准。

## 2. 风险与坑（下一批必读）

- **golden 对照必须用真实会话文件**：手编 fixture 会与 typed-decode 的字段补全（timestamp 等）纠缠出假差异；真实 pig 会话经 pi 读取零差异（双向兼容的又一次实证）。
- **JS `.replace` 的 `$` 语义**：今后任何"TS 里用 replace 注入内容"的移植（含 future batch 撞上 pi 代码的 `.replace(search, jsVar)`）都要先 grep 注入源里的 `$&`/`` $` ``/`$'`/`$$`，必要时复用 exporthtml 的 jsStringReplace。
- **pi 二进制版本 ≠ 同步基线**：全局 pi 是 0.85.1，pig 基线 0.86.0；主题/模型目录对照时差异先查 git 历史是不是 pig 自己的提交（本批四值差异即用户提交），再下"移植偏差"结论。
- **mock 冒烟的 models.json schema**：providers 包装 + models 数组（元素带 id），pi ModelConfigSchema 为准；写错时 /model 选择器会显示 "Invalid models.json schema" 诊断。
- go test 执行规范照旧（AGENTS.md 仓库机制节）：本批全量 rc=0 一次通过（此前 stash 验证均窄范围 -run）。

## 3. 下一批定义：P8-EH2（按用户排期定，候选见下）

Phase 8 余量（S6-3c handoff 第 3 节已圈）：**① photon 经 wazero**（仅图像转码半区：剪贴板不常见格式→PNG 的 convertToPng〔codingagent/utils/clipboard_image.go 已声明 not-ok〕；贴图主流程已交付不受影响；另解锁 terminal-image.ts 与 tui Image 组件）② **telemetry 真实上报面评估** ③ **goreleaser 发布链** ④ **/share gist API**（独立小批）。下一批选哪件由用户拍板，模型不擅自定。

> **2026-09-22 增补**：①的图像转码半区已用 Go 原生解码/缩放实现（BMP/WebP/TIFF→PNG + 2000px ApproxBiLinear resize，`codingagent/utils/image_process.go`），不再依赖 photon/wazero——convertToPng 余量仅剩 clipboard_image.go 的接线（可直接复用新实现）；photon/wazero 彻底出局，剩余解锁面只有 terminal-image.ts + tui Image 组件。见 docs/fix/2026-09-22-image-tool-413-oversize-and-history-bloat.md。

**若做 photon/wazero，要读的 pi 源文件**：
- `packages/coding-agent/src/utils/clipboard-image.ts`（convertToPng 的 photon 调用面）
- `packages/coding-agent/src/utils/terminal-image.ts`（657 行，iTerm2/kitty 六el 协议分族）
- `packages/tui/src/components/image.ts`（tui Image 组件）
- `photon` 的 WASM 接口面（wazero 实例化 + importObject 对齐；先探 wazero 对 photon wasm 的兼容性——photon 依赖 WASI + SIMD，**wazero 对 SIMD 支持情况是本批最大技术风险，动手前先写探针验证**）

## 4. 明确不要读的

- `packages/coding-agent/src/experimental/**`、`src/extensions/**`（Phase 7 不做）、`packages/agent/src/harness/**`、`packages/server|client|protocol/**` + `session-backends/**`（Phase 5 不做）
- `packages/tui/**`（S6-2 已清完）、`src/core/radius.ts`、`test/documentation.test.ts`
