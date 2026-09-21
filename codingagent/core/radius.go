// pi: src/core/radius.ts — the Radius provider id and gateway origin helper
// (pi v0.86.0; consumed by the /bug report upload path). The RadiusOAuth-
// Credential half of pi's radius-config (gateway config sanitize/models) has
// no consumer in pig yet — the radius provider itself (pi-messages adapter)
// is deferred (see AGENTS.md Phase 2 scope); only the id + gateway URL are
// ported here. Declared deviation, not an omission.
package core

import (
	"os"
	"regexp"
	"strings"
)

// RADIUS_PROVIDER_ID matches LLAMA-style inline provider ids (pi:
// RADIUS_PROVIDER_ID).
const RADIUS_PROVIDER_ID = "radius"

// ENV_RADIUS_GATEWAY overrides the Radius gateway origin (pi:
// ENV_RADIUS_GATEWAY).
const ENV_RADIUS_GATEWAY = "PI_RADIUS_GATEWAY"

// defaultRadiusGateway is the production gateway origin (pi:
// DEFAULT_RADIUS_GATEWAY from pi-ai/providers/radius-config).
const defaultRadiusGateway = "https://radius.pi.dev"

var radiusSchemeRe = regexp.MustCompile(`(?i)^https?://`)

// normalizeRadiusGatewayUrl adds the https scheme when missing and strips
// trailing slashes (pi: normalizeRadiusGatewayUrl).
func normalizeRadiusGatewayUrl(value string) string {
	withScheme := value
	if !radiusSchemeRe.MatchString(value) {
		withScheme = "https://" + value
	}
	return strings.TrimRight(withScheme, "/")
}

// GetRadiusGatewayUrl returns the Radius gateway origin, honoring the
// PI_RADIUS_GATEWAY override (pi: getRadiusGatewayUrl).
func GetRadiusGatewayUrl() string {
	value := os.Getenv(ENV_RADIUS_GATEWAY)
	if value == "" {
		value = defaultRadiusGateway
	}
	return normalizeRadiusGatewayUrl(value)
}
