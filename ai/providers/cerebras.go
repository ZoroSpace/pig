// pi: providers/cerebras.ts.
package providers

import (
	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/api"
	"github.com/ZoroSpace/pig/ai/auth"
)

// CerebrasProvider is the built-in Cerebras provider (pi: cerebrasProvider).
func CerebrasProvider() ai.Provider {
	return ai.CreateProvider(&ai.CreateProviderOptions{
		ID:      "cerebras",
		Name:    "Cerebras",
		BaseURL: "https://api.cerebras.ai/v1",
		Auth: auth.ProviderAuth{
			APIKey: auth.EnvAPIKeyAuth("Cerebras API key", []string{"CEREBRAS_API_KEY"}),
		},
		Models: GetBuiltinModels(ai.ProviderCerebras),
		// api: openAICompletionsApi() (pi: openAICompletionsApi(), lazy-loaded;
		// Go adapters compile in).
		API: api.OpenAICompletions{},
	})
}
