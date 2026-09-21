// pi: providers/meta.ts.
package providers

import (
	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/api"
	"github.com/ZoroSpace/pig/ai/auth"
	"github.com/ZoroSpace/pig/ai/types"
)

// MetaProvider is the built-in Meta provider (pi: metaProvider) — Meta Model
// API via the Muse subscription (OAuth) or META_API_KEY.
func MetaProvider() ai.Provider {
	return ai.CreateProvider(&ai.CreateProviderOptions{
		ID:      "meta",
		Name:    "Meta",
		BaseURL: "https://api.meta.ai/v1",
		Auth: auth.ProviderAuth{
			APIKey: auth.EnvAPIKeyAuth("Meta Model API key", []string{"META_API_KEY"}),
			OAuth:  auth.MetaOAuth{},
		},
		Models: GetBuiltinModels(types.ProviderMeta),
		API:    api.OpenAIResponses{},
	})
}
