package mcp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Tests for #10277 (pi 1387af7b4): project mcp.json entries without
// `command`/`url` override only `disabled` of a server defined in a later
// (global/user-level) file of the chain.

func TestProjectOverrideMergesDisabled(t *testing.T) {
	dir := t.TempDir()
	global := filepath.Join(dir, "mcp.json")
	project := filepath.Join(dir, "project-mcp.json")
	if err := os.WriteFile(global, []byte(`{"mcpServers":{"tools":{"command":"x","env":{"TOKEN":"secret"}}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	// An override cannot change the command, which would run with the global env.
	if err := os.WriteFile(project, []byte(`{"mcpServers":{"tools":{"disabled":true,"args":["y"]},"missing":{"disabled":true}}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	config, diags, err := LoadConfigFileChain([]ConfigFile{
		{Path: project, Project: true},
		{Path: global},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(config.MCPServers) != 1 {
		t.Fatalf("broken overrides must be dropped, got %v", config.SortedServerNames())
	}
	tools := config.MCPServers["tools"]
	if tools.Command != "x" || len(tools.Env) != 1 || tools.Env["TOKEN"] != "secret" {
		t.Fatalf("the broken override must leave the global definition untouched, got %+v", tools)
	}
	if _, overridden := config.Overrides["tools"]; overridden {
		t.Fatalf("broken override must not be recorded, got %v", config.Overrides)
	}
	joined := diagMessages(diags)
	if !strings.Contains(joined, `server "tools": an override can only set disabled`) {
		t.Fatalf("expected the extra-keys diagnostic, got %+v", diags)
	}
	if !strings.Contains(joined, `server "missing" needs "command" or "url", or a global server to override`) {
		t.Fatalf("expected the missing-base diagnostic, got %+v", diags)
	}

	// A clean override merges onto the global definition and records the path.
	if err := os.WriteFile(project, []byte(`{"mcpServers":{"tools":{"disabled":true}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	config, diags, err = LoadConfigFileChain([]ConfigFile{
		{Path: project, Project: true},
		{Path: global},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %+v", diags)
	}
	tools = config.MCPServers["tools"]
	if tools.Command != "x" || tools.Env["TOKEN"] != "secret" || !tools.Disabled {
		t.Fatalf("override must disable the global server, got %+v", tools)
	}
	if config.Overrides["tools"] != project {
		t.Fatalf("override path must be recorded, got %v", config.Overrides)
	}
	if config.Sources["tools"] != global {
		t.Fatalf("source stays the defining file, got %q", config.Sources["tools"])
	}
}

func diagMessages(diags []Diagnostic) string {
	var parts []string
	for _, d := range diags {
		parts = append(parts, d.Message)
	}
	return strings.Join(parts, "\n")
}

func TestProjectOverrideNonProjectScopeIsError(t *testing.T) {
	dir := t.TempDir()
	global := filepath.Join(dir, "mcp.json")
	if err := os.WriteFile(global, []byte(`{"mcpServers":{"tools":{"disabled":true}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	// Without project scope the entry is a normal (invalid) definition: it
	// must not silently become an override.
	config, diags, err := LoadConfigFileChain([]ConfigFile{{Path: global}})
	if err != nil {
		t.Fatal(err)
	}
	if len(config.MCPServers) != 0 {
		t.Fatalf("command-less entry must not load as a definition, got %v", config.MCPServers)
	}
	if !strings.Contains(diagMessages(diags), "neither command nor url") {
		t.Fatalf("expected the transport validation diagnostic, got %+v", diags)
	}
}

func TestOverrideServerDisableWritesProjectFile(t *testing.T) {
	dir := t.TempDir()
	global := filepath.Join(dir, "mcp.json")
	project := filepath.Join(dir, "project-mcp.json")
	if err := os.WriteFile(global, []byte(`{"mcpServers":{"tools":{"command":"x"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(project, []byte(`{"mcpServers":{"tools":{"disabled":true}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	config, _, err := LoadConfigFileChain([]ConfigFile{
		{Path: project, Project: true},
		{Path: global},
	})
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeServer{name: "tools"}
	manager := newTestManager(t, config, ManagerOptions{TransportFactory: fake.spawn(t.Context())})

	// Plain enable writes the override file and keeps the explicit value
	// (pi: keepDefaults — the override's value replaces the global's).
	if err := manager.SetServerDisabled("tools", false); err != nil {
		t.Fatal(err)
	}
	assertProjectFile(t, project, `"disabled": false`)
	if status := manager.Status()[0]; status.Disabled {
		t.Fatal("server must be enabled in memory")
	}

	// Disabling again writes true.
	if err := manager.SetServerDisabled("tools", true); err != nil {
		t.Fatal(err)
	}
	assertProjectFile(t, project, `"disabled": true`)
	if status := manager.Status()[0]; status.Status != statusDisabled || status.OverridePath != project {
		t.Fatalf("override path must surface in status, got %+v", status)
	}
}

func assertProjectFile(t *testing.T, path, want string) {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), want) {
		t.Fatalf("project file must contain %s, got:\n%s", want, content)
	}
}

func TestSetServerDisabledInProjectCreatesFile(t *testing.T) {
	dir := t.TempDir()
	global := filepath.Join(dir, "mcp.json")
	project := filepath.Join(dir, "does-not-exist-yet", "mcp.json")
	if err := os.WriteFile(global, []byte(`{"mcpServers":{"tools":{"command":"x","lifecycle":"eager"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	config, _, err := LoadConfigFileChain([]ConfigFile{{Path: global}})
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeServer{name: "tools"}
	manager := newTestManager(t, config, ManagerOptions{
		TransportFactory:  fake.spawn(t.Context()),
		ProjectConfigPath: project,
	})

	if err := manager.SetServerDisabledInProject("tools", true); err != nil {
		t.Fatal(err)
	}
	assertProjectFile(t, project, `"disabled": true`)
	if status := manager.Status()[0]; !status.Disabled || status.OverridePath != project || status.ProjectOverridable {
		t.Fatalf("overridden server must lose its project-toggle offer, got %+v", status)
	}

	// A later plain toggle also targets the project file (pi: entry.override).
	if err := manager.SetServerDisabled("tools", false); err != nil {
		t.Fatal(err)
	}
	assertProjectFile(t, project, `"disabled": false`)

	// Without a project config target the action errors clearly.
	bare := newTestManager(t, config, ManagerOptions{TransportFactory: fake.spawn(t.Context())})
	if err := bare.SetServerDisabledInProject("tools", true); err == nil || !strings.Contains(err.Error(), "no project mcp.json") {
		t.Fatalf("expected the no-project-config error, got %v", err)
	}
}

func TestProjectOverridableStatusFlag(t *testing.T) {
	dir := t.TempDir()
	global := filepath.Join(dir, "mcp.json")
	project := filepath.Join(dir, "project-mcp.json")
	if err := os.WriteFile(global, []byte(`{"mcpServers":{"a":{"command":"a"},"b":{"command":"b"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(project, []byte(`{"mcpServers":{"a":{"disabled":true}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	config, _, err := LoadConfigFileChain([]ConfigFile{
		{Path: project, Project: true},
		{Path: global},
	})
	if err != nil {
		t.Fatal(err)
	}
	manager := newTestManager(t, config, ManagerOptions{ProjectConfigPath: project})
	statuses := map[string]ServerStatus{}
	for _, s := range manager.Status() {
		statuses[s.Name] = s
	}
	if statuses["a"].ProjectOverridable {
		t.Fatal("already-overridden server must not offer the project toggle")
	}
	if !statuses["b"].ProjectOverridable {
		t.Fatal("global server must offer the project toggle")
	}

	// A manager without a project target offers nothing.
	bare := newTestManager(t, config, ManagerOptions{})
	for _, s := range bare.Status() {
		if s.ProjectOverridable {
			t.Fatal("project toggle must be unavailable without a project config path")
		}
	}
}
