//go:build !windows

// kill_unix.go — POSIX half of killProcessTree (split from nodejs.go in the
// Windows compile port, WINDOWS-SUPPORT 2026-09-14; Windows counterpart:
// kill_windows.go — compile-time dispatch replaced the runtime GOOS
// branch).

package env

import "syscall"

// killProcessTree force-kills a process and its group (pi:
// killProcessTree).
func killProcessTree(pid int) {
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
}
