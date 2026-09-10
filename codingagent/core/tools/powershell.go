// pi: src/core/tools/powershell.ts — the PowerShell variant of the shell
// tool.
package tools

import (
	"context"

	codingagentutils "github.com/ZoroSpace/pig/codingagent/utils"
)

// utf8OutputPrefix forces UTF-8 output encoding in PowerShell (pi:
// UTF8_OUTPUT_PREFIX).
const utf8OutputPrefix = "try { [Console]::OutputEncoding=[System.Text.Encoding]::UTF8 } catch {}\n"

// PowerShellToolSystemPromptContribution is the PowerShell tool's system
// prompt metadata (pi: powershellToolSystemPromptContribution).
var PowerShellToolSystemPromptContribution = struct {
	Snippet    string
	Guidelines []string
}{
	Snippet:    "Execute PowerShell commands",
	Guidelines: []string{"You can inspect PI_* environment variables for current model and session details."},
}

// PowerShellOperations/SpawnContext/SpawnHook/Details/Input are aliases of
// the bash tool's types (pi: type aliases).
type (
	PowerShellOperations   = BashOperations
	PowerShellSpawnContext = BashSpawnContext
	PowerShellSpawnHook    = BashSpawnHook
	PowerShellToolDetails  = BashToolDetails
	PowerShellToolInput    = BashToolInput
)

// PowerShellToolOptions parameterize the PowerShell tool (pi:
// PowerShellToolOptions = Pick<BashToolOptions, ...>).
type PowerShellToolOptions struct {
	// Operations customizes command execution. Default: local PowerShell.
	Operations *BashOperations
	// ExposeSessionEnvironment exposes current Pi session metadata as PI_*
	// environment variables. Default: true.
	ExposeSessionEnvironment *bool
	// SpawnHook adjusts command, cwd, or env before execution.
	SpawnHook BashSpawnHook
}

// CreateLocalPowerShellOperations builds PowerShell operations with the UTF-8
// output prefix (pi: createLocalPowerShellOperations).
func CreateLocalPowerShellOperations() PowerShellOperations {
	operations := CreateLocalShellOperations("PowerShell", "", func() (codingagentutils.ShellConfig, error) {
		return codingagentutils.GetPowerShellConfig()
	})
	return BashOperations{
		Exec: func(ctx context.Context, command string, cwd string, options *BashExecOptions) (*int, error) {
			return operations.Exec(ctx, utf8OutputPrefix+command, cwd, options)
		},
	}
}

// powershellToolConfig is the PowerShell variant (pi: powershellToolConfig).
var powershellToolConfig = ShellToolConfig{
	Name:             "powershell",
	Label:            "powershell",
	ShellName:        "PowerShell",
	Prompt:           "PS>",
	PromptSnippet:    PowerShellToolSystemPromptContribution.Snippet,
	PromptGuidelines: PowerShellToolSystemPromptContribution.Guidelines,
	TempFilePrefix:   "pi-powershell",
}

// CreatePowerShellToolDefinition builds the PowerShell definition (pi:
// createPowerShellToolDefinition).
func CreatePowerShellToolDefinition(cwd string, options *PowerShellToolOptions) *ToolDefinition {
	var bashOptions *BashToolOptions
	if options != nil {
		bashOptions = &BashToolOptions{
			Operations:               options.Operations,
			ExposeSessionEnvironment: options.ExposeSessionEnvironment,
			SpawnHook:                options.SpawnHook,
		}
		if bashOptions.Operations == nil {
			ops := CreateLocalPowerShellOperations()
			bashOptions.Operations = &ops
		}
	} else {
		ops := CreateLocalPowerShellOperations()
		bashOptions = &BashToolOptions{Operations: &ops}
	}
	return CreateShellToolDefinition(cwd, powershellToolConfig, bashOptions)
}

// CreatePowerShellTool wraps the PowerShell definition into an AgentTool
// (pi: createPowerShellTool).
func CreatePowerShellTool(cwd string, options *PowerShellToolOptions) Tool {
	return WrapToolDefinition(CreatePowerShellToolDefinition(cwd, options), nil)
}
