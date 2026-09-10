// pi: test/resolve-config-value.test.ts — config value resolution (literals,
// env templates, shell commands, caching).
package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ZoroSpace/pig/codingagent/utils"
)

func TestResolveConfigValue(t *testing.T) {
	clearCache := func() { ClearConfigValueCache() }

	t.Run("resolves literals, environment templates, and escapes", func(t *testing.T) {
		t.Setenv("TEST_CONFIG_LEFT", "left")
		t.Setenv("TEST_CONFIG_RIGHT", "right")
		clearCache()

		if got, _ := ResolveConfigValue("literal-key", nil); got != "literal-key" {
			t.Fatalf("got %q", got)
		}
		if got, _ := ResolveConfigValue("$TEST_CONFIG_LEFT", nil); got != "left" {
			t.Fatalf("got %q", got)
		}
		if got, _ := ResolveConfigValue("${TEST_CONFIG_LEFT}_$TEST_CONFIG_RIGHT", nil); got != "left_right" {
			t.Fatalf("got %q", got)
		}
		if got, _ := ResolveConfigValue("$$TEST_CONFIG_LEFT", nil); got != "$TEST_CONFIG_LEFT" {
			t.Fatalf("got %q", got)
		}
		if got, _ := ResolveConfigValue("$!literal-$TEST_CONFIG_RIGHT", nil); got != "!literal-right" {
			t.Fatalf("got %q", got)
		}
	})

	t.Run("uses credential-scoped environment before process.env", func(t *testing.T) {
		t.Setenv("TEST_CONFIG_SCOPED", "process")
		clearCache()

		got, found := ResolveConfigValue("$TEST_CONFIG_SCOPED", map[string]string{"TEST_CONFIG_SCOPED": "credential"})
		if !found || got != "credential" {
			t.Fatalf("got %q, %v", got, found)
		}
	})

	t.Run("executes shell commands and trims their output", func(t *testing.T) {
		clearCache()
		if got, found := ResolveConfigValue("!echo '  spaced-key  '", nil); !found || got != "spaced-key" {
			t.Fatalf("got %q, %v", got, found)
		}
		if got, found := ResolveConfigValue("!printf 'line1\\nline2'", nil); !found || got != "line1\nline2" {
			t.Fatalf("got %q, %v", got, found)
		}
		if got, found := ResolveConfigValue("!echo 'hello world' | tr ' ' '-'", nil); !found || got != "hello-world" {
			t.Fatalf("got %q, %v", got, found)
		}
	})

	t.Run("returns undefined when command resolution fails", func(t *testing.T) {
		clearCache()
		for _, command := range []string{"!exit 1", "!nonexistent-command-12345", "!printf ''"} {
			if got, found := ResolveConfigValue(command, nil); found {
				t.Fatalf("%s: got %q, want undefined", command, got)
			}
		}
	})

	t.Run("caches successful and failed commands until explicitly cleared", func(t *testing.T) {
		agentDir := t.TempDir()
		clearCache()
		counterFile := filepath.Join(agentDir, "counter")
		if err := os.WriteFile(counterFile, []byte("0"), 0o644); err != nil {
			t.Fatal(err)
		}
		escapedPath := filepath.ToSlash(counterFile)
		success := "!sh -c 'count=$(cat \"" + escapedPath + "\"); echo $((count + 1)) > \"" + escapedPath + "\"; echo value'"

		if got, _ := ResolveConfigValue(success, nil); got != "value" {
			t.Fatalf("first call got %q", got)
		}
		if got, _ := ResolveConfigValue(success, nil); got != "value" {
			t.Fatalf("second call got %q", got)
		}
		if count := readCounterFile(t, counterFile); count != "1" {
			t.Fatalf("counter = %q (command should have run once)", count)
		}

		clearCache()
		if got, _ := ResolveConfigValue(success, nil); got != "value" {
			t.Fatalf("after clear got %q", got)
		}
		if count := readCounterFile(t, counterFile); count != "2" {
			t.Fatalf("counter = %q", count)
		}

		failure := "!sh -c 'count=$(cat \"" + escapedPath + "\"); echo $((count + 1)) > \"" + escapedPath + "\"; exit 1'"
		if _, found := ResolveConfigValue(failure, nil); found {
			t.Fatal("expected failure to be undefined")
		}
		if _, found := ResolveConfigValue(failure, nil); found {
			t.Fatal("expected cached failure to be undefined")
		}
		if count := readCounterFile(t, counterFile); count != "3" {
			t.Fatalf("counter = %q (failure should be cached)", count)
		}
	})

	t.Run("does not cache environment values", func(t *testing.T) {
		clearCache()
		t.Setenv("TEST_CONFIG_DYNAMIC", "first")
		if got, _ := ResolveConfigValue("$TEST_CONFIG_DYNAMIC", nil); got != "first" {
			t.Fatalf("got %q", got)
		}
		t.Setenv("TEST_CONFIG_DYNAMIC", "second")
		if got, _ := ResolveConfigValue("$TEST_CONFIG_DYNAMIC", nil); got != "second" {
			t.Fatalf("got %q", got)
		}
	})

	t.Run("uncached resolution executes a command on every call", func(t *testing.T) {
		agentDir := t.TempDir()
		clearCache()
		counterFile := filepath.Join(agentDir, "uncached-counter")
		if err := os.WriteFile(counterFile, []byte("0"), 0o644); err != nil {
			t.Fatal(err)
		}
		escapedPath := filepath.ToSlash(counterFile)
		command := "!sh -c 'count=$(cat \"" + escapedPath + "\"); echo $((count + 1)) > \"" + escapedPath + "\"; echo value'"
		if got, _ := ResolveConfigValueUncached(command, nil); got != "value" {
			t.Fatalf("first call got %q", got)
		}
		if got, _ := ResolveConfigValueUncached(command, nil); got != "value" {
			t.Fatalf("second call got %q", got)
		}
		if count := readCounterFile(t, counterFile); count != "2" {
			t.Fatalf("counter = %q", count)
		}
	})

	t.Run("uses stdin when the configured shell requires it", func(t *testing.T) {
		// pi mocks getShellConfig and pins process.platform to win32 (the
		// configured-shell branch); pig pins the same two hooks.
		originalPlatform := configValuePlatform
		originalGetShellConfig := getShellConfigForConfigValue
		t.Cleanup(func() {
			configValuePlatform = originalPlatform
			getShellConfigForConfigValue = originalGetShellConfig
		})
		getShellConfigForConfigValue = func() (utils.ShellConfig, error) {
			return utils.ShellConfig{Shell: "/bin/bash", Args: []string{"-s"}, CommandTransport: "stdin"}, nil
		}
		configValuePlatform = "windows"
		clearCache()

		got, found := ResolveConfigValueUncached("!name='World'; echo \"Hello, ${name}!\"", nil)
		if !found || got != "Hello, World!" {
			t.Fatalf("got %q, %v", got, found)
		}
	})
}

func readCounterFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(content))
}
