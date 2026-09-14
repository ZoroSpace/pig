// pi: test/settings-manager.test.ts — SettingsManager behavior ported from
// the vitest suite (global/project layering, external-edit preservation,
// trust gating, per-setting getters/setters).
package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ZoroSpace/pig/codingagent"
)

// settingsTestDirs prepares the agent/project directory layout (pi:
// beforeEach).
func settingsTestDirs(t *testing.T) (agentDir string, projectDir string) {
	t.Helper()
	agentDir = filepath.Join(t.TempDir(), "agent")
	projectDir = filepath.Join(filepath.Dir(agentDir), "project")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(projectDir, ".pig"), 0o755); err != nil {
		t.Fatal(err)
	}
	return agentDir, projectDir
}

func writeSettingsFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readSettingsJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	parsed := map[string]any{}
	if err := json.Unmarshal(content, &parsed); err != nil {
		t.Fatal(err)
	}
	return parsed
}

func settingsJSONString(t *testing.T, value map[string]any, key string) string {
	t.Helper()
	raw, ok := value[key].(string)
	if !ok {
		t.Fatalf("expected %s to be a string, got %v", key, value[key])
	}
	return raw
}

func settingsJSONBool(t *testing.T, value map[string]any, key string) bool {
	t.Helper()
	raw, ok := value[key].(bool)
	if !ok {
		t.Fatalf("expected %s to be a bool, got %v", key, value[key])
	}
	return raw
}

func TestSettingsManagerPreservesExternallyAddedSettings(t *testing.T) {
	t.Run("preserves enabledModels when changing thinking level", func(t *testing.T) {
		agentDir, projectDir := settingsTestDirs(t)
		settingsPath := filepath.Join(agentDir, "settings.json")
		writeSettingsFile(t, settingsPath, `{"theme":"dark","defaultModel":"claude-sonnet"}`)

		// Create SettingsManager (simulates pi starting up)
		manager := CreateSettingsManager(projectDir, agentDir, nil)

		// Simulate user editing settings.json externally to add enabledModels
		currentSettings := readSettingsJSON(t, settingsPath)
		currentSettings["enabledModels"] = []any{"claude-opus-4-5", "gpt-5.2-codex"}
		updated, err := json.Marshal(currentSettings)
		if err != nil {
			t.Fatal(err)
		}
		writeSettingsFile(t, settingsPath, string(updated))

		// User changes thinking level via Shift+Tab
		manager.SetDefaultThinkingLevel("high")
		manager.Flush()

		// Verify enabledModels is preserved
		savedSettings := readSettingsJSON(t, settingsPath)
		models, ok := savedSettings["enabledModels"].([]any)
		if !ok || len(models) != 2 || models[0] != "claude-opus-4-5" || models[1] != "gpt-5.2-codex" {
			t.Fatalf("expected enabledModels preserved, got %v", savedSettings["enabledModels"])
		}
		if got := settingsJSONString(t, savedSettings, "defaultThinkingLevel"); got != "high" {
			t.Fatalf("defaultThinkingLevel = %q", got)
		}
		if got := settingsJSONString(t, savedSettings, "theme"); got != "dark" {
			t.Fatalf("theme = %q", got)
		}
		if got := settingsJSONString(t, savedSettings, "defaultModel"); got != "claude-sonnet" {
			t.Fatalf("defaultModel = %q", got)
		}
	})

	t.Run("preserves custom settings when changing theme", func(t *testing.T) {
		agentDir, projectDir := settingsTestDirs(t)
		settingsPath := filepath.Join(agentDir, "settings.json")
		writeSettingsFile(t, settingsPath, `{"defaultModel":"claude-sonnet"}`)

		manager := CreateSettingsManager(projectDir, agentDir, nil)

		// User adds custom settings externally
		currentSettings := readSettingsJSON(t, settingsPath)
		currentSettings["shellPath"] = "/bin/zsh"
		currentSettings["extensions"] = []any{"/path/to/extension.ts"}
		updated, err := json.Marshal(currentSettings)
		if err != nil {
			t.Fatal(err)
		}
		writeSettingsFile(t, settingsPath, string(updated))

		// User changes theme
		manager.SetTheme("light")
		manager.Flush()

		// Verify all settings preserved
		savedSettings := readSettingsJSON(t, settingsPath)
		if got := settingsJSONString(t, savedSettings, "shellPath"); got != "/bin/zsh" {
			t.Fatalf("shellPath = %q", got)
		}
		extensions, ok := savedSettings["extensions"].([]any)
		if !ok || len(extensions) != 1 || extensions[0] != "/path/to/extension.ts" {
			t.Fatalf("extensions = %v", savedSettings["extensions"])
		}
		if got := settingsJSONString(t, savedSettings, "theme"); got != "light" {
			t.Fatalf("theme = %q", got)
		}
	})

	t.Run("lets in-memory changes override file changes for same key", func(t *testing.T) {
		agentDir, projectDir := settingsTestDirs(t)
		settingsPath := filepath.Join(agentDir, "settings.json")
		writeSettingsFile(t, settingsPath, `{"theme":"dark"}`)

		manager := CreateSettingsManager(projectDir, agentDir, nil)

		// User externally sets thinking level to "low"
		currentSettings := readSettingsJSON(t, settingsPath)
		currentSettings["defaultThinkingLevel"] = "low"
		updated, err := json.Marshal(currentSettings)
		if err != nil {
			t.Fatal(err)
		}
		writeSettingsFile(t, settingsPath, string(updated))

		// But then changes it via UI to "high"
		manager.SetDefaultThinkingLevel("high")
		manager.Flush()

		// In-memory change should win
		savedSettings := readSettingsJSON(t, settingsPath)
		if got := settingsJSONString(t, savedSettings, "defaultThinkingLevel"); got != "high" {
			t.Fatalf("defaultThinkingLevel = %q", got)
		}
	})
}

func TestSettingsManagerPackagesMigration(t *testing.T) {
	t.Run("keeps local-only extensions in extensions array", func(t *testing.T) {
		agentDir, projectDir := settingsTestDirs(t)
		settingsPath := filepath.Join(agentDir, "settings.json")
		writeSettingsFile(t, settingsPath, `{"extensions":["/local/ext.ts","./relative/ext.ts"]}`)

		manager := CreateSettingsManager(projectDir, agentDir, nil)

		if packages := manager.GetPackages(); len(packages) != 0 {
			t.Fatalf("expected no packages, got %v", packages)
		}
		extensions := manager.GetExtensionPaths()
		if len(extensions) != 2 || extensions[0] != "/local/ext.ts" || extensions[1] != "./relative/ext.ts" {
			t.Fatalf("extensions = %v", extensions)
		}
	})

	t.Run("handles packages with filtering objects", func(t *testing.T) {
		agentDir, projectDir := settingsTestDirs(t)
		settingsPath := filepath.Join(agentDir, "settings.json")
		writeSettingsFile(t, settingsPath, `{"packages":["npm:simple-pkg",{"source":"npm:shitty-extensions","extensions":["extensions/oracle.ts"],"skills":[]}]}`)

		manager := CreateSettingsManager(projectDir, agentDir, nil)

		packages := manager.GetPackages()
		if len(packages) != 2 {
			t.Fatalf("expected 2 packages, got %v", packages)
		}
		if packages[0].Source != "npm:simple-pkg" || packages[0].ObjectForm {
			t.Fatalf("packages[0] = %+v", packages[0])
		}
		if !packages[1].ObjectForm || packages[1].Source != "npm:shitty-extensions" {
			t.Fatalf("packages[1] = %+v", packages[1])
		}
		if len(packages[1].Extensions) != 1 || packages[1].Extensions[0] != "extensions/oracle.ts" {
			t.Fatalf("packages[1].Extensions = %v", packages[1].Extensions)
		}
		if packages[1].Skills == nil || len(packages[1].Skills) != 0 {
			t.Fatalf("packages[1].Skills = %v (want empty, non-nil)", packages[1].Skills)
		}
	})
}

func TestSettingsManagerReload(t *testing.T) {
	t.Run("reloads global settings from disk", func(t *testing.T) {
		agentDir, projectDir := settingsTestDirs(t)
		settingsPath := filepath.Join(agentDir, "settings.json")
		writeSettingsFile(t, settingsPath, `{"theme":"dark","extensions":["/before.ts"]}`)

		manager := CreateSettingsManager(projectDir, agentDir, nil)

		writeSettingsFile(t, settingsPath, `{"theme":"light","extensions":["/after.ts"],"defaultModel":"claude-sonnet"}`)

		manager.Reload()

		if theme, ok := manager.GetTheme(); !ok || theme != "light" {
			t.Fatalf("theme = %q, %v", theme, ok)
		}
		if extensions := manager.GetExtensionPaths(); len(extensions) != 1 || extensions[0] != "/after.ts" {
			t.Fatalf("extensions = %v", extensions)
		}
		if model, ok := manager.GetDefaultModel(); !ok || model != "claude-sonnet" {
			t.Fatalf("defaultModel = %q, %v", model, ok)
		}
	})

	t.Run("keeps previous settings and reports the file path when the file is invalid", func(t *testing.T) {
		agentDir, projectDir := settingsTestDirs(t)
		settingsPath := filepath.Join(agentDir, "settings.json")
		writeSettingsFile(t, settingsPath, `{"theme":"dark"}`)

		manager := CreateSettingsManager(projectDir, agentDir, nil)

		writeSettingsFile(t, settingsPath, "{ invalid json")
		manager.Reload()

		if theme, ok := manager.GetTheme(); !ok || theme != "dark" {
			t.Fatalf("theme = %q, %v", theme, ok)
		}
		errors := manager.DrainErrors()
		if len(errors) != 1 {
			t.Fatalf("expected 1 error, got %v", errors)
		}
		if errors[0].Scope != SettingsScopeGlobal || errors[0].Path != settingsPath || errors[0].Error == nil {
			t.Fatalf("error = %+v", errors[0])
		}
	})
}

func TestSettingsManagerThemeSetting(t *testing.T) {
	t.Run("stores slash-separated automatic theme settings separately from fixed theme names", func(t *testing.T) {
		agentDir, projectDir := settingsTestDirs(t)
		settingsPath := filepath.Join(agentDir, "settings.json")
		writeSettingsFile(t, settingsPath, `{"theme":"light/dark"}`)

		manager := CreateSettingsManager(projectDir, agentDir, nil)

		if theme, ok := manager.GetTheme(); ok {
			t.Fatalf("expected no fixed theme, got %q", theme)
		}
		if setting, ok := manager.GetThemeSetting(); !ok || setting != "light/dark" {
			t.Fatalf("themeSetting = %q, %v", setting, ok)
		}

		manager.SetTheme("solarized-light/tokyo-night")
		manager.Flush()

		savedSettings := readSettingsJSON(t, settingsPath)
		if got := settingsJSONString(t, savedSettings, "theme"); got != "solarized-light/tokyo-night" {
			t.Fatalf("theme = %q", got)
		}
	})
}

func TestSettingsManagerErrorTracking(t *testing.T) {
	t.Run("collects and clears load errors via drainErrors", func(t *testing.T) {
		agentDir, projectDir := settingsTestDirs(t)
		globalSettingsPath := filepath.Join(agentDir, "settings.json")
		projectSettingsPath := filepath.Join(projectDir, ".pig", "settings.json")
		writeSettingsFile(t, globalSettingsPath, "{ invalid global json")
		writeSettingsFile(t, projectSettingsPath, "{ invalid project json")

		manager := CreateSettingsManager(projectDir, agentDir, nil)
		errors := manager.DrainErrors()

		if len(errors) != 2 {
			t.Fatalf("expected 2 errors, got %v", errors)
		}
		if errors[0].Scope != SettingsScopeGlobal || errors[0].Path != globalSettingsPath {
			t.Fatalf("errors[0] = %+v", errors[0])
		}
		if errors[1].Scope != SettingsScopeProject || errors[1].Path != projectSettingsPath {
			t.Fatalf("errors[1] = %+v", errors[1])
		}
		if again := manager.DrainErrors(); len(again) != 0 {
			t.Fatalf("expected drained errors cleared, got %v", again)
		}
	})
}

func TestSettingsManagerProjectTrust(t *testing.T) {
	t.Run("skips project settings when project is not trusted", func(t *testing.T) {
		agentDir, projectDir := settingsTestDirs(t)
		writeSettingsFile(t, filepath.Join(agentDir, "settings.json"), `{"theme":"global"}`)
		writeSettingsFile(t, filepath.Join(projectDir, ".pig", "settings.json"), `{"theme":"project"}`)

		manager := CreateSettingsManager(projectDir, agentDir, &SettingsManagerCreateOptions{ProjectTrusted: boolPtr(false)})

		if manager.IsProjectTrusted() {
			t.Fatal("expected project untrusted")
		}
		if theme, ok := manager.GetTheme(); !ok || theme != "global" {
			t.Fatalf("theme = %q, %v", theme, ok)
		}
		if projectSettings := manager.GetProjectSettings(); len(projectSettings) != 0 {
			t.Fatalf("projectSettings = %v", projectSettings)
		}
	})

	t.Run("reloads project settings after trust changes to true", func(t *testing.T) {
		agentDir, projectDir := settingsTestDirs(t)
		writeSettingsFile(t, filepath.Join(agentDir, "settings.json"), `{"theme":"global"}`)
		writeSettingsFile(t, filepath.Join(projectDir, ".pig", "settings.json"), `{"theme":"project"}`)
		manager := CreateSettingsManager(projectDir, agentDir, &SettingsManagerCreateOptions{ProjectTrusted: boolPtr(false)})

		manager.SetProjectTrusted(true)

		if !manager.IsProjectTrusted() {
			t.Fatal("expected project trusted")
		}
		if theme, ok := manager.GetTheme(); !ok || theme != "project" {
			t.Fatalf("theme = %q, %v", theme, ok)
		}
	})

	t.Run("fails project settings writes when project is not trusted", func(t *testing.T) {
		agentDir, projectDir := settingsTestDirs(t)
		projectSettingsPath := filepath.Join(projectDir, ".pig", "settings.json")
		writeSettingsFile(t, projectSettingsPath, `{"packages":["npm:existing"]}`)
		manager := CreateSettingsManager(projectDir, agentDir, &SettingsManagerCreateOptions{ProjectTrusted: boolPtr(false)})

		err := manager.SetProjectPackages([]PackageSource{{Source: "npm:new"}})
		if err == nil || err.Error() != "Project is not trusted; refusing to write project settings" {
			t.Fatalf("expected trust error, got %v", err)
		}
		manager.Flush()

		if projectSettings := manager.GetProjectSettings(); len(projectSettings) != 0 {
			t.Fatalf("projectSettings = %v", projectSettings)
		}
		savedSettings := readSettingsJSON(t, projectSettingsPath)
		packages, ok := savedSettings["packages"].([]any)
		if !ok || len(packages) != 1 || packages[0] != "npm:existing" {
			t.Fatalf("packages = %v", savedSettings["packages"])
		}
	})

	t.Run("reads default project trust from global settings only", func(t *testing.T) {
		agentDir, projectDir := settingsTestDirs(t)
		writeSettingsFile(t, filepath.Join(agentDir, "settings.json"), `{"defaultProjectTrust":"always"}`)
		writeSettingsFile(t, filepath.Join(projectDir, ".pig", "settings.json"), `{"defaultProjectTrust":"never"}`)

		manager := CreateSettingsManager(projectDir, agentDir, nil)

		if got := manager.GetDefaultProjectTrust(); got != DefaultProjectTrustAlways {
			t.Fatalf("defaultProjectTrust = %q", got)
		}
	})

	t.Run("defaults invalid project trust settings to ask", func(t *testing.T) {
		agentDir, projectDir := settingsTestDirs(t)
		writeSettingsFile(t, filepath.Join(agentDir, "settings.json"), `{"defaultProjectTrust":"sometimes"}`)

		manager := CreateSettingsManager(projectDir, agentDir, nil)

		if got := manager.GetDefaultProjectTrust(); got != DefaultProjectTrustAsk {
			t.Fatalf("defaultProjectTrust = %q", got)
		}
	})
}

func TestSettingsManagerProjectSettingsDirectoryCreation(t *testing.T) {
	t.Run("does not create .pig folder when only reading project settings", func(t *testing.T) {
		agentDir, projectDir := settingsTestDirs(t)
		// Agent dir with global settings, but NO .pig folder in project
		settingsPath := filepath.Join(agentDir, "settings.json")
		writeSettingsFile(t, settingsPath, `{"theme":"dark"}`)

		// Delete the .pig folder that the setup created
		if err := os.RemoveAll(filepath.Join(projectDir, ".pig")); err != nil {
			t.Fatal(err)
		}

		// Create SettingsManager (reads both global and project settings)
		manager := CreateSettingsManager(projectDir, agentDir, nil)

		// .pig folder should NOT have been created just from reading
		if _, err := os.Stat(filepath.Join(projectDir, ".pig")); !os.IsNotExist(err) {
			t.Fatal("expected .pig folder to not exist")
		}

		// Settings should still be loaded from global
		if theme, ok := manager.GetTheme(); !ok || theme != "dark" {
			t.Fatalf("theme = %q, %v", theme, ok)
		}
	})

	t.Run("creates .pig folder when writing project settings", func(t *testing.T) {
		agentDir, projectDir := settingsTestDirs(t)
		settingsPath := filepath.Join(agentDir, "settings.json")
		writeSettingsFile(t, settingsPath, `{"theme":"dark"}`)

		if err := os.RemoveAll(filepath.Join(projectDir, ".pig")); err != nil {
			t.Fatal(err)
		}

		manager := CreateSettingsManager(projectDir, agentDir, nil)

		if _, err := os.Stat(filepath.Join(projectDir, ".pig")); !os.IsNotExist(err) {
			t.Fatal("expected .pig folder to not exist yet")
		}

		// Write a project-specific setting
		if err := manager.SetProjectPackages([]PackageSource{{Source: "npm:test-pkg", ObjectForm: true}}); err != nil {
			t.Fatal(err)
		}
		manager.Flush()

		if _, err := os.Stat(filepath.Join(projectDir, ".pig")); err != nil {
			t.Fatalf("expected .pig folder to exist: %v", err)
		}
		if _, err := os.Stat(filepath.Join(projectDir, ".pig", "settings.json")); err != nil {
			t.Fatalf("expected project settings file to exist: %v", err)
		}
	})
}

func TestSettingsManagerHTTPIdleTimeoutMs(t *testing.T) {
	t.Run("defaults to 5 minutes", func(t *testing.T) {
		agentDir, projectDir := settingsTestDirs(t)
		manager := CreateSettingsManager(projectDir, agentDir, nil)
		got, err := manager.GetHTTPIdleTimeoutMs()
		if err != nil {
			t.Fatal(err)
		}
		if got != DefaultHTTPIdleTimeoutMs {
			t.Fatalf("httpIdleTimeoutMs = %d", got)
		}
	})

	t.Run("uses merged global and project settings", func(t *testing.T) {
		agentDir, projectDir := settingsTestDirs(t)
		writeSettingsFile(t, filepath.Join(agentDir, "settings.json"), `{"httpIdleTimeoutMs":300000}`)
		writeSettingsFile(t, filepath.Join(projectDir, ".pig", "settings.json"), `{"httpIdleTimeoutMs":0}`)

		manager := CreateSettingsManager(projectDir, agentDir, nil)

		got, err := manager.GetHTTPIdleTimeoutMs()
		if err != nil {
			t.Fatal(err)
		}
		if got != 0 {
			t.Fatalf("httpIdleTimeoutMs = %d", got)
		}
	})

	t.Run("rejects invalid timeout values", func(t *testing.T) {
		agentDir, projectDir := settingsTestDirs(t)
		writeSettingsFile(t, filepath.Join(agentDir, "settings.json"), `{"httpIdleTimeoutMs":-1}`)
		manager := CreateSettingsManager(projectDir, agentDir, nil)

		_, err := manager.GetHTTPIdleTimeoutMs()
		if err == nil || !strings.Contains(err.Error(), "Invalid httpIdleTimeoutMs setting") {
			t.Fatalf("expected invalid setting error, got %v", err)
		}
	})
}

func TestSettingsManagerExternalEditor(t *testing.T) {
	// pi mocks process.platform via Object.defineProperty; pig pins the
	// externalEditorDefaultPlatform hook.
	originalPlatform := externalEditorDefaultPlatform
	t.Cleanup(func() { externalEditorDefaultPlatform = originalPlatform })

	t.Run("resolves editor commands by precedence", func(t *testing.T) {
		t.Setenv("VISUAL", "vim")
		t.Setenv("EDITOR", "nano")
		if got := InMemorySettingsManager(Settings{"externalEditor": "code --wait"}, nil).GetExternalEditorCommand(); got != "code --wait" {
			t.Fatalf("externalEditor = %q", got)
		}
		if got := InMemorySettingsManager(Settings{}, nil).GetExternalEditorCommand(); got != "vim" {
			t.Fatalf("externalEditor = %q", got)
		}

		os.Unsetenv("VISUAL")
		t.Setenv("EDITOR", "emacs")
		if got := InMemorySettingsManager(Settings{}, nil).GetExternalEditorCommand(); got != "emacs" {
			t.Fatalf("externalEditor = %q", got)
		}
	})

	t.Run("falls back to platform defaults", func(t *testing.T) {
		os.Unsetenv("VISUAL")
		os.Unsetenv("EDITOR")

		externalEditorDefaultPlatform = "windows"
		if got := InMemorySettingsManager(Settings{}, nil).GetExternalEditorCommand(); got != "notepad" {
			t.Fatalf("externalEditor = %q", got)
		}

		externalEditorDefaultPlatform = "darwin"
		if got := InMemorySettingsManager(Settings{}, nil).GetExternalEditorCommand(); got != "nano" {
			t.Fatalf("externalEditor = %q", got)
		}

		externalEditorDefaultPlatform = "linux"
		if got := InMemorySettingsManager(Settings{}, nil).GetExternalEditorCommand(); got != "nano" {
			t.Fatalf("externalEditor = %q", got)
		}
	})
}

func TestSettingsManagerTuiMode(t *testing.T) {
	t.Run("defaults to regular and persists fullscreen mode", func(t *testing.T) {
		agentDir, projectDir := settingsTestDirs(t)
		manager := CreateSettingsManager(projectDir, agentDir, nil)

		if got := manager.GetTuiMode(); got != TuiModeRegular {
			t.Fatalf("tuiMode = %q", got)
		}

		manager.SetTuiMode(TuiModeFullscreen)
		manager.Flush()

		if got := manager.GetTuiMode(); got != TuiModeFullscreen {
			t.Fatalf("tuiMode = %q", got)
		}
		savedSettings := readSettingsJSON(t, filepath.Join(agentDir, "settings.json"))
		if got := settingsJSONString(t, savedSettings, "tuiMode"); got != "fullscreen" {
			t.Fatalf("tuiMode = %q", got)
		}
	})

	t.Run("falls back to regular for unsupported values", func(t *testing.T) {
		agentDir, projectDir := settingsTestDirs(t)
		writeSettingsFile(t, filepath.Join(agentDir, "settings.json"), `{"tuiMode":"other"}`)

		manager := CreateSettingsManager(projectDir, agentDir, nil)

		if got := manager.GetTuiMode(); got != TuiModeRegular {
			t.Fatalf("tuiMode = %q", got)
		}
	})

	t.Run("does not recognize the old uiMode setting", func(t *testing.T) {
		agentDir, projectDir := settingsTestDirs(t)
		writeSettingsFile(t, filepath.Join(agentDir, "settings.json"), `{"uiMode":"fullscreen"}`)

		manager := CreateSettingsManager(projectDir, agentDir, nil)

		if got := manager.GetTuiMode(); got != TuiModeRegular {
			t.Fatalf("tuiMode = %q", got)
		}
	})
}

func TestSettingsManagerValidatesAndPersistsFullscreenSettings(t *testing.T) {
	agentDir, projectDir := settingsTestDirs(t)
	manager := CreateSettingsManager(projectDir, agentDir, nil)
	if got := manager.GetFullscreenExitOutput(); got != FullscreenExitOutputTranscript {
		t.Fatalf("fullscreenExitOutput = %q", got)
	}
	if got := manager.GetFullscreenScrollbar(); got != ScrollViewScrollbarAuto {
		t.Fatalf("fullscreenScrollbar = %q", got)
	}
	if got := manager.GetFullscreenCopyOnSelect(); got != true {
		t.Fatalf("fullscreenCopyOnSelect = %v", got)
	}

	manager.SetFullscreenExitOutput(FullscreenExitOutputResumeHint)
	manager.SetFullscreenScrollbar(ScrollViewScrollbarHidden)
	manager.SetFullscreenCopyOnSelect(false)
	manager.Flush()
	savedSettings := readSettingsJSON(t, filepath.Join(agentDir, "settings.json"))
	if got := settingsJSONString(t, savedSettings, "fullscreenExitOutput"); got != "resume-hint" {
		t.Fatalf("fullscreenExitOutput = %q", got)
	}
	if got := settingsJSONString(t, savedSettings, "fullscreenScrollbar"); got != "hidden" {
		t.Fatalf("fullscreenScrollbar = %q", got)
	}
	if got := settingsJSONBool(t, savedSettings, "fullscreenCopyOnSelect"); got != false {
		t.Fatalf("fullscreenCopyOnSelect = %v", got)
	}

	writeSettingsFile(t, filepath.Join(agentDir, "settings.json"), `{"fullscreenExitOutput":"nothing","fullscreenScrollbar":"sometimes"}`)
	reloadedManager := CreateSettingsManager(projectDir, agentDir, nil)
	if got := reloadedManager.GetFullscreenExitOutput(); got != FullscreenExitOutputTranscript {
		t.Fatalf("fullscreenExitOutput = %q", got)
	}
	if got := reloadedManager.GetFullscreenScrollbar(); got != ScrollViewScrollbarAuto {
		t.Fatalf("fullscreenScrollbar = %q", got)
	}
	if got := reloadedManager.GetFullscreenCopyOnSelect(); got != true {
		t.Fatalf("fullscreenCopyOnSelect = %v", got)
	}
}

func TestSettingsManagerTerminalCapabilityOverrides(t *testing.T) {
	getOverrides := func(terminalJSON string) TerminalCapabilityOverrides {
		agentDir, projectDir := settingsTestDirs(t)
		writeSettingsFile(t, filepath.Join(agentDir, "settings.json"), `{"terminal":`+terminalJSON+`}`)
		manager := CreateSettingsManager(projectDir, agentDir, nil)
		return manager.GetTerminalCapabilityOverrides()
	}

	t.Run("maps explicit values and omits auto values", func(t *testing.T) {
		forcedOff := getOverrides(`{"images":false,"trueColor":false,"hyperlinks":false}`)
		if forcedOff.Images != false {
			t.Fatalf("images = %v, want false (pi null)", forcedOff.Images)
		}
		if forcedOff.TrueColor == nil || *forcedOff.TrueColor != false {
			t.Fatalf("trueColor = %v", forcedOff.TrueColor)
		}
		if forcedOff.Hyperlinks == nil || *forcedOff.Hyperlinks != false {
			t.Fatalf("hyperlinks = %v", forcedOff.Hyperlinks)
		}

		kitty := getOverrides(`{"images":"kitty","trueColor":true,"hyperlinks":true}`)
		if kitty.Images != "kitty" {
			t.Fatalf("images = %v, want kitty", kitty.Images)
		}
		if kitty.TrueColor == nil || *kitty.TrueColor != true {
			t.Fatalf("trueColor = %v", kitty.TrueColor)
		}
		if kitty.Hyperlinks == nil || *kitty.Hyperlinks != true {
			t.Fatalf("hyperlinks = %v", kitty.Hyperlinks)
		}

		auto := getOverrides(`{"images":"auto","trueColor":"auto","hyperlinks":"auto"}`)
		if auto.Images != nil || auto.TrueColor != nil || auto.Hyperlinks != nil {
			t.Fatalf("overrides = %+v, want all nil", auto)
		}
	})
}

func TestSettingsManagerOutputPad(t *testing.T) {
	t.Run("defaults to 1 and persists binary values", func(t *testing.T) {
		agentDir, projectDir := settingsTestDirs(t)
		manager := CreateSettingsManager(projectDir, agentDir, nil)

		if got := manager.GetOutputPad(); got != 1 {
			t.Fatalf("outputPad = %d", got)
		}

		manager.SetOutputPad(0)
		manager.Flush()

		if got := manager.GetOutputPad(); got != 0 {
			t.Fatalf("outputPad = %d", got)
		}
		savedSettings := readSettingsJSON(t, filepath.Join(agentDir, "settings.json"))
		if value, ok := savedSettings["outputPad"].(float64); !ok || value != 0 {
			t.Fatalf("outputPad = %v", savedSettings["outputPad"])
		}
	})

	t.Run("treats unsupported outputPad values as default padding", func(t *testing.T) {
		agentDir, projectDir := settingsTestDirs(t)
		writeSettingsFile(t, filepath.Join(agentDir, "settings.json"), `{"outputPad":2}`)

		manager := CreateSettingsManager(projectDir, agentDir, nil)

		if got := manager.GetOutputPad(); got != 1 {
			t.Fatalf("outputPad = %d", got)
		}
	})
}

func TestSettingsManagerMarkdownMermaid(t *testing.T) {
	t.Run("defaults to streaming and persists rendering modes", func(t *testing.T) {
		agentDir, projectDir := settingsTestDirs(t)
		manager := CreateSettingsManager(projectDir, agentDir, nil)

		if got := manager.GetMermaidRenderingMode(); got != MermaidRenderingStreaming {
			t.Fatalf("mermaid = %q", got)
		}

		manager.SetMermaidRenderingMode(MermaidRenderingFinal)
		manager.Flush()

		if got := manager.GetMermaidRenderingMode(); got != MermaidRenderingFinal {
			t.Fatalf("mermaid = %q", got)
		}
		savedSettings := readSettingsJSON(t, filepath.Join(agentDir, "settings.json"))
		markdown, ok := savedSettings["markdown"].(map[string]any)
		if !ok {
			t.Fatalf("markdown = %v", savedSettings["markdown"])
		}
		if got := settingsJSONString(t, markdown, "mermaid"); got != "final" {
			t.Fatalf("mermaid = %q", got)
		}
	})

	t.Run("falls back to streaming for unsupported values", func(t *testing.T) {
		agentDir, projectDir := settingsTestDirs(t)
		writeSettingsFile(t, filepath.Join(agentDir, "settings.json"), `{"markdown":{"mermaid":"sometimes"}}`)

		if got := CreateSettingsManager(projectDir, agentDir, nil).GetMermaidRenderingMode(); got != MermaidRenderingStreaming {
			t.Fatalf("mermaid = %q", got)
		}
	})
}

func TestSettingsManagerShellCommandPrefix(t *testing.T) {
	t.Run("loads shellCommandPrefix from settings", func(t *testing.T) {
		agentDir, projectDir := settingsTestDirs(t)
		settingsPath := filepath.Join(agentDir, "settings.json")
		writeSettingsFile(t, settingsPath, `{"shellCommandPrefix":"shopt -s expand_aliases"}`)

		manager := CreateSettingsManager(projectDir, agentDir, nil)

		if got, ok := manager.GetShellCommandPrefix(); !ok || got != "shopt -s expand_aliases" {
			t.Fatalf("shellCommandPrefix = %q, %v", got, ok)
		}
	})

	t.Run("returns undefined when shellCommandPrefix is not set", func(t *testing.T) {
		agentDir, projectDir := settingsTestDirs(t)
		settingsPath := filepath.Join(agentDir, "settings.json")
		writeSettingsFile(t, settingsPath, `{"theme":"dark"}`)

		manager := CreateSettingsManager(projectDir, agentDir, nil)

		if got, ok := manager.GetShellCommandPrefix(); ok {
			t.Fatalf("shellCommandPrefix = %q, %v", got, ok)
		}
	})

	t.Run("preserves shellCommandPrefix when saving unrelated settings", func(t *testing.T) {
		agentDir, projectDir := settingsTestDirs(t)
		settingsPath := filepath.Join(agentDir, "settings.json")
		writeSettingsFile(t, settingsPath, `{"shellCommandPrefix":"shopt -s expand_aliases"}`)

		manager := CreateSettingsManager(projectDir, agentDir, nil)
		manager.SetTheme("light")
		manager.Flush()

		savedSettings := readSettingsJSON(t, settingsPath)
		if got := settingsJSONString(t, savedSettings, "shellCommandPrefix"); got != "shopt -s expand_aliases" {
			t.Fatalf("shellCommandPrefix = %q", got)
		}
		if got := settingsJSONString(t, savedSettings, "theme"); got != "light" {
			t.Fatalf("theme = %q", got)
		}
	})
}

func TestSettingsManagerDefaultTools(t *testing.T) {
	t.Run("loads global defaults and lets project settings replace them", func(t *testing.T) {
		agentDir, projectDir := settingsTestDirs(t)
		writeSettingsFile(t, filepath.Join(agentDir, "settings.json"), `{"defaultTools":["read","bash"]}`)

		if tools, ok := CreateSettingsManager(projectDir, agentDir, nil).GetDefaultTools(); !ok || len(tools) != 2 || tools[0] != "read" || tools[1] != "bash" {
			t.Fatalf("defaultTools = %v, %v", tools, ok)
		}

		writeSettingsFile(t, filepath.Join(projectDir, ".pig", "settings.json"), `{"defaultTools":["grep"]}`)

		if tools, ok := CreateSettingsManager(projectDir, agentDir, nil).GetDefaultTools(); !ok || len(tools) != 1 || tools[0] != "grep" {
			t.Fatalf("defaultTools = %v, %v", tools, ok)
		}
	})

	t.Run("preserves an empty tool list", func(t *testing.T) {
		if tools, ok := InMemorySettingsManager(Settings{"defaultTools": []any{}}, nil).GetDefaultTools(); !ok || len(tools) != 0 {
			t.Fatalf("defaultTools = %v, %v", tools, ok)
		}
		if tools, ok := InMemorySettingsManager(Settings{}, nil).GetDefaultTools(); ok {
			t.Fatalf("defaultTools = %v, %v (want unset)", tools, ok)
		}
	})
}

func TestSettingsManagerGetSessionDir(t *testing.T) {
	t.Run("returns undefined when not set", func(t *testing.T) {
		agentDir, projectDir := settingsTestDirs(t)
		writeSettingsFile(t, filepath.Join(agentDir, "settings.json"), `{"theme":"dark"}`)
		manager := CreateSettingsManager(projectDir, agentDir, nil)
		if dir, ok := manager.GetSessionDir(); ok {
			t.Fatalf("sessionDir = %q, %v", dir, ok)
		}
	})

	t.Run("returns global sessionDir", func(t *testing.T) {
		agentDir, projectDir := settingsTestDirs(t)
		writeSettingsFile(t, filepath.Join(agentDir, "settings.json"), `{"sessionDir":"/tmp/sessions"}`)
		manager := CreateSettingsManager(projectDir, agentDir, nil)
		if dir, ok := manager.GetSessionDir(); !ok || dir != "/tmp/sessions" {
			t.Fatalf("sessionDir = %q, %v", dir, ok)
		}
	})

	t.Run("returns project sessionDir, overriding global", func(t *testing.T) {
		agentDir, projectDir := settingsTestDirs(t)
		writeSettingsFile(t, filepath.Join(agentDir, "settings.json"), `{"sessionDir":"/global/sessions"}`)
		writeSettingsFile(t, filepath.Join(projectDir, ".pig", "settings.json"), `{"sessionDir":"./sessions"}`)
		manager := CreateSettingsManager(projectDir, agentDir, nil)
		if dir, ok := manager.GetSessionDir(); !ok || dir != "./sessions" {
			t.Fatalf("sessionDir = %q, %v", dir, ok)
		}
	})

	t.Run("expands ~ in sessionDir", func(t *testing.T) {
		agentDir, projectDir := settingsTestDirs(t)
		writeSettingsFile(t, filepath.Join(agentDir, "settings.json"), `{"sessionDir":"~/sessions"}`)
		manager := CreateSettingsManager(projectDir, agentDir, nil)
		home, err := os.UserHomeDir()
		if err != nil {
			t.Fatal(err)
		}
		want := filepath.Join(home, "sessions")
		if dir, ok := manager.GetSessionDir(); !ok || dir != want {
			t.Fatalf("sessionDir = %q, %v (want %q)", dir, ok, want)
		}
	})
}

func TestSettingsManagerGetShellPath(t *testing.T) {
	t.Run("returns undefined when not set", func(t *testing.T) {
		agentDir, projectDir := settingsTestDirs(t)
		writeSettingsFile(t, filepath.Join(agentDir, "settings.json"), `{"theme":"dark"}`)
		manager := CreateSettingsManager(projectDir, agentDir, nil)
		if path, ok := manager.GetShellPath(); ok {
			t.Fatalf("shellPath = %q, %v", path, ok)
		}
	})

	t.Run("returns an absolute shellPath unchanged", func(t *testing.T) {
		agentDir, projectDir := settingsTestDirs(t)
		writeSettingsFile(t, filepath.Join(agentDir, "settings.json"), `{"shellPath":"/bin/zsh"}`)
		manager := CreateSettingsManager(projectDir, agentDir, nil)
		if path, ok := manager.GetShellPath(); !ok || path != "/bin/zsh" {
			t.Fatalf("shellPath = %q, %v", path, ok)
		}
	})

	t.Run("expands ~ in shellPath", func(t *testing.T) {
		agentDir, projectDir := settingsTestDirs(t)
		writeSettingsFile(t, filepath.Join(agentDir, "settings.json"), `{"shellPath":"~/.local/bin/agent-shell-sandbox"}`)
		manager := CreateSettingsManager(projectDir, agentDir, nil)
		home, err := os.UserHomeDir()
		if err != nil {
			t.Fatal(err)
		}
		want := filepath.Join(home, ".local/bin/agent-shell-sandbox")
		if path, ok := manager.GetShellPath(); !ok || path != want {
			t.Fatalf("shellPath = %q, %v (want %q)", path, ok, want)
		}
	})

	t.Run("expands a bare ~ in shellPath", func(t *testing.T) {
		agentDir, projectDir := settingsTestDirs(t)
		writeSettingsFile(t, filepath.Join(agentDir, "settings.json"), `{"shellPath":"~"}`)
		manager := CreateSettingsManager(projectDir, agentDir, nil)
		home, err := os.UserHomeDir()
		if err != nil {
			t.Fatal(err)
		}
		if path, ok := manager.GetShellPath(); !ok || path != home {
			t.Fatalf("shellPath = %q, %v (want %q)", path, ok, home)
		}
	})
}

// boolPtr is lo.ToPtr for tests (kept local for brevity).
func boolPtr(value bool) *bool {
	return &value
}

// Keep the codingagent import referenced (ConfigDirName pins the project
// settings path shape asserted through behavior above).
var _ = codingagent.ConfigDirName

// pig-only setting: CJK word-segmentation backend (no pi counterpart).
// A valid `cjkSegmenter` setting applies; invalid stored values resolve to
// "bigram" (the tui resolver's never-error contract).
func TestSettingsManagerCJKSegmenter(t *testing.T) {
	t.Run("defaults to bigram", func(t *testing.T) {
		agentDir, projectDir := settingsTestDirs(t)
		manager := CreateSettingsManager(projectDir, agentDir, nil)
		if got := manager.GetCJKSegmenter(); got != "bigram" {
			t.Fatalf("cjkSegmenter = %q, want bigram", got)
		}
	})

	t.Run("stored setting applies", func(t *testing.T) {
		agentDir, projectDir := settingsTestDirs(t)
		writeSettingsFile(t, filepath.Join(agentDir, "settings.json"), `{"cjkSegmenter":"sst"}`)
		manager := CreateSettingsManager(projectDir, agentDir, nil)
		if got := manager.GetCJKSegmenter(); got != "sst" {
			t.Fatalf("cjkSegmenter = %q, want sst", got)
		}
	})

	t.Run("invalid stored value resolves to bigram", func(t *testing.T) {
		agentDir, projectDir := settingsTestDirs(t)
		writeSettingsFile(t, filepath.Join(agentDir, "settings.json"), `{"cjkSegmenter":"icu"}`)
		manager := CreateSettingsManager(projectDir, agentDir, nil)
		if got := manager.GetCJKSegmenter(); got != "bigram" {
			t.Fatalf("cjkSegmenter = %q, want bigram", got)
		}
	})

	t.Run("Set persists and rejects invalid values", func(t *testing.T) {
		agentDir, projectDir := settingsTestDirs(t)
		manager := CreateSettingsManager(projectDir, agentDir, nil)

		manager.SetCJKSegmenter("icu")
		if saved, err := os.ReadFile(filepath.Join(agentDir, "settings.json")); err == nil && strings.Contains(string(saved), "cjkSegmenter") {
			t.Fatalf("invalid value must not be persisted: %s", saved)
		}

		manager.SetCJKSegmenter("gse")
		manager.Flush()
		saved := readSettingsJSON(t, filepath.Join(agentDir, "settings.json"))
		if got := settingsJSONString(t, saved, "cjkSegmenter"); got != "gse" {
			t.Fatalf("persisted cjkSegmenter = %q, want gse", got)
		}
		if got := manager.GetCJKSegmenter(); got != "gse" {
			t.Fatalf("cjkSegmenter = %q, want gse", got)
		}
	})
}
