// pi: test/trust-manager.test.ts — ProjectTrustStore persistence and
// trust-requiring resource detection.
package core

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProjectTrustStore(t *testing.T) {
	t.Run("stores decisions and inherits from parent directories", func(t *testing.T) {
		tempDir := t.TempDir()
		agentDir := filepath.Join(tempDir, "agent")
		cwd := filepath.Join(tempDir, "project")
		if err := os.MkdirAll(agentDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(cwd, 0o755); err != nil {
			t.Fatal(err)
		}

		store := NewProjectTrustStore(agentDir)
		parentDir := filepath.Join(tempDir, "trusted-parent")
		childDir := filepath.Join(parentDir, "project")
		if err := os.MkdirAll(childDir, 0o755); err != nil {
			t.Fatal(err)
		}

		decision, err := store.Get(childDir)
		if err != nil {
			t.Fatal(err)
		}
		if decision != nil {
			t.Fatalf("decision = %v, want nil", *decision)
		}
		if err := store.Set(parentDir, codingagentBoolPtr(true)); err != nil {
			t.Fatal(err)
		}
		decision, err = store.Get(childDir)
		if err != nil {
			t.Fatal(err)
		}
		if decision == nil || !*decision {
			t.Fatalf("decision = %v, want true", decision)
		}
		if err := store.Set(childDir, codingagentBoolPtr(false)); err != nil {
			t.Fatal(err)
		}
		decision, err = store.Get(childDir)
		if err != nil {
			t.Fatal(err)
		}
		if decision == nil || *decision {
			t.Fatalf("decision = %v, want false", decision)
		}
		if err := store.Set(childDir, nil); err != nil {
			t.Fatal(err)
		}
		decision, err = store.Get(childDir)
		if err != nil {
			t.Fatal(err)
		}
		if decision == nil || !*decision {
			t.Fatalf("decision = %v, want inherited true", decision)
		}
	})

	t.Run("detects trust-requiring project resources", func(t *testing.T) {
		tempDir := t.TempDir()
		agentDir := filepath.Join(tempDir, "agent")
		cwd := filepath.Join(tempDir, "project")
		if err := os.MkdirAll(agentDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(cwd, 0o755); err != nil {
			t.Fatal(err)
		}

		// pi pins process.env.HOME for the user ~/.agents/skills exclusion.
		t.Setenv("HOME", tempDir)

		if err := os.MkdirAll(filepath.Join(tempDir, ".pig", "agent"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(tempDir, ".agents", "skills"), 0o755); err != nil {
			t.Fatal(err)
		}
		if HasTrustRequiringProjectResources(tempDir) {
			t.Fatal("user-level .agents/skills must not trigger trust")
		}
		if HasTrustRequiringProjectResources(cwd) {
			t.Fatal("no project resources yet")
		}

		if err := os.WriteFile(filepath.Join(tempDir, ".pig", "settings.json"), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
		if !HasTrustRequiringProjectResources(tempDir) {
			t.Fatal("expected .pig/settings.json to trigger trust")
		}
		if err := os.Remove(filepath.Join(tempDir, ".pig", "settings.json")); err != nil {
			t.Fatal(err)
		}

		if err := os.MkdirAll(filepath.Join(cwd, ".pig"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(cwd, ".pig", "settings.json"), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
		if !HasTrustRequiringProjectResources(cwd) {
			t.Fatal("expected project .pig/settings.json to trigger trust")
		}

		if err := os.RemoveAll(filepath.Join(cwd, ".pig")); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(cwd, ".agents", "skills"), 0o755); err != nil {
			t.Fatal(err)
		}
		if !HasTrustRequiringProjectResources(cwd) {
			t.Fatal("expected project .agents/skills to trigger trust")
		}
	})
}

// codingagentBoolPtr keeps the test-local helper name distinct from the
// settings test's boolPtr.
func codingagentBoolPtr(value bool) *bool {
	return &value
}
