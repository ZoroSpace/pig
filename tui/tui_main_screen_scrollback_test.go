package tui

import (
	"fmt"
	"testing"
)

// assertBufferMatchesScrollback verifies the virtual terminal's scroll buffer
// contains exactly the component lines (each once, in order) and nothing
// else. Any duplicated region pushed into scrollback shows up as extra
// non-empty rows beyond the expected content or as misaligned rows.
//
// Regression harness for the scrollback-duplication bug
// (docs/fix/2026-10-13-scrollback-duplication-window-repaint.md): the
// shrink-spanning window repaint used to slide the viewport up by k rows and
// rewrite physical rows whose new content already existed k rows above in
// scrollback, so the same transcript region appeared twice in scroll history.
func assertBufferMatchesScrollback(t *testing.T, terminal *VirtualTerminal, want []string, phase string) {
	t.Helper()
	buf := terminal.GetScrollBuffer()
	if len(buf) < len(want) {
		t.Fatalf("%s: scroll buffer %d rows < expected %d", phase, len(buf), len(want))
	}
	for i, w := range want {
		if got := buf[i]; got != w {
			t.Fatalf("%s: scroll buffer row %d = %q, want %q", phase, i, got, w)
		}
	}
	for i := len(want); i < len(buf); i++ {
		if buf[i] != "" {
			t.Fatalf("%s: scroll buffer has %d unexpected extra rows; first extra row %d = %q", phase, len(buf)-len(want), i, buf[i])
		}
	}
}

// TestMainScreenTurnShapeNoScrollbackDuplication replays the user-reported
// turn shape end to end: transcript, tool card, live tool panel growing above
// the viewport, collapse, streamed answer, second panel, second collapse,
// more answer. After every frame the scrollback must contain each buffer line
// exactly once — growth frames via the incremental clamp path, large
// collapses via the scrollback-exact full render.
func TestMainScreenTurnShapeNoScrollbackDuplication(t *testing.T) {
	terminal, tui, component := newViewportTestTui(t)

	lines := buildNumberedLines(10)
	component.SetLines(lines)
	tui.RenderNow(false)

	phase := 0
	frame := func(tag string) {
		tui.RenderNow(false)
		phase++
		assertBufferMatchesScrollback(t, terminal, lines, fmt.Sprintf("frame%02d/%s", phase, tag))
	}

	// Tool card appears, then its live panel streams 12 rows (taller than the
	// 8-row viewport so the panel top sits above the viewport top).
	lines = append(lines, "card-grep")
	component.SetLines(lines)
	frame("card")

	for i := 0; i < 12; i++ {
		lines = append(lines, fmt.Sprintf("panel1-%02d", i))
		component.SetLines(lines)
		frame("grow1")
	}

	// Tool completes: live panel collapses into a compact result card
	// (slide 10 > maxShrinkWindowSlide → full render, scrollback exact).
	lines = append(lines[:len(lines)-12], "result-grep")
	component.SetLines(lines)
	frame("collapse1")

	// Answer text streams in.
	for i := 0; i < 6; i++ {
		lines = append(lines, fmt.Sprintf("answer-%02d", i))
		component.SetLines(lines)
		frame("answer")
	}

	// Second tool card with its own live panel, then collapse.
	lines = append(lines, "card-sed")
	component.SetLines(lines)
	frame("card2")
	for i := 0; i < 10; i++ {
		lines = append(lines, fmt.Sprintf("panel2-%02d", i))
		component.SetLines(lines)
		frame("grow2")
	}
	lines = append(lines[:len(lines)-10], "result-sed")
	component.SetLines(lines)
	frame("collapse2")

	// Final answer streams.
	for i := 6; i < 12; i++ {
		lines = append(lines, fmt.Sprintf("answer-%02d", i))
		component.SetLines(lines)
		frame("answer2")
	}
}
