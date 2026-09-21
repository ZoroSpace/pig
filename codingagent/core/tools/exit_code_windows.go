//go:build windows

// pi: src/core/tools/bash.ts reads `child.signalCode` (Node) to map
// signal-killed shells to 128 + signal number (pi v0.86.0, #9577). Windows
// process termination has no Unix signal semantics, so nothing reports
// signaled here (pi's osConstants.signals lookup has no Windows counterpart
// either; the fallback exit code 1 applies).
package tools

import (
	"os"
)

// signalExitCode reports the 128+signal exit code convention for a
// signal-terminated process. Windows has no signals (pi: osConstants.signals
// is a Node Unix concept) — always not-signaled.
func signalExitCode(ps *os.ProcessState) (code int, signaled bool) {
	return 0, false
}
