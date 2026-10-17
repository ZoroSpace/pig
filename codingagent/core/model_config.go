// pi: src/core/model-config.ts — an immutable, credential-blind models.json
// snapshot.
//
// Segment deviations (declared):
//   - pi validates with a compiled TypeBox schema and renders localized
//     validation errors; pig hand-checks the same schema over the decoded
//     JSON. Error message wording approximates TypeBox's (no pi test pins
//     schema-error text; the surrounding "Failed to load/parse models.json"
//     and "File: <path>" wrappers are verbatim and pinned by tests).
//   - pi deep-freezes a structuredClone of each provider entry; pig decodes
//     into fresh structs per load (equivalent immutability by construction).
//   - load() is async in pi (node fs promises); pig reads synchronously.
package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/ZoroSpace/pig/codingagent/utils"
)

// ModelsJsonPercentileCutoffs is the p50/p75/p90/p99 cutoff object (pi:
// PercentileCutoffsSchema).
type ModelsJsonPercentileCutoffs struct {
	P50 *float64 `json:"p50,omitempty"`
	P75 *float64 `json:"p75,omitempty"`
	P90 *float64 `json:"p90,omitempty"`
	P99 *float64 `json:"p99,omitempty"`
}

// ModelsJsonOpenRouterRouting is the openrouter routing compat block (pi:
// OpenRouterRoutingSchema).
type ModelsJsonOpenRouterRouting struct {
	AllowFallbacks         *bool          `json:"allow_fallbacks,omitempty"`
	RequireParameters      *bool          `json:"require_parameters,omitempty"`
	DataCollection         string         `json:"data_collection,omitempty"` // "deny" | "allow"
	ZDR                    *bool          `json:"zdr,omitempty"`
	EnforceDistillableText *bool          `json:"enforce_distillable_text,omitempty"`
	Order                  []string       `json:"order,omitempty"`
	Only                   []string       `json:"only,omitempty"`
	Ignore                 []string       `json:"ignore,omitempty"`
	Quantizations          []string       `json:"quantizations,omitempty"`
	Sort                   any            `json:"sort,omitempty"`                     // string | {by, partition}
	MaxPrice               map[string]any `json:"max_price,omitempty"`                // string|number values
	PreferredMinThroughput any            `json:"preferred_min_throughput,omitempty"` // number | percentile cutoffs
	PreferredMaxLatency    any            `json:"preferred_max_latency,omitempty"`    // number | percentile cutoffs
}

// ModelsJsonVercelGatewayRouting is the vercel gateway routing compat block
// (pi: VercelGatewayRoutingSchema).
type ModelsJsonVercelGatewayRouting struct {
	Only  []string `json:"only,omitempty"`
	Order []string `json:"order,omitempty"`
}

// ModelsJsonSamplingParamsByThinkingLevel maps pi thinking levels to
// per-level sampling parameter overrides (pi:
// SamplingParamsByThinkingLevelSchema, v1.0.2 #9776).
type ModelsJsonSamplingParamsByThinkingLevel map[string]map[string]any

// ModelsJsonThinkingLevelMap maps pi thinking levels to provider-specific
// values (pi: ThinkingLevelMapSchema, string | null members). A nil entry
// that is Present means an explicit null (the level is unsupported); a
// missing key is absent from Present.
type ModelsJsonThinkingLevelMap struct {
	Off     *string `json:"off,omitempty"`
	Minimal *string `json:"minimal,omitempty"`
	Low     *string `json:"low,omitempty"`
	Medium  *string `json:"medium,omitempty"`
	High    *string `json:"high,omitempty"`
	Xhigh   *string `json:"xhigh,omitempty"`
	Max     *string `json:"max,omitempty"`

	// Present records the keys the JSON carried (pig addition: the
	// missing/null distinction matters to ThinkingLevelMap merging).
	Present map[string]bool `json:"-"`
}

func (m *ModelsJsonThinkingLevelMap) UnmarshalJSON(data []byte) error {
	type alias ModelsJsonThinkingLevelMap
	var plain alias
	if err := json.Unmarshal(data, &plain); err != nil {
		return err
	}
	*m = ModelsJsonThinkingLevelMap(plain)
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	m.Present = map[string]bool{}
	for key := range raw {
		m.Present[key] = true
	}
	return nil
}

// ModelsJsonChatTemplateKwargVariable is the {$var, omitWhenOff} kwarg form
// (pi: ChatTemplateKwargVariableSchema).
type ModelsJsonChatTemplateKwargVariable struct {
	Var         string `json:"$var"` // "thinking.enabled" | "thinking.effort"
	OmitWhenOff *bool  `json:"omitWhenOff,omitempty"`
}

// ModelsJsonChatTemplateKwarg is a scalar or {$var} kwarg (pi:
// ChatTemplateKwargSchema: string | number | boolean | null | variable).
type ModelsJsonChatTemplateKwarg struct {
	Scalar any
	Var    *ModelsJsonChatTemplateKwargVariable
}

func (k *ModelsJsonChatTemplateKwarg) UnmarshalJSON(data []byte) error {
	var variable ModelsJsonChatTemplateKwargVariable
	if err := json.Unmarshal(data, &variable); err == nil && variable.Var != "" {
		k.Var = &variable
		return nil
	}
	return json.Unmarshal(data, &k.Scalar)
}

func (k ModelsJsonChatTemplateKwarg) MarshalJSON() ([]byte, error) {
	if k.Var != nil {
		return json.Marshal(k.Var)
	}
	return json.Marshal(k.Scalar)
}

// ModelsJsonCostTier is one request-wide pricing tier (pi:
// ModelCostTierSchema).
type ModelsJsonCostTier struct {
	InputTokensAbove float64 `json:"inputTokensAbove"`
	Input            float64 `json:"input"`
	Output           float64 `json:"output"`
	CacheRead        float64 `json:"cacheRead"`
	CacheWrite       float64 `json:"cacheWrite"`
}

// ModelsJsonCost is the required-rate cost object of a model definition (pi:
// ModelCostSchema).
type ModelsJsonCost struct {
	Input      float64              `json:"input"`
	Output     float64              `json:"output"`
	CacheRead  float64              `json:"cacheRead"`
	CacheWrite float64              `json:"cacheWrite"`
	Tiers      []ModelsJsonCostTier `json:"tiers,omitempty"`
}

// ModelsJsonOverrideCost is the all-optional cost object of a model
// override (pi: ModelOverrideSchema.cost).
type ModelsJsonOverrideCost struct {
	Input      *float64             `json:"input,omitempty"`
	Output     *float64             `json:"output,omitempty"`
	CacheRead  *float64             `json:"cacheRead,omitempty"`
	CacheWrite *float64             `json:"cacheWrite,omitempty"`
	Tiers      []ModelsJsonCostTier `json:"tiers,omitempty"`
}

// ModelsJsonPromptCache is a best-effort prompt cache lifetime in seconds
// per retention tier (pi: ModelPromptCacheSchema; values must be > 0).
type ModelsJsonPromptCache struct {
	Short *float64 `json:"short,omitempty"`
	Long  *float64 `json:"long,omitempty"`
}

// ModelsJsonInputLimits is the models.json inputLimits object (pi:
// ModelInputLimitsSchema, v0.87.0 #9631).
type ModelsJsonInputLimits struct {
	MaxRequestBytes *float64               `json:"maxRequestBytes,omitempty"`
	Images          *ModelsJsonImageLimits `json:"images,omitempty"`
}

// ModelsJsonImageLimits is the models.json inputLimits.images object (pi:
// the nested images schema).
type ModelsJsonImageLimits struct {
	Resize        *ModelsJsonImageResize `json:"resize,omitempty"`
	MaxPerMessage *float64               `json:"maxPerMessage,omitempty"`
	MaxPerRequest *float64               `json:"maxPerRequest,omitempty"`
}

// ModelsJsonImageResize is the models.json resize profile (pi:
// ImageResizeSchema).
type ModelsJsonImageResize struct {
	MaxWidth    *float64 `json:"maxWidth,omitempty"`
	MaxHeight   *float64 `json:"maxHeight,omitempty"`
	MaxBytes    *float64 `json:"maxBytes,omitempty"`
	JpegQuality *float64 `json:"jpegQuality,omitempty"`
}

// ModelsJsonModel is one model definition (pi: ModelDefinitionSchema).
type ModelsJsonModel struct {
	ID               string                      `json:"id"`
	Name             string                      `json:"name,omitempty"`
	API              string                      `json:"api,omitempty"`
	BaseURL          string                      `json:"baseUrl,omitempty"`
	Reasoning        *bool                       `json:"reasoning,omitempty"`
	ThinkingLevelMap *ModelsJsonThinkingLevelMap `json:"thinkingLevelMap,omitempty"`
	Input            []string                    `json:"input,omitempty"` // "text" | "image"
	InputLimits      *ModelsJsonInputLimits      `json:"inputLimits,omitempty"`
	Cost             *ModelsJsonCost             `json:"cost,omitempty"`
	PromptCache      *ModelsJsonPromptCache      `json:"promptCache,omitempty"`
	ContextWindow    *float64                    `json:"contextWindow,omitempty"`
	MaxTokens        *float64                    `json:"maxTokens,omitempty"`
	SamplingParams   map[string]any              `json:"samplingParams,omitempty"`
	// SamplingParamsByThinkingLevel holds per-pi-thinking-level sampling
	// overrides (pi: SamplingParamsByThinkingLevelSchema, v1.0.2 #9776).
	SamplingParamsByThinkingLevel ModelsJsonSamplingParamsByThinkingLevel `json:"samplingParamsByThinkingLevel,omitempty"`
	Headers                       map[string]string                       `json:"headers,omitempty"`
	Compat                        json.RawMessage                         `json:"compat,omitempty"`
}

// ModelsJsonModelOverride is one model override (pi:
// ModelOverrideSchema; no id — overrides attach by model id key).
type ModelsJsonModelOverride struct {
	Name             string                      `json:"name,omitempty"`
	Reasoning        *bool                       `json:"reasoning,omitempty"`
	ThinkingLevelMap *ModelsJsonThinkingLevelMap `json:"thinkingLevelMap,omitempty"`
	Input            []string                    `json:"input,omitempty"`
	InputLimits      *ModelsJsonInputLimits      `json:"inputLimits,omitempty"`
	Cost             *ModelsJsonOverrideCost     `json:"cost,omitempty"`
	PromptCache      *ModelsJsonPromptCache      `json:"promptCache,omitempty"`
	ContextWindow    *float64                    `json:"contextWindow,omitempty"`
	MaxTokens        *float64                    `json:"maxTokens,omitempty"`
	SamplingParams   map[string]any              `json:"samplingParams,omitempty"`
	// SamplingParamsByThinkingLevel holds per-pi-thinking-level sampling
	// overrides (pi: SamplingParamsByThinkingLevelSchema, v1.0.2 #9776).
	SamplingParamsByThinkingLevel ModelsJsonSamplingParamsByThinkingLevel `json:"samplingParamsByThinkingLevel,omitempty"`
	Headers                       map[string]string                       `json:"headers,omitempty"`
	Compat                        json.RawMessage                         `json:"compat,omitempty"`
}

// ModelsJsonProvider is one provider config (pi: ProviderConfigSchema).
type ModelsJsonProvider struct {
	Name           string                              `json:"name,omitempty"`
	BaseURL        string                              `json:"baseUrl,omitempty"`
	APIKey         string                              `json:"apiKey,omitempty"`
	API            string                              `json:"api,omitempty"`
	OAuth          string                              `json:"oauth,omitempty"` // "radius"
	Headers        map[string]string                   `json:"headers,omitempty"`
	Compat         json.RawMessage                     `json:"compat,omitempty"`
	AuthHeader     *bool                               `json:"authHeader,omitempty"`
	Models         []*ModelsJsonModel                  `json:"models,omitempty"`
	ModelOverrides map[string]*ModelsJsonModelOverride `json:"modelOverrides,omitempty"`
}

// schemaError is one validation finding rendered like TypeBox's errors (path
// segments joined with "."; pi: formatValidationPath).
type schemaError struct {
	path    string
	message string
}

func expectString(value any, path string, errs *[]schemaError) bool {
	s, ok := value.(string)
	if !ok || s == "" {
		*errs = append(*errs, schemaError{path: path, message: "expected a non-empty string"})
		return false
	}
	return true
}

func expectBool(value any, path string, errs *[]schemaError) {
	if _, ok := value.(bool); !ok {
		*errs = append(*errs, schemaError{path: path, message: "expected boolean"})
	}
}

func expectNumber(value any, path string, errs *[]schemaError) {
	if _, ok := value.(float64); !ok {
		*errs = append(*errs, schemaError{path: path, message: "expected number"})
	}
}

// expectInteger mirrors pi's Type.Integer({minimum, maximum}) for the
// inputLimits schema fields (v0.87.0 #9631); maximum 0 = no maximum.
func expectInteger(value any, path string, minimum int, maximum int, errs *[]schemaError) {
	number, ok := value.(float64)
	if !ok {
		*errs = append(*errs, schemaError{path: path, message: "expected number"})
		return
	}
	if number != float64(int(number)) {
		*errs = append(*errs, schemaError{path: path, message: "expected integer"})
		return
	}
	if int(number) < minimum {
		*errs = append(*errs, schemaError{path: path, message: fmt.Sprintf("expected integer >= %d", minimum)})
	}
	if maximum != 0 && int(number) > maximum {
		*errs = append(*errs, schemaError{path: path, message: fmt.Sprintf("expected integer <= %d", maximum)})
	}
}

func expectObject(value any, path string, errs *[]schemaError) bool {
	_, ok := value.(map[string]any)
	if !ok {
		*errs = append(*errs, schemaError{path: path, message: "expected object"})
	}
	return ok
}

var modelsJsonInputValues = map[string]bool{"text": true, "image": true}

// "volcengine" is pig-only (ai.ThinkingFormatVolcengine, the built-in
// volcengine-plan provider's format) but must stay configurable through
// models.json too — the validator missing it rejected the whole file
// (2026-10-12, docs/fix/2026-10-12-models-json-volcengine-thinking-format.md).
var modelsJsonThinkingFormatValues = map[string]bool{
	"openai": true, "openrouter": true, "together": true, "baseten": true,
	"deepseek": true, "zai": true, "qwen": true, "chat-template": true,
	"qwen-chat-template": true, "string-thinking": true, "ant-ling": true,
	"volcengine": true,
}

func expectInputArray(value any, path string, errs *[]schemaError) {
	items, ok := value.([]any)
	if !ok {
		*errs = append(*errs, schemaError{path: path, message: "expected array"})
		return
	}
	for i, item := range items {
		s, ok := item.(string)
		if !ok || !modelsJsonInputValues[s] {
			*errs = append(*errs, schemaError{path: fmt.Sprintf("%s.%d", path, i), message: `expected "text" or "image"`})
		}
	}
}

func expectHeaders(value any, path string, errs *[]schemaError) {
	headers, ok := value.(map[string]any)
	if !ok {
		*errs = append(*errs, schemaError{path: path, message: "expected object"})
		return
	}
	for key, entry := range headers {
		if _, ok := entry.(string); !ok {
			*errs = append(*errs, schemaError{path: path + "." + key, message: "expected string"})
		}
	}
}

func expectStringArray(value any, path string, errs *[]schemaError) {
	items, ok := value.([]any)
	if !ok {
		*errs = append(*errs, schemaError{path: path, message: "expected array"})
		return
	}
	for i, item := range items {
		if _, ok := item.(string); !ok {
			*errs = append(*errs, schemaError{path: fmt.Sprintf("%s.%d", path, i), message: "expected string"})
		}
	}
}

func expectPercentileCutoffs(value any, path string, errs *[]schemaError) bool {
	cutoffs, ok := value.(map[string]any)
	if !ok {
		return false
	}
	for _, key := range []string{"p50", "p75", "p90", "p99"} {
		if entry, present := cutoffs[key]; present {
			if _, ok := entry.(float64); !ok {
				*errs = append(*errs, schemaError{path: path + "." + key, message: "expected number"})
			}
		}
	}
	return true
}

func validateChatTemplateKwargs(value any, path string, errs *[]schemaError) {
	kwargs, ok := value.(map[string]any)
	if !ok {
		*errs = append(*errs, schemaError{path: path, message: "expected object"})
		return
	}
	for key, kwarg := range kwargs {
		kwargPath := path + "." + key
		if kwargMap, ok := kwarg.(map[string]any); ok {
			varValue, _ := kwargMap["$var"].(string)
			if varValue != "thinking.enabled" && varValue != "thinking.effort" {
				*errs = append(*errs, schemaError{path: kwargPath + ".$var", message: `expected "thinking.enabled" or "thinking.effort"`})
			}
			if omit, present := kwargMap["omitWhenOff"]; present {
				expectBool(omit, kwargPath+".omitWhenOff", errs)
			}
			continue
		}
		switch kwarg.(type) {
		case string, float64, bool, nil:
		default:
			*errs = append(*errs, schemaError{path: kwargPath, message: "expected a scalar or {$var} object"})
		}
	}
}

func validateOpenRouterRouting(value any, path string, errs *[]schemaError) {
	routing, ok := value.(map[string]any)
	if !ok {
		*errs = append(*errs, schemaError{path: path, message: "expected object"})
		return
	}
	if entry, present := routing["allow_fallbacks"]; present {
		expectBool(entry, path+".allow_fallbacks", errs)
	}
	if entry, present := routing["require_parameters"]; present {
		expectBool(entry, path+".require_parameters", errs)
	}
	if entry, present := routing["data_collection"]; present {
		if s, ok := entry.(string); !ok || (s != "deny" && s != "allow") {
			*errs = append(*errs, schemaError{path: path + ".data_collection", message: `expected "deny" or "allow"`})
		}
	}
	if entry, present := routing["zdr"]; present {
		expectBool(entry, path+".zdr", errs)
	}
	if entry, present := routing["enforce_distillable_text"]; present {
		expectBool(entry, path+".enforce_distillable_text", errs)
	}
	for _, key := range []string{"order", "only", "ignore", "quantizations"} {
		if entry, present := routing[key]; present {
			expectStringArray(entry, path+"."+key, errs)
		}
	}
	if entry, present := routing["sort"]; present {
		if sortMap, ok := entry.(map[string]any); ok {
			if by, present := sortMap["by"]; present {
				expectString(by, path+".sort.by", errs)
			}
			if partition, present := sortMap["partition"]; present && partition != nil {
				expectString(partition, path+".sort.partition", errs)
			}
		} else if _, ok := entry.(string); !ok {
			*errs = append(*errs, schemaError{path: path + ".sort", message: "expected string or object"})
		}
	}
	if entry, present := routing["max_price"]; present {
		prices, ok := entry.(map[string]any)
		if !ok {
			*errs = append(*errs, schemaError{path: path + ".max_price", message: "expected object"})
		} else {
			for _, key := range []string{"prompt", "completion", "image", "audio", "request"} {
				if price, present := prices[key]; present {
					if _, ok := price.(float64); !ok {
						if _, ok := price.(string); !ok {
							*errs = append(*errs, schemaError{path: path + ".max_price." + key, message: "expected number or string"})
						}
					}
				}
			}
		}
	}
	for _, key := range []string{"preferred_min_throughput", "preferred_max_latency"} {
		if entry, present := routing[key]; present {
			if _, ok := entry.(float64); !ok {
				if !expectPercentileCutoffs(entry, path+"."+key, errs) {
					*errs = append(*errs, schemaError{path: path + "." + key, message: "expected number or percentile cutoffs"})
				}
			}
		}
	}
}

func validateVercelGatewayRouting(value any, path string, errs *[]schemaError) {
	routing, ok := value.(map[string]any)
	if !ok {
		*errs = append(*errs, schemaError{path: path, message: "expected object"})
		return
	}
	for _, key := range []string{"only", "order"} {
		if entry, present := routing[key]; present {
			expectStringArray(entry, path+"."+key, errs)
		}
	}
}

func validateOpenAICompletionsCompatFields(value map[string]any, path string, errs *[]schemaError) {
	boolKeys := []string{
		"supportsStore", "supportsDeveloperRole", "supportsReasoningEffort", "supportsUsageInStreaming",
		"supportsFinishReason", "requiresToolResultName", "requiresAssistantAfterToolResult",
		"requiresThinkingAsText", "requiresReasoningContentOnAssistantMessages",
		"supportsOpenAIGrammarTools", "supportsStrictMode", "sendSessionAffinityHeaders",
		"supportsLongCacheRetention",
	}
	for _, key := range boolKeys {
		if entry, present := value[key]; present {
			expectBool(entry, path+"."+key, errs)
		}
	}
	// pi (v0.85.1): vllmPriority — top-level priority request field.
	if entry, present := value["vllmPriority"]; present {
		expectNumber(entry, path+".vllmPriority", errs)
	}
	if entry, present := value["maxTokensField"]; present {
		if s, ok := entry.(string); !ok || (s != "max_completion_tokens" && s != "max_tokens") {
			*errs = append(*errs, schemaError{path: path + ".maxTokensField", message: `expected "max_completion_tokens" or "max_tokens"`})
		}
	}
	if entry, present := value["thinkingFormat"]; present {
		if s, ok := entry.(string); !ok || !modelsJsonThinkingFormatValues[s] {
			*errs = append(*errs, schemaError{path: path + ".thinkingFormat", message: "expected a thinking format literal"})
		}
	}
	if entry, present := value["chatTemplateKwargs"]; present {
		validateChatTemplateKwargs(entry, path+".chatTemplateKwargs", errs)
	}
	if entry, present := value["chatTemplateArgs"]; present {
		validateChatTemplateKwargs(entry, path+".chatTemplateArgs", errs)
	}
	if entry, present := value["cacheControlFormat"]; present {
		if s, ok := entry.(string); !ok || s != "anthropic" {
			*errs = append(*errs, schemaError{path: path + ".cacheControlFormat", message: `expected "anthropic"`})
		}
	}
	if entry, present := value["openRouterRouting"]; present {
		validateOpenRouterRouting(entry, path+".openRouterRouting", errs)
	}
	if entry, present := value["vercelGatewayRouting"]; present {
		validateVercelGatewayRouting(entry, path+".vercelGatewayRouting", errs)
	}
	if _, present := value["deferredToolsMode"]; present {
		// pi v0.86.0 removed deferredToolsMode from the models.json schema
		// (the kimi deferred-tools mode is gone with mid-conversation system
		// messages); the key is no longer recognized.
		*errs = append(*errs, schemaError{path: path + ".deferredToolsMode", message: "unexpected property"})
	}
	if entry, present := value["sessionAffinityFormat"]; present {
		if s, ok := entry.(string); !ok || (s != "openai" && s != "openai-nosession" && s != "openrouter") {
			*errs = append(*errs, schemaError{path: path + ".sessionAffinityFormat", message: "expected a session affinity format literal"})
		}
	}
}

func validateOpenAIResponsesCompatFields(value map[string]any, path string, errs *[]schemaError) bool {
	allowed := map[string]bool{
		"supportsDeveloperRole": true, "sessionAffinityFormat": true, "supportsLongCacheRetention": true,
		"supportsStrictMode": true, "supportsOpenAIGrammarTools": true,
		"supportsMaxOutputTokens": true, "supportsMidConvoEffort": true,
	}
	matches := true
	for key := range value {
		if !allowed[key] {
			matches = false
			break
		}
	}
	if !matches {
		return false
	}
	for _, key := range []string{"supportsDeveloperRole", "supportsLongCacheRetention", "supportsStrictMode", "supportsOpenAIGrammarTools", "supportsMaxOutputTokens", "supportsMidConvoEffort"} {
		if entry, present := value[key]; present {
			expectBool(entry, path+"."+key, errs)
		}
	}
	if entry, present := value["sessionAffinityFormat"]; present {
		if s, ok := entry.(string); !ok || (s != "openai" && s != "openai-nosession" && s != "openrouter") {
			*errs = append(*errs, schemaError{path: path + ".sessionAffinityFormat", message: "expected a session affinity format literal"})
		}
	}
	return true
}

func validateAnthropicMessagesCompatFields(value map[string]any, path string, errs *[]schemaError) bool {
	allowed := map[string]bool{
		"supportsEagerToolInputStreaming": true, "supportsLongCacheRetention": true,
		"sendSessionAffinityHeaders": true, "supportsCacheControlOnTools": true,
		"supportsTemperature": true, "forceAdaptiveThinking": true, "allowEmptySignature": true,
		"supportsStrictTools": true, "supportsMidConvoEffort": true,
		"allowedFallbackModels": true,
	}
	matches := true
	for key := range value {
		if !allowed[key] {
			matches = false
			break
		}
	}
	if !matches {
		return false
	}
	for key := range allowed {
		if entry, present := value[key]; present && key != "allowedFallbackModels" {
			expectBool(entry, path+"."+key, errs)
		}
	}
	if entry, present := value["allowedFallbackModels"]; present {
		validateAllowedFallbackModels(entry, path+".allowedFallbackModels", errs)
	}
	return true
}

// validateAllowedFallbackModels checks the anthropic fallback list (pi:
// allowedFallbackModels — maxItems 3, each { provider, model, cost }).
func validateAllowedFallbackModels(value any, path string, errs *[]schemaError) {
	items, ok := value.([]any)
	if !ok {
		*errs = append(*errs, schemaError{path: path, message: "expected array"})
		return
	}
	if len(items) > 3 {
		*errs = append(*errs, schemaError{path: path, message: "expected array to have no more than 3 elements"})
	}
	for i, item := range items {
		itemPath := fmt.Sprintf("%s.%d", path, i)
		entry, ok := item.(map[string]any)
		if !ok {
			*errs = append(*errs, schemaError{path: itemPath, message: "expected object"})
			continue
		}
		for _, key := range []string{"provider", "model"} {
			if v, present := entry[key]; present {
				expectString(v, itemPath+"."+key, errs)
			} else {
				*errs = append(*errs, schemaError{path: itemPath + "." + key, message: "missing required property"})
			}
		}
		if v, present := entry["cost"]; present {
			validateCostRequired(v, itemPath+".cost", errs)
		} else {
			*errs = append(*errs, schemaError{path: itemPath + ".cost", message: "missing required property"})
		}
	}
}

// validateProviderCompat checks the compat union: the value must satisfy one
// of the three provider compat schemas (pi: ProviderCompatSchema union).
// Validation errors are only collected for the alternatives that fail on
// their own field types; unknown-key rejection distinguishes the responses /
// anthropic alternatives (declared approximation of TypeBox's union error
// reporting).
func validateProviderCompat(value any, path string, errs *[]schemaError) {
	compat, ok := value.(map[string]any)
	if !ok {
		*errs = append(*errs, schemaError{path: path, message: "expected object"})
		return
	}
	openaiErrs := []schemaError{}
	validateOpenAICompletionsCompatFields(compat, path, &openaiErrs)
	if len(openaiErrs) == 0 {
		return
	}
	responsesErrs := []schemaError{}
	if validateOpenAIResponsesCompatFields(compat, path, &responsesErrs) && len(responsesErrs) == 0 {
		return
	}
	anthropicErrs := []schemaError{}
	if validateAnthropicMessagesCompatFields(compat, path, &anthropicErrs) && len(anthropicErrs) == 0 {
		return
	}
	*errs = append(*errs, openaiErrs...)
}

func validateThinkingLevelMap(value any, path string, errs *[]schemaError) {
	levelMap, ok := value.(map[string]any)
	if !ok {
		*errs = append(*errs, schemaError{path: path, message: "expected object"})
		return
	}
	for _, key := range []string{"off", "minimal", "low", "medium", "high", "xhigh", "max"} {
		if entry, present := levelMap[key]; present && entry != nil {
			if _, ok := entry.(string); !ok {
				*errs = append(*errs, schemaError{path: path + "." + key, message: "expected string or null"})
			}
		}
	}
}

func validateCostRequired(value any, path string, errs *[]schemaError) {
	cost, ok := value.(map[string]any)
	if !ok {
		*errs = append(*errs, schemaError{path: path, message: "expected object"})
		return
	}
	for _, key := range []string{"input", "output", "cacheRead", "cacheWrite"} {
		entry, present := cost[key]
		if !present {
			*errs = append(*errs, schemaError{path: path + "." + key, message: "missing required property"})
			continue
		}
		expectNumber(entry, path+"."+key, errs)
	}
	validateCostTiers(cost["tiers"], path, errs)
}

func validateCostTiers(value any, path string, errs *[]schemaError) {
	if value == nil {
		return
	}
	tiers, ok := value.([]any)
	if !ok {
		*errs = append(*errs, schemaError{path: path + ".tiers", message: "expected array"})
		return
	}
	for i, tier := range tiers {
		tierPath := fmt.Sprintf("%s.tiers.%d", path, i)
		tierMap, ok := tier.(map[string]any)
		if !ok {
			*errs = append(*errs, schemaError{path: tierPath, message: "expected object"})
			continue
		}
		entry, present := tierMap["inputTokensAbove"]
		if !present {
			*errs = append(*errs, schemaError{path: tierPath + ".inputTokensAbove", message: "missing required property"})
		} else {
			expectNumber(entry, tierPath+".inputTokensAbove", errs)
		}
		for _, key := range []string{"input", "output", "cacheRead", "cacheWrite"} {
			if entry, present := tierMap[key]; present {
				expectNumber(entry, tierPath+"."+key, errs)
			}
		}
	}
}

// validatePromptCache checks the prompt cache lifetime object (pi:
// ModelPromptCacheSchema; exclusiveMinimum 0 for each tier).
// validateInputLimits validates the models.json inputLimits object (pi:
// ModelInputLimitsSchema, v0.87.0 #9631).
func validateInputLimits(value any, path string, errs *[]schemaError) {
	limits, ok := value.(map[string]any)
	if !ok {
		*errs = append(*errs, schemaError{path: path, message: "expected object"})
		return
	}
	if entry, present := limits["maxRequestBytes"]; present {
		expectInteger(entry, path+".maxRequestBytes", 1, 0, errs)
	}
	if entry, present := limits["images"]; present {
		images, ok := entry.(map[string]any)
		if !ok {
			*errs = append(*errs, schemaError{path: path + ".images", message: "expected object"})
			return
		}
		if item, present := images["maxPerMessage"]; present {
			expectInteger(item, path+".images.maxPerMessage", 1, 0, errs)
		}
		if item, present := images["maxPerRequest"]; present {
			expectInteger(item, path+".images.maxPerRequest", 1, 0, errs)
		}
		if item, present := images["resize"]; present {
			resize, ok := item.(map[string]any)
			if !ok {
				*errs = append(*errs, schemaError{path: path + ".images.resize", message: "expected object"})
				return
			}
			if field, present := resize["maxWidth"]; present {
				expectInteger(field, path+".images.resize.maxWidth", 1, 0, errs)
			}
			if field, present := resize["maxHeight"]; present {
				expectInteger(field, path+".images.resize.maxHeight", 1, 0, errs)
			}
			if field, present := resize["maxBytes"]; present {
				expectInteger(field, path+".images.resize.maxBytes", 1, 0, errs)
			}
			if field, present := resize["jpegQuality"]; present {
				expectInteger(field, path+".images.resize.jpegQuality", 1, 100, errs)
			}
		}
	}
}

func validatePromptCache(value any, path string, errs *[]schemaError) {
	promptCache, ok := value.(map[string]any)
	if !ok {
		*errs = append(*errs, schemaError{path: path, message: "expected object"})
		return
	}
	for _, key := range []string{"short", "long"} {
		if entry, present := promptCache[key]; present {
			number, isNumber := entry.(float64)
			if !isNumber || number <= 0 {
				*errs = append(*errs, schemaError{path: path + "." + key, message: "expected number greater than 0"})
			}
		}
	}
}

func validateCostOptional(value any, path string, errs *[]schemaError) {
	cost, ok := value.(map[string]any)
	if !ok {
		*errs = append(*errs, schemaError{path: path, message: "expected object"})
		return
	}
	for _, key := range []string{"input", "output", "cacheRead", "cacheWrite"} {
		if entry, present := cost[key]; present {
			expectNumber(entry, path+"."+key, errs)
		}
	}
	validateCostTiers(cost["tiers"], path, errs)
}

func validateModelDefinition(value any, path string, errs *[]schemaError) {
	model, ok := value.(map[string]any)
	if !ok {
		*errs = append(*errs, schemaError{path: path, message: "expected object"})
		return
	}
	id, present := model["id"]
	if !present {
		*errs = append(*errs, schemaError{path: path + ".id", message: "missing required property"})
	} else {
		expectString(id, path+".id", errs)
	}
	for _, key := range []string{"name", "api", "baseUrl"} {
		if entry, present := model[key]; present {
			expectString(entry, path+"."+key, errs)
		}
	}
	if entry, present := model["reasoning"]; present {
		expectBool(entry, path+".reasoning", errs)
	}
	if entry, present := model["thinkingLevelMap"]; present {
		validateThinkingLevelMap(entry, path+".thinkingLevelMap", errs)
	}
	if entry, present := model["input"]; present {
		expectInputArray(entry, path+".input", errs)
	}
	if entry, present := model["inputLimits"]; present {
		validateInputLimits(entry, path+".inputLimits", errs)
	}
	if entry, present := model["cost"]; present {
		validateCostRequired(entry, path+".cost", errs)
	}
	if entry, present := model["promptCache"]; present {
		validatePromptCache(entry, path+".promptCache", errs)
	}
	for _, key := range []string{"contextWindow", "maxTokens"} {
		if entry, present := model[key]; present {
			expectNumber(entry, path+"."+key, errs)
		}
	}
	if entry, present := model["headers"]; present {
		expectHeaders(entry, path+".headers", errs)
	}
	if entry, present := model["compat"]; present {
		validateProviderCompat(entry, path+".compat", errs)
	}
}

func validateModelOverride(value any, path string, errs *[]schemaError) {
	override, ok := value.(map[string]any)
	if !ok {
		*errs = append(*errs, schemaError{path: path, message: "expected object"})
		return
	}
	if entry, present := override["name"]; present {
		expectString(entry, path+".name", errs)
	}
	if entry, present := override["reasoning"]; present {
		expectBool(entry, path+".reasoning", errs)
	}
	if entry, present := override["thinkingLevelMap"]; present {
		validateThinkingLevelMap(entry, path+".thinkingLevelMap", errs)
	}
	if entry, present := override["input"]; present {
		expectInputArray(entry, path+".input", errs)
	}
	if entry, present := override["inputLimits"]; present {
		validateInputLimits(entry, path+".inputLimits", errs)
	}
	if entry, present := override["cost"]; present {
		validateCostOptional(entry, path+".cost", errs)
	}
	if entry, present := override["promptCache"]; present {
		validatePromptCache(entry, path+".promptCache", errs)
	}
	for _, key := range []string{"contextWindow", "maxTokens"} {
		if entry, present := override[key]; present {
			expectNumber(entry, path+"."+key, errs)
		}
	}
	if entry, present := override["headers"]; present {
		expectHeaders(entry, path+".headers", errs)
	}
	if entry, present := override["compat"]; present {
		validateProviderCompat(entry, path+".compat", errs)
	}
}

func validateProviderConfig(value any, path string, errs *[]schemaError) {
	provider, ok := value.(map[string]any)
	if !ok {
		*errs = append(*errs, schemaError{path: path, message: "expected object"})
		return
	}
	for _, key := range []string{"name", "baseUrl", "apiKey", "api"} {
		if entry, present := provider[key]; present {
			expectString(entry, path+"."+key, errs)
		}
	}
	if entry, present := provider["oauth"]; present {
		if s, ok := entry.(string); !ok || s != "radius" {
			*errs = append(*errs, schemaError{path: path + ".oauth", message: `expected "radius"`})
		}
	}
	if entry, present := provider["headers"]; present {
		expectHeaders(entry, path+".headers", errs)
	}
	if entry, present := provider["compat"]; present {
		validateProviderCompat(entry, path+".compat", errs)
	}
	if entry, present := provider["authHeader"]; present {
		expectBool(entry, path+".authHeader", errs)
	}
	if entry, present := provider["models"]; present {
		models, ok := entry.([]any)
		if !ok {
			*errs = append(*errs, schemaError{path: path + ".models", message: "expected array"})
		} else {
			for i, model := range models {
				validateModelDefinition(model, fmt.Sprintf("%s.models.%d", path, i), errs)
			}
		}
	}
	if entry, present := provider["modelOverrides"]; present {
		overrides, ok := entry.(map[string]any)
		if !ok {
			*errs = append(*errs, schemaError{path: path + ".modelOverrides", message: "expected object"})
		} else {
			for key, override := range overrides {
				validateModelOverride(override, path+".modelOverrides."+key, errs)
			}
		}
	}
}

// validateModelsConfigValue checks the decoded models.json against the
// schema (pi: ModelsConfigSchema / validateModelsConfig.Check).
func validateModelsConfigValue(value any) []schemaError {
	var errs []schemaError
	if !expectObject(value, "root", &errs) {
		return errs
	}
	root := value.(map[string]any)
	providers, present := root["providers"]
	if !present {
		*(&errs) = append(errs, schemaError{path: "providers", message: "missing required property"})
		return errs
	}
	providerMap, ok := providers.(map[string]any)
	if !ok {
		errs = append(errs, schemaError{path: "providers", message: "expected object"})
		return errs
	}
	for providerID, provider := range providerMap {
		validateProviderConfig(provider, "providers."+providerID, &errs)
	}
	return errs
}

// ModelConfig is one immutable load of models.json (pi: ModelConfig).
type ModelConfig struct {
	providers map[string]*ModelsJsonProvider
	keys      []string // JSON.parse insertion order
	err       string
}

func emptyModelConfig() *ModelConfig {
	return &ModelConfig{providers: map[string]*ModelsJsonProvider{}}
}

// LoadModelConfig loads models.json; an empty path yields an empty config
// (pi: ModelConfig.load(undefined)).
func LoadModelConfig(modelsJSONPath string) *ModelConfig {
	if modelsJSONPath == "" {
		return emptyModelConfig()
	}
	path := utils.NormalizePath(modelsJSONPath, nil)
	content, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return emptyModelConfig()
		}
		config := emptyModelConfig()
		config.err = fmt.Sprintf("Failed to load models.json: %s\n\nFile: %s", err.Error(), path)
		return config
	}

	parsed, err := parseJSONWithComments(content)
	if err != nil {
		config := emptyModelConfig()
		config.err = fmt.Sprintf("Failed to parse models.json: %s\n\nFile: %s", err.Error(), path)
		return config
	}

	if schemaErrs := validateModelsConfigValue(parsed); len(schemaErrs) > 0 {
		lines := make([]string, 0, len(schemaErrs))
		for _, schemaErr := range schemaErrs {
			lines = append(lines, fmt.Sprintf("  - %s: %s", schemaErr.path, schemaErr.message))
		}
		config := emptyModelConfig()
		config.err = fmt.Sprintf("Invalid models.json schema:\n%s\n\nFile: %s", strings.Join(lines, "\n"), path)
		return config
	}

	config := emptyModelConfig()
	root := parsed.(map[string]any)
	for providerID, rawProvider := range root["providers"].(map[string]any) {
		// encoding/json sorts map keys; pi's Object.entries follows JSON
		// document order. Re-walk the raw JSON for the key order (declared;
		// consumers only observe order through GetProviderIds).
		config.providers[providerID] = decodeModelsJSONProvider(rawProvider)
		config.keys = append(config.keys, providerID)
	}
	return config
}

// parseJSONWithComments decodes JSON after stripping // comments and
// trailing commas (pi: JSON.parse(stripJsonComments(stripBom(content)))).
func parseJSONWithComments(content []byte) (any, error) {
	stripped := utils.StripJsonComments(utils.StripBom(string(content)))
	var parsed any
	if err := json.Unmarshal([]byte(stripped), &parsed); err != nil {
		return nil, err
	}
	return parsed, nil
}

// decodeModelsJSONProvider builds the typed provider entry from raw JSON
// (pi: structuredClone + deepFreeze of the validated value). Errors cannot
// occur here: the value already passed schema validation.
func decodeModelsJSONProvider(raw any) *ModelsJsonProvider {
	encoded, err := json.Marshal(raw)
	if err != nil {
		return &ModelsJsonProvider{}
	}
	provider := &ModelsJsonProvider{}
	if err := json.Unmarshal(encoded, provider); err != nil {
		return &ModelsJsonProvider{}
	}
	return provider
}

// GetProvider returns one provider entry (pi: getProvider).
func (c *ModelConfig) GetProvider(providerID string) *ModelsJsonProvider {
	return c.providers[providerID]
}

// GetProviderIds lists provider ids in document order (pi:
// getProviderIds).
func (c *ModelConfig) GetProviderIds() []string {
	return append([]string(nil), c.keys...)
}

// GetError returns the load/validation error, if any (pi: getError).
func (c *ModelConfig) GetError() string {
	return c.err
}
