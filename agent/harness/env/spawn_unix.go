//go:build !windows

package env

import "syscall"

// spawnSysProcAttr builds the process attributes matching pi's spawn options
// on POSIX: detached (own process group, so killProcessTree can signal the
// whole tree).
func spawnSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}
