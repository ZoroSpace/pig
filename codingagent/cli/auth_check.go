// pi: src/cli/auth-check.ts — provider auth readiness checks for
// `pig auth check`.
package cli

import (
	"context"
	"fmt"

	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/auth"
	"github.com/ZoroSpace/pig/codingagent/core"
)

// AuthCheckStatus is one of the auth check outcomes (pi: AuthCheckStatus).
type AuthCheckStatus = string

const (
	AuthCheckReady    AuthCheckStatus = "ready"
	AuthCheckNotReady AuthCheckStatus = "not_ready"
	AuthCheckInvalid  AuthCheckStatus = "invalid"
)

// AuthCheckReason explains a non-ready check (pi: AuthCheckReason).
type AuthCheckReason = string

const (
	AuthCheckProviderNotFound         AuthCheckReason = "provider_not_found"
	AuthCheckCredentialsNotConfigured AuthCheckReason = "credentials_not_configured"
	AuthCheckCredentialNotAvailable   AuthCheckReason = "credential_not_available"
	AuthCheckInvalidState             AuthCheckReason = "invalid_state"
)

// AuthCheckResult is the outcome of a provider auth check (pi:
// AuthCheckResult).
type AuthCheckResult struct {
	Status   AuthCheckStatus `json:"status"`
	Provider string          `json:"provider"`
	Reason   AuthCheckReason `json:"reason,omitempty"`
	// AuthType is "api_key" | "oauth"; "" = not provided (pi: authType?).
	AuthType auth.AuthType `json:"authType,omitempty"`
}

// CheckProviderAuth checks whether a provider is ready for requests (pi:
// checkProviderAuth).
func CheckProviderAuth(ctx context.Context, args *Args, modelRuntime *core.ModelRuntime, options *CheckProviderAuthOptions) (AuthCheckResult, error) {
	if options == nil {
		options = &CheckProviderAuthOptions{Refresh: false}
	}
	target, err := ValidateAuthCommandArgs(args, AuthCommandCheck)
	if err != nil {
		return AuthCheckResult{}, err
	}
	provider := target.Provider
	if target.Model != "" {
		resolved := core.ResolveCliModel(core.ResolveCliModelOptions{
			CliProvider:  target.Provider,
			CliModel:     target.Model,
			ModelRuntime: core.NewModelRuntimeResolver(modelRuntime),
		})
		if resolved.Error != "" || resolved.Model == nil {
			message := resolved.Error
			if message == "" {
				message = fmt.Sprintf("Unable to resolve model %q", target.Model)
			}
			return AuthCheckResult{}, &AuthCommandError{Message: message}
		}
		provider = string(resolved.Model.Provider)
	}
	if provider == "" {
		return AuthCheckResult{}, &AuthCommandError{Message: "Unable to resolve an auth provider"}
	}
	if modelRuntime.GetError() != "" {
		return AuthCheckResult{Status: AuthCheckInvalid, Provider: provider, Reason: AuthCheckInvalidState}, nil
	}
	if modelRuntime.GetProvider(provider) == nil {
		return AuthCheckResult{Status: AuthCheckNotReady, Provider: provider, Reason: AuthCheckProviderNotFound}, nil
	}
	authCheck, err := modelRuntime.CheckAuth(ctx, provider)
	if err != nil {
		return AuthCheckResult{Status: AuthCheckInvalid, Provider: provider, Reason: AuthCheckInvalidState}, nil
	}
	if authCheck == nil {
		return AuthCheckResult{Status: AuthCheckNotReady, Provider: provider, Reason: AuthCheckCredentialsNotConfigured}, nil
	}
	if options.Refresh {
		_, refreshErr := modelRuntime.GetAuth(ctx, provider, nil)
		if refreshErr != nil {
			return AuthCheckResult{Status: AuthCheckNotReady, Provider: provider, Reason: AuthCheckCredentialsNotConfigured}, nil
		}
	}
	return AuthCheckResult{Status: AuthCheckReady, Provider: provider, AuthType: authCheck.Type}, nil
}

// CheckProviderAuthOptions carries checkProviderAuth's option (pi: options).
type CheckProviderAuthOptions struct {
	Refresh bool
}

// GetProviderCredential reads the current credential for a provider (pi:
// getProviderCredential). ok=false = no credential available.
func GetProviderCredential(ctx context.Context, providerID string, modelRuntime *core.ModelRuntime, credentials auth.CredentialStore, options *GetProviderCredentialOptions) (string, bool, error) {
	if options == nil {
		options = &GetProviderCredentialOptions{Refresh: false}
	}
	credential, err := credentials.Read(ctx, providerID)
	if err != nil {
		return "", false, err
	}
	if !options.Refresh && credential != nil && credential.CredentialType() == auth.CredentialTypeOAuth {
		return credential.(*auth.OAuthCredential).Access, true, nil
	}
	authResult, err := modelRuntime.GetAuth(ctx, providerID, nil)
	if err != nil {
		return "", false, err
	}
	value := GetAuthCredential(authResult)
	return value, value != "", nil
}

// GetProviderCredentialOptions carries getProviderCredential's option (pi:
// options).
type GetProviderCredentialOptions struct {
	Refresh bool
}

// CreateAuthCheckModelRuntime builds the no-network runtime used by auth
// checks (pi: createAuthCheckModelRuntime).
func CreateAuthCheckModelRuntime(credentials auth.CredentialStore) *core.ModelRuntime {
	return core.CreateModelRuntime(context.Background(), &core.CreateModelRuntimeOptions{
		Credentials:       credentials,
		ModelsStore:       &ai.InMemoryModelsStore{},
		AllowModelNetwork: false,
		RefreshOnCreate:   boolPtr(false),
	})
}
