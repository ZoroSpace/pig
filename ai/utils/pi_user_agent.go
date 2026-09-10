// pi: utils/pi-user-agent.ts.
package utils

import (
	"os/exec"
	"runtime"
	"strings"
	"sync"
)

// GetPiUserAgent builds the user agent sent to providers (pi:
// getPiUserAgent). pi renders `pi (<platform> <release>; <arch>)` from the
// runtime OS, or "pi (browser)" when it cannot reach node:os; pig always runs
// on a native OS, so it renders the native form. The release string comes from
// `uname -r` (the same kernel release node:os surfaces) and falls back to the
// GOOS string when uname is unavailable.
func GetPiUserAgent() string {
	return "pi (" + runtime.GOOS + " " + kernelRelease() + "; " + runtime.GOARCH + ")"
}

var (
	kernelReleaseOnce sync.Once
	kernelReleaseVal  string
)

func kernelRelease() string {
	kernelReleaseOnce.Do(func() {
		out, err := exec.Command("uname", "-r").Output()
		if err == nil {
			release := strings.TrimSpace(string(out))
			if release != "" {
				kernelReleaseVal = release
			}
		}
		if kernelReleaseVal == "" {
			kernelReleaseVal = runtime.GOOS
		}
	})
	return kernelReleaseVal
}
