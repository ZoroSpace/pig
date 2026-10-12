// diff_test.go — pig-side coverage for renderDiff (pi has no direct tests
// for diff.ts; per repo precedent, coverage is self-built against the
// behavior the edit tool renders).
package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ZoroSpace/pig/codingagent/modes/interactive/theme"
)

func TestRenderDiffContextAndAddRemoveColors(t *testing.T) {
	theme.InitTheme("dark", false)

	// Non-adjacent removed/added lines (a context line between them) render
	// as-is; adjacent single pairs take the intra-line branch instead.
	diffText := strings.Join([]string{
		" 1 unchanged",
		"-2 removed",
		" 2 kept",
		"+3 added",
		" 4 tail",
	}, "\n")
	got := RenderDiff(theme.Current(), diffText, RenderDiffOptions{})

	if !strings.Contains(got, theme.Current().Fg("toolDiffContext", " 1 unchanged")) {
		t.Errorf("context line not styled: %q", got)
	}
	if !strings.Contains(got, theme.Current().Fg("toolDiffRemoved", "-2 removed")) {
		t.Errorf("removed line not styled: %q", got)
	}
	if !strings.Contains(got, theme.Current().Fg("toolDiffAdded", "+3 added")) {
		t.Errorf("added line not styled: %q", got)
	}
}

func TestRenderDiffIntraLinePlainFg(t *testing.T) {
	theme.InitTheme("dark", false)

	// pig-only deviation (2026-10-12, docs/feat/BUILTIN-THEME-PALETTES.md
	// §BLOCK-BORDERS 九批): pi highlights intra-line changed tokens with
	// inverse video, which renders as a colored BACKGROUND on the terminal;
	// pig renders the whole line in its single fg hue with no intra-line
	// emphasis (bold was tried and declined the same day). This test
	// replaces the ported inverse assertions (declared adaptation, same
	// precedent as the user_message 3-line form).
	diffText := strings.Join([]string{
		"-1 return old",
		"+1 return new",
	}, "\n")
	got := RenderDiff(theme.Current(), diffText, RenderDiffOptions{})

	// No reverse video, no bold — the fg hue alone marks the change.
	if strings.Contains(got, "\x1b[7m") {
		t.Errorf("inverse video leaked into diff render: %q", got)
	}
	if strings.Contains(got, theme.Current().Bold("old")) || strings.Contains(got, theme.Current().Bold("new")) {
		t.Errorf("changed tokens bold-highlighted: %q", got)
	}
	// Content preserved on both lines.
	if strings.Count(got, "return") != 2 || !strings.Contains(got, "old") || !strings.Contains(got, "new") {
		t.Errorf("expected unchanged word on both lines and changed words present: %q", got)
	}
}

func TestRenderDiffMultiLineBlockFallsBack(t *testing.T) {
	theme.InitTheme("dark", false)

	// Two removed + one added: no intra-line diffing, lines shown as-is.
	diffText := strings.Join([]string{
		"-1 alpha",
		"-2 beta",
		"+1 gamma",
	}, "\n")
	got := RenderDiff(theme.Current(), diffText, RenderDiffOptions{})

	for _, want := range []string{
		theme.Current().Fg("toolDiffRemoved", "-1 alpha"),
		theme.Current().Fg("toolDiffRemoved", "-2 beta"),
		theme.Current().Fg("toolDiffAdded", "+1 gamma"),
	} {
		if !strings.Contains(got, want) {
			t.Errorf("block line missing: %q in %q", want, got)
		}
	}
}

func TestRenderDiffNonDiffLinesPassthrough(t *testing.T) {
	theme.InitTheme("dark", false)

	got := RenderDiff(theme.Current(), "not a diff line", RenderDiffOptions{})
	if !strings.Contains(got, theme.Current().Fg("toolDiffContext", "not a diff line")) {
		t.Errorf("unparsed line not styled as context: %q", got)
	}
}

// TestMain points PI_PACKAGE_DIR at the theme package's testdata so the
// built-in dark theme loads for renderer assertions (components TestMain
// precedent).
func TestMain(m *testing.M) {
	dir, err := filepath.Abs(filepath.Join("..", "..", "modes", "interactive", "theme", "testdata"))
	if err != nil {
		panic(err)
	}
	if os.Setenv("PI_PACKAGE_DIR", dir) != nil {
		panic(err)
	}
	os.Exit(m.Run())
}
