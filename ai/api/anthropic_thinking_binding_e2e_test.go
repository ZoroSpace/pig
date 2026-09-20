// pi: test/anthropic-thinking-binding-e2e.test.ts. Runs only with
// ANTHROPIC_API_KEY.
package api

import (
	gocontext "context"
	"testing"

	"github.com/samber/lo"

	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/ai/utils"
)

func TestAnthropicThinkingBindingE2E(t *testing.T) {
	if anthropicSmokeAPIKey() == "" {
		t.Skip("ANTHROPIC_API_KEY not set")
	}
	model := anthropicTestModel("claude-fable-5-1", types.ProviderAnthropic, &types.AnthropicMessagesCompat{
		ForceAdaptiveThinking:  lo.ToPtr(true),
		SupportsMidConvoEffort: lo.ToPtr(true),
	})
	request := func(t *testing.T, llmContext *types.TranscriptContext, effort AnthropicEffort) types.AssistantMessage {
		t.Helper()
		options := &AnthropicOptions{}
		options.APIKey = anthropicSmokeAPIKey()
		options.CacheRetention = types.CacheRetentionNone
		options.MaxTokens = lo.ToPtr(int64(1536))
		options.ThinkingEnabled = lo.ToPtr(true)
		options.ThinkingDisplay = AnthropicThinkingDisplaySummarized
		options.Effort = effort
		options.OnPayload = func(payload any, model *types.Model) any {
			// pi's strictBinding: force prefix_mismatch_behavior error.
			if params, ok := payload.(map[string]any); ok {
				if thinking, ok := params["thinking"].(map[string]any); ok {
					if binding, ok := thinking["block_binding"].(map[string]any); ok {
						binding["prefix_mismatch_behavior"] = "error"
					}
				}
			}
			return nil
		}
		return streamAnthropicMessages(gocontext.Background(), model, llmContext, options).Result()
	}
	first := request(t, utils.NormalizeContext(&types.Context{Messages: []types.Message{midConvoUser("one", 1)}}), AnthropicEffortLow)
	if first.StopReason != types.StopReasonStop {
		t.Fatalf("first stopReason = %q (%s)", first.StopReason, first.ErrorMessage)
	}
	second := request(t, utils.NormalizeContext(&types.Context{Messages: []types.Message{
		midConvoUser("one", 1),
		midConvoAssistant(t, model, "low"),
		midConvoUser("two", 2),
	}}), AnthropicEffortHigh)
	if second.StopReason != types.StopReasonStop {
		t.Fatalf("second stopReason = %q (%s)", second.StopReason, second.ErrorMessage)
	}
}
