//go:build windows

// resource_stats_windows.go — Windows stubs for the footer resource segment
// (pig-only).
//
// pig does not run on Windows yet (the TUI layer depends on POSIX signals —
// syscall.Kill/SIGWINCH — see tui/terminal.go), so these are compile
// health-stubs, not a supported sampling path: they report "unavailable"
// and the footer segment simply stays hidden. When pig gains Windows
// support, wire real implementations here, all cgo-free:
//
//   - RSS:      GetProcessMemoryInfo → WorkingSetSize (x/sys/windows)
//   - CPU:      GetProcessTimes → kernel+user FILETIMEs (100ns units)
//   - FD count: no /proc equivalent — GetProcessHandleCount reports
//     handles (a different currency; the FD alert threshold would need a
//     Windows-specific baseline, typical processes hold hundreds)
package components

// readRSSBytes is unavailable on Windows (stub; see file comment).
func readRSSBytes() (int64, bool) {
	return 0, false
}

// readFDCount is unavailable on Windows (stub; see file comment).
func readFDCount() (int64, bool) {
	return 0, false
}

// readProcessCPUMicros is unavailable on Windows (stub; see file comment).
func readProcessCPUMicros() (int64, bool) {
	return 0, false
}
