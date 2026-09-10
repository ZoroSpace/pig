package tui

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/samber/lo"
)

// pi: "shows a clickable jump-to-end indicator on the transcript's last row
// while scrolled up"
func TestTuiAltScreenShowsAClickableJumpToEndIndicatorWhileScrolledUp(t *testing.T) {
	terminal := NewVirtualTerminal(30, 6)
	tui := NewTuiAltScreen(terminal, nil, "", &TuiAltScreenOptions{
		ScrollToEndIndicator: func() string { return "\x1b[7m ↓ Jump to end \x1b[27m" },
	})
	transcript := NewScrollView(
		NewText(altTestLines(8), 0, 0),
		&ScrollViewOptions{Follow: "end", Primary: true},
	)
	tui.SetLayoutRoot(NewVStack([]StackChild{
		StackEntryOf(transcript, &StackEntryOptions{Basis: 0, Grow: 1, MinSize: 1}),
		StackEntryOf(NewText("editor\nfooter", 0, 0), &StackEntryOptions{MinSize: 1}),
	}, nil))
	tui.Start()
	terminal.WaitForRender()
	if altViewportSome(terminal, func(line string) bool { return strings.Contains(line, "Jump to end") }) {
		t.Fatal("indicator must be hidden while following end")
	}

	terminal.SendInput("\x1b[<64;1;1M")
	terminal.WaitForRender()
	if transcript.IsFollowingEnd() {
		t.Fatal("expected transcript scrolled away from end")
	}
	// pi asserts the full 30-column row (its xterm double keeps written
	// default-attribute pad cells); pig's VirtualTerminal trims trailing
	// default-attribute cells, so assert the trimmed form. The renderer
	// output is byte-identical (composite visible width = 30).
	if got := terminal.GetViewport()[3]; got != "line 7  ↓ Jump to end " {
		t.Fatalf("indicator row = %q", got)
	}
	if got := strings.TrimRight(terminal.GetViewport()[4], " \t"); got != "editor" {
		t.Fatalf("row after indicator = %q, want editor", got)
	}

	// Pressing next to the label starts a selection instead of jumping.
	terminal.SendInput("\x1b[<0;2;4M")
	terminal.SendInput("\x1b[<0;2;4m")
	terminal.WaitForRender()
	if transcript.IsFollowingEnd() {
		t.Fatal("selection press must not jump to end")
	}

	terminal.SendInput("\x1b[<0;15;4M")
	terminal.SendInput("\x1b[<0;15;4m")
	terminal.WaitForRender()
	if !transcript.IsFollowingEnd() {
		t.Fatal("indicator click should jump to end")
	}
	altAssertViewport(t, terminal, []string{"line 5", "line 6", "line 7", "line 8", "editor", "footer"})
	tui.Stop(nil)
}

// pi: "scrolls faster while Alt is held during wheel input"
func TestTuiAltScreenScrollsFasterWhileAltIsHeldDuringWheelInput(t *testing.T) {
	terminal := NewVirtualTerminal(20, 4)
	tui := NewTuiAltScreen(terminal, nil, "", nil)
	tui.AddChild(NewText(altTestLines(12), 0, 0))
	tui.Start()
	terminal.WaitForRender()
	if got := tui.ViewportTop(); got != 8 {
		t.Fatalf("viewportTop = %d, want 8", got)
	}

	// Alt modifier sets bit 8 on the wheel button (72 = 64 + 8).
	terminal.SendInput("\x1b[<72;1;1M")
	terminal.WaitForRender()
	if got := tui.ViewportTop(); got != 3 {
		t.Fatalf("viewportTop = %d, want 3", got)
	}
	tui.Stop(nil)
}

// pi: "leaves selections visible without copying when copyOnSelect is
// disabled"
func TestTuiAltScreenLeavesSelectionsVisibleWithoutCopyingWhenCopyOnSelectIsDisabled(t *testing.T) {
	terminal := &recordingTerminal{VirtualTerminal: NewVirtualTerminal(20, 4)}
	var copied []string
	tui := NewTuiAltScreen(terminal, nil, "", &TuiAltScreenOptions{
		CopyOnSelect: lo.ToPtr(false),
		CopySelection: func(text string) bool {
			copied = append(copied, text)
			return true
		},
	})
	tui.AddChild(NewText("alpha\nbeta\ngamma\ndelta", 0, 0))
	tui.Start()
	terminal.WaitForRender()

	terminal.SendInput("\x1b[<0;1;1M")
	terminal.SendInput("\x1b[<32;4;2M")
	terminal.SendInput("\x1b[<0;4;2m")
	terminal.WaitForRender()

	if len(copied) != 0 {
		t.Fatalf("copied = %q, want none", copied)
	}
	if !tui.HasActiveSelection() {
		t.Fatal("expected an active selection")
	}
	if !strings.Contains(terminal.eventsJoined(), "\x1b[7m") {
		t.Fatal("expected selection highlight")
	}
	for _, line := range terminal.GetViewport() {
		if strings.Contains(line, "Copied!") {
			t.Fatal("must not flash Copied! when copyOnSelect is disabled")
		}
	}
	tui.Stop(nil)
}

// pi: "copies an active selection programmatically"
func TestTuiAltScreenCopiesAnActiveSelectionProgrammatically(t *testing.T) {
	terminal := &recordingTerminal{VirtualTerminal: NewVirtualTerminal(20, 4)}
	var copied []string
	tui := NewTuiAltScreen(terminal, nil, "", &TuiAltScreenOptions{
		CopySelection: func(text string) bool {
			copied = append(copied, text)
			return true
		},
	})
	tui.AddChild(NewText("alpha\nbeta\ngamma\ndelta", 0, 0))
	tui.Start()
	terminal.WaitForRender()

	if tui.HasActiveSelection() {
		t.Fatal("expected no active selection initially")
	}
	if tui.CopyActiveSelectionToClipboard() {
		t.Fatal("expected programmatic copy to fail without selection")
	}

	terminal.SendInput("\x1b[<0;1;1M")
	terminal.SendInput("\x1b[<32;4;2M")
	terminal.SendInput("\x1b[<0;4;2m")
	terminal.WaitForRender()

	if !stringSlicesEqual(copied, []string{"alpha\nbeta"}) {
		t.Fatalf("copied = %q, want [alpha\\nbeta]", copied)
	}
	if !tui.HasActiveSelection() {
		t.Fatal("expected an active selection")
	}

	copied = nil
	if !tui.CopyActiveSelectionToClipboard() {
		t.Fatal("expected programmatic copy to succeed")
	}
	terminal.WaitForRender()

	if !stringSlicesEqual(copied, []string{"alpha\nbeta"}) {
		t.Fatalf("copied = %q, want [alpha\\nbeta]", copied)
	}
	if !altViewportSome(terminal, func(line string) bool { return strings.Contains(line, "Copied!") }) {
		t.Fatal("expected Copied! flash")
	}
	tui.Stop(nil)
}

// pi: "coalesces slash and hyphen separated segments for double-click word
// selection"
func TestTuiAltScreenCoalescesSlashAndHyphenSeparatedSegmentsForDoubleClickWordSelection(t *testing.T) {
	terminal := &recordingTerminal{VirtualTerminal: NewVirtualTerminal(20, 4)}
	var copied []string
	tui := NewTuiAltScreen(terminal, nil, "", &TuiAltScreenOptions{
		CopySelection: func(text string) bool {
			copied = append(copied, text)
			return true
		},
	})
	tui.AddChild(NewText("go to /usr/local/bin now\nsecond line here", 0, 0))
	tui.Start()
	terminal.WaitForRender()

	// Double-click "local" inside /usr/local/bin: the joiner segments glue
	// the path into one word.
	terminal.SendInput("\x1b[<0;8;1M")
	terminal.SendInput("\x1b[<0;8;1M")
	terminal.SendInput("\x1b[<0;8;1m")
	terminal.WaitForRender()

	if !stringSlicesEqual(copied, []string{"/usr/local/bin"}) {
		t.Fatalf("copied = %q, want [/usr/local/bin]", copied)
	}
	tui.Stop(nil)
}

// mouseCountingComponent counts clicks reported by the gesture machinery
// (pi: inline handleMouse component).
type mouseCountingComponent struct {
	clicks  []int
	onClick func()
}

func (c *mouseCountingComponent) Render(width int) []string { return []string{"control"} }
func (c *mouseCountingComponent) Invalidate()               {}
func (c *mouseCountingComponent) HandleMouse(event *TuiMouseEvent) *TuiMouseEventResult {
	if event.Type == MouseEventTypePress {
		return &TuiMouseEventResult{Handled: true}
	}
	if event.Type == MouseEventTypeClick {
		count := 0
		if event.ClickCount != nil {
			count = *event.ClickCount
		}
		c.clicks = append(c.clicks, count)
		return &TuiMouseEventResult{Handled: true}
	}
	return nil
}

// pi: "reports consecutive click counts to component-owned controls"
func TestTuiAltScreenReportsConsecutiveClickCountsToComponentOwnedControls(t *testing.T) {
	terminal := NewVirtualTerminal(20, 1)
	tui := NewTuiAltScreen(terminal, nil, "", nil)
	component := &mouseCountingComponent{}
	tui.AddChild(component)
	tui.Start()
	terminal.WaitForRender()

	for count := 0; count < 3; count++ {
		terminal.SendInput("\x1b[<0;1;1M")
		terminal.SendInput("\x1b[<0;1;1m")
	}
	terminal.WaitForRender()
	if !intSlicesEqual(component.clicks, []int{1, 2, 3}) {
		t.Fatalf("clicks = %v, want [1 2 3]", component.clicks)
	}
	tui.Stop(nil)
}

// pi: "reuses indexed transcript matches until the query or rendered lines
// change"
func TestTuiAltScreenReusesIndexedTranscriptMatchesUntilTheQueryOrRenderedLinesChange(t *testing.T) {
	index := NewAltScreenSearchIndex()
	lines := []string{"alpha QUICK", "brown fox"}
	result := index.Search(lines, "quick brown")
	if len(result.Matches) != 1 {
		t.Fatalf("matches = %d, want 1", len(result.Matches))
	}
	if !result.Changed {
		t.Fatal("first search must report changed")
	}

	again := index.Search(lines, "quick brown")
	if len(again.Matches) != 1 || again.Changed {
		t.Fatalf("cached search = %+v, want unchanged match", again)
	}

	if result := index.Search(append([]string{"lead"}, lines...), "quick brown"); !result.Changed {
		t.Fatal("changed lines must recompute")
	}
	if result := index.Search(lines, "fox"); !result.Changed {
		t.Fatal("changed query must recompute")
	}
	_ = time.Now // keep time import if assertions above change
}

// pi: "maps normalized ASCII and Unicode search matches back to rendered
// columns"
func TestTuiAltScreenMapsNormalizedAsciiAndUnicodeSearchMatchesBackToRenderedColumns(t *testing.T) {
	matches := FindAltScreenSearchMatches([]string{"alpha QUICK", "brown fox"}, "quick brown")
	if len(matches) != 1 {
		t.Fatalf("matches = %d, want 1", len(matches))
	}
	segments := matches[0].Segments
	if len(segments) != 2 {
		t.Fatalf("segments = %d, want 2", len(segments))
	}
	if segments[0].Row != 0 || segments[0].StartCol != 6 || segments[0].EndCol != 11 {
		t.Fatalf("segment 0 = %+v", segments[0])
	}
	if segments[1].Row != 1 || segments[1].StartCol != 0 || segments[1].EndCol != 5 {
		t.Fatalf("segment 1 = %+v", segments[1])
	}

	unicodeLines := []string{" café  UI ", "next"}
	unicodeMatches := FindAltScreenSearchMatches(unicodeLines, "café ui")
	if len(unicodeMatches) != 1 {
		t.Fatalf("unicode matches = %d, want 1", len(unicodeMatches))
	}
}

// pi: "focuses and captures drag gestures for mouse-aware components"
func TestTuiAltScreenFocusesAndCapturesDragGesturesForMouseAwareComponents(t *testing.T) {
	terminal := NewVirtualTerminal(20, 2)
	tui := NewTuiAltScreen(terminal, nil, "", nil)
	var events []string
	component := &mouseRecordingComponent{events: &events}
	tui.AddChild(component)
	tui.Start()
	terminal.WaitForRender()

	terminal.SendInput("\x1b[<0;1;1M")
	terminal.SendInput("\x1b[<32;5;2M")
	terminal.SendInput("\x1b[<0;5;2m")
	terminal.WaitForRender()

	if !stringSlicesEqual(events, []string{"press", "drag", "release"}) {
		t.Fatalf("events = %q", events)
	}
	if got := tui.GetFocusedComponent(); got != Component(component) {
		t.Fatalf("focused = %v, want component", got)
	}
	tui.Stop(nil)
}

// mouseRecordingComponent records mouse event types (pi: inline component).
type mouseRecordingComponent struct {
	events   *[]string
	wheelHit *int
}

func (c *mouseRecordingComponent) Render(width int) []string { return []string{"control"} }
func (c *mouseRecordingComponent) Invalidate()               {}
func (c *mouseRecordingComponent) HandleMouse(event *TuiMouseEvent) *TuiMouseEventResult {
	*c.events = append(*c.events, event.Type)
	if event.Type == MouseEventTypePress {
		return &TuiMouseEventResult{Handled: true, Capture: true, Focus: true}
	}
	if c.wheelHit != nil && event.Type == MouseEventTypeWheel {
		*c.wheelHit++
		return &TuiMouseEventResult{Handled: true}
	}
	return &TuiMouseEventResult{Handled: true}
}

// pi: "lets mouse-aware components consume wheel events before viewport
// scrolling"
func TestTuiAltScreenLetsMouseAwareComponentsConsumeWheelEventsBeforeViewportScrolling(t *testing.T) {
	terminal := NewVirtualTerminal(20, 3)
	tui := NewTuiAltScreen(terminal, nil, "", nil)
	wheelEvents := 0
	tui.AddChild(NewMouseRegion(
		NewText(altTestLines(8), 0, 0),
		func(event *TuiMouseEvent) *TuiMouseEventResult {
			if event.Type != MouseEventTypeWheel {
				return nil
			}
			wheelEvents++
			return &TuiMouseEventResult{Handled: true}
		},
	))
	tui.Start()
	terminal.WaitForRender()
	viewportTop := tui.ViewportTop()

	terminal.SendInput("\x1b[<64;1;1M")
	terminal.WaitForRender()
	if wheelEvents != 1 {
		t.Fatalf("wheelEvents = %d, want 1", wheelEvents)
	}
	if got := tui.ViewportTop(); got != viewportTop {
		t.Fatalf("viewportTop = %d, want %d", got, viewportTop)
	}
	tui.Stop(nil)
}

// pi: "does not rerender for handled no-op pointer motion"
func TestTuiAltScreenDoesNotRerenderForHandledNoOpPointerMotion(t *testing.T) {
	terminal := &recordingTerminal{VirtualTerminal: NewVirtualTerminal(20, 2)}
	tui := NewTuiAltScreen(terminal, nil, "", nil)
	component := &countingMouseComponent{}
	tui.AddChild(component)
	tui.Start()
	terminal.WaitForRender()
	renderedBeforeMotion := component.RenderCount()
	writesBeforeMotion := len(terminal.Events())

	terminal.SendInput("\x1b[<35;1;1M")
	terminal.WaitForRender()
	if got := component.RenderCount(); got != renderedBeforeMotion {
		t.Fatalf("renderCount = %d, want %d", got, renderedBeforeMotion)
	}
	if got := len(terminal.Events()); got != writesBeforeMotion {
		t.Fatalf("writes = %d, want %d", got, writesBeforeMotion)
	}
	tui.Stop(nil)
}

// countingMouseComponent counts renders and no-ops handled moves (pi: inline
// component). The counter is mutex-guarded: pig renders on a timer goroutine,
// while pi counts on the single event loop.
type countingMouseComponent struct {
	mu          sync.Mutex
	renderCount int
}

func (c *countingMouseComponent) Render(width int) []string {
	c.mu.Lock()
	c.renderCount++
	c.mu.Unlock()
	return []string{"hover target"}
}

func (c *countingMouseComponent) RenderCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.renderCount
}
func (c *countingMouseComponent) Invalidate() {}
func (c *countingMouseComponent) HandleMouse(event *TuiMouseEvent) *TuiMouseEventResult {
	if event.Type == MouseEventTypeMove {
		return &TuiMouseEventResult{Handled: true}
	}
	return nil
}
