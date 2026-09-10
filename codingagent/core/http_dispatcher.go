// pi: src/core/http-dispatcher.ts — HTTP transport tuning (portable half).
//
// Segment deviations:
//   - The undici dispatcher half (configureHttpDispatcher, the undici
//     Client/Pool wiring, the global-fetch installation and the undici error
//     listener) is Node-undici-specific with no Go counterpart — Go's
//     net/http needs no dispatcher swap; deferred until a consumer actually
//     needs Go transport tuning (position kept below).
//   - applyHttpProxySettings: process.env.HTTP_PROXY ??= proxy → only set
//     when currently unset (Go os.Environ scan for the case-insensitive key
//     is unnecessary here; Go's http transport reads the upper-case vars,
//     matching Node's upper-case convention).
//   - v0.85.1 adds undici's proxyTunnel:true (keep HTTP origins on CONNECT
//     tunnels as before Undici 8.7): Node-undici-specific, no Go equivalent
//     — Go's http.Transport always proxies https over CONNECT.
package core

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// DefaultHTTPIdleTimeoutMs is the default HTTP header/body idle timeout (pi:
// DEFAULT_HTTP_IDLE_TIMEOUT_MS).
const DefaultHTTPIdleTimeoutMs = 300_000

// defaultAutoSelectFamilyAttemptTimeoutMs mirrors Node's happy-eyeballs
// attempt timeout override (pi:
// DEFAULT_AUTO_SELECT_FAMILY_ATTEMPT_TIMEOUT_MS). No Go counterpart yet
// (net/http has its own fallback handling); kept for source parity.
const defaultAutoSelectFamilyAttemptTimeoutMs = 2_000

// HTTPIdleTimeoutChoice is one picker entry (pi: HTTP_IDLE_TIMEOUT_CHOICES
// member).
type HTTPIdleTimeoutChoice struct {
	Label     string
	TimeoutMs int64
}

// HTTPIdleTimeoutChoices lists the idle-timeout picker options (pi:
// HTTP_IDLE_TIMEOUT_CHOICES, declaration order kept).
var HTTPIdleTimeoutChoices = []HTTPIdleTimeoutChoice{
	{Label: "30 sec", TimeoutMs: 30_000},
	{Label: "1 min", TimeoutMs: 60_000},
	{Label: "2 min", TimeoutMs: 120_000},
	{Label: "5 min", TimeoutMs: 300_000},
	{Label: "disabled", TimeoutMs: 0},
}

// ParseHTTPIdleTimeoutMs parses the httpIdleTimeoutMs setting value (pi:
// parseHttpIdleTimeoutMs). Returns (n, true) for a valid timeout, (_, false)
// for undefined (value unset or unparseable).
func ParseHTTPIdleTimeoutMs(value any) (int64, bool) {
	switch v := value.(type) {
	case string:
		trimmed := strings.TrimSpace(v)
		if strings.EqualFold(trimmed, "disabled") {
			return 0, true
		}
		if len(trimmed) == 0 {
			return 0, false
		}
		// pi: Number(trimmed) recursion; NaN falls into the number branch's
		// !Number.isFinite check → undefined.
		number, err := strconv.ParseFloat(trimmed, 64)
		if err != nil {
			return 0, false
		}
		return ParseHTTPIdleTimeoutMs(number)
	case float64:
		if v != v || v < 0 { // NaN or negative (pi: !Number.isFinite || < 0)
			return 0, false
		}
		return int64(v), true
	case int64:
		if v < 0 {
			return 0, false
		}
		return v, true
	case int:
		if v < 0 {
			return 0, false
		}
		return int64(v), true
	default:
		return 0, false
	}
}

// FormatHTTPIdleTimeoutMs renders a timeout for display (pi:
// formatHttpIdleTimeoutMs). Non-choice values render as "<n> sec" (pi's
// number division → shortest float formatting).
func FormatHTTPIdleTimeoutMs(timeoutMs int64) string {
	for _, choice := range HTTPIdleTimeoutChoices {
		if choice.TimeoutMs == timeoutMs {
			return choice.Label
		}
	}
	seconds := float64(timeoutMs) / 1000
	return fmt.Sprintf("%s sec", strconv.FormatFloat(seconds, 'g', -1, 64))
}

// pi: configureHttpDispatcher and the undici Client/Pool/global-fetch
// installation. Deferred — Node-undici-specific, no Go counterpart (see
// header).

// ApplyHTTPProxySettings applies the httpProxy setting as HTTP_PROXY /
// HTTPS_PROXY for Pi-managed HTTP clients (pi: applyHttpProxySettings).
// Only sets variables that are currently unset (pi's ??=).
func ApplyHTTPProxySettings(httpProxy string) {
	proxy := strings.TrimSpace(httpProxy)
	if proxy == "" {
		return
	}
	if os.Getenv("HTTP_PROXY") == "" {
		os.Setenv("HTTP_PROXY", proxy)
	}
	if os.Getenv("HTTPS_PROXY") == "" {
		os.Setenv("HTTPS_PROXY", proxy)
	}
}
