# SYNC-S16 开工稿（预消化）：pi v1.0.4 → origin/main@27c7b6ff4

日期：2026-10-07 · 状态：待拍板开工 · 范围拍板：用户已定「对齐 pi main 最新 commit」
上游：pi 无 v1.0.5 tag（最新 tag 仍 v1.0.4），main HEAD = `27c7b6ff4`，v1.0.4..main 共 43 commit（+6062/−1316，179 文件）。
**基线机制处理**：S16 pin `origin/main@27c7b6ff4`；`Version` 与 `TestVersionTracksPiBaseline` 保持 1.0.4 不动（守卫语义 = 对齐最新正式 tag，未被破坏）；AGENTS.md 现状行改为「pig 基线现跟踪 pi v1.0.4 + main@27c7b6ff4（S16 提前消化）」。v1.0.5 tag 发布后 S17 只补 tag 之后增量并恢复 pin-tag 语义。

## 0. 零操作清单（19，收批时逐条声明）

| commit | 理由 |
|---|---|
| 68ccef176 / b0114ef5f / 76f6c06da / 636703a0a / 4dd2af42c / da866ada1 / ae92585d3 / 4c28a6865 | durable 全族，不做清单（S15 §0 沿用） |
| eb326d265 / 269121616 | codemode，不做清单 |
| ce8972a0e 的 codemode 消费面（models.classify images、docs/codemode.md） | 同上，ai 侧部分实移见 S16-1 |
| 311f0e020 的 durable/src/harness/context.ts 部分 | durable |
| 23cf2b948 / 9f013cf59 / 1ffb6bd62 / ea6fa125a / 428a12bc7 / 28dcce2ba | npm 发行/CI/nix/CHANGELOG/chore 基建，不迁 |
| 56b25ff4e / adae82464 | pi 文档，pig README 同步义务另算 |
| 43d376399 | pig 无 radius provider（grep 证实，仅 oauth 字符串残留在 model_config），零操作 |
| fe11328b0 | pig 无 faux provider（仅测试脚手架，无 prompt-cache 面），零操作 |
| 7fb59f995 | mistral-conversations adapter 延后（不做清单），retry 模式在 adapter 文件内 |
| b30a6dd77 | pig image resize 已 Go 原生（codingagent/utils/image_process.go），无 Node worker 概念 |
| 592fb57b7 | pig clipboard.go 按 TERMUX_VERSION env 检测（:90/221/254/270），无 platform() 门，天然覆盖；开工时声明核对即可 |
| 3ba22ce17 | TS 类型体操（private fields 破坏可赋值性回退 WeakMap），Go 具体类型无此问题，净效果并入 f284a2460 |

## 1. 批次划分与实移清单（20）

### S16-1 ai 区（~3000 pig 行，openai-decisions 是大头）

前置基建（先行，S16-2 依赖）：
- **f284a2460** 响应计时：ai/event_stream.go 统一 stamp `AssistantMessage.durationMs`（monotonic）；agent/proxy.go 与测试 mock 换用统一构造（pi: createAssistantMessageEventStream）；pig EventStream 是具体泛型 struct，private fields/WeakMap 问题不存在，注释声明。ai/types 补 `DurationMs *int64`（camelCase 线格式）。

实移：
1. **8b5708dbb** retry 补 `server_busy` / `servers are currently busy` → ai/utils/retry.go:51 可重试列表 + 测试 2 例
2. **27075fe07** charsPerToken 4→3.5 → ai/utils/estimate.go:26 + context estimate 测试；**311f0e020** 输出上限断言随之一批改齐（防中间红）
3. **2989eb581** bedrock converse OpenAI 模型 reasoning effort → ai/api/bedrock_converse_stream.go `buildAdditionalModelRequestFields`（pig :1520 现仅 Claude 分支）：model match candidates 含 gpt-oss → flat `reasoning_effort` clamp low/medium/high（xhigh/max→high）；gpt- → 嵌套 `reasoning.effort`（minimal→low，model.thinkingLevelMap 优先）。bedrock_thinking_payload_test.go 补 gpt-oss/gpt-5.x/gpt-6 例
4. **9ad083102** LoginOptions 补 `agentName`（默认 pi→pig 文案 Pig 化）→ ai/auth types + oauth_openai_chatgpt.go（agent_name_hint）+ oauth_openai_codex.go（originator）；两 oauth 测试补例
5. **8d8ae2fc2** anthropic 回调 free-port 回退 → ai/auth/oauth_anthropic.go:37-39（现固定 53692）：先试 53692，bind 失败回退 OS 空闲端口；authorize URL/paste 提示/token exchange 用实际绑定 redirect URI；注意 pi 的 `redirectHost: "localhost"` 参数（bind 127.0.0.1 与 URL host 分离），pig 回调 server 若无此参数一并补。**兼修 pig worktree 固定端口假红**（docs/fix/2026-09-12），改造时保持该 fix 的 Close/Serve 竞速语义
6. **6b5854454 + ce8972a0e** OpenAI Decisions classifier（openai provider 的 `gpt-6-luna`）：
   - classifier-shared 抽取（pi 从 system-one-shared 拆出）——**开工先核 pig classifier HTTP 共享层现状**（typesafe/cloudflare_workers_ai/openrouter 三家如何共享），按对应形状重构
   - 新 ai/api/openai_decisions.go（TS 197 行）+ lazy 变体；bool→predicate（true/false 语义追加进 instructions）、choice/score 直映、refusal 整体 error、usage 仅 input token 带 long-context multiplier
   - ClassifierContext 补 images（ai/types + 各 classifier API 发送或 error result）
   - providers/openai.go 注册 + OAuth 态从 classifier availability 隐藏
   - 生成器改动（generate-models.ts 22 行）→ pig 生成器 + models_generated.go 重生成
   - provider-retry：decisions 的 504 不重试
   - 测试：openai_decisions（TS 291 行）+ classifier_models + provider-retry 补例
   - 不做：codemode 消费面；llama-cpp-classify 相关（pig 无此文件，声明）

### S16-2 agent + codingagent 区（~2000 行）

7. **36a686ee8** durationMs 全链（#10549，本批唯一行格式变化）：
   - agent/agent_loop.go execute() 前后 stamp `ToolResultMessage.durationMs`；agent/types 补字段
   - codingagent `tool_execution_end` 事件 payload 加 durationMs（absent=工具未运行）
   - bash renderer "Took" 显示（reload 后仍显示——pig renderers_bash.go 形状核对）+ tool-execution 组件 + nested_tool_calls + exporthtml/tool_renderer + 回调 API（pi core/extensions/types.ts 7 行——pig 对应 core 回调面核对）
   - json.md 变化 → README/session 文档同步；**双向兼容门**：增量字段，旧会话缺失=零值不写出
   - 测试：agent-loop + tool-execution 组件补例
8. **ddaa0a034** `--tools +name/-name` → codingagent/core/tool_name_matcher.go 扩展：**纯** +name/-name 列表=按 defaultTools 增减（defaultToolModifiers，/reload 后 -name 保持移除）；混合列表/修饰符含模式 → getToolListError 校验拒绝（+/- 只认精确名，与 S15 的 `*` 通配正交）。落点：cli/args（诊断 error）、settings_manager（applyToolModifiers + defaultTools 文档）、agent_session、sdk.go；args/default-tools/matcher 测试补例
9. **2db5e359b** MCP manager responsive（#10562）——pig 偏差面核对+择优回填：pi 改 extensions/mcp/index.ts（pig 无此层）。核对三点：① 工具 getClient 执行时重查当前连接（禁用/替换后报错，不用陈旧 client）② reconnect/replace 串行化 + closing 等待 ③ /mcp 面板启动不等连接完成。integrations/mcp/manager.go 已有 connectMu/connectCancel/connectLocked（S15），预计大半覆盖；缺口回填 core/mcp_integration.go，剩余留缺口注释

### S16-3 TUI + interactive 区（~800 行）

10. **27c7b6ff4** bash ANSI 跨 chunk strip（#10504）——**pig 确认带同一 bug**：bash_executor.go:129 现为逐 chunk `StripAnsi(decoder.chunk(data))`。utils 补 `splitIncompleteAnsiSuffix`，bash_executor 加 pendingAnsi 缓冲 + flush；bash 持久化测试补跨 chunk 例
11. **83c9e2645** transcript 重建清 fullscreen 选区 → tui/tui_alt_screen.go（选区字段 :93/:101）+ interactive_mode.go
12. **b2363841a** Hyperlinks 终端检测补 Herdr → tui/terminal_image.go + 测试
13. **18336987a** outputPad 铺到全部 transcript 块 —— **⚠ 拍板项**：pig outputPad 现仅 assistant_message.go 消费；pi 铺到 bash/tool/custom/compaction/branch-summary/skill-invocation/settings-selector + tui Box/Text `setPaddingX`。pig BLOCK-BORDERS 整框化偏差（块=上下边框无填充）语义不同。建议：内容型组件（bash/tool 输出 body）对齐 pi 应用，边框壳跳过，tui Box/Text 补 setPaddingX；**拍板前不动**

## 2. 收尾动作

- Version 1.0.4 不动 + 守卫不动；AGENTS.md：现状行基线声明（见头部）+ 批次索引 S16 行；不做清单无新增
- 双向兼容门：durationMs 增量字段 v3 四向实测或声明核对
- README 同步：`--tools +name/-name`、outputPad 语义、json.md durationMs；verify-matrix 无新解锁面

## 3. 风险与坑

1. **oauth_anthropic free-port 改造**动回调 server 生命周期——2026-09-12 listener leak fix（Close 与 Serve 竞速）语义必须保持；固定端口测试（codex 1455 / anthropic 53692）同步适配，worktree 撞端口错峰规则可注记缓解
2. **classifier-shared 重构**先核 pig 现状再动手，三家 classifier provider 共享层形状可能已偏离 pi，重构错位会放大 diff
3. **estimate 3.5** 波及所有依赖 token 估算的断言（context estimate + 输出上限），一批内改齐
4. **durationMs** 是唯一线格式变化，写断言时注意旧会话回放字段缺失路径
5. **--tools** 三种语法共存（精确/`*`/`+/-`）：先落 getToolListError 校验再扩 matcher，防 S15 通配语义被回归
6. 18336987a 在拍板前不得触碰 BLOCK-BORDERS 渲染面

## 4. 明确不要读的

packages/durable/**、packages/codemode/**、packages/env/**、scripts/（npm 发行面）、.github/、nix/、packages/coding-agent/src/extensions/**（llama/codemode）、pi CHANGELOG 正文。
