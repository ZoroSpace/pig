//go:build windows

package mcp

import "os"

// processAliveOnce reports whether the pid is still running (test helper).
func processAliveOnce(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	_, err = p.Wait()
	return err == nil
}
