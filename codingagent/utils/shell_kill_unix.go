//go:build !windows

// shell_kill_unix.go — POSIX half of KillProcessTree (split from shell.go
// in the Windows compile port, WINDOWS-SUPPORT 2026-09-14; Windows
// counterpart: shell_kill_windows.go — compile-time dispatch replaced the
// runtime GOOS branch).

package utils

import (
	"runtime"
	"syscall"
)

// processGOOS and killProcessGroup are test seams for the platform branch
// (pi: tests replace process.platform and process.kill).
var (
	processGOOS      = runtime.GOOS
	killProcessGroup = syscall.Kill
)

// KillProcessTree kills a process and all its children (pi:
// killProcessTree).
func KillProcessTree(pid int) {
	if processGOOS == "darwin" {
		// macOS signals group members individually after releasing the group
		// lock. A killed child can wake its waiting shell before that shell is
		// signalled, letting `a; b` start b during cancellation (#11252).
		// Stop the group first to reduce this race; this is best-effort, not
		// an atomic freeze. SIGKILL also kills stopped processes. Linux holds
		// tasklist_lock across group signalling; child exit notification needs
		// its write lock, so the shell is signalled before observing the
		// killed child's exit.
		_ = killProcessGroup(-pid, syscall.SIGSTOP)
	}
	if err := killProcessGroup(-pid, syscall.SIGKILL); err != nil {
		// Fallback to killing just the child if process group kill fails.
		_ = killProcessGroup(pid, syscall.SIGKILL)
	}
}
