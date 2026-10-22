// pi: test/xai-responses.test.ts.
//
// Lives in package providers (pi's test imports the provider factory and the
// api module; pig's ai/api cannot import ai/providers).
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
	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/ai/utils"
)

// xaiModels returns the built-in xai catalog (pi: XAI_MODELS).
func xaiModels(t *testing.T) map[string]ai.Model {
	t.Helper()
	models, ok := ai.MODELS[ai.ProviderXAI]
	if !ok {
		t.Fatal("xai not in catalog")
	}
	return models
}

// responsesCompletedBody is the terminal SSE body used by the capture helper
// (pi: completedResponse()).
const responsesCompletedBody = `data: {"type":"response.completed","sequence_number":0,"response":{"id":"resp_xai_test","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2,"input_tokens_details":{"cached_tokens":0}}}}` + "\n\ndata: [DONE]\n\n"

// capturedXaiRequest records url/headers/body (pi: CapturedRequest).
type capturedXaiRequest struct {
	url     string
	headers http.Header
	body    map[string]any
}

// captureXaiRequest streams one provider-dispatched request (pi:
// captureRequest via xaiProvider().stream).
func captureXaiRequest(t *testing.T, model *types.Model, llmContext *types.TranscriptContext, options *types.StreamOptions) capturedXaiRequest {
	t.Helper()
	var captured capturedXaiRequest
	options.Fetch = func(req *http.Request) (*http.Response, error) {
		captured.url = req.URL.String()
		captured.headers = req.Header.Clone()
		body, _ := io.ReadAll(req.Body)
		var parsed map[string]any
		_ = json.Unmarshal(body, &parsed)
		captured.body = parsed
		return &http.Response{
			StatusCode: 200,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader(responsesCompletedBody)),
		}, nil
	}
	result := XaiProvider().Stream(context.Background(), model, llmContext, options).Result()
	if result.StopReason != types.StopReasonStop {
		t.Fatalf("stopReason = %v (%s), want stop", result.StopReason, result.ErrorMessage)
	}
	return captured
}

func TestXaiExcludesRetiredAndRedundantModels(t *testing.T) {
	models := xaiModels(t)
	for _, modelID := range []string{
		"grok-3", "grok-3-fast", "grok-4.20-0309-non-reasoning",
		"grok-4.20-0309-reasoning", "grok-build-0.1", "grok-code-fast-1",
	} {
		if _, present := models[modelID]; present {
			t.Fatalf("catalog includes excluded model %q", modelID)
		}
	}
}

func TestXaiRoutesEveryBuiltInModelThroughResponses(t *testing.T) {
	models := xaiModels(t)
	for id, model := range models {
		if model.API != types.ApiOpenAIResponses {
			t.Fatalf("model %s api = %s, want openai-responses", id, model.API)
		}
	}
	// pi: getSupportedThinkingLevels over the catalog entries.
	grok45 := models["grok-4.5"]
	levels := ai.GetSupportedThinkingLevels(&grok45)
	if want := []string{"low", "medium", "high", "xhigh"}; !equalLevels(levels, want) {
		t.Fatalf("grok-4.5 levels = %v, want %v", levels, want)
	}
	grok46 := models["grok-4.6"]
	levels = ai.GetSupportedThinkingLevels(&grok46)
	if want := []string{"low", "medium", "high", "xhigh"}; !equalLevels(levels, want) {
		t.Fatalf("grok-4.6 levels = %v, want %v", levels, want)
	}
	grok47 := models["grok-4.7"]
	levels = ai.GetSupportedThinkingLevels(&grok47)
	if want := []string{"low", "medium", "high", "xhigh"}; !equalLevels(levels, want) {
		t.Fatalf("grok-4.7 levels = %v, want %v", levels, want)
	}
	grok43 := models["grok-4.3"]
	levels = ai.GetSupportedThinkingLevels(&grok43)
	if want := []string{"off", "low", "medium", "high", "xhigh"}; !equalLevels(levels, want) {
		t.Fatalf("grok-4.3 levels = %v, want %v", levels, want)
	}
}

func equalLevels(levels []ai.ModelThinkingLevel, want []string) bool {
	if len(levels) != len(want) {
		return false
	}
	for i, level := range levels {
		if string(level) != want[i] {
			return false
		}
	}
	return true
}

func TestXaiGrok47CapabilitiesAndLongContextPricing(t *testing.T) {
	// pi: "includes Grok 4.7 capabilities and long-context pricing".
	models := xaiModels(t)
	grok47, ok := models["grok-4.7"]
	if !ok {
		t.Fatal("grok-4.7 not in catalog")
	}
	if grok47.API != types.ApiOpenAIResponses || !grok47.Reasoning ||
		grok47.ContextWindow != 500000 || grok47.MaxTokens != 500000 {
		t.Fatalf("grok-4.7 core fields = api:%s reasoning:%v ctx:%d max:%d",
			grok47.API, grok47.Reasoning, grok47.ContextWindow, grok47.MaxTokens)
	}
	if want := []ai.ModelInput{ai.ModelInputText, ai.ModelInputImage}; !equalModelInputs(grok47.Input, want) {
		t.Fatalf("grok-4.7 input = %v", grok47.Input)
	}
	if grok47.Cost.Input != 2 || grok47.Cost.Output != 6 || grok47.Cost.CacheRead != 0.5 || grok47.Cost.CacheWrite != 0 {
		t.Fatalf("grok-4.7 cost rates = %+v", grok47.Cost.ModelCostRates)
	}
	if len(grok47.Cost.Tiers) != 1 {
		t.Fatalf("grok-4.7 cost tiers = %d, want 1", len(grok47.Cost.Tiers))
	}
	tier := grok47.Cost.Tiers[0]
	if tier.InputTokensAbove != 200000 || tier.Input != 4 || tier.Output != 12 || tier.CacheRead != 1 || tier.CacheWrite != 0 {
		t.Fatalf("grok-4.7 long-context tier = %+v", tier)
	}
}

// equalModelInputs compares a model's input modalities against a literal set.
func equalModelInputs(inputs []ai.ModelInput, want []ai.ModelInput) bool {
	if len(inputs) != len(want) {
		return false
	}
	for i, input := range inputs {
		if input != want[i] {
			return false
		}
	}
	return true
}

func TestXaiResponsesRequestShape(t *testing.T) {
	models := xaiModels(t)
	grok45 := models["grok-4.5"]
	grok43 := models["grok-4.3"]

	t.Run("uses /responses with bearer auth and xAI-compatible request fields", func(t *testing.T) {
		// pi passes reasoningEffort via the typed options; through the
		// provider dispatch pig uses StreamSimple's reasoning level.
		var captured capturedXaiRequest
		options := &types.SimpleStreamOptions{}
		options.APIKey = "xai-test-token"
		options.SessionID = "pi-session-123"
		options.CacheRetention = types.CacheRetentionLong
		options.Reasoning = "medium"
		options.Fetch = func(req *http.Request) (*http.Response, error) {
			captured.url = req.URL.String()
			captured.headers = req.Header.Clone()
			body, _ := io.ReadAll(req.Body)
			var parsed map[string]any
			_ = json.Unmarshal(body, &parsed)
			captured.body = parsed
			return &http.Response{
				StatusCode: 200,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       io.NopCloser(strings.NewReader(responsesCompletedBody)),
			}, nil
		}
		result := XaiProvider().StreamSimple(context.Background(), &grok45, utils.NormalizeContext(&types.Context{
			SystemPrompt: "You are a careful coding assistant.",
			Messages:     []types.Message{userMessageAt("hello", 1)},
		}), options).Result()
		if result.StopReason != types.StopReasonStop {
			t.Fatalf("stopReason = %v (%s), want stop", result.StopReason, result.ErrorMessage)
		}

		if captured.url != "https://api.x.ai/v1/responses" {
			t.Fatalf("url = %s", captured.url)
		}
		if got := headerOf(captured.headers, "authorization"); got != "Bearer xai-test-token" {
			t.Fatalf("authorization = %q", got)
		}
		if got := headerOf(captured.headers, "user-agent"); got != utils.FormatUserAgent(nil) {
			t.Fatalf("user-agent = %q", got)
		}
		if got := headerOf(captured.headers, "session_id"); got != "pi-session-123" {
			t.Fatalf("session_id = %q", got)
		}
		if captured.body["model"] != "grok-4.5" || captured.body["store"] != false || captured.body["stream"] != true {
			t.Fatalf("body base fields = %v", captured.body)
		}
		if captured.body["prompt_cache_key"] != "pi-session-123" {
			t.Fatalf("prompt_cache_key = %v", captured.body["prompt_cache_key"])
		}
		reasoning, ok := captured.body["reasoning"].(map[string]any)
		if !ok || reasoning["effort"] != "medium" {
			t.Fatalf("reasoning = %v, want effort medium", captured.body["reasoning"])
		}
		if !reflectContains(captured.body["include"], "reasoning.encrypted_content") {
			t.Fatalf("include = %v", captured.body["include"])
		}
		if _, present := captured.body["prompt_cache_retention"]; present {
			t.Fatalf("prompt_cache_retention = %v, want absent (xai supportsLongCacheRetention false)", captured.body["prompt_cache_retention"])
		}
		input, ok := captured.body["input"].([]any)
		if !ok {
			t.Fatalf("input = %v", captured.body["input"])
		}
		foundDeveloper := false
		for _, item := range input {
			m, ok := item.(map[string]any)
			if ok && m["role"] == "developer" && m["content"] == "You are a careful coding assistant." {
				foundDeveloper = true
			}
		}
		if !foundDeveloper {
			t.Fatalf("input missing developer system item: %v", input)
		}
	})

	t.Run("requests encrypted reasoning without an effort override", func(t *testing.T) {
		captured := captureXaiRequest(t, &grok45, utils.NormalizeContext(&types.Context{
			Messages: []types.Message{userMessageAt("hello", 1)},
		}), streamOptionsWithKey("xai-test-token", "", ""))

		if captured.body["model"] != "grok-4.5" || captured.body["store"] != false {
			t.Fatalf("body base fields = %v", captured.body)
		}
		if !reflectContains(captured.body["include"], "reasoning.encrypted_content") {
			t.Fatalf("include = %v", captured.body["include"])
		}
		if _, present := captured.body["reasoning"]; present {
			t.Fatalf("reasoning = %v, want absent", captured.body["reasoning"])
		}
	})

	t.Run("uses /responses for Grok 4.7 with xhigh effort and encrypted reasoning", func(t *testing.T) {
		// pi passes reasoningEffort via the typed options; through the
		// provider dispatch pig uses StreamSimple's reasoning level.
		grok47 := models["grok-4.7"]
		var captured capturedXaiRequest
		options := &types.SimpleStreamOptions{}
		options.APIKey = "xai-test-token"
		options.Reasoning = "xhigh"
		options.Fetch = func(req *http.Request) (*http.Response, error) {
			captured.url = req.URL.String()
			captured.headers = req.Header.Clone()
			body, _ := io.ReadAll(req.Body)
			var parsed map[string]any
			_ = json.Unmarshal(body, &parsed)
			captured.body = parsed
			return &http.Response{
				StatusCode: 200,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       io.NopCloser(strings.NewReader(responsesCompletedBody)),
			}, nil
		}
		result := XaiProvider().StreamSimple(context.Background(), &grok47, utils.NormalizeContext(&types.Context{
			SystemPrompt: "You are a careful coding assistant.",
			Messages:     []types.Message{userMessageAt("hello", 1)},
		}), options).Result()
		if result.StopReason != types.StopReasonStop {
			t.Fatalf("stopReason = %v (%s)", result.StopReason, result.ErrorMessage)
		}

		if captured.url != "https://api.x.ai/v1/responses" {
			t.Fatalf("url = %s", captured.url)
		}
		if captured.body["model"] != "grok-4.7" || captured.body["store"] != false || captured.body["stream"] != true {
			t.Fatalf("body base fields = %v", captured.body)
		}
		reasoning, ok := captured.body["reasoning"].(map[string]any)
		if !ok || reasoning["effort"] != "xhigh" {
			t.Fatalf("reasoning = %v, want effort xhigh", captured.body["reasoning"])
		}
		if !reflectContains(captured.body["include"], "reasoning.encrypted_content") {
			t.Fatalf("include = %v", captured.body["include"])
		}
	})

	t.Run("uses /responses for Grok 4.3", func(t *testing.T) {
		var captured capturedXaiRequest
		options := &types.SimpleStreamOptions{}
		options.APIKey = "xai-test-token"
		options.Reasoning = "low"
		options.Fetch = func(req *http.Request) (*http.Response, error) {
			captured.url = req.URL.String()
			body, _ := io.ReadAll(req.Body)
			var parsed map[string]any
			_ = json.Unmarshal(body, &parsed)
			captured.body = parsed
			return &http.Response{
				StatusCode: 200,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       io.NopCloser(strings.NewReader(responsesCompletedBody)),
			}, nil
		}
		result := XaiProvider().StreamSimple(context.Background(), &grok43, utils.NormalizeContext(&types.Context{
			Messages: []types.Message{userMessageAt("hello", 1)},
		}), options).Result()
		if result.StopReason != types.StopReasonStop {
			t.Fatalf("stopReason = %v (%s)", result.StopReason, result.ErrorMessage)
		}

		if captured.url != "https://api.x.ai/v1/responses" {
			t.Fatalf("url = %s", captured.url)
		}
		if captured.body["model"] != "grok-4.3" || captured.body["store"] != false {
			t.Fatalf("body base fields = %v", captured.body)
		}
		if !reflectContains(captured.body["include"], "reasoning.encrypted_content") {
			t.Fatalf("include = %v", captured.body["include"])
		}
		reasoning, ok := captured.body["reasoning"].(map[string]any)
		if !ok || reasoning["effort"] != "low" {
			t.Fatalf("reasoning = %v, want effort low", captured.body["reasoning"])
		}
	})
}

func TestXaiResponsesUserAgent(t *testing.T) {
	models := xaiModels(t)
	grok45 := models["grok-4.5"]

	t.Run("uses pi's User-Agent by default for Responses requests", func(t *testing.T) {
		var userAgent string
		model := grok45
		model.Provider = types.ProviderOpenAI
		model.BaseURL = "https://api.openai.com/v1"
		options := &types.StreamOptions{}
		options.APIKey = "test-token"
		options.Fetch = func(req *http.Request) (*http.Response, error) {
			userAgent = req.Header.Get("User-Agent")
			return &http.Response{
				StatusCode: 200,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       io.NopCloser(strings.NewReader(responsesCompletedBody)),
			}, nil
		}
		result := api.OpenAIResponses{}.Stream(context.Background(), &model, utils.NormalizeContext(&types.Context{
			Messages: []types.Message{userMessageAt("hello", 1)},
		}), options).Result()
		if result.StopReason != types.StopReasonStop {
			t.Fatalf("stopReason = %v (%s)", result.StopReason, result.ErrorMessage)
		}
		if userAgent != utils.FormatUserAgent(nil) {
			t.Fatalf("user-agent = %q, want %q", userAgent, utils.FormatUserAgent(nil))
		}
	})

	t.Run("lets explicit headers override the default Responses User-Agent", func(t *testing.T) {
		captured := captureXaiRequest(t, &grok45, utils.NormalizeContext(&types.Context{
			Messages: []types.Message{userMessageAt("hello", 1)},
		}), func() *types.StreamOptions {
			opts := streamOptionsWithKey("xai-test-token", "", "")
			custom := "custom-agent"
			opts.Headers = types.ProviderHeaders{"User-Agent": &custom}
			return opts
		}())
		if got := headerOf(captured.headers, "user-agent"); got != "custom-agent" {
			t.Fatalf("user-agent = %q", got)
		}
	})
}

func TestXaiCompletionsUserAgent(t *testing.T) {
	customModel := &types.Model{
		ID: "grok-custom", Name: "Grok Custom", API: types.ApiOpenAICompletions,
		Provider: types.ProviderXAI, BaseURL: "https://api.x.ai/v1",
		Reasoning: false, Input: []types.ModelInput{types.ModelInputText},
		Cost:          types.ModelCost{},
		ContextWindow: 128000, MaxTokens: 16384,
	}
	chunks := `data: {"id":"chatcmpl-ua","choices":[{"delta":{"content":"ok"},"finish_reason":null,"index":0}]}` + "\n\n" +
		`data: {"id":"chatcmpl-ua","choices":[{"delta":{},"finish_reason":"stop","index":0}],"usage":{"prompt_tokens":1,"completion_tokens":1,"prompt_tokens_details":{"cached_tokens":0},"completion_tokens_details":{"reasoning_tokens":0}}}` + "\n\ndata: [DONE]\n\n"

	capture := func(headers types.ProviderHeaders) string {
		t.Helper()
		var userAgent string
		options := &api.OpenAICompletionsOptions{}
		options.APIKey = "xai-test-token"
		options.Headers = headers
		options.Fetch = func(req *http.Request) (*http.Response, error) {
			userAgent = req.Header.Get("User-Agent")
			return &http.Response{
				StatusCode: 200,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       io.NopCloser(strings.NewReader(chunks)),
			}, nil
		}
		result := api.OpenAICompletions{}.Stream(context.Background(), customModel, utils.NormalizeContext(&types.Context{
			Messages: []types.Message{userMessageAt("hello", 1)},
		}), &options.StreamOptions).Result()
		if result.StopReason != types.StopReasonStop {
			t.Fatalf("stopReason = %v (%s)", result.StopReason, result.ErrorMessage)
		}
		return userAgent
	}

	if got := capture(nil); got != utils.FormatUserAgent(nil) {
		t.Fatalf("default user-agent = %q, want %q", got, utils.FormatUserAgent(nil))
	}
	custom := "custom-agent"
	if got := capture(types.ProviderHeaders{"User-Agent": &custom}); got != "custom-agent" {
		t.Fatalf("overridden user-agent = %q, want custom-agent", got)
	}
}

// userMessageAt builds a user message with an explicit timestamp (pi:
// { role: "user", content, timestamp }).
func userMessageAt(text string, timestamp int64) types.UserMessage {
	return types.UserMessage{Role: types.RoleUser, Content: types.UserContent{String: text}, Timestamp: timestamp}
}

// headerOf reads a header case-insensitively (pi: headers.get).
func headerOf(headers http.Header, name string) string {
	for key, values := range headers {
		if strings.EqualFold(key, name) && len(values) > 0 {
			return values[0]
		}
	}
	return ""
}

// reflectContains reports whether the JSON-decoded include array contains
// the value.
func reflectContains(value any, want string) bool {
	items, ok := value.([]any)
	if !ok {
		return false
	}
	for _, item := range items {
		if s, ok := item.(string); ok && s == want {
			return true
		}
	}
	return false
}

// streamOptionsWithKey builds StreamOptions with the promoted auth fields
// set (APIKey lives on the embedded ProviderRequestOptions).
func streamOptionsWithKey(key, sessionID string, cacheRetention types.CacheRetention) *types.StreamOptions {
	opts := &types.StreamOptions{}
	opts.APIKey = key
	opts.SessionID = sessionID
	opts.CacheRetention = cacheRetention
	return opts
}
