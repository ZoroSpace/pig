// pi: providers/cloudflare-ai-gateway.ts.
package providers

import (
	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/api"
	"github.com/ZoroSpace/pig/ai/auth"
	"github.com/ZoroSpace/pig/ai/types"
)

// CloudflareAIGatewayProvider is the built-in Cloudflare AI Gateway provider
// (pi: cloudflareAIGatewayProvider). The api map is pinned to all three APIs:
// models.dev's gateway catalog drops and restores `workers-ai/*`
// (openai-completions) entries over time, and inference from `models` alone
// would otherwise reject the openai-completions entry whenever the generated
// catalog happens to contain none.
func CloudflareAIGatewayProvider() ai.Provider {
	return ai.CreateProvider(&ai.CreateProviderOptions{
		ID:   "cloudflare-ai-gateway",
		Name: "Cloudflare AI Gateway",
		Auth: auth.ProviderAuth{
			APIKey: CloudflareAIGatewayAuth(),
		},
		Models: GetBuiltinModels(types.ProviderCloudflareAIGateway),
		APIByAPI: map[string]ai.ProviderStreams{
			string(types.ApiAnthropicMessages): CloudflareStreams(api.AnthropicMessages{}),
			string(types.ApiOpenAICompletions): CloudflareStreams(api.OpenAICompletions{}),
			string(types.ApiOpenAIResponses):   CloudflareStreams(api.OpenAIResponses{}),
		},
	})
}
