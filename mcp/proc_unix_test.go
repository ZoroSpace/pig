//go:build !windows

package mcp

import "syscall"

// processAliveOnce reports whether the pid is still running (test helper).
func processAliveOnce(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}
