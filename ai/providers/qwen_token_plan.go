// pi: providers/qwen-token-plan.ts.
package providers

import (
	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/api"
	"github.com/ZoroSpace/pig/ai/auth"
	"github.com/ZoroSpace/pig/ai/types"
)

// QwenTokenPlanProvider is the built-in Qwen Token Plan provider (pi:
// qwenTokenPlanProvider).
func QwenTokenPlanProvider() ai.Provider {
	return ai.CreateProvider(&ai.CreateProviderOptions{
		ID:      "qwen-token-plan",
		Name:    "Qwen Token Plan",
		BaseURL: "https://token-plan.ap-southeast-1.maas.aliyuncs.com/compatible-mode/v1",
		Auth: auth.ProviderAuth{
			APIKey: auth.EnvAPIKeyAuth("Qwen Token Plan API key", []string{"QWEN_TOKEN_PLAN_API_KEY"}),
		},
		Models: GetBuiltinModels(types.ProviderQwenTokenPlan),
		API:    api.OpenAICompletions{},
	})
}
