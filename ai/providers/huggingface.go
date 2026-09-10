// pi: providers/huggingface.ts.
package providers

import (
	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/api"
	"github.com/ZoroSpace/pig/ai/auth"
)

// HuggingFaceProvider is the built-in Hugging Face provider (pi:
// huggingfaceProvider).
func HuggingFaceProvider() ai.Provider {
	return ai.CreateProvider(&ai.CreateProviderOptions{
		ID:      "huggingface",
		Name:    "Hugging Face",
		BaseURL: "https://router.huggingface.co/v1",
		Auth: auth.ProviderAuth{
			APIKey: auth.EnvAPIKeyAuth("Hugging Face token", []string{"HF_TOKEN"}),
		},
		Models: GetBuiltinModels(ai.ProviderHuggingFace),
		// api: openAICompletionsApi() (pi: openAICompletionsApi(), lazy-loaded;
		// Go adapters compile in).
		API: api.OpenAICompletions{},
	})
}
