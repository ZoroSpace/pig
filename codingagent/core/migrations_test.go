// migrations_test.go — pi has test/migrations.test.ts? It does not; the
// migrations were covered indirectly. P8-EH1 builds direct coverage
// pig-side, locking the file-level contracts (auth.json layout, session
// path encoding, tools→bin, deprecation warnings).
package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	codingagent "github.com/ZoroSpace/pig/codingagent"
	"github.com/ZoroSpace/pig/codingagent/utils"
)

func setMigrationAgentDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv(codingagent.EnvAgentDir, dir)
	return dir
}

func writeFileForMigration(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestMigrateAuthToAuthJson(t *testing.T) {
	t.Run("oauth.json migrates to auth.json", func(t *testing.T) {
		agentDir := setMigrationAgentDir(t)
		writeFileForMigration(t, filepath.Join(agentDir, "oauth.json"),
			`{"google": {"access_token": "t1", "refresh_token": "r1"}, "anthropic": {"access_token": "t2"}}`)

		providers := MigrateAuthToAuthJson()
		if len(providers) != 2 || providers[0] != "google" || providers[1] != "anthropic" {
			t.Fatalf("providers = %v", providers)
		}
		authRaw, err := os.ReadFile(filepath.Join(agentDir, "auth.json"))
		if err != nil {
			t.Fatal(err)
		}
		var parsed map[string]map[string]any
		if err := json.Unmarshal(authRaw, &parsed); err != nil {
			t.Fatal(err)
		}
		if parsed["google"]["type"] != "oauth" || parsed["google"]["access_token"] != "t1" || parsed["google"]["refresh_token"] != "r1" {
			t.Fatalf("google credential = %#v", parsed["google"])
		}
		if parsed["anthropic"]["type"] != "oauth" {
			t.Fatalf("anthropic credential = %#v", parsed["anthropic"])
		}
		if info, err := os.Stat(filepath.Join(agentDir, "auth.json")); err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("auth.json mode = %v (err=%v)", info.Mode(), err)
		}
		if _, err := os.Stat(filepath.Join(agentDir, "oauth.json.migrated")); err != nil {
			t.Fatalf("oauth.json not renamed: %v", err)
		}
		if _, err := os.Stat(filepath.Join(agentDir, "oauth.json")); !os.IsNotExist(err) {
			t.Fatal("oauth.json should be gone")
		}
	})

	t.Run("settings apiKeys migrates and is removed", func(t *testing.T) {
		agentDir := setMigrationAgentDir(t)
		writeFileForMigration(t, filepath.Join(agentDir, "settings.json"),
			`{"theme": "dark", "apiKeys": {"deepseek": "sk-1"}}`)

		providers := MigrateAuthToAuthJson()
		if len(providers) != 1 || providers[0] != "deepseek" {
			t.Fatalf("providers = %v", providers)
		}
		var auth map[string]any
		raw, err := os.ReadFile(filepath.Join(agentDir, "auth.json"))
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &auth); err != nil {
			t.Fatal(err)
		}
		if auth["deepseek"].(map[string]any)["type"] != "api_key" || auth["deepseek"].(map[string]any)["key"] != "sk-1" {
			t.Fatalf("deepseek credential = %#v", auth["deepseek"])
		}
		var settings map[string]any
		settingsRaw, err := os.ReadFile(filepath.Join(agentDir, "settings.json"))
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(settingsRaw, &settings); err != nil {
			t.Fatal(err)
		}
		if _, ok := settings["apiKeys"]; ok {
			t.Fatal("apiKeys should be deleted from settings.json")
		}
		if settings["theme"] != "dark" {
			t.Fatalf("other settings must survive: %#v", settings)
		}
	})

	t.Run("oauth wins over settings apiKeys", func(t *testing.T) {
		agentDir := setMigrationAgentDir(t)
		writeFileForMigration(t, filepath.Join(agentDir, "oauth.json"), `{"x": {"access_token": "tok"}}`)
		writeFileForMigration(t, filepath.Join(agentDir, "settings.json"), `{"apiKeys": {"x": "sk"}}`)

		providers := MigrateAuthToAuthJson()
		if len(providers) != 1 || providers[0] != "x" {
			t.Fatalf("providers = %v", providers)
		}
		raw, _ := os.ReadFile(filepath.Join(agentDir, "auth.json"))
		if !strings.Contains(string(raw), `"access_token"`) {
			t.Fatalf("oauth credential should win: %s", raw)
		}
		if strings.Contains(string(raw), `"api_key"`) {
			t.Fatalf("settings key must not overwrite oauth: %s", raw)
		}
	})

	t.Run("auth.json present skips everything", func(t *testing.T) {
		agentDir := setMigrationAgentDir(t)
		writeFileForMigration(t, filepath.Join(agentDir, "auth.json"), `{}`)
		writeFileForMigration(t, filepath.Join(agentDir, "oauth.json"), `{"x": {}}`)

		if providers := MigrateAuthToAuthJson(); len(providers) != 0 {
			t.Fatalf("providers = %v, want empty", providers)
		}
		raw, _ := os.ReadFile(filepath.Join(agentDir, "auth.json"))
		if string(raw) != "{}" {
			t.Fatal("existing auth.json must be untouched")
		}
		if _, err := os.Stat(filepath.Join(agentDir, "oauth.json")); err != nil {
			t.Fatal("oauth.json should remain (no rename on skip)")
		}
	})

	t.Run("malformed oauth.json skips", func(t *testing.T) {
		agentDir := setMigrationAgentDir(t)
		writeFileForMigration(t, filepath.Join(agentDir, "oauth.json"), `{not json`)

		if providers := MigrateAuthToAuthJson(); len(providers) != 0 {
			t.Fatalf("providers = %v", providers)
		}
		if _, err := os.Stat(filepath.Join(agentDir, "oauth.json")); err != nil {
			t.Fatal("malformed oauth.json must not be renamed")
		}
	})
}

func TestMigrateSessionsFromAgentRoot(t *testing.T) {
	agentDir := setMigrationAgentDir(t)
	// Stray session file directly in agentDir (pi #320).
	cwd := filepath.Join(agentDir, "some", "project")
	writeFileForMigration(t, filepath.Join(agentDir, "2026-01-01T00-00-00_abcd.jsonl"),
		`{"type":"session","version":3,"id":"s1","timestamp":"t","cwd":"`+cwd+`"}`+"\n")
	// Non-session first line: skipped.
	writeFileForMigration(t, filepath.Join(agentDir, "other.jsonl"), `{"type":"message"}`+"\n")
	// Target already exists: source stays.
	dupCwd := filepath.Join(agentDir, "dup", "project")
	dupSafe := "--" + strings.NewReplacer("/", "-").Replace(strings.TrimPrefix(dupCwd, "/")) + "--"
	writeFileForMigration(t, filepath.Join(agentDir, "sessions", dupSafe, "dup.jsonl"), "{}\n")
	writeFileForMigration(t, filepath.Join(agentDir, "dup.jsonl"),
		`{"type":"session","version":3,"id":"s2","timestamp":"t","cwd":"`+dupCwd+`"}`+"\n")

	MigrateSessionsFromAgentRoot()

	safe := "--" + strings.NewReplacer("/", "-").Replace(strings.TrimPrefix(cwd, "/")) + "--"
	moved := filepath.Join(agentDir, "sessions", safe, "2026-01-01T00-00-00_abcd.jsonl")
	if _, err := os.Stat(moved); err != nil {
		t.Fatalf("stray session not moved: %v", err)
	}
	if _, err := os.Stat(filepath.Join(agentDir, "2026-01-01T00-00-00_abcd.jsonl")); !os.IsNotExist(err) {
		t.Fatal("source file should be gone")
	}
	if _, err := os.Stat(filepath.Join(agentDir, "other.jsonl")); err != nil {
		t.Fatal("non-session jsonl must stay")
	}
	if _, err := os.Stat(filepath.Join(agentDir, "dup.jsonl")); err != nil {
		t.Fatal("dup session must stay when target exists")
	}
}

func TestMigrateToolsToBin(t *testing.T) {
	agentDir := setMigrationAgentDir(t)
	toolsDir := filepath.Join(agentDir, "tools")
	writeFileForMigration(t, filepath.Join(toolsDir, "fd"), "fd-binary")
	writeFileForMigration(t, filepath.Join(toolsDir, "rg"), "rg-binary")
	writeFileForMigration(t, filepath.Join(toolsDir, "rg.exe"), "rg-exe-binary")

	migrateToolsToBin()

	binDir := codingagent.GetBinDir()
	if _, err := os.Stat(filepath.Join(binDir, "fd")); err != nil {
		t.Fatalf("fd not moved to bin: %v", err)
	}
	if _, err := os.Stat(filepath.Join(binDir, "rg")); err != nil {
		t.Fatalf("rg not moved to bin: %v", err)
	}
	if _, err := os.Stat(filepath.Join(toolsDir, "fd")); !os.IsNotExist(err) {
		t.Fatal("tools/fd should be gone")
	}
	// rg.exe collides with rg in GetBinDir? bin/rg already exists → the
	// old rg.exe is deleted instead of moved.
	if _, err := os.Stat(filepath.Join(toolsDir, "rg.exe")); !os.IsNotExist(err) {
		t.Fatal("tools/rg.exe should be removed when target exists")
	}
}

func TestMigrateExtensionSystem(t *testing.T) {
	agentDir := setMigrationAgentDir(t)
	cwd := t.TempDir()
	projectDir := filepath.Join(cwd, codingagent.ConfigDirName)
	// commands → prompts (global) + hooks/ and custom tools/ warnings
	// (project).
	writeFileForMigration(t, filepath.Join(agentDir, "commands", "x.md"), "")
	writeFileForMigration(t, filepath.Join(projectDir, "hooks", "h.js"), "")
	writeFileForMigration(t, filepath.Join(projectDir, "tools", "custom.js"), "")

	warnings := migrateExtensionSystem(cwd)

	if _, err := os.Stat(filepath.Join(agentDir, "prompts", "x.md")); err != nil {
		t.Fatalf("global commands/ not migrated: %v", err)
	}
	if len(warnings) != 2 {
		t.Fatalf("warnings = %#v", warnings)
	}
	if !strings.Contains(warnings[0], "Project hooks/ directory found. Hooks have been renamed to extensions.") {
		t.Fatalf("hooks warning = %q", warnings[0])
	}
	if !strings.Contains(warnings[1], "Project tools/ directory contains custom tools. Custom tools have been merged into extensions.") {
		t.Fatalf("tools warning = %q", warnings[1])
	}
}

func TestCheckDeprecatedExtensionDirsIgnoresBinariesAndHidden(t *testing.T) {
	agentDir := setMigrationAgentDir(t)
	toolsDir := filepath.Join(agentDir, "tools")
	writeFileForMigration(t, filepath.Join(toolsDir, "fd"), "")
	writeFileForMigration(t, filepath.Join(toolsDir, "FD.EXE"), "")
	writeFileForMigration(t, filepath.Join(toolsDir, ".DS_Store"), "")

	if warnings := checkDeprecatedExtensionDirs(agentDir, "Global"); len(warnings) != 0 {
		t.Fatalf("warnings = %#v, want none", warnings)
	}
}

func TestMigrateKeybindingsConfigFile(t *testing.T) {
	agentDir := setMigrationAgentDir(t)
	configPath := filepath.Join(agentDir, "keybindings.json")
	writeFileForMigration(t, configPath, `{"exit": ["ctrl+c"]}`)

	migrateKeybindingsConfigFile()

	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatal(err)
	}
	if _, ok := parsed["app.exit"]; !ok {
		t.Fatalf("legacy 'exit' not migrated: %s", raw)
	}
	if !strings.HasSuffix(string(raw), "\n") {
		t.Fatal("keybindings.json rewrite must end with a newline")
	}

	// Non-migratable content is left alone.
	writeFileForMigration(t, configPath, `{"app.exit": ["ctrl+d"]}`)
	migrateKeybindingsConfigFile()
	raw2, _ := os.ReadFile(configPath)
	if string(raw2) != `{"app.exit": ["ctrl+d"]}` {
		t.Fatalf("non-legacy config rewritten: %s", raw2)
	}
}

func TestRunMigrationsAggregates(t *testing.T) {
	agentDir := setMigrationAgentDir(t)
	cwd := t.TempDir()
	writeFileForMigration(t, filepath.Join(agentDir, "oauth.json"), `{"google": {"access_token": "t"}}`)
	writeFileForMigration(t, filepath.Join(agentDir, "hooks", "h.js"), "")

	result := RunMigrations(cwd)
	if len(result.MigratedAuthProviders) != 1 || result.MigratedAuthProviders[0] != "google" {
		t.Fatalf("migrated = %v", result.MigratedAuthProviders)
	}
	if len(result.DeprecationWarnings) != 1 {
		t.Fatalf("warnings = %#v", result.DeprecationWarnings)
	}
}

// captureStdout redirects os.Stdout to a pipe for the duration of fn and
// returns everything written (pig test seam; fmt.Print writes to os.Stdout
// at call time, so a swap is picked up).
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		var sb strings.Builder
		buf := make([]byte, 4096)
		for {
			n, err := r.Read(buf)
			sb.Write(buf[:n])
			if err != nil {
				break
			}
		}
		done <- sb.String()
	}()
	fn()
	os.Stdout = orig
	w.Close()
	out := <-done
	r.Close()
	return out
}

func TestShowDeprecationWarningsOutput(t *testing.T) {
	// Non-TTY stdin (go test pipes) → no keypress wait; just verify output.
	got := captureStdout(t, func() {
		ShowDeprecationWarnings([]string{"Global hooks/ directory found."})
	})
	for _, want := range []string{
		"Warning: Global hooks/ directory found.",
		"Move your extensions to the extensions/ directory.",
		"https://github.com/earendil-works/pi/blob/main/packages/coding-agent/CHANGELOG.md#extensions-migration",
		"https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/extensions.md",
		"Press any key to continue...",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("output missing %q:\n%s", want, got)
		}
	}
}

func TestStripBomOnMigrationInputs(t *testing.T) {
	agentDir := setMigrationAgentDir(t)
	writeFileForMigration(t, filepath.Join(agentDir, "oauth.json"), "\xef\xbb\xbf{\"google\": {\"access_token\": \"t\"}}")

	providers := MigrateAuthToAuthJson()
	if len(providers) != 1 || providers[0] != "google" {
		t.Fatalf("BOM-prefixed oauth.json failed: %v (utils.StripBom: %v)", providers, utils.StripBom("\xef\xbb\xbf{}") == "{}")
	}
}
