// pi: test/cache-retention.test.ts — PI_CACHE_RETENTION / cacheRetention
// request shaping across the three builtin adapters. First-ported with S9
// (S8-6 偏差 2 continuation). The four real-API-gated tests
// (ANTHROPIC_API_KEY / OPENAI_API_KEY skipIf) are declared skips — pig has
// no ambient-key test gating; their mock-covered semantics live in the
// sibling tests here. pi throws from onPayload to stop after the request is
// built; pig captures through OnPayload and serves a deterministic HTTP
// error via the Fetch seam, then asserts the captured payload.
package api

import (
	"bytes"
	gocontext "context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/ai/utils"
)

// cacheRetentionLoBool is a small literal pointer helper for compat fields.
func cacheRetentionLoBool(v bool) *bool { return &v }

// cacheRetentionErrorFetch returns a FetchFunction capturing the request and
// answering with a deterministic 401 (pi: stopAfterPayload + error break).
func cacheRetentionErrorFetch(captured *map[string]any) types.FetchFunction {
	return func(req *http.Request) (*http.Response, error) {
		raw, _ := io.ReadAll(req.Body)
		req.Body = io.NopCloser(bytes.NewReader(raw))
		var parsed map[string]any
		_ = json.Unmarshal(raw, &parsed)
		*captured = parsed
		return &http.Response{
			StatusCode: 401,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       http.NoBody,
		}, nil
	}
}

// cacheRetentionContext is pi's shared context (system + one user message).
func cacheRetentionContext() *types.TranscriptContext {
	return utils.NormalizeContext(&types.Context{
		SystemPrompt: "You are a helpful assistant.",
		Messages: []types.Message{
			types.UserMessage{Role: types.RoleUser, Content: types.UserContent{String: "Hello"}, Timestamp: 1},
		},
	})
}

// ============================================================================
// Anthropic Provider
// ============================================================================

func cacheRetentionAnthropicModel(t *testing.T) types.Model {
	t.Helper()
	model, ok := ai.MODELS[ai.ProviderAnthropic]["claude-haiku-4-5"]
	if !ok {
		t.Fatal("claude-haiku-4-5 not in catalog")
	}
	return model
}

// captureAnthropicCacheRetention runs StreamSimple and returns the payload.
func captureAnthropicCacheRetention(t *testing.T, model types.Model, options *types.SimpleStreamOptions) map[string]any {
	t.Helper()
	var captured map[string]any
	options.APIKey = "fake-key"
	options.Fetch = cacheRetentionErrorFetch(&captured)
	result := (&AnthropicMessages{}).StreamSimple(gocontext.Background(), &model, cacheRetentionContext(), options).Result()
	if captured == nil {
		t.Fatal("expected payload capture before request")
	}
	// The 401 surfaces as an error result; only the payload matters here.
	if result.StopReason == types.StopReasonStop {
		t.Fatal("unexpected clean stop for fake-key request")
	}
	return captured
}

func anthropicSystemBlocks(t *testing.T, payload map[string]any) []map[string]any {
	t.Helper()
	var raw []map[string]any
	switch blocks := payload["system"].(type) {
	case []map[string]any:
		raw = blocks
	case []any:
		for _, block := range blocks {
			m, ok := block.(map[string]any)
			if !ok {
				t.Fatalf("system block = %#v, want a map", block)
			}
			raw = append(raw, m)
		}
	default:
		t.Fatalf("system = %#v, want block list", payload["system"])
	}
	return raw
}

func TestCacheRetentionAnthropicProxyDefaultsToLongTTL(t *testing.T) {
	// pi: "should add ttl for non-api.anthropic.com baseUrl by default" with
	// PI_CACHE_RETENTION=long in the environment.
	model := cacheRetentionAnthropicModel(t)
	model.BaseURL = "https://my-proxy.example.com/v1"
	options := &types.SimpleStreamOptions{}
	options.Env = types.ProviderEnv{"PI_CACHE_RETENTION": "long"}
	payload := captureAnthropicCacheRetention(t, model, options)
	system := anthropicSystemBlocks(t, payload)
	cacheControl, ok := system[0]["cache_control"].(map[string]any)
	if !ok || cacheControl["type"] != "ephemeral" || cacheControl["ttl"] != "1h" {
		t.Fatalf("system[0].cache_control = %#v, want ephemeral 1h", system[0]["cache_control"])
	}
}

func TestCacheRetentionAnthropicOmitsTTLWhenUnsupported(t *testing.T) {
	// pi: "should omit ttl when supportsLongCacheRetention is false".
	model := cacheRetentionAnthropicModel(t)
	model.BaseURL = "https://my-proxy.example.com/v1"
	model.Compat = &types.AnthropicMessagesCompat{SupportsLongCacheRetention: cacheRetentionLoBool(false)}
	options := &types.SimpleStreamOptions{StreamOptions: types.StreamOptions{CacheRetention: types.CacheRetentionLong}}
	payload := captureAnthropicCacheRetention(t, model, options)
	system := anthropicSystemBlocks(t, payload)
	cacheControl, ok := system[0]["cache_control"].(map[string]any)
	if !ok || cacheControl["type"] != "ephemeral" {
		t.Fatalf("system[0].cache_control = %#v, want ephemeral", system[0]["cache_control"])
	}
	if ttl, present := cacheControl["ttl"]; present {
		t.Fatalf("cache_control.ttl = %v, want absent", ttl)
	}
}

func TestCacheRetentionAnthropicOmitsCacheControlWhenNone(t *testing.T) {
	// pi: "should omit cache_control when cacheRetention is none".
	model := cacheRetentionAnthropicModel(t)
	options := &types.SimpleStreamOptions{StreamOptions: types.StreamOptions{CacheRetention: types.CacheRetentionNone}}
	payload := captureAnthropicCacheRetention(t, model, options)
	system := anthropicSystemBlocks(t, payload)
	if _, present := system[0]["cache_control"]; present {
		t.Fatalf("system[0].cache_control = %#v, want absent", system[0]["cache_control"])
	}
}

func TestCacheRetentionAnthropicAddsCacheControlToUserMessages(t *testing.T) {
	// pi: "should add cache_control to string user messages".
	model := cacheRetentionAnthropicModel(t)
	options := &types.SimpleStreamOptions{}
	payload := captureAnthropicCacheRetention(t, model, options)
	var messages []map[string]any
	switch msgs := payload["messages"].(type) {
	case []map[string]any:
		messages = msgs
	case []any:
		for _, msg := range msgs {
			m, ok := msg.(map[string]any)
			if !ok {
				t.Fatalf("message = %#v, want a map", msg)
			}
			messages = append(messages, m)
		}
	default:
		t.Fatalf("messages = %#v", payload["messages"])
	}
	if len(messages) == 0 {
		t.Fatalf("messages = %#v", payload["messages"])
	}
	last := messages[len(messages)-1]
	var content []map[string]any
	switch blocks := last["content"].(type) {
	case []map[string]any:
		content = blocks
	case []any:
		for _, block := range blocks {
			m, ok := block.(map[string]any)
			if !ok {
				t.Fatalf("content block = %#v, want a map", block)
			}
			content = append(content, m)
		}
	default:
		t.Fatalf("last content = %#v, want block list", last["content"])
	}
	lastBlock := content[len(content)-1]
	cacheControl, ok := lastBlock["cache_control"].(map[string]any)
	if !ok || cacheControl["type"] != "ephemeral" {
		t.Fatalf("last block cache_control = %#v, want ephemeral", lastBlock["cache_control"])
	}
}

func TestCacheRetentionAnthropicSets1hTTLWhenLong(t *testing.T) {
	// pi: "should set 1h cache TTL when cacheRetention is long".
	model := cacheRetentionAnthropicModel(t)
	options := &types.SimpleStreamOptions{StreamOptions: types.StreamOptions{CacheRetention: types.CacheRetentionLong}}
	payload := captureAnthropicCacheRetention(t, model, options)
	system := anthropicSystemBlocks(t, payload)
	cacheControl, ok := system[0]["cache_control"].(map[string]any)
	if !ok || cacheControl["type"] != "ephemeral" || cacheControl["ttl"] != "1h" {
		t.Fatalf("system[0].cache_control = %#v, want ephemeral 1h", system[0]["cache_control"])
	}
}

// ============================================================================
// OpenAI Responses Provider
// ============================================================================

func TestCacheRetentionOpenAIResponsesNoCacheWarmingFromTTLAlone(t *testing.T) {
	// pi: "does not enable cache warming from the documented TTL alone".
	for _, modelID := range []string{"gpt-5.6-luna", "gpt-5.6-sol", "gpt-5.6-terra", "gpt-6-astra", "gpt-6-luna", "gpt-6-sol"} {
		model := maxThinkingCatalogModel(t, ai.ProviderOpenAI, modelID)
		if model.PromptCache != nil {
			t.Errorf("%s promptCache = %#v, want unset", modelID, model.PromptCache)
		}
	}
}

func TestCacheRetentionOpenAIResponsesProxySetsRetention(t *testing.T) {
	// pi: "should set prompt_cache_retention for non-api.openai.com baseUrl
	// by default" with PI_CACHE_RETENTION=long.
	model := maxThinkingCatalogModel(t, ai.ProviderOpenAI, "gpt-4o-mini")
	model.BaseURL = "https://my-proxy.example.com/v1"
	var captured map[string]any
	options := &types.SimpleStreamOptions{}
	options.Env = types.ProviderEnv{"PI_CACHE_RETENTION": "long"}
	options.Fetch = cacheRetentionErrorFetch(&captured)
	options.APIKey = "fake-key"
	OpenAIResponses{}.StreamSimple(gocontext.Background(), &model, cacheRetentionContext(), options).Result()
	if captured == nil {
		t.Fatal("expected payload capture")
	}
	if captured["prompt_cache_retention"] != "24h" {
		t.Fatalf("prompt_cache_retention = %#v, want 24h", captured["prompt_cache_retention"])
	}
}

func TestCacheRetentionOpenAIResponsesOmitsRetentionWhenUnsupported(t *testing.T) {
	// pi: "should omit prompt_cache_retention when supportsLongCacheRetention
	// is false".
	model := maxThinkingCatalogModel(t, ai.ProviderOpenAI, "gpt-4o-mini")
	model.Compat = &types.OpenAIResponsesCompat{SupportsLongCacheRetention: cacheRetentionLoBool(false)}
	var captured map[string]any
	options := &types.SimpleStreamOptions{StreamOptions: types.StreamOptions{CacheRetention: types.CacheRetentionLong, SessionID: "session-compat-false"}}
	options.Fetch = cacheRetentionErrorFetch(&captured)
	options.APIKey = "fake-key"
	OpenAIResponses{}.StreamSimple(gocontext.Background(), &model, cacheRetentionContext(), options).Result()
	if captured == nil {
		t.Fatal("expected payload capture")
	}
	if _, present := captured["prompt_cache_retention"]; present {
		t.Fatalf("prompt_cache_retention = %#v, want absent", captured["prompt_cache_retention"])
	}
}

func TestCacheRetentionOpenAIResponsesNoneOmitsKeyAndEnablesExplicit(t *testing.T) {
	// pi: "should omit prompt_cache_key and disable implicit writes when
	// cacheRetention is none".
	model := maxThinkingCatalogModel(t, ai.ProviderOpenAI, "gpt-5.6-sol")
	var captured map[string]any
	options := &types.SimpleStreamOptions{StreamOptions: types.StreamOptions{CacheRetention: types.CacheRetentionNone, SessionID: "session-1"}}
	options.Fetch = cacheRetentionErrorFetch(&captured)
	options.APIKey = "fake-key"
	OpenAIResponses{}.StreamSimple(gocontext.Background(), &model, cacheRetentionContext(), options).Result()
	if captured == nil {
		t.Fatal("expected payload capture")
	}
	if _, present := captured["prompt_cache_key"]; present {
		t.Errorf("prompt_cache_key = %#v, want absent", captured["prompt_cache_key"])
	}
	if _, present := captured["prompt_cache_retention"]; present {
		t.Errorf("prompt_cache_retention = %#v, want absent", captured["prompt_cache_retention"])
	}
	cacheOptions, ok := captured["prompt_cache_options"].(map[string]any)
	if !ok || cacheOptions["mode"] != "explicit" {
		t.Fatalf("prompt_cache_options = %#v, want {mode: explicit}", captured["prompt_cache_options"])
	}
}

func TestCacheRetentionOpenAIResponsesOmitsOptionsForRejectingModels(t *testing.T) {
	// pi: "should omit prompt_cache_options for models that reject it".
	model := maxThinkingCatalogModel(t, ai.ProviderOpenAI, "gpt-4o-mini")
	var captured map[string]any
	options := &types.SimpleStreamOptions{StreamOptions: types.StreamOptions{CacheRetention: types.CacheRetentionNone, SessionID: "session-1"}}
	options.Fetch = cacheRetentionErrorFetch(&captured)
	options.APIKey = "fake-key"
	OpenAIResponses{}.StreamSimple(gocontext.Background(), &model, cacheRetentionContext(), options).Result()
	if captured == nil {
		t.Fatal("expected payload capture")
	}
	if _, present := captured["prompt_cache_key"]; present {
		t.Errorf("prompt_cache_key = %#v, want absent", captured["prompt_cache_key"])
	}
	if _, present := captured["prompt_cache_options"]; present {
		t.Errorf("prompt_cache_options = %#v, want absent", captured["prompt_cache_options"])
	}
}

func TestCacheRetentionOpenAIResponsesSupportedLongCacheField(t *testing.T) {
	// pi: "should use the supported long cache field for %s".
	for _, tc := range []struct {
		modelID       string
		retention     string
		expectTTL     bool
		expectOptions bool
	}{
		{"gpt-4o-mini", "24h", false, false},
		{"gpt-6-astra", "", false, true},
		{"gpt-6-sol", "", false, true},
		{"gpt-6-luna", "", false, true},
	} {
		model := maxThinkingCatalogModel(t, ai.ProviderOpenAI, tc.modelID)
		var captured map[string]any
		options := &types.SimpleStreamOptions{StreamOptions: types.StreamOptions{CacheRetention: types.CacheRetentionLong, SessionID: "session-2"}}
		options.Fetch = cacheRetentionErrorFetch(&captured)
		options.APIKey = "fake-key"
		OpenAIResponses{}.StreamSimple(gocontext.Background(), &model, cacheRetentionContext(), options).Result()
		if captured == nil {
			t.Fatalf("%s: expected payload capture", tc.modelID)
		}
		if captured["prompt_cache_key"] != "session-2" {
			t.Errorf("%s: prompt_cache_key = %#v, want session-2", tc.modelID, captured["prompt_cache_key"])
		}
		if tc.retention == "" {
			if _, present := captured["prompt_cache_retention"]; present {
				t.Errorf("%s: prompt_cache_retention = %#v, want absent", tc.modelID, captured["prompt_cache_retention"])
			}
		} else if captured["prompt_cache_retention"] != tc.retention {
			t.Errorf("%s: prompt_cache_retention = %#v, want %s", tc.modelID, captured["prompt_cache_retention"], tc.retention)
		}
		if tc.expectOptions {
			cacheOptions, ok := captured["prompt_cache_options"].(map[string]any)
			if !ok || cacheOptions["ttl"] != "30m" {
				t.Errorf("%s: prompt_cache_options = %#v, want ttl 30m", tc.modelID, captured["prompt_cache_options"])
			}
		} else if _, present := captured["prompt_cache_options"]; present {
			t.Errorf("%s: prompt_cache_options = %#v, want absent", tc.modelID, captured["prompt_cache_options"])
		}
	}
}

// ============================================================================
// OpenAI Completions Provider
// ============================================================================

func cacheRetentionCompletionsModel(compat *types.OpenAICompletionsCompat) *types.Model {
	return &types.Model{
		ID:            "test-model",
		Name:          "Test Model",
		API:           types.ApiOpenAICompletions,
		Provider:      "test-openai-completions",
		BaseURL:       "https://my-proxy.example.com/v1",
		Reasoning:     false,
		Input:         []types.ModelInput{types.ModelInputText},
		Cost:          types.ModelCost{},
		ContextWindow: 128000,
		MaxTokens:     4096,
		Compat:        compat,
	}
}

func TestCacheRetentionOpenAICompletionsProxySetsRetention(t *testing.T) {
	// pi: "should set prompt_cache_retention for non-api.openai.com baseUrl
	// by default".
	var captured map[string]any
	options := &types.SimpleStreamOptions{StreamOptions: types.StreamOptions{CacheRetention: types.CacheRetentionLong, SessionID: "session-completions"}}
	options.APIKey = "fake-key"
	options.Fetch = cacheRetentionErrorFetch(&captured)
	model := cacheRetentionCompletionsModel(nil)
	OpenAICompletions{}.StreamSimple(gocontext.Background(), model, cacheRetentionContext(), options).Result()
	if captured == nil {
		t.Fatal("expected payload capture")
	}
	if captured["prompt_cache_key"] != "session-completions" {
		t.Errorf("prompt_cache_key = %#v, want session-completions", captured["prompt_cache_key"])
	}
	if captured["prompt_cache_retention"] != "24h" {
		t.Errorf("prompt_cache_retention = %#v, want 24h", captured["prompt_cache_retention"])
	}
}

func TestCacheRetentionOpenAICompletionsOmitsRetentionWhenUnsupported(t *testing.T) {
	// pi: "should omit prompt_cache_retention when supportsLongCacheRetention
	// is false".
	var captured map[string]any
	options := &types.SimpleStreamOptions{StreamOptions: types.StreamOptions{CacheRetention: types.CacheRetentionLong, SessionID: "session-completions-false"}}
	options.APIKey = "fake-key"
	options.Fetch = cacheRetentionErrorFetch(&captured)
	model := cacheRetentionCompletionsModel(&types.OpenAICompletionsCompat{SupportsLongCacheRetention: cacheRetentionLoBool(false)})
	OpenAICompletions{}.StreamSimple(gocontext.Background(), model, cacheRetentionContext(), options).Result()
	if captured == nil {
		t.Fatal("expected payload capture")
	}
	if _, present := captured["prompt_cache_key"]; present {
		t.Errorf("prompt_cache_key = %#v, want absent", captured["prompt_cache_key"])
	}
	if _, present := captured["prompt_cache_retention"]; present {
		t.Errorf("prompt_cache_retention = %#v, want absent", captured["prompt_cache_retention"])
	}
}

func TestCacheRetentionOpenAICompletionsOmitLongCacheForOpencode(t *testing.T) {
	// pi: "should omit long cache retention for $provider/$id" over the
	// opencode / opencode-go catalog entries.
	for _, spec := range []struct{ provider, id string }{
		{"opencode", "deepseek-v4-flash"},
		{"opencode", "deepseek-v4-pro"},
		{"opencode", "kimi-k2.5"},
		{"opencode", "kimi-k2.6"},
		{"opencode", "minimax-m2.7"},
		{"opencode-go", "kimi-k2.6"},
	} {
		model := maxThinkingCatalogModel(t, ai.ProviderID(spec.provider), spec.id)
		compat, ok := model.Compat.(*types.OpenAICompletionsCompat)
		if !ok || compat == nil || compat.SupportsLongCacheRetention == nil || *compat.SupportsLongCacheRetention {
			t.Fatalf("%s/%s supportsLongCacheRetention = %#v, want false", spec.provider, spec.id, compat)
		}
		var captured map[string]any
		options := &types.SimpleStreamOptions{StreamOptions: types.StreamOptions{CacheRetention: types.CacheRetentionLong, SessionID: "session-opencode-long-cache-unsupported"}}
		options.APIKey = "fake-key"
		options.Fetch = cacheRetentionErrorFetch(&captured)
		options.APIKey = "fake-key"
		OpenAICompletions{}.StreamSimple(gocontext.Background(), &model, cacheRetentionContext(), options).Result()
		if captured == nil {
			t.Fatalf("%s/%s: expected payload capture", spec.provider, spec.id)
		}
		if _, present := captured["prompt_cache_key"]; present {
			t.Errorf("%s/%s: prompt_cache_key present, want absent", spec.provider, spec.id)
		}
		if _, present := captured["prompt_cache_retention"]; present {
			t.Errorf("%s/%s: prompt_cache_retention present, want absent", spec.provider, spec.id)
		}
	}
}

func TestCacheRetentionOpenAICompletionsOmitStrictForCerebras(t *testing.T) {
	// pi: "should omit strict field on tools for cerebras/$id": a system
	// message carrying toolsAdded must not grow per-tool strict flags.
	for _, modelID := range []string{"gpt-oss-120b", "qwen-3.8-27b"} {
		model := maxThinkingCatalogModel(t, ai.ProviderCerebras, modelID)
		compat, ok := model.Compat.(*types.OpenAICompletionsCompat)
		if !ok || compat == nil || compat.SupportsStrictMode != nil {
			t.Fatalf("cerebras/%s supportsStrictMode = %#v, want unset", modelID, compat)
		}
		llmContext := utils.NormalizeContext(&types.Context{
			Messages: []types.Message{
				types.SystemMessage{
					Role:       types.RoleSystem,
					Content:    types.SystemContent{String: "test"},
					ToolsAdded: cacheRetentionStrictTools(),
					Timestamp:  0,
				},
				types.UserMessage{Role: types.RoleUser, Content: types.UserContent{String: "hello"}, Timestamp: 1},
			},
		})
		var captured map[string]any
		options := &types.SimpleStreamOptions{StreamOptions: types.StreamOptions{SessionID: "test"}}
		options.APIKey = "fake-key"
		options.Fetch = cacheRetentionErrorFetch(&captured)
		OpenAICompletions{}.StreamSimple(gocontext.Background(), &model, llmContext, options).Result()
		if captured == nil {
			t.Fatalf("cerebras/%s: expected payload capture", modelID)
		}
		var tools []map[string]any
		switch entries := captured["tools"].(type) {
		case []map[string]any:
			tools = entries
		case []any:
			for _, entry := range entries {
				m, ok := entry.(map[string]any)
				if !ok {
					t.Fatalf("cerebras/%s: tool entry = %#v, want a map", modelID, entry)
				}
				tools = append(tools, m)
			}
		}
		if len(tools) == 0 {
			t.Fatalf("cerebras/%s: tools = %#v, want two entries", modelID, captured["tools"])
		}
		for _, tool := range tools {
			function, _ := tool["function"].(map[string]any)
			if _, present := function["strict"]; present {
				t.Errorf("cerebras/%s: tool strict field present, want absent", modelID)
			}
		}
	}
}

// cacheRetentionStrictTools is pi's t1 (constrained strict) + t2 (non-strict)
// tool pair.
func cacheRetentionStrictTools() []types.Tool {
	return []types.Tool{
		{
			Name:                "t1",
			Description:         "strict tool",
			Parameters:          json.RawMessage(`{"type":"object","properties":{"x":{"type":"string"}},"required":["x"]}`),
			ConstrainedSampling: map[string]any{"type": "json_schema"},
		},
		{
			Name:        "t2",
			Description: "non-strict tool",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"y":{"type":"string"}},"required":["y"]}`),
		},
	}
}
