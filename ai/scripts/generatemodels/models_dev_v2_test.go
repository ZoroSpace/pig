package main

import (
	"encoding/json"
	"testing"

	"github.com/ZoroSpace/pig/ai"
)

func TestNormalizeModelsDevV2Catalog(t *testing.T) {
	raw := `{
		"acme": {"models": {
			"chat-model": {
				"id": "chat-model", "type": "chat", "name": "Chat Model", "status": "stable",
				"modalities": {"input": ["text", "image/png"], "output": ["text"]},
				"capabilities": {
					"tools": {"supported": true},
					"reasoning": {"supported": true, "toggle": true, "effort": ["low", "high"]},
					"structured_output": true
				},
				"limit": {"context": 100000, "output": 8192},
				"cost": {"text": {
					"input": {"price": 1, "unit": "token", "cache": {"read": 0.1, "write": {"5m": 6.25, "1h": 10}}},
					"output": {"price": 2, "unit": "token"}
				}},
				"api": {
					"chat-completions": {"base_url": "https://acme.test/v1", "sdk": {"aisdk": "@ai-sdk/openai"}},
					"responses": {"base_url": "https://acme.test/v1"}
				}
			},
			"image-model": {"id": "image-model", "type": "image", "name": "Image"}
		}}
	}`
	var data modelsDevV2Catalog
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		t.Fatalf("decode v2 fixture: %v", err)
	}
	catalog := normalizeModelsDevV2Catalog(data)
	models := catalog["acme"].Models
	if _, ok := models["image-model"]; ok {
		t.Fatal("non-chat model entered the chat catalog")
	}
	m := models["chat-model"]
	if m == nil {
		t.Fatal("chat model missing")
	}
	if m.ID != "chat-model" || m.Name != "Chat Model" || m.Status != "stable" {
		t.Errorf("identity fields = %+v", m)
	}
	if m.ToolCall == nil || !*m.ToolCall || m.StructuredOutput == nil || !*m.StructuredOutput || m.Reasoning == nil || !*m.Reasoning {
		t.Errorf("capability pointers = tools:%v structured:%v reasoning:%v", m.ToolCall, m.StructuredOutput, m.Reasoning)
	}
	if len(m.ReasoningOptions) != 2 || m.ReasoningOptions[0].Type != "toggle" || m.ReasoningOptions[1].Type != "effort" {
		t.Errorf("reasoning options = %+v", m.ReasoningOptions)
	}
	if len(m.Modalities.Input) != 2 || m.Modalities.Input[0] != "text" || m.Modalities.Input[1] != "image" {
		t.Errorf("input modalities = %v", m.Modalities.Input)
	}
	if m.Cost == nil || m.Cost.Input == nil || *m.Cost.Input != 1 || *m.Cost.Output != 2 {
		t.Fatalf("cost = %+v", m.Cost)
	}
	if m.Cost.CacheRead == nil || *m.Cost.CacheRead != 0.1 {
		t.Errorf("cache read = %v", m.Cost.CacheRead)
	}
	// The 5m TTL is models.dev's first/default entry; Go maps lose insertion
	// order, so the normalizer must still choose it deterministically.
	if m.Cost.CacheWrite == nil || *m.Cost.CacheWrite != 6.25 {
		t.Errorf("cache write = %v, want 6.25", m.Cost.CacheWrite)
	}
	if len(m.APIs) != 2 || m.APIs[0].Protocol != "responses" || m.APIs[1].Protocol != "chat-completions" {
		t.Errorf("APIs = %+v", m.APIs)
	}
	if m.APIs[1].AISDK != "@ai-sdk/openai" {
		t.Errorf("aisdk = %q", m.APIs[1].AISDK)
	}
	if m.Provider == nil || m.Provider.Npm != "@ai-sdk/openai" {
		t.Errorf("provider npm hint = %+v", m.Provider)
	}
}

func TestNormalizeModelsDevV2CostTierOrder(t *testing.T) {
	raw := `{"text": {
		"input": {"price": 1, "cache": {"write": {"1h": 10, "5m": 6.25}}, "overrides": [
			{"when": {"context": {"gt": 400000}}, "price": 4, "cache": {"write": {"1h": 40, "5m": 25}}},
			{"when": {"context": {"gt": 200000}}, "price": 2}
		]},
		"output": {"price": 2, "overrides": [
			{"when": {"context": {"gt": 200000}}, "price": 3},
			{"when": {"context": {"gt": 400000}}, "price": 6}
		]}
	}}`
	var cost modelsDevV2Cost
	if err := json.Unmarshal([]byte(raw), &cost); err != nil {
		t.Fatal(err)
	}
	normalized := normalizeModelsDevV2Cost(&cost)
	if normalized == nil || len(normalized.Tiers) != 2 {
		t.Fatalf("tiers = %+v", normalized)
	}
	// pi preserves the order overrides are first seen (input first, then
	// output), not numeric order.
	if normalized.Tiers[0].Tier.Size == nil || *normalized.Tiers[0].Tier.Size != 400000 {
		t.Errorf("first tier = %+v, want 400000", normalized.Tiers[0])
	}
	if normalized.Tiers[1].Tier.Size == nil || *normalized.Tiers[1].Tier.Size != 200000 {
		t.Errorf("second tier = %+v, want 200000", normalized.Tiers[1])
	}
	if normalized.Tiers[0].Input == nil || *normalized.Tiers[0].Input != 4 || normalized.Tiers[0].Output == nil || *normalized.Tiers[0].Output != 6 {
		t.Errorf("400k rates = %+v", normalized.Tiers[0])
	}
	if normalized.Tiers[0].CacheWrite == nil || *normalized.Tiers[0].CacheWrite != 25 {
		t.Errorf("400k cache write = %v, want 25", normalized.Tiers[0].CacheWrite)
	}
}

func TestOpenCodeAPIProtocolSelection(t *testing.T) {
	base := "https://example.test/zen"
	model := &ModelsDevModel{APIs: []ModelsDevAPI{
		{Protocol: "chat-completions", BaseURL: base + "/openai/v1"},
		{Protocol: "messages", BaseURL: base + "/anthropic/v1"},
	}}
	gotAPI, gotBase, compat, ok := openCodeAPI(model)
	if !ok || gotAPI != ai.ApiAnthropicMessages || gotBase != base+"/anthropic" {
		t.Fatalf("priority = %v %q %v, want anthropic messages %q", gotAPI, gotBase, ok, base+"/anthropic")
	}
	if compat != nil {
		t.Errorf("messages compat = %+v, want nil", compat)
	}

	gotAPI, gotBase, compat, ok = openCodeAPI(&ModelsDevModel{APIs: []ModelsDevAPI{
		{Protocol: "chat-completions", BaseURL: base + "/openai/v1", AISDK: "@ai-sdk/alibaba"},
	}})
	if !ok || gotAPI != ai.ApiOpenAICompletions || gotBase != base+"/openai/v1" {
		t.Fatalf("chat completions = %v %q %v", gotAPI, gotBase, ok)
	}
	openAICompat, ok := compat.(*ai.OpenAICompletionsCompat)
	if !ok || openAICompat.CacheControlFormat == nil || *openAICompat.CacheControlFormat != ai.CacheControlAnthropic {
		t.Errorf("alibaba compat = %+v", compat)
	}

	if _, _, _, ok := openCodeAPI(&ModelsDevModel{APIs: []ModelsDevAPI{{Protocol: "bidi-generate-content"}}}); ok {
		t.Error("unsupported protocol unexpectedly accepted")
	}
}

func TestWithOpenAiLongContextPricingReplacesTiers(t *testing.T) {
	cost := ai.ModelCost{
		ModelCostRates: ai.ModelCostRates{Input: 1, Output: 5, CacheRead: 0.05, CacheWrite: 1.25},
		Tiers:          []ai.ModelCostTier{{InputTokensAbove: 100000, ModelCostRates: ai.ModelCostRates{Input: 9}}},
	}
	got := withOpenAiLongContextPricing(cost)
	if len(got.Tiers) != 1 {
		t.Fatalf("tiers = %+v, want exactly the long-context tier", got.Tiers)
	}
	tier := got.Tiers[0]
	if tier.InputTokensAbove != openAILongContextInputThreshold {
		t.Errorf("threshold = %d", tier.InputTokensAbove)
	}
	if tier.Input != 2 || tier.Output != 7.5 || tier.CacheRead != 0.1 || tier.CacheWrite != 2.5 {
		t.Errorf("tier rates = %+v", tier.ModelCostRates)
	}
	if _, ok := openAILongContextPricingModelIds["gpt-6.1-terra"]; !ok {
		t.Error("gpt-6.1-terra missing from the long-context pricing set")
	}
}
