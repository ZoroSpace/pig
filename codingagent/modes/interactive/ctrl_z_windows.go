//go:build windows

// ctrl_z_windows.go — Windows half of InteractiveMode.handleCtrlZ
// (WINDOWS-SUPPORT 2026-09-14). Windows has no process-group suspension
// (no SIGTSTP/SIGCONT constants); this keeps pi's windows-branch message,
// which pig previously selected at runtime via isWindows().

package interactive

// handleCtrlZ suspends the process (pi: handleCtrlZ).
func (m *InteractiveMode) handleCtrlZ() {
	m.showStatus("Suspend to background is not supported on Windows")
}
