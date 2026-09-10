// pi: providers/moonshotai-cn.ts.
package providers

import (
	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/api"
	"github.com/ZoroSpace/pig/ai/auth"
)

// MoonshotAICNProvider is the built-in Moonshot AI CN provider (pi:
// moonshotaiCnProvider).
func MoonshotAICNProvider() ai.Provider {
	return ai.CreateProvider(&ai.CreateProviderOptions{
		ID:      "moonshotai-cn",
		Name:    "Moonshot AI CN",
		BaseURL: "https://api.moonshot.cn/v1",
		Auth: auth.ProviderAuth{
			APIKey: auth.EnvAPIKeyAuth("Moonshot AI API key", []string{"MOONSHOT_API_KEY"}),
		},
		Models: GetBuiltinModels(ai.ProviderMoonshotAICN),
		// api: openAICompletionsApi() (pi: openAICompletionsApi(), lazy-loaded;
		// Go adapters compile in).
		API: api.OpenAICompletions{},
	})
}
