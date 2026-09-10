// pi: providers/deepseek.ts.
package providers

import (
	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/api"
	"github.com/ZoroSpace/pig/ai/auth"
)

// DeepSeekProvider is the built-in DeepSeek provider (pi: deepseekProvider).
func DeepSeekProvider() ai.Provider {
	return ai.CreateProvider(&ai.CreateProviderOptions{
		ID:      "deepseek",
		Name:    "DeepSeek",
		BaseURL: "https://api.deepseek.com",
		Auth: auth.ProviderAuth{
			APIKey: auth.EnvAPIKeyAuth("DeepSeek API key", []string{"DEEPSEEK_API_KEY"}),
		},
		Models: GetBuiltinModels(ai.ProviderDeepSeek),
		// api: openAICompletionsApi() — the openai-completions adapter (pi:
		// openAICompletionsApi(), lazy-loaded; Go adapters compile in).
		API: api.OpenAICompletions{},
	})
}
