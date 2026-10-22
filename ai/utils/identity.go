// pi: utils/identity.ts — how pi-ai identifies the calling app to providers.
// The ClientIdentity definitions live in ai/types so ProviderRequestOptions can
// reference them; this file carries the formatting/header helpers.
package utils

import (
	"fmt"
	"net/url"
	"runtime"
	"strings"

	"github.com/ZoroSpace/pig/ai/types"
)

// MergeClientIdentity merges identities field by field; later sources win and
// unset fields are skipped (pi: mergeClientIdentity).
func MergeClientIdentity(sources ...*types.ClientIdentity) *types.ClientIdentity {
	var merged *types.ClientIdentity
	for _, source := range sources {
		if source == nil {
			continue
		}
		if merged == nil {
			merged = &types.ClientIdentity{}
		}
		if source.UserAgent != nil {
			mode := *source.UserAgent
			merged.UserAgent = &mode
		}
		if source.Attribution != nil {
			setting := *source.Attribution
			merged.Attribution = &setting
		}
		if source.Version != "" {
			merged.Version = source.Version
		}
	}
	return merged
}

// GetClientVersion is the version in the detailed User-Agent (pi:
// getClientVersion).
func GetClientVersion(identity *types.ClientIdentity) string {
	if identity != nil && identity.Version != "" {
		return identity.Version
	}
	return PiAIVersion
}

// FormatUserAgent is the User-Agent value of an identity (pi:
// formatUserAgent). pi renders the Node/Bun runtime description; pig renders
// the Go runtime as `go/<version>` in the same slot.
func FormatUserAgent(identity *types.ClientIdentity) string {
	mode := types.UserAgentDetailed()
	if identity != nil && identity.UserAgent != nil {
		mode = identity.UserAgent
	}
	if mode.Custom != "" {
		return mode.Custom
	}
	if mode.Short {
		return "pi"
	}
	product := "pi/" + GetClientVersion(identity)
	return fmt.Sprintf("%s (%s; go/%s; %s)", product, runtime.GOOS, strings.TrimPrefix(runtime.Version(), "go"), runtime.GOARCH)
}

func matchesHost(baseURL, expectedHost string) bool {
	parsed, err := url.Parse(baseURL)
	return err == nil && parsed.Hostname() == expectedHost
}

func isOpenRouter(target types.ClientIdentityTarget) bool {
	return target.Provider == "openrouter" || strings.Contains(target.BaseURL, "openrouter.ai")
}

func isNvidiaNim(target types.ClientIdentityTarget) bool {
	return target.Provider == "nvidia" || matchesHost(target.BaseURL, "integrate.api.nvidia.com")
}

func isOpenCode(target types.ClientIdentityTarget) bool {
	return target.Provider == "opencode" || target.Provider == "opencode-go" ||
		matchesHost(target.BaseURL, "opencode.ai")
}

// upperFirst upper-cases the first code point (pi: name.charAt(0).toUpperCase()
// + name.slice(1); the Go port is rune-safe).
func upperFirst(value string) string {
	for _, r := range value {
		return strings.ToUpper(string(r)) + value[len(string(r)):]
	}
	return value
}

// GetAttributionHeaders returns provider attribution headers of an identity
// for a request target; nil when attribution is off (pi:
// getAttributionHeaders).
func GetAttributionHeaders(target types.ClientIdentityTarget, identity *types.ClientIdentity) types.ProviderHeaders {
	if identity == nil || identity.Attribution == nil || identity.Attribution.Attribution == nil {
		return nil
	}
	attribution := identity.Attribution.Attribution
	if isOpenRouter(target) {
		headers := types.ProviderHeaders{
			"HTTP-Referer":       stringPtr(attribution.URL),
			"X-OpenRouter-Title": stringPtr(attribution.Name),
		}
		if len(attribution.Categories) > 0 {
			headers["X-OpenRouter-Categories"] = stringPtr(strings.Join(attribution.Categories, ","))
		}
		return headers
	}
	if isNvidiaNim(target) {
		return types.ProviderHeaders{"X-BILLING-INVOKE-ORIGIN": stringPtr(upperFirst(attribution.Name))}
	}
	if isOpenCode(target) {
		return types.ProviderHeaders{"x-opencode-client": stringPtr(attribution.Name)}
	}
	return nil
}

// GetOpenCodeSessionHeaders is OpenCode's per-conversation routing header for
// requests to OpenCode, including custom providers pointing at it; nil without
// a session or for other targets (pi: getOpenCodeSessionHeaders).
func GetOpenCodeSessionHeaders(target types.ClientIdentityTarget, sessionID string) types.ProviderHeaders {
	if sessionID == "" || !isOpenCode(target) {
		return nil
	}
	return types.ProviderHeaders{"x-opencode-session": stringPtr(sessionID)}
}

// GetIdentityHeaders returns the default headers an identity adds to a
// request: User-Agent and provider attribution (pi: getIdentityHeaders). APIs
// apply them below model, provider, and request headers, so configured headers
// override them and nil request headers remove them.
func GetIdentityHeaders(target types.ClientIdentityTarget, identity *types.ClientIdentity) types.ProviderHeaders {
	headers := GetAttributionHeaders(target, identity)
	if headers == nil {
		headers = types.ProviderHeaders{}
	}
	headers["User-Agent"] = stringPtr(FormatUserAgent(identity))
	return headers
}

func stringPtr(value string) *string { return &value }
