// pi: providers/xiaomi-token-plan-ams.ts.
package providers

import (
	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/api"
	"github.com/ZoroSpace/pig/ai/auth"
	"github.com/ZoroSpace/pig/ai/types"
)

// XiaomiTokenPlanAMSProvider is the built-in Xiaomi Token Plan AMS provider
// (pi: xiaomiTokenPlanAmsProvider).
func XiaomiTokenPlanAMSProvider() ai.Provider {
	return ai.CreateProvider(&ai.CreateProviderOptions{
		ID:      "xiaomi-token-plan-ams",
		Name:    "Xiaomi Token Plan AMS",
		BaseURL: "https://token-plan-ams.xiaomimimo.com/v1",
		Auth: auth.ProviderAuth{
			APIKey: auth.EnvAPIKeyAuth("Xiaomi Token Plan AMS API key", []string{"XIAOMI_TOKEN_PLAN_AMS_API_KEY"}),
		},
		Models: GetBuiltinModels(types.ProviderXiaomiTokenPlanAMS),
		API:    api.OpenAICompletions{},
	})
}
