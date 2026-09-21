// wsl.go (pi: src/utils/wsl.ts) — Windows Subsystem for Linux detection,
// where Windows executables are reachable through interop. pi moved isWSL
// here from clipboard-image.ts in v0.86.1; pig keeps the same shape the
// clipboard-image port had (compiled per call, like the regex literal).
package utils

import (
	"os"
	"regexp"
)

// isWSL reports whether the session runs under WSL (pi: isWSL).
func isWSL(env func(string) string) bool {
	if env == nil {
		env = os.Getenv
	}
	if env("WSL_DISTRO_NAME") != "" || env("WSLENV") != "" {
		return true
	}
	release, err := os.ReadFile("/proc/version")
	if err != nil {
		return false
	}
	return regexp.MustCompile(`(?i)microsoft|wsl`).Match(release)
}
