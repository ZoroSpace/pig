//go:build windows

package mcp

import (
	"os/exec"
	"strconv"
)

// setProcessGroup is a no-op on windows: there is no process-group concept;
// killProcessGroup uses taskkill /T for tree termination instead.
func setProcessGroup(cmd *exec.Cmd) {}

// killProcessGroup terminates the process tree via taskkill /T /F, mirroring
// codingagent/utils/shell_kill_windows.go behavior.
func killProcessGroup(pid int) {
	if pid <= 0 {
		return
	}
	cmd := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(pid))
	_ = cmd.Run()
}
