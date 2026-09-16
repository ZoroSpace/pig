//go:build windows

// shell_kill_windows.go — Windows half of KillProcessTree (taskkill branch
// moved from shell.go verbatim in the Windows compile port,
// WINDOWS-SUPPORT 2026-09-14).

package utils

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
)

// KillProcessTree kills a process and all its children (pi:
// killProcessTree).
func KillProcessTree(pid int) {
	// pi (v0.85.1): use the trusted System32 executable so cleanup does
	// not depend on PATH.
	taskkill := filepath.Join(systemRoot(), "System32", "taskkill.exe")
	cmd := exec.Command(taskkill, "/F", "/T", "/PID", strconv.Itoa(pid))
	cmd.SysProcAttr = spawnSysProcAttr()
	_ = cmd.Start() // Ignore errors if taskkill fails.
}

// systemRoot resolves the Windows system root (pi:
// process.env.SystemRoot ?? "C:\\Windows").
func systemRoot() string {
	if root := os.Getenv("SystemRoot"); root != "" {
		return root
	}
	return `C:\Windows`
}
