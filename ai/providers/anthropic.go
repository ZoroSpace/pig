// pi: providers/anthropic.ts.
package providers

import (
	"context"

	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/auth"
)

// AnthropicProvider is the built-in Anthropic provider (pi:
// anthropicProvider).
func AnthropicProvider() ai.Provider {
	return ai.CreateProvider(&ai.CreateProviderOptions{
		ID:      "anthropic",
		Name:    "Anthropic",
		BaseURL: "https://api.anthropic.com",
		Auth: auth.ProviderAuth{
			APIKey: anthropicAPIKeyAuth(),
			// oauth: lazyOAuth({ name: "Anthropic (Claude Pro/Max)",
			// isSubscription: true, load: loadAnthropicOAuth }) — arrives
			// with the OAuth flows (auth/oauth/load.ts, README Phase 2).
		},
		Models: GetBuiltinModels(ai.ProviderAnthropic),
		// api: anthropicMessagesApi() — arrives with the anthropic-messages
		// adapter (README Phase 2).
	})
}

// anthropicAPIKeyAuth is Anthropic's non-standard api-key auth (pi:
// anthropicApiKeyAuth): stored credential, then ANTHROPIC_AUTH_TOKEN as a
// Bearer header, then ANTHROPIC_OAUTH_TOKEN / ANTHROPIC_API_KEY as api keys.
// The standard envApiKeyAuth chain does not apply because
// ANTHROPIC_AUTH_TOKEN must go out as Authorization: Bearer.
func anthropicAPIKeyAuth() auth.ApiKeyAuth {
	return anthropicAPIKeyAuthImpl{}
}

type anthropicAPIKeyAuthImpl struct{}

func (anthropicAPIKeyAuthImpl) Name() string { return "Anthropic API key" }

func (anthropicAPIKeyAuthImpl) Resolve(input auth.ApiKeyAuthInput) (*auth.AuthResult, error) {
	if err := input.Context.Err(); err != nil {
		return nil, err
	}
	if input.Credential != nil && input.Credential.Key != "" {
		return &auth.AuthResult{
			Auth:   auth.ModelAuth{APIKey: input.Credential.Key},
			Env:    input.Credential.Env,
			Source: "stored credential",
		}, nil
	}

	authToken, ok := input.Ctx.Env(ai.AnthropicAuthTokenEnv)
	if err := input.Context.Err(); err != nil {
		return nil, err
	}
	if ok {
		bearer := "Bearer " + authToken
		return &auth.AuthResult{
			Auth:   auth.ModelAuth{Headers: ai.ProviderHeaders{"Authorization": &bearer}},
			Source: ai.AnthropicAuthTokenEnv,
		}, nil
	}

	for _, envVar := range []string{ai.AnthropicOAuthTokenEnv, ai.AnthropicAPIKeyEnv} {
		apiKey, ok := input.Ctx.Env(envVar)
		if err := input.Context.Err(); err != nil {
			return nil, err
		}
		if ok {
			return &auth.AuthResult{Auth: auth.ModelAuth{APIKey: apiKey}, Source: envVar}, nil
		}
	}
	return nil, nil
}

// Login prompts for the key (pi: login member).
func (anthropicAPIKeyAuthImpl) Login(ctx context.Context, interaction auth.AuthInteraction) (*auth.ApiKeyCredential, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	key, err := interaction.Prompt(auth.AuthPrompt{Type: "secret", Message: "Enter Anthropic API key"})
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &auth.ApiKeyCredential{Key: key}, nil
}
