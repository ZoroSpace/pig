//go:build unix

// resource_stats_unix.go — CPU sampling for POSIX platforms (Linux,
// darwin; Windows uses GetProcessTimes — see resource_stats_windows.go).
// Shared by the linux/darwin platform files; see resource_stats.go for the
// poller.
package components

import (
	"syscall"
)

// readProcessCPUMicros reads the process's cumulative CPU time
// (utime+stime) in microseconds via getrusage — portable across the POSIX
// platforms pig supports, and finer-grained than /proc/self/stat's
// USER_HZ ticks.
func readProcessCPUMicros() (int64, bool) {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		return 0, false
	}
	utime := int64(ru.Utime.Sec)*1e6 + int64(ru.Utime.Usec)
	stime := int64(ru.Stime.Sec)*1e6 + int64(ru.Stime.Usec)
	return utime + stime, true
}
