// pig-only: native MCP (Model Context Protocol) client integration. pi has
// no native MCP (pi-mcp-adapter is an npm extension, and pig's extension
// system is on the not-doing list), so this is a pig-only surface with no pi
// counterpart.
//
// Responsibilities:
//   - config file chain (project root .mcp.json → project .pig/mcp.json →
//     user agentDir/mcp.json → global ~/.config/mcp/mcp.json, later wins),
//     with trust gating for the project-local files
//   - one *mcp.Manager per AgentSession, closed on Dispose
//   - the "mcp" proxy tool definition (status/search/describe/tool actions,
//     shape aligned to pi-mcp-adapter's proxy input)
package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/codingagent"
	"github.com/ZoroSpace/pig/codingagent/core/tools"
	pigmcp "github.com/ZoroSpace/pig/mcp"
)

// mcpGlobalConfigSubpath is the ecosystem-standard global config location
// (~/.config/mcp/mcp.json via os.UserConfigDir, i.e. XDG_CONFIG_HOME aware).
const mcpGlobalConfigSubpath = "mcp/mcp.json"

// createMCPManager loads the MCP config file chain for one session cwd and
// builds the manager (nil = no servers configured). Project-local files are
// only loaded when the project is trusted; skipping them produces a warning
// diagnostic.
func createMCPManager(cwd, agentDir string, projectTrusted bool) (*pigmcp.Manager, []AgentSessionRuntimeDiagnostic) {
	var paths []string
	var diagnostics []AgentSessionRuntimeDiagnostic

	rootPath := filepath.Join(cwd, ".mcp.json")
	if fileExistsForMCP(rootPath) {
		if projectTrusted {
			paths = append(paths, rootPath)
		} else {
			diagnostics = append(diagnostics, AgentSessionRuntimeDiagnostic{
				Type:    AgentSessionRuntimeDiagnosticWarning,
				Message: fmt.Sprintf("Skipping %s: project directory is not trusted. Trust the project to enable its MCP servers.", rootPath),
			})
		}
	}
	projectPath := filepath.Join(cwd, codingagent.ConfigDirName, "mcp.json")
	if fileExistsForMCP(projectPath) {
		if projectTrusted {
			paths = append(paths, projectPath)
		} else {
			diagnostics = append(diagnostics, AgentSessionRuntimeDiagnostic{
				Type:    AgentSessionRuntimeDiagnosticWarning,
				Message: fmt.Sprintf("Skipping %s: project directory is not trusted. Trust the project to enable its MCP servers.", projectPath),
			})
		}
	}
	paths = append(paths, userMCPConfigPath(agentDir))
	if globalPath := mcpGlobalConfigPathFn(); globalPath != "" {
		paths = append(paths, globalPath)
	}

	config, configDiagnostics, err := pigmcp.LoadConfigFiles(paths)
	if err != nil {
		diagnostics = append(diagnostics, AgentSessionRuntimeDiagnostic{
			Type:    AgentSessionRuntimeDiagnosticWarning,
			Message: fmt.Sprintf("Failed to load MCP config: %v", err),
		})
		return nil, diagnostics
	}
	for _, diag := range configDiagnostics {
		severity := AgentSessionRuntimeDiagnosticWarning
		if diag.Severity == "error" {
			severity = AgentSessionRuntimeDiagnosticError
		}
		message := diag.Message
		if diag.Path != "" {
			message = fmt.Sprintf("%s: %s", diag.Path, message)
		}
		if diag.Server != "" {
			message = fmt.Sprintf("%s (server %q)", message, diag.Server)
		}
		diagnostics = append(diagnostics, AgentSessionRuntimeDiagnostic{Type: severity, Message: message})
	}
	if config.IsEmpty() {
		return nil, diagnostics
	}

	manager := pigmcp.NewManager(config, pigmcp.ManagerOptions{
		ClientVersion: codingagent.Version,
		// Connection-time header resolution (not load-time): `!command`
		// results come from the process-wide config-value cache, so
		// token-rotation headers carry that cache's expiry semantics.
		ResolveHeaders: func(server string, headers map[string]string) (map[string]string, error) {
			return ResolveHeadersOrThrow(headers, fmt.Sprintf("MCP server %q", server), nil)
		},
	})
	return manager, diagnostics
}

func fileExistsForMCP(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// userMCPConfigPath resolves the user-level mcp.json under the session's
// agent dir (explicit agentDir wins so tests are isolated from ~/.pig).
func userMCPConfigPath(agentDir string) string {
	if agentDir != "" {
		return filepath.Join(agentDir, "mcp.json")
	}
	return codingagent.GetMCPPath()
}

// defaultMCPGlobalConfigPath resolves ~/.config/mcp/mcp.json ("" = none).
func defaultMCPGlobalConfigPath() string {
	userConfigDir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(userConfigDir, mcpGlobalConfigSubpath)
}

// mcpGlobalConfigPathFn is a test seam for the global config path (pig test
// convention: package-level var swapped per test).
var mcpGlobalConfigPathFn = defaultMCPGlobalConfigPath

// ---- the mcp proxy tool ----

const mcpProxyToolName = "mcp"

// mcpProxyToolSchema is the proxy tool's input schema: one object carrying
// exactly one action (shape aligned to pi-mcp-adapter's proxy input).
const mcpProxyToolSchema = `{
  "type": "object",
  "properties": {
    "status": {
      "type": "boolean",
      "description": "Set to true to list the configured MCP servers and their connection state."
    },
    "search": {
      "type": "string",
      "description": "Query string to search MCP tools by name/description. Pass an empty string to list every tool."
    },
    "describe": {
      "type": "string",
      "description": "Tool name to describe in full (parameters schema)."
    },
    "tool": {
      "type": "string",
      "description": "Tool name to invoke, as returned by search (display name, raw name, or server__tool)."
    },
    "args": {
      "type": "object",
      "description": "Arguments object passed to the tool named by \"tool\".",
      "additionalProperties": true
    }
  },
  "required": []
}`

// mcpProxyToolInput is the decoded proxy tool input.
type mcpProxyToolInput struct {
	Status   bool           `json:"status,omitempty"`
	Search   *string        `json:"search,omitempty"`
	Describe string         `json:"describe,omitempty"`
	Tool     string         `json:"tool,omitempty"`
	Args     map[string]any `json:"args,omitempty"`
}

// createMCPProxyToolDefinition builds the "mcp" proxy tool backed by the
// manager. Render functions stay nil: tool-execution's fallback rendering
// covers the call/result rows (declared pig deviation from pi-mcp-adapter,
// which ships its own renderers).
func createMCPProxyToolDefinition(manager *pigmcp.Manager) *tools.ToolDefinition {
	return &tools.ToolDefinition{
		Name:  mcpProxyToolName,
		Label: "MCP",
		Description: "Discover and call tools from configured MCP servers. " +
			"Set status to list servers, pass search to find tools (empty string lists all), " +
			"describe for a tool's full schema, or tool (+args) to invoke one.",
		PromptSnippet: "Call MCP server tools via this proxy: search first ({\"search\": \"query\"}), then invoke ({\"tool\": \"server__tool\", \"args\": {...}}).",
		Parameters:    json.RawMessage(mcpProxyToolSchema),
		Execute: func(ctx context.Context, _ string, params any, _ agent.AgentToolUpdateCallback, _ tools.ToolExecuteContext) (agent.AgentToolResult, error) {
			input, err := decodeMCPProxyParams(params)
			if err != nil {
				return agent.AgentToolResult{}, err
			}
			switch {
			case input.Status:
				return mcpStatusResult(manager), nil
			case input.Search != nil:
				return mcpSearchResult(ctx, manager, *input.Search)
			case input.Describe != "":
				return mcpDescribeResult(ctx, manager, input.Describe)
			case input.Tool != "":
				return mcpCallResult(ctx, manager, input.Tool, input.Args)
			default:
				return agent.AgentToolResult{}, errors.New(`mcp tool requires one of: status, search, describe, or tool`)
			}
		},
	}
}

func decodeMCPProxyParams(params any) (mcpProxyToolInput, error) {
	var input mcpProxyToolInput
	data, err := json.Marshal(params)
	if err != nil {
		return input, err
	}
	if err := json.Unmarshal(data, &input); err != nil {
		return input, fmt.Errorf("invalid mcp tool arguments: %w", err)
	}
	return input, nil
}

func mcpStatusResult(manager *pigmcp.Manager) agent.AgentToolResult {
	servers := manager.Status()
	if len(servers) == 0 {
		return mcpTextResult("No MCP servers configured.")
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("%d MCP server(s):\n", len(servers)))
	for _, s := range servers {
		b.WriteString(fmt.Sprintf("- %s [%s] %s", s.Name, s.Transport, s.Status))
		if s.ToolCount > 0 {
			b.WriteString(fmt.Sprintf(", %d tools", s.ToolCount))
		}
		if s.IdleMs > 0 {
			b.WriteString(fmt.Sprintf(", idle %ds", s.IdleMs/1000))
		}
		b.WriteString("\n")
		if s.LastError != "" {
			b.WriteString(fmt.Sprintf("  error: %s\n", s.LastError))
		}
	}
	return mcpTextResult(strings.TrimSuffix(b.String(), "\n"))
}

func mcpSearchResult(ctx context.Context, manager *pigmcp.Manager, query string) (agent.AgentToolResult, error) {
	// Listing triggers lazy connects (discovery implies connection; same
	// semantics as pi-mcp-adapter).
	matches, err := manager.SearchTools(ctx, query, 0)
	if err != nil {
		return agent.AgentToolResult{}, err
	}
	if len(matches) == 0 {
		return mcpTextResult(fmt.Sprintf("No MCP tools match %q.", query)), nil
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("%d MCP tool(s):\n", len(matches)))
	for _, match := range matches {
		b.WriteString(fmt.Sprintf("- %s (server %q)", match.Tool.Name, match.Tool.Server))
		if match.Tool.Name != match.Tool.RawName {
			b.WriteString(fmt.Sprintf(" raw: %s", match.Tool.RawName))
		}
		b.WriteString("\n")
		if match.Tool.Description != "" {
			b.WriteString(fmt.Sprintf("  %s\n", match.Tool.Description))
		}
	}
	return mcpTextResult(strings.TrimSuffix(b.String(), "\n")), nil
}

func mcpDescribeResult(ctx context.Context, manager *pigmcp.Manager, name string) (agent.AgentToolResult, error) {
	info, err := manager.DescribeTool(ctx, name)
	if err != nil {
		return agent.AgentToolResult{}, err
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("%s (server %q)\n", info.Name, info.Server))
	if info.Description != "" {
		b.WriteString(info.Description + "\n")
	}
	if info.Schema != nil {
		schema, err := json.MarshalIndent(info.Schema, "", "  ")
		if err == nil {
			b.WriteString("Parameters:\n" + string(schema))
		}
	}
	return mcpTextResult(strings.TrimSuffix(b.String(), "\n")), nil
}

func mcpCallResult(ctx context.Context, manager *pigmcp.Manager, tool string, args map[string]any) (agent.AgentToolResult, error) {
	server := ""
	name := tool
	if s, rest, ok := strings.Cut(tool, "__"); ok {
		server, name = s, rest
	} else {
		// Bare name: resolve across servers (ambiguity is an error, never a
		// silent pick).
		info, err := manager.DescribeTool(ctx, tool)
		if err != nil {
			return agent.AgentToolResult{}, err
		}
		server = info.Server
	}
	output, err := manager.CallTool(ctx, server, name, args)
	if err != nil {
		return agent.AgentToolResult{}, err
	}
	if output.IsError {
		// MCP spec: tool-handler failures are normal results with IsError,
		// surfaced to the LLM. pig tools report failures as Go errors, which
		// the agent loop renders as a standard IsError tool result.
		return agent.AgentToolResult{}, errors.New(mcpContentText(output.Content))
	}
	return agent.AgentToolResult{Content: convertMCPContent(output.Content)}, nil
}

func mcpTextResult(text string) agent.AgentToolResult {
	return agent.AgentToolResult{Content: []types.Content{&types.TextContent{Kind: types.KindText, Text: text}}}
}

// mcpContentText joins the text content of an MCP result (for IsError
// conversion and empty-result fallbacks).
func mcpContentText(contents []gomcp.Content) string {
	var parts []string
	for _, content := range contents {
		if text, ok := content.(*gomcp.TextContent); ok {
			parts = append(parts, text.Text)
		}
	}
	if len(parts) == 0 {
		return "MCP tool failed (no error text returned)"
	}
	return strings.Join(parts, "\n")
}

// convertMCPContent maps MCP content blocks to pig's tool-result content.
// Text and images pass through; audio/resources become descriptive text
// (MCP-2 material).
func convertMCPContent(contents []gomcp.Content) []types.Content {
	if len(contents) == 0 {
		return []types.Content{&types.TextContent{Kind: types.KindText, Text: "(empty result)"}}
	}
	out := make([]types.Content, 0, len(contents))
	for _, content := range contents {
		switch c := content.(type) {
		case *gomcp.TextContent:
			out = append(out, &types.TextContent{Kind: types.KindText, Text: c.Text})
		case *gomcp.ImageContent:
			// SDK ImageContent.Data is already base64-encoded.
			out = append(out, &types.ImageContent{Kind: types.KindImage, Data: string(c.Data), MimeType: c.MIMEType})
		case *gomcp.AudioContent:
			out = append(out, &types.TextContent{Kind: types.KindText, Text: fmt.Sprintf("[audio content: %s, %d base64 bytes]", c.MIMEType, len(c.Data))})
		case *gomcp.ResourceLink:
			out = append(out, &types.TextContent{Kind: types.KindText, Text: fmt.Sprintf("[resource link: %s]", c.URI)})
		case *gomcp.EmbeddedResource:
			out = append(out, &types.TextContent{Kind: types.KindText, Text: fmt.Sprintf("[embedded resource: %s]", c.Resource.URI)})
		default:
			out = append(out, &types.TextContent{Kind: types.KindText, Text: "[unsupported content type]"})
		}
	}
	return out
}
