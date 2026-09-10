//go:build !windows

package utils

import "syscall"

// spawnSysProcAttr matches pi's spawn options on POSIX: detached (own
// process group, so killProcessTree can signal the whole tree).
func spawnSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}
