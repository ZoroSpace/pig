//go:build windows

package env

import "syscall"

// spawnSysProcAttr builds the process attributes matching pi's spawn options
// on Windows: windowsHide (CREATE_NO_WINDOW via HideWindow) plus a new
// process group for tree kills.
func spawnSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x00000200, // CREATE_NEW_PROCESS_GROUP
	}
}
