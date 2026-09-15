// pi: providers/qwen-token-plan-cn.ts.
package providers

import (
	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/api"
	"github.com/ZoroSpace/pig/ai/auth"
	"github.com/ZoroSpace/pig/ai/types"
)

// QwenTokenPlanCNProvider is the built-in Qwen Token Plan CN provider (pi:
// qwenTokenPlanCnProvider).
func QwenTokenPlanCNProvider() ai.Provider {
	return ai.CreateProvider(&ai.CreateProviderOptions{
		ID:      "qwen-token-plan-cn",
		Name:    "Qwen Token Plan CN",
		BaseURL: "https://token-plan.cn-beijing.maas.aliyuncs.com/compatible-mode/v1",
		Auth: auth.ProviderAuth{
			APIKey: auth.EnvAPIKeyAuth("Qwen Token Plan CN API key", []string{"QWEN_TOKEN_PLAN_CN_API_KEY"}),
		},
		Models: GetBuiltinModels(types.ProviderQwenTokenPlanCN),
		API:    api.OpenAICompletions{},
	})
}
