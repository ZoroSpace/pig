// Ported from pi's test/sdk-openrouter-attribution.test.ts. The cloudflare /
// codex cases ride the same capture helper as sdk-stream-options (pi's
// openai-codex-responses cases are covered by the completions adapter —
// Phase 2 decision).
package core

import (
	"testing"

	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/types"
)

func attributionModel(provider string, baseURL string, id string) *types.Model {
	if id == "" {
		id = provider + "-test-model"
	}
	return &types.Model{
		ID:            id,
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

func captureAttributionHeaders(t *testing.T, model *types.Model, telemetryEnabled *bool, providerHeaders map[string]string, requestHeaders map[string]string, sessionID string) map[string]*string {
	t.Helper()
	settings := Settings{}
	if telemetryEnabled != nil {
		settings["enableInstallTelemetry"] = *telemetryEnabled
	}
	options := &types.SimpleStreamOptions{StreamOptions: types.StreamOptions{SessionID: sessionID}}
	if requestHeaders != nil {
		options.ProviderRequestOptions.Headers = make(types.ProviderHeaders, len(requestHeaders))
		for key, value := range requestHeaders {
			options.ProviderRequestOptions.Headers[key] = &value
		}
	}
	captured, cleanup := sdkCaptureStreamOptionsWithModel(t, model, settings, options, providerHeaders, requestHeaders, sessionID)
	defer cleanup()
	if captured == nil {
		t.Fatal("stream options were not captured")
	}
	return captured.Headers
}

func headersGet(headers map[string]*string, key string) (string, bool) {
	if headers == nil {
		return "", false
	}
	value, ok := headers[key]
	if !ok || value == nil {
		return "", false
	}
	return *value, true
}

func TestCreateAgentSessionProviderAttributionHeaders(t *testing.T) {
	t.Run("adds default attribution headers for OpenRouter models", func(t *testing.T) {
		headers := captureAttributionHeaders(t, attributionModel("openrouter", "https://openrouter.ai/api/v1", ""), nil, nil, nil, "")
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

	t.Run("does not add attribution headers when telemetry is disabled", func(t *testing.T) {
		disabled := false
		headers := captureAttributionHeaders(t, attributionModel("openrouter", "https://openrouter.ai/api/v1", ""), &disabled, nil, nil, "")
		if _, ok := headersGet(headers, "HTTP-Referer"); ok {
			t.Fatal("HTTP-Referer should be absent")
		}
		if _, ok := headersGet(headers, "X-OpenRouter-Title"); ok {
			t.Fatal("X-OpenRouter-Title should be absent")
		}
		if _, ok := headersGet(headers, "X-OpenRouter-Categories"); ok {
			t.Fatal("X-OpenRouter-Categories should be absent")
		}
	})

	t.Run("adds attribution headers for custom providers routed through OpenRouter", func(t *testing.T) {
		headers := captureAttributionHeaders(t, attributionModel("custom-openrouter", "https://openrouter.ai/api/v1", ""), nil, nil, nil, "")
		if value, _ := headersGet(headers, "HTTP-Referer"); value != "https://pi.dev" {
			t.Fatalf("HTTP-Referer = %q", value)
		}
	})

	t.Run("preserves legacy OpenRouter base URL substring attribution matching", func(t *testing.T) {
		headers := captureAttributionHeaders(t, attributionModel("custom-openrouter", "not-a-url-openrouter.ai", ""), nil, nil, nil, "")
		if value, _ := headersGet(headers, "HTTP-Referer"); value != "https://pi.dev" {
			t.Fatalf("HTTP-Referer = %q", value)
		}
	})

	t.Run("lets provider and request headers override the defaults", func(t *testing.T) {
		headers := captureAttributionHeaders(t,
			attributionModel("openrouter", "https://openrouter.ai/api/v1", ""),
			nil,
			map[string]string{
				"HTTP-Referer":            "https://provider.example",
				"X-OpenRouter-Categories": "provider-category",
			},
			map[string]string{
				"X-OpenRouter-Title": "request-title",
			},
			"")
		if value, _ := headersGet(headers, "HTTP-Referer"); value != "https://provider.example" {
			t.Fatalf("HTTP-Referer = %q", value)
		}
		if value, _ := headersGet(headers, "X-OpenRouter-Title"); value != "request-title" {
			t.Fatalf("X-OpenRouter-Title = %q", value)
		}
		if value, _ := headersGet(headers, "X-OpenRouter-Categories"); value != "provider-category" {
			t.Fatalf("X-OpenRouter-Categories = %q", value)
		}
	})

	t.Run("adds default attribution headers for direct NVIDIA NIM endpoints", func(t *testing.T) {
		headers := captureAttributionHeaders(t, attributionModel("custom-nim", "https://integrate.api.nvidia.com/v1", ""), nil, nil, nil, "")
		if value, _ := headersGet(headers, "X-BILLING-INVOKE-ORIGIN"); value != "Pi" {
			t.Fatalf("X-BILLING-INVOKE-ORIGIN = %q", value)
		}
	})

	t.Run("adds default attribution headers for the NVIDIA provider", func(t *testing.T) {
		headers := captureAttributionHeaders(t, attributionModel("nvidia", "https://example.test/v1", ""), nil, nil, nil, "")
		if value, _ := headersGet(headers, "X-BILLING-INVOKE-ORIGIN"); value != "Pi" {
			t.Fatalf("X-BILLING-INVOKE-ORIGIN = %q", value)
		}
	})

	t.Run("does not add NVIDIA NIM attribution headers when telemetry is disabled", func(t *testing.T) {
		disabled := false
		headers := captureAttributionHeaders(t, attributionModel("nvidia", "https://integrate.api.nvidia.com/v1", ""), &disabled, nil, nil, "")
		if _, ok := headersGet(headers, "X-BILLING-INVOKE-ORIGIN"); ok {
			t.Fatal("X-BILLING-INVOKE-ORIGIN should be absent")
		}
	})

	t.Run("lets provider and request headers override NVIDIA NIM defaults", func(t *testing.T) {
		headers := captureAttributionHeaders(t,
			attributionModel("nvidia", "https://integrate.api.nvidia.com/v1", ""),
			nil,
			map[string]string{"X-BILLING-INVOKE-ORIGIN": "Provider"},
			map[string]string{"X-BILLING-INVOKE-ORIGIN": "Request"},
			"")
		if value, _ := headersGet(headers, "X-BILLING-INVOKE-ORIGIN"); value != "Request" {
			t.Fatalf("X-BILLING-INVOKE-ORIGIN = %q", value)
		}
	})

	t.Run("does not add NVIDIA NIM attribution headers for NVIDIA models routed through OpenRouter", func(t *testing.T) {
		headers := captureAttributionHeaders(t,
			attributionModel("openrouter", "https://openrouter.ai/api/v1", "nvidia/nemotron-3-super-120b-a12b"),
			nil, nil, nil, "")
		if value, _ := headersGet(headers, "HTTP-Referer"); value != "https://pi.dev" {
			t.Fatalf("HTTP-Referer = %q", value)
		}
		if _, ok := headersGet(headers, "X-BILLING-INVOKE-ORIGIN"); ok {
			t.Fatal("X-BILLING-INVOKE-ORIGIN should be absent")
		}
	})

	t.Run("does not add NVIDIA NIM attribution headers for NVIDIA models routed through Vercel AI Gateway", func(t *testing.T) {
		headers := captureAttributionHeaders(t,
			attributionModel("vercel-ai-gateway", "https://ai-gateway.vercel.sh/v1", "nvidia/nemotron-3-super-120b-a12b"),
			nil, nil, nil, "")
		if _, ok := headersGet(headers, "X-BILLING-INVOKE-ORIGIN"); ok {
			t.Fatal("X-BILLING-INVOKE-ORIGIN should be absent")
		}
	})

	t.Run("adds OpenCode session headers", func(t *testing.T) {
		headers := captureAttributionHeaders(t, attributionModel("opencode", "https://opencode.ai/zen/v1", ""), nil, nil, nil, "opencode-session")
		if value, _ := headersGet(headers, "x-opencode-session"); value != "opencode-session" {
			t.Fatalf("x-opencode-session = %q", value)
		}
		if value, _ := headersGet(headers, "x-opencode-client"); value != "pi" {
			t.Fatalf("x-opencode-client = %q", value)
		}
	})

	t.Run("lets configured OpenCode headers override the defaults", func(t *testing.T) {
		headers := captureAttributionHeaders(t,
			attributionModel("opencode", "https://opencode.ai/zen/v1", ""),
			nil,
			map[string]string{
				"x-opencode-session": "configured-session",
				"x-opencode-client":  "configured-client",
			},
			nil,
			"opencode-session")
		if value, _ := headersGet(headers, "x-opencode-session"); value != "configured-session" {
			t.Fatalf("x-opencode-session = %q", value)
		}
		if value, _ := headersGet(headers, "x-opencode-client"); value != "configured-client" {
			t.Fatalf("x-opencode-client = %q", value)
		}
	})
}
