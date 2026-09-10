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

func TestRenderDiffIntraLineInverse(t *testing.T) {
	theme.InitTheme("dark", false)

	// Single removed + single added line → word-level intra-line diff with
	// inverse on the changed token, indentation left un-highlighted.
	diffText := strings.Join([]string{
		"-1 return old",
		"+1 return new",
	}, "\n")
	got := RenderDiff(theme.Current(), diffText, RenderDiffOptions{})

	if !strings.Contains(got, theme.Current().Inverse("old")) {
		t.Errorf("removed change not inverse-highlighted: %q", got)
	}
	if !strings.Contains(got, theme.Current().Inverse("new")) {
		t.Errorf("added change not inverse-highlighted: %q", got)
	}
	// Unchanged word "return" must appear once per line without inverse.
	if strings.Count(got, "return") != 2 {
		t.Errorf("expected unchanged word on both lines: %q", got)
	}
	// The leading indent ("-1 " / "+1 ") must not be inside the inverse.
	if strings.Contains(got, theme.Current().Inverse("-1 ")) || strings.Contains(got, theme.Current().Inverse("+1 ")) {
		t.Errorf("prefix inverse-highlighted: %q", got)
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
