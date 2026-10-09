// ai/scripts/generatemodels/ai_gateway_pricing (pi:
// packages/ai/scripts/ai-gateway-pricing.ts, S17 943a10e74) — converts Vercel
// AI Gateway pricing to ModelCost. Each rate lists its own prompt-length
// brackets; every bracket start above zero becomes a request-wide tier with
// the rates in effect there.
package main

import (
	"encoding/json"
	"math"
	"sort"

	"github.com/ZoroSpace/pig/ai"
)

// AiGatewayPriceTier is a prompt-length bracket (pi: AiGatewayPriceTier).
// Min is inclusive and Max exclusive, both in prompt tokens. Cost is $/token
// as string|number; absent means the base rate stays in effect.
type AiGatewayPriceTier struct {
	Cost *json.Number `json:"cost"`
	Min  *float64     `json:"min"`
	Max  *float64     `json:"max"`
}

// aiGatewayPerMillion converts $/token to $/million tokens (pi: perMillion —
// parseFloat ?? "0", non-finite becomes 0, then *1_000_000 rounded to 6dp).
func aiGatewayPerMillion(value *json.Number) float64 {
	if value == nil {
		return 0
	}
	f, err := value.Float64()
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0
	}
	return roundCost(f * 1_000_000)
}

// aiGatewayBracket is one rate field's bracket accessor (pi: one RATE_FIELDS
// entry — the rates slot plus the pricing brackets that override it).
type aiGatewayBracket struct {
	rate   func(r *ai.ModelCostRates) *float64
	tiers  func(p *AIGatewayPricing) []AiGatewayPriceTier
	perMln func(t *AiGatewayPriceTier) float64
}

// aiGatewayBrackets maps ModelCostRates fields to AIGatewayPricing bracket
// fields (pi: RATE_FIELDS — input/input_tiers, output/output_tiers,
// cacheRead/input_cache_read_tiers, cacheWrite/input_cache_write_tiers).
var aiGatewayBrackets = []aiGatewayBracket{
	{func(r *ai.ModelCostRates) *float64 { return &r.Input },
		func(p *AIGatewayPricing) []AiGatewayPriceTier { return p.InputTiers },
		func(t *AiGatewayPriceTier) float64 { return aiGatewayPerMillion(t.Cost) }},
	{func(r *ai.ModelCostRates) *float64 { return &r.Output },
		func(p *AIGatewayPricing) []AiGatewayPriceTier { return p.OutputTiers },
		func(t *AiGatewayPriceTier) float64 { return aiGatewayPerMillion(t.Cost) }},
	{func(r *ai.ModelCostRates) *float64 { return &r.CacheRead },
		func(p *AIGatewayPricing) []AiGatewayPriceTier { return p.InputCacheReadTiers },
		func(t *AiGatewayPriceTier) float64 { return aiGatewayPerMillion(t.Cost) }},
	{func(r *ai.ModelCostRates) *float64 { return &r.CacheWrite },
		func(p *AIGatewayPricing) []AiGatewayPriceTier { return p.InputCacheWriteTiers },
		func(t *AiGatewayPriceTier) float64 { return aiGatewayPerMillion(t.Cost) }},
}

// getAiGatewayCost converts AI Gateway pricing to $/million tokens (pi:
// getAiGatewayCost).
func getAiGatewayCost(pricing *AIGatewayPricing) ai.ModelCost {
	base := ai.ModelCostRates{
		Input:      aiGatewayPerMillion(pricingField(pricing, func(p *AIGatewayPricing) json.Number { return p.Input })),
		Output:     aiGatewayPerMillion(pricingField(pricing, func(p *AIGatewayPricing) json.Number { return p.Output })),
		CacheRead:  aiGatewayPerMillion(pricingField(pricing, func(p *AIGatewayPricing) json.Number { return p.InputCacheRead })),
		CacheWrite: aiGatewayPerMillion(pricingField(pricing, func(p *AIGatewayPricing) json.Number { return p.InputCacheWrite })),
	}

	// Bracket starts above zero, across all rates, sorted ascending (pi: the
	// `starts` Set over every rate's brackets).
	startSet := map[float64]bool{}
	if pricing != nil {
		for _, field := range aiGatewayBrackets {
			for i := range field.tiers(pricing) {
				bracket := &field.tiers(pricing)[i]
				if bracket.Min != nil && *bracket.Min > 0 {
					startSet[*bracket.Min] = true
				}
			}
		}
	}
	if len(startSet) == 0 {
		return ai.ModelCost{ModelCostRates: base}
	}
	starts := make([]float64, 0, len(startSet))
	for start := range startSet {
		starts = append(starts, start)
	}
	sort.Float64s(starts)

	tiers := make([]ai.ModelCostTier, 0, len(starts))
	for _, start := range starts {
		rates := base
		for _, field := range aiGatewayBrackets {
			for i := range field.tiers(pricing) {
				bracket := &field.tiers(pricing)[i]
				// pi: (candidate.min ?? 0) <= start && (candidate.max === undefined || start < candidate.max);
				// a bracket that covers the start overrides the rate only when it lists a cost.
				min := 0.0
				if bracket.Min != nil {
					min = *bracket.Min
				}
				if min <= start && (bracket.Max == nil || start < *bracket.Max) && bracket.Cost != nil {
					*field.rate(&rates) = field.perMln(bracket)
				}
			}
		}
		tiers = append(tiers, ai.ModelCostTier{
			ModelCostRates:   rates,
			InputTokensAbove: int64(start) - 1,
		})
	}
	return ai.ModelCost{ModelCostRates: base, Tiers: tiers}
}

// pricingField reads a base rate from a possibly-nil pricing object.
func pricingField(p *AIGatewayPricing, get func(p *AIGatewayPricing) json.Number) *json.Number {
	if p == nil {
		return nil
	}
	v := get(p)
	return &v
}
