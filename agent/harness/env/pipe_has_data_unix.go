//go:build !windows

// pipe_has_data_unix.go — non-blocking "is there readable data" peek for the
// processWaiter grace timer (2026-10-20 flake close). pi re-arms its idle
// timer while the output spill is still draining; pig's equivalent is a
// zero-timeout poll on the stdout/stderr pipe fds, so finalizing never cuts
// a draining pipe short. The poll is a syscall that does not disturb the
// reader goroutine blocked in read() on the same fd. Windows counterpart:
// pipe_has_data_windows.go (anonymous pipe peek is not available there, so
// the grace timer falls back to its original unconditional finalize).
package env

import (
	"os"

	"golang.org/x/sys/unix"
)

// pipeHasData reports whether the pipe's read end currently has buffered
// data (POLLIN), without consuming it. HUP-only (write ends closed, buffer
// empty) is not "draining": the reader will hit EOF immediately.
func pipeHasData(f *os.File) bool {
	pollFds := []unix.PollFd{{Fd: int32(f.Fd()), Events: unix.POLLIN}}
	for {
		_, err := unix.Poll(pollFds, 0)
		if err == unix.EINTR {
			continue
		}
		return err == nil && pollFds[0].Revents&unix.POLLIN != 0
	}
}
