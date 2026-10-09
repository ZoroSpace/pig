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

// combineModels combines the fetched catalogs (models.dev has priority).
func combineModels(modelsDevModels, openRouterModels, aiGatewayModels []*ai.Model) []*ai.Model {
	// pi filters xai retired ids and the opencode gpt-5.3-codex-spark alias
	// here.
	allModels := make([]*ai.Model, 0, len(modelsDevModels)+len(openRouterModels)+len(aiGatewayModels))
	for _, model := range modelsDevModels {
		if model.Provider == ai.ProviderXAI && xaiBuiltinExcludedModelIds[model.ID] {
			continue
		}
		if (model.Provider == ai.ProviderOpenCode || model.Provider == ai.ProviderOpenCodeGo) && model.ID == "gpt-5.3-codex-spark" {
			continue
		}
		allModels = append(allModels, model)
	}
	allModels = append(allModels, openRouterModels...)
	allModels = append(allModels, aiGatewayModels...)
	return allModels
}

// xaiBuiltinExcludedModelIds lists retired/redundant xai ids kept out of the
// built-in catalog (pi: XAI_BUILTIN_EXCLUDED_MODEL_IDS).
var xaiBuiltinExcludedModelIds = stringSet(
	"grok-3", "grok-3-fast", "grok-4.20-0309-non-reasoning", "grok-4.20-0309-reasoning",
	"grok-build-0.1", "grok-code-fast-1",
)

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
	openRouterCatalog, err := fetchOpenRouterCatalog(ctx)
	if err != nil {
		if genOptions.strict {
			return err
		}
		fmt.Fprintf(os.Stderr, "Failed to fetch OpenRouter models: %v\n", err)
		openRouterCatalog = nil
	}
	aiGateway, err := fetchAiGatewayModels(ctx)
	if err != nil {
		if genOptions.strict {
			return err
		}
		fmt.Fprintf(os.Stderr, "Failed to fetch Vercel AI Gateway models: %v\n", err)
		aiGateway = nil
	}
	modelsDevClassifiers, err := loadModelsDevClassifierModels(ctx)
	if err != nil {
		if genOptions.strict {
			return err
		}
		fmt.Fprintf(os.Stderr, "Failed to load models.dev classifier data: %v\n", err)
		modelsDevClassifiers = nil
	}

	// Combine chat models (models.dev has priority); pi filters xai retired
	// ids and the opencode gpt-5.3-codex-spark alias here.
	var openRouterChat, aiGatewayChat []*ai.Model
	if openRouterCatalog != nil {
		openRouterChat = openRouterCatalog.chat
	}
	if aiGateway != nil {
		aiGatewayChat = aiGateway.chat
	}
	allModels := combineModels(modelsDevModels, openRouterChat, aiGatewayChat)

	// Add Claude Opus 5.5 until models.dev includes it (pi b4588f26a).
	// https://platform.claude.com/docs/en/models/opus-5-5/overview
	if !hasProviderModel(allModels, ai.ProviderAnthropic, "claude-opus-5-5") {
		allModels = append(allModels, &ai.Model{
			ID: "claude-opus-5-5", Name: "Claude Opus 5.5",
			API: ai.ApiAnthropicMessages, Provider: ai.ProviderAnthropic, BaseURL: "https://api.anthropic.com",
			Reasoning: true,
			ThinkingLevelMap: ai.ThinkingLevelMap{
				ai.ModelThinkingOff:     nil,
				ai.ModelThinkingMinimal: nil,
				ai.ModelThinkingLow:     lo.ToPtr("low"),
				ai.ModelThinkingMedium:  lo.ToPtr("medium"),
				ai.ModelThinkingHigh:    lo.ToPtr("high"),
				ai.ModelThinkingXHigh:   lo.ToPtr("xhigh"),
				ai.ModelThinkingMax:     lo.ToPtr("max"),
			},
			Input:         []ai.ModelInput{ai.ModelInputText, ai.ModelInputImage},
			Cost:          ai.ModelCost{ModelCostRates: ai.ModelCostRates{Input: 4, Output: 20, CacheRead: 0.2, CacheWrite: 5}},
			ContextWindow: 1000000, MaxTokens: 128000,
		})
	}

	// Add Claude Sonnet 5.5 until models.dev includes it (pi c90d9ea58).
	// https://platform.claude.com/docs/en/models/sonnet-5-5/overview
	if !hasProviderModel(allModels, ai.ProviderAnthropic, "claude-sonnet-5-5") {
		allModels = append(allModels, &ai.Model{
			ID: "claude-sonnet-5-5", Name: "Claude Sonnet 5.5",
			API: ai.ApiAnthropicMessages, Provider: ai.ProviderAnthropic, BaseURL: "https://api.anthropic.com",
			Reasoning: true,
			ThinkingLevelMap: ai.ThinkingLevelMap{
				ai.ModelThinkingOff:     nil,
				ai.ModelThinkingMinimal: nil,
				ai.ModelThinkingLow:     lo.ToPtr("low"),
				ai.ModelThinkingMedium:  lo.ToPtr("medium"),
				ai.ModelThinkingHigh:    lo.ToPtr("high"),
				ai.ModelThinkingXHigh:   lo.ToPtr("xhigh"),
				ai.ModelThinkingMax:     lo.ToPtr("max"),
			},
			Input:         []ai.ModelInput{ai.ModelInputText, ai.ModelInputImage},
			Cost:          ai.ModelCost{ModelCostRates: ai.ModelCostRates{Input: 2, Output: 10, CacheRead: 0.2, CacheWrite: 2.5}},
			ContextWindow: 1000000, MaxTokens: 128000,
		})
	}

	// Add Claude Haiku 5.5 until models.dev includes it (pi f76c1db66). Prompts over
	// 100k input tokens are billed at 5x for the whole request.
	// https://platform.claude.com/docs/en/models/haiku-5-5/overview
	if !hasProviderModel(allModels, ai.ProviderAnthropic, "claude-haiku-5-5") {
		allModels = append(allModels, &ai.Model{
			ID: "claude-haiku-5-5", Name: "Claude Haiku 5.5",
			API: ai.ApiAnthropicMessages, Provider: ai.ProviderAnthropic, BaseURL: "https://api.anthropic.com",
			Reasoning: true,
			ThinkingLevelMap: ai.ThinkingLevelMap{
				ai.ModelThinkingOff:     nil,
				ai.ModelThinkingMinimal: nil,
				ai.ModelThinkingLow:     lo.ToPtr("low"),
				ai.ModelThinkingMedium:  lo.ToPtr("medium"),
				ai.ModelThinkingHigh:    lo.ToPtr("high"),
				ai.ModelThinkingXHigh:   lo.ToPtr("xhigh"),
				ai.ModelThinkingMax:     lo.ToPtr("max"),
			},
			Input: []ai.ModelInput{ai.ModelInputText, ai.ModelInputImage},
			Cost: ai.ModelCost{
				ModelCostRates: ai.ModelCostRates{Input: 0.1, Output: 0.5, CacheRead: 0.01, CacheWrite: 0.125},
				Tiers: []ai.ModelCostTier{{
					ModelCostRates:   ai.ModelCostRates{Input: 0.5, Output: 2.5, CacheRead: 0.05, CacheWrite: 0.625},
					InputTokensAbove: 100000,
				}},
			},
			ContextWindow: 1000000, MaxTokens: 128000,
		})
	}

	// The authenticated Copilot catalog advertised these models on
	// 2026-09-22, but models.dev did not include them yet (pi 27c072e98).
	for _, model := range missingCopilotModels() {
		if !hasProviderModel(allModels, model.Provider, model.ID) {
			allModels = append(allModels, model)
		}
	}

	// Temporary overrides until upstream model metadata is corrected (pi).
	for _, candidate := range allModels {
		if candidate.Provider == ai.ProviderGithubCopilot && githubCopilotExtendedContextModels[candidate.ID] {
			candidate.ContextWindow = 1000000
		}
		// models.dev may list Opus 5.5 / Sonnet 5.5 / Haiku 5.5 before their effort
		// metadata is complete (pi b7f4b05c7; v0.99.1 extends to Sonnet 5.5
		// and GitHub Copilot Opus 5.5, c90d9ea58 + fde38ed7c; v1.1.0 adds
		// Haiku 5.5, f76c1db66).
		if (candidate.Provider == ai.ProviderAnthropic &&
			(candidate.ID == "claude-opus-5-5" || candidate.ID == "claude-sonnet-5-5" || candidate.ID == "claude-haiku-5-5")) ||
			(candidate.Provider == ai.ProviderGithubCopilot && candidate.ID == "claude-opus-5.5") {
			mergeThinkingLevelMap(candidate, ai.ThinkingLevelMap{
				ai.ModelThinkingOff:     nil,
				ai.ModelThinkingMinimal: nil,
				ai.ModelThinkingLow:     lo.ToPtr("low"),
				ai.ModelThinkingMedium:  lo.ToPtr("medium"),
				ai.ModelThinkingHigh:    lo.ToPtr("high"),
				ai.ModelThinkingXHigh:   lo.ToPtr("xhigh"),
				ai.ModelThinkingMax:     lo.ToPtr("max"),
			})
		}
		if (candidate.Provider == ai.ProviderAnthropic || candidate.Provider == ai.ProviderOpenCode || candidate.Provider == ai.ProviderOpenCodeGo) &&
			(candidate.ID == "claude-opus-4-6" ||
				candidate.ID == "claude-sonnet-4-6" ||
				candidate.ID == "claude-opus-4.6" ||
				candidate.ID == "claude-sonnet-4.6") {
			candidate.ContextWindow = 1000000
		}
		// OpenCode variants list Claude Sonnet 4/4.5 with 1M context, actual
		// limit is 200K.
		if (candidate.Provider == ai.ProviderOpenCode || candidate.Provider == ai.ProviderOpenCodeGo) &&
			(candidate.ID == "claude-sonnet-4-5" || candidate.ID == "claude-sonnet-4") {
			candidate.ContextWindow = 200000
		}
		if (candidate.Provider == ai.ProviderOpenCode || candidate.Provider == ai.ProviderOpenCodeGo) && candidate.ID == "gpt-5.4" {
			candidate.ContextWindow = 272000
			candidate.MaxTokens = 128000
		}
		// Keep direct OpenAI requests in the short-context pricing tier by
		// default. Users can opt into the larger context through model
		// overrides, so retain long-context cost metadata on the capped models.
		if candidate.Provider == ai.ProviderOpenAI && openAIShortContextCappedModelIds[candidate.ID] {
			candidate.ContextWindow = openAILongContextInputThreshold
			candidate.MaxTokens = 128000
		}
		if candidate.Provider == ai.ProviderOpenAI && openAILongContextPricingModelIds[candidate.ID] {
			standardCost, hasStandard := openAIStandardCosts[candidate.ID]
			if hasStandard {
				candidate.Cost = withOpenAiLongContextPricing(standardCost)
			} else {
				candidate.Cost = withOpenAiLongContextPricing(candidate.Cost)
			}
		}
		// Cloudflare AI Gateway passes OpenAI usage through at OpenAI list prices.
		if candidate.Provider == ai.ProviderCloudflareAIGateway {
			if standardCost, hasStandard := openAIStandardCosts[candidate.ID]; hasStandard {
				candidate.Cost = withOpenAiLongContextPricing(standardCost)
			}
		}
		// models.dev reports gpt-5-pro output as 272000 (a duplicate of the
		// input sub-limit); the actual max output is 128000. Also propagates
		// to the derived Azure clone.
		if candidate.Provider == ai.ProviderOpenAI && candidate.ID == "gpt-5-pro" {
			candidate.MaxTokens = 128000
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

	// pi's missingOpenAiModels: catalog models absent from models.dev.
	allModels = append(allModels, missingOpenAiModels()...)

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

	// pi's codexModels (openai-codex-responses) follow the minimax filter;
	// Mistral Medium 3.5 (mistral-conversations) stays skipped (unported API).
	allModels = append(allModels, codexModels()...)

	allModels = addOpenRouterAliases(allModels)

	// Azure Foundry deploys these with larger context windows than OpenAI's
	// own short-tier defaults (pi: the azureOpenAiModels clone; see
	// models-sold-directly-by-azure docs).
	allModels = append(allModels, cloneAzureOpenAIModels(allModels)...)
	// Azure resells DeepSeek at its own rates; the clone ships as an
	// openai-completions model (pi: the azureDeepSeekModels clone, v1.0.3
	// #9645).
	allModels = append(allModels, cloneAzureDeepSeekModels(allModels)...)

	for _, model := range allModels {
		applyOpenAICompletionsCompatMetadata(model)
		applyAnthropicMessagesCompatMetadata(model)
		applyModelsDevReasoningOptionMetadata(model)
		applyThinkingLevelMetadata(model)
		applyStrictToolCompatMetadata(model)
		applyOpenAIGrammarToolCompatMetadata(model)
		applyOpenAIToolSearchMetadata(model)
		applyOpenAICompletionsTranscriptMetadata(model)
		applyOpenAIResponsesTranscriptMetadata(model)
		applyOpenAIExplicitPromptCacheMetadata(model)
		applyPromptCacheMetadata(model)
		applyImageInputMetadata(model)
	}
	applyAnthropicAllowedFallbackModelMetadata(filterAnthropicFallback(allModels))

	// Group by provider and deduplicate by id; the first entry wins, so
	// models.dev takes priority over OpenRouter/AI Gateway (pi). Chat, image,
	// and classifier catalogs stay separate so one upstream id can expose
	// several operations with different implementations (pi: ProviderCatalog,
	// v0.99.1 #9948).
	classifierModels := append([]*ai.Model{}, modelsDevClassifiers...)
	if openRouterCatalog != nil {
		classifierModels = append(classifierModels, openRouterCatalog.classifiers...)
	}
	if aiGateway != nil {
		classifierModels = append(classifierModels, aiGateway.classifiers...)
	}
	classifierModels = append(classifierModels, opencodeClassifierModels()...)
	classifierModels = append(classifierModels, cloudflareWorkersAIClassifierModels()...)
	classifierModels = append(classifierModels, openAIClassifierModels()...)

	providers := map[ai.ProviderID]*providerCatalog{}
	for _, model := range allModels {
		entry := providers[model.Provider]
		if entry == nil {
			entry = &providerCatalog{chat: map[string]*ai.Model{}, image: map[string]*ai.Model{}, classifier: map[string]*ai.Model{}}
			providers[model.Provider] = entry
		}
		model.Type = ai.ModelTypeChat
		if _, ok := entry.chat[model.ID]; !ok {
			entry.chat[model.ID] = model
		}
	}
	if openRouterCatalog != nil {
		for _, model := range openRouterCatalog.images {
			applyImageInputMetadata(model)
			entry := providers[model.Provider]
			if entry == nil {
				entry = &providerCatalog{chat: map[string]*ai.Model{}, image: map[string]*ai.Model{}, classifier: map[string]*ai.Model{}}
				providers[model.Provider] = entry
			}
			if _, ok := entry.image[model.ID]; !ok {
				entry.image[model.ID] = model
			}
		}
	}
	for _, model := range classifierModels {
		entry := providers[model.Provider]
		if entry == nil {
			entry = &providerCatalog{chat: map[string]*ai.Model{}, image: map[string]*ai.Model{}, classifier: map[string]*ai.Model{}}
			providers[model.Provider] = entry
		}
		if _, ok := entry.classifier[model.ID]; !ok {
			entry.classifier[model.ID] = model
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
	fmt.Printf("Generated %s (%d providers, %d chat / %d image / %d classifier models)\n",
		outPath, len(providers), len(allModels), catalogImageCount(providers), catalogClassifierCount(providers))

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

// hasProviderModel reports whether the catalog already holds provider/id
// (pi: allModels.some(...) guards on the push blocks).
func hasProviderModel(models []*ai.Model, provider ai.ProviderID, id string) bool {
	for _, model := range models {
		if model.Provider == provider && model.ID == id {
			return true
		}
	}
	return false
}

// deepseekCompat is pi's deepseekCompat (openai-completions thinking format).
func deepseekCompat() *ai.OpenAICompletionsCompat {
	return &ai.OpenAICompletionsCompat{
		RequiresReasoningContentOnAssistantMessages: lo.ToPtr(true),
		ThinkingFormat: lo.ToPtr(ai.ThinkingFormatDeepSeek),
	}
}

// deepseekV4Models is pi's hardcoded DeepSeek V4.1 Flash / V4 Pro catalog
// (renamed from DeepSeek V4 Flash in v0.86.0: "update DeepSeek Flash
// catalog"; DeepSeek also offers time-based off-peak rates, which the cost
// schema cannot represent yet).
func deepseekV4Models() []*ai.Model {
	return []*ai.Model{
		{
			ID:               "deepseek-flash",
			Name:             "DeepSeek V4.1 Flash",
			API:              ai.ApiOpenAICompletions,
			BaseURL:          "https://api.deepseek.com",
			Provider:         ai.ProviderDeepSeek,
			Reasoning:        true,
			ThinkingLevelMap: func() ai.ThinkingLevelMap { m := cloneThinkingLevelMap(deepseekV4FlashThinkingLevelMap); return m }(),
			Input:            []ai.ModelInput{ai.ModelInputText, ai.ModelInputImage},
			Cost:             ai.ModelCost{ModelCostRates: ai.ModelCostRates{Input: 0.3, Output: 1.2, CacheRead: 0.006, CacheWrite: 0}},
			ContextWindow:    1000000,
			MaxTokens:        384000,
			Compat:           deepseekCompat(),
		},
		{
			ID:            "deepseek-v4-pro",
			Name:          "DeepSeek V4 Pro",
			API:           ai.ApiOpenAICompletions,
			BaseURL:       "https://api.deepseek.com",
			Provider:      ai.ProviderDeepSeek,
			Reasoning:     true,
			Input:         []ai.ModelInput{ai.ModelInputText},
			Cost:          ai.ModelCost{ModelCostRates: ai.ModelCostRates{Input: 1.32, Output: 3.96, CacheRead: 0.044, CacheWrite: 0}},
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

func printStatistics(allModels []*ai.Model, providers map[ai.ProviderID]*providerCatalog) {
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
	fmt.Printf("  Image models: %d\n", catalogImageCount(providers))
	fmt.Printf("  Classifier models: %d\n", catalogClassifierCount(providers))
	providerIDs := make([]string, 0, len(providers))
	for providerID := range providers {
		providerIDs = append(providerIDs, string(providerID))
	}
	sort.Strings(providerIDs)
	for _, providerID := range providerIDs {
		entry := providers[ai.ProviderID(providerID)]
		fmt.Printf("  %s: %d chat / %d image / %d classifier models\n",
			providerID, len(entry.chat), len(entry.image), len(entry.classifier))
	}
}

func catalogImageCount(providers map[ai.ProviderID]*providerCatalog) int {
	n := 0
	for _, entry := range providers {
		n += len(entry.image)
	}
	return n
}

func catalogClassifierCount(providers map[ai.ProviderID]*providerCatalog) int {
	n := 0
	for _, entry := range providers {
		n += len(entry.classifier)
	}
	return n
}

// providerCatalog keeps the three per-provider catalogs apart (pi:
// ProviderCatalog in generate-models.ts).
type providerCatalog struct {
	chat       map[string]*ai.Model
	image      map[string]*ai.Model
	classifier map[string]*ai.Model
}

// opencodeClassifierModels are the hardcoded OpenCode Zen Jev classifiers
// (pi: OPENCODE_CLASSIFIER_MODELS). Neither the /zen/v1/models listing nor
// models.dev carries metadata for them.
// https://opencode.ai/docs/zen
func opencodeClassifierModels() []*ai.Model {
	return []*ai.Model{
		{
			Type:          ai.ModelTypeClassifier,
			ID:            "jev-1.13",
			Name:          "Jev 1.13",
			API:           ai.ClassifierApiTypesafeSystemOne,
			Provider:      ai.ProviderOpenCode,
			BaseURL:       "https://opencode.ai/zen/v1",
			Input:         []ai.ModelInput{ai.ModelInputText},
			Cost:          ai.ModelCost{ModelCostRates: ai.ModelCostRates{Input: 0.042}},
			ContextWindow: 32000,
		},
		{
			Type:          ai.ModelTypeClassifier,
			ID:            "jev-1.13-free",
			Name:          "Jev 1.13 Free",
			API:           ai.ClassifierApiTypesafeSystemOne,
			Provider:      ai.ProviderOpenCode,
			BaseURL:       "https://opencode.ai/zen/v1",
			Input:         []ai.ModelInput{ai.ModelInputText},
			Cost:          ai.ModelCost{},
			ContextWindow: 32000,
		},
	}
}

// cloudflareWorkersAIClassifierModels are the hardcoded Workers AI System One
// classifiers (pi: CLOUDFLARE_WORKERS_AI_CLASSIFIER_MODELS). Workers AI has no
// unauthenticated catalog and models.dev does not list its System One models
// yet; Cloudflare publishes pricing only in the dashboard.
// https://developers.cloudflare.com/ai/models/typesafe/jev/
func cloudflareWorkersAIClassifierModels() []*ai.Model {
	return []*ai.Model{
		// Cloudflare-hosted Clef decision models. They accept images, but
		// classifier contexts carry text or JSON state only, so the catalog
		// advertises text.
		// Pricing: https://developers.cloudflare.com/workers-ai/models/clef/
		// and https://developers.cloudflare.com/workers-ai/models/clef-flash/
		{
			Type:     ai.ModelTypeClassifier,
			ID:       "@cf/cloudflare/clef",
			Name:     "Clef",
			API:      ai.ClassifierApiCloudflareWorkersAI,
			Provider: ai.ProviderCloudflareWorkersAI,
			// pi: CLOUDFLARE_WORKERS_AI_REST_BASE_URL — the System One
			// envelope hangs off the REST base without the /v1 segment.
			BaseURL:       "https://api.cloudflare.com/client/v4/accounts/{CLOUDFLARE_ACCOUNT_ID}/ai",
			Input:         []ai.ModelInput{ai.ModelInputText},
			Cost:          ai.ModelCost{ModelCostRates: ai.ModelCostRates{Input: 0.24}},
			ContextWindow: 65536,
		},
		{
			Type:          ai.ModelTypeClassifier,
			ID:            "@cf/cloudflare/clef-flash",
			Name:          "Clef Flash",
			API:           ai.ClassifierApiCloudflareWorkersAI,
			Provider:      ai.ProviderCloudflareWorkersAI,
			BaseURL:       "https://api.cloudflare.com/client/v4/accounts/{CLOUDFLARE_ACCOUNT_ID}/ai",
			Input:         []ai.ModelInput{ai.ModelInputText},
			Cost:          ai.ModelCost{ModelCostRates: ai.ModelCostRates{Input: 0.09}},
			ContextWindow: 65536,
		},
		{
			Type:     ai.ModelTypeClassifier,
			ID:       "typesafe/jev",
			Name:     "Jev",
			API:      ai.ClassifierApiCloudflareWorkersAI,
			Provider: ai.ProviderCloudflareWorkersAI,
			// pi: CLOUDFLARE_WORKERS_AI_REST_BASE_URL — the System One
			// envelope hangs off the REST base without the /v1 segment.
			BaseURL:       "https://api.cloudflare.com/client/v4/accounts/{CLOUDFLARE_ACCOUNT_ID}/ai",
			Input:         []ai.ModelInput{ai.ModelInputText},
			Cost:          ai.ModelCost{},
			ContextWindow: 32000,
		},
	}
}

// openAIClassifierModels are the hardcoded OpenAI Decisions classifiers (pi:
// OPENAI_CLASSIFIER_MODELS). The model needs an API key and is hidden from
// availability while openai uses Sign in with ChatGPT.
// https://developers.openai.com/api/docs/guides/decisions
func openAIClassifierModels() []*ai.Model {
	return []*ai.Model{
		// OpenAI Decisions API (public beta): gpt-6-luna is its only model.
		// It bills input tokens only, with the same long-context multiplier
		// as chat requests. Only API keys work: Sign in with ChatGPT tokens
		// are rejected on /v1/decisions, and the Codex backend has no
		// Decisions route. The endpoint rejects inputs above 922K tokens (the
		// model's documented maximum input), but requests running longer than
		// about five seconds, currently above roughly 600K input tokens, fail
		// with a gateway timeout.
		{
			Type:          ai.ModelTypeClassifier,
			ID:            "gpt-6-luna",
			Name:          "GPT-6 Luna",
			API:           ai.ClassifierApiOpenAIDecisions,
			Provider:      ai.ProviderOpenAI,
			BaseURL:       "https://api.openai.com/v1",
			Input:         []ai.ModelInput{ai.ModelInputText, ai.ModelInputImage},
			Cost:          withOpenAiLongContextPricing(ai.ModelCost{ModelCostRates: ai.ModelCostRates{Input: 0.1, Output: 0, CacheRead: 0, CacheWrite: 0}}),
			ContextWindow: 922000,
		},
	}
}
