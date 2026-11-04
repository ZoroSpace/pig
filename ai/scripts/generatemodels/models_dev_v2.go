// pi: catalog/sources/models-dev.ts — models.dev v2 reader and normalizer.
// The experimental v2 schema is read through modelsDevV2Model and normalized
// into the v1-shaped ModelsDevModel the provider rules consume, so the rule
// files stay stable across the source migration.
package main

import (
	"encoding/json"
	"sort"
	"strings"
)

// modelsDevV2Catalog is the models.dev v2 top-level catalog: provider id ->
// {models: {model id: entry}} (pi: ModelsDevV2Catalog).
type modelsDevV2Catalog map[string]*modelsDevV2Provider

type modelsDevV2Provider struct {
	Models map[string]*modelsDevV2Model `json:"models"`
}

// modelsDevV2Model is the subset of a v2 entry the catalog reads (pi:
// ModelsDevV2Model).
type modelsDevV2Model struct {
	ID           string                     `json:"id"`
	Type         string                     `json:"type"`
	Name         string                     `json:"name"`
	Status       string                     `json:"status"`
	Modalities   *ModelsDevModalities       `json:"modalities"`
	Capabilities *modelsDevV2Capabilities   `json:"capabilities"`
	Limit        *ModelsDevLimit            `json:"limit"`
	Cost         *modelsDevV2Cost           `json:"cost"`
	API          map[string]*modelsDevV2API `json:"api"`
}

type modelsDevV2Capabilities struct {
	Tools            *modelsDevV2Tools     `json:"tools"`
	Reasoning        *modelsDevV2Reasoning `json:"reasoning"`
	StructuredOutput bool                  `json:"structured_output"`
}

type modelsDevV2Tools struct {
	Supported bool `json:"supported"`
}

type modelsDevV2Reasoning struct {
	Supported bool               `json:"supported"`
	Toggle    bool               `json:"toggle"`
	Effort    []string           `json:"effort"`
	Budget    *modelsDevV2Budget `json:"budget"`
}

type modelsDevV2Budget struct {
	Min *float64 `json:"min"`
	Max *float64 `json:"max"`
}

type modelsDevV2API struct {
	BaseURL string `json:"base_url"`
	SDK     *struct {
		AISDK string `json:"aisdk"`
	} `json:"sdk"`
}

type modelsDevV2Cost struct {
	Text *modelsDevV2TextCost `json:"text"`
}

type modelsDevV2TextCost struct {
	Input  *modelsDevV2Rate  `json:"input"`
	Output *modelsDevV2Price `json:"output"`
}

type modelsDevV2Rate struct {
	Unit      string
	Price     *float64                `json:"price"`
	Cache     *modelsDevV2CachePrices `json:"cache"`
	Overrides []modelsDevV2Override   `json:"overrides"`
}

type modelsDevV2Price struct {
	Unit      string
	Price     *float64              `json:"price"`
	Overrides []modelsDevV2Override `json:"overrides"`
}

type modelsDevV2CachePrices struct {
	Read     *float64        `json:"read"`
	Write    json.RawMessage `json:"write"`
	Explicit *struct {
		Read  *float64        `json:"read"`
		Write json.RawMessage `json:"write"`
	} `json:"explicit"`
}

type modelsDevV2Override struct {
	When  map[string]any          `json:"when"`
	Price *float64                `json:"price"`
	Cache *modelsDevV2CachePrices `json:"cache"`
}

// normalizeModelsDevV2Catalog converts every provider's entries into the
// v1-shaped catalog. Only chat models are kept; image/speech/realtime/
// decision entries are dropped here (pi: modelsDevChatModels).
func normalizeModelsDevV2Catalog(data modelsDevV2Catalog) ModelsDevCatalog {
	out := ModelsDevCatalog{}
	for providerID, provider := range data {
		if provider == nil || provider.Models == nil {
			continue
		}
		models := make(map[string]*ModelsDevModel, len(provider.Models))
		for modelID, model := range provider.Models {
			// Unlike v1, v2 always states the type. Only chat models enter the
			// chat catalog; image/speech/realtime/decision entries stay out
			// (pi: modelsDevChatModels).
			if model == nil || model.Type != "chat" {
				continue
			}
			models[modelID] = normalizeModelsDevV2Model(model)
		}
		out[providerID] = &ModelsDevProvider{Models: models}
	}
	return out
}

// normalizeModelsDevV2Model is pi normalizeModelsDevModel: booleans become
// the v1 pointer fields, MIME modalities collapse to text/image, and the v2
// nested cost shape flattens to cost.input/output/cache_*.
func normalizeModelsDevV2Model(model *modelsDevV2Model) *ModelsDevModel {
	reasoning := model.Capabilities != nil && model.Capabilities.Reasoning != nil
	var reasoningSupported bool
	if model.Capabilities != nil && model.Capabilities.Reasoning != nil {
		reasoningSupported = model.Capabilities.Reasoning.Supported
	}

	toolCall := model.Capabilities != nil && model.Capabilities.Tools != nil && model.Capabilities.Tools.Supported
	structuredOutput := model.Capabilities != nil && model.Capabilities.StructuredOutput

	normalized := &ModelsDevModel{
		ID:               model.ID,
		Name:             model.Name,
		Type:             model.Type,
		Status:           model.Status,
		ToolCall:         loBool(toolCall),
		StructuredOutput: loBool(structuredOutput),
		Reasoning:        loBool(reasoningSupported),
		ReasoningOptions: normalizeReasoningOptions(model.Capabilities),
		Limit:            model.Limit,
		Cost:             normalizeModelsDevV2Cost(model.Cost),
		Modalities:       normalizeModalities(model.Modalities),
	}
	if reasoning && !reasoningSupported {
		// Keep the explicit false rather than the zero value: some rules test
		// the pointer itself to distinguish "not listed" from "does not reason".
		normalized.Reasoning = loBool(false)
	}
	normalized.APIs = normalizeAPIs(model.API)
	if npm := preferredAPINpm(model, ""); npm != "" {
		normalized.Provider = &ModelsDevProviderInfo{Npm: npm}
	}
	return normalized
}

func loBool(v bool) *bool { return &v }

func normalizeReasoningOptions(capabilities *modelsDevV2Capabilities) []ModelsDevReasoningOption {
	if capabilities == nil || capabilities.Reasoning == nil || !capabilities.Reasoning.Supported {
		return nil
	}
	reasoning := capabilities.Reasoning
	var options []ModelsDevReasoningOption
	if reasoning.Toggle {
		options = append(options, ModelsDevReasoningOption{Type: "toggle"})
	}
	if reasoning.Effort != nil {
		values := make([]*string, len(reasoning.Effort))
		for i := range reasoning.Effort {
			value := reasoning.Effort[i]
			values[i] = &value
		}
		options = append(options, ModelsDevReasoningOption{Type: "effort", Values: values})
	}
	if reasoning.Budget != nil {
		options = append(options, ModelsDevReasoningOption{Type: "budget_tokens", Min: reasoning.Budget.Min, Max: reasoning.Budget.Max})
	}
	return options
}

// normalizeModalities maps v2 MIME input/output lists to the v1 text/image
// vocabulary. pi normalizes to booleans; the provider rules only ask for
// text and image.
func normalizeModalities(modalities *ModelsDevModalities) *ModelsDevModalities {
	if modalities == nil {
		return nil
	}
	normalize := func(values []string) []string {
		var out []string
		for _, value := range values {
			switch {
			case value == "text":
				out = appendUnique(out, "text")
			case value == "image" || strings.HasPrefix(value, "image/"):
				out = appendUnique(out, "image")
			}
		}
		return out
	}
	return &ModelsDevModalities{Input: normalize(modalities.Input), Output: normalize(modalities.Output)}
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func normalizeModelsDevV2Cost(cost *modelsDevV2Cost) *ModelsDevCost {
	if cost == nil || cost.Text == nil || cost.Text.Input == nil || cost.Text.Output == nil {
		return nil
	}
	input := cost.Text.Input
	output := cost.Text.Output
	if !perToken(input.Unit) || !perToken(output.Unit) {
		return nil
	}
	normalized := &ModelsDevCost{
		Input:      input.Price,
		Output:     output.Price,
		CacheRead:  input.Cache.readPrice(),
		CacheWrite: input.Cache.writePrice(),
	}
	var tiers []*ModelsDevCostTier
	tierIndex := map[int64]int{}
	tier := func(above int64) *ModelsDevCostTier {
		if index, ok := tierIndex[above]; ok {
			return tiers[index]
		}
		size := above
		entry := &ModelsDevCostTier{Tier: &ModelsDevTier{Type: "context", Size: &size}}
		tierIndex[above] = len(tiers)
		tiers = append(tiers, entry)
		return entry
	}
	for _, override := range input.Overrides {
		above, ok := contextThreshold(override.When)
		if !ok {
			continue
		}
		entry := tier(above)
		if override.Price != nil {
			entry.Input = override.Price
		}
		if override.Cache != nil {
			if override.Cache.Read != nil {
				entry.CacheRead = override.Cache.Read
			}
			if write := override.Cache.writePrice(); write != nil {
				entry.CacheWrite = write
			}
		}
	}
	for _, override := range output.Overrides {
		above, ok := contextThreshold(override.When)
		if !ok {
			continue
		}
		if override.Price != nil {
			tier(above).Output = override.Price
		}
	}
	for _, entry := range tiers {
		normalized.Tiers = append(normalized.Tiers, *entry)
	}
	return normalized
}

func perToken(unit string) bool {
	return unit == "" || unit == "token" || unit == "tokens"
}

func contextThreshold(when map[string]any) (int64, bool) {
	if len(when) != 1 {
		return 0, false
	}
	context, ok := when["context"].(map[string]any)
	if !ok {
		return 0, false
	}
	for _, key := range []string{"gt", "gte"} {
		if value, ok := numberAsInt64(context[key]); ok {
			return value, true
		}
	}
	return 0, false
}

func numberAsInt64(value any) (int64, bool) {
	switch v := value.(type) {
	case float64:
		return int64(v), true
	case json.Number:
		parsed, err := v.Int64()
		return parsed, err == nil
	}
	return 0, false
}

// normalizeAPIs preserves models.dev's API entries (pi: normalizeModelsDevModel
// apis) in the order the provider rules probe.
func normalizeAPIs(apis map[string]*modelsDevV2API) []ModelsDevAPI {
	var out []ModelsDevAPI
	for _, protocol := range []string{"responses", "messages", "generate-content", "chat-completions"} {
		entry := apis[protocol]
		if entry == nil {
			continue
		}
		normalized := ModelsDevAPI{Protocol: protocol, BaseURL: entry.BaseURL}
		if entry.SDK != nil {
			normalized.AISDK = entry.SDK.AISDK
		}
		out = append(out, normalized)
	}
	return out
}

func (cache *modelsDevV2CachePrices) readPrice() *float64 {
	if cache == nil {
		return nil
	}
	return cache.Read
}

func (cache *modelsDevV2CachePrices) writePrice() *float64 {
	if cache == nil {
		return nil
	}
	if price := firstCacheWrite(cache.Write); price != nil {
		return price
	}
	if cache.Explicit != nil {
		return firstCacheWrite(cache.Explicit.Write)
	}
	return nil
}

// firstCacheWrite takes the first TTL's write price, the host's default
// (pi: firstPrice / cacheWritePrice).
func firstCacheWrite(raw json.RawMessage) *float64 {
	if len(raw) == 0 {
		return nil
	}
	var single float64
	if err := json.Unmarshal(raw, &single); err == nil {
		return &single
	}
	var byTTL map[string]float64
	if err := json.Unmarshal(raw, &byTTL); err == nil {
		// pi takes Object.values(...)[0]. JSON object insertion order is lost
		// in a Go map, so prefer the default 5m TTL and otherwise pick the
		// lexicographically first TTL to stay deterministic.
		if price, ok := byTTL["5m"]; ok {
			return &price
		}
		keys := make([]string, 0, len(byTTL))
		for key := range byTTL {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		if len(keys) > 0 {
			price := byTTL[keys[0]]
			return &price
		}
	}
	return nil
}

// preferredAPINpm selects the SDK hint for the provider rule that still keys
// on npm (OpenCode). The protocol order mirrors pi's OPENCODE_PROTOCOLS.
func preferredAPINpm(model *modelsDevV2Model, _ string) string {
	for _, protocol := range []string{"responses", "messages", "generate-content", "chat-completions"} {
		entry := model.API[protocol]
		if entry == nil || entry.SDK == nil {
			continue
		}
		if entry.SDK.AISDK != "" {
			return entry.SDK.AISDK
		}
	}
	return ""
}
