// pi: generate-models.ts — data-source types (ModelsDevModel, OpenRouter,
// Vercel AI Gateway). The generator is a standalone main package: it imports
// github.com/ZoroSpace/pig/ai for the Model/compat types it emits into
// ai/models_generated.go.
package main

import (
	"encoding/json"
	"math"
	"strings"

	"github.com/ZoroSpace/pig/ai"
)

// ModelsDevModel is one models.dev catalog entry (pi: ModelsDevModel).
// models.dev returns the catalog as { providerId: { models: { modelId: ... } } }.
type ModelsDevModel struct {
	ID               string                     `json:"id"`
	Name             string                     `json:"name"`
	ToolCall         *bool                      `json:"tool_call"`
	StructuredOutput *bool                      `json:"structured_output"`
	Reasoning        *bool                      `json:"reasoning"`
	ReasoningOptions []ModelsDevReasoningOption `json:"reasoning_options"`
	Status           string                     `json:"status"`
	Limit            *ModelsDevLimit            `json:"limit"`
	Cost             *ModelsDevCost             `json:"cost"`
	Modalities       *ModelsDevModalities       `json:"modalities"`
	Provider         *ModelsDevProviderInfo     `json:"provider"`
}

// ModelsDevLimit is a model's context/output token limits (pi: limit).
type ModelsDevLimit struct {
	Context *int64 `json:"context"`
	Output  *int64 `json:"output"`
}

// ModelsDevCost is a model's pricing (pi: cost).
type ModelsDevCost struct {
	Input      *float64            `json:"input"`
	Output     *float64            `json:"output"`
	CacheRead  *float64            `json:"cache_read"`
	CacheWrite *float64            `json:"cache_write"`
	Tiers      []ModelsDevCostTier `json:"tiers"`
}

// ModelsDevCostTier is one request-wide pricing tier (pi: cost.tiers[]).
type ModelsDevCostTier struct {
	Input      *float64       `json:"input"`
	Output     *float64       `json:"output"`
	CacheRead  *float64       `json:"cache_read"`
	CacheWrite *float64       `json:"cache_write"`
	Tier       *ModelsDevTier `json:"tier"`
}

// ModelsDevTier names a tier's threshold type and size (pi: tier).
type ModelsDevTier struct {
	Type string `json:"type"`
	Size *int64 `json:"size"`
}

// ModelsDevModalities lists a model's input/output modalities (pi: modalities).
type ModelsDevModalities struct {
	Input  []string `json:"input"`
	Output []string `json:"output"`
}

// ModelsDevProviderInfo is the models.dev npm hint used to route OpenCode
// models (pi: provider.npm).
type ModelsDevProviderInfo struct {
	Npm string `json:"npm"`
}

// ModelsDevProvider is one models.dev provider entry (pi: ModelsDevProvider).
type ModelsDevProvider struct {
	Models map[string]*ModelsDevModel `json:"models"`
}

// ModelsDevCatalog is the models.dev top-level catalog: provider id -> entry.
type ModelsDevCatalog map[string]*ModelsDevProvider

// OpenRouterResponse is the OpenRouter /api/v1/models payload.
type OpenRouterResponse struct {
	Data []OpenRouterModel `json:"data"`
}

// OpenRouterModel is one OpenRouter model (pi: fetchOpenRouterModels).
type OpenRouterModel struct {
	ID                  string                  `json:"id"`
	Name                string                  `json:"name"`
	SupportedParameters []string                `json:"supported_parameters"`
	Architecture        *OpenRouterArchitecture `json:"architecture"`
	Pricing             *OpenRouterPricing      `json:"pricing"`
	TopProvider         *OpenRouterTopProvider  `json:"top_provider"`
	ContextLength       *int64                  `json:"context_length"`
}

// OpenRouterArchitecture carries input modalities. pi calls
// architecture.modality.includes("image"); the real API sends a "text->image"
// string, so the type is string-or-list to preserve both shapes.
type OpenRouterArchitecture struct {
	Modality stringOrList `json:"modality"`
}

// OpenRouterPricing is per-token USD as strings (pi: pricing).
type OpenRouterPricing struct {
	Prompt          *string `json:"prompt"`
	Completion      *string `json:"completion"`
	InputCacheRead  *string `json:"input_cache_read"`
	InputCacheWrite *string `json:"input_cache_write"`
}

// OpenRouterTopProvider carries the top provider's limits (pi: top_provider).
type OpenRouterTopProvider struct {
	ContextLength       *int64 `json:"context_length"`
	MaxCompletionTokens *int64 `json:"max_completion_tokens"`
}

// stringOrList decodes a JSON value that may be a string ("text->image") or a
// list of strings; includes() matches either shape (pi's architecture.modality
// .includes("image")).
type stringOrList struct {
	str  string
	list []string
}

func (s *stringOrList) UnmarshalJSON(data []byte) error {
	var str string
	if err := json.Unmarshal(data, &str); err == nil {
		s.str = str
		s.list = nil
		return nil
	}
	return json.Unmarshal(data, &s.list)
}

func (s *stringOrList) includes(v string) bool {
	if s == nil {
		return false
	}
	if s.str != "" {
		return strings.Contains(s.str, v)
	}
	for _, x := range s.list {
		if x == v {
			return true
		}
	}
	return false
}

// AIGatewayResponse is the Vercel AI Gateway /v1/models payload.
type AIGatewayResponse struct {
	Data []AIGatewayModel `json:"data"`
}

// AIGatewayModel is one Vercel AI Gateway model (pi: AiGatewayModel).
type AIGatewayModel struct {
	ID            string            `json:"id"`
	Name          string            `json:"name"`
	ContextWindow *int64            `json:"context_window"`
	MaxTokens     *int64            `json:"max_tokens"`
	Tags          []string          `json:"tags"`
	Pricing       *AIGatewayPricing `json:"pricing"`
}

// AIGatewayPricing is per-token USD as string|number (pi: pricing).
type AIGatewayPricing struct {
	Input           json.Number `json:"input"`
	Output          json.Number `json:"output"`
	InputCacheRead  json.Number `json:"input_cache_read"`
	InputCacheWrite json.Number `json:"input_cache_write"`
}

// number returns the pi toNumber(value) value: non-finite and empty become 0.
func (p *AIGatewayPricing) number(v json.Number) float64 {
	if v == "" {
		return 0
	}
	f, err := v.Float64()
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0
	}
	return f
}

// boolVal is pi's `x === true` over an optional bool.
func boolVal(b *bool) bool { return b != nil && *b }

// orString is pi's `x || fallback` for strings.
func orString(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// int64Or is pi's `x?.limit?.context || fallback` (0 and missing both fall back).
func int64Or(v *int64, fallback int64) int64 {
	if v == nil || *v == 0 {
		return fallback
	}
	return *v
}

// floatVal is pi's `x?.cost?.input || 0`.
func floatVal(v *float64) float64 {
	if v == nil {
		return 0
	}
	return *v
}

// limitContext / limitOutput dereference ModelsDevLimit for int64Or.
func limitContext(m *ModelsDevModel) *int64 {
	if m.Limit == nil {
		return nil
	}
	return m.Limit.Context
}

func limitOutput(m *ModelsDevModel) *int64 {
	if m.Limit == nil {
		return nil
	}
	return m.Limit.Output
}

// containsString reports whether a slice contains a value.
func containsString(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

// inputOf builds the Model.Input modalities from models.dev modalities
// (pi: `m.modalities?.input?.includes("image") ? ["text", "image"] : ["text"]`).
func inputOf(m *ModelsDevModel) []ai.ModelInput {
	if m.Modalities != nil && containsString(m.Modalities.Input, "image") {
		return []ai.ModelInput{ai.ModelInputText, ai.ModelInputImage}
	}
	return []ai.ModelInput{ai.ModelInputText}
}

// modelsDevCostOf converts a models.dev cost to ModelCost (pi: the inline
// `cost: { input: m.cost?.input || 0, ... }` four-field form; tiers are not
// carried for the ported providers — pi uses getModelsDevCost only for
// github-copilot, which pig has not ported yet).
func modelsDevCostOf(c *ModelsDevCost) ai.ModelCost {
	var input, output, cacheRead, cacheWrite *float64
	var tiers []ai.ModelCostTier
	if c != nil {
		input, output, cacheRead, cacheWrite = c.Input, c.Output, c.CacheRead, c.CacheWrite
		// pi: getModelsDevCost keeps context-sized tiers only.
		for _, tier := range c.Tiers {
			if tier.Tier == nil || tier.Tier.Type != "context" || tier.Tier.Size == nil {
				continue
			}
			tiers = append(tiers, ai.ModelCostTier{
				ModelCostRates: ai.ModelCostRates{
					Input:      floatVal(tier.Input),
					Output:     floatVal(tier.Output),
					CacheRead:  floatVal(tier.CacheRead),
					CacheWrite: floatVal(tier.CacheWrite),
				},
				InputTokensAbove: *tier.Tier.Size,
			})
		}
	}
	return ai.ModelCost{
		ModelCostRates: ai.ModelCostRates{
			Input:      floatVal(input),
			Output:     floatVal(output),
			CacheRead:  floatVal(cacheRead),
			CacheWrite: floatVal(cacheWrite),
		},
		Tiers: tiers,
	}
}

// roundCost is pi's Number(value.toFixed(6)).
func roundCost(value float64) float64 {
	return math.Round(value*1_000_000) / 1_000_000
}
