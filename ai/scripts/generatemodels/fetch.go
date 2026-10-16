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
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/providers"
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

// anthropicPrefixRegexp matches pi's /^anthropic\// OpenRouter routing test
// (Go raw string: the slash needs no escape).
var anthropicPrefixRegexp = regexp.MustCompile(`^anthropic/`)

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

// parseFloatDefault parses pi's `parseFloat(model.pricing?.X || "0")`.
func parseFloatDefault(p *providers.OpenRouterPricing, get func(*providers.OpenRouterPricing) *string) float64 {
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

// aiGatewayCatalog splits the Vercel AI Gateway listing by operation (pi:
// fetchAiGatewayModels return shape).
type aiGatewayCatalog struct {
	chat        []*ai.Model
	classifiers []*ai.Model
}

// aiGatewayTypesafeBaseURL serves evaluation models such as TypeSafe's Jev
// through the TypeSafe-compatible System One endpoint (pi:
// AI_GATEWAY_TYPESAFE_BASE_URL).
// https://vercel.com/docs/ai-gateway/sdks-and-apis/typesafe
const aiGatewayTypesafeBaseURL = "https://ai-gateway.vercel.sh/typesafe/v1"

// fetchAiGatewayModels returns tool-use Vercel AI Gateway chat models plus
// evaluation classifier models (pi: fetchAiGatewayModels).
func fetchAiGatewayModels(ctx context.Context) (*aiGatewayCatalog, error) {
	fmt.Println("Fetching models from Vercel AI Gateway API...")
	var data AIGatewayResponse
	if err := httpGetJSON(ctx, aiGatewayModelsURL, &data); err != nil {
		return nil, err
	}

	catalog := &aiGatewayCatalog{}
	for i := range data.Data {
		model := &data.Data[i]
		// Evaluation models such as TypeSafe's Jev are served through the
		// TypeSafe-compatible System One endpoint.
		if model.Type == "evaluation" {
			catalog.classifiers = append(catalog.classifiers, &ai.Model{
				Type:     ai.ModelTypeClassifier,
				ID:       model.ID,
				Name:     orString(model.Name, model.ID),
				API:      ai.ClassifierApiTypesafeSystemOne,
				Provider: ai.ProviderVercelAIGateway,
				BaseURL:  aiGatewayTypesafeBaseURL,
				Input:    []ai.ModelInput{ai.ModelInputText},
				Cost: ai.ModelCost{ModelCostRates: ai.ModelCostRates{
					Input:  roundCost(gatewayNumber(model.Pricing, func(p *AIGatewayPricing) json.Number { return p.Input }) * 1_000_000),
					Output: roundCost(gatewayNumber(model.Pricing, func(p *AIGatewayPricing) json.Number { return p.Output }) * 1_000_000),
				}},
				ContextWindow: int64Or(model.ContextWindow, 4096),
			})
			continue
		}
		if !containsString(model.Tags, "tool-use") {
			continue
		}

		input := []ai.ModelInput{ai.ModelInputText}
		if containsString(model.Tags, "vision") {
			input = append(input, ai.ModelInputImage)
		}

		catalog.chat = append(catalog.chat, &ai.Model{
			ID:        model.ID,
			Name:      orString(model.Name, model.ID),
			API:       ai.ApiAnthropicMessages,
			BaseURL:   aiGatewayBaseURL,
			Provider:  ai.ProviderVercelAIGateway,
			Reasoning: containsString(model.Tags, "reasoning"),
			Input:     input,
			// v0.86.0 ("preserve Vercel AI Gateway unsigned thinking"): the
			// gateway strips thinking signatures, so replay accepts empty ones.
			Compat: &ai.AnthropicMessagesCompat{AllowEmptySignature: lo.ToPtr(true)},
			// S17 (pi 943a10e74): per-rate prompt-length brackets become
			// request-wide tiers (pi: cost: getAiGatewayCost(model.pricing)).
			Cost:          getAiGatewayCost(model.Pricing),
			ContextWindow: int64Or(model.ContextWindow, 4096),
			MaxTokens:     int64Or(model.MaxTokens, 4096),
		})
	}

	fmt.Printf("Fetched %d tool-capable and %d classifier models from Vercel AI Gateway\n", len(catalog.chat), len(catalog.classifiers))
	return catalog, nil
}

// modelsDevClassifierURL lists models.dev decision (classifier) models (pi:
// loadModelsDevClassifierModels).
const modelsDevClassifierURL = "https://models.dev/models.json?type=decision"

// loadModelsDevClassifierModels returns the direct TypeSafe Jev classifier
// (pi: loadModelsDevClassifierModels). System One reports token usage, so
// classify() results carry token counts but price them at zero — the
// canonical models.dev entry has no direct-provider pricing.
func loadModelsDevClassifierModels(ctx context.Context) ([]*ai.Model, error) {
	fmt.Println("Fetching classifier models from models.dev API...")
	// The response is a flat metadata record keyed by provider/model id (pi:
	// Record<string, ModelsDevMetadata>, data["typesafe/jev-latest"]).
	var data map[string]*ModelsDevModel
	if err := httpGetJSON(ctx, modelsDevClassifierURL, &data); err != nil {
		return nil, err
	}
	entry := data["typesafe/jev-latest"]
	if entry == nil || entry.Type != "decision" {
		return nil, fmt.Errorf("models.dev did not return decision model typesafe/jev-latest")
	}
	input := []ai.ModelInput{ai.ModelInputText}
	if entry.Modalities != nil && containsString(entry.Modalities.Input, "image") {
		input = append(input, ai.ModelInputImage)
	}
	contextWindow := int64(64000)
	if entry.Limit != nil && entry.Limit.Context != nil {
		contextWindow = *entry.Limit.Context
	}
	return []*ai.Model{{
		Type:          ai.ModelTypeClassifier,
		ID:            "jev-latest",
		Name:          entry.Name,
		API:           ai.ClassifierApiTypesafeSystemOne,
		Provider:      "typesafe",
		BaseURL:       "https://api.typesafe.ai/v1/",
		Input:         input,
		Cost:          ai.ModelCost{},
		ContextWindow: contextWindow,
	}}, nil
}

func gatewayNumber(p *AIGatewayPricing, get func(*AIGatewayPricing) json.Number) float64 {
	if p == nil {
		return 0
	}
	return p.number(get(p))
}
