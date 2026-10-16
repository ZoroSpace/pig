# 2026-09-11 交互模式工具 live 输出整段不显示（多级丢失，已收口）

- **日期**：2026-09-11 23:0x（文件 mtime，第三轮收口更新）
- **状态**：**已修复收口**——两级丢失点全部修复（ba4e88b 转换器 + 本轮渲染端双形态），用户真机验证通过（echo + go test 卡片 live 输出正常）
- **涉及文件**：
  - `codingagent/core/tools/render_utils.go`（`GetTextOutput` 补 `*TextContent`/`*ImageContent` 分支——**本 bug 的主根因**）
  - `codingagent/core/tools/renderers_edit.go` / `renderers_write.go`（错误文本提取补指针分支）
  - `codingagent/modes/interactive/components/tool_execution.go`（image 块提取补指针分支）
  - `codingagent/modes/interactive/interactive_mode.go`（第一轮已修：`resultToComponentResult`，ba4e88b）
  - `codingagent/modes/interactive/interactive_mode_tool_result_test.go`（回归测试；本轮探针改指针形态 + 新增 handleEvent 级测试）
  - `codingagent/core/tools/render_text_output_test.go`（新增：GetTextOutput 双形态单测）
  - `codingagent/core/agent_session_tool_result_events_test.go`（第一轮：事件泵载荷契约，PASS 无偏差）

## 现象

交互模式里**所有工具**的 live 输出整段不显示：卡片只剩命令行 + "Took Xs" 脚注。**会话恢复（resume）后同会话的工具输出反而正常**。

## 根因（两级丢失点，分两轮修）

### 第一级（ba4e88b，第一轮修复）

`agent/agent_loop.go` 事件载荷装 `agent.AgentToolResult`，`resultToComponentResult` 只认 `types.ToolResultMessage` → 静默空。已补 value/pointer 分支并保留。

### 第二级（本轮，主根因）

pig 的 content 块存在**值/指针双形态**：builtin 工具全部经 `asTextContent`（shared.go:47）产出 **`&types.TextContent`（指针）**，read.go 直接构造 `&types.ImageContent`；而 JSONL 解码（`DecodeContent`）与 LLM 响应解码（`types.NewTextContent`）产出**值形态**。渲染端的 type switch 只匹配值形态：

- `GetTextOutput`（render_utils.go）——bash/grep/find/ls/read 五个渲染器全走它 → **live 输出全灭**
- `renderers_edit.go` / `renderers_write.go` 错误文本提取 → live 错误信息丢失
- `tool_execution.go` image 块提取 → live 图片块丢失

"Took" 脚注不依赖 content（只看 `state.StartedAt`），所以卡片显示命令行 + Took 而预览区空——与症状逐字吻合。

**resume 正常的原因**：JSONL 解码产出值形态，`GetTextOutput` 匹配值形态。

**模型侧不受影响**：`TransformMessages` 不转换 ToolResultMessage 内容，序列化经 MarshalJSON 对两形态等价——bug 只在渲染端 type switch。

## 为什么第一轮测试没测出来

第一轮全部测试（转换器/组件渲染/事件泵）的探针用 `types.TextContent{...}` **值形态**，每层都通过；真实流程带指针形态，测试链整体对该 bug 盲。教训：**测试探针必须镜像真实生产者的形态**（本轮把 `probeAgentToolResult` 改为 `&types.TextContent` 后立即复现）。

## 排查路径（第二轮）

按 fix 文档既定计划执行：

1. **handleEvent 级测试**（文档要求的缺失测试形态）：`newSeamTestMode` 半构造 + 直接 `m.handleEvent` 喂真实事件 → **PASS**（排除 handleEvent 胶水层：pendingTools 查找/转换器/UpdateResult 调用链无丢失）
2. 包装链审计：`WrapToolDefinition` / `WrapRegisteredTool` / `WithBuiltInRenderers` 均透传 `AgentToolResult` 原样（排除）
3. 事件类型审计：`agent_loop.go` emit 的 `PartialResult`/`Result` 确为 `AgentToolResult` 值；`TransformMessages` 不碰 tool result（排除）
4. **渲染端穷举**：对 live 渲染路径全部消费 tool content 的 type switch 逐个核对，发现 4 处值匹配 + 生产端 `asTextContent` 指针形态的矛盾
5. 探针改指针形态 → 组件渲染测试与 handleEvent 测试 **FAIL**（渲染帧只有命令行，症状逐字复现）→ 修 4 处 → 全绿

## 回归测试（stash 验证：修复前 FAIL / 修复后 PASS）

`codingagent/modes/interactive/interactive_mode_tool_result_test.go`：

- `TestHandleEventLiveToolOutputReachesMountedComponent`——handleEvent 级全链路：真实 AgentSessionEvent（start/update/end，指针形态探针）过 `m.handleEvent`，断言挂载卡片渲染帧含探针（**本轮新增**）
- `TestLiveToolExecutionResultReachesBashRenderer`——转换 → UpdateResult → 真实 bash 渲染器帧断言（探针已改指针形态，对 bug 敏感）
- `TestResultToComponentResultCarriesAgentToolResult` / `TestResultToComponentResultKeepsToolResultMessage`——转换器 value/pointer/IsError/Details + resume 路径

`codingagent/core/tools/render_text_output_test.go`（**本轮新增**）：

- `TestGetTextOutputHandlesPointerBlocks` / `TestGetTextOutputHandlesPointerImageBlocks`——GetTextOutput 对指针/值两形态的文本与图片块匹配

验证方式：`git stash push` 4 个修复文件 → 两包 FAIL；`git stash pop` → PASS。全仓 `go test ./... -race` 纳入常规回归。

## 同族问题登记（未处理，不在本 fix 范围）

值/指针双形态 type switch 是 pig 特有 artifact（pi 无此区分），此前已有 footer-usage（第四例）、print_mode/json_event 等多起。本次修的是**渲染路径**；仓内仍有单形态匹配点（如 `custom_message.go:124` 仅指针、`agent_session_runtime.go:71` 仅指针、`session_manager.go` 等仅值），凡命中非自身流程内容时均为潜伏 bug。后续若再撞同族，考虑统一收敛方案（如 types 层提供 `AsTextContent(c) (TextContent, bool)` 助手或生产端形态归一）。
