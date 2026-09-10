// tui/autocomplete_test.go — ported from pi: packages/tui/test/autocomplete.test.ts
//
// Declared deviations:
//   - getSuggestions is async in TS → direct synchronous call with
//     context.Background() (see autocomplete.go header).
//   - The "ignores invalid slash command argument completion results" case
//     (editor.test.ts) folds into nil-return; TS's non-array payload has no
//     Go representation (declared at the test).

package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// resolveFdPath resolves the fd binary path (pi: resolveFdPath).
func resolveFdPath() string {
	cmd := exec.Command("which", "fd")
	out, err := cmd.Output()
	if err != nil || len(out) == 0 {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		if line != "" {
			return strings.TrimSpace(line)
		}
	}
	return ""
}

// folderStructure describes a test folder layout (pi: FolderStructure).
type folderStructure struct {
	dirs  []string
	files map[string]string
}

// setupFolder creates the folder structure under baseDir (pi: setupFolder).
func setupFolder(t *testing.T, baseDir string, structure folderStructure) {
	t.Helper()
	for _, dir := range structure.dirs {
		if err := os.MkdirAll(filepath.Join(baseDir, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for filePath, contents := range structure.files {
		fullPath := filepath.Join(baseDir, filePath)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fullPath, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// autocompleteSuggestions is the Go equivalent of calling getSuggestions on
// the provider with a fresh AbortController (pi: getSuggestions helper).
func autocompleteSuggestions(provider *CombinedAutocompleteProvider, lines []string, cursorLine, cursorCol int, force bool) *AutocompleteSuggestions {
	return provider.GetSuggestions(context.Background(), lines, cursorLine, cursorCol, AutocompleteGetSuggestionsOptions{Force: force})
}

// fdReportsExternalSymlinks probes whether the local fd still reports
// symlinked directories whose target lies outside the search root. fd ≥ 10.4
// stopped traversing/reporting them (upstream behavior change; pi pins
// 10.3.0 only for darwin-x64 and otherwise installs latest). The port is
// byte-identical to pi's fd invocation — when the probe fails the
// environmental deviation is declared and the subtests skip.
func fdReportsExternalSymlinks(t *testing.T, fdPath string) bool {
	t.Helper()
	root := t.TempDir()
	base := filepath.Join(root, "cwd")
	outside := filepath.Join(root, "outside")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(outside, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "nested", "file.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../outside", filepath.Join(base, "probe_link")); err != nil {
		t.Fatal(err)
	}
	entries := walkDirectoryWithFd(context.Background(), base, fdPath, "probe_link", 100)
	return len(entries) > 0
}

func TestCombinedAutocompleteProviderExtractPathPrefix(t *testing.T) {
	t.Run("extracts / from 'hey /' when forced", func(t *testing.T) {
		provider := NewCombinedAutocompleteProvider(nil, "/tmp", "")
		lines := []string{"hey /"}
		cursorLine := 0
		cursorCol := 5 // After the "/"

		result := autocompleteSuggestions(provider, lines, cursorLine, cursorCol, true)

		if result == nil {
			t.Fatal("Should return suggestions for root directory")
		}
		if result.Prefix != "/" {
			t.Errorf("Prefix should be '/', got %q", result.Prefix)
		}
	})

	t.Run("extracts /A from '/A' when forced", func(t *testing.T) {
		provider := NewCombinedAutocompleteProvider(nil, "/tmp", "")
		lines := []string{"/A"}
		cursorLine := 0
		cursorCol := 2 // After the "A"

		result := autocompleteSuggestions(provider, lines, cursorLine, cursorCol, true)

		// This might return nil if /A doesn't match anything, which is fine
		// We're mainly testing that the prefix extraction works
		if result != nil && result.Prefix != "/A" {
			t.Errorf("Prefix should be '/A', got %q", result.Prefix)
		}
	})

	t.Run("does not trigger for slash commands", func(t *testing.T) {
		provider := NewCombinedAutocompleteProvider(nil, "/tmp", "")
		lines := []string{"/model"}
		cursorLine := 0
		cursorCol := 6 // After "model"

		result := autocompleteSuggestions(provider, lines, cursorLine, cursorCol, true)

		if result != nil {
			t.Error("Should not trigger for slash commands")
		}
	})

	t.Run("triggers for absolute paths after slash command argument", func(t *testing.T) {
		provider := NewCombinedAutocompleteProvider(nil, "/tmp", "")
		lines := []string{"/command /"}
		cursorLine := 0
		cursorCol := 10 // After the second "/"

		result := autocompleteSuggestions(provider, lines, cursorLine, cursorCol, true)

		if result == nil {
			t.Fatal("Should trigger for absolute paths in command arguments")
		}
		if result.Prefix != "/" {
			t.Errorf("Prefix should be '/', got %q", result.Prefix)
		}
	})
}

func TestCombinedAutocompleteProviderFdFileSuggestions(t *testing.T) {
	fdPath := resolveFdPath()
	if fdPath == "" {
		t.Skip("fd is not available")
	}

	rootDir := t.TempDir()
	baseDir := filepath.Join(rootDir, "cwd")
	outsideDir := filepath.Join(rootDir, "outside")
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outsideDir, 0o755); err != nil {
		t.Fatal(err)
	}

	t.Run("returns all files and folders for empty @ query", func(t *testing.T) {
		setupFolder(t, baseDir, folderStructure{
			dirs:  []string{"src"},
			files: map[string]string{"README.md": "readme"},
		})

		provider := NewCombinedAutocompleteProvider(nil, baseDir, fdPath)
		line := "@"
		result := autocompleteSuggestions(provider, []string{line}, 0, len(line), false)

		var values []string
		if result != nil {
			for _, item := range result.Items {
				values = append(values, item.Value)
			}
		}
		sort.Strings(values)
		want := []string{"@README.md", "@src/"}
		sort.Strings(want)
		if strings.Join(values, ",") != strings.Join(want, ",") {
			t.Errorf("values = %v, want %v", values, want)
		}
	})

	t.Run("matches file with extension in query", func(t *testing.T) {
		setupFolder(t, baseDir, folderStructure{
			files: map[string]string{"file.txt": "content"},
		})

		provider := NewCombinedAutocompleteProvider(nil, baseDir, fdPath)
		line := "@file.txt"
		result := autocompleteSuggestions(provider, []string{line}, 0, len(line), false)

		found := false
		if result != nil {
			for _, item := range result.Items {
				if item.Value == "@file.txt" {
					found = true
				}
			}
		}
		if !found {
			t.Errorf("expected @file.txt in results")
		}
	})

	t.Run("filters are case insensitive", func(t *testing.T) {
		setupFolder(t, baseDir, folderStructure{
			dirs:  []string{"src"},
			files: map[string]string{"README.md": "readme"},
		})

		provider := NewCombinedAutocompleteProvider(nil, baseDir, fdPath)
		line := "@re"
		result := autocompleteSuggestions(provider, []string{line}, 0, len(line), false)

		var values []string
		if result != nil {
			for _, item := range result.Items {
				values = append(values, item.Value)
			}
		}
		if strings.Join(values, ",") != "@README.md" {
			t.Errorf("values = %v, want [@README.md]", values)
		}
	})

	t.Run("ranks directories before files", func(t *testing.T) {
		setupFolder(t, baseDir, folderStructure{
			dirs:  []string{"src"},
			files: map[string]string{"src.txt": "text"},
		})

		provider := NewCombinedAutocompleteProvider(nil, baseDir, fdPath)
		line := "@src"
		result := autocompleteSuggestions(provider, []string{line}, 0, len(line), false)

		if result == nil || len(result.Items) == 0 {
			t.Fatal("expected suggestions")
		}
		firstValue := result.Items[0].Value
		hasSrcFile := false
		for _, item := range result.Items {
			if item.Value == "@src.txt" {
				hasSrcFile = true
			}
		}
		if firstValue != "@src/" {
			t.Errorf("first value = %q, want @src/", firstValue)
		}
		if !hasSrcFile {
			t.Error("expected @src.txt in results")
		}
	})

	t.Run("returns nested file paths", func(t *testing.T) {
		setupFolder(t, baseDir, folderStructure{
			files: map[string]string{"src/index.ts": "export {};\n"},
		})

		provider := NewCombinedAutocompleteProvider(nil, baseDir, fdPath)
		line := "@index"
		result := autocompleteSuggestions(provider, []string{line}, 0, len(line), false)

		found := false
		if result != nil {
			for _, item := range result.Items {
				if item.Value == "@src/index.ts" {
					found = true
				}
			}
		}
		if !found {
			t.Error("expected @src/index.ts in results")
		}
	})

	t.Run("matches deeply nested paths", func(t *testing.T) {
		setupFolder(t, baseDir, folderStructure{
			files: map[string]string{
				"packages/tui/src/autocomplete.ts": "export {};",
				"packages/ai/src/autocomplete.ts":  "export {};",
			},
		})

		provider := NewCombinedAutocompleteProvider(nil, baseDir, fdPath)
		line := "@tui/src/auto"
		result := autocompleteSuggestions(provider, []string{line}, 0, len(line), false)

		hasTui := false
		hasAi := false
		if result != nil {
			for _, item := range result.Items {
				if item.Value == "@packages/tui/src/autocomplete.ts" {
					hasTui = true
				}
				if item.Value == "@packages/ai/src/autocomplete.ts" {
					hasAi = true
				}
			}
		}
		if !hasTui {
			t.Error("expected @packages/tui/src/autocomplete.ts in results")
		}
		if hasAi {
			t.Error("did not expect @packages/ai/src/autocomplete.ts in results")
		}
	})

	t.Run("matches directory in middle of path with --full-path", func(t *testing.T) {
		setupFolder(t, baseDir, folderStructure{
			files: map[string]string{
				"src/components/Button.tsx": "export {};",
				"src/utils/helpers.ts":      "export {};",
			},
		})

		provider := NewCombinedAutocompleteProvider(nil, baseDir, fdPath)
		line := "@components/"
		result := autocompleteSuggestions(provider, []string{line}, 0, len(line), false)

		hasButton := false
		hasHelpers := false
		if result != nil {
			for _, item := range result.Items {
				if item.Value == "@src/components/Button.tsx" {
					hasButton = true
				}
				if item.Value == "@src/utils/helpers.ts" {
					hasHelpers = true
				}
			}
		}
		if !hasButton {
			t.Error("expected @src/components/Button.tsx in results")
		}
		if hasHelpers {
			t.Error("did not expect @src/utils/helpers.ts in results")
		}
	})

	t.Run("scopes fuzzy search to relative directories and searches recursively", func(t *testing.T) {
		setupFolder(t, outsideDir, folderStructure{
			files: map[string]string{
				"nested/alpha.ts":             "export {};",
				"nested/deeper/also-alpha.ts": "export {};",
				"nested/deeper/zzz.ts":        "export {};",
			},
		})

		provider := NewCombinedAutocompleteProvider(nil, baseDir, fdPath)
		line := "@../outside/a"
		result := autocompleteSuggestions(provider, []string{line}, 0, len(line), false)

		hasAlpha := false
		hasAlsoAlpha := false
		hasZzz := false
		if result != nil {
			for _, item := range result.Items {
				if item.Value == "@../outside/nested/alpha.ts" {
					hasAlpha = true
				}
				if item.Value == "@../outside/nested/deeper/also-alpha.ts" {
					hasAlsoAlpha = true
				}
				if item.Value == "@../outside/nested/deeper/zzz.ts" {
					hasZzz = true
				}
			}
		}
		if !hasAlpha || !hasAlsoAlpha {
			t.Errorf("expected scoped alpha results, got %+v", result)
		}
		if hasZzz {
			t.Error("did not expect @../outside/nested/deeper/zzz.ts in results")
		}
	})

	t.Run("quotes paths with spaces for @ suggestions", func(t *testing.T) {
		setupFolder(t, baseDir, folderStructure{
			dirs:  []string{"my folder"},
			files: map[string]string{"my folder/test.txt": "content"},
		})

		provider := NewCombinedAutocompleteProvider(nil, baseDir, fdPath)
		line := "@my"
		result := autocompleteSuggestions(provider, []string{line}, 0, len(line), false)

		found := false
		if result != nil {
			for _, item := range result.Items {
				if item.Value == "@\"my folder/\"" {
					found = true
				}
			}
		}
		if !found {
			t.Error("expected @\"my folder/\" in results")
		}
	})

	t.Run("includes hidden paths but excludes .git", func(t *testing.T) {
		setupFolder(t, baseDir, folderStructure{
			dirs: []string{".pig", ".github", ".git"},
			files: map[string]string{
				".pig/config.json":         "{}",
				".github/workflows/ci.yml": "name: ci",
				".git/config":              "[core]",
			},
		})

		provider := NewCombinedAutocompleteProvider(nil, baseDir, fdPath)
		line := "@"
		result := autocompleteSuggestions(provider, []string{line}, 0, len(line), false)

		hasPi := false
		hasGithub := false
		hasGit := false
		if result != nil {
			for _, item := range result.Items {
				if item.Value == "@.pig/" {
					hasPi = true
				}
				if item.Value == "@.github/" {
					hasGithub = true
				}
				if item.Value == "@.git" || strings.HasPrefix(item.Value, "@.git/") {
					hasGit = true
				}
			}
		}
		if !hasPi || !hasGithub {
			t.Errorf("expected hidden paths in results, got %+v", result)
		}
		if hasGit {
			t.Error("did not expect .git paths in results")
		}
	})

	t.Run("follows symlinked directories for fuzzy @ search", func(t *testing.T) {
		if !fdReportsExternalSymlinks(t, fdPath) {
			t.Skip("local fd does not report symlinks pointing outside the search root (fd >= 10.4 upstream change); pi test env does")
		}
		// pi: fresh mkdtemp per test (beforeEach) — isolate the shared
		// symlink name in its own directory pair.
		// pi: fresh mkdtemp root with cwd/ + outside/ siblings — the
		// symlink target "../outside" must resolve.
		root := t.TempDir()
		baseDir = filepath.Join(root, "cwd")
		outsideDir = filepath.Join(root, "outside")
		if err := os.MkdirAll(baseDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(outsideDir, 0o755); err != nil {
			t.Fatal(err)
		}
		setupFolder(t, baseDir, folderStructure{
			files: map[string]string{"dir/some_file.txt": "real"},
		})
		setupFolder(t, outsideDir, folderStructure{
			files: map[string]string{"some_file.txt": "symlinked"},
		})
		if err := os.Symlink("../outside", filepath.Join(baseDir, "symlinked_dir")); err != nil {
			t.Fatal(err)
		}

		provider := NewCombinedAutocompleteProvider(nil, baseDir, fdPath)
		line := "@some"
		result := autocompleteSuggestions(provider, []string{line}, 0, len(line), false)

		hasDir := false
		hasSymlinked := false
		if result != nil {
			for _, item := range result.Items {
				if item.Value == "@dir/some_file.txt" {
					hasDir = true
				}
				if item.Value == "@symlinked_dir/some_file.txt" {
					hasSymlinked = true
				}
			}
		}
		if !hasDir || !hasSymlinked {
			t.Errorf("expected symlinked results, got %+v", result)
		}
	})

	t.Run("returns symlinked directories when matching their name", func(t *testing.T) {
		if !fdReportsExternalSymlinks(t, fdPath) {
			t.Skip("local fd does not report symlinks pointing outside the search root (fd >= 10.4 upstream change); pi test env does")
		}
		root := t.TempDir()
		baseDir = filepath.Join(root, "cwd")
		outsideDir = filepath.Join(root, "outside")
		if err := os.MkdirAll(baseDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(outsideDir, 0o755); err != nil {
			t.Fatal(err)
		}
		setupFolder(t, outsideDir, folderStructure{
			files: map[string]string{"nested/file.txt": "symlinked"},
		})
		if err := os.Symlink("../outside", filepath.Join(baseDir, "symlinked_dir")); err != nil {
			t.Fatal(err)
		}

		provider := NewCombinedAutocompleteProvider(nil, baseDir, fdPath)
		line := "@symlinked"
		result := autocompleteSuggestions(provider, []string{line}, 0, len(line), false)

		found := false
		if result != nil {
			for _, item := range result.Items {
				if item.Value == "@symlinked_dir/" {
					found = true
				}
			}
		}
		if !found {
			t.Error("expected @symlinked_dir/ in results")
		}
	})

	t.Run("returns symlinked files without requiring type l", func(t *testing.T) {
		setupFolder(t, baseDir, folderStructure{
			files: map[string]string{"original.txt": "content"},
		})
		if err := os.Symlink("original.txt", filepath.Join(baseDir, "link.txt")); err != nil {
			t.Fatal(err)
		}

		provider := NewCombinedAutocompleteProvider(nil, baseDir, fdPath)
		line := "@link"
		result := autocompleteSuggestions(provider, []string{line}, 0, len(line), false)

		found := false
		if result != nil {
			for _, item := range result.Items {
				if item.Value == "@link.txt" {
					found = true
				}
			}
		}
		if !found {
			t.Error("expected @link.txt in results")
		}
	})

	t.Run("returns the same @ suggestions when the cwd path contains the query", func(t *testing.T) {
		normalBaseDir := filepath.Join(rootDir, "cwd-normal")
		queryInPathBaseDir := filepath.Join(rootDir, "cwd-plan-repro")
		if err := os.MkdirAll(normalBaseDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(queryInPathBaseDir, 0o755); err != nil {
			t.Fatal(err)
		}

		structure := folderStructure{
			dirs: []string{"packages/coding-agent/examples/extensions/plan-mode"},
			files: map[string]string{
				"packages/coding-agent/examples/extensions/plan-mode/README.md": "readme",
				"packages/tui/docs/plan.md":                                     "plan",
			},
		}
		setupFolder(t, normalBaseDir, structure)
		setupFolder(t, queryInPathBaseDir, structure)

		query := "@plan"
		normalProvider := NewCombinedAutocompleteProvider(nil, normalBaseDir, fdPath)
		queryInPathProvider := NewCombinedAutocompleteProvider(nil, queryInPathBaseDir, fdPath)

		normalResult := autocompleteSuggestions(normalProvider, []string{query}, 0, len(query), false)
		queryInPathResult := autocompleteSuggestions(queryInPathProvider, []string{query}, 0, len(query), false)

		normalize := func(result *AutocompleteSuggestions) []string {
			var out []string
			if result != nil {
				for _, item := range result.Items {
					out = append(out, item.Label+" :: "+item.Description)
				}
			}
			sort.Strings(out)
			return out
		}

		if strings.Join(normalize(queryInPathResult), "|") != strings.Join(normalize(normalResult), "|") {
			t.Errorf("results differ:\nnormal = %v\nqueryInPath = %v", normalize(normalResult), normalize(queryInPathResult))
		}
		normal := normalize(normalResult)
		foundPlanMode := false
		foundPlanMd := false
		for _, entry := range normal {
			if entry == "plan-mode/ :: packages/coding-agent/examples/extensions/plan-mode" {
				foundPlanMode = true
			}
			if entry == "plan.md :: packages/tui/docs/plan.md" {
				foundPlanMd = true
			}
		}
		if !foundPlanMode || !foundPlanMd {
			t.Errorf("expected plan-mode/ and plan.md entries, got %v", normal)
		}
	})

	t.Run("continues autocomplete inside quoted @ paths", func(t *testing.T) {
		setupFolder(t, baseDir, folderStructure{
			files: map[string]string{
				"my folder/test.txt":  "content",
				"my folder/other.txt": "content",
			},
		})

		provider := NewCombinedAutocompleteProvider(nil, baseDir, fdPath)
		line := "@\"my folder/\""
		result := autocompleteSuggestions(provider, []string{line}, 0, len(line)-1, false)

		if result == nil {
			t.Fatal("Should return suggestions for quoted folder path")
		}
		hasTest := false
		hasOther := false
		for _, item := range result.Items {
			if item.Value == "@\"my folder/test.txt\"" {
				hasTest = true
			}
			if item.Value == "@\"my folder/other.txt\"" {
				hasOther = true
			}
		}
		if !hasTest || !hasOther {
			t.Errorf("expected quoted suggestions, got %+v", result.Items)
		}
	})

	t.Run("applies quoted @ completion without duplicating closing quote", func(t *testing.T) {
		setupFolder(t, baseDir, folderStructure{
			files: map[string]string{"my folder/test.txt": "content"},
		})

		provider := NewCombinedAutocompleteProvider(nil, baseDir, fdPath)
		line := "@\"my folder/te\""
		cursorCol := len(line) - 1
		result := autocompleteSuggestions(provider, []string{line}, 0, cursorCol, false)

		if result == nil {
			t.Fatal("Should return suggestions for quoted @ path")
		}
		var item *AutocompleteItem
		for i := range result.Items {
			if result.Items[i].Value == "@\"my folder/test.txt\"" {
				item = &result.Items[i]
			}
		}
		if item == nil {
			t.Fatal("Should find test.txt suggestion")
		}

		applied := provider.ApplyCompletion([]string{line}, 0, cursorCol, *item, result.Prefix)
		if applied.Lines[0] != "@\"my folder/test.txt\" " {
			t.Errorf("applied = %q, want %q", applied.Lines[0], "@\"my folder/test.txt\" ")
		}
	})
}

func TestCombinedAutocompleteProviderDotSlashPathCompletion(t *testing.T) {
	baseDir := t.TempDir()

	t.Run("preserves ./ prefix when completing paths", func(t *testing.T) {
		setupFolder(t, baseDir, folderStructure{
			files: map[string]string{
				"update.sh": "#!/bin/bash",
				"utils.ts":  "export {};",
			},
		})

		provider := NewCombinedAutocompleteProvider(nil, baseDir, "")
		line := "./up"
		result := autocompleteSuggestions(provider, []string{line}, 0, len(line), true)

		if result == nil {
			t.Fatal("Should return suggestions for ./ path")
		}
		found := false
		for _, item := range result.Items {
			if item.Value == "./update.sh" {
				found = true
			}
		}
		if !found {
			t.Errorf("Expected ./update.sh in %+v", result.Items)
		}
	})

	t.Run("preserves ./ prefix for directory completions", func(t *testing.T) {
		setupFolder(t, baseDir, folderStructure{
			dirs:  []string{"src"},
			files: map[string]string{"src/index.ts": "export {};"},
		})

		provider := NewCombinedAutocompleteProvider(nil, baseDir, "")
		line := "./sr"
		result := autocompleteSuggestions(provider, []string{line}, 0, len(line), true)

		if result == nil {
			t.Fatal("Should return suggestions for ./ directory path")
		}
		found := false
		for _, item := range result.Items {
			if item.Value == "./src/" {
				found = true
			}
		}
		if !found {
			t.Errorf("Expected ./src/ in %+v", result.Items)
		}
	})
}

func TestCombinedAutocompleteProviderQuotedPathCompletion(t *testing.T) {
	baseDir := t.TempDir()

	t.Run("quotes paths with spaces for direct completion", func(t *testing.T) {
		setupFolder(t, baseDir, folderStructure{
			dirs:  []string{"my folder"},
			files: map[string]string{"my folder/test.txt": "content"},
		})

		provider := NewCombinedAutocompleteProvider(nil, baseDir, "")
		line := "my"
		result := autocompleteSuggestions(provider, []string{line}, 0, len(line), true)

		if result == nil {
			t.Fatal("Should return suggestions for path completion")
		}
		found := false
		for _, item := range result.Items {
			if item.Value == "\"my folder/\"" {
				found = true
			}
		}
		if !found {
			t.Errorf("Expected \"my folder/\" in %+v", result.Items)
		}
	})

	t.Run("continues completion inside quoted paths", func(t *testing.T) {
		setupFolder(t, baseDir, folderStructure{
			files: map[string]string{
				"my folder/test.txt":  "content",
				"my folder/other.txt": "content",
			},
		})

		provider := NewCombinedAutocompleteProvider(nil, baseDir, "")
		line := "\"my folder/\""
		result := autocompleteSuggestions(provider, []string{line}, 0, len(line)-1, true)

		if result == nil {
			t.Fatal("Should return suggestions for quoted folder path")
		}
		hasTest := false
		hasOther := false
		for _, item := range result.Items {
			if item.Value == "\"my folder/test.txt\"" {
				hasTest = true
			}
			if item.Value == "\"my folder/other.txt\"" {
				hasOther = true
			}
		}
		if !hasTest || !hasOther {
			t.Errorf("expected quoted suggestions, got %+v", result.Items)
		}
	})

	t.Run("applies quoted completion without duplicating closing quote", func(t *testing.T) {
		setupFolder(t, baseDir, folderStructure{
			files: map[string]string{"my folder/test.txt": "content"},
		})

		provider := NewCombinedAutocompleteProvider(nil, baseDir, "")
		line := "\"my folder/te\""
		cursorCol := len(line) - 1
		result := autocompleteSuggestions(provider, []string{line}, 0, cursorCol, true)

		if result == nil {
			t.Fatal("Should return suggestions for quoted path")
		}
		var item *AutocompleteItem
		for i := range result.Items {
			if result.Items[i].Value == "\"my folder/test.txt\"" {
				item = &result.Items[i]
			}
		}
		if item == nil {
			t.Fatal("Should find test.txt suggestion")
		}

		applied := provider.ApplyCompletion([]string{line}, 0, cursorCol, *item, result.Prefix)
		if applied.Lines[0] != "\"my folder/test.txt\"" {
			t.Errorf("applied = %q, want %q", applied.Lines[0], "\"my folder/test.txt\"")
		}
	})
}

// pi v0.85.1: "ranks shallower same-score @ matches before deeper matches"
func TestCombinedAutocompleteProviderRanksShallowerSameScoreAtMatchesBeforeDeeperMatches(t *testing.T) {
	fdPath := resolveFdPath()
	if fdPath == "" {
		t.Skip("fd is not available")
	}

	baseDir := t.TempDir()
	setupFolder(t, baseDir, folderStructure{
		dirs: []string{
			"scope/aaa/venv/lib/python3.12/site-packages/pkg/core/profile",
			"scope/projects",
		},
	})

	provider := NewCombinedAutocompleteProvider(nil, baseDir, fdPath)
	line := "@scope/pro"
	result := provider.GetSuggestions(context.Background(), []string{line}, 0, len(line), AutocompleteGetSuggestionsOptions{})

	values := []string{}
	for _, item := range result.Items {
		values = append(values, item.Value)
	}
	if len(values) == 0 || values[0] != "@scope/projects/" {
		t.Fatalf("values[0] = %q, want @scope/projects/", values)
	}
	found := false
	for _, value := range values {
		if value == "@scope/aaa/venv/lib/python3.12/site-packages/pkg/core/profile/" {
			found = true
		}
	}
	if !found {
		t.Fatalf("deep profile match missing from %q", values)
	}
}

// pi v0.85.1: "includes scoped direct children when recursive @ matches are
// flooded"
func TestCombinedAutocompleteProviderIncludesScopedDirectChildrenWhenRecursiveAtMatchesAreFlooded(t *testing.T) {
	fdPath := resolveFdPath()
	if fdPath == "" {
		t.Skip("fd is not available")
	}

	baseDir := t.TempDir()
	dirs := []string{"scope/projects"}
	for index := 0; index < 250; index++ {
		dirs = append(dirs, fmt.Sprintf("scope/a%03d/venv/lib/python3.12/site-packages/pkg/core/profile", index+1))
	}
	setupFolder(t, baseDir, folderStructure{dirs: dirs})

	provider := NewCombinedAutocompleteProvider(nil, baseDir, fdPath)
	line := "@scope/pro"
	result := provider.GetSuggestions(context.Background(), []string{line}, 0, len(line), AutocompleteGetSuggestionsOptions{})

	values := []string{}
	for _, item := range result.Items {
		values = append(values, item.Value)
	}
	if len(values) == 0 || values[0] != "@scope/projects/" {
		t.Fatalf("values[0] = %q, want @scope/projects/", values)
	}
	found := false
	for _, value := range values {
		if strings.Contains(value, "/profile/") {
			found = true
		}
	}
	if !found {
		t.Fatalf("deep fuzzy matches missing after direct children: %q", values)
	}
}
