// pi: utils/openrouter-cost.ts — OpenRouter-reported billing.
package utils

import (
	"math"
	"strings"

	"github.com/samber/lo"

	"github.com/ZoroSpace/pig/ai/types"
)

// OpenRouterUsageAccounting is the billing accounting OpenRouter adds to
// streamed usage at runtime (pi: OpenRouterUsageAccounting). The OpenAI and
// Anthropic SDKs do not type these fields.
type OpenRouterUsageAccounting struct {
	// Cost is the amount charged to the user's OpenRouter account.
	Cost *float64 `json:"cost"`
	// IsByok reports whether OpenRouter used the user's upstream provider key.
	IsByok *bool `json:"is_byok"`
	// CostDetails carries upstream costs when IsByok is true.
	CostDetails *OpenRouterCostDetails `json:"cost_details"`
}

// OpenRouterCostDetails is the cost breakdown OpenRouter reports for BYOK
// requests (pi: OpenRouterUsageAccounting["cost_details"]).
type OpenRouterCostDetails struct {
	// UpstreamInferenceCost is the total inference cost billed through the
	// user's upstream provider account, when available.
	UpstreamInferenceCost *float64 `json:"upstream_inference_cost"`
	// UpstreamInferencePromptCost is the upstream cost for prompt processing,
	// including provider-side cache pricing.
	UpstreamInferencePromptCost *float64 `json:"upstream_inference_prompt_cost"`
	// UpstreamInferenceCompletionsCost is the upstream cost for completion
	// generation.
	UpstreamInferenceCompletionsCost *float64 `json:"upstream_inference_completions_cost"`
	// ServerToolCost is metered server-tool cost already included in Cost.
	ServerToolCost *float64 `json:"server_tool_cost"`
}

func isValidCost(value *float64) bool {
	return value != nil && !math.IsNaN(*value) && !math.IsInf(*value, 0) && *value >= 0
}

// IsOpenRouterModel reports whether a model is routed through OpenRouter (pi:
// isOpenRouterModel).
func IsOpenRouterModel(model *types.Model) bool {
	if model == nil {
		return false
	}
	return model.Provider == "openrouter" ||
		strings.Contains(model.BaseURL, "openrouter.ai")
}

// ApplyOpenRouterReportedCost replaces the catalog total with OpenRouter's
// billed cost when the response contains complete, valid accounting (pi:
// applyOpenRouterReportedCost).
func ApplyOpenRouterReportedCost(usage *types.Usage, reported OpenRouterUsageAccounting) {
	if !isValidCost(reported.Cost) {
		return
	}

	if reported.IsByok != nil && !*reported.IsByok {
		usage.Cost.Total = *reported.Cost
		usage.Cost.TotalFromProvider = lo.ToPtr(true)
		return
	}

	if reported.IsByok != nil && *reported.IsByok {
		details := reported.CostDetails
		if details == nil {
			return
		}
		upstreamCost := details.UpstreamInferenceCost
		if upstreamCost == nil {
			if !isValidCost(details.UpstreamInferencePromptCost) ||
				!isValidCost(details.UpstreamInferenceCompletionsCost) {
				return
			}
			sum := *details.UpstreamInferencePromptCost + *details.UpstreamInferenceCompletionsCost
			upstreamCost = &sum
		}
		if !isValidCost(upstreamCost) {
			return
		}
		usage.Cost.Total = *reported.Cost + *upstreamCost
		usage.Cost.TotalFromProvider = lo.ToPtr(true)
	}
}
