// pi: test/anthropic-opus-4-8-smoke.test.ts. Runs only with ANTHROPIC_API_KEY.
package api

import (
	gocontext "context"
	"strings"
	"testing"

	"github.com/samber/lo"

	"github.com/ZoroSpace/pig/ai/types"
)

func TestAnthropicOpus48Smoke(t *testing.T) {
	if anthropicSmokeAPIKey() == "" {
		t.Skip("ANTHROPIC_API_KEY not set")
	}
	model := catalogAnthropicModel(t, types.ProviderAnthropic, "claude-opus-4-8")
	options := &types.SimpleStreamOptions{}
	options.APIKey = anthropicSmokeAPIKey()
	options.Reasoning = types.ThinkingLevelHigh
	options.MaxTokens = lo.ToPtr(int64(1024))
	var capturedPayload map[string]any
	options.OnPayload = func(payload any, model *types.Model) any {
		if m, ok := payload.(map[string]any); ok {
			capturedPayload = m
		}
		return nil
	}
	stream := (&AnthropicMessages{}).StreamSimple(gocontext.Background(), model, &types.Context{
		SystemPrompt: "You are a precise assistant. Follow the user's instructions exactly.",
		Messages: []types.Message{types.UserMessage{
			Role:      types.RoleUser,
			Content:   types.UserContent{String: "Compute 48291 * 7317 and 90844 - 17729, add the results, and determine whether the sum is divisible by 11. Reply with exactly this format and nothing else: sum=<sum>; divisibleBy11=<yes|no>"},
			Timestamp: 1,
		}},
	}, options)
	sawThinking := false
	for event := range stream.Events() {
		if event.Type == types.EventThinkingStart || event.Type == types.EventThinkingDelta || event.Type == types.EventThinkingEnd {
			sawThinking = true
		}
	}
	result := stream.Result()
	if result.StopReason != types.StopReasonStop {
		t.Fatalf("stopReason = %q (%s)", result.StopReason, result.ErrorMessage)
	}
	if !sawThinking {
		t.Error("expected thinking events")
	}
	thinking := mapOf(t, capturedPayload["thinking"])
	if !stringMapEqual(thinking, map[string]any{"type": "adaptive"}) {
		t.Errorf("thinking = %#v, want adaptive", thinking)
	}
	if !stringMapEqual(mapOf(t, capturedPayload["output_config"]), map[string]any{"effort": "high"}) {
		t.Errorf("output_config = %#v", capturedPayload["output_config"])
	}
	if text := assistantText(&result); !strings.Contains(text, "sum=353418362; divisibleBy11=yes") {
		t.Errorf("text = %q", text)
	}
}
