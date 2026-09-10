// pi: providers/ant-ling.ts.
package providers

import (
	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/api"
	"github.com/ZoroSpace/pig/ai/auth"
)

// AntLingProvider is the built-in Ant Ling provider (pi: antLingProvider).
func AntLingProvider() ai.Provider {
	return ai.CreateProvider(&ai.CreateProviderOptions{
		ID:      "ant-ling",
		Name:    "Ant Ling",
		BaseURL: "https://api.ant-ling.com/v1",
		Auth: auth.ProviderAuth{
			APIKey: auth.EnvAPIKeyAuth("Ant Ling API key", []string{"ANT_LING_API_KEY"}),
		},
		Models: GetBuiltinModels(ai.ProviderAntLing),
		// api: openAICompletionsApi() (pi: openAICompletionsApi(), lazy-loaded;
		// Go adapters compile in).
		API: api.OpenAICompletions{},
	})
}
