// scrollbar_theme_test.go (pi: test/scrollbar-theme.test.ts) — fullscreen
// theme colors: optional scrollbar and search-highlight tokens with their
// fallbacks. Ported in S5b (existed at the pi baseline; pig deferred it to
// this batch, ported with the v0.85.1 scrollbarTrack/fg-thumb migration).
package theme

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// initValidatorSeam mirrors pi's module-level
// `setThemeJsonValidator(validateThemeJson)`.
func initValidatorSeam() {
	SetThemeJsonValidator(ValidateThemeJson)
}

func loadDarkThemeMap(t *testing.T) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "theme", "dark.json"))
	if err != nil {
		t.Fatal(err)
	}
	var theme map[string]any
	if err := json.Unmarshal(raw, &theme); err != nil {
		t.Fatal(err)
	}
	return theme
}

func writeThemeFile(t *testing.T, theme map[string]any) string {
	t.Helper()
	testDir := t.TempDir()
	themePath := filepath.Join(testDir, theme["name"].(string)+".json")
	raw, err := json.Marshal(theme)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(themePath, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return themePath
}

func TestScrollbarThemeFallbacks(t *testing.T) {
	initValidatorSeam()

	for _, tc := range []struct{ token, fallback string }{
		{"scrollbarTrack", "muted"},
		{"scrollbarThumb", "text"},
	} {
		t.Run("falls back to "+tc.fallback+" when "+tc.token+" omitted", func(t *testing.T) {
			themeJson := loadDarkThemeMap(t)
			themeJson["name"] = "missing-" + tc.token + "-theme"
			colors := themeJson["colors"].(map[string]any)
			delete(colors, tc.token)

			loaded, err := LoadThemeFromPath(writeThemeFile(t, themeJson), ColorModeTruecolor)
			if err != nil {
				t.Fatal(err)
			}
			if loaded.GetFgAnsi(ThemeColor(tc.token)) != loaded.GetFgAnsi(ThemeColor(tc.fallback)) {
				t.Fatalf("%s does not fall back to %s", tc.token, tc.fallback)
			}
		})
	}
}

func TestScrollbarThemeExplicitColors(t *testing.T) {
	initValidatorSeam()

	themeJson := loadDarkThemeMap(t)
	themeJson["name"] = "custom-scrollbar-theme"
	colors := themeJson["colors"].(map[string]any)
	colors["scrollbarTrack"] = "#654321"
	colors["scrollbarThumb"] = "#123456"

	loaded, err := LoadThemeFromPath(writeThemeFile(t, themeJson), ColorModeTruecolor)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := loaded.GetFgAnsi("scrollbarTrack"), "\x1b[38;2;101;67;33m"; got != want {
		t.Fatalf("scrollbarTrack = %q, want %q", got, want)
	}
	if got, want := loaded.GetFgAnsi("scrollbarThumb"), "\x1b[38;2;18;52;86m"; got != want {
		t.Fatalf("scrollbarThumb = %q, want %q", got, want)
	}
}

func TestSearchHighlightThemeFallbacks(t *testing.T) {
	initValidatorSeam()

	themeJson := loadDarkThemeMap(t)
	themeJson["name"] = "legacy-search-theme"
	colors := themeJson["colors"].(map[string]any)
	delete(colors, "searchMatchBg")
	delete(colors, "searchMatchText")

	loaded, err := LoadThemeFromPath(writeThemeFile(t, themeJson), ColorModeTruecolor)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.GetBgAnsi("searchMatchBg") != loaded.GetBgAnsi("selectedBg") {
		t.Fatal("searchMatchBg does not fall back to selectedBg")
	}
	if loaded.GetFgAnsi("searchMatchText") != loaded.GetFgAnsi("text") {
		t.Fatal("searchMatchText does not fall back to text")
	}
}

func TestSearchHighlightThemeExplicitColors(t *testing.T) {
	initValidatorSeam()

	themeJson := loadDarkThemeMap(t)
	themeJson["name"] = "custom-search-theme"
	colors := themeJson["colors"].(map[string]any)
	colors["searchMatchBg"] = "#112233"
	colors["searchMatchText"] = "#223344"

	loaded, err := LoadThemeFromPath(writeThemeFile(t, themeJson), ColorModeTruecolor)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := loaded.GetBgAnsi("searchMatchBg"), "\x1b[48;2;17;34;51m"; got != want {
		t.Fatalf("searchMatchBg = %q, want %q", got, want)
	}
	if got, want := loaded.GetFgAnsi("searchMatchText"), "\x1b[38;2;34;51;68m"; got != want {
		t.Fatalf("searchMatchText = %q, want %q", got, want)
	}
}
