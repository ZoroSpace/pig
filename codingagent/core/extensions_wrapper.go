// pi: core/extensions/wrapper.ts — tool wrappers for extension-registered
// tools. These wrappers only adapt tool execution so extension tools receive
// the runner context; tool call and tool result interception is handled by
// AgentSession via agent hooks.
package core

import (
	"context"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/codingagent/core/tools"
)

// WrapRegisteredTool wraps a RegisteredTool into an AgentTool (pi:
// wrapRegisteredTool). The wrapped execute snapshots the active tool set
// around execution and reports newly added tool names on the result.
func WrapRegisteredTool(registeredTool RegisteredTool, runner *ExtensionRunner) agent.AgentTool {
	tool := tools.WrapToolDefinition(registeredTool.Definition, func() tools.ToolExecuteContext {
		ctx := runner.CreateContext()
		return &ctx
	})
	execute := tool.Execute
	tool.Execute = func(ctx context.Context, toolCallID string, params any, onUpdate agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
		activeBefore := runner.GetActiveTools()
		result, err := execute(ctx, toolCallID, params, onUpdate)
		if err != nil {
			return result, err
		}
		activeAfter := runner.GetActiveTools()
		for _, name := range activeBefore {
			stillActive := false
			for _, after := range activeAfter {
				if after == name {
					stillActive = true
					break
				}
			}
			if !stillActive {
				// A tool was removed: report the result unchanged.
				return result, nil
			}
		}
		beforeNames := map[string]bool{}
		for _, name := range activeBefore {
			beforeNames[name] = true
		}
		var addedToolNames []string
		for _, name := range activeAfter {
			if !beforeNames[name] {
				addedToolNames = append(addedToolNames, name)
			}
		}
		if len(addedToolNames) == 0 {
			return result, nil
		}
		seen := map[string]bool{}
		for _, name := range result.AddedToolNames {
			seen[name] = true
		}
		for _, name := range addedToolNames {
			if !seen[name] {
				result.AddedToolNames = append(result.AddedToolNames, name)
				seen[name] = true
			}
		}
		return result, nil
	}
	return tool.AgentTool
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
