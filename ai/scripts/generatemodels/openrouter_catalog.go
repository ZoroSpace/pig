// pi: scripts/openrouter-catalog.ts (v0.99.1 #9948). Builds the OpenRouter
// catalog from the default listing plus the `output_modalities=image` and
// `output_modalities=decisions` listings. The default listing omits image-only
// and decision models, so those come from the other listings. An upstream
// model may appear in several results; it then gets separate entries per
// operation (chat/image/classifier).
package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/providers"
)

// openRouterCatalog splits the OpenRouter listings by model operation.
type openRouterCatalog struct {
	chat        []*ai.Model
	images      []*ai.Model
	classifiers []*ai.Model
}

// fetchOpenRouterList fetches one OpenRouter listing (pi: fetchOpenRouterList).
func fetchOpenRouterList(ctx context.Context, query string) ([]OpenRouterModel, error) {
	var data OpenRouterResponse
	if err := httpGetJSON(ctx, openRouterURL+query, &data); err != nil {
		return nil, err
	}
	return data.Data, nil
}

// fetchOpenRouterCatalog fetches the three OpenRouter listings and builds the
// catalog (pi: fetchOpenRouterModels).
func fetchOpenRouterCatalog(ctx context.Context) (*openRouterCatalog, error) {
	fmt.Println("Fetching models from OpenRouter API...")
	listed, err := fetchOpenRouterList(ctx, "")
	if err != nil {
		return nil, err
	}
	imageListed, err := fetchOpenRouterList(ctx, "?output_modalities=image")
	if err != nil {
		return nil, err
	}
	decisionListed, err := fetchOpenRouterList(ctx, "?output_modalities=decisions")
	if err != nil {
		return nil, err
	}
	catalog := buildOpenRouterCatalog(listed, imageListed, decisionListed)
	fmt.Printf("Fetched %d tool-capable, %d image, and %d classifier models from OpenRouter\n",
		len(catalog.chat), len(catalog.images), len(catalog.classifiers))
	if len(catalog.images) == 0 {
		return nil, fmt.Errorf("OpenRouter API returned no usable image models")
	}
	return catalog, nil
}

// buildOpenRouterCatalog is the pi buildOpenRouterCatalog port.
func buildOpenRouterCatalog(listed, imageListed, decisionListed []OpenRouterModel) *openRouterCatalog {
	catalog := &openRouterCatalog{}

	for i := range listed {
		model := &listed[i]
		// Non-tool models yield nil (pi: `continue`).
		if chat := buildOpenRouterChatModel(model); chat != nil {
			catalog.chat = append(catalog.chat, chat)
		}
	}

	seenImages := map[string]bool{}
	for i := range imageListed {
		model := &imageListed[i]
		if seenImages[model.ID] {
			continue
		}
		seenImages[model.ID] = true
		output := modalities(modalityList(model.Architecture, false))
		if !containsModelInput(output, ai.ModelInputImage) {
			continue
		}
		input := modalities(modalityList(model.Architecture, true))
		if len(input) == 0 {
			input = []ai.ModelInput{ai.ModelInputText}
		}
		catalog.images = append(catalog.images, &ai.Model{
			Type:     ai.ModelTypeImage,
			ID:       model.ID,
			Name:     model.Name,
			API:      ai.ImageApiOpenRouter,
			Provider: ai.ProviderOpenRouter,
			BaseURL:  "https://openrouter.ai/api/v1",
			Input:    input,
			Output:   output,
			Cost:     providers.OpenRouterCost(model.Pricing),
		})
	}

	// Decision models such as TypeSafe's Jev are served through OpenRouter's
	// TypeSafe-compatible System One endpoint.
	seenClassifiers := map[string]bool{}
	for i := range decisionListed {
		model := &decisionListed[i]
		if seenClassifiers[model.ID] {
			continue
		}
		seenClassifiers[model.ID] = true
		if !containsString(rawOutputModalities(model), "decisions") {
			continue
		}
		input := modalities(modalityList(model.Architecture, true))
		if len(input) == 0 {
			input = []ai.ModelInput{ai.ModelInputText}
		}
		catalog.classifiers = append(catalog.classifiers, &ai.Model{
			Type:          ai.ModelTypeClassifier,
			ID:            model.ID,
			Name:          model.Name,
			API:           ai.ClassifierApiTypesafeSystemOne,
			Provider:      ai.ProviderOpenRouter,
			BaseURL:       "https://openrouter.ai/api/v1",
			Input:         input,
			Cost:          providers.OpenRouterCost(model.Pricing),
			ContextWindow: openRouterContextWindow(model),
		})
	}

	return catalog
}

// buildOpenRouterChatModel converts one default-listing entry into a chat
// model (pi: the chat loop of buildOpenRouterCatalog).
func buildOpenRouterChatModel(model *OpenRouterModel) *ai.Model {
	// Only include models that support tools.
	if !containsString(model.SupportedParameters, "tools") {
		return nil
	}
	// Parse input modalities.
	input := []ai.ModelInput{ai.ModelInputText}
	if model.Architecture != nil && model.Architecture.Modality.includes("image") {
		input = append(input, ai.ModelInputImage)
	}

	thinkingLevelMap := getOpenRouterThinkingLevelMap(model.Reasoning)

	// pi: anthropic/* models (except :batch aliases) are served by the
	// Anthropic-compatible endpoint without the /v1 suffix.
	useAnthropicMessages := anthropicPrefixRegexp.MatchString(model.ID) && !strings.HasSuffix(model.ID, ":batch")
	baseURL := "https://openrouter.ai/api/v1"
	api := ai.ApiOpenAICompletions
	if useAnthropicMessages {
		baseURL = "https://openrouter.ai/api"
		api = ai.ApiAnthropicMessages
	}

	m := &ai.Model{
		Type:          ai.ModelTypeChat,
		ID:            model.ID,
		Name:          model.Name,
		API:           api,
		BaseURL:       baseURL,
		Provider:      ai.ProviderOpenRouter,
		Reasoning:     containsString(model.SupportedParameters, "reasoning"),
		Input:         input,
		Cost:          providers.OpenRouterCost(model.Pricing),
		ContextWindow: openRouterContextWindow(model),
		MaxTokens:     openRouterMaxTokens(model),
	}
	if thinkingLevelMap != nil {
		m.ThinkingLevelMap = *thinkingLevelMap
	}
	return m
}

// containsModelInput is containsString for modality lists.
func containsModelInput(xs []ai.ModelInput, v ai.ModelInput) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

// rawOutputModalities returns the unfiltered output modality strings
// ("decisions" is a decision-listing modality that modalities() would drop).
func rawOutputModalities(model *OpenRouterModel) []string {
	if model.Architecture == nil {
		return nil
	}
	return model.Architecture.OutputModalities
}

// modalityList picks input or output modalities off the architecture record.
func modalityList(arch *OpenRouterArchitecture, input bool) []string {
	if arch == nil {
		return nil
	}
	if input {
		return arch.InputModalities
	}
	return arch.OutputModalities
}

// modalities filters to text/image and dedupes, preserving first-seen order
// (pi: modalities helper).
func modalities(values []string) []ai.ModelInput {
	seen := map[ai.ModelInput]bool{}
	var out []ai.ModelInput
	for _, v := range values {
		if v != "text" && v != "image" {
			continue
		}
		m := ai.ModelInput(v)
		if seen[m] {
			continue
		}
		seen[m] = true
		out = append(out, m)
	}
	return out
}

// openRouterPerMillion, openRouterCost and pricingFieldString moved to
// ai/providers/openrouter_api.go (pi: openRouterCost is shared with the
// runtime provider so the generator and model refresh agree).

// openRouterContextWindow is top_provider.context_length ||
// context_length || 4096. JS `||` treats 0 as absent, so zero-length
// entries fall through to the next source (observed on decision-listing
// models).
func openRouterContextWindow(model *OpenRouterModel) int64 {
	if model.TopProvider != nil && model.TopProvider.ContextLength != nil && *model.TopProvider.ContextLength != 0 {
		return *model.TopProvider.ContextLength
	}
	if model.ContextLength != nil && *model.ContextLength != 0 {
		return *model.ContextLength
	}
	return 4096
}

// openRouterMaxTokens is top_provider.max_completion_tokens || 4096 (zero
// counts as absent, pi's || semantics).
func openRouterMaxTokens(model *OpenRouterModel) int64 {
	if model.TopProvider != nil && model.TopProvider.MaxCompletionTokens != nil && *model.TopProvider.MaxCompletionTokens != 0 {
		return *model.TopProvider.MaxCompletionTokens
	}
	return 4096
}
