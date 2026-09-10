// open_browser.go (pi: src/utils/open-browser.ts) — open a URL or file in
// the platform browser/default handler.
//
// This intentionally never invokes a shell. On Windows, do not use
// `cmd /c start`: cmd.exe re-parses metacharacters (&, |, ^, ...) before
// `start` runs, which would make attacker-controlled URLs injectable.
package utils

import (
	"os/exec"
	"runtime"
)

// OpenBrowser opens a URL or file with the platform default handler (pi:
// openBrowser). Launcher failures are swallowed: browser launch is
// best-effort and callers still present the target to the user.
func OpenBrowser(target string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", target)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", target)
	default:
		cmd = exec.Command("xdg-open", target)
	}
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Stdin = nil
	// Detached + unref'd: the launcher outlives the process (pi: spawn with
	// detached: true + unref()).
	cmd.SysProcAttr = spawnSysProcAttr()
	if err := cmd.Start(); err != nil {
		return
	}
	go func() { _ = cmd.Wait() }()
}
