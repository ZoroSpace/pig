// pi: providers/anthropic.ts.
package providers

import (
	"context"

	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/api"
	"github.com/ZoroSpace/pig/ai/auth"
	"github.com/ZoroSpace/pig/ai/types"
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
			// OAuth: Claude Pro/Max flow (pi: lazyOAuth({ name: "Anthropic
			// (Claude Pro/Max)", isSubscription: true, load:
			// loadAnthropicOAuth }); Go has no dynamic import, so the flow
			// links directly).
			OAuth: auth.AnthropicOAuth{},
		},
		Models: GetBuiltinModels(ai.ProviderAnthropic),
		API:    api.AnthropicMessages{},
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

	// Workload identity federation: the adapter exchanges the identity token
	// for a short-lived access token and refreshes it itself. Last in line so
	// keys and ANTHROPIC_AUTH_TOKEN keep winning, as in the SDK. The ids are
	// provider config rather than auth, so they travel in `env` (pi #10242).
	federation := types.ProviderEnv{}
	for _, envVar := range []string{ai.AnthropicFederationRuleIDEnv, ai.AnthropicOrganizationIDEnv, ai.AnthropicIdentityTokenFileEnv} {
		value, ok := input.Ctx.Env(envVar)
		if err := input.Context.Err(); err != nil {
			return nil, err
		}
		if !ok {
			return nil, nil
		}
		federation[envVar] = value
	}
	for _, envVar := range []string{ai.AnthropicServiceAccountIDEnv, ai.AnthropicWorkspaceIDEnv} {
		value, ok := input.Ctx.Env(envVar)
		if err := input.Context.Err(); err != nil {
			return nil, err
		}
		if ok {
			federation[envVar] = value
		}
	}
	return &auth.AuthResult{Auth: auth.ModelAuth{}, Env: federation, Source: "workload identity federation"}, nil
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
