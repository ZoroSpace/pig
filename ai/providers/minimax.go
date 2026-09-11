// pi: providers/minimax.ts.
package providers

import (
	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/api"
	"github.com/ZoroSpace/pig/ai/auth"
	"github.com/ZoroSpace/pig/ai/types"
)

// MinimaxProvider is the built-in MiniMax provider (pi: minimaxProvider).
func MinimaxProvider() ai.Provider {
	return ai.CreateProvider(&ai.CreateProviderOptions{
		ID:      "minimax",
		Name:    "MiniMax",
		BaseURL: "https://api.minimax.io/anthropic",
		Auth: auth.ProviderAuth{
			APIKey: auth.EnvAPIKeyAuth("MiniMax API key", []string{"MINIMAX_API_KEY"}),
		},
		Models: GetBuiltinModels(types.ProviderMinimax),
		API:    api.AnthropicMessages{},
	})
}
