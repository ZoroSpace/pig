// pi: src/core/tools/index.ts — the tool factory surface.
//
// Segment deviations:
//   - ToolName/allToolNames stay as the same literal union/Set; pig uses a
//     named string type plus a map-based Set.
//   - ToolDefinition (ToolDef) arrives with the extensions batch; the
//     create*ToolDefinition functions are exported (they return the non-UI
//     ToolDefinition struct, see tool_context.go).
//   - The throw on unknown tool names → error return.
package tools

import (
	"fmt"
)

// ToolName is the closed tool-name vocabulary (pi: ToolName).
type ToolName string

const (
	ToolRead       ToolName = "read"
	ToolBash       ToolName = "bash"
	ToolPowerShell ToolName = "powershell"
	ToolEdit       ToolName = "edit"
	ToolWrite      ToolName = "write"
	ToolGrep       ToolName = "grep"
	ToolFind       ToolName = "find"
	ToolLs         ToolName = "ls"
)

// allToolNames is pi's Set of tool names.
var allToolNames = map[ToolName]struct{}{
	ToolRead: {}, ToolBash: {}, ToolPowerShell: {}, ToolEdit: {},
	ToolWrite: {}, ToolGrep: {}, ToolFind: {}, ToolLs: {},
}

// IsAllToolName reports membership in pi's allToolNames set.
func IsAllToolName(name ToolName) bool {
	_, ok := allToolNames[name]
	return ok
}

// ToolsOptions collects the per-tool options (pi: ToolsOptions).
type ToolsOptions struct {
	Read       *ReadToolOptions
	Bash       *BashToolOptions
	PowerShell *PowerShellToolOptions
	Write      *WriteToolOptions
	Edit       *EditToolOptions
	Grep       *GrepToolOptions
	Find       *FindToolOptions
	Ls         *LsToolOptions
}

// CreateToolDefinition creates one tool definition by name (pi:
// createToolDefinition).
func CreateToolDefinition(toolName ToolName, cwd string, options *ToolsOptions) (*ToolDefinition, error) {
	if options == nil {
		options = &ToolsOptions{}
	}
	switch toolName {
	case ToolRead:
		return CreateReadToolDefinition(cwd, options.Read), nil
	case ToolBash:
		return CreateBashToolDefinition(cwd, options.Bash), nil
	case ToolPowerShell:
		return CreatePowerShellToolDefinition(cwd, options.PowerShell), nil
	case ToolEdit:
		return CreateEditToolDefinition(cwd, options.Edit), nil
	case ToolWrite:
		return CreateWriteToolDefinition(cwd, options.Write), nil
	case ToolGrep:
		return CreateGrepToolDefinition(cwd, options.Grep), nil
	case ToolFind:
		return CreateFindToolDefinition(cwd, options.Find), nil
	case ToolLs:
		return CreateLsToolDefinition(cwd, options.Ls), nil
	default:
		return nil, fmt.Errorf("Unknown tool name: %s", toolName)
	}
}

// CreateTool creates one tool by name (pi: createTool).
func CreateTool(toolName ToolName, cwd string, options *ToolsOptions) (Tool, error) {
	if options == nil {
		options = &ToolsOptions{}
	}
	switch toolName {
	case ToolRead:
		return CreateReadTool(cwd, options.Read), nil
	case ToolBash:
		return CreateBashTool(cwd, options.Bash), nil
	case ToolPowerShell:
		return CreatePowerShellTool(cwd, options.PowerShell), nil
	case ToolEdit:
		return CreateEditTool(cwd, options.Edit), nil
	case ToolWrite:
		return CreateWriteTool(cwd, options.Write), nil
	case ToolGrep:
		return CreateGrepTool(cwd, options.Grep), nil
	case ToolFind:
		return CreateFindTool(cwd, options.Find), nil
	case ToolLs:
		return CreateLsTool(cwd, options.Ls), nil
	default:
		return Tool{}, fmt.Errorf("Unknown tool name: %s", toolName)
	}
}

// CreateCodingToolDefinitions builds the edit-capable definitions (pi:
// createCodingToolDefinitions).
func CreateCodingToolDefinitions(cwd string, options *ToolsOptions) []*ToolDefinition {
	if options == nil {
		options = &ToolsOptions{}
	}
	return []*ToolDefinition{
		CreateReadToolDefinition(cwd, options.Read),
		CreateBashToolDefinition(cwd, options.Bash),
		CreateEditToolDefinition(cwd, options.Edit),
		CreateWriteToolDefinition(cwd, options.Write),
	}
}

// CreateReadOnlyToolDefinitions builds the read-only definitions (pi:
// createReadOnlyToolDefinitions).
func CreateReadOnlyToolDefinitions(cwd string, options *ToolsOptions) []*ToolDefinition {
	if options == nil {
		options = &ToolsOptions{}
	}
	return []*ToolDefinition{
		CreateReadToolDefinition(cwd, options.Read),
		CreateGrepToolDefinition(cwd, options.Grep),
		CreateFindToolDefinition(cwd, options.Find),
		CreateLsToolDefinition(cwd, options.Ls),
	}
}

// CreateAllToolDefinitions builds every definition keyed by name (pi:
// createAllToolDefinitions).
func CreateAllToolDefinitions(cwd string, options *ToolsOptions) map[ToolName]*ToolDefinition {
	if options == nil {
		options = &ToolsOptions{}
	}
	return map[ToolName]*ToolDefinition{
		ToolRead:       CreateReadToolDefinition(cwd, options.Read),
		ToolBash:       CreateBashToolDefinition(cwd, options.Bash),
		ToolPowerShell: CreatePowerShellToolDefinition(cwd, options.PowerShell),
		ToolEdit:       CreateEditToolDefinition(cwd, options.Edit),
		ToolWrite:      CreateWriteToolDefinition(cwd, options.Write),
		ToolGrep:       CreateGrepToolDefinition(cwd, options.Grep),
		ToolFind:       CreateFindToolDefinition(cwd, options.Find),
		ToolLs:         CreateLsToolDefinition(cwd, options.Ls),
	}
}

// CreateCodingTools builds the edit-capable tools (pi: createCodingTools).
func CreateCodingTools(cwd string, options *ToolsOptions) []Tool {
	if options == nil {
		options = &ToolsOptions{}
	}
	return []Tool{
		CreateReadTool(cwd, options.Read),
		CreateBashTool(cwd, options.Bash),
		CreateEditTool(cwd, options.Edit),
		CreateWriteTool(cwd, options.Write),
	}
}

// CreateReadOnlyTools builds the read-only tools (pi: createReadOnlyTools).
func CreateReadOnlyTools(cwd string, options *ToolsOptions) []Tool {
	if options == nil {
		options = &ToolsOptions{}
	}
	return []Tool{
		CreateReadTool(cwd, options.Read),
		CreateGrepTool(cwd, options.Grep),
		CreateFindTool(cwd, options.Find),
		CreateLsTool(cwd, options.Ls),
	}
}

// CreateAllTools builds every tool keyed by name (pi: createAllTools).
func CreateAllTools(cwd string, options *ToolsOptions) map[ToolName]Tool {
	if options == nil {
		options = &ToolsOptions{}
	}
	return map[ToolName]Tool{
		ToolRead:       CreateReadTool(cwd, options.Read),
		ToolBash:       CreateBashTool(cwd, options.Bash),
		ToolPowerShell: CreatePowerShellTool(cwd, options.PowerShell),
		ToolEdit:       CreateEditTool(cwd, options.Edit),
		ToolWrite:      CreateWriteTool(cwd, options.Write),
		ToolGrep:       CreateGrepTool(cwd, options.Grep),
		ToolFind:       CreateFindTool(cwd, options.Find),
		ToolLs:         CreateLsTool(cwd, options.Ls),
	}
}
