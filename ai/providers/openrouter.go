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
			// pi: lazyOAuth({ name: "OpenRouter OAuth", loginLabel:
			// "Sign in with OpenRouter", load: loadOpenRouterOAuth }) —
			// filled in by S10-2 (Go flows are statically linked).
			OAuth: auth.OpenRouterOAuth{},
		},
		// pi: models = OPENROUTER_MODELS + OPENROUTER_IMAGE_MODELS +
		// OPENROUTER_CLASSIFIER_MODELS — the #9948 mixed catalog; chat-only
		// narrowing happens at Models.GetModels (pi: getModels filter).
		Models: append(append(GetBuiltinModels(ai.ProviderOpenRouter),
			GetBuiltinImageModels(ai.ProviderOpenRouter)...),
			GetBuiltinClassifierModels(ai.ProviderOpenRouter)...),
		// api: openAICompletionsApi() (pi: openAICompletionsApi(), lazy-loaded;
		// Go adapters compile in).
		API: api.OpenAICompletions{},
	})
}
