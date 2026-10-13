// tui/tui_main_screen_viewport_test.go — pig-only regression tests for the
// "terminal jumps to top then replays to bottom" bug (pi issue #9255, still
// open upstream as of v0.85.1; pi's tui-main-screen.ts is unchanged after the
// pinned baseline).
//
// pi's TuiMainScreen.doRender takes fullRender(true) — ESC[2J ESC[H ESC[3J
// (clear screen + clear scrollback) followed by repainting the ENTIRE buffer —
// whenever the first changed line sits above the viewport top. Once a
// streaming live block grows taller than the viewport, its top edge stays
// above the viewport top and this fires on nearly every frame (measured at
// 99.9% of renders in pi#9255), showing as a violent jump to the top of the
// transcript followed by a rapid replay down to the bottom. Most visible in
// tmux, which lacks DEC 2026 synchronized output to hide the intermediate
// frames, and whose pane zoom/resize/attach events trigger additional full
// renders (width/height change paths).
//
// pig clamps the repaint to the visible viewport range instead; these tests
// pin the three branches (skip / clamp / keep-full-render-on-shrink) plus the
// visible content produced by the clamped repaint.
package tui

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// buildNumberedLines returns n plain lines "row-00" .. "row-(n-1)".
func buildNumberedLines(n int) []string {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = fmt.Sprintf("row-%02d", i)
	}
	return lines
}

// newViewportTestTui builds an 8-row main-screen TUI driven synchronously via
// RenderNow (render pipeline runs inline, no throttle scheduling involved).
func newViewportTestTui(t *testing.T) (*VirtualTerminal, *TuiMainScreen, *testComponent) {
	t.Helper()
	terminal := NewVirtualTerminal(40, 8)
	tui := NewTuiMainScreen(terminal, nil, "")
	component := &testComponent{}
	tui.AddChild(component)
	tui.Start()
	t.Cleanup(func() { tui.Stop(nil) })
	return terminal, tui, component
}

// The #9255 storm shape: a live block whose top edge (row 10) is above the
// viewport top (12) rebuilds and grows every frame. Each frame must repaint
// the visible range only — no full redraw, no screen clear — and the visible
// viewport must end up showing the latest buffer lines.
func TestMainScreenNoFullRedrawStormWhenChangesStartAboveViewport(t *testing.T) {
	terminal, tui, component := newViewportTestTui(t)

	// 20 lines in an 8-row viewport: rows 0..11 fall into scrollback,
	// previousViewportTop = 12. The first render accounts for one fullRender.
	component.SetLines(buildNumberedLines(20))
	tui.RenderNow(false)
	redraws := tui.FullRedraws()

	for frame := 0; frame < 5; frame++ {
		lines := buildNumberedLines(20 + frame)
		lines[10] = fmt.Sprintf("live-%d", frame)
		lines = append(lines, fmt.Sprintf("tail-%d", frame))
		component.SetLines(lines)
		terminal.ClearWrites()
		tui.RenderNow(false)

		if got := tui.FullRedraws(); got != redraws {
			t.Fatalf("frame %d: full redraw storm (count %d -> %d)", frame, redraws, got)
		}
		if writes := terminal.GetWrites(); strings.Contains(writes, "\x1b[2J") {
			t.Fatalf("frame %d: screen cleared (ESC[2J emitted): %q", frame, writes)
		}
	}

	// Final buffer is 25 lines; the viewport must show its last 8 rows.
	final := buildNumberedLines(24)
	final[10] = "live-4"
	final = append(final, "tail-4")
	got := terminal.GetViewport()
	want := final[len(final)-8:]
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("viewport = %q, want %q", got, want)
	}
}

// Changes confined to scrollback (entirely above the viewport top) are
// invisible: nothing may be painted and no full redraw may fire. Bookkeeping
// must still advance so the next viewport-visible diff stays correct.
func TestMainScreenScrollbackOnlyChangeSkipsPainting(t *testing.T) {
	terminal, tui, component := newViewportTestTui(t)

	component.SetLines(buildNumberedLines(20))
	tui.RenderNow(false)
	before := terminal.GetViewport()
	redraws := tui.FullRedraws()

	lines := buildNumberedLines(20)
	lines[5] = "rewritten-above"
	component.SetLines(lines)
	terminal.ClearWrites()
	tui.RenderNow(false)

	if got := tui.FullRedraws(); got != redraws {
		t.Fatalf("full redraw for scrollback-only change (%d -> %d)", redraws, got)
	}
	if writes := terminal.GetWrites(); strings.Contains(writes, "\x1b[2J") || strings.Contains(writes, "\x1b[2K") {
		t.Fatalf("screen painted for scrollback-only change: %q", writes)
	}
	if got := terminal.GetViewport(); !reflect.DeepEqual(got, before) {
		t.Fatalf("viewport changed for scrollback-only edit: %q -> %q", before, got)
	}

	// Follow-up change inside the viewport must repaint against the NEW
	// previousLines (row 19 = screen row 7 at viewport top 12).
	lines2 := buildNumberedLines(20)
	lines2[5] = "rewritten-above"
	lines2[19] = "rewritten-below"
	component.SetLines(lines2)
	terminal.ClearWrites()
	tui.RenderNow(false)

	if got := tui.FullRedraws(); got != redraws {
		t.Fatalf("follow-up viewport change triggered full redraw (%d -> %d)", redraws, got)
	}
	vp := terminal.GetViewport()
	if vp[7] != "rewritten-below" {
		t.Fatalf("viewport row 7 = %q, want rewritten-below", vp[7])
	}
	if vp[0] != "row-12" {
		t.Fatalf("viewport row 0 = %q, want unchanged row-12", vp[0])
	}
}

// Equal-length rebuild whose top edge is above the viewport top: the repaint
// must be clamped to the visible range (rows above stay stale in scrollback —
// strictly better than pi's behavior of erasing the scrollback entirely).
func TestMainScreenBlockRebuildAboveViewportRepaintsVisibleRange(t *testing.T) {
	terminal, tui, component := newViewportTestTui(t)

	component.SetLines(buildNumberedLines(20))
	tui.RenderNow(false)
	redraws := tui.FullRedraws()

	lines := buildNumberedLines(20)
	lines[10] = "live-rebuild"
	lines[19] = "tail-updated"
	component.SetLines(lines)
	terminal.ClearWrites()
	tui.RenderNow(false)

	if got := tui.FullRedraws(); got != redraws {
		t.Fatalf("equal-length rebuild triggered full redraw (%d -> %d)", redraws, got)
	}
	if writes := terminal.GetWrites(); strings.Contains(writes, "\x1b[2J") {
		t.Fatalf("screen cleared for equal-length rebuild: %q", writes)
	}
	want := []string{"row-12", "row-13", "row-14", "row-15", "row-16", "row-17", "row-18", "tail-updated"}
	if got := terminal.GetViewport(); !reflect.DeepEqual(got, want) {
		t.Fatalf("viewport = %q, want %q", got, want)
	}
}

// Tool-call collapse shape: a live output panel TALLER than the viewport
// (top edge above viewportTop) folds up to a compact card. The buffer shrinks
// while firstChanged sits above the viewport top — this must repaint the
// visible window in place, NOT wipe screen+scrollback and replay the whole
// transcript (which is what pi does and pig round-1 kept, storming on every
// tall tool completion).
// Large collapse (slide 17 > maxShrinkWindowSlide): full render — visually a
// single jump-and-replay, but the reconstructed scrollback is line-exact. The
// window repaint this test used to pin duplicates slide-many scrollback rows
// (docs/fix/2026-10-13-scrollback-duplication-window-repaint.md).
func TestMainScreenLargeCollapseFullRendersScrollbackExact(t *testing.T) {
	terminal, tui, component := newViewportTestTui(t)

	// Transcript, then a live tool panel streams taller than the viewport:
	// 20 rows + 20 panel rows = 40; viewport top ends at 32, panel top (20)
	// is far above it. Growth frames stay incremental (round-1 clamp path).
	lines := buildNumberedLines(20)
	for i := 0; i < 20; i++ {
		lines = append(lines, fmt.Sprintf("panel-%02d", i))
		component.SetLines(lines)
		tui.RenderNow(false)
	}
	redraws := tui.FullRedraws()

	// Tool completes: the 20-row panel collapses to a 3-row card (slide 17).
	collapsed := buildNumberedLines(20)
	collapsed = append(collapsed, "card-0", "card-1", "card-2")
	component.SetLines(collapsed)
	terminal.ClearWrites()
	tui.RenderNow(false)

	writes := terminal.GetWrites()
	if !strings.Contains(writes, "\x1b[2J") || !strings.Contains(writes, "\x1b[3J") {
		t.Fatalf("large collapse must full-render (slide > %d): %q", maxShrinkWindowSlide, writes[:minInt(200, len(writes))])
	}
	if got := tui.FullRedraws(); got != redraws+1 {
		t.Fatalf("collapse should full-render exactly once (count %d, want %d)", got, redraws+1)
	}
	// The rebuilt terminal must match the buffer line-for-line — no duplicated
	// region, no stale live rows (this is the scrollback-duplication killer).
	assertBufferMatchesScrollback(t, terminal, collapsed, "large collapse")
	// The viewport shows the bottom 8 rows of the collapsed buffer.
	wantViewport := []string{"row-15", "row-16", "row-17", "row-18", "row-19", "card-0", "card-1", "card-2"}
	if got := terminal.GetViewport(); !reflect.DeepEqual(got, wantViewport) {
		t.Fatalf("viewport = %q, want %q", got, wantViewport)
	}

	// Bookkeeping stays consistent: a follow-up append paints incrementally.
	grown := buildNumberedLines(20)
	grown = append(grown, "card-0", "tail-0")
	component.SetLines(grown)
	terminal.ClearWrites()
	tui.RenderNow(false)
	if writes := terminal.GetWrites(); strings.Contains(writes, "\x1b[2J") {
		t.Fatalf("follow-up append triggered full render: %q", writes)
	}
	if vp := terminal.GetViewport(); vp[6] != "tail-0" {
		t.Fatalf("viewport row 6 = %q, want tail-0", vp[6])
	}
	assertBufferMatchesScrollback(t, terminal, grown, "post-collapse append")
}

// Small collapse (slide 7 ≤ maxShrinkWindowSlide): the smooth window repaint
// survives — a few duplicated boundary rows are imperceptible, and no
// full-render replay fires.
func TestMainScreenSmallCollapseStillRepaintsWindow(t *testing.T) {
	terminal, tui, component := newViewportTestTui(t)

	lines := buildNumberedLines(20)
	for i := 0; i < 10; i++ {
		lines = append(lines, fmt.Sprintf("panel-%02d", i))
		component.SetLines(lines)
		tui.RenderNow(false)
	}
	redraws := tui.FullRedraws()
	scrollTop := terminal.GetScrollBuffer()[:12]

	collapsed := buildNumberedLines(20)
	collapsed = append(collapsed, "card-0", "card-1", "card-2")
	component.SetLines(collapsed)
	terminal.ClearWrites()
	tui.RenderNow(false)

	writes := terminal.GetWrites()
	if strings.Contains(writes, "\x1b[2J") || strings.Contains(writes, "\x1b[3J") || strings.Contains(writes, "\x1b[H") {
		t.Fatalf("small collapse must keep the window repaint: %q", writes[:minInt(200, len(writes))])
	}
	if got := tui.FullRedraws(); got != redraws+1 {
		t.Fatalf("small collapse should repaint the window once (count %d, want %d)", got, redraws+1)
	}
	after := terminal.GetScrollBuffer()
	if len(after) < len(scrollTop) {
		t.Fatalf("scrollback truncated: %d rows < %d", len(after), len(scrollTop))
	}
	for i, want := range scrollTop {
		if after[i] != want {
			t.Fatalf("scrollback row %d changed: %q, want %q", i, after[i], want)
		}
	}
	wantViewport := []string{"row-15", "row-16", "row-17", "row-18", "row-19", "card-0", "card-1", "card-2"}
	if got := terminal.GetViewport(); !reflect.DeepEqual(got, wantViewport) {
		t.Fatalf("viewport = %q, want %q", got, wantViewport)
	}
}

// Content shrinking while changes start above the viewport: the viewport must
// slide up to keep the buffer bottom (editor/input) visible, which relative-
// only painting cannot express — pig repaints just the visible window instead
// of pi's fullRender(true) (no scrollback wipe, no top-of-buffer replay).
func TestMainScreenShrinkSpanningViewportRepaintsWindow(t *testing.T) {
	terminal, tui, component := newViewportTestTui(t)

	component.SetLines(buildNumberedLines(20))
	tui.RenderNow(false)
	redraws := tui.FullRedraws()
	scrollBefore := terminal.GetScrollBuffer()

	lines := buildNumberedLines(17)
	lines[10] = "changed"
	component.SetLines(lines)
	terminal.ClearWrites()
	tui.RenderNow(false)

	writes := terminal.GetWrites()
	if strings.Contains(writes, "\x1b[2J") || strings.Contains(writes, "\x1b[3J") || strings.Contains(writes, "\x1b[H") {
		t.Fatalf("shrink spanning viewport wiped screen/scrollback: %q", writes)
	}
	if got := tui.FullRedraws(); got != redraws+1 {
		t.Fatalf("shrink spanning viewport should repaint the window (count %d, want %d)", got, redraws+1)
	}
	scrollAfter := terminal.GetScrollBuffer()
	if len(scrollAfter) < len(scrollBefore) {
		t.Fatalf("scrollback truncated: %d rows < %d", len(scrollAfter), len(scrollBefore))
	}
	for i, want := range scrollBefore {
		if i >= len(scrollAfter) {
			break
		}
		// Rows above the new viewport top keep their old content; rows inside
		// the repainted window are expected to change.
		if i < 9 && scrollAfter[i] != want {
			t.Fatalf("scrollback row %d changed: %q, want %q", i, scrollAfter[i], want)
		}
	}
	want := []string{"row-09", "changed", "row-11", "row-12", "row-13", "row-14", "row-15", "row-16"}
	if got := terminal.GetViewport(); !reflect.DeepEqual(got, want) {
		t.Fatalf("viewport = %q, want %q", got, want)
	}
}
