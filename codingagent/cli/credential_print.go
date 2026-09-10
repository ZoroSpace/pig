// pi: src/cli/credential-print.ts — resolve one configured provider
// credential for `auth print-api-key` / `auth print-bearer-token`.
package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/auth"
	"github.com/ZoroSpace/pig/codingagent/core"
)

// defaultBearerTokenMinExpiryMs mirrors DEFAULT_BEARER_TOKEN_MIN_EXPIRY_MS.
const defaultBearerTokenMinExpiryMs = 30 * 60_000

// credentialPrintKind is one credential-printing command kind (pi:
// CredentialPrintKind = Exclude<AuthCommandKind, "check">).
type credentialPrintKind = AuthCommandKind

// providerCandidate is one provider to resolve a credential for (pi: the
// inline `{ id: string; model?: Model }`).
type providerCandidate struct {
	id    string
	model *ai.Model
}

// ResolveCredentialForPrint resolves one configured provider credential.
//
// This intentionally calls ModelRuntime.GetAuth, which refreshes and persists
// OAuth credentials with less than five minutes remaining through the normal
// request-auth path (pi: resolveCredentialForPrint).
func ResolveCredentialForPrint(ctx context.Context, args *Args, modelRuntime *core.ModelRuntime, kind credentialPrintKind, minExpiryMs *int64) (string, error) {
	target, err := ValidateAuthCommandArgs(args, kind)
	if err != nil {
		return "", err
	}
	credentialList, err := modelRuntime.ListCredentials(ctx)
	if err != nil {
		return "", err
	}
	credentialTypes := map[string]auth.CredentialType{}
	for _, credential := range credentialList {
		credentialTypes[credential.ProviderID] = credential.Type
	}
	var providers []providerCandidate
	if target.Provider != "" {
		provider := modelRuntime.GetProvider(target.Provider)
		if provider == nil {
			return "", &AuthCommandError{Message: fmt.Sprintf(
				"Unknown provider %q. Use --list-models to see available providers.", target.Provider)}
		}
		if target.Model != "" {
			resolved := core.ResolveCliModel(core.ResolveCliModelOptions{
				CliProvider:  provider.ID(),
				CliModel:     target.Model,
				ModelRuntime: core.NewModelRuntimeResolver(modelRuntime),
			})
			if resolved.Error != "" || resolved.Model == nil {
				message := resolved.Error
				if message == "" {
					message = "Unable to resolve the requested provider/model"
				}
				return "", &AuthCommandError{Message: message}
			}
			providers = append(providers, providerCandidate{id: provider.ID(), model: resolved.Model})
		} else {
			providers = append(providers, providerCandidate{id: provider.ID()})
		}
	} else {
		for _, provider := range modelRuntime.GetProviders() {
			if _, has := credentialTypes[provider.ID()]; !has {
				continue
			}
			resolved := core.ResolveCliModel(core.ResolveCliModelOptions{
				CliProvider:  provider.ID(),
				CliModel:     target.Model,
				ModelRuntime: core.NewModelRuntimeResolver(modelRuntime),
			})
			if resolved.Model != nil && resolved.Error == "" && !containsCustomModelIdNote(resolved.Warning) {
				providers = append(providers, providerCandidate{id: provider.ID(), model: resolved.Model})
			}
		}
		if len(providers) == 0 {
			return "", &AuthCommandError{Message: fmt.Sprintf(
				"Model %q not found. Use --list-models to see available models.", target.Model)}
		}
	}

	type resolvedCredential struct {
		providerID string
		value      string
	}
	var credentials []resolvedCredential
	for _, provider := range providers {
		credentialType, has := credentialTypes[provider.id]
		if kind == AuthCommandAPIKey && has && credentialType == auth.CredentialTypeOAuth {
			continue
		}
		if kind == AuthCommandBearerToken && (!has || credentialType != auth.CredentialTypeOAuth) {
			continue
		}
		var overrides *core.ModelRuntimeAuthOverrides
		if kind == AuthCommandBearerToken {
			minValidity := int64(defaultBearerTokenMinExpiryMs)
			if minExpiryMs != nil {
				minValidity = *minExpiryMs
			}
			overrides = &core.ModelRuntimeAuthOverrides{MinOAuthValidityMs: minValidity}
		}
		var authResult *auth.AuthResult
		if provider.model != nil {
			authResult, err = modelRuntime.GetAuthForModel(ctx, provider.model, overrides)
		} else {
			authResult, err = modelRuntime.GetAuth(ctx, provider.id, overrides)
		}
		if err != nil {
			return "", err
		}
		if value := GetAuthCredential(authResult); value != "" {
			credentials = append(credentials, resolvedCredential{providerID: provider.id, value: value})
		}
	}

	if len(credentials) == 1 {
		return credentials[0].value, nil
	}
	if len(credentials) == 0 {
		providerID := ""
		if len(providers) > 0 {
			providerID = providers[0].id
		}
		credentialType, has := credentialTypes[providerID]
		if target.Provider != "" && kind == AuthCommandAPIKey && has && credentialType == auth.CredentialTypeOAuth {
			return "", &AuthCommandError{Message: fmt.Sprintf(
				"Provider %q is configured with OAuth, not an API key", providerID)}
		}
		if target.Provider != "" && kind == AuthCommandBearerToken && (!has || credentialType != auth.CredentialTypeOAuth) {
			return "", &AuthCommandError{Message: fmt.Sprintf(
				"Provider %q is not configured with an OAuth bearer token", providerID)}
		}
		kindLabel := "OAuth bearer token"
		if kind == AuthCommandAPIKey {
			kindLabel = "API key"
		}
		return "", &AuthCommandError{Message: fmt.Sprintf("No usable %s is configured", kindLabel)}
	}
	ids := make([]string, len(credentials))
	for i, c := range credentials {
		ids[i] = c.providerID
	}
	return "", &AuthCommandError{Message: fmt.Sprintf(
		"Multiple configured providers matched (%s). Specify --provider.", strings.Join(ids, ", "))}
}

// containsCustomModelIdNote reports whether a resolver warning mentions the
// custom-model-id fallback (pi: resolved.warning?.includes("Using custom
// model id")).
func containsCustomModelIdNote(warning string) bool {
	return warning != "" && strings.Contains(warning, "Using custom model id")
}
