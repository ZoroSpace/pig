// pi: test/settings-diagnostics.test.ts — settings diagnostics collection
// and dedup.
package core

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestSettingsDiagnostics(t *testing.T) {
	t.Run("includes the settings file path for file-backed storage", func(t *testing.T) {
		agentDir, projectDir := settingsTestDirs(t)
		settingsPath := filepath.Join(agentDir, "settings.json")
		writeSettingsFile(t, settingsPath, "{")

		diagnostics := CollectSettingsDiagnostics(CreateSettingsManager(projectDir, agentDir, nil))

		if len(diagnostics) != 1 {
			t.Fatalf("expected 1 diagnostic, got %v", diagnostics)
		}
		if diagnostics[0].Type != "warning" {
			t.Fatalf("type = %q", diagnostics[0].Type)
		}
		if !strings.HasPrefix(diagnostics[0].Message, "Invalid settings file "+settingsPath+":") {
			t.Fatalf("message = %q", diagnostics[0].Message)
		}
	})

	t.Run("falls back to the settings scope for storage without file paths", func(t *testing.T) {
		storage := erroringSettingsStorage{}

		diagnostics := CollectSettingsDiagnostics(SettingsManagerFromStorage(storage, nil))

		if len(diagnostics) != 1 {
			t.Fatalf("expected 1 diagnostic, got %v", diagnostics)
		}
		if diagnostics[0] != (AgentSessionRuntimeDiagnostic{Type: "warning", Message: "Invalid global settings: backend failed"}) {
			t.Fatalf("diagnostic = %+v", diagnostics[0])
		}
	})

	t.Run("deduplicates diagnostics by type and message", func(t *testing.T) {
		warning := AgentSessionRuntimeDiagnostic{Type: "warning", Message: "Invalid settings file /tmp/settings.json"}

		got := DeduplicateDiagnostics([]AgentSessionRuntimeDiagnostic{
			warning,
			warning,
			{Type: "error", Message: warning.Message},
		})

		if len(got) != 2 {
			t.Fatalf("expected 2 diagnostics, got %v", got)
		}
		if got[0] != warning {
			t.Fatalf("got[0] = %+v", got[0])
		}
		if got[1] != (AgentSessionRuntimeDiagnostic{Type: "error", Message: warning.Message}) {
			t.Fatalf("got[1] = %+v", got[1])
		}
	})
}

// erroringSettingsStorage fails global loads and yields empty project
// content (pi: inline SettingsStorage object literal).
type erroringSettingsStorage struct{}

func (erroringSettingsStorage) WithLock(scope SettingsScope, fn func(current *string) *string) error {
	if scope == SettingsScopeGlobal {
		return &forkedError{"backend failed"}
	}
	_ = fn(nil)
	return nil
}

// forkedError is a plain error with a fixed message.
type forkedError struct{ message string }

func (e *forkedError) Error() string { return e.message }
