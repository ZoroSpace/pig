# 2026-09-11 交互模式工具 live 输出整段不显示（多级丢失，转换器已修、live 路径仍未决）

- **日期**：2026-09-11 22:09（文件 mtime，第二轮更新）
- **状态**：**部分修复 / 仍未决**——转换层 bug 已修（9cb15b2）但 live 卡片仍空；丢失点已收敛到 InteractiveMode 内部 live 路径，待下一轮排查
- **涉及文件**：
  - `codingagent/modes/interactive/interactive_mode.go`（已修：`resultToComponentResult` + `tool_execution_update` 调用点；**第二丢失点也在此文件的 live 事件路径，未定位**）
  - `codingagent/modes/interactive/interactive_mode_tool_result_test.go`（回归测试，新增）
  - `codingagent/core/agent_session_tool_result_events_test.go`（全链路载荷契约测试，新增，PASS）

## 现象

交互模式里**所有工具**的 live 输出整段不显示：卡片只剩命令行 + "Took Xs" 脚注。用户多例实证（`go test` 管道输出、heredoc python print、无条件 `echo "=== ALL GREEN CHECK DONE ==="` 均消失）。**会话恢复（resume）后同会话的工具输出反而正常**。

关键症状签名：卡片显示 "Took"（final 渲染发生过，partial 态显示 "Elapsed"）但预览区为空——说明**某个**组件收到了 final UpdateResult 却带着空 content。

## 第一轮修复（已提交 9cb15b2，正确但不足够）

`agent/agent_loop.go` 的 tool_execution_update/end 事件里 `PartialResult`/`Result` 装的是 `agent.AgentToolResult`（`:748`/`:818`），而 `interactive_mode.go` 的 `resultToComponentResult` 只认 `types.ToolResultMessage` → 静默返回空 Content/Details。pi 是直接 spread（interactive-mode.ts:3351/3360）。修复：转换器补 `AgentToolResult` value/pointer 分支 + partial 路径统一走它（顺带修回 partial Details 丢失）+ 删除死代码 `partialResultContent`。

**此修复正确且保留**（partial Details 丢失是真实偏差），但用户重编译重启后 live 卡片**仍空**——存在第二丢失点。

## 第二轮排查证据链（全部实测）

| 环节 | 证据 | 结论 |
|---|---|---|
| 运行二进制含修复 | `go version -m /home/lee/Projects/pig/pig` → vcs.revision=9cb15b2…, modified=false；进程 21:51:43 启动（修复 21:50:55） | ✅ 非旧二进制 |
| 工具产出/落盘 | 会话 JSONL（`~/.pig/agent/sessions/--home-lee-Projects-pig--/2026-09-11T13-15-34-588Z_01a0909b….jsonl`）逐条检查：**每个 toolResult 都有完整 content**（含空跑 demo 那次 L109） | ✅ 工具执行/OutputAccumulator/截断/持久化完好 |
| 会话事件泵载荷 | 新增 core 全链路测试 `TestAgentSessionToolExecutionEventsCarryContent`（真实 AgentSession + agent loop + 带 partial/final content 的工具 + Subscribe 监听器）断言 update/end 事件载荷完整 → **PASS** | ✅ pump→监听器不丢 |
| 转换器+组件渲染 | `TestResultToComponentResultCarriesAgentToolResult` / `TestLiveToolExecutionResultReachesBashRenderer`（转换 → UpdateResult → 真实 bash 渲染器 Render 显示探针）→ **PASS** | ✅ 组件层不丢 |
| 字段遮蔽 | `AgentSessionEvent` 嵌入 `agent.AgentEvent`，无 `Result`/`PartialResult` 重声明（仅 CompactionResult 换名）；core 测试直接读 `event.Result` 拿到 content | ✅ 无遮蔽 |
| 工具包装层 | `WrapToolDefinition`（tool_context.go:223）/`CreateToolDefinitionFromAgentTool` 透传；`WithBuiltInRenderers` 仅补 nil 渲染器 | ✅ 透传 |
| `ExecuteBash`/`BashExecutionUpdate` | session 级 `!` 命令功能（agent_session.go:3912，interactive:2738），与模型 bash 工具无关 | ✅ 无关 |

## 矛盾核心（下一 agent 的起点）

pump 交付满载荷 + 组件层渲染完好，但 live 卡片空 → **丢失点在 InteractiveMode 内部：`session.Subscribe` 监听器（interactive_mode.go:1170）→ `handleEvent` 的 tool_execution_* case（:1380-1395）→ `m.pendingTools` 组件实例** 之间。

未排查嫌疑（按优先级）：

1. **`m.pendingTools[event.ToolCallID]` 在 update/end 时是否 nil**（tool_execution_start 是否先于 end 到达 handleEvent；pendingTools 在 AgentEnd/AgentStart 被整体清空重建——竞态可能）。注意 "Took" 已渲染 ⇒ 有组件收到过 final UpdateResult ⇒ **确认收到结果的就是 chatContainer 里渲染的那一个实例**，还是存在第二个创建/替换路径（如 assistant message_end 附近的重建、`SetArgsComplete` 之后的重渲染）。
2. **运行时真正生效的订阅链**：interactive_mode.go:1170 是 `bindCurrentSession` 里的订阅；确认 main.go/startup 装配与 rebind/switchSession（T5b-3 的 `mutateUI`/`RunExclusive` 包装）有没有第二个订阅、事件包装或丢弃。
3. **handleEvent 是否真的执行到 tool case**：最快定位法——在 :1360/:1380/:1388 三个 case 加临时 debug 落盘（如 `os.WriteFile` 到 `/tmp/pig-toolevent-debug.log`，记录 `event.Type`/`ToolCallID`/`Result` 类型与 content 长度），用户跑一次有输出的命令即区分「事件没到 / 组件查不到 / 内容为空」。
4. **live vs resume 路径的组件实例差异**：resume 正常（:1876 用 transcript 的 `*ToolResultMessage` 构造）而 live 异常——对比两条路径构造的组件与数据源差异。

## 缺失的测试形态（下一 agent 先补这个）

现有两组测试都 PASS 但 live 仍坏 ⇒ 缺 **handleEvent 级测试**：在 package interactive 构造最小 InteractiveMode（参考 `test_session_seam_test.go:125` 的 `newSeamTestMode` 半构造模式，需补 `footer`/`isInitialized`/`settingsManager` 等 handleEvent 触碰的字段），向 `m.chatContainer` 挂一个真实 bash 渲染器的 `ToolExecutionComponent` 并注册进 `m.pendingTools`，然后**直接调用 `m.handleEvent`** 喂 `&core.AgentSessionEvent{AgentEvent: agent.AgentEvent{Type: ToolExecutionUpdate/End, ToolCallID: ..., Result: agent.AgentToolResult{...}}}`，断言渲染帧含探针文本。该测试若复现（FAIL）即可在纯 Go 环境二分，不必依赖真机。

## 复现方式

交互模式让 agent 跑任意有输出的命令（如 `go test ./... 2>&1 | tail -5; echo "=== ALL GREEN CHECK DONE ==="`）——卡片无输出；`--continue` 恢复同一会话后，同批工具卡片输出正常。

## 已落地测试（全仓 `-race` 绿）

- `codingagent/modes/interactive/interactive_mode_tool_result_test.go`：转换器 value/pointer/IsError/Details + ToolResultMessage 恢复路径锁定 + 组件级 update→end 渲染
- `codingagent/core/agent_session_tool_result_events_test.go`：会话事件泵载荷契约（update/end 均含 content）
