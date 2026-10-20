// pi: generate-models.ts minimax block (#11121) — hidden older models and
// adaptive thinking metadata for effort-level models.
package main

import (
	"testing"

	"github.com/ZoroSpace/pig/ai"
)

func TestProcessMinimaxModelsHidesOldModelsAndAddsAdaptiveThinking(t *testing.T) {
	enabled := true
	low, medium, high, xhigh, max := "low", "medium", "high", "xhigh", "max"
	data := ModelsDevCatalog{
		"minimax": {Models: map[string]*ModelsDevModel{
			"MiniMax-M2.5": {Name: "MiniMax-M2.5", ToolCall: &enabled},
			"MiniMax-M2.7": {Name: "MiniMax-M2.7", ToolCall: &enabled, Reasoning: &enabled},
			"MiniMax-M3.1-Flash": {
				Name: "MiniMax-M3.1-Flash", ToolCall: &enabled, Reasoning: &enabled,
				ReasoningOptions: []ModelsDevReasoningOption{{
					Type:   "effort",
					Values: []*string{&low, &medium, &high, &xhigh, &max},
				}},
			},
		}},
		"minimax-cn": {Models: map[string]*ModelsDevModel{
			"MiniMax-M3": {Name: "MiniMax-M3", ToolCall: &enabled, Reasoning: &enabled},
		}},
	}

	models := processMinimaxModels(data)
	byProvider := map[ai.ProviderID]map[string]*ai.Model{}
	for _, model := range models {
		if byProvider[model.Provider] == nil {
			byProvider[model.Provider] = map[string]*ai.Model{}
		}
		byProvider[model.Provider][model.ID] = model
	}

	if byProvider[ai.ProviderMinimax]["MiniMax-M2.5"] != nil {
		t.Error("MiniMax-M2.5 should be hidden")
	}
	if byProvider[ai.ProviderMinimax]["MiniMax-M2.7"] == nil {
		t.Error("MiniMax-M2.7 should remain")
	}
	m3 := byProvider[ai.ProviderMinimax]["MiniMax-M3.1-Flash"]
	if m3 == nil {
		t.Fatal("MiniMax-M3.1-Flash missing")
	}
	if m3.ThinkingLevelMap[ai.ModelThinkingOff] != nil ||
		m3.ThinkingLevelMap[ai.ModelThinkingLow] == nil ||
		m3.ThinkingLevelMap[ai.ModelThinkingMax] == nil {
		t.Errorf("unexpected thinking level map: %+v", m3.ThinkingLevelMap)
	}
	compat, ok := m3.Compat.(*ai.AnthropicMessagesCompat)
	if !ok || compat.ForceAdaptiveThinking == nil || !*compat.ForceAdaptiveThinking {
		t.Errorf("ForceAdaptiveThinking not set: %#v", m3.Compat)
	}
	if byProvider[ai.ProviderMinimaxCN]["MiniMax-M3"] == nil {
		t.Error("MiniMax-M3 should remain on minimax-cn")
	}
}
