// pi: test/settings-manager-bug.test.ts — regression tests for the bug where
// external file changes to arrays were overwritten by stale in-memory state.
//
// The bug scenario was:
//  1. Pi starts with settings.json containing packages: ["npm:some-pkg"]
//  2. User externally edits file to packages: []
//  3. User changes an unrelated setting (e.g., theme) via UI
//  4. save() would overwrite packages back to ["npm:some-pkg"] from stale
//     in-memory state
//
// The fix tracks which fields were explicitly modified during the session,
// and only those fields override file values during save().
package core

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestSettingsManagerExternalEditPreservation(t *testing.T) {
	t.Run("preserves file changes to packages array when changing unrelated setting", func(t *testing.T) {
		agentDir, projectDir := settingsTestDirs(t)
		settingsPath := filepath.Join(agentDir, "settings.json")

		// Initial state: packages has one item
		writeSettingsFile(t, settingsPath, `{"theme":"dark","packages":["npm:pi-mcp-adapter"]}`)

		// Pi starts up, loads settings into memory
		manager := CreateSettingsManager(projectDir, agentDir, nil)

		// At this point, globalSettings.packages = ["npm:pi-mcp-adapter"]
		if packages := manager.GetPackages(); len(packages) != 1 || packages[0].Source != "npm:pi-mcp-adapter" {
			t.Fatalf("packages = %v", packages)
		}

		// User externally edits settings.json to remove the package
		currentSettings := readSettingsJSON(t, settingsPath)
		currentSettings["packages"] = []any{} // User wants to remove this!
		updated, err := json.Marshal(currentSettings)
		if err != nil {
			t.Fatal(err)
		}
		writeSettingsFile(t, settingsPath, string(updated))

		// Verify file was changed
		if fileSettings := readSettingsJSON(t, settingsPath); len(fileSettings["packages"].([]any)) != 0 {
			t.Fatalf("packages = %v", fileSettings["packages"])
		}

		// User changes an UNRELATED setting via UI (this triggers save)
		manager.SetTheme("light")
		manager.Flush()

		// With the fix, packages should be preserved as [] (not reverted to
		// startup value)
		savedSettings := readSettingsJSON(t, settingsPath)

		packages, ok := savedSettings["packages"].([]any)
		if !ok || len(packages) != 0 {
			t.Fatalf("packages = %v (want preserved empty)", savedSettings["packages"])
		}
		if got := settingsJSONString(t, savedSettings, "theme"); got != "light" {
			t.Fatalf("theme = %q", got)
		}
	})

	t.Run("preserves file changes to extensions array when changing unrelated setting", func(t *testing.T) {
		agentDir, projectDir := settingsTestDirs(t)
		settingsPath := filepath.Join(agentDir, "settings.json")

		writeSettingsFile(t, settingsPath, `{"theme":"dark","extensions":["/old/extension.ts"]}`)

		manager := CreateSettingsManager(projectDir, agentDir, nil)

		// User externally updates extensions
		currentSettings := readSettingsJSON(t, settingsPath)
		currentSettings["extensions"] = []any{"/new/extension.ts"}
		updated, err := json.Marshal(currentSettings)
		if err != nil {
			t.Fatal(err)
		}
		writeSettingsFile(t, settingsPath, string(updated))

		// Change unrelated setting
		manager.SetDefaultThinkingLevel("high")
		manager.Flush()

		savedSettings := readSettingsJSON(t, settingsPath)

		// With the fix, extensions should be preserved (not reverted to
		// startup value)
		extensions, ok := savedSettings["extensions"].([]any)
		if !ok || len(extensions) != 1 || extensions[0] != "/new/extension.ts" {
			t.Fatalf("extensions = %v", savedSettings["extensions"])
		}
	})

	t.Run("preserves external project settings changes when updating unrelated project field", func(t *testing.T) {
		agentDir, projectDir := settingsTestDirs(t)
		projectSettingsPath := filepath.Join(projectDir, ".pig", "settings.json")
		writeSettingsFile(t, projectSettingsPath, `{"extensions":["./old-extension.ts"],"prompts":["./old-prompt.md"]}`)

		manager := CreateSettingsManager(projectDir, agentDir, nil)

		currentProjectSettings := readSettingsJSON(t, projectSettingsPath)
		currentProjectSettings["prompts"] = []any{"./new-prompt.md"}
		updated, err := json.Marshal(currentProjectSettings)
		if err != nil {
			t.Fatal(err)
		}
		writeSettingsFile(t, projectSettingsPath, string(updated))

		if err := manager.SetProjectExtensionPaths([]string{"./updated-extension.ts"}); err != nil {
			t.Fatal(err)
		}
		manager.Flush()

		savedProjectSettings := readSettingsJSON(t, projectSettingsPath)
		prompts, ok := savedProjectSettings["prompts"].([]any)
		if !ok || len(prompts) != 1 || prompts[0] != "./new-prompt.md" {
			t.Fatalf("prompts = %v", savedProjectSettings["prompts"])
		}
		extensions, ok := savedProjectSettings["extensions"].([]any)
		if !ok || len(extensions) != 1 || extensions[0] != "./updated-extension.ts" {
			t.Fatalf("extensions = %v", savedProjectSettings["extensions"])
		}
	})

	t.Run("lets in-memory project changes override external changes for the same project field", func(t *testing.T) {
		agentDir, projectDir := settingsTestDirs(t)
		projectSettingsPath := filepath.Join(projectDir, ".pig", "settings.json")
		writeSettingsFile(t, projectSettingsPath, `{"extensions":["./initial-extension.ts"]}`)

		manager := CreateSettingsManager(projectDir, agentDir, nil)

		currentProjectSettings := readSettingsJSON(t, projectSettingsPath)
		currentProjectSettings["extensions"] = []any{"./external-extension.ts"}
		updated, err := json.Marshal(currentProjectSettings)
		if err != nil {
			t.Fatal(err)
		}
		writeSettingsFile(t, projectSettingsPath, string(updated))

		if err := manager.SetProjectExtensionPaths([]string{"./in-memory-extension.ts"}); err != nil {
			t.Fatal(err)
		}
		manager.Flush()

		savedProjectSettings := readSettingsJSON(t, projectSettingsPath)
		extensions, ok := savedProjectSettings["extensions"].([]any)
		if !ok || len(extensions) != 1 || extensions[0] != "./in-memory-extension.ts" {
			t.Fatalf("extensions = %v", savedProjectSettings["extensions"])
		}
	})
}
