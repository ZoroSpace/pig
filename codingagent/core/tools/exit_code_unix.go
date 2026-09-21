//go:build unix

// pi: src/core/tools/bash.ts reads `child.signalCode` (Node) to map
// signal-killed shells to 128 + signal number (pi v0.86.0, #9577). The Go
// equivalent reads the WaitStatus; the signal half is Unix-only, so the
// extraction is split by build tag (see exit_code_windows.go).
package tools

import (
	"os"
	"syscall"
)

// signalExitCode reports the 128+signal exit code convention for a
// signal-terminated process (pi: 128 + osConstants.signals[signalCode]).
// signaled=false means the process was not killed by a signal.
func signalExitCode(ps *os.ProcessState) (code int, signaled bool) {
	ws, ok := ps.Sys().(syscall.WaitStatus)
	if !ok || !ws.Signaled() {
		return 0, false
	}
	// pi: signals[signalCode] ?? 0 → 128 when the signal is unknown; the Go
	// WaitStatus always carries the signal number when Signaled().
	return 128 + int(ws.Signal()), true
}
