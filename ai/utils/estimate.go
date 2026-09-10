// pi: utils/estimate.ts.
package utils

import (
	"encoding/json"

	"github.com/ZoroSpace/pig/ai/types"
)

// ContextUsageEstimate is the token estimate of one context (pi:
// ContextUsageEstimate).
type ContextUsageEstimate struct {
	// Tokens is the estimated total context tokens.
	Tokens int64
	// UsageTokens is tokens reported by the most recent applicable assistant
	// usage block.
	UsageTokens int64
	// TrailingTokens is the estimated tokens after the most recent applicable
	// assistant usage block.
	TrailingTokens int64
	// LastUsageIndex is the index of the applicable message that provided
	// usage, or -1 when none exists (pi: number | null).
	LastUsageIndex int64
}

const charsPerToken = 4
const estimatedImageChars = 4800

// CalculateContextTokens returns the reported context size of one usage block
// (pi: calculateContextTokens).
func CalculateContextTokens(usage *types.Usage) int64 {
	if usage.TotalTokens != 0 {
		return usage.TotalTokens
	}
	return usage.Input + usage.Output + usage.CacheRead + usage.CacheWrite
}

func safeJSONStringifyEstimate(value any) string {
	if b, err := json.Marshal(value); err == nil {
		return string(b)
	}
	return "[unserializable]"
}

func estimateTextAndImageContentChars(content *types.UserContent) int64 {
	if content.Parts == nil {
		return int64(len(content.String))
	}
	var chars int64
	for _, block := range content.Parts {
		switch b := block.(type) {
		case types.TextContent:
			chars += int64(len(b.Text))
		case types.ImageContent:
			chars += estimatedImageChars
		}
	}
	return chars
}

func estimateTextAndImageContentBlocksChars(content []types.Content) int64 {
	var chars int64
	for _, block := range content {
		switch b := block.(type) {
		case types.TextContent:
			chars += int64(len(b.Text))
		case types.ImageContent:
			chars += estimatedImageChars
		}
	}
	return chars
}

// EstimateTextTokens estimates the token count of a text (pi:
// estimateTextTokens).
func EstimateTextTokens(text string) int64 {
	return ceilDiv(int64(len(text)), charsPerToken)
}

// EstimateTextAndImageContentTokens estimates the tokens of a user/tool-result
// content body (pi: estimateTextAndImageContentTokens).
func EstimateTextAndImageContentTokens(content *types.UserContent) int64 {
	return ceilDiv(estimateTextAndImageContentChars(content), charsPerToken)
}

// EstimateMessageTokens estimates the tokens of one message (pi:
// estimateMessageTokens).
func EstimateMessageTokens(message types.Message) int64 {
	var chars int64
	switch m := message.(type) {
	case types.UserMessage:
		return EstimateTextAndImageContentTokens(&m.Content)
	case types.ToolResultMessage:
		return ceilDiv(estimateTextAndImageContentBlocksChars(m.Content), charsPerToken)
	case types.AssistantMessage:
		for _, block := range m.Content {
			switch b := block.(type) {
			case types.TextContent:
				chars += int64(len(b.Text))
			case types.ThinkingContent:
				chars += int64(len(b.Thinking))
			case types.ToolCall:
				chars += int64(len(b.Name)) + int64(len(safeJSONStringifyEstimate(b.Arguments)))
			}
		}
	}
	return ceilDiv(chars, charsPerToken)
}

func messageTimestamp(message types.Message) int64 {
	switch m := message.(type) {
	case types.UserMessage:
		return m.Timestamp
	case types.AssistantMessage:
		return m.Timestamp
	case types.ToolResultMessage:
		return m.Timestamp
	}
	return 0
}

func getLastAssistantUsageInfo(messages []types.Message) (usage types.Usage, index int64, ok bool) {
	latestPrefixTimestamp := int64(-1) << 62 // Number.NEGATIVE_INFINITY
	var foundUsage types.Usage
	var foundIndex int64 = -1
	found := false
	for i, message := range messages {
		if assistant, isAssistant := message.(types.AssistantMessage); isAssistant {
			usageAppliesToPrefix := assistant.Timestamp >= latestPrefixTimestamp
			if usageAppliesToPrefix &&
				assistant.StopReason != types.StopReasonAborted &&
				assistant.StopReason != types.StopReasonError &&
				CalculateContextTokens(&assistant.Usage) > 0 {
				foundUsage = assistant.Usage
				foundIndex = int64(i)
				found = true
			}
		}
		if ts := messageTimestamp(message); ts > latestPrefixTimestamp {
			latestPrefixTimestamp = ts
		}
	}
	return foundUsage, foundIndex, found
}

func estimateMessages(messages []types.Message) ContextUsageEstimate {
	usage, usageIndex, found := getLastAssistantUsageInfo(messages)
	if found {
		usageTokens := CalculateContextTokens(&usage)
		var trailingTokens int64
		for i := usageIndex + 1; i < int64(len(messages)); i++ {
			trailingTokens += EstimateMessageTokens(messages[i])
		}
		return ContextUsageEstimate{
			Tokens:         usageTokens + trailingTokens,
			UsageTokens:    usageTokens,
			TrailingTokens: trailingTokens,
			LastUsageIndex: usageIndex,
		}
	}
	var tokens int64
	for _, message := range messages {
		tokens += EstimateMessageTokens(message)
	}
	return ContextUsageEstimate{Tokens: tokens, UsageTokens: 0, TrailingTokens: tokens, LastUsageIndex: -1}
}

func estimateToolsTokens(tools []types.Tool) int64 {
	if len(tools) == 0 {
		return 0
	}
	return EstimateTextTokens(safeJSONStringifyEstimate(tools))
}

// EstimateContextTokens estimates the tokens of a full context or a bare
// message list (pi: estimateContextTokens).
func EstimateContextTokens(context *types.Context) ContextUsageEstimate {
	estimate := estimateMessages(context.Messages)
	if estimate.LastUsageIndex != -1 {
		addedNames := map[string]bool{}
		for _, message := range context.Messages[estimate.LastUsageIndex+1:] {
			if tr, isToolResult := message.(types.ToolResultMessage); isToolResult {
				for _, name := range tr.AddedToolNames {
					addedNames[name] = true
				}
			}
		}
		var addedToolTokens int64
		if len(addedNames) > 0 {
			var filtered []types.Tool
			for _, tool := range context.Tools {
				if addedNames[tool.Name] {
					filtered = append(filtered, tool)
				}
			}
			addedToolTokens = estimateToolsTokens(filtered)
		}
		return ContextUsageEstimate{
			Tokens:         estimate.Tokens + addedToolTokens,
			UsageTokens:    estimate.UsageTokens,
			TrailingTokens: estimate.TrailingTokens + addedToolTokens,
			LastUsageIndex: estimate.LastUsageIndex,
		}
	}
	var prefixTokens int64
	if context.SystemPrompt != "" {
		prefixTokens += EstimateTextTokens(context.SystemPrompt)
	}
	prefixTokens += estimateToolsTokens(context.Tools)
	return ContextUsageEstimate{
		Tokens:         estimate.Tokens + prefixTokens,
		UsageTokens:    estimate.UsageTokens,
		TrailingTokens: estimate.TrailingTokens + prefixTokens,
		LastUsageIndex: estimate.LastUsageIndex,
	}
}

func ceilDiv(a, b int64) int64 {
	if a <= 0 {
		return 0
	}
	return (a + b - 1) / b
}
