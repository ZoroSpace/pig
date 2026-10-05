# S14 开工稿：pi v1.0.2 → v1.0.3 同步（预写于 S13 收口后、2026-10-05）

**状态**：**终稿（2026-10-05 收口）**。pi diff 范围 `v1.0.2..v1.0.3`，共 22 commit，实移 5 项 + 零操作 13 commit（durable 11 + codemode 3 + Nix/docs，见 §3），单批收口。

## 0. 终稿追加（开工稿 → 实际执行）

- **验收门**：build/vet/gofmt 干净；全量 `-race` 单次 rc=0 31 包（`/tmp/pig-test-out-s14.log`）；tmux 冒烟四项全通（复用 S13 环境 `/tmp/pig-s13-smoke`：header `v1.0.3`、`hello smoke`→`pong` ↑1↓1、ctrl+d 干净退出、jsonl usage input 1/output 1/cost.total 0，mock-requests.log 仅 1 条 → 零真实 API）。
- **Version** 1.0.2→1.0.3（config.go + config_test.go）。
- **models_generated.go 改走机械变换而非整体重生成**：生成器改完重跑后发现实时 models.dev 漂移（bedrock sonnet-5-5 EU/US、nvidia nemotron-3-super、openrouter classifier 等）**超出 pi v1.0.3 自身目录**（pi v1.0.3 的 models.generated.ts 仅 14 行改名、未重生成），整体重生成会让 pig 领先同步目标且 catalog golden 测试红。改为：恢复 HEAD 版 generated 文件 → `ProviderID("azure-openai-responses")`→`ProviderID("azure")` 机械替换（45 处，仅 provider 键、Api 字符串 `Api("azure-openai-responses")` 不动）→ 拼入生成器产出的 azure deepseek-v4-pro 条目（取自重生成结果，非手写）。此为「生成物禁手改」的一次记录在案的例外，理由：生成器输入（models.dev）已漂移过基线，唯一能精确对齐 pi v1.0.3 目录的方式就是该变换；下次自然重生成即收敛。
- **1.2 记录偏差**：pi 在 fn 内 disarm 锁等待 signal；pig `CredentialStore.Modify` 无法拆分两段取消，`RefreshStoredOAuthCredential` 改为 Modify 跑在 caller 取消不到的 `context.Background()` 上 + fn 入口重查 caller ctx（等锁不被唤醒、等锁期间 abort 则入口报错不刷新——可观测结果与 pi 一致：已开始的刷新必完成并落盘）。
- **1.3 实际落点**：pig `uncaughtCrash` 目前未接线（PANIC-RESILIENCE 偏差收容 panic）、stdin readLoop 读错误静默返回无 crash 路径——pi 的 stdin stream error handler 子项 pig 零操作；ENOTTY 补全（code map + errno 表）+ `uncaughtCrash` 死终端短路（restore+exit 内联，pig 无 emergencyTerminalExit 函数）落地。
- **1.4 文档面零操作**：pig 无 keybindings.md 用户文档；alt-screen 路由测试改名重写为 pi v1.0.3 语义（Home/End 归编辑器、Ctrl+Home/End 归 transcript）。
- **all_test.go** provider 计数 40→41、期望顺序插 `azure`。
- 新测试：`ai/providers/azure_test.go`（10 子测，pi azure-openai-completions.test.ts 移植）、`ai/api/azure_openai_config_test.go`（base URL 归一化 + deployment name 9 子测）、`ai/models_test.go` OAuth 持久化 2 个回归测试、`codingagent/modes/interactive/dead_terminal_test.go`（ENOTTY 识别）。

## 1. 实移项（按施工顺序）

### 1.1 Azure Foundry Chat Completions —— pi `a37306d43`（大头，含 pig 首次移植 azure provider）

上游把 provider `azure-openai-responses` 改名 `azure` 并新增 openai-completions 双派发。**pig 从未注册过 azure provider**（`ai/providers/all.go:129` 注释仍列为 remaining factory，仅 adapter/生成器散件存在），故直接按 v1.0.3 终态首移，不走「先移旧再改名」。上游声明的 auth.json/models.json 破坏性改名对 pig 零迁移负担（无存量用户），文档记一句即可。

**要读的 pi 源文件**：
- `packages/ai/src/api/azure-openai-config.ts`（新，107 行：resolveAzureBaseUrl / resolveDeploymentName / deployment name map helpers）
- `packages/ai/src/providers/azure.ts`（新，55 行：azureStreams wrapper + withDeploymentName onPayload）+ `azure.models.ts`
- `packages/ai/src/api/azure-openai-responses.ts`（-114 行：config 抽走后的余量）
- `packages/ai/src/providers/all.ts`、`packages/ai/src/types.ts`（KnownProvider 改名，**Api id "azure-openai-responses" 不变**）、`packages/ai/src/env-api-keys.ts`
- `packages/ai/scripts/generate-models.ts`（45 行改动）
- `packages/coding-agent/src/core/model-resolver.ts`（defaultModelPerProvider 键改名）

**pig 落点**：
- 新 `ai/api/azure_openai_config.go`：从 `ai/api/azure_openai_responses.go` 抽出 `resolveDeploymentName`（:136）、`parseDeploymentNameMap`（:113）、base URL 解析（:329 resolveAzureConfig）——两边对照，pig 已有实现是抽取而非新写
- 新 `ai/providers/azure.go`：provider id `azure`，api 表挂 `azure-openai-responses` + `openai-completions`（后者套 wrapper：把 endpoint 解析到 model 上——pig openai-completions 从 model.baseUrl 读，与 pi 同假设，开工核实）；注册进 `BuiltinProviders()`（all.go 按字母序 anthropic 之后）并清 stale 注释
- `ai/types/types.go`：KnownProvider 成员 `"azure-openai-responses"` → `"azure"`；`ApiAzureOpenAIResponses` 常量（:34）原样
- `ai/env_api_keys.go:109` 键改名；`codingagent/core/model_resolver.go` defaultModelPerProvider 键改名
- 生成器：`ai/scripts/generatemodels/`（metadata.go :359/:851/:1118/:1173、openai_models.go :155 clone 逻辑）对照 generate-models.ts 改后**重跑生成** `ai/models_generated.go`（禁手改）
- `ai/index.go` 补 types 新类型别名（编译器兜底）

**要移植的 pi 测试**：`packages/ai/test/azure-openai-completions.test.ts`（280 行，主力）→ `ai/providers/azure_test.go` 或 `ai/api/`；`azure-openai-base-url.test.ts`（22 行改动）并存量；`model-resolver.test.ts`（6 行）→ `codingagent/core/model_resolver_test.go`。

### 1.2 OAuth 轮换 token 取消后仍持久化 —— pi `bde882c74`

上游抽出 `refreshStoredOAuthCredential`（resolve.ts +81 行）：signal 只取消**等锁**；刷新一旦开始即不受 signal 取消（provider 可能已轮换 refresh token，弃掉即死），只受 15s timeout 约束，结果在锁内持久化后才释放锁。

**pig 落点**：`ai/auth/resolve.go:164` `resolveStoredOAuth`——现 `credentials.Modify(ctx, …)`（:179）同一 ctx 既管锁等待又派生 refreshCtx（:187）管刷新，需拆成 pi 的双信号语义（pig 惯例：等锁用调用方 ctx，刷新用独立 timeout ctx，锁内写盘后才返回）。pi 的 `packages/ai/src/models.ts` 同路径（model-list 刷新改走同一 helper，21 行）→ pig `ai/models.go` Refresh 面核实后接入。

**pi 测试**：`packages/ai/test/models-runtime.test.ts`（77 行改动）+ `model-runtime-auth-options.test.ts`（6 行）。

### 1.3 死终端 stdin 错误不计 crash —— pi `4c6b724ea`

**pig 落点**（已核实）：
- `codingagent/modes/interactive/interactive_mode.go:148` `deadTerminalErrorCodes` 加 `"ENOTTY"`；`codingagent/utils/node_errors.go` `errnoNames` 表加 `syscall.ENOTTY`
- `interactive_mode.go:3007` `uncaughtCrash`：入口加 `isDeadTerminalError(err)` → `emergencyTerminalExit()` 短路（pig 有完整对应物，含 crash 记录面 core.TakeUnnotifiedCrash/CrashLogPath）
- pi 给 stdin 挂 stream error handler 一节：pig 字节级 TUI 的 stdin 读路径不同，开工时确认 pig stdin 读错误是否已被 uncaughtCrash 收口覆盖，无漏点则该子项零操作并注释声明

**pi 测试**：`5080-signal-shutdown-extension-cleanup.test.ts`（+136 行）pig 侧找对应 signal/shutdown 测试落点。

### 1.4 全屏模式 Home/End 归还编辑器 —— pi `6100fe5a8`

**pig 落点**：`tui/keybindings.go:48` `cursorLineStart` 去 `"ctrl+home"`；`cursorLineEnd` 去 `"ctrl+end"`；`:97` `altScreen.top` `"home"`→`"ctrl+home"`、`altScreen.bottom` 同理。pi `keybindings.md` 8 行文档改动找 pig 对应文档；测试 `keybindings.test.ts` + `tui-alt-screen.test.ts`（30 行）→ `tui/` 对应测试。

### 1.5 Version 1.0.3 —— pi `d78dc83d6`

`codingagent/config.go` Version `"1.0.2"`→`"1.0.3"` + config_test.go 基线常量（stash 红→绿验证）。

## 2. 收口动作

照旧：AGENTS.md 批次索引一行 + 本文件回填终稿 + Version bump + 全量 `-race` 单次（`out=$(go test ./... -race -count=1 2>&1); rc=$?`）+ tmux 冒烟（env -i 清凭证 + mock provider，抽查 jsonl 零真实调用）。双向兼容：无 jsonl/wire 结构改动，v3/v4 门按声明沿用（本批唯一的「改 key」是 azure provider 名，pig 无存量用户，如实记入声明）。

## 3. 零操作声明（13 commit，后续批遇到不再重判）

- **durable 全族 11 个**：`a84510819`（FileSystem.watch）、`4748c627a`、`a19c09d9b`、`cdf79797b`、`674d64f09`、`be882f3fa`、`864777ba6`、`1543dd8f6`、`f5d20047b`、`acfc60198`、`2e63fcdfb`，及 `1965a8069` 的 durable 半边——不做清单整体延后
- **codemode 3 个**：`b2b5c42f6`（test-only）、`d677d0ee7`、`1b094148b`——codemode+tool_search 不做。`d677d0ee7` 的共享面同样不跟随（pig-only 偏差）：pig MCP 图片走内联 base64（`core/mcp_integration.go:520`）无 saveToTempFile；`core/bash_executor.go` 的 `pi-bash-` 临时文件已 0600；统一 output-file 助手无消费者
- `1965a8069` 的 coding-agent 半边 = bug-report-hint 测试 stub（/bug 已删，链路不存在）
- `0b7287ce7` Nix catalog pin（pig 无 nix/）；`2a12fa4e8`/`200387122` changelog 文档

## 4. 风险与坑

1. **1.1 是首移非同步**：pig 无 azure provider 存量，别照抄「改名 diff」——直接按 azure.ts 终态写；生成器 metadata.go 里 azure 分支是 pig 既有实现，对照 pi 改动后重跑生成，禁止手改 models_generated.go
2. **1.2 的 context 语义**：pig 侧切勿把「锁等待可取消」实现成「整个 Modify 可取消」——刷新体内的写盘必须在取消不可达的路径上完成，这正是本 fix 的存在理由；测试要覆盖「caller cancel 后 token 仍落盘」
3. **1.3 别顺手把 stdin handler 照搬**：Node stream error 事件在 pig 无对应物，找 pig 自己的 stdin 读错误出口（bytes 读取循环/terminal 包），先确认现状再动
4. ENOTTY 在 Linux/macOS 的 errno 值不同，`errnoNames` 表用 `syscall.ENOTTY` 常量即可，勿写数字

## 5. 明确不要读的

- `packages/durable/**` 全部（11 commit 零操作，勿翻实现）
- codemode 相关：`extensions/codemode/**`、`bash-executor.ts` 的 codemode 面、QuickJS/install-change 逻辑
- `scripts/build-coding-agent-bundle.mjs`（1b094148b 的 bundle 面，pig 无）
- pi 的 nix/ 目录
