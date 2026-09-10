// pi: providers/together.ts.
package providers

import (
	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/api"
	"github.com/ZoroSpace/pig/ai/auth"
)

// TogetherProvider is the built-in Together provider (pi: togetherProvider).
func TogetherProvider() ai.Provider {
	return ai.CreateProvider(&ai.CreateProviderOptions{
		ID:      "together",
		Name:    "Together",
		BaseURL: "https://api.together.ai/v1",
		Auth: auth.ProviderAuth{
			APIKey: auth.EnvAPIKeyAuth("Together API key", []string{"TOGETHER_API_KEY"}),
		},
		Models: GetBuiltinModels(ai.ProviderTogether),
		// api: openAICompletionsApi() (pi: openAICompletionsApi(), lazy-loaded;
		// Go adapters compile in).
		API: api.OpenAICompletions{},
	})
}
