//go:build !windows

// terminal_unix.go — POSIX half of ProcessTerminal's resize plumbing
// (split from terminal.go for the Windows compile port, WINDOWS-SUPPORT
// 2026-09-14; Windows counterpart: terminal_windows.go).

package tui

import (
	"os"
	"os/signal"
	"runtime"
	"syscall"
)

// refreshTerminalDimensions refreshes terminal dimensions on POSIX platforms
// by sending SIGWINCH to this process. Best-effort: some environments
// (restricted seccomp or LSM policies) return EACCES for kill(2); in that
// case the dimensions refresh is skipped rather than crashing (pi:
// refreshTerminalDimensions).
// terminalPlatform and killSelfSignal are test seams for pi's
// process.platform / process.kill monkey-patching (Go cannot override
// syscalls).
var (
	terminalPlatform = runtime.GOOS
	killSelfSignal   = func() error { return syscall.Kill(os.Getpid(), syscall.SIGWINCH) }
)

func refreshTerminalDimensions() {
	if terminalPlatform == "windows" {
		return
	}
	_ = killSelfSignal()
}

// startWinchHandler runs a goroutine forwarding SIGWINCH to onResize.
func (t *ProcessTerminal) startWinchHandler(onResize func()) {
	t.mu.Lock()
	if t.winchStop != nil {
		t.mu.Unlock()
		return
	}
	stop := make(chan struct{})
	t.winchStop = stop
	t.mu.Unlock()

	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	t.winchWG.Add(1)
	go func() {
		defer t.winchWG.Done()
		for {
			select {
			case <-stop:
				signal.Stop(winch)
				return
			case <-winch:
				if onResize != nil {
					onResize()
				}
			}
		}
	}()
}
