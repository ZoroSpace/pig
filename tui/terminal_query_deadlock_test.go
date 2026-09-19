// tui/terminal_query_deadlock_test.go — regression for the input-dispatch
// deadlock in QueryTerminalBackgroundColor (pig-only concurrency face; see
// docs/fix/2026-09-19-terminal-query-dispatch-deadlock.md). pi awaits the
// query promises on its single event loop, so a theme change triggered from
// a key handler never blocks input processing. pig's QueryTerminal-
// BackgroundColor is synchronous and callable from inside input dispatch
// (b.mu held by the dispatching goroutine): its timeout timer used to take
// b.mu too, so it could never settle the query and the whole TUI froze
// (settings → Theme → Automatic → Apply reproduced it 100%).
package tui

import (
	"io"
	"testing"
	"time"
)

// dispatchDeadlockTerminal is a minimal Terminal stub: only Write matters
// (the query writes the OSC 11 request); everything else is inert.
type dispatchDeadlockTerminal struct {
	out io.Writer
}

func (t *dispatchDeadlockTerminal) Start(onInput func(data string), onResize func()) {
}
func (t *dispatchDeadlockTerminal) Stop() {}
func (t *dispatchDeadlockTerminal) DrainInput(maxMs int, idleMs int) {
}
func (t *dispatchDeadlockTerminal) Write(data string) {
	if t.out != nil {
		_, _ = io.WriteString(t.out, data)
	}
}
func (t *dispatchDeadlockTerminal) Columns() int              { return 80 }
func (t *dispatchDeadlockTerminal) Rows() int                 { return 24 }
func (t *dispatchDeadlockTerminal) KittyProtocolActive() bool { return false }
func (t *dispatchDeadlockTerminal) MoveBy(lines int)          {}
func (t *dispatchDeadlockTerminal) HideCursor()               {}
func (t *dispatchDeadlockTerminal) ShowCursor()               {}
func (t *dispatchDeadlockTerminal) ClearLine()                {}
func (t *dispatchDeadlockTerminal) ClearFromCursor()          {}
func (t *dispatchDeadlockTerminal) ClearScreen()              {}
func (t *dispatchDeadlockTerminal) SetTitle(title string)     {}
func (t *dispatchDeadlockTerminal) SetProgress(active bool)   {}

// dispatchDeadlockSubclass is an inert tuiSubclass so the immediate-render
// goroutine spawned by input dispatch has a doRender to call.
type dispatchDeadlockSubclass struct{}

func (s *dispatchDeadlockSubclass) doRender() error                           { return nil }
func (s *dispatchDeadlockSubclass) resetRenderState()                         {}
func (s *dispatchDeadlockSubclass) beforeTerminalStart()                      {}
func (s *dispatchDeadlockSubclass) afterTerminalStart()                       {}
func (s *dispatchDeadlockSubclass) beforeTerminalStop(options TuiStopOptions) {}
func (s *dispatchDeadlockSubclass) afterTerminalStop(options TuiStopOptions)  {}
func (s *dispatchDeadlockSubclass) getMountedRoots() []Component              { return nil }

// queryOnInputComponent reproduces the interactive settings chain: a key
// handler that synchronously queries the terminal background from within
// HandleInput (theme apply → applyFromSettings → detectTerminalThemeForAuto).
type queryOnInputComponent struct {
	tui            *TuiBase
	timeout        time.Duration
	done           chan struct{}
	useColorScheme bool
}

func (c *queryOnInputComponent) HandleInput(data string) {
	if c.useColorScheme {
		c.tui.QueryTerminalColorScheme(int(c.timeout / time.Millisecond))
	} else {
		c.tui.QueryTerminalBackgroundColor(int(c.timeout / time.Millisecond))
	}
	close(c.done)
}
func (c *queryOnInputComponent) Render(width int) []string { return nil }
func (c *queryOnInputComponent) Invalidate()               {}

func TestTerminalQueryTimeoutSurvivesInputDispatch(t *testing.T) {
	testTerminalQueryFromDispatch(t, false)
}

// TestColorSchemeQuerySurvivesInputDispatch is the regression for the
// deadlock the Apply button actually hit first: QueryTerminalColorScheme
// registered its settle listener via OnTerminalColorSchemeChange, which took
// b.mu directly — self-deadlock while the dispatching goroutine holds b.mu,
// before the OSC 11 fallback is ever reached.
func TestColorSchemeQuerySurvivesInputDispatch(t *testing.T) {
	testTerminalQueryFromDispatch(t, true)
}

func testTerminalQueryFromDispatch(t *testing.T, colorScheme bool) {
	t.Helper()
	term := &dispatchDeadlockTerminal{out: io.Discard}
	b := newTuiBase(term, TuiModeRegular, &dispatchDeadlockSubclass{}, nil, "")

	comp := &queryOnInputComponent{
		tui:            b,
		timeout:        50 * time.Millisecond,
		done:           make(chan struct{}),
		useColorScheme: colorScheme,
	}
	b.SetFocus(comp)

	// Run the dispatch on its own goroutine like the terminal reader does;
	// the query blocks inside HandleInput while b.mu is held.
	go func() { b.handleTerminalInput("x") }()

	// Pre-fix: the OSC 11 timeout timer blocked on b.mu (background-query
	// variant) / listener registration blocked on b.mu (color-scheme
	// variant), so the query — and this dispatch — never completes → the
	// 5s guard fires. Post-fix: the timer settles via CAS at ~50ms.
	select {
	case <-comp.done:
	case <-time.After(5 * time.Second):
		name := "background"
		if colorScheme {
			name = "color-scheme"
		}
		t.Fatalf("input dispatch deadlocked: %s query could not settle while b.mu was held by the dispatching goroutine", name)
	}
}

func TestTerminalQueryRealReplyWinsOverTimeout(t *testing.T) {
	term := &dispatchDeadlockTerminal{out: io.Discard}
	b := newTuiBase(term, TuiModeRegular, &dispatchDeadlockSubclass{}, nil, "")

	// Reply delivered from another goroutine while the query is pending —
	// consume path (b.mu held) must win the settle race and deliver the
	// parsed color instead of the timeout's nil. Query timeout is long
	// enough that only the reply path can settle within the guard.
	go func() {
		time.Sleep(10 * time.Millisecond)
		b.handleTerminalInput("\x1b]11;rgb:0000/0000/0000\x07")
	}()

	done := make(chan *RgbColor, 1)
	go func() { done <- b.QueryTerminalBackgroundColor(10_000) }()
	select {
	case rgb := <-done:
		if rgb == nil {
			t.Fatal("query returned nil (timeout) although a reply was delivered")
		}
		if rgb.R != 0 || rgb.G != 0 || rgb.B != 0 {
			t.Fatalf("parsed color %+v, want 0/0/0", rgb)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("query with real reply did not settle")
	}
}
