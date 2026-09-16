//go:build !windows

// shell_kill_unix.go — POSIX half of KillProcessTree (split from shell.go
// in the Windows compile port, WINDOWS-SUPPORT 2026-09-14; Windows
// counterpart: shell_kill_windows.go — compile-time dispatch replaced the
// runtime GOOS branch).

package utils

import "syscall"

// KillProcessTree kills a process and all its children (pi:
// killProcessTree).
func KillProcessTree(pid int) {
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil {
		// Fallback to killing just the child if process group kill fails.
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
}
