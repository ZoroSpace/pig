// Ported from pi's test/model-resolver.test.ts. pi casts plain objects to
// ModelRuntime with `as unknown as`; pig stubs the ModelResolverRuntime
// interface instead. The warn sink is swapped via the modelResolverWarn var
// (pi: vi.spyOn(console, "warn")).
package core

import (
	"context"
	"sort"
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

// referenceModel builds a resolver reference-catalog fixture (pi:
// referenceModel).
func referenceModel(provider, id string) ai.Model {
	return ai.Model{
		ID:            id,
		Name:          id,
		API:           ai.ApiOpenAICompletions,
		Provider:      ai.ProviderID(provider),
		BaseURL:       "https://example.invalid",
		Reasoning:     true,
		Input:         []ai.ModelInput{ai.ModelInputText},
		Cost:          ai.ModelCost{},
		ContextWindow: 128000,
		MaxTokens:     8192,
	}
}

var referenceCatalog = []ai.Model{
	referenceModel("anthropic", "claude-sonnet-4-5"),
	referenceModel("anthropic", "claude-sonnet-4-5-20250929"),
	referenceModel("anthropic", "claude-opus-4-8"),
	referenceModel("anthropic", "exact"),
	// Fuzzy-matches "shared"; an ambiguous exact "shared" must not fall through to it.
	referenceModel("anthropic", "shared-pro"),
	referenceModel("openai", "gpt-4o"),
	referenceModel("openrouter", "qwen/qwen3-coder:exacto"),
	referenceModel("openrouter", "openai/gpt-4o:extended"),
	referenceModel("gw-a", "shared"),
	referenceModel("gw-a", "anthropic/future"),
	referenceModel("gw-a", "anthropic/exact"),
	referenceModel("gw-a", "anthropic/claude-opus-4.8"),
	referenceModel("gw-a", "anthropic/claude-opus"),
	referenceModel("gw-b", "shared"),
	referenceModel("gw-b", "anthropic/future"),
	referenceModel("gw-b", "claude-opus-4-8"),
	referenceModel("gw-b", "gw-b/free"),
	referenceModel("gw-b", "zz:free"),
}

type refExpect struct {
	status        string // "resolved" | "ambiguous" | "not-found"
	model         string
	thinkingLevel agent.ThinkingLevel
	warning       string
	ambiguous     []string
	usableCount   int
}

type refCase struct {
	name           string
	text           string
	expected       refExpect
	usable         []string
	provider       string
	thinkingSuffix string // empty = "strict"
	fuzzy          *bool
}

var referenceCases = []refCase{
	// Reference syntax
	{name: "exact id", text: "claude-sonnet-4-5", expected: refExpect{status: "resolved", model: "anthropic/claude-sonnet-4-5"}},
	{name: "exact id is case-insensitive", text: "GPT-4O", expected: refExpect{status: "resolved", model: "openai/gpt-4o"}},
	{name: "fuzzy prefers aliases over dated ids", text: "sonnet", expected: refExpect{status: "resolved", model: "anthropic/claude-sonnet-4-5"}},
	{name: "thinking suffix", text: "sonnet:high", expected: refExpect{status: "resolved", model: "anthropic/claude-sonnet-4-5", thinkingLevel: "high"}},
	{name: "colon in a literal id is not a thinking suffix", text: "qwen/qwen3-coder:exacto", expected: refExpect{status: "resolved", model: "openrouter/qwen/qwen3-coder:exacto"}},
	{name: "provider prefix, colon id, and thinking suffix", text: "openrouter/qwen/qwen3-coder:exacto:high", expected: refExpect{status: "resolved", model: "openrouter/qwen/qwen3-coder:exacto", thinkingLevel: "high"}},
	{name: "raw id wins when the provider prefix names no matching model", text: "openai/gpt-4o:extended", expected: refExpect{status: "resolved", model: "openrouter/openai/gpt-4o:extended"}},
	{name: "fuzzy searches the prefixed provider first", text: "openrouter/qwen", expected: refExpect{status: "resolved", model: "openrouter/qwen/qwen3-coder:exacto"}},
	{name: "strict keeps an invalid suffix in the id", text: "gpt-4o:random", expected: refExpect{status: "not-found"}},
	{name: "lenient drops an invalid suffix with a warning", text: "gpt-4o:random", thinkingSuffix: "lenient", expected: refExpect{status: "resolved", model: "openai/gpt-4o", warning: `Invalid thinking level "random"`}},
	{name: "lenient invalid outer suffix discards the inner thinking level", text: "qwen/qwen3-coder:exacto:high:random", thinkingSuffix: "lenient", expected: refExpect{status: "resolved", model: "openrouter/qwen/qwen3-coder:exacto", warning: `Invalid thinking level "random"`}},
	{name: "none keeps the suffix in the id", text: "gpt-4o:high", thinkingSuffix: "none", expected: refExpect{status: "not-found"}},
	{name: "exact-only skips fuzzy matches", text: "sonnet", fuzzy: boolPtr(false), expected: refExpect{status: "not-found"}},
	{name: "empty text", text: "  ", expected: refExpect{status: "not-found"}},

	// Exact matches across providers (#7327)
	{name: "sole usable provider wins", text: "shared", usable: []string{"gw-b"}, expected: refExpect{status: "resolved", model: "gw-b/shared"}},
	{name: "no usable provider is ambiguous instead of falling through to fuzzy", text: "shared", expected: refExpect{status: "ambiguous", ambiguous: []string{"gw-a/shared", "gw-b/shared"}}},
	{name: "several usable providers are ambiguous", text: "shared", usable: []string{"gw-a", "gw-b"}, expected: refExpect{status: "ambiguous", ambiguous: []string{"gw-a/shared", "gw-b/shared"}, usableCount: 2}},
	{name: "thinking suffix does not change provider selection", text: "shared:high", usable: []string{"gw-b"}, expected: refExpect{status: "resolved", model: "gw-b/shared", thinkingLevel: "high"}},
	{name: "thinking suffix does not bypass ambiguity", text: "shared:high", usable: []string{"gw-a", "gw-b"}, expected: refExpect{status: "ambiguous", ambiguous: []string{"gw-a/shared", "gw-b/shared"}, usableCount: 2}},
	{name: "provider prefix without a direct model selects the usable raw id", text: "anthropic/future", usable: []string{"gw-b"}, expected: refExpect{status: "resolved", model: "gw-b/anthropic/future"}},
	{name: "usable qualified match beats a usable raw id", text: "anthropic/exact", usable: []string{"anthropic", "gw-a"}, expected: refExpect{status: "resolved", model: "anthropic/exact"}},
	{name: "usable raw id beats an unusable qualified match", text: "anthropic/exact", usable: []string{"gw-a"}, expected: refExpect{status: "resolved", model: "gw-a/anthropic/exact"}},
	{name: "unusable qualified match is kept when nothing is usable", text: "anthropic/exact", expected: refExpect{status: "resolved", model: "anthropic/exact"}},
	{name: "named provider's usable fuzzy match beats a raw id elsewhere", text: "anthropic/claude-opus", usable: []string{"anthropic"}, expected: refExpect{status: "resolved", model: "anthropic/claude-opus-4-8"}},
	{name: "usable raw id beats the named provider's unusable fuzzy match", text: "anthropic/claude-opus", usable: []string{"gw-a"}, expected: refExpect{status: "resolved", model: "gw-a/anthropic/claude-opus"}},
	{name: "named provider's fuzzy match is kept when nothing is usable", text: "anthropic/claude-opus", expected: refExpect{status: "resolved", model: "anthropic/claude-opus-4-8"}},
	{name: "named provider's literal id beats its fuzzy matches", text: "gw-b/free", expected: refExpect{status: "resolved", model: "gw-b/gw-b/free"}},

	// Fuzzy matches across providers
	{name: "fuzzy prefers usable providers", text: "opus", usable: []string{"gw-a"}, expected: refExpect{status: "resolved", model: "gw-a/anthropic/claude-opus-4.8"}},
	{name: "fuzzy id tie across usable providers is ambiguous", text: "opus", usable: []string{"anthropic", "gw-a", "gw-b"}, expected: refExpect{status: "ambiguous", ambiguous: []string{"anthropic/claude-opus-4-8", "gw-b/claude-opus-4-8"}, usableCount: 2}},
	{name: "fuzzy id tie resolves to the sole usable provider", text: "opus", usable: []string{"anthropic"}, expected: refExpect{status: "resolved", model: "anthropic/claude-opus-4-8"}},

	// Explicit provider constraint
	{name: "explicit provider ignores usable providers elsewhere", text: "shared", provider: "gw-a", usable: []string{"gw-b"}, expected: refExpect{status: "resolved", model: "gw-a/shared"}},
	{name: "explicit provider accepts its own prefix", text: "GW-B/shared", provider: "gw-b", expected: refExpect{status: "resolved", model: "gw-b/shared"}},
	{name: "explicit provider allows fuzzy matches", text: "4o", provider: "openai", expected: refExpect{status: "resolved", model: "openai/gpt-4o"}},
	{name: "explicit provider never leaves the provider", text: "gpt-4o", provider: "gw-a", expected: refExpect{status: "not-found"}},
}

func modelRef(m ai.Model) string { return string(m.Provider) + "/" + m.ID }

func TestResolveModelReference(t *testing.T) {
	reversed := make([]ai.Model, len(referenceCatalog))
	for i, m := range referenceCatalog {
		reversed[len(referenceCatalog)-1-i] = m
	}
	for _, tc := range referenceCases {
		t.Run(tc.name, func(t *testing.T) {
			usable := map[string]bool{}
			for _, p := range tc.usable {
				usable[p] = true
			}
			// Catalog order must never decide the result.
			for _, models := range [][]ai.Model{referenceCatalog, reversed} {
				thinkingSuffix := tc.thinkingSuffix
				if thinkingSuffix == "" {
					thinkingSuffix = "strict"
				}
				fuzzy := true
				if tc.fuzzy != nil {
					fuzzy = *tc.fuzzy
				}
				result := ResolveModelReference(ModelReferenceRequest{
					Text:           tc.text,
					Provider:       tc.provider,
					ThinkingSuffix: thinkingSuffix,
					Fuzzy:          fuzzy,
				}, models, func(provider string) bool { return usable[provider] })

				switch tc.expected.status {
				case "not-found":
					if result.Status != "not-found" {
						t.Fatalf("status = %q, want not-found (%+v)", result.Status, result)
					}
				case "ambiguous":
					if result.Status != "ambiguous" {
						t.Fatalf("status = %q, want ambiguous (%+v)", result.Status, result)
					}
					got := make([]string, 0, len(result.Candidates))
					for _, m := range result.Candidates {
						got = append(got, modelRef(m))
					}
					sort.Strings(got)
					if strings.Join(got, ",") != strings.Join(tc.expected.ambiguous, ",") {
						t.Fatalf("candidates = %v, want %v", got, tc.expected.ambiguous)
					}
					if result.UsableCount != tc.expected.usableCount {
						t.Fatalf("usableCount = %d, want %d", result.UsableCount, tc.expected.usableCount)
					}
				default:
					if result.Status != "resolved" {
						t.Fatalf("status = %q, want resolved (%+v)", result.Status, result)
					}
					if got := modelRef(*result.Model); got != tc.expected.model {
						t.Fatalf("model = %q, want %q", got, tc.expected.model)
					}
					if result.ThinkingLevel != tc.expected.thinkingLevel {
						t.Fatalf("thinkingLevel = %q, want %q", result.ThinkingLevel, tc.expected.thinkingLevel)
					}
					if tc.expected.warning != "" {
						if !strings.Contains(result.Warning, tc.expected.warning) {
							t.Fatalf("warning = %q, want to contain %q", result.Warning, tc.expected.warning)
						}
					} else if result.Warning != "" {
						t.Fatalf("warning = %q, want none", result.Warning)
					}
				}
			}
		})
	}
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

func TestResolveModelScopeAddsEveryProviderForAmbiguousPattern(t *testing.T) {
	gpt4o := resolverMockModels[1]
	azure := gpt4o
	azure.Provider = "azure"
	codex := gpt4o
	codex.Provider = "openai-codex"
	codex.ID = "gpt-4o-mini"
	available := append(append([]ai.Model(nil), resolverAllModels...), azure, codex)
	registry := &stubResolverRuntime{available: available}

	for _, tc := range []struct {
		pattern       string
		thinkingLevel agent.ThinkingLevel
	}{
		{"gpt-4o:high", "high"},
		{"GPT-4o", ""},
	} {
		result, err := ResolveModelScopeWithDiagnostics(context.Background(), []string{tc.pattern}, registry)
		if err != nil {
			t.Fatal(err)
		}
		if len(result.ScopedModels) != 2 {
			t.Fatalf("pattern %q: scopedModels = %+v, want 2", tc.pattern, result.ScopedModels)
		}
		seen := map[string]agent.ThinkingLevel{}
		for _, scoped := range result.ScopedModels {
			seen[string(scoped.Model.Provider)+"/"+scoped.Model.ID] = scoped.ThinkingLevel
		}
		for _, key := range []string{"azure/gpt-4o", "openai/gpt-4o"} {
			if level, ok := seen[key]; !ok || level != tc.thinkingLevel {
				t.Fatalf("pattern %q: scoped %q = %q (present %v), want %q", tc.pattern, key, level, ok, tc.thinkingLevel)
			}
		}
		if len(result.Diagnostics) != 1 {
			t.Fatalf("pattern %q: diagnostics = %+v", tc.pattern, result.Diagnostics)
		}
		diagnostic := result.Diagnostics[0]
		if diagnostic.Code != "ambiguous" ||
			diagnostic.Message != `Pattern "`+tc.pattern+`" matches several providers: azure, openai. Use provider/id to choose one.` {
			t.Fatalf("pattern %q: diagnostic = %+v", tc.pattern, diagnostic)
		}
	}
}

// TestResolveModelScopeMatchesGlobPatternsAgainstProviderQualifiedIDs locks
// the Minimatch argument order (glob, value): a swapped call would match
// nothing and emit a "No models match pattern" diagnostic.
func TestResolveModelScopeMatchesGlobPatternsAgainstProviderQualifiedIDs(t *testing.T) {
	registry := &stubResolverRuntime{available: resolverAllModels}

	result, err := ResolveModelScopeWithDiagnostics(context.Background(), []string{"anthropic/claude-*"}, registry)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.ScopedModels) != 1 || result.ScopedModels[0].Model.ID != "claude-sonnet-4-5" {
		t.Fatalf("scopedModels = %+v, want [claude-sonnet-4-5]", result.ScopedModels)
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

	t.Run("duplicate raw ids behind a known provider prefix (#10160)", func(t *testing.T) {
		direct := resolverMockModels[0]
		direct.Provider, direct.ID = "anthropic", "claude-sonnet-4-5"
		openrouter := resolverMockModels[0]
		openrouter.Provider, openrouter.ID = "openrouter", "anthropic/claude-opus-5.5"
		vercel := resolverMockModels[0]
		vercel.Provider, vercel.ID = "vercel-ai-gateway", "anthropic/claude-opus-5.5"
		registry := func(configured ...string) *stubResolverRuntime {
			authByP := map[string]bool{}
			for _, p := range configured {
				authByP[p] = true
			}
			return &stubResolverRuntime{models: []ai.Model{direct, openrouter, vercel}, authByP: authByP}
		}

		t.Run("selects the authenticated gateway, with or without a thinking suffix", func(t *testing.T) {
			for _, tc := range []struct {
				cliModel      string
				thinkingLevel agent.ThinkingLevel
			}{
				{"anthropic/claude-opus-5.5", ""},
				{"anthropic/claude-opus-5.5:high", "high"},
			} {
				result := ResolveCliModel(ResolveCliModelOptions{CliModel: tc.cliModel, ModelRuntime: registry("vercel-ai-gateway")})
				if result.Error != "" {
					t.Fatalf("cliModel %q: error = %q", tc.cliModel, result.Error)
				}
				if string(result.Model.Provider) != "vercel-ai-gateway" || result.ThinkingLevel != tc.thinkingLevel {
					t.Fatalf("cliModel %q: model = %+v, thinking = %q", tc.cliModel, result.Model, result.ThinkingLevel)
				}
			}
		})

		t.Run("reports ambiguity instead of choosing by catalog order", func(t *testing.T) {
			result := ResolveCliModel(ResolveCliModelOptions{CliModel: "anthropic/claude-opus-5.5", ModelRuntime: registry()})
			if result.Model != nil {
				t.Fatalf("model = %+v", result.Model)
			}
			for _, want := range []string{
				"openrouter/anthropic/claude-opus-5.5, vercel-ai-gateway/anthropic/claude-opus-5.5",
				"No matching provider is authenticated. Use --provider or provider/model.",
			} {
				if !strings.Contains(result.Error, want) {
					t.Fatalf("error %q missing %q", result.Error, want)
				}
			}
		})

		t.Run("does not let stored credentials choose the provider for --api-key", func(t *testing.T) {
			result := ResolveCliModel(ResolveCliModelOptions{
				CliModel: "anthropic/claude-opus-5.5", InlineApiKey: true, ModelRuntime: registry("vercel-ai-gateway"),
			})
			if result.Model != nil {
				t.Fatalf("model = %+v", result.Model)
			}
			if !strings.Contains(result.Error, "--api-key does not choose a provider.") {
				t.Fatalf("error = %q", result.Error)
			}
		})
	})

	t.Run("custom model fallback with :thinking suffix (#5552)", func(t *testing.T) {
		neuralwattModel := resolverModel("some-base-model", "Some Base Model", "neuralwatt", "https://api.neuralwatt.com", false)
		modelsWithNeuralwatt := append(append([]ai.Model(nil), resolverAllModels...), neuralwattModel)
		registry := &stubResolverRuntime{models: modelsWithNeuralwatt}

		t.Run("strips :thinking suffix from custom model id in fallback path", func(t *testing.T) {
			result := ResolveCliModel(ResolveCliModelOptions{CliModel: "neuralwatt/zai-org/GLM-5.1-FP8:high", ModelRuntime: registry})
			if result.Error != "" {
				t.Fatalf("error = %q", result.Error)
			}
			if string(result.Model.Provider) != "neuralwatt" {
				t.Fatalf("provider = %q", result.Model.Provider)
			}
			if result.Model.ID != "zai-org/GLM-5.1-FP8" || !result.Model.Reasoning || result.ThinkingLevel != "high" {
				t.Fatalf("result = %+v", result)
			}
		})

		t.Run("custom model without thinking suffix works normally in fallback path", func(t *testing.T) {
			result := ResolveCliModel(ResolveCliModelOptions{CliModel: "neuralwatt/zai-org/GLM-5.1-FP8", ModelRuntime: registry})
			if result.Error != "" {
				t.Fatalf("error = %q", result.Error)
			}
			if string(result.Model.Provider) != "neuralwatt" || result.Model.ID != "zai-org/GLM-5.1-FP8" || result.ThinkingLevel != "" {
				t.Fatalf("result = %+v", result)
			}
		})

		t.Run("all valid thinking levels work in fallback path", func(t *testing.T) {
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
			result := ResolveCliModel(ResolveCliModelOptions{CliModel: "neuralwatt/zai-org/GLM-5.1-FP8:banana", ModelRuntime: registry})
			if result.Error != "" {
				t.Fatalf("error = %q", result.Error)
			}
			if string(result.Model.Provider) != "neuralwatt" {
				t.Fatalf("provider = %q", result.Model.Provider)
			}
			if result.Model.ID != "zai-org/GLM-5.1-FP8:banana" || result.ThinkingLevel != "" {
				t.Fatalf("result = %+v", result)
			}
		})

		t.Run("explicit --provider with custom model:thinking strips suffix correctly", func(t *testing.T) {
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
			result := ResolveCliModel(ResolveCliModelOptions{
				CliModel: "neuralwatt/zai-org/GLM-5.1-FP8:high", CliThinking: "medium", ModelRuntime: registry,
			})
			if result.Error != "" {
				t.Fatalf("error = %q", result.Error)
			}
			if string(result.Model.Provider) != "neuralwatt" {
				t.Fatalf("provider = %q", result.Model.Provider)
			}
			if result.Model.ID != "zai-org/GLM-5.1-FP8:high" || result.ThinkingLevel != "" {
				t.Fatalf("result = %+v", result)
			}
		})
	})
}

func TestDefaultModelSelection(t *testing.T) {
	t.Run("openai defaults track current models", func(t *testing.T) {
		if DefaultModelPerProvider["openai"] != "gpt-5.5" || DefaultModelPerProvider["openai-codex"] != "gpt-6.1-sol" {
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

	t.Run("xiaomi and nvidia defaults track current models", func(t *testing.T) {
		want := map[string]string{
			"xiaomi": "mimo-v2.6-pro", "xiaomi-token-plan-cn": "mimo-v2.6-pro",
			"xiaomi-token-plan-ams": "mimo-v2.6-pro", "xiaomi-token-plan-sgp": "mimo-v2.6-pro",
			"nvidia": "nvidia/nemotron-3.5-super-vl-120b-a12b",
		}
		for provider, defaultID := range want {
			if DefaultModelPerProvider[provider] != defaultID {
				t.Fatalf("%s default = %q, want %q", provider, DefaultModelPerProvider[provider], defaultID)
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
