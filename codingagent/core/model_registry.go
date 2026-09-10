// pi: src/core/model-registry.ts — synchronous compatibility facade exposed
// to extensions. Coding-agent internals use ModelRuntime directly.
package core

import (
	"context"
	"errors"
	"fmt"

	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/auth"
	"github.com/ZoroSpace/pig/ai/types"
)

// ResolvedRequestAuth is getApiKeyAndHeaders' outcome (pi: ResolvedRequestAuth
// union). Ok distinguishes the two forms.
type ResolvedRequestAuth struct {
	Ok bool
	// Error is set when Ok is false.
	Error string
	// APIKey/Headers/BaseUrl/Env are set when Ok is true.
	APIKey  string
	Headers types.ProviderHeaders
	BaseURL string
	Env     map[string]string
}

// ClearRegistryApiKeyCache re-exported (pi: clearApiKeyCache re-export).
var ClearRegistryApiKeyCache = ClearApiKeyCache

// ModelRegistry is the extension-facing facade over ModelRuntime (pi:
// ModelRegistry).
type ModelRegistry struct {
	runtime *ModelRuntime
}

// NewModelRegistry wraps a runtime (pi: constructor).
func NewModelRegistry(runtime *ModelRuntime) *ModelRegistry {
	return &ModelRegistry{runtime: runtime}
}

// Refresh reloads models.json (pi: refresh; "await before making synchronous
// registry reads").
func (r *ModelRegistry) Refresh(ctx context.Context, options *ai.ModelsRefreshOptions) ai.ModelsRefreshResult {
	return r.runtime.Refresh(ctx, options)
}

// GetError aggregates runtime errors (pi: getError).
func (r *ModelRegistry) GetError() string {
	return r.runtime.GetError()
}

// GetAll lists all models (pi: getAll).
func (r *ModelRegistry) GetAll() []ai.Model {
	return append([]ai.Model(nil), r.runtime.GetModels("")...)
}

// GetAvailable lists available models (pi: getAvailable).
func (r *ModelRegistry) GetAvailable() []ai.Model {
	return append([]ai.Model(nil), r.runtime.GetAvailableSnapshot()...)
}

// Find looks up one model; nil when absent (pi: find).
func (r *ModelRegistry) Find(provider, modelID string) *ai.Model {
	model, ok := r.runtime.GetModel(provider, modelID)
	if !ok {
		return nil
	}
	return &model
}

// HasConfiguredAuth reports whether the model's provider is configured (pi:
// hasConfiguredAuth).
func (r *ModelRegistry) HasConfiguredAuth(model ai.Model) bool {
	return r.runtime.HasConfiguredAuth(string(model.Provider))
}

// GetApiKeyAndHeaders resolves request auth for a model (pi:
// getApiKeyAndHeaders).
func (r *ModelRegistry) GetApiKeyAndHeaders(ctx context.Context, model ai.Model) ResolvedRequestAuth {
	resolution, err := r.runtime.GetAuthForModel(ctx, &model, nil)
	if err != nil {
		message := err.Error()
		var syncErr *CredentialSynchronizationError
		if errors.As(err, &syncErr) && syncErr.Cause != nil {
			message = syncErr.Cause.Error()
		}
		// pi: the authHeader sentinel rewrites to a friendly message.
		if message == "authHeader requires a resolved API key" {
			message = fmt.Sprintf("No API key found for %q", model.Provider)
		}
		return ResolvedRequestAuth{Ok: false, Error: message}
	}
	if resolution == nil {
		compatibility := r.runtime.GetCompatibilityRequestConfig(model)
		if compatibility.AuthHeader {
			return ResolvedRequestAuth{Ok: false, Error: fmt.Sprintf("No API key found for %q", model.Provider)}
		}
		return ResolvedRequestAuth{Ok: true, Headers: compatibility.Headers}
	}
	out := ResolvedRequestAuth{
		Ok:      true,
		APIKey:  resolution.Auth.APIKey,
		Headers: resolution.Auth.Headers,
		Env:     map[string]string(resolution.Env),
	}
	if resolution.Auth.BaseURL != "" {
		out.BaseURL = resolution.Auth.BaseURL
	}
	return out
}

// GetProviderAuthStatus reports the provider auth status (pi:
// getProviderAuthStatus).
func (r *ModelRegistry) GetProviderAuthStatus(provider string) AuthStatus {
	return r.runtime.GetProviderAuthStatus(provider)
}

// GetProvider returns one provider; nil when absent (pi: getProvider).
func (r *ModelRegistry) GetProvider(provider string) ai.Provider {
	return r.runtime.GetProvider(provider)
}

// Complete dispatches one completion (pi: complete).
func (r *ModelRegistry) Complete(ctx context.Context, model *ai.Model, llmContext *ai.Context, options *ai.ModelsStreamOptions) (*ai.AssistantMessage, error) {
	return r.runtime.Complete(ctx, model, llmContext, options)
}

// GetProviderDisplayName returns the display name, falling back to the id
// (pi: getProviderDisplayName).
func (r *ModelRegistry) GetProviderDisplayName(provider string) string {
	if p := r.runtime.GetProvider(provider); p != nil {
		return p.Name()
	}
	return provider
}

// GetProviderAuth resolves provider auth (pi: getProviderAuth).
func (r *ModelRegistry) GetProviderAuth(ctx context.Context, provider string) (*auth.AuthResult, error) {
	return r.runtime.GetAuth(ctx, provider, nil)
}

// GetApiKeyForProvider resolves the provider's API key, swallowing errors
// (pi: getApiKeyForProvider).
func (r *ModelRegistry) GetApiKeyForProvider(ctx context.Context, provider string) string {
	resolution, err := r.runtime.GetAuth(ctx, provider, nil)
	if err != nil || resolution == nil {
		return ""
	}
	return resolution.Auth.APIKey
}

// IsUsingOAuth reports OAuth-backed auth (pi: isUsingOAuth).
func (r *ModelRegistry) IsUsingOAuth(model ai.Model) bool {
	return r.runtime.IsUsingOAuth(string(model.Provider))
}

// RegisterProvider registers a provider config by name (pi: registerProvider
// string overload).
func (r *ModelRegistry) RegisterProvider(providerName string, config *ProviderConfigInput) error {
	if config == nil {
		return errors.New("Provider config is required when registering by name")
	}
	return r.runtime.RegisterProvider(providerName, config)
}

// RegisterNativeProvider registers a Go-native provider (pi: registerProvider
// Provider overload).
func (r *ModelRegistry) RegisterNativeProvider(provider ai.Provider) error {
	return r.runtime.RegisterNativeProvider(provider)
}

// UnregisterProvider removes an extension registration (pi:
// unregisterProvider).
func (r *ModelRegistry) UnregisterProvider(providerName string) {
	r.runtime.UnregisterProvider(providerName)
}

// GetRegisteredProviderConfig returns one registration (pi:
// getRegisteredProviderConfig).
func (r *ModelRegistry) GetRegisteredProviderConfig(providerName string) *ProviderConfigInput {
	return r.runtime.GetRegisteredProviderConfig(providerName)
}

// GetRegisteredNativeProvider returns one native registration (pi:
// getRegisteredNativeProvider).
func (r *ModelRegistry) GetRegisteredNativeProvider(providerName string) ai.Provider {
	return r.runtime.GetRegisteredNativeProvider(providerName)
}

// GetRegisteredProviderIds lists registered provider ids (pi:
// getRegisteredProviderIds).
func (r *ModelRegistry) GetRegisteredProviderIds() []string {
	return r.runtime.GetRegisteredProviderIds()
}
