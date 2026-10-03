package theme

import (
	"testing"

	"github.com/ZoroSpace/pig/tui"
)

// pig-only: the promoted built-in palettes (2026-10-03,
// docs/feat/BUILTIN-THEME-PALETTES.md). dark/light stay untouched; the new
// themes must load from the embedded assets and expose their palette colors
// verbatim.

func TestBuiltinOneDarkPro(t *testing.T) {
	t.Run("loads with dark appearance and One Dark Pro accent", func(t *testing.T) {
		theme := GetThemeByName("one-dark-pro")
		if theme == nil {
			t.Fatal("one-dark-pro theme not found")
		}
		if theme.Appearance() != TerminalThemeDark {
			t.Fatalf("appearance = %q, want dark", theme.Appearance())
		}
		if got := tui.ColorToHex(theme.Colors()[ColorAccent]); got != "#c678dd" {
			t.Fatalf("accent = %q, want #c678dd", got)
		}
		if got := tui.ColorToHex(theme.Colors()[ColorText]); got != "#abb2bf" {
			t.Fatalf("text = %q, want #abb2bf", got)
		}
	})

	t.Run("is listed as a built-in theme", func(t *testing.T) {
		found := false
		for _, name := range GetAvailableThemes() {
			if name == "one-dark-pro" {
				found = true
			}
		}
		if !found {
			t.Fatal("one-dark-pro missing from GetAvailableThemes")
		}
	})
}

func TestBuiltinGitHubLight(t *testing.T) {
	t.Run("loads with light appearance and light-blue accent", func(t *testing.T) {
		theme := GetThemeByName("github-light")
		if theme == nil {
			t.Fatal("github-light theme not found")
		}
		if theme.Appearance() != TerminalThemeLight {
			t.Fatalf("appearance = %q, want light", theme.Appearance())
		}
		// pig-only (2026-10-03): accent is the file/path color; the user
		// dimmed it to the same pale blue as the input box (thinkingMedium).
		if got := tui.ColorToHex(theme.Colors()[ColorAccent]); got != "#6f9fd0" {
			t.Fatalf("accent = %q, want #6f9fd0", got)
		}
		if got := tui.ColorToHex(theme.Colors()[ColorText]); got != "#1f2328" {
			t.Fatalf("text = %q, want #1f2328", got)
		}
	})

	t.Run("list markers use the body text color (pig-only)", func(t *testing.T) {
		// pig-only (2026-10-03, user request): the list marker (ordered-list
		// numbers and `-` bullets share this token) is black/dark instead of
		// Primer's violet.
		theme := GetThemeByName("github-light")
		if theme == nil {
			t.Fatal("github-light theme not found")
		}
		if got := tui.ColorToHex(theme.Colors()[ColorMdListBullet]); got != "#1f2328" {
			t.Fatalf("mdListBullet = %q, want #1f2328", got)
		}
	})

	t.Run("defines the optional pig-only rule tokens", func(t *testing.T) {
		theme := GetThemeByName("github-light")
		if theme == nil {
			t.Fatal("github-light theme not found")
		}
		for token, want := range map[string]string{
			"userMessageRule": "#6f9fd0",
			"toolSuccessRule": "#85a893",
			"toolErrorRule":   "#c68f8f",
		} {
			if !theme.HasFgToken(token) {
				t.Fatalf("%s token missing", token)
			}
			if got := tui.ColorToHex(theme.Colors()[token]); got != want {
				t.Fatalf("%s = %q, want %q", token, got, want)
			}
		}
	})

	t.Run("one-dark-pro pins the user message rule to its blue", func(t *testing.T) {
		theme := GetThemeByName("one-dark-pro")
		if theme == nil {
			t.Fatal("one-dark-pro theme not found")
		}
		if !theme.HasFgToken("userMessageRule") {
			t.Fatal("userMessageRule token missing")
		}
		if got := tui.ColorToHex(theme.Colors()["userMessageRule"]); got != "#61afef" {
			t.Fatalf("userMessageRule = %q, want #61afef", got)
		}
	})

	t.Run("FgOptional falls back when the token is undefined", func(t *testing.T) {
		theme := GetThemeByName("dark")
		if theme == nil {
			t.Fatal("dark theme not found")
		}
		if theme.HasFgToken("userMessageRule") {
			t.Fatal("dark should not define userMessageRule")
		}
		want := theme.Fg("text", "x")
		if got := theme.FgOptional("userMessageRule", "text", "x"); got != want {
			t.Fatalf("FgOptional fallback = %q, want %q", got, want)
		}
		wantDefined := theme.Fg("dim", "x")
		if got := theme.FgOptional("dim", "text", "x"); got != wantDefined {
			t.Fatalf("FgOptional defined = %q, want %q", got, wantDefined)
		}
	})

	t.Run("is listed as a built-in theme", func(t *testing.T) {
		found := false
		for _, name := range GetAvailableThemes() {
			if name == "github-light" {
				found = true
			}
		}
		if !found {
			t.Fatal("github-light missing from GetAvailableThemes")
		}
	})
}

func TestLegacyBuiltinThemesStillLoad(t *testing.T) {
	for _, name := range []string{"dark", "light"} {
		theme := GetThemeByName(name)
		if theme == nil {
			t.Fatalf("%s theme not found", name)
		}
	}
}
