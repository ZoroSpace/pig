// pi: providers/xiaomi-token-plan-sgp.ts.
package providers

import (
	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/api"
	"github.com/ZoroSpace/pig/ai/auth"
	"github.com/ZoroSpace/pig/ai/types"
)

// XiaomiTokenPlanSGPProvider is the built-in Xiaomi Token Plan SGP provider
// (pi: xiaomiTokenPlanSgpProvider).
func XiaomiTokenPlanSGPProvider() ai.Provider {
	return ai.CreateProvider(&ai.CreateProviderOptions{
		ID:      "xiaomi-token-plan-sgp",
		Name:    "Xiaomi Token Plan SGP",
		BaseURL: "https://token-plan-sgp.xiaomimimo.com/v1",
		Auth: auth.ProviderAuth{
			APIKey: auth.EnvAPIKeyAuth("Xiaomi Token Plan SGP API key", []string{"XIAOMI_TOKEN_PLAN_SGP_API_KEY"}),
		},
		Models: GetBuiltinModels(types.ProviderXiaomiTokenPlanSGP),
		API:    api.OpenAICompletions{},
	})
}
