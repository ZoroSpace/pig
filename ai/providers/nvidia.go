// pi: providers/nvidia.ts.
package providers

import (
	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/api"
	"github.com/ZoroSpace/pig/ai/auth"
)

// NVIDIAProvider is the built-in NVIDIA provider (pi: nvidiaProvider).
func NVIDIAProvider() ai.Provider {
	return ai.CreateProvider(&ai.CreateProviderOptions{
		ID:      "nvidia",
		Name:    "NVIDIA",
		BaseURL: "https://integrate.api.nvidia.com/v1",
		Auth: auth.ProviderAuth{
			APIKey: auth.EnvAPIKeyAuth("NVIDIA API key", []string{"NVIDIA_API_KEY"}),
		},
		Models: GetBuiltinModels(ai.ProviderNVIDIA),
		// api: openAICompletionsApi() (pi: openAICompletionsApi(), lazy-loaded;
		// Go adapters compile in).
		API: api.OpenAICompletions{},
	})
}
