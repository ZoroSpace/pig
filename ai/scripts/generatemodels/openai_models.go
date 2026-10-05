// pi: generate-models.ts — the openai-responses catalog blocks that arrived
// with the B2 adapter: long-context pricing helpers, missingOpenAiModels,
// and the Azure Foundry clone.
package main

import (
	"github.com/samber/lo"

	"github.com/ZoroSpace/pig/ai"
)

// openAILongContextInputThreshold is pi's OPENAI_LONG_CONTEXT_INPUT_THRESHOLD.
const openAILongContextInputThreshold = 272000

// openAIShortContextCappedModelIds lists the OpenAI models kept in the
// short-context pricing tier by default (pi:
// OPENAI_SHORT_CONTEXT_CAPPED_MODEL_IDS).
var openAIShortContextCappedModelIds = stringSet(
	"gpt-5.4", "gpt-5.5", "gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna", "gpt-6-astra", "gpt-6-sol", "gpt-6-luna",
	"gpt-6.1-sol",
)

// openAILongContextPricingModelIds lists the models carrying long-context
// tier pricing (pi: OPENAI_LONG_CONTEXT_PRICING_MODEL_IDS).
var openAILongContextPricingModelIds = stringSet(
	"gpt-5.4", "gpt-5.4-pro", "gpt-5.5", "gpt-5.5-pro", "gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna",
	"gpt-6-astra", "gpt-6-sol", "gpt-6-luna", "gpt-6.1-sol",
)

// openAIStandardCosts keeps the current OpenAI prices authoritative until
// models.dev and passthrough catalogs catch up (pi:
// OPENAI_STANDARD_COSTS — renamed from OPENAI_GPT_56_STANDARD_COSTS by
// db91e0331). https://developers.openai.com/api/docs/pricing
var openAIStandardCosts = map[string]ai.ModelCost{
	"gpt-5.6-luna":  {ModelCostRates: ai.ModelCostRates{Input: 0.2, Output: 1.2, CacheRead: 0.02, CacheWrite: 0.25}},
	"gpt-5.6-sol":   {ModelCostRates: ai.ModelCostRates{Input: 4, Output: 20, CacheRead: 0.4, CacheWrite: 5}},
	"gpt-5.6-terra": {ModelCostRates: ai.ModelCostRates{Input: 2, Output: 12, CacheRead: 0.2, CacheWrite: 2.5}},
	"gpt-6-astra":   {ModelCostRates: ai.ModelCostRates{Input: 10, Output: 50, CacheRead: 1, CacheWrite: 12.5}},
	"gpt-6-luna":    {ModelCostRates: ai.ModelCostRates{Input: 0.1, Output: 0.5, CacheRead: 0.01, CacheWrite: 0.125}},
	"gpt-6-sol":     {ModelCostRates: ai.ModelCostRates{Input: 2, Output: 10, CacheRead: 0.2, CacheWrite: 2.5}},
	"gpt-6.1-sol":   {ModelCostRates: ai.ModelCostRates{Input: 2, Output: 10, CacheRead: 0.1, CacheWrite: 2.5}},
}

// withOpenAiLongContextPricing appends the long-context tier to a cost (pi:
// withOpenAiLongContextPricing): the base cost stays the short-context
// tier, and requests above the threshold use 2x input/cache rates and 1.5x
// output.
func withOpenAiLongContextPricing(cost ai.ModelCost) ai.ModelCost {
	cost.Tiers = append(cost.Tiers, ai.ModelCostTier{
		InputTokensAbove: openAILongContextInputThreshold,
		ModelCostRates: ai.ModelCostRates{
			Input:      roundCost(cost.Input * 2),
			Output:     roundCost(cost.Output * 1.5),
			CacheRead:  roundCost(cost.CacheRead * 2),
			CacheWrite: roundCost(cost.CacheWrite * 2),
		},
	})
	return cost
}

// missingOpenAiModels is pi's missingOpenAiModels: catalog models absent
// from models.dev.
func missingOpenAiModels() []*ai.Model {
	return []*ai.Model{
		{
			ID: "gpt-6.1-sol", Name: "GPT-6.1 Sol",
			API: ai.ApiOpenAIResponses, Provider: ai.ProviderOpenAI, BaseURL: "https://api.openai.com/v1",
			Reasoning: true, Input: []ai.ModelInput{ai.ModelInputText, ai.ModelInputImage},
			Cost:          withOpenAiLongContextPricing(mustStandardCost("gpt-6.1-sol")),
			ContextWindow: openAILongContextInputThreshold, MaxTokens: 128000,
		},
		{
			ID: "gpt-6-astra", Name: "GPT-6 Astra",
			API: ai.ApiOpenAIResponses, Provider: ai.ProviderOpenAI, BaseURL: "https://api.openai.com/v1",
			Reasoning: true, Input: []ai.ModelInput{ai.ModelInputText, ai.ModelInputImage},
			Cost:          withOpenAiLongContextPricing(mustStandardCost("gpt-6-astra")),
			ContextWindow: openAILongContextInputThreshold, MaxTokens: 128000,
		},
		{
			ID: "gpt-6-sol", Name: "GPT-6 Sol",
			API: ai.ApiOpenAIResponses, Provider: ai.ProviderOpenAI, BaseURL: "https://api.openai.com/v1",
			Reasoning: true, Input: []ai.ModelInput{ai.ModelInputText, ai.ModelInputImage},
			Cost:          withOpenAiLongContextPricing(mustStandardCost("gpt-6-sol")),
			ContextWindow: openAILongContextInputThreshold, MaxTokens: 128000,
		},
		{
			ID: "gpt-6-luna", Name: "GPT-6 Luna",
			API: ai.ApiOpenAIResponses, Provider: ai.ProviderOpenAI, BaseURL: "https://api.openai.com/v1",
			Reasoning: true, Input: []ai.ModelInput{ai.ModelInputText, ai.ModelInputImage},
			Cost:          withOpenAiLongContextPricing(mustStandardCost("gpt-6-luna")),
			ContextWindow: openAILongContextInputThreshold, MaxTokens: 128000,
		},
		{
			ID: "gpt-5.6-sol", Name: "GPT-5.6 Sol",
			API: ai.ApiOpenAIResponses, Provider: ai.ProviderOpenAI, BaseURL: "https://api.openai.com/v1",
			Reasoning: true, Input: []ai.ModelInput{ai.ModelInputText, ai.ModelInputImage},
			Cost:          withOpenAiLongContextPricing(mustStandardCost("gpt-5.6-sol")),
			ContextWindow: openAILongContextInputThreshold, MaxTokens: 128000,
		},
		{
			ID: "gpt-5.6-terra", Name: "GPT-5.6 Terra",
			API: ai.ApiOpenAIResponses, Provider: ai.ProviderOpenAI, BaseURL: "https://api.openai.com/v1",
			Reasoning: true, Input: []ai.ModelInput{ai.ModelInputText, ai.ModelInputImage},
			Cost:          withOpenAiLongContextPricing(mustStandardCost("gpt-5.6-terra")),
			ContextWindow: openAILongContextInputThreshold, MaxTokens: 128000,
		},
		{
			ID: "gpt-5.6-luna", Name: "GPT-5.6 Luna",
			API: ai.ApiOpenAIResponses, Provider: ai.ProviderOpenAI, BaseURL: "https://api.openai.com/v1",
			Reasoning: true, Input: []ai.ModelInput{ai.ModelInputText, ai.ModelInputImage},
			Cost:          withOpenAiLongContextPricing(mustStandardCost("gpt-5.6-luna")),
			ContextWindow: openAILongContextInputThreshold, MaxTokens: 128000,
		},
		{
			ID: "gpt-5-chat-latest", Name: "GPT-5 Chat Latest",
			API: ai.ApiOpenAIResponses, Provider: ai.ProviderOpenAI, BaseURL: "https://api.openai.com/v1",
			Reasoning: false, Input: []ai.ModelInput{ai.ModelInputText, ai.ModelInputImage},
			Cost:          ai.ModelCost{ModelCostRates: ai.ModelCostRates{Input: 1.25, Output: 10, CacheRead: 0.125, CacheWrite: 0}},
			ContextWindow: 128000, MaxTokens: 16384,
		},
	}
}

// mustStandardCost looks up OPENAI_STANDARD_COSTS (pi indexes the record
// directly).
func mustStandardCost(modelID string) ai.ModelCost {
	cost, ok := openAIStandardCosts[modelID]
	if !ok {
		panic("missing OPENAI_GPT_56_STANDARD_COSTS entry: " + modelID)
	}
	return cost
}

// azureContextWindowOverrides lists Azure Foundry context-window overrides
// (pi: AZURE_CONTEXT_WINDOW_OVERRIDES).
var azureContextWindowOverrides = map[string]int64{
	"gpt-5.4":       1050000,
	"gpt-5.5":       1050000,
	"gpt-5.6-luna":  1050000,
	"gpt-5.6-sol":   1050000,
	"gpt-5.6-terra": 1050000,
}

// cloneAzureOpenAIModels clones the openai openai-responses models into the
// azure provider (pi: the azureOpenAiModels clone; v1.0.3 #9645 renamed the
// provider from "azure-openai-responses" to "azure"). The
// spread keeps thinkingLevelMap/compat/samplingParams/headers; only api,
// provider, baseUrl, the flat cost rates, and the context window change.
func cloneAzureOpenAIModels(allModels []*ai.Model) []*ai.Model {
	var out []*ai.Model
	for _, model := range allModels {
		if model.Provider != ai.ProviderOpenAI || model.API != ai.ApiOpenAIResponses {
			continue
		}
		contextWindow := model.ContextWindow
		if override, ok := azureContextWindowOverrides[model.ID]; ok {
			contextWindow = override
		}
		clone := *model
		clone.API = ai.ApiAzureOpenAIResponses
		clone.Provider = ai.ProviderAzure
		clone.BaseURL = ""
		clone.Cost = ai.ModelCost{ModelCostRates: model.Cost.ModelCostRates}
		clone.ContextWindow = contextWindow
		// pi's metadata merges are copy-on-write ({...model.compat, ...patch}),
		// so the clone keeps the snapshot taken here while later merges only
		// affect the original. Reproduce that with deep copies of the two
		// shared mutable members.
		if model.ThinkingLevelMap != nil {
			clone.ThinkingLevelMap = cloneThinkingLevelMap(model.ThinkingLevelMap)
		}
		if compat, ok := model.Compat.(*ai.OpenAIResponsesCompat); ok && compat != nil {
			clone.Compat = copyOpenAIResponsesCompat(compat)
		}
		out = append(out, &clone)
	}
	return out
}

// azureDeepSeekV4ProCost is Azure's DeepSeek resale pricing — US data zone,
// checked 2026-09-16 (pi: AZURE_DEEPSEEK_V4_PRO_COST).
// https://azure.microsoft.com/en-us/pricing/details/ai-foundry-models/deepseek/
var azureDeepSeekV4ProCost = ai.ModelCostRates{Input: 1.925, Output: 3.828, CacheRead: 0.165, CacheWrite: 0}

// cloneAzureDeepSeekModels clones DeepSeek's deepseek-v4-pro into the azure
// provider as an openai-completions model (pi: the azureDeepSeekModels
// clone, v1.0.3 #9645). Azure 400s on DeepSeek's `thinking` field and on
// every prompt cache parameter, discards a `developer` system message
// unbilled once reasoning_effort is set, and honours mid-convo ones.
func cloneAzureDeepSeekModels(allModels []*ai.Model) []*ai.Model {
	var out []*ai.Model
	for _, model := range allModels {
		if model.Provider != ai.ProviderDeepSeek || model.ID != "deepseek-v4-pro" {
			continue
		}
		clone := *model
		clone.Provider = ai.ProviderAzure
		clone.BaseURL = ""
		clone.Cost = ai.ModelCost{ModelCostRates: azureDeepSeekV4ProCost}
		if model.ThinkingLevelMap != nil {
			clone.ThinkingLevelMap = cloneThinkingLevelMap(model.ThinkingLevelMap)
		}
		if compat, ok := model.Compat.(*ai.OpenAICompletionsCompat); ok && compat != nil {
			clone.Compat = withCompat(compat, func(c *ai.OpenAICompletionsCompat) {
				c.SupportsDeveloperRole = lo.ToPtr(false)
				c.SupportsMidConvoSystemMessages = lo.ToPtr(true)
				c.ThinkingFormat = lo.ToPtr(ai.ThinkingFormatOpenAI)
				c.SupportsLongCacheRetention = lo.ToPtr(false)
			})
		}
		out = append(out, &clone)
	}
	return out
}
