// pi: providers/xiaomi-token-plan-cn.ts.
package providers

import (
	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/api"
	"github.com/ZoroSpace/pig/ai/auth"
	"github.com/ZoroSpace/pig/ai/types"
)

// XiaomiTokenPlanCNProvider is the built-in Xiaomi Token Plan CN provider (pi:
// xiaomiTokenPlanCnProvider).
func XiaomiTokenPlanCNProvider() ai.Provider {
	return ai.CreateProvider(&ai.CreateProviderOptions{
		ID:      "xiaomi-token-plan-cn",
		Name:    "Xiaomi Token Plan CN",
		BaseURL: "https://token-plan-cn.xiaomimimo.com/v1",
		Auth: auth.ProviderAuth{
			APIKey: auth.EnvAPIKeyAuth("Xiaomi Token Plan CN API key", []string{"XIAOMI_TOKEN_PLAN_CN_API_KEY"}),
		},
		Models: GetBuiltinModels(types.ProviderXiaomiTokenPlanCN),
		API:    api.OpenAICompletions{},
	})
}
