// pi: api/transform-messages.ts.
package api

import (
	"strings"
	"time"

	"github.com/ZoroSpace/pig/ai/types"
)

const nonVisionUserImagePlaceholder = "(image omitted: model does not support images)"
const nonVisionToolImagePlaceholder = "(tool image omitted: model does not support images)"

func replaceImagesWithPlaceholder(content []types.Content, placeholder string) []types.Content {
	result := make([]types.Content, 0, len(content))
	previousWasPlaceholder := false
	for _, block := range content {
		if block.ContentKind() == types.KindImage {
			if !previousWasPlaceholder {
				result = append(result, types.NewTextContent(placeholder))
			}
			previousWasPlaceholder = true
			continue
		}
		result = append(result, block)
		if text, ok := block.(types.TextContent); ok {
			previousWasPlaceholder = text.Text == placeholder
		} else {
			previousWasPlaceholder = false
		}
	}
	return result
}

func modelSupportsImages(model *types.Model) bool {
	for _, input := range model.Input {
		if input == types.ModelInputImage {
			return true
		}
	}
	return false
}

func downgradeUnsupportedImages(messages []types.Message, model *types.Model) []types.Message {
	if modelSupportsImages(model) {
		return messages
	}
	out := make([]types.Message, 0, len(messages))
	for _, msg := range messages {
		switch m := msg.(type) {
		case types.UserMessage:
			if m.Content.Parts != nil {
				m.Content.Parts = replaceImagesWithPlaceholder(m.Content.Parts, nonVisionUserImagePlaceholder)
			}
			out = append(out, m)
		case types.ToolResultMessage:
			m.Content = replaceImagesWithPlaceholder(m.Content, nonVisionToolImagePlaceholder)
			out = append(out, m)
		default:
			out = append(out, m)
		}
	}
	return out
}

// NormalizeToolCallIDFn normalizes a tool call id for cross-provider
// compatibility (pi: the optional normalizeToolCallId callback; OpenAI
// Responses API generates 450+ char ids with `|` separators that Anthropic
// APIs reject).
type NormalizeToolCallIDFn func(id string, model *types.Model, source *types.AssistantMessage) string

// TransformMessages normalizes a message list for replay: downgrades
// unsupported images, converts thinking blocks, normalizes tool call ids, and
// inserts synthetic empty tool results for orphaned tool calls (pi:
// transformMessages).
func TransformMessages(messages []types.Message, model *types.Model, normalizeToolCallID NormalizeToolCallIDFn) []types.Message {
	toolCallIDMap := map[string]string{}
	imageAwareMessages := downgradeUnsupportedImages(messages, model)

	transformed := make([]types.Message, 0, len(imageAwareMessages))
	for _, msg := range imageAwareMessages {
		switch m := msg.(type) {
		case types.UserMessage:
			transformed = append(transformed, m)
		case types.ToolResultMessage:
			if normalizedID, ok := toolCallIDMap[m.ToolCallID]; ok && normalizedID != m.ToolCallID {
				m.ToolCallID = normalizedID
			}
			transformed = append(transformed, m)
		case types.AssistantMessage:
			isSameModel := m.Provider == model.Provider && m.API == model.API && m.Model == model.ID
			var transformedContent []types.Content
			for _, block := range m.Content {
				switch b := block.(type) {
				case types.ThinkingContent:
					if b.Redacted {
						if isSameModel {
							transformedContent = append(transformedContent, b)
						}
						continue
					}
					if isSameModel && b.ThinkingSignature != "" {
						transformedContent = append(transformedContent, b)
						continue
					}
					if strings.TrimSpace(b.Thinking) == "" {
						continue
					}
					if isSameModel {
						transformedContent = append(transformedContent, b)
					} else {
						transformedContent = append(transformedContent, types.NewTextContent(b.Thinking))
					}
				case types.TextContent:
					if isSameModel {
						transformedContent = append(transformedContent, b)
					} else {
						transformedContent = append(transformedContent, types.NewTextContent(b.Text))
					}
				case types.ToolCall:
					normalizedToolCall := b
					if !isSameModel && b.ThoughtSignature != "" {
						normalizedToolCall.ThoughtSignature = ""
					}
					if !isSameModel && normalizeToolCallID != nil {
						normalizedID := normalizeToolCallID(b.ID, model, &m)
						if normalizedID != b.ID {
							toolCallIDMap[b.ID] = normalizedID
							normalizedToolCall.ID = normalizedID
						}
					}
					transformedContent = append(transformedContent, normalizedToolCall)
				default:
					transformedContent = append(transformedContent, b)
				}
			}
			m.Content = transformedContent
			transformed = append(transformed, m)
		default:
			transformed = append(transformed, m)
		}
	}

	result := make([]types.Message, 0, len(transformed))
	var pendingToolCalls []types.ToolCall
	existingToolResultIDs := map[string]bool{}
	insertSyntheticToolResults := func() {
		if len(pendingToolCalls) == 0 {
			return
		}
		for _, tc := range pendingToolCalls {
			if !existingToolResultIDs[tc.ID] {
				result = append(result, types.ToolResultMessage{
					Role:       types.RoleToolResult,
					ToolCallID: tc.ID,
					ToolName:   tc.Name,
					Content:    []types.Content{types.NewTextContent("No result provided")},
					IsError:    true,
					Timestamp:  time.Now().UnixMilli(),
				})
			}
		}
		pendingToolCalls = nil
		existingToolResultIDs = map[string]bool{}
	}

	for _, msg := range transformed {
		switch m := msg.(type) {
		case types.AssistantMessage:
			insertSyntheticToolResults()
			if m.StopReason == types.StopReasonError || m.StopReason == types.StopReasonAborted {
				continue
			}
			var toolCalls []types.ToolCall
			for _, block := range m.Content {
				if tc, ok := block.(types.ToolCall); ok {
					toolCalls = append(toolCalls, tc)
				}
			}
			if len(toolCalls) > 0 {
				pendingToolCalls = toolCalls
				existingToolResultIDs = map[string]bool{}
			}
			result = append(result, m)
		case types.ToolResultMessage:
			existingToolResultIDs[m.ToolCallID] = true
			result = append(result, m)
		case types.UserMessage:
			insertSyntheticToolResults()
			result = append(result, m)
		default:
			result = append(result, m)
		}
	}
	insertSyntheticToolResults()
	return result
}
