// pi: providers/opencode.ts.
package providers

import (
	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/api"
	"github.com/ZoroSpace/pig/ai/auth"
	"github.com/ZoroSpace/pig/ai/types"
)

// OpenCodeProvider is the built-in OpenCode Zen provider (pi:
// opencodeProvider) — a four-protocol provider; the catalog routes per model.
func OpenCodeProvider() ai.Provider {
	return ai.CreateProvider(&ai.CreateProviderOptions{
		ID:   "opencode",
		Name: "OpenCode Zen",
		Auth: auth.ProviderAuth{
			APIKey: auth.EnvAPIKeyAuth("OpenCode API key", []string{"OPENCODE_API_KEY"}),
		},
		Models: GetBuiltinModels(types.ProviderOpenCode),
		// v0.86.0: every entry rides the OpenCode session-header wrapper
		// (pi: withOpenCodeSessionHeader, regression #9326).
		APIByAPI: map[string]ai.ProviderStreams{
			string(types.ApiAnthropicMessages):  WithOpenCodeSessionHeader(api.AnthropicMessages{}),
			string(types.ApiGoogleGenerativeAI): WithOpenCodeSessionHeader(api.GoogleGenerativeAI{}),
			string(types.ApiOpenAICompletions):  WithOpenCodeSessionHeader(api.OpenAICompletions{}),
			string(types.ApiOpenAIResponses):    WithOpenCodeSessionHeader(api.OpenAIResponses{}),
		},
	})
}
