//go:build darwin

// resource_stats_darwin.go — macOS samplers for the footer resource segment
// (pig-only). See resource_stats.go for the shared poller.
//
// macOS has no procfs, and reading RSS via mach APIs (task_info /
// proc_pidinfo) would require cgo, which pig's fully static build
// (CGO_ENABLED=0) rules out. The resident set size therefore comes from
// spawning the system ps — ~1-2ms per sample, imperceptible at the 5s
// cadence, and the same "current memory" semantics as Linux's statm.
package components

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// readRSSBytes reads the current resident set size by asking ps
// (`ps -o rss= -p <pid>` prints kilobytes for our own pid).
func readRSSBytes() (int64, bool) {
	out, err := exec.Command("ps", "-o", "rss=", "-p", strconv.Itoa(os.Getpid())).Output()
	if err != nil {
		return 0, false
	}
	kb, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	if err != nil || kb <= 0 {
		return 0, false
	}
	return kb * 1024, true
}

// readFDCount counts open file descriptors via /dev/fd (fdesc lists the
// calling process's descriptors).
func readFDCount() (int64, bool) {
	entries, err := os.ReadDir("/dev/fd")
	if err != nil {
		return 0, false
	}
	return int64(len(entries)), true
}
