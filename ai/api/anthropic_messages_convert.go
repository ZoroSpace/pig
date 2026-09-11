// pi: api/anthropic-messages.ts (convertToolResult, convertMessages,
// insertThinkingLevelMessages, convertTools, mapStopReason). Same TS source
// file as anthropic_messages.go; split for readability, package api.
package api

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/ai/utils"
)

// convertAnthropicToolResult converts one tool result message (pi:
// convertToolResult). Tool references from deferred tool loads must not mix
// with ordinary tool-result content, so the ordinary content is returned as
// sibling blocks when references exist.
func convertAnthropicToolResult(msg *types.ToolResultMessage, isOAuthToken bool, deferredToolNames map[string]struct{}, loadedToolNames map[string]struct{}, normalizeToolName func(string) string) (toolResult map[string]any, siblingContent []map[string]any) {
	var references []map[string]any
	for _, name := range msg.AddedToolNames {
		normalizedName := normalizeToolName(name)
		if _, deferred := deferredToolNames[normalizedName]; !deferred {
			continue
		}
		if _, loaded := loadedToolNames[normalizedName]; loaded {
			continue
		}
		loadedToolNames[normalizedName] = struct{}{}
		referenceName := name
		if isOAuthToken {
			referenceName = toClaudeCodeName(name)
		}
		references = append(references, map[string]any{"type": "tool_reference", "tool_name": referenceName})
	}
	convertedContent := convertContentBlocks(msg.Content)
	result := map[string]any{
		"type":        "tool_result",
		"tool_use_id": msg.ToolCallID,
		"content":     convertedContent,
		"is_error":    msg.IsError,
	}
	if len(references) > 0 {
		// Anthropic rejects tool references mixed with ordinary tool-result
		// content.
		result["content"] = references
		if text, ok := convertedContent.(string); ok {
			siblingContent = []map[string]any{{"type": "text", "text": text}}
		} else {
			siblingContent, _ = convertedContent.([]map[string]any)
		}
	}
	return result, siblingContent
}

// convertedAnthropicMessages is convertMessages' result (pi:
// ConvertedAnthropicMessages). assistantLevels maps the params-array index of
// each managed assistant message to its recorded effort.
type convertedAnthropicMessages struct {
	messages        []map[string]any
	assistantLevels map[int]AnthropicEffort
}

// convertAnthropicMessages converts history into Anthropic message params
// (pi: convertMessages).
func convertAnthropicMessages(transformedMessages []types.Message, isOAuthToken bool, cacheControl *anthropicCacheControl, allowEmptySignature bool, deferredToolNames map[string]struct{}, normalizeToolName func(string) string, managedProvider string) (convertedAnthropicMessages, error) {
	var params []map[string]any
	converted := convertedAnthropicMessages{assistantLevels: map[int]AnthropicEffort{}}
	loadedToolNames := map[string]struct{}{}

	for i := 0; i < len(transformedMessages); i++ {
		msg := transformedMessages[i]

		if user, ok := msg.(types.UserMessage); ok {
			if user.Content.Parts == nil {
				if strings.TrimSpace(user.Content.String) != "" {
					params = append(params, map[string]any{
						"role":    "user",
						"content": utils.SanitizeSurrogates(user.Content.String),
					})
				}
			} else {
				var blocks []map[string]any
				for _, item := range user.Content.Parts {
					if text, ok := item.(types.TextContent); ok {
						blocks = append(blocks, map[string]any{"type": "text", "text": utils.SanitizeSurrogates(text.Text)})
					} else if image, ok := item.(types.ImageContent); ok {
						blocks = append(blocks, map[string]any{
							"type": "image",
							"source": map[string]any{
								"type":       "base64",
								"media_type": image.MimeType,
								"data":       image.Data,
							},
						})
					}
				}
				var filteredBlocks []map[string]any
				for _, b := range blocks {
					if b["type"] == "text" {
						if strings.TrimSpace(b["text"].(string)) == "" {
							continue
						}
					}
					filteredBlocks = append(filteredBlocks, b)
				}
				if len(filteredBlocks) == 0 {
					continue
				}
				params = append(params, map[string]any{"role": "user", "content": filteredBlocks})
			}
		} else if assistant, ok := msg.(types.AssistantMessage); ok {
			var blocks []map[string]any

			for _, block := range assistant.Content {
				switch b := block.(type) {
				case types.TextContent:
					if strings.TrimSpace(b.Text) == "" {
						continue
					}
					blocks = append(blocks, map[string]any{"type": "text", "text": utils.SanitizeSurrogates(b.Text)})
				case types.ThinkingContent:
					// Redacted thinking: pass the opaque payload back as
					// redacted_thinking.
					if b.Redacted {
						blocks = append(blocks, map[string]any{"type": "redacted_thinking", "data": b.ThinkingSignature})
						continue
					}
					thinkingSignature := b.ThinkingSignature
					hasThinkingSignature := strings.TrimSpace(thinkingSignature) != ""
					if strings.TrimSpace(b.Thinking) == "" && !hasThinkingSignature {
						continue
					}
					// If thinking signature is missing/empty (e.g., from an
					// aborted stream), convert to plain text for Anthropic.
					// Some compatible providers emit and accept empty
					// signatures, so let marked models preserve the block.
					if !hasThinkingSignature {
						if allowEmptySignature {
							blocks = append(blocks, map[string]any{
								"type":      "thinking",
								"thinking":  utils.SanitizeSurrogates(b.Thinking),
								"signature": "",
							})
						} else {
							blocks = append(blocks, map[string]any{
								"type": "text",
								"text": utils.SanitizeSurrogates(b.Thinking),
							})
						}
					} else {
						blocks = append(blocks, map[string]any{
							"type":      "thinking",
							"thinking":  utils.SanitizeSurrogates(b.Thinking),
							"signature": thinkingSignature,
						})
					}
				case types.ToolCall:
					name := b.Name
					if isOAuthToken {
						name = toClaudeCodeName(b.Name)
					}
					input := b.Arguments
					if input == nil {
						input = map[string]any{}
					}
					blocks = append(blocks, map[string]any{
						"type":  "tool_use",
						"id":    b.ID,
						"name":  name,
						"input": input,
					})
				}
			}
			if len(blocks) == 0 {
				continue
			}
			messageIndex := len(params)
			params = append(params, map[string]any{"role": "assistant", "content": blocks})
			if managedProvider != "" && assistant.API == types.ApiAnthropicMessages &&
				string(assistant.Provider) == managedProvider && isAnthropicEffort(assistant.ProviderThinkingLevel) {
				converted.assistantLevels[messageIndex] = AnthropicEffort(assistant.ProviderThinkingLevel)
			}
		} else if _, ok := msg.(types.ToolResultMessage); ok {
			// Collect all consecutive toolResult messages, needed for z.ai
			// Anthropic endpoint.
			var toolResults []map[string]any
			var siblingContent []map[string]any
			j := i
			for j < len(transformedMessages) {
				next, ok := transformedMessages[j].(types.ToolResultMessage)
				if !ok {
					break
				}
				toolResult, sibling := convertAnthropicToolResult(&next, isOAuthToken, deferredToolNames, loadedToolNames, normalizeToolName)
				toolResults = append(toolResults, toolResult)
				siblingContent = append(siblingContent, sibling...)
				j++
			}

			// Skip the messages we've already processed.
			i = j - 1

			// Displaced reference-bearing results must follow every
			// tool_result block.
			content := append(append([]map[string]any{}, toolResults...), siblingContent...)
			params = append(params, map[string]any{"role": "user", "content": content})
		}
	}

	// Add cache_control to the last user message to cache conversation
	// history.
	if cacheControl != nil && len(params) > 0 {
		lastMessage := params[len(params)-1]
		if lastMessage["role"] == "user" {
			switch content := lastMessage["content"].(type) {
			case []map[string]any:
				if len(content) > 0 {
					lastBlock := content[len(content)-1]
					if lastBlock["type"] == "text" || lastBlock["type"] == "image" || lastBlock["type"] == "tool_result" {
						lastBlock["cache_control"] = cacheControl
					}
				}
			case string:
				lastMessage["content"] = []map[string]any{{
					"type":          "text",
					"text":          content,
					"cache_control": cacheControl,
				}}
			}
		}
	}

	converted.messages = params
	return converted, nil
}

// isAnthropicEffort reports whether value is a valid effort level (pi:
// isAnthropicEffort).
func isAnthropicEffort(value string) bool {
	switch AnthropicEffort(value) {
	case AnthropicEffortLow, AnthropicEffortMedium, AnthropicEffortHigh, AnthropicEffortXHigh, AnthropicEffortMax:
		return true
	}
	return false
}

// insertThinkingLevelMessages inserts effort-only system markers before each
// managed assistant message and appends the active marker (pi:
// insertThinkingLevelMessages).
func insertThinkingLevelMessages(converted convertedAnthropicMessages, activeEffort AnthropicEffort) []map[string]any {
	var messages []map[string]any
	for index, message := range converted.messages {
		if historicalEffort, ok := converted.assistantLevels[index]; ok {
			messages = append(messages, map[string]any{
				"role":          "system",
				"content":       []any{},
				"output_config": map[string]any{"effort": string(historicalEffort)},
			})
		}
		messages = append(messages, message)
	}
	messages = append(messages, map[string]any{
		"role":          "system",
		"content":       []any{},
		"output_config": map[string]any{"effort": string(activeEffort)},
	})
	return messages
}

// convertAnthropicTools converts tools to Anthropic tool params (pi:
// convertTools).
func convertAnthropicTools(tools []types.Tool, isOAuthToken bool, supportsEagerToolInputStreaming bool, supportsStrictTools bool, cacheControl *anthropicCacheControl, deferLoading bool) ([]map[string]any, error) {
	if tools == nil {
		return nil, nil
	}

	out := make([]map[string]any, 0, len(tools))
	for index, tool := range tools {
		strict, err := ResolveJSONSchemaStrictSampling(&tool, supportsStrictTools)
		if err != nil {
			return nil, err
		}
		strictEnabled := strict != nil && *strict
		parameters, err := GetJSONSchemaToolParameters(&tool, strictEnabled)
		if err != nil {
			return nil, err
		}
		var schemaMap map[string]any
		if err := json.Unmarshal(parameters, &schemaMap); err != nil {
			return nil, err
		}
		properties, hasProperties := schemaMap["properties"]
		if !hasProperties || properties == nil {
			properties = map[string]any{}
		}
		required, hasRequired := schemaMap["required"]
		if !hasRequired || required == nil {
			required = []any{}
		}
		inputSchema := map[string]any{
			"type":       "object",
			"properties": properties,
			"required":   required,
		}
		if strictEnabled {
			// pi: { ...parameters, ...legacyInputSchema } — the full strict
			// schema overlaid with the legacy shape.
			full := map[string]any{}
			for key, value := range schemaMap {
				full[key] = value
			}
			full["type"] = "object"
			full["properties"] = properties
			full["required"] = required
			inputSchema = full
		}

		name := tool.Name
		if isOAuthToken {
			name = toClaudeCodeName(tool.Name)
		}
		toolParam := map[string]any{
			"name":         name,
			"description":  tool.Description,
			"input_schema": inputSchema,
		}
		if supportsEagerToolInputStreaming {
			toolParam["eager_input_streaming"] = true
		}
		if strictEnabled {
			toolParam["strict"] = true
		}
		if deferLoading {
			toolParam["defer_loading"] = true
		}
		if cacheControl != nil && index == len(tools)-1 {
			toolParam["cache_control"] = cacheControl
		}
		out = append(out, toolParam)
	}
	return out, nil
}

// mapAnthropicStopReason maps an Anthropic stop reason to the normalized one
// (pi: mapStopReason). Unknown stop reasons return an error — pi throws and
// the stream catch surfaces it, skipping the rest of the event.
func mapAnthropicStopReason(reason string, stopDetails *anthropicStopDetails) (types.StopReason, string, error) {
	switch reason {
	case "end_turn":
		return types.StopReasonStop, "", nil
	case "max_tokens":
		return types.StopReasonLength, "", nil
	case "tool_use":
		return types.StopReasonToolUse, "", nil
	case "refusal":
		message := "The model refused to complete the request"
		if stopDetails != nil && stopDetails.Explanation != "" {
			message = stopDetails.Explanation
		}
		return types.StopReasonError, message, nil
	case "pause_turn": // Stop is good enough -> resubmit
		return types.StopReasonStop, "", nil
	case "stop_sequence":
		// We don't supply stop sequences, so this should never happen.
		return types.StopReasonStop, "", nil
	case "sensitive": // Content flagged by safety filters (not yet in SDK types)
		return types.StopReasonError, "Provider stopped with: sensitive", nil
	default:
		// Handle unknown stop reasons gracefully (API may add new values);
		// pi throws and the stream catch surfaces the message.
		return types.StopReasonError, "", fmt.Errorf("Unhandled stop reason: %s", reason)
	}
}
