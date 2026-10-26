// pi: test/constrained-sampling.test.ts (new-test additions only; the older
// makeStrictJsonSchema/appendGrammarToolInputJsonDelta cases predate S11-1 and
// are covered via the adapter payload tests).
package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/samber/lo"

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

func TestGrammarToolInputJSONDeltas(t *testing.T) {
	buffer := &GrammarToolInputJSONBuffer{}
	first, err := AppendGrammarToolInputJSONDelta(buffer, "payload", `a"`)
	if err != nil || first == nil {
		t.Fatalf("append = %v, %v", first, err)
	}
	final := "a\"\nb"
	second, err := CloseGrammarToolInputJSON(buffer, "payload", &final)
	if err != nil || second == nil {
		t.Fatalf("close = %v, %v", second, err)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(*first+*second), &decoded); err != nil {
		t.Fatalf("decode deltas: %v", err)
	}
	if decoded["payload"] != final {
		t.Fatalf("payload = %#v, want %q", decoded["payload"], final)
	}
	if delta, err := CloseGrammarToolInputJSON(buffer, "payload", &final); err != nil || delta != nil {
		t.Fatalf("second close = %v, %v", delta, err)
	}
	changed := "changed"
	if _, err := CloseGrammarToolInputJSON(buffer, "payload", &changed); err == nil {
		t.Fatal("expected changed-after-close error")
	}
	if _, err := AppendGrammarToolInputJSONDelta(buffer, "payload", "x"); err == nil {
		t.Fatal("expected append-after-close error")
	}

	nonMonotonic := &GrammarToolInputJSONBuffer{}
	AppendGrammarToolInputJSONDelta(nonMonotonic, "payload", "abc")
	other := "abd"
	if _, err := CloseGrammarToolInputJSON(nonMonotonic, "payload", &other); err == nil {
		t.Fatal("expected non-monotonic error")
	}
}

func TestOpenAIResponsesStreamsCustomToolInitialInput(t *testing.T) {
	model := constrainedSamplingTestModel()
	output := newResponsesTestOutput(model)
	stream := types.NewAssistantMessageEventStream()
	events := responsesEventSeq(
		&responsesStreamEvent{Type: "response.output_item.added", OutputIndex: 0, Item: &responsesOutputItem{
			Type: "custom_tool_call", ID: "ctc_1", CallID: "call_1", Name: "sample_tool", Input: lo.ToPtr("a"),
		}},
		&responsesStreamEvent{Type: "response.custom_tool_call_input.delta", OutputIndex: 0, Delta: "b"},
		&responsesStreamEvent{Type: "response.custom_tool_call_input.done", OutputIndex: 0, Input: "abc"},
		&responsesStreamEvent{Type: "response.output_item.done", OutputIndex: 0, Item: &responsesOutputItem{
			Type: "custom_tool_call", ID: "ctc_1", CallID: "call_1", Name: "sample_tool", Input: lo.ToPtr("abc"),
		}},
		&responsesStreamEvent{Type: "response.completed", Response: &responsesResponseObject{ID: "resp_1", Status: "completed"}},
	)
	if err := ProcessResponsesStream(events, output, stream, model, &OpenAIResponsesStreamOptions{
		GrammarToolInputProperties: map[string]string{"sample_tool": "payload"},
	}); err != nil {
		t.Fatalf("ProcessResponsesStream: %v", err)
	}
	stream.End()

	var startArguments map[string]any
	var JSONDeltas []string
	for event := range stream.Events() {
		switch event.Type {
		case types.EventToolCallStart:
			if call, ok := event.Partial.Content[event.ContentIndex].(types.ToolCall); ok {
				startArguments = call.Arguments
			}
		case types.EventToolCallDelta:
			JSONDeltas = append(JSONDeltas, event.Delta)
		}
	}
	if startArguments["payload"] != "a" {
		t.Fatalf("start arguments = %#v", startArguments)
	}
	if output.StopReason != types.StopReasonToolUse {
		t.Fatalf("stop reason = %v", output.StopReason)
	}
	toolCall, ok := output.Content[0].(types.ToolCall)
	if !ok || toolCall.Arguments["payload"] != "abc" {
		t.Fatalf("tool call = %#v", output.Content[0])
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(strings.Join(JSONDeltas, "")), &decoded); err != nil {
		t.Fatalf("decode deltas: %v", err)
	}
	if decoded["payload"] != "abc" {
		t.Fatalf("delta payload = %#v", decoded["payload"])
	}
}
