// pi: api/openai-completions.ts (compatibility auto-detection + options
// resolution). Same TS source file as openai_completions.go; split for
// readability, package api.
package api

import (
	"strings"

	"github.com/ZoroSpace/pig/ai/types"
)

// resolveThinkingTokenBudgetField selects the top-level budget request field
// (pi: resolveThinkingTokenBudgetField).
func resolveThinkingTokenBudgetField(compat *resolvedOpenAICompletionsCompat) *types.ThinkingTokenBudgetField {
	if compat.thinkingTokenBudgetField != nil {
		return compat.thinkingTokenBudgetField
	}
	if compat.supportsThinkingTokenBudget {
		field := types.ThinkingTokenBudgetFieldVLLM
		return &field
	}
	return nil
}

// resolveClampedThinkingBudget computes the thinking token budget, capped to
// leave room for the answer under the response ceiling (pi:
// resolveClampedThinkingBudget).
func resolveClampedThinkingBudget(model *types.Model, options *OpenAICompletionsOptions, params map[string]any) int64 {
	if options.ReasoningEffort == "" || !model.Reasoning {
		return 0
	}
	var ceiling int64 = model.MaxTokens
	if v, ok := params["max_tokens"]; ok {
		if f, ok := toInt64(v); ok {
			ceiling = f
		}
	} else if v, ok := params["max_completion_tokens"]; ok {
		if f, ok := toInt64(v); ok {
			ceiling = f
		}
	}
	return ClampThinkingBudgetToAnswerRoom(ThinkingBudgetForLevel(types.ThinkingLevel(options.ReasoningEffort), options.ThinkingBudgets), ceiling)
}

func toInt64(v any) (int64, bool) {
	switch n := v.(type) {
	case int64:
		return n, true
	case float64:
		return int64(n), true
	case int:
		return int64(n), true
	}
	return 0, false
}

// buildChatTemplateValues resolves chat-template kwarg values (pi:
// buildChatTemplateValues).
func buildChatTemplateValues(model *types.Model, options *OpenAICompletionsOptions, values map[string]types.ChatTemplateKwargValue, thinkingBudget int64) map[string]any {
	resolvedValues := map[string]any{}
	for key, value := range values {
		resolved, ok := resolveChatTemplateKwargValue(model, options, value, thinkingBudget)
		if ok {
			resolvedValues[key] = resolved
		}
	}
	if len(resolvedValues) == 0 {
		return nil
	}
	return resolvedValues
}

// resolveChatTemplateKwargValue resolves one chat-template kwarg value (pi:
// resolveChatTemplateKwargValue). ok=false drops the kwarg.
func resolveChatTemplateKwargValue(model *types.Model, options *OpenAICompletionsOptions, value types.ChatTemplateKwargValue, thinkingBudget int64) (any, bool) {
	plain, isVar := value.Value.(types.ChatTemplateKwargVar)
	if !isVar {
		return value.Value, true
	}
	reasoningEffort := options.ReasoningEffort
	if reasoningEffort == "" && plain.OmitWhenOff {
		return nil, false
	}
	if plain.Var == types.ChatTemplateVarThinkingEnabled {
		return reasoningEffort != "", true
	}
	if plain.Var == types.ChatTemplateVarThinkingBudget {
		return thinkingBudget, true
	}
	var mappedValue *string
	if reasoningEffort != "" {
		mappedValue = model.ThinkingLevelMap[types.ModelThinkingLevel(reasoningEffort)]
	} else {
		mappedValue = model.ThinkingLevelMap[types.ModelThinkingOff]
	}
	if mappedValue == nil {
		return string(reasoningEffort), true
	}
	return *mappedValue, true
}

// getCompatCacheControl builds the cache_control marker for anthropic-format
// prompt caching (pi: getCompatCacheControl).
func getCompatCacheControl(compat *resolvedOpenAICompletionsCompat, cacheRetention types.CacheRetention) *openAICacheControl {
	if compat.cacheControlFormat == nil || *compat.cacheControlFormat != types.CacheControlAnthropic || cacheRetention == types.CacheRetentionNone {
		return nil
	}
	ttl := ""
	if cacheRetention == types.CacheRetentionLong && compat.supportsLongCacheRetention {
		ttl = "1h"
	}
	return &openAICacheControl{Type: "ephemeral", TTL: ttl}
}

// applyAnthropicCacheControl marks the system prompt, last tool, and last
// conversation message with cache_control (pi: applyAnthropicCacheControl).
func applyAnthropicCacheControl(messages []map[string]any, tools []map[string]any, cacheControl *openAICacheControl) {
	addCacheControlToSystemPrompt(messages, cacheControl)
	addCacheControlToLastTool(tools, cacheControl)
	addCacheControlToLastConversationMessage(messages, cacheControl)
}

func addCacheControlToSystemPrompt(messages []map[string]any, cacheControl *openAICacheControl) {
	for _, message := range messages {
		role, _ := message["role"].(string)
		if role == "system" || role == "developer" {
			addCacheControlToInstructionMessage(message, cacheControl)
			return
		}
	}
}

func addCacheControlToLastTool(tools []map[string]any, cacheControl *openAICacheControl) {
	if len(tools) == 0 {
		return
	}
	lastTool := tools[len(tools)-1]
	lastTool["cache_control"] = cacheControl
}

func addCacheControlToInstructionMessage(message map[string]any, cacheControl *openAICacheControl) {
	addCacheControlToTextContent(message, cacheControl)
}

func addCacheControlToLastConversationMessage(messages []map[string]any, cacheControl *openAICacheControl) {
	for i := len(messages) - 1; i >= 0; i-- {
		message := messages[i]
		role, _ := message["role"].(string)
		if role == "user" || role == "assistant" || role == "tool" {
			if addCacheControlToTextContent(message, cacheControl) {
				return
			}
		}
	}
}

func addCacheControlToTextContent(message map[string]any, cacheControl *openAICacheControl) bool {
	content, present := message["content"]
	if !present {
		return false
	}
	if text, ok := content.(string); ok {
		if len(text) == 0 {
			return false
		}
		message["content"] = []any{
			map[string]any{"type": "text", "text": text, "cache_control": cacheControl},
		}
		return true
	}
	parts, ok := content.([]any)
	if !ok {
		return false
	}
	for i := len(parts) - 1; i >= 0; i-- {
		part, ok := parts[i].(map[string]any)
		if !ok {
			continue
		}
		if part["type"] == "text" {
			part["cache_control"] = cacheControl
			return true
		}
	}
	return false
}

// detectCompat auto-detects compatibility settings from provider name and
// baseUrl (pi: detectCompat).
func detectCompat(model *types.Model) *resolvedOpenAICompletionsCompat {
	provider := string(model.Provider)
	baseURL := model.BaseURL
	baseURLLower := strings.ToLower(baseURL)

	isZai := provider == "zai" || provider == "zai-coding-cn" || strings.Contains(baseURL, "api.z.ai") || strings.Contains(baseURL, "open.bigmodel.cn")
	isTogether := provider == "together" || strings.Contains(baseURL, "api.together.ai") || strings.Contains(baseURL, "api.together.xyz")
	isMoonshot := provider == "moonshotai" || provider == "moonshotai-cn" || strings.Contains(baseURL, "api.moonshot.")
	isOpenRouter := provider == "openrouter" || strings.Contains(baseURL, "openrouter.ai")
	isCloudflareWorkersAI := provider == "cloudflare-workers-ai" || strings.Contains(baseURL, "api.cloudflare.com")
	isCloudflareAIGateway := provider == "cloudflare-ai-gateway" || strings.Contains(baseURL, "gateway.ai.cloudflare.com")
	isNvidia := provider == "nvidia" || strings.Contains(baseURL, "integrate.api.nvidia.com")
	isAntLing := provider == "ant-ling" || strings.Contains(baseURL, "api.ant-ling.com")
	isDeepSeek := provider == "deepseek" || strings.Contains(baseURLLower, "deepseek.com")

	isNonStandard := isNvidia ||
		provider == "cerebras" ||
		strings.Contains(baseURL, "cerebras.ai") ||
		provider == "xai" ||
		strings.Contains(baseURL, "api.x.ai") ||
		isTogether ||
		strings.Contains(baseURL, "chutes.ai") ||
		isDeepSeek ||
		isZai ||
		isMoonshot ||
		provider == "opencode" ||
		strings.Contains(baseURL, "opencode.ai") ||
		isCloudflareWorkersAI ||
		isCloudflareAIGateway ||
		isAntLing

	useMaxTokens := strings.Contains(baseURL, "chutes.ai") ||
		isDeepSeek ||
		isMoonshot ||
		isCloudflareAIGateway ||
		isTogether ||
		isNvidia ||
		isAntLing ||
		isZai

	isGrok := provider == "xai" || strings.Contains(baseURL, "api.x.ai")
	isOpenRouterDeveloperRoleModel := isOpenRouter && (strings.HasPrefix(model.ID, "anthropic/") || strings.HasPrefix(model.ID, "openai/"))
	var cacheControlFormat *types.CacheControlFormat
	if isOpenRouter && strings.HasPrefix(model.ID, "anthropic/") {
		format := types.CacheControlAnthropic
		cacheControlFormat = &format
	}

	maxTokensField := types.MaxTokensCompletion
	if useMaxTokens {
		maxTokensField = types.MaxTokensPlain
	}
	thinkingFormat := types.ThinkingFormatOpenAI
	switch {
	case isDeepSeek:
		thinkingFormat = types.ThinkingFormatDeepSeek
	case isZai:
		thinkingFormat = types.ThinkingFormatZai
	case isTogether:
		thinkingFormat = types.ThinkingFormatTogether
	case isAntLing:
		thinkingFormat = types.ThinkingFormatAntLing
	case isOpenRouter:
		thinkingFormat = types.ThinkingFormatOpenRouter
	}

	sessionAffinityFormat := types.SessionAffinityOpenAI
	if isOpenRouter {
		sessionAffinityFormat = types.SessionAffinityOpenRouter
	}

	supportsLongCacheRetention := !(isTogether || isCloudflareWorkersAI || isCloudflareAIGateway || isNvidia || isAntLing)

	return &resolvedOpenAICompletionsCompat{
		supportsStore:                               !isNonStandard,
		supportsDeveloperRole:                       isOpenRouterDeveloperRoleModel || (!isNonStandard && !isOpenRouter),
		supportsReasoningEffort:                     !isGrok && !isZai && !isMoonshot && !isTogether && !isCloudflareAIGateway && !isNvidia && !isAntLing,
		supportsUsageInStreaming:                    true,
		supportsFinishReason:                        true,
		maxTokensField:                              maxTokensField,
		requiresToolResultName:                      false,
		requiresAssistantAfterToolResult:            false,
		requiresThinkingAsText:                      false,
		requiresReasoningContentOnAssistantMessages: isDeepSeek,
		thinkingFormat:                              thinkingFormat,
		openRouterRouting:                           &types.OpenRouterRouting{},
		vercelGatewayRouting:                        &types.VercelGatewayRouting{},
		chatTemplateKwargs:                          map[string]types.ChatTemplateKwargValue{},
		chatTemplateArgs:                            map[string]types.ChatTemplateKwargValue{},
		zaiToolStream:                               false,
		supportsThinkingTokenBudget:                 false,
		thinkingTokenBudgetField:                    nil,
		supportsStrictMode:                          !isMoonshot && !isTogether && !isCloudflareAIGateway && !isNvidia,
		supportsOpenAIGrammarTools:                  false,
		supportsMidConvoSystemMessages:              false,
		supportsMidConvoToolAdditions:               false,
		cacheControlFormat:                          cacheControlFormat,
		sendSessionAffinityHeaders:                  false,
		sessionAffinityFormat:                       sessionAffinityFormat,
		supportsLongCacheRetention:                  supportsLongCacheRetention,
	}
}

// getCompat returns the resolved compatibility settings for a model:
// auto-detected from provider/URL, overridden by explicit model.compat (pi:
// getCompat).
func getCompat(model *types.Model) *resolvedOpenAICompletionsCompat {
	detected := detectCompat(model)
	compat, ok := model.Compat.(*types.OpenAICompletionsCompat)
	if !ok || compat == nil {
		return detected
	}
	resolved := *detected
	if compat.SupportsStore != nil {
		resolved.supportsStore = *compat.SupportsStore
	}
	if compat.SupportsDeveloperRole != nil {
		resolved.supportsDeveloperRole = *compat.SupportsDeveloperRole
	}
	if compat.SupportsReasoningEffort != nil {
		resolved.supportsReasoningEffort = *compat.SupportsReasoningEffort
	}
	if compat.SupportsUsageInStreaming != nil {
		resolved.supportsUsageInStreaming = *compat.SupportsUsageInStreaming
	}
	if compat.SupportsFinishReason != nil {
		resolved.supportsFinishReason = *compat.SupportsFinishReason
	}
	if compat.MaxTokensField != nil {
		resolved.maxTokensField = *compat.MaxTokensField
	}
	if compat.RequiresToolResultName != nil {
		resolved.requiresToolResultName = *compat.RequiresToolResultName
	}
	if compat.RequiresAssistantAfterToolResult != nil {
		resolved.requiresAssistantAfterToolResult = *compat.RequiresAssistantAfterToolResult
	}
	if compat.RequiresThinkingAsText != nil {
		resolved.requiresThinkingAsText = *compat.RequiresThinkingAsText
	}
	if compat.RequiresReasoningContentOnAssistantMessages != nil {
		resolved.requiresReasoningContentOnAssistantMessages = *compat.RequiresReasoningContentOnAssistantMessages
	}
	if compat.ThinkingFormat != nil {
		resolved.thinkingFormat = *compat.ThinkingFormat
	}
	if compat.OpenRouterRouting != nil {
		resolved.openRouterRouting = compat.OpenRouterRouting
	}
	if compat.VercelGatewayRouting != nil {
		resolved.vercelGatewayRouting = compat.VercelGatewayRouting
	}
	if compat.ChatTemplateKwargs != nil {
		resolved.chatTemplateKwargs = compat.ChatTemplateKwargs
	}
	if compat.ChatTemplateArgs != nil {
		resolved.chatTemplateArgs = compat.ChatTemplateArgs
	}
	if compat.ZaiToolStream != nil {
		resolved.zaiToolStream = *compat.ZaiToolStream
	}
	if compat.SupportsThinkingTokenBudget != nil {
		resolved.supportsThinkingTokenBudget = *compat.SupportsThinkingTokenBudget
	}
	if compat.ThinkingTokenBudgetField != nil {
		resolved.thinkingTokenBudgetField = compat.ThinkingTokenBudgetField
	}
	if compat.SupportsStrictMode != nil {
		resolved.supportsStrictMode = *compat.SupportsStrictMode
	}
	if compat.SupportsOpenAIGrammarTools != nil {
		resolved.supportsOpenAIGrammarTools = *compat.SupportsOpenAIGrammarTools
	}
	if compat.CacheControlFormat != nil {
		resolved.cacheControlFormat = compat.CacheControlFormat
	}
	if compat.SendSessionAffinityHeaders != nil {
		resolved.sendSessionAffinityHeaders = *compat.SendSessionAffinityHeaders
	}
	if compat.SupportsMidConvoSystemMessages != nil {
		resolved.supportsMidConvoSystemMessages = *compat.SupportsMidConvoSystemMessages
	}
	if compat.SupportsMidConvoToolAdditions != nil {
		resolved.supportsMidConvoToolAdditions = *compat.SupportsMidConvoToolAdditions
	}
	if compat.SessionAffinityFormat != nil {
		resolved.sessionAffinityFormat = *compat.SessionAffinityFormat
	}
	if compat.SupportsLongCacheRetention != nil {
		resolved.supportsLongCacheRetention = *compat.SupportsLongCacheRetention
	}
	resolved.vllmPriority = compat.VllmPriority
	return &resolved
}
