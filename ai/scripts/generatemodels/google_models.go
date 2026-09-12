// pi: generate-models.ts — the models.dev Google block ("Process Google
// models", generate-models.ts 1545-1572) plus the Google thinking-api helpers
// (isGoogleThinkingApi at 551; isGemini3ProModel / isGemini3FlashModel /
// isGemma4Model at 859-871). The Vertex block (1576-1613) stays skipped: the
// google-vertex adapter is unported (C4 candidate).
package main

import (
	"regexp"
	"strings"

	"github.com/ZoroSpace/pig/ai"
)

var (
	googleGemini3ProRe   = regexp.MustCompile(`gemini-3(?:\.\d+)?-pro`)
	googleGemini3FlashRe = regexp.MustCompile(`gemini-3(?:\.\d+)?-flash`)
	googleGemma4Re       = regexp.MustCompile(`gemma-?4`)
)

// isGoogleThinkingApi reports whether the model speaks a Google thinking API
// (pi: isGoogleThinkingApi). The google-vertex half stays for the unported
// vertex adapter (no vertex models reach this generator yet).
func isGoogleThinkingApi(model *ai.Model) bool {
	return model.API == ai.ApiGoogleGenerativeAI || model.API == ai.Api("google-vertex")
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
		models = append(models, &ai.Model{
			ID:            modelID,
			Name:          orString(m.Name, modelID),
			API:           ai.ApiGoogleGenerativeAI,
			Provider:      ai.ProviderGoogle,
			BaseURL:       "https://generativelanguage.googleapis.com/v1beta",
			Reasoning:     boolVal(source.Reasoning),
			Input:         inputOf(source),
			Cost:          modelsDevCostOf(source.Cost),
			ContextWindow: int64Or(limitContext(source), 4096),
			MaxTokens:     int64Or(limitOutput(source), 4096),
		})
		recordModelsDevReasoningOptions("google", modelID, source)
	}
	return models
}
