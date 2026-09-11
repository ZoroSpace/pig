// pi: providers/xai.ts.
package providers

import (
	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/api"
	"github.com/ZoroSpace/pig/ai/auth"
	"github.com/ZoroSpace/pig/ai/types"
)

// XaiProvider is the built-in xAI provider (pi: xaiProvider).
func XaiProvider() ai.Provider {
	return ai.CreateProvider(&ai.CreateProviderOptions{
		ID:      "xai",
		Name:    "xAI",
		BaseURL: "https://api.x.ai/v1",
		Auth: auth.ProviderAuth{
			APIKey: auth.EnvAPIKeyAuth("xAI API key", []string{"XAI_API_KEY"}),
			OAuth:  auth.XaiOAuth{},
		},
		Models: GetBuiltinModels(types.ProviderXAI),
		API:    api.OpenAIResponses{},
	})
}
