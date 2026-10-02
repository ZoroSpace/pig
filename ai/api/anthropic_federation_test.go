// pi: test/anthropic-federation-sdk.test.ts (+ the stream-level cases of
// test/anthropic-federation.test.ts). pi runs the real Anthropic SDK against
// a fake fetch; pig's adapter performs the exchange natively against the same
// wire contract.
package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/ai/utils"
)

// recordedFederationRequest is one request seen by the fake fetch (pi:
// RecordedRequest).
type recordedFederationRequest struct {
	path          string
	authorization string
	method        string
	betaHeader    string
	body          map[string]any
}

// federationTestFetch records requests, answers /v1/oauth/token with an
// exchange response and everything else with a minimal SSE stream (pi:
// createFetch + sseResponse). captured optionally mirrors the /v1/messages
// request like fakeAnthropicFetch.
func federationTestFetch(requests *[]recordedFederationRequest, captured *capturedAnthropicRequest) types.FetchFunction {
	return func(req *http.Request) (*http.Response, error) {
		recorded := recordedFederationRequest{
			path:          req.URL.Path,
			authorization: req.Header.Get("Authorization"),
			method:        req.Method,
			betaHeader:    req.Header.Get("anthropic-beta"),
		}
		bodyBytes, _ := io.ReadAll(req.Body)
		var parsed map[string]any
		_ = json.Unmarshal(bodyBytes, &parsed)
		recorded.body = parsed
		*requests = append(*requests, recorded)
		if captured != nil && req.URL.Path == "/v1/messages" {
			captured.req = req
			captured.body = parsed
		}

		if req.URL.Path == "/v1/oauth/token" {
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
}

// federationTestModel mirrors pi's anthropicModel fixture.
func federationTestModel() *types.Model {
	return &types.Model{
		ID: "claude-test", Name: "Claude Test", API: types.ApiAnthropicMessages,
		Provider:      types.ProviderAnthropic,
		BaseURL:       "https://api.anthropic.com",
		Input:         []types.ModelInput{types.ModelInputText},
		ContextWindow: 100000, MaxTokens: 4096,
	}
}

func federationTestContext() *types.TranscriptContext {
	return utils.NormalizeContext(&types.Context{
		SystemPrompt: "System prompt.",
		Messages:     []types.Message{types.UserMessage{Role: types.RoleUser, Content: types.UserContent{String: "Hello"}, Timestamp: 1}},
	})
}

// writeFederationIdentityToken writes the identity JWT and returns the env
// carrying the three required federation variables (pi: beforeAll +
// federationEnv).
func writeFederationIdentityToken(t *testing.T) types.ProviderEnv {
	t.Helper()
	identityTokenFile := filepath.Join(t.TempDir(), "identity.jwt")
	if err := os.WriteFile(identityTokenFile, []byte("header.payload.signature"), 0o600); err != nil {
		t.Fatal(err)
	}
	return types.ProviderEnv{
		"ANTHROPIC_FEDERATION_RULE_ID":  "fdrl_test",
		"ANTHROPIC_ORGANIZATION_ID":     "org-test",
		"ANTHROPIC_IDENTITY_TOKEN_FILE": identityTokenFile,
	}
}

// resetFederationCache isolates the process-level token cache per test (pi:
// each test gets a fresh SDK client; pig's cache is a package var).
func resetFederationCache(t *testing.T) {
	t.Helper()
	theAnthropicFederationTokenCache = anthropicFederationTokenCache{}
	t.Cleanup(func() { theAnthropicFederationTokenCache = anthropicFederationTokenCache{} })
}

func runFederationStream(t *testing.T, model *types.Model, options *types.StreamOptions) types.AssistantMessage {
	t.Helper()
	stream := streamAnthropicMessages(context.Background(), model, federationTestContext(), &AnthropicOptions{StreamOptions: *options})
	result := stream.Result()
	if result.StopReason != types.StopReasonStop {
		t.Fatalf("stopReason = %q (%s)", result.StopReason, result.ErrorMessage)
	}
	return result
}

// https://github.com/earendil-works/pi/issues/10177
func TestAnthropicFederationExchangesIdentityTokenOnceAcrossRequests(t *testing.T) {
	resetFederationCache(t)
	env := writeFederationIdentityToken(t)
	var requests []recordedFederationRequest
	options := &types.StreamOptions{}
	options.Env = env
	options.Fetch = federationTestFetch(&requests, nil)

	model := federationTestModel()
	for i := 0; i < 3; i++ {
		runFederationStream(t, model, options)
	}

	exchanges := 0
	var messageRequests []recordedFederationRequest
	for _, request := range requests {
		if request.path == "/v1/oauth/token" {
			exchanges++
		} else if request.path == "/v1/messages" {
			messageRequests = append(messageRequests, request)
		}
	}
	if exchanges != 1 {
		t.Errorf("token exchanges = %d, want 1", exchanges)
	}
	if len(messageRequests) != 3 {
		t.Fatalf("message requests = %d, want 3", len(messageRequests))
	}
	for _, request := range messageRequests {
		if request.authorization != "Bearer federated-token" {
			t.Errorf("Authorization = %q, want Bearer federated-token", request.authorization)
		}
	}
}

func TestAnthropicFederationDoesNotRunForHeaderOwnedAuth(t *testing.T) {
	resetFederationCache(t)
	t.Setenv("ANTHROPIC_FEDERATION_RULE_ID", "fdrl_test")
	t.Setenv("ANTHROPIC_ORGANIZATION_ID", "org-test")
	identityTokenFile := filepath.Join(t.TempDir(), "identity.jwt")
	if err := os.WriteFile(identityTokenFile, []byte("header.payload.signature"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ANTHROPIC_IDENTITY_TOKEN_FILE", identityTokenFile)

	var requests []recordedFederationRequest
	options := &types.StreamOptions{}
	options.Fetch = federationTestFetch(&requests, nil)
	bearer := "Bearer auth-token"
	options.Headers = types.ProviderHeaders{"Authorization": &bearer}

	result := runFederationStream(t, federationTestModel(), options)
	_ = result

	if len(requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(requests))
	}
	if requests[0].path != "/v1/messages" {
		t.Errorf("path = %q, want /v1/messages", requests[0].path)
	}
	if requests[0].authorization != "Bearer auth-token" {
		t.Errorf("Authorization = %q, want Bearer auth-token", requests[0].authorization)
	}
}

func TestAnthropicFederationExplicitAPIKeyWins(t *testing.T) {
	resetFederationCache(t)
	env := writeFederationIdentityToken(t)
	var requests []recordedFederationRequest
	options := &types.StreamOptions{}
	options.Env = env
	options.APIKey = "explicit-key"
	options.Fetch = federationTestFetch(&requests, nil)

	runFederationStream(t, federationTestModel(), options)

	if len(requests) != 1 || requests[0].path != "/v1/messages" {
		t.Fatalf("requests = %#v, want a single /v1/messages", requests)
	}
	// The API-key branch sends x-api-key; the fake records only the
	// Authorization header, so assert it is absent.
	if requests[0].authorization != "" {
		t.Errorf("Authorization = %q, want absent", requests[0].authorization)
	}
}

func TestAnthropicFederationDoesNotFederateOtherAnthropicMessagesProviders(t *testing.T) {
	resetFederationCache(t)
	env := writeFederationIdentityToken(t)
	var requests []recordedFederationRequest
	options := &types.StreamOptions{}
	options.Env = env
	options.Fetch = federationTestFetch(&requests, nil)

	model := federationTestModel()
	model.Provider = types.ProviderID("kimi-coding")
	model.BaseURL = "https://api.kimi.com/coding"
	stream := streamAnthropicMessages(context.Background(), model, federationTestContext(), &AnthropicOptions{StreamOptions: *options})
	result := stream.Result()
	if result.StopReason != types.StopReasonError {
		t.Fatalf("stopReason = %q, want error", result.StopReason)
	}
	if len(requests) != 0 {
		t.Fatalf("requests = %#v, want none", requests)
	}
}

// S11-1 pig-side wire pinning: the exchange request carries the jwt-bearer
// grant against /v1/oauth/token with the federation beta header, and the
// messages request gets oauth-2025-04-20 appended to anthropic-beta while the
// betas param itself stays clean.
func TestAnthropicFederationWireShape(t *testing.T) {
	resetFederationCache(t)
	env := writeFederationIdentityToken(t)
	var requests []recordedFederationRequest
	captured := &capturedAnthropicRequest{}
	options := &types.StreamOptions{}
	options.Env = env
	options.Fetch = federationTestFetch(&requests, captured)

	runFederationStream(t, federationTestModel(), options)

	var exchange, message *recordedFederationRequest
	for i := range requests {
		if requests[i].path == "/v1/oauth/token" && exchange == nil {
			exchange = &requests[i]
		}
		if requests[i].path == "/v1/messages" && message == nil {
			message = &requests[i]
		}
	}
	if exchange == nil || message == nil {
		t.Fatalf("requests = %#v, want an exchange and a message request", requests)
	}

	if exchange.method != "POST" {
		t.Errorf("exchange method = %q, want POST", exchange.method)
	}
	if exchange.body["grant_type"] != "urn:ietf:params:oauth:grant-type:jwt-bearer" {
		t.Errorf("grant_type = %v", exchange.body["grant_type"])
	}
	if exchange.body["assertion"] != "header.payload.signature" {
		t.Errorf("assertion = %v, want the file contents", exchange.body["assertion"])
	}
	if exchange.body["federation_rule_id"] != "fdrl_test" || exchange.body["organization_id"] != "org-test" {
		t.Errorf("exchange body = %v", exchange.body)
	}
	if !anthropicBetaListContains(exchange.betaHeader, anthropicFederationBetaHeader) {
		t.Errorf("exchange anthropic-beta = %q, want it to contain %s", exchange.betaHeader, anthropicFederationBetaHeader)
	}

	if got := captured.req.Header.Get("Authorization"); got != "Bearer federated-token" {
		t.Errorf("messages Authorization = %q, want Bearer federated-token", got)
	}
	if captured.req.Header.Get("x-api-key") != "" {
		t.Errorf("messages x-api-key = %q, want absent", captured.req.Header.Get("x-api-key"))
	}
	betaHeader := captured.req.Header.Get("anthropic-beta")
	if !anthropicBetaListContains(betaHeader, anthropicOAuthAPIBetaHeader) {
		t.Errorf("messages anthropic-beta = %q, want it to contain %s", betaHeader, anthropicOAuthAPIBetaHeader)
	}
	if betas := betasFromBody(captured.body); containsString(betas, anthropicOAuthAPIBetaHeader) {
		t.Errorf("betas param = %v, want it to omit %s", betas, anthropicOAuthAPIBetaHeader)
	}
}

func TestAnthropicFederationRejectsInsecureTokenEndpoint(t *testing.T) {
	err := requireAnthropicSecureTokenEndpoint("http://api.example.com")
	if err == nil || !strings.Contains(err.Error(), "non-https") {
		t.Fatalf("err = %v, want a non-https refusal", err)
	}
	if err := requireAnthropicSecureTokenEndpoint("http://localhost:8080"); err != nil {
		t.Errorf("loopback http: err = %v, want nil", err)
	}
	if err := requireAnthropicSecureTokenEndpoint("https://api.anthropic.com"); err != nil {
		t.Errorf("https: err = %v, want nil", err)
	}
}
