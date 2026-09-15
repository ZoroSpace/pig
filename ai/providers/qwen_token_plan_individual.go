// pi: providers/qwen-token-plan-individual.ts.
package providers

import (
	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/api"
	"github.com/ZoroSpace/pig/ai/auth"
	"github.com/ZoroSpace/pig/ai/types"
)

// QwenTokenPlanIndividualProvider is the built-in Qwen Token Plan Individual
// provider (pi: qwenTokenPlanIndividualProvider). It reuses the international
// endpoint and the QWEN_TOKEN_PLAN_API_KEY variable with a narrower catalog.
func QwenTokenPlanIndividualProvider() ai.Provider {
	return ai.CreateProvider(&ai.CreateProviderOptions{
		ID:      "qwen-token-plan-individual",
		Name:    "Qwen Token Plan Individual",
		BaseURL: "https://token-plan.ap-southeast-1.maas.aliyuncs.com/compatible-mode/v1",
		Auth: auth.ProviderAuth{
			APIKey: auth.EnvAPIKeyAuth("Qwen Token Plan Individual API key", []string{"QWEN_TOKEN_PLAN_API_KEY"}),
		},
		Models: GetBuiltinModels(types.ProviderQwenTokenPlanInd),
		API:    api.OpenAICompletions{},
	})
}
