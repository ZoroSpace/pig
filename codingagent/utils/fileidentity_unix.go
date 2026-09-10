//go:build !windows

package utils

import (
	"io/fs"
	"syscall"
)

// fileIdentity extracts the (device, inode) pair for change detection (pi:
// stats.dev/stats.ino with bigint). Not available on Windows.
func fileIdentity(info fs.FileInfo) (uint64, uint64) {
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		return uint64(stat.Dev), uint64(stat.Ino)
	}
	return 0, 0
}
