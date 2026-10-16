// pi: test/model-cost-tiers.test.ts (S17 943a10e74). Covers OpenRouter
// pricing overrides (prompt-length → tiers, time-of-day skipped) and Vercel
// AI Gateway per-rate bracket tiers.
package main

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/providers"
)

// openRouterChatCost runs one pricing shape through the OpenRouter catalog
// builder (pi: openRouterChatCost helper).
func openRouterChatCost(t *testing.T, pricing *providers.OpenRouterPricing) ai.ModelCost {
	t.Helper()
	catalog := buildOpenRouterCatalog([]OpenRouterModel{
		{
			OpenRouterModel:     providers.OpenRouterModel{ID: "anthropic/claude-haiku-5.5", Pricing: pricing},
			Name:                "Claude Haiku 5.5",
			SupportedParameters: []string{"tools"},
		},
	}, nil, nil)
	if len(catalog.chat) == 0 {
		t.Fatal("catalog.chat is empty")
	}
	return catalog.chat[0].Cost
}

// rateAssert asserts one ai.ModelCost against a literal built from the pi
// test's TS object (tiers compared field-wise).
func assertModelCost(t *testing.T, got ai.ModelCost, want ai.ModelCost) {
	t.Helper()
	if got.Input != want.Input || got.Output != want.Output || got.CacheRead != want.CacheRead || got.CacheWrite != want.CacheWrite {
		t.Fatalf("base rates = %+v, want %+v", got.ModelCostRates, want.ModelCostRates)
	}
	if len(got.Tiers) != len(want.Tiers) {
		t.Fatalf("tiers = %+v, want %+v", got.Tiers, want.Tiers)
	}
	for i, tier := range got.Tiers {
		if !reflect.DeepEqual(tier, want.Tiers[i]) {
			t.Fatalf("tier[%d] = %+v, want %+v", i, tier, want.Tiers[i])
		}
	}
}

func strPtr(s string) *string { return &s }
func i64Ptr(i int64) *int64   { return &i }
func f64Ptr(f float64) *float64 {
	return &f
}
func jsonNum(s string) *json.Number {
	n := json.Number(s)
	return &n
}

func TestOpenRouterPricingOverrides(t *testing.T) {
	t.Run("turns prompt-length overrides into tiers", func(t *testing.T) {
		cost := openRouterChatCost(t, &providers.OpenRouterPricing{
			Prompt:          strPtr("0.0000001"),
			Completion:      strPtr("0.0000005"),
			InputCacheRead:  strPtr("0.00000001"),
			InputCacheWrite: strPtr("0.000000125"),
			Overrides: []providers.OpenRouterPricingOverride{{
				MinPromptTokens: i64Ptr(100000),
				Prompt:          strPtr("0.0000005"),
				Completion:      strPtr("0.0000025"),
				InputCacheRead:  strPtr("0.00000005"),
				InputCacheWrite: strPtr("0.000000625"),
			}},
		})
		assertModelCost(t, cost, ai.ModelCost{
			ModelCostRates: ai.ModelCostRates{Input: 0.1, Output: 0.5, CacheRead: 0.01, CacheWrite: 0.125},
			Tiers: []ai.ModelCostTier{{
				ModelCostRates:   ai.ModelCostRates{Input: 0.5, Output: 2.5, CacheRead: 0.05, CacheWrite: 0.625},
				InputTokensAbove: 100000,
			}},
		})
	})

	t.Run("keeps base rates missing from an override", func(t *testing.T) {
		cost := openRouterChatCost(t, &providers.OpenRouterPricing{
			Prompt:          strPtr("0.000002"),
			Completion:      strPtr("0.000012"),
			InputCacheRead:  strPtr("0.0000002"),
			InputCacheWrite: strPtr("0.000000375"),
			Overrides: []providers.OpenRouterPricingOverride{{
				MinPromptTokens: i64Ptr(200000),
				Prompt:          strPtr("0.000004"),
				Completion:      strPtr("0.000018"),
			}},
		})
		if len(cost.Tiers) != 1 {
			t.Fatalf("tiers = %+v, want exactly one", cost.Tiers)
		}
		want := ai.ModelCostTier{
			ModelCostRates:   ai.ModelCostRates{Input: 4, Output: 18, CacheRead: 0.2, CacheWrite: 0.375},
			InputTokensAbove: 200000,
		}
		if !reflect.DeepEqual(cost.Tiers[0], want) {
			t.Fatalf("tier = %+v, want %+v", cost.Tiers[0], want)
		}
	})

	t.Run("skips time-of-day overrides", func(t *testing.T) {
		cost := openRouterChatCost(t, &providers.OpenRouterPricing{
			Prompt:     strPtr("0.000000132"),
			Completion: strPtr("0.000000528"),
			Overrides: []providers.OpenRouterPricingOverride{
				{UTCStart: f64Ptr(0), UTCEnd: f64Ptr(1600), Prompt: strPtr("0.000000132"), Completion: strPtr("0.000000528")},
				{UTCDays: []string{"saturday"}, MinPromptTokens: i64Ptr(1000), Prompt: strPtr("0.0000001")},
			},
		})
		assertModelCost(t, cost, ai.ModelCost{
			ModelCostRates: ai.ModelCostRates{Input: 0.132, Output: 0.528},
		})
	})
}

func TestAiGatewayPricingTiers(t *testing.T) {
	t.Run("turns bracket starts into tiers with the rates in effect there", func(t *testing.T) {
		cost := getAiGatewayCost(&AIGatewayPricing{
			Input:          *jsonNum("0.000001"),
			Output:         *jsonNum("0.000005"),
			InputCacheRead: json.Number("0.0000002"),
			InputTiers: []AiGatewayPriceTier{
				{Cost: jsonNum("0.000001"), Min: f64Ptr(0), Max: f64Ptr(32001)},
				{Cost: jsonNum("0.0000018"), Min: f64Ptr(32001), Max: f64Ptr(128001)},
				{Cost: jsonNum("0.000003"), Min: f64Ptr(128001)},
			},
			OutputTiers: []AiGatewayPriceTier{
				{Cost: jsonNum("0.000005"), Min: f64Ptr(0), Max: f64Ptr(32001)},
				{Cost: jsonNum("0.000009"), Min: f64Ptr(32001), Max: f64Ptr(128001)},
				{Cost: jsonNum("0.000015"), Min: f64Ptr(128001)},
			},
		})
		assertModelCost(t, cost, ai.ModelCost{
			ModelCostRates: ai.ModelCostRates{Input: 1, Output: 5, CacheRead: 0.2},
			Tiers: []ai.ModelCostTier{
				{ModelCostRates: ai.ModelCostRates{Input: 1.8, Output: 9, CacheRead: 0.2}, InputTokensAbove: 32000},
				{ModelCostRates: ai.ModelCostRates{Input: 3, Output: 15, CacheRead: 0.2}, InputTokensAbove: 128000},
			},
		})
	})

	t.Run("returns base rates without tiers", func(t *testing.T) {
		cost := getAiGatewayCost(&AIGatewayPricing{
			Input:  *jsonNum("0.000003"),
			Output: *jsonNum("0.000015"),
		})
		assertModelCost(t, cost, ai.ModelCost{
			ModelCostRates: ai.ModelCostRates{Input: 3, Output: 15},
		})
	})
}

// TestModelsDevCostOfTierFallback pins the getModelsDevCost ?? semantics: a
// rate a tier does not list keeps the base price (pi 943a10e74).
func TestModelsDevCostOfTierFallback(t *testing.T) {
	in := 2.0
	out := 12.0
	cacheRead := 0.2
	tierIn := 4.0
	tierOut := 18.0
	cost := modelsDevCostOf(&ModelsDevCost{
		Input:     &in,
		Output:    &out,
		CacheRead: &cacheRead,
		Tiers: []ModelsDevCostTier{{
			Tier:  &ModelsDevTier{Type: "context", Size: i64Ptr(200000)},
			Input: &tierIn, Output: &tierOut,
			// CacheRead/CacheWrite absent: keep the base price.
		}},
	})
	want := ai.ModelCostTier{
		ModelCostRates:   ai.ModelCostRates{Input: 4, Output: 18, CacheRead: 0.2, CacheWrite: 0},
		InputTokensAbove: 200000,
	}
	if len(cost.Tiers) != 1 || !reflect.DeepEqual(cost.Tiers[0], want) {
		t.Fatalf("tiers = %+v, want [%+v]", cost.Tiers, want)
	}
}
