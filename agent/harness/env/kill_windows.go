//go:build windows

// kill_windows.go — Windows half of killProcessTree (taskkill branch moved
// from nodejs.go verbatim in the Windows compile port, WINDOWS-SUPPORT
// 2026-09-14).

package env

import (
	"os/exec"
	"strconv"
)

// killProcessTree force-kills a process and its group (pi:
// killProcessTree).
func killProcessTree(pid int) {
	cmd := exec.Command("taskkill", "/F", "/T", "/PID", strconv.Itoa(pid))
	cmd.SysProcAttr = spawnSysProcAttr()
	_ = cmd.Start() // pi ignores errors.
}
