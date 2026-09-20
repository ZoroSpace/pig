// pi: test/anthropic-auth-token.test.ts.
//
// The TS tests mock the Anthropic SDK class; pig captures the actual HTTP
// request through a fake transport, asserting the same auth/header surface.
package providers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/api"
	"github.com/ZoroSpace/pig/ai/auth"
	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/ai/utils"
)

// capturedAuthRequest records the wire request of one adapter call.
type capturedAuthRequest struct {
	headers http.Header
	body    map[string]any
}

// fakeAuthFetch serves one minimal SSE stream and captures the request (pi:
// the mocked @anthropic-ai/sdk FakeAnthropic).
func fakeAuthFetch(t *testing.T, captured *capturedAuthRequest) types.FetchFunction {
	t.Helper()
	return func(req *http.Request) (*http.Response, error) {
		captured.headers = req.Header
		bodyBytes, _ := io.ReadAll(req.Body)
		var parsed map[string]any
		_ = json.Unmarshal(bodyBytes, &parsed)
		captured.body = parsed
		return &http.Response{
			StatusCode: 200,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body: io.NopCloser(strings.NewReader(
				"event: message_start\n" +
					`data: {"type":"message_start","message":{"id":"msg_test","usage":{"input_tokens":1,"output_tokens":0}}}` + "\n\n" +
					"event: message_delta\n" +
					`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}` + "\n\n" +
					"event: message_stop\n" +
					`data: {"type":"message_stop"}` + "\n\n")),
			Request: req,
		}, nil
	}
}

// mapAuthContext is already defined in all_test.go.

func TestAnthropicAuthTokenResolveBearerHeader(t *testing.T) {
	provider := AnthropicProvider()
	authResult, err := provider.Auth().APIKey.Resolve(auth.ApiKeyAuthInput{
		Ctx: mapAuthContext{
			"ANTHROPIC_AUTH_TOKEN":  "auth-token",
			"ANTHROPIC_OAUTH_TOKEN": "oauth-token",
			"ANTHROPIC_API_KEY":     "api-key",
		},
		Context: context.Background(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if authResult == nil {
		t.Fatal("expected an auth result")
	}
	if authResult.Auth.APIKey != "" || authResult.Auth.Headers == nil {
		t.Fatalf("auth = %+v, want header auth", authResult.Auth)
	}
	if got := *authResult.Auth.Headers["Authorization"]; got != "Bearer auth-token" {
		t.Errorf("Authorization = %q, want Bearer auth-token", got)
	}
	if authResult.Source != ai.AnthropicAuthTokenEnv {
		t.Errorf("source = %q, want %q", authResult.Source, ai.AnthropicAuthTokenEnv)
	}
}

func TestAnthropicAuthTokenPreservesOAuthShapedToken(t *testing.T) {
	provider := AnthropicProvider()
	authResult, err := provider.Auth().APIKey.Resolve(auth.ApiKeyAuthInput{
		Ctx: mapAuthContext{
			"ANTHROPIC_OAUTH_TOKEN": "oauth-token",
			"ANTHROPIC_API_KEY":     "api-key",
		},
		Context: context.Background(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if authResult == nil || authResult.Auth.APIKey != "oauth-token" || authResult.Source != ai.AnthropicOAuthTokenEnv {
		t.Fatalf("auth = %+v, want apiKey oauth-token from ANTHROPIC_OAUTH_TOKEN", authResult)
	}
}

// runAuthTokenStream drives one stream and captures the wire request.
func runAuthTokenStream(t *testing.T, model *types.Model, llmContext *types.TranscriptContext, options *types.StreamOptions) *capturedAuthRequest {
	t.Helper()
	captured := &capturedAuthRequest{}
	if options == nil {
		options = &types.StreamOptions{}
	}
	options.Fetch = fakeAuthFetch(t, captured)
	if options.APIKey == "" && options.Headers == nil {
		options.APIKey = "unused"
	}
	stream := (&api.AnthropicMessages{}).Stream(context.Background(), model, llmContext, options)
	result := stream.Result()
	if result.StopReason != types.StopReasonStop {
		t.Fatalf("stopReason = %q (%s)", result.StopReason, result.ErrorMessage)
	}
	return captured
}

func TestAnthropicAuthTokenUsesAuthorizationWithoutOAuthShaping(t *testing.T) {
	model := &types.Model{
		ID: "claude-test", Name: "Claude Test", API: types.ApiAnthropicMessages,
		Provider: types.ProviderAnthropic, BaseURL: "https://api.anthropic.com",
		Input: []types.ModelInput{types.ModelInputText}, ContextWindow: 100000, MaxTokens: 4096,
	}
	llmContext := utils.NormalizeContext(&types.Context{
		SystemPrompt: "System prompt.",
		Messages:     []types.Message{types.UserMessage{Role: types.RoleUser, Content: types.UserContent{String: "Hello"}, Timestamp: 1}},
	})
	options := &types.StreamOptions{}
	options.Headers = types.ProviderHeaders{}
	bearer := "Bearer gateway-token"
	options.Headers["Authorization"] = &bearer
	captured := runAuthTokenStream(t, model, llmContext, options)

	if captured.headers.Get("x-api-key") != "" {
		t.Errorf("x-api-key = %q, want absent", captured.headers.Get("x-api-key"))
	}
	if got := captured.headers.Get("Authorization"); got != "Bearer gateway-token" {
		t.Errorf("Authorization = %q, want Bearer gateway-token", got)
	}
	if betas := betasFromBody(captured.body); containsString(betas, "oauth-2025-04-20") {
		t.Errorf("betas = %v, want it to omit oauth-2025-04-20", betas)
	}
	system := assertSystemBlocks(t, captured.body)
	if len(system) != 1 || system[0]["text"] != "System prompt." {
		t.Errorf("system = %#v, want [System prompt.]", system)
	}
}

// mapAuthContextEnvModel builds a Models instance with an injectable env and
// the anthropic provider registered.
func mapAuthContextEnvModel(t *testing.T, env map[string]string) ai.Models {
	t.Helper()
	models := ai.NewModels(&ai.CreateModelsOptions{AuthContext: mapAuthContext(env)})
	models.SetProvider(AnthropicProvider())
	return models
}

func TestAnthropicAuthTokenThreadsAuthContextThroughHeaders(t *testing.T) {
	model := &types.Model{
		ID: "claude-test", Name: "Claude Test", API: types.ApiAnthropicMessages,
		Provider: types.ProviderAnthropic, BaseURL: "https://api.anthropic.com",
		Input: []types.ModelInput{types.ModelInputText}, ContextWindow: 100000, MaxTokens: 4096,
	}
	llmContext := utils.NormalizeContext(&types.Context{
		SystemPrompt: "System prompt.",
		Messages:     []types.Message{types.UserMessage{Role: types.RoleUser, Content: types.UserContent{String: "Hello"}, Timestamp: 1}},
	})
	captured := &capturedAuthRequest{}
	models := mapAuthContextEnvModel(t, map[string]string{"ANTHROPIC_AUTH_TOKEN": "ctx-token"})
	options := &ai.ModelsSimpleStreamOptions{}
	options.Fetch = fakeAuthFetch(t, captured)
	result := models.StreamSimple(context.Background(), model, &ai.Context{Messages: llmContext.Messages}, options).Result()
	if result.StopReason != types.StopReasonStop {
		t.Fatalf("stopReason = %q (%s)", result.StopReason, result.ErrorMessage)
	}

	if captured.headers.Get("x-api-key") != "" {
		t.Errorf("x-api-key = %q, want absent", captured.headers.Get("x-api-key"))
	}
	if got := captured.headers.Get("Authorization"); got != "Bearer ctx-token" {
		t.Errorf("Authorization = %q, want Bearer ctx-token", got)
	}
	if betas := betasFromBody(captured.body); containsString(betas, "oauth-2025-04-20") {
		t.Errorf("betas = %v, want it to omit oauth-2025-04-20", betas)
	}
}

func TestAnthropicAuthTokenPreservesOAuthShaping(t *testing.T) {
	model := &types.Model{
		ID: "claude-test", Name: "Claude Test", API: types.ApiAnthropicMessages,
		Provider: types.ProviderAnthropic, BaseURL: "https://api.anthropic.com",
		Input: []types.ModelInput{types.ModelInputText}, ContextWindow: 100000, MaxTokens: 4096,
	}
	llmContext := utils.NormalizeContext(&types.Context{
		SystemPrompt: "System prompt.",
		Messages:     []types.Message{types.UserMessage{Role: types.RoleUser, Content: types.UserContent{String: "Hello"}, Timestamp: 1}},
	})
	captured := &capturedAuthRequest{}
	models := mapAuthContextEnvModel(t, map[string]string{"ANTHROPIC_OAUTH_TOKEN": "sk-ant-oat-test"})
	options := &ai.ModelsSimpleStreamOptions{}
	options.Fetch = fakeAuthFetch(t, captured)
	result := models.StreamSimple(context.Background(), model, &ai.Context{Messages: llmContext.Messages}, options).Result()
	if result.StopReason != types.StopReasonStop {
		t.Fatalf("stopReason = %q (%s)", result.StopReason, result.ErrorMessage)
	}

	if got := captured.headers.Get("Authorization"); got != "Bearer sk-ant-oat-test" {
		t.Errorf("Authorization = %q, want Bearer sk-ant-oat-test", got)
	}
	if betas := betasFromBody(captured.body); !containsString(betas, "oauth-2025-04-20") {
		t.Errorf("betas = %v, want it to contain oauth-2025-04-20", betas)
	}
}

func TestAnthropicAuthTokenExplicitHeadersOverrideEnv(t *testing.T) {
	model := &types.Model{
		ID: "claude-test", Name: "Claude Test", API: types.ApiAnthropicMessages,
		Provider: types.ProviderAnthropic, BaseURL: "https://api.anthropic.com",
		Input: []types.ModelInput{types.ModelInputText}, ContextWindow: 100000, MaxTokens: 4096,
	}
	llmContext := utils.NormalizeContext(&types.Context{
		SystemPrompt: "System prompt.",
		Messages:     []types.Message{types.UserMessage{Role: types.RoleUser, Content: types.UserContent{String: "Hello"}, Timestamp: 1}},
	})
	captured := &capturedAuthRequest{}
	models := mapAuthContextEnvModel(t, map[string]string{"ANTHROPIC_AUTH_TOKEN": "ctx-token"})
	options := &ai.ModelsSimpleStreamOptions{}
	options.Fetch = fakeAuthFetch(t, captured)
	bearer := "Bearer explicit-token"
	options.Headers = types.ProviderHeaders{"Authorization": &bearer}
	result := models.StreamSimple(context.Background(), model, &ai.Context{Messages: llmContext.Messages}, options).Result()
	if result.StopReason != types.StopReasonStop {
		t.Fatalf("stopReason = %q (%s)", result.StopReason, result.ErrorMessage)
	}
	if got := captured.headers.Get("Authorization"); got != "Bearer explicit-token" {
		t.Errorf("Authorization = %q, want Bearer explicit-token", got)
	}
}

func TestAnthropicCompatibleUserAgents(t *testing.T) {
	model := &types.Model{
		ID: "claude-test", Name: "Claude Test", API: types.ApiAnthropicMessages,
		Provider: types.ProviderAnthropic, BaseURL: "https://api.anthropic.com",
		Input: []types.ModelInput{types.ModelInputText}, ContextWindow: 100000, MaxTokens: 4096,
	}
	kimiModel := *model
	kimiModel.ID = "kimi-for-coding"
	kimiModel.Name = "Kimi For Coding"
	kimiModel.Provider = types.ProviderKimiCoding
	kimiModel.BaseURL = "https://api.kimi.com/coding"
	llmContext := utils.NormalizeContext(&types.Context{
		SystemPrompt: "System prompt.",
		Messages:     []types.Message{types.UserMessage{Role: types.RoleUser, Content: types.UserContent{String: "Hello"}, Timestamp: 1}},
	})

	t.Run("uses pi's User-Agent by default", func(t *testing.T) {
		captured := runAuthTokenStream(t, model, llmContext, nil)
		if got := captured.headers.Get("User-Agent"); got != utils.GetPiUserAgent() {
			t.Errorf("User-Agent = %q, want %q", got, utils.GetPiUserAgent())
		}
	})

	t.Run("lets explicit headers override the default User-Agent", func(t *testing.T) {
		options := &types.StreamOptions{}
		options.APIKey = "kimi-key"
		custom := "custom-client"
		options.Headers = types.ProviderHeaders{"User-Agent": &custom}
		captured := runAuthTokenStream(t, &kimiModel, llmContext, options)
		if got := captured.headers.Get("User-Agent"); got != "custom-client" {
			t.Errorf("User-Agent = %q, want custom-client", got)
		}
	})

	t.Run("preserves explicit Anthropic beta header replacement", func(t *testing.T) {
		options := &types.StreamOptions{}
		options.APIKey = "anthropic-key"
		custom := "custom-beta"
		options.Headers = types.ProviderHeaders{"anthropic-beta": &custom}
		captured := runAuthTokenStream(t, model, llmContext, options)
		if betas := betasFromBody(captured.body); !reflectDeepEqualStrings(betas, []string{"custom-beta"}) {
			t.Errorf("betas = %v, want [custom-beta]", betas)
		}
	})

	t.Run("preserves explicit Anthropic beta header suppression", func(t *testing.T) {
		options := &types.StreamOptions{}
		options.APIKey = "anthropic-key"
		options.Headers = types.ProviderHeaders{"anthropic-beta": nil}
		captured := runAuthTokenStream(t, model, llmContext, options)
		if betas := betasFromBody(captured.body); len(betas) != 0 {
			t.Errorf("betas = %v, want none", betas)
		}
	})
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func reflectDeepEqualStrings(got []string, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// assertSystemBlocks reads the system param as block maps (pi:
// expect.objectContaining({ text: "System prompt." })).
func assertSystemBlocks(t *testing.T, body map[string]any) []map[string]any {
	t.Helper()
	raw, ok := body["system"].([]any)
	if !ok {
		t.Fatalf("system = %#v, want block array", body["system"])
	}
	out := make([]map[string]any, 0, len(raw))
	for _, entry := range raw {
		if block, ok := entry.(map[string]any); ok {
			out = append(out, block)
		}
	}
	return out
}

// betasFromBody reads the betas param from the captured request body (the
// live map carries []string).
func betasFromBody(body map[string]any) []string {
	switch value := body["betas"].(type) {
	case []any:
		var out []string
		for _, entry := range value {
			if s, ok := entry.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case []string:
		return value
	}
	return nil
}
