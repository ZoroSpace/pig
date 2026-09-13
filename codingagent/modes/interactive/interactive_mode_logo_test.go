package interactive

// Regression tests for the pig-only ASCII art startup logo
// (buildPigLogo in interactive_mode.go). pi prints the plain single-line
// "pi v0.85.1" header; pig shows a figlet "pig" art block with the version
// trailing the bottom line (user-requested deviation).

import (
	"regexp"
	"strings"
	"testing"

	"github.com/ZoroSpace/pig/codingagent/modes/interactive/theme"
	"github.com/ZoroSpace/pig/tui"
)

var sgrSequence = regexp.MustCompile("\x1b\\[[0-9;]*m")

// expectedPigLogoArt is the bare-text art (version excluded from the last
// line's expectation here; see TestBuildPigLogoAppendsDimVersion).
var expectedPigLogoArt = []string{
	" ____    _         ",
	"|  _ \\  (_)   __ _ ",
	"| |_) | | |  / _` |",
	"|  __/  | | | (_| |",
	"|_|     |_|  \\__, |",
	"             |___/",
}

func newLogoTestTheme(t *testing.T) *theme.Theme {
	t.Helper()
	tl, err := theme.NewTheme(
		map[string]theme.ColorValue{"accent": "#ff0000", "dim": "#888888", "muted": "#666666"},
		nil,
		theme.ColorMode256,
		nil,
	)
	if err != nil {
		t.Fatalf("NewTheme: %v", err)
	}
	return tl
}

func TestBuildPigLogoArt(t *testing.T) {
	logo := buildPigLogo(newLogoTestTheme(t), "0.85.1")
	lines := strings.Split(logo, "\n")
	if len(lines) != len(expectedPigLogoArt) {
		t.Fatalf("logo has %d lines, want %d:\n%s", len(lines), len(expectedPigLogoArt), logo)
	}
	for i, line := range lines {
		bare := sgrSequence.ReplaceAllString(line, "")
		want := expectedPigLogoArt[i]
		if i == len(lines)-1 {
			// The version tag trails the bottom art line.
			want += " v0.85.1"
		}
		if bare != want {
			t.Errorf("line %d = %q, want %q", i, bare, want)
		}
	}
}

func TestBuildPigLogoColumnWidth(t *testing.T) {
	// Every art line is uniformly 19 columns so the block stays aligned
	// regardless of theme color sequence lengths.
	logo := buildPigLogo(newLogoTestTheme(t), "0.85.1")
	lines := strings.Split(logo, "\n")
	for i, line := range lines {
		if w := tui.VisibleWidth(line); w != 19 && i < len(lines)-1 {
			t.Errorf("line %d visible width = %d, want 19 (%q)", i, w, line)
		}
	}
	if w := tui.VisibleWidth(lines[len(lines)-1]); w <= 19 {
		t.Errorf("bottom line missing version tag (width %d): %q", w, lines[len(lines)-1])
	}
}

func TestBuildPigLogoColoring(t *testing.T) {
	// Art lines are bold+dim individually (SGR must not leak across
	// lines in the diff renderer); the version tag is dim without bold.
	logo := buildPigLogo(newLogoTestTheme(t), "0.85.1")
	lines := strings.Split(logo, "\n")
	for i, line := range lines {
		if !strings.HasPrefix(line, "\x1b[1m") {
			t.Errorf("line %d not bold-wrapped: %q", i, line)
		}
		// The last line trails the dim version tag, so it ends with the
		// dim fg reset instead of the bold reset.
		if i < len(lines)-1 && !strings.HasSuffix(line, "\x1b[22m") {
			t.Errorf("line %d not bold-terminated: %q", i, line)
		}
	}
	// The trailing version chunk after the last bold reset is dim-wrapped.
	last := lines[len(lines)-1]
	idx := strings.LastIndex(last, "\x1b[22m")
	if idx < 0 {
		t.Fatalf("no bold reset in last line: %q", last)
	}
	versionPart := last[idx+len("\x1b[22m"):]
	if !strings.HasPrefix(versionPart, "\x1b[38;5;") || !strings.HasSuffix(versionPart, " v0.85.1\x1b[39m") {
		t.Errorf("version tag not dim-wrapped: %q", versionPart)
	}
}
