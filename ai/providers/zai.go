// pi: providers/zai.ts.
package providers

import (
	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/api"
	"github.com/ZoroSpace/pig/ai/auth"
)

// ZaiProvider is the built-in Z.AI provider (pi: zaiProvider).
func ZaiProvider() ai.Provider {
	return ai.CreateProvider(&ai.CreateProviderOptions{
		ID:      "zai",
		Name:    "Z.AI",
		BaseURL: "https://api.z.ai/api/coding/paas/v4",
		Auth: auth.ProviderAuth{
			APIKey: auth.EnvAPIKeyAuth("Z.AI API key", []string{"ZAI_API_KEY"}),
		},
		Models: GetBuiltinModels(ai.ProviderZai),
		// api: openAICompletionsApi() (pi: openAICompletionsApi(), lazy-loaded;
		// Go adapters compile in).
		API: api.OpenAICompletions{},
	})
}
