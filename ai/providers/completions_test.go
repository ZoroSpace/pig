package providers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/samber/lo"

	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/api"
	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/ai/utils"
)

// bareModel builds a reasoning model without catalog compat overrides, so the
// request body reflects pure URL/provider auto-detection (detectCompat).
func bareModel(provider string, baseURL string) types.Model {
	return types.Model{
		ID:        "test-model",
		Provider:  types.ProviderID(provider),
		API:       types.ApiOpenAICompletions,
		BaseURL:   baseURL,
		Reasoning: true,
		ThinkingLevelMap: types.ThinkingLevelMap{
			types.ModelThinkingHigh: lo.ToPtr("high"),
		},
		Input: []types.ModelInput{types.ModelInputText},
	}
}

// captureBody runs one StreamSimple against a stub SSE server and returns the
// captured request body.
func captureBody(t *testing.T, model types.Model, reasoning bool) map[string]any {
	t.Helper()
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &captured)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, "data: {\"id\":\"x\",\"model\":\"test-model\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(srv.Close)
	model.BaseURL = srv.URL

	opts := types.SimpleStreamOptions{}
	opts.APIKey = "test-key"
	if reasoning {
		opts.Reasoning = types.ThinkingLevelHigh
	}
	var adapter api.OpenAICompletions
	stream := adapter.StreamSimple(context.Background(), &model, utils.NormalizeContext(&types.Context{
		Messages: []types.Message{
			types.UserMessage{Role: types.RoleUser, Content: types.UserContent{String: "hi"}, Timestamp: 1},
		},
	}), &opts)
	result := stream.Result()
	if result.StopReason == types.StopReasonError {
		t.Fatalf("stream error: %s", result.ErrorMessage)
	}
	return captured
}

func TestDetectCompatProviderFamilies(t *testing.T) {
	cases := []struct {
		name           string
		provider       string
		baseURL        string
		maxTokensField string // expected: "max_tokens" | "max_completion_tokens"
		noStore        bool
	}{
		{"groq standard", "groq", "https://api.groq.com/openai/v1", "max_completion_tokens", false},
		{"cerebras nonstandard", "cerebras", "https://api.cerebras.ai/v1", "max_completion_tokens", true},
		{"together max_tokens", "together", "https://api.together.ai/v1", "max_tokens", true},
		{"nvidia max_tokens", "nvidia", "https://integrate.api.nvidia.com/v1", "max_tokens", true},
		{"zai max_tokens", "zai", "https://api.z.ai/api/coding/paas/v4", "max_tokens", true},
		{"moonshot max_tokens", "moonshotai", "https://api.moonshot.ai/v1", "max_tokens", true},
		{"deepseek max_tokens", "deepseek", "https://api.deepseek.com", "max_tokens", true},
		{"huggingface standard", "huggingface", "https://router.huggingface.co/v1", "max_completion_tokens", false},
		{"ant-ling max_tokens", "ant-ling", "https://api.ant-ling.com/v1", "max_tokens", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			params := captureBody(t, bareModel(tc.provider, tc.baseURL), true)
			if tc.noStore {
				if _, present := params["store"]; present {
					t.Fatalf("store should be absent for %s: %v", tc.provider, params)
				}
			} else if v, ok := params["store"]; !ok || v != false {
				t.Fatalf("store should be false for %s: %v", tc.provider, params)
			}
			if tc.maxTokensField == "max_tokens" {
				if _, ok := params["max_tokens"]; !ok {
					t.Fatalf("max_tokens missing for %s: %v", tc.provider, params)
				}
			} else if _, ok := params["max_completion_tokens"]; !ok {
				t.Fatalf("max_completion_tokens missing for %s: %v", tc.provider, params)
			}
			if _, ok := params["stream_options"]; !ok {
				t.Fatalf("stream_options missing for %s: %v", tc.provider, params)
			}
		})
	}
}

func TestDetectCompatThinkingEncodings(t *testing.T) {
	t.Run("deepseek thinking", func(t *testing.T) {
		params := captureBody(t, bareModel("deepseek", "https://api.deepseek.com"), true)
		thinking, ok := params["thinking"].(map[string]any)
		if !ok || thinking["type"] != "enabled" {
			t.Fatalf("deepseek thinking = %v", params["thinking"])
		}
		if params["reasoning_effort"] == nil {
			t.Fatalf("deepseek reasoning_effort missing: %v", params)
		}
	})
	t.Run("together thinking", func(t *testing.T) {
		params := captureBody(t, bareModel("together", "https://api.together.ai/v1"), true)
		reasoning, ok := params["reasoning"].(map[string]any)
		if !ok || reasoning["enabled"] != true {
			t.Fatalf("together reasoning = %v", params["reasoning"])
		}
	})
	t.Run("zai thinking", func(t *testing.T) {
		params := captureBody(t, bareModel("zai", "https://api.z.ai/api/coding/paas/v4"), true)
		thinking, ok := params["thinking"].(map[string]any)
		if !ok || thinking["type"] != "enabled" {
			t.Fatalf("zai thinking = %v", params["thinking"])
		}
		if _, present := params["reasoning_effort"]; present {
			t.Fatalf("zai should not send reasoning_effort: %v", params)
		}
	})
	t.Run("openrouter thinking", func(t *testing.T) {
		params := captureBody(t, bareModel("openrouter", "https://openrouter.ai/api/v1"), true)
		reasoning, ok := params["reasoning"].(map[string]any)
		if !ok || reasoning["effort"] == nil {
			t.Fatalf("openrouter reasoning = %v", params["reasoning"])
		}
	})
}

func TestBuiltinProvidersAssembled(t *testing.T) {
	providers := BuiltinProviders()
	if len(providers) < 10 {
		t.Fatalf("expected >= 10 builtin providers, got %d", len(providers))
	}
	seen := map[string]bool{}
	for _, p := range providers {
		if seen[p.ID()] {
			t.Fatalf("duplicate provider id %q", p.ID())
		}
		seen[p.ID()] = true
		models := p.GetModels()
		// typesafe is classifier-only (S10-1): no chat catalog until the
		// classify() runtime lands (pig declared deviation).
		if p.ID() != string(ai.ProviderOpenAI) && p.ID() != "typesafe" && len(models) == 0 {
			t.Fatalf("provider %s has no models", p.ID())
		}
		// openai-codex is OAuth-only (pi: auth oauth, no api key).
		if p.ID() != string(ai.ProviderOpenAICodex) && p.Auth().APIKey == nil {
			t.Fatalf("provider %s missing api key auth", p.ID())
		}
	}
	for _, want := range []string{
		"deepseek", "groq", "openrouter", "together", "zai", "nvidia",
		"moonshotai", "moonshotai-cn", "cerebras", "ant-ling", "baseten",
		"huggingface", "zai-coding-cn",
	} {
		if !seen[want] {
			t.Fatalf("builtin provider %q missing", want)
		}
	}
}

func TestBuiltinModelsRegistersNewProviders(t *testing.T) {
	models := BuiltinModels(nil)
	for _, id := range []string{"groq", "together", "zai", "nvidia", "moonshotai", "cerebras", "openrouter", "deepseek"} {
		if models.GetProvider(id) == nil {
			t.Fatalf("provider %s not registered in BuiltinModels", id)
		}
	}
}
