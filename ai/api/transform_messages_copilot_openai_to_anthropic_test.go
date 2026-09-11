// pi: test/transform-messages-copilot-openai-to-anthropic.test.ts.
package api

import (
	"testing"

	"github.com/ZoroSpace/pig/ai/types"
)

func TestTransformMessagesCopilotOpenAIToAnthropic(t *testing.T) {
	// pi: anthropicNormalizeToolCallId.
	normalize := func(id string, model *types.Model, source *types.AssistantMessage) string {
		return normalizeAnthropicToolCallID(id)
	}
	model := &types.Model{
		ID: "claude-sonnet-4.6", Name: "Claude Sonnet 4.6", API: types.ApiAnthropicMessages,
		Provider: types.ProviderID("github-copilot"), BaseURL: "https://api.individual.githubcopilot.com",
		Reasoning: true, Input: []types.ModelInput{types.ModelInputText, types.ModelInputImage},
		ContextWindow: 128000, MaxTokens: 16000,
	}
	makeAssistant := func(content []types.Content, api types.Api, modelID string) types.AssistantMessage {
		return types.AssistantMessage{
			Role: types.RoleAssistant, Content: content, API: api,
			Provider: types.ProviderID("github-copilot"), Model: modelID,
			Usage: types.Usage{}, StopReason: types.StopReasonToolUse, Timestamp: 1,
		}
	}

	t.Run("converts thinking blocks to plain text when source model differs", func(t *testing.T) {
		messages := []types.Message{
			types.UserMessage{Role: types.RoleUser, Content: types.UserContent{String: "hello"}, Timestamp: 1},
			makeAssistant([]types.Content{
				types.ThinkingContent{Kind: types.KindThinking, Thinking: "Let me think about this...", ThinkingSignature: "reasoning_content"},
				types.NewTextContent("Hi there!"),
			}, types.ApiOpenAICompletions, "gpt-4o"),
		}
		result := TransformMessages(messages, model, normalize)
		var assistant *types.AssistantMessage
		for i := range result {
			if m, ok := result[i].(types.AssistantMessage); ok {
				assistant = &m
			}
		}
		if assistant == nil {
			t.Fatal("no assistant message")
		}
		textBlocks, thinkingBlocks := 0, 0
		for _, block := range assistant.Content {
			switch block.ContentKind() {
			case types.KindText:
				textBlocks++
			case types.KindThinking:
				thinkingBlocks++
			}
		}
		if thinkingBlocks != 0 {
			t.Errorf("thinking blocks = %d, want 0", thinkingBlocks)
		}
		if textBlocks < 2 {
			t.Errorf("text blocks = %d, want >= 2", textBlocks)
		}
	})

	t.Run("removes thoughtSignature from tool calls when migrating between models", func(t *testing.T) {
		toolCall := types.NewToolCall("call_123", "bash", map[string]any{"command": "ls"})
		toolCall.ThoughtSignature = "encrypted-thought"
		messages := []types.Message{
			types.UserMessage{Role: types.RoleUser, Content: types.UserContent{String: "run a command"}, Timestamp: 1},
			makeAssistant([]types.Content{toolCall}, types.ApiOpenAICompletions, "gpt-4o"),
			types.ToolResultMessage{Role: types.RoleToolResult, ToolCallID: "call_123", ToolName: "bash", Timestamp: 2},
		}
		result := TransformMessages(messages, model, normalize)
		for i := range result {
			if m, ok := result[i].(types.AssistantMessage); ok {
				for _, block := range m.Content {
					if tc, ok := block.(types.ToolCall); ok && tc.ThoughtSignature != "" {
						t.Errorf("thoughtSignature = %q, want it removed", tc.ThoughtSignature)
					}
				}
			}
		}
	})
}
