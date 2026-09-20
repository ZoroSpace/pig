// pi: generate-models.ts — the models.dev Google blocks ("Process Google
// models" 1545-1572 and "Process Google Vertex Gemini models" 1576-1613)
// plus the Google thinking-api helpers (isGoogleThinkingApi at 551;
// isGemini3ProModel / isGemini3FlashModel / isGemma4Model at 859-871).
package main

import (
	"regexp"
	"strings"

	"github.com/ZoroSpace/pig/ai"
	"github.com/samber/lo"
)

var (
	googleGemini3ProRe   = regexp.MustCompile(`gemini-3(?:\.\d+)?-pro`)
	googleGemini3FlashRe = regexp.MustCompile(`gemini-3(?:\.\d+)?-flash`)
	googleGemma4Re       = regexp.MustCompile(`gemma-?4`)
)

// isGoogleThinkingApi reports whether the model speaks a Google thinking API
// (pi: isGoogleThinkingApi).
func isGoogleThinkingApi(model *ai.Model) bool {
	return model.API == ai.ApiGoogleGenerativeAI || model.API == ai.ApiGoogleVertex
}

func isGemini3ProModel(modelID string) bool {
	return googleGemini3ProRe.MatchString(strings.ToLower(modelID))
}

func isGemini3FlashModel(modelID string) bool {
	id := strings.ToLower(modelID)
	if googleGemini3FlashRe.MatchString(id) {
		return true
	}
	return id == "gemini-flash-latest" || id == "gemini-flash-lite-latest"
}

func isGemma4Model(modelID string) bool {
	return googleGemma4Re.MatchString(strings.ToLower(modelID))
}

// getGoogleThinkingLevelMap derives the thinking level map from models.dev
// verified effort options, falling back to the Gemma 4 table (pi:
// getGoogleThinkingLevelMap, v0.86.0 — "derive Google thinking levels from
// models.dev" and "avoid unsupported Gemini thinking levels"; the hardcoded
// Gemini 3 Pro/Flash merges were removed in favor of the derived maps).
func getGoogleThinkingLevelMap(modelID string, reasoningOptions []ModelsDevReasoningOption) *ai.ThinkingLevelMap {
	if effortMap := getEffortThinkingLevelMap(reasoningOptions); effortMap != nil {
		return effortMap
	}
	if isGemma4Model(modelID) {
		m := ai.ThinkingLevelMap{
			ai.ModelThinkingOff:     nil,
			ai.ModelThinkingMinimal: lo.ToPtr("MINIMAL"),
			ai.ModelThinkingLow:     nil,
			ai.ModelThinkingMedium:  nil,
			ai.ModelThinkingHigh:    lo.ToPtr("HIGH"),
		}
		return &m
	}
	return nil
}

// processGoogleModels (pi: loadModelsDevData google block). The
// gemini-flash-latest / gemini-flash-lite-latest aliases inherit the source
// model's metadata so their reasoning flags and budgets track the concrete
// generation they alias.
func processGoogleModels(data ModelsDevCatalog) []*ai.Model {
	provider := data["google"]
	if provider == nil || provider.Models == nil {
		return nil
	}
	var models []*ai.Model
	for modelID, m := range provider.Models {
		if !boolVal(m.ToolCall) {
			continue
		}
		source := m
		if modelID == "gemini-flash-latest" {
			if aliased := provider.Models["gemini-3.5-flash"]; aliased != nil {
				source = aliased
			}
		}
		if modelID == "gemini-flash-lite-latest" {
			if aliased := provider.Models["gemini-3.1-flash-lite"]; aliased != nil {
				source = aliased
			}
		}
		thinkingLevelMap := getGoogleThinkingLevelMap(modelID, source.ReasoningOptions)
		models = append(models, &ai.Model{
			ID:               modelID,
			Name:             orString(m.Name, modelID),
			API:              ai.ApiGoogleGenerativeAI,
			Provider:         ai.ProviderGoogle,
			BaseURL:          "https://generativelanguage.googleapis.com/v1beta",
			Reasoning:        boolVal(source.Reasoning),
			ThinkingLevelMap: derefThinkingLevelMap(thinkingLevelMap),
			Input:            inputOf(source),
			Cost:             modelsDevCostOf(source.Cost),
			ContextWindow:    int64Or(limitContext(source), 4096),
			MaxTokens:        int64Or(limitOutput(source), 4096),
		})
		recordModelsDevReasoningOptions("google", modelID, source)
	}
	return models
}

// vertexBaseURL is the catalog baseUrl for google-vertex models (pi:
// VERTEX_BASE_URL).
const vertexBaseURL = "https://{location}-aiplatform.googleapis.com"

// processGoogleVertexModels (pi: loadModelsDevData "Process Google Vertex
// Gemini models", generate-models.ts 1576-1613). The google-vertex models.dev
// catalog also includes Claude, OpenAI, and other MaaS models that do not use
// the @google/genai Gemini streaming path implemented by the google-vertex
// provider, so non-gemini ids are skipped.
func processGoogleVertexModels(data ModelsDevCatalog) []*ai.Model {
	provider := data["google-vertex"]
	if provider == nil || provider.Models == nil {
		return nil
	}
	var models []*ai.Model
	for modelID, m := range provider.Models {
		if !boolVal(m.ToolCall) {
			continue
		}
		if !strings.HasPrefix(modelID, "gemini-") {
			continue
		}
		if modelID == "gemini-3.1-flash-lite-preview" {
			continue
		}
		source := m
		if modelID == "gemini-flash-latest" {
			if aliased := provider.Models["gemini-3.5-flash"]; aliased != nil {
				source = aliased
			}
		}
		if modelID == "gemini-flash-lite-latest" {
			if aliased := provider.Models["gemini-3.1-flash-lite"]; aliased != nil {
				source = aliased
			}
		}

		// models.dev reports Vertex cache_read/cache_write values for Gemini
		// 2.5 Flash that do not match the official Gemini API standard pricing
		// table. pi only accounts cachedContentTokenCount as cacheRead.
		cacheRead := 0.0
		if modelID == "gemini-2.5-flash" {
			cacheRead = 0.03
		} else if source.Cost != nil && source.Cost.CacheRead != nil {
			cacheRead = *source.Cost.CacheRead
		}
		cost := ai.ModelCostRates{}
		if source.Cost != nil {
			if source.Cost.Input != nil {
				cost.Input = *source.Cost.Input
			}
			if source.Cost.Output != nil {
				cost.Output = *source.Cost.Output
			}
		}
		cost.CacheRead = cacheRead
		thinkingLevelMap := getGoogleThinkingLevelMap(modelID, source.ReasoningOptions)
		models = append(models, &ai.Model{
			ID:               modelID,
			Name:             orString(m.Name, modelID),
			API:              ai.ApiGoogleVertex,
			Provider:         ai.ProviderGoogleVertex,
			BaseURL:          vertexBaseURL,
			Reasoning:        boolVal(source.Reasoning),
			ThinkingLevelMap: derefThinkingLevelMap(thinkingLevelMap),
			Input:            inputOf(source),
			Cost:             ai.ModelCost{ModelCostRates: cost},
			ContextWindow:    int64Or(limitContext(source), 4096),
			MaxTokens:        int64Or(limitOutput(source), 4096),
		})
		recordModelsDevReasoningOptions("google-vertex", modelID, source)
	}
	return models
}

// derefThinkingLevelMap copies a map pointer into a value Model field (nil
// stays nil).
func derefThinkingLevelMap(m *ai.ThinkingLevelMap) ai.ThinkingLevelMap {
	if m == nil {
		return nil
	}
	return *m
}
