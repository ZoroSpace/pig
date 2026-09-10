// pi: providers/zai-coding-cn.ts.
package providers

import (
	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/api"
	"github.com/ZoroSpace/pig/ai/auth"
)

// ZaiCodingCNProvider is the built-in Z.AI Coding CN provider (pi:
// zaiCodingCnProvider).
func ZaiCodingCNProvider() ai.Provider {
	return ai.CreateProvider(&ai.CreateProviderOptions{
		ID:      "zai-coding-cn",
		Name:    "Z.AI Coding CN",
		BaseURL: "https://open.bigmodel.cn/api/coding/paas/v4",
		Auth: auth.ProviderAuth{
			APIKey: auth.EnvAPIKeyAuth("Z.AI Coding CN API key", []string{"ZAI_CODING_CN_API_KEY"}),
		},
		Models: GetBuiltinModels(ai.ProviderZaiCodingCN),
		// api: openAICompletionsApi() (pi: openAICompletionsApi(), lazy-loaded;
		// Go adapters compile in).
		API: api.OpenAICompletions{},
	})
}
