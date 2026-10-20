//go:build windows

// shell_path_exists_windows.go — Windows half of shellPathExists (S20).

package utils

import "os"

// shellPathExists reports whether path exists (pi: fs.accessSync(path,
// fs.constants.F_OK)). os.Stat is deliberately avoided: Node's stat/access
// split exists because the Windows Store app-execution aliases (which
// "where bash.exe" returns for WSL) make GetFileAttributesEx fail with
// EACCES even though the alias is runnable.
func shellPathExists(path string) bool {
	file, err := os.OpenFile(path, os.O_RDONLY, 0)
	if err != nil {
		return false
	}
	_ = file.Close()
	return true
}
