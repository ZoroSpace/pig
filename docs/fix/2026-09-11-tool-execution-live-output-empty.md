# 2026-09-11 交互模式工具 live 输出整段不显示（resultToComponentResult 丢 AgentToolResult）

- **日期**：2026-09-11 21:50（文件 mtime）
- **状态**：已修复，回归测试锁定
- **涉及文件**：
  - `codingagent/modes/interactive/interactive_mode.go`（修复点：`resultToComponentResult` + `tool_execution_update` 调用点）
  - `codingagent/modes/interactive/interactive_mode_tool_result_test.go`（回归测试，新增）

## 现象

交互模式里**所有工具**的 live 输出整段不显示：卡片只剩命令行 + "Took Xs" 脚注。用户两例实证：

1. heredoc 脚本里 python `print` 的计数没显示（`go test` 管道例，`Took 0.5s`）；
2. 67.9s 全仓验收命令里 `echo "=== ALL GREEN CHECK DONE ==="`（`;` 链结尾、无条件必打）也没显示——go test 失败的错误自然更看不见。

佐证签名：卡片显示 "Took"（而非 partial 态的 "Elapsed"）说明渲染器收到的是**最终结果**、却带空 content；而工具层空输出会兜底 `"(no output)"`，只有 content 在「AgentToolResult → UI」中途被丢才会完全空白。**会话恢复（resume）后旧工具输出反而正常**——恢复路径喂的是 `*types.ToolResultMessage`（transcript entry），恰好匹配转换器，掩盖了 bug。

## 根因

类型不匹配的静默丢弃：

- 生产侧：`agent/agent_loop.go` 的 `tool_execution_update`/`tool_execution_end` 事件里 `PartialResult`/`Result` 装的是 **`agent.AgentToolResult`**（`:748` / `:818`，`finalizedToolCallOutcome.result` 即 `AgentToolResult`）；`AgentSession.handleAgentEvent` 原样转发给 UI（`agent_session.go:975`）。
- 消费侧：`interactive_mode.go` 的 `resultToComponentResult` 只做 `types.ToolResultMessage` / `*types.ToolResultMessage` 两个 case，`agent.AgentToolResult` 不匹配任何分支 → 返回空 Content/Details；`partialResultContent` 同病。
- pi 规格：`interactive-mode.ts:3351/3360` 是直接 spread——`component.updateResult({ ...event.partialResult, isError: false }, true)` / `{ ...event.result, isError: event.isError }`，content 和 details 都透传。所以 pi 显示、pig 不显示。

影响面：所有工具（bash/read/edit/grep/…）的 live 流式输出与最终输出全部不可见，不止 go test。

## 修复

1. `resultToComponentResult` 补 `agent.AgentToolResult` 与 `*agent.AgentToolResult`（nil 守卫）两个 case，content/details 均提取；
2. `tool_execution_update` 调用点改用同一转换器（`isError=false`），对齐 pi 的 spread 语义——顺带修掉 partial `Details`（bash 流式期的截断信息）此前同样被丢的偏差；
3. 删除改后只剩死代码的 `partialResultContent`。

## 回归测试（stash 验证：修复前 FAIL、修复后 PASS）

`codingagent/modes/interactive/interactive_mode_tool_result_test.go`（探针文本 `=== ALL GREEN CHECK DONE ===`，与命令行文案刻意不同以防误报）：

- `TestResultToComponentResultCarriesAgentToolResult`——end seam：value + pointer + IsError 传播 + Details 提取；
- `TestResultToComponentResultKeepsToolResultMessage`——恢复路径锁定（ToolResultMessage value/pointer，修复前即 PASS，证明测试只锁被破坏的 seam）；
- `TestLiveToolExecutionResultReachesBashRenderer`——按 `handleEvent` 真实序列（partial → final）经 `resultToComponentResult` 喂真实 bash 渲染器的 `ToolExecutionComponent`，两态都断言探针可见。

验证方式：修复前运行 → `content blocks = 0, want 1` / `rendered frame missing output text`（2 FAIL，恢复路径 1 PASS）；修复后 3 测试全 PASS；全仓 `go build && go vet && gofmt -l` 干净、`go test ./... -count=1 -race` 28 包全绿。

## 复现方式（修复前）

交互模式让 agent 跑任意有输出的命令，例如 `go test ./... -count=1 -race 2>&1 | grep -E "^(FAIL|--- FAIL)" | head -5; echo "=== ALL GREEN CHECK DONE ==="`——卡片无任何输出；修复后 partial 流式与最终结果均显示。
