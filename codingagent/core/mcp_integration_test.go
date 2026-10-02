// pig-only tests for the native MCP integration (core/mcp_integration.go +
// the AgentSession/sdk wiring). The mcp package's own tests cover the
// manager; these cover the core bridge: config chain + trust gating, proxy
// tool registration/activation, execute dispatch, and Dispose teardown.
package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/codingagent/core/tools"
	themePkg "github.com/ZoroSpace/pig/codingagent/modes/interactive/theme"
	pigmcp "github.com/ZoroSpace/pig/integrations/mcp"
	"github.com/ZoroSpace/pig/tui"
)

// newMCPFakeServerManager builds a manager over an in-memory fake MCP server
// (single "echo" tool) — the mcp package's fake-server pattern, mirrored here
// because its test helpers are not exported.
func newMCPFakeServerManager(t *testing.T, serverName string, config *pigmcp.Config) *pigmcp.Manager {
	t.Helper()
	connects := 0
	manager := pigmcp.NewManager(config, pigmcp.ManagerOptions{
		TransportFactory: func(server string, cfg pigmcp.ServerConfig) (gomcp.Transport, error) {
			if server != serverName {
				return nil, fmt.Errorf("unexpected server %q", server)
			}
			connects++
			clientTransport, serverTransport := gomcp.NewInMemoryTransports()
			srv := gomcp.NewServer(&gomcp.Implementation{Name: "fake-" + serverName, Version: "1.0.0"}, nil)
			type echoInput struct {
				Message string `json:"message"`
			}
			gomcp.AddTool(srv, &gomcp.Tool{Name: "echo", Description: "echo the message back"}, func(_ context.Context, _ *gomcp.CallToolRequest, in echoInput) (*gomcp.CallToolResult, any, error) {
				return &gomcp.CallToolResult{Content: []gomcp.Content{&gomcp.TextContent{Text: "echo: " + in.Message}}}, nil, nil
			})
			gomcp.AddTool(srv, &gomcp.Tool{Name: "fail", Description: "always fails"}, func(_ context.Context, _ *gomcp.CallToolRequest, _ struct{}) (*gomcp.CallToolResult, any, error) {
				return nil, nil, errors.New("boom")
			})
			go func() { _ = srv.Run(context.Background(), serverTransport) }()
			return clientTransport, nil
		},
	})
	t.Cleanup(func() { _ = manager.Close() })
	if connects != 0 {
		t.Fatal("manager must be lazy")
	}
	return manager
}

func writeMCPConfigFile(t *testing.T, path, json string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(json), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestMCPConfigFileChainLaterWins(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("PIG_CODING_AGENT_DIR", "")
	manager := newMCPFakeServerManager(t, "fs", nil) // keep fake factory unused; chain test builds its own managers below

	writeMCPConfigFile(t, filepath.Join(tempDir, ".mcp.json"), `{"mcpServers": {"root": {"command": "root-cmd"}}}`)
	writeMCPConfigFile(t, filepath.Join(tempDir, ".pig", "mcp.json"), `{"mcpServers": {"project": {"command": "project-cmd"}}}`)

	t.Run("project files load when trusted", func(t *testing.T) {
		writeMCPConfigFile(t, filepath.Join(tempDir, "agent", "mcp.json"), `{"mcpServers": {"user": {"command": "user-cmd"}}}`)
		m, diags := createMCPManager(tempDir, filepath.Join(tempDir, "agent"), true)
		if m == nil {
			t.Fatal("expected a manager")
		}
		if diags != nil {
			t.Fatalf("expected no diagnostics, got %+v", diags)
		}
		if got := strings.Join(m.ServerNames(), ","); got != "project,root,user" {
			t.Fatalf("unexpected servers: %q", got)
		}
		_ = m.Close()
	})

	t.Run("project files are skipped when untrusted", func(t *testing.T) {
		// The user-level file still loads: trust gates project-local files only.
		m, diags := createMCPManager(tempDir, filepath.Join(tempDir, "agent"), false)
		if m == nil {
			t.Fatal("user-level config must load regardless of project trust")
		}
		_ = m.Close()
		if got := strings.Join(m.ServerNames(), ","); got != "user" {
			t.Fatalf("project servers must be skipped, got %v", m.ServerNames())
		}
		var warnings []string
		for _, d := range diags {
			warnings = append(warnings, d.Message)
		}
		joined := strings.Join(warnings, "\n")
		if !strings.Contains(joined, ".mcp.json") || !strings.Contains(joined, "not trusted") {
			t.Fatalf("expected untrusted-project warnings, got %+v", diags)
		}
	})

	t.Run("later files win per server", func(t *testing.T) {
		writeMCPConfigFile(t, filepath.Join(tempDir, "agent", "mcp.json"), `{"mcpServers": {"root": {"command": "user-cmd"}}}`)
		m, _ := createMCPManager(tempDir, filepath.Join(tempDir, "agent"), true)
		if m == nil {
			t.Fatal("expected a manager")
		}
		defer m.Close()
		status := map[string]string{}
		for _, s := range m.Status() {
			status[s.Name] = s.Lifecycle // placeholder read to force the map
		}
		if _, ok := status["user"]; ok {
			t.Fatal("user file was overwritten by the later-wins assertion file")
		}
		if len(m.ServerNames()) != 2 { // root (from user file) + project
			t.Fatalf("unexpected server set: %v", m.ServerNames())
		}
	})

	t.Run("global config path seam participates", func(t *testing.T) {
		original := mcpGlobalConfigPathFn
		mcpGlobalConfigPathFn = func() string { return filepath.Join(tempDir, "global-mcp.json") }
		t.Cleanup(func() { mcpGlobalConfigPathFn = original })
		writeMCPConfigFile(t, filepath.Join(tempDir, "global-mcp.json"), `{"mcpServers": {"global": {"command": "global-cmd"}}}`)
		m, _ := createMCPManager(tempDir, filepath.Join(tempDir, "agent"), false)
		if m == nil {
			t.Fatal("global config must load even for untrusted projects")
		}
		defer m.Close()
		// "root" survives from the user-level file written earlier.
		if got := strings.Join(m.ServerNames(), ","); got != "global,root" {
			t.Fatalf("expected global + user servers, got %v", m.ServerNames())
		}
	})

	_ = manager // lazy by construction; asserted above
}

func TestMCPTrustRequiringResources(t *testing.T) {
	tempDir := t.TempDir()

	if HasTrustRequiringProjectResources(tempDir) {
		t.Fatal("empty project must not be trust-requiring")
	}
	writeMCPConfigFile(t, filepath.Join(tempDir, ".mcp.json"), `{"mcpServers": {}}`)
	if !HasTrustRequiringProjectResources(tempDir) {
		t.Fatal("root .mcp.json must gate project trust")
	}

	pigDir := filepath.Join(tempDir, ".mcp-json-case") // separate cwd
	writeMCPConfigFile(t, filepath.Join(pigDir, ".pig", "mcp.json"), `{"mcpServers": {}}`)
	if !HasTrustRequiringProjectResources(pigDir) {
		t.Fatal(".pig/mcp.json must gate project trust")
	}
}

func TestMCPProxyToolRegistrationAndActivation(t *testing.T) {
	tempDir := t.TempDir()
	fake := newMCPFakeServerManager(t, "fs", &pigmcp.Config{MCPServers: map[string]pigmcp.ServerConfig{
		"fs": {Command: "unused"},
	}})

	sessionManager, err := InMemorySession(tempDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	result, err := CreateAgentSession(context.Background(), &CreateAgentSessionOptions{
		Cwd:            tempDir,
		AgentDir:       tempDir,
		SessionManager: sessionManager,
		MCPManager:     fake,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer result.Session.Dispose()

	definition := result.Session.GetToolDefinition("mcp")
	if definition == nil {
		t.Fatal("mcp proxy tool must be registered")
	}
	if definition.PromptSnippet == "" {
		t.Fatal("mcp proxy tool must contribute a prompt snippet")
	}
	active := result.Session.GetActiveToolNames()
	found := false
	for _, name := range active {
		if name == "mcp" {
			found = true
		}
	}
	if !found {
		t.Fatalf("mcp proxy tool must be active by default, got %v", active)
	}

	// Reload rebuilds the registry; the manager (and with it the proxy tool)
	// must survive.
	if err := result.Session.Reload(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if result.Session.GetToolDefinition("mcp") == nil {
		t.Fatal("mcp proxy tool must survive reload")
	}
}

func TestMCPProxyToolExecuteDispatch(t *testing.T) {
	fake := newMCPFakeServerManager(t, "fs", &pigmcp.Config{MCPServers: map[string]pigmcp.ServerConfig{
		"fs": {Command: "unused"},
	}})
	definition := createMCPProxyToolDefinition(fake)
	ctx := context.Background()

	t.Run("status", func(t *testing.T) {
		result, err := definition.Execute(ctx, "id1", map[string]any{"status": true}, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		text := resultText(result)
		if !strings.Contains(text, "fs") || !strings.Contains(text, "disconnected") {
			t.Fatalf("unexpected status output: %q", text)
		}
	})

	t.Run("search then describe", func(t *testing.T) {
		result, err := definition.Execute(ctx, "id2", map[string]any{"search": ""}, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		text := resultText(result)
		if !strings.Contains(text, `echo (server "fs")`) || !strings.Contains(text, "echo the message back") {
			t.Fatalf("unexpected search output: %q", text)
		}

		result, err = definition.Execute(ctx, "id3", map[string]any{"describe": "fs__echo"}, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if text := resultText(result); !strings.Contains(text, "message") {
			t.Fatalf("describe must include the input schema, got %q", text)
		}
	})

	t.Run("call with full name, bare name and error surface", func(t *testing.T) {
		result, err := definition.Execute(ctx, "id4", map[string]any{"tool": "fs__echo", "args": map[string]any{"message": "hi"}}, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if text := resultText(result); text != "echo: hi" {
			t.Fatalf("unexpected call result: %q", text)
		}

		result, err = definition.Execute(ctx, "id5", map[string]any{"tool": "echo", "args": map[string]any{"message": "bare"}}, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if text := resultText(result); text != "echo: bare" {
			t.Fatalf("bare name must resolve: %q", text)
		}

		_, err = definition.Execute(ctx, "id6", map[string]any{"tool": "fs__fail"}, nil, nil)
		if err == nil || !strings.Contains(err.Error(), "boom") {
			t.Fatalf("IsError result must surface as a Go error, got %v", err)
		}
	})

	t.Run("no action errors", func(t *testing.T) {
		if _, err := definition.Execute(ctx, "id7", map[string]any{}, nil, nil); err == nil {
			t.Fatal("empty input must error")
		}
	})
}

func TestMCPDisposeClosesManager(t *testing.T) {
	tempDir := t.TempDir()
	fake := newMCPFakeServerManager(t, "fs", &pigmcp.Config{MCPServers: map[string]pigmcp.ServerConfig{
		"fs": {Command: "unused"},
	}})
	sessionManager, err := InMemorySession(tempDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	result, err := CreateAgentSession(context.Background(), &CreateAgentSessionOptions{
		Cwd:            tempDir,
		AgentDir:       tempDir,
		SessionManager: sessionManager,
		MCPManager:     fake,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Force a connect so Close has something to tear down.
	if _, err := fake.SearchTools(context.Background(), "", 0); err != nil {
		t.Fatal(err)
	}
	result.Session.Dispose()

	definition := result.Session.GetToolDefinition("mcp")
	if definition == nil {
		t.Fatal("definition lookup must still work after dispose")
	}
	if _, err := fake.CallTool(context.Background(), "fs", "echo", nil); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("manager must be closed after session Dispose, got %v", err)
	}
}

func resultText(result agent.AgentToolResult) string {
	var parts []string
	for _, content := range result.Content {
		if text, ok := content.(*types.TextContent); ok {
			parts = append(parts, text.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// ---- proxy tool call-row renderer (pig-only) ----

// mcpIdentityTheme is a UITheme fake that returns text unstyled, so title
// assertions are plain substring checks.
type mcpIdentityTheme struct{}

func (mcpIdentityTheme) Fg(color string, text string) string { return text }
func (mcpIdentityTheme) Bg(color string, text string) string { return text }
func (mcpIdentityTheme) Bold(text string) string             { return text }
func (mcpIdentityTheme) Italic(text string) string           { return text }
func (mcpIdentityTheme) Underline(text string) string        { return text }
func (mcpIdentityTheme) Inverse(text string) string          { return text }
func (mcpIdentityTheme) Strikethrough(text string) string    { return text }
func (mcpIdentityTheme) GetFgAnsi(color string) string       { return "" }
func (mcpIdentityTheme) GetBgAnsi(color string) string       { return "" }

func TestMCPProxyRenderCallShowsToolAndServer(t *testing.T) {
	theme := mcpIdentityTheme{}
	cases := []struct {
		name string
		args map[string]any
		want string
	}{
		{"server__tool full form", map[string]any{"tool": "amap-maps__maps_geo"}, "maps_geo (amap-maps)"},
		{"bare tool name", map[string]any{"tool": "maps_geo"}, "maps_geo"},
		{"search with query", map[string]any{"search": "weather"}, `search "weather"`},
		{"search empty lists all", map[string]any{"search": ""}, "search (all tools)"},
		{"describe", map[string]any{"describe": "maps_geo"}, "describe maps_geo"},
		{"status", map[string]any{"status": true}, "status"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			component := mcpRenderCall(tc.args, theme, tools.ToolRenderContext{})
			text, ok := component.(*tui.Text)
			if !ok {
				t.Fatalf("expected *tui.Text, got %T", component)
			}
			got := strings.Join(text.Render(200), "\n")
			if !strings.Contains(got, "mcp") {
				t.Fatalf("title missing proxy label %q:\n%s", "mcp", got)
			}
			if !strings.Contains(got, tc.want) {
				t.Fatalf("title missing %q:\n%s", tc.want, got)
			}
		})
	}
}

func TestMCPProxyRenderCallDegradesGracefully(t *testing.T) {
	theme := mcpIdentityTheme{}
	// Streaming may deliver partial args (no action fields yet) or
	// non-map garbage; the renderer must not panic and must keep the bare
	// "mcp" label (fallback parity).
	for _, args := range []any{map[string]any{}, nil, "garbage", 42} {
		component := mcpRenderCall(args, theme, tools.ToolRenderContext{})
		text, ok := component.(*tui.Text)
		if !ok {
			t.Fatalf("expected *tui.Text, got %T", component)
		}
		got := strings.Join(text.Render(200), "\n")
		if strings.TrimSpace(got) != "mcp" {
			t.Fatalf("args %#v: want bare label, got %q", args, got)
		}
	}
}

func TestMCPProxyRenderCallReusesLastComponent(t *testing.T) {
	theme := mcpIdentityTheme{}
	last := tui.NewText("old", 0, 0)
	component := mcpRenderCall(map[string]any{"tool": "amap-maps__maps_weather"}, theme, tools.ToolRenderContext{LastComponent: last})
	if component != tui.Component(last) {
		t.Fatalf("renderer must reuse the LastComponent render slot")
	}
}

// ---- proxy tool result renderer (pi v1.0.0 0582d9c11) ----

// The collapsed MCP result preview is limited to visual (wrapped) lines: MCP
// results are often one long JSON line that used to wrap across the whole
// screen (pi #10255-adjacent fix 0582d9c11).
func TestMCPProxyRenderResultCollapsesToVisualLines(t *testing.T) {
	// KeyHint renders through the global theme (renderers_bash parity).
	themePkg.InitTheme("dark", false)
	theme := mcpIdentityTheme{}
	result := tools.ToolRenderResult{Content: []types.Content{
		&types.TextContent{Kind: types.KindText, Text: strings.Repeat("x", 300)},
	}}
	component := mcpRenderResult(result, tools.ToolRenderResultOptions{}, theme, tools.ToolRenderContext{})
	container, ok := component.(*mcpResultRenderComponent)
	if !ok {
		t.Fatalf("expected *mcpResultRenderComponent, got %T", component)
	}
	rendered := container.Render(40)
	// 1 spacer + 5 visual preview lines + 1 hint.
	if len(rendered) != 7 {
		t.Fatalf("rendered %d lines at width 40, want 7:\n%s", len(rendered), strings.Join(rendered, "\n"))
	}
	output := strings.Join(rendered, "\n")
	if !strings.Contains(output, "... (3 more lines,") {
		t.Fatalf("hint missing hidden-line count:\n%s", output)
	}
}

func TestMCPProxyRenderResultExpandedShowsAll(t *testing.T) {
	themePkg.InitTheme("dark", false)
	theme := mcpIdentityTheme{}
	result := tools.ToolRenderResult{Content: []types.Content{
		&types.TextContent{Kind: types.KindText, Text: strings.Repeat("x", 300)},
	}}
	component := mcpRenderResult(result, tools.ToolRenderResultOptions{Expanded: true}, theme, tools.ToolRenderContext{})
	container, ok := component.(*mcpResultRenderComponent)
	if !ok {
		t.Fatalf("expected *mcpResultRenderComponent, got %T", component)
	}
	rendered := container.Render(40)
	output := strings.Join(rendered, "\n")
	if strings.Contains(output, "more lines") {
		t.Fatalf("expanded view must not carry a hint:\n%s", output)
	}
	if total := strings.Count(strings.ReplaceAll(output, "\n", ""), "x"); total != 300 {
		t.Fatalf("expanded view shows %d of 300 chars", total)
	}
}

func TestMCPProxyRenderResultReusesLastComponent(t *testing.T) {
	themePkg.InitTheme("dark", false)
	theme := mcpIdentityTheme{}
	last := &mcpResultRenderComponent{}
	last.AddChild(tui.NewText("old", 0, 0))
	result := tools.ToolRenderResult{Content: []types.Content{
		&types.TextContent{Kind: types.KindText, Text: "done"},
	}}
	component := mcpRenderResult(result, tools.ToolRenderResultOptions{}, theme, tools.ToolRenderContext{LastComponent: last})
	if component != tui.Component(last) {
		t.Fatalf("renderer must reuse the LastComponent render slot")
	}
	if len(last.Children) != 2 { // spacer + text; "old" cleared
		t.Fatalf("expected 2 children after clear+rebuild, got %d", len(last.Children))
	}
}

func TestMCPProxyRenderResultEmptyKeepsComponent(t *testing.T) {
	theme := mcpIdentityTheme{}
	component := mcpRenderResult(tools.ToolRenderResult{}, tools.ToolRenderResultOptions{}, theme, tools.ToolRenderContext{})
	container, ok := component.(*mcpResultRenderComponent)
	if !ok {
		t.Fatalf("expected *mcpResultRenderComponent, got %T", component)
	}
	if len(container.Children) != 0 {
		t.Fatalf("empty result must not add children, got %d", len(container.Children))
	}
}
