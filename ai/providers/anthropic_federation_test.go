// pi: test/anthropic-federation.test.ts — the provider resolve half (the
// stream-level half lives in ai/api/anthropic_federation_test.go).
package providers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/auth"
	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/ai/utils"
)

// resolveAnthropicWithEnv resolves Anthropic api-key auth against a fixed env
// (pi: resolveWithEnv; fileExists is false like pi's ctx stub).
func resolveAnthropicWithEnv(t *testing.T, env map[string]string) (*auth.AuthResult, error) {
	t.Helper()
	return AnthropicProvider().Auth().APIKey.Resolve(auth.ApiKeyAuthInput{
		Ctx:     mapAuthContext(env),
		Context: context.Background(),
	})
}

func federationResolveEnv() map[string]string {
	return map[string]string{
		"ANTHROPIC_FEDERATION_RULE_ID":  "fdrl_test",
		"ANTHROPIC_ORGANIZATION_ID":     "org-test",
		"ANTHROPIC_SERVICE_ACCOUNT_ID":  "svac_test",
		"ANTHROPIC_IDENTITY_TOKEN_FILE": "/tmp/identity.jwt",
	}
}

// https://github.com/earendil-works/pi/issues/10177
func TestAnthropicFederationResolvesProviderEnvWithNoRequestAuth(t *testing.T) {
	result, err := resolveAnthropicWithEnv(t, federationResolveEnv())
	if err != nil {
		t.Fatal(err)
	}
	if result == nil {
		t.Fatal("expected an auth result")
	}
	if result.Source != "workload identity federation" {
		t.Errorf("source = %q, want workload identity federation", result.Source)
	}
	if !reflect.DeepEqual(result.Env, types.ProviderEnv(federationResolveEnv())) {
		t.Errorf("env = %v, want %v", result.Env, federationResolveEnv())
	}
	if result.Auth.APIKey != "" || result.Auth.Headers != nil {
		t.Errorf("auth = %+v, want empty", result.Auth)
	}
}

func TestAnthropicFederationPassesWorkspaceIDThrough(t *testing.T) {
	env := federationResolveEnv()
	env["ANTHROPIC_WORKSPACE_ID"] = "wrkspc_test"
	result, err := resolveAnthropicWithEnv(t, env)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || result.Env["ANTHROPIC_WORKSPACE_ID"] != "wrkspc_test" {
		t.Fatalf("env = %+v, want ANTHROPIC_WORKSPACE_ID passthrough", result)
	}
}

func TestAnthropicFederationNotConfiguredWhenVariableMissing(t *testing.T) {
	env := federationResolveEnv()
	delete(env, "ANTHROPIC_IDENTITY_TOKEN_FILE")
	result, err := resolveAnthropicWithEnv(t, env)
	if err != nil {
		t.Fatal(err)
	}
	if result != nil {
		t.Fatalf("result = %+v, want nil", result)
	}
}

func TestAnthropicFederationTreatsServiceAccountIDAsOptional(t *testing.T) {
	env := federationResolveEnv()
	delete(env, "ANTHROPIC_SERVICE_ACCOUNT_ID")
	result, err := resolveAnthropicWithEnv(t, env)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || !reflect.DeepEqual(result.Env, types.ProviderEnv(env)) {
		t.Fatalf("env = %+v, want %v", result, env)
	}
	if result.Source != "workload identity federation" {
		t.Errorf("source = %q", result.Source)
	}
}

func TestAnthropicFederationKeepsKeyAndAuthTokenPrecedence(t *testing.T) {
	withAPIKey := federationResolveEnv()
	withAPIKey["ANTHROPIC_API_KEY"] = "api-key"
	result, err := resolveAnthropicWithEnv(t, withAPIKey)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || result.Auth.APIKey != "api-key" || result.Source != "ANTHROPIC_API_KEY" {
		t.Fatalf("result = %+v, want api-key from ANTHROPIC_API_KEY", result)
	}

	withAuthToken := federationResolveEnv()
	withAuthToken["ANTHROPIC_AUTH_TOKEN"] = "auth-token"
	result, err = resolveAnthropicWithEnv(t, withAuthToken)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || result.Auth.Headers == nil || result.Source != "ANTHROPIC_AUTH_TOKEN" {
		t.Fatalf("result = %+v, want Bearer header from ANTHROPIC_AUTH_TOKEN", result)
	}
	if got := *result.Auth.Headers["Authorization"]; got != "Bearer auth-token" {
		t.Errorf("Authorization = %q, want Bearer auth-token", got)
	}
}

// pi: "threads authContext federation variables through Models" — the Models
// resolve chain must carry the federation env into the adapter, which
// exchanges the identity token and bearer-authenticates the request.
func TestAnthropicFederationThreadsAuthContextThroughModels(t *testing.T) {
	identityTokenFile := filepath.Join(t.TempDir(), "identity.jwt")
	if err := os.WriteFile(identityTokenFile, []byte("header.payload.signature"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{
		"ANTHROPIC_FEDERATION_RULE_ID":  "fdrl_test",
		"ANTHROPIC_ORGANIZATION_ID":     "org-test",
		"ANTHROPIC_IDENTITY_TOKEN_FILE": identityTokenFile,
	}

	var exchangeBodies []map[string]any
	fetch := func(req *http.Request) (*http.Response, error) {
		bodyBytes, _ := io.ReadAll(req.Body)
		var parsed map[string]any
		_ = json.Unmarshal(bodyBytes, &parsed)
		if strings.HasSuffix(req.URL.Path, "/v1/oauth/token") {
			exchangeBodies = append(exchangeBodies, parsed)
			return &http.Response{
				StatusCode: 200,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"access_token":"federated-token","expires_in":3600}`)),
				Request:    req,
			}, nil
		}
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

	model := &types.Model{
		ID: "claude-test", Name: "Claude Test", API: types.ApiAnthropicMessages,
		Provider: types.ProviderAnthropic, BaseURL: "https://api.anthropic.com",
		Input: []types.ModelInput{types.ModelInputText}, ContextWindow: 100000, MaxTokens: 4096,
	}
	llmContext := utils.NormalizeContext(&types.Context{
		SystemPrompt: "System prompt.",
		Messages:     []types.Message{types.UserMessage{Role: types.RoleUser, Content: types.UserContent{String: "Hello"}, Timestamp: 1}},
	})
	models := mapAuthContextEnvModel(t, env)
	options := &ai.ModelsSimpleStreamOptions{}
	options.Fetch = fetch
	result := models.StreamSimple(context.Background(), model, &ai.Context{Messages: llmContext.Messages}, options).Result()
	if result.StopReason != types.StopReasonStop {
		t.Fatalf("stopReason = %q (%s)", result.StopReason, result.ErrorMessage)
	}
	if len(exchangeBodies) != 1 {
		t.Fatalf("exchanges = %d, want 1", len(exchangeBodies))
	}
	if exchangeBodies[0]["federation_rule_id"] != "fdrl_test" || exchangeBodies[0]["organization_id"] != "org-test" {
		t.Errorf("exchange body = %v", exchangeBodies[0])
	}
}
