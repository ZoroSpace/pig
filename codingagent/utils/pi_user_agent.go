// pi: src/utils/pi-user-agent.ts — the coding-agent user agent sent to
// management endpoints (the pi.dev model catalog). pig/ai has its own
// provider-facing user agent (ai/utils/pi_user_agent.go).
//
// Segment deviation (declared): pi renders the JS runtime segment
// (`bun/x.y.z` or `node/vX`); pig has no JS runtime and reports "pig" in its
// place, keeping the `pi/<version> (<platform>; <runtime>; <arch>)` shape.
package utils

import (
	"fmt"
	"runtime"
)

// GetPiUserAgent builds the user agent sent to management endpoints (pi:
// getPiUserAgent(version)).
func GetPiUserAgent(version string) string {
	return fmt.Sprintf("pi/%s (%s; pig; %s)", version, runtime.GOOS, runtime.GOARCH)
}
