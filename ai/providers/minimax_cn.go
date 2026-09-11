// pi: providers/minimax-cn.ts.
package providers

import (
	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/api"
	"github.com/ZoroSpace/pig/ai/auth"
	"github.com/ZoroSpace/pig/ai/types"
)

// MinimaxCNProvider is the built-in MiniMax CN provider (pi:
// minimaxCnProvider).
func MinimaxCNProvider() ai.Provider {
	return ai.CreateProvider(&ai.CreateProviderOptions{
		ID:      "minimax-cn",
		Name:    "MiniMax CN",
		BaseURL: "https://api.minimaxi.com/anthropic",
		Auth: auth.ProviderAuth{
			APIKey: auth.EnvAPIKeyAuth("MiniMax CN API key", []string{"MINIMAX_CN_API_KEY"}),
		},
		Models: GetBuiltinModels(types.ProviderMinimaxCN),
		API:    api.AnthropicMessages{},
	})
}
