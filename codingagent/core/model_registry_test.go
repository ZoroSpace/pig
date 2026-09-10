// Ported from pi's test/model-registry.test.ts. Adaptations declared at each
// site: pig's builtin provider set is the completions-family subset (Phase 2
// decision), so builtin-provider tests use openrouter/deepseek in place of
// anthropic/google/openai; the cloudflare and github-copilot tests are
// deferred with those providers.
package core

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/auth"
	"github.com/ZoroSpace/pig/ai/types"
	"github.com/samber/lo"
)

type registryHarness struct {
	tempDir        string
	modelsJSONPath string
	authStorage    *AuthStorage
}

func newRegistryHarness(t *testing.T) *registryHarness {
	t.Helper()
	tempDir := t.TempDir()
	h := &registryHarness{
		tempDir:        tempDir,
		modelsJSONPath: filepath.Join(tempDir, "models.json"),
		authStorage:    NewInMemoryAuthStorage(authStorageData{}),
	}
	t.Cleanup(ClearApiKeyCache)
	return h
}

// createRegistry mirrors test/model-runtime-test-utils.ts createModelRegistry.
func (h *registryHarness) createRegistry(t *testing.T) *ModelRegistry {
	t.Helper()
	runtime := CreateModelRuntime(context.Background(), &CreateModelRuntimeOptions{
		Credentials:       h.authStorage,
		ModelsPath:        lo.ToPtr(h.modelsJSONPath),
		ModelsStore:       &InMemoryCodingAgentModelsStore{},
		AllowModelNetwork: false,
	})
	return NewModelRegistry(runtime)
}

func (h *registryHarness) writeRawModelsJSON(t *testing.T, providers map[string]any) {
	t.Helper()
	content, err := json.Marshal(map[string]any{"providers": providers})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(h.modelsJSONPath, content, 0o600); err != nil {
		t.Fatal(err)
	}
}

// registryProviderConfig is the minimal provider config helper (pi:
// providerConfig).
func registryProviderConfig(baseURL string, ids []string, api string) map[string]any {
	models := make([]any, 0, len(ids))
	for _, id := range ids {
		models = append(models, map[string]any{
			"id": id, "name": id, "reasoning": false,
			"input":         []string{"text"},
			"cost":          map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0},
			"contextWindow": 100000, "maxTokens": 8000,
		})
	}
	return map[string]any{"baseUrl": baseURL, "apiKey": "test-key", "api": api, "models": models}
}

func getModelsForProvider(registry *ModelRegistry, provider string) []ai.Model {
	var out []ai.Model
	for _, model := range registry.GetAll() {
		if string(model.Provider) == provider {
			out = append(out, model)
		}
	}
	return out
}

func TestModelRegistryBaseURLOverrideKeepsAllBuiltinModels(t *testing.T) {
	h := newRegistryHarness(t)
	h.writeRawModelsJSON(t, map[string]any{
		"openrouter": map[string]any{"baseUrl": "https://my-proxy.example.com/v1"},
	})
	registry := h.createRegistry(t)

	models := getModelsForProvider(registry, "openrouter")
	if len(models) <= 1 {
		t.Fatalf("models = %d, want multiple builtin models", len(models))
	}
	hasKimi := false
	for _, model := range models {
		if strings.Contains(model.ID, "kimi") {
			hasKimi = true
		}
	}
	if !hasKimi {
		t.Fatal("no kimi model found")
	}
}

func TestModelRegistryBaseURLOverrideChangesURLOnAllBuiltinModels(t *testing.T) {
	h := newRegistryHarness(t)
	h.writeRawModelsJSON(t, map[string]any{
		"openrouter": map[string]any{"baseUrl": "https://my-proxy.example.com/v1"},
	})
	registry := h.createRegistry(t)

	for _, model := range getModelsForProvider(registry, "openrouter") {
		if model.BaseURL != "https://my-proxy.example.com/v1" {
			t.Fatalf("model %s baseUrl = %q", model.ID, model.BaseURL)
		}
	}
}

func TestModelRegistryOverridingHeadersResolvesAtRequestTime(t *testing.T) {
	h := newRegistryHarness(t)
	h.writeRawModelsJSON(t, map[string]any{
		"openrouter": map[string]any{
			"baseUrl": "https://my-proxy.example.com/v1",
			"headers": map[string]any{"X-Custom-Header": "custom-value"},
		},
	})
	registry := h.createRegistry(t)

	for _, model := range getModelsForProvider(registry, "openrouter") {
		authResult := registry.GetApiKeyAndHeaders(context.Background(), model)
		if !authResult.Ok {
			t.Fatalf("auth not ok for %s: %s", model.ID, authResult.Error)
		}
		if header := authResult.Headers["X-Custom-Header"]; header == nil || *header != "custom-value" {
			t.Fatalf("headers = %+v", authResult.Headers)
		}
	}
}

func TestModelRegistryHeadersOnlyOverrideResolvesAtRequestTime(t *testing.T) {
	h := newRegistryHarness(t)
	h.writeRawModelsJSON(t, map[string]any{
		"openrouter": map[string]any{
			"headers": map[string]any{"X-Custom-Header": "custom-value"},
		},
	})
	registry := h.createRegistry(t)
	if registry.GetError() != "" {
		t.Fatalf("error = %q", registry.GetError())
	}
	for _, model := range getModelsForProvider(registry, "openrouter") {
		authResult := registry.GetApiKeyAndHeaders(context.Background(), model)
		if !authResult.Ok {
			t.Fatalf("auth not ok for %s: %s", model.ID, authResult.Error)
		}
		if header := authResult.Headers["X-Custom-Header"]; header == nil || *header != "custom-value" {
			t.Fatalf("headers = %+v", authResult.Headers)
		}
	}
}

func TestModelRegistryUnconfiguredCompatibilityAuthIncludesStaticModelHeaders(t *testing.T) {
	h := newRegistryHarness(t)
	registry := h.createRegistry(t)
	all := registry.GetAll()
	if len(all) == 0 {
		t.Fatal("no models")
	}
	model := all[0]
	model.Provider = "missing-provider"
	model.Headers = map[string]string{"X-Static-Model": "static-value"}

	authResult := registry.GetApiKeyAndHeaders(context.Background(), model)
	if !authResult.Ok {
		t.Fatalf("auth = %+v, want ok", authResult)
	}
	if authResult.APIKey != "" || authResult.BaseURL != "" || len(authResult.Env) != 0 {
		t.Fatalf("auth = %+v, want headers only", authResult)
	}
	if len(authResult.Headers) != 1 {
		t.Fatalf("headers = %+v", authResult.Headers)
	}
	if header := authResult.Headers["X-Static-Model"]; header == nil || *header != "static-value" {
		t.Fatalf("headers = %+v", authResult.Headers)
	}
}

func TestModelRegistryBaseURLOnlyOverrideDoesNotAffectOtherProviders(t *testing.T) {
	h := newRegistryHarness(t)
	h.writeRawModelsJSON(t, map[string]any{
		"openrouter": map[string]any{"baseUrl": "https://my-proxy.example.com/v1"},
	})
	registry := h.createRegistry(t)

	models := getModelsForProvider(registry, "deepseek")
	if len(models) == 0 {
		t.Fatal("no deepseek models")
	}
	if models[0].BaseURL == "https://my-proxy.example.com/v1" {
		t.Fatalf("deepseek baseUrl = %q, want original", models[0].BaseURL)
	}
}

func TestModelRegistryCanMixBaseURLOverrideAndModelsMerge(t *testing.T) {
	h := newRegistryHarness(t)
	h.writeRawModelsJSON(t, map[string]any{
		// baseUrl-only for openrouter.
		"openrouter": map[string]any{"baseUrl": "https://openrouter-proxy.example.com/v1"},
		// Add a custom model for deepseek (merged with built-ins).
		"deepseek": registryProviderConfig("https://deepseek-proxy.example.com/v1", []string{"deepseek-custom"}, "openai-completions"),
	})
	registry := h.createRegistry(t)

	openrouterModels := getModelsForProvider(registry, "openrouter")
	if len(openrouterModels) <= 1 {
		t.Fatalf("openrouter models = %d", len(openrouterModels))
	}
	if openrouterModels[0].BaseURL != "https://openrouter-proxy.example.com/v1" {
		t.Fatalf("openrouter baseUrl = %q", openrouterModels[0].BaseURL)
	}

	deepseekModels := getModelsForProvider(registry, "deepseek")
	if len(deepseekModels) <= 1 {
		t.Fatalf("deepseek models = %d", len(deepseekModels))
	}
	hasCustom := false
	for _, model := range deepseekModels {
		if model.ID == "deepseek-custom" {
			hasCustom = true
		}
	}
	if !hasCustom {
		t.Fatal("deepseek-custom missing")
	}
}

func TestModelRegistryRefreshPicksUpBaseURLOverrideChanges(t *testing.T) {
	h := newRegistryHarness(t)
	h.writeRawModelsJSON(t, map[string]any{
		"openrouter": map[string]any{"baseUrl": "https://first-proxy.example.com/v1"},
	})
	registry := h.createRegistry(t)
	if got := getModelsForProvider(registry, "openrouter")[0].BaseURL; got != "https://first-proxy.example.com/v1" {
		t.Fatalf("baseUrl = %q", got)
	}

	h.writeRawModelsJSON(t, map[string]any{
		"openrouter": map[string]any{"baseUrl": "https://second-proxy.example.com/v1"},
	})
	registry.Refresh(context.Background(), nil)

	if got := getModelsForProvider(registry, "openrouter")[0].BaseURL; got != "https://second-proxy.example.com/v1" {
		t.Fatalf("baseUrl = %q", got)
	}
}

func TestModelRegistryBuiltinProviderCustomModelsInheritAPIAndBaseURL(t *testing.T) {
	h := newRegistryHarness(t)
	h.writeRawModelsJSON(t, map[string]any{
		"openrouter": map[string]any{
			"models": []any{
				map[string]any{
					"id": "fake-provider/fake-model", "name": "Fake model",
					"reasoning": true, "input": []string{"text"},
				},
			},
		},
	})
	registry := h.createRegistry(t)
	if registry.GetError() != "" {
		t.Fatalf("error = %q", registry.GetError())
	}
	model := registry.Find("openrouter", "fake-provider/fake-model")
	if model == nil {
		t.Fatal("model missing")
	}
	if model.API != ai.ApiOpenAICompletions {
		t.Fatalf("api = %q", model.API)
	}
	if model.BaseURL != "https://openrouter.ai/api/v1" {
		t.Fatalf("baseUrl = %q", model.BaseURL)
	}
}

func TestModelRegistryNonBuiltinProviderCustomModelsStillRequireBaseURL(t *testing.T) {
	h := newRegistryHarness(t)
	h.writeRawModelsJSON(t, map[string]any{
		"my-custom-provider": map[string]any{
			"apiKey": "test-key",
			"models": []any{
				map[string]any{
					"id": "my-model", "api": "openai-completions",
					"reasoning": false, "input": []string{"text"},
				},
			},
		},
	})
	registry := h.createRegistry(t)
	if !strings.Contains(registry.GetError(), "baseUrl") {
		t.Fatalf("error = %q", registry.GetError())
	}
}

func TestModelRegistryReportsEveryProviderCompositionError(t *testing.T) {
	h := newRegistryHarness(t)
	h.writeRawModelsJSON(t, map[string]any{
		"broken-one": map[string]any{"api": "openai-completions", "models": []any{map[string]any{"id": "one"}}},
		"broken-two": map[string]any{"api": "openai-completions", "models": []any{map[string]any{"id": "two"}}},
	})
	registry := h.createRegistry(t)
	errText := registry.GetError()
	if !strings.Contains(errText, `Provider "broken-one"`) || !strings.Contains(errText, `Provider "broken-two"`) {
		t.Fatalf("error = %q", errText)
	}
}

func TestModelRegistryCustomProviderWithSameNameAsBuiltinMerges(t *testing.T) {
	h := newRegistryHarness(t)
	h.writeRawModelsJSON(t, map[string]any{
		"openrouter": registryProviderConfig("https://my-proxy.example.com/v1", []string{"kimi-custom"}, "openai-completions"),
	})
	registry := h.createRegistry(t)
	models := getModelsForProvider(registry, "openrouter")

	if len(models) <= 1 {
		t.Fatalf("models = %d", len(models))
	}
	hasCustom, hasBuiltin := false, false
	for _, model := range models {
		if model.ID == "kimi-custom" {
			hasCustom = true
		}
		if strings.Contains(model.ID, "kimi") {
			hasBuiltin = true
		}
	}
	if !hasCustom || !hasBuiltin {
		t.Fatalf("custom=%v builtin=%v", hasCustom, hasBuiltin)
	}
}

func TestModelRegistryCustomModelWithSameIDReplacesBuiltinModel(t *testing.T) {
	h := newRegistryHarness(t)
	h.writeRawModelsJSON(t, map[string]any{
		"openrouter": registryProviderConfig("https://my-proxy.example.com/v1", []string{"anthropic/claude-sonnet-4"}, "openai-completions"),
	})
	registry := h.createRegistry(t)
	var sonnetModels []ai.Model
	for _, model := range getModelsForProvider(registry, "openrouter") {
		if model.ID == "anthropic/claude-sonnet-4" {
			sonnetModels = append(sonnetModels, model)
		}
	}
	if len(sonnetModels) != 1 {
		t.Fatalf("sonnet models = %d, want 1", len(sonnetModels))
	}
	if sonnetModels[0].BaseURL != "https://my-proxy.example.com/v1" {
		t.Fatalf("baseUrl = %q", sonnetModels[0].BaseURL)
	}
}

func TestModelRegistryCustomProviderDoesNotAffectOtherBuiltinProviders(t *testing.T) {
	h := newRegistryHarness(t)
	h.writeRawModelsJSON(t, map[string]any{
		"openrouter": registryProviderConfig("https://my-proxy.example.com/v1", []string{"kimi-custom"}, "openai-completions"),
	})
	registry := h.createRegistry(t)
	if len(getModelsForProvider(registry, "deepseek")) == 0 {
		t.Fatal("deepseek models missing")
	}
	if len(getModelsForProvider(registry, "zai")) == 0 {
		t.Fatal("zai models missing")
	}
}

func TestModelRegistryProviderLevelBaseURLAppliesToBuiltinAndCustomModels(t *testing.T) {
	h := newRegistryHarness(t)
	h.writeRawModelsJSON(t, map[string]any{
		"openrouter": registryProviderConfig("https://merged-proxy.example.com/v1", []string{"kimi-custom"}, "openai-completions"),
	})
	registry := h.createRegistry(t)
	for _, model := range getModelsForProvider(registry, "openrouter") {
		if model.BaseURL != "https://merged-proxy.example.com/v1" {
			t.Fatalf("model %s baseUrl = %q", model.ID, model.BaseURL)
		}
	}
}

func TestModelRegistryProviderLevelCompatAppliesToCustomModels(t *testing.T) {
	h := newRegistryHarness(t)
	h.writeRawModelsJSON(t, map[string]any{
		"demo": map[string]any{
			"baseUrl": "https://example.com/v1",
			"apiKey":  "DEMO_KEY",
			"api":     "openai-completions",
			"compat": map[string]any{
				"supportsUsageInStreaming": false,
				"maxTokensField":           "max_tokens",
			},
			"models": []any{
				map[string]any{
					"id": "demo-model", "reasoning": false, "input": []string{"text"},
					"cost":          map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0},
					"contextWindow": 1000, "maxTokens": 100,
				},
			},
		},
	})
	registry := h.createRegistry(t)
	model := registry.Find("demo", "demo-model")
	if model == nil || model.Compat == nil {
		t.Fatal("model or compat missing")
	}
	compat := model.Compat.(*ai.OpenAICompletionsCompat)
	if compat.SupportsUsageInStreaming == nil || *compat.SupportsUsageInStreaming {
		t.Fatalf("supportsUsageInStreaming = %+v", compat.SupportsUsageInStreaming)
	}
	if compat.MaxTokensField == nil || *compat.MaxTokensField != "max_tokens" {
		t.Fatalf("maxTokensField = %+v", compat.MaxTokensField)
	}
}

func TestModelRegistryModelLevelCompatOverridesProviderLevelCompat(t *testing.T) {
	h := newRegistryHarness(t)
	h.writeRawModelsJSON(t, map[string]any{
		"demo": map[string]any{
			"baseUrl": "https://example.com/v1",
			"apiKey":  "DEMO_KEY",
			"api":     "openai-completions",
			"compat": map[string]any{
				"supportsUsageInStreaming": false,
				"maxTokensField":           "max_tokens",
			},
			"models": []any{
				map[string]any{
					"id": "demo-model", "reasoning": false, "input": []string{"text"},
					"cost":          map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0},
					"contextWindow": 1000, "maxTokens": 100,
					"compat": map[string]any{
						"supportsUsageInStreaming": true,
						"maxTokensField":           "max_completion_tokens",
					},
				},
			},
		},
	})
	registry := h.createRegistry(t)
	model := registry.Find("demo", "demo-model")
	if model == nil || model.Compat == nil {
		t.Fatal("model or compat missing")
	}
	compat := model.Compat.(*ai.OpenAICompletionsCompat)
	if compat.SupportsUsageInStreaming == nil || !*compat.SupportsUsageInStreaming {
		t.Fatalf("supportsUsageInStreaming = %+v", compat.SupportsUsageInStreaming)
	}
	if compat.MaxTokensField == nil || *compat.MaxTokensField != "max_completion_tokens" {
		t.Fatalf("maxTokensField = %+v", compat.MaxTokensField)
	}
}

func TestModelRegistryProviderLevelCompatAppliesToBuiltinModels(t *testing.T) {
	h := newRegistryHarness(t)
	h.writeRawModelsJSON(t, map[string]any{
		"openrouter": map[string]any{
			"compat": map[string]any{
				"supportsUsageInStreaming": false,
				"supportsStrictMode":       false,
			},
		},
	})
	registry := h.createRegistry(t)
	models := getModelsForProvider(registry, "openrouter")
	if len(models) == 0 {
		t.Fatal("no models")
	}
	for _, model := range models {
		compat, ok := model.Compat.(*ai.OpenAICompletionsCompat)
		if !ok || compat.SupportsUsageInStreaming == nil || *compat.SupportsUsageInStreaming {
			t.Fatalf("model %s supportsUsageInStreaming = %+v", model.ID, compat)
		}
		if compat.SupportsStrictMode == nil || *compat.SupportsStrictMode {
			t.Fatalf("model %s supportsStrictMode = %+v", model.ID, compat)
		}
	}
}

func TestModelRegistryModelSchemaAcceptsThinkingLevelMapAndCompatFlags(t *testing.T) {
	h := newRegistryHarness(t)
	h.writeRawModelsJSON(t, map[string]any{
		"demo": map[string]any{
			"baseUrl": "https://example.com/v1",
			"apiKey":  "DEMO_KEY",
			"api":     "openai-completions",
			"models": []any{
				map[string]any{
					"id": "demo-model", "reasoning": true, "input": []string{"text"},
					"cost":          map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0},
					"contextWindow": 1000, "maxTokens": 100,
					"thinkingLevelMap": map[string]any{
						"minimal": nil,
						"high":    "max",
					},
					"compat": map[string]any{
						"supportsStrictMode": false,
						"cacheControlFormat": "anthropic",
					},
				},
			},
		},
	})
	registry := h.createRegistry(t)
	model := registry.Find("demo", "demo-model")
	if model == nil {
		t.Fatal("model missing")
	}
	if registry.GetError() != "" {
		t.Fatalf("error = %q", registry.GetError())
	}
	// pi: thinkingLevelMap equals {minimal: null, high: "max"} — the nil
	// entries suppress the inherited levels.
	if got := model.ThinkingLevelMap[types.ModelThinkingLevel("high")]; got == nil || *got != "max" {
		t.Fatalf("high = %v", got)
	}
	if got, ok := model.ThinkingLevelMap[types.ModelThinkingLevel("minimal")]; !ok || got != nil {
		t.Fatalf("minimal = %v", got)
	}
	compat := model.Compat.(*ai.OpenAICompletionsCompat)
	if compat.SupportsStrictMode == nil || *compat.SupportsStrictMode {
		t.Fatalf("supportsStrictMode = %+v", compat.SupportsStrictMode)
	}
	if compat.CacheControlFormat == nil || *compat.CacheControlFormat != "anthropic" {
		t.Fatalf("cacheControlFormat = %+v", compat.CacheControlFormat)
	}
}

func TestModelRegistryCompatSchemaAcceptsChatTemplateThinkingConfiguration(t *testing.T) {
	h := newRegistryHarness(t)
	h.writeRawModelsJSON(t, map[string]any{
		"demo": map[string]any{
			"baseUrl": "https://example.com/v1",
			"apiKey":  "DEMO_KEY",
			"api":     "openai-completions",
			"models": []any{
				map[string]any{
					"id": "kwargs-model", "reasoning": true, "input": []string{"text"},
					"cost":          map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0},
					"contextWindow": 1000, "maxTokens": 100,
					"compat": map[string]any{
						"thinkingFormat": "chat-template",
						"chatTemplateKwargs": map[string]any{
							"preserve_thinking": true,
							"thinking":          map[string]any{"$var": "thinking.enabled"},
						},
					},
				},
				map[string]any{
					"id": "args-model", "reasoning": true, "input": []string{"text"},
					"cost":          map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0},
					"contextWindow": 1000, "maxTokens": 100,
					"compat": map[string]any{
						"thinkingFormat": "baseten",
						"chatTemplateArgs": map[string]any{
							"enable_thinking": map[string]any{"$var": "thinking.enabled"},
						},
					},
				},
			},
		},
	})
	registry := h.createRegistry(t)
	if registry.GetError() != "" {
		t.Fatalf("error = %q", registry.GetError())
	}
	kwargsModel := registry.Find("demo", "kwargs-model")
	argsModel := registry.Find("demo", "args-model")
	if kwargsModel == nil || argsModel == nil {
		t.Fatal("models missing")
	}
	kwargsCompat := kwargsModel.Compat.(*ai.OpenAICompletionsCompat)
	argsCompat := argsModel.Compat.(*ai.OpenAICompletionsCompat)
	if kwargsCompat.ThinkingFormat == nil || *kwargsCompat.ThinkingFormat != types.ThinkingFormatChatTemplate {
		t.Fatalf("kwargs thinkingFormat = %+v", kwargsCompat.ThinkingFormat)
	}
	if len(kwargsCompat.ChatTemplateKwargs) != 2 {
		t.Fatalf("chatTemplateKwargs = %+v", kwargsCompat.ChatTemplateKwargs)
	}
	if argsCompat.ThinkingFormat == nil || *argsCompat.ThinkingFormat != types.ThinkingFormatBaseten {
		t.Fatalf("args thinkingFormat = %+v", argsCompat.ThinkingFormat)
	}
	if len(argsCompat.ChatTemplateArgs) != 1 {
		t.Fatalf("chatTemplateArgs = %+v", argsCompat.ChatTemplateArgs)
	}
}

func TestModelRegistryCompatSchemaAcceptsAnthropicFlags(t *testing.T) {
	h := newRegistryHarness(t)
	h.writeRawModelsJSON(t, map[string]any{
		"demo": map[string]any{
			"baseUrl": "https://example.com",
			"apiKey":  "DEMO_KEY",
			"api":     "anthropic-messages",
			"compat": map[string]any{
				"supportsEagerToolInputStreaming": false,
				"supportsLongCacheRetention":      false,
			},
			"models": []any{
				map[string]any{
					"id": "demo-model", "reasoning": true, "input": []string{"text"},
					"cost":          map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0},
					"contextWindow": 1000, "maxTokens": 100,
				},
			},
		},
	})
	registry := h.createRegistry(t)
	if registry.GetError() != "" {
		t.Fatalf("error = %q", registry.GetError())
	}
	model := registry.Find("demo", "demo-model")
	if model == nil || model.Compat == nil {
		t.Fatal("model or compat missing")
	}
	compat := model.Compat.(*ai.AnthropicMessagesCompat)
	if compat.SupportsEagerToolInputStreaming == nil || *compat.SupportsEagerToolInputStreaming {
		t.Fatalf("supportsEagerToolInputStreaming = %+v", compat.SupportsEagerToolInputStreaming)
	}
	if compat.SupportsLongCacheRetention == nil || *compat.SupportsLongCacheRetention {
		t.Fatalf("supportsLongCacheRetention = %+v", compat.SupportsLongCacheRetention)
	}
}

func TestModelRegistryModelLevelBaseURLOverridesProviderLevelBaseURL(t *testing.T) {
	h := newRegistryHarness(t)
	h.writeRawModelsJSON(t, map[string]any{
		"opencode-go": map[string]any{
			"baseUrl": "https://opencode.ai/zen/go/v1",
			"apiKey":  "TEST_KEY",
			"models": []any{
				map[string]any{
					"id": "minimax-m2.5", "api": "anthropic-messages",
					"baseUrl":   "https://opencode.ai/zen/go",
					"reasoning": true, "input": []string{"text"},
					"cost":          map[string]any{"input": 0.3, "output": 1.2, "cacheRead": 0.03, "cacheWrite": 0},
					"contextWindow": 204800, "maxTokens": 131072,
				},
				map[string]any{
					"id": "glm-5", "api": "openai-completions",
					"reasoning": true, "input": []string{"text"},
					"cost":          map[string]any{"input": 1, "output": 3.2, "cacheRead": 0.2, "cacheWrite": 0},
					"contextWindow": 204800, "maxTokens": 131072,
				},
			},
		},
	})
	registry := h.createRegistry(t)
	m25 := registry.Find("opencode-go", "minimax-m2.5")
	glm5 := registry.Find("opencode-go", "glm-5")
	if m25 == nil || glm5 == nil {
		t.Fatal("models missing")
	}
	if m25.BaseURL != "https://opencode.ai/zen/go" {
		t.Fatalf("m25 baseUrl = %q", m25.BaseURL)
	}
	if glm5.BaseURL != "https://opencode.ai/zen/go/v1" {
		t.Fatalf("glm5 baseUrl = %q", glm5.BaseURL)
	}
}

func TestModelRegistryModelOverridesStillApplyWhenProviderAlsoDefinesModels(t *testing.T) {
	h := newRegistryHarness(t)
	h.writeRawModelsJSON(t, map[string]any{
		"openrouter": map[string]any{
			"baseUrl": "https://my-proxy.example.com/v1",
			"apiKey":  "OPENROUTER_API_KEY",
			"api":     "openai-completions",
			"models": []any{
				map[string]any{
					"id": "custom/openrouter-model", "name": "Custom OpenRouter Model",
					"reasoning": false, "input": []string{"text"},
					"cost":          map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0},
					"contextWindow": 128000, "maxTokens": 16384,
				},
			},
			"modelOverrides": map[string]any{
				"anthropic/claude-sonnet-4": map[string]any{"name": "Overridden Built-in Sonnet"},
			},
		},
	})
	registry := h.createRegistry(t)
	models := getModelsForProvider(registry, "openrouter")

	hasCustom := false
	sonnetOverridden := false
	for _, model := range models {
		if model.ID == "custom/openrouter-model" {
			hasCustom = true
		}
		if model.ID == "anthropic/claude-sonnet-4" && model.Name == "Overridden Built-in Sonnet" {
			sonnetOverridden = true
		}
	}
	if !hasCustom || !sonnetOverridden {
		t.Fatalf("custom=%v overridden=%v", hasCustom, sonnetOverridden)
	}
}

func TestModelRegistryRefreshReloadsMergedCustomModelsFromDisk(t *testing.T) {
	h := newRegistryHarness(t)
	h.writeRawModelsJSON(t, map[string]any{
		"openrouter": registryProviderConfig("https://first-proxy.example.com/v1", []string{"kimi-custom"}, "openai-completions"),
	})
	registry := h.createRegistry(t)
	if !modelListContainsID(getModelsForProvider(registry, "openrouter"), "kimi-custom") {
		t.Fatal("kimi-custom missing")
	}

	h.writeRawModelsJSON(t, map[string]any{
		"openrouter": registryProviderConfig("https://second-proxy.example.com/v1", []string{"kimi-custom-2"}, "openai-completions"),
	})
	registry.Refresh(context.Background(), nil)

	models := getModelsForProvider(registry, "openrouter")
	if modelListContainsID(models, "kimi-custom") {
		t.Fatal("kimi-custom should be gone")
	}
	if !modelListContainsID(models, "kimi-custom-2") {
		t.Fatal("kimi-custom-2 missing")
	}
	hasBuiltin := false
	for _, model := range models {
		if strings.Contains(model.ID, "kimi") {
			hasBuiltin = true
		}
	}
	if !hasBuiltin {
		t.Fatal("builtin models lost")
	}
}

func TestModelRegistryRemovingCustomModelsKeepsBuiltinProviderModels(t *testing.T) {
	h := newRegistryHarness(t)
	h.writeRawModelsJSON(t, map[string]any{
		"openrouter": registryProviderConfig("https://proxy.example.com/v1", []string{"kimi-custom"}, "openai-completions"),
	})
	registry := h.createRegistry(t)
	if !modelListContainsID(getModelsForProvider(registry, "openrouter"), "kimi-custom") {
		t.Fatal("kimi-custom missing")
	}

	h.writeRawModelsJSON(t, map[string]any{})
	registry.Refresh(context.Background(), nil)

	models := getModelsForProvider(registry, "openrouter")
	if modelListContainsID(models, "kimi-custom") {
		t.Fatal("kimi-custom should be gone")
	}
	hasBuiltin := false
	for _, model := range models {
		if strings.Contains(model.ID, "kimi") {
			hasBuiltin = true
		}
	}
	if !hasBuiltin {
		t.Fatal("builtin models lost")
	}
}

func modelListContainsID(models []ai.Model, id string) bool {
	for _, model := range models {
		if model.ID == id {
			return true
		}
	}
	return false
}

func TestModelRegistryModelOverrideAppliesToASingleBuiltinModel(t *testing.T) {
	h := newRegistryHarness(t)
	h.writeRawModelsJSON(t, map[string]any{
		"openrouter": map[string]any{
			"modelOverrides": map[string]any{
				"anthropic/claude-sonnet-4": map[string]any{"name": "Custom Sonnet Name"},
			},
		},
	})
	registry := h.createRegistry(t)
	models := getModelsForProvider(registry, "openrouter")

	sonnet := lo.FindOrElse(models, ai.Model{}, func(m ai.Model) bool { return m.ID == "anthropic/claude-sonnet-4" })
	if sonnet.ID == "" {
		t.Fatal("sonnet missing")
	}
	if sonnet.Name != "Custom Sonnet Name" {
		t.Fatalf("sonnet name = %q", sonnet.Name)
	}
	opus := lo.FindOrElse(models, ai.Model{}, func(m ai.Model) bool { return m.ID == "anthropic/claude-opus-4" })
	if opus.ID != "" && opus.Name == "Custom Sonnet Name" {
		t.Fatal("override leaked to opus")
	}
}

func TestModelRegistryCustomModelAndModelOverrideCarrySamplingParams(t *testing.T) {
	h := newRegistryHarness(t)
	h.writeRawModelsJSON(t, map[string]any{
		"openrouter": map[string]any{
			"baseUrl": "https://my-proxy.example.com/v1",
			"api":     "openai-completions",
			"models": []any{
				map[string]any{
					"id":             "custom/sampling-model",
					"samplingParams": map[string]any{"temperature": 1, "top_p": 0.95, "top_k": 0},
				},
			},
			"modelOverrides": map[string]any{
				"anthropic/claude-sonnet-4": map[string]any{
					"samplingParams": map[string]any{"top_p": 0.9},
				},
			},
		},
	})
	registry := h.createRegistry(t)
	models := getModelsForProvider(registry, "openrouter")

	custom := lo.FindOrElse(models, ai.Model{}, func(m ai.Model) bool { return m.ID == "custom/sampling-model" })
	if custom.SamplingParams["temperature"] != float64(1) || custom.SamplingParams["top_p"] != 0.95 || custom.SamplingParams["top_k"] != float64(0) {
		t.Fatalf("custom samplingParams = %+v", custom.SamplingParams)
	}
	sonnet := lo.FindOrElse(models, ai.Model{}, func(m ai.Model) bool { return m.ID == "anthropic/claude-sonnet-4" })
	if len(sonnet.SamplingParams) != 1 || sonnet.SamplingParams["top_p"] != 0.9 {
		t.Fatalf("sonnet samplingParams = %+v", sonnet.SamplingParams)
	}
	opus := lo.FindOrElse(models, ai.Model{}, func(m ai.Model) bool { return m.ID == "anthropic/claude-opus-4" })
	if opus.SamplingParams != nil {
		t.Fatalf("opus samplingParams = %+v, want unset", opus.SamplingParams)
	}
}

func TestModelRegistryModelOverrideWithCompatOpenRouterRouting(t *testing.T) {
	h := newRegistryHarness(t)
	h.writeRawModelsJSON(t, map[string]any{
		"openrouter": map[string]any{
			"modelOverrides": map[string]any{
				"anthropic/claude-sonnet-4": map[string]any{
					"compat": map[string]any{
						"openRouterRouting": map[string]any{"only": []string{"amazon-bedrock"}},
					},
				},
			},
		},
	})
	registry := h.createRegistry(t)
	models := getModelsForProvider(registry, "openrouter")
	sonnet := lo.FindOrElse(models, ai.Model{}, func(m ai.Model) bool { return m.ID == "anthropic/claude-sonnet-4" })
	if sonnet.ID == "" {
		t.Fatal("sonnet missing")
	}
	compat := sonnet.Compat.(*ai.OpenAICompletionsCompat)
	if compat.OpenRouterRouting == nil || !slices.Equal(compat.OpenRouterRouting.Only, []string{"amazon-bedrock"}) {
		t.Fatalf("openRouterRouting = %+v", compat.OpenRouterRouting)
	}
}

func TestModelRegistrySupportsFinishReasonConfiguredAtProviderAndModelLevels(t *testing.T) {
	h := newRegistryHarness(t)
	h.writeRawModelsJSON(t, map[string]any{
		"openrouter": map[string]any{
			"compat": map[string]any{"supportsFinishReason": true},
			"modelOverrides": map[string]any{
				"anthropic/claude-sonnet-4": map[string]any{
					"compat": map[string]any{"supportsFinishReason": false},
				},
			},
		},
	})
	registry := h.createRegistry(t)
	models := getModelsForProvider(registry, "openrouter")
	sonnet := lo.FindOrElse(models, ai.Model{}, func(m ai.Model) bool { return m.ID == "anthropic/claude-sonnet-4" })
	opus := lo.FindOrElse(models, ai.Model{}, func(m ai.Model) bool { return m.ID == "anthropic/claude-opus-4" })

	sonnetCompat := sonnet.Compat.(*ai.OpenAICompletionsCompat)
	opusCompat := opus.Compat.(*ai.OpenAICompletionsCompat)
	if sonnetCompat.SupportsFinishReason == nil || *sonnetCompat.SupportsFinishReason {
		t.Fatalf("sonnet supportsFinishReason = %+v", sonnetCompat.SupportsFinishReason)
	}
	if opusCompat.SupportsFinishReason == nil || !*opusCompat.SupportsFinishReason {
		t.Fatalf("opus supportsFinishReason = %+v", opusCompat.SupportsFinishReason)
	}
}

func TestModelRegistryModelOverrideDeepMergesCompatSettings(t *testing.T) {
	h := newRegistryHarness(t)
	h.writeRawModelsJSON(t, map[string]any{
		"openrouter": map[string]any{
			"modelOverrides": map[string]any{
				"anthropic/claude-sonnet-4": map[string]any{
					"compat": map[string]any{
						"openRouterRouting": map[string]any{"order": []string{"anthropic", "together"}},
					},
				},
			},
		},
	})
	registry := h.createRegistry(t)
	models := getModelsForProvider(registry, "openrouter")
	sonnet := lo.FindOrElse(models, ai.Model{}, func(m ai.Model) bool { return m.ID == "anthropic/claude-sonnet-4" })
	compat := sonnet.Compat.(*ai.OpenAICompletionsCompat)
	if compat.OpenRouterRouting == nil {
		t.Fatal("openRouterRouting missing")
	}
	// Deep merge: routing fields merge over any inherited routing config.
	if !slices.Equal(compat.OpenRouterRouting.Order, []string{"anthropic", "together"}) {
		t.Fatalf("order = %+v", compat.OpenRouterRouting.Order)
	}
}

func TestModelRegistryMultipleModelOverridesOnSameProvider(t *testing.T) {
	h := newRegistryHarness(t)
	h.writeRawModelsJSON(t, map[string]any{
		"openrouter": map[string]any{
			"modelOverrides": map[string]any{
				"anthropic/claude-sonnet-4": map[string]any{
					"compat": map[string]any{"openRouterRouting": map[string]any{"only": []string{"amazon-bedrock"}}},
				},
				"anthropic/claude-opus-4": map[string]any{
					"compat": map[string]any{"openRouterRouting": map[string]any{"only": []string{"anthropic"}}},
				},
			},
		},
	})
	registry := h.createRegistry(t)
	models := getModelsForProvider(registry, "openrouter")
	sonnet := lo.FindOrElse(models, ai.Model{}, func(m ai.Model) bool { return m.ID == "anthropic/claude-sonnet-4" })
	opus := lo.FindOrElse(models, ai.Model{}, func(m ai.Model) bool { return m.ID == "anthropic/claude-opus-4" })
	sonnetCompat := sonnet.Compat.(*ai.OpenAICompletionsCompat)
	opusCompat := opus.Compat.(*ai.OpenAICompletionsCompat)
	if !slices.Equal(sonnetCompat.OpenRouterRouting.Only, []string{"amazon-bedrock"}) {
		t.Fatalf("sonnet routing = %+v", sonnetCompat.OpenRouterRouting)
	}
	if !slices.Equal(opusCompat.OpenRouterRouting.Only, []string{"anthropic"}) {
		t.Fatalf("opus routing = %+v", opusCompat.OpenRouterRouting)
	}
}

func TestModelRegistryModelOverrideCombinedWithBaseURLOverride(t *testing.T) {
	h := newRegistryHarness(t)
	h.writeRawModelsJSON(t, map[string]any{
		"openrouter": map[string]any{
			"baseUrl": "https://my-proxy.example.com/v1",
			"modelOverrides": map[string]any{
				"anthropic/claude-sonnet-4": map[string]any{"name": "Proxied Sonnet"},
			},
		},
	})
	registry := h.createRegistry(t)
	models := getModelsForProvider(registry, "openrouter")
	sonnet := lo.FindOrElse(models, ai.Model{}, func(m ai.Model) bool { return m.ID == "anthropic/claude-sonnet-4" })
	if sonnet.BaseURL != "https://my-proxy.example.com/v1" || sonnet.Name != "Proxied Sonnet" {
		t.Fatalf("sonnet = %+v", sonnet)
	}
	opus := lo.FindOrElse(models, ai.Model{}, func(m ai.Model) bool { return m.ID == "anthropic/claude-opus-4" })
	if opus.BaseURL != "https://my-proxy.example.com/v1" {
		t.Fatalf("opus baseUrl = %q", opus.BaseURL)
	}
	if opus.Name == "Proxied Sonnet" {
		t.Fatal("name override leaked to opus")
	}
}

func TestModelRegistryModelOverrideForNonExistentModelIDIsIgnored(t *testing.T) {
	h := newRegistryHarness(t)
	h.writeRawModelsJSON(t, map[string]any{
		"openrouter": map[string]any{
			"modelOverrides": map[string]any{
				"nonexistent/model-id": map[string]any{"name": "This should not appear"},
			},
		},
	})
	registry := h.createRegistry(t)
	models := getModelsForProvider(registry, "openrouter")
	if modelListContainsID(models, "nonexistent/model-id") {
		t.Fatal("override created a model")
	}
	if registry.GetError() != "" {
		t.Fatalf("error = %q", registry.GetError())
	}
}

func TestModelRegistryModelOverrideCanChangeCostFieldsPartially(t *testing.T) {
	h := newRegistryHarness(t)
	h.writeRawModelsJSON(t, map[string]any{
		"openrouter": map[string]any{
			"modelOverrides": map[string]any{
				"anthropic/claude-sonnet-4": map[string]any{
					"cost": map[string]any{"input": 99},
				},
			},
		},
	})
	registry := h.createRegistry(t)
	models := getModelsForProvider(registry, "openrouter")
	sonnet := lo.FindOrElse(models, ai.Model{}, func(m ai.Model) bool { return m.ID == "anthropic/claude-sonnet-4" })
	if sonnet.Cost.Input != 99 {
		t.Fatalf("input cost = %v", sonnet.Cost.Input)
	}
	if sonnet.Cost.Output <= 0 {
		t.Fatalf("output cost = %v, want preserved", sonnet.Cost.Output)
	}
}

func TestModelRegistryModelOverrideCanAddHeadersAtRequestTime(t *testing.T) {
	h := newRegistryHarness(t)
	h.writeRawModelsJSON(t, map[string]any{
		"openrouter": map[string]any{
			"modelOverrides": map[string]any{
				"anthropic/claude-sonnet-4": map[string]any{
					"headers": map[string]any{"X-Custom-Model-Header": "value"},
				},
			},
		},
	})
	registry := h.createRegistry(t)
	models := getModelsForProvider(registry, "openrouter")
	sonnet := lo.FindOrElse(models, ai.Model{}, func(m ai.Model) bool { return m.ID == "anthropic/claude-sonnet-4" })
	if sonnet.ID == "" {
		t.Fatal("sonnet missing")
	}
	authResult := registry.GetApiKeyAndHeaders(context.Background(), sonnet)
	if !authResult.Ok {
		t.Fatalf("auth = %+v", authResult)
	}
	if header := authResult.Headers["X-Custom-Model-Header"]; header == nil || *header != "value" {
		t.Fatalf("headers = %+v", authResult.Headers)
	}
}

func TestModelRegistryRefreshPicksUpModelOverrideChanges(t *testing.T) {
	h := newRegistryHarness(t)
	h.writeRawModelsJSON(t, map[string]any{
		"openrouter": map[string]any{
			"modelOverrides": map[string]any{
				"anthropic/claude-sonnet-4": map[string]any{"name": "First Name"},
			},
		},
	})
	registry := h.createRegistry(t)
	sonnet := lo.FindOrElse(getModelsForProvider(registry, "openrouter"), ai.Model{},
		func(m ai.Model) bool { return m.ID == "anthropic/claude-sonnet-4" })
	if sonnet.Name != "First Name" {
		t.Fatalf("name = %q", sonnet.Name)
	}

	h.writeRawModelsJSON(t, map[string]any{
		"openrouter": map[string]any{
			"modelOverrides": map[string]any{
				"anthropic/claude-sonnet-4": map[string]any{"name": "Second Name"},
			},
		},
	})
	registry.Refresh(context.Background(), nil)

	sonnet = lo.FindOrElse(getModelsForProvider(registry, "openrouter"), ai.Model{},
		func(m ai.Model) bool { return m.ID == "anthropic/claude-sonnet-4" })
	if sonnet.Name != "Second Name" {
		t.Fatalf("name = %q", sonnet.Name)
	}
}

func TestModelRegistryRemovingModelOverrideRestoresBuiltinValues(t *testing.T) {
	h := newRegistryHarness(t)
	h.writeRawModelsJSON(t, map[string]any{
		"openrouter": map[string]any{
			"modelOverrides": map[string]any{
				"anthropic/claude-sonnet-4": map[string]any{"name": "Custom Name"},
			},
		},
	})
	registry := h.createRegistry(t)
	sonnet := lo.FindOrElse(getModelsForProvider(registry, "openrouter"), ai.Model{},
		func(m ai.Model) bool { return m.ID == "anthropic/claude-sonnet-4" })
	if sonnet.Name != "Custom Name" {
		t.Fatalf("name = %q", sonnet.Name)
	}

	h.writeRawModelsJSON(t, map[string]any{})
	registry.Refresh(context.Background(), nil)

	restored := lo.FindOrElse(getModelsForProvider(registry, "openrouter"), ai.Model{},
		func(m ai.Model) bool { return m.ID == "anthropic/claude-sonnet-4" })
	if restored.Name == "Custom Name" {
		t.Fatalf("name = %q, want builtin restored", restored.Name)
	}
}

func TestModelRegistryGetProviderDisplayNameResolvesRegisteredBuiltinAndFallbackNames(t *testing.T) {
	h := newRegistryHarness(t)
	registry := h.createRegistry(t)

	if got := registry.GetProviderDisplayName("deepseek"); got != "DeepSeek" {
		t.Fatalf("deepseek = %q", got)
	}
	if got := registry.GetProviderDisplayName("zai"); got != "Z.AI" {
		t.Fatalf("zai = %q", got)
	}
	if got := registry.GetProviderDisplayName("unknown-provider"); got != "unknown-provider" {
		t.Fatalf("unknown = %q", got)
	}

	if err := registry.RegisterProvider("named-provider", &ProviderConfigInput{
		Name:    "Named Provider",
		BaseURL: "https://provider.test/v1",
		APIKey:  "test-key",
		API:     ai.ApiOpenAICompletions,
		Models:  []ProviderConfigInputModel{registryTestModel("demo-model", "Demo Model")},
	}); err != nil {
		t.Fatal(err)
	}
	if got := registry.GetProviderDisplayName("named-provider"); got != "Named Provider" {
		t.Fatalf("named-provider = %q", got)
	}

	if err := registry.RegisterProvider("oauth-provider", &ProviderConfigInput{
		BaseURL: "https://provider.test/v1",
		API:     ai.ApiOpenAICompletions,
		OAuth: &ExtensionOAuthConfig{
			Name: "OAuth Provider",
			Login: func(callbacks ai.OAuthLoginCallbacks) (auth.OAuthCredentials, error) {
				return auth.OAuthCredentials{Access: "access", Refresh: "refresh", Expires: time.Now().Add(60_000).UnixMilli()}, nil
			},
			RefreshToken: func(ctx context.Context, credentials auth.OAuthCredentials) (auth.OAuthCredentials, error) {
				return credentials, nil
			},
			GetAPIKey: func(credentials auth.OAuthCredentials) string { return credentials.Access },
		},
		Models: []ProviderConfigInputModel{registryTestModel("demo-model", "Demo Model")},
	}); err != nil {
		t.Fatal(err)
	}
	if got := registry.GetProviderDisplayName("oauth-provider"); got != "OAuth Provider" {
		t.Fatalf("oauth-provider = %q", got)
	}
}

func registryTestModel(id, name string) ProviderConfigInputModel {
	return ProviderConfigInputModel{
		ID: id, Name: name, Reasoning: false,
		Input:         []ai.ModelInput{ai.ModelInputText},
		Cost:          ai.ModelCost{},
		ContextWindow: 128000, MaxTokens: 4096,
	}
}

func TestModelRegistryModelOverridesApplyToDynamicallyRegisteredProviderModels(t *testing.T) {
	h := newRegistryHarness(t)
	h.writeRawModelsJSON(t, map[string]any{
		"extension-provider": map[string]any{
			"modelOverrides": map[string]any{
				"extension-model": map[string]any{
					"name": "Overridden Extension Model",
					"thinkingLevelMap": map[string]any{
						"off": nil, "minimal": nil, "low": nil, "medium": nil, "xhigh": "max",
					},
					"headers": map[string]any{"x-model-override": "enabled"},
				},
			},
		},
	})
	registry := h.createRegistry(t)
	if err := registry.RegisterProvider("extension-provider", &ProviderConfigInput{
		BaseURL: "https://provider.test/v1",
		APIKey:  "test-key",
		API:     ai.ApiOpenAICompletions,
		Models: []ProviderConfigInputModel{{
			ID: "extension-model", Name: "Extension Model", Reasoning: true,
			Input: []ai.ModelInput{ai.ModelInputText}, Cost: ai.ModelCost{},
			ContextWindow: 128000, MaxTokens: 4096,
		}},
	}); err != nil {
		t.Fatal(err)
	}

	model := registry.Find("extension-provider", "extension-model")
	if model == nil {
		t.Fatal("extension model was not registered")
	}
	if model.Name != "Overridden Extension Model" {
		t.Fatalf("name = %q", model.Name)
	}
	// pi: thinkingLevelMap equals the override map with null entries.
	for key, want := range map[string]*string{
		"off": nil, "minimal": nil, "low": nil, "medium": nil, "xhigh": lo.ToPtr("max"),
	} {
		got, ok := model.ThinkingLevelMap[types.ModelThinkingLevel(key)]
		if !ok {
			t.Fatalf("thinkingLevelMap missing %q", key)
		}
		if want == nil && got != nil {
			t.Fatalf("thinkingLevelMap[%q] = %q, want null", key, *got)
		}
		if want != nil && (got == nil || *got != *want) {
			t.Fatalf("thinkingLevelMap[%q] = %v, want %q", key, got, *want)
		}
	}
	levels := ai.GetSupportedThinkingLevels(model)
	if len(levels) != 2 || levels[0] != types.ModelThinkingHigh || levels[1] != types.ModelThinkingXHigh {
		t.Fatalf("supported thinking levels = %v, want [high xhigh]", levels)
	}
	authResult := registry.GetApiKeyAndHeaders(context.Background(), *model)
	if !authResult.Ok {
		t.Fatalf("auth = %+v", authResult)
	}
	if header := authResult.Headers["x-model-override"]; header == nil || *header != "enabled" {
		t.Fatalf("headers = %+v", authResult.Headers)
	}
}

func TestModelRegistryRegisterProviderTreatsUppercaseAPIKeyAndHeadersAsLiterals(t *testing.T) {
	h := newRegistryHarness(t)
	t.Setenv("CUSTOM_NAME", "env-CUSTOM_NAME")
	t.Setenv("BEARER", "env-BEARER")
	t.Setenv("MODEL_TOKEN", "env-MODEL_TOKEN")
	registry := h.createRegistry(t)

	config := registryProviderConfig("https://provider.test/v1", []string{"demo-model"}, "openai-completions")
	config["apiKey"] = "CUSTOM_NAME"
	config["headers"] = map[string]any{"Authorization": "BEARER"}
	if err := registry.RegisterProvider("literal-provider", &ProviderConfigInput{
		Name:    "literal-provider",
		BaseURL: "https://provider.test/v1",
		APIKey:  "CUSTOM_NAME",
		API:     ai.ApiOpenAICompletions,
		Headers: map[string]string{"Authorization": "BEARER"},
		Models: []ProviderConfigInputModel{{
			ID: "demo-model", Name: "demo-model", Reasoning: false,
			Input: []ai.ModelInput{ai.ModelInputText}, Cost: ai.ModelCost{},
			ContextWindow: 100000, MaxTokens: 8000,
			Headers: map[string]string{"x-model-token": "MODEL_TOKEN"},
		}},
	}); err != nil {
		t.Fatal(err)
	}

	if got := registry.GetApiKeyForProvider(context.Background(), "literal-provider"); got != "CUSTOM_NAME" {
		t.Fatalf("api key = %q, want the literal", got)
	}
	model := registry.Find("literal-provider", "demo-model")
	if model == nil {
		t.Fatal("model missing")
	}
	authResult := registry.GetApiKeyAndHeaders(context.Background(), *model)
	if !authResult.Ok || authResult.APIKey != "CUSTOM_NAME" {
		t.Fatalf("auth = %+v", authResult)
	}
	if header := authResult.Headers["Authorization"]; header == nil || *header != "BEARER" {
		t.Fatalf("headers = %+v", authResult.Headers)
	}
	if header := authResult.Headers["x-model-token"]; header == nil || *header != "MODEL_TOKEN" {
		t.Fatalf("headers = %+v", authResult.Headers)
	}
}

func TestModelRegistryFailedRegisterProviderDoesNotPersistInvalidStreamSimpleConfig(t *testing.T) {
	h := newRegistryHarness(t)
	registry := h.createRegistry(t)

	err := registry.RegisterProvider("broken-provider", &ProviderConfigInput{})
	_ = err
	// pi: registerProvider({streamSimple}) throws the api-required error.
	err = func() (err error) {
		defer func() {
			if r := recover(); r != nil {
				if e, ok := r.(error); ok {
					err = e
				}
			}
		}()
		return registry.RegisterProvider("broken-provider", &ProviderConfigInput{
			StreamSimple: func(ctx context.Context, model *ai.Model, llmContext *ai.Context, options *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream {
				panic("should not run")
			},
		})
	}()
	if err == nil || !strings.Contains(err.Error(), `Provider broken-provider: "api" is required when registering streamSimple.`) {
		t.Fatalf("error = %v", err)
	}
	result := registry.Refresh(context.Background(), nil)
	if result.Aborted {
		t.Fatal("refresh aborted")
	}
}

func TestModelRegistryFailedRegisterProviderDoesNotRemoveExistingProviderModels(t *testing.T) {
	h := newRegistryHarness(t)
	registry := h.createRegistry(t)

	if err := registry.RegisterProvider("demo-provider", &ProviderConfigInput{
		BaseURL: "https://provider.test/v1",
		APIKey:  "test-key",
		API:     ai.ApiOpenAICompletions,
		Models: []ProviderConfigInputModel{{
			ID: "demo-model", Name: "Demo Model", Reasoning: false,
			Input: []ai.ModelInput{ai.ModelInputText}, Cost: ai.ModelCost{},
			ContextWindow: 128000, MaxTokens: 4096,
		}},
	}); err != nil {
		t.Fatal(err)
	}
	if registry.Find("demo-provider", "demo-model") == nil {
		t.Fatal("demo-model missing after first registration")
	}

	err := registry.RegisterProvider("demo-provider", &ProviderConfigInput{
		BaseURL: "https://provider.test/v2",
		APIKey:  "test-key",
		Models: []ProviderConfigInputModel{{
			ID: "broken-model", Name: "Broken Model", Reasoning: false,
			Input: []ai.ModelInput{ai.ModelInputText}, Cost: ai.ModelCost{},
			ContextWindow: 128000, MaxTokens: 4096,
		}},
	})
	if err == nil || !strings.Contains(err.Error(), `Provider demo-provider, model broken-model: no "api" specified.`) {
		t.Fatalf("error = %v", err)
	}
	if registry.Find("demo-provider", "demo-model") == nil {
		t.Fatal("demo-model lost after failed re-registration")
	}
	if result := registry.Refresh(context.Background(), nil); result.Aborted {
		t.Fatal("refresh aborted")
	}
	if registry.Find("demo-provider", "demo-model") == nil {
		t.Fatal("demo-model lost after refresh")
	}
}

func TestModelRegistryUnregisterProviderRemovesTheRuntimeOAuthOverlayWithoutMutatingGlobalState(t *testing.T) {
	h := newRegistryHarness(t)
	registry := h.createRegistry(t)

	if err := registry.RegisterProvider("openrouter", &ProviderConfigInput{
		OAuth: &ExtensionOAuthConfig{
			Name: "Custom OpenRouter OAuth",
			Login: func(callbacks ai.OAuthLoginCallbacks) (auth.OAuthCredentials, error) {
				return auth.OAuthCredentials{
					Access: "custom-access-token", Refresh: "custom-refresh-token",
					Expires: time.Now().Add(60_000).UnixMilli(),
				}, nil
			},
			RefreshToken: func(ctx context.Context, credentials auth.OAuthCredentials) (auth.OAuthCredentials, error) {
				return credentials, nil
			},
			GetAPIKey: func(credentials auth.OAuthCredentials) string { return credentials.Access },
		},
	}); err != nil {
		t.Fatal(err)
	}
	config := registry.GetRegisteredProviderConfig("openrouter")
	if config == nil || config.OAuth == nil || config.OAuth.Name != "Custom OpenRouter OAuth" {
		t.Fatalf("config = %+v", config)
	}

	registry.UnregisterProvider("openrouter")
	if registry.GetRegisteredProviderConfig("openrouter") != nil {
		t.Fatal("config survived unregister")
	}
}

func TestModelRegistryStreamSimpleOverlaysDoNotMutateTheGlobalCompatAPIRegistry(t *testing.T) {
	h := newRegistryHarness(t)
	registry := h.createRegistry(t)

	if err := registry.RegisterProvider("stream-override-provider", &ProviderConfigInput{
		API: ai.ApiOpenAICompletions,
		StreamSimple: func(ctx context.Context, model *ai.Model, llmContext *ai.Context, options *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream {
			panic("custom streamSimple override")
		},
	}); err != nil {
		t.Fatal(err)
	}

	assertRegistryNotOverridden(t)
	registry.UnregisterProvider("stream-override-provider")
	assertRegistryNotOverridden(t)
}

func assertRegistryNotOverridden(t *testing.T) {
	t.Helper()
	streams, ok := ai.GetApiProvider(ai.ApiOpenAICompletions)
	if !ok {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			if message := fmt.Sprint(r); strings.Contains(message, "custom streamSimple override") {
				t.Fatal("extension overlay leaked into the global api registry")
			}
		}
	}()
	streams.StreamSimple(context.Background(), &ai.Model{API: ai.ApiOpenAICompletions}, &ai.Context{}, &ai.SimpleStreamOptions{})
}

// registryProviderWithAPIKey is the providerWithApiKey helper (pi).
func registryProviderWithAPIKey(apiKey string) map[string]any {
	return map[string]any{
		"baseUrl": "https://example.com/v1",
		"apiKey":  apiKey,
		"api":     "anthropic-messages",
		"models": []any{
			map[string]any{
				"id": "test-model", "name": "Test Model",
				"reasoning": false, "input": []string{"text"},
				"cost":          map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0},
				"contextWindow": 100000, "maxTokens": 8000,
			},
		},
	}
}

func TestModelRegistryAPIKeyCommandExecutesAndUsesStdout(t *testing.T) {
	h := newRegistryHarness(t)
	h.writeRawModelsJSON(t, map[string]any{
		"custom-provider": registryProviderWithAPIKey("!echo test-api-key-from-command"),
	})
	registry := h.createRegistry(t)
	if got := registry.GetApiKeyForProvider(context.Background(), "custom-provider"); got != "test-api-key-from-command" {
		t.Fatalf("api key = %q", got)
	}
}

func TestModelRegistryAPIKeyCommandTrimsWhitespace(t *testing.T) {
	h := newRegistryHarness(t)
	h.writeRawModelsJSON(t, map[string]any{
		"custom-provider": registryProviderWithAPIKey(`!echo '  spaced-key  '`),
	})
	registry := h.createRegistry(t)
	if got := registry.GetApiKeyForProvider(context.Background(), "custom-provider"); got != "spaced-key" {
		t.Fatalf("api key = %q", got)
	}
}

func TestModelRegistryAPIKeyCommandHandlesMultilineOutput(t *testing.T) {
	h := newRegistryHarness(t)
	h.writeRawModelsJSON(t, map[string]any{
		"custom-provider": registryProviderWithAPIKey("!printf 'line1\\nline2'"),
	})
	registry := h.createRegistry(t)
	if got := registry.GetApiKeyForProvider(context.Background(), "custom-provider"); got != "line1\nline2" {
		t.Fatalf("api key = %q", got)
	}
}

func TestModelRegistryAPIKeyCommandReturnsEmptyOnFailure(t *testing.T) {
	h := newRegistryHarness(t)
	h.writeRawModelsJSON(t, map[string]any{
		"custom-provider": registryProviderWithAPIKey("!exit 1"),
	})
	registry := h.createRegistry(t)
	if got := registry.GetApiKeyForProvider(context.Background(), "custom-provider"); got != "" {
		t.Fatalf("api key = %q, want empty", got)
	}
}

func TestModelRegistryAPIKeyCommandReturnsEmptyOnNonexistentCommand(t *testing.T) {
	h := newRegistryHarness(t)
	h.writeRawModelsJSON(t, map[string]any{
		"custom-provider": registryProviderWithAPIKey("!nonexistent-command-12345"),
	})
	registry := h.createRegistry(t)
	if got := registry.GetApiKeyForProvider(context.Background(), "custom-provider"); got != "" {
		t.Fatalf("api key = %q, want empty", got)
	}
}

func TestModelRegistryAPIKeyCommandReturnsEmptyOnEmptyOutput(t *testing.T) {
	h := newRegistryHarness(t)
	h.writeRawModelsJSON(t, map[string]any{
		"custom-provider": registryProviderWithAPIKey("!printf ''"),
	})
	registry := h.createRegistry(t)
	if got := registry.GetApiKeyForProvider(context.Background(), "custom-provider"); got != "" {
		t.Fatalf("api key = %q, want empty", got)
	}
}

func TestModelRegistryAPIKeyEnvPrefixResolvesToEnvValue(t *testing.T) {
	h := newRegistryHarness(t)
	t.Setenv("TEST_API_KEY_12345", "env-api-key-value")
	h.writeRawModelsJSON(t, map[string]any{
		"custom-provider": registryProviderWithAPIKey("$TEST_API_KEY_12345"),
	})
	registry := h.createRegistry(t)
	if got := registry.GetApiKeyForProvider(context.Background(), "custom-provider"); got != "env-api-key-value" {
		t.Fatalf("api key = %q", got)
	}
}

func TestModelRegistryAPIKeyBracedEnvSyntaxResolvesToEnvValue(t *testing.T) {
	h := newRegistryHarness(t)
	t.Setenv("TEST_BRACED_API_KEY_12345", "braced-env-api-key-value")
	h.writeRawModelsJSON(t, map[string]any{
		"custom-provider": registryProviderWithAPIKey("${TEST_BRACED_API_KEY_12345}"),
	})
	registry := h.createRegistry(t)
	if got := registry.GetApiKeyForProvider(context.Background(), "custom-provider"); got != "braced-env-api-key-value" {
		t.Fatalf("api key = %q", got)
	}
}

func TestModelRegistryAPIKeyInterpolatesBracedEnvReferencesInsideLiterals(t *testing.T) {
	h := newRegistryHarness(t)
	t.Setenv("TEST_INTERPOLATED_PART_A_12345", "left")
	t.Setenv("TEST_INTERPOLATED_PART_B_12345", "right")
	h.writeRawModelsJSON(t, map[string]any{
		"custom-provider": registryProviderWithAPIKey("${TEST_INTERPOLATED_PART_A_12345}_${TEST_INTERPOLATED_PART_B_12345}"),
	})
	registry := h.createRegistry(t)
	if got := registry.GetApiKeyForProvider(context.Background(), "custom-provider"); got != "left_right" {
		t.Fatalf("api key = %q", got)
	}
}

func TestModelRegistryAPIKeyDoubleDollarEscapesLeadingDollar(t *testing.T) {
	h := newRegistryHarness(t)
	h.writeRawModelsJSON(t, map[string]any{
		"custom-provider": registryProviderWithAPIKey("$$TEST_API_KEY_12345"),
	})
	registry := h.createRegistry(t)
	if got := registry.GetApiKeyForProvider(context.Background(), "custom-provider"); got != "$TEST_API_KEY_12345" {
		t.Fatalf("api key = %q", got)
	}
}

func TestModelRegistryAPIKeyEscapedBangStillInterpolatesLaterEnvRefs(t *testing.T) {
	h := newRegistryHarness(t)
	t.Setenv("TEST_API_KEY_12345", "env-api-key-value")
	h.writeRawModelsJSON(t, map[string]any{
		"custom-provider": registryProviderWithAPIKey("$!literal-$TEST_API_KEY_12345"),
	})
	registry := h.createRegistry(t)
	if got := registry.GetApiKeyForProvider(context.Background(), "custom-provider"); got != "!literal-env-api-key-value" {
		t.Fatalf("api key = %q", got)
	}
}

func TestModelRegistryPlainAPIKeyUsedDirectlyEvenWhenItMatchesAnEnvVar(t *testing.T) {
	h := newRegistryHarness(t)
	t.Setenv("TEST_API_KEY_12345", "env-api-key-value")
	h.writeRawModelsJSON(t, map[string]any{
		"custom-provider": registryProviderWithAPIKey("TEST_API_KEY_12345"),
	})
	registry := h.createRegistry(t)
	if got := registry.GetApiKeyForProvider(context.Background(), "custom-provider"); got != "TEST_API_KEY_12345" {
		t.Fatalf("api key = %q", got)
	}
}

func TestModelRegistryAPIKeyLiteralValueUsedDirectly(t *testing.T) {
	h := newRegistryHarness(t)
	os.Unsetenv("literal_api_key_value")
	h.writeRawModelsJSON(t, map[string]any{
		"custom-provider": registryProviderWithAPIKey("literal_api_key_value"),
	})
	registry := h.createRegistry(t)
	if got := registry.GetApiKeyForProvider(context.Background(), "custom-provider"); got != "literal_api_key_value" {
		t.Fatalf("api key = %q", got)
	}
}

func TestModelRegistryAPIKeyCommandCanUseShellFeaturesLikePipes(t *testing.T) {
	h := newRegistryHarness(t)
	h.writeRawModelsJSON(t, map[string]any{
		"custom-provider": registryProviderWithAPIKey("!echo 'hello world' | tr ' ' '-'"),
	})
	registry := h.createRegistry(t)
	if got := registry.GetApiKeyForProvider(context.Background(), "custom-provider"); got != "hello-world" {
		t.Fatalf("api key = %q", got)
	}
}

// writeCounterFile seeds a counter file and returns the counting command
// (pi: counterFile + sh -c command pattern).
func writeCounterFile(t *testing.T, h *registryHarness, name string) string {
	t.Helper()
	counterFile := filepath.Join(h.tempDir, name)
	if err := os.WriteFile(counterFile, []byte("0"), 0o600); err != nil {
		t.Fatal(err)
	}
	return counterFile
}

func readCounter(t *testing.T, path string) int {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	if _, err := fmt.Sscanf(strings.TrimSpace(string(content)), "%d", &count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestModelRegistryCommandIsExecutedOnEveryProviderLookup(t *testing.T) {
	h := newRegistryHarness(t)
	counterFile := writeCounterFile(t, h, "counter")
	command := fmt.Sprintf(`!sh -c 'count=$(cat "%s"); echo $((count + 1)) > "%s"; echo "key-value"'`, counterFile, counterFile)
	h.writeRawModelsJSON(t, map[string]any{
		"custom-provider": registryProviderWithAPIKey(command),
	})
	registry := h.createRegistry(t)
	for i := 0; i < 3; i++ {
		registry.GetApiKeyForProvider(context.Background(), "custom-provider")
	}
	if count := readCounter(t, counterFile); count != 3 {
		t.Fatalf("count = %d, want 3", count)
	}
}

func TestModelRegistryCommandsAreReExecutedAcrossRegistryInstances(t *testing.T) {
	h := newRegistryHarness(t)
	counterFile := writeCounterFile(t, h, "counter")
	command := fmt.Sprintf(`!sh -c 'count=$(cat "%s"); echo $((count + 1)) > "%s"; echo "key-value"'`, counterFile, counterFile)
	h.writeRawModelsJSON(t, map[string]any{
		"custom-provider": registryProviderWithAPIKey(command),
	})
	registry1 := h.createRegistry(t)
	registry1.GetApiKeyForProvider(context.Background(), "custom-provider")
	registry2 := h.createRegistry(t)
	registry2.GetApiKeyForProvider(context.Background(), "custom-provider")
	if count := readCounter(t, counterFile); count != 2 {
		t.Fatalf("count = %d, want 2", count)
	}
}

func TestModelRegistryDifferentCommandsResolveIndependently(t *testing.T) {
	h := newRegistryHarness(t)
	h.writeRawModelsJSON(t, map[string]any{
		"provider-a": registryProviderWithAPIKey("!echo key-a"),
		"provider-b": registryProviderWithAPIKey("!echo key-b"),
	})
	registry := h.createRegistry(t)
	if got := registry.GetApiKeyForProvider(context.Background(), "provider-a"); got != "key-a" {
		t.Fatalf("key-a = %q", got)
	}
	if got := registry.GetApiKeyForProvider(context.Background(), "provider-b"); got != "key-b" {
		t.Fatalf("key-b = %q", got)
	}
}

func TestModelRegistryFailedCommandsAreRetried(t *testing.T) {
	h := newRegistryHarness(t)
	counterFile := writeCounterFile(t, h, "counter")
	command := fmt.Sprintf(`!sh -c 'count=$(cat "%s"); echo $((count + 1)) > "%s"; exit 1'`, counterFile, counterFile)
	h.writeRawModelsJSON(t, map[string]any{
		"custom-provider": registryProviderWithAPIKey(command),
	})
	registry := h.createRegistry(t)
	if got := registry.GetApiKeyForProvider(context.Background(), "custom-provider"); got != "" {
		t.Fatalf("key1 = %q, want empty", got)
	}
	if got := registry.GetApiKeyForProvider(context.Background(), "custom-provider"); got != "" {
		t.Fatalf("key2 = %q, want empty", got)
	}
	if count := readCounter(t, counterFile); count != 2 {
		t.Fatalf("count = %d, want 2", count)
	}
}

func TestModelRegistryProviderAuthStatusReportsAPIKeyEnvironmentVariables(t *testing.T) {
	h := newRegistryHarness(t)
	t.Setenv("TEST_API_KEY_STATUS_TEST_98765", "status-test-key")
	h.writeRawModelsJSON(t, map[string]any{
		"custom-provider": registryProviderWithAPIKey("$TEST_API_KEY_STATUS_TEST_98765"),
	})
	registry := h.createRegistry(t)
	status := registry.GetProviderAuthStatus("custom-provider")
	if !status.Configured || status.Source != "environment" || status.Label != "TEST_API_KEY_STATUS_TEST_98765" {
		t.Fatalf("status = %+v", status)
	}
}

func TestModelRegistryProviderAuthStatusReportsInterpolatedAPIKeyEnvironmentVariables(t *testing.T) {
	h := newRegistryHarness(t)
	t.Setenv("TEST_API_KEY_STATUS_PART_A_98765", "left")
	t.Setenv("TEST_API_KEY_STATUS_PART_B_98765", "right")
	h.writeRawModelsJSON(t, map[string]any{
		"custom-provider": registryProviderWithAPIKey("${TEST_API_KEY_STATUS_PART_A_98765}_${TEST_API_KEY_STATUS_PART_B_98765}"),
	})
	registry := h.createRegistry(t)
	status := registry.GetProviderAuthStatus("custom-provider")
	if !status.Configured || status.Source != "environment" ||
		status.Label != "TEST_API_KEY_STATUS_PART_A_98765, TEST_API_KEY_STATUS_PART_B_98765" {
		t.Fatalf("status = %+v", status)
	}
}

func TestModelRegistryProviderAuthStatusReportsNonEnvAPIKeyValuesAsConfigKey(t *testing.T) {
	h := newRegistryHarness(t)
	h.writeRawModelsJSON(t, map[string]any{
		"custom-provider": registryProviderWithAPIKey("literal_api_key_value"),
	})
	registry := h.createRegistry(t)
	status := registry.GetProviderAuthStatus("custom-provider")
	if !status.Configured || status.Source != "models_json_key" {
		t.Fatalf("status = %+v", status)
	}
}

func TestModelRegistryMissingExplicitEnvAPIKeyKeepsProviderUnavailable(t *testing.T) {
	h := newRegistryHarness(t)
	os.Unsetenv("TEST_API_KEY_MISSING_TEST_98765")
	h.writeRawModelsJSON(t, map[string]any{
		"custom-provider": registryProviderWithAPIKey("$TEST_API_KEY_MISSING_TEST_98765"),
	})
	registry := h.createRegistry(t)
	status := registry.GetProviderAuthStatus("custom-provider")
	if status.Configured {
		t.Fatalf("status = %+v, want unconfigured", status)
	}
	for _, model := range registry.GetAvailable() {
		if string(model.Provider) == "custom-provider" {
			t.Fatal("unconfigured provider is available")
		}
	}
}

func TestModelRegistryProviderAuthStatusReportsCommandAPIKeyValuesWithoutExecutingThem(t *testing.T) {
	h := newRegistryHarness(t)
	counterFile := writeCounterFile(t, h, "status-counter")
	command := fmt.Sprintf(`!sh -c 'echo 1 > "%s"; echo key-value'`, counterFile)
	h.writeRawModelsJSON(t, map[string]any{
		"custom-provider": registryProviderWithAPIKey(command),
	})
	registry := h.createRegistry(t)
	status := registry.GetProviderAuthStatus("custom-provider")
	if !status.Configured || status.Source != "models_json_command" {
		t.Fatalf("status = %+v", status)
	}
	content, err := os.ReadFile(counterFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "0" {
		t.Fatalf("command was executed: %q", content)
	}
}

func TestModelRegistryEnvironmentVariablesAreNotCached(t *testing.T) {
	h := newRegistryHarness(t)
	t.Setenv("TEST_API_KEY_CACHE_TEST_98765", "first-value")
	h.writeRawModelsJSON(t, map[string]any{
		"custom-provider": registryProviderWithAPIKey("$TEST_API_KEY_CACHE_TEST_98765"),
	})
	registry := h.createRegistry(t)

	if got := registry.GetApiKeyForProvider(context.Background(), "custom-provider"); got != "first-value" {
		t.Fatalf("key1 = %q", got)
	}
	t.Setenv("TEST_API_KEY_CACHE_TEST_98765", "second-value")
	if got := registry.GetApiKeyForProvider(context.Background(), "custom-provider"); got != "second-value" {
		t.Fatalf("key2 = %q", got)
	}
}

func TestModelRegistryGetAvailableDoesNotExecuteCommandBackedAPIKeyResolution(t *testing.T) {
	h := newRegistryHarness(t)
	counterFile := writeCounterFile(t, h, "counter")
	command := fmt.Sprintf(`!sh -c 'count=$(cat "%s"); echo $((count + 1)) > "%s"; echo "key-value"'`, counterFile, counterFile)
	h.writeRawModelsJSON(t, map[string]any{
		"custom-provider": registryProviderWithAPIKey(command),
	})
	registry := h.createRegistry(t)
	found := false
	for _, model := range registry.GetAvailable() {
		if string(model.Provider) == "custom-provider" {
			found = true
		}
	}
	if !found {
		t.Fatal("custom-provider not available")
	}
	if count := readCounter(t, counterFile); count != 0 {
		t.Fatalf("count = %d, want 0 (no execution)", count)
	}
}

func TestModelRegistryGetAPIKeyAndHeadersResolvesAuthHeaderOnEveryRequest(t *testing.T) {
	h := newRegistryHarness(t)
	tokenFile := filepath.Join(h.tempDir, "token")
	if err := os.WriteFile(tokenFile, []byte("token-1"), 0o600); err != nil {
		t.Fatal(err)
	}
	config := registryProviderWithAPIKey(fmt.Sprintf(`!sh -c 'cat "%s"'`, tokenFile))
	config["authHeader"] = true
	h.writeRawModelsJSON(t, map[string]any{"custom-provider": config})

	registry := h.createRegistry(t)
	model := registry.Find("custom-provider", "test-model")
	if model == nil {
		t.Fatal("model missing")
	}

	authResult := registry.GetApiKeyAndHeaders(context.Background(), *model)
	if !authResult.Ok || authResult.APIKey != "token-1" {
		t.Fatalf("auth1 = %+v", authResult)
	}
	if len(authResult.Headers) != 1 {
		t.Fatalf("headers = %+v", authResult.Headers)
	}
	if header := authResult.Headers["Authorization"]; header == nil || *header != "Bearer token-1" {
		t.Fatalf("headers = %+v", authResult.Headers)
	}

	if err := os.WriteFile(tokenFile, []byte("token-2"), 0o600); err != nil {
		t.Fatal(err)
	}
	authResult = registry.GetApiKeyAndHeaders(context.Background(), *model)
	if !authResult.Ok || authResult.APIKey != "token-2" {
		t.Fatalf("auth2 = %+v", authResult)
	}
	if header := authResult.Headers["Authorization"]; header == nil || *header != "Bearer token-2" {
		t.Fatalf("headers = %+v", authResult.Headers)
	}
}

func TestModelRegistryGetAPIKeyAndHeadersResolvesConfiguredAuthExactlyOnce(t *testing.T) {
	h := newRegistryHarness(t)
	counterFile := writeCounterFile(t, h, "auth-counter")
	command := fmt.Sprintf(`!sh -c 'count=$(cat "%s"); count=$((count + 1)); echo "$count" > "%s"; echo "token-$count"'`, counterFile, counterFile)
	config := registryProviderWithAPIKey(command)
	config["authHeader"] = true
	h.writeRawModelsJSON(t, map[string]any{"custom-provider": config})

	registry := h.createRegistry(t)
	authResult := registry.GetApiKeyAndHeaders(context.Background(), *registry.Find("custom-provider", "test-model"))
	if !authResult.Ok || authResult.APIKey != "token-1" {
		t.Fatalf("auth = %+v", authResult)
	}
	if header := authResult.Headers["Authorization"]; header == nil || *header != "Bearer token-1" {
		t.Fatalf("headers = %+v", authResult.Headers)
	}
	if count := readCounter(t, counterFile); count != 1 {
		t.Fatalf("count = %d, want 1 (resolved exactly once)", count)
	}
}

func TestModelRegistryStoredCredentialsBypassLowerPriorityConfiguredAuthCommands(t *testing.T) {
	h := newRegistryHarness(t)
	counterFile := writeCounterFile(t, h, "fallback-counter")
	command := fmt.Sprintf(`!sh -c 'echo 1 > "%s"; echo fallback-key'`, counterFile)
	h.writeRawModelsJSON(t, map[string]any{
		"custom-provider": registryProviderWithAPIKey(command),
	})
	if _, err := h.authStorage.Modify(context.Background(), "custom-provider", func(current auth.Credential) (auth.Credential, error) {
		return apiKeyCred("stored-key"), nil
	}); err != nil {
		t.Fatal(err)
	}

	registry := h.createRegistry(t)
	authResult := registry.GetApiKeyAndHeaders(context.Background(), *registry.Find("custom-provider", "test-model"))
	if !authResult.Ok || authResult.APIKey != "stored-key" {
		t.Fatalf("auth = %+v", authResult)
	}
	if count := readCounter(t, counterFile); count != 0 {
		t.Fatalf("count = %d, want 0 (command bypassed)", count)
	}
}

func TestModelRegistryGetAPIKeyAndHeadersPreservesTheLegacyMissingKeyAuthHeaderError(t *testing.T) {
	h := newRegistryHarness(t)
	h.writeRawModelsJSON(t, map[string]any{
		"custom-provider": map[string]any{
			"baseUrl":    "https://example.test/v1",
			"api":        "openai-completions",
			"authHeader": true,
			"models":     []any{map[string]any{"id": "test-model"}},
		},
	})
	registry := h.createRegistry(t)
	authResult := registry.GetApiKeyAndHeaders(context.Background(), *registry.Find("custom-provider", "test-model"))
	if authResult.Ok {
		t.Fatalf("auth = %+v, want error", authResult)
	}
	if authResult.Error != `No API key found for "custom-provider"` {
		t.Fatalf("error = %q", authResult.Error)
	}
}

func TestModelRegistryGetAPIKeyAndHeadersReturnsAnErrorForFailedAuthHeaderResolution(t *testing.T) {
	h := newRegistryHarness(t)
	config := registryProviderWithAPIKey("!exit 1")
	config["authHeader"] = true
	h.writeRawModelsJSON(t, map[string]any{"custom-provider": config})

	registry := h.createRegistry(t)
	model := registry.Find("custom-provider", "test-model")
	if model == nil {
		t.Fatal("model missing")
	}
	authResult := registry.GetApiKeyAndHeaders(context.Background(), *model)
	if authResult.Ok {
		t.Fatalf("auth = %+v, want error", authResult)
	}
	if !strings.Contains(authResult.Error, `Failed to resolve API key for provider "custom-provider"`) {
		t.Fatalf("error = %q", authResult.Error)
	}
}

// --- dynamic provider override persistence (pi: describe block) ---

func TestModelRegistryBaseURLOnlyOverrideKeepsBuiltinProviderModelsAfterRefresh(t *testing.T) {
	h := newRegistryHarness(t)
	registry := h.createRegistry(t)

	if err := registry.RegisterProvider("openrouter", &ProviderConfigInput{
		BaseURL: "https://proxy.test/openrouter",
	}); err != nil {
		t.Fatal(err)
	}
	registry.Refresh(context.Background(), nil)

	models := getModelsForProvider(registry, "openrouter")
	if len(models) <= 1 {
		t.Fatalf("models = %d", len(models))
	}
	for _, model := range models {
		if model.BaseURL != "https://proxy.test/openrouter" {
			t.Fatalf("model %s baseUrl = %q", model.ID, model.BaseURL)
		}
	}
}

func TestModelRegistryModelsOnlyOverrideReplacesBuiltinProviderModelsAfterRefresh(t *testing.T) {
	h := newRegistryHarness(t)
	registry := h.createRegistry(t)

	if err := registry.RegisterProvider("openrouter", &ProviderConfigInput{
		BaseURL: "https://custom.test/openrouter",
		APIKey:  "test-key",
		API:     ai.ApiOpenAICompletions,
		Models: []ProviderConfigInputModel{{
			ID: "custom-claude", Name: "custom-claude", Reasoning: false,
			Input: []ai.ModelInput{ai.ModelInputText}, Cost: ai.ModelCost{},
			ContextWindow: 100000, MaxTokens: 8000,
		}},
	}); err != nil {
		t.Fatal(err)
	}
	registry.Refresh(context.Background(), nil)

	models := getModelsForProvider(registry, "openrouter")
	if len(models) != 1 || models[0].ID != "custom-claude" {
		t.Fatalf("models = %+v, want [custom-claude]", models)
	}
	if found := registry.Find("openrouter", "custom-claude"); found == nil || found.BaseURL != "https://custom.test/openrouter" {
		t.Fatalf("found = %+v", found)
	}
}

func TestModelRegistryModelsPlusBaseURLOverrideReplacesBuiltinProviderModelsAfterRefresh(t *testing.T) {
	h := newRegistryHarness(t)
	registry := h.createRegistry(t)

	if err := registry.RegisterProvider("openrouter", &ProviderConfigInput{
		BaseURL: "https://custom.test/openrouter",
		APIKey:  "test-key",
		API:     ai.ApiOpenAICompletions,
		Models: []ProviderConfigInputModel{{
			ID: "custom-claude", Name: "custom-claude", Reasoning: false,
			Input: []ai.ModelInput{ai.ModelInputText}, Cost: ai.ModelCost{},
			ContextWindow: 100000, MaxTokens: 8000,
		}},
	}); err != nil {
		t.Fatal(err)
	}
	// Re-registration merges: the models registration persists, the new
	// baseUrl overlay wins.
	if err := registry.RegisterProvider("openrouter", &ProviderConfigInput{
		BaseURL: "https://proxy.test/openrouter",
	}); err != nil {
		t.Fatal(err)
	}
	registry.Refresh(context.Background(), nil)

	models := getModelsForProvider(registry, "openrouter")
	if len(models) != 1 || models[0].ID != "custom-claude" {
		t.Fatalf("models = %+v, want [custom-claude]", models)
	}
	if found := registry.Find("openrouter", "custom-claude"); found == nil || found.BaseURL != "https://proxy.test/openrouter" {
		t.Fatalf("found = %+v", found)
	}
}

func TestModelRegistryModelsOnlyCustomProviderRegistrationSurvivesRefresh(t *testing.T) {
	h := newRegistryHarness(t)
	registry := h.createRegistry(t)

	if err := registry.RegisterProvider("custom-provider", &ProviderConfigInput{
		BaseURL: "https://custom.test/v1",
		APIKey:  "test-key",
		API:     ai.ApiOpenAICompletions,
		Models: []ProviderConfigInputModel{
			{ID: "custom-a", Name: "custom-a", Reasoning: false, Input: []ai.ModelInput{ai.ModelInputText}, Cost: ai.ModelCost{}, ContextWindow: 100000, MaxTokens: 8000},
			{ID: "custom-b", Name: "custom-b", Reasoning: false, Input: []ai.ModelInput{ai.ModelInputText}, Cost: ai.ModelCost{}, ContextWindow: 100000, MaxTokens: 8000},
		},
	}); err != nil {
		t.Fatal(err)
	}
	registry.Refresh(context.Background(), nil)

	ids := []string{}
	for _, model := range getModelsForProvider(registry, "custom-provider") {
		ids = append(ids, model.ID)
	}
	if !slices.Equal(ids, []string{"custom-a", "custom-b"}) {
		t.Fatalf("models = %v", ids)
	}
}

func TestModelRegistryBaseURLOnlyOverrideKeepsCustomProviderModelsAfterRefresh(t *testing.T) {
	h := newRegistryHarness(t)
	registry := h.createRegistry(t)

	if err := registry.RegisterProvider("custom-provider", &ProviderConfigInput{
		BaseURL: "https://custom.test/v1",
		APIKey:  "test-key",
		API:     ai.ApiOpenAICompletions,
		Models: []ProviderConfigInputModel{
			{ID: "custom-a", Name: "custom-a", Reasoning: false, Input: []ai.ModelInput{ai.ModelInputText}, Cost: ai.ModelCost{}, ContextWindow: 100000, MaxTokens: 8000},
			{ID: "custom-b", Name: "custom-b", Reasoning: false, Input: []ai.ModelInput{ai.ModelInputText}, Cost: ai.ModelCost{}, ContextWindow: 100000, MaxTokens: 8000},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := registry.RegisterProvider("custom-provider", &ProviderConfigInput{
		BaseURL: "https://proxy.test/custom",
	}); err != nil {
		t.Fatal(err)
	}
	registry.Refresh(context.Background(), nil)

	models := getModelsForProvider(registry, "custom-provider")
	ids := []string{}
	for _, model := range models {
		ids = append(ids, model.ID)
		if model.BaseURL != "https://proxy.test/custom" {
			t.Fatalf("model %s baseUrl = %q", model.ID, model.BaseURL)
		}
	}
	if !slices.Equal(ids, []string{"custom-a", "custom-b"}) {
		t.Fatalf("models = %v", ids)
	}
}

func TestModelRegistryHeadersOnlyOverrideKeepsCustomProviderModelsAfterRefresh(t *testing.T) {
	h := newRegistryHarness(t)
	registry := h.createRegistry(t)

	if err := registry.RegisterProvider("custom-provider", &ProviderConfigInput{
		BaseURL: "https://custom.test/v1",
		APIKey:  "test-key",
		API:     ai.ApiOpenAICompletions,
		Models: []ProviderConfigInputModel{
			{ID: "custom-a", Name: "custom-a", Reasoning: false, Input: []ai.ModelInput{ai.ModelInputText}, Cost: ai.ModelCost{}, ContextWindow: 100000, MaxTokens: 8000},
			{ID: "custom-b", Name: "custom-b", Reasoning: false, Input: []ai.ModelInput{ai.ModelInputText}, Cost: ai.ModelCost{}, ContextWindow: 100000, MaxTokens: 8000},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := registry.RegisterProvider("custom-provider", &ProviderConfigInput{
		Headers: map[string]string{"x-proxy": "enabled"},
	}); err != nil {
		t.Fatal(err)
	}
	registry.Refresh(context.Background(), nil)

	models := getModelsForProvider(registry, "custom-provider")
	ids := []string{}
	for _, model := range models {
		ids = append(ids, model.ID)
		if model.BaseURL != "https://custom.test/v1" {
			t.Fatalf("model %s baseUrl = %q", model.ID, model.BaseURL)
		}
	}
	if !slices.Equal(ids, []string{"custom-a", "custom-b"}) {
		t.Fatalf("models = %v", ids)
	}
	authResult := registry.GetApiKeyAndHeaders(context.Background(), models[0])
	if !authResult.Ok {
		t.Fatalf("auth = %+v", authResult)
	}
	if header := authResult.Headers["x-proxy"]; header == nil || *header != "enabled" {
		t.Fatalf("headers = %+v", authResult.Headers)
	}
}
