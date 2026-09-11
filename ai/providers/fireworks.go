// pi: providers/fireworks.ts.
package providers

import (
	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/api"
	"github.com/ZoroSpace/pig/ai/auth"
	"github.com/ZoroSpace/pig/ai/types"
)

// FireworksProvider is the built-in Fireworks provider (pi:
// fireworksProvider) — a dual-protocol provider: the catalog routes
// anthropic-messages and openai-completions models per model.
func FireworksProvider() ai.Provider {
	return ai.CreateProvider(&ai.CreateProviderOptions{
		ID:      "fireworks",
		Name:    "Fireworks",
		BaseURL: "https://api.fireworks.ai/inference",
		Auth: auth.ProviderAuth{
			APIKey: auth.EnvAPIKeyAuth("Fireworks API key", []string{"FIREWORKS_API_KEY"}),
		},
		Models: GetBuiltinModels(types.ProviderFireworks),
		APIByAPI: map[string]ai.ProviderStreams{
			string(types.ApiAnthropicMessages): api.AnthropicMessages{},
			string(types.ApiOpenAICompletions): api.OpenAICompletions{},
		},
	})
}
