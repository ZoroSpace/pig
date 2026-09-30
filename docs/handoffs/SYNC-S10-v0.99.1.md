# S10：pi v0.87.1 → v0.99.1 基线同步计划（📋 计划稿，2026-09-30 立）

**状态**：📋 **计划（未开工）**。pig 基线现跟踪 pi v0.87.1（S9 收口态），目标推进到 v0.99.1（d86654abb，2026-09-29）。
**规模**：92 commits / 672 files / **+71983/−13458**。注意 pi 版本号从 0.87.1 直接跳到 0.99.0（跳过 0.88~0.98，无中间 tag），changelog 以 v0.99.0/v0.99.1 两节为准。
**与 S8 对比**：S8 是 18 commits/+7.5k 行，本批 92 commits/+72k 行——但约 1/3 是「不做清单」覆盖面、约 1/3 是两个 pig 从未镜像的新包（durable/chord），真正落 pig 映射面的源码 diff 约 **8k 行**（ai 4.3k + tui 1.7k + core/interactive 2k+），总量与 S8 相当或略大。

## 0. 范围裁决（用户已拍板，2026-09-30）

1. **durable + chord 两个新包整体不迁**：pi 新产品线（durable = 任务运行时/document 状态/harness registry，chord = 服务与复制状态组合运行时），占 diff 近 10k 行。与 pig 冻结的 harness 上半身/RPC 高度相关，**声明整体延后**，等 pi 侧 durable 收敛（仍在 Package 15 阶段）再议。涉及「顶层包 = pi packages/\* 镜像」不变量，在 AGENTS.md 记一笔。
2. **virtual models 不做**（experimental，按 2026-09-21「不做清单」pi experimental 一律不做；core 侧 `/session` 每物理模型 cost 拆分随 virtual models 一起跳过，不单独拆）。
3. **codemode + tool_search 不做**（builtin extension 形态 + QuickJS 沙箱依赖，路线与成本见 §5 候选设计记录；将来 dogfooding 出现真实需求再独立立项）。
4. **MCP 保持 pig 自研路线不动**（integrations/mcp，MCP-1/MCP-2）：不迁 pi 的 builtin mcp extension（3.6k 行，OAuth/streamable HTTP/`pi mcp` CLI/registerMcpServer），只**择优回填** pig 自研实现里缺失的修复（首推 #10159 OAuth 刷新跨进程串行化；其余逐条甄别）。
5. **直接跳过**：RPC 族（disposition #9098/#9803、RpcClient #9990）、扩展 tool API 全族（exposure/namespace/outputSchema/ctx.executeTool/nested-tool-calls）、`provider_stream_event`（#9901）、`--no-extensions`/`builtin:<name>` 命名语义、/bug 相关引用处（09-30 决策）、TypeScript 7 构建链、llama.cpp provider/classify 面（pig 无 llama.cpp；System One/Jev classifier 随 codemode 跳过，但 catalog 类型判别是结构性的，在 S10-1 内裁剪处理）。

## 1. 三条硬变化（先读，决定全局姿态）

1. **模型类型判别重构（结构性最大）**：pi 删除 `image-models.generated.ts`（−834）与 `images-models.ts`（−275），OpenRouter 图像模型并入主 catalog，模型条目变 chat/image/classifier 判别（#9948 "unify image and classifier model infrastructure"）；`ModelRuntime` 新增 `generateImages()`/`getModelsOfType()` 等。**pig 的 generatemodels + generateimagemodels 双生成器链路要合并重构**——本批唯一有架构风险的点，S10-1 开工前先出设计。pig 侧对应物：`ai/models_generated.go`、`ai/image_models_generated.go`、`ai/scripts/generatemodels`、`ai/scripts/generateimagemodels`、`core/models_store.go`/`model_registry`/`remote_catalog_provider.go` 消费面。
2. **OAuth 回调服务器共享重构**：新共享 `callback-server.ts`（+183），anthropic/openai-codex/openrouter/radius 四个 OAuth 文件各减 130~180 行改走共享回调服务器；新增 **openai-chatgpt OAuth**（+310，Sign in with ChatGPT，deviceId 落全局 settings）。pig 的 OAuth 是各家独立实现（`ai/auth/oauth_*.go`），正好对齐重构顺带统一；pig 已知的回调端口竞态 fix（2026-09-12）语义并入共享层时需保持。
3. **System theme 成为默认主题**：颜色从终端报告的前景/背景/ANSI palette 派生（地基为全新 OKLab/OKHSL 色彩引擎：`tui/oklab.ts` +233、`tui/colors.ts` +367、interactive/theme/theme.ts ±675 重写），theme 文件新增 `#rgb`/`oklch()`/`okhsl()`/`appearance`，亮暗检测改背景色优先，内置 dark/light 换 OKHSL 新色板。纯数学移植难度低但量不小；pig 的 theme_json validator seam（S5b）要同步扩。

## 2. 增量盘点（92 commits 按主题归类 → pig 归属）

### 模型/catalog 面（→ S10-1）
| commit | 主题 |
|---|---|
| a328aa89a | **unify image and classifier model infrastructure（#9948，结构性）** |
| 4c2dfd936 | regenerate image models（随 #9948） |
| 7fd564cbb | pi.dev catalog 协议共享（#9763，`types=chat,image,classifier`）→ remote-catalog-provider |
| 12c416e1a | GPT-6.1 Sol（Codex 新默认） |
| c90d9ea58 | Claude Sonnet 5.5（1M 上下文 + adaptive thinking） |
| 28fd59086 + e1787702d + 6f7551516 | Kimi K2.6 删除 → K3（Together/OpenCode Go/Fireworks 默认跟随） |
| a6ca86102 | OpenAI Fast mode 计价（#10034） |
| 667fc3dd3 | Vercel AI Gateway 1h cache 计价（#9210） |
| dc84c1ac0 / 8930b9ec0 / c1449660c | Mistral thinking level（#9678）/ 空 delta（#9674）/ OpenCode qwen3.8-flash thinking signature（#10047） |
| c01f687e5 | 模型级 samplingParams 直连 stream()/complete()（#9506） |
| 1b2aa0ca0 | 未完成 Responses tool call 拒绝（#9974） |
| fde38ed7c | Copilot Opus 5.5 effort 稳定化 |
| a7d17e39a / 33e203354 / 04efdfc38 / 89a5c7bda | Jev/System One classifier 族 —— **随 codemode 跳过**，仅 catalog 类型判别在 S10-1 处理 |

### OAuth 面（→ S10-2）
| commit | 主题 |
|---|---|
| 02eed88fd + 72abf01ba | openai-chatgpt 登录 + bundled 修复 |
| 4df157433 | 共享 OAuth callback server（四家迁移） |

### tui/theme 面（→ S10-3）
| commit | 主题 |
|---|---|
| bf8e4b953 + 567469096 | System theme（#10067）+ 色彩值与 theme.style（#8398，oklab/colors 地基） |
| b485fa312 | theme 切换与流式渲染降本（footer usage totals 缓存、bash 预览缓存、sanitizeBinaryOutput） |
| f1927c2d5 | fullscreenWheelScrollLines 设置（#9758，wheel-scroll.ts） |
| 7cf037c21 | Kitty 图像纵横比失真（#9957） |
| d5629e204 | 括号/反引号内 autocomplete 路径触发 |
| 36af9dc48 | /skill 前缀 autocomplete（#9944） |
| b3487650f | overlay 关闭后光标恢复（#10026） |
| （#9973） | 自定义主题 truecolor 覆盖失效修复 |

### core/interactive 面（→ S10-4）
| commit | 主题 |
|---|---|
| ff72faba2 | 会话文件改在首条用户消息时创建（#10000，session-manager 面） |
| 30a1d1849 | defaultTools `+name`/`-name` 增量语法 |
| 1ff5b6fdd | bash/powershell structured results 扩到 1MiB（truncated + full_output_path） |
| 49681e1b7 | read null offset/limit 渲染 `:1` 修复（#9996） |
| fd889a274 | Finder 粘贴文件路径而非图标（#10136） |
| 8676a0dcd | X11 clipboard 误判图像（#9786，pig clipboard 面，Linux 半区） |
| b2bd111f2 | HTML export 隐藏消息 H 切换（#10020） |
| 2b0a123de | 启动 banner 去 Themes 段 |
| 5fd446ca1 | （settings-manager +104：新设置键族，随各批消化） |

### 跳过（按 §0 裁决逐条对号）
codemode and MCP（8562bcf66 及 follow-ups）、durable ~15 commits、chord ~8 commits、4259686d9（builtin:\name）、002fc8385（provider_stream_event）、9d1a65035/8d897edaa/f444ea5ea（扩展装载语义）、e473b5cd8/92e8d4f02（RPC）、ca7460d16（TS7）、da19b63c7 + llama/classify 族、b313731b8（JSONL sidecar reclamation，疑属 durable storage——开工时甄别一次确认）、changelog/release/docs commits。

## 3. 批次拆分（依赖序；S10-2 与 S10-3 改动面不相交，可 worktree 并行）

### S10-1：ai 模型面 + catalog 生成器合并重构（最大结构批，~2500 pig 行）
- pi 源：`ai/src/types.ts`（+194）、`models.ts`（+436）、`model-catalog.ts`（+78）、`providers/all.ts`（+95）、`model-runtime` 消费面、`remote-catalog-provider.ts`（+53）
- pig：`ai/types/`、`ai/models.go`/`index.go`、`ai/scripts/generatemodels`（**合并 generateimagemodels**）、catalog 重生成 + 同刻逐字段对照、`core/{model_registry,model_runtime*,remote_catalog_provider,models_store}.go`
- 要点：① 生成器合并设计先行（classifier 类型 pig 侧是否落枚举待裁剪：catalog 判别要，classify 运行时要随 codemode 跳过）；② ai 修复族 12 条逐条过（§2 表）；③ `image_models_generated.go` 删除后 OpenRouter 图像模型进主 catalog 的 golden 重同步；④ pi.dev 协议共享对 remote-catalog-provider 的影响。
- **开工前置**：本批先单独出 1 页设计（生成器合并 + classifier 裁剪边界），过目后再动手。

### S10-2：OAuth 重构 + ChatGPT 登录（~1500 pig 行）
- pi 源：`ai/src/auth/oauth/callback-server.ts`（+183 新建）、`{openai-chatgpt,anthropic,openai-codex,openrouter,radius}.ts`、settings deviceId
- pig：`ai/auth/` 新 `oauth_callback_server.go` + 四家 oauth 文件迁移 + 新 `oauth_openai_chatgpt.go` + `core/settings_manager.go` deviceId 段
- 要点：ChatGPT 登录流（PKCE/deviceId 稳定标识）、共享回调服务器的端口分配与生命周期（对照 pig 2026-09-12 端口泄漏 fix 语义）；verify-matrix 加 openai ChatGPT 登录行。

### S10-3：tui OKLab/OKHSL + System theme（~2500 pig 行，可与 S10-2 并行）
- pi 源：`tui/src/{oklab,colors,wheel-scroll}.ts`、`terminal-colors.ts`、`terminal-image.ts`、`autocomplete.ts`、`tui.ts`/`utils.ts`（渲染降本）、interactive/theme/theme.ts 重写
- pig：`tui/oklab.go`/`colors.go`（新）、`theme` 包扩 `#rgb`/`oklch()`/`okhsl()`/`appearance`、system theme 默认 + 终端 palette 探测、内置 dark/light 色板重生成（pig 的 embedded theme assets 同步）、渲染性能族、wheel/autocomplete/光标修复
- 要点：OKLab/OKHSL 纯数学 1:1 移植 + golden（node 直跑 pi 侧函数对拍）；亮暗检测优先级链（背景色 → 亮暗报告 → COLORFGBG）；pig-only 的 CJK/theme validator seam（S5b）回归保持。

### S10-4：core/interactive 杂项收尾（~1500 pig 行）
- §2 core/interactive 表逐条 + agent-session.ts（±922）中**非扩展耦合的剩余面**甄别消化（compaction/utils +41、usage-totals、性能缓存；扩展/boundary 相关部分确认跳过清单覆盖）+ settings 新键族 + banner
- 收尾：Version → 0.99.1（`TestVersionTracksPiBaseline`）、双向兼容 v3 四向（pig↔pi v0.99.1 互读 + `--continue` 带历史；#10000 首消息建文件注意老会话兼容）+ v4 门声明核对（harness jsonl 冻结区 diff 检查）、AGENTS.md 交付段

### 验收门（每批同 S8 惯例）
`go build ./... && go vet ./... && gofmt -l` 干净；全量 `out=$(go test ./... -race -count=1 -timeout 600s 2>&1); rc=$?` **只跑一次**；TUI 触觉面变更走 tmux 真 TTY 冒烟；catalog 批与 pi 同刻逐字段对照。

## 4. 开工前甄别清单（S10-1 前一次性做完）
1. agent-session.ts ±922 行逐 hunk 甄别：扩展耦合（跳）vs core 剩余面（S10-4）。
2. resource-loader.ts +262：mcp/codemode 装载 vs 通用诊断/装载面。
3. provider-composer.ts +234：virtual models 耦合 vs 通用。
4. nested-tool-calls.ts +261：确认整体属扩展 API 面（ctx.executeTool），pig 跳过不留半截。
5. b313731b8 JSONL sidecar reclamation 归属（durable？）。
6. pig 自研 MCP vs pi 新 MCP 差异清单（回填候选：#10159 OAuth 刷新串行化为首推）。

## 5. codemode 候选设计记录（延后立项，本批不做）

**触发条件**：dogfooding 中出现真实痛点（工具 schema 塞不下 / 需要编程式并行编排工具）再立项，不混入同步批。

**语义**（pi 0.99.0，非 sub-agent）：同一模型同一轮，模型写一段代码一次提交，沙箱确定性执行、期间零额外 LLM 推理，结果摘要回当前轮。省上下文（工具 schema + 中间结果不进对话）与往返延迟。sub-agent 则是独立推理实例烧 token，两者互补不替代。

**pig 侧语言选型**（2026-09-30 讨论结论，候选优先序）：
1. **Starlark（首选）**：纯 Go 实现（go-starlark）、零 CGO、天生为嵌入设计、默认无 IO 无并发（能力经显式 builtin 暴露——pig 工具注册成 builtin 即天然沙箱桥）、启动微秒级、Python 方言（模型熟练度高于 JS）。语言子集小（无 while/无生态库），但工具编排够用。
2. **bash + 工具子命令（对照方案，性价比最高）**：零新引擎零 CGO，bash 工具本就是「写代码一次提交」，只差内置工具不可调——把工具暴露为 `pig tool read ...` 类子命令即可编排。缺点：复杂编排时错误处理/结构化结果传递糙。
3. **Go 源码一次提交**：模型写 Go 无障碍，但执行侧要落盘→`go run`（编译冷启动 1~2s）→ 用户机器需 Go 工具链（违背单静态二进制承诺）→ 子进程回调工具需自建本地 RPC 面（Phase 5 砍掉的面变相回归）。工程面最大，不推荐。
4. **wazero + QuickJS(WASM)**：纯 Go 无 CGO、隔离最好，但需自维护 QuickJS→WASM 构建产物管线；wazero 本在 Phase 8 余量清单，真需要完整 JS 生态时两条合一起做。

**pi 参考实现**：`packages/coding-agent/src/extensions/codemode/`（worker.ts/execute.ts/tool.ts/renderer.ts 等 6 文件，QuickJS 沙箱 + worker 隔离 + 1MiB bash structured output + models.classify()）。立项时按方案 1 出设计文档走 feat 批流程。

## 6. 开工顺序建议

S10-1 设计稿 → S10-1 → S10-2 ∥ S10-3（worktree 并行，改动面不相交：ai/auth vs tui/theme）→ S10-4 收尾。全程注意 ai/auth 固定端口（codex 1455 / anthropic 53692）在并行期错峰。
