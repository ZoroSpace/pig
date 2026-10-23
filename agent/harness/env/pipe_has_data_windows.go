//go:build windows

// pipe_has_data_windows.go — Windows half of the pipeHasData peek. Windows
// anonymous pipes (os.Pipe) do not expose a portable zero-timeout readability
// check here, so the processWaiter grace timer keeps its original
// unconditional-finalize behavior on Windows (the drain-cutoff flake this
// guards against is a POSIX -race/contention artifact; see
// pipe_has_data_unix.go).
package env

import "os"

func pipeHasData(f *os.File) bool {
	return false
}
