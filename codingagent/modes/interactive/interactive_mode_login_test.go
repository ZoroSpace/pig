// interactive_mode_login_test.go (pi: test/oauth-selector.test.ts — the
// getLoginProviderOptions projection case; the OAuthSelectorComponent render
// cases live in the components package).
package interactive

import (
	"context"
	"testing"

	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/auth"
	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/codingagent/modes/interactive/components"
)

// fakeAuthAPIKey is a named API-key auth (pi's fake method { name, login }).
type fakeAuthAPIKey struct{ name string }

func (f *fakeAuthAPIKey) Name() string { return f.name }
func (f *fakeAuthAPIKey) Resolve(auth.ApiKeyAuthInput) (*auth.AuthResult, error) {
	return nil, nil
}

// fakeAuthOAuth is a named OAuth auth (pi's fake method { name, login }).
type fakeAuthOAuth struct{ name string }

func (f *fakeAuthOAuth) Name() string         { return f.name }
func (f *fakeAuthOAuth) IsSubscription() bool { return false }
func (f *fakeAuthOAuth) LoginLabel() string   { return "" }
func (f *fakeAuthOAuth) Login(context.Context, auth.AuthInteraction) (*auth.OAuthCredential, error) {
	return nil, nil
}
func (f *fakeAuthOAuth) Refresh(context.Context, *auth.OAuthCredential) (*auth.OAuthCredential, error) {
	return nil, nil
}
func (f *fakeAuthOAuth) ToAuth(*auth.OAuthCredential) (*auth.ModelAuth, error) {
	return nil, nil
}

// fakeAuthProvider is a minimal provider carrying fake auth methods (pi's
// fake providers in the getLoginProviderOptions test).
type fakeAuthProvider struct {
	id     string
	name   string
	oauth  auth.OAuthAuth
	apiKey auth.ApiKeyAuth
	models []ai.Model
}

func (f *fakeAuthProvider) ID() string                     { return f.id }
func (f *fakeAuthProvider) Name() string                   { return f.name }
func (f *fakeAuthProvider) BaseURL() string                { return "" }
func (f *fakeAuthProvider) Headers() types.ProviderHeaders { return nil }
func (f *fakeAuthProvider) Auth() auth.ProviderAuth {
	return auth.ProviderAuth{APIKey: f.apiKey, OAuth: f.oauth}
}
func (f *fakeAuthProvider) GetModels() []ai.Model { return f.models }
func (f *fakeAuthProvider) Stream(context.Context, *ai.Model, *ai.Context, *ai.StreamOptions) *ai.AssistantMessageEventStream {
	return ai.NewAssistantMessageEventStream()
}
func (f *fakeAuthProvider) StreamSimple(context.Context, *ai.Model, *ai.Context, *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream {
	return ai.NewAssistantMessageEventStream()
}

func TestGetLoginProviderOptionsProjectsProviderAuthWithoutFiltering(t *testing.T) {
	session := createInteractiveTestSession(t, &fixtureResourceLoader{}, nil)
	mode := newSeamTestMode(session, nil)

	runtime := session.GetModelRuntime()
	if err := runtime.RegisterNativeProvider(&fakeAuthProvider{
		id:     "anthropic",
		name:   "Anthropic",
		oauth:  &fakeAuthOAuth{name: "Anthropic (Claude Pro/Max)"},
		apiKey: &fakeAuthAPIKey{name: "Anthropic API key"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.RegisterNativeProvider(&fakeAuthProvider{
		id:     "google-vertex",
		name:   "Google Vertex AI",
		apiKey: &fakeAuthAPIKey{name: "Google Cloud credentials"},
	}); err != nil {
		t.Fatal(err)
	}

	apiKeyType := auth.AuthTypeAPIKey
	apiKeyOptions := mode.getLoginProviderOptions(&apiKeyType)
	// pig adaptation: the real runtime also carries the builtin apiKey
	// providers (pi's test isolates via a fake `this`), so the projection is
	// asserted by locating the fake providers instead of exact-list equality.
	var anthropicKey, googleKey *components.AuthSelectorProvider
	for i := range apiKeyOptions {
		switch apiKeyOptions[i].ID {
		case "anthropic":
			anthropicKey = &apiKeyOptions[i]
		case "google-vertex":
			googleKey = &apiKeyOptions[i]
		}
	}
	if anthropicKey == nil || googleKey == nil {
		t.Fatalf("api_key options missing fake providers: %+v", apiKeyOptions)
	}
	if anthropicKey.Name != "Anthropic" || anthropicKey.AuthType != auth.AuthTypeAPIKey {
		t.Fatalf("api_key anthropic = %+v, want Anthropic/API key", anthropicKey)
	}
	if name := components.AuthSelectorMethodName(anthropicKey.Method); name != "Anthropic API key" {
		t.Fatalf("api_key method = %q, want Anthropic API key", name)
	}
	if googleKey.Name != "Google Vertex AI" || googleKey.AuthType != auth.AuthTypeAPIKey {
		t.Fatalf("api_key google-vertex = %+v, want Google Vertex AI/API key", googleKey)
	}
	if name := components.AuthSelectorMethodName(googleKey.Method); name != "Google Cloud credentials" {
		t.Fatalf("api_key method = %q, want Google Cloud credentials", name)
	}

	oauthType := auth.AuthTypeOAuth
	oauthOptions := mode.getLoginProviderOptions(&oauthType)
	if len(oauthOptions) != 1 {
		t.Fatalf("oauth options = %d entries, want 1", len(oauthOptions))
	}
	if oauthOptions[0].ID != "anthropic" || oauthOptions[0].Name != "Anthropic" || oauthOptions[0].AuthType != auth.AuthTypeOAuth {
		t.Fatalf("oauth option[0] = %+v, want anthropic/oauth", oauthOptions[0])
	}
}
