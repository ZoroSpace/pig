// pi: providers/opencode-go.ts.
package providers

import (
	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/api"
	"github.com/ZoroSpace/pig/ai/auth"
	"github.com/ZoroSpace/pig/ai/types"
)

// OpenCodeGoProvider is the built-in OpenCode Go provider (pi:
// opencodeGoProvider) — a four-protocol provider; the catalog routes per
// model.
func OpenCodeGoProvider() ai.Provider {
	return ai.CreateProvider(&ai.CreateProviderOptions{
		ID:   "opencode-go",
		Name: "OpenCode Go",
		Auth: auth.ProviderAuth{
			APIKey: auth.EnvAPIKeyAuth("OpenCode API key", []string{"OPENCODE_API_KEY"}),
		},
		Models: GetBuiltinModels(types.ProviderOpenCodeGo),
		// v0.86.0: session-header wrapper (pi: #9326). v1.3.0 adds
		// google-generative-ai (the /inference/go base has a Gemini route).
		APIByAPI: map[string]ai.ProviderStreams{
			string(types.ApiAnthropicMessages):  WithOpenCodeSessionHeader(api.AnthropicMessages{}),
			string(types.ApiGoogleGenerativeAI): WithOpenCodeSessionHeader(api.GoogleGenerativeAI{}),
			string(types.ApiOpenAICompletions):  WithOpenCodeSessionHeader(api.OpenAICompletions{}),
			string(types.ApiOpenAIResponses):    WithOpenCodeSessionHeader(api.OpenAIResponses{}),
		},
	})
}
