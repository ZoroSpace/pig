// pi: test/anthropic-force-adaptive-thinking.test.ts.
package api

import (
	"testing"

	"github.com/samber/lo"

	"github.com/ZoroSpace/pig/ai/types"
)

func TestAnthropicForceAdaptiveThinkingPayload(t *testing.T) {
	makeCustom := func(compat *types.AnthropicMessagesCompat) *types.Model {
		model := anthropicTestModel("vendor--claude-opus-latest", types.ProviderID("vendor-proxy"), compat)
		model.Name = "Vendor Proxy Opus Latest"
		model.BaseURL = unreachableAnthropicBaseURL
		return model
	}
	capture := func(model *types.Model, reasoning types.ThinkingLevel) anthropicThinkingPayload {
		options := &types.SimpleStreamOptions{}
		options.Reasoning = reasoning
		return thinkingPayloadFrom(t, captureSimplePayload(t, model, thinkingDisableContext(), options))
	}

	t.Run("sends legacy thinking payload for custom model ids by default", func(t *testing.T) {
		payload := capture(makeCustom(nil), types.ThinkingLevelMedium)
		if payload.thinking["type"] != "enabled" {
			t.Errorf("thinking = %#v, want enabled", payload.thinking)
		}
		if payload.outputConfig != nil {
			t.Errorf("output_config = %#v, want it omitted", payload.outputConfig)
		}
	})

	t.Run("sends adaptive thinking payload when compat.forceAdaptiveThinking is true", func(t *testing.T) {
		payload := capture(makeCustom(&types.AnthropicMessagesCompat{ForceAdaptiveThinking: lo.ToPtr(true)}), types.ThinkingLevelMedium)
		if !stringMapEqual(payload.thinking, map[string]any{"type": "adaptive", "display": "summarized"}) {
			t.Errorf("thinking = %#v", payload.thinking)
		}
		if !stringMapEqual(payload.outputConfig, map[string]any{"effort": "medium"}) {
			t.Errorf("output_config = %#v", payload.outputConfig)
		}
	})

	t.Run("uses adaptive thinking with native xhigh effort for Claude Fable 5", func(t *testing.T) {
		model := catalogAnthropicModel(t, types.ProviderAnthropic, "claude-fable-5")
		payload := capture(model, types.ThinkingLevelXHigh)
		if !stringMapEqual(payload.thinking, map[string]any{"type": "adaptive", "display": "summarized"}) {
			t.Errorf("thinking = %#v", payload.thinking)
		}
		if !stringMapEqual(payload.outputConfig, map[string]any{"effort": "xhigh"}) {
			t.Errorf("output_config = %#v", payload.outputConfig)
		}
	})

	t.Run("allows built-in adaptive models to opt out with forceAdaptiveThinking false", func(t *testing.T) {
		model := catalogAnthropicModel(t, types.ProviderAnthropic, "claude-opus-4-8")
		model.Compat = &types.AnthropicMessagesCompat{ForceAdaptiveThinking: lo.ToPtr(false)}
		payload := capture(model, types.ThinkingLevelMedium)
		if payload.thinking["type"] != "enabled" {
			t.Errorf("thinking = %#v, want enabled", payload.thinking)
		}
		if payload.outputConfig != nil {
			t.Errorf("output_config = %#v, want it omitted", payload.outputConfig)
		}
	})

	t.Run("preserves thinking.type=disabled when reasoning is off regardless of override", func(t *testing.T) {
		payload := capture(makeCustom(&types.AnthropicMessagesCompat{ForceAdaptiveThinking: lo.ToPtr(true)}), "")
		if !stringMapEqual(payload.thinking, map[string]any{"type": "disabled"}) {
			t.Errorf("thinking = %#v, want disabled", payload.thinking)
		}
		if payload.outputConfig != nil {
			t.Errorf("output_config = %#v, want it omitted", payload.outputConfig)
		}
	})
}
