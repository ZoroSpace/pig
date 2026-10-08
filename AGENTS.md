# pig：pi 的 Go 全量重写方案

记录于 2026-08-30，spec 基线为 `~/Projects/pi` commit `4e58f324f`（v0.84.3，2026-08-24），基线 hash 在 Phase 0 固定。

> **2026-10-04 瘦身改组**：本文件自「批次流水账」改为「活规则 + 索引」。历史每批的过程决策、偏差、测试与冒烟记录原文均已落盘于 `docs/handoffs/<批次>.md`、`docs/feat/`、`docs/fix/`、`docs/batches/2026-09.md`，此处只保留每批一行索引；被删摘要原文可在 git 历史（`git log --follow -p AGENTS.md`）找回。**新增批次的追加义务**：fix 在 docs/fix/ 清单补一行、feat/同步批在「批次索引」补一行——均为单行，禁止把长摘要写回本文件。

## 仓库机制（先读）

- **go test 执行规范（2026-09-11 立，起因：同一命令全量 `-race` 被重复跑两三遍）**：全量回归 `go test ./... -race -count=1` **只跑一次**——输出存变量后多处消费：`out=$(go test ./... -race -count=1 2>&1); rc=$?`，`rc` 即最终裁决（任何包失败非零），失败详情 `echo "$out" | grep -E '^(FAIL|---)'`、通过包数 `echo "$out" | grep -c '^ok'` 均从同一份输出取；重要回归可 `echo "$out" > /tmp/pig-test-out.log` 落盘备复盘。**严禁**为提取失败详情/数 ok 行数等目的把同一命令重跑第二遍（`-race` 全量双倍时间纯属白烧）
- **go test 必须带 -timeout（2026-09-11 立，起因：openai-responses 测试漏 End() 致 Events() 遍历永阻，无超时白卡数分钟）**：凡预期很快结束的单测，命令一律显式加 `-timeout`（开发期建议 `-timeout 120s`，全量 `-race` 建议 `-timeout 600s`），不得裸跑 `go test`。Go 默认 10m 超时太宽，卡住时代价高；被卡住时先用 `go test -timeout 30s -run '<最窄的 -run>'` 定位是否死锁，再回头修测试（典型根因：EventStream 未 End 就遍历 Events()、goroutine 忘记退出）
- **所有可能阻塞的命令一律套 timeout（2026-09-20 追强，起因：node --test 探针裸跑卡死 2 小时+）**：此规则不限于 go test——`node --test`、tsx/python 探针、任何跑测试或冒烟的命令都必须用 `timeout <n> <cmd>` 前缀（如 `timeout 60 node --test test/foo.test.ts`）；TS 测试里用了 fake timers（`t.mock.timers`）时尤其危险（flush 依赖 setImmediate/mock 交互，极易永阻）。go test 的 `-timeout` 与外部 `timeout` 前缀都不可省
- **integrations/ 目录准入规则（2026-09-27 立，起因：MCP-2 收口后 mcp/ 占顶层破坏「顶层包 = pi packages/* 镜像」不变量）**：顶层包只放 pi `packages/*` 的镜像层；pig 内建的 pi-extension 对应物按功能分流——纯功能型（命令/UI）落 core/components（/share、/bug 先例），**协议/外部系统集成且大到需要独立叶子包的进 `integrations/<name>/`**（首例 `integrations/mcp/`，自顶层 `mcp/` 迁入），保持叶子约束（只依赖其 SDK + stdlib，pig 侧知识留 core）。
- **tmux**：agent 自身（及用户的长期会话）跑在同一 tmux server 里。冒烟/测试只允许 `tmux new-session -d -s <专属名字>`，结束 `tmux kill-session -t <专属名字>`；**严禁 `tmux kill-server`**。批量杀进程时 `pkill -f` 的模式会匹配自身命令行，用 `^/absolute/path` 锚定。**冒烟启动 pig 必须显式清空凭证环境变量（2026-10-03 立，起因：隔离 agent 目录的冒烟经 tmux server 环境误调真实 DeepSeek API ~6 次 ≈ $0.10）**：`PIG_CODING_AGENT_DIR` 只隔离配置/凭证**文件**（auth.json/settings/sessions），不隔离环境变量里的 API key（tmux server 环境继承用户 shell 的 key，进程 env 一查即中）——启动命令必须前缀 `DEEPSEEK_API_KEY= VOLCENGINE_API_KEY= OPENAI_API_KEY= ANTHROPIC_API_KEY= …`（置空即 unset，`auth.Env` 对空串返回 false）或 `env -u`，并在冒烟后抽查会话 jsonl 的 provider/usage 字段确认零真实调用。
- **每批收口必须预写下一批 handoff（2026-10-02 立，起因：S11-2 开工前靠 S11-1 会话上下文预消化 8 个 commit 落点，省掉开工批重翻 pi diff 与重新定位 pig 代码的 token 开销）**：每完成一批（含收口 commit 之前），在当前会话上下文还热着时把**下一批**的开工稿写进 `docs/handoffs/`：逐 commit 消化 pi diff、核实 pig 落点（文件/函数/行号/测试基建）、预判偏差与跳过项（引用不做清单裁决），做到开工批「照单施工、无需再翻 pi 仓库」。下一批范围未定时至少写「下一批候选 + 已知落点」段。首例：docs/handoffs/S11-2.md（4 实移 4 零操作声明）。
- **git worktree 并行开发规范（2026-09-12 立）**：用户要求用 git worktree 并行开发时按此执行——① **适用判据**：小 feature/fix 且**改动文件集合不相交**才并行（`interactive_mode.go`、`agent_session.go`、`BuiltinProviders`、`ai/models_generated.go`、`ai/index.go`、`go.mod` 是大磁铁/共享注册面，双方都碰就串行）；S 系基线同步批等动多区域的大迭代**独占串行**，期间并行小 fix 会无谓扩大冲突面。② **流程四步**：`git worktree add ../pig-<fix|feat>-<主题> -b <fix|feat>/<主题>` → 在 worktree 内正常开工（读 AGENTS.md + 对应 `docs/fix/`、`docs/feat/` 先例，回归测试 stash 验证照常走）→ 各分支独立过 §3.5 验收门 → master `git merge --no-ff` 合流（后合的一方先 rebase/merge master，**终审再跑一遍全量 `-race`**）→ `git worktree remove`。③ **共享写入点**：`AGENTS.md` 是唯一必然双写的文件（fix 在 `docs/fix/` 现有文件列表补一行、feat 在「批次索引」补一行），均为单行追加，后合者手工并、成本≈0；`docs/fix/`、`docs/feat/`、`docs/handoffs/` 每起独立文件，天然零冲突；`go.mod`/`go.sum` 双方都加了依赖则合流后 `go mod tidy`；`models_generated.go` 必冲突，rebase 后重跑生成脚本勿手工并。④ **全量 `-race` 错峰**：两个 worktree 同时跑会撞 `ai/auth` 的固定端口（codex 1455 / anthropic 53692，见 `docs/fix/2026-09-12-oauth-callback-listener-leak-flaky.md`）假红 `bind: address already in use`，错开几分钟或并行期间单跑时跳过该包。⑤ Go build cache 内容寻址共享（~/.cache/go-build），两 worktree 编译缓存大部分复用，并行成本远低于 node_modules 世界。并行 agent 的 tmux 窗格由用户自己开，agent 不感知、不管理 tmux（仅在需要真 TTY 冒烟时按既有 tmux 规则建临时 session）。

## 当前状态（2026-10-07）

- **pig 基线跟踪 pi v1.0.4 + main@27c7b6ff4**（S16 三批已全部收口，提前消化 43 commit——用户拍板提前消化，pi 无 v1.0.5 tag 故 Version 1.0.4 与 `TestVersionTracksPiBaseline` 守卫语义不动，v1.0.5 发布后 S17 只补增量）；全量 `go test ./... -race` 31 包全绿；最近一批 = S16-3（handoff：`docs/handoffs/S16-3.md`，含 S16 全系收口声明与 commit 13 outputPad 跳过声明）。
- **Dogfooding（2026-09-11 起）**：用户日常自用 pig，pi 保留作 fallback 与规格书。约定：① 日常撞到的 pig bug 一律走 `docs/fix/` 登记 + 回归测试锁定流程；② 缺 provider/adapter 卡住日常时按需提前拉起移植批；③ **真机验证矩阵** `docs/handoffs/verify-matrix.md`——provider 真机/OAuth 验证逐家解锁，每拿到一个 key 按表销账。
- **真机冒烟测试**一律 `//go:build smoke` 门控移出默认门禁（跑法见 `docs/feat/TEST-SUITE-RUNTIME.md`）；冒烟启动必须 mock provider / 清凭证（见仓库机制 tmux 条）。
- 路线余量：Phase 8 余量（photon/wazero、telemetry、goreleaser、/share、terminal-image）顺序由用户拍板；dogfooding 插批随时插入。

## 不做清单（用户定稿，后续批次碰到相关面一律不动）

- **Phase 5**（RPC/sqlite 后端）、**harness 上半身**（S2 已取消）、**pi experimental**、**Phase 7 扩展系统**全部不做（2026-09-21 定稿），历史 handoff 的「下一批建议 T7」作废。
- **/bug 命令与 bug-report 上报链**（Radius 上传 / zip 打包 / crash-log 的 /bug 指针）不移植——pig 用户走 GitHub issue（2026-09-30 追加，`docs/fix/2026-09-30-remove-bug-command.md`）；**后续 pi 上游对 /bug / bug-report 面的任何改动永不迁移，也不得把 /bug 相关能力重新加回 pig**。
- durable/chord 两个 pi 新包整体延后不迁；**pi-env**（`@earendil-works/pi-env`，SSH 远程执行环境 + Rust 远端 daemon，Durable ExecutionEnv 实现）同族随 durable 延后（2026-10-06 S15 拍板，`docs/handoffs/SYNC-S15-v1.0.4.md` §0）；virtual models 按「pi experimental 不做」跳过；codemode+tool_search 不做（候选设计见 `docs/handoffs/SYNC-S10-v0.99.1.md` §5）；npm 发行与 3D 彩蛋不迁。
- mistral-conversations / pi-messages adapter 按需延后（无内置 provider 消费）。

## pig-only 偏差清单（同步批遇到这些面一律跳过/维持，勿被上游「纠正」）

1. **BLOCK-BORDERS / 整框化**：消息/工具块为「上下边框、无填充」设计（pi 上游为填充设计），user message 规则线、tool_execution 状态色边框、frame_body.go 全族——同步批遇这些面一律跳过（`docs/feat/BUILTIN-THEME-PALETTES.md`，含同日撤销的两内置主题存档与 pig-only FgOptional token 机制）。
2. **daxnuts 保留**：pig 先于上游的存量面且用户明确喜欢，不跟随上游删除；触发条件不变（OpenCode provider 下 ID 含 kimi-k2.5 的模型）。
3. **MCP 自研路线**：`integrations/mcp/`（协议引擎 + OAuth 整族）+ `core/mcp_integration.go` 桥接 + `/mcp` 面板，对齐 pi-mcp-adapter 语义取舍版；上游 mcp extension / pi-mcp-adapter 包不整体迁移，择优回填（`docs/feat/MCP.md`）。
4. **配置目录 `.pig` / env 前缀 `PIG_*`**（存量数据不迁移，`~/.pig` 与 `~/.pi` 隔离）；其余 pi 自有 env 与目录无关的不改。
5. **write 预览 200ms 节流防闪屏**（`docs/fix/2026-09-30-write-preview-streaming-flicker.md`，pi 无对应物）。
6. **logoStyle 三值启动 logo**（blocks 默认 / plain / gradient）+ header spacer pi 原样（`docs/feat` git 历史）。
7. **CJK 切词后端可切换**（`cjkSegmenter` 设置 + `PIG_CJK_SEGMENTER`，默认 bigram；vendor 词典见 `docs/feat/CJK-WORD-SEGMENTATION.md`）；日文 kana 逐字切为已知缺口。
8. **footer 资源段**（pig-only 键 `showResourceStats`，默认关；CPU 异常浮出，`docs/feat/FOOTER-RESOURCE-STATS.md`）。
9. **`[Image N]` 粘贴占位符 + macOS 剪贴板贴图外部工具路**（osascript 主路，SSH/tmux 图像原理性无解不追；`docs/feat/CLIPBOARD-IMAGE-PLACEHOLDER.md`、`MAC-CLIPBOARD-IMAGE.md`）。
10. **autocomplete stale accept 守卫** `autocompleteAcceptIsCurrent`（pig-only，`docs/fix/2026-09-18-autocomplete-stale-accept-mangles-text.md`）。
11. **主屏视口重绘三分支修复**（pig 独立偏差，pi 上游 #9255 仍 open；`docs/fix/2026-09-13-main-screen-viewport-redraw-jump.md`）。
12. **协议值与服务端点保留 pi 形态**（X-BILLING-INVOKE-ORIGIN、pi.dev URL 族、User-Agent `pi/` 等），文案 Pig 化。

## 移植教训速查（跨批有效）

- 终端宽度不对称原则：多算只损 padding 一列（安全），少算触发折行失同步（灾难）——不确定时往宽了算；宽度类问题唯一可信证据是向真实终端查询读回。
- 非 ASCII 字面量移植前必须 hexdump/cat -A 核对（隐形韩文字符陷阱）。
- 「双形态家族」：pi 单线程下值/指针形态不分家，pig 解码产出值形态、构造器两种都有——所有 type switch 断言必须双形态兜底（已五例）。
- pi 重命名必须同批整体改（见 §3.5）；pig 侧进程并发面（mutex/RunExclusive/mutateUI）是 pi 单线程事件循环的等价物，新后台 goroutine 一律收容（panic-resilience）+ UI 突变入 mutateUI。

## 批次索引（每批一行，细节一律见对应 handoff/feat/fix 文档）

**基线同步批 S 系（全部收口）**：
- S1（2026-09-10，→v0.85.1）session/jsonl 重写 + 双向兼容 4 门 — `docs/handoffs/SYNC-S1-v0.85.1.md`
- S2 ❌ 取消（harness 上半身冻结决策，消费者证据链在该文档）— `docs/handoffs/SYNC-S2-v0.85.1.md`
- S3（→v0.85.1）print 路保真 + agent/proxy.go 补齐 — `docs/handoffs/S3.md`
- S4（→v0.85.1，与 S2 并行）TUI 增量：鼠标协议/滚动条/搜索 — `docs/handoffs/S4.md`
- S5a / S5b（→v0.85.1）core 层 / interactive+组件+theme，S5 收口 — `docs/handoffs/S5a.md`、`S5b.md`
- S6-1 → S6-2 → S6-3a → S6-3b → S6-3c（→v0.86.0）ai+agent / TUI / core / interactive+misc / 清口 — `docs/handoffs/S6-*.md`
- S7（→v0.86.1）Meta provider + OSC 52 + 首次全 catalog 对照并修 7 个生成器缺口 — `docs/handoffs/S7.md`
- S8-1 → S8-7 + 收尾（→v0.87.0）agent 回调面 / projection+context_edit / compaction 投影化 / boundaries / interactive / strict+InputLimits / 杂项 / Version+四门 — `docs/handoffs/SYNC-S8-v0.87.0.md` + `S8-*.md`
- S9（→v0.87.1）生成器 9 commit + 缺口首移植 5 文件 — `docs/handoffs/S9.md`
- S10-1 → S10-4（→v0.99.1）模型面三型统一+生成器合并 / OAuth 重构+ChatGPT 登录 / OKLab+system theme / core 杂项+cwd symlink 修复 — `docs/handoffs/SYNC-S10-v0.99.1.md` + `S10-*.md`
- S11-1 → S11-3 + 收尾（→v1.0.0）anthropic federation+copy code / defaultTools reload 等 / fullscreen 默认+quietStartup+pastel / Version+四门 — `docs/handoffs/SYNC-S11-v1.0.0.md` + `S11-*.md`
- S12-1 → S12-3（→v1.0.1）anthropic inline tools / MCP 项目 override+auth URL / daxnuts 拍板+收尾 — `docs/handoffs/SYNC-S12-v1.0.1.md` + `S12-*.md`
- S12-4（插批 feat，不涉基线）MCP OAuth 整族 — `docs/handoffs/S12-4.md`
- S13（→v1.0.2）per-thinking-level sampling params（唯一实移）— `docs/handoffs/SYNC-S13-v1.0.2.md`
- S14（→v1.0.3）azure provider 首移（v1.0.3 改名形态）+ OAuth 刷新取消语义 + 死终端 ENOTTY + TUI Home/End — `docs/handoffs/SYNC-S14-v1.0.3.md`
- S15（→v1.0.4）ai retry HTTP/2 补模式 + 语法高亮多行保色/subst + MCP OAuth application_type + --tools 通配/--no-mcp + MCP Close 中断连接中 connect（pig 形状适配）— `docs/handoffs/SYNC-S15-v1.0.4.md`
- S16-1 ✅（2026-10-07，→main@27c7b6ff4 提前消化，无 v1.0.5 tag 故 Version 不动）ai 层 7 commit：retry server_busy / estimate 3.5 / bedrock OpenAI reasoning effort / LoginOptions.AgentName / anthropic 回调 free-port 回退 / durationMs ai 侧基建（拷贝语义并发偏差）/ classifier 目录平价（openai-decisions 常量+生成器条目+FilterAllModels，运行时零操作）— `docs/handoffs/S16-1.md` + 计划稿 `SYNC-S16-pre-digest.md`
- S16-2 ✅（2026-10-07）agent+codingagent 区：durationMs 全链（agent loop 盖戳/事件/扩展回调/渲染面/bash Took reload 存活/线格式兼容门）+ `--tools +name/-name`（GetToolListError/applyToolModifiers/sdk 判定/reload 修饰符保留/-name 永移）+ MCP #10562 偏差面核对（disable 先 abortConnect + 面板 toggle 后台化两处回填，其余三点已覆盖零回填）— `docs/handoffs/S16-2.md`
- S16-3 ✅（2026-10-07）TUI+interactive 区：bash ANSI 跨 chunk strip（SplitIncompleteAnsiSuffix RE2 look-ahead 改写 + bash_executor 单出口重构 + decoder FullRune 截断缓冲顺修）+ transcript 重建清选区（ResetTextSelection + renderSessionEntries 入口）+ herdr 超链接检测（detectHyperlinksCapability 仅 herdr live + PI_HYPERLINKS，其余分支 deferred）+ commit 13 outputPad 拍板前跳过 + S16 全系收口声明 — `docs/handoffs/S16-3.md`

**Phase 2 provider/adapter 批（2026-09-11~15，Phase 2 收口）**：
- A1+A2 anthropic-messages adapter + provider 铺开 + Claude/Kimi OAuth — `docs/handoffs/A1.md`、`A2.md`
- B1+B2 openai-responses 双 adapter + xai/openai provider — `docs/handoffs/B1.md`、`B2.md`
- C1 codex-responses（含 WebSocket 传输）/ C2 codex OAuth / C3 google / C4 google-vertex — `docs/handoffs/C1.md`~`C4.md`
- P2-T1 12 家 completions 系/网关 provider + copilot OAuth；P2-T2 bedrock adapter（aws-sdk-go-v2）+ amazon-bedrock — `docs/handoffs/P2-T1.md`、`P2-T2.md`

**Phase 6 TUI 批（2026-09-09~10，T1-T6 全量交付）**：
- T1 内核（utils/keys/terminal/tui/主屏）→ T2 编辑器两批 → T3 基础组件（含 markdown/marked Lexer）→ T4 交互组件（scroll-view/layout 引擎/alt-screen）→ T5a-1/T5a-2 theme+消息/工具渲染 → T5b/5b-2/5b-3/5b-4 外壳+扩展 UI+grok-mermaid → T6-1/T6-2 选择器族五命令 — 决策分别见 `docs/handoffs/T*.md` 与 `docs/batches/2026-09.md`。

**Phase 3/4 早期批次（2026-09-02~09-08，harness/session/core/print 模式 CLI 7 子批，Phase 0-4 交付）**：过程决策与偏差原文已整体迁移 `docs/batches/2026-09.md`；print 模式 CLI 拆批与里程碑 M1/M2 定义见 git 历史本文件 §4。

**dogfooding 插批 feat（活偏差已入上文偏差清单，此处只留索引）**：
- PROV-VOLC-PLAN（09-10 火山 Coding Plan provider）· LATEX（09-10）· PANIC-RESILIENCE（09-11）· WINDOWS-SUPPORT（09-16）· CJK-WORD-SEG / CJK-SEG-MODE / CJK-SETTINGS（09-12~14）· TUI-LOGO（09-13 起多次追加）· FOOTER-RESOURCE-STATS（09-14 起四次收敛）· MAC-CLIPBOARD-IMAGE / CLIPBOARD-IMAGE-PLACEHOLDER（09-16）· SLASH-COMMANDS-COMPLETION（09-18）· MCP-1 / MCP-2 / MCP 渲染器（09-27）· TEST-SUITE-RUNTIME（09-30）· WRITE-PREVIEW-FLICKER（09-30 fix）· BUILTIN-THEME-PALETTES + BLOCK-BORDERS + 整框化（10-03，主题部分同日撤销）——文档见 `docs/feat/`、`docs/fix/` 对应文件。

## docs/fix/：bug 排查记录（登记规则）

`docs/fix/` 存放**非批次性的 bug 排查与修复记录**——与 docs/handoffs/（批次交接）、docs/batches/（历史批次决策归档）分工：handoff 记录「做了什么、下一批做什么」，fix 记录「哪里坏了、根因是什么、怎么修的、怎么复现」。文件命名 `YYYY-MM-DD-<简短主题>.md`，头部标注日期（精确到分钟）、状态、涉及文件路径。

**登记规则**：每起 fix 完成后在下方「现有文件」列表补一条**一行摘要**（现象 + 根因），详情一律落对应 md；**禁止把长摘要写回本文件**。**每起 fix 必须同时落地回归测试函数**（stash 验证红→绿，fix 文档写明测试函数名与验证方式）。**README 同步义务**：fix 或批次若改变用户可见面，同批更新 README 对应段 + `docs/handoffs/verify-matrix.md`。

现有文件：
- `2026-10-03-restored-tool-results-dropped.md`：`--continue` 后工具卡片恒 muted 且输出不显示（toolResult 指针-only 断言丢值形态，双形态家族第五例）
- `2026-10-02-auth-select-stale-frame.md`：/login 对话框画面冻结（showAuthSelect 丢 requestRender）
- `2026-10-02-theme-style-colorfgbg-env.md`：TestThemeStyles 受宿主 COLORFGBG 漂移影响（t.Setenv 加固）
- `2026-10-01-truecolor-detection-missing.md`：主题色被量化 256 色（GetCapabilities truecolor 分支是占位桩）
- `2026-09-30-jump-to-end-indicator-rect-test-race.md`：指示器 rect 测试竞态（新增持锁访问器）
- `2026-09-30-remove-bug-command.md`：/bug 整体删除（pig-only 偏差定论，链路永不迁移）
- `2026-09-30-settings-hint-esc-cancel-misleading.md`：/settings 提示改 "Esc to confirm and go back"
- `2026-09-29-footer-stale-after-session-replacement.md`：/resume 后 footer 冻结（applyRuntimeSettings 漏调 + SetSession 不重建 view）
- `2026-09-29-settings-space-key-not-toggling.md`：/settings 空格不切换（编码空格进搜索分支；定论仅 Enter 切换）
- `2026-09-29-cpu-alert-threshold-false-positive.md`：CPU 告警阈值 30%→70%
- `2026-09-26-provider-refresh-goroutine-mutates-ui.md`：刷新 goroutine 裸改 UI race（包进 mutateUI）
- `2026-09-26-bash-update-throttle-onupdate-overlap.md`：bash 节流器 onUpdate 重叠 race（锁跨回调持有）
- `2026-09-24-ci-go-toolchain-mismatch.md`：CI go toolchain 版本混用（workflow 钉版本 + 守卫测试）
- `2026-09-24-copilot-retry-after-stub-header-data-race.md`：copilot 测试 stub 丢 Retry-After header 致竞态
- `2026-09-24-pending-tools-concurrent-map-fatal.md`：pendingTools 并发 map fatal（pig-only 锁全访问点）
- `2026-09-22-addmessagetochat-value-form-drop.md`：resume 重建丢 compaction/custom/branch 消息（双形态家族第四例）
- `2026-09-22-image-tool-413-oversize-and-history-bloat.md`：图片会话 413 + 内存/JSONL 膨胀（resize 管线 Go 原生化 + toolResult 图片 elision）
- `2026-09-22-stale-version-after-sync.md`：Version 常量漏 bump（TestVersionTracksPiBaseline 守卫）
- `2026-09-22-vs16-emoji-width-wrap-desync.md`：VS16 emoji 少算 1 格致流式鬼影（回滚 09-21 特例；确立宽度不对称原则）
- `2026-09-21-vs16-emoji-width-table-misalign.md`：**已被上条纠正回滚，勿引用其结论**
- `2026-09-19-terminal-query-dispatch-deadlock.md`：终端查询死锁（CAS + 可重入锁 + 九访问器审计）
- `2026-09-18-autocomplete-stale-accept-mangles-text.md`：stale 补全 accept 拼坏文本（pig-only 守卫三处 accept 点）
- `2026-09-18-reload-command-leaks-to-model.md`：/reload 原文发给模型（submit switch 漏 case）
- `2026-09-16-paths-canonicalize-test-expectation.md`：测试期望值没过 realpath（macOS /private 前缀）
- `2026-09-16-clipboard-path-eaten-by-slash-command.md`：`/` 开头文本被兑底吞掉（收窄为显式 deferred 清单）
- `2026-09-14-tui-turn-memory-leak.md`：逐轮内存增长定论非泄漏（设计内留存；顺带修 2 个真 ctx 泄漏）
- `2026-09-13-env-api-keys-fill.md`：env API key 回填层漏移植（ai/env_api_keys.go 补齐）
- `2026-09-13-main-screen-viewport-redraw-jump.md`：主屏跳顶后重绘风暴（pig 独立三分支修复）
- `2026-09-12-markdown-cjk-punct-emphasis-closer-rejected.md`：marked multiple-of-3 条件写反（CJK 斜体失效）
- `2026-09-12-oauth-callback-listener-leak-flaky.md`：OAuth 回调 listener 泄漏（Close 与 Serve 竞速；并行 worktree 撞固定端口 1455/53692 亦见此）
- `2026-09-11-tool-execution-live-output-empty.md`：工具 live 输出整段不显示（渲染端双形态，两级丢失点）
- `2026-09-11-find-test-fd-8-6-no-require-git.md`：fd 8.6 无 --no-require-git（测试能力探测 SKIP，运行时保持对齐 pi）
- `2026-09-11-toolcall-title-invalid-arg.md`：调用行标题 [invalid arg] 闪现（str() 三分语义被压扁）
- `2026-09-11-latex-escaped-byte-rune-index-panic.md`：CJK 行内公式 panic（字节偏移当 rune 下标）
- `2026-09-11-footer-usage-stats-missing.md`：footer usage 统计消失（单边指针断言，双形态家族）
- `2026-09-11-markdown-table-cjk-rendering.md`：CJK 表格 UTF-8 损坏 + 分隔线错位（rune→byte 偏移）
- `2026-09-10-skill-desc-length-and-auth-storage-flaky.md`：skill 长度字节计数误用 + auth-storage 测试 flaky
- `2026-09-10-tui-streaming-empty-partial-content.md`：非流式渲染空（blocks 快照 + StatusIndicator 按值拷贝）
- `2026-09-10-reflect-method-func-identity.md`：reflect 方法值身份比较误用（utils.SameFuncValue 统一）
- `2026-09-10-cjk-adjacent-inline-math-not-tokenized.md`：CJK 相邻公式不触发（字节→rune 索引换算缺失）

## 1. 目标与保真度定义

把 pi（TypeScript monorepo，核心约 9.2 万行 TS）全量重写为 Go：单个静态二进制、无运行时 Node 主依赖、秒启低内存、交叉编译简单。

**对齐标准 = 行为规格级对齐**（非代码级翻译）：provider 流式事件序列（含 usage/tool call 边界/错误中止语义）、CLI 行为、会话 JSONL schema 双向兼容、TUI 交互（pi-tui 移植）。pi 仓库整体作为可执行规格书。明确放弃：npm 库消费者、有 UI 的 TS 扩展、photon 性能特征。

## 2. 基础决策

| 决策 | 结论 |
|---|---|
| spec 基线 | pin pi 正式 tag，之后按同步批选择性跟随（永不追 main） |
| 扩展模型 | Go 主进程 + Node sidecar（Phase 7 已裁决不做，设计存档 `docs/handoffs/EXTENSION-PLAN.md`） |
| TUI | pi-tui 全量移植（2026-09-09 变更，弃 bubbletea；字节级程序 + 17k 行测试 test-for-test 移植） |
| 会话格式 | 兼容读写 pi 的 JSONL session（v3 层；v4 runtime 层写入器 pi v1.0.0 已删，不做） |
| 目录/env | **`.pig` 偏差（2026-09-10 用户决策）**：项目级 `.pig/`、用户级 `~/.pig/agent/`、`PIG_CODING_AGENT_DIR`，存量数据不迁移 |

技术选型：Go 1.26.0（go.mod floor，CI 钉版本）、golang.org/x/term + rivo/uniseg、modernc.org/sqlite、goldmark + chroma、aws-sdk-go-v2、gse（CJK 词典，vendor）。

## 3. 架构

### 3.1 模块布局（与 pi 包一一对应）

命名规则：目录 = pi 包名去 `-`（`coding-agent` → `codingagent`）；文件 = TS 文件名转 snake_case（下划线间隔），两边 grep 同名即可互查。顶层包只放 pi `packages/*` 镜像层；pig 内建 pi-extension 对应物按功能分流——纯功能型落 core/components，协议/外部系统集成进 `integrations/<name>/`（见仓库机制准入规则）。

```
pig/
├── ai/                             # ← packages/ai（types/ auth/ api/ compat/ providers/；models_generated.go 生成物禁手改）
├── agent/                          # ← packages/agent（harness/ search/）
├── tui/                            # ← packages/tui（pi-tui 全量）
├── protocol/ client/ server/       # ← 同名 pi 包（Phase 5 不做，仅骨架）
├── sessionbackends/ telemetry/ evals/
├── codingagent/                    # ← packages/coding-agent（core/ modes/ server/ client/ cli/ extensions/ utils/）
├── integrations/<name>/            # pig 内建外部系统集成叶子包（mcp 先例）
├── cmd/pig/main.go                 # ← coding-agent src/main.ts + cli.ts
└── scripts/
```

依赖方向（= pi build 顺序，depguard 固化）：`tui`/`telemetry` 叶子 → `ai` → `agent` → `protocol`/`client`/`server` → `codingagent`（依赖全部）；`integrations/` 只依赖其 SDK + stdlib。测试贴代码（`_test.go` + 各包 `testdata/`）；生成物统一 `_generated.go` 后缀。

### 3.2 扩展边界

Phase 7 扩展系统已裁决不做（见不做清单）。sidecar 协议设计与兼容矩阵存档于 `docs/handoffs/EXTENSION-PLAN.md` 与 git 历史。

### 3.4 改写规则（TS → Go）

移植任何 pi 模块前先读本节。规则的目的只有一个：让 pig 与 pi 保持可 grep、可对照、线格式兼容。

**命名与顺序**

- 目录 = pi 包名去 `-`；文件 = TS 文件名转 snake_case（**Go 文件名一律下划线间隔，禁止去连字符连写**）：`agent-loop.ts` → `agent_loop.go`、`zai-coding-cn.ts` → `zai_coding_cn.go`、`moonshotai.ts` → `moonshot_ai.go`（无分隔的连写复合词同样按下划线拆分）、`error-body.ts` → `error_body.go`。反例（禁止）：`zaicodingcn.go`、`agentloop.go`
- 文件内声明顺序跟随 pi 源文件声明顺序；新增类型插入对应位置，跳过的段落留相对顺序空档
- pi 的字段注释（默认值、例外语义、平台特判）是移植内容的一部分 → Go 字段注释逐条带上；字段语义与 Go 零值冲突时改用指针（如显式 0 有含义的 `MaxRetries *int`）
- 命名保留 pi 名，但参数名避开 Go 惯用词：pi 的 `context` 参数 → `llmContext`（`ctx` 专属于 `context.Context`）
- 凡相对 pi 有缺失（整段类型、结构体字段、union 成员、文件内分段）→ 必须在缺失位置留注释，写明缺什么、为什么、何时补；"以后再说"不构成省略注释的理由
- 例外：`utils/event-stream.ts` → `ai/event_stream.go`（包根，避免 import cycle）
- pi 的内联 union 提为命名 Go 类型时（`Role`、`ModelInput`、`ContentKind`），放在原声明位置

**线格式（硬约束）**

- JSON 字段名与 pi 完全一致（camelCase），字符串字面量完全一致（`"toolCall"`、`"toolResult"`、事件名、stop reason）——会话与 RPC 线兼容依赖此条，改动即破坏双向兼容

**类型映射**

- discriminated union（`Content`、`Message`）→ interface + 具体类型 + `Decode*` 分发函数；heterogeneous 数组字段配自定义 `UnmarshalJSON`（alias 技巧 + raw 字段重解码）
- 事件 union（`AssistantMessageEvent`）→ 单 struct + `Type` 判别字段
- `X | string` 开放 union（`Api`、`ProviderID`）→ 命名 string 类型 + 已知值常量，不封闭
- TS 可选字段 → 指针 + `omitempty`；区分"未设置"与"零值"是 compat 语义的一部分；取指针一律用 `lo.ToPtr(x)`，**禁止手写本地 ptr 助手**
- 接口继承重复声明同名字段且仅细化文档 → Go 嵌入自动提升，注释挂嵌入字段上
- TypeBox schema → JSON Schema（`json.RawMessage`）；timestamp 统一 int64 毫秒
- AbortSignal → Go 惯例：取消经 `context.Context` 参数传递，不进 options 结构体；被省略的 pi 字段必须在类型注释里显式列出并写明归期
- pi 回调允许返回 Promise → Go 回调为同步签名，异步由适配层内部处理
- 泛型 `TModel`（仅回调签名）→ 具体 `*Model`；`startSpan<T>` 返回值传播 → 回调只传 `error`，其余值走闭包
- async 可迭代类（`EventStream<T, R>`）→ 泛型 struct + `iter.Seq` + 结果 channel；TS 端永不 resolve 的边界 case 在 Go 侧 resolve 零值并注释声明
- 接口可选成员（`fetchDeferred?`）→ 拆分 capability 接口（`DeferredStreamer`），调用侧类型断言
- TS 条件类型 / 类型映射表 → 无运行时对应物，各 adapter 自声明 options 类型，dispatch 侧类型断言

**结构保真**

- pi 的一个类/闭包对象/函数 → pig 一个 struct/函数，一一对应；不得擅自拆分、合并、引入辅助类型或改变逻辑归属
- 对结构组织有任何偏离 pi 的需要，先说明理由、征得确认后再动手；"代码更干净"不构成单方面改结构的理由
- 行为与结构的偏差（含顺手简化）必须在变更说明里显式列出，不允许静默带入

**包分层规则（ai 模块）**

- 依赖严格单向：`ai/types`（叶子，仅 import telemetry）← `ai/auth` ← `ai` ← `ai/api`、`ai/compat`、`ai/providers`（叶子，import 向下）← `agent` ← `codingagent`。depguard 固化
- `ai/types` 是消息模型/事件协议/compat 的规范定义地；`ai/index.go` 把 types 面再导出为别名与 var 绑定（新增 types 类型时须同步补别名，漏了编译器立即发现）
- 泛型类型无法起别名：`EventStream[E, R]` 本体只在 `ai/types`，需要原始泛型的消费者直接 import `ai/types`
- 实现载体变化时概念名仍跟 pi（如 `AbortController` → `refreshController`），不起新名；同一行为的语言收敛允许简化，但必须在注释里声明
- `A & Record<string, unknown>`（provider 专属扩展键）→ struct + `Extra map[string]any`；`X | null` 记录字段 → `map[string]*string`（nil = 抑制默认值）

### 3.5 批次执行协议（handoff 交接，2026-09-10 定稿）

每批收尾时，除本文件批次索引更新与验收门外，**必须写 `docs/handoffs/<批次号>.md`**（每批一份；不覆盖不删除——旧 handoff 自然成为该批的决策归档）。写作时点 = 批次完成、上下文满载时——此时写几乎零成本，而下一个全新 session 只读本文件 + 最新一份 handoff（约 2k token）即可直接开工，免去重新探索代码（约 50 倍开销）。本文件批次索引必须显式指向最新 handoff 的完整路径，防误读旧文件开工错的批次。**另见仓库机制「每批收口必须预写下一批 handoff」条：下一批开工稿在收口 commit 之前预写。**

**模板**（全文控制在 2000 token 以内，只写增量，本文件已有的规则/决策写引用不写重复）：

1. **本批完成**：新建/改动文件清单（精确路径 + 行数）；测试（新增函数数/子测试数、全仓总数、-race 是否全绿）；与本文件的新增偏差与决策（仅本批新产生的，每条一句话）
2. **下一批定义**（批次范围由用户拍板，不得自行发明）：要读的 pi 源文件精确路径逐个列出；要移植的 pi 测试文件精确路径；预期产出的 Go 文件目标路径（按命名规则）；依赖与顺序
3. **风险与坑**：上批实证发现、下一批大概率会撞的——逐条列现象/原因/正确处置
4. **明确不要读的**：与下一批无关、禁止顺手翻阅的目录/文件（防上下文浪费）

**硬性要求**：

- 「下一批定义」里的 pi 源文件路径必须精确到文件，禁止模糊描述——探索规格书是下一批开销大头，路径列全等于省掉它
- **pi 重命名必须同批整体改**：基线同步遇 pi 改名时，pig 侧一次性批量重命名定义与全部引用点（含测试），改完 `grep 旧名` 确认零残留，禁止新旧名混用或逐点零散修补
- **执行纪律（token 预算，2026-09-21 立，复盘见 `docs/handoffs/S6-3b.md` 第 0 节）**：① 动笔前把涉及的全部 API 签名、嵌入字段归属、设置键形态、测试 harness 能力一次性读齐，测试代码争取一次编译通过，禁止「写→编译错→修」循环；② 大文件多处改动合并为一次 edit/write，edit 老文本取最小唯一片段；③ 输出纪律：minified/golden/大 JSON 不进上下文，验证长行用 `grep -c`/`cut` 截断，goroutine dump 先过滤再输出；④ 新文件 write 一次成型优先于 diff-heavy 文件，每个测试写完立即窄范围 `-run` 验证
- 若判断计划需要调整，在文档末尾单列「给人类的建议」段提出，**不擅自改计划**——批次范围的决策权在人，模型只执行定义良好的批次
- 写完自查：一个从未参与本项目的 session，只读本文件 + 最新 handoff 能否直接开始下一批而无需探索？不能则补齐
- 验收门照旧：`go build && go vet && gofmt -l` 干净 + `go test ./... -race` 全绿，方可收批

## 4. 分阶段计划（状态总览）

原则：先无头、后有脸；可自动验证的先行。**批次体量控制（2026-09-21 修订，长期有效）**：按 pig 预算行数计——源码 + 配套测试 + 测试基建合计 ≤6000 pig 行，超出则继续拆子批；pi 行数仅作参考（interactive/TUI 面 ×2-3、core 面 ×1.2-1.5）。同一批不得同时含「diff-heavy 基线同步」与「新功能族」两大块；上下文消耗过 25% 时做中点检查，剩余超预算就把已完成部分先收口成 handoff。

| 阶段 | 状态 |
|---|---|
| Phase 0 脚手架 / Phase 1 ai 核心 | ✅ |
| Phase 2 ai providers | ✅ 全量收口（P2-T2 bedrock 清零存量尾；仅 mistral/pi-messages 按需延后） |
| Phase 3 agent 包 | ✅ |
| Phase 4 coding-agent 无头核心 + print 模式 CLI（7 子批） | ✅（M2 达成） |
| Phase 5 protocol/server/client/sqlite | ❌ 不做（2026-09-21 用户决策） |
| Phase 6 TUI（pi-tui 移植，T1-T6） | ✅（M4 交互日常自用达成） |
| Phase 7 扩展系统 | ❌ 不做（2026-09-21 用户决策） |
| Phase 8 杂项 + 发布 | 🔶 P8-EH1（export-html + --export + migrations）✅；余 photon/wazero、telemetry、goreleaser、/share 待用户拍板 |
| 基线同步 S1→S13 | ✅ 现跟踪 pi v1.0.2（v1.0.2 起 pi 删 agent 侧 jsonl v4 写入器，v4 互操作门为声明核对制） |

### 基线同步策略（2026-09-08/09 定调，长期有效）

1. **钉正式 tag，永不追 main**：同步目标恒为「当时最新正式 tag」，不追无版本的中间态
2. **不逐版跟进**：多版累积一次消化更便宜（每版固定成本 = 全量回归 + 快照 + 偏差记录）；但大漂移可按区拆系列批（S6/S8/S10/S11 先例）
3. **分区消化，风险序**：线格式核对（硬约束优先）→ ai/agent/session 增量 → TUI 增量 → coding-agent 增量，每区做完跑全仓回归
4. **收尾动作**：本文件重新 pin + 偏差清单归档 + Version bump（`TestVersionTracksPiBaseline` 守卫）+ 双向兼容门（v3 四向实测或按 handoff 声明沿用）
5. **pi 仓库只读**：对比用 `git fetch` + `git diff <old-tag>..<new-tag>` 或独立 worktree；catalog golden 上游漂移可整文件重 sync（C2 先例）
