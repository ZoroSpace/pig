// DeepSeek Anthropic-compat endpoint smoke (pi: none — pig-only verification
// channel, see docs/handoffs/PLAN-ANTHROPIC-RESPONSES.md §0.1). Real API,
// deepseek-flash, small max_tokens. Run with:
//
//	DEEPSEEK_API_KEY=sk-... go test -tags=smoke ./ai/api/ -run TestAnthropicDeepSeekSmoke -v
//
// The key is read from the environment and never written to disk.
//go:build smoke

package api

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/samber/lo"

	"github.com/ZoroSpace/pig/ai/types"
)

func deepseekAnthropicSmokeModel(t *testing.T) *types.Model {
	t.Helper()
	if key := os.Getenv("DEEPSEEK_API_KEY"); key == "" || key == "xxx" {
		t.Skip("DEEPSEEK_API_KEY not set")
	}
	return &types.Model{
		ID:            "deepseek-flash",
		Name:          "DeepSeek Flash",
		API:           types.ApiAnthropicMessages,
		Provider:      types.ProviderID("deepseek-anthropic"),
		BaseURL:       "https://api.deepseek.com/anthropic",
		Reasoning:     true,
		Input:         []types.ModelInput{types.ModelInputText, types.ModelInputImage},
		Cost:          types.ModelCost{},
		ContextWindow: 128000,
		MaxTokens:     8192,
	}
}

func deepseekSmokeOptions(apiKey string, maxTokens int64) *types.StreamOptions {
	options := &types.StreamOptions{}
	options.APIKey = apiKey
	options.MaxTokens = lo.ToPtr(maxTokens)
	return options
}

func TestAnthropicDeepSeekSmokeText(t *testing.T) {
	model := deepseekAnthropicSmokeModel(t)
	options := deepseekSmokeOptions(os.Getenv("DEEPSEEK_API_KEY"), 64)
	stream := (&AnthropicMessages{}).Stream(context.Background(), model, &types.Context{
		Messages: []types.Message{types.UserMessage{
			Role: types.RoleUser, Content: types.UserContent{String: "Reply with exactly one word: pong"}, Timestamp: 1,
		}},
	}, options)
	result := stream.Result()
	t.Logf("stopReason=%s usage=%+v err=%s", result.StopReason, result.Usage, result.ErrorMessage)
	if result.StopReason != types.StopReasonStop {
		t.Fatalf("stopReason = %q (%s)", result.StopReason, result.ErrorMessage)
	}
	if !strings.Contains(assistantText(&result), "pong") {
		t.Errorf("text = %q, want it to contain pong", assistantText(&result))
	}
	if result.Usage.Input <= 0 || result.Usage.Output <= 0 {
		t.Errorf("usage = %+v, want positive counts", result.Usage)
	}
	if result.ResponseID == "" {
		t.Errorf("responseId = empty")
	}
}

func TestAnthropicDeepSeekSmokeThinking(t *testing.T) {
	model := deepseekAnthropicSmokeModel(t)
	simple := &types.SimpleStreamOptions{}
	simple.APIKey = os.Getenv("DEEPSEEK_API_KEY")
	simple.MaxTokens = lo.ToPtr(int64(384))
	simple.Reasoning = types.ThinkingLevelLow
	stream := (&AnthropicMessages{}).StreamSimple(context.Background(), model, &types.Context{
		Messages: []types.Message{types.UserMessage{
			Role: types.RoleUser, Content: types.UserContent{String: "What is 13*17? Think briefly, then answer with just the number."}, Timestamp: 1,
		}},
	}, simple)
	sawThinking := false
	for event := range stream.Events() {
		if event.Type == types.EventThinkingStart || event.Type == types.EventThinkingDelta {
			sawThinking = true
		}
	}
	result := stream.Result()
	t.Logf("stopReason=%s usage=%+v err=%s", result.StopReason, result.Usage, result.ErrorMessage)
	if result.StopReason != types.StopReasonStop {
		t.Fatalf("stopReason = %q (%s)", result.StopReason, result.ErrorMessage)
	}
	if !sawThinking {
		t.Error("expected thinking events")
	}
	if !strings.Contains(assistantText(&result), "221") {
		t.Errorf("text = %q, want 221", assistantText(&result))
	}
}

func TestAnthropicDeepSeekSmokeToolCall(t *testing.T) {
	model := deepseekAnthropicSmokeModel(t)
	llmContext := &types.Context{
		Messages: []types.Message{types.UserMessage{
			Role: types.RoleUser, Content: types.UserContent{String: "Call echo_value with value set to hello."}, Timestamp: 1,
		}},
		Tools: []types.Tool{{
			Name:        "echo_value",
			Description: "Echo a string value",
			Parameters:  mustJSONRaw(`{"type":"object","properties":{"value":{"type":"string","description":"The value to echo"}},"required":["value"]}`),
		}},
	}
	options := deepseekSmokeOptions(os.Getenv("DEEPSEEK_API_KEY"), 256)
	stream := (&AnthropicMessages{}).Stream(context.Background(), model, llmContext, options)
	result := stream.Result()
	t.Logf("stopReason=%s usage=%+v err=%s", result.StopReason, result.Usage, result.ErrorMessage)
	if result.StopReason != types.StopReasonToolUse {
		t.Fatalf("stopReason = %q (%s)", result.StopReason, result.ErrorMessage)
	}
	calls := result.ToolCalls()
	if len(calls) != 1 || calls[0].Name != "echo_value" {
		t.Fatalf("toolCalls = %+v, want one echo_value", calls)
	}
	if calls[0].Arguments["value"] != "hello" {
		t.Errorf("arguments = %+v, want value hello", calls[0].Arguments)
	}
}

func mustJSONRaw(s string) []byte {
	return []byte(s)
}
