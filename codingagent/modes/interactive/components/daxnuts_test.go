package components

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/ZoroSpace/pig/codingagent/modes/interactive/theme"
)

// daxnutsStripANSI removes SGR/truecolor sequences for width checks (the
// component's own centering uses tui.VisibleWidth; the test asserts the same
// visible length by construction).
func daxnutsStripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b {
			for j := i; j < len(s); j++ {
				if s[j] == 'm' {
					i = j
					break
				}
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// daxnutsVisibleLen strips ANSI and counts runes (all characters here are
// BMP, matching pi's JS .length on the stripped string).
func daxnutsVisibleLen(s string) int {
	return utf8.RuneCountInString(daxnutsStripANSI(s))
}

func TestDaxnutsParseImage(t *testing.T) {
	if len(daxHex) != daxnutsWidth*daxnutsHeight*6 {
		t.Fatalf("daxHex length = %d, want %d", len(daxHex), daxnutsWidth*daxnutsHeight*6)
	}
	pixels := daxnutsParseImage()
	if len(pixels) != daxnutsHeight || len(pixels[0]) != daxnutsWidth {
		t.Fatalf("parsed grid = %dx%d, want %dx%d", len(pixels), len(pixels[0]), daxnutsHeight, daxnutsWidth)
	}
	// First pixel "bbbab8" and last pixel "b0aba8" come straight from pi's
	// DAX_HEX (byte-verified against daxnuts.ts at port time).
	if pixels[0][0] != [3]int{0xbb, 0xba, 0xb8} {
		t.Fatalf("pixels[0][0] = %v, want [187 186 184]", pixels[0][0])
	}
	if pixels[daxnutsHeight-1][daxnutsWidth-1] != [3]int{0xb0, 0xab, 0xa8} {
		t.Fatalf("pixels[31][31] = %v, want [176 171 168]", pixels[daxnutsHeight-1][daxnutsWidth-1])
	}
}

func TestDaxnutsBuildImage(t *testing.T) {
	lines := daxnutsBuildImage()
	if len(lines) != daxnutsHeight/2 {
		t.Fatalf("buildImage lines = %d, want %d", len(lines), daxnutsHeight/2)
	}
	for i, line := range lines {
		if !strings.HasSuffix(line, daxnutsReset) {
			t.Fatalf("line %d missing reset suffix", i)
		}
		visible := daxnutsVisibleLen(line)
		if visible != daxnutsWidth {
			t.Fatalf("line %d visible width = %d, want %d", i, visible, daxnutsWidth)
		}
		if strings.Count(line, "\u2584") != daxnutsWidth {
			t.Fatalf("line %d half-block count = %d, want %d", i, strings.Count(line, "\u2584"), daxnutsWidth)
		}
	}
}

func TestDaxnutsRenderFullReveal(t *testing.T) {
	initTestTheme(t)
	d := NewDaxnutsComponent(nil)
	d.Dispose()
	// Force the completed state deterministically (the ticker goroutine is
	// stopped by Dispose; all shared fields are guarded by d.mu).
	d.mu.Lock()
	d.tick = d.maxTicks
	d.cachedWidth = 0
	d.mu.Unlock()

	const width = 80
	lines := d.Render(width)

	// Layout: blank, 16 image rows, blank, 3 text lines, blank, 2 link
	// lines, blank (pi: render at tick >= maxTicks).
	if len(lines) != 25 {
		t.Fatalf("render lines = %d, want 25", len(lines))
	}
	revealText := strings.Join(lines, "\n")
	for _, want := range []string{
		"Free Kimi K2.5 via OpenCode Zen",
		"Powered by daxnuts",
		"@thdxr",
		"Try OpenCode",
		"https://mistral.ai/news/mistral-vibe-2-0",
	} {
		if !strings.Contains(revealText, want) {
			t.Fatalf("render missing %q", want)
		}
	}
	if strings.Contains(revealText, "\u2593") {
		t.Fatalf("scanline still visible after full reveal")
	}
	// Image rows are centered: left pad (80-32)/2 = 24 spaces, then 32 cells
	// of content (padded visible width 56).
	for i := 1; i <= daxnutsHeight/2; i++ {
		stripped := daxnutsStripANSI(lines[i])
		if got := utf8.RuneCountInString(stripped) - utf8.RuneCountInString(strings.TrimLeft(stripped, " ")); got != (width-daxnutsWidth)/2 {
			t.Fatalf("image row %d left pad = %d, want %d", i, got, (width-daxnutsWidth)/2)
		}
		if got := daxnutsVisibleLen(lines[i]); got != (width-daxnutsWidth)/2+daxnutsWidth {
			t.Fatalf("image row %d padded width = %d, want %d", i, got, (width-daxnutsWidth)/2+daxnutsWidth)
		}
	}
	// Text lines use theme colors, not raw truecolor (pi: t.fg(...)).
	if !strings.Contains(lines[18], theme.Fg("accent", "Free Kimi K2.5 via OpenCode Zen")) {
		t.Fatalf("accent text line missing theme color, got %q", lines[18])
	}
}
