// pi: core/provider-attribution.ts — provider attribution headers merged into
// every model request (openrouter/nvidia/cloudflare/opencode specifics).
package core

import (
	"net/url"
	"strings"

	"github.com/ZoroSpace/pig/ai/types"
	"github.com/samber/lo"
)

const (
	openRouterHost            = "openrouter.ai"
	nvidiaNimHost             = "integrate.api.nvidia.com"
	cloudflareAPIHost         = "api.cloudflare.com"
	cloudflareAIGatewayHost   = "gateway.ai.cloudflare.com"
	opencodeHost              = "opencode.ai"
	installTelemetryUserAgent = "pi-coding-agent"
)

func matchesHost(baseURL string, expectedHost string) bool {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return false
	}
	return parsed.Hostname() == expectedHost
}

func isOpenRouterModel(model *types.Model) bool {
	return model.Provider == "openrouter" || strings.Contains(model.BaseURL, openRouterHost)
}

func isNvidiaNimModel(model *types.Model) bool {
	return model.Provider == "nvidia" || matchesHost(model.BaseURL, nvidiaNimHost)
}

func isCloudflareModel(model *types.Model) bool {
	return model.Provider == "cloudflare-workers-ai" ||
		model.Provider == "cloudflare-ai-gateway" ||
		matchesHost(model.BaseURL, cloudflareAPIHost) ||
		matchesHost(model.BaseURL, cloudflareAIGatewayHost)
}

// getDefaultAttributionHeaders builds the install-telemetry attribution
// headers (pi: getDefaultAttributionHeaders).
func getDefaultAttributionHeaders(model *types.Model, settingsManager *SettingsManager) types.ProviderHeaders {
	if !IsInstallTelemetryEnabled(settingsManager, nil) {
		return nil
	}

	// pi's header values are plain string maps; pig's ProviderHeaders wraps
	// values in pointers so they survive the merge (lo.ToPtr per §3.4).
	strHeaders := func(values map[string]string) types.ProviderHeaders {
		headers := make(types.ProviderHeaders, len(values))
		for key, value := range values {
			headers[key] = lo.ToPtr(value)
		}
		return headers
	}

	if isOpenRouterModel(model) {
		return strHeaders(map[string]string{
			"HTTP-Referer":            "https://pi.dev",
			"X-OpenRouter-Title":      "pi",
			"X-OpenRouter-Categories": "cli-agent",
		})
	}

	if isNvidiaNimModel(model) {
		return strHeaders(map[string]string{
			"X-BILLING-INVOKE-ORIGIN": "Pi",
		})
	}

	if isCloudflareModel(model) {
		return strHeaders(map[string]string{
			"User-Agent": installTelemetryUserAgent,
		})
	}

	return nil
}

// getSessionHeaders builds the opencode session headers (pi:
// getSessionHeaders). sessionId "" = undefined.
func getSessionHeaders(model *types.Model, sessionID string) types.ProviderHeaders {
	if sessionID == "" {
		return nil
	}
	if model.Provider != "opencode" &&
		model.Provider != "opencode-go" &&
		!matchesHost(model.BaseURL, opencodeHost) {
		return nil
	}
	return types.ProviderHeaders{
		"x-opencode-session": lo.ToPtr(sessionID),
		"x-opencode-client":  lo.ToPtr("pi"),
	}
}

// mergeProviderAttributionHeaders merges session + attribution headers over
// the request header sources (pi: mergeProviderAttributionHeaders). pi's
// variadic headerSources become a slice; nil entries are skipped. A value
// later in the chain wins (Object.assign semantics).
func MergeProviderAttributionHeaders(
	model *types.Model,
	settingsManager *SettingsManager,
	sessionID string,
	headerSources ...types.ProviderHeaders,
) types.ProviderHeaders {
	merged := types.ProviderHeaders{}
	for key, value := range getSessionHeaders(model, sessionID) {
		merged[key] = value
	}
	for key, value := range getDefaultAttributionHeaders(model, settingsManager) {
		merged[key] = value
	}

	for _, headers := range headerSources {
		if headers != nil {
			for key, value := range headers {
				merged[key] = value
			}
		}
	}

	if len(merged) == 0 {
		return nil
	}
	return merged
}
