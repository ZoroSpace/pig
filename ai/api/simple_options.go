// pi: api/simple-options.ts.
package api

import (
	"strings"

	"github.com/samber/lo"

	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/ai/utils"
)

const contextSafetyTokens = 4096
const minMaxTokens = 1

// ClampMaxTokensToContext caps max tokens to the context window minus the
// estimated context, keeping at least minMaxTokens (pi:
// clampMaxTokensToContext).
func ClampMaxTokensToContext(model *types.Model, context *types.TranscriptContext, maxTokens int64) int64 {
	if model.ContextWindow <= 0 {
		return maxInt64(minMaxTokens, maxTokens)
	}
	available := model.ContextWindow - utils.EstimateContextTokens(context).Tokens - contextSafetyTokens
	return minInt64(maxTokens, maxInt64(minMaxTokens, available))
}

// ResolveSamplingParams merges model defaults, the effective thinking level's
// overrides, and request-level params (later wins per key). thinkingLevel is
// clamped to the model's supported levels first; "" means off (pi:
// resolveSamplingParams, v1.0.2 #9776).
func ResolveSamplingParams(model *types.Model, thinkingLevel types.ModelThinkingLevel, requestParams map[string]any) map[string]any {
	if thinkingLevel == "" {
		thinkingLevel = types.ModelThinkingOff
	}
	effectiveThinkingLevel := ai.ClampThinkingLevel(model, thinkingLevel)
	thinkingLevelParams := model.SamplingParamsByThinkingLevel[effectiveThinkingLevel]
	if model.SamplingParams == nil && thinkingLevelParams == nil && requestParams == nil {
		return nil
	}
	merged := make(map[string]any)
	for key, value := range model.SamplingParams {
		merged[key] = value
	}
	for key, value := range thinkingLevelParams {
		merged[key] = value
	}
	for key, value := range requestParams {
		merged[key] = value
	}
	return merged
}

// BuildBaseOptions maps a SimpleStreamOptions into the StreamOptions shared by
// full streams (pi: buildBaseOptions). signal is intentionally absent (Go
// cancellation travels as context).
func BuildBaseOptions(model *types.Model, context *types.TranscriptContext, options *types.SimpleStreamOptions, apiKey string) types.StreamOptions {
	// v1.0.2 #9776: sampling params are resolved here over the request's
	// thinking level; each adapter's buildParams re-merges the same layers
	// (idempotent, same shape as pi).
	reasoning := types.ModelThinkingLevel(options.Reasoning)
	samplingParams := ResolveSamplingParams(model, reasoning, options.SamplingParams)
	maxTokens := options.MaxTokens
	if maxTokens == nil {
		maxTokens = &model.MaxTokens
	}
	clamped := ClampMaxTokensToContext(model, context, *maxTokens)

	base := types.StreamOptions{
		Temperature:    options.Temperature,
		SamplingParams: samplingParams,
		MaxTokens:      &clamped,
		Transport:      options.Transport,
		CacheRetention: options.CacheRetention,
		SessionID:      options.SessionID,
		Metadata:       options.Metadata,
	}
	base.TelemetryContext = options.TelemetryContext
	if apiKey != "" {
		base.APIKey = apiKey
	} else {
		base.APIKey = options.APIKey
	}
	base.Fetch = options.Fetch
	base.Headers = options.Headers
	base.OnPayload = options.OnPayload
	base.OnResponse = options.OnResponse
	base.TimeoutMs = options.TimeoutMs
	base.WebsocketConnectTimeoutMs = options.WebsocketConnectTimeoutMs
	base.MaxRetries = options.MaxRetries
	base.MaxRetryDelayMs = options.MaxRetryDelayMs
	base.Env = options.Env
	return base
}

// MinAnswerTokens is the tokens always left for the answer when a thinking
// budget shares the response ceiling (pi: MIN_ANSWER_TOKENS).
const MinAnswerTokens = 1024

// DefaultThinkingBudgets is the token budget per thinking level (pi:
// DEFAULT_THINKING_BUDGETS).
func DefaultThinkingBudgets() types.ThinkingBudgets {
	return types.ThinkingBudgets{
		Minimal: lo.ToPtr(int64(1024)),
		Low:     lo.ToPtr(int64(2048)),
		Medium:  lo.ToPtr(int64(8192)),
		High:    lo.ToPtr(int64(16384)),
	}
}

// ClampReasoning folds the two top reasoning levels into "high" (pi:
// clampReasoning).
func ClampReasoning(effort types.ThinkingLevel) types.ThinkingLevel {
	if effort == types.ThinkingLevelXHigh || effort == types.ThinkingLevelMax {
		return types.ThinkingLevelHigh
	}
	return effort
}

// ThinkingBudgetForLevel returns the token budget for a reasoning level,
// merging customBudgets over the defaults (pi: thinkingBudgetForLevel).
func ThinkingBudgetForLevel(reasoningLevel types.ThinkingLevel, customBudgets *types.ThinkingBudgets) int64 {
	budgets := DefaultThinkingBudgets()
	if customBudgets != nil {
		if customBudgets.Minimal != nil {
			budgets.Minimal = customBudgets.Minimal
		}
		if customBudgets.Low != nil {
			budgets.Low = customBudgets.Low
		}
		if customBudgets.Medium != nil {
			budgets.Medium = customBudgets.Medium
		}
		if customBudgets.High != nil {
			budgets.High = customBudgets.High
		}
	}
	level := ClampReasoning(reasoningLevel)
	var budget *int64
	switch level {
	case types.ThinkingLevelMinimal:
		budget = budgets.Minimal
	case types.ThinkingLevelLow:
		budget = budgets.Low
	case types.ThinkingLevelMedium:
		budget = budgets.Medium
	case types.ThinkingLevelHigh:
		budget = budgets.High
	}
	if budget == nil {
		return 0
	}
	return *budget
}

// ClampThinkingBudgetToAnswerRoom caps a thinking budget so at least
// MinAnswerTokens remain under a shared response ceiling (pi:
// clampThinkingBudgetToAnswerRoom).
func ClampThinkingBudgetToAnswerRoom(thinkingBudget int64, ceiling int64) int64 {
	return minInt64(thinkingBudget, maxInt64(0, ceiling-MinAnswerTokens))
}

// AdjustMaxTokensForThinking fits a thinking budget inside the response
// ceiling (pi: adjustMaxTokensForThinking). A nil baseMaxTokens means no
// explicit caller cap: use the model cap and fit thinking inside it.
func AdjustMaxTokensForThinking(baseMaxTokens *int64, modelMaxTokens int64, reasoningLevel types.ThinkingLevel, customBudgets *types.ThinkingBudgets) (maxTokens int64, thinkingBudget int64) {
	thinkingBudget = ThinkingBudgetForLevel(reasoningLevel, customBudgets)
	if baseMaxTokens == nil {
		maxTokens = modelMaxTokens
	} else {
		maxTokens = minInt64(*baseMaxTokens+thinkingBudget, modelMaxTokens)
	}
	if maxTokens <= thinkingBudget {
		thinkingBudget = ClampThinkingBudgetToAnswerRoom(thinkingBudget, maxTokens)
	}
	return maxTokens, thinkingBudget
}

func stringsContains(s, substr string) bool { return strings.Contains(s, substr) }

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func minInt64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
