// pi: test/transform-messages-cross-model-thinking.test.ts.
package api

import (
	"testing"

	"github.com/ZoroSpace/pig/ai/types"
)

func crossModelKimi() *types.Model {
	return &types.Model{
		ID: "moonshotai/Kimi-K3", Name: "Kimi K3", API: types.ApiOpenAICompletions,
		Provider: types.ProviderID("sference"), BaseURL: "https://api.sference.com/v1",
		Reasoning: true, Input: []types.ModelInput{types.ModelInputText, types.ModelInputImage},
		ContextWindow: 1048576, MaxTokens: 32768,
	}
}

func crossModelAssistant(modelID string, content []types.Content) types.AssistantMessage {
	return types.AssistantMessage{
		Role: types.RoleAssistant, Content: content, API: types.ApiOpenAICompletions,
		Provider: types.ProviderID("sference"), Model: modelID,
		Usage: types.Usage{}, StopReason: types.StopReasonToolUse, Timestamp: 1,
	}
}

func thinkingWithSignature(text string) types.ThinkingContent {
	return types.ThinkingContent{Kind: types.KindThinking, Thinking: text, ThinkingSignature: "reasoning_content"}
}

func TestTransformMessagesCrossModelThinking(t *testing.T) {
	// https://github.com/earendil-works/pi/issues/10995
	t.Run("does not send another model's readable thinking as text", func(t *testing.T) {
		messages := []types.Message{
			types.UserMessage{Role: types.RoleUser, Content: types.UserContent{String: "Why does curl work but node fails?"}, Timestamp: 1},
			crossModelAssistant("zai-org/GLM-5.3", []types.Content{
				thinkingWithSignature("The user asks about ECONNRESET. Candidates: proxy, IPv6, TLS."),
				types.NewTextContent("Let me check the TLS versions."),
				types.NewToolCall("call_1", "bash", map[string]any{"command": "curl -v"}),
			}),
			types.ToolResultMessage{
				Role: types.RoleToolResult, ToolCallID: "call_1", ToolName: "bash",
				Content: []types.Content{types.NewTextContent("TLSv1.2")}, IsError: false, Timestamp: 2,
			},
		}

		result := TransformMessages(messages, crossModelKimi(), nil)
		var assistant *types.AssistantMessage
		for i := range result {
			if m, ok := result[i].(types.AssistantMessage); ok {
				assistant = &m
			}
		}
		if assistant == nil {
			t.Fatal("no assistant message")
		}
		if len(assistant.Content) != 2 {
			t.Fatalf("content = %#v, want text + toolCall", assistant.Content)
		}
		if text, ok := assistant.Content[0].(types.TextContent); !ok || text.Text != "Let me check the TLS versions." {
			t.Errorf("content[0] = %#v", assistant.Content[0])
		}
		if tc, ok := assistant.Content[1].(types.ToolCall); !ok || tc.ID != "call_1" || tc.Name != "bash" {
			t.Errorf("content[1] = %#v", assistant.Content[1])
		}
	})

	t.Run("keeps thinking only from the target model when models alternate", func(t *testing.T) {
		kimiThinking := func(text string) types.ThinkingContent { return thinkingWithSignature(text) }
		messages := []types.Message{
			types.UserMessage{Role: types.RoleUser, Content: types.UserContent{String: "1"}, Timestamp: 1},
			crossModelAssistant("moonshotai/Kimi-K3", []types.Content{kimiThinking("kimi 1"), types.NewTextContent("a")}),
			types.UserMessage{Role: types.RoleUser, Content: types.UserContent{String: "2"}, Timestamp: 2},
			crossModelAssistant("zai-org/GLM-5.3", []types.Content{kimiThinking("glm 1"), types.NewTextContent("b")}),
			types.UserMessage{Role: types.RoleUser, Content: types.UserContent{String: "3"}, Timestamp: 3},
			crossModelAssistant("moonshotai/Kimi-K3", []types.Content{kimiThinking("kimi 2"), types.NewTextContent("c")}),
			types.UserMessage{Role: types.RoleUser, Content: types.UserContent{String: "4"}, Timestamp: 4},
			crossModelAssistant("zai-org/GLM-5.3", []types.Content{kimiThinking("glm 2"), types.NewTextContent("d")}),
			types.UserMessage{Role: types.RoleUser, Content: types.UserContent{String: "hi"}, Timestamp: 5},
		}

		result := TransformMessages(messages, crossModelKimi(), nil)
		var contents [][]types.Content
		for i := range result {
			if m, ok := result[i].(types.AssistantMessage); ok {
				contents = append(contents, m.Content)
			}
		}
		if len(contents) != 4 {
			t.Fatalf("assistant count = %d, want 4", len(contents))
		}
		wantThinking := []bool{true, false, true, false}
		for i, content := range contents {
			_, hasThinking := content[0].(types.ThinkingContent)
			if hasThinking != wantThinking[i] {
				t.Errorf("content[%d] thinking = %v, want %v (%#v)", i, hasThinking, wantThinking[i], content)
			}
		}
	})
}
