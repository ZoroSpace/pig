# RPC mode 计划稿（pig，2026-10-17 起草）

> 状态：**R1/R2/R3 全部完成**（2026-10-17）：协议内核 + 全命令 dispatch + main 接线 + 测试（jsonl/prompt/#5868/状态/会话/bash/export/fork）+ README + 冒烟 + 全量 `-race` 全绿。本文件即 RPC mode handoff。
> 上游基线：pi v1.3.0（`~/Projects/pi` commit 见 AGENTS.md）。
> 触发：用户 2026-10-17 拍板「先写 md，分批，然后开始」。

## 0. 背景与决策（先读）

1. **术语澄清**：pi 的「RPC」有两层，别混：
   - **`--mode rpc`**（`packages/coding-agent/src/modes/rpc/`，本批目标）：headless、**JSON over stdin/stdout** 的本机进程内嵌协议。**仍存在**。
   - **experimental client/server 栈**（`packages/{server,client,protocol,session-backends}`、`src/experimental/`、`src/client/`、coordinator/durable）：unix socket + CBOR 的服务端化路线。**已被 pi 删除**（`7472cda0d`，2026-10-12，早于 v1.3.0）——AGENTS.md 说的「Phase 5 不做」指的是这一层，它现在**上游也已不存在**，没有移植对象。
2. **扩展 UI 半区不做**：用户 2026-10-17 明确「扩展不会做；pig 的扩展和 pi 的扩展不通」。rpc-mode 的 `extension_ui_request` / `extension_ui_response` 通道是给 pi 扩展用的，pig **永久无消费方** → 类型可留/声明跳过，**不实现 UI context 的协议编解码**（`BindExtensions` 传 `UIContext: nil` 安全，见 core/agent_session.go:4458）。`get_commands` 的 `source:"extension"` 分支恒空。
3. **命名**：这是 independent feat 批，不是 Phase 5、不是 S 系基线同步批。commit scope 用 `rpc`。
4. **`rpc-client.ts`（617 行）不迁**：它是给外部 TS 消费者的客户端库（spawn CLI + 读写管道），pig 无消费方；只在文档声明。

## 1. pi 源文件（精确路径 + 行数）

| 文件 | 行数 | 去向 |
|---|---|---|
| `packages/coding-agent/src/modes/rpc/rpc-types.ts` | 303 | `codingagent/modes/rpc/rpc_types.go` |
| `packages/coding-agent/src/modes/rpc/jsonl.ts` | 58 | `codingagent/modes/rpc/jsonl.go` |
| `packages/coding-agent/src/modes/rpc/rpc-mode.ts` | 819 | `codingagent/modes/rpc/rpc_mode.go` |
| `packages/coding-agent/src/modes/rpc/rpc-client.ts` | 617 | **跳过**（无消费者） |

测试：

| 文件 | 行数 | 处置 |
|---|---|---|
| `test/rpc-jsonl.test.ts` | 65 | **直移**（纯单测） |
| `test/rpc-prompt-response-semantics.test.ts` | 452 | 移非扩展子集（prompt preflight 语义）；扩展命令/输入拦截用例跳过 |
| `test/suite/regressions/5868-rpc-unknown-command-id.test.ts` | 113 | **移**（未知命令保留 id） |
| `test/suite/regressions/7150-rpc-prompt-during-compaction.test.ts` | 92 | 实为 core 语义测试（不调 runRpcMode）→ 落 core 侧或复用 |
| `test/rpc.test.ts` | 399 | **冒烟级**（spawn CLI + 真 API，`describe.skipIf(!ANTHROPIC_API_KEY)`）：不进门禁，转 `//go:build smoke` 或删 |
| `test/rpc-client-{clear-queue,clone,process-exit}.test.ts` | — | 客户端测试，跳过 |

## 2. pig 现有基建（先核实过，全部在位）

- `codingagent/modes/rpc/doc.go` —— 占位包（本批实体化）。
- `codingagent/modes/print_mode.go` —— **模式模板**：`PrintRuntimeHost` 接口、`rebindSession`、`BindExtensions`、信号处理、`core.WriteRawStdout` / `WaitForRawStdoutBackpressure` / `FlushRawStdout`、`SetRebindSession`。
- `codingagent/modes/json_event.go` —— `ToJsonEvent` + `MarshalJsonEvent`（RPC 事件流复用）。
- `codingagent/core/output_guard.go` —— `TakeOverStdout` 等全族。
- `codingagent/core/agent_session.go` —— 命令落点 API 见表 §3。
- `cmd/pig/main.go:1119` —— 现直接报 "RPC mode is not yet available"；`:103` appMode 解析已认 rpc；`:921` `@file` 在 rpc 下报错；`:1059` rpc 不读管道 stdin。

## 3. 命令 → pig API 映射表（实现时照此，避免重探索）

| rpc 命令 | pig 落点 |
|---|---|
| `prompt` | `session.Prompt(ctx, msg, &PromptOptions{Images, StreamingBehavior, Source: InputSourceRPC, PreflightResult})`（preflight 回调已存在，agent_session.go:412） |
| `steer` / `follow_up` | `session.Steer/FollowUp(ctx, msg, images, &QueueUserInputOptions{Source: InputSourceRPC})` |
| `abort` | `session.Abort(ctx)` |
| `clear_queue` | `session.ClearQueue()` |
| `new_session` | `host.NewSession(ctx, &RuntimeNewSessionOptions{ParentSession})` → 不取消则 `rebindSession()` |
| `get_state` | `Model()` / `ThinkingLevel()` / `IsStreaming()` / `IsCompacting()` / `SteeringMode()`(agent.QueueMode→string) / `FollowUpMode()` / `SessionFile()` / `SessionID()` / `SessionName() (string,bool)` / `AutoCompactionEnabled()` / `State().Messages` / `PendingMessageCount()` |
| `set_model` | 从 `GetModelRuntime().GetAvailableSnapshot()` 按 provider+id 找 → `SetModel(ctx, &model, opts)` |
| `cycle_model` | `CycleModel(ctx, direction, opts)`（nil = 无可循环 → null） |
| `get_available_models` | `GetModelRuntime().GetAvailableSnapshot()` |
| `set_thinking_level` | `SetThinkingLevel(level, opts)` |
| `cycle_thinking_level` | `CycleThinkingLevel(opts)`（"" → null） |
| `get_available_thinking_levels` | `GetAvailableThinkingLevels()` |
| `set_steering_mode` / `set_follow_up_mode` | `SetSteeringMode` / `SetFollowUpMode` |
| `compact` | `Compact(ctx, customInstructions)` |
| `set_auto_compaction` | `SetAutoCompactionEnabled(bool)` |
| `set_auto_retry` / `abort_retry` | `SetAutoRetryEnabled` / `AbortRetry` |
| `bash` / `abort_bash` | `GetExtensionRunner().EmitUserBash`（命中则 `RecordBashResult`）→ `ExecuteBash(ctx, cmd, nil, &BashExecuteOptions{ExcludeFromContext, ID})` / `AbortBash()` |
| `get_session_stats` | `GetSessionStats()` |
| `export_html` | `ExportToHtml(outputPath, themeName)`（pi 只传 outputPath；pig 需 themeName，取 settings theme 或 ""） |
| `switch_session` | `host.SwitchSession(ctx, path, opts)` → rebind |
| `fork` / `clone` | `host.Fork(ctx, entryID, &ForkOptions{Position})` → rebind；clone 用 `SessionManager.GetLeafID()` + `Position:"at"` |
| `get_fork_messages` | `GetUserMessagesForForking()` |
| `get_entries` | `SessionManager.GetEntries()` + `GetLeafID()`，`since` 游标切片 |
| `get_tree` | `SessionManager.GetTree()` + `GetLeafID()` |
| `get_last_assistant_text` | `GetLastAssistantText()` |
| `set_session_name` | `SetSessionName(trim)`，空报错 |
| `get_messages` | `State().Messages` |
| `get_commands` | `GetExtensionRunner().GetRegisteredCommands()`（恒空）+ `PromptTemplates()` + `GetResourceLoader().GetSkills()` |

线格式硬约束：命令/响应/事件 JSON 字段名与 pi 逐字一致（camelCase，`type`/`id`/`command`/`success`/`data`/`error`）。

## 4. 批次拆分

### R1 —— 协议内核（✅ 完成 2026-10-17）
- 新建 `codingagent/modes/rpc/jsonl.go`（`SerializeJsonLine` + `AttachJsonlLineReader`，LF-only，CRLF 容忍）。**偏差**：Go `encoding/json` 无条件转义 U+2028/U+2029，pi 的 `JSON.stringify` 不转义 → `SetEscapeHTML(false)` + `unescapeLineSeparators` 还原（已测：真分隔符还原、字面 `\\u2028` 不被误改）。
- 新建 `codingagent/modes/rpc/rpc_types.go`（全部 union 类型：命令/响应/状态/扩展 UI 类型声明；单 struct + `Type` 判别 + 标准 struct 解码分发）。
- 新建 `codingagent/modes/rpc/rpc_mode.go`：`RpcRuntimeHost` 接口、`RunRpcMode`（默认 stdin/stdout，`TakeOverStdout`）+ 内部可注入 IO 的 `runRpcMode`（测试用，声明偏差）、output/success/error helper、`rebindSession`、信号处理、JSONL 输入循环、shutdown/EOF；**全部命令 dispatch 已实现**（prompt/steer/follow_up/abort/clear_queue/new_session/get_state/set_model/cycle_model/get_available_models/set_thinking_level/cycle_thinking_level/get_available_thinking_levels/set_steering_mode/set_follow_up_mode/compact/set_auto_compaction/set_auto_retry/abort_retry/bash/abort_bash/get_session_stats/export_html/switch_session/fork/clone/get_fork_messages/get_entries/get_tree/get_last_assistant_text/set_session_name/get_messages/get_commands）。**偏差**：每行命令起独立 goroutine（pi `void handleInputLine`）；`handleCommand` 外包 recover 复刻 pi 的 try/catch 错误响应（panic-resilience）。
- 接线 `cmd/pig/main.go` case "rpc" → 非离线时后台 15s 超时刷新 model catalog（pi main.ts:940-947）+ `core.PrintTimings()` + `rpc.RunRpcMode(ctx, runtime)`；清「Phase 5」注释。
- 测试（均已绿）：`jsonl_test.go`（5 例：含 U+2028/2029 保真、字面 `\\u2028`、CRLF、无尾 LF）、`rpc_mode_test.go`（未知命令保留 id=#5868；prompt preflight 失败/成功/排队三态的非扩展子集）。
- 存量测试基建：RPC 包内自建 `rpcTestSession`/`rpcTestRuntimeHost`/`syncOutput` + io.Pipe 注入（等价 `modes/print_mode_test.go` 模式，避免跨包依赖）。

### R2 —— 会话与 bash 命令测试 + 保真核对（✅ 完成 2026-10-17）
- 命令 dispatch 已在 R1 落地，本批补测试与逐条对照 pi（`rpc-mode.ts`）：
  - `rpc_commands_test.go`：`set_session_name`（trim + 空名报错）+ `get_state` 反映；`clear_queue`（steer+follow_up 入队后清空、二次清空为空）；`get_messages` + `get_last_assistant_text` + `get_entries`（since 游标命中/未知游标报错）+ `get_tree`；`get_commands`（三源合并为空数组而非 null）；`set_steering_mode` 反映到 `get_state`。
  - `rpc_commands_bash_test.go`：`bash`（真实 `echo`）+ `abort_bash`；`export_html` 错误传播（in-memory 会话报 core 错，成功路径由 core 8 个 export_html 测试覆盖）；`fork`/`clone`（空会话 clone 报 "no current entry selected"、prompt 后 clone/get_fork_messages/fork 形状）。
- 保真修正：`bash` 转发扩展 `operations`（`bashOptionsForRpc`，无扩展时恒 nil）；`handleCommand` 外包 recover 复刻 pi try/catch。
- 已声明偏差：`get_last_assistant_text` 空文本时 pig 出 `null`（pi 运行期 `undefined` 省略键，其类型声明却写 `string | null` → 取声明契约）；`get_entries` 的 `since` 用 `""` 表缺省（pi 用 `undefined` 判定，`since:""` 边界不同）。
- `prompt-during-compaction`（`test/suite/regressions/7150`）：实为 core 语义测试，由 core 侧覆盖，不重复移植。

### R3 —— 收口（✅ 完成 2026-10-17）
- README「与 pi 的能力边界」表拆两行：`RPC 模式` → ✅ 对齐；`sqlite 会话后端 / client-server 栈` → 上游已删 / 不适用。
- `docs/handoffs/verify-matrix.md`：无 RPC 行（该表只记 provider 真机），无需改。
- 冒烟：`printf '{"id":"1","type":"get_state"}\n{"id":"2","type":"foobar"}\n' | pig --mode rpc --offline`（`env -i` 清凭证 + 隔离 `PIG_CODING_AGENT_DIR`，非 TTY 无需 tmux）：两条 response 均按 id 返回，零网络调用。
- **冒烟实测偏差（已修）**：Go 调度可能在快速命令的 response 写出前就读到 stdin EOF 并退出（Node 在 `end` 前已排空 microtask，故 pi 不丢）→ 加 `handlers sync.WaitGroup` + EOF 路径 1s 有界等待（pig-only 健壮性；长 prompt/bash 不会拖住退出超过 1s）。
- 全量 `go test ./... -race -count=1 -timeout 600s` 单次全绿（33 包）；`go build ./... && go vet ./... && gofmt -l` 干净。
- AGENTS.md 批次索引补一行；本文件即 RPC mode handoff。

## 5. 风险与坑

1. **模式阻塞语义**：pi `runRpcMode` 返回永不 resolve 的 Promise（进程长活）。Go 侧 `RunRpcMode` 须阻塞至 stdin EOF / shutdown；单测走可注入 IO 的版本，勿用真 stdin。
2. **stdout 接管**：`TakeOverStdout` 后 `os.Stdout` 指 stderr，`WriteRawStdout` 写原始 stdout。测试捕获 stdout 用 `os.Pipe` + 交换 `os.Stdout`（print_mode_test.go:179 模式），并注意 `output_guard` 全局状态需 `RestoreStdout` 复位。
3. **双形态家族**：`ai.Model` 值形态为主，但 `session.Model()` 返回 `*types.Model`；RPC 响应里的 model 序列化须与 pi 的 Model JSON 逐字段一致（值/指针 marshal 相同，但 `get_available_models` 是 `[]ai.Model` 值切片）。断言的 type switch 记得双形态兜底（AGENTS.md 教训）。
4. **CycleThinkingLevel 返回 ""**：pi 的 `cycleThinkingLevel()` 返回 undefined 时 rpc 出 `null`；pig 用 "" 判空。
5. **CycleModel 返回 nil**：pi null；直接 `null`。
6. **bash 的 EmitUserBash**：pig 无扩展时恒返回 nil，直接走 `ExecuteBash`；但为结构保真保留 emit 调用（Phase 7 落地后自然生效）。
7. **export_html themeName**：pi 内部解析主题，pig 签名要 themeName——传 settings 的 theme 名，取不到传 ""（需核实 `ExportToHtml` 对 "" 的行为）。
8. **prompt preflight 时序**：pi 先 `void session.prompt(...)` 立即返回、成功与否由 preflight 回调异步 emit；pig 的 `Prompt` 是同步阻塞语义，preflight 回调在跑起来前触发——须对齐「一条 prompt 只 emit 一条 response」的语义（#9098），失败路径只 emit 一条 error。
9. **EOF 与响应顺序**：pig 每行命令独立 goroutine，EOF 前可能未及写出 response → 已加有界 drain（见 R3）；多行命令的响应顺序不保证（协议用 `id` 关联，pi 近似按序）。
8. **prompt preflight 时序**：pi 先 `void session.prompt(...)` 立即返回、成功与否由 preflight 回调异步 emit；pig 的 `Prompt` 是同步阻塞语义，preflight 回调在跑起来前触发——须对齐「一条 prompt 只 emit 一条 response」的语义（#9098），失败路径只 emit 一条 error。

## 6. 明确不要读（防上下文浪费）

- `packages/{server,client,protocol,session-backends}/`（源码已删，磁盘残留 `dist/` 是陈旧产物，禁止引用）。
- `packages/coding-agent/src/modes/rpc/rpc-client.ts`（不迁）。
- `packages/coding-agent/src/experimental/`、`src/client/`、`src/cli/experimental/`、coordinator/durable（已删/不做）。
- `test/rpc-client-*.test.ts`、`test/rpc-example.ts`。
