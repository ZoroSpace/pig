// pi: generate-image-models.ts — fetch functions: OpenRouter image-model
// catalog (fetchOpenRouterImageModels, parseOpenRouterImageModels).
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/ZoroSpace/pig/ai"
)

// openRouterImageModelsURL is the OpenRouter /models endpoint filtered to
// image-capable models (pi: `${OPENROUTER_BASE_URL}/models?output_modalities=image`).
const openRouterImageModelsURL = "https://openrouter.ai/api/v1/models?output_modalities=image"

// openRouterImageModelRecord is pi's OpenRouterModelRecord subset the image
// catalog consumes. context_length is parsed but unused (pi declares it in
// the record interface and never reads it).
type openRouterImageModelRecord struct {
	ID            string                       `json:"id"`
	Name          string                       `json:"name"`
	ContextLength *int64                       `json:"context_length"`
	Architecture  *openRouterImageArchitecture `json:"architecture"`
	Pricing       *openRouterImagePricing      `json:"pricing"`
}

// openRouterImageArchitecture is pi's OpenRouterModelRecord.architecture.
type openRouterImageArchitecture struct {
	InputModalities  []string `json:"input_modalities"`
	OutputModalities []string `json:"output_modalities"`
}

// openRouterImagePricing is pi's OpenRouterModelRecord.pricing. Prices are
// per-token strings; costs in the catalog are per million tokens.
type openRouterImagePricing struct {
	Prompt          *string `json:"prompt"`
	Completion      *string `json:"completion"`
	InputCacheRead  *string `json:"input_cache_read"`
	InputCacheWrite *string `json:"input_cache_write"`
}

// openRouterImageModelsResponse is the OpenRouter /models payload.
type openRouterImageModelsResponse struct {
	Data []openRouterImageModelRecord `json:"data"`
}

// httpClient is the shared fetch client (pi's global fetch).
var httpClient = &http.Client{Timeout: 60 * time.Second}

// httpGetJSON GETs url and unmarshals the JSON body into out.
func httpGetJSON(ctx context.Context, url string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "pig-generateimagemodels")
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s returned %s", url, resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	return json.Unmarshal(body, out)
}

// parseOpenRouterImageModels builds image models from an OpenRouter /models
// payload (pi: parseOpenRouterImageModels). Models whose output modalities do
// not include "image" are skipped; models with no text/image input modality
// default to text input. strict rethrows instead of returning empty lists.
func parseOpenRouterImageModels(resp *openRouterImageModelsResponse, strict bool) ([]*ai.ImagesModel, error) {
	if resp == nil || len(resp.Data) == 0 {
		if strict {
			return nil, fmt.Errorf("OpenRouter API returned a missing or empty image model list")
		}
		return nil, nil
	}

	models := make([]*ai.ImagesModel, 0, len(resp.Data))
	for _, m := range resp.Data {
		input := filterImageModalities(archInput(m.Architecture))
		output := filterImageModalities(archOutput(m.Architecture))
		if !containsModelInput(output, ai.ModelInputImage) {
			continue
		}
		if len(input) == 0 {
			input = append(input, ai.ModelInputText)
		}

		models = append(models, &ai.ImagesModel{
			ID:       m.ID,
			Name:     m.Name,
			API:      ai.ImagesApiOpenRouter,
			Provider: ai.ImagesProviderOpenRouter,
			BaseURL:  "https://openrouter.ai/api/v1",
			Input:    input,
			Output:   output,
			Cost: ai.ModelCost{ModelCostRates: ai.ModelCostRates{
				Input:      parseImagePrice(pricingField(m.Pricing, func(p *openRouterImagePricing) *string { return p.Prompt })) * 1_000_000,
				Output:     parseImagePrice(pricingField(m.Pricing, func(p *openRouterImagePricing) *string { return p.Completion })) * 1_000_000,
				CacheRead:  parseImagePrice(pricingField(m.Pricing, func(p *openRouterImagePricing) *string { return p.InputCacheRead })) * 1_000_000,
				CacheWrite: parseImagePrice(pricingField(m.Pricing, func(p *openRouterImagePricing) *string { return p.InputCacheWrite })) * 1_000_000,
			}},
		})
	}

	if strict && len(models) == 0 {
		return nil, fmt.Errorf("OpenRouter API returned no usable image models")
	}
	return models, nil
}

// fetchOpenRouterImageModels fetches and parses the OpenRouter image catalog
// (pi: fetchOpenRouterImageModels). On failure non-strict runs return nil and
// let the caller emit an empty catalog, matching pi.
func fetchOpenRouterImageModels(ctx context.Context, strict bool) ([]*ai.ImagesModel, error) {
	fmt.Println("Fetching image models from OpenRouter API...")
	var resp openRouterImageModelsResponse
	if err := httpGetJSON(ctx, openRouterImageModelsURL, &resp); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to fetch OpenRouter image models: %v\n", err)
		if strict {
			return nil, err
		}
		return nil, nil
	}
	models, err := parseOpenRouterImageModels(&resp, strict)
	if err != nil {
		return nil, err
	}
	fmt.Printf("Fetched %d image models from OpenRouter\n", len(models))
	return models, nil
}

// archInput / archOutput guard nil Architecture (pi: `?.` access).
func archInput(a *openRouterImageArchitecture) []string {
	if a == nil {
		return nil
	}
	return a.InputModalities
}

func archOutput(a *openRouterImageArchitecture) []string {
	if a == nil {
		return nil
	}
	return a.OutputModalities
}

// pricingField guards nil Pricing (pi: `model.pricing?.X`).
func pricingField(p *openRouterImagePricing, get func(*openRouterImagePricing) *string) *string {
	if p == nil {
		return nil
	}
	return get(p)
}

// filterImageModalities keeps only the text/image modalities, deduplicated
// and in order (pi: `new Set(modalities.filter(m => m === "text" || m === "image"))`).
func filterImageModalities(modalities []string) []ai.ModelInput {
	var out []ai.ModelInput
	seen := map[ai.ModelInput]bool{}
	for _, m := range modalities {
		if m != "text" && m != "image" {
			continue
		}
		v := ai.ModelInput(m)
		if seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

// parseImagePrice parses pi's `parseFloat(model.pricing?.X || "0")`.
func parseImagePrice(s *string) float64 {
	if s == nil {
		return 0
	}
	f, err := strconv.ParseFloat(*s, 64)
	if err != nil {
		return 0
	}
	return f
}

func containsModelInput(modalities []ai.ModelInput, want ai.ModelInput) bool {
	for _, m := range modalities {
		if m == want {
			return true
		}
	}
	return false
}
