package utils

import (
	"testing"

	"github.com/samber/lo"

	"github.com/ZoroSpace/pig/ai/types"
)

func openRouterTestUsage(total float64) *types.Usage {
	return &types.Usage{
		Input: 1, Output: 1, TotalTokens: 2,
		Cost: types.Cost{Input: 0.25, Output: 0.75, Total: total},
	}
}

func TestIsOpenRouterModel(t *testing.T) {
	cases := []struct {
		provider string
		baseURL  string
		want     bool
	}{
		{"openrouter", "https://example.com", true},
		{"openrouter-work", "https://openrouter.ai/api/v1", true},
		{"custom", "https://example.com/v1", false},
	}
	for _, test := range cases {
		model := &types.Model{Provider: types.ProviderID(test.provider), BaseURL: test.baseURL}
		if got := IsOpenRouterModel(model); got != test.want {
			t.Errorf("%s %s = %v, want %v", test.provider, test.baseURL, got, test.want)
		}
	}
	if IsOpenRouterModel(nil) {
		t.Error("nil model should not be OpenRouter")
	}
}

func TestApplyOpenRouterReportedCost(t *testing.T) {
	t.Run("uses the reported cost for non-BYOK requests", func(t *testing.T) {
		usage := openRouterTestUsage(1)
		ApplyOpenRouterReportedCost(usage, OpenRouterUsageAccounting{
			Cost:   lo.ToPtr(0.0),
			IsByok: lo.ToPtr(false),
			CostDetails: &OpenRouterCostDetails{
				UpstreamInferenceCost:            lo.ToPtr(0.0),
				UpstreamInferencePromptCost:      lo.ToPtr(0.0),
				UpstreamInferenceCompletionsCost: lo.ToPtr(0.0),
			},
		})
		if usage.Cost.Total != 0 {
			t.Errorf("total = %v, want 0", usage.Cost.Total)
		}
		if usage.Cost.TotalFromProvider == nil || !*usage.Cost.TotalFromProvider {
			t.Errorf("totalFromProvider = %v, want true", usage.Cost.TotalFromProvider)
		}
	})

	t.Run("adds the upstream inference cost for BYOK requests", func(t *testing.T) {
		usage := openRouterTestUsage(1)
		ApplyOpenRouterReportedCost(usage, OpenRouterUsageAccounting{
			Cost:   lo.ToPtr(0.1),
			IsByok: lo.ToPtr(true),
			CostDetails: &OpenRouterCostDetails{
				UpstreamInferenceCost:            lo.ToPtr(0.9),
				UpstreamInferencePromptCost:      lo.ToPtr(0.6),
				UpstreamInferenceCompletionsCost: lo.ToPtr(0.3),
			},
		})
		if usage.Cost.Total != 1 {
			t.Errorf("total = %v, want 1", usage.Cost.Total)
		}
		if usage.Cost.TotalFromProvider == nil || !*usage.Cost.TotalFromProvider {
			t.Errorf("totalFromProvider = %v, want true", usage.Cost.TotalFromProvider)
		}
	})

	t.Run("uses upstream cost components when the total is omitted", func(t *testing.T) {
		usage := openRouterTestUsage(1)
		ApplyOpenRouterReportedCost(usage, OpenRouterUsageAccounting{
			Cost:   lo.ToPtr(0.1),
			IsByok: lo.ToPtr(true),
			CostDetails: &OpenRouterCostDetails{
				UpstreamInferencePromptCost:      lo.ToPtr(0.6),
				UpstreamInferenceCompletionsCost: lo.ToPtr(0.3),
			},
		})
		if usage.Cost.Total < 0.999999 || usage.Cost.Total > 1.000001 {
			t.Errorf("total = %v, want 1", usage.Cost.Total)
		}
	})

	t.Run("keeps the catalog total when accounting is incomplete", func(t *testing.T) {
		usage := openRouterTestUsage(1)
		usage.Cost.TotalFromProvider = lo.ToPtr(false)
		ApplyOpenRouterReportedCost(usage, OpenRouterUsageAccounting{})
		ApplyOpenRouterReportedCost(usage, OpenRouterUsageAccounting{
			Cost:   lo.ToPtr(0.1),
			IsByok: lo.ToPtr(true),
		})
		ApplyOpenRouterReportedCost(usage, OpenRouterUsageAccounting{
			Cost:        lo.ToPtr(-1.0),
			IsByok:      lo.ToPtr(false),
			CostDetails: nil,
		})
		if usage.Cost.Total != 1 {
			t.Errorf("total = %v, want 1", usage.Cost.Total)
		}
		if usage.Cost.TotalFromProvider == nil || *usage.Cost.TotalFromProvider {
			t.Errorf("totalFromProvider = %v, want false", usage.Cost.TotalFromProvider)
		}
	})
}
