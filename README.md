# pig

> **Go 版 AI 编程助手 · pi 的行为级兼容重写**

[![Go 1.25+](https://img.shields.io/badge/Go-1.25%2B-00ADD8?logo=go)](https://go.dev/)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](#license)
[![Status: Dogfooding](https://img.shields.io/badge/status-dogfooding-orange.svg)](#provider实现--验证要分开看)
[![Go](https://github.com/ZoroSpace/pig/actions/workflows/go.yml/badge.svg)](https://github.com/ZoroSpace/pig/actions/workflows/go.yml)
[![codecov](https://codecov.io/gh/ZoroSpace/pig/graph/badge.svg)](https://codecov.io/gh/ZoroSpace/pig)
[![Go Report Card](https://goreportcard.com/badge/github.com/ZoroSpace/pig)](https://goreportcard.com/report/github.com/ZoroSpace/pig)

pig 是 [pi coding agent](https://github.com/earendil-works/pi) 的 Go 全量重写——一个跑在终端里的 AI 编程智能体：读代码、改代码、跑命令、流式回复，会话可分支可回溯。

pig 与 pi 做到**行为规格级对齐**：pi 的 TypeScript 代码就是 pig 的规格书。provider 流式事件序列、CLI 参数与退出码、会话 JSONL 逐字节格式、TUI 的每一次按键手感，都逐项对照移植。pi 的经验、文档、肌肉记忆和**现有会话记录**都可以直接带过来。

> [!WARNING]
> pig 处于活跃开发中的 dogfooding 阶段，CLI 与内部结构可能变化。目前 DeepSeek、火山方舟 Coding Plan 与 Google（部分）经过真实 API 验证，其余 provider 尚未打过真实服务，请勿默认视为可用，详见下文[验证分层](#provider实现--验证要分开看)。

## 起源

这个项目始于一次学习：在研究 pi agent 时，一些 TypeScript 的写法读不透，索性把读到的逻辑翻成 Go——翻完发现理解顺畅多了，随即起意：干脆全部翻译、复刻一遍。

于是用 pi + glm-5.3-flash 模型驱动开发，从零开始，10 天构建出 pig：底层的会话存储格式、中间层的 agent 循环与工具执行、流式事件协议，到上层的按键手感与交互体验，全部对齐 pi v0.84.3；随后又用一天追平最新的 v0.85.1。从某个 commit 起，pig 自己的开发也已切换到 pig 上进行（pi 保留作规格书与 fallback），此后长期维护：**pi 出一个新版本，就对齐一个版本。**

这里的"对齐"是文件、函数、struct 级的一一对应：上游每改一处，pig 侧都能精确定位到对应的文件与函数。这种粒度既是当初看懂 pi 的方式，也是能持续跟进上游迭代的保障。个别包与实验特性因 pi 仍在快速迭代而暂缓（见[能力边界](#与-pi-的能力边界)），等上游稳定后会写进本项目。

最后一点声明：手上没有各家 API 与账号，真实服务的实测只覆盖了 DeepSeek、火山方舟 Coding Plan 两家，以及 Google 的部分链路（详见[验证分层](#provider实现--验证要分开看)）。**欢迎拿到其他家 key 的朋友帮忙实测**——发现偏差请提 issue 或 PR，修复会按仓库惯例带回归测试合入。

## 概览

| 项目 | 当前情况 |
| --- | --- |
| 运行方式 | 交互式 TUI、`print` 文本模式、JSON 事件流模式 |
| 构建产物 | 单个 Go 二进制，无 Node 依赖（静态精简构建 28 MB） |
| 启动开销 | CLI 启动约 25ms、峰值内存约 30MB |
| 默认配置 | `~/.pig/agent`，项目级配置目录为 `.pig/` |
| 会话格式 | 与 pi JSONL 双向兼容（v3/v4 两代格式实测互读） |
| Provider | 25 家内置、约 960 个模型、7 种 API 协议适配器 |
| 真实服务验证 | DeepSeek、火山方舟 Coding Plan、Google（部分；其余见[验证分层](#provider实现--验证要分开看)） |
| 测试 | 1300+ 测试函数 · 28 个包 · `-race` 全绿 |
| License | MIT |

## 快速开始

### 1. 构建

```bash
git clone https://github.com/ZoroSpace/pig.git && cd pig
scripts/build.sh          # 产出全静态 ./pig（28 MB）；或 go build -o pig ./cmd/pig
```

### 2. 启动

```bash
./pig
```

进入交互式 TUI。

### 3. 配置认证

输入 `/login`：

- 在 provider 列表选择要用的服务（DeepSeek、火山方舟 Coding Plan、Anthropic、Gemini……）
- API key 类：粘贴 key 回车即可，凭证存入 `~/.pig/agent/auth.json`
- OAuth 类（Anthropic / Kimi / OpenAI Codex / xAI）：浏览器跳转或设备码流程

### 4. 开始对话

登录后 pig 自动选一个可用模型（想指定就用 `/model`），直接提问，例如：

> 请快速浏览这个仓库，并告诉我最值得优先修复的问题

脚本与 CI 场景可以跳过交互登录，直接环境变量 + 参数：

```bash
DEEPSEEK_API_KEY=your-api-key \
  ./pig --provider deepseek --model deepseek-v4-flash -p "解释这个项目的整体结构"
```

完整环境变量与认证状态见[认证](#认证)。

## 特点

### 🔥 Pure Go：一个文件，开箱即跑

pig 是纯 Go 实现，编译产物为**单个静态二进制**，拷到任何 Linux / macOS / Windows 机器上就能跑，交叉编译一行命令。与 pi（npm 安装，Node 运行时 + 依赖树）同机实测：

| | pig | pi |
| --- | --- | --- |
| 磁盘占用 | 28 MB 静态二进制 × 1 | 440 MB（npm 包，含依赖树）+ Node 运行时 117 MB |
| 启动（`--list-models`） | 0.025s | ~1.0s |
| 峰值内存 | ~30 MB | ~140 MB |
| 部署方式 | 拷一个文件 | Node + npm install |

无 npm install、无版本漂移、无 `node_modules` 黑洞，CI 容器和老旧机器上同样秒起。

### 🔁 与 pi 无缝互通

这是 pig 最刻意的设计约束，也是它区别于"又一个 CLI agent"的地方：

- **会话记录双向兼容**：pig 写的 JSONL 会话，pi 读得懂；反之亦然（用 pi v0.85.1 真实代码实测）。你今天的 pi 会话，明天可以在 pig 里继续，随时来回切换
- **逐字段一致的模型目录**：内置模型 catalog 由生成脚本产出，与 pi 的模型数据逐字段零差异
- **配置隔离，互不踩踏**：pig 用 `~/.pig/`，pi 用 `~/.pi/`，两套目录完全独立；想共用存量数据见[从 pi 迁移](#从-pi-迁移数据)

### 🌐 模型接入：25 家 provider，7 种 API 协议

各家模型服务的接口格式互不相同（OpenAI、Anthropic、Gemini 的 API 长得完全不一样）。pig 为每种接口格式移植了独立的适配器，事件语义（usage、tool call 边界、思考块、缓存命中、中止/错误/重试）逐协议对齐：

| API 协议 | 覆盖 |
| --- | --- |
| openai-completions | OpenAI / DeepSeek / OpenRouter / Groq / Cerebras / Together / NVIDIA / Moonshot / Z.ai 等 |
| openai-responses | OpenAI / xAI |
| azure-openai-responses | Azure OpenAI |
| openai-codex-responses | OpenAI Codex（含 WebSocket 传输与 zstd 请求压缩） |
| anthropic-messages | Anthropic / Kimi Coding / MiniMax / Vercel AI Gateway / Fireworks |
| google-generative-ai | Google Gemini |
| google-vertex | Vertex AI（含 ADC 服务账号认证） |

内置 25 家 provider、约 960 个模型开箱即用；非内置端点通过 `models.json` 自定义 provider 挂进来。Anthropic / Kimi / OpenAI Codex / xAI 已移植 OAuth 登录（浏览器 + device code 双流程，目前为 mock 级验证，真实登录链路待销账）。

### 🛠 干活的工具：内置七件套

| 工具 | 作用 |
| --- | --- |
| `read` | 读取文件 |
| `bash` | 执行 shell 命令 |
| `powershell` | 在 Windows 上执行 PowerShell 命令 |
| `edit` | 对文件做定向查找替换 |
| `write` | 创建或覆盖文件 |
| `grep` / `find` / `ls` | 只读文件操作 |

编辑工具带 diff 预览与文件互斥锁，bash 工具支持长输出落盘与流式折叠渲染；项目级 `AGENTS.md` / `CLAUDE.md` 上下文文件、SKILL.md 技能、prompt 模板、项目信任机制全部就位。

可以限制本次运行的工具权限：

```bash
./pig --no-tools                          # 完全禁用工具
./pig --tools read,grep,find,ls -p "只审查源码，不修改任何文件"
./pig --exclude-tools bash,write          # 禁用指定工具
```

### ⌨️ 交互体验：pi-tui 全量移植的终端手感

pig 把 pi 自研的 pi-tui **整库移植**，没用任何现成 TUI 框架——差分行渲染器、编辑器引擎、补全系统一个不落：

- **编辑器手感**：emacs 键位 / kill-ring / undo 栈 / 词级导航与删除 / 键位自定义，连按键冲突诊断都和 pi 一致
- **富渲染**：流式 Markdown、表格、LaTeX 数学公式、Mermaid 图表、语法高亮，全在终端里
- **会话操作**：`/tree` 会话树导航、`/fork` 从任意历史消息分叉、`/resume` 会话搜索、`/compact` 手动压缩——全部是交互式选择器
- **鼠标支持**：滚动、选择、点击折叠工具输出（取决于终端能力，自动探测）
- **主题系统**：与 pi 格式互通的主题文件，light/dark 自定义

**对中文用户特别友好**（pig 相对 pi 的增强）：

- 编辑器**中文分词**：内置中文词典（gse），Ctrl+方向键按词跳转/删除，不再逐字挪动
- 修复了一整类 pi 上游的 CJK 渲染问题：中文表格对齐、CJK 相邻行内公式、中文标点斜体定界
- 内置**火山引擎 Coding Plan** provider，国内开箱即用

### 🌳 会话管理：一棵可分支回溯的树

pig 的会话记录天然支持**分支**：从任意历史节点重走、树形导航、分支间对比。配合自动压缩（上下文将满时自动总结裁剪，压缩边界保留）、自动重试（指数退避）、steering / follow-up 双队列，长会话不易失控。

### 🧪 工程质量：1300+ 个测试函数压阵

pig 的行为对齐有测试兜底：**17 万行 Go 源码 + 7.2 万行测试，1300+ 个测试函数，28 个包，`-race` 全绿**。pi 的大部分测试文件被 test-for-test 移植（含 @xterm/headless 终端仿真的 TUI golden 测试），每个修复都带回归测试锁定。崩溃韧性：任何 goroutine panic 落盘到 `~/.pig/agent/logs/` 并尽力恢复，不静默丢会话。

## 日常用法

### 交互模式

在项目目录直接启动，获得带流式响应的终端会话：

```bash
./pig
```

常用交互命令：

| 命令 | 作用 |
| --- | --- |
| `/model` | 选择模型 |
| `/thinking` | 设置 thinking level |
| `/settings` | 修改运行设置 |
| `/tree` | 浏览会话分支 |
| `/resume` | 恢复历史会话 |
| `/fork` | 从当前会话分支 |
| `/login` / `/logout` | 管理 provider 认证 |
| `/trust` | 管理项目级信任 |
| `/compact` | 压缩当前会话上下文 |
| `/new` / `/hotkeys` | 新会话 / 查看全部快捷键 |

按 `Ctrl+P` 可以在已配置的模型之间快速切换。

### 脚本与 CI

`-p`（print）模式处理完请求后退出，`--mode json` 输出机器可读事件流：

```bash
./pig -p "列出所有 Go 文件，并分别概括它们的职责"
./pig --mode json -p "找出仓库中所有 TODO 注释"
git diff | ./pig -p "审查这个 diff，指出可能的正确性问题"
./pig @AGENTS.md "按仓库约定总结这个项目的提交规范"
```

支持管道输入与 `@文件` 引用；会话参数如 `--continue` / `--resume` / `--no-session` / `--session-dir` 见 `./pig --help`。

## Provider：实现 ≠ 验证

同一家 provider，"移植完成"、"协议验证"、"真实服务验证"是三个可信度层级，pig 把它们分开记录：

- **已实现**：代码和本地单测已具备对应能力
- **协议级验证**：通过 DeepSeek 兼容端点或 mock 验证了请求、事件和解析逻辑
- **真实服务验证**：确实访问了目标 provider 的真实 API，并完成最小可用链路

| 状态 | 范围 | 说明 |
| --- | --- | --- |
| ✅ 真实端点已验证 | DeepSeek、火山方舟 Coding Plan、Google（部分） | 真实 API 流式对话；火山方舟含 thinking 场景；Google 为 gemini-3.x flash 基础链路 + thinking 关闭探针（部分模型受账号档位/地区限制，详见验证矩阵） |
| 🔶 兼容端点已验证 | Anthropic Messages、OpenAI Responses 系 | 经 DeepSeek 的 `/anthropic` 与 Responses 兼容端点验证了协议层；Anthropic / OpenAI 官方端点本身尚未实测 |
| 🧪 mock / 单测已验证 | 其余 provider、OAuth、provider 特有语义 | 无真实服务或真实登录链路验证 |

<details>
<summary>当前注册的 25 个内置 Provider</summary>

`ant-ling`、`anthropic`、`baseten`、`cerebras`、`deepseek`、`fireworks`、`google`、`google-vertex`、`groq`、`huggingface`、`kimi-coding`、`minimax`、`minimax-cn`、`moonshotai`、`moonshotai-cn`、`nvidia`、`openai`、`openai-codex`、`openrouter`、`together`、`vercel-ai-gateway`、`volcengine-plan`、`xai`、`zai`、`zai-coding-cn`

</details>

完整验证边界、待验证条件与现成 probe 见[真实服务验证矩阵](docs/handoffs/verify-matrix.md)。

## 认证

最简单的方式是设置 provider 对应的环境变量：

| Provider | 环境变量 | 当前验证 |
| --- | --- | --- |
| DeepSeek | `DEEPSEEK_API_KEY` | ✅ 真实端点 |
| 火山方舟 Coding Plan | `VOLCENGINE_API_KEY` | ✅ 真实端点 |
| Anthropic | `ANTHROPIC_API_KEY` | ⏳ 未实测 |
| OpenAI | `OPENAI_API_KEY` | ⏳ 未实测 |
| Google Gemini | `GEMINI_API_KEY` | ⏳ 未实测 |
| xAI | `XAI_API_KEY` | ⏳ 未实测 |
| OpenRouter | `OPENROUTER_API_KEY` | ⏳ 未实测 |

环境变量只是配置入口，各家能否放心用，以上表验证状态为准；完整变量列表见 `./pig --help`。认证状态检查：

```bash
./pig auth check --provider deepseek
./pig auth print-api-key --provider deepseek
```

交互模式里也可以随时 `/login` 补配或换 provider。

## 会话与配置

pig 默认与 pi 使用不同的配置目录：

```text
~/.pig/agent/
├── auth.json          # API key 与 OAuth 凭证
├── settings.json      # 全局设置
├── models.json        # 自定义 provider / 模型覆盖
├── sessions/          # JSONL 会话树
└── ...
```

项目级配置目录为当前项目下的 `.pig/`。关键环境变量：`PIG_CODING_AGENT_DIR`（覆盖配置根目录）、`PIG_CODING_AGENT_SESSION_DIR`（覆盖会话目录）。

常用会话参数：

```bash
./pig --continue "继续我们上次的工作"
./pig --name "重构认证模块"
./pig --no-session "本次不保存会话"
./pig --session-dir /path/to/sessions
```

### 从 pi 迁移数据

pig 与 pi 的配置目录相互独立，存量数据需要自己搬。推荐直接复制：

```bash
cp -a ~/.pi/agent ~/.pig/agent
```

只想临时试一下 pig 时，也可以不复制，直接把配置根目录指过去：`export PIG_CODING_AGENT_DIR=~/.pi/agent`。pig 与 pi 的会话 JSONL 在支持范围内双向兼容，但手动操作会话文件前建议先备份。

## 与 pi 的能力边界

| 能力 | pi | pig |
| --- | --- | --- |
| 交互 / print / JSON 三模式、7 种 API 协议、25 家 provider、模型 catalog | ✅ | ✅ 对齐 |
| 会话树 / 分支 / 压缩 / 导出 HTML | ✅ | ✅ |
| TUI（编辑器、补全、主题、鼠标、搜索）与 Markdown / LaTeX / Mermaid | ✅ | ✅ pi-tui 全量移植 |
| 中文分词与 CJK 渲染修复 | — | ✅ pig 增强 |
| RPC 模式 / sqlite 会话后端 | ✅ | ⏳ 按需排期 |
| 扩展系统（Node sidecar、包管理器） | ✅ | ⏳ 装配位已留，见 [EXTENSION-PLAN](docs/handoffs/EXTENSION-PLAN.md) |
| 终端图片检测、photon/WASM 图像处理、发行打包 | ✅ | ⏳ |

两个大块按计划押后：

- **harness runtime / 上半部分**：v0.85.1 的 `agent-harness` 门面与 events 重写、`hooks`、`execution` 正在快速迭代，等上游稳定再评估（已迁移的 session、JSONL、compaction、tools 不受影响）
- **pi 的 experimental 代码**（远程编排等实验性架构）：暂不跟进

## 项目结构

```
cmd/pig          CLI 装配入口
codingagent/     agent 运行时：会话管理、模型解析、设置、工具、TUI 交互模式
├─ core/         AgentSession、凭证、压缩、会话树、内置工具
├─ modes/        print / json / interactive 三种模式
agent/           agent 循环：steering/follow-up 队列、工具执行、生命周期
ai/              provider 层：7 种 API 协议适配器、模型 catalog、OAuth
tui/             pi-tui 移植：差分渲染器、编辑器引擎、组件库
```

## 开发

环境要求：Go 1.25+；跑真实请求时需要至少一个 provider 的凭证。

```bash
gofmt -w <改动文件>
go build ./cmd/pig
go vet ./...
go test ./... -timeout 120s                  # 单测（显式超时）
go test ./... -race -count=1 -timeout 600s   # 全量回归
```

约定：

- 全量 `-race` 回归**只跑一次**，需要通过包数或失败详情时复用同一次输出
- 连真实服务的测试（各 provider、OAuth 流程）需要真实凭证，默认不在普通测试中执行；验证策略见[真实服务验证矩阵](docs/handoffs/verify-matrix.md)
- 重要修复与迁移决策记录到 `docs/fix/`、`docs/feat/` 或 `docs/handoffs/`，每起 fix 必须附带回归测试

## 设计取向

| 取向 | 说明 |
| --- | --- |
| 行为级兼容 | 以 pi 的行为和线格式为规格，不追求逐行复制 |
| 单二进制 | 主运行时不依赖 Node，构建结果是 Go 静态二进制 |
| 配置隔离 | pig 使用 `.pig` / `~/.pig/agent`，不触碰 pi 的 `.pi` 数据 |
| 可验证交付 | 代码移植、mock、兼容端点、真实端点分层记录 |
| 保守跟进 | 上游快速迭代或实验性模块，等稳定后再迁移 |

## 相关文档

- [真实服务验证矩阵](docs/handoffs/verify-matrix.md)
- [harness 冻结决策](docs/handoffs/SYNC-S2-v0.85.1.md)
- [扩展系统设计与边界](docs/handoffs/EXTENSION-PLAN.md)
- [CJK 中文切词](docs/feat/CJK-WORD-SEGMENTATION.md)
- [LaTeX 渲染](docs/feat/LATEX.md)
- [火山方舟 Coding Plan](docs/feat/PROV-VOLC-PLAN.md)
- [panic 韧性](docs/feat/PANIC-RESILIENCE.md)

## 贡献

欢迎提交 bug 修复和聚焦的改进。涉及行为变化时：

1. 添加或更新回归测试（修复需先以 stash 方式验证测试确实能抓到原 bug）
2. 先运行相关包的窄范围测试
3. 交付前运行全量 `-race` 测试
4. 修复与决策记录到 `docs/fix/`、`docs/feat/` 或 `docs/handoffs/`

## License

本项目基于 [pi](https://github.com/earendil-works/pi)（MIT，© Mario Zechner）的行为规格重写，沿用 MIT License，上游版权声明见 [LICENSE](LICENSE)。
