//go:build !windows

// ctrl_z_unix.go — POSIX half of InteractiveMode.handleCtrlZ (split from
// interactive_mode.go in the Windows compile port, WINDOWS-SUPPORT
// 2026-09-14; Windows counterpart: ctrl_z_windows.go — compile-time
// dispatch replaced the runtime isWindows() branch).

package interactive

import (
	"os"
	"os/signal"
	"syscall"

	pigutils "github.com/ZoroSpace/pig/utils"
)

// handleCtrlZ suspends the process (pi: handleCtrlZ).
func (m *InteractiveMode) handleCtrlZ() {
	// Stop the TUI (restore terminal to normal mode), then signal the
	// process group; SIGCONT restarts the TUI (pig: pi's keep-alive timer
	// and SIGINT ignore have no Go event-loop equivalent — signal delivery
	// is independent of timers).
	m.ui.Stop(nil)
	if err := syscall.Kill(0, syscall.SIGTSTP); err != nil {
		m.ui.Start()
		return
	}
	cont := make(chan os.Signal, 1)
	signal.Notify(cont, syscall.SIGCONT)
	go func() {
		// pig panic containment (see docs/feat/PANIC-RESILIENCE.md).
		defer pigutils.RecoverPanic("interactive.sigcont")
		<-cont
		signal.Stop(cont)
		m.ui.Start()
		m.ui.RequestRender(true)
	}()
}
