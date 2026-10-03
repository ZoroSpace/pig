// pi: generate-models.ts — the amazon-bedrock block of loadModelsDevData (the
// P2-T2 bedrock batch).
package main

import (
	"slices"
	"strings"

	"github.com/ZoroSpace/pig/ai"
	"github.com/samber/lo"
)

// bedrockInferenceProfileOnlyModelIDs lists models only reachable through an
// inference profile; the plain model id is excluded from the catalog (pi:
// BEDROCK_INFERENCE_PROFILE_ONLY_MODEL_IDS).
var bedrockInferenceProfileOnlyModelIDs = stringSet("anthropic.claude-opus-5")

// getBedrockBaseUrl routes EU inference profiles to the EU endpoint (pi:
// getBedrockBaseUrl).
func getBedrockBaseUrl(modelID string) string {
	if strings.HasPrefix(modelID, "eu.") {
		return "https://bedrock-runtime.eu-central-1.amazonaws.com"
	}
	return "https://bedrock-runtime.us-east-1.amazonaws.com"
}

// processBedrockModels (pi: loadModelsDevData amazon-bedrock block).
func processBedrockModels(data ModelsDevCatalog) []*ai.Model {
	provider := data["amazon-bedrock"]
	if provider == nil || provider.Models == nil {
		return nil
	}
	var models []*ai.Model
	for modelID, m := range provider.Models {
		if !boolVal(m.ToolCall) {
			continue
		}
		if bedrockInferenceProfileOnlyModelIDs[modelID] {
			continue
		}

		if strings.HasPrefix(modelID, "ai21.jamba") {
			// These models doesn't support tool use in streaming mode
			continue
		}

		if strings.HasPrefix(modelID, "mistral.mistral-7b-instruct-v0") {
			// These models doesn't support system messages
			continue
		}

		input := []ai.ModelInput{ai.ModelInputText}
		if m.Modalities != nil && slices.Contains(m.Modalities.Input, "image") {
			input = append(input, ai.ModelInputImage)
		}

		model := &ai.Model{
			ID:        modelID,
			Name:      orString(m.Name, modelID),
			API:       ai.ApiBedrockConverseStream,
			Provider:  ai.ProviderAmazonBedrock,
			BaseURL:   getBedrockBaseUrl(modelID),
			Reasoning: boolVal(m.Reasoning),
			Input:     input,
			// modelsDevCostOf keeps models.dev pricing tiers, e.g. the
			// long-context tier for OpenAI models (pi #10326).
			Cost:          modelsDevCostOf(m.Cost),
			ContextWindow: int64Or(limitContext(m), 4096),
			MaxTokens:     int64Or(limitOutput(m), 4096),
		}
		if boolVal(m.StructuredOutput) {
			model.Compat = &ai.BedrockCompat{SupportsStrictMode: lo.ToPtr(true)}
		}
		models = append(models, model)
		recordModelsDevReasoningOptions("amazon-bedrock", modelID, m)
	}
	return models
}
