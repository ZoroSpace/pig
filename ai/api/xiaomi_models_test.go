// pi: test/xiaomi-models.test.ts — MiMo Chat Completions only supports a
// thinking on/off toggle (#10818): the payload carries thinking.type and never
// reasoning_effort.
package api

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/types"
)

func captureXiaomiPayload(t *testing.T, model *types.Model, reasoning types.ThinkingLevel) map[string]any {
	t.Helper()
	var captured map[string]any
	stream := ai.StreamSimple(context.Background(), model, &types.Context{Messages: []types.Message{
		types.UserMessage{Role: types.RoleUser, Content: types.UserContent{String: "test"}, Timestamp: 0},
	}}, &ai.SimpleStreamOptions{
		StreamOptions: types.StreamOptions{
			ProviderRequestOptions: types.ProviderRequestOptions{
				APIKey: "test-xiaomi-key",
				OnPayload: func(payload any, model *types.Model) any {
					if m, ok := payload.(map[string]any); ok {
						captured = m
					}
					return nil
				},
			},
		},
		Reasoning: reasoning,
	})
	stream.Result()
	if captured == nil {
		t.Fatal("expected payload capture")
	}
	normalized, err := json.Marshal(captured)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(normalized, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestXiaomiMimoThinkingTogglePayload(t *testing.T) {
	providers := []types.ProviderID{
		types.ProviderXiaomi,
		types.ProviderXiaomiTokenPlanCN,
		types.ProviderXiaomiTokenPlanAMS,
		types.ProviderXiaomiTokenPlanSGP,
	}
	for _, provider := range providers {
		for id, model := range ai.MODELS[provider] {
			if !model.Reasoning {
				continue
			}
			provider, id, model := provider, id, model
			t.Run(string(provider)+"/"+id, func(t *testing.T) {
				model.BaseURL = "http://127.0.0.1:9"

				enabled := captureXiaomiPayload(t, &model, types.ThinkingLevelHigh)
				thinking, ok := enabled["thinking"].(map[string]any)
				if !ok || thinking["type"] != "enabled" {
					t.Errorf("high thinking = %#v, want enabled", enabled["thinking"])
				}
				if _, has := enabled["reasoning_effort"]; has {
					t.Errorf("high reasoning_effort = %v, want absent", enabled["reasoning_effort"])
				}

				disabled := captureXiaomiPayload(t, &model, types.ThinkingLevel(""))
				thinking, ok = disabled["thinking"].(map[string]any)
				if !ok || thinking["type"] != "disabled" {
					t.Errorf("off thinking = %#v, want disabled", disabled["thinking"])
				}
				if _, has := disabled["reasoning_effort"]; has {
					t.Errorf("off reasoning_effort = %v, want absent", disabled["reasoning_effort"])
				}
			})
		}
	}
}
