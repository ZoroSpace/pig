// pi: test/settings-manager.test.ts defaultTools group (+name/-name syntax,
// 30a1d1849) and test/default-tools-setting.test.ts resolution semantics.
package core

import (
	"os"
	"path/filepath"
	"slices"
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
