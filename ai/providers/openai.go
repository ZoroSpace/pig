// pi: providers/openai.ts.
package providers

import (
	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/auth"
)

// OpenAIProvider is the built-in OpenAI provider (pi: openaiProvider).
func OpenAIProvider() ai.Provider {
	return ai.CreateProvider(&ai.CreateProviderOptions{
		ID:      "openai",
		Name:    "OpenAI",
		BaseURL: "https://api.openai.com/v1",
		Auth: auth.ProviderAuth{
			APIKey: auth.EnvAPIKeyAuth("OpenAI API key", []string{"OPENAI_API_KEY"}),
		},
		// models: Object.values(OPENAI_MODELS) — openai models are served
		// through openai-responses, so they are skipped in ai/models_generated.go
		// (see ai/scripts/generatemodels/process.go) and arrive with that
		// adapter (README Phase 2).
		// api: openAIResponsesApi() — arrives with the openai-responses
		// adapter (README Phase 2).
	})
}
