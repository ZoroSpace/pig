// pi: providers/openrouter-api.ts.
package providers

import (
	"math"
	"strconv"

	"github.com/ZoroSpace/pig/ai"
)

// OpenRouterPricing is OpenRouter's per-token prices, as decimal strings (pi:
// OpenRouterPricing).
type OpenRouterPricing struct {
	Prompt          *string                     `json:"prompt"`
	Completion      *string                     `json:"completion"`
	InputCacheRead  *string                     `json:"input_cache_read"`
	InputCacheWrite *string                     `json:"input_cache_write"`
	Overrides       []OpenRouterPricingOverride `json:"overrides"`
}

// OpenRouterPricingOverride is a conditional price. MinPromptTokens selects
// prompt-length pricing; the utc_* fields select time-of-day or weekday
// pricing. Missing rates keep the base price (pi: OpenRouterPricingOverride).
type OpenRouterPricingOverride struct {
	MinPromptTokens *int64   `json:"min_prompt_tokens"`
	UTCStart        *float64 `json:"utc_start"`
	UTCEnd          *float64 `json:"utc_end"`
	UTCDays         []string `json:"utc_days"`
	Prompt          *string  `json:"prompt"`
	Completion      *string  `json:"completion"`
	InputCacheRead  *string  `json:"input_cache_read"`
	InputCacheWrite *string  `json:"input_cache_write"`
}

// OpenRouterTopProvider carries the top provider's limits (pi: the inline
// top_provider shape of OpenRouterModel).
type OpenRouterTopProvider struct {
	ContextLength       *int64 `json:"context_length"`
	MaxCompletionTokens *int64 `json:"max_completion_tokens"`
}

// OpenRouterModel is the set of fields pig reads from a model in OpenRouter's
// GET /models and GET /models/user (pi: OpenRouterModel).
type OpenRouterModel struct {
	ID            string                 `json:"id"`
	ContextLength *int64                 `json:"context_length"`
	TopProvider   *OpenRouterTopProvider `json:"top_provider"`
	Pricing       *OpenRouterPricing     `json:"pricing"`
}

// OpenRouterCost converts OpenRouter's $/token prices to pig's $/1M tokens
// (pi: openRouterCost). Missing, invalid and negative base prices are 0.
// Prompt-length overrides become request-wide tiers; a rate a tier does not
// list keeps the base price. Time-of-day overrides are skipped because
// ModelCost cannot express them.
func OpenRouterCost(pricing *OpenRouterPricing) ai.ModelCost {
	base := ai.ModelCostRates{
		Input:      openRouterPerMillion(openRouterRate(pricing, func(p *OpenRouterPricing) *string { return p.Prompt }), 0),
		Output:     openRouterPerMillion(openRouterRate(pricing, func(p *OpenRouterPricing) *string { return p.Completion }), 0),
		CacheRead:  openRouterPerMillion(openRouterRate(pricing, func(p *OpenRouterPricing) *string { return p.InputCacheRead }), 0),
		CacheWrite: openRouterPerMillion(openRouterRate(pricing, func(p *OpenRouterPricing) *string { return p.InputCacheWrite }), 0),
	}
	var tiers []ai.ModelCostTier
	if pricing != nil {
		for i := range pricing.Overrides {
			override := &pricing.Overrides[i]
			if override.MinPromptTokens == nil || override.UTCStart != nil || override.UTCEnd != nil || override.UTCDays != nil {
				continue
			}
			tiers = append(tiers, ai.ModelCostTier{
				ModelCostRates: ai.ModelCostRates{
					Input:      openRouterPerMillion(override.Prompt, base.Input),
					Output:     openRouterPerMillion(override.Completion, base.Output),
					CacheRead:  openRouterPerMillion(override.InputCacheRead, base.CacheRead),
					CacheWrite: openRouterPerMillion(override.InputCacheWrite, base.CacheWrite),
				},
				InputTokensAbove: *override.MinPromptTokens,
			})
		}
	}
	return ai.ModelCost{ModelCostRates: base, Tiers: tiers}
}

// openRouterPerMillion converts one $/token rate to $/1M tokens (pi:
// perMillion). 0 means free. Missing, invalid and negative prices use the
// fallback: OpenRouter reports -1 for router models such as `openrouter/auto`,
// whose price depends on the model they pick.
func openRouterPerMillion(value *string, fallback float64) float64 {
	if value == nil {
		return fallback
	}
	perToken, err := strconv.ParseFloat(*value, 64)
	if err != nil || !(perToken >= 0) {
		return fallback
	}
	perMillion := perToken * 1_000_000
	return math.Round(perMillion*1_000_000) / 1_000_000
}

// openRouterRate reads a rate from a possibly-nil pricing object.
func openRouterRate(p *OpenRouterPricing, get func(*OpenRouterPricing) *string) *string {
	if p == nil {
		return nil
	}
	return get(p)
}
