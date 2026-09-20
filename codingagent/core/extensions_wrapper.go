// pi: core/extensions/wrapper.ts — tool wrappers for extension-registered
// tools. These wrappers only adapt tool execution so extension tools receive
// the runner context; tool call and tool result interception is handled by
// AgentSession via agent hooks.
package core

import (
	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/codingagent/core/tools"
)

// WrapRegisteredTool wraps a RegisteredTool into an AgentTool (pi:
// wrapRegisteredTool). v0.86.0 removed the addedToolNames reporting wrapper:
// tool introductions move to transcript system messages.
func WrapRegisteredTool(registeredTool RegisteredTool, runner *ExtensionRunner) agent.AgentTool {
	return tools.WrapToolDefinition(registeredTool.Definition, func() tools.ToolExecuteContext {
		ctx := runner.CreateContext()
		return &ctx
	}).AgentTool
}

// WrapRegisteredTools wraps all registered tools into AgentTools (pi:
// wrapRegisteredTools).
func WrapRegisteredTools(registeredTools []RegisteredTool, runner *ExtensionRunner) []agent.AgentTool {
	out := make([]agent.AgentTool, 0, len(registeredTools))
	for _, tool := range registeredTools {
		out = append(out, WrapRegisteredTool(tool, runner))
	}
	return out
}
