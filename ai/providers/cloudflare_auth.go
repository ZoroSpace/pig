// pi: providers/cloudflare-auth.ts — Cloudflare API-key auth for Workers AI
// and AI Gateway. Per-field merge: prefer the stored credential, fall back to
// ambient env; a credential carrying only the API key still picks up the
// account / gateway id from the environment.
package providers

import (
	"context"

	"github.com/ZoroSpace/pig/ai/auth"
	"github.com/ZoroSpace/pig/ai/types"
)

const cloudflareAPIKeyEnv = "CLOUDFLARE_API_KEY"

// cloudflareAuthKind discriminates the two auth flavors (pi:
// CloudflareAuthKind).
type cloudflareAuthKind string

const (
	cloudflareAuthWorkersAI cloudflareAuthKind = "workers-ai"
	cloudflareAuthAIGateway cloudflareAuthKind = "ai-gateway"
)

// cloudflareResolveValue resolves one auth field with per-field merge (pi:
// resolveValue): the credential value wins, ambient env fills the rest.
func cloudflareResolveValue(name string, input auth.ApiKeyAuthInput) (string, bool) {
	credential := input.Credential
	if credential != nil {
		var fromCredential string
		if name == cloudflareAPIKeyEnv {
			fromCredential = credential.Key
		} else if credential.Env != nil {
			fromCredential = credential.Env[name]
		}
		if fromCredential != "" {
			return fromCredential, true
		}
	}
	return input.Ctx.Env(name)
}

// cloudflareResolveEnv resolves the full credential set (pi:
// resolveCloudflareEnv); ok is false when any required field is missing.
func cloudflareResolveEnv(kind cloudflareAuthKind, input auth.ApiKeyAuthInput) (apiKey, accountID, gatewayID string, source string, ok bool) {
	apiKey, _ = cloudflareResolveValue(cloudflareAPIKeyEnv, input)
	accountID, _ = cloudflareResolveValue(cloudflareAccountIDEnv, input)
	if kind == cloudflareAuthAIGateway {
		gatewayID, _ = cloudflareResolveValue(cloudflareGatewayIDEnv, input)
	}
	if apiKey == "" || accountID == "" || (kind == cloudflareAuthAIGateway && gatewayID == "") {
		return "", "", "", "", false
	}
	if input.Credential != nil {
		source = "stored credential"
	} else {
		source = cloudflareAPIKeyEnv
	}
	return apiKey, accountID, gatewayID, source, true
}

// CloudflareWorkersAIAuth is the Workers AI auth (pi: cloudflareWorkersAIAuth):
// Authorization Bearer plus the account id env.
func CloudflareWorkersAIAuth() auth.ApiKeyAuth {
	return cloudflareWorkersAIAuth{}
}

type cloudflareWorkersAIAuth struct{}

func (cloudflareWorkersAIAuth) Name() string { return "Cloudflare API key" }

func (cloudflareWorkersAIAuth) Resolve(input auth.ApiKeyAuthInput) (*auth.AuthResult, error) {
	apiKey, accountID, _, source, ok := cloudflareResolveEnv(cloudflareAuthWorkersAI, input)
	if !ok {
		return nil, nil
	}
	return &auth.AuthResult{
		Auth:   auth.ModelAuth{APIKey: apiKey},
		Env:    types.ProviderEnv{cloudflareAccountIDEnv: accountID},
		Source: source,
	}, nil
}

// CloudflareWorkersAIAuthLogin is the interactive-setup capability (pi: the
// login member of cloudflareWorkersAIAuth).
type CloudflareWorkersAIAuthLogin struct{}

func (CloudflareWorkersAIAuthLogin) Login(ctx context.Context, interaction auth.AuthInteraction) (*auth.ApiKeyCredential, error) {
	key, err := interaction.Prompt(auth.AuthPrompt{Type: "secret", Message: "Enter Cloudflare API key"})
	if err != nil {
		return nil, err
	}
	accountID, err := interaction.Prompt(auth.AuthPrompt{Type: "text", Message: "Enter Cloudflare account ID"})
	if err != nil {
		return nil, err
	}
	return &auth.ApiKeyCredential{
		Key: key,
		Env: types.ProviderEnv{cloudflareAccountIDEnv: accountID},
	}, nil
}

// CloudflareAIGatewayAuth is the AI Gateway auth (pi: cloudflareAIGatewayAuth):
// the gateway token rides in cf-aig-authorization while the SDK's own
// Authorization / x-api-key headers are suppressed.
func CloudflareAIGatewayAuth() auth.ApiKeyAuth {
	return cloudflareAIGatewayAuth{}
}

type cloudflareAIGatewayAuth struct{}

func (cloudflareAIGatewayAuth) Name() string { return "Cloudflare API key" }

func (cloudflareAIGatewayAuth) Resolve(input auth.ApiKeyAuthInput) (*auth.AuthResult, error) {
	apiKey, accountID, gatewayID, source, ok := cloudflareResolveEnv(cloudflareAuthAIGateway, input)
	if !ok {
		return nil, nil
	}
	bearer := "Bearer " + apiKey
	return &auth.AuthResult{
		Auth: auth.ModelAuth{Headers: types.ProviderHeaders{
			"cf-aig-authorization": &bearer,
			"Authorization":        nil,
			"x-api-key":            nil,
		}},
		Env: types.ProviderEnv{
			cloudflareAccountIDEnv: accountID,
			cloudflareGatewayIDEnv: gatewayID,
		},
		Source: source,
	}, nil
}

// CloudflareAIGatewayAuthLogin is the interactive-setup capability (pi: the
// login member of cloudflareAIGatewayAuth).
type CloudflareAIGatewayAuthLogin struct{}

func (CloudflareAIGatewayAuthLogin) Login(ctx context.Context, interaction auth.AuthInteraction) (*auth.ApiKeyCredential, error) {
	key, err := interaction.Prompt(auth.AuthPrompt{Type: "secret", Message: "Enter Cloudflare API key"})
	if err != nil {
		return nil, err
	}
	accountID, err := interaction.Prompt(auth.AuthPrompt{Type: "text", Message: "Enter Cloudflare account ID"})
	if err != nil {
		return nil, err
	}
	gatewayID, err := interaction.Prompt(auth.AuthPrompt{Type: "text", Message: "Enter Cloudflare AI Gateway ID"})
	if err != nil {
		return nil, err
	}
	return &auth.ApiKeyCredential{
		Key: key,
		Env: types.ProviderEnv{
			cloudflareAccountIDEnv: accountID,
			cloudflareGatewayIDEnv: gatewayID,
		},
	}, nil
}
