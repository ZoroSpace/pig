//go:build windows

package utils

import "os/exec"

// setClipboardCommandDetached is a no-op on Windows (pi: detached is false
// there; windowsHide covers the console window).
func setClipboardCommandDetached(cmd *exec.Cmd) {}
