// process_cwd.go — pig-only helper for the process working directory (pi:
// `process.cwd()` in main.ts).
//
// Deviation being fixed (S10-4 bidirectional-compat finding): bun/node's
// `process.cwd()` returns the kernel getcwd(3) result, which resolves
// symlinks (macOS: `cd /tmp/x` → "/private/tmp/x"). Go's os.Getwd() prefers
// $PWD when it matches ".", so it keeps the symlinked spelling ("/tmp/x").
// The session directory name is encoded from the cwd, so the two spellings
// put pig and pi sessions in different directories and `--continue` never
// finds the other CLI's sessions. Resolving symlinks restores the shared
// directory name; on failure the raw value falls back like pi's
// canonicalizePath.
package cli

import (
	"os"
	"path/filepath"
	"runtime"
)

// ProcessCwd returns the working directory with symlinks resolved, matching
// bun's process.cwd() on darwin/linux. Windows keeps the raw os.Getwd value:
// its getcwd equivalent does not resolve junctions, and pi behaves the same.
func ProcessCwd() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	if runtime.GOOS == "windows" {
		return cwd, nil
	}
	if resolved, linkErr := filepath.EvalSymlinks(cwd); linkErr == nil {
		return resolved, nil
	}
	return cwd, nil
}
