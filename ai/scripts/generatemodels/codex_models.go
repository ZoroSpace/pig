// pi: generate-models.ts — the codexModels block (ChatGPT Codex / OAuth
// openai-codex-responses models, hardcoded outside models.dev). Older model
// limits are based on observed server behavior; GPT-5.6 and GPT-6 use
// Codex's 272k default catalog limit.
package main

import "github.com/ZoroSpace/pig/ai"

const (
	codexBaseURL      = "https://chatgpt.com/backend-api"
	codexContext      = 272000
	codexGpt56Context = 272000
	codexSparkContext = 128000
	codexMaxTokens    = 128000
)

// codexModels is pi's codexModels: a small, explicit list to avoid aliases.
func codexModels() []*ai.Model {
	return []*ai.Model{
		{
			ID: "gpt-6.1-sol", Name: "GPT-6.1 Sol",
			API: ai.ApiOpenAICodexResponses, Provider: ai.ProviderOpenAICodex, BaseURL: codexBaseURL,
			Reasoning: true, Input: []ai.ModelInput{ai.ModelInputText, ai.ModelInputImage},
			Cost:          withOpenAiLongContextPricing(mustStandardCost("gpt-6.1-sol")),
			ContextWindow: codexContext, MaxTokens: codexMaxTokens,
		},
		{
			ID: "gpt-6-astra", Name: "GPT-6 Astra",
			API: ai.ApiOpenAICodexResponses, Provider: ai.ProviderOpenAICodex, BaseURL: codexBaseURL,
			Reasoning: true, Input: []ai.ModelInput{ai.ModelInputText, ai.ModelInputImage},
			Cost:          withOpenAiLongContextPricing(mustStandardCost("gpt-6-astra")),
			ContextWindow: codexContext, MaxTokens: codexMaxTokens,
		},
		{
			ID: "gpt-6-sol", Name: "GPT-6 Sol",
			API: ai.ApiOpenAICodexResponses, Provider: ai.ProviderOpenAICodex, BaseURL: codexBaseURL,
			Reasoning: true, Input: []ai.ModelInput{ai.ModelInputText, ai.ModelInputImage},
			Cost:          withOpenAiLongContextPricing(mustStandardCost("gpt-6-sol")),
			ContextWindow: codexContext, MaxTokens: codexMaxTokens,
		},
		{
			ID: "gpt-6-luna", Name: "GPT-6 Luna",
			API: ai.ApiOpenAICodexResponses, Provider: ai.ProviderOpenAICodex, BaseURL: codexBaseURL,
			Reasoning: true, Input: []ai.ModelInput{ai.ModelInputText, ai.ModelInputImage},
			Cost:          withOpenAiLongContextPricing(mustStandardCost("gpt-6-luna")),
			ContextWindow: codexContext, MaxTokens: codexMaxTokens,
		},
		{
			ID: "gpt-5.3-codex-spark", Name: "GPT-5.3 Codex Spark",
			API: ai.ApiOpenAICodexResponses, Provider: ai.ProviderOpenAICodex, BaseURL: codexBaseURL,
			Reasoning: true, Input: []ai.ModelInput{ai.ModelInputText},
			Cost:          ai.ModelCost{ModelCostRates: ai.ModelCostRates{Input: 1.75, Output: 14, CacheRead: 0.175, CacheWrite: 0}},
			ContextWindow: codexSparkContext, MaxTokens: codexMaxTokens,
		},
		{
			ID: "gpt-5.5", Name: "GPT-5.5",
			API: ai.ApiOpenAICodexResponses, Provider: ai.ProviderOpenAICodex, BaseURL: codexBaseURL,
			Reasoning: true, Input: []ai.ModelInput{ai.ModelInputText, ai.ModelInputImage},
			Cost:          withOpenAiLongContextPricing(ai.ModelCost{ModelCostRates: ai.ModelCostRates{Input: 5, Output: 30, CacheRead: 0.5, CacheWrite: 0}}),
			ContextWindow: codexContext, MaxTokens: codexMaxTokens,
		},
		{
			ID: "gpt-5.6-luna", Name: "GPT-5.6 Luna",
			API: ai.ApiOpenAICodexResponses, Provider: ai.ProviderOpenAICodex, BaseURL: codexBaseURL,
			Reasoning: true, Input: []ai.ModelInput{ai.ModelInputText, ai.ModelInputImage},
			Cost:          withOpenAiLongContextPricing(mustStandardCost("gpt-5.6-luna")),
			ContextWindow: codexGpt56Context, MaxTokens: codexMaxTokens,
		},
		{
			ID: "gpt-5.6-sol", Name: "GPT-5.6 Sol",
			API: ai.ApiOpenAICodexResponses, Provider: ai.ProviderOpenAICodex, BaseURL: codexBaseURL,
			Reasoning: true, Input: []ai.ModelInput{ai.ModelInputText, ai.ModelInputImage},
			Cost:          withOpenAiLongContextPricing(mustStandardCost("gpt-5.6-sol")),
			ContextWindow: codexGpt56Context, MaxTokens: codexMaxTokens,
		},
		{
			ID: "gpt-5.6-terra", Name: "GPT-5.6 Terra",
			API: ai.ApiOpenAICodexResponses, Provider: ai.ProviderOpenAICodex, BaseURL: codexBaseURL,
			Reasoning: true, Input: []ai.ModelInput{ai.ModelInputText, ai.ModelInputImage},
			Cost:          withOpenAiLongContextPricing(mustStandardCost("gpt-5.6-terra")),
			ContextWindow: codexGpt56Context, MaxTokens: codexMaxTokens,
		},
	}
}
