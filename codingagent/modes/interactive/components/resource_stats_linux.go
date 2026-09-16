//go:build linux

// resource_stats_linux.go — Linux samplers for the footer resource segment
// (pig-only). See resource_stats.go for the shared poller.
package components

import (
	"os"
	"strconv"
	"strings"
)

// readRSSBytes reads the current resident set size from /proc/self/statm
// (field 2, resident pages) scaled by the page size.
func readRSSBytes() (int64, bool) {
	data, err := os.ReadFile("/proc/self/statm")
	if err != nil {
		return 0, false
	}
	fields := strings.Fields(string(data))
	if len(fields) < 2 {
		return 0, false
	}
	pages, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil || pages <= 0 {
		return 0, false
	}
	return pages * int64(os.Getpagesize()), true
}

// readFDCount counts open file descriptors (/proc/self/fd entries; the
// count includes the listing handle itself — ±1 noise is irrelevant).
func readFDCount() (int64, bool) {
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		return 0, false
	}
	return int64(len(entries)), true
}
