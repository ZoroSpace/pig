# 恢复会话时工具卡片丢结果：框色恒为 pending / 输出整段不显示

- **日期**：2026-10-03 12:44（各修复文件 mtime）
- **状态**：已修复，回归测试锁定，全量 `-race` rc=0（31 包）
- **涉及文件**：
  - `codingagent/modes/interactive/interactive_mode.go`（renderSessionItems 的 toolResult 分支、addMessageToChat 的 bashExecution 分支、新增 `asToolResultMessage`）
  - `codingagent/modes/interactive/interactive_mode_tool_result_test.go`（新增 2 个回归测试函数）

## 现象（用户报告）

`pig --continue` 恢复会话后，**恢复出来的工具卡片框是暗灰/近黑色**（而不是成功绿 / 失败红），并且**卡片里只有命令行那一行、工具输出整段不显示**。

用户原话：「是我用 pig continue 的时候，这个框变成黑色的了，是不是有bug」——注意这**与当日主题改动无关**（我最初只改了 `mdListBullet`，卡片框色与之无关），起因是恢复路径的结果匹配失败。

## 根因：双形态家族的第五例（指针/值断言）

`renderSessionItems`（恢复/重建 transcript 的主循环）里匹配工具结果时用的是**只认指针**的类型断言：

```go
} else if message.MessageRole() == types.RoleToolResult {
    toolResult, ok := message.(*types.ToolResultMessage)
    if !ok {
        continue          // ← 值形态的 toolResult 在这里被整条丢弃
    }
    if component := renderedPendingTools[toolResult.ToolCallID]; component != nil {
        component.UpdateResult(resultToComponentResult(toolResult, false), false)
        ...
```

而**会话 JSONL 解码路径产出的是值形态**（`core/session_manager.go: SessionEntryToContextMessages` → `case types.ToolResultMessage: message = m` 是值），
pi 侧是 TS 对象、没有指针/值之分，所以这是 pig 端口独有的缺陷（同类第 5 例：T5a-2「值类型 Content 断言」、09-11 工具 live 输出、09-11 footer usage、09-22 addMessageToChat 三族）。

结果链：

1. `renderedPendingTools[toolCallID]` 永远匹配不上 → 卡片永远停在**构造时的 `borderStateKey = "muted"`** 状态 → 框线用 `muted` 色（浅色主题下 #59636e 近黑，正是用户看到的「黑色框」；`github-light` 的 `toolSuccessRule` #85a893 根本没机会生效）；
2. `UpdateResult` 从未被调用 → `c.result == nil` → 渲染器只画调用行，**工具输出不显示**（`ctrl+o` 展开也没内容）。

**同样的缺陷还在隔壁分支**：`addMessageToChat` 的 `case harness.RoleBashExecution` 也是 `message.(*harness.BashExecutionMessage)`，而会话解码产出值形态（`core/session_manager.go:1047 var m harness.BashExecutionMessage`）→ 恢复会话时 `!command` 块**整块不渲染**（live 路径传指针所以日常不易发现）。

## 修法

按本仓 09-11 起的「双形态」约定补齐（值优先兜底指针）：

1. 新增 `asToolResultMessage(message)`（与既有 `asAssistantMessage` 并列同型），`renderSessionItems` 改走它；
2. `addMessageToChat` 的 bashExecution 分支补值形态兜底。

**行为面 zero-diff**：live 路径（`pendingTools` + `tool_execution_end` 事件）不走这两个分支的修复点，恢复路径在修复后多出的是「本应有」的输出与状态色。

## 验证

### 1. 真机复现（修复前/后各一次）

把真实会话文件复制到临时 agent 目录（不干扰用户正在写入的会话）：

```
D=/tmp/pig-repro/agent/sessions/--Users-lee-Projects-pig--   # 复制真实 jsonl + settings（theme 固定 github-light）
tmux new-session -d -s pig-repro -x 110 -y 30 \
  "cd /Users/lee/Projects/pig && PIG_CODING_AGENT_DIR=/tmp/pig-repro/agent ./pig --continue"
tmux capture-pane -p -e -t pig-repro   # 看框线 SGR 与是否有输出行
```

- **修复前**：框线 `\x1b[38;2;89;99;110m`（muted #59636e），卡片内除命令行外**无任何内容**；
- **修复后**：框线 `\x1b[38;2;133;168;147m`（`toolSuccessRule` #85a893 柔绿），并出现输出行（如 `... (14 earlier lines, ctrl+o to expand)` 及正文）。

### 2. 回归测试（stash 式红→绿）

`codingagent/modes/interactive/interactive_mode_tool_result_test.go`：

- `TestRestoreRendersToolResultsOnRestoredCards`：用 session manager 追加 user / assistant(toolCall) / toolResult 三条真实形态条目 → `renderSessionEntries`（恢复路径）→ 断言卡片帧里**能看到结果文本**、且**框线颜色 = 主题 success 色**（帧里所有纯 `─` 规则行都必须是 success 色）；
- `TestRestoreRendersBashExecutionMessages`：追加值形态 `harness.BashExecutionMessage` → 断言 `BashExecutionComponent` 被挂载且 command 正确。

红验证（临时把两处断言改回指针-only）：两条测试分别报
`session restore: no mounted tool card frame contains "=== ALL GREEN CHECK DONE ==="` 与 `restored bash execution block not mounted`；恢复修复后均 PASS。

### 3. 门槛

`gofmt` / `go vet ./...` 干净；全量 `go test ./... -race -count=1 -timeout 900s` **单次** rc=0（31 包，日志 `/tmp/pig-test-out-restore-fix.log`）。

## 备注

- 本次同时改了 `github-light` 主题的 `mdListBullet`（列表标记 violet → text #1f2328，用户要求）——与本体缺陷无关，回归测试见 `theme_builtin_palettes_test.go: TestBuiltinGitHubLight/list markers use the body text color`。
- 内置主题从二进制 embed 读取，主题改动需重新 `go build -o pig ./cmd/pig` 并重开会话才生效（本次已重编译）。
