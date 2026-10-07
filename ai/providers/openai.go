// pi: providers/openai.ts.
package providers

import (
	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/api"
	"github.com/ZoroSpace/pig/ai/auth"
	"github.com/ZoroSpace/pig/ai/types"
)

// OpenAIProvider is the built-in OpenAI provider (pi: openaiProvider).
func OpenAIProvider() ai.Provider {
	return ai.CreateProvider(&ai.CreateProviderOptions{
		ID:      "openai",
		Name:    "OpenAI",
		BaseURL: "https://api.openai.com/v1",
		Auth: auth.ProviderAuth{
			APIKey: auth.EnvAPIKeyAuth("OpenAI API key", []string{"OPENAI_API_KEY"}),
			// pi v0.99.1 wires the openai provider's OAuth to the new
			// Sign in with ChatGPT flow (pi: lazyOAuth + loadOpenAIChatGPTOAuth;
			// Go flows are statically linked, no lazy wrapper).
			OAuth: auth.OpenAIChatGPTOAuth{},
		},
		Models: GetBuiltinModels(types.ProviderOpenAI),
		API:    api.OpenAIResponses{},
		// Sign in with ChatGPT tokens only reach the Responses API; the
		// Decisions API rejects them (pi: filterAllModels).
		FilterAllModels: func(models []ai.Model, credential auth.Credential) []ai.Model {
			if credential == nil || credential.CredentialType() != auth.CredentialTypeOAuth {
				return models
			}
			available := make([]ai.Model, 0, len(models))
			for _, model := range models {
				if model.Type == ai.ModelTypeClassifier {
					continue
				}
				available = append(available, model)
			}
			return available
		},
	})
}
