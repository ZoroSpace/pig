// pi: providers/baseten.ts.
package providers

import (
	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/api"
	"github.com/ZoroSpace/pig/ai/auth"
)

// BasetenProvider is the built-in Baseten provider (pi: basetenProvider).
func BasetenProvider() ai.Provider {
	return ai.CreateProvider(&ai.CreateProviderOptions{
		ID:      "baseten",
		Name:    "Baseten",
		BaseURL: "https://inference.baseten.co/v1",
		Auth: auth.ProviderAuth{
			APIKey: auth.EnvAPIKeyAuth("Baseten API key", []string{"BASETEN_API_KEY"}),
		},
		Models: GetBuiltinModels(ai.ProviderBaseten),
		// api: openAICompletionsApi() (pi: openAICompletionsApi(), lazy-loaded;
		// Go adapters compile in).
		API: api.OpenAICompletions{},
	})
}
