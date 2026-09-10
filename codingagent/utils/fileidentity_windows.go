//go:build windows

package utils

import "io/fs"

// fileIdentity returns zeros on Windows (pi's stats.dev/stats.ino come from
// libuv's Windows stat shims with no Go counterpart).
func fileIdentity(info fs.FileInfo) (uint64, uint64) {
	return 0, 0
}
