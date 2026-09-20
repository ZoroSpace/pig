// Ported from pi's test/model-runtime-modify-models-compat.test.ts.
package core

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/auth"
	"github.com/ZoroSpace/pig/ai/types"
	"github.com/samber/lo"
)

func lifecycleModel(id string) ai.Model {
	return ai.Model{
		ID: id, Name: id, API: ai.ApiOpenAICompletions, Provider: "extension-oauth",
		BaseURL: "https://example.test/v1", Reasoning: false,
		Input: []ai.ModelInput{ai.ModelInputText}, Cost: ai.ModelCost{},
		ContextWindow: 1000, MaxTokens: 100,
	}
}

func TestModelRuntimeRegistersNativeProvidersWithTheirAuthImplementation(t *testing.T) {
	runtime := CreateModelRuntime(context.Background(), &CreateModelRuntimeOptions{
		Credentials:       NewInMemoryAuthStorage(authStorageData{}),
		ModelsStore:       &InMemoryCodingAgentModelsStore{},
		ModelsPath:        lo.ToPtr(""),
		AllowModelNetwork: false,
	})
	nativeModel := lifecycleModel("native")
	nativeModel.Provider = "extension-native"
	nativeModel.BaseURL = "https://fallback.test/v1"
	provider := ai.CreateProvider(&ai.CreateProviderOptions{
		ID:     "extension-native",
		Name:   "Extension Native",
		Auth:   auth.ProviderAuth{APIKey: &nativeSetupAuth{}},
		Models: []ai.Model{nativeModel},
		API:    notUsedStreams{},
	})

	if err := runtime.RegisterNativeProvider(provider); err != nil {
		t.Fatal(err)
	}
	registry := NewModelRegistry(runtime)
	if registry.GetProvider("extension-native") != provider {
		t.Fatal("registry provider identity mismatch")
	}
	if registry.GetRegisteredNativeProvider("extension-native") != provider {
		t.Fatal("registered native provider identity mismatch")
	}
	found := false
	for _, id := range registry.GetRegisteredProviderIds() {
		if id == "extension-native" {
			found = true
		}
	}
	if !found {
		t.Fatal("extension-native missing from registered ids")
	}
	if registry.Find("extension-native", "native") == nil {
		t.Fatal("find(extension-native, native) missing")
	}

	if _, err := runtime.Login(context.Background(), "extension-native", auth.AuthTypeAPIKey, &promptInteraction{secret: "secret"}); err != nil {
		t.Fatal(err)
	}
	result, err := registry.GetProviderAuth(context.Background(), "extension-native")
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || result.Auth.APIKey != "secret" || result.Auth.BaseURL != "https://resolved.test/v1" {
		t.Fatalf("auth = %+v", result)
	}

	registry.UnregisterProvider("extension-native")
	if registry.GetProvider("extension-native") != nil {
		t.Fatal("extension-native still registered")
	}
}

type promptInteraction struct{ secret string }

func (i *promptInteraction) Prompt(prompt auth.AuthPrompt) (string, error) { return i.secret, nil }
func (i *promptInteraction) Notify(event auth.AuthEvent)                   {}

// nativeSetupAuth is the native provider's api-key method: login prompts,
// resolve overlays a baseUrl (pi: nativeSetupAuth).
type nativeSetupAuth struct{}

func (a *nativeSetupAuth) Name() string { return "Native setup" }

func (a *nativeSetupAuth) Login(ctx context.Context, interaction auth.AuthInteraction) (*auth.ApiKeyCredential, error) {
	key, err := interaction.Prompt(auth.AuthPrompt{Type: "secret", Message: "API key"})
	if err != nil {
		return nil, err
	}
	return &auth.ApiKeyCredential{Key: key}, nil
}

func (a *nativeSetupAuth) Check(input auth.ApiKeyAuthInput) (*auth.AuthCheck, error) {
	if input.Credential != nil && input.Credential.Key != "" {
		return &auth.AuthCheck{Type: auth.AuthTypeAPIKey, Source: "stored native key"}, nil
	}
	return nil, nil
}

func (a *nativeSetupAuth) Resolve(input auth.ApiKeyAuthInput) (*auth.AuthResult, error) {
	if input.Credential != nil && input.Credential.Key != "" {
		return &auth.AuthResult{
			Auth:   auth.ModelAuth{APIKey: input.Credential.Key, BaseURL: "https://resolved.test/v1"},
			Source: "stored native key",
		}, nil
	}
	return nil, nil
}

func TestModelRuntimePreservesNativeDeferredMethodsThroughProviderOverlays(t *testing.T) {
	tempDir := t.TempDir()
	modelsPath := filepath.Join(tempDir, "models.json")
	modelsJSON, err := json.Marshal(map[string]any{
		"providers": map[string]any{
			"extension-native-deferred": map[string]any{"baseUrl": "https://overlay.test/v1"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(modelsPath, modelsJSON, 0o600); err != nil {
		t.Fatal(err)
	}

	runtime := CreateModelRuntime(context.Background(), &CreateModelRuntimeOptions{
		Credentials:       NewInMemoryAuthStorage(authStorageData{}),
		ModelsStore:       &InMemoryCodingAgentModelsStore{},
		ModelsPath:        lo.ToPtr(modelsPath),
		AllowModelNetwork: false,
	})
	nativeModel := lifecycleModel("native-deferred")
	nativeModel.Provider = "extension-native-deferred"
	nativeModel.BaseURL = "https://native.test/v1"

	fetchedBaseURL := ""
	var fetchedOptions *ai.DeferredFetchOptions
	cancelledID := ""
	var cancelledOptions *ai.ProviderRequestOptions
	provider := &nativeDeferredProvider{
		id: "extension-native-deferred", model: nativeModel,
		fetchedBaseURL: &fetchedBaseURL, fetchedOptions: &fetchedOptions,
		cancelledID: &cancelledID, cancelledOptions: &cancelledOptions,
	}

	if err := runtime.RegisterNativeProvider(provider); err != nil {
		t.Fatal(err)
	}
	composedModel, ok := runtime.GetModel(provider.ID(), nativeModel.ID)
	if !ok {
		t.Fatal("composed model missing")
	}

	message, err := runtime.FetchDeferred(context.Background(), &composedModel, &ai.DeferredHandle{
		Provider: ai.ProviderID(provider.ID()), ModelID: nativeModel.ID, API: nativeModel.API, ID: "fetch-id",
	}, &ai.ModelsDeferredFetchOptions{
		DeferredFetchOptions: ai.DeferredFetchOptions{
			Wait: 25,
			ProviderRequestOptions: types.ProviderRequestOptions{
				Headers: types.ProviderHeaders{"X-Fetch": lo.ToPtr("fetch")},
			},
		},
		ModelsRequestTransforms: ai.ModelsRequestTransforms{
			TransformHeaders: func(headers types.ProviderHeaders) (types.ProviderHeaders, error) {
				out := types.ProviderHeaders{}
				for key, value := range headers {
					out[key] = value
				}
				out["X-Transformed"] = lo.ToPtr("fetch")
				return out, nil
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if message == nil {
		t.Fatal("fetchDeferred returned no message")
	}
	if err := runtime.CancelDeferred(context.Background(), &composedModel, &ai.DeferredHandle{
		Provider: ai.ProviderID(provider.ID()), ModelID: nativeModel.ID, API: nativeModel.API, ID: "cancel-id",
	}, &ai.ModelsDeferredCancelOptions{
		ProviderRequestOptions: types.ProviderRequestOptions{TimeoutMs: 100},
		ModelsRequestTransforms: ai.ModelsRequestTransforms{
			TransformHeaders: func(headers types.ProviderHeaders) (types.ProviderHeaders, error) {
				out := types.ProviderHeaders{}
				for key, value := range headers {
					out[key] = value
				}
				out["X-Transformed"] = lo.ToPtr("cancel")
				return out, nil
			},
		},
	}); err != nil {
		t.Fatal(err)
	}

	if fetchedBaseURL != "https://overlay.test/v1" {
		t.Fatalf("fetchedBaseURL = %q", fetchedBaseURL)
	}
	if fetchedOptions == nil || fetchedOptions.Wait != 25 || fetchedOptions.APIKey != "key" {
		t.Fatalf("fetchedOptions = %+v", fetchedOptions)
	}
	if header := fetchedOptions.Headers["X-Fetch"]; header == nil || *header != "fetch" {
		t.Fatalf("fetched headers = %+v", fetchedOptions.Headers)
	}
	if header := fetchedOptions.Headers["X-Transformed"]; header == nil || *header != "fetch" {
		t.Fatalf("fetched headers = %+v", fetchedOptions.Headers)
	}
	if cancelledID != "cancel-id" {
		t.Fatalf("cancelledID = %q", cancelledID)
	}
	if cancelledOptions == nil || cancelledOptions.TimeoutMs != 100 || cancelledOptions.APIKey != "key" {
		t.Fatalf("cancelledOptions = %+v", cancelledOptions)
	}
	if header := cancelledOptions.Headers["X-Transformed"]; header == nil || *header != "cancel" {
		t.Fatalf("cancelled headers = %+v", cancelledOptions.Headers)
	}
}

// unconditionalAPIKeyAuth resolves without requiring a stored credential
// (pi's test auth stubs resolve: async () => ...).
type unconditionalAPIKeyAuth struct {
	name   string
	key    string
	source string
}

func (a *unconditionalAPIKeyAuth) Name() string { return a.name }

func (a *unconditionalAPIKeyAuth) Login(ctx context.Context, interaction auth.AuthInteraction) (*auth.ApiKeyCredential, error) {
	return &auth.ApiKeyCredential{Key: a.key}, nil
}

func (a *unconditionalAPIKeyAuth) Resolve(input auth.ApiKeyAuthInput) (*auth.AuthResult, error) {
	return &auth.AuthResult{Auth: auth.ModelAuth{APIKey: a.key}, Source: a.source}, nil
}

// nativeDeferredProvider is a native provider with deferred capability (pi:
// the provider literal with fetchDeferred/cancelDeferred).
type nativeDeferredProvider struct {
	id               string
	model            ai.Model
	fetchedBaseURL   *string
	fetchedOptions   **ai.DeferredFetchOptions
	cancelledID      *string
	cancelledOptions **ai.ProviderRequestOptions
}

func (p *nativeDeferredProvider) ID() string                     { return p.id }
func (p *nativeDeferredProvider) Name() string                   { return p.id }
func (p *nativeDeferredProvider) BaseURL() string                { return "" }
func (p *nativeDeferredProvider) Headers() types.ProviderHeaders { return nil }
func (p *nativeDeferredProvider) Auth() auth.ProviderAuth {
	// pi: resolve: async () => ({ auth: { apiKey: "key" }, source: "native" }) —
	// unconditional resolution, no stored credential required.
	return auth.ProviderAuth{APIKey: &unconditionalAPIKeyAuth{name: "Native key", key: "key", source: "native"}}
}
func (p *nativeDeferredProvider) GetModels() []ai.Model {
	model := p.model
	return []ai.Model{model}
}
func (p *nativeDeferredProvider) Stream(ctx context.Context, model *ai.Model, llmContext *ai.TranscriptContext, options *ai.StreamOptions) *ai.AssistantMessageEventStream {
	panic("unused")
}
func (p *nativeDeferredProvider) StreamSimple(ctx context.Context, model *ai.Model, llmContext *ai.TranscriptContext, options *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream {
	panic("unused")
}

func (p *nativeDeferredProvider) FetchDeferred(ctx context.Context, model *ai.Model, handle *ai.DeferredHandle, options *ai.DeferredFetchOptions) *ai.AssistantMessageEventStream {
	*p.fetchedBaseURL = model.BaseURL
	*p.fetchedOptions = options
	stream := ai.NewAssistantMessageEventStream()
	message := &ai.AssistantMessage{
		Role: ai.RoleAssistant, Content: []ai.Content{},
		API: model.API, Provider: model.Provider, Model: model.ID,
		Usage: ai.Usage{}, StopReason: ai.StopReasonStop, Timestamp: 0,
	}
	stream.Push(ai.AssistantMessageEvent{Type: ai.EventStart, Partial: message})
	stream.Push(ai.AssistantMessageEvent{Type: ai.EventDone, Reason: ai.StopReasonStop, Message: message})
	stream.End()
	return stream
}

func (p *nativeDeferredProvider) CancelDeferred(ctx context.Context, model *ai.Model, handle *ai.DeferredHandle, options *ai.DeferredCancelOptions) error {
	*p.cancelledID = handle.ID
	*p.cancelledOptions = options
	return nil
}

func TestModelRuntimeAppliesModelsJSONOverridesAboveNativeProviders(t *testing.T) {
	tempDir := t.TempDir()
	modelsPath := filepath.Join(tempDir, "models.json")
	modelsJSON, err := json.Marshal(map[string]any{
		"providers": map[string]any{
			"extension-native": map[string]any{
				"modelOverrides": map[string]any{
					"native": map[string]any{"contextWindow": 4242},
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(modelsPath, modelsJSON, 0o600); err != nil {
		t.Fatal(err)
	}

	runtime := CreateModelRuntime(context.Background(), &CreateModelRuntimeOptions{
		Credentials:       NewInMemoryAuthStorage(authStorageData{}),
		ModelsStore:       &InMemoryCodingAgentModelsStore{},
		ModelsPath:        lo.ToPtr(modelsPath),
		AllowModelNetwork: false,
	})
	nativeModel := lifecycleModel("native")
	nativeModel.Provider = "extension-native"
	nativeModel.BaseURL = "https://native.test/v1"
	if err := runtime.RegisterNativeProvider(ai.CreateProvider(&ai.CreateProviderOptions{
		ID:     "extension-native",
		Name:   "Extension Native",
		Auth:   auth.ProviderAuth{APIKey: &staticAPIKeyAuth{name: "Native key", key: "key"}},
		Models: []ai.Model{nativeModel},
		API:    notUsedStreams{},
	})); err != nil {
		t.Fatal(err)
	}

	model, ok := runtime.GetModel("extension-native", "native")
	if !ok {
		t.Fatal("model missing")
	}
	if model.ContextWindow != 4242 {
		t.Fatalf("contextWindow = %d, want 4242", model.ContextWindow)
	}
}

func TestModelRuntimePublishesRefreshModelsResultsWithoutForcingModelsStorePersistence(t *testing.T) {
	modelsStore := &InMemoryCodingAgentModelsStore{}
	runtime := CreateModelRuntime(context.Background(), &CreateModelRuntimeOptions{
		Credentials:       NewInMemoryAuthStorage(authStorageData{}),
		ModelsStore:       modelsStore,
		ModelsPath:        lo.ToPtr(""),
		AllowModelNetwork: false,
	})
	liveModel := lifecycleModel("live")
	liveModel.Provider = "extension-dynamic"
	liveModel.BaseURL = "http://localhost:8080/v1"
	if err := runtime.RegisterProvider("extension-dynamic", &ProviderConfigInput{
		BaseURL: "http://localhost:8080/v1",
		APIKey:  "local",
		API:     ai.ApiOpenAICompletions,
		RefreshModels: func(ctx context.Context, refreshCtx *ai.RefreshModelsContext) ([]ProviderConfigInputModel, error) {
			return []ProviderConfigInputModel{providerConfigModelFrom(liveModel)}, nil
		},
	}); err != nil {
		t.Fatal(err)
	}

	runtime.Refresh(context.Background(), &ai.ModelsRefreshOptions{AllowNetwork: lo.ToPtr(false)})
	if _, ok := runtime.GetModel("extension-dynamic", "live"); !ok {
		t.Fatal("live model missing after refresh")
	}
	entry, err := modelsStore.Read(context.Background(), "extension-dynamic")
	if err != nil {
		t.Fatal(err)
	}
	if entry != nil {
		t.Fatalf("modelsStore entry = %+v, want nil", entry)
	}
}

func providerConfigModelFrom(model ai.Model) ProviderConfigInputModel {
	return ProviderConfigInputModel{
		ID: model.ID, Name: model.Name, API: model.API, BaseURL: model.BaseURL,
		Reasoning: model.Reasoning, ThinkingLevelMap: model.ThinkingLevelMap,
		Input: model.Input, Cost: model.Cost, ContextWindow: model.ContextWindow,
		MaxTokens: model.MaxTokens, SamplingParams: model.SamplingParams,
		Headers: model.Headers, Compat: model.Compat,
	}
}

func TestModelRuntimeAppliesLegacyOAuthModifyModelsAfterAsyncCredentialInitialization(t *testing.T) {
	runtime := CreateModelRuntime(context.Background(), &CreateModelRuntimeOptions{
		Credentials: NewInMemoryAuthStorage(credData(
			"extension-oauth", oauthCred("access", "refresh", time.Now().Add(60_000).UnixMilli()),
		)),
		ModelsStore:       &InMemoryCodingAgentModelsStore{},
		ModelsPath:        lo.ToPtr(""),
		AllowModelNetwork: false,
	})
	baseModel := lifecycleModel("base")
	baseModel.Provider = "extension-oauth"
	if err := runtime.RegisterProvider("extension-oauth", &ProviderConfigInput{
		BaseURL: "https://example.test/v1",
		API:     ai.ApiOpenAICompletions,
		Models:  []ProviderConfigInputModel{providerConfigModelFrom(baseModel)},
		OAuth: &ExtensionOAuthConfig{
			Name: "Extension OAuth",
			Login: func(callbacks ai.OAuthLoginCallbacks) (auth.OAuthCredentials, error) {
				return auth.OAuthCredentials{}, errors.New("not used")
			},
			RefreshToken: func(ctx context.Context, credential auth.OAuthCredentials) (auth.OAuthCredentials, error) {
				return credential, nil
			},
			GetAPIKey: func(credentials auth.OAuthCredentials) string { return credentials.Access },
			ModifyModels: func(models []ai.Model, credential auth.OAuthCredentials) []ai.Model {
				if credential.Access == "access" {
					credentialModel := lifecycleModel("credential-model")
					credentialModel.Provider = "extension-oauth"
					return append(models, credentialModel)
				}
				return models
			},
		},
	}); err != nil {
		t.Fatal(err)
	}

	if _, ok := runtime.GetModel("extension-oauth", "base"); !ok {
		t.Fatal("base model missing")
	}
	if _, ok := runtime.GetModel("extension-oauth", "credential-model"); !ok {
		t.Fatal("credential-model missing after credential-aware modification")
	}

	if err := runtime.Logout(context.Background(), "extension-oauth"); err != nil {
		t.Fatal(err)
	}
	if _, ok := runtime.GetModel("extension-oauth", "credential-model"); ok {
		t.Fatal("credential-model should be gone after logout")
	}
}
