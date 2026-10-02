package interactive

// Regression tests for the pig-only ASCII art startup logo
// (buildPigLogo in interactive_mode.go). pi prints a 2-line half-block mark
// (components/pi-logo.ts) next to the version and key hints; pig shows a
// 6-line figlet "pig" block colored with a per-start random two-hue gradient
// and keeps the version trailing the bottom art line (user-requested
// deviation).

import (
	"math"
	"math/rand"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/ZoroSpace/pig/codingagent/core"
	"github.com/ZoroSpace/pig/codingagent/modes/interactive/theme"
	"github.com/ZoroSpace/pig/tui"
)

var sgrSequence = regexp.MustCompile("\x1b\\[[0-9;]*m")

// expectedPigLogoArt is the bare-text art (version excluded from the last
// line's expectation here; see TestBuildPigLogoArt).
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
	return newLogoTestThemeWithMode(t, theme.ColorMode256)
}

func newLogoTestThemeWithMode(t *testing.T, mode theme.ColorMode) *theme.Theme {
	t.Helper()
	tl, err := theme.NewTheme(
		map[string]theme.ColorValue{
			"accent": "#ff0000", "dim": "#888888", "muted": "#666666",
			"text": "#d4d4d4",
		},
		nil,
		mode,
		nil,
	)
	if err != nil {
		t.Fatalf("NewTheme: %v", err)
	}
	return tl
}

// parseTruecolorFg decodes an "\x1b[38;2;R;G;Bm" sequence.
func parseTruecolorFg(t *testing.T, sequence string) tui.RgbColor {
	t.Helper()
	if !strings.HasPrefix(sequence, "\x1b[38;2;") || !strings.HasSuffix(sequence, "m") {
		t.Fatalf("not a truecolor foreground sequence: %q", sequence)
	}
	fields := strings.Split(strings.TrimSuffix(strings.TrimPrefix(sequence, "\x1b[38;2;"), "m"), ";")
	if len(fields) != 3 {
		t.Fatalf("expected 3 channels in %q", sequence)
	}
	channel := func(value string) int {
		parsed, err := strconv.Atoi(value)
		if err != nil {
			t.Fatalf("bad channel %q in %q", value, sequence)
		}
		return parsed
	}
	return tui.RgbColor{R: channel(fields[0]), G: channel(fields[1]), B: channel(fields[2])}
}

// pinLogoGradient installs a deterministic ramp for the test's duration. The
// shipped gradient is sampled per process start, so assertions that compare
// against concrete escape sequences or the test theme's palette entries must
// not depend on the random draw.
func pinLogoGradient(t *testing.T) {
	t.Helper()
	original := pigLogoBrandGradient
	pigLogoBrandGradient = append([]tui.Color(nil), pigLogoFallbackGradient...)
	t.Cleanup(func() { pigLogoBrandGradient = original })
}

func TestBuildPigLogoArt(t *testing.T) {
	pinLogoGradient(t)
	logo := buildPigLogo(newLogoTestTheme(t), "0.85.1", core.LogoStyleGradient)
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
	// Every art line is uniformly 19 columns (the last one 18) so the block
	// stays aligned regardless of the gradient's escape sequence lengths.
	pinLogoGradient(t)
	logo := buildPigLogo(newLogoTestTheme(t), "0.85.1", core.LogoStyleGradient)
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

func TestPigLogoBrandGradient(t *testing.T) {
	// The ramp runs from the first anchor to the last one across the art
	// columns: both endpoints are the pure anchor colors and adjacent columns
	// stay close, i.e. it is a gradient and not a couple of solid bands.
	pinLogoGradient(t)
	sequences, ok := pigLogoForegroundAnsi(tui.ColorModeTruecolorN, 19)
	if !ok {
		t.Fatal("pigLogoForegroundAnsi failed for the literal brand colors")
	}
	if len(sequences) != 19 {
		t.Fatalf("got %d sequences, want 19", len(sequences))
	}
	for _, anchor := range []struct {
		column int
		color  tui.Color
	}{
		{0, pigLogoBrandGradient[0]},
		{18, pigLogoBrandGradient[len(pigLogoBrandGradient)-1]},
	} {
		got := parseTruecolorFg(t, sequences[anchor.column])
		want := tui.ColorToRgb(anchor.color)
		if absInt(got.R-want.R) > 2 || absInt(got.G-want.G) > 2 || absInt(got.B-want.B) > 2 {
			t.Errorf("column %d = %+v, want %+v", anchor.column, got, want)
		}
	}
	unique := map[string]bool{}
	for column, sequence := range sequences {
		if unique[sequence] {
			t.Errorf("column %d repeats the color of an earlier column: %q", column, sequence)
		}
		unique[sequence] = true
		if column == 0 {
			continue
		}
		previous := parseTruecolorFg(t, sequences[column-1])
		current := parseTruecolorFg(t, sequences[column])
		delta := max(absInt(current.R-previous.R), max(absInt(current.G-previous.G), absInt(current.B-previous.B)))
		if delta > 40 {
			t.Errorf("column %d jumps %d channels (%+v -> %+v), want a smooth ramp", column, delta, previous, current)
		}
	}
}

func TestPigLogoRampAnchors(t *testing.T) {
	// A middle anchor (odd anchor count on an odd column count) lands on its
	// own pure color: with n anchors the columns split into n-1 even segments.
	pinLogoGradient(t)
	pigLogoBrandGradient = []tui.Color{
		{Kind: tui.ColorKindRgb, R: 228, G: 138, B: 122},
		{Kind: tui.ColorKindRgb, R: 79, G: 142, B: 179},
		{Kind: tui.ColorKindRgb, R: 234, G: 182, B: 93},
	}
	sequences, ok := pigLogoForegroundAnsi(tui.ColorModeTruecolorN, 19)
	if !ok {
		t.Fatal("pigLogoForegroundAnsi failed for the literal brand colors")
	}
	for _, anchor := range []struct {
		column int
		color  tui.Color
	}{
		{0, pigLogoBrandGradient[0]},
		{9, pigLogoBrandGradient[1]},
		{18, pigLogoBrandGradient[2]},
	} {
		got := parseTruecolorFg(t, sequences[anchor.column])
		want := tui.ColorToRgb(anchor.color)
		if absInt(got.R-want.R) > 2 || absInt(got.G-want.G) > 2 || absInt(got.B-want.B) > 2 {
			t.Errorf("column %d = %+v, want %+v", anchor.column, got, want)
		}
	}
}

func TestPigLogoForegroundAnsiEdgeCases(t *testing.T) {
	pinLogoGradient(t)
	if _, ok := pigLogoForegroundAnsi(tui.ColorMode256color, 0); ok {
		t.Error("zero columns must report failure")
	}
	if _, ok := pigLogoForegroundAnsi(tui.ColorMode256color, -1); ok {
		t.Error("negative columns must report failure")
	}
	sequences, ok := pigLogoForegroundAnsi(tui.ColorMode256color, 1)
	if !ok || len(sequences) != 1 {
		t.Fatalf("single column = %v, %v", sequences, ok)
	}
	if sequences[0] != tui.ForegroundAnsi(pigLogoBrandGradient[0], tui.ColorMode256color) {
		t.Errorf("single column = %q, want the ramp's first anchor", sequences[0])
	}
}

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

// The default style (plain) draws the figlet art in the theme's dim color —
// the same color as the "Press ctrl+o to show…" startup hint. Every art line
// is bold and self-contained, no brand-gradient sequences appear, and the
// version tag trails the bottom line in the same dim color. This is the
// pig-only logoStyle=plain rendering (settings default).
func TestBuildPigLogoPlainIsThemeDimColored(t *testing.T) {
	pinLogoGradient(t)
	tl := newLogoTestTheme(t)
	logo := buildPigLogo(tl, "0.85.1", core.LogoStylePlain)
	lines := strings.Split(logo, "\n")
	if len(lines) != len(expectedPigLogoArt) {
		t.Fatalf("plain logo has %d lines, want %d:\n%s", len(lines), len(expectedPigLogoArt), logo)
	}
	dimSeq := strings.TrimSuffix(tl.Fg("dim", ""), "\x1b[39m")
	fgSequence := regexp.MustCompile("\x1b\\[38;[0-9;]*m")
	for i, line := range lines {
		bare := sgrSequence.ReplaceAllString(line, "")
		want := expectedPigLogoArt[i]
		if i == len(lines)-1 {
			want += " v0.85.1"
		}
		if bare != want {
			t.Errorf("line %d = %q, want %q", i, bare, want)
		}
		if !strings.HasPrefix(line, "\x1b[1m") {
			t.Errorf("line %d not bold-wrapped: %q", i, line)
		}
		// Every foreground sequence is the theme's dim color (matching the
		// startup hint line), never a gradient anchor.
		sequences := fgSequence.FindAllString(line, -1)
		if len(sequences) == 0 {
			t.Errorf("line %d carries no foreground color: %q", i, line)
		}
		for _, sequence := range sequences {
			if sequence != dimSeq {
				t.Errorf("line %d uses non-dim color %q, want %q (%q)", i, sequence, dimSeq, line)
			}
		}
	}
}

// The gradient style stays selectable (pig-only logoStyle=gradient): it uses
// the brand ramp and therefore differs from the plain rendering.
func TestBuildPigLogoStyleSelectsRendering(t *testing.T) {
	pinLogoGradient(t)
	tl := newLogoTestThemeWithMode(t, theme.ColorModeTruecolor)
	plain := buildPigLogo(tl, "0.85.1", core.LogoStylePlain)
	gradient := buildPigLogo(tl, "0.85.1", core.LogoStyleGradient)
	if plain == gradient {
		t.Fatal("plain and gradient logo styles render identically")
	}
	if !strings.Contains(gradient, tui.ForegroundAnsi(pigLogoBrandGradient[0], tui.ColorModeTruecolorN)) {
		t.Errorf("gradient style missing the ramp's first brand color: %q", gradient)
	}
	if strings.Contains(plain, tui.ForegroundAnsi(pigLogoBrandGradient[0], tui.ColorModeTruecolorN)) {
		t.Errorf("plain style must not carry brand colors: %q", plain)
	}
	// An unknown value degrades to the plain rendering instead of erroring.
	if unknown := buildPigLogo(tl, "0.85.1", "nonsense"); unknown != plain {
		t.Errorf("unknown logo style must fall back to plain:\n%s\n%s", unknown, plain)
	}
}

func TestBuildPigLogoColoring(t *testing.T) {
	// Art lines are bold and self-contained (SGR state must not leak across
	// lines in the diff renderer); the gradient replaces the theme colors of
	// the art, while the version tag keeps following the theme.
	pinLogoGradient(t)
	tl := newLogoTestTheme(t)
	logo := buildPigLogo(tl, "0.85.1", core.LogoStyleGradient)
	lines := strings.Split(logo, "\n")
	textSeq := strings.TrimSuffix(tl.Fg("text", ""), "\x1b[39m")
	for i, line := range lines {
		if !strings.HasPrefix(line, "\x1b[1m") {
			t.Errorf("line %d not bold-wrapped: %q", i, line)
		}
		if !strings.HasSuffix(line, "\x1b[22m") && i < len(lines)-1 {
			t.Errorf("line %d not bold-terminated: %q", i, line)
		}
		if i == len(lines)-1 {
			continue
		}
		// Brand colors are quantized to the 256-color palette in this mode.
		if !strings.Contains(line, "\x1b[38;5;") {
			t.Errorf("line %d carries no brand color: %q", i, line)
		}
		for _, name := range []string{"text", "accent", "dim"} {
			seq := strings.TrimSuffix(tl.Fg(theme.ThemeColor(name), ""), "\x1b[39m")
			if strings.Contains(line, seq) {
				t.Errorf("line %d contains %s-colored segment, want brand colors only:\n%s", i, name, line)
			}
		}
	}
	// The bottom line: bold art segment, then the version tag in plain text
	// color (no bold).
	last := lines[len(lines)-1]
	idx := strings.LastIndex(last, "\x1b[22m")
	if idx < 0 {
		t.Fatalf("no bold reset in bottom line: %q", last)
	}
	if last[:idx] == "\x1b[1m" {
		t.Errorf("bottom line has no colored art segment: %q", last)
	}
	versionPart := last[idx+len("\x1b[22m"):]
	if !strings.HasPrefix(versionPart, textSeq) || !strings.HasSuffix(versionPart, " v0.85.1\x1b[39m") {
		t.Errorf("version tag not plain-text-wrapped: %q", versionPart)
	}
}

func TestBuildPigLogoFollowsColorMode(t *testing.T) {
	// The gradient is theme-independent but follows the terminal's color mode
	// (pi: pi-logo.ts quantizes its fixed rgb colors the same way).
	pinLogoGradient(t)
	quantized := buildPigLogo(newLogoTestThemeWithMode(t, theme.ColorMode256), "0.85.1", core.LogoStyleGradient)
	if !strings.Contains(quantized, "\x1b[38;5;") || strings.Contains(quantized, "\x1b[38;2;") {
		t.Errorf("256-color logo must use palette sequences only: %q", quantized)
	}

	truecolor := buildPigLogo(newLogoTestThemeWithMode(t, theme.ColorModeTruecolor), "0.85.1", core.LogoStyleGradient)
	if !strings.Contains(truecolor, tui.ForegroundAnsi(pigLogoBrandGradient[0], tui.ColorModeTruecolorN)) {
		t.Errorf("truecolor logo missing the ramp's first brand color: %q", truecolor)
	}
	if strings.Contains(truecolor, "\x1b[38;5;") {
		t.Errorf("truecolor logo must not use palette sequences: %q", truecolor)
	}
}

func TestSamplePigLogoGradientDrawsWithinRanges(t *testing.T) {
	// Scripted unit draws pin the sampling contract: the five draws are, in
	// order, hue, arc, direction, lightness and saturation. draw = 0.5 for the
	// arc selects the middle of the shipped arc range.
	draws := []float64{0.5, 0.5, 0.0, 1.0, 0.0} // negative direction, max lightness, min saturation
	index := 0
	random := func() float64 {
		value := draws[index]
		index++
		return value
	}
	anchors := samplePigLogoGradient(random)
	if index != len(draws) {
		t.Fatalf("sampling consumed %d draws, want %d", index, len(draws))
	}
	if len(anchors) != 2 {
		t.Fatalf("got %d anchors, want 2", len(anchors))
	}
	// hue = 180, arc = 90 reversed, so the second anchor sits at 90 degrees.
	wantHues := []float64{180, 90}
	for i, anchor := range anchors {
		channels := tui.ColorToOkhsl(anchor)
		if delta := math.Abs(channels.H - wantHues[i]); delta > 2 {
			t.Errorf("anchor %d hue = %.2f, want %.2f", i, channels.H, wantHues[i])
		}
		if delta := math.Abs(channels.L - pigLogoGradientLightnessMax); delta > 0.01 {
			t.Errorf("anchor %d lightness = %.3f, want %.2f", i, channels.L, pigLogoGradientLightnessMax)
		}
		if delta := math.Abs(channels.S - pigLogoGradientSaturationMin); delta > 0.02 {
			t.Errorf("anchor %d saturation = %.3f, want %.2f", i, channels.S, pigLogoGradientSaturationMin)
		}
	}
	if tui.ColorToHex(anchors[0]) == tui.ColorToHex(anchors[1]) {
		t.Errorf("both anchors are %s despite the hue arc", tui.ColorToHex(anchors[0]))
	}
}

func TestSamplePigLogoGradientProducesDistinctRamp(t *testing.T) {
	// The shipped ranges must keep the 19 art columns distinguishable for any
	// draw: a narrower arc (or a pair whose quantized colors collapse) would
	// render as one flat band. Endpoints also stay inside a legible lightness
	// band on dark and light terminals.
	rng := rand.New(rand.NewSource(20261001))
	for trial := 0; trial < 200; trial++ {
		original := pigLogoBrandGradient
		pigLogoBrandGradient = samplePigLogoGradient(rng.Float64)
		sequences, ok := pigLogoForegroundAnsi(tui.ColorModeTruecolorN, 19)
		pigLogoBrandGradient = original
		if !ok {
			t.Fatalf("trial %d: pigLogoForegroundAnsi failed", trial)
		}
		seen := map[string]bool{}
		for column, sequence := range sequences {
			if seen[sequence] {
				t.Errorf("trial %d: column %d repeats an earlier color (%q)", trial, column, sequence)
			}
			seen[sequence] = true
		}
		for _, column := range []int{0, 18} {
			rgb := parseTruecolorFg(t, sequences[column])
			lightness := tui.ColorToOklch(tui.Color{Kind: tui.ColorKindRgb, R: rgb.R, G: rgb.G, B: rgb.B}).L
			if lightness < 0.55 || lightness > 0.80 {
				t.Errorf("trial %d: column %d lightness %.3f outside the legible band (rgb %+v)", trial, column, lightness, rgb)
			}
		}
	}
}

func TestRandomPigLogoGradientVariesPerCall(t *testing.T) {
	// Guard for the user request: the two anchors differ on every start
	// instead of being the fixed blue -> purple brand pair.
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		gradient := randomPigLogoGradient()
		if len(gradient) != 2 {
			t.Fatalf("got %d anchors, want 2", len(gradient))
		}
		seen[tui.ColorToHex(gradient[0])+"->"+tui.ColorToHex(gradient[1])] = true
	}
	if len(seen) < 45 {
		t.Errorf("50 starts produced only %d distinct gradients", len(seen))
	}
}

func TestPigLogoGradientSampledOncePerProcess(t *testing.T) {
	// The ramp is sampled when the process starts, not per render: rebuilding
	// the header (theme change, invalidate) must reuse the colors of this run,
	// while the next process start draws new ones (simulated by swapping the
	// ramp the renderer reads).
	pinLogoGradient(t)
	first := buildPigLogo(newLogoTestTheme(t), "0.85.1", core.LogoStyleGradient)
	if second := buildPigLogo(newLogoTestTheme(t), "0.85.1", core.LogoStyleGradient); second != first {
		t.Errorf("logo changed between renders of the same process:\n%s\n%s", first, second)
	}
	pigLogoBrandGradient = []tui.Color{
		{Kind: tui.ColorKindRgb, R: 0, G: 200, B: 0},
		{Kind: tui.ColorKindRgb, R: 200, G: 0, B: 0},
	}
	if third := buildPigLogo(newLogoTestTheme(t), "0.85.1", core.LogoStyleGradient); third == first {
		t.Error("logo ignored the resampled ramp")
	}
}

// pigLogoBlockCell is one decoded half-block cell: the fg color and glyph,
// plus the bg color when two pieces meet in the cell and their colors are
// packed into ▀ (upper pixel as fg, lower as bg; pi does the same).
type pigLogoBlockCell struct {
	r, g, b       int
	bgR, bgG, bgB int
	hasBg         bool
	glyph         string
}

// expectedPigLogoBlocksCells lists every non-empty cell of the half-block
// wordmark, line by line: p is a coral top L over a blue side T, i is the
// yellow bar (pi's 4x4 mark, components/pi-logo.ts), and g repeats the same
// shapes with a yellow base bar (colors editable in pigLogoBlocksPieces).
var expectedPigLogoBlocksCells = [][]pigLogoBlockCell{
	{
		{r: 228, g: 138, b: 122, bgR: 79, bgG: 142, bgB: 179, hasBg: true, glyph: "▀"},
		{r: 228, g: 138, b: 122, glyph: "▀"},
		{r: 228, g: 138, b: 122, glyph: "█"},
		{r: 228, g: 138, b: 122, glyph: "█"},
		{r: 228, g: 138, b: 122, glyph: "▀"},
		{r: 228, g: 138, b: 122, bgR: 79, bgG: 142, bgB: 179, hasBg: true, glyph: "▀"},
	},
	{
		{r: 79, g: 142, b: 179, glyph: "█"},
		{r: 79, g: 142, b: 179, glyph: "▀"},
		{r: 234, g: 182, b: 93, glyph: "█"},
		{r: 79, g: 142, b: 179, glyph: "▀"},
		{r: 79, g: 142, b: 179, glyph: "█"},
	},
	{
		{r: 234, g: 182, b: 93, glyph: "▀"},
		{r: 234, g: 182, b: 93, glyph: "▀"},
	},
}

// TestBuildPigLogoBlocksMark locks the bare-text shape of the blocks
// wordmark: three lines (7x5 pixel grid), version on the top line (pi's
// header layout, unlike the figlet styles' bottom-line version), and every
// cell drawn in its piece's brand color in truecolor mode.
func TestBuildPigLogoBlocksMark(t *testing.T) {
	logo := buildPigLogo(newLogoTestThemeWithMode(t, theme.ColorModeTruecolor), "0.85.1", core.LogoStyleBlocks)
	lines := strings.Split(logo, "\n")
	if len(lines) != 3 {
		t.Fatalf("blocks logo has %d lines, want 3:\n%q", len(lines), logo)
	}
	wantBare := []string{"▀▀█ █▀▀ v0.85.1", "█▀ █ ▀█", "     ▀▀"}
	for i, line := range lines {
		if bare := sgrSequence.ReplaceAllString(line, ""); bare != wantBare[i] {
			t.Errorf("line %d = %q, want %q", i, bare, wantBare[i])
		}
	}
	cell := regexp.MustCompile(`\x1b\[38;2;(\d+);(\d+);(\d+)m(?:\x1b\[48;2;(\d+);(\d+);(\d+)m)?([▀▄█])`)
	for i, line := range lines {
		matches := cell.FindAllStringSubmatch(line, -1)
		if len(matches) != len(expectedPigLogoBlocksCells[i]) {
			t.Fatalf("line %d has %d styled cells, want %d: %q", i, len(matches), len(expectedPigLogoBlocksCells[i]), line)
		}
		for j, match := range matches {
			want := expectedPigLogoBlocksCells[i][j]
			channel := func(value string) int {
				parsed, err := strconv.Atoi(value)
				if err != nil {
					t.Fatalf("bad channel %q", value)
				}
				return parsed
			}
			if got := (tui.RgbColor{R: channel(match[1]), G: channel(match[2]), B: channel(match[3])}); got != (tui.RgbColor{R: want.r, G: want.g, B: want.b}) {
				t.Errorf("line %d cell %d = %+v, want (%d,%d,%d)", i, j, got, want.r, want.g, want.b)
			}
			if match[4] != "" != want.hasBg {
				t.Errorf("line %d cell %d hasBg = %v, want %v", i, j, match[4] != "", want.hasBg)
			}
			if want.hasBg {
				if got := (tui.RgbColor{R: channel(match[4]), G: channel(match[5]), B: channel(match[6])}); got != (tui.RgbColor{R: want.bgR, G: want.bgG, B: want.bgB}) {
					t.Errorf("line %d cell %d bg = %+v, want (%d,%d,%d)", i, j, got, want.bgR, want.bgG, want.bgB)
				}
			}
			if match[7] != want.glyph {
				t.Errorf("line %d cell %d glyph = %q, want %q", i, j, match[7], want.glyph)
			}
		}
	}
}

// TestBuildPigLogoBlocksVersionColor checks the version tag on the top line
// renders in the theme's dim color (pi renders its version dim too).
func TestBuildPigLogoBlocksVersionColor(t *testing.T) {
	tl := newLogoTestThemeWithMode(t, theme.ColorModeTruecolor)
	logo := buildPigLogo(tl, "0.85.1", core.LogoStyleBlocks)
	top := strings.Split(logo, "\n")[0]
	if !strings.Contains(top, tl.Fg("dim", " v0.85.1")) {
		t.Errorf("version tag not dim-colored on the top line: %q", top)
	}
}

// TestBuildPigLogoBlocksAppleTerminalFallback covers the pi supportsPiLogo
// fallback: Apple Terminal draws half blocks with gaps between rows, so it
// gets the plain "pig" text wordmark in the letter colors instead.
func TestBuildPigLogoBlocksAppleTerminalFallback(t *testing.T) {
	original := pigLogoIsAppleTerminal
	pigLogoIsAppleTerminal = func() bool { return true }
	t.Cleanup(func() { pigLogoIsAppleTerminal = original })

	logo := buildPigLogo(newLogoTestThemeWithMode(t, theme.ColorModeTruecolor), "0.85.1", core.LogoStyleBlocks)
	if strings.ContainsAny(logo, "▀▄█") {
		t.Fatalf("Apple Terminal fallback must not draw half blocks: %q", logo)
	}
	if bare := sgrSequence.ReplaceAllString(logo, ""); bare != "pig v0.85.1" {
		t.Errorf("fallback wordmark = %q, want %q", bare, "pig v0.85.1")
	}
	for _, color := range []string{"\x1b[38;2;228;138;122m", "\x1b[38;2;79;142;179m", "\x1b[38;2;234;182;93m"} {
		if !strings.Contains(logo, color) {
			t.Errorf("fallback wordmark missing letter color %s: %q", color, logo)
		}
	}
}
