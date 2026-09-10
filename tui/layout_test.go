// tui/layout_test.go (pi: packages/tui/test/layout.test.ts) — the "viewport
// layout" describe block. pi's layout.test.ts also contains
// renderLayoutFrame/ScrollView acceptance owned by the T4 batch; the VStack
// gap case was ported early with stack_test.go (T3).
//
// Test-fixture note: pi constructs components with inline closures; Go uses
// the same shapes (func literals implementing Component).
package tui

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/samber/lo"
)

func visibleLinesLocal(lines []string) []string {
	out := make([]string, len(lines))
	for i, line := range lines {
		out[i] = strings.TrimRight(StripTerminalSequences(line), " ")
	}
	return out
}

func TestViewportLayoutAllocatesVerticalGrowSpaceDeterministically(t *testing.T) {
	frame := RenderLayoutFrame(
		NewVStack([]StackChild{
			StackEntryOf(NewText("top", 0, 0), &StackEntryOptions{Basis: 1, Shrink: lo.ToPtr(0)}),
			StackEntryOf(NewText("body", 0, 0), &StackEntryOptions{Basis: 0, Grow: 1}),
		}, nil),
		10, 4,
		func() {},
	)

	heights := make([]int, len(frame.Root.Children))
	for i, child := range frame.Root.Children {
		heights[i] = child.Rect.Height
	}
	if want := []int{1, 3}; !intSlicesEqual(heights, want) {
		t.Fatalf("heights = %v, want %v", heights, want)
	}
	if got := visibleLinesLocal(frame.Lines); !stringSlicesEqual(got, []string{"top", "body", "", ""}) {
		t.Fatalf("lines = %q, want %q", got, []string{"top", "body", "", ""})
	}
}

// fixedLinesComponent is pi's inline `{ render: () => lines, invalidate: () => {} }`.
type fixedLinesComponent struct {
	mu    sync.Mutex
	lines []string
	count int
}

func (c *fixedLinesComponent) Render(width int) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.count++
	return c.lines
}

func (c *fixedLinesComponent) Invalidate() {}

func (c *fixedLinesComponent) RenderCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.count
}

func (c *fixedLinesComponent) SetLines(lines []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lines = lines
}

func TestViewportLayoutDoesNotRenderFixedBasisScrollContentDuringStackMeasurement(t *testing.T) {
	renderCount := 0
	transcript := NewScrollView(&countingComponent{f: func() { renderCount++ },
		lines: []string{"one", "two", "three"}}, nil)
	root := NewVStack([]StackChild{
		StackEntryOf(transcript, &StackEntryOptions{Basis: 0, Grow: 1}),
		StackEntryOf(NewText("dock", 0, 0), &StackEntryOptions{Basis: "auto"}),
	}, nil)
	RenderLayoutFrame(root, 10, 3, func() {})
	if renderCount != 1 {
		t.Fatalf("renderCount = %d, want 1", renderCount)
	}
}

type countingComponent struct {
	f     func()
	lines []string
}

func (c *countingComponent) Render(width int) []string {
	if c.f != nil {
		c.f()
	}
	return c.lines
}
func (c *countingComponent) Invalidate() {}

// Declared deviation: pi uses a JS sparse array of 1e9 entries (no actual
// allocation); a Go []string of 1e9 entries would be a 16GB pointer array and
// OOM-flaky. 1e7 lines exercises the same clip-only paint path.
func TestViewportLayoutPaintsOnlyClippedRowsFromVeryLargeScrollContent(t *testing.T) {
	lineCount := 10_000_000
	lines := make([]string, lineCount)
	lines[lineCount-4] = "before"
	lines[lineCount-3] = "visible 1"
	lines[lineCount-2] = "visible 2"
	lines[lineCount-1] = "visible 3"
	transcript := NewScrollView(
		&countingComponent{lines: lines},
		&ScrollViewOptions{Follow: "end"},
	)

	frame := RenderLayoutFrame(transcript, 10, 3, func() {})
	if got := visibleLinesLocal(frame.Lines); !stringSlicesEqual(got, []string{"visible 1", "visible 2", "visible 3"}) {
		t.Fatalf("lines = %q", got)
	}
}

func TestViewportLayoutShrinksEntriesToTheirMinimumSizes(t *testing.T) {
	frame := RenderLayoutFrame(
		NewVStack([]StackChild{
			StackEntryOf(NewText("a1\na2\na3", 0, 0), &StackEntryOptions{Shrink: lo.ToPtr(1), MinSize: 1}),
			StackEntryOf(NewText("b1\nb2\nb3", 0, 0), &StackEntryOptions{Shrink: lo.ToPtr(0)}),
		}, nil),
		10, 4,
		func() {},
	)

	heights := make([]int, len(frame.Root.Children))
	for i, child := range frame.Root.Children {
		heights[i] = child.Rect.Height
	}
	if want := []int{1, 3}; !intSlicesEqual(heights, want) {
		t.Fatalf("heights = %v, want %v", heights, want)
	}
	if got := visibleLinesLocal(frame.Lines); !stringSlicesEqual(got, []string{"a1", "b1", "b2", "b3"}) {
		t.Fatalf("lines = %q", got)
	}
}

func TestViewportLayoutIncludesNestedMinimumSizesInIntrinsicStackMeasurement(t *testing.T) {
	dock := NewVStack([]StackChild{
		NewText("top1\ntop2\ntop3", 0, 0),
		StackEntryOf(NewText("selector", 0, 0), &StackEntryOptions{MinSize: 3}),
		NewText("below", 0, 0),
		StackEntryOf(NewText("footer", 0, 0), &StackEntryOptions{MinSize: 1}),
	}, nil)
	frame := RenderLayoutFrame(
		NewVStack([]StackChild{
			StackEntryOf(NewText("body", 0, 0), &StackEntryOptions{Basis: 0, Grow: 1, MinSize: 1}),
			StackEntryOf(dock, &StackEntryOptions{Basis: "auto", MinSize: 1}),
		}, nil),
		10, 9,
		func() {},
	)

	if got := visibleLinesLocal(frame.Lines); !stringSlicesEqual(got, []string{
		"body", "top1", "top2", "top3", "selector", "", "", "below", "footer",
	}) {
		t.Fatalf("lines = %q", got)
	}
}

func TestViewportLayoutOmitsGapsAroundInvisibleEntries(t *testing.T) {
	stack := NewVStack(
		[]StackChild{
			NewText("one", 0, 0),
			StackEntryOf(NewText("hidden", 0, 0), &StackEntryOptions{Visible: func(viewport LayoutViewport) bool { return false }}),
			NewText("two", 0, 0),
		},
		&StackOptions{Gap: lo.ToPtr(1)},
	)
	got := make([]string, 0)
	for _, line := range stack.Render(10) {
		got = append(got, strings.TrimRight(line, " "))
	}
	if !stringSlicesEqual(got, []string{"one", "", "two"}) {
		t.Fatalf("lines = %q", got)
	}
}

func TestViewportLayoutCropsKittyImagesAtAScrollViewsLowerBoundary(t *testing.T) {
	imageId := 124
	imageLine := EncodeKitty("AAAA", EncodeKittyParams{Columns: 2, Rows: 3, ImageId: imageId, MoveCursorOff: true})
	RegisterKittyImageMetadata(KittyImageMetadata{ImageId: imageId, Columns: 2, Rows: 3, WidthPx: 100, HeightPx: 100})
	transcript := NewScrollView(&countingComponent{lines: []string{"one", "two", imageLine, "", ""}}, nil)
	frame := RenderLayoutFrame(
		NewVStack([]StackChild{
			StackEntryOf(transcript, &StackEntryOptions{Basis: 0, Grow: 1}),
			NewText("dock", 0, 0),
		}, nil),
		20, 4,
		func() {},
	)

	if !strings.Contains(frame.Lines[2], "y=0,h=34,r=1") {
		t.Fatalf("line 2 missing crop controls: %q", frame.Lines[2])
	}
}

func TestViewportLayoutComposesHorizontalChildrenAtAllocatedWidths(t *testing.T) {
	frame := RenderLayoutFrame(
		NewHStack([]StackChild{
			StackEntryOf(NewText("left", 0, 0), &StackEntryOptions{Basis: 6, Shrink: lo.ToPtr(0)}),
			StackEntryOf(NewText("right", 0, 0), &StackEntryOptions{Basis: 6, Shrink: lo.ToPtr(0)}),
		}, nil),
		12, 1,
		func() {},
	)
	if got := visibleLinesLocal(frame.Lines); !stringSlicesEqual(got, []string{"left  right"}) {
		t.Fatalf("lines = %q", got)
	}
}

func TestViewportLayoutDoesNotPaintZeroWidthHorizontalChildren(t *testing.T) {
	frame := RenderLayoutFrame(
		NewHStack([]StackChild{
			StackEntryOf(NewText("hidden", 0, 0), &StackEntryOptions{Basis: 0, Shrink: lo.ToPtr(0)}),
			StackEntryOf(NewText("shown", 0, 0), &StackEntryOptions{Basis: 0, Grow: 1}),
		}, nil),
		5, 1,
		func() {},
	)
	if got := visibleLinesLocal(frame.Lines); !stringSlicesEqual(got, []string{"shown"}) {
		t.Fatalf("lines = %q", got)
	}
}

func TestViewportLayoutTracksFollowEndStateAndReturnsUnusedScrollDelta(t *testing.T) {
	scrollView := NewScrollView(NewText("1\n2\n3\n4\n5\n6", 0, 0), &ScrollViewOptions{
		Follow:  "end",
		Primary: true,
	})
	RenderLayoutFrame(scrollView, 10, 3, func() {})
	if scrollView.ScrollTop() != 3 {
		t.Fatalf("scrollTop = %d, want 3", scrollView.ScrollTop())
	}
	if !scrollView.IsFollowingEnd() {
		t.Fatal("expected following end")
	}

	if got := scrollView.ScrollBy(-2); got != 0 {
		t.Fatalf("scrollBy(-2) = %d, want 0", got)
	}
	if scrollView.ScrollTop() != 1 {
		t.Fatalf("scrollTop = %d, want 1", scrollView.ScrollTop())
	}
	if scrollView.IsFollowingEnd() {
		t.Fatal("expected not following end")
	}
	if got := scrollView.ScrollBy(-3); got != -2 {
		t.Fatalf("scrollBy(-3) = %d, want -2", got)
	}
	if scrollView.ScrollTop() != 0 {
		t.Fatalf("scrollTop = %d, want 0", scrollView.ScrollTop())
	}
	if got := scrollView.ScrollBy(10); got != 7 {
		t.Fatalf("scrollBy(10) = %d, want 7", got)
	}
	if scrollView.ScrollTop() != 3 {
		t.Fatalf("scrollTop = %d, want 3", scrollView.ScrollTop())
	}
	if !scrollView.IsFollowingEnd() {
		t.Fatal("expected following end")
	}
}

func TestViewportLayoutRendersATransientProportionalScrollbarWithoutReplacingCellContent(t *testing.T) {
	sourceLines := []string{"abcd界", "abcde2", "abcde3", "abcde4", "abcde5", "abcde6", "abcde7", "abcde8"}
	contentBackground := "\x1b[42m"
	scrollbarBackground := "\x1b[48;5;1m"
	scrollbarStyle := func(text string) string { return scrollbarBackground + text + "\x1b[49m" }
	content := NewText(strings.Join(sourceLines, "\n"), 0, 0, func(text string) string {
		return contentBackground + text + "\x1b[49m"
	})
	scrollView := NewScrollView(content, &ScrollViewOptions{
		Scrollbar:            ScrollViewScrollbarAuto,
		ScrollbarStyle:       scrollbarStyle,
		ScrollbarHideDelayMs: lo.ToPtr(10),
	})
	render := func() []string { return RenderLayoutFrame(scrollView, 6, 4, func() {}).Lines }
	thumbRows := func(lines []string) []bool {
		out := make([]bool, len(lines))
		for i, line := range lines {
			out[i] = strings.Contains(line, scrollbarBackground)
		}
		return out
	}
	stripped := func(lines []string) []string {
		out := make([]string, len(lines))
		for i, line := range lines {
			out[i] = StripTerminalSequences(line)
		}
		return out
	}

	lines := render()
	if got := thumbRows(lines); !boolSlicesEqual(got, []bool{false, false, false, false}) {
		t.Fatalf("thumbRows = %v", got)
	}
	if got := stripped(lines); !stringSlicesEqual(got, sourceLines[:4]) {
		t.Fatalf("lines = %q", got)
	}

	scrollView.ScrollBy(2)
	lines = render()
	if got := thumbRows(lines); !boolSlicesEqual(got, []bool{false, true, true, false}) {
		t.Fatalf("thumbRows = %v", got)
	}
	if got := stripped(lines); !stringSlicesEqual(got, sourceLines[2:6]) {
		t.Fatalf("lines = %q", got)
	}
	if strings.LastIndex(lines[1], contentBackground) > strings.LastIndex(lines[1], scrollbarBackground) {
		t.Fatalf("content background painted over scrollbar: %q", lines[1])
	}

	time.Sleep(30 * time.Millisecond)
	lines = render()
	if got := thumbRows(lines); !boolSlicesEqual(got, []bool{false, false, false, false}) {
		t.Fatalf("thumbRows after hide = %v", got)
	}

	scrollView.ScrollToEnd()
	lines = render()
	if got := thumbRows(lines); !boolSlicesEqual(got, []bool{false, false, true, true}) {
		t.Fatalf("thumbRows after scrollToEnd = %v", got)
	}
	if got := stripped(lines); !stringSlicesEqual(got, sourceLines[4:]) {
		t.Fatalf("lines = %q", got)
	}

	followedContent := NewText(strings.Join(sourceLines, "\n"), 0, 0)
	followed := NewScrollView(followedContent, &ScrollViewOptions{
		Follow:         "end",
		Scrollbar:      ScrollViewScrollbarAuto,
		ScrollbarStyle: scrollbarStyle,
	})
	RenderLayoutFrame(followed, 6, 4, func() {})
	if followed.ScrollTop() != 4 {
		t.Fatalf("followed scrollTop = %d, want 4", followed.ScrollTop())
	}
	followedContent.SetText(strings.Join(sourceLines, "\n") + "\nabcde9")
	growthFrame := RenderLayoutFrame(followed, 6, 4, func() {})
	if followed.ScrollTop() != 5 {
		t.Fatalf("followed scrollTop after growth = %d, want 5", followed.ScrollTop())
	}
	for _, line := range growthFrame.Lines {
		if strings.Contains(line, scrollbarBackground) {
			t.Fatalf("growth frame shows scrollbar: %q", line)
		}
	}

	fittingContent := NewText("1\n2", 0, 0)
	automatic := NewScrollView(fittingContent, &ScrollViewOptions{Scrollbar: ScrollViewScrollbarAuto, ScrollbarStyle: scrollbarStyle})
	RenderLayoutFrame(automatic, 6, 4, func() {})
	automatic.ScrollBy(1)
	for _, line := range RenderLayoutFrame(automatic, 6, 4, func() {}).Lines {
		if strings.Contains(line, scrollbarBackground) {
			t.Fatalf("fitting content shows scrollbar: %q", line)
		}
	}

	alwaysFitting := NewScrollView(fittingContent, &ScrollViewOptions{Scrollbar: ScrollViewScrollbarAlways, ScrollbarStyle: scrollbarStyle})
	alwaysFittingFrame := RenderLayoutFrame(alwaysFitting, 6, 4, func() {})
	if alwaysFittingFrame.Root.Children[0].Rect.Width != 5 {
		t.Fatalf("always-fitting scroll width = %d, want 5", alwaysFittingFrame.Root.Children[0].Rect.Width)
	}
	for _, line := range alwaysFittingFrame.Lines {
		if !strings.Contains(line, scrollbarBackground) {
			t.Fatalf("always-fitting line missing scrollbar: %q", line)
		}
	}

	alwaysOverflowing := NewScrollView(content, &ScrollViewOptions{Scrollbar: ScrollViewScrollbarAlways, ScrollbarStyle: scrollbarStyle})
	alwaysOverflowingFrame := RenderLayoutFrame(alwaysOverflowing, 6, 4, func() {})
	if alwaysOverflowingFrame.Root.Children[0].Rect.Width != 5 {
		t.Fatalf("always-overflowing scroll width = %d, want 5", alwaysOverflowingFrame.Root.Children[0].Rect.Width)
	}
	count := 0
	for _, line := range alwaysOverflowingFrame.Lines {
		if strings.Contains(line, scrollbarBackground) {
			count++
		}
	}
	if count != 2 {
		t.Fatalf("always-overflowing scrollbar rows = %d, want 2", count)
	}

	thumbHeightFor := func(contentHeight int) int {
		xs := make([]string, contentHeight)
		for i := range xs {
			xs[i] = "x"
		}
		sized := NewScrollView(NewText(strings.Join(xs, "\n"), 0, 0), &ScrollViewOptions{
			Scrollbar:      ScrollViewScrollbarAuto,
			ScrollbarStyle: scrollbarStyle,
		})
		RenderLayoutFrame(sized, 6, 20, func() {})
		sized.ScrollBy(1)
		count := 0
		for _, line := range RenderLayoutFrame(sized, 6, 20, func() {}).Lines {
			if strings.Contains(line, scrollbarBackground) {
				count++
			}
		}
		return count
	}
	for _, tc := range []struct{ contentHeight, want int }{{21, 19}, {40, 10}, {100, 4}, {400, 2}} {
		if got := thumbHeightFor(tc.contentHeight); got != tc.want {
			t.Fatalf("thumbHeightFor(%d) = %d, want %d", tc.contentHeight, got, tc.want)
		}
	}
}

func TestViewportLayoutUpdatesReservedScrollbarLayoutAtRuntime(t *testing.T) {
	scrollView := NewScrollView(NewText("123456", 0, 0), &ScrollViewOptions{Scrollbar: ScrollViewScrollbarAlways})
	render := func() *LayoutFrame {
		return RenderLayoutFrame(NewHStack([]StackChild{scrollView}, &StackOptions{Align: "start"}), 6, 2, func() {})
	}
	always := render()
	if got := visibleLinesLocal(always.Lines); !stringSlicesEqual(got, []string{"12345", "6"}) {
		t.Fatalf("lines = %q", got)
	}
	if always.Root.Children[0].Rect.Width != 6 {
		t.Fatalf("scroll box width = %d, want 6", always.Root.Children[0].Rect.Width)
	}
	if always.Root.Children[0].Children[0].Rect.Width != 5 {
		t.Fatalf("content width = %d, want 5", always.Root.Children[0].Children[0].Rect.Width)
	}

	scrollView.SetScrollbar(ScrollViewScrollbarHidden)
	if render().Root.Children[0].Children[0].Rect.Width != 6 {
		t.Fatalf("content width after hidden = %d, want 6", render().Root.Children[0].Children[0].Rect.Width)
	}
	if scrollView.IsScrollbarVisible() {
		t.Fatal("expected scrollbar invisible")
	}
}

func TestViewportLayoutMeasuresNestedScrollContentFromConstrainedChildGeometry(t *testing.T) {
	inner := NewScrollView(NewText("1\n2\n3\n4\n5\n6", 0, 0), nil)
	outer := NewScrollView(NewVStack([]StackChild{
		StackEntryOf(inner, &StackEntryOptions{Basis: 2}),
		NewText("tail", 0, 0),
	}, nil), nil)
	RenderLayoutFrame(outer, 10, 2, func() {})

	if inner.ViewportHeight() != 2 {
		t.Fatalf("inner viewportHeight = %d, want 2", inner.ViewportHeight())
	}
	if got := outer.ScrollBy(10); got != 9 {
		t.Fatalf("outer scrollBy(10) = %d, want 9", got)
	}
	if outer.ScrollTop() != 1 {
		t.Fatalf("outer scrollTop = %d, want 1", outer.ScrollTop())
	}
}

func TestViewportLayoutRebuildsGeometryAfterContentChanges(t *testing.T) {
	text := NewText("one", 0, 0)
	root := NewVStack([]StackChild{text}, nil)
	first := RenderLayoutFrame(root, 10, 4, func() {})
	text.SetText("one\ntwo\nthree")
	second := RenderLayoutFrame(root, 10, 4, func() {})

	if first.Root.Children[0].Lines == nil || len(first.Root.Children[0].Lines) != 1 {
		t.Fatalf("first lines = %d, want 1", len(first.Root.Children[0].Lines))
	}
	if second.Root.Children[0].Lines == nil || len(second.Root.Children[0].Lines) != 3 {
		t.Fatalf("second lines = %d, want 3", len(second.Root.Children[0].Lines))
	}
}

// --- small helpers ----------------------------------------------------------

func intSlicesEqual(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func boolSlicesEqual(a, b []bool) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
