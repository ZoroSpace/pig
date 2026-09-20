// pi: test/env-api-keys.test.ts (ported in full), plus pig-side coverage for
// the withEnvApiKey wiring in compat_registry.go and the Vertex ADC / Bedrock
// ambient branches (pi keeps those uncovered in this suite; they are exercised
// here through scoped ProviderEnv so no process env leaks between tests).
package ai

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/ZoroSpace/pig/ai/types"
)

// unsetEnvVar unsets an env var for the test and restores the previous state
// afterwards (pi's afterEach restore; t.Setenv cannot express unset).
func unsetEnvVar(t *testing.T, name string) {
	t.Helper()
	old, had := os.LookupEnv(name)
	os.Unsetenv(name)
	if had {
		t.Cleanup(func() { os.Setenv(name, old) })
	}
}

func TestEnvApiKeysGitHubCopilotGenericTokensIgnored(t *testing.T) {
	unsetEnvVar(t, "COPILOT_GITHUB_TOKEN")
	t.Setenv("GH_TOKEN", "gh-token")
	t.Setenv("GITHUB_TOKEN", "github-token")

	if got := FindEnvKeys("github-copilot", nil); got != nil {
		t.Errorf("FindEnvKeys(github-copilot) = %v, want nil", got)
	}
	if got := GetEnvApiKey("github-copilot", nil); got != "" {
		t.Errorf("GetEnvApiKey(github-copilot) = %q, want empty", got)
	}
}

func TestEnvApiKeysGitHubCopilotFromCopilotToken(t *testing.T) {
	t.Setenv("COPILOT_GITHUB_TOKEN", "copilot-token")
	t.Setenv("GH_TOKEN", "gh-token")
	t.Setenv("GITHUB_TOKEN", "github-token")

	assertEnvKeys(t, "github-copilot", nil, []string{"COPILOT_GITHUB_TOKEN"}, "copilot-token")
}

func TestEnvApiKeysZaiCodingCN(t *testing.T) {
	t.Setenv("ZAI_CODING_CN_API_KEY", "zai-coding-cn-token")

	assertEnvKeys(t, "zai-coding-cn", nil, []string{"ZAI_CODING_CN_API_KEY"}, "zai-coding-cn-token")
}

func TestEnvApiKeysAnthropicReportsAuthTokenPrefersOAuth(t *testing.T) {
	unsetEnvVar(t, "ANTHROPIC_AUTH_TOKEN")
	unsetEnvVar(t, "ANTHROPIC_OAUTH_TOKEN")
	unsetEnvVar(t, "ANTHROPIC_API_KEY")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "auth-token")
	t.Setenv("ANTHROPIC_OAUTH_TOKEN", "oauth-token")
	t.Setenv("ANTHROPIC_API_KEY", "api-key")

	assertEnvKeys(t, "anthropic", nil,
		[]string{"ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_OAUTH_TOKEN", "ANTHROPIC_API_KEY"}, "oauth-token")
}

func TestEnvApiKeysAnthropicAuthTokenNotAnAPIKey(t *testing.T) {
	unsetEnvVar(t, "ANTHROPIC_AUTH_TOKEN")
	unsetEnvVar(t, "ANTHROPIC_OAUTH_TOKEN")
	unsetEnvVar(t, "ANTHROPIC_API_KEY")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "auth-token")

	assertEnvKeys(t, "anthropic", nil, []string{"ANTHROPIC_AUTH_TOKEN"}, "")
}

func TestEnvApiKeysAnthropicOAuthTokenPreserved(t *testing.T) {
	unsetEnvVar(t, "ANTHROPIC_AUTH_TOKEN")
	unsetEnvVar(t, "ANTHROPIC_OAUTH_TOKEN")
	unsetEnvVar(t, "ANTHROPIC_API_KEY")
	t.Setenv("ANTHROPIC_OAUTH_TOKEN", "oauth-token")

	assertEnvKeys(t, "anthropic", nil, []string{"ANTHROPIC_OAUTH_TOKEN"}, "oauth-token")
}

func TestEnvApiKeysAnthropicFallbackAPIKey(t *testing.T) {
	unsetEnvVar(t, "ANTHROPIC_AUTH_TOKEN")
	unsetEnvVar(t, "ANTHROPIC_OAUTH_TOKEN")
	unsetEnvVar(t, "ANTHROPIC_API_KEY")
	t.Setenv("ANTHROPIC_API_KEY", "api-key")

	if got := GetEnvApiKey("anthropic", nil); got != "api-key" {
		t.Errorf("GetEnvApiKey(anthropic) = %q, want api-key", got)
	}
}

// Pig-added: scoped ProviderEnv wins over the process env (pi:
// getProviderEnvValue precedence, exercised directly against GetEnvApiKey).
func TestEnvApiKeyScopedEnvPrecedence(t *testing.T) {
	unsetEnvVar(t, "GEMINI_API_KEY")
	t.Setenv("GEMINI_API_KEY", "process-key")

	got := GetEnvApiKey("google", types.ProviderEnv{"GEMINI_API_KEY": "scoped-key"})
	if got != "scoped-key" {
		t.Errorf("GetEnvApiKey(google, scoped) = %q, want scoped-key", got)
	}
}

func TestGetEnvApiKeyVertexADCAmbient(t *testing.T) {
	unsetEnvVar(t, "GOOGLE_CLOUD_API_KEY")
	credentials := filepath.Join(t.TempDir(), "application_default_credentials.json")
	if err := os.WriteFile(credentials, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := types.ProviderEnv{
		"GOOGLE_APPLICATION_CREDENTIALS": credentials,
		"GOOGLE_CLOUD_PROJECT":           "project",
		"GOOGLE_CLOUD_LOCATION":          "us-central1",
	}

	if got := GetEnvApiKey("google-vertex", env); got != AmbientAuthMarker {
		t.Errorf("GetEnvApiKey(google-vertex, ADC) = %q, want %q", got, AmbientAuthMarker)
	}

	// Missing location: no ambient marker (pi: hasProject && hasLocation).
	incomplete := types.ProviderEnv{
		"GOOGLE_APPLICATION_CREDENTIALS": credentials,
		"GOOGLE_CLOUD_PROJECT":           "project",
	}
	if got := GetEnvApiKey("google-vertex", incomplete); got != "" {
		t.Errorf("GetEnvApiKey(google-vertex, no location) = %q, want empty", got)
	}
}

func TestGetEnvApiKeyBedrockAmbient(t *testing.T) {
	env := types.ProviderEnv{"AWS_PROFILE": "default"}
	if got := GetEnvApiKey("amazon-bedrock", env); got != AmbientAuthMarker {
		t.Errorf("GetEnvApiKey(amazon-bedrock, AWS_PROFILE) = %q, want %q", got, AmbientAuthMarker)
	}

	env = types.ProviderEnv{"AWS_ACCESS_KEY_ID": "id", "AWS_SECRET_ACCESS_KEY": "secret"}
	if got := GetEnvApiKey("amazon-bedrock", env); got != AmbientAuthMarker {
		t.Errorf("GetEnvApiKey(amazon-bedrock, IAM) = %q, want %q", got, AmbientAuthMarker)
	}

	env = types.ProviderEnv{"AWS_ACCESS_KEY_ID": "id"}
	if got := GetEnvApiKey("amazon-bedrock", env); got != "" {
		t.Errorf("GetEnvApiKey(amazon-bedrock, key id only) = %q, want empty", got)
	}
}

func assertEnvKeys(t *testing.T, provider types.ProviderID, env types.ProviderEnv, wantFound []string, wantKey string) {
	t.Helper()
	found := FindEnvKeys(provider, env)
	if len(found) != len(wantFound) {
		t.Fatalf("FindEnvKeys(%s) = %v, want %v", provider, found, wantFound)
	}
	for i := range wantFound {
		if found[i] != wantFound[i] {
			t.Fatalf("FindEnvKeys(%s) = %v, want %v", provider, found, wantFound)
		}
	}
	if got := GetEnvApiKey(provider, env); got != wantKey {
		t.Errorf("GetEnvApiKey(%s) = %q, want %q", provider, got, wantKey)
	}
}

// envKeyCaptureStreams records the options the dispatcher hands to the
// provider (pig-added wiring coverage for withEnvApiKey).
type envKeyCaptureStreams struct {
	mu       sync.Mutex
	captured *SimpleStreamOptions
}

func (s *envKeyCaptureStreams) Stream(ctx context.Context, model *Model, llmContext *TranscriptContext, options *StreamOptions) *AssistantMessageEventStream {
	return NewAssistantMessageEventStream()
}

func (s *envKeyCaptureStreams) StreamSimple(ctx context.Context, model *Model, llmContext *TranscriptContext, options *SimpleStreamOptions) *AssistantMessageEventStream {
	s.mu.Lock()
	s.captured = options
	s.mu.Unlock()
	stream := NewAssistantMessageEventStream()
	message := types.AssistantMessage{
		Role:         types.RoleAssistant,
		Content:      []types.Content{},
		API:          model.API,
		Provider:     model.Provider,
		Model:        model.ID,
		StopReason:   types.StopReasonError,
		ErrorMessage: "captured",
	}
	stream.Push(types.AssistantMessageEvent{Type: types.EventError, Reason: types.StopReasonError, Message: &message})
	stream.EndWith(message)
	return stream
}

func registerEnvKeyCapture(t *testing.T) *envKeyCaptureStreams {
	t.Helper()
	streams := &envKeyCaptureStreams{}
	RegisterApiProvider(Api("test-env-key-fill"), streams, "env-api-keys-test")
	t.Cleanup(func() { UnregisterApiProviders("env-api-keys-test") })
	return streams
}

func TestStreamSimpleWithEnvApiKey(t *testing.T) {
	unsetEnvVar(t, "GEMINI_API_KEY")
	streams := registerEnvKeyCapture(t)
	model := &Model{Provider: "google", API: Api("test-env-key-fill"), ID: "test-model"}

	t.Run("fills from provider env var", func(t *testing.T) {
		t.Setenv("GEMINI_API_KEY", "env-key")
		options := &SimpleStreamOptions{}
		StreamSimple(context.Background(), model, &Context{}, options)
		streams.mu.Lock()
		captured := streams.captured
		streams.mu.Unlock()
		if captured == nil || captured.APIKey != "env-key" {
			t.Fatalf("captured APIKey = %v, want env-key", captured)
		}
		if captured == options {
			t.Error("fill must copy, not mutate the caller's options")
		}
	})

	t.Run("explicit key wins", func(t *testing.T) {
		t.Setenv("GEMINI_API_KEY", "env-key")
		options := &SimpleStreamOptions{}
		options.APIKey = "explicit-key"
		StreamSimple(context.Background(), model, &Context{}, options)
		streams.mu.Lock()
		captured := streams.captured
		streams.mu.Unlock()
		if captured != options || captured.APIKey != "explicit-key" {
			t.Fatalf("captured = %v, want the original options with explicit-key", captured)
		}
	})

	t.Run("ambient marker leaves options untouched", func(t *testing.T) {
		credentials := filepath.Join(t.TempDir(), "adc.json")
		if err := os.WriteFile(credentials, []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
		unsetEnvVar(t, "GOOGLE_CLOUD_API_KEY")
		vertexModel := &Model{Provider: "google-vertex", API: Api("test-env-key-fill"), ID: "test-model"}
		options := &SimpleStreamOptions{}
		options.Env = types.ProviderEnv{
			"GOOGLE_APPLICATION_CREDENTIALS": credentials,
			"GOOGLE_CLOUD_PROJECT":           "project",
			"GOOGLE_CLOUD_LOCATION":          "us-central1",
		}
		StreamSimple(context.Background(), vertexModel, &Context{}, options)
		streams.mu.Lock()
		captured := streams.captured
		streams.mu.Unlock()
		if captured != options || captured.APIKey != "" {
			t.Fatalf("captured = %v, want the original options with empty APIKey (ambient auth)", captured)
		}
	})

	t.Run("no env no key stays empty", func(t *testing.T) {
		options := &SimpleStreamOptions{}
		StreamSimple(context.Background(), model, &Context{}, options)
		streams.mu.Lock()
		captured := streams.captured
		streams.mu.Unlock()
		if captured != options || captured.APIKey != "" {
			t.Fatalf("captured = %v, want the original options with empty APIKey", captured)
		}
	})
}
