// pi: providers/moonshotai.ts.
package providers

import (
	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/api"
	"github.com/ZoroSpace/pig/ai/auth"
)

// MoonshotAIProvider is the built-in Moonshot AI provider (pi:
// moonshotaiProvider).
func MoonshotAIProvider() ai.Provider {
	return ai.CreateProvider(&ai.CreateProviderOptions{
		ID:      "moonshotai",
		Name:    "Moonshot AI",
		BaseURL: "https://api.moonshot.ai/v1",
		Auth: auth.ProviderAuth{
			APIKey: auth.EnvAPIKeyAuth("Moonshot AI API key", []string{"MOONSHOT_API_KEY"}),
		},
		Models: GetBuiltinModels(ai.ProviderMoonshotAI),
		// api: openAICompletionsApi() (pi: openAICompletionsApi(), lazy-loaded;
		// Go adapters compile in).
		API: api.OpenAICompletions{},
	})
}
