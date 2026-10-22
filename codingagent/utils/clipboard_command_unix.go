//go:build !windows

package utils

import (
	"os/exec"
	"syscall"
)

// setClipboardCommandDetached starts clipboard helpers in their own process
// group (pi: spawn({ detached: process.platform !== "win32" })). Clipboard
// writers can daemonize and keep serving the selection; without this, Ctrl+Z
// suspends pi's whole process group and takes the clipboard owner with it
// (#11179).
func setClipboardCommandDetached(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}
