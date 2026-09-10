// ai/scripts/generatemodels (pi: packages/ai/scripts/generate-models.ts) —
// fetches the models.dev, OpenRouter, and Vercel AI Gateway catalogs and
// regenerates ai/models_generated.go.
//
// Run: cd ai/scripts/generatemodels && go run .
// Output: ai/models_generated.go (overwritten in place).
//
// pi's model-data.ts / check-model-data.ts (validation of per-provider shards
// plus data/*.json and .manifest.json) are not ported: the generator emits a
// single file with no data/manifest layout to validate (see the
// ai/models_generated.go header comment). See also ai/scripts/generateimagemodels.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/ZoroSpace/pig/ai"
	"github.com/samber/lo"
)

// generatorOptions mirrors pi's readGeneratorOptions subset that pig uses.
type generatorOptions struct {
	// strict fails the run (instead of logging) when an upstream fetch fails.
	strict bool
}

var genOptions generatorOptions

func main() {
	for _, arg := range os.Args[1:] {
		switch arg {
		case "--strict":
			genOptions.strict = true
		default:
			fmt.Fprintf(os.Stderr, "Unknown argument: %s\n", arg)
			os.Exit(1)
		}
	}
	if err := generateModels(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// generateModels is the pi generateModels() port for the ported provider set.
func generateModels(ctx context.Context) error {
	modelsDevModels, err := loadModelsDevData(ctx)
	if err != nil {
		if genOptions.strict {
			return err
		}
		fmt.Fprintf(os.Stderr, "Failed to load models.dev data: %v\n", err)
		modelsDevModels = nil
	}
	openRouterModels, err := fetchOpenRouterModels(ctx)
	if err != nil {
		if genOptions.strict {
			return err
		}
		fmt.Fprintf(os.Stderr, "Failed to fetch OpenRouter models: %v\n", err)
		openRouterModels = nil
	}
	aiGatewayModels, err := fetchAiGatewayModels(ctx)
	if err != nil {
		if genOptions.strict {
			return err
		}
		fmt.Fprintf(os.Stderr, "Failed to fetch Vercel AI Gateway models: %v\n", err)
		aiGatewayModels = nil
	}

	// Combine models (models.dev has priority). pi also filters out xai
	// built-in ids and the opencode gpt-5.3-codex-spark alias here; both
	// providers are unported, so the filter is a no-op.
	allModels := make([]*ai.Model, 0, len(modelsDevModels)+len(openRouterModels)+len(aiGatewayModels))
	allModels = append(allModels, modelsDevModels...)
	allModels = append(allModels, openRouterModels...)
	allModels = append(allModels, aiGatewayModels...)

	// Temporary overrides until upstream model metadata is corrected (pi).
	// github-copilot extended context, opencode sonnet/gpt-5.4, openai
	// short/long-context pricing, cloudflare-ai-gateway pricing, and gpt-5-pro
	// are skipped (unported providers); the OpenRouter / Vercel gateway
	// overrides apply.
	for _, candidate := range allModels {
		if (candidate.Provider == ai.ProviderAnthropic || candidate.Provider == ai.ProviderOpenCode || candidate.Provider == ai.ProviderOpenCodeGo) &&
			(candidate.ID == "claude-opus-4-6" ||
				candidate.ID == "claude-sonnet-4-6" ||
				candidate.ID == "claude-opus-4.6" ||
				candidate.ID == "claude-sonnet-4.6") {
			candidate.ContextWindow = 1000000
		}
		if (candidate.Provider == ai.ProviderOpenRouter && openRouterKimiK3ModelIds[candidate.ID]) ||
			(candidate.Provider == ai.ProviderVercelAIGateway && candidate.ID == "moonshotai/kimi-k3") {
			candidate.MaxTokens = kimiK3MaxTokens
		}
		if candidate.Provider == ai.ProviderOpenRouter && candidate.ID == "moonshotai/kimi-k2.5" {
			candidate.Cost.Input = 0.41
			candidate.Cost.Output = 2.06
			candidate.Cost.CacheRead = 0.07
			candidate.MaxTokens = 4096
		}
		if candidate.Provider == ai.ProviderOpenRouter && strings.HasPrefix(candidate.ID, "moonshotai/kimi-k2.6") {
			mergeOpenAICompletionsCompat(candidate, &ai.OpenAICompletionsCompat{
				SupportsDeveloperRole:                       lo.ToPtr(false),
				RequiresReasoningContentOnAssistantMessages: lo.ToPtr(true),
			})
		}
		if candidate.Provider == ai.ProviderOpenRouter && candidate.ID == "z-ai/glm-5" {
			candidate.Cost.Input = 0.6
			candidate.Cost.Output = 1.9
			candidate.Cost.CacheRead = 0.119
		}
	}

	// pi's missingOpenAiModels (gpt-5.6 sol/terra/luna, gpt-5-chat-latest) use
	// openai-responses and are skipped.

	allModels = append(allModels, deepseekV4Models()...)
	allModels = append(allModels, antLingModels()...)

	for _, candidate := range allModels {
		if candidate.API != ai.ApiOpenAICompletions || !strings.Contains(candidate.ID, "deepseek-v4") {
			continue
		}
		if qwenTokenPlanProviderIds[string(candidate.Provider)] {
			continue
		}
		preservesNativeReasoningEffort := candidate.Provider == ai.ProviderOpenRouter || candidate.Provider == ai.ProviderOpenCode
		if preservesNativeReasoningEffort {
			mergeOpenAICompletionsCompat(candidate, &ai.OpenAICompletionsCompat{
				RequiresReasoningContentOnAssistantMessages: lo.ToPtr(true),
			})
		} else {
			mergeOpenAICompletionsCompat(candidate, deepseekCompat())
		}
	}

	// Only MiniMax M2.7 / M3 direct models are served by pi (pi: minimaxDirectSupportedIds).
	minimaxDirectSupportedIDs := stringSet("MiniMax-M2.7", "MiniMax-M2.7-highspeed", "MiniMax-M3")
	filtered := allModels[:0]
	for _, candidate := range allModels {
		if (candidate.Provider == ai.ProviderMinimax || candidate.Provider == ai.ProviderMinimaxCN) &&
			!minimaxDirectSupportedIDs[candidate.ID] {
			continue
		}
		filtered = append(filtered, candidate)
	}
	allModels = filtered

	// pi's codexModels (openai-codex-responses) and Mistral Medium 3.5
	// (mistral-conversations) are skipped (unported APIs).

	allModels = addOpenRouterAliases(allModels)

	// pi clones Azure Foundry models here (azure-openai-responses); skipped.

	for _, model := range allModels {
		applyOpenAICompletionsCompatMetadata(model)
		applyAnthropicMessagesCompatMetadata(model)
		applyModelsDevReasoningOptionMetadata(model)
		applyThinkingLevelMetadata(model)
		applyStrictToolCompatMetadata(model)
		// pi also runs applyOpenAIGrammarToolCompatMetadata,
		// applyOpenAIToolSearchMetadata, and
		// applyOpenAIExplicitPromptCacheMetadata — all openai-responses only,
		// skipped.
	}
	applyAnthropicAllowedFallbackModelMetadata(filterAnthropicFallback(allModels))

	// Group by provider and deduplicate by model id; the first entry wins, so
	// models.dev takes priority over OpenRouter/AI Gateway (pi).
	providers := map[ai.ProviderID]map[string]*ai.Model{}
	for _, model := range allModels {
		if providers[model.Provider] == nil {
			providers[model.Provider] = map[string]*ai.Model{}
		}
		if providers[model.Provider][model.ID] == nil {
			providers[model.Provider][model.ID] = model
		}
	}

	generatedAt := time.Now().UnixMilli()
	content, err := generateModelsFile(providers, generatedAt)
	if err != nil {
		return err
	}
	outPath := filepath.Join(sourceDir(), "..", "..", "models_generated.go")
	if err := os.WriteFile(outPath, content, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", outPath, err)
	}
	fmt.Printf("Generated %s (%d providers, %d models)\n", outPath, len(providers), len(allModels))

	printStatistics(allModels, providers)
	return nil
}

// filterAnthropicFallback returns the anthropic-messages models participating
// in allowed-fallback metadata (pi: isAnthropicFallbackMetadataModel filter).
func filterAnthropicFallback(models []*ai.Model) []*ai.Model {
	var out []*ai.Model
	for _, model := range models {
		if isAnthropicFallbackMetadataModel(model) {
			out = append(out, model)
		}
	}
	return out
}

// deepseekCompat is pi's deepseekCompat (openai-completions thinking format).
func deepseekCompat() *ai.OpenAICompletionsCompat {
	return &ai.OpenAICompletionsCompat{
		RequiresReasoningContentOnAssistantMessages: lo.ToPtr(true),
		ThinkingFormat: lo.ToPtr(ai.ThinkingFormatDeepSeek),
	}
}

// deepseekV4Models is pi's hardcoded DeepSeek V4 Flash / Pro catalog.
func deepseekV4Models() []*ai.Model {
	return []*ai.Model{
		{
			ID:            "deepseek-v4-flash",
			Name:          "DeepSeek V4 Flash",
			API:           ai.ApiOpenAICompletions,
			BaseURL:       "https://api.deepseek.com",
			Provider:      ai.ProviderDeepSeek,
			Reasoning:     true,
			Input:         []ai.ModelInput{ai.ModelInputText},
			Cost:          ai.ModelCost{ModelCostRates: ai.ModelCostRates{Input: 0.14, Output: 0.28, CacheRead: 0.0028, CacheWrite: 0}},
			ContextWindow: 1000000,
			MaxTokens:     384000,
			Compat:        deepseekCompat(),
		},
		{
			ID:            "deepseek-v4-pro",
			Name:          "DeepSeek V4 Pro",
			API:           ai.ApiOpenAICompletions,
			BaseURL:       "https://api.deepseek.com",
			Provider:      ai.ProviderDeepSeek,
			Reasoning:     true,
			Input:         []ai.ModelInput{ai.ModelInputText},
			Cost:          ai.ModelCost{ModelCostRates: ai.ModelCostRates{Input: 0.435, Output: 0.87, CacheRead: 0.003625, CacheWrite: 0}},
			ContextWindow: 1000000,
			MaxTokens:     384000,
			Compat:        deepseekCompat(),
		},
	}
}

// antLingModels is pi's hardcoded Ant Ling / Ring catalog.
func antLingModels() []*ai.Model {
	antLingCompat := &ai.OpenAICompletionsCompat{
		SupportsStore:              lo.ToPtr(false),
		SupportsDeveloperRole:      lo.ToPtr(false),
		SupportsReasoningEffort:    lo.ToPtr(false),
		MaxTokensField:             lo.ToPtr(ai.MaxTokensPlain),
		SupportsLongCacheRetention: lo.ToPtr(false),
	}
	return []*ai.Model{
		{
			ID:            "Ling-2.6-flash",
			Name:          "Ling 2.6 Flash",
			API:           ai.ApiOpenAICompletions,
			BaseURL:       "https://api.ant-ling.com/v1",
			Provider:      ai.ProviderAntLing,
			Reasoning:     false,
			Input:         []ai.ModelInput{ai.ModelInputText},
			Cost:          ai.ModelCost{ModelCostRates: ai.ModelCostRates{Input: 0.01, Output: 0.02, CacheRead: 0, CacheWrite: 0}},
			ContextWindow: 262144,
			MaxTokens:     65536,
			Compat:        copyOpenAICompat(antLingCompat),
		},
		{
			ID:            "Ling-2.6-1T",
			Name:          "Ling 2.6 1T",
			API:           ai.ApiOpenAICompletions,
			BaseURL:       "https://api.ant-ling.com/v1",
			Provider:      ai.ProviderAntLing,
			Reasoning:     false,
			Input:         []ai.ModelInput{ai.ModelInputText},
			Cost:          ai.ModelCost{ModelCostRates: ai.ModelCostRates{Input: 0.06, Output: 0.25, CacheRead: 0, CacheWrite: 0}},
			ContextWindow: 262144,
			MaxTokens:     65536,
			Compat:        copyOpenAICompat(antLingCompat),
		},
		{
			ID:            "Ring-2.6-1T",
			Name:          "Ring 2.6 1T",
			API:           ai.ApiOpenAICompletions,
			BaseURL:       "https://api.ant-ling.com/v1",
			Provider:      ai.ProviderAntLing,
			Reasoning:     true,
			Input:         []ai.ModelInput{ai.ModelInputText},
			Cost:          ai.ModelCost{ModelCostRates: ai.ModelCostRates{Input: 0.06, Output: 0.25, CacheRead: 0, CacheWrite: 0}},
			ContextWindow: 262144,
			MaxTokens:     65536,
			Compat: withCompat(antLingCompat, func(c *ai.OpenAICompletionsCompat) {
				c.ThinkingFormat = lo.ToPtr(ai.ThinkingFormatAntLing)
			}),
		},
	}
}

// addOpenRouterAliases appends pi's "auto" and "openrouter/fusion" router
// aliases when absent from the OpenRouter catalog.
func addOpenRouterAliases(allModels []*ai.Model) []*ai.Model {
	has := func(id string) bool {
		for _, m := range allModels {
			if m.Provider == ai.ProviderOpenRouter && m.ID == id {
				return true
			}
		}
		return false
	}
	if !has("auto") {
		allModels = append(allModels, &ai.Model{
			ID:        "auto",
			Name:      "Auto",
			API:       ai.ApiOpenAICompletions,
			Provider:  ai.ProviderOpenRouter,
			BaseURL:   "https://openrouter.ai/api/v1",
			Reasoning: true,
			Input:     []ai.ModelInput{ai.ModelInputText, ai.ModelInputImage},
			Cost: ai.ModelCost{ModelCostRates: ai.ModelCostRates{
				// we don't know about the costs because OpenRouter auto routes
				// to different models and then charges you for the underlying
				// used model.
				Input: 0, Output: 0, CacheRead: 0, CacheWrite: 0,
			}},
			ContextWindow: 2000000,
			MaxTokens:     30000,
		})
	}
	if !has("openrouter/fusion") {
		allModels = append(allModels, &ai.Model{
			ID:        "openrouter/fusion",
			Name:      "OpenRouter: Fusion",
			API:       ai.ApiOpenAICompletions,
			Provider:  ai.ProviderOpenRouter,
			BaseURL:   "https://openrouter.ai/api/v1",
			Reasoning: true,
			Input:     []ai.ModelInput{ai.ModelInputText},
			Cost: ai.ModelCost{ModelCostRates: ai.ModelCostRates{
				// we don't know about the costs because Fusion routes to
				// multiple models and then charges you for the underlying used
				// models.
				Input: 0, Output: 0, CacheRead: 0, CacheWrite: 0,
			}},
			ContextWindow: 1000000,
			MaxTokens:     30000,
		})
	}
	return allModels
}

// sourceDir resolves the generator's source directory so the output path is
// stable regardless of the working directory.
func sourceDir() string {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		return "."
	}
	return filepath.Dir(filename)
}

func printStatistics(allModels []*ai.Model, providers map[ai.ProviderID]map[string]*ai.Model) {
	totalModels := len(allModels)
	reasoningModels := 0
	for _, model := range allModels {
		if model.Reasoning {
			reasoningModels++
		}
	}
	fmt.Printf("\nModel Statistics:\n")
	fmt.Printf("  Total tool-capable models: %d\n", totalModels)
	fmt.Printf("  Reasoning-capable models: %d\n", reasoningModels)
	providerIDs := make([]string, 0, len(providers))
	for providerID := range providers {
		providerIDs = append(providerIDs, string(providerID))
	}
	sort.Strings(providerIDs)
	for _, providerID := range providerIDs {
		fmt.Printf("  %s: %d models\n", providerID, len(providers[ai.ProviderID(providerID)]))
	}
}
