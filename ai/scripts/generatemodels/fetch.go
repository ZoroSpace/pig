// pi: generate-models.ts — fetch functions: models.dev (loadModelsDevData),
// OpenRouter (fetchOpenRouterModels), Vercel AI Gateway (fetchAiGatewayModels),
// NVIDIA NIM live model ids (fetchNvidiaNimModelIds).
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ZoroSpace/pig/ai"
	"github.com/samber/lo"
)

const (
	modelsDevURL       = "https://models.dev/api.json"
	openRouterURL      = "https://openrouter.ai/api/v1/models"
	aiGatewayModelsURL = "https://ai-gateway.vercel.sh/v1/models"
	aiGatewayBaseURL   = "https://ai-gateway.vercel.sh"
	nvidiaBaseURL      = "https://integrate.api.nvidia.com/v1"
)

// nvidiaHeaders are the static NVIDIA NIM request headers (pi: NVIDIA_HEADERS).
var nvidiaHeaders = map[string]string{"NVCF-POLL-SECONDS": "3600"}

// nvidiaOpenAICompat is the NVIDIA NIM compat override (pi: NVIDIA_OPENAI_COMPAT).
var nvidiaOpenAICompat = &ai.OpenAICompletionsCompat{
	SupportsStore:              lo.ToPtr(false),
	SupportsDeveloperRole:      lo.ToPtr(false),
	SupportsReasoningEffort:    lo.ToPtr(false),
	MaxTokensField:             lo.ToPtr(ai.MaxTokensPlain),
	SupportsStrictMode:         lo.ToPtr(false),
	SupportsLongCacheRetention: lo.ToPtr(false),
}

// nvidiaNimUnsupportedModels are models.dev NVIDIA models that the live NIM
// catalog lists but pi does not serve (pi: NVIDIA_NIM_UNSUPPORTED_MODELS).
var nvidiaNimUnsupportedModels = stringSet(
	"abacusai/dracarys-llama-3.1-70b-instruct",
	"bytedance/seed-oss-36b-instruct",
	"deepseek-ai/deepseek-v4-flash",
	"deepseek-ai/deepseek-v4-pro",
	"google/gemma-2-2b-it",
	"google/gemma-3n-e2b-it",
	"google/gemma-3n-e4b-it",
	"google/gemma-4-31b-it",
	"meta/llama-3.2-1b-instruct",
	"meta/llama-4-maverick-17b-128e-instruct",
	"microsoft/phi-4-mini-instruct",
	"minimaxai/minimax-m2.7",
	"mistralai/mistral-nemotron",
	"nvidia/nemotron-mini-4b-instruct",
	"qwen/qwen3-next-80b-a3b-instruct",
	"qwen/qwen3.5-397b-a17b",
	"sarvamai/sarvam-m",
	"upstage/solar-10.7b-instruct",
)

// stringSet builds a set from a list (pi's Set literals).
func stringSet(values ...string) map[string]bool {
	m := make(map[string]bool, len(values))
	for _, v := range values {
		m[v] = true
	}
	return m
}

// httpClient is the shared fetch client (30s per request).
var httpClient = &http.Client{Timeout: 60 * time.Second}

func httpGetJSON(ctx context.Context, url string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "pig-generatemodels")
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

// normalizeNvidiaModelId lowercases and replaces "_" with "." (pi:
// normalizeNvidiaModelId).
func normalizeNvidiaModelId(modelID string) string {
	return strings.ReplaceAll(strings.ToLower(modelID), "_", ".")
}

// nvidiaNimModelIdList is the NVIDIA NIM /models payload (pi: NvidiaNimModelListItem).
type nvidiaNimModelIdList struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
}

// fetchNvidiaNimModelIds returns live NVIDIA NIM model ids plus their
// normalized aliases (pi: fetchNvidiaNimModelIds). A nil/empty map on failure
// unless strict, which the caller decides (strict rethrows in pi).
func fetchNvidiaNimModelIds(ctx context.Context) (map[string]string, error) {
	modelIds := map[string]string{}
	var data nvidiaNimModelIdList
	if err := httpGetJSON(ctx, nvidiaBaseURL+"/models", &data); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to fetch NVIDIA NIM models: %v\n", err)
		return modelIds, err
	}
	for _, m := range data.Data {
		modelIds[m.ID] = m.ID
		modelIds[normalizeNvidiaModelId(m.ID)] = m.ID
	}
	fmt.Printf("Fetched %d model IDs from NVIDIA NIM\n", len(data.Data))
	return modelIds, nil
}

// fetchOpenRouterModels returns tool-capable OpenRouter models as
// openai-completions models (pi: fetchOpenRouterModels).
func fetchOpenRouterModels(ctx context.Context) ([]*ai.Model, error) {
	fmt.Println("Fetching models from OpenRouter API...")
	var data OpenRouterResponse
	if err := httpGetJSON(ctx, openRouterURL, &data); err != nil {
		return nil, err
	}

	var models []*ai.Model
	for i := range data.Data {
		model := &data.Data[i]
		if !containsString(model.SupportedParameters, "tools") {
			continue
		}

		input := []ai.ModelInput{ai.ModelInputText}
		if model.Architecture != nil && model.Architecture.Modality.includes("image") {
			input = append(input, ai.ModelInputImage)
		}

		inputCost := roundCost(parseFloatDefault(model.Pricing, func(p *OpenRouterPricing) *string { return p.Prompt }) * 1_000_000)
		outputCost := roundCost(parseFloatDefault(model.Pricing, func(p *OpenRouterPricing) *string { return p.Completion }) * 1_000_000)
		cacheReadCost := roundCost(parseFloatDefault(model.Pricing, func(p *OpenRouterPricing) *string { return p.InputCacheRead }) * 1_000_000)
		cacheWriteCost := roundCost(parseFloatDefault(model.Pricing, func(p *OpenRouterPricing) *string { return p.InputCacheWrite }) * 1_000_000)

		contextWindow := int64(4096)
		if model.TopProvider != nil && model.TopProvider.ContextLength != nil {
			contextWindow = *model.TopProvider.ContextLength
		} else if model.ContextLength != nil {
			contextWindow = *model.ContextLength
		}

		maxTokens := int64(4096)
		if model.TopProvider != nil && model.TopProvider.MaxCompletionTokens != nil {
			maxTokens = *model.TopProvider.MaxCompletionTokens
		}

		models = append(models, &ai.Model{
			ID:            model.ID,
			Name:          model.Name,
			API:           ai.ApiOpenAICompletions,
			BaseURL:       "https://openrouter.ai/api/v1",
			Provider:      ai.ProviderOpenRouter,
			Reasoning:     containsString(model.SupportedParameters, "reasoning"),
			Input:         input,
			Cost:          ai.ModelCost{ModelCostRates: ai.ModelCostRates{Input: inputCost, Output: outputCost, CacheRead: cacheReadCost, CacheWrite: cacheWriteCost}},
			ContextWindow: contextWindow,
			MaxTokens:     maxTokens,
		})
	}

	fmt.Printf("Fetched %d tool-capable models from OpenRouter\n", len(models))
	return models, nil
}

// parseFloatDefault parses pi's `parseFloat(model.pricing?.X || "0")`.
func parseFloatDefault(p *OpenRouterPricing, get func(*OpenRouterPricing) *string) float64 {
	if p == nil {
		return 0
	}
	v := get(p)
	if v == nil {
		return 0
	}
	f, err := strconv.ParseFloat(*v, 64)
	if err != nil {
		return 0
	}
	return f
}

// fetchAiGatewayModels returns tool-use Vercel AI Gateway models as
// anthropic-messages models (pi: fetchAiGatewayModels).
func fetchAiGatewayModels(ctx context.Context) ([]*ai.Model, error) {
	fmt.Println("Fetching models from Vercel AI Gateway API...")
	var data AIGatewayResponse
	if err := httpGetJSON(ctx, aiGatewayModelsURL, &data); err != nil {
		return nil, err
	}

	var models []*ai.Model
	for i := range data.Data {
		model := &data.Data[i]
		if !containsString(model.Tags, "tool-use") {
			continue
		}

		input := []ai.ModelInput{ai.ModelInputText}
		if containsString(model.Tags, "vision") {
			input = append(input, ai.ModelInputImage)
		}

		pricing := model.Pricing
		inputCost := roundCost(gatewayNumber(pricing, func(p *AIGatewayPricing) json.Number { return p.Input }) * 1_000_000)
		outputCost := roundCost(gatewayNumber(pricing, func(p *AIGatewayPricing) json.Number { return p.Output }) * 1_000_000)
		cacheReadCost := roundCost(gatewayNumber(pricing, func(p *AIGatewayPricing) json.Number { return p.InputCacheRead }) * 1_000_000)
		cacheWriteCost := roundCost(gatewayNumber(pricing, func(p *AIGatewayPricing) json.Number { return p.InputCacheWrite }) * 1_000_000)

		models = append(models, &ai.Model{
			ID:            model.ID,
			Name:          orString(model.Name, model.ID),
			API:           ai.ApiAnthropicMessages,
			BaseURL:       aiGatewayBaseURL,
			Provider:      ai.ProviderVercelAIGateway,
			Reasoning:     containsString(model.Tags, "reasoning"),
			Input:         input,
			Cost:          ai.ModelCost{ModelCostRates: ai.ModelCostRates{Input: inputCost, Output: outputCost, CacheRead: cacheReadCost, CacheWrite: cacheWriteCost}},
			ContextWindow: int64Or(model.ContextWindow, 4096),
			MaxTokens:     int64Or(model.MaxTokens, 4096),
		})
	}

	fmt.Printf("Fetched %d tool-capable models from Vercel AI Gateway\n", len(models))
	return models, nil
}

func gatewayNumber(p *AIGatewayPricing, get func(*AIGatewayPricing) json.Number) float64 {
	if p == nil {
		return 0
	}
	return p.number(get(p))
}
