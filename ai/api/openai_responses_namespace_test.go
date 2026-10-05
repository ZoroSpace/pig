// pi: test/openai-responses-namespace.test.ts and
// test/openai-responses-partial-json-cleanup.test.ts.
package api

import (
	"testing"

	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/ai/utils"
)

// newResponsesNamespaceModel is the namespace test's fixture model (pi:
// the file-level `model` constant).
func newResponsesNamespaceModel() *types.Model {
	return &types.Model{
		ID: "gpt-5.4", Name: "GPT-5.4", API: types.ApiOpenAIResponses,
		Provider: types.ProviderOpenAI, BaseURL: openAIResponsesBaseURL,
		Reasoning: true, Input: []types.ModelInput{types.ModelInputText},
		Cost:          responsesModelCost(0, 0, 0, 0),
		ContextWindow: 400000, MaxTokens: 128000,
	}
}

func TestOpenAIResponsesTerminalMessagesOmitsAbsentErrorMessage(t *testing.T) {
	model := newResponsesNamespaceModel()
	output := newResponsesTestOutput(model)
	stream := types.NewAssistantMessageEventStream()

	events := responsesEventSeq(
		&responsesStreamEvent{Type: "response.output_item.added", OutputIndex: 0, Item: &responsesOutputItem{
			Type: "function_call", ID: "fc_test", CallID: "call_test", Name: "lookup",
		}},
		&responsesStreamEvent{Type: "response.output_item.done", OutputIndex: 0, Item: &responsesOutputItem{
			Type: "function_call", ID: "fc_test", CallID: "call_test", Name: "lookup",
			Arguments: `{"value":"hello"}`,
		}},
		&responsesStreamEvent{Type: "response.completed", Response: &responsesResponseObject{ID: "resp_test", Status: "completed"}},
	)
	if err := ProcessResponsesStream(events, output, stream, model, nil); err != nil {
		t.Fatalf("ProcessResponsesStream: %v", err)
	}

	if output.ErrorMessage != "" {
		t.Fatalf("errorMessage = %q, want absent", output.ErrorMessage)
	}
}

func TestOpenAIResponsesToolCallNamespaces(t *testing.T) {
	functionCallEvents := func() func(yield func(*responsesStreamEvent) bool) {
		return responsesEventSeq(
			&responsesStreamEvent{Type: "response.output_item.added", OutputIndex: 0, Item: &responsesOutputItem{
				Type: "function_call", ID: "fc_test", CallID: "call_test", Name: "lookup",
			}},
			&responsesStreamEvent{Type: "response.output_item.done", OutputIndex: 0, Item: &responsesOutputItem{
				Type: "function_call", ID: "fc_test", CallID: "call_test", Name: "lookup",
				Arguments: `{"value":"hello"}`, Namespace: "dynamic_tools",
			}},
			&responsesStreamEvent{Type: "response.completed", Response: &responsesResponseObject{ID: "resp_test", Status: "completed"}},
		)
	}
	customToolCallEvents := func() func(yield func(*responsesStreamEvent) bool) {
		return responsesEventSeq(
			&responsesStreamEvent{Type: "response.output_item.added", OutputIndex: 0, Item: &responsesOutputItem{
				Type: "custom_tool_call", ID: "ctc_test", CallID: "call_test", Name: "query",
			}},
			&responsesStreamEvent{Type: "response.output_item.done", OutputIndex: 0, Item: &responsesOutputItem{
				Type: "custom_tool_call", ID: "ctc_test", CallID: "call_test", Name: "query",
				Input: "hello", Namespace: "dynamic_tools",
			}},
			&responsesStreamEvent{Type: "response.completed", Response: &responsesResponseObject{ID: "resp_test", Status: "completed"}},
		)
	}
	getToolCall := func(t *testing.T, output *types.AssistantMessage) types.ToolCall {
		t.Helper()
		block, ok := output.Content[0].(types.ToolCall)
		if !ok {
			t.Fatalf("expected toolCall block, got %T", output.Content[0])
		}
		return block
	}
	findItem := func(items []any, itemType string) map[string]any {
		for _, item := range items {
			if m, ok := item.(map[string]any); ok && m["type"] == itemType {
				return m
			}
		}
		return nil
	}

	t.Run("round-trips a function namespace received only on output_item.done", func(t *testing.T) {
		model := newResponsesNamespaceModel()
		output := newResponsesTestOutput(model)
		stream := types.NewAssistantMessageEventStream()
		if err := ProcessResponsesStream(functionCallEvents(), output, stream, model, nil); err != nil {
			t.Fatalf("ProcessResponsesStream: %v", err)
		}

		toolCall := getToolCall(t, output)
		if toolCall.ID != "call_test|fc_test" || toolCall.Name != "lookup" || toolCall.Namespace != "dynamic_tools" {
			t.Fatalf("toolCall = %+v", toolCall)
		}
		if toolCall.Arguments["value"] != "hello" {
			t.Fatalf("arguments = %v", toolCall.Arguments)
		}

		replayed := ConvertResponsesMessages(model, utils.NormalizeContext(&types.Context{Messages: []types.Message{*output}}), map[string]struct{}{"openai": {}}, nil)
		item := findItem(replayed, "function_call")
		if item == nil {
			t.Fatal("expected function_call item")
		}
		if item["id"] != "fc_test" || item["call_id"] != "call_test" || item["name"] != "lookup" ||
			item["arguments"] != `{"value":"hello"}` || item["namespace"] != "dynamic_tools" {
			t.Fatalf("replayed = %v", item)
		}
	})

	t.Run("round-trips a custom-tool namespace received only on output_item.done", func(t *testing.T) {
		model := newResponsesNamespaceModel()
		output := newResponsesTestOutput(model)
		stream := types.NewAssistantMessageEventStream()
		grammarProps := map[string]string{"query": "input"}
		if err := ProcessResponsesStream(customToolCallEvents(), output, stream, model, &OpenAIResponsesStreamOptions{
			GrammarToolInputProperties: grammarProps,
		}); err != nil {
			t.Fatalf("ProcessResponsesStream: %v", err)
		}

		toolCall := getToolCall(t, output)
		if toolCall.ID != "call_test|ctc_test" || toolCall.Name != "query" || toolCall.Namespace != "dynamic_tools" {
			t.Fatalf("toolCall = %+v", toolCall)
		}
		if toolCall.Arguments["input"] != "hello" {
			t.Fatalf("arguments = %v", toolCall.Arguments)
		}

		replayed := ConvertResponsesMessages(model, utils.NormalizeContext(&types.Context{Messages: []types.Message{*output}}),
			map[string]struct{}{"openai": {}}, &ConvertResponsesMessagesOptions{GrammarToolInputProperties: grammarProps})
		item := findItem(replayed, "custom_tool_call")
		if item == nil {
			t.Fatal("expected custom_tool_call item")
		}
		if item["id"] != "ctc_test" || item["call_id"] != "call_test" || item["name"] != "query" ||
			item["input"] != "hello" || item["namespace"] != "dynamic_tools" {
			t.Fatalf("replayed = %v", item)
		}
	})

	t.Run("drops namespaces when the target cannot replay their load items", func(t *testing.T) {
		output := newResponsesTestOutput(newResponsesNamespaceModel())
		output.Content = append(output.Content,
			types.ToolCall{Kind: types.KindToolCall, ID: "call_function|fc_test", Name: "lookup",
				Arguments: map[string]any{"value": "hello"}, Namespace: "dynamic_tools"},
			types.ToolCall{Kind: types.KindToolCall, ID: "call_custom|ctc_test", Name: "query",
				Arguments: map[string]any{"input": "hello"}, Namespace: "dynamic_tools"},
		)
		base := newResponsesNamespaceModel()
		targetModels := []*types.Model{
			{ID: "gpt-5.2", Name: "GPT-5.2", API: base.API, Provider: base.Provider, BaseURL: base.BaseURL,
				Reasoning: base.Reasoning, Input: base.Input, Cost: base.Cost, ContextWindow: base.ContextWindow, MaxTokens: base.MaxTokens},
			{ID: base.ID, Name: base.Name, API: base.API, Provider: "azure", BaseURL: base.BaseURL,
				Reasoning: base.Reasoning, Input: base.Input, Cost: base.Cost, ContextWindow: base.ContextWindow, MaxTokens: base.MaxTokens},
			{ID: "gpt-5.3-codex-spark", Name: "GPT-5.3 Codex Spark", API: types.Api("openai-codex-responses"), Provider: "openai-codex",
				BaseURL: base.BaseURL, Reasoning: base.Reasoning, Input: base.Input, Cost: base.Cost,
				ContextWindow: base.ContextWindow, MaxTokens: base.MaxTokens},
		}

		for _, targetModel := range targetModels {
			replayed := ConvertResponsesMessages(targetModel, utils.NormalizeContext(&types.Context{Messages: []types.Message{*output}}),
				map[string]struct{}{"openai": {}},
				&ConvertResponsesMessagesOptions{GrammarToolInputProperties: map[string]string{"query": "input"}})
			functionCall := findItem(replayed, "function_call")
			customToolCall := findItem(replayed, "custom_tool_call")
			if functionCall == nil {
				t.Fatalf("%s: expected function_call item", targetModel.Provider)
			}
			if _, present := functionCall["namespace"]; present {
				t.Fatalf("%s: function_call has namespace %v", targetModel.Provider, functionCall["namespace"])
			}
			if customToolCall == nil {
				t.Fatalf("%s: expected custom_tool_call item", targetModel.Provider)
			}
			if _, present := customToolCall["namespace"]; present {
				t.Fatalf("%s: custom_tool_call has namespace %v", targetModel.Provider, customToolCall["namespace"])
			}
		}
	})

	t.Run("does not add a namespace to ordinary function calls", func(t *testing.T) {
		model := newResponsesNamespaceModel()
		output := newResponsesTestOutput(model)
		output.Content = append(output.Content, types.ToolCall{
			Kind: types.KindToolCall, ID: "call_test|fc_test", Name: "lookup",
			Arguments: map[string]any{"value": "hello"},
		})

		replayed := ConvertResponsesMessages(model, utils.NormalizeContext(&types.Context{Messages: []types.Message{*output}}),
			map[string]struct{}{"openai": {}}, nil)
		item := findItem(replayed, "function_call")
		if item == nil {
			t.Fatal("expected function_call item")
		}
		if _, present := item["namespace"]; present {
			t.Fatalf("function_call has namespace %v", item["namespace"])
		}
	})
}

func TestOpenAIResponsesPartialJSONCleanup(t *testing.T) {
	argumentsJSON := `{"path":"README.md","content":"updated"}`
	model := newResponsesTerminalModel()
	output := newResponsesTestOutput(model)
	stream := types.NewAssistantMessageEventStream()

	events := responsesEventSeq(
		&responsesStreamEvent{Type: "response.output_item.added", Item: &responsesOutputItem{
			Type: "function_call", ID: "fc_test", CallID: "call_test", Name: "edit",
		}},
		&responsesStreamEvent{Type: "response.function_call_arguments.delta", Delta: `{"path":"README.md"`},
		&responsesStreamEvent{Type: "response.function_call_arguments.delta", Delta: `,"content":"updated"}`},
		&responsesStreamEvent{Type: "response.function_call_arguments.done", Arguments: argumentsJSON},
		&responsesStreamEvent{Type: "response.output_item.done", Item: &responsesOutputItem{
			Type: "function_call", ID: "fc_test", CallID: "call_test", Name: "edit",
			Arguments: argumentsJSON,
		}},
		&responsesStreamEvent{Type: "response.completed", SequenceNumber: loInt64Ptr(5), Response: &responsesResponseObject{ID: "resp_test", Status: "completed"}},
	)
	if err := ProcessResponsesStream(events, output, stream, model, nil); err != nil {
		t.Fatalf("ProcessResponsesStream: %v", err)
	}
	// The reducer leaves the stream open; end it so the drain below stops.
	stream.End()

	if len(output.Content) != 1 {
		t.Fatalf("content length = %d", len(output.Content))
	}
	persistedToolCall, ok := output.Content[0].(types.ToolCall)
	if !ok {
		t.Fatalf("expected toolCall block, got %T", output.Content[0])
	}
	if persistedToolCall.Arguments["path"] != "README.md" || persistedToolCall.Arguments["content"] != "updated" {
		t.Fatalf("arguments = %v", persistedToolCall.Arguments)
	}
	// pi: "partialJson" in persistedToolCall === false — pig's value
	// snapshots never carry the scratch buffer.

	var toolCallEnd *types.AssistantMessageEvent
	for event := range stream.Events() {
		if event.Type == types.EventToolCallEnd {
			e := event
			toolCallEnd = &e
		}
	}
	if toolCallEnd == nil {
		t.Fatal("expected toolcall_end event")
	}
	if toolCallEnd.ToolCall == nil || toolCallEnd.ToolCall.Name != "edit" ||
		toolCallEnd.ToolCall.ID != "call_test|fc_test" {
		t.Fatalf("toolcall_end = %+v", toolCallEnd.ToolCall)
	}
	if len(toolCallEnd.ToolCall.Arguments) != 2 {
		t.Fatalf("toolcall_end arguments = %v", toolCallEnd.ToolCall.Arguments)
	}
}

func loInt64Ptr(v int64) *int64 { return &v }
