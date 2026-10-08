// pi: test/settings-manager.test.ts defaultTools group (+name/-name syntax,
// 30a1d1849) and test/default-tools-setting.test.ts resolution semantics.
package core

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestDefaultToolsPlusMinusSyntax(t *testing.T) {
	t.Run("applies +name and -name to the default selection", func(t *testing.T) {
		manager := InMemorySettingsManager(Settings{"defaultTools": []any{"+codemode", "-write"}}, nil)
		got, ok := manager.GetDefaultTools()
		if !ok {
			t.Fatal("expected defaultTools to be set")
		}
		want := []string{"read", "bash", "edit", "codemode"}
		if !slices.Equal(got, want) {
			t.Fatalf("got %v, want %v", got, want)
		}

		manager = InMemorySettingsManager(Settings{"defaultTools": []any{"read", "+grep", "+read"}}, nil)
		got, _ = manager.GetDefaultTools()
		want = []string{"read", "grep"}
		if !slices.Equal(got, want) {
			t.Fatalf("got %v, want %v", got, want)
		}
	})

	t.Run("only modifiers start from the default tool names", func(t *testing.T) {
		manager := InMemorySettingsManager(Settings{"defaultTools": []any{"-write"}}, nil)
		got, _ := manager.GetDefaultTools()
		want := []string{"read", "bash", "edit"}
		if !slices.Equal(got, want) {
			t.Fatalf("got %v, want %v", got, want)
		}
	})

	t.Run("an empty list is preserved", func(t *testing.T) {
		manager := InMemorySettingsManager(Settings{"defaultTools": []any{}}, nil)
		got, ok := manager.GetDefaultTools()
		if !ok {
			t.Fatal("expected defaultTools to be set")
		}
		if len(got) != 0 {
			t.Fatalf("expected empty selection, got %v", got)
		}
	})

	t.Run("layers project modifiers on top of the global selection", func(t *testing.T) {
		tempDir := newTempDir(t)
		agentDir := filepath.Join(tempDir, "agent")
		projectDir := filepath.Join(tempDir, "project")
		if err := os.MkdirAll(filepath.Join(projectDir, ".pig"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(agentDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(agentDir, "settings.json"),
			[]byte(`{"defaultTools": ["read", "bash", "+codemode"]}`), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(projectDir, ".pig", "settings.json"),
			[]byte(`{"defaultTools": ["-codemode", "+tool_search"]}`), 0o644); err != nil {
			t.Fatal(err)
		}

		manager := CreateSettingsManager(projectDir, agentDir, nil)
		got, ok := manager.GetDefaultTools()
		if !ok {
			t.Fatal("expected defaultTools to be set")
		}
		want := []string{"read", "bash", "tool_search"}
		if !slices.Equal(got, want) {
			t.Fatalf("got %v, want %v", got, want)
		}

		manager.ApplyOverrides(Settings{"defaultTools": []any{"+codemode"}})
		got, _ = manager.GetDefaultTools()
		want = []string{"read", "bash", "tool_search", "codemode"}
		if !slices.Equal(got, want) {
			t.Fatalf("got %v, want %v", got, want)
		}
	})

	t.Run("a non-modifier project list replaces the global one", func(t *testing.T) {
		tempDir := newTempDir(t)
		agentDir := filepath.Join(tempDir, "agent")
		projectDir := filepath.Join(tempDir, "project")
		if err := os.MkdirAll(filepath.Join(projectDir, ".pig"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(agentDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(agentDir, "settings.json"),
			[]byte(`{"defaultTools": ["read", "bash", "+codemode"]}`), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(projectDir, ".pig", "settings.json"),
			[]byte(`{"defaultTools": ["grep"]}`), 0o644); err != nil {
			t.Fatal(err)
		}

		manager := CreateSettingsManager(projectDir, agentDir, nil)
		got, _ := manager.GetDefaultTools()
		want := []string{"grep"}
		if !slices.Equal(got, want) {
			t.Fatalf("got %v, want %v", got, want)
		}
	})

	t.Run("a malformed value resolves to an empty selection", func(t *testing.T) {
		// pi: `Array.isArray(tools) ? tools.filter(...) : []` — a wrong-typed
		// defaultTools yields an empty selection, not the default four tools.
		manager := InMemorySettingsManager(Settings{"defaultTools": "read"}, nil)
		got, ok := manager.GetDefaultTools()
		if !ok {
			t.Fatal("expected defaultTools to be set")
		}
		if len(got) != 0 {
			t.Fatalf("expected empty selection, got %v", got)
		}
	})
}

// pi: test/default-tools-setting.test.ts reload describe (#10245). The
// extension tool registered defaultActive:false is replaced by the built-in
// grep tool — pig has no extension defaultActive face (Phase 7 not ported),
// and grep plays the same "tool that exists but is not initially active"
// role.
func TestDefaultToolsReloadActivatesNewlyAddedTools(t *testing.T) {
	tempDir := newTempDir(t)
	agentDir := filepath.Join(tempDir, "agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeSettings := func(t *testing.T, settings map[string]any) {
		t.Helper()
		data, err := json.Marshal(settings)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	createFileSession := func(t *testing.T, options *CreateAgentSessionOptions) *AgentSession {
		t.Helper()
		settingsManager := CreateSettingsManager(tempDir, agentDir, nil)
		sessionManager, err := InMemorySession(tempDir, nil)
		if err != nil {
			t.Fatal(err)
		}
		opts := &CreateAgentSessionOptions{
			Cwd:             tempDir,
			AgentDir:        agentDir,
			SessionManager:  sessionManager,
			SettingsManager: settingsManager,
		}
		if options != nil {
			opts.Tools = options.Tools
			opts.NoTools = options.NoTools
			opts.ExcludeTools = options.ExcludeTools
		}
		result, err := CreateAgentSession(context.Background(), opts)
		if err != nil {
			t.Fatal(err)
		}
		return result.Session
	}

	t.Run("activates only tools newly added to defaultTools", func(t *testing.T) {
		session := createFileSession(t, nil)
		defer session.Dispose()
		if got := session.GetActiveToolNames(); !slices.Equal(got, []string{"read", "bash", "edit", "write"}) {
			t.Fatalf("initial active tools = %v", got)
		}
		session.SetActiveToolsByName([]string{"read", "edit", "write"})

		writeSettings(t, map[string]any{"defaultTools": []string{"+grep"}})
		if err := session.Reload(context.Background(), nil); err != nil {
			t.Fatal(err)
		}
		// bash was disabled during the session and is not newly added, so it
		// stays off.
		got := session.GetActiveToolNames()
		slices.Sort(got)
		if want := []string{"edit", "grep", "read", "write"}; !slices.Equal(got, want) {
			t.Fatalf("active tools after adding grep = %v, want %v", got, want)
		}

		// Removing tools from the setting does not disable them.
		writeSettings(t, map[string]any{"defaultTools": []string{"-read"}})
		if err := session.Reload(context.Background(), nil); err != nil {
			t.Fatal(err)
		}
		got = session.GetActiveToolNames()
		slices.Sort(got)
		if want := []string{"edit", "grep", "read", "write"}; !slices.Equal(got, want) {
			t.Fatalf("active tools after removing read = %v, want %v", got, want)
		}
	})

	t.Run("keeps tools removed by -name tool options removed on reload", func(t *testing.T) {
		writeSettings(t, map[string]any{"defaultTools": []string{"read"}})
		session := createFileSession(t, &CreateAgentSessionOptions{Tools: []string{"-bash", "+grep"}})
		defer session.Dispose()
		got := session.GetActiveToolNames()
		slices.Sort(got)
		if want := []string{"grep", "read"}; !slices.Equal(got, want) {
			t.Fatalf("active tools = %v, want %v", got, want)
		}

		writeSettings(t, map[string]any{"defaultTools": []string{"read", "bash"}})
		if err := session.Reload(context.Background(), nil); err != nil {
			t.Fatal(err)
		}
		got = session.GetActiveToolNames()
		slices.Sort(got)
		if want := []string{"grep", "read"}; !slices.Equal(got, want) {
			t.Fatalf("active tools after reload = %v, want %v", got, want)
		}
	})

	t.Run("keeps explicit tool options on reload", func(t *testing.T) {
		allowlisted := createFileSession(t, &CreateAgentSessionOptions{Tools: []string{"read"}})
		writeSettings(t, map[string]any{"defaultTools": []string{"+grep"}})
		if err := allowlisted.Reload(context.Background(), nil); err != nil {
			t.Fatal(err)
		}
		if got := allowlisted.GetActiveToolNames(); !slices.Equal(got, []string{"read"}) {
			t.Fatalf("allowlisted active tools = %v, want [read]", got)
		}
		allowlisted.Dispose()

		writeSettings(t, map[string]any{})
		builtinless := createFileSession(t, &CreateAgentSessionOptions{NoTools: "builtin"})
		writeSettings(t, map[string]any{"defaultTools": []string{"+grep"}})
		if err := builtinless.Reload(context.Background(), nil); err != nil {
			t.Fatal(err)
		}
		if got := builtinless.GetActiveToolNames(); len(got) != 0 {
			t.Fatalf("builtinless active tools = %v, want []", got)
		}
		builtinless.Dispose()

		writeSettings(t, map[string]any{})
		excluded := createFileSession(t, &CreateAgentSessionOptions{ExcludeTools: []string{"grep"}})
		writeSettings(t, map[string]any{"defaultTools": []string{"+grep"}})
		if err := excluded.Reload(context.Background(), nil); err != nil {
			t.Fatal(err)
		}
		got := excluded.GetActiveToolNames()
		slices.Sort(got)
		if want := []string{"bash", "edit", "read", "write"}; !slices.Equal(got, want) {
			t.Fatalf("excluded active tools = %v, want %v", got, want)
		}
		excluded.Dispose()
	})
}

// pi: test/default-tools-setting.test.ts "applies +name and -name tool
// options to the default selection" and "rejects invalid tool modifier
// options" (#10549). pig has no extension tools, so the built-in grep/find
// (registered but inactive by default) play the role of pi's inactive_tool.
func TestDefaultToolsToolModifierOptions(t *testing.T) {
	tempDir := newTempDir(t)
	agentDir := filepath.Join(tempDir, "agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeSettings := func(t *testing.T, settings map[string]any) {
		t.Helper()
		data, err := json.Marshal(settings)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	createSession := func(t *testing.T, tools []string, noTools string) (*AgentSession, error) {
		t.Helper()
		settingsManager := CreateSettingsManager(tempDir, agentDir, nil)
		sessionManager, err := InMemorySession(tempDir, nil)
		if err != nil {
			t.Fatal(err)
		}
		result, err := CreateAgentSession(context.Background(), &CreateAgentSessionOptions{
			Cwd:             tempDir,
			AgentDir:        agentDir,
			SessionManager:  sessionManager,
			SettingsManager: settingsManager,
			Tools:           tools,
			NoTools:         noTools,
		})
		if err != nil {
			return nil, err
		}
		return result.Session, nil
	}

	t.Run("applies +name and -name tool options to the default selection", func(t *testing.T) {
		writeSettings(t, map[string]any{"defaultTools": []string{"+grep"}})
		session, err := createSession(t, []string{"-write", "+find"}, "")
		if err != nil {
			t.Fatal(err)
		}
		defer session.Dispose()
		got := session.GetActiveToolNames()
		slices.Sort(got)
		if want := []string{"bash", "edit", "find", "grep", "read"}; !slices.Equal(got, want) {
			t.Fatalf("active tools = %v, want %v", got, want)
		}

		writeSettings(t, map[string]any{"defaultTools": []string{"read"}})
		toolLess, err := createSession(t, []string{"+grep"}, "all")
		if err != nil {
			t.Fatal(err)
		}
		defer toolLess.Dispose()
		if got := toolLess.GetActiveToolNames(); !slices.Equal(got, []string{"grep"}) {
			t.Fatalf("tool-less active tools = %v, want [grep]", got)
		}
	})

	t.Run("rejects invalid tool modifier options", func(t *testing.T) {
		writeSettings(t, map[string]any{})
		if _, err := createSession(t, []string{"read", "+grep"}, ""); err == nil ||
			!strings.Contains(err.Error(), "Invalid tools option: tool names cannot be mixed with +name or -name entries") {
			t.Fatalf("mixed tools error = %v", err)
		}
		if _, err := createSession(t, []string{"-gr*"}, ""); err == nil ||
			!strings.Contains(err.Error(), "+name and -name entries take exact tool names, not patterns: -gr*") {
			t.Fatalf("pattern modifier error = %v", err)
		}
	})
}
