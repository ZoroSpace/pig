// pi: src/core/tools/tool-definition-wrapper.ts — synthesize a minimal
// ToolDefinition from an AgentTool.
//
// wrapToolDefinition/wrapToolDefinitions already live in tool_context.go
// (consolidated with the tools batch); only the AgentTool→ToolDefinition
// synthesis is ported here.
package tools

import (
	"context"

	"github.com/ZoroSpace/pig/agent"
)

// CreateToolDefinitionFromAgentTool synthesizes a minimal ToolDefinition from
// an AgentTool (pi: createToolDefinitionFromAgentTool). This keeps
// AgentSession's internal registry definition-first even when a caller
// provides plain AgentTool overrides that do not include prompt metadata or
// renderers.
func CreateToolDefinitionFromAgentTool(tool agent.AgentTool) *ToolDefinition {
	return &ToolDefinition{
		Name:                tool.Name,
		Label:               tool.Label,
		Description:         tool.Description,
		Parameters:          tool.Parameters,
		ConstrainedSampling: tool.ConstrainedSampling,
		PrepareArguments:    tool.PrepareArguments,
		ExecutionMode:       tool.ExecutionMode,
		Execute: func(ctx context.Context, toolCallID string, params any, onUpdate agent.AgentToolUpdateCallback, toolCtx ToolExecuteContext) (agent.AgentToolResult, error) {
			return tool.Execute(ctx, toolCallID, params, onUpdate)
		},
	}
}
