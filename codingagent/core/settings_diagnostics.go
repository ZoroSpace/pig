// pi: src/core/settings-diagnostics.ts — settings load-error diagnostics.
//
// AgentSessionRuntimeDiagnostic's canonical home is pi's
// agent-session-services.ts; pig declares it here ahead of that batch (the
// agent-session batch moves nothing — the type is consumed across
// settings-diagnostics and agent-session-services alike). The union
// "info" | "warning" | "error" collapses to a string Type field.
package core

import "fmt"

// AgentSessionRuntimeDiagnostic is one user-facing runtime diagnostic (pi:
// AgentSessionRuntimeDiagnostic; canonical source
// agent-session-services.ts).
type AgentSessionRuntimeDiagnostic struct {
	Type    string // "info" | "warning" | "error"
	Message string
}

// CollectSettingsDiagnostics drains settings errors into warnings (pi:
// collectSettingsDiagnostics).
func CollectSettingsDiagnostics(settingsManager *SettingsManager) []AgentSessionRuntimeDiagnostic {
	errors := settingsManager.DrainErrors()
	diagnostics := make([]AgentSessionRuntimeDiagnostic, 0, len(errors))
	for _, settingsError := range errors {
		if settingsError.Path != "" {
			diagnostics = append(diagnostics, AgentSessionRuntimeDiagnostic{
				Type:    "warning",
				Message: fmt.Sprintf("Invalid settings file %s: %s", settingsError.Path, settingsError.Error.Error()),
			})
		} else {
			diagnostics = append(diagnostics, AgentSessionRuntimeDiagnostic{
				Type:    "warning",
				Message: fmt.Sprintf("Invalid %s settings: %s", settingsError.Scope, settingsError.Error.Error()),
			})
		}
	}
	return diagnostics
}

// DeduplicateDiagnostics removes duplicate type/message diagnostics while
// preserving their first occurrence (pi: deduplicateDiagnostics). Startup
// and runtime settings managers can report the same file error.
func DeduplicateDiagnostics(diagnostics []AgentSessionRuntimeDiagnostic) []AgentSessionRuntimeDiagnostic {
	seen := make(map[string]struct{}, len(diagnostics))
	result := make([]AgentSessionRuntimeDiagnostic, 0, len(diagnostics))
	for _, diagnostic := range diagnostics {
		key := diagnostic.Type + "\x00" + diagnostic.Message
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, diagnostic)
	}
	return result
}
