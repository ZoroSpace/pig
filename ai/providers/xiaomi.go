// pi: providers/xiaomi.ts.
package providers

import (
	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/api"
	"github.com/ZoroSpace/pig/ai/auth"
	"github.com/ZoroSpace/pig/ai/types"
)

// XiaomiProvider is the built-in Xiaomi provider (pi: xiaomiProvider).
func XiaomiProvider() ai.Provider {
	return ai.CreateProvider(&ai.CreateProviderOptions{
		ID:      "xiaomi",
		Name:    "Xiaomi",
		BaseURL: "https://api.xiaomimimo.com/v1",
		Auth: auth.ProviderAuth{
			APIKey: auth.EnvAPIKeyAuth("Xiaomi API key", []string{"XIAOMI_API_KEY"}),
		},
		Models: GetBuiltinModels(types.ProviderXiaomi),
		API:    api.OpenAICompletions{},
	})
}
