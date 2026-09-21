// migrations.go (pi: src/migrations.ts, v0.86.0)
//
// One-time migrations that run on startup.
//
// Location deviation: pi keeps this file at the coding-agent src root, but
// it needs core's keybindings migration and pig's codingagent root package
// cannot import core (core imports the root for GetAgentDir) — so the file
// lives in package core, as scoped in the P8-EH1 handoff.
//
// Console-color deviation: pi colors these messages with chalk (green /
// yellow / dim); pig drops the coloring, consistent with the CLI batch
// (main.go prints plain diagnostics).
package core

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/term"

	codingagent "github.com/ZoroSpace/pig/codingagent"
	"github.com/ZoroSpace/pig/codingagent/utils"
)

const migrationGuideURL = "https://github.com/earendil-works/pi/blob/main/packages/coding-agent/CHANGELOG.md#extensions-migration"
const extensionsDocURL = "https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/extensions.md"

// jsonObjectEntry is one top-level property of a JSON object, preserving
// document order (pig addition: JS Object.entries iterates in document
// order, Go maps are unordered — migrations that fold oauth.json /
// settings.apiKeys need the original order for the providers list).
type jsonObjectEntry struct {
	Key   string
	Value json.RawMessage
}

// jsonObjectEntries decodes a JSON object's top-level entries in document
// order. Non-object input yields an error (pi's Object.entries over a
// parsed array/string would produce index/odd keys; pig treats those
// shapes as malformed and skips the migration — declared edge deviation).
func jsonObjectEntries(data []byte) ([]jsonObjectEntry, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '{' {
		return nil, fmt.Errorf("not a JSON object")
	}
	var entries []jsonObjectEntry
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, ok := keyTok.(string)
		if !ok {
			return nil, fmt.Errorf("object key is not a string")
		}
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return nil, err
		}
		entries = append(entries, jsonObjectEntry{Key: key, Value: value})
	}
	if _, err := dec.Token(); err != nil { // closing '}'
		return nil, err
	}
	return entries, nil
}

// MigrateAuthToAuthJson migrates legacy oauth.json and settings.json
// apiKeys to auth.json (pi: migrateAuthToAuthJson).
//
// Returns the provider names that were migrated.
//
// Serialization deviation: the rewritten auth.json key order is Go's map
// (sorted) rather than JS's insertion order; the providers list order
// follows the source document via jsonObjectEntries.
func MigrateAuthToAuthJson() []string {
	agentDir := codingagent.GetAgentDir()
	authPath := filepath.Join(agentDir, "auth.json")
	oauthPath := filepath.Join(agentDir, "oauth.json")
	settingsPath := filepath.Join(agentDir, "settings.json")

	// Skip if auth.json already exists
	if fileExistsMigrations(authPath) {
		return nil
	}

	migrated := map[string]any{}
	var providers []string

	// Migrate oauth.json
	if fileExistsMigrations(oauthPath) {
		content, err := os.ReadFile(oauthPath)
		if err == nil {
			entries, parseErr := jsonObjectEntries([]byte(utils.StripBom(string(content))))
			if parseErr == nil {
				for _, entry := range entries {
					var cred map[string]any
					if err := json.Unmarshal(entry.Value, &cred); err != nil {
						cred = nil
					}
					merged := map[string]any{"type": "oauth"}
					for k, v := range cred {
						merged[k] = v
					}
					migrated[entry.Key] = merged
					providers = append(providers, entry.Key)
				}
				_ = os.Rename(oauthPath, oauthPath+".migrated")
			}
			// Skip on parse error (pi: catch)
		}
	}

	// Migrate settings.json apiKeys
	if fileExistsMigrations(settingsPath) {
		content, err := os.ReadFile(settingsPath)
		if err == nil {
			var settings map[string]json.RawMessage
			if err := json.Unmarshal([]byte(utils.StripBom(string(content))), &settings); err == nil {
				if apiKeyRaw, ok := settings["apiKeys"]; ok && apiKeyRaw != nil && string(apiKeyRaw) != "null" {
					if apiKeyEntries, entryErr := jsonObjectEntries(apiKeyRaw); entryErr == nil {
						for _, entry := range apiKeyEntries {
							if _, dup := migrated[entry.Key]; !dup {
								var key string
								if err := json.Unmarshal(entry.Value, &key); err == nil {
									migrated[entry.Key] = map[string]any{"type": "api_key", "key": key}
									providers = append(providers, entry.Key)
								}
							}
						}
						delete(settings, "apiKeys")
						rewritten, marshalErr := json.MarshalIndent(settings, "", "  ")
						if marshalErr == nil {
							_ = os.WriteFile(settingsPath, rewritten, 0o644)
						}
					}
				}
			}
			// Skip on parse error (pi: catch)
		}
	}

	if len(migrated) > 0 {
		_ = os.MkdirAll(filepath.Dir(authPath), 0o755)
		authJSON, err := json.MarshalIndent(migrated, "", "  ")
		if err != nil {
			return providers
		}
		_ = os.WriteFile(authPath, authJSON, 0o600)
	}

	return providers
}

// fileExistsMigrations is existsSync for the migrations (pi: existsSync).
func fileExistsMigrations(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// MigrateSessionsFromAgentRoot migrates sessions from
// ~/.pig/agent/*.jsonl to proper session directories (pi:
// migrateSessionsFromAgentRoot).
//
// Bug in v0.30.0: Sessions were saved to ~/.pi/agent/ instead of
// ~/.pi/agent/sessions/<encoded-cwd>/. This migration moves them
// to the correct location based on the cwd in their session header.
//
// See: https://github.com/earendil-works/pi-mono/issues/320
func MigrateSessionsFromAgentRoot() {
	agentDir := codingagent.GetAgentDir()

	// Find all .jsonl files directly in agentDir (not in subdirectories)
	dirEntries, err := os.ReadDir(agentDir)
	if err != nil {
		return
	}
	var files []string
	for _, dirEntry := range dirEntries {
		if strings.HasSuffix(dirEntry.Name(), ".jsonl") {
			files = append(files, filepath.Join(agentDir, dirEntry.Name()))
		}
	}

	if len(files) == 0 {
		return
	}

	for _, file := range files {
		func() {
			defer func() {
				// Skip files that can't be migrated (pi: try/catch)
				_ = recover()
			}()

			// Read first line to get session header
			content, err := os.ReadFile(file)
			if err != nil {
				return
			}
			firstLine := strings.SplitN(string(content), "\n", 2)[0]
			if strings.TrimSpace(firstLine) == "" {
				return
			}

			var header struct {
				Type string `json:"type"`
				Cwd  string `json:"cwd"`
			}
			if err := json.Unmarshal([]byte(firstLine), &header); err != nil {
				return
			}
			if header.Type != "session" || header.Cwd == "" {
				return
			}

			cwd := header.Cwd

			// Compute the correct session directory (same encoding as
			// session-manager.ts; the raw header cwd is encoded as-is —
			// pi strips exactly one leading separator, unlike
			// getDefaultSessionDirPath which resolves first).
			if len(cwd) > 0 && (cwd[0] == '/' || cwd[0] == '\\') {
				cwd = cwd[1:]
			}
			safePath := "--" + strings.NewReplacer("/", "-", "\\", "-", ":", "-").Replace(cwd) + "--"
			correctDir := filepath.Join(agentDir, "sessions", safePath)

			// Create directory if needed
			if !fileExistsMigrations(correctDir) {
				if err := os.MkdirAll(correctDir, 0o755); err != nil {
					return
				}
			}

			// Move the file
			fileName := filepath.Base(file)
			newPath := filepath.Join(correctDir, fileName)

			if fileExistsMigrations(newPath) {
				return // Skip if target exists
			}

			_ = os.Rename(file, newPath)
		}()
	}
}

// migrateCommandsToPrompts migrates commands/ to prompts/ if needed.
// Works for both regular directories and symlinks.
func migrateCommandsToPrompts(baseDir string, label string) bool {
	commandsDir := filepath.Join(baseDir, "commands")
	promptsDir := filepath.Join(baseDir, "prompts")

	if fileExistsMigrations(commandsDir) && !fileExistsMigrations(promptsDir) {
		if err := os.Rename(commandsDir, promptsDir); err == nil {
			fmt.Printf("Migrated %s commands/ → prompts/\n", label)
			return true
		} else {
			fmt.Printf("Warning: Could not migrate %s commands/ to prompts/: %s\n", label, err.Error())
		}
	}
	return false
}

func migrateKeybindingsConfigFile() {
	configPath := filepath.Join(codingagent.GetAgentDir(), "keybindings.json")
	if !fileExistsMigrations(configPath) {
		return
	}

	content, err := os.ReadFile(configPath)
	if err != nil {
		return
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(utils.StripBom(string(content))), &parsed); err != nil {
		return
	}
	if parsed == nil {
		return
	}
	config, migrated := MigrateKeybindingsConfig(parsed)
	if !migrated {
		return
	}
	rewritten, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(configPath, append(rewritten, '\n'), 0o644)
}

// migrateToolsToBin moves fd/rg binaries from tools/ to bin/ if they
// exist.
func migrateToolsToBin() {
	agentDir := codingagent.GetAgentDir()
	toolsDir := filepath.Join(agentDir, "tools")
	binDir := codingagent.GetBinDir()

	if !fileExistsMigrations(toolsDir) {
		return
	}

	binaries := []string{"fd", "rg", "fd.exe", "rg.exe"}
	movedAny := false

	for _, bin := range binaries {
		oldPath := filepath.Join(toolsDir, bin)
		newPath := filepath.Join(binDir, bin)

		if fileExistsMigrations(oldPath) {
			if !fileExistsMigrations(binDir) {
				if err := os.MkdirAll(binDir, 0o755); err != nil {
					continue
				}
			}
			if !fileExistsMigrations(newPath) {
				if err := os.Rename(oldPath, newPath); err == nil {
					movedAny = true
				}
			} else {
				// Target exists, just delete the old one
				_ = os.Remove(oldPath)
			}
		}
	}

	if movedAny {
		fmt.Printf("Migrated managed binaries tools/ → bin/\n")
	}
}

// checkDeprecatedExtensionDirs checks for deprecated hooks/ and tools/
// directories (pi: checkDeprecatedExtensionDirs).
// Note: tools/ may contain fd/rg binaries extracted by pi, so only warn if
// it has other files.
func checkDeprecatedExtensionDirs(baseDir string, label string) []string {
	hooksDir := filepath.Join(baseDir, "hooks")
	toolsDir := filepath.Join(baseDir, "tools")
	var warnings []string

	if fileExistsMigrations(hooksDir) {
		warnings = append(warnings, label+" hooks/ directory found. Hooks have been renamed to extensions.")
	}

	if fileExistsMigrations(toolsDir) {
		// Check if tools/ contains anything other than fd/rg (which are
		// auto-extracted binaries)
		entries, err := os.ReadDir(toolsDir)
		if err == nil {
			hasCustomTools := false
			for _, entry := range entries {
				name := entry.Name()
				lower := strings.ToLower(name)
				if lower != "fd" && lower != "rg" && lower != "fd.exe" && lower != "rg.exe" && !strings.HasPrefix(name, ".") {
					hasCustomTools = true
					break
				}
			}
			if hasCustomTools {
				warnings = append(warnings, label+" tools/ directory contains custom tools. Custom tools have been merged into extensions.")
			}
		}
	}

	return warnings
}

// migrateExtensionSystem runs extension system migrations
// (commands→prompts) and collects warnings about deprecated directories
// (pi: migrateExtensionSystem).
func migrateExtensionSystem(cwd string) []string {
	agentDir := codingagent.GetAgentDir()
	projectDir := filepath.Join(cwd, codingagent.ConfigDirName)

	// Migrate commands/ to prompts/
	migrateCommandsToPrompts(agentDir, "Global")
	migrateCommandsToPrompts(projectDir, "Project")

	// Check for deprecated directories
	warnings := checkDeprecatedExtensionDirs(agentDir, "Global")
	warnings = append(warnings, checkDeprecatedExtensionDirs(projectDir, "Project")...)

	return warnings
}

// ShowDeprecationWarnings prints deprecation warnings and waits for a
// keypress (pi: showDeprecationWarnings).
//
// pig addition: pi's stdin flow-mode wait never resolves on a piped
// stdin's EOF; the caller only invokes this for interactive mode (TTY), so
// the raw-mode keypress is skipped when stdin is not a character device.
func ShowDeprecationWarnings(warnings []string) {
	if len(warnings) == 0 {
		return
	}

	for _, warning := range warnings {
		fmt.Printf("Warning: %s\n", warning)
	}
	fmt.Printf("\nMove your extensions to the extensions/ directory.\n")
	fmt.Printf("Migration guide: %s\n", migrationGuideURL)
	fmt.Printf("Documentation: %s\n", extensionsDocURL)
	fmt.Printf("\nPress any key to continue...\n")

	if isCharDeviceMigrations(os.Stdin) {
		fd := int(os.Stdin.Fd())
		if oldState, err := term.MakeRaw(fd); err == nil {
			var buf [1]byte
			_, _ = os.Stdin.Read(buf[:])
			_ = term.Restore(fd, oldState)
		}
	}
	fmt.Println()
}

// isCharDeviceMigrations reports whether the file is a character device
// (pi: process.stdin.isTTY — cmd/pig has the same helper; core needs its
// own copy).
func isCharDeviceMigrations(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// RunMigrationsResult is runMigrations's return object (pi: {
// migratedAuthProviders, deprecationWarnings }).
type RunMigrationsResult struct {
	MigratedAuthProviders []string
	DeprecationWarnings   []string
}

// RunMigrations runs all migrations. Called once on startup (pi:
// runMigrations).
func RunMigrations(cwd string) RunMigrationsResult {
	migratedAuthProviders := MigrateAuthToAuthJson()
	MigrateSessionsFromAgentRoot()
	migrateToolsToBin()
	migrateKeybindingsConfigFile()
	deprecationWarnings := migrateExtensionSystem(cwd)
	return RunMigrationsResult{
		MigratedAuthProviders: migratedAuthProviders,
		DeprecationWarnings:   deprecationWarnings,
	}
}
