// pi: generate-models.ts — models.dev catalog processing. Ported for every
// provider whose wire API pig has implemented (openai-completions /
// anthropic-messages / openai-responses / google-generative-ai /
// bedrock-converse-stream). Providers that pi serves through unported APIs
// (mistral-conversations) are skipped with the pi processing noted in
// comments.
package main

import (
	"context"
	"fmt"
	"maps"
	"regexp"
	"sort"
	"strings"

	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/api"
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

// copyOpenAIResponsesCompat clones an OpenAIResponsesCompat (pi spreads
// compat objects with {...}); nil passes through.
func copyOpenAIResponsesCompat(c *ai.OpenAIResponsesCompat) *ai.OpenAIResponsesCompat {
	if c == nil {
		return nil
	}
	cp := *c
	return &cp
}

// cloneOpenAIResponsesCompat copies c (nil-safe).

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

// modelsDevOpenAIUnsupportedModelIDs lists models.dev ids OpenAI APIs do not
// accept (pi: MODELS_DEV_OPENAI_UNSUPPORTED_MODEL_IDS).
var modelsDevOpenAIUnsupportedModelIDs = stringSet("gpt-5.6")

// xaiResponsesCompat is the xAI Responses compat (pi: XAI_RESPONSES_COMPAT).
var xaiResponsesCompat = &ai.OpenAIResponsesCompat{
	SupportsLongCacheRetention: lo.ToPtr(false),
}

// processOpenAIModels (pi: loadModelsDevData openai block — the openai-responses
// catalog).
func processOpenAIModels(data ModelsDevCatalog) []*ai.Model {
	provider := data["openai"]
	if provider == nil || provider.Models == nil {
		return nil
	}
	var models []*ai.Model
	for modelID, m := range provider.Models {
		if !boolVal(m.ToolCall) {
			continue
		}
		// models.dev lists this alias, but it is not accepted by OpenAI APIs.
		if modelsDevOpenAIUnsupportedModelIDs[modelID] {
			continue
		}
		models = append(models, &ai.Model{
			ID:            modelID,
			Name:          orString(m.Name, modelID),
			API:           ai.ApiOpenAIResponses,
			Provider:      ai.ProviderOpenAI,
			BaseURL:       "https://api.openai.com/v1",
			Reasoning:     boolVal(m.Reasoning),
			Input:         inputOf(m),
			Cost:          modelsDevCostOf(m.Cost),
			ContextWindow: int64Or(limitContext(m), 4096),
			MaxTokens:     int64Or(limitOutput(m), 4096),
		})
		recordModelsDevReasoningOptions("openai", modelID, m)
	}
	return models
}

// processXaiModels (pi: loadModelsDevData xai block — the openai-responses
// catalog).
func processXaiModels(data ModelsDevCatalog) []*ai.Model {
	provider := data["xai"]
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
			API:           ai.ApiOpenAIResponses,
			Provider:      ai.ProviderXAI,
			BaseURL:       "https://api.x.ai/v1",
			Compat:        copyOpenAIResponsesCompat(xaiResponsesCompat),
			Reasoning:     boolVal(m.Reasoning),
			Input:         inputOf(m),
			Cost:          modelsDevCostOf(m.Cost),
			ContextWindow: int64Or(limitContext(m), 4096),
			MaxTokens:     int64Or(limitOutput(m), 4096),
		})
		recordModelsDevReasoningOptions("xai", modelID, m)
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

	// pi also processes the mistral (mistral-conversations) block here; it is
	// skipped (its wire API is unported in pig).

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
	models = append(models, processBedrockModels(data)...)
	models = append(models, processGoogleModels(data)...)
	models = append(models, processGoogleVertexModels(data)...)
	models = append(models, processOpenAIModels(data)...)
	models = append(models, processXaiModels(data)...)
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
	models = append(models, processCloudflareModels(data)...)
	models = append(models, processOpenCodeModels(data)...)
	models = append(models, processGitHubCopilotModels(data)...)
	models = append(models, processXiaomiModels(data)...)
	models = append(models, processQwenTokenPlanModels(data)...)

	fmt.Printf("Loaded %d tool-capable models from models.dev\n", len(models))
	return models, nil
}

// ---------------------------------------------------------------------------
// P2-T1 providers (pi: loadModelsDevData cloudflare / opencode /
// github-copilot / xiaomi / qwen-token-plan blocks; the cloudflare endpoint
// constants come from ai/api/cloudflare.go, pi imports src/api/cloudflare.ts).

// copilotStaticHeaders ride every github-copilot model (pi:
// COPILOT_STATIC_HEADERS).
var copilotStaticHeaders = map[string]string{
	"User-Agent":             "GitHubCopilotChat/0.35.0",
	"Editor-Version":         "vscode/1.107.0",
	"Editor-Plugin-Version":  "copilot-chat/0.35.0",
	"Copilot-Integration-Id": "vscode-chat",
}

// openCodeGoGlm52ThinkingLevelMap (pi: OPENCODE_GO_GLM52_THINKING_LEVEL_MAP).
var openCodeGoGlm52ThinkingLevelMap = ai.ThinkingLevelMap{
	ai.ModelThinkingOff:     nil,
	ai.ModelThinkingMinimal: nil,
	ai.ModelThinkingLow:     nil,
	ai.ModelThinkingMedium:  nil,
	ai.ModelThinkingHigh:    lo.ToPtr("high"),
	ai.ModelThinkingMax:     lo.ToPtr("max"),
}

// qwenTokenPlanFallbackThinkingLevelMap (pi:
// QWEN_TOKEN_PLAN_FALLBACK_THINKING_LEVEL_MAP).
var qwenTokenPlanFallbackThinkingLevelMap = ai.ThinkingLevelMap{
	ai.ModelThinkingMinimal: nil,
	ai.ModelThinkingLow:     nil,
	ai.ModelThinkingMedium:  nil,
	ai.ModelThinkingHigh:    lo.ToPtr("high"),
	ai.ModelThinkingXHigh:   nil,
	ai.ModelThinkingMax:     lo.ToPtr("max"),
}

// qwenTokenPlanReasoningEffortFallbackModelIDs (pi:
// QWEN_TOKEN_PLAN_REASONING_EFFORT_FALLBACK_MODEL_IDS).
var qwenTokenPlanReasoningEffortFallbackModelIDs = stringSet("glm-5", "glm-5.1")

// qwenTokenPlanExcludedModelIDs lists retired preview ids — models.dev may
// still list them after GA ships (pi: QWEN_TOKEN_PLAN_EXCLUDED_MODEL_IDS).
var qwenTokenPlanExcludedModelIDs = stringSet("qwen3.8-max-preview")

// qwenTokenPlanIndividualModelIDs is the QwenCloud Token Plan Individual
// text-model allowlist, verified 2026-09-03. Retired models remain excluded
// above even if the public catalog lags.
// https://docs.qwencloud.com/token-plan/personal/token-plan-personal-overview
// (pi: QWEN_TOKEN_PLAN_INDIVIDUAL_MODEL_IDS)
var qwenTokenPlanIndividualModelIDs = stringSet(
	"deepseek-v4-flash-0731",
	"deepseek-v4-pro",
	"deepseek-v4-pro-0813",
	"glm-5.2",
	"qwen3.6-flash",
	"qwen3.7-max",
	"qwen3.7-plus",
	"qwen3.8-flash",
	"qwen3.8-max",
)

// openCodeOpenAICompletionsLongCacheRetentionUnsupportedModels (pi:
// OPENCODE_OPENAI_COMPLETIONS_LONG_CACHE_RETENTION_UNSUPPORTED_MODELS).
var openCodeOpenAICompletionsLongCacheRetentionUnsupportedModels = stringSet(
	"opencode:deepseek-v4-flash",
	"opencode:deepseek-v4-pro",
	"opencode:kimi-k2.5",
	"opencode:kimi-k2.6",
	"opencode:minimax-m2.7",
	"opencode-go:kimi-k2.6",
)

// githubCopilotExtendedContextModels lists the "Models with extended
// capabilities" Copilot models supporting the extended 1 million token
// context window (pi: GITHUB_COPILOT_EXTENDED_CONTEXT_MODELS).
var githubCopilotExtendedContextModels = stringSet(
	"claude-fable-5",
	"claude-opus-4.6",
	"claude-opus-4.7",
	"claude-opus-4.8",
	"claude-opus-5",
	"claude-sonnet-4.6",
	"claude-sonnet-5",
	"gpt-5.3-codex",
	"gpt-5.4",
	"gpt-5.5",
)

// githubCopilotThinkingLevelOverrides narrows models.dev metadata over the
// authenticated GitHub Copilot /models endpoint (checked 2026-06-15; pi:
// GITHUB_COPILOT_THINKING_LEVEL_OVERRIDES).
var githubCopilotThinkingLevelOverrides = map[string]ai.ThinkingLevelMap{
	"claude-opus-4.7":   {ai.ModelThinkingMinimal: lo.ToPtr("low")},
	"claude-opus-4.8":   {ai.ModelThinkingMinimal: lo.ToPtr("low")},
	"claude-opus-5":     {ai.ModelThinkingMinimal: lo.ToPtr("low")},
	"claude-sonnet-4.6": {ai.ModelThinkingMinimal: lo.ToPtr("low"), ai.ModelThinkingMax: lo.ToPtr("max")},
}

// processCloudflareModels handles Workers AI and AI Gateway (pi:
// loadModelsDevData cloudflare-workers-ai / cloudflare-ai-gateway blocks).
func processCloudflareModels(data ModelsDevCatalog) []*ai.Model {
	var models []*ai.Model

	// Process Cloudflare Workers AI models
	if workersAI := data["cloudflare-workers-ai"]; workersAI != nil && workersAI.Models != nil {
		for modelID, m := range workersAI.Models {
			if !boolVal(m.ToolCall) {
				continue
			}
			models = append(models, &ai.Model{
				ID:            modelID,
				Name:          modelsDevName(m, modelID),
				API:           ai.ApiOpenAICompletions,
				Provider:      ai.ProviderCloudflareWorkersAI,
				BaseURL:       api.CloudflareWorkersAIBaseURL,
				Reasoning:     boolVal(m.Reasoning),
				Input:         inputOf(m),
				Cost:          modelsDevCostOf(m.Cost),
				Compat:        &ai.OpenAICompletionsCompat{SendSessionAffinityHeaders: lo.ToPtr(true)},
				ContextWindow: int64Or(limitContext(m), 4096),
				MaxTokens:     int64Or(limitOutput(m), 4096),
			})
			recordModelsDevReasoningOptions("cloudflare-workers-ai", modelID, m)
		}
	}

	// Process Cloudflare AI Gateway models
	cloudflareAIGatewayModelIDs := map[string]bool{}
	if gateway := data["cloudflare-ai-gateway"]; gateway != nil && gateway.Models != nil {
		for prefixedID, m := range gateway.Models {
			if !boolVal(m.ToolCall) {
				continue
			}
			slashIdx := strings.Index(prefixedID, "/")
			if slashIdx == -1 {
				continue
			}
			upstream := prefixedID[:slashIdx]
			nativeID := prefixedID[slashIdx+1:]

			var apiKind ai.Api
			var baseURL, id string
			switch upstream {
			case "openai":
				apiKind = ai.ApiOpenAIResponses
				baseURL = api.CloudflareAIGatewayOpenAIBaseURL
				id = nativeID
			case "anthropic":
				apiKind = ai.ApiAnthropicMessages
				baseURL = api.CloudflareAIGatewayAnthropicBaseURL
				id = nativeID
			case "workers-ai":
				apiKind = ai.ApiOpenAICompletions
				baseURL = api.CloudflareAIGatewayCompatBaseURL
				id = prefixedID
			default:
				continue
			}

			// Gateway passthroughs forward session affinity headers to
			// upstreams that use them for cache/routing affinity. pi's object
			// spread keeps whichever compat shape the later metadata merge
			// expects: anthropic-messages models take anthropic compat here
			// (forceAdaptiveThinking etc. merge into the same object).
			var compat ai.ModelCompat
			if upstream == "anthropic" {
				compat = &ai.AnthropicMessagesCompat{SendSessionAffinityHeaders: lo.ToPtr(true)}
			} else if upstream == "workers-ai" {
				compat = &ai.OpenAICompletionsCompat{SendSessionAffinityHeaders: lo.ToPtr(true)}
			}

			cloudflareAIGatewayModelIDs[id] = true
			models = append(models, &ai.Model{
				ID:            id,
				Name:          modelsDevName(m, id),
				API:           apiKind,
				Provider:      ai.ProviderCloudflareAIGateway,
				BaseURL:       baseURL,
				Reasoning:     boolVal(m.Reasoning),
				Input:         inputOf(m),
				Cost:          modelsDevCostOf(m.Cost),
				Compat:        compat,
				ContextWindow: int64Or(limitContext(m), 4096),
				MaxTokens:     int64Or(limitOutput(m), 4096),
			})
			recordModelsDevReasoningOptions("cloudflare-ai-gateway", id, m)
		}
	}

	// The gateway proxies Workers AI through its OpenAI-compatible /compat
	// endpoint, but models.dev may omit or intermittently drop those
	// `workers-ai/*` entries from the AI Gateway catalog. Mirror the Workers
	// AI catalog under the documented prefix so the gateway keeps its
	// OpenAI-compatible models stable.
	if workersAI := data["cloudflare-workers-ai"]; workersAI != nil && workersAI.Models != nil {
		for modelID, m := range workersAI.Models {
			if !boolVal(m.ToolCall) {
				continue
			}
			id := "workers-ai/" + modelID
			if cloudflareAIGatewayModelIDs[id] {
				continue
			}
			cloudflareAIGatewayModelIDs[id] = true

			models = append(models, &ai.Model{
				ID:            id,
				Name:          modelsDevName(m, id),
				API:           ai.ApiOpenAICompletions,
				Provider:      ai.ProviderCloudflareAIGateway,
				BaseURL:       api.CloudflareAIGatewayCompatBaseURL,
				Reasoning:     boolVal(m.Reasoning),
				Input:         inputOf(m),
				Cost:          modelsDevCostOf(m.Cost),
				Compat:        &ai.OpenAICompletionsCompat{SendSessionAffinityHeaders: lo.ToPtr(true)},
				ContextWindow: int64Or(limitContext(m), 4096),
				MaxTokens:     int64Or(limitOutput(m), 4096),
			})
			recordModelsDevReasoningOptions("cloudflare-ai-gateway", id, m)
		}
	}

	return models
}

// processOpenCodeModels handles opencode and opencode-go (pi:
// loadModelsDevData opencodeVariants block). models.dev's provider.npm hint
// routes each model to its wire API.
func processOpenCodeModels(data ModelsDevCatalog) []*ai.Model {
	type openCodeVariant struct {
		key      string
		provider ai.ProviderID
		basePath string
	}
	variants := []openCodeVariant{
		{key: "opencode", provider: ai.ProviderOpenCode, basePath: "https://opencode.ai/zen"},
		{key: "opencode-go", provider: ai.ProviderOpenCodeGo, basePath: "https://opencode.ai/zen/go"},
	}

	var models []*ai.Model
	for _, variant := range variants {
		entry := data[variant.key]
		if entry == nil || entry.Models == nil {
			continue
		}
		for modelID, m := range entry.Models {
			if !boolVal(m.ToolCall) || m.Status == "deprecated" {
				continue
			}

			npm := ""
			if m.Provider != nil {
				npm = m.Provider.Npm
			}
			var apiKind ai.Api
			var baseURL string
			var compat ai.ModelCompat

			switch npm {
			case "@ai-sdk/openai":
				apiKind = ai.ApiOpenAIResponses
				baseURL = variant.basePath + "/v1"
				compat = &ai.OpenAIResponsesCompat{SessionAffinityFormat: lo.ToPtr(ai.SessionAffinityOpenAINoSession)}
			case "@ai-sdk/anthropic":
				apiKind = ai.ApiAnthropicMessages
				// Anthropic SDK appends /v1/messages to baseURL
				baseURL = variant.basePath
			case "@ai-sdk/google":
				apiKind = ai.ApiGoogleGenerativeAI
				baseURL = variant.basePath + "/v1"
			case "@ai-sdk/alibaba":
				apiKind = ai.ApiOpenAICompletions
				baseURL = variant.basePath + "/v1"
				compat = &ai.OpenAICompletionsCompat{CacheControlFormat: lo.ToPtr(ai.CacheControlAnthropic)}
			default:
				// null, undefined, or @ai-sdk/openai-compatible
				apiKind = ai.ApiOpenAICompletions
				baseURL = variant.basePath + "/v1"
			}

			if variant.provider == ai.ProviderOpenCode && modelID == "grok-build-0.1" {
				// pi: compat = { ...(compat ?? {}), supportsReasoningEffort: false }.
				// supportsReasoningEffort is a completions-only key that the
				// openai-responses adapter never reads (only openai-completions.ts
				// consumes it), so for a responses-typed compat the extra key is
				// inert in pi and is dropped here; the completions shapes match.
				switch existing := compat.(type) {
				case *ai.OpenAIResponsesCompat:
					_ = existing
				case *ai.OpenAICompletionsCompat:
					existing.SupportsReasoningEffort = lo.ToPtr(false)
				default:
					compat = &ai.OpenAICompletionsCompat{SupportsReasoningEffort: lo.ToPtr(false)}
				}
			}

			if (variant.provider == ai.ProviderOpenCode || variant.provider == ai.ProviderOpenCodeGo) && modelID == "kimi-k2.6" {
				// OpenCode Kimi K2.6 accepts Anthropic-style thinking objects
				// and rejects string thinking values or combined
				// reasoning_effort. pi spreads thinkingFormat/supportsReasoningEffort
				// into whatever object exists; for the anthropic-typed nil case
				// that object is inert (the anthropic adapter reads neither
				// key), which the fresh completions compat reproduces.
				switch existing := compat.(type) {
				case *ai.OpenAICompletionsCompat:
					existing.ThinkingFormat = lo.ToPtr(ai.ThinkingFormatDeepSeek)
					existing.SupportsReasoningEffort = lo.ToPtr(false)
				default:
					compat = &ai.OpenAICompletionsCompat{
						ThinkingFormat:          lo.ToPtr(ai.ThinkingFormatDeepSeek),
						SupportsReasoningEffort: lo.ToPtr(false),
					}
				}
			}

			// Fix known mismatches between models.dev npm data and actual
			// OpenCode Go endpoint behaviour. models.dev reports these models
			// as @ai-sdk/anthropic, but the OpenCode Go endpoints either
			// don't accept Anthropic SDK auth (MiniMax M2.7) or are served
			// through the OpenAI-compatible /v1/chat/completions path (Qwen
			// 3.5/3.6). Switch them to openai-completions so requests use
			// Bearer auth and the standard /v1/chat/completions endpoint.
			if variant.provider == ai.ProviderOpenCodeGo {
				if modelID == "minimax-m2.7" {
					apiKind = ai.ApiOpenAICompletions
					baseURL = variant.basePath + "/v1"
				}
				if modelID == "qwen3.5-plus" || modelID == "qwen3.6-plus" {
					apiKind = ai.ApiOpenAICompletions
					baseURL = variant.basePath + "/v1"
					// Qwen/DashScope uses enable_thinking at the top level.
					openAICompat := asOpenAICompat(compat)
					openAICompat.ThinkingFormat = lo.ToPtr(ai.ThinkingFormatQwen)
					compat = openAICompat
				}
			}

			if apiKind == ai.ApiOpenAICompletions {
				openAICompat := asOpenAICompat(compat)
				openAICompat.MaxTokensField = lo.ToPtr(ai.MaxTokensPlain)
				if openCodeOpenAICompletionsLongCacheRetentionUnsupportedModels[string(variant.provider)+":"+modelID] {
					openAICompat.SupportsLongCacheRetention = lo.ToPtr(false)
				}
				compat = openAICompat
			}

			models = append(models, &ai.Model{
				ID:            modelID,
				Name:          modelsDevName(m, modelID),
				API:           apiKind,
				Provider:      variant.provider,
				BaseURL:       baseURL,
				Reasoning:     boolVal(m.Reasoning),
				Input:         inputOf(m),
				Cost:          modelsDevCostOf(m.Cost),
				Compat:        compat,
				ContextWindow: int64Or(limitContext(m), 4096),
				MaxTokens:     int64Or(limitOutput(m), 4096),
			})
			recordModelsDevReasoningOptions(string(variant.provider), modelID, m)
		}
	}
	return models
}

// asOpenAICompat reads an existing OpenAICompletionsCompat out of a compat
// value or starts a fresh one (pi's `{...(compat ?? {}), field}` spreads).
func asOpenAICompat(compat ai.ModelCompat) *ai.OpenAICompletionsCompat {
	if openAICompat, ok := compat.(*ai.OpenAICompletionsCompat); ok && openAICompat != nil {
		return openAICompat
	}
	return &ai.OpenAICompletionsCompat{}
}

var githubCopilotClaudeRe = regexp.MustCompile(`^claude-(haiku|sonnet|opus|fable)-[45]([.\-]|$)`)

// processGitHubCopilotModels handles the Copilot catalog (pi:
// loadModelsDevData github-copilot block). Claude 4.x/5.x models route to
// anthropic-messages; Grok, gpt-5, oswe, and MAI-Code models are only served
// through the Copilot /responses endpoint.
func processGitHubCopilotModels(data ModelsDevCatalog) []*ai.Model {
	entry := data["github-copilot"]
	if entry == nil || entry.Models == nil {
		return nil
	}
	var models []*ai.Model
	for modelID, m := range entry.Models {
		if !boolVal(m.ToolCall) || m.Status == "deprecated" {
			continue
		}

		isCopilotClaude := githubCopilotClaudeRe.MatchString(modelID)
		needsResponsesAPI := strings.HasPrefix(modelID, "grok-") ||
			strings.HasPrefix(modelID, "gpt-5") ||
			strings.HasPrefix(modelID, "oswe") ||
			strings.HasPrefix(modelID, "mai-")

		apiKind := ai.ApiOpenAICompletions
		if isCopilotClaude {
			apiKind = ai.ApiAnthropicMessages
		} else if needsResponsesAPI {
			apiKind = ai.ApiOpenAIResponses
		}

		model := &ai.Model{
			ID:            modelID,
			Name:          modelsDevName(m, modelID),
			API:           apiKind,
			Provider:      ai.ProviderGithubCopilot,
			BaseURL:       "https://api.individual.githubcopilot.com",
			Reasoning:     boolVal(m.Reasoning),
			Input:         inputOf(m),
			Cost:          modelsDevCostOf(m.Cost),
			Headers:       maps.Clone(copilotStaticHeaders),
			ContextWindow: int64Or(limitContext(m), 128000),
			MaxTokens:     int64Or(limitOutput(m), 8192),
		}
		if apiKind == ai.ApiAnthropicMessages {
			if compat := getAnthropicMessagesCompat("github-copilot", modelID); compat != nil {
				model.Compat = compat
			}
		} else if apiKind == ai.ApiOpenAICompletions {
			// compat only applies to openai-completions
			model.Compat = &ai.OpenAICompletionsCompat{
				SupportsStore:           lo.ToPtr(false),
				SupportsDeveloperRole:   lo.ToPtr(false),
				SupportsReasoningEffort: lo.ToPtr(false),
			}
		}
		models = append(models, model)
		recordModelsDevReasoningOptions("github-copilot", modelID, m)
	}
	return models
}

// processXiaomiModels handles Xiaomi MiMo (pi: loadModelsDevData xiaomiVariants
// block). Built-in `xiaomi` targets the API billing endpoint (single stable
// URL, keys from platform.xiaomimimo.com); the three `xiaomi-token-plan-*`
// providers cover prepaid Token Plan endpoints in cn / ams / sgp.
func processXiaomiModels(data ModelsDevCatalog) []*ai.Model {
	xiaomiCompat := &ai.OpenAICompletionsCompat{
		RequiresReasoningContentOnAssistantMessages: lo.ToPtr(true),
		ThinkingFormat: lo.ToPtr(ai.ThinkingFormatDeepSeek),
	}
	type xiaomiVariant struct {
		source   string
		provider ai.ProviderID
		baseURL  string
	}
	variants := []xiaomiVariant{
		{source: "xiaomi", provider: ai.ProviderXiaomi, baseURL: "https://api.xiaomimimo.com/v1"},
		{source: "xiaomi-token-plan-cn", provider: ai.ProviderXiaomiTokenPlanCN, baseURL: "https://token-plan-cn.xiaomimimo.com/v1"},
		{source: "xiaomi-token-plan-ams", provider: ai.ProviderXiaomiTokenPlanAMS, baseURL: "https://token-plan-ams.xiaomimimo.com/v1"},
		{source: "xiaomi-token-plan-sgp", provider: ai.ProviderXiaomiTokenPlanSGP, baseURL: "https://token-plan-sgp.xiaomimimo.com/v1"},
	}

	var models []*ai.Model
	for _, variant := range variants {
		entry := data[variant.source]
		if entry == nil || entry.Models == nil {
			continue
		}
		for modelID, m := range entry.Models {
			if !boolVal(m.ToolCall) || m.Status == "deprecated" {
				continue
			}
			models = append(models, &ai.Model{
				ID:            modelID,
				Name:          modelsDevName(m, modelID),
				API:           ai.ApiOpenAICompletions,
				Provider:      variant.provider,
				BaseURL:       variant.baseURL,
				Reasoning:     boolVal(m.Reasoning),
				Input:         inputOf(m),
				Cost:          modelsDevCostOf(m.Cost),
				Compat:        copyOpenAICompat(xiaomiCompat),
				ContextWindow: int64Or(limitContext(m), 4096),
				MaxTokens:     int64Or(limitOutput(m), 4096),
			})
			recordModelsDevReasoningOptions(string(variant.provider), modelID, m)
		}
	}
	return models
}

// processQwenTokenPlanModels handles Alibaba Cloud Model Studio Token Plan
// models (pi: loadModelsDevData qwenTokenPlanVariants block). International
// and China use separate endpoints and API keys (sk-sp- prefix); the
// Individual provider reuses the international source and endpoint with a
// narrower catalog. models.dev keys are "alibaba-token-plan[-cn]"; pi exposes
// them as "qwen-token-plan[-cn]" plus the Individual catalog view.
func processQwenTokenPlanModels(data ModelsDevCatalog) []*ai.Model {
	qwenTokenPlanCompat := &ai.OpenAICompletionsCompat{
		ThinkingFormat:          lo.ToPtr(ai.ThinkingFormatQwen),
		SupportsDeveloperRole:   lo.ToPtr(false),
		SupportsStore:           lo.ToPtr(false),
		SupportsReasoningEffort: lo.ToPtr(true),
	}
	type qwenTokenPlanVariant struct {
		source   string
		provider ai.ProviderID
		baseURL  string
		modelIDs map[string]bool
	}
	variants := []qwenTokenPlanVariant{
		{source: "alibaba-token-plan", provider: ai.ProviderQwenTokenPlan, baseURL: "https://token-plan.ap-southeast-1.maas.aliyuncs.com/compatible-mode/v1"},
		{source: "alibaba-token-plan", provider: ai.ProviderQwenTokenPlanInd, baseURL: "https://token-plan.ap-southeast-1.maas.aliyuncs.com/compatible-mode/v1", modelIDs: qwenTokenPlanIndividualModelIDs},
		{source: "alibaba-token-plan-cn", provider: ai.ProviderQwenTokenPlanCN, baseURL: "https://token-plan.cn-beijing.maas.aliyuncs.com/compatible-mode/v1"},
	}

	var models []*ai.Model
	for _, variant := range variants {
		entry := data[variant.source]
		var providerModels map[string]*ModelsDevModel
		if entry != nil {
			providerModels = entry.Models
		}
		emittedModelIDs := map[string]bool{}
		trackEmitted := variant.modelIDs != nil

		for modelID, m := range providerModels {
			if !boolVal(m.ToolCall) || qwenTokenPlanExcludedModelIDs[modelID] {
				continue
			}
			if variant.modelIDs != nil && !variant.modelIDs[modelID] {
				continue
			}
			thinkingLevelMap := getEffortThinkingLevelMap(m.ReasoningOptions)
			if thinkingLevelMap == nil && qwenTokenPlanReasoningEffortFallbackModelIDs[modelID] {
				fallback := cloneThinkingLevelMap(qwenTokenPlanFallbackThinkingLevelMap)
				thinkingLevelMap = &fallback
			}

			compat := copyOpenAICompat(qwenTokenPlanCompat)
			if thinkingLevelMap == nil {
				compat.SupportsReasoningEffort = lo.ToPtr(false)
			}

			models = append(models, &ai.Model{
				ID:        modelID,
				Name:      modelsDevName(m, modelID),
				API:       ai.ApiOpenAICompletions,
				Provider:  variant.provider,
				BaseURL:   variant.baseURL,
				Reasoning: boolVal(m.Reasoning),
				Input:     inputOf(m),
				Cost:      modelsDevCostOf(m.Cost),
				Compat:    compat,
				ThinkingLevelMap: func() ai.ThinkingLevelMap {
					if thinkingLevelMap != nil {
						return *thinkingLevelMap
					}
					return nil
				}(),
				ContextWindow: int64Or(limitContext(m), 4096),
				MaxTokens:     int64Or(limitOutput(m), 4096),
			})
			if trackEmitted {
				emittedModelIDs[modelID] = true
			}
		}

		if trackEmitted && genOptions.strict {
			assertExactModelIDs(string(variant.provider), qwenTokenPlanIndividualModelIDs, emittedModelIDs)
		}
	}
	return models
}

// assertExactModelIds fails the strict run when an allowlisted catalog view
// diverges from the generated one (pi: model-data.ts assertExactModelIds).
func assertExactModelIDs(label string, expected, actual map[string]bool) {
	expectedIDs := sortedSetMembers(expected)
	actualIDs := sortedSetMembers(actual)
	if sameStrings(expectedIDs, actualIDs) {
		return
	}
	panic(fmt.Sprintf("%s model IDs do not match (%s)", label, describeSetDifference(expectedIDs, actualIDs)))
}

func sortedSetMembers(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for id := range set {
		if set[id] {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// describeSetDifference renders "missing: [...]; extra: [...]" (pi:
// describeSetDifference).
func describeSetDifference(expected, actual []string) string {
	expectedSet := map[string]bool{}
	for _, id := range expected {
		expectedSet[id] = true
	}
	actualSet := map[string]bool{}
	for _, id := range actual {
		actualSet[id] = true
	}
	var missing, extra []string
	for _, id := range expected {
		if !actualSet[id] {
			missing = append(missing, id)
		}
	}
	for _, id := range actual {
		if !expectedSet[id] {
			extra = append(extra, id)
		}
	}
	return fmt.Sprintf("missing: %v; extra: %v", missing, extra)
}

// modelsDevName is pi's `m.name || modelId` fallback.
func modelsDevName(m *ModelsDevModel, fallback string) string {
	if m.Name != "" {
		return m.Name
	}
	return fallback
}
