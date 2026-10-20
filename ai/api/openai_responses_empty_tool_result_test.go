// pi: test/openai-responses-empty-tool-result.test.ts.
package api

import (
	"testing"

	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/ai/utils"
)

func TestOpenAIResponsesEmptyToolResult(t *testing.T) {
	// pi #8720: whitespace-only text (e.g. a bare "\r\n" from bash) counts as
	// empty and uses the same placeholder.
	for _, text := range []string{"", "\r\n", " \n\t"} {
		t.Run("uses '(no tool output)' placeholder for tool result text "+text, func(t *testing.T) {
			// pi: getModel("openai", "gpt-4o-mini") — the B2 catalog provides it.
			base, ok := ai.MODELS[ai.ProviderOpenAI]["gpt-4o-mini"]
			if !ok {
				t.Fatal("gpt-4o-mini not in catalog")
			}
			model := &base

			assistant := types.AssistantMessage{
				Role: types.RoleAssistant,
				Content: []types.Content{types.ToolCall{
					Kind: types.KindToolCall, ID: "tool-1", Name: "bash",
					Arguments: map[string]any{"command": "true"},
				}},
				API: model.API, Provider: model.Provider, Model: model.ID,
				StopReason: types.StopReasonToolUse, Timestamp: 0,
			}
			toolResult := types.ToolResultMessage{
				Role: types.RoleToolResult, ToolCallID: "tool-1", ToolName: "bash",
				Content: []types.Content{types.NewTextContent(text)}, IsError: false, Timestamp: 1,
			}
			llmContext := utils.NormalizeContext(&types.Context{
				Messages: []types.Message{
					responsesUserMessage("Run the command"),
					assistant,
					toolResult,
				},
			})

			input := ConvertResponsesMessages(model, llmContext, map[string]struct{}{
				"openai": {}, "openai-codex": {}, "opencode": {},
			}, nil)
			var functionCallOutput map[string]any
			for _, item := range input {
				if m, ok := item.(map[string]any); ok && m["type"] == "function_call_output" {
					functionCallOutput = m
					break
				}
			}
			if functionCallOutput == nil {
				t.Fatal("expected function_call_output item")
			}
			if functionCallOutput["output"] != "(no tool output)" {
				t.Fatalf("output = %v, want (no tool output)", functionCallOutput["output"])
			}
		})
	}
}
