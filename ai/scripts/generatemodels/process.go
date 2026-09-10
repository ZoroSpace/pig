// pi: generate-models.ts — models.dev catalog processing. Only providers
// whose wire API pig has implemented are ported (openai-completions /
// anthropic-messages). Providers that pi serves through unported APIs
// (openai-responses, google-generative-ai, google-vertex,
// bedrock-converse-stream, mistral-conversations, openai-codex-responses) and
// providers explicitly out of pig's Phase 1 scope (github-copilot,
// cloudflare-workers-ai/-gateway, opencode/opencode-go, qwen-token-plan,
// xiaomi) are skipped with the pi processing noted in comments.
package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/ZoroSpace/pig/ai"
	"github.com/samber/lo"
)

const togetherBaseURL = "https://api.together.ai/v1"

// Together compat variants (pi: TOGETHER_BASE_COMPAT and friends).
var togetherBaseCompat = &ai.OpenAICompletionsCompat{
	SupportsStore:              lo.ToPtr(false),
	SupportsDeveloperRole:      lo.ToPtr(false),
	SupportsReasoningEffort:    lo.ToPtr(false),
	MaxTokensField:             lo.ToPtr(ai.MaxTokensPlain),
	SupportsStrictMode:         lo.ToPtr(false),
	SupportsLongCacheRetention: lo.ToPtr(false),
}

var togetherToggleReasoningCompat = withCompat(togetherBaseCompat, func(c *ai.OpenAICompletionsCompat) {
	c.ThinkingFormat = lo.ToPtr(ai.ThinkingFormatTogether)
})

var togetherReasoningEffortCompat = withCompat(togetherBaseCompat, func(c *ai.OpenAICompletionsCompat) {
	c.SupportsReasoningEffort = lo.ToPtr(true)
	c.ThinkingFormat = lo.ToPtr(ai.ThinkingFormatOpenAI)
})

var togetherToggleReasoningEffortCompat = withCompat(togetherToggleReasoningCompat, func(c *ai.OpenAICompletionsCompat) {
	c.SupportsReasoningEffort = lo.ToPtr(true)
})

var togetherReasoningOnlyModels = stringSet("deepseek-ai/DeepSeek-R1", "MiniMaxAI/MiniMax-M2.7")
var togetherReasoningEffortModels = stringSet("openai/gpt-oss-20b", "openai/gpt-oss-120b")
var togetherToggleReasoningEffortModels = stringSet("deepseek-ai/DeepSeek-V4-Pro")

var togetherFixedReasoningLevelMap = ai.ThinkingLevelMap{
	ai.ModelThinkingOff:     nil,
	ai.ModelThinkingMinimal: nil,
	ai.ModelThinkingLow:     nil,
	ai.ModelThinkingMedium:  nil,
}
var togetherReasoningEffortLevelMap = ai.ThinkingLevelMap{
	ai.ModelThinkingOff:     nil,
	ai.ModelThinkingMinimal: nil,
}
var togetherDeepseekV4ThinkingLevelMap = ai.ThinkingLevelMap{
	ai.ModelThinkingMinimal: nil,
	ai.ModelThinkingLow:     nil,
	ai.ModelThinkingMedium:  nil,
	ai.ModelThinkingHigh:    lo.ToPtr("high"),
	ai.ModelThinkingXHigh:   nil,
}
var togetherToggleReasoningLevelMap = ai.ThinkingLevelMap{
	ai.ModelThinkingMinimal: nil,
	ai.ModelThinkingLow:     nil,
	ai.ModelThinkingMedium:  nil,
}

// copyOpenAICompat shallow-copies a compat (pointer fields stay shared; pi
// spreads compat objects per model, so model-own copies must not alias the
// const originals).
func copyOpenAICompat(c *ai.OpenAICompletionsCompat) *ai.OpenAICompletionsCompat {
	if c == nil {
		return nil
	}
	cp := *c
	return &cp
}

func copyAnthropicCompat(c *ai.AnthropicMessagesCompat) *ai.AnthropicMessagesCompat {
	if c == nil {
		return nil
	}
	cp := *c
	return &cp
}

// withCompat clones c, applies fn to the clone, and returns it.
func withCompat(c *ai.OpenAICompletionsCompat, fn func(*ai.OpenAICompletionsCompat)) *ai.OpenAICompletionsCompat {
	cp := copyOpenAICompat(c)
	fn(cp)
	return cp
}

func cloneThinkingLevelMap(m ai.ThinkingLevelMap) ai.ThinkingLevelMap {
	cp := ai.ThinkingLevelMap{}
	for k, v := range m {
		cp[k] = v
	}
	return cp
}

// getTogetherCompat (pi: getTogetherCompat).
func getTogetherCompat(modelID string, reasoning bool) *ai.OpenAICompletionsCompat {
	if !reasoning {
		return copyOpenAICompat(togetherBaseCompat)
	}
	if togetherReasoningEffortModels[modelID] {
		return copyOpenAICompat(togetherReasoningEffortCompat)
	}
	if togetherToggleReasoningEffortModels[modelID] {
		return copyOpenAICompat(togetherToggleReasoningEffortCompat)
	}
	if togetherReasoningOnlyModels[modelID] {
		return copyOpenAICompat(togetherBaseCompat)
	}
	return copyOpenAICompat(togetherToggleReasoningCompat)
}

// getTogetherThinkingLevelMap (pi: getTogetherThinkingLevelMap).
func getTogetherThinkingLevelMap(modelID string, reasoning bool) *ai.ThinkingLevelMap {
	if !reasoning {
		return nil
	}
	var m ai.ThinkingLevelMap
	switch {
	case togetherReasoningEffortModels[modelID]:
		m = cloneThinkingLevelMap(togetherReasoningEffortLevelMap)
	case togetherToggleReasoningEffortModels[modelID]:
		m = cloneThinkingLevelMap(togetherDeepseekV4ThinkingLevelMap)
	case togetherReasoningOnlyModels[modelID]:
		m = cloneThinkingLevelMap(togetherFixedReasoningLevelMap)
	default:
		m = cloneThinkingLevelMap(togetherToggleReasoningLevelMap)
	}
	return &m
}

// processAnthropicModels (pi: loadModelsDevData anthropic block).
func processAnthropicModels(data ModelsDevCatalog) []*ai.Model {
	provider := data["anthropic"]
	if provider == nil || provider.Models == nil {
		return nil
	}
	var models []*ai.Model
	for modelID, m := range provider.Models {
		if !boolVal(m.ToolCall) {
			continue
		}
		models = append(models, &ai.Model{
			ID:            modelID,
			Name:          orString(m.Name, modelID),
			API:           ai.ApiAnthropicMessages,
			Provider:      ai.ProviderAnthropic,
			BaseURL:       "https://api.anthropic.com",
			Reasoning:     boolVal(m.Reasoning),
			Input:         inputOf(m),
			Cost:          modelsDevCostOf(m.Cost),
			ContextWindow: int64Or(limitContext(m), 4096),
			MaxTokens:     int64Or(limitOutput(m), 4096),
		})
		recordModelsDevReasoningOptions("anthropic", modelID, m)
	}
	return models
}

// processGroqModels (pi: loadModelsDevData groq block).
func processGroqModels(data ModelsDevCatalog) []*ai.Model {
	provider := data["groq"]
	if provider == nil || provider.Models == nil {
		return nil
	}
	var models []*ai.Model
	for modelID, m := range provider.Models {
		if !boolVal(m.ToolCall) {
			continue
		}
		models = append(models, &ai.Model{
			ID:            modelID,
			Name:          orString(m.Name, modelID),
			API:           ai.ApiOpenAICompletions,
			Provider:      ai.ProviderGroq,
			BaseURL:       "https://api.groq.com/openai/v1",
			Reasoning:     boolVal(m.Reasoning),
			Input:         inputOf(m),
			Cost:          modelsDevCostOf(m.Cost),
			ContextWindow: int64Or(limitContext(m), 4096),
			MaxTokens:     int64Or(limitOutput(m), 4096),
		})
		recordModelsDevReasoningOptions("groq", modelID, m)
	}
	return models
}

// processCerebrasModels (pi: loadModelsDevData cerebras block).
func processCerebrasModels(data ModelsDevCatalog) []*ai.Model {
	provider := data["cerebras"]
	if provider == nil || provider.Models == nil {
		return nil
	}
	var models []*ai.Model
	for modelID, m := range provider.Models {
		if !boolVal(m.ToolCall) {
			continue
		}
		models = append(models, &ai.Model{
			ID:            modelID,
			Name:          orString(m.Name, modelID),
			API:           ai.ApiOpenAICompletions,
			Provider:      ai.ProviderCerebras,
			BaseURL:       "https://api.cerebras.ai/v1",
			Reasoning:     boolVal(m.Reasoning),
			Input:         inputOf(m),
			Cost:          modelsDevCostOf(m.Cost),
			ContextWindow: int64Or(limitContext(m), 4096),
			MaxTokens:     int64Or(limitOutput(m), 4096),
		})
		recordModelsDevReasoningOptions("cerebras", modelID, m)
	}
	return models
}

type zaiVariant struct {
	source   string
	provider string
	baseURL  string
}

// processZaiModels (pi: processZaiModels) — z.ai coding-plan and its China
// twin. Provider keys are the models.dev sources; the exposed provider ids are
// "zai" / "zai-coding-cn".
var zaiVariants = []zaiVariant{
	{source: "zai-coding-plan", provider: "zai", baseURL: "https://api.z.ai/api/coding/paas/v4"},
	{source: "zhipuai-coding-plan", provider: "zai-coding-cn", baseURL: "https://open.bigmodel.cn/api/coding/paas/v4"},
}

func processZaiModels(data ModelsDevCatalog) []*ai.Model {
	var models []*ai.Model
	for _, variant := range zaiVariants {
		provider := data[variant.source]
		if provider == nil || provider.Models == nil {
			continue
		}
		for modelID, m := range provider.Models {
			if !boolVal(m.ToolCall) {
				continue
			}

			var thinkingLevelMap *ai.ThinkingLevelMap
			if tlm := getEffortThinkingLevelMap(m.ReasoningOptions); tlm != nil {
				thinkingLevelMap = tlm
			}
			isGLM52 := modelID == "glm-5.2" || modelID == "glm-5.2-highspeed"
			if thinkingLevelMap != nil && isGLM52 {
				(*thinkingLevelMap)[ai.ModelThinkingOff] = lo.ToPtr("none")
			}
			supportsReasoningEffort := thinkingLevelMap != nil

			referenceCost := m.Cost
			if zai := data["zai"]; zai != nil && zai.Models != nil {
				if zaiModel := zai.Models[modelID]; zaiModel != nil && zaiModel.Cost != nil {
					referenceCost = zaiModel.Cost
				}
			}

			compat := &ai.OpenAICompletionsCompat{
				SupportsDeveloperRole: lo.ToPtr(false),
				ThinkingFormat:        lo.ToPtr(ai.ThinkingFormatZai),
			}
			if supportsReasoningEffort {
				compat.SupportsReasoningEffort = lo.ToPtr(true)
			}
			if !zaiToolStreamUnsupportedModels[modelID] {
				compat.ZaiToolStream = lo.ToPtr(true)
			}

			model := &ai.Model{
				ID:            modelID,
				Name:          orString(m.Name, modelID),
				API:           ai.ApiOpenAICompletions,
				Provider:      ai.ProviderID(variant.provider),
				BaseURL:       variant.baseURL,
				Reasoning:     boolVal(m.Reasoning),
				Input:         inputOf(m),
				Cost:          modelsDevCostOf(referenceCost),
				Compat:        compat,
				ContextWindow: int64Or(limitContext(m), 4096),
				MaxTokens:     int64Or(limitOutput(m), 4096),
			}
			if thinkingLevelMap != nil {
				model.ThinkingLevelMap = *thinkingLevelMap
			}
			models = append(models, model)
			recordModelsDevReasoningOptions(variant.provider, modelID, m)
		}
	}
	return models
}

// processBasetenModels (pi: processBasetenModels).
func processBasetenModels(provider *ModelsDevProvider) []*ai.Model {
	if provider == nil || provider.Models == nil {
		return nil
	}
	const baseURL = "https://inference.baseten.co/v1"
	baseCompat := &ai.OpenAICompletionsCompat{
		SupportsStore:              lo.ToPtr(false),
		SupportsDeveloperRole:      lo.ToPtr(false),
		SupportsReasoningEffort:    lo.ToPtr(false),
		SupportsUsageInStreaming:   lo.ToPtr(true),
		MaxTokensField:             lo.ToPtr(ai.MaxTokensPlain),
		SupportsStrictMode:         lo.ToPtr(true),
		SupportsLongCacheRetention: lo.ToPtr(false),
	}
	reasoningEffortCompat := withCompat(baseCompat, func(c *ai.OpenAICompletionsCompat) {
		c.SupportsReasoningEffort = lo.ToPtr(true)
		c.ThinkingFormat = lo.ToPtr(ai.ThinkingFormatOpenAI)
	})
	toggleReasoningCompat := withCompat(baseCompat, func(c *ai.OpenAICompletionsCompat) {
		c.ThinkingFormat = lo.ToPtr(ai.ThinkingFormatBaseten)
		c.ChatTemplateArgs = map[string]ai.ChatTemplateKwargValue{
			"enable_thinking": {Value: ai.ChatTemplateKwargVar{Var: ai.ChatTemplateVarThinkingEnabled}},
		}
	})
	toggleReasoningEffortCompat := withCompat(reasoningEffortCompat, func(c *ai.OpenAICompletionsCompat) {
		c.ThinkingFormat = lo.ToPtr(ai.ThinkingFormatBaseten)
		c.ChatTemplateArgs = map[string]ai.ChatTemplateKwargValue{
			"enable_thinking": {Value: ai.ChatTemplateKwargVar{Var: ai.ChatTemplateVarThinkingEnabled}},
		}
	})
	toggleThinkingLevelMap := ai.ThinkingLevelMap{
		ai.ModelThinkingOff:     lo.ToPtr("off"),
		ai.ModelThinkingMinimal: nil,
		ai.ModelThinkingLow:     nil,
		ai.ModelThinkingMedium:  nil,
		ai.ModelThinkingHigh:    lo.ToPtr("high"),
		ai.ModelThinkingXHigh:   nil,
		ai.ModelThinkingMax:     nil,
	}
	glm52ThinkingLevelMap := ai.ThinkingLevelMap{
		ai.ModelThinkingOff:     lo.ToPtr("none"),
		ai.ModelThinkingMinimal: nil,
		ai.ModelThinkingLow:     nil,
		ai.ModelThinkingMedium:  nil,
		ai.ModelThinkingHigh:    lo.ToPtr("high"),
		ai.ModelThinkingXHigh:   nil,
		ai.ModelThinkingMax:     lo.ToPtr("max"),
	}

	var models []*ai.Model
	for modelID, m := range provider.Models {
		if m.Status == "deprecated" {
			continue
		}
		reasoning := boolVal(m.Reasoning)
		reasoningOptions := m.ReasoningOptions
		isGLM52 := modelID == "zai-org/GLM-5.2" || modelID == "zai-org/GLM-5.2-Fast"
		supportsToggle := reasoningOptionsHasType(reasoningOptions, "toggle") || isGLM52
		supportsEffort := reasoningOptionsHasType(reasoningOptions, "effort") || isGLM52

		var compat *ai.OpenAICompletionsCompat
		switch {
		case supportsToggle && supportsEffort:
			compat = copyOpenAICompat(toggleReasoningEffortCompat)
		case supportsToggle:
			compat = copyOpenAICompat(toggleReasoningCompat)
		case supportsEffort:
			compat = copyOpenAICompat(reasoningEffortCompat)
		default:
			compat = copyOpenAICompat(baseCompat)
		}

		var thinkingLevelMap *ai.ThinkingLevelMap
		switch {
		case isGLM52:
			m2 := cloneThinkingLevelMap(glm52ThinkingLevelMap)
			thinkingLevelMap = &m2
		case supportsToggle:
			m2 := cloneThinkingLevelMap(toggleThinkingLevelMap)
			thinkingLevelMap = &m2
		default:
			if tlm := getEffortThinkingLevelMap(reasoningOptions); tlm != nil {
				thinkingLevelMap = tlm
			}
		}

		model := &ai.Model{
			ID:            modelID,
			Name:          orString(m.Name, modelID),
			API:           ai.ApiOpenAICompletions,
			Provider:      ai.ProviderBaseten,
			BaseURL:       baseURL,
			Reasoning:     reasoning,
			Input:         inputOf(m),
			Cost:          modelsDevCostOf(m.Cost),
			Compat:        compat,
			ContextWindow: int64Or(limitContext(m), 4096),
			MaxTokens:     int64Or(limitOutput(m), 4096),
		}
		if thinkingLevelMap != nil {
			model.ThinkingLevelMap = *thinkingLevelMap
		}
		models = append(models, model)
	}
	return models
}

func reasoningOptionsHasType(options []ModelsDevReasoningOption, t string) bool {
	for _, o := range options {
		if o.Type == t {
			return true
		}
	}
	return false
}

// processFireworksModels (pi: processFireworksModels). models.dev key is
// "fireworks-ai"; the exposed provider id is "fireworks".
func processFireworksModels(provider *ModelsDevProvider) []*ai.Model {
	if provider == nil || provider.Models == nil {
		return nil
	}
	anthropicCompat := &ai.AnthropicMessagesCompat{
		SendSessionAffinityHeaders:      lo.ToPtr(true),
		SupportsEagerToolInputStreaming: lo.ToPtr(false),
		SupportsCacheControlOnTools:     lo.ToPtr(false),
		SupportsLongCacheRetention:      lo.ToPtr(false),
	}
	openAICompat := &ai.OpenAICompletionsCompat{
		SupportsStore:              lo.ToPtr(false),
		SupportsDeveloperRole:      lo.ToPtr(false),
		SendSessionAffinityHeaders: lo.ToPtr(true),
		SupportsLongCacheRetention: lo.ToPtr(false),
	}
	kimiK3Compat := withCompat(openAICompat, func(c *ai.OpenAICompletionsCompat) {
		c.RequiresReasoningContentOnAssistantMessages = lo.ToPtr(true)
		c.ThinkingFormat = lo.ToPtr(ai.ThinkingFormatOpenAI)
		c.DeferredToolsMode = lo.ToPtr(ai.DeferredToolsKimi)
	})

	var models []*ai.Model
	for modelID, m := range provider.Models {
		if !boolVal(m.ToolCall) {
			continue
		}
		common := ai.Model{
			ID:            modelID,
			Name:          orString(m.Name, modelID),
			Provider:      ai.ProviderFireworks,
			Reasoning:     boolVal(m.Reasoning),
			Input:         inputOf(m),
			Cost:          modelsDevCostOf(m.Cost),
			ContextWindow: int64Or(limitContext(m), 4096),
			MaxTokens:     int64Or(limitOutput(m), 4096),
		}
		switch {
		case strings.Contains(modelID, "glm-5p2"):
			common.API = ai.ApiOpenAICompletions
			common.BaseURL = "https://api.fireworks.ai/inference/v1"
			common.Compat = copyOpenAICompat(openAICompat)
		case strings.Contains(modelID, "kimi-k3"):
			common.API = ai.ApiOpenAICompletions
			common.BaseURL = "https://api.fireworks.ai/inference/v1"
			common.Compat = copyOpenAICompat(kimiK3Compat)
		default:
			// Fireworks Anthropic-compatible API — SDK appends /v1/messages.
			common.API = ai.ApiAnthropicMessages
			common.BaseURL = "https://api.fireworks.ai/inference"
			common.Compat = copyAnthropicCompat(anthropicCompat)
		}
		models = append(models, &common)
		recordModelsDevReasoningOptions("fireworks", modelID, m)
	}
	return models
}

// processNvidiaModels (pi: loadModelsDevData nvidia block). Live model ids
// come from the NVIDIA NIM API.
func processNvidiaModels(data ModelsDevCatalog, nvidiaNimModelIDs map[string]string) []*ai.Model {
	provider := data["nvidia"]
	if provider == nil || provider.Models == nil {
		return nil
	}
	var models []*ai.Model
	for modelID, m := range provider.Models {
		if !boolVal(m.ToolCall) {
			continue
		}
		if m.Modalities == nil || !containsString(m.Modalities.Input, "text") {
			continue
		}
		if m.Modalities == nil || !containsString(m.Modalities.Output, "text") {
			continue
		}

		liveModelID := nvidiaNimModelIDs[modelID]
		if liveModelID == "" {
			liveModelID = nvidiaNimModelIDs[normalizeNvidiaModelId(modelID)]
		}
		if liveModelID == "" {
			continue
		}
		if nvidiaNimUnsupportedModels[liveModelID] {
			continue
		}

		models = append(models, &ai.Model{
			ID:            liveModelID,
			Name:          orString(m.Name, liveModelID),
			API:           ai.ApiOpenAICompletions,
			Provider:      ai.ProviderNVIDIA,
			BaseURL:       nvidiaBaseURL,
			Headers:       nvidiaHeaders,
			Reasoning:     boolVal(m.Reasoning),
			Input:         inputOf(m),
			Cost:          modelsDevCostOf(m.Cost),
			Compat:        copyOpenAICompat(nvidiaOpenAICompat),
			ContextWindow: int64Or(limitContext(m), 4096),
			MaxTokens:     int64Or(limitOutput(m), 4096),
		})
		recordModelsDevReasoningOptions("nvidia", liveModelID, m)
	}
	return models
}

// processTogetherModels (pi: loadModelsDevData together block). The models.dev
// key is "togetherai"; pi also accepts "together" and "together-ai".
func processTogetherModels(data ModelsDevCatalog) []*ai.Model {
	provider := data["together"]
	if provider == nil {
		provider = data["togetherai"]
	}
	if provider == nil {
		provider = data["together-ai"]
	}
	if provider == nil || provider.Models == nil {
		return nil
	}
	var models []*ai.Model
	for modelID, m := range provider.Models {
		if !boolVal(m.ToolCall) {
			continue
		}
		if m.Status == "deprecated" {
			continue
		}
		reasoning := boolVal(m.Reasoning)
		model := &ai.Model{
			ID:            modelID,
			Name:          orString(m.Name, modelID),
			API:           ai.ApiOpenAICompletions,
			Provider:      ai.ProviderTogether,
			BaseURL:       togetherBaseURL,
			Reasoning:     reasoning,
			Input:         inputOf(m),
			Cost:          modelsDevCostOf(m.Cost),
			Compat:        getTogetherCompat(modelID, reasoning),
			ContextWindow: int64Or(limitContext(m), 4096),
			MaxTokens:     int64Or(limitOutput(m), 4096),
		}
		if tlm := getTogetherThinkingLevelMap(modelID, reasoning); tlm != nil {
			model.ThinkingLevelMap = *tlm
		}
		models = append(models, model)
		recordModelsDevReasoningOptions("together", modelID, m)
	}
	return models
}

type minimaxVariant struct {
	key      string
	provider ai.ProviderID
	baseURL  string
}

// processMinimaxModels (pi: loadModelsDevData minimax block) — MiniMax's
// Anthropic-compatible API.
var minimaxVariants = []minimaxVariant{
	{key: "minimax", provider: ai.ProviderMinimax, baseURL: "https://api.minimax.io/anthropic"},
	{key: "minimax-cn", provider: ai.ProviderMinimaxCN, baseURL: "https://api.minimaxi.com/anthropic"},
}

func processMinimaxModels(data ModelsDevCatalog) []*ai.Model {
	var models []*ai.Model
	for _, variant := range minimaxVariants {
		provider := data[variant.key]
		if provider == nil || provider.Models == nil {
			continue
		}
		for modelID, m := range provider.Models {
			if !boolVal(m.ToolCall) {
				continue
			}
			models = append(models, &ai.Model{
				ID:            modelID,
				Name:          orString(m.Name, modelID),
				API:           ai.ApiAnthropicMessages,
				Provider:      variant.provider,
				BaseURL:       variant.baseURL,
				Reasoning:     boolVal(m.Reasoning),
				Input:         inputOf(m),
				Cost:          modelsDevCostOf(m.Cost),
				ContextWindow: int64Or(limitContext(m), 4096),
				MaxTokens:     int64Or(limitOutput(m), 4096),
			})
			recordModelsDevReasoningOptions(string(variant.provider), modelID, m)
		}
	}
	return models
}

// processKimiCodingModels (pi: loadModelsDevData kimi-for-coding block).
func processKimiCodingModels(data ModelsDevCatalog) []*ai.Model {
	provider := data["kimi-for-coding"]
	if provider == nil || provider.Models == nil {
		return nil
	}
	kimiModels := provider.Models
	_, hasCanonicalModel := kimiModels["kimi-for-coding"]
	kimiAliases := stringSet("k2p5", "k2p6", "k2p7")

	var models []*ai.Model
	for modelID, m := range kimiModels {
		if !boolVal(m.ToolCall) {
			continue
		}
		// models.dev may expose versioned aliases (e.g. k2p5/k2p6/k2p7).
		// Normalize aliases to the canonical model id and drop duplicates when
		// the canonical model exists.
		if kimiAliases[modelID] && hasCanonicalModel {
			continue
		}
		normalizedID := modelID
		normalizedName := orString(m.Name, modelID)
		if kimiAliases[modelID] {
			normalizedID = "kimi-for-coding"
			normalizedName = "Kimi For Coding"
		}
		isKimiK3 := normalizedID == "k3"
		allowEmptySignature := isKimiK3 || normalizedID == "kimi-for-coding"
		impliedCost := kimiCodingImpliedCosts[normalizedID]

		compat := &ai.AnthropicMessagesCompat{ForceAdaptiveThinking: lo.ToPtr(true)}
		if allowEmptySignature {
			compat.AllowEmptySignature = lo.ToPtr(true)
		}

		cost := modelsDevCostOf(m.Cost)
		if impliedCost.Input != 0 || impliedCost.Output != 0 || impliedCost.CacheRead != 0 || impliedCost.CacheWrite != 0 {
			if cost.Input == 0 {
				cost.Input = impliedCost.Input
			}
			if cost.Output == 0 {
				cost.Output = impliedCost.Output
			}
			if cost.CacheRead == 0 {
				cost.CacheRead = impliedCost.CacheRead
			}
			if cost.CacheWrite == 0 {
				cost.CacheWrite = impliedCost.CacheWrite
			}
		}

		models = append(models, &ai.Model{
			ID:            normalizedID,
			Name:          normalizedName,
			API:           ai.ApiAnthropicMessages,
			Provider:      ai.ProviderKimiCoding,
			BaseURL:       "https://api.kimi.com/coding",
			Compat:        compat,
			Reasoning:     isKimiK3 || boolVal(m.Reasoning),
			Input:         inputOf(m),
			Cost:          cost,
			ContextWindow: int64Or(limitContext(m), 4096),
			MaxTokens:     int64Or(limitOutput(m), 4096),
		})
		recordModelsDevReasoningOptions("kimi-coding", normalizedID, m)
	}
	return models
}

type moonshotVariant struct {
	key      string
	provider ai.ProviderID
	baseURL  string
}

// processMoonshotModels (pi: loadModelsDevData moonshot block).
var moonshotVariants = []moonshotVariant{
	{key: "moonshotai", provider: ai.ProviderMoonshotAI, baseURL: "https://api.moonshot.ai/v1"},
	{key: "moonshotai-cn", provider: ai.ProviderMoonshotAICN, baseURL: "https://api.moonshot.cn/v1"},
}

func processMoonshotModels(data ModelsDevCatalog) []*ai.Model {
	moonshotCompat := &ai.OpenAICompletionsCompat{
		SupportsStore:           lo.ToPtr(false),
		SupportsDeveloperRole:   lo.ToPtr(false),
		SupportsReasoningEffort: lo.ToPtr(false),
		MaxTokensField:          lo.ToPtr(ai.MaxTokensPlain),
		SupportsStrictMode:      lo.ToPtr(false),
		ThinkingFormat:          lo.ToPtr(ai.ThinkingFormatDeepSeek),
	}

	var models []*ai.Model
	for _, variant := range moonshotVariants {
		provider := data[variant.key]
		if provider == nil || provider.Models == nil {
			continue
		}
		for modelID, m := range provider.Models {
			if !boolVal(m.ToolCall) {
				continue
			}
			isKimiK3 := modelID == "kimi-k3"
			compat := copyOpenAICompat(moonshotCompat)
			if isKimiK3 {
				compat.RequiresReasoningContentOnAssistantMessages = lo.ToPtr(true)
				compat.DeferredToolsMode = lo.ToPtr(ai.DeferredToolsKimi)
				compat.ThinkingFormat = lo.ToPtr(ai.ThinkingFormatOpenAI)
				compat.SupportsReasoningEffort = lo.ToPtr(true)
			}
			cost := modelsDevCostOf(m.Cost)
			if isKimiK3 {
				if cost.Input == 0 {
					cost.Input = kimiK3Cost.Input
				}
				if cost.Output == 0 {
					cost.Output = kimiK3Cost.Output
				}
				if cost.CacheRead == 0 {
					cost.CacheRead = kimiK3Cost.CacheRead
				}
				if cost.CacheWrite == 0 {
					cost.CacheWrite = kimiK3Cost.CacheWrite
				}
			}
			models = append(models, &ai.Model{
				ID:            modelID,
				Name:          orString(m.Name, modelID),
				API:           ai.ApiOpenAICompletions,
				Provider:      variant.provider,
				BaseURL:       variant.baseURL,
				Reasoning:     isKimiK3 || boolVal(m.Reasoning),
				Input:         inputOf(m),
				Cost:          cost,
				ContextWindow: int64Or(limitContext(m), 4096),
				MaxTokens:     int64Or(limitOutput(m), 4096),
				Compat:        compat,
			})
			recordModelsDevReasoningOptions(string(variant.provider), modelID, m)
		}
	}
	return models
}

// processHuggingFaceModels (pi: loadModelsDevData huggingface block).
func processHuggingFaceModels(data ModelsDevCatalog) []*ai.Model {
	provider := data["huggingface"]
	if provider == nil || provider.Models == nil {
		return nil
	}
	var models []*ai.Model
	for modelID, m := range provider.Models {
		if !boolVal(m.ToolCall) {
			continue
		}
		models = append(models, &ai.Model{
			ID:        modelID,
			Name:      orString(m.Name, modelID),
			API:       ai.ApiOpenAICompletions,
			Provider:  ai.ProviderHuggingFace,
			BaseURL:   "https://router.huggingface.co/v1",
			Reasoning: boolVal(m.Reasoning),
			Input:     inputOf(m),
			Cost:      modelsDevCostOf(m.Cost),
			Compat: &ai.OpenAICompletionsCompat{
				SupportsDeveloperRole: lo.ToPtr(false),
			},
			ContextWindow: int64Or(limitContext(m), 4096),
			MaxTokens:     int64Or(limitOutput(m), 4096),
		})
		recordModelsDevReasoningOptions("huggingface", modelID, m)
	}
	return models
}

// loadModelsDevData fetches and processes the models.dev catalog, returning
// the tool-capable models for the ported providers (pi: loadModelsDevData).
func loadModelsDevData(ctx context.Context) ([]*ai.Model, error) {
	fmt.Println("Fetching models from models.dev API...")
	var data ModelsDevCatalog
	if err := httpGetJSON(ctx, modelsDevURL, &data); err != nil {
		return nil, err
	}

	var models []*ai.Model

	// pi also processes amazon-bedrock, google, google-vertex, openai
	// (openai-responses), xai (openai-responses), mistral
	// (mistral-conversations), cloudflare-workers-ai, cloudflare-ai-gateway,
	// github-copilot, opencode/opencode-go, qwen-token-plan, and xiaomi
	// blocks here; all are skipped (their wire APIs are unported in pig).

	nvidiaNimModelIDs := map[string]string{}
	if data["nvidia"] != nil && data["nvidia"].Models != nil {
		var err error
		nvidiaNimModelIDs, err = fetchNvidiaNimModelIds(ctx)
		if err != nil {
			if genOptions.strict {
				return nil, err
			}
			nvidiaNimModelIDs = map[string]string{}
		}
	}

	models = append(models, processAnthropicModels(data)...)
	models = append(models, processGroqModels(data)...)
	models = append(models, processCerebrasModels(data)...)
	models = append(models, processZaiModels(data)...)
	models = append(models, processFireworksModels(data["fireworks-ai"])...)
	models = append(models, processNvidiaModels(data, nvidiaNimModelIDs)...)
	models = append(models, processTogetherModels(data)...)
	models = append(models, processBasetenModels(data["baseten"])...)
	models = append(models, processMinimaxModels(data)...)
	models = append(models, processKimiCodingModels(data)...)
	models = append(models, processMoonshotModels(data)...)
	models = append(models, processHuggingFaceModels(data)...)

	fmt.Printf("Loaded %d tool-capable models from models.dev\n", len(models))
	return models, nil
}
