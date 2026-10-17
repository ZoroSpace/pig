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
