//go:build windows

package utils

import "syscall"

// spawnSysProcAttr matches pi's spawn options on Windows: windowsHide plus a
// new process group for tree kills.
func spawnSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x00000200, // CREATE_NEW_PROCESS_GROUP
	}
}
