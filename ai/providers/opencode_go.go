// pi: providers/opencode-go.ts.
package providers

import (
	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/api"
	"github.com/ZoroSpace/pig/ai/auth"
	"github.com/ZoroSpace/pig/ai/types"
)

// OpenCodeGoProvider is the built-in OpenCode Go provider (pi:
// opencodeGoProvider) — a three-protocol provider; the catalog routes per
// model.
func OpenCodeGoProvider() ai.Provider {
	return ai.CreateProvider(&ai.CreateProviderOptions{
		ID:   "opencode-go",
		Name: "OpenCode Go",
		Auth: auth.ProviderAuth{
			APIKey: auth.EnvAPIKeyAuth("OpenCode API key", []string{"OPENCODE_API_KEY"}),
		},
		Models: GetBuiltinModels(types.ProviderOpenCodeGo),
		APIByAPI: map[string]ai.ProviderStreams{
			string(types.ApiAnthropicMessages): api.AnthropicMessages{},
			string(types.ApiOpenAICompletions): api.OpenAICompletions{},
			string(types.ApiOpenAIResponses):   api.OpenAIResponses{},
		},
	})
}
