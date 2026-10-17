// pi: test/openai-responses-message-id.test.ts.
package api

import (
	"testing"

	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/ai/utils"
)

func TestOpenAIResponsesMessageIDConversion(t *testing.T) {
	t.Run("generates unique fallback message IDs for multiple text blocks in one assistant turn", func(t *testing.T) {
		// pi: getModel("openai-codex", "gpt-5.5") — the codex provider is a
		// later batch, so the id/api identity is hand-constructed.
		model := &types.Model{
			ID: "gpt-5.5", Name: "GPT-5.5", API: types.Api("openai-codex-responses"),
			Provider: types.ProviderID("openai-codex"),
		}
		assistant := types.AssistantMessage{
			Role: types.RoleAssistant,
			Content: []types.Content{
				types.NewTextContent("first answer"),
				types.NewTextContent("visible answer"),
			},
			API: types.ApiAnthropicMessages, Provider: types.ProviderAnthropic,
			Model: "claude-opus-4-8", StopReason: types.StopReasonStop, Timestamp: 0,
		}
		llmContext := utils.NormalizeContext(&types.Context{
			SystemPrompt: "You are concise.",
			Messages:     []types.Message{responsesUserMessage("hello"), assistant},
		})

		input := ConvertResponsesMessages(model, llmContext, map[string]struct{}{
			"openai": {}, "openai-codex": {}, "opencode": {},
		}, nil)
		var messageIDs []string
		for _, item := range input {
			if m, ok := item.(map[string]any); ok && m["type"] == "message" {
				if id, ok := m["id"].(string); ok {
					messageIDs = append(messageIDs, id)
				}
			}
		}

		if len(messageIDs) != 2 || messageIDs[0] != "msg_pi_1" || messageIDs[1] != "msg_pi_1_1" {
			t.Fatalf("messageIds = %v, want [msg_pi_1 msg_pi_1_1]", messageIDs)
		}
		seen := map[string]struct{}{}
		for _, id := range messageIDs {
			seen[id] = struct{}{}
		}
		if len(seen) != len(messageIDs) {
			t.Fatalf("messageIds not unique: %v", messageIDs)
		}
	})
}
