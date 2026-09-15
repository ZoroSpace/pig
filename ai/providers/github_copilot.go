// pi: providers/github-copilot.ts.
package providers

import (
	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/api"
	"github.com/ZoroSpace/pig/ai/auth"
	"github.com/ZoroSpace/pig/ai/types"
)

// GitHubCopilotProvider is the built-in GitHub Copilot provider (pi:
// githubCopilotProvider) — a three-protocol provider; the catalog routes
// Claude models to anthropic-messages, GPT-5/Grok/MAI models to
// openai-responses, and the rest to openai-completions.
func GitHubCopilotProvider() ai.Provider {
	return ai.CreateProvider(&ai.CreateProviderOptions{
		ID:      "github-copilot",
		Name:    "GitHub Copilot",
		BaseURL: "https://api.individual.githubcopilot.com",
		Auth: auth.ProviderAuth{
			APIKey: auth.EnvAPIKeyAuth("GitHub Copilot token", []string{"COPILOT_GITHUB_TOKEN"}),
			OAuth:  auth.GitHubCopilotOAuth{},
		},
		Models:       GetBuiltinModels(types.ProviderGithubCopilot),
		FilterModels: filterGitHubCopilotModels,
		APIByAPI: map[string]ai.ProviderStreams{
			string(types.ApiAnthropicMessages): api.AnthropicMessages{},
			string(types.ApiOpenAICompletions): api.OpenAICompletions{},
			string(types.ApiOpenAIResponses):   api.OpenAIResponses{},
		},
	})
}

func init() {
	// pi's oauth file statically imports the generated GITHUB_COPILOT_MODELS
	// catalog for the policy-model filter; package auth cannot import the
	// catalog (layering), so the provider injects the lookup.
	auth.SetGitHubCopilotKnownModelIDs(func() map[string]bool {
		models, ok := ai.MODELS[types.ProviderGithubCopilot]
		if !ok {
			return nil
		}
		known := make(map[string]bool, len(models))
		for id := range models {
			known[id] = true
		}
		return known
	})
}

// filterGitHubCopilotModels filters the catalog to the authenticated
// account's available models (pi: filterModels). Non-OAuth credentials and
// OAuth credentials without a well-formed availableModelIds list pass every
// model through.
func filterGitHubCopilotModels(models []ai.Model, credential auth.Credential) []ai.Model {
	oauthCredential, ok := credential.(*auth.OAuthCredential)
	if !ok {
		return models
	}
	availableModelIDs, ok := auth.GitHubCopilotAvailableModelIDs(oauthCredential)
	if !ok {
		return models
	}
	available := make(map[string]bool, len(availableModelIDs))
	for _, id := range availableModelIDs {
		available[id] = true
	}
	filtered := make([]ai.Model, 0, len(models))
	for _, model := range models {
		if available[model.ID] {
			filtered = append(filtered, model)
		}
	}
	return filtered
}
