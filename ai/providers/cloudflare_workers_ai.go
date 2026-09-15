// pi: providers/cloudflare-workers-ai.ts.
package providers

import (
	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/api"
	"github.com/ZoroSpace/pig/ai/auth"
	"github.com/ZoroSpace/pig/ai/types"
)

// CloudflareWorkersAIProvider is the built-in Cloudflare Workers AI provider
// (pi: cloudflareWorkersAIProvider).
func CloudflareWorkersAIProvider() ai.Provider {
	return ai.CreateProvider(&ai.CreateProviderOptions{
		ID:   "cloudflare-workers-ai",
		Name: "Cloudflare Workers AI",
		Auth: auth.ProviderAuth{
			APIKey: CloudflareWorkersAIAuth(),
		},
		Models: GetBuiltinModels(types.ProviderCloudflareWorkersAI),
		API:    CloudflareStreams(api.OpenAICompletions{}),
	})
}
