// pi: test/openai-responses-foreign-toolcall-id.test.ts.
package api

import (
	"regexp"
	"strings"
	"testing"

	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/ai/utils"
)

// copilotRawToolCallID is a real GitHub Copilot tool-call id: a call id and
// a 450+-char base64 item id joined by "|" (pi: COPILOT_RAW_TOOL_CALL_ID).
const copilotRawToolCallID = "call_4VnzVawQXPB9MgYib7CiQFEY|I9b95oN1wD/cHXKTw3PpRkL6KkCtzTJhUxMouMWYwHeTo2j3htzfSk7YPx2vifiIM4g3A8XXyOj8q4Bt6SLUG7gqY1E3ELkrkVQNHglRfUmWj84lqxJY+Puieb3VKyX0FB+83TUzn91cDMF/4gzt990IzqVrc+nIb9RRscRD070Du16q1glydVjWR0SBJsE6TbY/esOjFpqplogQqrajm1eI++f3eLi73R6q7hVusY0QbeFySVxABCjhN0lXB04caBe1rzHjYzul6MAXj7uq+0r17VLq+yrtyYhN12wkmFqHeqTyEei6EFPbMy24Nc+IbJlkP0OCg02W+gOnyBFcbi2ctvJFSOhSjt1CqBdqCnnhwUqXjbWiT0wh3DmLScRgTHmGkaI+oAcQQjfic65nxj+TnEkReA=="

func TestOpenAIResponsesForeignToolCallIDNormalization(t *testing.T) {
	t.Run("hashes foreign Copilot tool item IDs into a bounded Codex-safe fc_<hash> shape", func(t *testing.T) {
		// pi: getModel("openai-codex", "gpt-5.5") — the codex provider is a
		// later batch, so the id/api identity is hand-constructed.
		model := &types.Model{
			ID: "gpt-5.5", Name: "GPT-5.5", API: types.Api("openai-codex-responses"),
			Provider: types.ProviderID("openai-codex"),
		}
		assistant := types.AssistantMessage{
			Role: types.RoleAssistant,
			Content: []types.Content{types.ToolCall{
				Kind: types.KindToolCall, ID: copilotRawToolCallID, Name: "edit",
				Arguments: map[string]any{"path": "src/styles/app.css"},
			}},
			API: types.ApiOpenAIResponses, Provider: types.ProviderID("github-copilot"),
			Model: "gpt-5.5", StopReason: types.StopReasonToolUse, Timestamp: 0,
		}
		toolResult := types.ToolResultMessage{
			Role: types.RoleToolResult, ToolCallID: copilotRawToolCallID, ToolName: "edit",
			Content: []types.Content{types.NewTextContent("ok")}, IsError: false, Timestamp: 1,
		}
		llmContext := &types.Context{
			SystemPrompt: "You are concise.",
			Messages: []types.Message{
				responsesUserMessage("Use the tool."),
				assistant,
				toolResult,
			},
		}

		input := ConvertResponsesMessages(model, llmContext, map[string]struct{}{
			"openai": {}, "openai-codex": {}, "opencode": {},
		}, nil)
		var functionCall map[string]any
		for _, item := range input {
			if m, ok := item.(map[string]any); ok && m["type"] == "function_call" {
				functionCall = m
				break
			}
		}
		if functionCall == nil {
			t.Fatal("expected function_call item")
		}

		parts := strings.SplitN(copilotRawToolCallID, "|", 2)
		expectedItemID := "fc_" + utils.ShortHash(parts[1])
		if functionCall["id"] != expectedItemID {
			t.Fatalf("id = %v, want %v", functionCall["id"], expectedItemID)
		}
		if len(functionCall["id"].(string)) > 64 {
			t.Fatalf("id length = %d, want <= 64", len(functionCall["id"].(string)))
		}
		if !regexp.MustCompile(`^fc_[A-Za-z0-9]+$`).MatchString(functionCall["id"].(string)) {
			t.Fatalf("id = %v, want fc_<base36>", functionCall["id"])
		}
	})
}
