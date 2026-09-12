// pi: providers/google.ts.
package providers

import (
	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/api"
	"github.com/ZoroSpace/pig/ai/auth"
	"github.com/ZoroSpace/pig/ai/types"
)

// GoogleProvider is the built-in Google (Gemini API) provider (pi:
// googleProvider).
func GoogleProvider() ai.Provider {
	return ai.CreateProvider(&ai.CreateProviderOptions{
		ID:      "google",
		Name:    "Google",
		BaseURL: "https://generativelanguage.googleapis.com/v1beta",
		Auth: auth.ProviderAuth{
			APIKey: auth.EnvAPIKeyAuth("Gemini API key", []string{"GEMINI_API_KEY"}),
		},
		Models: GetBuiltinModels(types.ProviderGoogle),
		API:    api.GoogleGenerativeAI{},
	})
}
