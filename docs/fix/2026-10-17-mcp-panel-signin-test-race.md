# MCP panel sign-in test race（2026-10-17）

- **状态**：已修复
- **涉及文件**：`codingagent/modes/interactive/components/mcp_panel_test.go`
- **测试函数**：`TestMCPPanelSignInAndCancel`

## 现象

`TestMCPPanelSignInAndCancel` 间歇性失败（约 1/4 概率，全包一次性跑时；单跑与 `-shuffle` 通过）：

```
mcp_panel_test.go:502: ctrl+s must call SignIn, calls =
```

## 根因

`MCPPanelComponent.signInSelected`（`mcp_panel.go`）先**同步**设置 `p.signIn = view` 并渲染出 "Sign in to docs" 屏幕，再 `go` 起后台 goroutine 调用 `Provider.SignIn`。测试在 `waitFor` 看到屏幕文字后**立即**读取 `provider.calls`，此时后台 goroutine 可能尚未执行，`calls` 仍为空 → 断言失败。

这是测试自身的竞态（断言与异步副作用赛跑），并非产品代码缺陷。S19-2 在 `tree_selector_test.go` 新增一个测试后改变了同包测试的调度时序，将这一潜在 flake 放大到可复现。

## 修复

把对 `calls` 的立即断言改为 `h.waitFor("signin call", …)`，轮询直到 `provider.calls` 出现 `signin:docs`（内部持 `provider.mu`，与写入侧一致）。

## 验证

`for i in $(seq 1 8); do go test ./codingagent/modes/interactive/components/ -count=1; done` 连续通过；修复前同命令偶发失败。
