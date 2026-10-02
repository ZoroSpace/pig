// pi: test/constrained-sampling.test.ts (new-test additions only; the older
// makeStrictJsonSchema/appendGrammarToolInputJsonDelta cases predate S11-1 and
// are covered via the adapter payload tests).
package api

import (
	"testing"

	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/ai/utils"
)

func constrainedSamplingTestModel() *types.Model {
	return &types.Model{
		ID: "gpt-test", Name: "GPT Test", API: types.ApiOpenAIResponses,
		Provider: types.ProviderOpenAI, BaseURL: "https://api.openai.com/v1",
		Reasoning: false, Input: []types.ModelInput{types.ModelInputText, types.ModelInputImage},
		Cost:          types.ModelCost{},
		ContextWindow: 128000, MaxTokens: 4096,
	}
}

// earendil-works/radius#115: a gateway forwards another model's history as a
// foreign provider. The foreign item id is normalized to fc_*, which does not
// match the ctc_* prefix required by the replayed custom_tool_call, so the id
// must be dropped.
func TestConstrainedToolSamplingDropsForeignItemIDsWhenReplayingGrammarCalls(t *testing.T) {
	assistant := types.AssistantMessage{
		Role: types.RoleAssistant,
		Content: []types.Content{types.ToolCall{
			Kind: types.KindToolCall, ID: "call_1|ctc_1", Name: "sample_tool",
			Arguments: map[string]any{"payload": "abc"},
		}},
		API:        types.Api("pi-messages"),
		Provider:   types.ProviderID("radius"),
		Model:      "gpt-other",
		StopReason: types.StopReasonToolUse,
		Timestamp:  0,
	}
	toolResult := types.ToolResultMessage{
		Role: types.RoleToolResult, ToolCallID: "call_1|ctc_1", ToolName: "sample_tool",
		Content: []types.Content{types.NewTextContent("done")}, IsError: false, Timestamp: 1,
	}
	llmContext := utils.NormalizeContext(&types.Context{
		Messages: []types.Message{assistant, toolResult},
	})

	messages := ConvertResponsesMessages(constrainedSamplingTestModel(), llmContext,
		map[string]struct{}{"openai": {}},
		&ConvertResponsesMessagesOptions{
			GrammarToolInputProperties: map[string]string{"sample_tool": "payload"},
		})

	var call map[string]any
	for _, item := range messages {
		if m, ok := item.(map[string]any); ok && m["type"] == "custom_tool_call" {
			call = m
			break
		}
	}
	if call == nil {
		t.Fatalf("expected custom_tool_call item, got %#v", messages)
	}
	if call["call_id"] != "call_1" {
		t.Errorf("call_id = %v, want call_1", call["call_id"])
	}
	if call["input"] != "abc" {
		t.Errorf("input = %v, want abc", call["input"])
	}
	if id, present := call["id"]; present {
		t.Errorf("id = %v, want it omitted", id)
	}
}
