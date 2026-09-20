// pi: test/anthropic-cache-write-1h-cost.test.ts.
package api

import (
	gocontext "context"
	"testing"

	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/ai/utils"
)

// cacheWrite1hEvents builds a stream whose usage carries a cache_creation
// breakdown (pi: eventsWithCacheCreation).
func cacheWrite1hEvents(cacheCreation map[string]any) []anthropicTestEvent {
	startUsage := map[string]any{
		"input_tokens":                100,
		"output_tokens":               0,
		"cache_read_input_tokens":     0,
		"cache_creation_input_tokens": 1000000,
	}
	if cacheCreation != nil {
		startUsage["cache_creation"] = cacheCreation
	}
	return []anthropicTestEvent{
		{event: "message_start", data: mustJSON(map[string]any{"type": "message_start", "message": map[string]any{"id": "msg_test", "usage": startUsage}})},
		{event: "content_block_start", data: mustJSON(map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": ""}})},
		{event: "content_block_delta", data: mustJSON(map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": "Hi"}})},
		{event: "content_block_stop", data: mustJSON(map[string]any{"type": "content_block_stop", "index": 0})},
		{event: "message_delta", data: mustJSON(map[string]any{
			"type":  "message_delta",
			"delta": map[string]any{"stop_reason": "end_turn"},
			"usage": map[string]any{"input_tokens": 100, "output_tokens": 5, "cache_read_input_tokens": 0, "cache_creation_input_tokens": 1000000},
		})},
		{event: "message_stop", data: mustJSON(map[string]any{"type": "message_stop"})},
	}
}

func TestAnthropicCacheWrite1hCost(t *testing.T) {
	// claude-opus-4-8: input 5, cacheWrite (5m) 6.25 per Mtok. 1h write =
	// 2x input = 10.
	model := catalogAnthropicModel(t, types.ProviderAnthropic, "claude-opus-4-8")
	llmContext := utils.NormalizeContext(&types.Context{Messages: []types.Message{types.UserMessage{Role: types.RoleUser, Content: types.UserContent{String: "hi"}, Timestamp: 1}}})

	t.Run("prices the 1h portion at 2x input and the rest at the 5m rate", func(t *testing.T) {
		options := anthropicTestOptions(cacheWrite1hEvents(map[string]any{"ephemeral_5m_input_tokens": 600000, "ephemeral_1h_input_tokens": 400000}), nil)
		result := streamAnthropicMessages(gocontext.Background(), model, llmContext, options).Result()
		if result.Usage.CacheWrite != 1000000 {
			t.Errorf("cacheWrite = %d", result.Usage.CacheWrite)
		}
		if result.Usage.CacheWrite1h == nil || *result.Usage.CacheWrite1h != 400000 {
			t.Errorf("cacheWrite1h = %#v, want 400000", result.Usage.CacheWrite1h)
		}
		// 600k * 6.25/Mtok + 400k * 10/Mtok = 3.75 + 4.0 = 7.75
		if diff := result.Usage.Cost.CacheWrite - 7.75; diff > 1e-9 || diff < -1e-9 {
			t.Errorf("cost.cacheWrite = %v, want 7.75", result.Usage.Cost.CacheWrite)
		}
	})

	t.Run("falls back to the 5m rate when no breakdown is reported", func(t *testing.T) {
		options := anthropicTestOptions(cacheWrite1hEvents(nil), nil)
		result := streamAnthropicMessages(gocontext.Background(), model, llmContext, options).Result()
		if result.Usage.CacheWrite != 1000000 {
			t.Errorf("cacheWrite = %d", result.Usage.CacheWrite)
		}
		if result.Usage.CacheWrite1h != nil && *result.Usage.CacheWrite1h != 0 {
			t.Errorf("cacheWrite1h = %#v, want 0", result.Usage.CacheWrite1h)
		}
		// 1M * 6.25/Mtok = 6.25
		if diff := result.Usage.Cost.CacheWrite - 6.25; diff > 1e-9 || diff < -1e-9 {
			t.Errorf("cost.cacheWrite = %v, want 6.25", result.Usage.Cost.CacheWrite)
		}
	})
}
