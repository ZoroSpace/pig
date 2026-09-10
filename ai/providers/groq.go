// pi: providers/groq.ts.
package providers

import (
	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/api"
	"github.com/ZoroSpace/pig/ai/auth"
)

// GroqProvider is the built-in Groq provider (pi: groqProvider).
func GroqProvider() ai.Provider {
	return ai.CreateProvider(&ai.CreateProviderOptions{
		ID:      "groq",
		Name:    "Groq",
		BaseURL: "https://api.groq.com/openai/v1",
		Auth: auth.ProviderAuth{
			APIKey: auth.EnvAPIKeyAuth("Groq API key", []string{"GROQ_API_KEY"}),
		},
		Models: GetBuiltinModels(ai.ProviderGroq),
		// api: openAICompletionsApi() (pi: openAICompletionsApi(), lazy-loaded;
		// Go adapters compile in).
		API: api.OpenAICompletions{},
	})
}
