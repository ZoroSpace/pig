// Ported from pi's test/sdk-client-identity.test.ts. The capture point is the
// provider's StreamSimple options, after the Models layer merged identity and
// session headers (pi: the same post-applyAuth options).
package core

import (
	"testing"

	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/types"
	aiutils "github.com/ZoroSpace/pig/ai/utils"
	"github.com/ZoroSpace/pig/codingagent"
)

func identityModel(provider string, baseURL string) *types.Model {
	return &types.Model{
		ID:            provider + "-test-model",
		Name:          provider + " Test Model",
		API:           ai.ApiOpenAICompletions,
		Provider:      types.ProviderID(provider),
		BaseURL:       baseURL,
		Reasoning:     false,
		Input:         []ai.ModelInput{ai.ModelInputText},
		Cost:          ai.ModelCost{},
		ContextWindow: 128000,
		MaxTokens:     4096,
	}
}

func captureIdentityOptions(t *testing.T, model *types.Model, telemetryEnabled *bool, providerHeaders map[string]string, sessionID string) *ai.SimpleStreamOptions {
	t.Helper()
	settings := Settings{}
	if telemetryEnabled != nil {
		settings["enableInstallTelemetry"] = *telemetryEnabled
	}
	options := &types.SimpleStreamOptions{StreamOptions: types.StreamOptions{SessionID: sessionID}}
	captured, cleanup := sdkCaptureStreamOptionsWithModel(t, model, settings, options, providerHeaders, nil, sessionID)
	defer cleanup()
	if captured == nil {
		t.Fatal("stream options were not captured")
	}
	return captured
}

func headersGet(headers types.ProviderHeaders, key string) (string, bool) {
	if headers == nil {
		return "", false
	}
	value, ok := headers[key]
	if !ok || value == nil {
		return "", false
	}
	return *value, true
}

func identityTarget(model *types.Model) types.ClientIdentityTarget {
	return types.ClientIdentityTarget{Provider: string(model.Provider), BaseURL: model.BaseURL}
}

func TestCreateAgentSessionClientIdentity(t *testing.T) {
	t.Run("identifies pig with a detailed User-Agent and attribution as a CLI agent", func(t *testing.T) {
		model := identityModel("openrouter", "https://openrouter.ai/api/v1")
		options := captureIdentityOptions(t, model, nil, nil, "")
		identity := options.Identity
		if identity == nil {
			t.Fatal("identity is nil")
		}
		if identity.UserAgent == nil || identity.UserAgent.Short || identity.UserAgent.Custom != "" {
			t.Fatalf("userAgent = %+v, want detailed", identity.UserAgent)
		}
		if identity.Version != codingagent.Version {
			t.Fatalf("version = %q, want %q", identity.Version, codingagent.Version)
		}
		if identity.Attribution == nil || identity.Attribution.Attribution == nil ||
			identity.Attribution.Attribution.Name != "pi" {
			t.Fatalf("attribution = %+v, want pi", identity.Attribution)
		}
		headers := aiutils.GetAttributionHeaders(identityTarget(model), identity)
		if value, _ := headersGet(headers, "HTTP-Referer"); value != "https://pi.dev" {
			t.Fatalf("HTTP-Referer = %q", value)
		}
		if value, _ := headersGet(headers, "X-OpenRouter-Title"); value != "pi" {
			t.Fatalf("X-OpenRouter-Title = %q", value)
		}
		if value, _ := headersGet(headers, "X-OpenRouter-Categories"); value != "cli-agent" {
			t.Fatalf("X-OpenRouter-Categories = %q", value)
		}
	})

	t.Run("turns attribution off when telemetry is disabled", func(t *testing.T) {
		disabled := false
		model := identityModel("nvidia", "https://integrate.api.nvidia.com/v1")
		options := captureIdentityOptions(t, model, &disabled, nil, "")
		if options.Identity == nil || options.Identity.Attribution == nil || options.Identity.Attribution.Attribution != nil {
			t.Fatalf("attribution = %+v, want explicit null", options.Identity)
		}
		if headers := aiutils.GetAttributionHeaders(identityTarget(model), options.Identity); len(headers) != 0 {
			t.Fatalf("attribution headers = %v, want none", headers)
		}
	})

	t.Run("turns attribution off with PI_TELEMETRY=0", func(t *testing.T) {
		t.Setenv("PI_TELEMETRY", "0")
		options := captureIdentityOptions(t, identityModel("openrouter", "https://openrouter.ai/api/v1"), nil, nil, "")
		if options.Identity == nil || options.Identity.Attribution == nil || options.Identity.Attribution.Attribution != nil {
			t.Fatalf("attribution = %+v, want explicit null", options.Identity)
		}
	})

	t.Run("adds OpenCode session headers for custom OpenCode providers", func(t *testing.T) {
		options := captureIdentityOptions(t, identityModel("custom-opencode", "https://opencode.ai/zen/v1"), nil, nil, "opencode-session")
		if value, _ := headersGet(options.Headers, "x-opencode-session"); value != "opencode-session" {
			t.Fatalf("x-opencode-session = %q", value)
		}
	})

	t.Run("lets configured OpenCode headers override the session header", func(t *testing.T) {
		options := captureIdentityOptions(t, identityModel("opencode", "https://opencode.ai/zen/v1"), nil,
			map[string]string{"x-opencode-session": "configured-session"}, "opencode-session")
		if value, _ := headersGet(options.Headers, "x-opencode-session"); value != "configured-session" {
			t.Fatalf("x-opencode-session = %q", value)
		}
	})
}
