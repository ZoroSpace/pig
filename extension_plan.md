# Phase 7 扩展系统设计方案（讨论结论，2026-09-08）

来源：围绕 T7 扩展系统与 sidecar 桥接设计的多轮讨论。基线 pi commit `4e58f324f`。
本文档是 T7 启动时的设计输入；与 AGENTS.md §3.2（扩展边界）配合阅读，与其冲突处以 AGENTS.md 为准。

---

## 0. 核心定性

- **T7 是整个项目从"移植"变成"设计"的转折点**：前六个阶段全部是"pi 源码 + pi 测试 = 机械验收"，sidecar 与桥接协议在 pi 中没有对应物（pi 的扩展是进程内 TS 加载），没有现成规格书与测试可搬，速度按移植批次的 1/3~1/5 预估。
- **扩展生态现实：pig 没有自己的扩展生态，直接消费 pi 的 TS 扩展**。一切设计以"现有 pi 生态包可加载运行"为验收标准，不存在面向 pig 的包。
- pig 侧消费面已就绪：Phase 4 第 5 批续二交付的 `extensions_runner.go` / `extensions_types.go` / `extensions_wrapper.go`（handler 分发循环、事件 struct、RegisteredTool、invocationName 去重链、bindCore）灌入 Extension 值即活；`provider_composer.go` 的 `applyExtension`/`composeAPIKeyAuth`/`adaptOAuth` 按 registerProvider 的形状预写。

---

## 1. Sidecar 架构（AGENTS.md §2/§3.2 已定，此处补实施要点）

```
pig (Go 主进程)
 └── spawn 子进程: node sidecar.js <extension>
      └── sidecar 内部加载真 pi 运行时 + 目标扩展
      ↕  stdio 行分隔 JSON-RPC 2.0
```

- **每个扩展一个 sidecar 子进程**；扩展以为自己在 pi 里，所有 pi.* API 调用被 sidecar 翻译成 JSON-RPC。
- **红线：sidecar 必须给扩展完整 Node 环境**（fs/net/child_process 不设限）。MCP server、OAuth 回调服务器、LSP、子 agent 进程全是扩展自己 spawn 的，全部发生在 sidecar 内部，不过桥。桥上只走 pi API 面，**不**试图沙箱化扩展的自主行为。
- protocol 包的 framing/schemas（1.2k 行）可直接复用于消息层。
- 生命周期：`initialize`（manifest 协商）→ 调用 → `shutdown`；UI 类 API 在协议层显式 no-op（能力集可查询，降级而非崩溃）。

---

## 2. 扩展 API 分类与桥接模式

### 2.1 ExtensionAPI 的七类消费面

| # | 类别 | API 面 | pig 兼容性 |
|---|---|---|---|
| 1 | 事件订阅 | `pi.on(...)` ~35 种事件 | ✅ 订阅制尽力送达 |
| 2 | 自定义工具 | `registerTool` / `unregisterTool`（动态） | ✅ 完整支持 |
| 3 | 命令/快捷键/flag | `registerCommand` / `registerShortcut` / `registerFlag` / `getFlag` | ✅ 完整支持 |
| 4 | 会话/工具操控 | `setActiveTools`、`sendMessage`/`sendUserMessage`（steer/followUp）、`appendEntry`、`exec`、model/thinking 切换、`setSessionName`/`setLabel` | ✅ 反向调用桥接 |
| 5 | Provider 注册 | `registerProvider`（声明式 config + 自定义 streamSimple + OAuth 三回调） | ✅ 可桥接 |
| 6 | 渲染定制 | `registerMessageRenderer` / `registerEntryRenderer` / `registerMarkdownTransformer`、tool definition 的 renderCall/renderResult | ⚠️ 部分：数据完整落盘（custom entry 走 jsonl），显示降级为纯文本；markdown transformer no-op |
| 7 | UI 交互 | `ctx.ui.select/input/setStatus/setWidget/setEditorText/setEditorComponent/overlay/custom` | ❌ 明确放弃（能力集声明 + 优雅降级） |

文件形态与装载来源：单文件 .ts / 带 package.json 的目录包（可带 npm 依赖）/ 附带 prompts/、agents/、SKILL.md 资源；CLI flag、`~/.pi/agent/extensions/`、项目目录、包管理器安装。路径解析半区 pig 已落（`package_manager_paths.go`），npm 安装机器（package-manager-cli.ts 1102 行）随 T7。

### 2.2 五种桥接模式

| 模式 | 走向 | 时机 | 覆盖类别 |
|---|---|---|---|
| 声明 | sidecar → pig（一次性） | initialize 握手 | 2/3/5 的注册面（**动态注册除外，见 §3**） |
| 推送 | pig → sidecar notification | 事件发生时 | 1（无结果事件） |
| 往返 A | pig → sidecar request | LLM 调工具 / 结果型事件（tool_call 短路、session_before_* 覆盖、message_end 替换、input 改写） | 1（结果型）+ 2 |
| 往返 B | sidecar → pig request | handler 执行中反向操控宿主 | 3/4 |
| 事件流 | pig → sidecar 请求 + 流式回传 | 每次 LLM 请求 | 5（自定义 streamSimple） |

关键语义点：

- **带结果事件是 request**：pig 必须等响应才能继续（返回值决定 continue/覆盖）。流式路径上的 `message_update` 绝不能做成 request。
- **往返 B 的重入**：反向调用发生在 pig 正等 command_invoke 响应期间，pig 的 RPC 层必须异步 dispatch（能处理"等响应时新进请求"），不能朴素一问一答。
- **自定义 streamSimple 桥接**：pig 发完整 llm context 给 sidecar，扩展流函数的 `AssistantMessageEvent` 逐个 notification 回传。pig 的事件协议当初按"单 struct + Type 判别字段"设计，线格式天然友好。OAuth 三回调（login/refreshToken/getApiKey）为三条独立 RPC，用户交互发生在 sidecar 内，pig 只等最终凭证（ctx 取消语义对齐）。
- pig 侧落点：`sendMessage`/`sendUserMessage` → AgentSession 既有队列；`appendEntry` → jsonl custom entry（线格式已有）；`setActiveTools` → refreshToolRegistry 过滤；ctx 取消 → cancel notification。

---

## 3. 生态实证：pi-mcp-adapter 与 Top 10

### 3.1 pi-mcp-adapter（下载量第一）实测消费面

拆包分析（v2.32.1）：`pi.exec` ×14（spawn MCP server/OAuth 回调服务器）、`registerTool` ×5（**运行时动态注册**）、`unregisterTool` ×2（半公开 API）、`setActiveTools` ×4（deferred loading）、事件 4 种、命令/flag ×7、`sendUserMessage`/`sendMessage` ×2、**`ctx.ui.notify` ×31、`ctx.ui.custom` ×10（交互面板）**。

结论：**核心功能链路（server 管理 → 工具暴露 → OAuth）100% 落在已规划桥接模式内**；丢失的是 notify 提示与交互面板，且面板功能有 `/mcp` 命令行 fallback。

### 3.2 Top 10 兼容性预判

| 扩展 | 预判 | 说明 |
|---|---|---|
| pi-mcp-adapter | ✅ 核心全支持 | UI 面板/notify 降级 |
| pi-web-access | ✅ | 搜索/fetch 在 sidecar 内跑 |
| pi-subagents | ✅ | 子 agent 为 exec 起子 pi 进程 |
| @companion-ai/feynman | ✅ | package（prompts/skills 资源），可能不过桥 |
| @juicesharp/rpiv-ask-user-question | ❌ 核心即 UI | questionnaire/select 是产品本体 |
| pi-background-tasks | ✅ | 后台 shell/子 pi 在 sidecar 内 |
| @juicesharp/rpiv-todo | ⚠️ 半支持 | custom entry 持久化 ✅；live overlay ❌ |
| context-mode | ✅ | 同 adapter 模式 |
| billion-context | ✅ 无需扩展 | models.json baseUrl 原生支持 |
| pi-lens | ✅ | LSP 进程在 sidecar 内，status UI 降级 |

分布：7 个核心完整、2 个降级可用、1 个放弃——验证 §3.2 兼容矩阵的预判。

### 3.3 对协议设计的三条硬要求（由 pi-mcp-adapter 逼出）

1. **registerTool 必须是运行时一等 RPC，不是握手期一次性声明**——工具随 MCP server 连接动态注册/摘除（含 unregisterTool）。
2. **sidecar 给完整 Node 环境，不沙箱**（见 §1 红线）。
3. **UI 能力集分级协商**：`ctx.ui.notify` 桥接为轻量文本通知（成本极低、对生态体验挽回大），`ctx.ui.custom`/面板才 no-op。能力集形如 `notify: "text" | "none"`、`custom: boolean`。

---

## 4. 兼容性判定与安装门禁

前提约束：**没有作者自声明**——pig 无自有生态，pi 作者不会为 pig 加 manifest 字段。判定全部由 pig 侧产生。

### 4.1 判定分级

| 判定 | 依据 | 处理 |
|---|---|---|
| ✅ 兼容 | 未触碰 UI 面 | 正常安装 |
| ⚠️ 降级可用 | 用了 `ui.notify` / 渲染器（有文本 fallback） | 允许安装，打印降级清单 |
| ❌ 不兼容 | UI 是无 fallback 的核心主路径 | **拒装**，exit 非 0，逐条列理由；`--force` 覆盖 |

**反例教训**：pi-mcp-adapter 用了 10 处 `ctx.ui.custom` 但核心完全可用——一刀切拒装"碰到 UI API 的包"会把 top1 拒之门外。必须分级 + `--force` 出口。

### 4.2 四层判定机制（无作者声明版）

1. **兼容性数据库（主机制）**：pig 项目维护，以"包名 + 版本范围"为键随 pig 发布。下载榜是现成工作清单，top 包手工验证一次录入结论。
2. **探针农场（让数据库自动生长）**：CI 定时任务——拉取下载榜 Top N → npm install 沙箱 → sidecar 插桩模式空跑（工厂 + session_start + 命令枚举）→ 产出 API 消费面报告 → 复核入库。插桩方式：包一层 `ExtensionAPI` 代理记录真实调用，比静态扫描准。局限：只覆盖加载期 + 事件触发点可达的路径。
3. **静态扫描（长尾兜底，低置信度）**：特征签名扫描（`ctx.ui.custom`、`setWidget`、`EditorComponent` 等）。命中 UI 面默认**警告不拒装**，仅"UI 为无 fallback 主路径"的强特征才拒绝。注意 dist 产物打包压缩可能使 TS 特征失效。
4. **运行期能力协商（最终事实 + 反馈回路）**：no-op 调用走 `ExtensionError` 上报（listener 已就位）；真实使用中观测到的探针盲区 UI 调用记录下来回报数据库。

可选反向动作：向 pi 上游提案 manifest 可选能力声明（pi 自身也受益）。成则白捡数据源，不成无妨。

### 4.3 交互形态

```
$ pig install npm:rpiv-ask-user-question
✗ Incompatible with pig (source: compatibility database, v2.3)
    Requires interactive TUI components (questionnaire panel) —
    this is the package's core feature, no text fallback.
    Rejected. (--force to override)

$ pig install npm:some-unknown-pkg
⚠️  Unknown package — runtime probe found UI usage (ctx.ui.notify x3)
    Core functions should work; status notifications will appear as text.
    Installing. Report issues with: pig compat report some-unknown-pkg
```

- 判定结果（含依据）写进包管理器本地数据库，`pig list`/更新携带标记；`--force` 装入的包每次加载仍提示。
- `pig compat report <pkg>` 子命令：用户一句反馈 = 数据库一条候选记录，生态适配滚雪球。
- 实现落点：全部在 T7 package-manager 批（npm 安装机器 + cli）；**建议"兼容性判定"单列一个子批次**，不阻塞五类桥接模式，但决定用户对扩展生态的第一印象。

---

## 5. T7 批次拆分建议

按依赖序与风险隔离：

1. **T7a 协议与 sidecar 骨架**：消息 schema（五种桥接模式）+ initialize 握手 + 生命周期 + pig 端异步 dispatch RPC 层；conformance 用例手写
2. **T7b 桥接主链路**：事件推送（notification/request 双形态）+ registerTool/unregisterTool 动态注册 + 工具执行往返（含 onUpdate 流式）
3. **T7c 反向调用 + 命令**：往返 B（sendMessage/setActiveTools/exec/…）+ registerCommand/registerFlag + 重入处理
4. **T7d Provider 桥接**：声明式 provider（复用 provider_composer）+ 自定义 streamSimple 事件流 + OAuth 三回调
5. **T7e 包管理器**：npm 安装机器（package-manager-cli 1102 行移植）+ pi-manifest 扩展入口发现 + **兼容性判定子批（§4）**
6. **验收标杆**：sidecar 跑通 **pi-mcp-adapter + pi-web-access + pi-subagents** 三个真实包，即实测覆盖"工具/命令/事件/反向调用/动态注册/子进程"六条链路——比手写 conformance 更有说服力

进度策略（2026-09-08 讨论定调）：若 v1.0 冲刺期 T7 未收口，可按"无扩展模式"先发布（Go 原生接口组织已就绪，sidecar 延后 v1.1），不让它拖住发布。

**批次顺序定调（2026-09-08 用户决策）**：`T4 → T5（M4）→ T6 → v1.0 → baseline 同步批 → T7`。即 v1.0 前不做基线同步——T4-T6 从 pin 基线 `4e58f324f`（v0.84.3）移植，pi 上游增量留到 v1.0 后一次性消化。含义与代价：

- 同步批届时须覆盖 **core 漂移 + TUI 漂移**两块（T4-T6 移植自旧基线，pi-tui 上游变更届时补 delta），全仓 808 个测试函数是回归安全网；同步前 diff 重点盯 `packages/tui`、`core/extensions/types.ts`（T7 协议输入）、会话线格式字段
- 线格式风险窗口：若 pi 在 08-24 → v1.0 之间改了 JSONL schema，pig 会按旧格式落盘。同步批时逐条核对，必要时做迁移
- **对 T7 影响有限**：sidecar 内跑的是真 pi 运行时，扩展 facing 语义可由 sidecar 捆绑的 pi 运行时独立跟进；pig 必须同步的是 TUI 手感、线格式、AgentSession 语义
- pi 仓库只读参照，不同步前不动其分支（需要对比时用 `git fetch` + `git diff 4e58f324f..origin/main` 或独立 worktree，不改工作区）

---

## 6. 明确放弃项（与 AGENTS.md §1 对齐，运维提示语备好）

- 有 UI 的 TS 扩展（widgets、编辑器组件、主题运行时切换、overlay 面板）
- `registerMarkdownTransformer`（流式路径逐块 RPC 延迟不可接受）
- 工具自定义渲染（renderCall/renderResult）——pig 用默认渲染
- npm 库消费者（pig 不是 TS 包）
