// Ported from pi's test/model-runtime-auth-options.test.ts, adapted to pig's
// builtin provider set: pig ships the completions-family subset only (Phase 2
// decision) and has no OAuth builtin providers, so the provider-projection
// assertions check pig's providers and the OAuth distinction moves to
// extension-registered providers. pi's AbortSignal assertions use contexts.
package core

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/auth"
	"github.com/ZoroSpace/pig/ai/types"
	"github.com/samber/lo"
)

type authMethodOption struct {
	authtype string
	provider ai.Provider
	method   auth.ApiKeyAuth
	oauth    auth.OAuthAuth
}

// errorStream produces a terminal error stream (pi: throwing inside the
// stream function; LazyStream encodes it).
func errorStream(message string) *ai.AssistantMessageEventStream {
	stream := ai.NewAssistantMessageEventStream()
	msg := &ai.AssistantMessage{
		Role:         ai.RoleAssistant,
		Content:      []ai.Content{},
		API:          ai.ApiOpenAICompletions,
		Provider:     "test",
		Model:        "test",
		Usage:        ai.Usage{},
		StopReason:   ai.StopReasonError,
		ErrorMessage: message,
		Timestamp:    0,
	}
	stream.Push(ai.AssistantMessageEvent{Type: ai.EventError, Reason: ai.StopReasonError, Message: msg})
	stream.End()
	return stream
}

func authOptions(t *testing.T, runtime *ModelRuntime, authtype string) []authMethodOption {
	t.Helper()
	var options []authMethodOption
	for _, provider := range runtime.GetProviders() {
		providerAuth := provider.Auth()
		if authtype == "" || authtype == "oauth" {
			if providerAuth.OAuth != nil {
				options = append(options, authMethodOption{authtype: "oauth", provider: provider, oauth: providerAuth.OAuth})
			}
		}
		if authtype == "" || authtype == "api_key" {
			if providerAuth.APIKey != nil {
				options = append(options, authMethodOption{authtype: "api_key", provider: provider, method: providerAuth.APIKey})
			}
		}
	}
	return options
}

func runtimeTestModel(id string) ProviderConfigInputModel {
	return ProviderConfigInputModel{
		ID:            id,
		Name:          id,
		Reasoning:     false,
		Input:         []ai.ModelInput{ai.ModelInputText},
		Cost:          ai.ModelCost{},
		ContextWindow: 10000,
		MaxTokens:     1000,
	}
}

func TestModelRuntimeAcceptsAPigAICredentialStore(t *testing.T) {
	credentials := &auth.InMemoryCredentialStore{}
	if _, err := credentials.Modify(context.Background(), "deepseek", func(current auth.Credential) (auth.Credential, error) {
		return &auth.ApiKeyCredential{Key: "stored-key"}, nil
	}); err != nil {
		t.Fatal(err)
	}
	runtime := CreateModelRuntime(context.Background(), &CreateModelRuntimeOptions{
		Credentials: credentials,
		ModelsPath:  lo.ToPtr(""),
	})
	result, err := runtime.GetAuth(context.Background(), "deepseek", nil)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || result.Auth.APIKey != "stored-key" {
		t.Fatalf("auth = %+v, want stored-key", result)
	}
}

func TestModelRuntimeScopesProviderAvailabilityReadsAndRecordsRefreshFailures(t *testing.T) {
	base := &auth.InMemoryCredentialStore{}
	var mu sync.Mutex
	var reads []string
	failReads := false
	credentials := &scopedReadStore{base: base, reads: &reads, fail: &failReads, mu: &mu}
	runtime := CreateModelRuntime(context.Background(), &CreateModelRuntimeOptions{
		Credentials: credentials,
		ModelsPath:  lo.ToPtr(""),
	})

	mu.Lock()
	reads = nil
	mu.Unlock()
	if _, err := runtime.GetAvailableForProvider(context.Background(), "deepseek"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	readSet := map[string]bool{}
	for _, id := range reads {
		readSet[id] = true
	}
	mu.Unlock()
	if len(readSet) != 1 || !readSet["deepseek"] {
		t.Fatalf("reads = %v, want only deepseek", readSet)
	}

	mu.Lock()
	failReads = true
	mu.Unlock()
	_, err := runtime.GetAvailableForProvider(context.Background(), "deepseek")
	if err == nil || !strings.Contains(err.Error(), "Credential store read failed for deepseek") {
		t.Fatalf("error = %v, want credential read failure", err)
	}
	if !strings.Contains(runtime.GetError(), "Availability refresh: Credential store read failed for deepseek") {
		t.Fatalf("error = %q", runtime.GetError())
	}

	mu.Lock()
	failReads = false
	mu.Unlock()
	if _, err := runtime.GetAvailable(context.Background()); err != nil {
		t.Fatal(err)
	}
	if runtime.GetError() != "" {
		t.Fatalf("error = %q, want empty", runtime.GetError())
	}
}

type scopedReadStore struct {
	base  *auth.InMemoryCredentialStore
	reads *[]string
	fail  *bool
	mu    *sync.Mutex
}

func (s *scopedReadStore) Read(ctx context.Context, providerID string) (auth.Credential, error) {
	s.mu.Lock()
	*s.reads = append(*s.reads, providerID)
	fail := *s.fail
	s.mu.Unlock()
	if fail {
		return nil, &credentialReadError{providerID: providerID}
	}
	return s.base.Read(ctx, providerID)
}

type credentialReadError struct{ providerID string }

func (e *credentialReadError) Error() string { return "read failed for " + e.providerID }

func (s *scopedReadStore) List(ctx context.Context) ([]auth.CredentialInfo, error) {
	return s.base.List(ctx)
}

func (s *scopedReadStore) Modify(ctx context.Context, providerID string, fn func(current auth.Credential) (auth.Credential, error)) (auth.Credential, error) {
	return s.base.Modify(ctx, providerID, fn)
}

func (s *scopedReadStore) Delete(ctx context.Context, providerID string) error {
	return s.base.Delete(ctx, providerID)
}

func TestModelRuntimeProjectsProviderOwnedMethodsNamesAndStatus(t *testing.T) {
	runtime := CreateModelRuntime(context.Background(), &CreateModelRuntimeOptions{
		Credentials: NewInMemoryAuthStorage(authStorageData{}),
		ModelsPath:  lo.ToPtr(""),
	})
	options := authOptions(t, runtime, "")

	found := map[string]string{}
	oauthFound := map[string]string{}
	for _, option := range options {
		if option.authtype != "api_key" {
			// A2: anthropic and kimi-coding carry builtin OAuth methods
			// (pi: anthropicOAuth / kimiCodingOAuth); the method surface is
			// the OAuthAuth, not the ApiKeyAuth.
			oauthFound[option.provider.ID()] = option.oauth.Name()
			continue
		}
		found[option.provider.ID()] = option.method.Name()
	}
	// pig's builtin set: every provider projects an API-key method with its
	// own display name (pi asserts the same for amazon-bedrock /
	// google-vertex / cloudflare, which pig defers).
	for _, providerID := range []string{"deepseek", "openrouter", "zai", "groq"} {
		if found[providerID] == "" {
			t.Fatalf("provider %s has no api_key method", providerID)
		}
	}
	if found["deepseek"] != "DeepSeek API key" {
		t.Fatalf("deepseek method name = %q", found["deepseek"])
	}
	// The A2 builtin OAuth methods project their flow names.
	if got := oauthFound["anthropic"]; got != "Anthropic (Claude Pro/Max)" {
		t.Errorf("anthropic oauth method = %q, want Anthropic (Claude Pro/Max)", got)
	}
	if got := oauthFound["kimi-coding"]; got != "Kimi Code (subscription)" {
		t.Errorf("kimi-coding oauth method = %q, want Kimi Code (subscription)", got)
	}
	// Type filters.
	for _, option := range authOptions(t, runtime, "api_key") {
		if option.authtype != "api_key" {
			t.Fatal("api_key filter leaked other types")
		}
	}
	oauthOptions := authOptions(t, runtime, "oauth")
	if len(oauthOptions) != 2 {
		t.Fatalf("oauth options = %d, want the two A2 builtin oauth providers", len(oauthOptions))
	}
	for _, option := range oauthOptions {
		id := option.provider.ID()
		if id != "anthropic" && id != "kimi-coding" {
			t.Fatalf("unexpected oauth builtin: %s", id)
		}
	}
}

func TestModelRuntimeAttachesTheProvidersActiveAuthStatusToEveryMethodOption(t *testing.T) {
	// pi stores an OAuth credential for the anthropic builtin (which has an
	// OAuth method); pig has no builtin OAuth provider, so the same
	// projection is checked through an extension-registered OAuth provider.
	runtime := CreateModelRuntime(context.Background(), &CreateModelRuntimeOptions{
		Credentials: NewInMemoryAuthStorage(credData(
			"ext-oauth-status", oauthCred("access", "refresh", time.Now().Add(60_000).UnixMilli()),
		)),
		ModelsPath: lo.ToPtr(""),
	})
	if err := runtime.RegisterProvider("ext-oauth-status", &ProviderConfigInput{
		Name:    "Ext OAuth Status",
		BaseURL: "https://example.test/v1",
		API:     ai.ApiOpenAICompletions,
		Models:  []ProviderConfigInputModel{runtimeTestModel("ext-oauth-status-model")},
		OAuth: &ExtensionOAuthConfig{
			Name: "Ext OAuth",
			Login: func(callbacks ai.OAuthLoginCallbacks) (auth.OAuthCredentials, error) {
				return auth.OAuthCredentials{Access: "access", Refresh: "refresh", Expires: time.Now().Add(60_000).UnixMilli()}, nil
			},
			RefreshToken: func(ctx context.Context, credentials auth.OAuthCredentials) (auth.OAuthCredentials, error) {
				return credentials, nil
			},
			GetAPIKey: func(credentials auth.OAuthCredentials) string { return credentials.Access },
		},
	}); err != nil {
		t.Fatal(err)
	}
	// Availability refresh attaches the active auth status.
	if _, err := runtime.GetAvailable(context.Background()); err != nil {
		t.Fatal(err)
	}
	check, err := runtime.CheckAuth(context.Background(), "ext-oauth-status")
	if err != nil {
		t.Fatal(err)
	}
	if check == nil || check.Type != auth.AuthTypeOAuth {
		t.Fatalf("checkAuth = %+v, want oauth", check)
	}
}

func TestModelRuntimeDistinguishesSubscriptionOAuthFromGenericOAuthSignIn(t *testing.T) {
	// pi checks builtin OAuth providers (anthropic/openrouter/radius); pig has
	// none, so subscription distinction is checked through extension-registered
	// OAuth providers with stored credentials.
	runtime := CreateModelRuntime(context.Background(), &CreateModelRuntimeOptions{
		Credentials: NewInMemoryAuthStorage(credData(
			"ext-sub", oauthCred("ext-sub-access", "ext-sub-refresh", time.Now().Add(60_000).UnixMilli()),
			"ext-generic", oauthCred("ext-generic-access", "ext-generic-refresh", time.Now().Add(60_000).UnixMilli()),
		)),
		ModelsPath: lo.ToPtr(""),
	})
	if err := runtime.RegisterProvider("ext-sub", &ProviderConfigInput{
		Name:    "Ext Sub",
		BaseURL: "https://example.test/v1",
		API:     ai.ApiOpenAICompletions,
		Models:  []ProviderConfigInputModel{runtimeTestModel("ext-sub-model")},
		OAuth: &ExtensionOAuthConfig{
			Name:           "Ext Subscription",
			IsSubscription: true,
			Login: func(callbacks ai.OAuthLoginCallbacks) (auth.OAuthCredentials, error) {
				return auth.OAuthCredentials{Access: "access", Refresh: "refresh", Expires: time.Now().Add(60_000).UnixMilli()}, nil
			},
			RefreshToken: func(ctx context.Context, credentials auth.OAuthCredentials) (auth.OAuthCredentials, error) {
				return credentials, nil
			},
			GetAPIKey: func(credentials auth.OAuthCredentials) string { return credentials.Access },
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.RegisterProvider("ext-generic", &ProviderConfigInput{
		Name:    "Ext Generic",
		BaseURL: "https://example.test/v1",
		API:     ai.ApiOpenAICompletions,
		Models:  []ProviderConfigInputModel{runtimeTestModel("ext-generic-model")},
		OAuth: &ExtensionOAuthConfig{
			Name: "Ext Generic",
			Login: func(callbacks ai.OAuthLoginCallbacks) (auth.OAuthCredentials, error) {
				return auth.OAuthCredentials{Access: "access", Refresh: "refresh", Expires: time.Now().Add(60_000).UnixMilli()}, nil
			},
			RefreshToken: func(ctx context.Context, credentials auth.OAuthCredentials) (auth.OAuthCredentials, error) {
				return credentials, nil
			},
			GetAPIKey: func(credentials auth.OAuthCredentials) string { return credentials.Access },
		},
	}); err != nil {
		t.Fatal(err)
	}

	// Availability refresh populates snapshot.auth from CheckAuth.
	if _, err := runtime.GetAvailable(context.Background()); err != nil {
		t.Fatal(err)
	}
	if runtime.IsUsingOAuth("ext-sub") != true {
		t.Fatal("ext-sub should be using OAuth")
	}
	if runtime.IsUsingSubscription("ext-sub") != true {
		t.Fatal("ext-sub should be subscription-backed")
	}
	if runtime.IsUsingOAuth("ext-generic") != true {
		t.Fatal("ext-generic should be using OAuth")
	}
	if runtime.IsUsingSubscription("ext-generic") != false {
		t.Fatal("ext-generic should not be subscription-backed")
	}
}

func TestModelRuntimeConstructsAnAPIKeyMethodForAnExtensionAPIKeyProvider(t *testing.T) {
	runtime := CreateModelRuntime(context.Background(), &CreateModelRuntimeOptions{
		Credentials: NewInMemoryAuthStorage(authStorageData{}),
		ModelsPath:  lo.ToPtr(""),
	})
	if err := runtime.RegisterProvider("extension-api-key", &ProviderConfigInput{
		Name:    "Extension API Key",
		BaseURL: "https://example.test/v1",
		APIKey:  "$EXTENSION_TEST_API_KEY",
		API:     ai.ApiOpenAICompletions,
		Models:  []ProviderConfigInputModel{runtimeTestModel("extension-model")},
	}); err != nil {
		t.Fatal(err)
	}

	options := authOptions(t, runtime, "")
	var matched []authMethodOption
	for _, option := range options {
		if option.provider.ID() == "extension-api-key" {
			matched = append(matched, option)
		}
	}
	if len(matched) != 1 || matched[0].authtype != "api_key" {
		t.Fatalf("options = %+v", matched)
	}
	if matched[0].method.Name() != "API key" {
		t.Fatalf("method name = %q", matched[0].method.Name())
	}
	if _, ok := matched[0].method.(auth.ApiKeyAuthLogin); !ok {
		t.Fatal("method login capability missing")
	}
}

func TestModelRuntimeResolvesConfiguredAuthFromRequestScopedEnvironmentOverrides(t *testing.T) {
	runtime := CreateModelRuntime(context.Background(), &CreateModelRuntimeOptions{
		Credentials: NewInMemoryAuthStorage(authStorageData{}),
		ModelsPath:  lo.ToPtr(""),
	})
	if err := runtime.RegisterProvider("request-env-provider", &ProviderConfigInput{
		BaseURL: "https://example.test/v1",
		APIKey:  "$REQUEST_SCOPED_API_KEY",
		Headers: map[string]string{"x-request-value": "$REQUEST_SCOPED_HEADER"},
		API:     ai.ApiOpenAICompletions,
		Models:  []ProviderConfigInputModel{runtimeTestModel("request-env-model")},
	}); err != nil {
		t.Fatal(err)
	}

	result, err := runtime.GetAuth(context.Background(), "request-env-provider", &ModelRuntimeAuthOverrides{
		Env: types.ProviderEnv{
			"REQUEST_SCOPED_API_KEY": "request-key",
			"REQUEST_SCOPED_HEADER":  "request-header",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || result.Auth.APIKey != "request-key" {
		t.Fatalf("result = %+v", result)
	}
	if header := result.Auth.Headers["x-request-value"]; header == nil || *header != "request-header" {
		t.Fatalf("headers = %+v", result.Auth.Headers)
	}
	if len(result.Auth.Headers) != 1 {
		t.Fatalf("headers = %+v, want exactly the configured entry", result.Auth.Headers)
	}
}

func TestModelRuntimeLetsAnExplicitAuthorizationHeaderOverrideAuthHeaderCaseInsensitively(t *testing.T) {
	runtime := CreateModelRuntime(context.Background(), &CreateModelRuntimeOptions{
		Credentials: NewInMemoryAuthStorage(authStorageData{}),
		ModelsPath:  lo.ToPtr(""),
	})
	var capturedHeaders types.ProviderHeaders
	if err := runtime.RegisterProvider("auth-header-provider", &ProviderConfigInput{
		BaseURL:    "https://example.test/v1",
		APIKey:     "generated-key",
		AuthHeader: lo.ToPtr(true),
		API:        ai.ApiOpenAICompletions,
		StreamSimple: func(ctx context.Context, model *ai.Model, llmContext *ai.Context, options *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream {
			capturedHeaders = options.Headers
			return errorStream("captured")
		},
		Models: []ProviderConfigInputModel{runtimeTestModel("auth-header-model")},
	}); err != nil {
		t.Fatal(err)
	}
	model, ok := runtime.GetModel("auth-header-provider", "auth-header-model")
	if !ok {
		t.Fatal("model missing")
	}

	runtime.CompleteSimple(context.Background(), &model, &ai.Context{}, &ai.ModelsSimpleStreamOptions{
		SimpleStreamOptions: ai.SimpleStreamOptions{
			StreamOptions: ai.StreamOptions{
				ProviderRequestOptions: types.ProviderRequestOptions{
					Headers: types.ProviderHeaders{"authorization": lo.ToPtr("Explicit token")},
				},
			},
		},
	})

	if header := capturedHeaders["authorization"]; header == nil || *header != "Explicit token" {
		t.Fatalf("captured = %+v", capturedHeaders)
	}
	if _, ok := capturedHeaders["Authorization"]; ok {
		t.Fatalf("captured = %+v, want the caller's casing only", capturedHeaders)
	}
}

func TestModelRuntimeTransformsFullyAssembledHeadersOnceWithoutForwardingTheTransform(t *testing.T) {
	runtime := CreateModelRuntime(context.Background(), &CreateModelRuntimeOptions{
		Credentials: NewInMemoryAuthStorage(authStorageData{}),
		ModelsPath:  lo.ToPtr(""),
	})
	var capturedHeaders types.ProviderHeaders
	transforms := 0
	headerModel := runtimeTestModel("header-model")
	headerModel.Headers = map[string]string{"x-model": "model"}
	if err := runtime.RegisterProvider("header-provider", &ProviderConfigInput{
		BaseURL:    "https://example.test/v1",
		APIKey:     "generated-key",
		AuthHeader: lo.ToPtr(true),
		Headers:    map[string]string{"x-provider": "provider"},
		API:        ai.ApiOpenAICompletions,
		StreamSimple: func(ctx context.Context, model *ai.Model, llmContext *ai.Context, options *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream {
			capturedHeaders = options.Headers
			return errorStream("captured")
		},
		Models: []ProviderConfigInputModel{headerModel},
	}); err != nil {
		t.Fatal(err)
	}
	model, ok := runtime.GetModel("header-provider", "header-model")
	if !ok {
		t.Fatal("model missing")
	}

	runtime.CompleteSimple(context.Background(), &model, &ai.Context{}, &ai.ModelsSimpleStreamOptions{
		SimpleStreamOptions: ai.SimpleStreamOptions{
			StreamOptions: ai.StreamOptions{
				ProviderRequestOptions: types.ProviderRequestOptions{
					Headers: types.ProviderHeaders{"x-explicit": lo.ToPtr("explicit")},
				},
			},
		},
		ModelsRequestTransforms: ai.ModelsRequestTransforms{
			TransformHeaders: func(headers types.ProviderHeaders) (types.ProviderHeaders, error) {
				transforms++
				want := map[string]string{
					"Authorization": "Bearer generated-key",
					"x-provider":    "provider",
					"x-model":       "model",
					"x-explicit":    "explicit",
				}
				if len(headers) != len(want) {
					t.Fatalf("transform input = %+v, want %+v", headers, want)
				}
				for key, value := range want {
					if header := headers[key]; header == nil || *header != value {
						t.Fatalf("transform input = %+v, want %+v", headers, want)
					}
				}
				out := types.ProviderHeaders{}
				for key, value := range headers {
					out[key] = value
				}
				out["x-transformed"] = lo.ToPtr("yes")
				return out, nil
			},
		},
	})

	if transforms != 1 {
		t.Fatalf("transforms = %d, want 1", transforms)
	}
	want := map[string]string{
		"Authorization": "Bearer generated-key",
		"x-provider":    "provider",
		"x-model":       "model",
		"x-explicit":    "explicit",
		"x-transformed": "yes",
	}
	if len(capturedHeaders) != len(want) {
		t.Fatalf("captured = %+v, want %+v", capturedHeaders, want)
	}
	for key, value := range want {
		if header := capturedHeaders[key]; header == nil || *header != value {
			t.Fatalf("captured = %+v, want %+v", capturedHeaders, want)
		}
	}
}

func TestModelRuntimeForwardsCancellationToExtensionOAuthRefresh(t *testing.T) {
	credentials := NewInMemoryAuthStorage(credData(
		"extension-oauth", oauthCred("expired", "refresh", 0),
	))
	runtime := CreateModelRuntime(context.Background(), &CreateModelRuntimeOptions{
		Credentials: credentials,
		ModelsPath:  lo.ToPtr(""),
	})
	refreshCtxs := make(chan context.Context, 4)
	if err := runtime.RegisterProvider("extension-oauth", &ProviderConfigInput{
		Name:    "Extension OAuth",
		BaseURL: "https://example.test/v1",
		API:     ai.ApiOpenAICompletions,
		Models:  []ProviderConfigInputModel{runtimeTestModel("extension-model")},
		OAuth: &ExtensionOAuthConfig{
			Name: "Extension subscription",
			Login: func(callbacks ai.OAuthLoginCallbacks) (auth.OAuthCredentials, error) {
				return auth.OAuthCredentials{Access: "access", Refresh: "refresh", Expires: time.Now().Add(60_000).UnixMilli()}, nil
			},
			RefreshToken: func(ctx context.Context, credential auth.OAuthCredentials) (auth.OAuthCredentials, error) {
				refreshCtxs <- ctx
				return credential, nil
			},
			GetAPIKey: func(credentials auth.OAuthCredentials) string { return credentials.Access },
		},
	}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())

	if _, err := runtime.GetAuth(ctx, "extension-oauth", nil); err != nil {
		t.Fatal(err)
	}
	select {
	case received := <-refreshCtxs:
		cancel()
		select {
		case <-received.Done():
		case <-time.After(time.Second):
			t.Fatal("refresh context was not cancelled")
		}
	case <-time.After(time.Second):
		t.Fatal("refresh was not called")
	}
}

func TestModelRuntimeDoesNotFabricateAnAPIKeyMethodForAnExtensionOAuthOnlyProvider(t *testing.T) {
	runtime := CreateModelRuntime(context.Background(), &CreateModelRuntimeOptions{
		Credentials: NewInMemoryAuthStorage(authStorageData{}),
		ModelsPath:  lo.ToPtr(""),
	})
	if err := runtime.RegisterProvider("extension-oauth", &ProviderConfigInput{
		Name:    "Extension OAuth",
		BaseURL: "https://example.test/v1",
		API:     ai.ApiOpenAICompletions,
		Models:  []ProviderConfigInputModel{runtimeTestModel("extension-model")},
		OAuth: &ExtensionOAuthConfig{
			Name:           "Extension subscription",
			IsSubscription: true,
			Login: func(callbacks ai.OAuthLoginCallbacks) (auth.OAuthCredentials, error) {
				return auth.OAuthCredentials{Access: "access", Refresh: "refresh", Expires: time.Now().Add(60_000).UnixMilli()}, nil
			},
			RefreshToken: func(ctx context.Context, credentials auth.OAuthCredentials) (auth.OAuthCredentials, error) {
				return credentials, nil
			},
			GetAPIKey: func(credentials auth.OAuthCredentials) string { return credentials.Access },
		},
	}); err != nil {
		t.Fatal(err)
	}

	var matched []authMethodOption
	for _, option := range authOptions(t, runtime, "") {
		if option.provider.ID() == "extension-oauth" {
			matched = append(matched, option)
		}
	}
	if len(matched) != 1 || matched[0].authtype != "oauth" {
		t.Fatalf("options = %+v", matched)
	}
	if matched[0].oauth.Name() != "Extension subscription" || !matched[0].oauth.IsSubscription() {
		t.Fatalf("method = %+v", matched[0].oauth)
	}
}
