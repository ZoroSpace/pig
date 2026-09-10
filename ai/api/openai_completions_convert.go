// pi: api/openai-completions.ts (message/tool conversion + usage parsing).
// Same TS source file as openai_completions.go; split for readability,
// package api.
package api

import (
	"encoding/json"
	"strings"

	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/ai/utils"
)

// ConvertCompletionsMessagesOptions are the convertMessages extras (pi:
// ConvertCompletionsMessagesOptions).
type ConvertCompletionsMessagesOptions struct {
	GrammarToolInputProperties map[string]string
}

// normalizeToolCallID handles pipe-separated IDs from the OpenAI Responses API
// (pi: convertMessages' inner normalizeToolCallId): format {call_id}|{id},
// where {id} can be 400+ chars with special chars (+ / =). The call_id part is
// extracted, sanitized, and truncated to 40 chars (OpenAI limit); multiple
// tool calls sharing a call_id keep item-level uniqueness.
func normalizeToolCallID(id string, model *types.Model) string {
	if strings.Contains(id, "|") {
		separatorIndex := strings.Index(id, "|")
		callID := sanitizeToolIDChars(id[:separatorIndex])
		itemID := sanitizeToolIDChars(id[separatorIndex+1:])
		combinedID := callID
		if len(itemID) > 0 {
			combinedID = callID + "_" + itemID
		}
		if len(combinedID) <= 40 {
			return combinedID
		}
		hash := utils.ShortHash(id)
		if len(hash) > 8 {
			hash = hash[:8]
		}
		prefixLen := 40 - len(hash) - 1
		if prefixLen < 1 {
			prefixLen = 1
		}
		if prefixLen > len(callID) {
			prefixLen = len(callID)
		}
		return callID[:prefixLen] + "_" + hash
	}
	if model.Provider == types.ProviderOpenAI {
		if len(id) > 40 {
			return id[:40]
		}
	}
	return id
}

func sanitizeToolIDChars(s string) string {
	var b strings.Builder
	for _, c := range s {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-' {
			b.WriteRune(c)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}

// convertMessages converts the context into chat completion message params
// (pi: convertMessages). The params are plain JSON maps so optional fields
// serialize on demand.
func convertMessages(model *types.Model, llmContext *types.Context, compat *resolvedOpenAICompletionsCompat, options *ConvertCompletionsMessagesOptions) []map[string]any {
	var params []map[string]any

	normalizedMessages := TransformMessages(llmContext.Messages, model, func(id string, m *types.Model, source *types.AssistantMessage) string {
		return normalizeToolCallID(id, m)
	})

	if llmContext.SystemPrompt != "" {
		useDeveloperRole := model.Reasoning && compat.supportsDeveloperRole
		role := "system"
		if useDeveloperRole {
			role = "developer"
		}
		params = append(params, map[string]any{"role": role, "content": utils.SanitizeSurrogates(llmContext.SystemPrompt)})
	}

	lastRole := ""

	for i := 0; i < len(normalizedMessages); i++ {
		msg := normalizedMessages[i]
		// Some providers don't allow user messages directly after tool results;
		// insert a synthetic assistant message to bridge the gap.
		if compat.requiresAssistantAfterToolResult && lastRole == "toolResult" && msg.MessageRole() == types.RoleUser {
			params = append(params, map[string]any{"role": "assistant", "content": "I have processed the tool results."})
		}

		switch m := msg.(type) {
		case types.UserMessage:
			var content any
			if m.Content.Parts == nil {
				content = utils.SanitizeSurrogates(m.Content.String)
			} else {
				var parts []any
				for _, item := range m.Content.Parts {
					switch block := item.(type) {
					case types.TextContent:
						parts = append(parts, map[string]any{"type": "text", "text": utils.SanitizeSurrogates(block.Text)})
					case types.ImageContent:
						parts = append(parts, map[string]any{
							"type":      "image_url",
							"image_url": map[string]any{"url": "data:" + block.MimeType + ";base64," + block.Data},
						})
					}
				}
				if len(parts) == 0 {
					continue
				}
				content = parts
			}
			params = append(params, map[string]any{"role": "user", "content": content})
			lastRole = "user"

		case types.AssistantMessage:
			assistantMsg := map[string]any{
				"role":    "assistant",
				"content": nil,
			}
			if compat.requiresAssistantAfterToolResult {
				assistantMsg["content"] = ""
			}

			var assistantTextParts []map[string]any
			for _, block := range m.Content {
				if text, ok := block.(types.TextContent); ok && strings.TrimSpace(text.Text) != "" {
					assistantTextParts = append(assistantTextParts, map[string]any{"type": "text", "text": utils.SanitizeSurrogates(text.Text)})
				}
			}
			var assistantText strings.Builder
			for _, part := range assistantTextParts {
				assistantText.WriteString(part["text"].(string))
			}

			var thinkingBlocks []types.ThinkingContent
			var toolCalls []types.ToolCall
			for _, block := range m.Content {
				switch b := block.(type) {
				case types.ThinkingContent:
					thinkingBlocks = append(thinkingBlocks, b)
				case types.ToolCall:
					toolCalls = append(toolCalls, b)
				}
			}

			var preservedReasoningDetails []map[string]any
			for _, block := range thinkingBlocks {
				if details := parseOpenAIReasoningDetails(block.ThinkingSignature); details != nil {
					preservedReasoningDetails = details
					break
				}
			}
			if preservedReasoningDetails == nil {
				for _, toolCall := range toolCalls {
					if detail := parseLegacyEncryptedReasoningDetail(toolCall.ThoughtSignature); detail != nil {
						preservedReasoningDetails = append(preservedReasoningDetails, detail)
					}
				}
			}

			var nonEmptyThinkingBlocks []types.ThinkingContent
			for _, block := range thinkingBlocks {
				if strings.TrimSpace(block.Thinking) != "" {
					nonEmptyThinkingBlocks = append(nonEmptyThinkingBlocks, block)
				}
			}

			if len(nonEmptyThinkingBlocks) > 0 {
				if compat.requiresThinkingAsText {
					var thinkingParts []string
					for _, block := range nonEmptyThinkingBlocks {
						thinkingParts = append(thinkingParts, utils.SanitizeSurrogates(block.Thinking))
					}
					thinkingText := strings.Join(thinkingParts, "\n\n")
					contentParts := []any{map[string]any{"type": "text", "text": thinkingText}}
					for _, part := range assistantTextParts {
						contentParts = append(contentParts, part)
					}
					assistantMsg["content"] = contentParts
				} else {
					// Always send assistant content as a plain string (OpenAI
					// Chat Completions standard format); sending an array of
					// text parts is non-standard and makes some models (e.g.
					// DeepSeek V3.2 via NVIDIA NIM) mirror the block structure.
					if assistantText.Len() > 0 {
						assistantMsg["content"] = assistantText.String()
					}
					if preservedReasoningDetails == nil {
						signature := nonEmptyThinkingBlocks[0].ThinkingSignature
						if model.Provider == "opencode-go" && signature == "reasoning" {
							signature = "reasoning_content"
						}
						if signature != "" && isOpenAICompletionsReasoningField(signature) {
							var texts []string
							for _, block := range nonEmptyThinkingBlocks {
								texts = append(texts, block.Thinking)
							}
							assistantMsg[signature] = strings.Join(texts, "\n")
						}
					}
				}
			} else if assistantText.Len() > 0 {
				assistantMsg["content"] = assistantText.String()
			}

			if len(toolCalls) > 0 {
				var converted []any
				for _, tc := range toolCalls {
					customInputProperty := ""
					if options != nil {
						customInputProperty = options.GrammarToolInputProperties[tc.Name]
					}
					if customInputProperty != "" {
						input, err := GetGrammarToolInput(tc.Name, tc.Arguments, customInputProperty)
						if err != nil {
							input = ""
						}
						converted = append(converted, map[string]any{
							"id":     tc.ID,
							"type":   "custom",
							"custom": map[string]any{"name": tc.Name, "input": utils.SanitizeSurrogates(input)},
						})
					} else {
						argumentsJSON, _ := json.Marshal(tc.Arguments)
						converted = append(converted, map[string]any{
							"id":   tc.ID,
							"type": "function",
							"function": map[string]any{
								"name":      tc.Name,
								"arguments": string(argumentsJSON),
							},
						})
					}
				}
				assistantMsg["tool_calls"] = converted
			}
			if preservedReasoningDetails != nil {
				assistantMsg["reasoning_details"] = preservedReasoningDetails
			}
			if compat.requiresReasoningContentOnAssistantMessages && model.Reasoning {
				if _, present := assistantMsg["reasoning_content"]; !present {
					assistantMsg["reasoning_content"] = ""
				}
			}
			content, _ := assistantMsg["content"]
			hasContent := false
			if content != nil {
				switch c := content.(type) {
				case string:
					hasContent = len(c) > 0
				case []any:
					hasContent = len(c) > 0
				}
			}
			_, hasToolCalls := assistantMsg["tool_calls"]
			if !hasContent && !hasToolCalls {
				continue
			}
			params = append(params, assistantMsg)
			lastRole = "assistant"

		case types.ToolResultMessage:
			var imageBlocks []any
			deferredToolNames := map[string]struct{}{}
			j := i
			for ; j < len(normalizedMessages); j++ {
				toolMsg, ok := normalizedMessages[j].(types.ToolResultMessage)
				if !ok {
					break
				}
				var textParts []string
				hasImages := false
				for _, block := range toolMsg.Content {
					if text, ok := block.(types.TextContent); ok {
						textParts = append(textParts, text.Text)
					}
					if block.ContentKind() == types.KindImage {
						hasImages = true
					}
				}
				textResult := strings.Join(textParts, "\n")
				hasText := len(textResult) > 0
				toolResultText := "(no tool output)"
				if hasText {
					toolResultText = textResult
				} else if hasImages {
					toolResultText = "(see attached image)"
				}
				toolResultMsg := map[string]any{
					"role":         "tool",
					"content":      utils.SanitizeSurrogates(toolResultText),
					"tool_call_id": toolMsg.ToolCallID,
				}
				if compat.requiresToolResultName && toolMsg.ToolName != "" {
					toolResultMsg["name"] = toolMsg.ToolName
				}
				params = append(params, toolResultMsg)

				if compat.deferredToolsMode != nil && *compat.deferredToolsMode == types.DeferredToolsKimi {
					for _, name := range toolMsg.AddedToolNames {
						deferredToolNames[name] = struct{}{}
					}
				}
				if hasImages && modelSupportsImages(model) {
					for _, block := range toolMsg.Content {
						if image, ok := block.(types.ImageContent); ok {
							imageBlocks = append(imageBlocks, map[string]any{
								"type":      "image_url",
								"image_url": map[string]any{"url": "data:" + image.MimeType + ";base64," + image.Data},
							})
						}
					}
				}
			}
			i = j - 1

			if len(imageBlocks) > 0 {
				if compat.requiresAssistantAfterToolResult {
					params = append(params, map[string]any{"role": "assistant", "content": "I have processed the tool results."})
				}
				contentParts := []any{map[string]any{"type": "text", "text": "Attached image(s) from tool result:"}}
				contentParts = append(contentParts, imageBlocks...)
				params = append(params, map[string]any{"role": "user", "content": contentParts})
				lastRole = "user"
			} else {
				lastRole = "toolResult"
			}

			if len(deferredToolNames) > 0 {
				deferredTools := getToolsByName(llmContext.Tools, deferredToolNames)
				if len(deferredTools) > 0 {
					kimiToolMessage := map[string]any{
						"role":  "system",
						"tools": convertTools(deferredTools, compat),
					}
					params = append(params, kimiToolMessage)
				}
			}
		}
	}

	return params
}

// convertTools converts tools to chat completion tool params (pi:
// convertTools).
func convertTools(tools []types.Tool, compat *resolvedOpenAICompletionsCompat) []map[string]any {
	var out []map[string]any
	for _, tool := range tools {
		grammar, err := ResolveGrammarConstrainedSampling(&tool, compat.supportsOpenAIGrammarTools)
		if err == nil && grammar != nil {
			out = append(out, map[string]any{
				"type": "custom",
				"custom": map[string]any{
					"name":        tool.Name,
					"description": tool.Description,
					"format": map[string]any{
						"type": "grammar",
						"grammar": map[string]any{
							"syntax":     grammar.Format,
							"definition": grammar.Definition,
						},
					},
				},
			})
			continue
		}
		strict, err := ResolveJSONSchemaStrictSampling(&tool, compat.supportsStrictMode)
		if err != nil {
			// pi throws; the caller surfaces it as a stream error. Fall back to
			// a plain function tool so the request still leaves.
			strict = nil
		}
		parameters, _ := GetJSONSchemaToolParameters(&tool, strict != nil && *strict)
		function := map[string]any{
			"name":        tool.Name,
			"description": tool.Description,
			"parameters":  json.RawMessage(parameters),
		}
		if compat.supportsStrictMode {
			function["strict"] = strict != nil && *strict
		}
		out = append(out, map[string]any{"type": "function", "function": function})
	}
	return out
}

// parseChunkUsage converts a raw usage chunk into a normalized Usage (pi:
// parseChunkUsage).
func parseChunkUsage(raw *chatCompletionUsage, model *types.Model) types.Usage {
	promptTokens := int64(raw.PromptTokens)
	var cacheReadTokens, cacheWriteTokens int64
	if raw.PromptTokensDetails != nil {
		cacheReadTokens = int64(raw.PromptTokensDetails.CachedTokens)
		cacheWriteTokens = int64(raw.PromptTokensDetails.CacheWriteTokens)
	} else if raw.PromptCacheHitTokens != 0 {
		cacheReadTokens = int64(raw.PromptCacheHitTokens)
	} else if raw.CachedTokens != 0 {
		cacheReadTokens = int64(raw.CachedTokens)
	}
	input := maxInt64(0, promptTokens-cacheReadTokens-cacheWriteTokens)
	outputTokens := int64(raw.CompletionTokens)
	var reasoning int64
	if raw.CompletionTokensDetails != nil {
		reasoning = int64(raw.CompletionTokensDetails.ReasoningTokens)
	}
	usage := types.Usage{
		Input:       input,
		Output:      outputTokens,
		CacheRead:   cacheReadTokens,
		CacheWrite:  cacheWriteTokens,
		Reasoning:   &reasoning,
		TotalTokens: input + outputTokens + cacheReadTokens + cacheWriteTokens,
		Cost:        types.Cost{},
	}
	ai.CalculateCost(model, &usage)
	return usage
}

// mapStopReason maps a provider finish_reason to a normalized stop reason
// (pi: mapStopReason).
func mapStopReason(reason string) (types.StopReason, string) {
	switch reason {
	case "stop", "end":
		return types.StopReasonStop, ""
	case "length":
		return types.StopReasonLength, ""
	case "function_call", "tool_calls":
		return types.StopReasonToolUse, ""
	case "content_filter":
		return types.StopReasonError, "Provider finish_reason: content_filter"
	case "network_error":
		return types.StopReasonError, "Provider finish_reason: network_error"
	default:
		return types.StopReasonError, "Provider finish_reason: " + reason
	}
}
