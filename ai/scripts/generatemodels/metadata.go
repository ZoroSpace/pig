// pi: generate-models.ts — compat detection (detectOpenAICompletionsCompat),
// thinking-level metadata, Anthropic fallback metadata, and the per-model
// metadata application pipeline. Branch comments mark logic that targets
// providers pig has not ported yet (openai-responses / google / bedrock /
// github-copilot / opencode / qwen-token-plan / xiaomi): kept verbatim where a
// kept provider can still match, and skipped (with the pi line noted) where
// the trigger API/provider is never produced by this generator.
package main

import (
	"regexp"
	"strings"

	"github.com/ZoroSpace/pig/ai"
	"github.com/samber/lo"
)

// ZAI_TOOL_STREAM_UNSUPPORTED_MODELS (pi) — zai models that reject tool_stream.
var zaiToolStreamUnsupportedModels = stringSet("glm-4.5", "glm-4.5-air", "glm-4.5-flash", "glm-4.5v")

// DeepSeek V4 thinking level maps (pi: DEEPSEEK_V4_THINKING_LEVEL_MAP and the
// flash variant).
var deepseekV4ThinkingLevelMap = ai.ThinkingLevelMap{
	ai.ModelThinkingMinimal: nil,
	ai.ModelThinkingLow:     nil,
	ai.ModelThinkingMedium:  nil,
	ai.ModelThinkingHigh:    lo.ToPtr("high"),
	ai.ModelThinkingMax:     lo.ToPtr("max"),
}

// fireworksAdaptiveThinkingFallbackModels are verified against Fireworks
// Messages raw_output on 2026-09-10 (#9323). Fall back to verified support
// when models.dev omits effort metadata; this is not an allowlist. Any
// Fireworks Messages model advertising effort uses adaptive thinking (pi:
// FIREWORKS_ADAPTIVE_THINKING_FALLBACK_MODELS).
var fireworksAdaptiveThinkingFallbackModels = stringSet(
	"accounts/fireworks/models/deepseek-v4-flash-0731",
	"accounts/fireworks/models/deepseek-v4-flash-vision-exp",
	"accounts/fireworks/models/deepseek-v4-pro-0813",
	"accounts/fireworks/models/qwen3p8-max",
	"accounts/fireworks/models/qwen3p8-2p4t-a95b",
)

var deepseekV4FlashThinkingLevelMap = ai.ThinkingLevelMap{
	ai.ModelThinkingMinimal: nil,
	ai.ModelThinkingLow:     lo.ToPtr("low"),
	ai.ModelThinkingMedium:  nil,
	ai.ModelThinkingHigh:    lo.ToPtr("high"),
	ai.ModelThinkingMax:     lo.ToPtr("max"),
}

// KIMI_K3 constants (pi: KIMI_K3_MAX_TOKENS, KIMI_K3_COST,
// KIMI_CODING_IMPLIED_COSTS).
const kimiK3MaxTokens = 131072

var kimiK3Cost = ai.ModelCostRates{Input: 3, Output: 15, CacheRead: 0.3, CacheWrite: 0}

// Kimi Coding is subscription-backed, so models.dev reports zero cost. Use the
// equivalent Moonshot API rates to estimate the value of subscription usage.
var kimiCodingImpliedCosts = map[string]ai.ModelCostRates{
	"k3":                        {Input: 3, Output: 15, CacheRead: 0.3, CacheWrite: 0},
	"kimi-for-coding":           {Input: 0.95, Output: 4, CacheRead: 0.19, CacheWrite: 0},
	"kimi-for-coding-highspeed": {Input: 1.9, Output: 8, CacheRead: 0.38, CacheWrite: 0},
	"kimi-k2-thinking":          {Input: 0.6, Output: 2.5, CacheRead: 0.15, CacheWrite: 0},
}

var openRouterKimiK3ModelIds = stringSet("moonshotai/kimi-k3", "~moonshotai/kimi-latest")

// ANT_LING_RING_THINKING_LEVEL_MAP (pi) — Ring reasons by default; only
// high/xhigh have documented explicit effort controls.
var antLingRingThinkingLevelMap = ai.ThinkingLevelMap{
	ai.ModelThinkingOff:     nil,
	ai.ModelThinkingMinimal: nil,
	ai.ModelThinkingLow:     nil,
	ai.ModelThinkingMedium:  nil,
	ai.ModelThinkingHigh:    lo.ToPtr("high"),
	ai.ModelThinkingXHigh:   lo.ToPtr("xhigh"),
}

// ANTHROPIC_ALLOWED_FALLBACK_MODELS (pi).
var anthropicAllowedFallbackModels = map[string][]string{
	"claude-fable-5": {"claude-opus-4-8", "claude-opus-5"},
	"claude-opus-5":  {"claude-opus-4-8"},
}

// EAGER_TOOL_INPUT_STREAMING_UNSUPPORTED_ANTHROPIC_MODELS (pi) lists
// anthropic-messages models that reject per-tool eager_input_streaming. All
// entries are github-copilot models, so this never matches until that provider
// is ported; kept verbatim for the port.
// verifiedAnthropicMidConvoEffortProviders are the providers whose
// mid-conversation effort transport is verified (pi:
// VERIFIED_ANTHROPIC_MID_CONVO_EFFORT_PROVIDERS).
var verifiedAnthropicMidConvoEffortProviders = stringSet("anthropic", "openrouter")

// midConvoEffortUnsupportedAnthropicModels gates the effort transport on
// models known to reject it (pi: MID_CONVO_EFFORT_UNSUPPORTED_ANTHROPIC_MODELS,
// v0.86.0 — OpenRouter rejects configuration_update system messages on Opus 5
// while accepting them on Fable 5.1).
var midConvoEffortUnsupportedAnthropicModels = stringSet("openrouter:anthropic/claude-opus-5")

// supportsAnthropicMidConvoEffort (pi: supportsAnthropicMidConvoEffort,
// v0.86.0 — narrowed to the models with a verified effort transport; v0.87.1
// b4588f26a extends opus to 5[.-]5).
func supportsAnthropicMidConvoEffort(modelID string) bool {
	id := strings.ToLower(modelID)
	id = regexp.MustCompile(`^~?anthropic/`).ReplaceAllString(id, "")
	return regexp.MustCompile(`^claude-opus-(?:5|5[.-]5)(?:-\d{8})?$`).MatchString(id) ||
		regexp.MustCompile(`^claude-sonnet-5[.-]5(?:-\d{8})?$`).MatchString(id) ||
		regexp.MustCompile(`^claude-(?:fable|mythos)-5(?:[.-]1)(?:-\d{8})?$`).MatchString(id)
}

// supportsAnthropicMidConvoSystemMessages reports whether the exact model id
// accepts system messages mid-conversation (pi:
// supportsAnthropicMidConvoSystemMessages, v0.86.0; v0.87.1 b4588f26a extends
// opus 5 to 5[.-]5).
func supportsAnthropicMidConvoSystemMessages(modelID string) bool {
	return regexp.MustCompile(`^claude-opus-(?:4[.-]8|5(?:[.-]5)?)(?:-\d{8})?$`).MatchString(modelID) ||
		regexp.MustCompile(`^claude-sonnet-5[.-]5(?:-\d{8})?$`).MatchString(modelID) ||
		regexp.MustCompile(`^claude-(?:fable|mythos)-5(?:[.-]1)?(?:-\d{8})?$`).MatchString(modelID)
}

var eagerToolInputStreamingUnsupportedAnthropicModels = stringSet(
	"github-copilot:claude-haiku-4.5",
	"github-copilot:claude-sonnet-4",
	"github-copilot:claude-sonnet-4.5",
)

// OPENAI_COMPLETIONS_DEFAULT_COMPAT (pi) — the baseline every resolved compat
// is diffed against.
var openAICompletionsDefaultCompat = openAICompletionsResolvedCompat{
	supportsStore:                               true,
	supportsDeveloperRole:                       true,
	supportsReasoningEffort:                     true,
	supportsUsageInStreaming:                    true,
	supportsFinishReason:                        true,
	maxTokensField:                              ai.MaxTokensCompletion,
	requiresToolResultName:                      false,
	requiresAssistantAfterToolResult:            false,
	requiresThinkingAsText:                      false,
	requiresReasoningContentOnAssistantMessages: false,
	thinkingFormat:                              ai.ThinkingFormatOpenAI,
	zaiToolStream:                               false,
	supportsStrictMode:                          false,
	supportsOpenAIGrammarTools:                  false,
	sendSessionAffinityHeaders:                  false,
	supportsLongCacheRetention:                  true,
}

// openAICompletionsResolvedCompat is the fully-resolved openai-completions
// compat (pi: OpenAICompletionsResolvedCompat = typeof OPENAI_COMPLETIONS_DEFAULT_COMPAT
// plus optional cacheControlFormat).
type openAICompletionsResolvedCompat struct {
	supportsStore                               bool
	supportsDeveloperRole                       bool
	supportsReasoningEffort                     bool
	supportsUsageInStreaming                    bool
	supportsFinishReason                        bool
	maxTokensField                              ai.MaxTokensField
	requiresToolResultName                      bool
	requiresAssistantAfterToolResult            bool
	requiresThinkingAsText                      bool
	requiresReasoningContentOnAssistantMessages bool
	thinkingFormat                              ai.ThinkingFormat
	chatTemplateKwargs                          map[string]ai.ChatTemplateKwargValue
	chatTemplateArgs                            map[string]ai.ChatTemplateKwargValue
	zaiToolStream                               bool
	supportsStrictMode                          bool
	supportsOpenAIGrammarTools                  bool
	cacheControlFormat                          *ai.CacheControlFormat
	sendSessionAffinityHeaders                  bool
	supportsLongCacheRetention                  bool
}

// openRouterAnthropicID matches pi's /^~?anthropic\// cacheControlFormat probe.
var openRouterAnthropicID = regexp.MustCompile(`^~?anthropic/`)

// detectOpenAICompletionsCompat resolves a model's openai-completions compat
// from provider id and base URL (pi: detectOpenAICompletionsCompat).
func detectOpenAICompletionsCompat(model *ai.Model) openAICompletionsResolvedCompat {
	provider := string(model.Provider)
	baseURL := model.BaseURL
	lowerBaseURL := strings.ToLower(baseURL)

	isZai := provider == "zai" || provider == "zai-coding-cn" ||
		strings.Contains(baseURL, "api.z.ai") || strings.Contains(baseURL, "open.bigmodel.cn")
	isTogether := provider == "together" ||
		strings.Contains(baseURL, "api.together.ai") || strings.Contains(baseURL, "api.together.xyz")
	isMoonshot := provider == "moonshotai" || provider == "moonshotai-cn" || strings.Contains(baseURL, "api.moonshot.")
	isOpenRouter := provider == "openrouter" || strings.Contains(baseURL, "openrouter.ai")
	isCloudflareWorkersAI := provider == "cloudflare-workers-ai" || strings.Contains(baseURL, "api.cloudflare.com")
	isCloudflareAiGateway := provider == "cloudflare-ai-gateway" || strings.Contains(baseURL, "gateway.ai.cloudflare.com")
	isNvidia := provider == "nvidia" || strings.Contains(baseURL, "integrate.api.nvidia.com")
	isCerebras := provider == "cerebras" || strings.Contains(baseURL, "cerebras.ai")
	isAntLing := provider == "ant-ling" || strings.Contains(baseURL, "api.ant-ling.com")
	isTogetherReasoningOnly := isTogether && togetherReasoningOnlyModels[model.ID]
	isDeepSeek := provider == "deepseek" || strings.Contains(lowerBaseURL, "deepseek.com")

	isNonStandard := isNvidia ||
		isCerebras ||
		provider == "xai" || strings.Contains(baseURL, "api.x.ai") ||
		isTogether ||
		strings.Contains(baseURL, "chutes.ai") ||
		isDeepSeek ||
		isZai ||
		isMoonshot ||
		provider == "opencode" || strings.Contains(baseURL, "opencode.ai") ||
		isCloudflareWorkersAI ||
		isCloudflareAiGateway ||
		isAntLing

	useMaxTokens := strings.Contains(baseURL, "chutes.ai") ||
		isDeepSeek || isMoonshot || isCloudflareAiGateway || isTogether || isNvidia || isAntLing || isZai

	isGrok := provider == "xai" || strings.Contains(baseURL, "api.x.ai")
	isOpenRouterDeveloperRoleModel := isOpenRouter &&
		(strings.HasPrefix(model.ID, "anthropic/") || strings.HasPrefix(model.ID, "openai/"))

	compat := openAICompletionsResolvedCompat{
		supportsStore:                               !isNonStandard,
		supportsDeveloperRole:                       isOpenRouterDeveloperRoleModel || (!isNonStandard && !isOpenRouter),
		supportsReasoningEffort:                     !isGrok && !isZai && !isMoonshot && !isTogether && !isCloudflareAiGateway && !isNvidia && !isAntLing,
		supportsUsageInStreaming:                    true,
		supportsFinishReason:                        true,
		maxTokensField:                              ai.MaxTokensCompletion,
		requiresReasoningContentOnAssistantMessages: isDeepSeek,
		thinkingFormat:                              ai.ThinkingFormatOpenAI,
		zaiToolStream:                               false,
		// Preserve built-in behavior as explicit metadata against the
		// conservative runtime default (pi #9816).
		supportsStrictMode:         !isMoonshot && !isTogether && !isCloudflareAiGateway && !isNvidia && !isCerebras,
		supportsOpenAIGrammarTools: false,
		sendSessionAffinityHeaders: isOpenRouter,
		supportsLongCacheRetention: !(isTogether || isCloudflareWorkersAI || isCloudflareAiGateway || isNvidia || isAntLing),
	}
	if useMaxTokens {
		compat.maxTokensField = ai.MaxTokensPlain
	}
	switch {
	case isDeepSeek:
		compat.thinkingFormat = ai.ThinkingFormatDeepSeek
	case isZai:
		compat.thinkingFormat = ai.ThinkingFormatZai
	case isTogether && !isTogetherReasoningOnly:
		compat.thinkingFormat = ai.ThinkingFormatTogether
	case isAntLing:
		compat.thinkingFormat = ai.ThinkingFormatAntLing
	case isOpenRouter:
		compat.thinkingFormat = ai.ThinkingFormatOpenRouter
	}
	if provider == "openrouter" && openRouterAnthropicID.MatchString(model.ID) {
		format := ai.CacheControlAnthropic
		compat.cacheControlFormat = &format
	}
	return compat
}

// openAICompletionsCompatDelta returns the compat fields that differ from the
// default (pi: openAICompletionsCompatDelta).
func openAICompletionsCompatDelta(compat openAICompletionsResolvedCompat) *ai.OpenAICompletionsCompat {
	d := &ai.OpenAICompletionsCompat{}
	def := openAICompletionsDefaultCompat
	if compat.supportsStore != def.supportsStore {
		d.SupportsStore = lo.ToPtr(compat.supportsStore)
	}
	if compat.supportsDeveloperRole != def.supportsDeveloperRole {
		d.SupportsDeveloperRole = lo.ToPtr(compat.supportsDeveloperRole)
	}
	if compat.supportsReasoningEffort != def.supportsReasoningEffort {
		d.SupportsReasoningEffort = lo.ToPtr(compat.supportsReasoningEffort)
	}
	if compat.supportsUsageInStreaming != def.supportsUsageInStreaming {
		d.SupportsUsageInStreaming = lo.ToPtr(compat.supportsUsageInStreaming)
	}
	if compat.supportsFinishReason != def.supportsFinishReason {
		d.SupportsFinishReason = lo.ToPtr(compat.supportsFinishReason)
	}
	if compat.maxTokensField != def.maxTokensField {
		f := compat.maxTokensField
		d.MaxTokensField = &f
	}
	if compat.requiresToolResultName != def.requiresToolResultName {
		d.RequiresToolResultName = lo.ToPtr(compat.requiresToolResultName)
	}
	if compat.requiresAssistantAfterToolResult != def.requiresAssistantAfterToolResult {
		d.RequiresAssistantAfterToolResult = lo.ToPtr(compat.requiresAssistantAfterToolResult)
	}
	if compat.requiresThinkingAsText != def.requiresThinkingAsText {
		d.RequiresThinkingAsText = lo.ToPtr(compat.requiresThinkingAsText)
	}
	if compat.requiresReasoningContentOnAssistantMessages != def.requiresReasoningContentOnAssistantMessages {
		d.RequiresReasoningContentOnAssistantMessages = lo.ToPtr(compat.requiresReasoningContentOnAssistantMessages)
	}
	if compat.thinkingFormat != def.thinkingFormat {
		f := compat.thinkingFormat
		d.ThinkingFormat = &f
	}
	// chatTemplateKwargs/chatTemplateArgs: pi skips the field when both the
	// value and the default are plain empty objects; the default is always
	// empty, so the delta carries a non-empty map.
	if len(compat.chatTemplateKwargs) > 0 {
		d.ChatTemplateKwargs = compat.chatTemplateKwargs
	}
	if len(compat.chatTemplateArgs) > 0 {
		d.ChatTemplateArgs = compat.chatTemplateArgs
	}
	if compat.zaiToolStream != def.zaiToolStream {
		d.ZaiToolStream = lo.ToPtr(compat.zaiToolStream)
	}
	if compat.supportsStrictMode != def.supportsStrictMode {
		d.SupportsStrictMode = lo.ToPtr(compat.supportsStrictMode)
	}
	if compat.supportsOpenAIGrammarTools != def.supportsOpenAIGrammarTools {
		d.SupportsOpenAIGrammarTools = lo.ToPtr(compat.supportsOpenAIGrammarTools)
	}
	if compat.cacheControlFormat != nil {
		d.CacheControlFormat = compat.cacheControlFormat
	}
	if compat.sendSessionAffinityHeaders != def.sendSessionAffinityHeaders {
		d.SendSessionAffinityHeaders = lo.ToPtr(compat.sendSessionAffinityHeaders)
	}
	if compat.supportsLongCacheRetention != def.supportsLongCacheRetention {
		d.SupportsLongCacheRetention = lo.ToPtr(compat.supportsLongCacheRetention)
	}
	return d
}

// mergeThinkingLevelMap is pi's mergeThinkingLevelMap (spread merge).
func mergeThinkingLevelMap(model *ai.Model, m ai.ThinkingLevelMap) {
	if model.ThinkingLevelMap == nil {
		model.ThinkingLevelMap = ai.ThinkingLevelMap{}
	}
	for k, v := range m {
		model.ThinkingLevelMap[k] = v
	}
}

// modelsDevReasoningOptions records models.dev reasoning_options per provider
// model for later effort-map derivation (pi: modelsDevReasoningOptions Map).
var modelsDevReasoningOptions = map[string][]ModelsDevReasoningOption{}

func getModelKey(model *ai.Model) string { return string(model.Provider) + ":" + model.ID }

// recordModelsDevReasoningOptions stores a model's reasoning_options (pi).
func recordModelsDevReasoningOptions(provider, id string, source *ModelsDevModel) {
	if source.ReasoningOptions != nil {
		modelsDevReasoningOptions[provider+":"+id] = source.ReasoningOptions
	}
}

// supportsDirectReasoningEffort reports whether reasoning_effort can be sent
// directly (pi: supportsDirectReasoningEffort).
func supportsDirectReasoningEffort(model *ai.Model) bool {
	if model.API == ai.ApiAnthropicMessages {
		if compat, ok := model.Compat.(*ai.AnthropicMessagesCompat); ok && compat.ForceAdaptiveThinking != nil && *compat.ForceAdaptiveThinking {
			return true
		}
		return false
	}
	if model.API == ai.ApiOpenAIResponses || model.API == ai.ApiAzureOpenAIResponses || model.API == ai.Api("openai-codex-responses") {
		return true
	}
	if model.API != ai.ApiOpenAICompletions {
		return false
	}
	detected := detectOpenAICompletionsCompat(model)
	thinkingFormat := detected.thinkingFormat
	supportsReasoningEffort := detected.supportsReasoningEffort
	if compat, ok := model.Compat.(*ai.OpenAICompletionsCompat); ok {
		if compat.ThinkingFormat != nil {
			thinkingFormat = *compat.ThinkingFormat
		}
		if compat.SupportsReasoningEffort != nil {
			supportsReasoningEffort = *compat.SupportsReasoningEffort
		}
	}
	return thinkingFormat == ai.ThinkingFormatOpenAI && supportsReasoningEffort
}

// applyModelsDevReasoningOptionMetadata derives a thinking level map from the
// recorded reasoning_options when the model takes direct reasoning_effort (pi).
func applyModelsDevReasoningOptionMetadata(model *ai.Model) {
	reasoningOptions := modelsDevReasoningOptions[getModelKey(model)]
	if len(reasoningOptions) == 0 || !supportsDirectReasoningEffort(model) {
		return
	}
	thinkingLevelMap := getEffortThinkingLevelMap(reasoningOptions)
	if thinkingLevelMap != nil {
		mergeThinkingLevelMap(model, *thinkingLevelMap)
	}
}

// mergeOpenAICompletionsCompat merges compat into the model's existing
// openai-completions compat; src wins where set (pi: `{ ...existing,
// ...compat }`). The existing compat is copied first: provider builders share
// const compat objects across models, and pi's spread always allocates.
func mergeOpenAICompletionsCompat(model *ai.Model, compat *ai.OpenAICompletionsCompat) {
	if existing, ok := model.Compat.(*ai.OpenAICompletionsCompat); ok && existing != nil {
		copied := *existing
		mergeOverrideNonNil(&copied, compat)
		model.Compat = &copied
		return
	}
	copied := *compat
	model.Compat = &copied
}

// mergeAnthropicMessagesCompat merges compat into the model's existing
// anthropic-messages compat; src wins where set (pi: mergeAnthropicMessagesCompat
// spread). The existing compat is copied first for the same aliasing reason as
// mergeOpenAICompletionsCompat.
func mergeAnthropicMessagesCompat(model *ai.Model, compat *ai.AnthropicMessagesCompat) {
	if existing, ok := model.Compat.(*ai.AnthropicMessagesCompat); ok && existing != nil {
		copied := *existing
		mergeAnthropicOverrideNonNil(&copied, compat)
		model.Compat = &copied
		return
	}
	copied := *compat
	model.Compat = &copied
}

// mergeOverrideNonNil copies non-nil pointer/slice/map fields of src into dst
// (pi spread `{ ...dst, ...src }`: src wins where set).
func mergeOverrideNonNil(dst, src *ai.OpenAICompletionsCompat) {
	if src.SupportsStore != nil {
		dst.SupportsStore = src.SupportsStore
	}
	if src.SupportsDeveloperRole != nil {
		dst.SupportsDeveloperRole = src.SupportsDeveloperRole
	}
	if src.SupportsReasoningEffort != nil {
		dst.SupportsReasoningEffort = src.SupportsReasoningEffort
	}
	if src.SupportsUsageInStreaming != nil {
		dst.SupportsUsageInStreaming = src.SupportsUsageInStreaming
	}
	if src.SupportsFinishReason != nil {
		dst.SupportsFinishReason = src.SupportsFinishReason
	}
	if src.MaxTokensField != nil {
		dst.MaxTokensField = src.MaxTokensField
	}
	if src.RequiresToolResultName != nil {
		dst.RequiresToolResultName = src.RequiresToolResultName
	}
	if src.RequiresAssistantAfterToolResult != nil {
		dst.RequiresAssistantAfterToolResult = src.RequiresAssistantAfterToolResult
	}
	if src.RequiresThinkingAsText != nil {
		dst.RequiresThinkingAsText = src.RequiresThinkingAsText
	}
	if src.RequiresReasoningContentOnAssistantMessages != nil {
		dst.RequiresReasoningContentOnAssistantMessages = src.RequiresReasoningContentOnAssistantMessages
	}
	if src.ThinkingFormat != nil {
		dst.ThinkingFormat = src.ThinkingFormat
	}
	if src.ChatTemplateKwargs != nil {
		dst.ChatTemplateKwargs = src.ChatTemplateKwargs
	}
	if src.ChatTemplateArgs != nil {
		dst.ChatTemplateArgs = src.ChatTemplateArgs
	}
	if src.OpenRouterRouting != nil {
		dst.OpenRouterRouting = src.OpenRouterRouting
	}
	if src.VercelGatewayRouting != nil {
		dst.VercelGatewayRouting = src.VercelGatewayRouting
	}
	if src.ZaiToolStream != nil {
		dst.ZaiToolStream = src.ZaiToolStream
	}
	if src.ThinkingTokenBudgetField != nil {
		dst.ThinkingTokenBudgetField = src.ThinkingTokenBudgetField
	}
	if src.SupportsThinkingTokenBudget != nil {
		dst.SupportsThinkingTokenBudget = src.SupportsThinkingTokenBudget
	}
	if src.SupportsMidConvoSystemMessages != nil {
		dst.SupportsMidConvoSystemMessages = src.SupportsMidConvoSystemMessages
	}
	if src.SupportsMidConvoToolAdditions != nil {
		dst.SupportsMidConvoToolAdditions = src.SupportsMidConvoToolAdditions
	}
	if src.SupportsOpenAIGrammarTools != nil {
		dst.SupportsOpenAIGrammarTools = src.SupportsOpenAIGrammarTools
	}
	if src.SupportsStrictMode != nil {
		dst.SupportsStrictMode = src.SupportsStrictMode
	}
	if src.CacheControlFormat != nil {
		dst.CacheControlFormat = src.CacheControlFormat
	}
	if src.SendSessionAffinityHeaders != nil {
		dst.SendSessionAffinityHeaders = src.SendSessionAffinityHeaders
	}
	if src.SessionAffinityFormat != nil {
		dst.SessionAffinityFormat = src.SessionAffinityFormat
	}
	if src.SupportsLongCacheRetention != nil {
		dst.SupportsLongCacheRetention = src.SupportsLongCacheRetention
	}
}

// mergeFillMissing copies non-nil src fields into dst only where dst is unset
// (pi `{ ...detected, ...existing }`: existing wins, detected fills gaps).
func mergeFillMissing(dst, src *ai.OpenAICompletionsCompat) {
	if dst.SupportsStore == nil {
		dst.SupportsStore = src.SupportsStore
	}
	if dst.SupportsDeveloperRole == nil {
		dst.SupportsDeveloperRole = src.SupportsDeveloperRole
	}
	if dst.SupportsReasoningEffort == nil {
		dst.SupportsReasoningEffort = src.SupportsReasoningEffort
	}
	if dst.SupportsUsageInStreaming == nil {
		dst.SupportsUsageInStreaming = src.SupportsUsageInStreaming
	}
	if dst.SupportsFinishReason == nil {
		dst.SupportsFinishReason = src.SupportsFinishReason
	}
	if dst.MaxTokensField == nil {
		dst.MaxTokensField = src.MaxTokensField
	}
	if dst.RequiresToolResultName == nil {
		dst.RequiresToolResultName = src.RequiresToolResultName
	}
	if dst.RequiresAssistantAfterToolResult == nil {
		dst.RequiresAssistantAfterToolResult = src.RequiresAssistantAfterToolResult
	}
	if dst.RequiresThinkingAsText == nil {
		dst.RequiresThinkingAsText = src.RequiresThinkingAsText
	}
	if dst.RequiresReasoningContentOnAssistantMessages == nil {
		dst.RequiresReasoningContentOnAssistantMessages = src.RequiresReasoningContentOnAssistantMessages
	}
	if dst.ThinkingFormat == nil {
		dst.ThinkingFormat = src.ThinkingFormat
	}
	if dst.ChatTemplateKwargs == nil {
		dst.ChatTemplateKwargs = src.ChatTemplateKwargs
	}
	if dst.ChatTemplateArgs == nil {
		dst.ChatTemplateArgs = src.ChatTemplateArgs
	}
	if dst.OpenRouterRouting == nil {
		dst.OpenRouterRouting = src.OpenRouterRouting
	}
	if dst.VercelGatewayRouting == nil {
		dst.VercelGatewayRouting = src.VercelGatewayRouting
	}
	if dst.ZaiToolStream == nil {
		dst.ZaiToolStream = src.ZaiToolStream
	}
	if dst.ThinkingTokenBudgetField == nil {
		dst.ThinkingTokenBudgetField = src.ThinkingTokenBudgetField
	}
	if dst.SupportsThinkingTokenBudget == nil {
		dst.SupportsThinkingTokenBudget = src.SupportsThinkingTokenBudget
	}
	if dst.SupportsOpenAIGrammarTools == nil {
		dst.SupportsOpenAIGrammarTools = src.SupportsOpenAIGrammarTools
	}
	if dst.SupportsStrictMode == nil {
		dst.SupportsStrictMode = src.SupportsStrictMode
	}
	if dst.CacheControlFormat == nil {
		dst.CacheControlFormat = src.CacheControlFormat
	}
	if dst.SendSessionAffinityHeaders == nil {
		dst.SendSessionAffinityHeaders = src.SendSessionAffinityHeaders
	}
	if dst.SessionAffinityFormat == nil {
		dst.SessionAffinityFormat = src.SessionAffinityFormat
	}
	if dst.SupportsLongCacheRetention == nil {
		dst.SupportsLongCacheRetention = src.SupportsLongCacheRetention
	}
}

// openAICompatEmpty reports whether no compat field is set (pi: Object.keys
// length === 0).
func openAICompatEmpty(compat *ai.OpenAICompletionsCompat) bool {
	return compat.SupportsStore == nil &&
		compat.SupportsDeveloperRole == nil &&
		compat.SupportsReasoningEffort == nil &&
		compat.SupportsUsageInStreaming == nil &&
		compat.SupportsFinishReason == nil &&
		compat.MaxTokensField == nil &&
		compat.RequiresToolResultName == nil &&
		compat.RequiresAssistantAfterToolResult == nil &&
		compat.RequiresThinkingAsText == nil &&
		compat.RequiresReasoningContentOnAssistantMessages == nil &&
		compat.ThinkingFormat == nil &&
		compat.ChatTemplateKwargs == nil &&
		compat.ChatTemplateArgs == nil &&
		compat.OpenRouterRouting == nil &&
		compat.VercelGatewayRouting == nil &&
		compat.ZaiToolStream == nil &&
		compat.ThinkingTokenBudgetField == nil &&
		compat.SupportsThinkingTokenBudget == nil &&
		compat.SupportsOpenAIGrammarTools == nil &&
		compat.SupportsStrictMode == nil &&
		compat.CacheControlFormat == nil &&
		compat.SendSessionAffinityHeaders == nil &&
		compat.SessionAffinityFormat == nil &&
		compat.SupportsLongCacheRetention == nil
}

// applyOpenAICompletionsCompatMetadata (pi: applyOpenAICompletionsCompatMetadata).
func applyOpenAICompletionsCompatMetadata(model *ai.Model) {
	if model.API != ai.ApiOpenAICompletions {
		return
	}
	detected := openAICompletionsCompatDelta(detectOpenAICompletionsCompat(model))
	if existing, ok := model.Compat.(*ai.OpenAICompletionsCompat); ok && existing != nil {
		copied := *existing
		mergeFillMissing(&copied, detected)
		if openAICompatEmpty(&copied) {
			model.Compat = nil
		} else {
			model.Compat = &copied
		}
		return
	}
	if openAICompatEmpty(detected) {
		return
	}
	model.Compat = detected
}

// mergeAnthropicOverrideNonNil copies non-nil src fields into dst (pi spread
// `{ ...dst, ...src }`: src wins where set).
func mergeAnthropicOverrideNonNil(dst, src *ai.AnthropicMessagesCompat) {
	if src.SupportsEagerToolInputStreaming != nil {
		dst.SupportsEagerToolInputStreaming = src.SupportsEagerToolInputStreaming
	}
	if src.SupportsLongCacheRetention != nil {
		dst.SupportsLongCacheRetention = src.SupportsLongCacheRetention
	}
	if src.SendSessionAffinityHeaders != nil {
		dst.SendSessionAffinityHeaders = src.SendSessionAffinityHeaders
	}
	if src.SupportsCacheControlOnTools != nil {
		dst.SupportsCacheControlOnTools = src.SupportsCacheControlOnTools
	}
	if src.SupportsTemperature != nil {
		dst.SupportsTemperature = src.SupportsTemperature
	}
	if src.ForceAdaptiveThinking != nil {
		dst.ForceAdaptiveThinking = src.ForceAdaptiveThinking
	}
	if src.AllowEmptySignature != nil {
		dst.AllowEmptySignature = src.AllowEmptySignature
	}
	if src.SupportsStrictTools != nil {
		dst.SupportsStrictTools = src.SupportsStrictTools
	}
	if src.SupportsMidConvoEffort != nil {
		dst.SupportsMidConvoEffort = src.SupportsMidConvoEffort
	}
	if src.SupportsMidConvoSystemMessages != nil {
		dst.SupportsMidConvoSystemMessages = src.SupportsMidConvoSystemMessages
	}
	if src.SupportsMidConvoToolChanges != nil {
		dst.SupportsMidConvoToolChanges = src.SupportsMidConvoToolChanges
	}
	if src.AllowedFallbackModels != nil {
		dst.AllowedFallbackModels = src.AllowedFallbackModels
	}
}

// getAnthropicMessagesCompat returns anthropic-messages compat overrides keyed
// by provider/model (pi: getAnthropicMessagesCompat). The eager-streaming set
// only lists github-copilot models and the allowEmptySignature branch only
// matches xiaomi providers.
func getAnthropicMessagesCompat(provider, modelID string) *ai.AnthropicMessagesCompat {
	compat := &ai.AnthropicMessagesCompat{}
	hadField := false
	if verifiedAnthropicMidConvoEffortProviders[provider] &&
		supportsAnthropicMidConvoEffort(modelID) &&
		!midConvoEffortUnsupportedAnthropicModels[provider+":"+modelID] {
		compat.SupportsMidConvoEffort = lo.ToPtr(true)
		hadField = true
	}
	if provider == "anthropic" && supportsAnthropicMidConvoSystemMessages(modelID) {
		compat.SupportsMidConvoSystemMessages = lo.ToPtr(true)
		compat.SupportsMidConvoToolChanges = lo.ToPtr(true)
		hadField = true
	}
	// OpenCode Zen and GitHub Copilot forward mid-conversation system messages
	// but reject tool_addition/tool_removal blocks, so tool changes stay
	// top-level there (pi v0.86.0).
	if (provider == "opencode" || provider == "github-copilot") && supportsAnthropicMidConvoSystemMessages(modelID) {
		compat.SupportsMidConvoSystemMessages = lo.ToPtr(true)
		hadField = true
	}
	if eagerToolInputStreamingUnsupportedAnthropicModels[provider+":"+modelID] {
		compat.SupportsEagerToolInputStreaming = lo.ToPtr(false)
		hadField = true
	}
	if provider == "xiaomi" || strings.HasPrefix(provider, "xiaomi-token-plan-") {
		compat.AllowEmptySignature = lo.ToPtr(true)
		hadField = true
	}
	// OpenCode Qwen 3.8 Flash emits and accepts thinking blocks with empty
	// signatures (pi v0.99.1 c1449660c #10047).
	if (provider == "opencode" || provider == "opencode-go") && modelID == "qwen3.8-flash" {
		compat.AllowEmptySignature = lo.ToPtr(true)
		hadField = true
	}
	if !hadField {
		return nil
	}
	return compat
}

// applyAnthropicMessagesCompatMetadata (pi: applyAnthropicMessagesCompatMetadata).
func applyAnthropicMessagesCompatMetadata(model *ai.Model) {
	if model.API != ai.ApiAnthropicMessages {
		return
	}
	compat := getAnthropicMessagesCompat(string(model.Provider), model.ID)
	if compat != nil {
		mergeAnthropicMessagesCompat(model, compat)
		if compat.SupportsMidConvoEffort != nil {
			// pi: mergeThinkingLevelMap(model, { off: null }) — the effort
			// transport accepts an explicit off level.
			mergeThinkingLevelMap(model, ai.ThinkingLevelMap{ai.ModelThinkingOff: nil})
		}
	}
}

// isAnthropicFallbackMetadataModel (pi) — anthropic models that participate in
// allowed-fallback metadata.
func isAnthropicFallbackMetadataModel(model *ai.Model) bool {
	if model.Provider != ai.ProviderAnthropic || model.API != ai.ApiAnthropicMessages {
		return false
	}
	if _, ok := anthropicAllowedFallbackModels[model.ID]; ok {
		return true
	}
	for _, fallbackIDs := range anthropicAllowedFallbackModels {
		if containsString(fallbackIDs, model.ID) {
			return true
		}
	}
	return false
}

// applyAnthropicAllowedFallbackModelMetadata (pi:
// applyAnthropicAllowedFallbackModelMetadata).
func applyAnthropicAllowedFallbackModelMetadata(models []*ai.Model) {
	modelsByID := map[string]*ai.Model{}
	for _, model := range models {
		modelsByID[model.ID] = model
	}
	for modelID, fallbackModelIds := range anthropicAllowedFallbackModels {
		model := modelsByID[modelID]
		if model == nil {
			continue
		}
		// pi: when the model carries the verified mid-convo effort transport,
		// fallbacks are filtered down to models that have it too.
		filterFallbacks := false
		if compat, ok := model.Compat.(*ai.AnthropicMessagesCompat); ok && compat != nil && compat.SupportsMidConvoEffort != nil && *compat.SupportsMidConvoEffort {
			filterFallbacks = true
		}
		var allowed []ai.AnthropicAllowedFallbackModel
		for _, fallbackModelID := range fallbackModelIds {
			if filterFallbacks && !supportsAnthropicMidConvoEffort(fallbackModelID) {
				continue
			}
			fallbackModel := modelsByID[fallbackModelID]
			if fallbackModel != nil {
				allowed = append(allowed, ai.AnthropicAllowedFallbackModel{
					Provider: fallbackModel.Provider,
					Model:    fallbackModel.ID,
					Cost:     fallbackModel.Cost,
				})
			}
		}
		if len(allowed) > 0 {
			mergeAnthropicMessagesCompat(model, &ai.AnthropicMessagesCompat{AllowedFallbackModels: allowed})
		}
	}
}

// applyStrictToolCompatMetadata (pi: applyStrictToolCompatMetadata).
func applyStrictToolCompatMetadata(model *ai.Model) {
	if (model.Provider == ai.ProviderOpenAI || model.Provider == ai.ProviderCloudflareAIGateway) &&
		model.API == ai.ApiOpenAIResponses {
		mergeOpenAIResponsesCompat(model, &ai.OpenAIResponsesCompat{SupportsStrictMode: lo.ToPtr(true)})
	} else if model.Provider == ai.ProviderAnthropic && model.API == ai.ApiAnthropicMessages {
		mergeAnthropicMessagesCompat(model, &ai.AnthropicMessagesCompat{SupportsStrictTools: lo.ToPtr(true)})
	}
}

// mergeOpenAIResponsesCompat merges the pointer fields of compat into
// model.Compat when model carries an OpenAIResponsesCompat (pi's compat
// spread: {...model.compat, ...overrides}).
func mergeOpenAIResponsesCompat(model *ai.Model, compat *ai.OpenAIResponsesCompat) {
	existing, ok := model.Compat.(*ai.OpenAIResponsesCompat)
	if !ok || existing == nil {
		model.Compat = copyOpenAIResponsesCompat(compat)
		return
	}
	if compat.SupportsDeveloperRole != nil {
		existing.SupportsDeveloperRole = compat.SupportsDeveloperRole
	}
	if compat.SessionAffinityFormat != nil {
		existing.SessionAffinityFormat = compat.SessionAffinityFormat
	}
	if compat.SupportsLongCacheRetention != nil {
		existing.SupportsLongCacheRetention = compat.SupportsLongCacheRetention
	}
	if compat.SupportsStrictMode != nil {
		existing.SupportsStrictMode = compat.SupportsStrictMode
	}
	if compat.SupportsOpenAIGrammarTools != nil {
		existing.SupportsOpenAIGrammarTools = compat.SupportsOpenAIGrammarTools
	}
	if compat.SupportsAdditionalTools != nil {
		existing.SupportsAdditionalTools = compat.SupportsAdditionalTools
	}
	if compat.SupportsToolSearch != nil {
		existing.SupportsToolSearch = compat.SupportsToolSearch
	}
	if compat.SupportsMidConvoSystemMessages != nil {
		existing.SupportsMidConvoSystemMessages = compat.SupportsMidConvoSystemMessages
	}
	if compat.SupportsExplicitPromptCacheMode != nil {
		existing.SupportsExplicitPromptCacheMode = compat.SupportsExplicitPromptCacheMode
	}
	if compat.SupportsMaxOutputTokens != nil {
		existing.SupportsMaxOutputTokens = compat.SupportsMaxOutputTokens
	}
}

// openAIGrammarToolProviders lists the Responses endpoints verified or
// documented to pass OpenAI custom grammar tools through (pi:
// OPENAI_GRAMMAR_TOOL_PROVIDERS). OpenAI rejects `type: "custom"` tools for
// pre-GPT-5 models (gpt-4.x, gpt-4o, o-series).
var openAIGrammarToolProviders = stringSet(
	"openai", "openai-codex", "azure-openai-responses", "github-copilot", "opencode", "cloudflare-ai-gateway",
)

// openAIGrammarToolApis (pi: OPENAI_GRAMMAR_TOOL_APIS).
var openAIGrammarToolApis = stringSet(
	string(ai.ApiOpenAIResponses), string(ai.ApiAzureOpenAIResponses), "openai-codex-responses",
)

// applyOpenAIGrammarToolCompatMetadata (pi:
// applyOpenAIGrammarToolCompatMetadata).
func applyOpenAIGrammarToolCompatMetadata(model *ai.Model) {
	if !openAIGrammarToolApis[string(model.API)] || !openAIGrammarToolProviders[string(model.Provider)] {
		return
	}
	match := openAIGrammarToolGptRe.FindStringSubmatch(model.ID)
	if match == nil || match[1] < "5" {
		return
	}
	mergeOpenAIResponsesCompat(model, &ai.OpenAIResponsesCompat{SupportsOpenAIGrammarTools: lo.ToPtr(true)})
}

// openAIGrammarToolGptRe matches gpt-<N> prefixes (pi:
// /^gpt-(\d+)/.exec). gpt-5.6 is a leading zero concern; the numeric
// comparison uses the matched year-major version digit.
var openAIGrammarToolGptRe = regexp.MustCompile(`^gpt-(\d+)`)

// openAIToolSearchModelIDs lists the models with tool-search verified
// (pi: OPENAI_TOOL_SEARCH_MODEL_IDS).
var openAIToolSearchModelIDs = stringSet(
	"gpt-5.4", "gpt-5.4-mini", "gpt-5.4-pro", "gpt-5.5", "gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna",
	"gpt-6-astra", "gpt-6-sol", "gpt-6-luna", "gpt-6.1-sol",
)

// openAIAdditionalToolsModelIDs is pi's OPENAI_ADDITIONAL_TOOLS_MODEL_IDS
// (same set as the tool-search ids).
var openAIAdditionalToolsModelIDs = openAIToolSearchModelIDs

// openAIMidConvoSystemMessageModelIDs is pi's
// OPENAI_MID_CONVO_SYSTEM_MESSAGE_MODEL_IDS (same set as the tool-search
// ids; v0.86.0 mid-conversation system messages).
var openAIMidConvoSystemMessageModelIDs = openAIToolSearchModelIDs

// openAICodexAdditionalToolsModelIDs is pi's
// OPENAI_CODEX_ADDITIONAL_TOOLS_MODEL_IDS (the codex branch stays inert
// until the codex adapter lands).
var openAICodexAdditionalToolsModelIDs = stringSet(
	"gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna", "gpt-6-astra", "gpt-6-sol", "gpt-6-luna", "gpt-6.1-sol",
)

// applyOpenAIToolSearchMetadata (pi: applyOpenAIToolSearchMetadata).
func applyOpenAIToolSearchMetadata(model *ai.Model) {
	isOpenAIResponses := model.Provider == ai.ProviderOpenAI && model.API == ai.ApiOpenAIResponses
	isOpenAICodex := model.Provider == ai.ProviderID("openai-codex") && model.API == ai.Api("openai-codex-responses")
	if !(isOpenAIResponses || isOpenAICodex) || !openAIToolSearchModelIDs[model.ID] {
		return
	}
	supportsAdditionalTools := (isOpenAIResponses && openAIAdditionalToolsModelIDs[model.ID]) ||
		(isOpenAICodex && openAICodexAdditionalToolsModelIDs[model.ID])
	compat := &ai.OpenAIResponsesCompat{SupportsToolSearch: lo.ToPtr(true)}
	if supportsAdditionalTools {
		compat.SupportsAdditionalTools = lo.ToPtr(true)
	}
	mergeOpenAIResponsesCompat(model, compat)
}

// applyOpenAICompletionsTranscriptMetadata (pi:
// applyOpenAICompletionsTranscriptMetadata, v0.86.0). Moonshot Kimi K2.6/K2.7
// accept system text after the conversation starts but reject tool-bearing
// system messages. Kimi K3 accepts both forms; Fireworks and OpenCode pass
// its tool-bearing form through. GitHub Copilot forwards K3 text but silently
// drops its tool-bearing message. DeepSeek V4 Pro and OpenAI models behind
// OpenRouter also accept plain system text in place.
func applyOpenAICompletionsTranscriptMetadata(model *ai.Model) {
	if model.API != ai.ApiOpenAICompletions {
		return
	}
	isKimiK3 := (strings.HasPrefix(string(model.Provider), "moonshot") && model.ID == "kimi-k3") ||
		(model.Provider == ai.ProviderID("fireworks") && strings.Contains(model.ID, "kimi-k3")) ||
		((model.Provider == ai.ProviderID("opencode") || model.Provider == ai.ProviderID("opencode-go")) && model.ID == "kimi-k3")
	isMoonshotKimiK2 := strings.HasPrefix(string(model.Provider), "moonshot") &&
		(model.ID == "kimi-k2.6" || model.ID == "kimi-k2.7-code" || model.ID == "kimi-k2.7-code-highspeed")
	isTextOnly := isMoonshotKimiK2 ||
		(model.Provider == ai.ProviderID("github-copilot") && model.ID == "kimi-k3") ||
		(model.Provider == ai.ProviderID("deepseek") && model.ID == "deepseek-v4-pro") ||
		(model.Provider == ai.ProviderOpenRouter &&
			strings.HasPrefix(model.ID, "openai/") &&
			openAIMidConvoSystemMessageModelIDs[strings.TrimPrefix(model.ID, "openai/")])
	if !isKimiK3 && !isTextOnly {
		return
	}
	compat := &ai.OpenAICompletionsCompat{SupportsMidConvoSystemMessages: lo.ToPtr(true)}
	if isKimiK3 {
		compat.SupportsMidConvoToolAdditions = lo.ToPtr(true)
	}
	mergeOpenAICompletionsCompat(model, compat)
}

// openAIResponsesProxyProviders are the proxy providers that pass mid-convo
// developer messages through to OpenAI (pi: OPENAI_RESPONSES_PROXY_PROVIDERS).
var openAIResponsesProxyProviders = stringSet("opencode", "opencode-go", "github-copilot")

// applyOpenAIResponsesTranscriptMetadata (pi:
// applyOpenAIResponsesTranscriptMetadata, v0.86.0). Newer OpenAI Responses
// models accept developer messages after the conversation has started.
// OpenCode Zen, OpenCode Go, and GitHub Copilot pass both those messages and
// additional_tools items through to OpenAI unchanged; tool search is not
// verified through those proxies.
func applyOpenAIResponsesTranscriptMetadata(model *ai.Model) {
	isOpenAIResponses := model.Provider == ai.ProviderOpenAI && model.API == ai.ApiOpenAIResponses
	isOpenAICodex := model.Provider == ai.ProviderID("openai-codex") && model.API == ai.Api("openai-codex-responses")
	isProxiedResponses := openAIResponsesProxyProviders[string(model.Provider)] && model.API == ai.ApiOpenAIResponses
	if !(isOpenAIResponses || isOpenAICodex || isProxiedResponses) || !openAIMidConvoSystemMessageModelIDs[model.ID] {
		return
	}
	compat := &ai.OpenAIResponsesCompat{SupportsMidConvoSystemMessages: lo.ToPtr(true)}
	if isProxiedResponses {
		compat.SupportsAdditionalTools = lo.ToPtr(true)
	}
	mergeOpenAIResponsesCompat(model, compat)
}

// anthropicPromptCache is the direct-Anthropic prompt cache lifetime table
// (pi: ANTHROPIC_PROMPT_CACHE). Anthropic ephemeral entries have a hard
// five-minute lifetime; ttl "1h" extends it to one hour. Only direct
// Anthropic is annotated so cache warming does not assume equivalent behavior
// through proxies.
var anthropicPromptCache = ai.ModelPromptCache{
	ai.CacheRetentionShort: 300,
	ai.CacheRetentionLong:  3600,
}

// applyPromptCacheMetadata (pi: applyPromptCacheMetadata, v0.86.0). Do not
// add OpenAI lifetimes yet. Before enabling warming for explicit OpenAI
// caches, re-evaluate it using observed expiry, replay, and billing behavior;
// a documented TTL alone does not establish full cache loss.
func applyPromptCacheMetadata(model *ai.Model) {
	if model.Provider == ai.ProviderAnthropic && model.API == ai.ApiAnthropicMessages {
		model.PromptCache = anthropicPromptCache
	}
}

// defaultImageResize is the fallback resize profile for vision models (pi:
// DEFAULT_IMAGE_RESIZE in generate-models.ts, v0.87.0 #9631). Keep the
// generated default no less restrictive than coding-agent's historical image
// preprocessing: provider limits can narrow this profile, but unknown
// providers retain the cache-safe 2000px / 4.5 MiB behavior.
var defaultImageResize = ai.ModelImageResizeOptions{
	MaxWidth:    2000,
	MaxHeight:   2000,
	MaxBytes:    4.5 * 1024 * 1024,
	JpegQuality: 80,
}

// applyImageInputMetadata (pi: applyImageInputMetadata, v0.87.0 #9631).
// Every vision model gets the default resize profile; known direct providers
// additionally record their request-size and image-count limits.
func applyImageInputMetadata(model *ai.Model) {
	hasImage := false
	for _, input := range model.Input {
		if input == ai.ModelInputImage {
			hasImage = true
			break
		}
	}
	if !hasImage {
		return
	}

	var providerLimits *ai.ModelInputLimits
	switch model.Provider {
	case ai.ProviderAnthropic:
		maxPerRequest := int64(600)
		if model.ContextWindow == 200000 {
			maxPerRequest = 100
		}
		providerLimits = &ai.ModelInputLimits{
			MaxRequestBytes: 32 * 1024 * 1024,
			Images:          &ai.ModelImageInputLimits{MaxPerRequest: maxPerRequest},
		}
	case ai.ProviderAmazonBedrock:
		providerLimits = &ai.ModelInputLimits{
			Images: &ai.ModelImageInputLimits{MaxPerMessage: 20},
		}
	case ai.ProviderOpenAI:
		providerLimits = &ai.ModelInputLimits{
			MaxRequestBytes: 512 * 1024 * 1024,
			Images:          &ai.ModelImageInputLimits{MaxPerRequest: 1500},
		}
	case ai.ProviderGoogle:
		providerLimits = &ai.ModelInputLimits{
			MaxRequestBytes: 20 * 1024 * 1024,
			Images:          &ai.ModelImageInputLimits{MaxPerRequest: 3600},
		}
	}
	configured := model.InputLimits

	// pi: model.inputLimits = { ...providerLimits, ...model.inputLimits,
	// images: { ...providerLimits?.images, ...configuredImages,
	// resize: { ...DEFAULT_IMAGE_RESIZE, ...configuredImages?.resize } } }.
	// Zero int64 fields mean "unset" (schema minimum is 1), so per-field
	// zero checks reproduce the TS spread semantics.
	merged := &ai.ModelInputLimits{}
	if providerLimits != nil {
		merged.MaxRequestBytes = providerLimits.MaxRequestBytes
	}
	if configured != nil && configured.MaxRequestBytes != 0 {
		merged.MaxRequestBytes = configured.MaxRequestBytes
	}
	images := &ai.ModelImageInputLimits{}
	if providerLimits != nil && providerLimits.Images != nil {
		images.MaxPerMessage = providerLimits.Images.MaxPerMessage
		images.MaxPerRequest = providerLimits.Images.MaxPerRequest
	}
	var configuredResize *ai.ModelImageResizeOptions
	if configured != nil && configured.Images != nil {
		if configured.Images.MaxPerMessage != 0 {
			images.MaxPerMessage = configured.Images.MaxPerMessage
		}
		if configured.Images.MaxPerRequest != 0 {
			images.MaxPerRequest = configured.Images.MaxPerRequest
		}
		configuredResize = configured.Images.Resize
	}
	resize := defaultImageResize
	if configuredResize != nil {
		if configuredResize.MaxWidth != 0 {
			resize.MaxWidth = configuredResize.MaxWidth
		}
		if configuredResize.MaxHeight != 0 {
			resize.MaxHeight = configuredResize.MaxHeight
		}
		if configuredResize.MaxBytes != 0 {
			resize.MaxBytes = configuredResize.MaxBytes
		}
		if configuredResize.JpegQuality != 0 {
			resize.JpegQuality = configuredResize.JpegQuality
		}
	}
	images.Resize = &resize
	merged.Images = images
	model.InputLimits = merged
}

// applyOpenAIExplicitPromptCacheMetadata (pi:
// applyOpenAIExplicitPromptCacheMetadata). OpenAI charges prompt-cache
// writes starting with the GPT-5.6 family, and exactly those models accept
// `prompt_cache_options`; older models reject the parameter.
// https://developers.openai.com/api/docs/guides/prompt-caching
func applyOpenAIExplicitPromptCacheMetadata(model *ai.Model) {
	if model.Provider != ai.ProviderOpenAI || model.API != ai.ApiOpenAIResponses {
		return
	}
	if !(model.Cost.CacheWrite > 0) {
		return
	}
	mergeOpenAIResponsesCompat(model, &ai.OpenAIResponsesCompat{SupportsExplicitPromptCacheMode: lo.ToPtr(true)})
}

// supportsOpenAiXhigh (pi).
func supportsOpenAiXhigh(modelID string) bool {
	return strings.Contains(modelID, "gpt-5.2") ||
		strings.Contains(modelID, "gpt-5.3") ||
		strings.Contains(modelID, "gpt-5.4") ||
		strings.Contains(modelID, "gpt-5.5") ||
		strings.Contains(modelID, "gpt-5.6") ||
		strings.Contains(modelID, "gpt-6")
}

// supportsOpenAiMax (pi).
func supportsOpenAiMax(model *ai.Model) bool {
	return (strings.Contains(model.ID, "gpt-5.6") || strings.Contains(model.ID, "gpt-6")) &&
		(model.API == ai.ApiOpenAIResponses ||
			model.API == ai.ApiAzureOpenAIResponses ||
			model.API == ai.Api("openai-codex-responses") ||
			model.API == ai.ApiOpenAICompletions)
}

// openAIResponsesNoneReasoningModels lists the openai models whose off level
// maps to the provider-native "none" effort (pi:
// OPENAI_RESPONSES_NONE_REASONING_MODELS).
var openAIResponsesNoneReasoningModels = stringSet(
	"gpt-5.1", "gpt-5.2", "gpt-5.3-codex", "gpt-5.4", "gpt-5.4-mini", "gpt-5.4-nano",
	"gpt-5.5", "gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna", "gpt-6-sol", "gpt-6-luna",
)

// isAnthropicAdaptiveThinkingModel (pi).
func isAnthropicAdaptiveThinkingModel(modelID string) bool {
	return strings.Contains(modelID, "opus-4-6") ||
		strings.Contains(modelID, "opus-4.6") ||
		strings.Contains(modelID, "opus-4-7") ||
		strings.Contains(modelID, "opus-4.7") ||
		strings.Contains(modelID, "opus-4-8") ||
		strings.Contains(modelID, "opus-4.8") ||
		strings.Contains(modelID, "opus-5") ||
		strings.Contains(modelID, "opus.5") ||
		strings.Contains(modelID, "sonnet-4-6") ||
		strings.Contains(modelID, "sonnet-4.6") ||
		strings.Contains(modelID, "sonnet-5") ||
		strings.Contains(modelID, "sonnet.5") ||
		strings.Contains(modelID, "fable-5")
}

// isAnthropicTemperatureUnsupportedModel (pi) — Claude Opus 4.7+ rejects the
// temperature field.
func isAnthropicTemperatureUnsupportedModel(modelID string) bool {
	id := strings.ToLower(modelID)
	return strings.Contains(id, "opus-4-7") ||
		strings.Contains(id, "opus-4.7") ||
		strings.Contains(id, "opus-4-8") ||
		strings.Contains(id, "opus-4.8") ||
		strings.Contains(id, "opus-5") ||
		strings.Contains(id, "opus.5") ||
		strings.Contains(id, "sonnet-5-5") ||
		strings.Contains(id, "sonnet-5.5")
}

// qwenTokenPlanProviderIds (pi: QWEN_TOKEN_PLAN_PROVIDER_IDS) — the deepseek
// compat loop skips these (unported).
var qwenTokenPlanProviderIds = stringSet("qwen-token-plan", "qwen-token-plan-cn", "qwen-token-plan-individual")

// applyThinkingLevelMetadata (pi: applyThinkingLevelMetadata). Branches for
// unported providers (github-copilot, google/gemini/gemma, openai-codex,
// opencode-go, opencode grok-build) are skipped; the openai-responses /
// azure / xai branches apply since B2.
func applyThinkingLevelMetadata(model *ai.Model) {
	// pi: openai-responses/azure gpt-5* models mark off unsupported (it is
	// re-enabled to "none" for the none-reasoning set below).
	if (model.API == ai.ApiOpenAIResponses || model.API == ai.ApiAzureOpenAIResponses) &&
		strings.HasPrefix(model.ID, "gpt-5") {
		mergeThinkingLevelMap(model, ai.ThinkingLevelMap{ai.ModelThinkingOff: nil})
	}
	if model.ID == "gpt-6-astra" || model.ID == "gpt-6-sol" || model.ID == "gpt-6-luna" || model.ID == "gpt-6.1-sol" {
		if model.API == ai.ApiOpenAIResponses || model.API == ai.ApiAzureOpenAIResponses || model.API == ai.Api("openai-codex-responses") {
			// pi db91e0331: Astra rejects an explicit off effort (null), Sol/Luna
			// accept the provider-native "none". v0.99.1 12c416e1a: GPT-6.1 Sol
			// joins Astra in rejecting effort "none".
			merge := ai.ThinkingLevelMap{
				ai.ModelThinkingMinimal: nil,
				ai.ModelThinkingLow:     lo.ToPtr("low"),
				ai.ModelThinkingMedium:  lo.ToPtr("medium"),
				ai.ModelThinkingHigh:    lo.ToPtr("high"),
				ai.ModelThinkingXHigh:   lo.ToPtr("xhigh"),
				ai.ModelThinkingMax:     lo.ToPtr("max"),
			}
			if model.ID == "gpt-6-astra" || model.ID == "gpt-6.1-sol" {
				merge[ai.ModelThinkingOff] = nil
			} else {
				merge[ai.ModelThinkingOff] = lo.ToPtr("none")
			}
			mergeThinkingLevelMap(model, merge)
		}
	}
	// pi: github-copilot gpt-5* models map minimal to the provider-native
	// "low" effort.
	if model.Provider == ai.ProviderGithubCopilot && strings.HasPrefix(model.ID, "gpt-5") {
		mergeThinkingLevelMap(model, ai.ThinkingLevelMap{ai.ModelThinkingMinimal: lo.ToPtr("low")})
	}
	if model.API == ai.ApiOpenAIResponses && model.Provider == ai.ProviderOpenAI &&
		openAIResponsesNoneReasoningModels[model.ID] {
		mergeThinkingLevelMap(model, ai.ThinkingLevelMap{ai.ModelThinkingOff: lo.ToPtr("none")})
	}
	// xAI models without verified effort options must not send the undocumented
	// "none"/"minimal" efforts.
	if model.Provider == ai.ProviderXAI && model.API == ai.ApiOpenAIResponses && model.ThinkingLevelMap == nil {
		mergeThinkingLevelMap(model, ai.ThinkingLevelMap{ai.ModelThinkingOff: nil, ai.ModelThinkingMinimal: nil})
	}
	if supportsOpenAiXhigh(model.ID) {
		mergeThinkingLevelMap(model, ai.ThinkingLevelMap{ai.ModelThinkingXHigh: lo.ToPtr("xhigh")})
	}
	if supportsOpenAiMax(model) {
		mergeThinkingLevelMap(model, ai.ThinkingLevelMap{ai.ModelThinkingMax: lo.ToPtr("max")})
	}
	// pi: provider "openai" gpt-5.5 -> minimal null.
	if model.Provider == ai.ProviderOpenAI && model.ID == "gpt-5.5" {
		mergeThinkingLevelMap(model, ai.ThinkingLevelMap{ai.ModelThinkingMinimal: nil})
	}
	if strings.HasSuffix(model.ID, "gpt-5.5-pro") {
		mergeThinkingLevelMap(model, ai.ThinkingLevelMap{ai.ModelThinkingOff: nil, ai.ModelThinkingMinimal: nil, ai.ModelThinkingLow: nil})
	}
	if isAnthropicAdaptiveThinkingModel(model.ID) {
		if strings.Contains(model.ID, "opus-4-6") || strings.Contains(model.ID, "opus-4.6") ||
			strings.Contains(model.ID, "sonnet-4-6") || strings.Contains(model.ID, "sonnet-4.6") {
			mergeThinkingLevelMap(model, ai.ThinkingLevelMap{ai.ModelThinkingMax: lo.ToPtr("max")})
		}
		if strings.Contains(model.ID, "opus-4-7") || strings.Contains(model.ID, "opus-4.7") ||
			strings.Contains(model.ID, "opus-4-8") || strings.Contains(model.ID, "opus-4.8") ||
			strings.Contains(model.ID, "opus-5") || strings.Contains(model.ID, "opus.5") ||
			strings.Contains(model.ID, "sonnet-5") || strings.Contains(model.ID, "sonnet.5") {
			mergeThinkingLevelMap(model, ai.ThinkingLevelMap{ai.ModelThinkingXHigh: lo.ToPtr("xhigh"), ai.ModelThinkingMax: lo.ToPtr("max")})
		}
		if strings.Contains(model.ID, "fable-5") {
			mergeThinkingLevelMap(model, ai.ThinkingLevelMap{ai.ModelThinkingOff: nil, ai.ModelThinkingXHigh: lo.ToPtr("xhigh"), ai.ModelThinkingMax: lo.ToPtr("max")})
		}
		if model.API == ai.ApiAnthropicMessages && isAnthropicAdaptiveThinkingModel(model.ID) {
			mergeAnthropicMessagesCompat(model, &ai.AnthropicMessagesCompat{ForceAdaptiveThinking: lo.ToPtr(true)})
		}
		if model.API == ai.ApiAnthropicMessages && isAnthropicTemperatureUnsupportedModel(model.ID) {
			mergeAnthropicMessagesCompat(model, &ai.AnthropicMessagesCompat{SupportsTemperature: lo.ToPtr(false)})
		}
	}
	if model.API == ai.ApiOpenAICompletions && strings.Contains(model.ID, "deepseek-v4") && model.ThinkingLevelMap == nil {
		switch {
		case model.Provider == ai.ProviderOpenRouter:
			mergeThinkingLevelMap(model, ai.ThinkingLevelMap{
				ai.ModelThinkingMinimal: nil,
				ai.ModelThinkingLow:     nil,
				ai.ModelThinkingMedium:  nil,
				ai.ModelThinkingHigh:    lo.ToPtr("high"),
				ai.ModelThinkingXHigh:   lo.ToPtr("xhigh"),
				ai.ModelThinkingMax:     nil,
			})
		case (model.Provider == ai.ProviderDeepSeek || model.Provider == ai.ProviderOpenCode || model.Provider == ai.ProviderOpenCodeGo) &&
			strings.Contains(model.ID, "deepseek-v4-flash"):
			mergeThinkingLevelMap(model, deepseekV4FlashThinkingLevelMap)
		default:
			mergeThinkingLevelMap(model, deepseekV4ThinkingLevelMap)
		}
	}
	if model.Provider == ai.ProviderGroq && model.ID == "qwen/qwen3.6-27b" {
		mergeThinkingLevelMap(model, ai.ThinkingLevelMap{ai.ModelThinkingMinimal: nil, ai.ModelThinkingLow: nil, ai.ModelThinkingMedium: nil, ai.ModelThinkingHigh: lo.ToPtr("default")})
	}
	// pi: provider "openai-codex" maps minimal to the provider-native "low"
	// effort for every xhigh-capable codex model.
	if model.Provider == ai.ProviderOpenAICodex && supportsOpenAiXhigh(model.ID) {
		mergeThinkingLevelMap(model, ai.ThinkingLevelMap{ai.ModelThinkingMinimal: lo.ToPtr("low")})
	}
	if (model.Provider == ai.ProviderMoonshotAI || model.Provider == ai.ProviderMoonshotAICN) &&
		(model.ID == "kimi-k2.7-code" || model.ID == "kimi-k2.7-code-highspeed") {
		// Kimi K2.7 Code is always-thinking. Official docs say
		// `thinking: { type: "disabled" }` is rejected, and callers can omit
		// the thinking parameter to use the enabled default.
		mergeThinkingLevelMap(model, ai.ThinkingLevelMap{ai.ModelThinkingOff: nil})
	}
	if model.Provider == ai.ProviderOpenRouter && strings.HasPrefix(model.ID, "inception/mercury-2") {
		// Mercury 2 in instant mode (reasoning_effort: "none") disables tool
		// calling. Mark "off" unsupported so the openai-completions provider
		// omits the reasoning param instead of defaulting to
		// {reasoning:{effort:"none"}}.
		mergeThinkingLevelMap(model, ai.ThinkingLevelMap{ai.ModelThinkingOff: nil})
	}
	if model.Provider == ai.ProviderOpenRouter && model.ID == "z-ai/glm-5.2" {
		mergeThinkingLevelMap(model, ai.ThinkingLevelMap{ai.ModelThinkingXHigh: lo.ToPtr("xhigh")})
	}
	if model.Provider == ai.ProviderFireworks {
		if model.API == ai.ApiAnthropicMessages && model.Compat != nil {
			if compat, ok := model.Compat.(*ai.AnthropicMessagesCompat); ok && compat != nil &&
				compat.ForceAdaptiveThinking != nil && *compat.ForceAdaptiveThinking {
				// Qwen Max currently advertises only a toggle. Prefer upstream
				// effort metadata once available instead of replacing it with
				// this fallback.
				if model.ID == "accounts/fireworks/models/qwen3p8-max" && model.ThinkingLevelMap == nil {
					mergeThinkingLevelMap(model, *getEffortThinkingLevelMap([]ModelsDevReasoningOption{
						{Type: "effort", Values: []*string{lo.ToPtr("low"), lo.ToPtr("medium"), lo.ToPtr("xhigh")}},
					}))
				}
				reasoningOptions := modelsDevReasoningOptions[getModelKey(model)]
				hasToggle := false
				for _, option := range reasoningOptions {
					if option.Type == "toggle" {
						hasToggle = true
						break
					}
				}
				if hasToggle ||
					// The 2.4T alias omits the verified toggle in models.dev.
					model.ID == "accounts/fireworks/models/qwen3p8-2p4t-a95b" {
					mergeThinkingLevelMap(model, ai.ThinkingLevelMap{ai.ModelThinkingOff: lo.ToPtr("none")})
				}
				if model.ID == "accounts/fireworks/models/deepseek-v4-pro-0813" {
					mergeThinkingLevelMap(model, ai.ThinkingLevelMap{ai.ModelThinkingLow: lo.ToPtr("low")})
				}
			}
		}
		if strings.Contains(model.ID, "glm-5p2") {
			// GLM 5.2 and its fast router support off/high/max. Fireworks maps
			// low and medium to high, so do not expose those aliases as
			// distinct levels.
			mergeThinkingLevelMap(model, ai.ThinkingLevelMap{
				ai.ModelThinkingOff:     lo.ToPtr("none"),
				ai.ModelThinkingMinimal: nil,
				ai.ModelThinkingLow:     nil,
				ai.ModelThinkingMedium:  nil,
				ai.ModelThinkingMax:     lo.ToPtr("max"),
			})
		}
		if strings.Contains(model.ID, "kimi-k3") {
			// Fireworks maps medium to high on both APIs; do not expose it as a
			// distinct level.
			mergeThinkingLevelMap(model, ai.ThinkingLevelMap{ai.ModelThinkingMedium: nil})
		}
	}
	if model.Provider == ai.ProviderOpenCodeGo && model.ID == "glm-5.2" {
		mergeThinkingLevelMap(model, openCodeGoGlm52ThinkingLevelMap)
	}
	if model.Provider == ai.ProviderOpenCodeGo && model.ID == "kimi-k2.6" {
		// OpenCode Go exposes Kimi K2.6 thinking as on/off, not distinct
		// effort tiers.
		mergeThinkingLevelMap(model, ai.ThinkingLevelMap{ai.ModelThinkingMinimal: nil, ai.ModelThinkingLow: nil, ai.ModelThinkingMedium: nil})
	}
	if model.Provider == ai.ProviderOpenCode && model.ID == "grok-build-0.1" {
		// OpenCode Zen Grok Build reasons by default but rejects explicit
		// reasoningEffort.
		mergeThinkingLevelMap(model, ai.ThinkingLevelMap{ai.ModelThinkingOff: nil, ai.ModelThinkingMinimal: nil, ai.ModelThinkingLow: nil, ai.ModelThinkingMedium: nil})
	}
	if model.Provider == ai.ProviderAntLing && model.Reasoning {
		// Ring reasons by default. Only high/xhigh have documented explicit
		// effort controls.
		mergeThinkingLevelMap(model, antLingRingThinkingLevelMap)
	}
	if model.Provider == ai.ProviderGithubCopilot {
		if override, ok := githubCopilotThinkingLevelOverrides[model.ID]; ok {
			mergeThinkingLevelMap(model, override)
		}
	}
}
