# 2026-10-16 下一轮刷新丢弃 handler 注入的 system prompt Sections——同一次运行内工具调用后 prompt 变化

- **日期**：2026-10-16 17:30
- **状态**：已修复收口（回归测试红→绿）
- **涉及文件**：`codingagent/core/agent_session.go`（下一轮刷新 `merged` 构造加 `Sections: currentOptions.Sections`）、`codingagent/core/agent_session_custom_message_run_test.go`（新增）

## 现象

移植 pi v1.2.0 `a447ba11f`（自定义消息触发的 run 也走 `before_agent_start`）时，为「同一次运行的每个请求保持同一 system prompt」写的回归测试红了：`before_agent_start` handler 通过 `event.SystemPromptOptions.Sections` 注入的 `extension_rules` 段，在**同一运行的下一个请求**（首个请求返回 tool call 后）消失，被下一轮刷新 patch 掉。

## 根因

`agent_session.go` 的 agent 下一轮刷新（`_installAgentNextTurnRefresh` 等价物，~line 1089）把 prompt 选项**逐字段重建**为 `merged`，但漏了 `Sections`：

```go
merged := NormalizeBuildSystemPromptOptions(BuildSystemPromptOptions{
    CustomPrompt: ..., ForceSystemPrompt: ..., SelectedTools: ...,
    ToolSnippets: ..., ToolGuidelines: ..., PromptGuidelines: ...,
    AppendSystemPrompt: ..., Cwd: ..., ContextFiles: ..., Skills: ...,
    // Sections 缺失
})
```

pi 上游是 `normalizeBuildSystemPromptOptions({ ...runOptions, selectedTools, toolSnippets, toolGuidelines })`——`...runOptions` 展开携带 `sections`。pig 的重建漏字段使得 handler 注入的 section 只在当前请求存在，工具调用后下一轮刷新即被移除；这影响**所有**运行（不止自定义消息 run，用户 prompt 运行里有工具调用时同样丢段）。pi 的 `a447ba11f` 只修了「自定义消息 run 从不设置 `_runSystemPromptOptions`」，pig 在此之前还叠加了这条字段遗漏。

## 修复

`merged` 构造补 `Sections: currentOptions.Sections`（与 pi 的 `...runOptions` 展开等价）。

## 回归测试

- `codingagent/core/agent_session_custom_message_run_test.go`：
  - `TestUserPromptRunKeepsHandlerSectionsAcrossToolCall`——用户 prompt + 工具调用，断言两次请求都含 handler section（**直击本条根因**）。
  - `TestCustomMessageRunKeepsSameSystemPromptForEveryRequest`——同批 `a447ba11f` 的自定义消息 run 版本。
  - `TestCustomMessageSentWhileStartingJoinsTheStartingRun`、`TestCustomMessageWithoutTriggerTurnFromHandlerJoinsFirstRequest`、`TestCustomMessageRunAbortsWhenAbortRequestedDuringSetup`——`a447ba11f` 的其余行为。

验证：改前 `TestUserPromptRunKeepsHandlerSectionsAcrossToolCall` 红（`request 1 lost the handler section`），加 `Sections` 行后 `go test ./codingagent/core/ -race` 全绿。
