// pi: providers/kimi-coding.ts.
package providers

import (
	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/api"
	"github.com/ZoroSpace/pig/ai/auth"
	"github.com/ZoroSpace/pig/ai/types"
)

// KimiCodingProvider is the built-in Kimi For Coding provider (pi:
// kimiCodingProvider).
func KimiCodingProvider() ai.Provider {
	return ai.CreateProvider(&ai.CreateProviderOptions{
		ID:      "kimi-coding",
		Name:    "Kimi For Coding",
		BaseURL: "https://api.kimi.com/coding",
		Auth: auth.ProviderAuth{
			APIKey: auth.EnvAPIKeyAuth("Kimi API key", []string{"KIMI_API_KEY"}),
			OAuth:  auth.KimiCodingOAuth{},
		},
		Models: GetBuiltinModels(types.ProviderKimiCoding),
		API:    api.AnthropicMessages{},
	})
}
