//go:build !windows

package mcp

import (
	"os/exec"
	"syscall"
)

// setProcessGroup puts the child in its own process group so killProcessGroup
// can reap the whole tree (MCP servers commonly spawn children of their own).
// Mirrors codingagent/utils/spawn_attr_unix.go behavior.
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killProcessGroup best-effort SIGKILLs the whole process group. Mirrors
// codingagent/utils/shell_kill_unix.go behavior.
func killProcessGroup(pid int) {
	if pid <= 0 {
		return
	}
	_ = syscall.Kill(-pid, syscall.SIGKILL)
	_ = syscall.Kill(pid, syscall.SIGKILL)
}
