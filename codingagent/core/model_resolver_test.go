// Ported from pi's test/model-resolver.test.ts. pi casts plain objects to
// ModelRuntime with `as unknown as`; pig stubs the ModelResolverRuntime
// interface instead. The warn sink is swapped via the modelResolverWarn var
// (pi: vi.spyOn(console, "warn")).
package core

import (
	"context"
	"strings"
	"testing"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/auth"
	"github.com/ZoroSpace/pig/ai/providers"
	"github.com/ZoroSpace/pig/ai/types"
)

func resolverModel(id, name, provider, baseURL string, reasoning bool) ai.Model {
	return ai.Model{
		ID:            id,
		Name:          name,
		API:           ai.ApiAnthropicMessages, // Using same type for simplicity (pi mock comment)
		Provider:      ai.ProviderID(provider),
		BaseURL:       baseURL,
		Reasoning:     reasoning,
		Input:         []ai.ModelInput{ai.ModelInputText, ai.ModelInputImage},
		Cost:          ai.ModelCost{ModelCostRates: ai.ModelCostRates{Input: 3, Output: 15, CacheRead: 0.3, CacheWrite: 3.75}},
		ContextWindow: 200000,
		MaxTokens:     8192,
	}
}

// mockModels mirrors pi's test fixtures.
var resolverMockModels = []ai.Model{
	resolverModel("claude-sonnet-4-5", "Claude Sonnet 4.5", "anthropic", "https://api.anthropic.com", true),
	resolverModel("gpt-4o", "GPT-4o", "openai", "https://api.openai.com", false),
}

var resolverMockOpenRouterModels = []ai.Model{
	resolverModel("qwen/qwen3-coder:exacto", "Qwen3 Coder Exacto", "openrouter", "https://openrouter.ai/api/v1", true),
	resolverModel("openai/gpt-4o:extended", "GPT-4o Extended", "openrouter", "https://openrouter.ai/api/v1", false),
}

var resolverAllModels = append(append([]ai.Model(nil), resolverMockModels...), resolverMockOpenRouterModels...)

type stubResolverRuntime struct {
	models    []ai.Model
	available []ai.Model
	authByP   map[string]bool
}

func (s *stubResolverRuntime) GetModels() []ai.Model { return s.models }

func (s *stubResolverRuntime) HasConfiguredAuth(providerID string) bool { return s.authByP[providerID] }

func (s *stubResolverRuntime) GetAvailable(ctx context.Context) ([]ai.Model, error) {
	return s.available, nil
}

func (s *stubResolverRuntime) GetAvailableSnapshot() []ai.Model { return s.available }

func (s *stubResolverRuntime) GetModel(providerID, modelID string) (ai.Model, bool) {
	for _, model := range s.models {
		if string(model.Provider) == providerID && model.ID == modelID {
			return model, true
		}
	}
	return ai.Model{}, false
}

func TestParseModelPatternSimplePatternsWithoutColons(t *testing.T) {
	t.Run("exact match returns model with undefined thinking level", func(t *testing.T) {
		result := ParseModelPattern("claude-sonnet-4-5", resolverAllModels, nil)
		if result.Model == nil || result.Model.ID != "claude-sonnet-4-5" {
			t.Fatalf("model = %v", result.Model)
		}
		if result.ThinkingLevel != "" || result.Warning != "" {
			t.Fatalf("thinkingLevel = %q, warning = %q", result.ThinkingLevel, result.Warning)
		}
	})

	t.Run("partial match returns best model with undefined thinking level", func(t *testing.T) {
		result := ParseModelPattern("sonnet", resolverAllModels, nil)
		if result.Model == nil || result.Model.ID != "claude-sonnet-4-5" {
			t.Fatalf("model = %v", result.Model)
		}
		if result.ThinkingLevel != "" || result.Warning != "" {
			t.Fatalf("thinkingLevel = %q, warning = %q", result.ThinkingLevel, result.Warning)
		}
	})

	t.Run("no match returns undefined model and thinking level", func(t *testing.T) {
		result := ParseModelPattern("nonexistent", resolverAllModels, nil)
		if result.Model != nil || result.ThinkingLevel != "" || result.Warning != "" {
			t.Fatalf("result = %+v", result)
		}
	})
}

func TestParseModelPatternsWithValidThinkingLevels(t *testing.T) {
	t.Run("sonnet:high returns sonnet with high thinking level", func(t *testing.T) {
		result := ParseModelPattern("sonnet:high", resolverAllModels, nil)
		if result.Model == nil || result.Model.ID != "claude-sonnet-4-5" || result.ThinkingLevel != "high" || result.Warning != "" {
			t.Fatalf("result = %+v", result)
		}
	})

	t.Run("gpt-4o:medium returns gpt-4o with medium thinking level", func(t *testing.T) {
		result := ParseModelPattern("gpt-4o:medium", resolverAllModels, nil)
		if result.Model == nil || result.Model.ID != "gpt-4o" || result.ThinkingLevel != "medium" || result.Warning != "" {
			t.Fatalf("result = %+v", result)
		}
	})

	t.Run("all valid thinking levels work", func(t *testing.T) {
		for _, level := range []string{"off", "minimal", "low", "medium", "high", "xhigh", "max"} {
			result := ParseModelPattern("sonnet:"+level, resolverAllModels, nil)
			if result.Model == nil || result.Model.ID != "claude-sonnet-4-5" || string(result.ThinkingLevel) != level || result.Warning != "" {
				t.Fatalf("level %s: result = %+v", level, result)
			}
		}
	})
}

func TestParseModelPatternsWithInvalidThinkingLevels(t *testing.T) {
	t.Run("sonnet:random returns sonnet with undefined thinking level and warning", func(t *testing.T) {
		result := ParseModelPattern("sonnet:random", resolverAllModels, nil)
		if result.Model == nil || result.Model.ID != "claude-sonnet-4-5" || result.ThinkingLevel != "" {
			t.Fatalf("result = %+v", result)
		}
		if !strings.Contains(result.Warning, "Invalid thinking level") || !strings.Contains(result.Warning, "random") {
			t.Fatalf("warning = %q", result.Warning)
		}
	})

	t.Run("gpt-4o:invalid returns gpt-4o with undefined thinking level and warning", func(t *testing.T) {
		result := ParseModelPattern("gpt-4o:invalid", resolverAllModels, nil)
		if result.Model == nil || result.Model.ID != "gpt-4o" || result.ThinkingLevel != "" {
			t.Fatalf("result = %+v", result)
		}
		if !strings.Contains(result.Warning, "Invalid thinking level") {
			t.Fatalf("warning = %q", result.Warning)
		}
	})
}

func TestParseModelPatternsOpenRouterModelsWithColonsInIDs(t *testing.T) {
	t.Run("qwen3-coder:exacto matches the model with undefined thinking level", func(t *testing.T) {
		result := ParseModelPattern("qwen/qwen3-coder:exacto", resolverAllModels, nil)
		if result.Model == nil || result.Model.ID != "qwen/qwen3-coder:exacto" || result.ThinkingLevel != "" || result.Warning != "" {
			t.Fatalf("result = %+v", result)
		}
	})

	t.Run("openrouter/qwen/qwen3-coder:exacto matches with provider prefix", func(t *testing.T) {
		result := ParseModelPattern("openrouter/qwen/qwen3-coder:exacto", resolverAllModels, nil)
		if result.Model == nil || result.Model.ID != "qwen/qwen3-coder:exacto" || string(result.Model.Provider) != "openrouter" {
			t.Fatalf("result = %+v", result)
		}
		if result.ThinkingLevel != "" || result.Warning != "" {
			t.Fatalf("thinkingLevel = %q, warning = %q", result.ThinkingLevel, result.Warning)
		}
	})

	t.Run("qwen3-coder:exacto:high matches model with high thinking level", func(t *testing.T) {
		result := ParseModelPattern("qwen/qwen3-coder:exacto:high", resolverAllModels, nil)
		if result.Model == nil || result.Model.ID != "qwen/qwen3-coder:exacto" || result.ThinkingLevel != "high" || result.Warning != "" {
			t.Fatalf("result = %+v", result)
		}
	})

	t.Run("openrouter/qwen/qwen3-coder:exacto:high matches with provider and thinking level", func(t *testing.T) {
		result := ParseModelPattern("openrouter/qwen/qwen3-coder:exacto:high", resolverAllModels, nil)
		if result.Model == nil || result.Model.ID != "qwen/qwen3-coder:exacto" || string(result.Model.Provider) != "openrouter" || result.ThinkingLevel != "high" || result.Warning != "" {
			t.Fatalf("result = %+v", result)
		}
	})

	t.Run("gpt-4o:extended matches the extended model with undefined thinking level", func(t *testing.T) {
		result := ParseModelPattern("openai/gpt-4o:extended", resolverAllModels, nil)
		if result.Model == nil || result.Model.ID != "openai/gpt-4o:extended" || result.ThinkingLevel != "" || result.Warning != "" {
			t.Fatalf("result = %+v", result)
		}
	})
}

func TestParseModelPatternsInvalidThinkingLevelsWithOpenRouterModels(t *testing.T) {
	t.Run("qwen3-coder:exacto:random returns model with undefined thinking level and warning", func(t *testing.T) {
		result := ParseModelPattern("qwen/qwen3-coder:exacto:random", resolverAllModels, nil)
		if result.Model == nil || result.Model.ID != "qwen/qwen3-coder:exacto" || result.ThinkingLevel != "" {
			t.Fatalf("result = %+v", result)
		}
		if !strings.Contains(result.Warning, "Invalid thinking level") || !strings.Contains(result.Warning, "random") {
			t.Fatalf("warning = %q", result.Warning)
		}
	})

	t.Run("qwen3-coder:exacto:high:random returns model with undefined thinking level and warning", func(t *testing.T) {
		result := ParseModelPattern("qwen/qwen3-coder:exacto:high:random", resolverAllModels, nil)
		if result.Model == nil || result.Model.ID != "qwen/qwen3-coder:exacto" || result.ThinkingLevel != "" {
			t.Fatalf("result = %+v", result)
		}
		if !strings.Contains(result.Warning, "Invalid thinking level") || !strings.Contains(result.Warning, "random") {
			t.Fatalf("warning = %q", result.Warning)
		}
	})
}

func TestParseModelPatternsEdgeCases(t *testing.T) {
	t.Run("empty pattern matches via partial matching", func(t *testing.T) {
		// Empty string is included in all model ids, so partial matching finds a match.
		result := ParseModelPattern("", resolverAllModels, nil)
		if result.Model == nil {
			t.Fatal("model = nil, want a match")
		}
		if result.ThinkingLevel != "" {
			t.Fatalf("thinkingLevel = %q", result.ThinkingLevel)
		}
	})

	t.Run("pattern ending with colon treats empty suffix as invalid", func(t *testing.T) {
		result := ParseModelPattern("sonnet:", resolverAllModels, nil)
		// Empty string after colon is not a valid thinking level, so it tries
		// "sonnet:" which won't match, then tries "sonnet".
		if result.Model == nil || result.Model.ID != "claude-sonnet-4-5" {
			t.Fatalf("result = %+v", result)
		}
		if !strings.Contains(result.Warning, "Invalid thinking level") {
			t.Fatalf("warning = %q", result.Warning)
		}
	})
}

func TestResolveModelScopeWithDiagnosticsNoConsoleWarnings(t *testing.T) {
	var warned []string
	previousWarn := modelResolverWarn
	modelResolverWarn = func(message string) { warned = append(warned, message) }
	defer func() { modelResolverWarn = previousWarn }()

	registry := &stubResolverRuntime{available: resolverAllModels}

	result, err := ResolveModelScopeWithDiagnostics(context.Background(),
		[]string{"sonnet:high", "gpt-4o:invalid", "missing"}, registry)
	if err != nil {
		t.Fatal(err)
	}

	if len(result.ScopedModels) != 2 ||
		result.ScopedModels[0].Model.ID != "claude-sonnet-4-5" || result.ScopedModels[1].Model.ID != "gpt-4o" {
		t.Fatalf("scopedModels = %+v", result.ScopedModels)
	}
	if result.ScopedModels[0].ThinkingLevel != "high" || result.ScopedModels[1].ThinkingLevel != "" {
		t.Fatalf("thinking levels = %q / %q", result.ScopedModels[0].ThinkingLevel, result.ScopedModels[1].ThinkingLevel)
	}
	if len(result.Diagnostics) != 2 ||
		result.Diagnostics[0].Type != "warning" ||
		result.Diagnostics[0].Message != `Invalid thinking level "invalid" in pattern "gpt-4o:invalid". Using default instead.` ||
		result.Diagnostics[0].Code != "invalid-thinking-level" ||
		result.Diagnostics[0].Pattern != "gpt-4o:invalid" ||
		result.Diagnostics[1].Type != "warning" ||
		result.Diagnostics[1].Message != `No models match pattern "missing"` ||
		result.Diagnostics[1].Code != "no-match" ||
		result.Diagnostics[1].Pattern != "missing" {
		t.Fatalf("diagnostics = %+v", result.Diagnostics)
	}
	if len(warned) != 0 {
		t.Fatalf("warnings written: %v", warned)
	}
}

func TestResolveModelScopePreservesCLIWarningOutput(t *testing.T) {
	var warned []string
	previousWarn := modelResolverWarn
	modelResolverWarn = func(message string) { warned = append(warned, message) }
	defer func() { modelResolverWarn = previousWarn }()

	registry := &stubResolverRuntime{available: resolverAllModels}

	scopedModels, err := ResolveModelScope(context.Background(), []string{"missing"}, registry)
	if err != nil {
		t.Fatal(err)
	}

	if len(scopedModels) != 0 {
		t.Fatalf("scopedModels = %+v, want empty", scopedModels)
	}
	if len(warned) != 1 || !strings.Contains(warned[0], `Warning: No models match pattern "missing"`) {
		t.Fatalf("warnings = %v", warned)
	}
}

func TestResolveModelScopeResolvesBracketedModelIDsAsExactReferencesBeforeGlobMatching(t *testing.T) {
	bracketedModel := resolverModel("bracketed-model[1m]", "Bracketed Model", "custom", "https://example.invalid", true)
	models := append(append([]ai.Model(nil), resolverAllModels...), bracketedModel)
	registry := &stubResolverRuntime{available: models}

	result, err := ResolveModelScopeWithDiagnostics(context.Background(), []string{"custom/bracketed-model[1m]"}, registry)
	if err != nil {
		t.Fatal(err)
	}

	if len(result.ScopedModels) != 1 || result.ScopedModels[0].Model.ID != "bracketed-model[1m]" {
		t.Fatalf("scopedModels = %+v", result.ScopedModels)
	}
	if len(result.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %+v", result.Diagnostics)
	}
}

func TestResolveModelScopeResolvesBracketedModelIDsWithThinkingLevelsAsExactReferencesBeforeGlobMatching(t *testing.T) {
	bracketedModel := resolverModel("bracketed-model[1m]", "Bracketed Model", "custom", "https://example.invalid", true)
	models := append(append([]ai.Model(nil), resolverAllModels...), bracketedModel)
	registry := &stubResolverRuntime{available: models}

	result, err := ResolveModelScopeWithDiagnostics(context.Background(), []string{"custom/bracketed-model[1m]:high"}, registry)
	if err != nil {
		t.Fatal(err)
	}

	if len(result.ScopedModels) != 1 || result.ScopedModels[0].Model.ID != "bracketed-model[1m]" {
		t.Fatalf("scopedModels = %+v", result.ScopedModels)
	}
	if result.ScopedModels[0].ThinkingLevel != "high" {
		t.Fatalf("thinkingLevel = %q", result.ScopedModels[0].ThinkingLevel)
	}
	if len(result.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %+v", result.Diagnostics)
	}
}

func TestResolveCliModel(t *testing.T) {
	t.Run("resolves --model provider/id without --provider", func(t *testing.T) {
		registry := &stubResolverRuntime{models: resolverAllModels}
		result := ResolveCliModel(ResolveCliModelOptions{CliModel: "openai/gpt-4o", ModelRuntime: registry})
		if result.Error != "" {
			t.Fatalf("error = %q", result.Error)
		}
		if string(result.Model.Provider) != "openai" || result.Model.ID != "gpt-4o" {
			t.Fatalf("model = %+v", result.Model)
		}
	})

	t.Run("resolves fuzzy patterns within an explicit provider", func(t *testing.T) {
		registry := &stubResolverRuntime{models: resolverAllModels}
		result := ResolveCliModel(ResolveCliModelOptions{CliProvider: "openai", CliModel: "4o", ModelRuntime: registry})
		if result.Error != "" {
			t.Fatalf("error = %q", result.Error)
		}
		if string(result.Model.Provider) != "openai" || result.Model.ID != "gpt-4o" {
			t.Fatalf("model = %+v", result.Model)
		}
	})

	t.Run("supports --model <pattern>:<thinking> (without explicit --thinking)", func(t *testing.T) {
		registry := &stubResolverRuntime{models: resolverAllModels}
		result := ResolveCliModel(ResolveCliModelOptions{CliModel: "sonnet:high", ModelRuntime: registry})
		if result.Error != "" {
			t.Fatalf("error = %q", result.Error)
		}
		if result.Model == nil || result.Model.ID != "claude-sonnet-4-5" || result.ThinkingLevel != "high" {
			t.Fatalf("result = %+v", result)
		}
	})

	t.Run("prefers exact model id match over provider inference (OpenRouter-style ids)", func(t *testing.T) {
		registry := &stubResolverRuntime{models: resolverAllModels}
		result := ResolveCliModel(ResolveCliModelOptions{CliModel: "openai/gpt-4o:extended", ModelRuntime: registry})
		if result.Error != "" {
			t.Fatalf("error = %q", result.Error)
		}
		if string(result.Model.Provider) != "openrouter" || result.Model.ID != "openai/gpt-4o:extended" {
			t.Fatalf("model = %+v", result.Model)
		}
	})

	t.Run("does not strip invalid :suffix as thinking level in --model (treat as raw id)", func(t *testing.T) {
		registry := &stubResolverRuntime{models: resolverAllModels}
		result := ResolveCliModel(ResolveCliModelOptions{CliProvider: "openai", CliModel: "gpt-4o:extended", ModelRuntime: registry})
		if result.Error != "" {
			t.Fatalf("error = %q", result.Error)
		}
		if string(result.Model.Provider) != "openai" || result.Model.ID != "gpt-4o:extended" {
			t.Fatalf("model = %+v", result.Model)
		}
	})

	t.Run("allows custom model ids for explicit providers without double prefixing", func(t *testing.T) {
		registry := &stubResolverRuntime{models: resolverAllModels}
		result := ResolveCliModel(ResolveCliModelOptions{
			CliProvider: "openrouter", CliModel: "openrouter/openai/ghost-model", ModelRuntime: registry,
		})
		if result.Error != "" {
			t.Fatalf("error = %q", result.Error)
		}
		if string(result.Model.Provider) != "openrouter" || result.Model.ID != "openai/ghost-model" {
			t.Fatalf("model = %+v", result.Model)
		}
	})

	t.Run("returns a clear error when there are no models", func(t *testing.T) {
		registry := &stubResolverRuntime{models: []ai.Model{}}
		result := ResolveCliModel(ResolveCliModelOptions{CliProvider: "openai", CliModel: "gpt-4o", ModelRuntime: registry})
		if result.Model != nil {
			t.Fatalf("model = %+v", result.Model)
		}
		if !strings.Contains(result.Error, "No models available") {
			t.Fatalf("error = %q", result.Error)
		}
	})

	t.Run("prefers the sole authenticated provider for an ambiguous bare exact model id", func(t *testing.T) {
		azureModel := resolverModel("gpt-5.6-sol", "GPT 5.6 Sol", "azure-openai-responses", "https://api.openai.com", false)
		codexModel := resolverModel("gpt-5.6-sol", "GPT 5.6 Sol", "openai-codex", "https://api.openai.com", false)
		registry := &stubResolverRuntime{
			models:  []ai.Model{azureModel, codexModel},
			authByP: map[string]bool{"openai-codex": true},
		}
		result := ResolveCliModel(ResolveCliModelOptions{CliModel: "gpt-5.6-sol", ModelRuntime: registry})
		if result.Error != "" {
			t.Fatalf("error = %q", result.Error)
		}
		if string(result.Model.Provider) != "openai-codex" || result.Model.ID != "gpt-5.6-sol" {
			t.Fatalf("model = %+v", result.Model)
		}
	})

	t.Run("requires an explicit provider for an ambiguous bare exact model id without a unique authenticated provider", func(t *testing.T) {
		azureModel := resolverModel("gpt-5.6-sol", "GPT 5.6 Sol", "azure-openai-responses", "https://api.openai.com", false)
		codexModel := resolverModel("gpt-5.6-sol", "GPT 5.6 Sol", "openai-codex", "https://api.openai.com", false)
		registry := &stubResolverRuntime{models: []ai.Model{azureModel, codexModel}, authByP: map[string]bool{}}
		result := ResolveCliModel(ResolveCliModelOptions{CliModel: "gpt-5.6-sol", ModelRuntime: registry})
		if result.Model != nil {
			t.Fatalf("model = %+v", result.Model)
		}
		for _, want := range []string{
			`Model "gpt-5.6-sol" is ambiguous across providers`,
			"azure-openai-responses/gpt-5.6-sol",
			"openai-codex/gpt-5.6-sol",
			"Use --provider or provider/model",
		} {
			if !strings.Contains(result.Error, want) {
				t.Fatalf("error %q missing %q", result.Error, want)
			}
		}
	})

	t.Run("prefers provider/model split over gateway model with matching id", func(t *testing.T) {
		zaiModel := resolverModel("glm-5", "GLM-5", "zai", "https://open.bigmodel.cn/api/paas/v4", true)
		gatewayModel := resolverModel("zai/glm-5", "GLM-5", "vercel-ai-gateway", "https://ai-gateway.vercel.sh", true)
		registry := &stubResolverRuntime{
			models:  append(append([]ai.Model(nil), resolverAllModels...), zaiModel, gatewayModel),
			authByP: map[string]bool{"zai": true, "vercel-ai-gateway": true, "anthropic": true, "openai": true, "openrouter": true},
		}
		result := ResolveCliModel(ResolveCliModelOptions{CliModel: "zai/glm-5", ModelRuntime: registry})
		if result.Error != "" {
			t.Fatalf("error = %q", result.Error)
		}
		if string(result.Model.Provider) != "zai" || result.Model.ID != "glm-5" {
			t.Fatalf("model = %+v", result.Model)
		}
	})

	t.Run("prefers an authenticated exact raw model id over an unauthenticated inferred provider", func(t *testing.T) {
		commandcodeModel := resolverModel("xiaomi/mimo-v2.5-pro", "Xiaomi MiMo via Commandcode", "commandcode", "https://example.invalid", false)
		xiaomiModel := resolverModel("mimo-v2.5-pro", "Xiaomi MiMo", "xiaomi", "https://api.xiaomimimo.com", false)
		registry := &stubResolverRuntime{
			models:  append(append([]ai.Model(nil), resolverAllModels...), commandcodeModel, xiaomiModel),
			authByP: map[string]bool{"commandcode": true},
		}
		result := ResolveCliModel(ResolveCliModelOptions{CliModel: "xiaomi/mimo-v2.5-pro", ModelRuntime: registry})
		if result.Error != "" {
			t.Fatalf("error = %q", result.Error)
		}
		if string(result.Model.Provider) != "commandcode" || result.Model.ID != "xiaomi/mimo-v2.5-pro" {
			t.Fatalf("model = %+v", result.Model)
		}
	})

	t.Run("resolves provider-prefixed fuzzy patterns (openrouter/qwen -> openrouter model)", func(t *testing.T) {
		registry := &stubResolverRuntime{models: resolverAllModels}
		result := ResolveCliModel(ResolveCliModelOptions{CliModel: "openrouter/qwen", ModelRuntime: registry})
		if result.Error != "" {
			t.Fatalf("error = %q", result.Error)
		}
		if string(result.Model.Provider) != "openrouter" || result.Model.ID != "qwen/qwen3-coder:exacto" {
			t.Fatalf("model = %+v", result.Model)
		}
	})

	t.Run("strips :thinking suffix from custom model id in fallback path", func(t *testing.T) {
		neuralwattModel := resolverModel("some-base-model", "Some Base Model", "neuralwatt", "https://api.neuralwatt.com", false)
		registry := &stubResolverRuntime{
			models: append(append([]ai.Model(nil), resolverAllModels...), neuralwattModel),
		}
		result := ResolveCliModel(ResolveCliModelOptions{
			CliModel: "neuralwatt/zai-org/GLM-5.1-FP8:high", ModelRuntime: registry,
		})
		if result.Error != "" {
			t.Fatalf("error = %q", result.Error)
		}
		if string(result.Model.Provider) != "neuralwatt" {
			t.Fatalf("provider = %q", result.Model.Provider)
		}
		// The :high suffix must NOT leak into the model id sent to the API.
		if result.Model.ID != "zai-org/GLM-5.1-FP8" || !result.Model.Reasoning || result.ThinkingLevel != "high" {
			t.Fatalf("result = %+v", result)
		}
	})

	t.Run("custom model without thinking suffix works normally in fallback path", func(t *testing.T) {
		neuralwattModel := resolverModel("some-base-model", "Some Base Model", "neuralwatt", "https://api.neuralwatt.com", false)
		registry := &stubResolverRuntime{
			models: append(append([]ai.Model(nil), resolverAllModels...), neuralwattModel),
		}
		result := ResolveCliModel(ResolveCliModelOptions{
			CliModel: "neuralwatt/zai-org/GLM-5.1-FP8", ModelRuntime: registry,
		})
		if result.Error != "" {
			t.Fatalf("error = %q", result.Error)
		}
		if string(result.Model.Provider) != "neuralwatt" || result.Model.ID != "zai-org/GLM-5.1-FP8" || result.ThinkingLevel != "" {
			t.Fatalf("result = %+v", result)
		}
	})

	t.Run("all valid thinking levels work in fallback path", func(t *testing.T) {
		neuralwattModel := resolverModel("some-base-model", "Some Base Model", "neuralwatt", "https://api.neuralwatt.com", false)
		registry := &stubResolverRuntime{
			models: append(append([]ai.Model(nil), resolverAllModels...), neuralwattModel),
		}
		for _, level := range []string{"off", "minimal", "low", "medium", "high", "xhigh", "max"} {
			result := ResolveCliModel(ResolveCliModelOptions{
				CliModel: "neuralwatt/zai-org/GLM-5.1-FP8:" + level, ModelRuntime: registry,
			})
			if result.Error != "" {
				t.Fatalf("level %s: error = %q", level, result.Error)
			}
			if result.Model == nil || result.Model.ID != "zai-org/GLM-5.1-FP8" || string(result.ThinkingLevel) != level {
				t.Fatalf("level %s: result = %+v", level, result)
			}
		}
	})

	t.Run("invalid thinking suffix on custom model is treated as part of model id", func(t *testing.T) {
		neuralwattModel := resolverModel("some-base-model", "Some Base Model", "neuralwatt", "https://api.neuralwatt.com", false)
		registry := &stubResolverRuntime{
			models: append(append([]ai.Model(nil), resolverAllModels...), neuralwattModel),
		}
		result := ResolveCliModel(ResolveCliModelOptions{
			CliModel: "neuralwatt/zai-org/GLM-5.1-FP8:banana", ModelRuntime: registry,
		})
		if result.Error != "" {
			t.Fatalf("error = %q", result.Error)
		}
		if string(result.Model.Provider) != "neuralwatt" {
			t.Fatalf("provider = %q", result.Model.Provider)
		}
		// Invalid suffix stays in the id (it's not a thinking level).
		if result.Model.ID != "zai-org/GLM-5.1-FP8:banana" || result.ThinkingLevel != "" {
			t.Fatalf("result = %+v", result)
		}
	})

	t.Run("explicit --provider with custom model:thinking strips suffix correctly", func(t *testing.T) {
		neuralwattModel := resolverModel("some-base-model", "Some Base Model", "neuralwatt", "https://api.neuralwatt.com", false)
		registry := &stubResolverRuntime{
			models: append(append([]ai.Model(nil), resolverAllModels...), neuralwattModel),
		}
		result := ResolveCliModel(ResolveCliModelOptions{
			CliProvider: "neuralwatt", CliModel: "zai-org/GLM-5.1-FP8:high", ModelRuntime: registry,
		})
		if result.Error != "" {
			t.Fatalf("error = %q", result.Error)
		}
		if string(result.Model.Provider) != "neuralwatt" || result.Model.ID != "zai-org/GLM-5.1-FP8" || result.ThinkingLevel != "high" {
			t.Fatalf("result = %+v", result)
		}
	})

	t.Run("with explicit --thinking, :suffix is kept as part of model id", func(t *testing.T) {
		neuralwattModel := resolverModel("some-base-model", "Some Base Model", "neuralwatt", "https://api.neuralwatt.com", false)
		registry := &stubResolverRuntime{
			models: append(append([]ai.Model(nil), resolverAllModels...), neuralwattModel),
		}
		result := ResolveCliModel(ResolveCliModelOptions{
			CliModel: "neuralwatt/zai-org/GLM-5.1-FP8:high", CliThinking: "medium", ModelRuntime: registry,
		})
		if result.Error != "" {
			t.Fatalf("error = %q", result.Error)
		}
		if string(result.Model.Provider) != "neuralwatt" {
			t.Fatalf("provider = %q", result.Model.Provider)
		}
		// :high is kept as part of the model id since --thinking was explicit.
		if result.Model.ID != "zai-org/GLM-5.1-FP8:high" || result.ThinkingLevel != "" {
			t.Fatalf("result = %+v", result)
		}
	})
}

func TestDefaultModelSelection(t *testing.T) {
	t.Run("openai defaults track current models", func(t *testing.T) {
		if DefaultModelPerProvider["openai"] != "gpt-5.5" || DefaultModelPerProvider["openai-codex"] != "gpt-5.5" {
			t.Fatalf("openai defaults drifted: %v %v", DefaultModelPerProvider["openai"], DefaultModelPerProvider["openai-codex"])
		}
	})

	t.Run("zai, minimax, cerebras, and ant-ling defaults track current models", func(t *testing.T) {
		want := map[string]string{
			"zai": "glm-5.3", "zai-coding-cn": "glm-5.3", "minimax": "MiniMax-M2.7",
			"minimax-cn": "MiniMax-M2.7", "cerebras": "gpt-oss-120b", "ant-ling": "Ring-2.6-1T",
		}
		for provider, defaultID := range want {
			if DefaultModelPerProvider[provider] != defaultID {
				t.Fatalf("%s default = %q, want %q", provider, DefaultModelPerProvider[provider], defaultID)
			}
		}
	})

	t.Run("built-in defaults exist in generated provider catalogs", func(t *testing.T) {
		// pig's built-in provider rollout covers the completions-family subset
		// (Phase 2 decision), so this checks the providers pig ships.
		for _, provider := range providers.GetBuiltinProviders() {
			defaultID, ok := DefaultModelPerProvider[string(provider)]
			if !ok {
				t.Fatalf("provider %q has no default in defaultModelPerProvider", provider)
			}
			found := false
			for _, model := range providers.GetBuiltinModels(provider) {
				if model.ID == defaultID {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("%s default %s should exist in its generated catalog", provider, defaultID)
			}
		}
	})

	t.Run("ai-gateway default tracks current model", func(t *testing.T) {
		if DefaultModelPerProvider["vercel-ai-gateway"] != "zai/glm-5.1" {
			t.Fatalf("vercel-ai-gateway default = %q", DefaultModelPerProvider["vercel-ai-gateway"])
		}
	})

	t.Run("xai default tracks current model", func(t *testing.T) {
		if DefaultModelPerProvider["xai"] != "grok-4.7" {
			t.Fatalf("xai default = %q", DefaultModelPerProvider["xai"])
		}
	})

	t.Run("qwen token plan individual default tracks current model", func(t *testing.T) {
		if DefaultModelPerProvider["qwen-token-plan-individual"] != "qwen3.8-max" {
			t.Fatalf("qwen-token-plan-individual default = %q", DefaultModelPerProvider["qwen-token-plan-individual"])
		}
	})

	t.Run("findInitialModel accepts explicit provider custom model ids", func(t *testing.T) {
		registry := &stubResolverRuntime{models: resolverAllModels}
		result := FindInitialModel(FindInitialModelOptions{
			CliProvider:  "openrouter",
			CliModel:     "openrouter/openai/ghost-model",
			ModelRuntime: registry,
		})
		if result.Model == nil || string(result.Model.Provider) != "openrouter" || result.Model.ID != "openai/ghost-model" {
			t.Fatalf("result = %+v", result)
		}
	})

	t.Run("findInitialModel selects ai-gateway default when available", func(t *testing.T) {
		aiGatewayModel := resolverModel("anthropic/claude-opus-4-6", "Claude Opus 4.6", "vercel-ai-gateway", "https://ai-gateway.vercel.sh", true)
		registry := &stubResolverRuntime{available: []ai.Model{aiGatewayModel}}
		result := FindInitialModel(FindInitialModelOptions{ModelRuntime: registry})
		if result.Model == nil || string(result.Model.Provider) != "vercel-ai-gateway" || result.Model.ID != "anthropic/claude-opus-4-6" {
			t.Fatalf("result = %+v", result)
		}
	})

	t.Run("findInitialModel ignores an unauthenticated saved default", func(t *testing.T) {
		savedDeepSeekModel := resolverModel("deepseek-v4-flash", "DeepSeek V4 Flash", "deepseek", "https://api.deepseek.com", true)
		localDeepSeekModel := resolverModel("deepseek-v4-flash", "DeepSeek V4 Flash", "spark-two", "http://spark-two:8000/v1", true)
		registry := &stubResolverRuntime{
			models:    []ai.Model{savedDeepSeekModel},
			available: []ai.Model{localDeepSeekModel},
			authByP:   map[string]bool{"spark-two": true},
		}
		result := FindInitialModel(FindInitialModelOptions{
			DefaultProvider: "deepseek",
			DefaultModelID:  "deepseek-v4-flash",
			ModelRuntime:    registry,
		})
		if result.Model == nil || string(result.Model.Provider) != "spark-two" || result.Model.ID != "deepseek-v4-flash" {
			t.Fatalf("result = %+v", result)
		}
	})
}

// agent.ThinkingLevel appears in the resolver signatures; keep the import
// honest for the fixture typed constants.
var _ agent.ThinkingLevel = agent.ThinkingLevel("medium")

func TestPersistedDefaultModelScoping(t *testing.T) {
	// pi (v0.85.1): persisting a default model adds it to a non-empty scope
	// and appends the reference to the enabled-models patterns.
	makeModels := func() []*ai.Model {
		m1 := &ai.Model{ID: "model-a", Name: "Model A", API: ai.ApiOpenAICompletions, Provider: "test-provider", Input: []types.ModelInput{types.ModelInputText}, ContextWindow: 1000, MaxTokens: 100, Cost: types.ModelCost{}}
		m2 := &ai.Model{ID: "model-b", Name: "Model B", API: ai.ApiOpenAICompletions, Provider: "test-provider", Input: []types.ModelInput{types.ModelInputText}, ContextWindow: 1000, MaxTokens: 100, Cost: types.ModelCost{}}
		return []*types.Model{m1, m2}
	}

	createSession := func(t *testing.T, options *createAgentTestSessionOptions, scoped bool, persistedScope []string) (*AgentSession, *SettingsManager) {
		t.Helper()
		models := makeModels()
		modelA, modelB := models[0], models[1]
		settingsManager := InMemorySettingsManager(nil, nil)
		if persistedScope != nil {
			settingsManager.SetEnabledModels(persistedScope)
		}
		authStorage := NewInMemoryAuthStorage(authStorageData{})
		if _, err := authStorage.Modify(context.Background(), "test-provider", func(credential auth.Credential) (auth.Credential, error) {
			return &auth.ApiKeyCredential{Key: "test-key"}, nil
		}); err != nil {
			t.Fatal(err)
		}
		registry, modelRuntime := createTestModelRegistry(t, authStorage, "")
		if err := modelRuntime.RegisterProvider("test-provider", &ProviderConfigInput{
			API: ai.ApiOpenAICompletions,
			Models: []ProviderConfigInputModel{
				{ID: modelA.ID, Name: modelA.Name, API: ai.ApiOpenAICompletions, BaseURL: "http://localhost:0", Input: []ai.ModelInput{ai.ModelInputText}, ContextWindow: 1000, MaxTokens: 100},
				{ID: modelB.ID, Name: modelB.Name, API: ai.ApiOpenAICompletions, BaseURL: "http://localhost:0", Input: []ai.ModelInput{ai.ModelInputText}, ContextWindow: 1000, MaxTokens: 100},
			},
		}); err != nil {
			t.Fatal(err)
		}
		agentInstance, err := agent.NewAgent(agent.AgentOptions{
			StreamFn: func(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
				stream := types.NewAssistantMessageEventStream()
				stream.End()
				return stream
			},
			InitialState: &agent.AgentState{Model: modelA, SystemPrompt: "test"},
		})
		if err != nil {
			t.Fatal(err)
		}
		scopedModels := []ScopedModel(nil)
		if scoped {
			scopedModels = []ScopedModel{{Model: *modelA}}
		}
		session := NewAgentSession(AgentSessionConfig{
			Agent:           agentInstance,
			SessionManager:  mustInMemorySessionManager(t),
			SettingsManager: settingsManager,
			Cwd:             t.TempDir(),
			ModelRuntime:    registry.GetModelRuntime(),
			ResourceLoader:  createTestResourceLoader(),
			ScopedModels:    scopedModels,
		})
		t.Cleanup(session.Dispose)
		return session, settingsManager
	}

	t.Run("adds a persisted default to an existing scoped model list", func(t *testing.T) {
		models := makeModels()
		session, settingsManager := createSession(t, nil, true, []string{"test-provider/model-a"})

		if err := session.SetModel(context.Background(), models[1], &ModelMutationOptions{Persist: true}); err != nil {
			t.Fatal(err)
		}

		if provider, _ := settingsManager.GetDefaultProvider(); provider != "test-provider" {
			t.Fatalf("default provider = %q", provider)
		}
		if model, _ := settingsManager.GetDefaultModel(); model != "model-b" {
			t.Fatalf("default model = %q", model)
		}
		scoped := session.ScopedModels()
		if len(scoped) != 2 || scoped[0].Model.ID != "model-a" || scoped[1].Model.ID != "model-b" {
			t.Fatalf("scoped models = %+v", scoped)
		}
		enabled, ok := settingsManager.GetEnabledModels()
		if !ok || len(enabled) != 2 || enabled[0] != "test-provider/model-a" || enabled[1] != "test-provider/model-b" {
			t.Fatalf("enabled models = %v, %v", enabled, ok)
		}
	})

	t.Run("does not create a scoped model list when all models are available", func(t *testing.T) {
		models := makeModels()
		session, settingsManager := createSession(t, nil, false, nil)

		if err := session.SetModel(context.Background(), models[1], &ModelMutationOptions{Persist: true}); err != nil {
			t.Fatal(err)
		}

		if scoped := session.ScopedModels(); len(scoped) != 0 {
			t.Fatalf("scoped models = %+v", scoped)
		}
		if _, ok := settingsManager.GetEnabledModels(); ok {
			t.Fatal("enabled models should be absent")
		}
	})

	t.Run("keeps session-only model changes out of scope", func(t *testing.T) {
		models := makeModels()
		session, settingsManager := createSession(t, nil, true, []string{"test-provider/model-a"})

		if err := session.SetModel(context.Background(), models[1], &ModelMutationOptions{Persist: false}); err != nil {
			t.Fatal(err)
		}

		if scoped := session.ScopedModels(); len(scoped) != 1 || scoped[0].Model.ID != "model-a" {
			t.Fatalf("scoped models = %+v", scoped)
		}
		enabled, ok := settingsManager.GetEnabledModels()
		if !ok || len(enabled) != 1 || enabled[0] != "test-provider/model-a" {
			t.Fatalf("enabled models = %v, %v", enabled, ok)
		}
	})
}

// mustInMemorySessionManager builds a throwaway in-memory session manager.
func mustInMemorySessionManager(t *testing.T) *SessionManager {
	t.Helper()
	session, err := InMemorySession(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	return session
}
