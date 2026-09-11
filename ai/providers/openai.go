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
		},
		Models: GetBuiltinModels(types.ProviderOpenAI),
		API:    api.OpenAIResponses{},
	})
}
