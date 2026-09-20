// pi: test/anthropic-temperature-compat.test.ts.
package api

import (
	"testing"

	"github.com/samber/lo"

	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/ai/utils"
)

func temperatureTestModel(compat *types.AnthropicMessagesCompat) *types.Model {
	model := anthropicTestModel("vendor--claude-opus-4-7", types.ProviderID("vendor-proxy"), compat)
	model.Name = "Vendor Proxy Opus 4.7"
	model.BaseURL = unreachableAnthropicBaseURL
	return model
}

// catalogAnthropicModel reads one generated catalog entry (pi: getModel;
// ai/providers cannot be imported here — it imports this package for the
// builtin registration).
func catalogAnthropicModel(t *testing.T, provider types.ProviderID, id string) *types.Model {
	t.Helper()
	models, ok := ai.MODELS[provider]
	if !ok {
		t.Fatalf("no %s catalog", provider)
	}
	model, ok := models[id]
	if !ok {
		t.Fatalf("catalog model %s/%s not found", provider, id)
	}
	return &model
}

func TestAnthropicTemperatureOmittedForUnsupportedModels(t *testing.T) {
	cases := []struct {
		name  string
		model *types.Model
	}{
		{"opus-4-7", catalogAnthropicModel(t, types.ProviderAnthropic, "claude-opus-4-7")},
		{"opus-4-8", catalogAnthropicModel(t, types.ProviderAnthropic, "claude-opus-4-8")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.model.BaseURL = unreachableAnthropicBaseURL
			payload := captureSimplePayload(t, tc.model, temperatureTestContext(), temperatureOptions(0.0))
			if _, present := payload["temperature"]; present {
				t.Errorf("temperature = %#v, want it omitted", payload["temperature"])
			}
		})
	}
}

func TestAnthropicTemperatureOmittedForOpus47DefaultTemperature(t *testing.T) {
	model := catalogAnthropicModel(t, types.ProviderAnthropic, "claude-opus-4-7")
	model.BaseURL = unreachableAnthropicBaseURL
	payload := captureSimplePayload(t, model, temperatureTestContext(), temperatureOptions(1.0))
	if _, present := payload["temperature"]; present {
		t.Errorf("temperature = %#v, want it omitted", payload["temperature"])
	}
}

func TestAnthropicTemperatureKeptForOlderModels(t *testing.T) {
	cases := []struct {
		name  string
		model *types.Model
	}{
		{"opus-4-6", catalogAnthropicModel(t, types.ProviderAnthropic, "claude-opus-4-6")},
		{"sonnet-4-6", catalogAnthropicModel(t, types.ProviderAnthropic, "claude-sonnet-4-6")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.model.BaseURL = unreachableAnthropicBaseURL
			payload := captureSimplePayload(t, tc.model, temperatureTestContext(), temperatureOptions(0.0))
			if got, ok := payload["temperature"].(float64); !ok || got != 0 {
				t.Errorf("temperature = %#v, want 0", payload["temperature"])
			}
		})
	}
}

func TestAnthropicTemperatureOmittedForCustomModelsWithCompatDisabled(t *testing.T) {
	model := temperatureTestModel(&types.AnthropicMessagesCompat{SupportsTemperature: lo.ToPtr(false)})
	payload := captureSimplePayload(t, model, temperatureTestContext(), temperatureOptions(0.0))
	if _, present := payload["temperature"]; present {
		t.Errorf("temperature = %#v, want it omitted", payload["temperature"])
	}
}

func temperatureTestContext() *types.TranscriptContext {
	return utils.NormalizeContext(&types.Context{
		Messages: []types.Message{types.UserMessage{Role: types.RoleUser, Content: types.UserContent{String: "Hello"}, Timestamp: 1}},
	})
}

// temperatureOptions builds simple-stream options with a temperature (the
// field lives on the embedded StreamOptions).
func temperatureOptions(value float64) *types.SimpleStreamOptions {
	options := &types.SimpleStreamOptions{}
	options.Temperature = lo.ToPtr(value)
	return options
}
