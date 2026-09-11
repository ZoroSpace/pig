// pi: providers/vercel-ai-gateway.ts.
package providers

import (
	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/api"
	"github.com/ZoroSpace/pig/ai/auth"
	"github.com/ZoroSpace/pig/ai/types"
)

// VercelAIGatewayProvider is the built-in Vercel AI Gateway provider (pi:
// vercelAIGatewayProvider). The gateway serves both anthropic-messages and
// openai-completions models; models.json custom providers pick the API per
// model, while the builtin catalog entry routes per-model via APIByAPI.
func VercelAIGatewayProvider() ai.Provider {
	return ai.CreateProvider(&ai.CreateProviderOptions{
		ID:      "vercel-ai-gateway",
		Name:    "Vercel AI Gateway",
		BaseURL: "https://ai-gateway.vercel.sh",
		Auth: auth.ProviderAuth{
			APIKey: auth.EnvAPIKeyAuth("Vercel AI Gateway API key", []string{"AI_GATEWAY_API_KEY"}),
		},
		Models: GetBuiltinModels(types.ProviderVercelAIGateway),
		API:    api.AnthropicMessages{},
	})
}
