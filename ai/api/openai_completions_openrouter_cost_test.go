package api

import (
	"testing"

	"github.com/samber/lo"

	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/ai/utils"
)

func openRouterCostModel(provider string) *types.Model {
	return &types.Model{
		ID: "openrouter/auto", Name: "OpenRouter Auto", API: types.ApiOpenAICompletions,
		Provider: types.ProviderID(provider), BaseURL: "https://openrouter.ai/api/v1",
		Cost: types.ModelCost{ModelCostRates: types.ModelCostRates{Input: 5, Output: 15, CacheRead: 1, CacheWrite: 2}},
	}
}

func TestParseChunkUsageUsesOpenRouterReportedTotal(t *testing.T) {
	raw := &chatCompletionUsage{
		PromptTokens: 100, CompletionTokens: 20,
		Cost:   lo.ToPtr(0.004),
		IsByok: lo.ToPtr(false),
		CostDetails: &utils.OpenRouterCostDetails{
			UpstreamInferenceCost: lo.ToPtr(0.003),
		},
	}
	for _, provider := range []string{"openrouter", "openrouter-work"} {
		usage := parseChunkUsage(raw, openRouterCostModel(provider))
		if usage.Cost.Total != 0.004 {
			t.Errorf("%s total = %v, want 0.004", provider, usage.Cost.Total)
		}
		if usage.Cost.TotalFromProvider == nil || !*usage.Cost.TotalFromProvider {
			t.Errorf("%s totalFromProvider = %v, want true", provider, usage.Cost.TotalFromProvider)
		}
		if usage.Cost.Input == 0 || usage.Cost.Output == 0 {
			t.Errorf("%s catalog components = %#v, want estimates", provider, usage.Cost)
		}
	}
}

func TestParseChunkUsageAddsOpenRouterBYOKUpstreamCost(t *testing.T) {
	raw := &chatCompletionUsage{
		PromptTokens: 100, CompletionTokens: 20,
		Cost:   lo.ToPtr(0.0002),
		IsByok: lo.ToPtr(true),
		CostDetails: &utils.OpenRouterCostDetails{
			UpstreamInferenceCost: lo.ToPtr(0.003),
		},
	}
	usage := parseChunkUsage(raw, openRouterCostModel("openrouter"))
	if usage.Cost.Total != 0.0032 {
		t.Errorf("total = %v, want 0.0032", usage.Cost.Total)
	}
	if usage.Cost.TotalFromProvider == nil || !*usage.Cost.TotalFromProvider {
		t.Errorf("totalFromProvider = %v, want true", usage.Cost.TotalFromProvider)
	}
}

func TestParseChunkUsageKeepsCatalogTotalForIncompleteAccounting(t *testing.T) {
	raw := &chatCompletionUsage{
		PromptTokens: 100, CompletionTokens: 20,
		Cost:   lo.ToPtr(0.1),
		IsByok: lo.ToPtr(true),
	}
	model := openRouterCostModel("openrouter")
	usage := parseChunkUsage(raw, model)
	if usage.Cost.Total != 0.0008 {
		t.Errorf("total = %v, want catalog estimate 0.0008", usage.Cost.Total)
	}
	if usage.Cost.TotalFromProvider == nil || *usage.Cost.TotalFromProvider {
		t.Errorf("totalFromProvider = %v, want false", usage.Cost.TotalFromProvider)
	}
}
