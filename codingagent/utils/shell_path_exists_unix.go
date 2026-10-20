//go:build !windows

// shell_path_exists_unix.go — POSIX half of shellPathExists (S20).

package utils

import "syscall"

// shellPathExists reports whether path exists, following symlinks (pi:
// fs.accessSync(path, fs.constants.F_OK); Node's access is access(2), which is
// not limited by read permission).
func shellPathExists(path string) bool {
	return syscall.Access(path, 0) == nil
}
