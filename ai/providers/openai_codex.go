// pi: providers/openai-codex.ts.
package providers

import (
	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/api"
	"github.com/ZoroSpace/pig/ai/auth"
	"github.com/ZoroSpace/pig/ai/types"
)

// OpenAICodexProvider is the built-in OpenAI Codex provider (pi:
// openaiCodexProvider). OAuth-only auth (pi wraps the flow in lazyOAuth for
// bundle splitting, which has no Go counterpart — see auth/helpers.go).
func OpenAICodexProvider() ai.Provider {
	return ai.CreateProvider(&ai.CreateProviderOptions{
		ID:      "openai-codex",
		Name:    "OpenAI Codex",
		BaseURL: "https://chatgpt.com/backend-api",
		Auth: auth.ProviderAuth{
			OAuth: auth.OpenAICodexOAuth{},
		},
		Models: GetBuiltinModels(types.ProviderOpenAICodex),
		API:    api.OpenAICodexResponses{},
	})
}
