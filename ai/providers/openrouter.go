// pi: providers/openrouter.ts.
package providers

import (
	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/api"
	"github.com/ZoroSpace/pig/ai/auth"
)

// OpenRouterProvider is the built-in OpenRouter provider (pi:
// openrouterProvider).
func OpenRouterProvider() ai.Provider {
	return ai.CreateProvider(&ai.CreateProviderOptions{
		ID:      "openrouter",
		Name:    "OpenRouter",
		BaseURL: "https://openrouter.ai/api/v1",
		Auth: auth.ProviderAuth{
			APIKey: auth.EnvAPIKeyAuth("OpenRouter API key", []string{"OPENROUTER_API_KEY"}),
			// oauth: lazyOAuth({ name: "OpenRouter OAuth", loginLabel:
			// "Sign in with OpenRouter", load: loadOpenRouterOAuth }) —
			// arrives with the OAuth flows (auth/oauth/load.ts, README
			// Phase 2).
		},
		Models: GetBuiltinModels(ai.ProviderOpenRouter),
		// api: openAICompletionsApi() (pi: openAICompletionsApi(), lazy-loaded;
		// Go adapters compile in).
		API: api.OpenAICompletions{},
	})
}
