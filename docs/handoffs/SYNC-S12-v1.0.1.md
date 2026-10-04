# S12：pi v1.0.0 → v1.0.1 基线同步计划（✅ 全部收口：S12-1 ✅、S12-2 ✅、S12-3 ✅）

**状态**：**S12 系列 ✅ 全部收口（2026-10-04，S12-3 终稿 docs/handoffs/S12-3.md）**。S12-1 ✅（2026-10-03，终稿 docs/handoffs/S12-1.md）；S12-2 ✅（2026-10-04，终稿 docs/handoffs/S12-2.md）；S12-3 ✅（2026-10-04：daxnuts 零操作声明〔用户拍板保留〕+ Version 1.0.1 + 双向兼容 v3 门 1/2 实测全通 + tmux 冒烟）。**pig 基线现跟踪 pi v1.0.1**。S12-4（MCP OAuth）未拍板不开工，候选清单在 S11-CLOSING §4 + 本稿 §0.2-3。
**规模**：29 commits / 136 files / **+3186/−3238**（v1.0.0..v1.0.1 全区间）。大头是 npm 发行面（shrinkwrap 移除/audit/Nix flake/README，pig 全部不适用），**真正落 pig 映射面的生产面 diff 约 1.2k 行 + 测试**——比 S11（~1.5k）更小，是 v1.0 系列最轻的同步批。
**性质**：全部是特性增补与修复族，无结构性重构；本批 pi **动了 catalog**（生成器三处修正 + Clef 分类器 + NVIDIA 默认模型），**无线格式改动**（session jsonl / v4 门预期零 diff），无新框架依赖。

## 0. 范围裁决

### 0.1 沿用「不做清单」直接跳过（无争议，11 commits）

| commit | 内容 | 跳过理由 |
|---|---|---|
| 581e7ba78 | 移除 npm shrinkwrap + 推荐托管安装器 | npm 发行面，pig 是 Go 单二进制 |
| 0495646a8 | pin brace-expansion 5.0.12 | npm 依赖面 |
| 9fba660cf | npm audit 接受 node-forge advisory | npm 依赖面 |
| c71a496ff | Nix flake（#9137，+997） | 仓库打包面，pig 无对应物 |
| de7e675de / 4215d4d6b / 86dfceec4 | README×2 / changelog 审计 / Unreleased 段 | pi 自家文档 |
| a7229ddc2 | Release v1.0.1 | → 收尾批 Version bump |
| 319fecb89 | codemode 脚本输出封顶（+72） | **codemode 不做**（2026-09-30 拍板） |
| 221cbcb02 | Cloudflare Clef 文档 | 纯 docs，随 4812cb268 一并消化 |

### 0.2 已拍板与待拍板

1. **3D 彩蛋不迁移（用户已拍板，2026-10-03）**：`c450f2c0f`（恢复 3D pi logo 彩蛋）+ `7fbbd5f4a`（fullscreen /arminsayshi 播 3D Armin）整体跳过，记入延后清单。注：`easter-egg-3d.ts`（1292 行）是纯软件光线投射 + braille 像素渲染、无 WebGL 依赖，技术上可移植，但纯娱乐面，若日后想要单独插批。pig 侧现存的 2D armin.go 不受影响（上游 2D armin.ts 仍保留）。
2. **daxnuts 删除是否跟随（✅ 已拍板，2026-10-04：保留）**：`1c1e9c0ef` 上游删除了 daxnuts 彩蛋（−183 行，pig 对应 `components/daxnuts.go`）。用户拍板保留（pig 先于上游的存量面且用户明确喜欢），S12-3 对该 commit 整体零操作声明，后续同步批按 pig-only 偏差跳过 daxnuts 相关面。
3. **MCP OAuth（S11-4 悬案）是否借 `1499466d8` 立项（待拍板）**：pi 这轮给 mcp 包 OAuth 加了 opt-in 的 client ID metadata documents（+344）。pig integrations/mcp 至今无 OAuth，本批该 commit 连同整个 MCP OAuth 族继续挂账，追加进 S11-CLOSING §4 候选清单；若拍板立项则 S12-4 单列。

### 0.3 本批必迁面（生产面 ~1.2k 行 + 测试）

见 §2 / §3。唯二用户可见行为变更：① Anthropic native tool changes 换 beta header 与 wire 形态（inline tools）；② ChatGPT 登录端口被占从「降级 manual paste」改为「直接报错」。其余为修复族与 catalog 修正。

## 1. 观察（先读）

1. **anthropic inline tools 是本批唯一的 wire 形态变更**（`b271b0a52`，10 files +136/−97）：beta header 从 `mid-conversation-tool-changes-2026-07-01` 换成 `inline-tools-2026-09-15`；后续工具改为在 `tool_addition` 块内**按值内联定义**（`tool_addition.tools` 带 schema），请求级 tool list 全程固定，同名重定义不再触发全量 tool list 重发；`hasToolRedefinitions()` 门控删除（函数保留标 deprecated，transcript 层不再消费）；deferred placeholder（`__pi_deferred_placeholder__`）机制保留。pig `ai/api/anthropic_messages.go` 是手写 adapter，`convertMessages` 需加「native tool changes 时把后续工具转成 value 形态」的回调参数；`ai/utils/transcript.go` 同步去 `hasToolRedefinitions` 消费点。上游 `transcript-tool-changes.test.ts` 重写 124 行需全量移植。
2. **`11449730c` 的机制与修复要拆开看**：上游新增 `registerToolRenderer` 扩展 API（resolver 链），**扩展 API 机制属 Phase 7 不做，不迁**；但它修的用户可见问题（#10285：resume 会话里 MCP 工具调用在服务器连接前被完全展开渲染）对 pig 成立。pig 走 pig-only `mcpRenderCall` 等价路——开工时核实 pig 的 `mcp__<server>__<tool>` 前缀调用在工具未注册/服务器未连时是否已天然走 MCP 渲染器（大概率是，`mcpRenderCall` 挂在 RenderCall 按 name 匹配），核实后声明或补小修。
3. **Kitty 非 PNG 转换 pig 用 Go stdlib 反而更简单**：上游 `a276dabe5` 引入 `setImageTranscoder` seam + photon（wasm）注册；pig 无 photon，`image/jpeg`/`image/gif`/`golang.org/x/image/webp` 解码 → PNG 编码即可。⚠️ 前提核实：pig 图像管线此前声明「Phase 8 延后」（S10-3 `calculateImageCellSize` 等），这两例能否落地取决于 `tui/terminal_image.go` 现状覆盖层级——开工 handoff 先核实，覆盖不到就整体延后 Phase 8，不为本批拉起管线。
4. **catalog 重生成是 S12-1 的机械收尾**：生成器四处修正（§2 表）改完重跑，与 pi v1.0.1 `data/*.json` 逐字段对照；无新 provider、无新模型族（Clef 是 classifier 模型，pig S10-1 已有 opencode/cloudflare 硬编码 classifier 表基座）。

## 2. 增量盘点（生产面 commits 按 pig 归属归类）

### ai 层 + catalog（→ S12-1，改动面 `ai/` + `cli/`）

| commit | 主题 | pig 落点 |
|---|---|---|
| b271b0a52 | **Anthropic mid-convo tools inline**（§1-1，+136/−97） | `ai/api/anthropic_messages.go` + `ai/utils/transcript.go`；transcript-tool-changes 测试全量移植 |
| eeac84ca9 | **ChatGPT 登录端口被占 fail-closed**（#10265：原降级 manual paste，浏览器回调会撞上别人的 listener 报 state mismatch；现 EADDRINUSE 直接抛清晰错误） | `ai/auth/oauth_openai_chatgpt.go`——pig 的 S10-2 共享回调服务器结构不同但同语义，去端口占用时的 manual 回落分支即可 |
| 3874b3e98 | retry 增加 "Selected model is at capacity" pattern（+1） | `ai/utils/retry.go`（S11-1 Retry-After 同文件先例） |
| 9b3c19da5 | `--models` 忽略空条目（+5/−1） | `cli/args.go` + args_test |
| 4812cb268 | **Cloudflare Clef 分类器**（#10316：system-one 响应两形态——三方模型带 run record `{state,result.answers}`，CF 自营 `@cf/cloudflare/clef` 直接 `{answers}`；生成器 classifier 表 +20） | `ai/api/cloudflare*.go`（P2-T1 transport）+ 分类器表（S10-1 基座）+ 生成器 |
| 4665fafb4 | 保留 models.dev 定价 tiers 给 Amazon Bedrock（生成器 −6/+3） | 生成器 |
| c10bfb0d7 | Cloudflare AI Gateway 用 dashed Claude model ID（生成器 +5/−1） | 生成器 |
| 28eaccb8e | Together DeepSeek V4 Pro model ID 更新（#10336，1 行） | 生成器 |
| 49b9df489 | NVIDIA 默认模型 → nemotron-3-ultra-550b-a55b | `core/model-resolver` 对应物（defaultModelPerProvider 表） |

### MCP + interactive（→ S12-2 主半区，改动面 `integrations/mcp/` + `codingagent/modes/interactive/` + `core/` 桥接）

| commit | 主题 | pig 落点 |
|---|---|---|
| 1387af7b4 | **项目 mcp.json 可覆盖全局服务器的开关**（#10277：项目条目不含 command/url/type 即 override，仅覆盖 enabled/exposure/toolExposure；/mcp 对全局服务器的启停落盘项目文件） | `integrations/mcp/config.go`——pig 语义映射：override 条目（无 command/url）仅允许 `disabled` 键，later-wins 合并特殊分支；MCP-2 的 JSON 手术（disable.go）落盘目标扩展项目文件；exposure/toolExposure 无对应面裁剪 |
| 11449730c | **MCP 工具调用在服务器连接前可渲染**（#10285；机制不迁，见 §1-2） | 核实 pig-only `mcpRenderCall` 前缀匹配路径，声明或小修；上游测试 10285-mcp-tool-renderers.test.ts 甄别移植 |
| ced72c2f0 | **OAuth 登录 URL 可复制**（login-dialog 用 AuthUrlComponent 显示 copy hint，`app.message.copy` 键位复制；/mcp login 改走 sign-in 界面） | pig auth selector / login 面板加 copy hint（键位 defs 现成）；「/mcp login sign-in 界面」半区依赖 MCP OAuth，随 §0.2-3 裁决 |
| 1499466d8 | MCP OAuth client ID metadata documents（opt-in） | **挂账 S11-4 候选清单**（§0.2-3），不单做 |

### tui 图像（→ S12-2 并行半区，改动面 `tui/`；前置核实见 §1-3）

| commit | 主题 | pig 落点 |
|---|---|---|
| a276dabe5 | **Kitty 非 PNG 转换**：pi-tui 暴露 `setImageTranscoder`，Image 组件 JPEG/GIF/WebP 经它转 PNG，无 transcoder 显示文本回落 | `tui/terminal_image.go` 加 transcoder seam（pig 用 stdlib 实现）；tool_execution / interactive 接线 `ensurePngTranscoder` 等价物 |
| 672000c80 | **WezTerm 图片滚动时保留**（alt-screen 重绘不再擦图片格） | `tui/tui_alt_screen.go`（pig 已有 v0.86.0 WezTerm 擦除语义，S4 落点） |

### 彩蛋（→ S12-3）

| commit | 主题 | pig 落点 |
|---|---|---|
| 1c1e9c0ef | 删除 daxnuts 彩蛋（−183） | `components/daxnuts.go` + interactive 接线，待 §0.2-2 拍板 |
| c450f2c0f / 7fbbd5f4a | 3D 彩蛋 | **不迁移**（§0.2-1 已拍板） |

## 3. 批次划分与顺序

- **S12-1 ai 层 + catalog**（~400 行 pi diff）：inline tools（含 transcript 测试全量移植）+ ChatGPT 端口 fail-closed + retry pattern + `--models` 空条目 + Clef 分类器 + 生成器三处修正 + NVIDIA 默认；catalog 重生成 + 与 pi v1.0.1 data/*.json 逐字段对照 + golden 四文件重同步。
- **S12-2 MCP 面 ∥ TUI 图像**：主半区 MCP（mcp.json override + mcpRenderCall 核实 + copy URL）；并行半区 TUI 图像（先核实管线覆盖层级，覆盖不到则本半区整体改声明延后 Phase 8）。两半区文件不相交（`integrations/mcp/`+interactive vs `tui/`），可 worktree 并行；碰 `interactive_mode.go` 前核对磁铁规则。
- **S12-3 彩蛋处置 + 收尾**：daxnuts 处置（§0.2-2）+ Version bump 1.0.1（`TestVersionTracksPiBaseline`）+ 双向兼容声明沿用 S11 结论（本批无线格式改动，v3 四门抽查 pig↔pi 互读即可）+ 全量 `-race` 单次 + tmux 冒烟。
- **S12-4（仅若 §0.2-3 拍板立项）**：MCP OAuth 整族，独立最后开工。

## 4. 验收门（沿用 S11）

- `go build && go vet && gofmt` 干净；全量 `go test ./... -race -count=1 -timeout 600s` **只跑一次** rc=0。
- catalog 与 pi v1.0.1 生成器产出逐字段对照（classifier 面 Clef 为新增点，重点核对）。
- tmux 冒烟清单：anthropic inline tools wire 形态（mock 断言 beta header `inline-tools-2026-09-15` 与 `tool_addition` value payload；真机验证需 Anthropic key，登记 verify-matrix）/ ChatGPT 登录端口占用负路径 / /mcp 项目 override 落盘 / copy URL / 图像半区（若做）真终端渲染。**冒烟凭证 env 清空规则照旧**（AGENTS.md tmux 条目）。
- 测试新增预估：~15 函数（anthropic transcript 家族 ~6、Clef ~2、oauth 端口 ~2、mcp override ~3、args ~1、图像 ~4 若做）。

## 5. 未决项备忘

1. daxnuts 删除是否跟随（§0.2-2）——唯一阻塞 S12-3 的拍板项，其余可先行。
2. MCP OAuth（S11-4）是否借本批立项（§0.2-3）——不阻塞 S12-1/2/3。
3. TUI 图像半区的管线覆盖层级核实结论（S12-2 回填，2026-10-04）：pig 的 kitty **传输路不存在**——`tui/terminal_image.go` 是声明缩编版，Image 组件本体在 Phase 8 清单（tool_execution 内为 `pendingImageComponent` 占位且 `caps.Images` 恒 falsy），`EncodeKitty` 零非测试消费者 → **a276dabe5 整体延后 Phase 8**（不为本批拉起管线）；**672000c80 正常移植**（alt-screen kitty 机制 S4/S6-2 已交付，`GetKittyImagePlacementRows` + `imageCellsNeedRedraw` + `drawKittyImagesLast` 三段式），详见 S12-2 终稿 §1。
4. `golang.org/x/image/webp` 若不在 go.mod（开工核实），WebP 半区可裁剪为 JPEG/GIF 并声明。

## 6. 开工后回填（2026-10-03，S12-1）

- **§2 漏排一个 ai 层 commit**：`69f0be6f0`（Bedrock Claude replay 丢弃过期 thinking blocks，#10324，+32/−16）——计划盘点时漏计，已在 S12-1 纳入交付（`supportsThinkingBlockBinding` + `thinking.block_binding` + binding beta，GovCloud 跳过；测试六子测升级 + 三新子测），见 S12-1 终稿 §1。
- **§5-4 核实**：`golang.org/x/image v0.46.0` 已在 go.mod（webp 可用，无需裁剪）；`tui/terminal_image.go` 存在且含 kitty placement/crop 元数据层与 `GetPngDimensions`（PNG 专用入口），覆盖层级核实（非 PNG 图像在 pig 的消费面）仍为 S12-2 开工第一动作，见 docs/handoffs/S12-2.md。
- **golden 重同步决策**：models.dev 上游在 pi v1.0.1 打快照后有漂移（bedrock 177→183、openrouter 新增 2 image + 3 classifier 及存量内容漂移），pig golden 按 C2 先例重同步到 pi 生成器 live 产出，见 S12-1 终稿 §2。
