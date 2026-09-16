//go:build windows

// terminal_windows.go — Windows half of ProcessTerminal's resize plumbing
// (WINDOWS-SUPPORT 2026-09-14; POSIX counterpart: terminal_unix.go).
//
// Windows has no SIGWINCH. pi's Node 'resize' event is likewise poll-fed on
// Windows (libuv polls the console size on a timer), so the resize handler
// is driven by a size-poll goroutine rather than a signal. Declared
// deviation from the original plan's "compare inside the render tick"
// option: pig's render pipeline is event-driven (scheduleRender via
// time.AfterFunc) with no fixed tick to piggyback on — a pure render-tick
// compare would miss resizes while the UI is idle.

package tui

import (
	"os"
	"time"

	"golang.org/x/term"
)

// windowsResizePollInterval is how often the console size is polled for
// resizes. A single GetConsoleScreenBufferInfo per tick is negligible at
// this cadence.
const windowsResizePollInterval = 250 * time.Millisecond

// consoleSizeFn reads the physical console size of the output stream.
// Test seam (mirrors terminalPlatform/killSelfSignal on unix): Go tests
// cannot allocate a real Windows console, so tests override this var.
var consoleSizeFn = func(t *ProcessTerminal) (int, int, bool) {
	if f, ok := t.out.(*os.File); ok {
		if w, h, err := term.GetSize(int(f.Fd())); err == nil {
			return w, h, true
		}
	}
	return 0, 0, false
}

// refreshTerminalDimensions is a no-op on Windows: there is no SIGWINCH to
// re-send, and the resize poll in startWinchHandler re-reads the console
// size on its own cadence (the POSIX seam-based refresh lives in
// terminal_unix.go; pi's refreshTerminalDimensions is POSIX-only too).
func refreshTerminalDimensions() {}

// startWinchHandler polls the console size and forwards changes to
// onResize (Windows counterpart of the SIGWINCH goroutine). Lifecycle is
// identical to the unix version: Stop() closes winchStop and winchWG tracks
// the goroutine.
func (t *ProcessTerminal) startWinchHandler(onResize func()) {
	t.mu.Lock()
	if t.winchStop != nil {
		t.mu.Unlock()
		return
	}
	stop := make(chan struct{})
	t.winchStop = stop
	t.mu.Unlock()

	t.winchWG.Add(1)
	go func() {
		defer t.winchWG.Done()
		ticker := time.NewTicker(windowsResizePollInterval)
		defer ticker.Stop()
		lastW, lastH := -1, -1
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				w, h, ok := consoleSizeFn(t)
				if !ok || (w == lastW && h == lastH) {
					continue
				}
				lastW, lastH = w, h
				if onResize != nil {
					onResize()
				}
			}
		}
	}()
}
