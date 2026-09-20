// pi: test/anthropic-thinking-disable.test.ts.
package api

import (
	"context"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/samber/lo"

	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/ai/utils"
)

type anthropicThinkingPayload struct {
	thinking     map[string]any
	outputConfig map[string]any
}

func thinkingPayloadFrom(t *testing.T, payload map[string]any) anthropicThinkingPayload {
	t.Helper()
	thinking, _ := payload["thinking"].(map[string]any)
	outputConfig, _ := payload["output_config"].(map[string]any)
	return anthropicThinkingPayload{thinking: thinking, outputConfig: outputConfig}
}

func captureThinkingPayload(t *testing.T, model *types.Model, options *types.SimpleStreamOptions) anthropicThinkingPayload {
	t.Helper()
	model.BaseURL = unreachableAnthropicBaseURL
	return thinkingPayloadFrom(t, captureSimplePayload(t, model, thinkingDisableContext(), options))
}

func thinkingDisableContext() *types.TranscriptContext {
	return utils.NormalizeContext(&types.Context{
		Messages: []types.Message{types.UserMessage{Role: types.RoleUser, Content: types.UserContent{String: "Hello"}, Timestamp: 1}},
	})
}

func TestAnthropicThinkingDisablePayload(t *testing.T) {
	cases := []struct {
		name         string
		model        *types.Model
		reasoning    types.ThinkingLevel
		wantThinking map[string]any
		wantOutput   bool
	}{
		{
			name:         "sends thinking.type=disabled for budget-based reasoning models when thinking is off",
			model:        catalogAnthropicModel(t, types.ProviderAnthropic, "claude-sonnet-4-5"),
			wantThinking: map[string]any{"type": "disabled"},
		},
		{
			name:         "sends thinking.type=disabled for adaptive reasoning models when thinking is off",
			model:        catalogAnthropicModel(t, types.ProviderAnthropic, "claude-opus-4-6"),
			wantThinking: map[string]any{"type": "disabled"},
		},
		{
			name:         "sends thinking.type=disabled for Claude Opus 4.8 when thinking is off",
			model:        catalogAnthropicModel(t, types.ProviderAnthropic, "claude-opus-4-8"),
			wantThinking: map[string]any{"type": "disabled"},
		},
		{
			name:  "omits thinking.type=disabled for Claude Fable 5 when thinking is off",
			model: catalogAnthropicModel(t, types.ProviderAnthropic, "claude-fable-5"),
		},
		{
			name:         "uses adaptive thinking for Claude Opus 4.8 when reasoning is enabled",
			model:        catalogAnthropicModel(t, types.ProviderAnthropic, "claude-opus-4-8"),
			reasoning:    types.ThinkingLevelHigh,
			wantThinking: map[string]any{"type": "adaptive", "display": "summarized"},
			wantOutput:   true,
		},
		{
			name:         "uses adaptive thinking for Claude Sonnet 5 when reasoning is enabled",
			model:        catalogAnthropicModel(t, types.ProviderAnthropic, "claude-sonnet-5"),
			reasoning:    types.ThinkingLevelHigh,
			wantThinking: map[string]any{"type": "adaptive", "display": "summarized"},
			wantOutput:   true,
		},
		{
			name:         "maps xhigh reasoning to effort=xhigh for Claude Opus 4.8",
			model:        catalogAnthropicModel(t, types.ProviderAnthropic, "claude-opus-4-8"),
			reasoning:    types.ThinkingLevelXHigh,
			wantThinking: map[string]any{"type": "adaptive", "display": "summarized"},
			wantOutput:   true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			options := &types.SimpleStreamOptions{}
			options.Reasoning = tc.reasoning
			payload := captureThinkingPayload(t, tc.model, options)
			assertThinkingEqual(t, tc.wantThinking, payload.thinking)
			if tc.wantOutput {
				if !stringMapEqual(payload.outputConfig, map[string]any{"effort": "high"}) &&
					!stringMapEqual(payload.outputConfig, map[string]any{"effort": "xhigh"}) {
					t.Errorf("output_config = %#v, want effort high or xhigh", payload.outputConfig)
				}
			} else if payload.outputConfig != nil {
				t.Errorf("output_config = %#v, want it omitted", payload.outputConfig)
			}
		})
	}
}

// assertThinkingEqual compares the thinking param with expected key/value
// pairs; a nil expectation means the field must be absent.
func assertThinkingEqual(t *testing.T, want map[string]any, got map[string]any) {
	t.Helper()
	if want == nil {
		if got != nil {
			t.Errorf("thinking = %#v, want it omitted", got)
		}
		return
	}
	if got == nil {
		t.Fatalf("thinking = nil, want %#v", want)
	}
	for key, value := range want {
		if got[key] != value {
			t.Errorf("thinking.%s = %#v, want %#v (full: %#v)", key, got[key], value, got)
		}
	}
}

func stringMapEqual(got map[string]any, want map[string]any) bool {
	if got == nil || want == nil {
		return got == nil && want == nil
	}
	if len(got) != len(want) {
		return false
	}
	// reflect.DeepEqual: values include uncomparable slices (content: []).
	for key, value := range want {
		if !reflect.DeepEqual(got[key], value) {
			return false
		}
	}
	return true
}

// TestAnthropicThinkingDisableE2E mirrors pi's ANTHROPIC_API_KEY-gated e2e;
// it runs only with a real key (pig's smoke runs use the DeepSeek endpoint
// instead).
func TestAnthropicThinkingDisableE2E(t *testing.T) {
	if anthropicSmokeAPIKey() == "" {
		t.Skip("ANTHROPIC_API_KEY not set")
	}
	model := catalogAnthropicModel(t, types.ProviderAnthropic, "claude-sonnet-4-5")
	context := utils.NormalizeContext(&types.Context{
		SystemPrompt: "You are a precise assistant. Follow the requested output format exactly.",
		Messages: []types.Message{types.UserMessage{
			Role:      types.RoleUser,
			Content:   types.UserContent{String: "Before replying, carefully solve 36863 * 5279 internally. Then reply with the word pong repeated exactly 40 times, separated by single spaces. Do not add any other text."},
			Timestamp: 1,
		}},
	})
	options := &types.SimpleStreamOptions{}
	options.Temperature = lo.ToPtr(0.0)
	options.MaxTokens = lo.ToPtr(int64(160))
	options.APIKey = anthropicSmokeAPIKey()

	stream := (&AnthropicMessages{}).StreamSimple(backgroundCtx(), model, context, options)
	thinkingEventCount := 0
	for event := range stream.Events() {
		if event.Type == types.EventThinkingStart || event.Type == types.EventThinkingEnd || event.Type == types.EventThinkingDelta {
			thinkingEventCount++
		}
	}
	result := stream.Result()
	if result.StopReason != types.StopReasonStop {
		t.Fatalf("stopReason = %q (%s)", result.StopReason, result.ErrorMessage)
	}
	if thinkingEventCount != 0 {
		t.Errorf("thinkingEventCount = %d, want 0", thinkingEventCount)
	}
	for _, block := range result.Content {
		if block.ContentKind() == types.KindThinking {
			t.Errorf("content contains a thinking block: %#v", block)
		}
	}
	pong := regexp.MustCompile(`(?i)\bpong\b`)
	if count := len(pong.FindAllString(assistantText(&result), -1)); count < 35 {
		t.Errorf("pong count = %d, want >= 35", count)
	}
}

func assistantText(message *types.AssistantMessage) string {
	var b strings.Builder
	for _, block := range message.Content {
		if text, ok := block.(types.TextContent); ok {
			b.WriteString(text.Text)
		}
	}
	return strings.TrimSpace(b.String())
}

func anthropicSmokeAPIKey() string {
	return os.Getenv("ANTHROPIC_API_KEY")
}

func backgroundCtx() context.Context { return context.Background() }
