//go:build windows

// terminal_windows_test.go — resize-poll tests for the Windows half of
// ProcessTerminal's resize plumbing (WINDOWS-SUPPORT; see
// terminal_windows.go). The console size reader is a test seam
// (consoleSizeFn) since tests cannot allocate a real Windows console.
package tui

import (
	"io"
	"strings"
	"testing"
	"time"
)

// resizePollHarness drives the poll loop with a scripted size sequence.
type resizePollHarness struct {
	pt       *ProcessTerminal
	sizes    chan [2]int
	resizes  chan struct{}
	original func(*ProcessTerminal) (int, int, bool)
}

func newResizePollHarness(t *testing.T) *resizePollHarness {
	t.Helper()
	h := &resizePollHarness{
		// in: empty reader (Start's readLoop exits on EOF); out: discard.
		pt:      &ProcessTerminal{in: strings.NewReader(""), out: io.Discard},
		sizes:   make(chan [2]int, 8),
		resizes: make(chan struct{}, 8),
	}
	h.original = consoleSizeFn
	consoleSizeFn = func(*ProcessTerminal) (int, int, bool) {
		select {
		case s := <-h.sizes:
			return s[0], s[1], true
		default:
			return 0, 0, false
		}
	}
	t.Cleanup(func() { consoleSizeFn = h.original })
	return h
}

func TestWindowsResizePollFiresOnChangeOnly(t *testing.T) {
	h := newResizePollHarness(t)
	h.pt.Start(func(string) {}, func() { h.resizes <- struct{}{} })
	defer h.pt.Stop()

	// First observed size is the baseline: one event (matches unix, where
	// Start's refreshTerminalDimensions self-sends one SIGWINCH).
	h.sizes <- [2]int{80, 24}
	// Same size again: no event.
	h.sizes <- [2]int{80, 24}
	// Changed size: exactly one more event.
	h.sizes <- [2]int{120, 40}

	deadline := time.Now().Add(5 * time.Second)
	count := 0
	for count < 2 {
		select {
		case <-h.resizes:
			count++
		case <-time.After(time.Until(deadline)):
			t.Fatalf("expected 2 resize events (baseline + change), got %d", count)
		}
	}
	// And nothing further while the scripted sequence is exhausted (later
	// polls read an empty channel → skipped).
	select {
	case <-h.resizes:
		t.Fatalf("expected no further resize events, got an extra one (total would be %d)", count+1)
	case <-time.After(2*windowsResizePollInterval + 200*time.Millisecond):
	}
}

func TestWindowsResizePollStopStopsGoroutine(t *testing.T) {
	h := newResizePollHarness(t)
	h.pt.Start(func(string) {}, nil)
	h.pt.Stop()

	// Stop() closes winchStop; the goroutine must exit promptly.
	done := make(chan struct{})
	go func() {
		h.pt.winchWG.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("winch poll goroutine did not exit after Stop()")
	}
}
