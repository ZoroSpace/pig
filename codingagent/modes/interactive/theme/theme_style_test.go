package theme

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ZoroSpace/pig/tui"
)

// Port of test/theme-style.test.ts (v0.99.1).

type styleThemeFile struct {
	Name       string          `json:"name"`
	Appearance string          `json:"appearance,omitempty"`
	Vars       map[string]any  `json:"vars,omitempty"`
	Colors     map[string]any  `json:"colors"`
	Export     map[string]any  `json:"export,omitempty"`
	Schema     json.RawMessage `json:"$schema,omitempty"`
}

// loadStyleTheme loads a copy of a built-in theme, modified by `edit` (pi:
// loadTheme in theme-style.test.ts).
func loadStyleTheme(t *testing.T, base string, edit func(*styleThemeFile)) *Theme {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "theme", base+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var themeJson styleThemeFile
	if err := json.Unmarshal(data, &themeJson); err != nil {
		t.Fatal(err)
	}
	if edit != nil {
		edit(&themeJson)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, themeJson.Name+".json")
	out, err := json.Marshal(themeJson)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatal(err)
	}
	theme, err := LoadThemeFromPath(path, ColorModeTruecolor)
	if err != nil {
		t.Fatal(err)
	}
	return theme
}

func TestThemeStyles(t *testing.T) {
	// pig-only hardening: the palette-only subtest expects "no terminal info
	// → follow the theme's own base"; a host COLORFGBG (e.g. "0;15" = light)
	// would otherwise be consulted by DetectTerminalTheme's fallback chain
	// and flip the expectation (found in the S11 closing smoke session's
	// shell env). pi's suite has the same latent env sensitivity.
	t.Setenv("COLORFGBG", "")
	t.Cleanup(func() { SetTerminalColors(tui.TerminalColors{}) })

	t.Run("renders theme tokens the same as the generic text styler", func(t *testing.T) {
		theme := loadStyleTheme(t, "dark", nil)
		bg := theme.Colors()["toolSuccessBg"]
		fg := theme.Colors()["success"]
		got := theme.Style("Ready", ThemeStyle{Fg: ColorSuccess, Bg: "toolSuccessBg", TextAttributes: tui.TextAttributes{Bold: true}})
		want := tui.StyleText("Ready", tui.TextStyle{Fg: &fg, Bg: &bg, TextAttributes: tui.TextAttributes{Bold: true}}, ColorModeTruecolor)
		if got != want {
			t.Fatalf("style = %q, want %q", got, want)
		}
	})

	t.Run("rejects unknown tokens and tokens in the wrong slot", func(t *testing.T) {
		theme := loadStyleTheme(t, "dark", nil)
		func() {
			defer func() {
				if recover() == nil {
					t.Fatal("expected unknown token to panic")
				}
			}()
			theme.Style("x", ThemeStyle{Fg: "notAToken"})
		}()
		func() {
			defer func() {
				rec := recover()
				if rec == nil {
					t.Fatal("expected background token in fg slot to panic")
				}
				if msg, ok := rec.(string); !ok || !strings.Contains(msg, "Unknown theme color: userMessageBg") {
					t.Fatalf("panic = %v", rec)
				}
			}()
			theme.Style("x", ThemeStyle{Fg: "userMessageBg"})
		}()
	})

	t.Run("loads OKLCH theme values", func(t *testing.T) {
		theme := loadStyleTheme(t, "dark", func(json *styleThemeFile) {
			json.Colors["accent"] = "oklch(62% 0.1 200)"
		})
		color := theme.Colors()[ColorAccent]
		if color.Kind != tui.ColorKindOklch || color.L != 0.62 || color.C != 0.1 || color.H != 200 {
			t.Fatalf("accent = %+v", color)
		}
	})

	t.Run("loads OKHSL theme values, including through variables", func(t *testing.T) {
		theme := loadStyleTheme(t, "dark", func(json *styleThemeFile) {
			if json.Vars == nil {
				json.Vars = map[string]any{}
			}
			json.Vars["brand"] = "okhsl(250 60% 55%)"
			json.Colors["accent"] = "brand"
			json.Colors["error"] = "okhsl(20 90% 60%)"
		})
		accent := tui.ColorToHex(theme.Colors()[ColorAccent])
		wantAccent, _ := tui.NewOkhslColor(250, 0.6, 0.55)
		if accent != tui.ColorToHex(wantAccent) {
			t.Fatalf("accent = %q, want %q", accent, tui.ColorToHex(wantAccent))
		}
		errColor := tui.ColorToHex(theme.Colors()[ColorError])
		wantError, _ := tui.NewOkhslColor(20, 0.9, 0.6)
		if errColor != tui.ColorToHex(wantError) {
			t.Fatalf("error = %q, want %q", errColor, tui.ColorToHex(wantError))
		}
	})

	t.Run("detects the appearance unless it is declared", func(t *testing.T) {
		if theme := loadStyleTheme(t, "dark", nil); theme.Appearance() != TerminalThemeDark {
			t.Fatalf("dark appearance = %q", theme.Appearance())
		}
		if theme := loadStyleTheme(t, "light", nil); theme.Appearance() != TerminalThemeLight {
			t.Fatalf("light appearance = %q", theme.Appearance())
		}
		// Without a declaration, the appearance is detected from the theme's own colors.
		for _, base := range []string{"dark", "light"} {
			theme := loadStyleTheme(t, base, func(json *styleThemeFile) { json.Appearance = "" })
			if theme.Appearance() != TerminalTheme(base) {
				t.Fatalf("%s detected appearance = %q", base, theme.Appearance())
			}
		}
		if theme := loadStyleTheme(t, "dark", func(json *styleThemeFile) { json.Appearance = "light" }); theme.Appearance() != TerminalThemeLight {
			t.Fatalf("declared light = %q", theme.Appearance())
		}

		// Palette colors 0-15 follow the terminal palette, so such themes
		// follow the terminal background.
		paletteOnly := loadStyleTheme(t, "dark", func(json *styleThemeFile) {
			json.Appearance = ""
			for key := range json.Colors {
				if strings.HasSuffix(key, "Bg") {
					json.Colors[key] = 0
				} else {
					json.Colors[key] = 7
				}
			}
		})
		if paletteOnly.Appearance() != TerminalThemeDark {
			t.Fatalf("palette-only appearance = %q", paletteOnly.Appearance())
		}
		SetTerminalColors(tui.TerminalColors{Background: &tui.RgbColor{R: 250, G: 250, B: 250}})
		if paletteOnly.Appearance() != TerminalThemeLight {
			t.Fatalf("palette-only with light terminal = %q", paletteOnly.Appearance())
		}
	})

	t.Run("renders empty tokens as terminal defaults and reports concrete colors for them", func(t *testing.T) {
		SetTerminalColors(tui.TerminalColors{})
		theme := loadStyleTheme(t, "dark", func(json *styleThemeFile) {
			json.Colors["text"] = ""
			json.Colors["userMessageBg"] = ""
		})
		if got := theme.Fg(ColorText, "x"); got != "\x1b[39mx\x1b[39m" {
			t.Fatalf("fg(text) = %q", got)
		}
		if got := theme.Bg(BgUserMessageBg, "x"); got != "\x1b[49mx\x1b[49m" {
			t.Fatalf("bg(userMessageBg) = %q", got)
		}
		if got := tui.ColorToHex(theme.Colors()[ColorText]); got != "#e5e5e7" {
			t.Fatalf("colors.text = %q", got)
		}
		if got := tui.ColorToHex(theme.Colors()[BgUserMessageBg]); got != "#000000" {
			t.Fatalf("colors.userMessageBg = %q", got)
		}

		SetTerminalColors(tui.TerminalColors{
			Foreground: &tui.RgbColor{R: 200, G: 210, B: 220},
			Background: &tui.RgbColor{R: 10, G: 20, B: 30},
		})
		if got := tui.ColorToHex(theme.Colors()[ColorText]); got != "#c8d2dc" {
			t.Fatalf("colors.text = %q", got)
		}
		if got := tui.ColorToHex(theme.Colors()[BgUserMessageBg]); got != "#0a141e" {
			t.Fatalf("colors.userMessageBg = %q", got)
		}
	})
}
