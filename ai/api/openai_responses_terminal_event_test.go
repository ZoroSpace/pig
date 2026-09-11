// pi: test/openai-responses-terminal-event.test.ts.
package api

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/ZoroSpace/pig/ai/types"
)

// responsesEventSeq adapts a slice of events into the reducer's iterator (pi:
// the async generator fixtures).
func responsesEventSeq(events ...*responsesStreamEvent) func(yield func(*responsesStreamEvent) bool) {
	return func(yield func(*responsesStreamEvent) bool) {
		for _, e := range events {
			if !yield(e) {
				return
			}
		}
	}
}

// newResponsesTestOutput builds the reducer's output message (pi:
// createOutput).
func newResponsesTestOutput(model *types.Model) *types.AssistantMessage {
	return &types.AssistantMessage{
		Role:       types.RoleAssistant,
		Content:    []types.Content{},
		API:        model.API,
		Provider:   model.Provider,
		Model:      model.ID,
		Usage:      types.Usage{},
		StopReason: types.StopReasonPending,
		Timestamp:  0,
	}
}

// newResponsesTerminalModel builds the fixture model (pi: createModel).
func newResponsesTerminalModel() *types.Model {
	return &types.Model{
		ID: "gpt-5-mini", Name: "GPT-5 Mini", API: types.ApiOpenAIResponses,
		Provider: types.ProviderOpenAI, BaseURL: openAIResponsesBaseURL,
		Reasoning: true, Input: []types.ModelInput{types.ModelInputText},
		Cost:          responsesModelCost(0, 0, 0, 0),
		ContextWindow: 400000, MaxTokens: 128000,
	}
}

// observeStopReasons drains the stream while recording each event's partial
// stop reason (pi: the stream.push override).
func observeStopReasons(stream *types.AssistantMessageEventStream) []types.StopReason {
	var observed []types.StopReason
	for event := range stream.Events() {
		if event.Partial != nil {
			observed = append(observed, event.Partial.StopReason)
		}
	}
	return observed
}

func TestOpenAIResponsesTerminalEventHandling(t *testing.T) {
	t.Run("rejects streams that end before a terminal response event", func(t *testing.T) {
		model := newResponsesTerminalModel()
		output := newResponsesTestOutput(model)
		stream := types.NewAssistantMessageEventStream()

		events := responsesEventSeq(
			&responsesStreamEvent{Type: "response.created", Response: &responsesResponseObject{ID: "resp_early_eof"}},
			&responsesStreamEvent{Type: "response.output_item.added", OutputIndex: 0, Item: &responsesOutputItem{Type: "reasoning", ID: "rs_early_eof"}},
			&responsesStreamEvent{Type: "response.reasoning_text.delta", OutputIndex: 0, Delta: "partial reasoning before the stream ends"},
		)
		err := ProcessResponsesStream(events, output, stream, model, nil)
		if err == nil || err.Error() != "OpenAI Responses stream ended before a terminal response event" {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("emits an error final result when the wrapper stream ends before a terminal response event", func(t *testing.T) {
		model := newResponsesTerminalModel()
		llmContext := &types.Context{
			Messages: []types.Message{types.UserMessage{Role: types.RoleUser, Content: types.UserContent{Parts: []types.Content{types.NewTextContent("hi")}}, Timestamp: 0}},
		}
		captured := &capturedResponsesRequest{}
		options := &OpenAIResponsesOptions{}
		options.APIKey = "test"
		options.Fetch = func(req *http.Request) (*http.Response, error) {
			captured.req = req
			return &http.Response{
				StatusCode: 200,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body: io.NopCloser(strings.NewReader("data: " + `{"type":"response.created","sequence_number":0,"response":{"id":"resp_wrapper_early_eof"}}` + "\n\n" +
					"data: " + `{"type":"response.output_item.added","sequence_number":1,"output_index":0,"item":{"type":"reasoning","id":"rs_wrapper_early_eof","summary":[]}}` + "\n\n" +
					"data: " + `{"type":"response.reasoning_text.delta","sequence_number":2,"output_index":0,"content_index":0,"item_id":"rs_wrapper_early_eof","delta":"partial reasoning before the wrapper stream ends"}` + "\n\n")),
			}, nil
		}
		stream := streamOpenAIResponses(context.Background(), model, llmContext, options)
		var events []types.AssistantMessageEvent
		var initialStopReason types.StopReason
		for event := range stream.Events() {
			if event.Type == types.EventStart && event.Partial != nil {
				initialStopReason = event.Partial.StopReason
			}
			events = append(events, event)
		}
		result := stream.Result()
		lastEvent := events[len(events)-1]
		if initialStopReason != types.StopReasonPending {
			t.Fatalf("initialStopReason = %v", initialStopReason)
		}
		if lastEvent.Type != types.EventError {
			t.Fatalf("last event = %v, want error", lastEvent.Type)
		}
		if result.StopReason != types.StopReasonError {
			t.Fatalf("result.stopReason = %v", result.StopReason)
		}
		if result.ErrorMessage != "OpenAI Responses stream ended before a terminal response event" {
			t.Fatalf("errorMessage = %q", result.ErrorMessage)
		}
	})

	phaseCases := []struct {
		phases   [2]string
		expected []types.StopReason
	}{
		{[2]string{"commentary", "commentary"}, []types.StopReason{types.StopReasonPending, types.StopReasonPending}},
		{[2]string{"final_answer", "final_answer"}, []types.StopReason{types.StopReasonStop, types.StopReasonStop}},
		{[2]string{"commentary", "final_answer"}, []types.StopReason{types.StopReasonPending, types.StopReasonStop}},
	}
	for _, tc := range phaseCases {
		tc := tc
		t.Run("tracks message phases "+tc.phases[0]+"->"+tc.phases[1], func(t *testing.T) {
			model := newResponsesTerminalModel()
			output := newResponsesTestOutput(model)
			stream := types.NewAssistantMessageEventStream()

			events := responsesEventSeq(
				&responsesStreamEvent{Type: "response.output_item.added", OutputIndex: 0, Item: &responsesOutputItem{
					Type: "message", ID: "msg_phase", Role: "assistant", Status: "in_progress", Phase: tc.phases[0],
				}},
				&responsesStreamEvent{Type: "response.output_item.done", OutputIndex: 0, Item: &responsesOutputItem{
					Type: "message", ID: "msg_phase", Role: "assistant", Status: "completed",
					Content: []responsesContentPart{{Type: "output_text", Text: "answer"}}, Phase: tc.phases[1],
				}},
				&responsesStreamEvent{Type: "response.completed", Response: &responsesResponseObject{ID: "resp_phase", Status: "completed"}},
			)
			if err := ProcessResponsesStream(events, output, stream, model, nil); err != nil {
				t.Fatalf("ProcessResponsesStream: %v", err)
			}
			stream.End()

			observed := observeStopReasons(stream)
			if len(observed) != len(tc.expected) {
				t.Fatalf("observed = %v, want %v", observed, tc.expected)
			}
			for i, want := range tc.expected {
				if observed[i] != want {
					t.Fatalf("observed = %v, want %v", observed, tc.expected)
				}
			}
			if output.StopReason != types.StopReasonStop {
				t.Fatalf("output.stopReason = %v", output.StopReason)
			}
		})
	}

	t.Run("replaces a provisional final-answer stop with an incomplete terminal reason", func(t *testing.T) {
		model := newResponsesTerminalModel()
		output := newResponsesTestOutput(model)
		stream := types.NewAssistantMessageEventStream()

		events := responsesEventSeq(
			&responsesStreamEvent{Type: "response.output_item.added", OutputIndex: 0, Item: &responsesOutputItem{
				Type: "message", ID: "msg_phase", Role: "assistant", Status: "in_progress", Phase: "final_answer",
			}},
			&responsesStreamEvent{Type: "response.output_item.done", OutputIndex: 0, Item: &responsesOutputItem{
				Type: "message", ID: "msg_phase", Role: "assistant", Status: "completed",
				Content: []responsesContentPart{{Type: "output_text", Text: "answer"}}, Phase: "final_answer",
			}},
			&responsesStreamEvent{Type: "response.incomplete", Response: &responsesResponseObject{
				ID: "resp_phase", Status: "incomplete",
				IncompleteDetails: &responsesIncompleteDetails{Reason: "max_output_tokens"},
			}},
		)
		if err := ProcessResponsesStream(events, output, stream, model, nil); err != nil {
			t.Fatalf("ProcessResponsesStream: %v", err)
		}
		stream.End()

		observed := observeStopReasons(stream)
		if len(observed) != 2 || observed[0] != types.StopReasonStop || observed[1] != types.StopReasonStop {
			t.Fatalf("observed = %v, want [stop stop]", observed)
		}
		if output.StopReason != types.StopReasonLength {
			t.Fatalf("output.stopReason = %v", output.StopReason)
		}
	})

	t.Run("finalizes completed terminal events as stop", func(t *testing.T) {
		model := newResponsesTerminalModel()
		output := newResponsesTestOutput(model)
		stream := types.NewAssistantMessageEventStream()

		events := responsesEventSeq(
			&responsesStreamEvent{Type: "response.completed", Response: &responsesResponseObject{
				ID: "resp_completed", Status: "completed",
				Usage: &responsesUsage{
					InputTokens: 20, OutputTokens: 7, TotalTokens: 27,
					InputTokensDetails: &struct {
						CachedTokens     int64 `json:"cached_tokens"`
						CacheWriteTokens int64 `json:"cache_write_tokens"`
					}{CachedTokens: 2, CacheWriteTokens: 3},
				},
			}},
		)
		if err := ProcessResponsesStream(events, output, stream, model, nil); err != nil {
			t.Fatalf("ProcessResponsesStream: %v", err)
		}

		if output.ResponseID != "resp_completed" {
			t.Fatalf("responseId = %v", output.ResponseID)
		}
		if output.StopReason != types.StopReasonStop {
			t.Fatalf("stopReason = %v", output.StopReason)
		}
		if output.RawStopReason != "completed" {
			t.Fatalf("rawStopReason = %v", output.RawStopReason)
		}
		if output.Usage.Input != 15 || output.Usage.Output != 7 || output.Usage.CacheRead != 2 ||
			output.Usage.CacheWrite != 3 || output.Usage.TotalTokens != 27 {
			t.Fatalf("usage = %+v", output.Usage)
		}
	})

	t.Run("finalizes incomplete terminal events as length stops", func(t *testing.T) {
		model := newResponsesTerminalModel()
		output := newResponsesTestOutput(model)
		stream := types.NewAssistantMessageEventStream()

		events := responsesEventSeq(
			&responsesStreamEvent{Type: "response.incomplete", Response: &responsesResponseObject{
				ID: "resp_incomplete", Status: "incomplete",
				IncompleteDetails: &responsesIncompleteDetails{Reason: "max_output_tokens"},
				Usage: &responsesUsage{
					InputTokens: 30, OutputTokens: 12, TotalTokens: 42,
					InputTokensDetails: &struct {
						CachedTokens     int64 `json:"cached_tokens"`
						CacheWriteTokens int64 `json:"cache_write_tokens"`
					}{CachedTokens: 5},
				},
			}},
		)
		if err := ProcessResponsesStream(events, output, stream, model, nil); err != nil {
			t.Fatalf("ProcessResponsesStream: %v", err)
		}

		if output.ResponseID != "resp_incomplete" {
			t.Fatalf("responseId = %v", output.ResponseID)
		}
		if output.StopReason != types.StopReasonLength {
			t.Fatalf("stopReason = %v", output.StopReason)
		}
		if output.RawStopReason != "incomplete.max_output_tokens" {
			t.Fatalf("rawStopReason = %v", output.RawStopReason)
		}
		if output.Usage.Input != 25 || output.Usage.Output != 12 || output.Usage.CacheRead != 5 ||
			output.Usage.CacheWrite != 0 || output.Usage.TotalTokens != 42 {
			t.Fatalf("usage = %+v", output.Usage)
		}
	})

	for _, reason := range []string{"content_filter", "max_time_limit"} {
		reason := reason
		t.Run("finalizes content-filtered/unknown incomplete responses as non-retryable errors ("+reason+")", func(t *testing.T) {
			model := newResponsesTerminalModel()
			output := newResponsesTestOutput(model)
			stream := types.NewAssistantMessageEventStream()

			events := responsesEventSeq(
				&responsesStreamEvent{Type: "response.incomplete", Response: &responsesResponseObject{
					ID: "resp_incomplete", Status: "incomplete",
					IncompleteDetails: &responsesIncompleteDetails{Reason: reason},
					Usage: &responsesUsage{
						InputTokens: 30, OutputTokens: 12, TotalTokens: 42,
						InputTokensDetails: &struct {
							CachedTokens     int64 `json:"cached_tokens"`
							CacheWriteTokens int64 `json:"cache_write_tokens"`
						}{CachedTokens: 5},
					},
				}},
			)
			if err := ProcessResponsesStream(events, output, stream, model, nil); err != nil {
				t.Fatalf("ProcessResponsesStream: %v", err)
			}

			if output.StopReason != types.StopReasonError {
				t.Fatalf("stopReason = %v", output.StopReason)
			}
			if output.RawStopReason != "incomplete."+reason {
				t.Fatalf("rawStopReason = %v", output.RawStopReason)
			}
			if output.ErrorMessage != "Response incomplete: "+reason {
				t.Fatalf("errorMessage = %q", output.ErrorMessage)
			}
		})
	}

	t.Run("rejects failed terminal events with the provider error", func(t *testing.T) {
		model := newResponsesTerminalModel()
		output := newResponsesTestOutput(model)
		stream := types.NewAssistantMessageEventStream()

		events := responsesEventSeq(
			&responsesStreamEvent{Type: "response.failed", Response: &responsesResponseObject{
				ID: "resp_failed", Status: "failed",
				Error: &responsesResponseError{Code: "server_error", Message: "boom"},
			}},
		)
		err := ProcessResponsesStream(events, output, stream, model, nil)
		if err == nil || err.Error() != "server_error: boom" {
			t.Fatalf("err = %v", err)
		}
		if output.RawStopReason != "failed" {
			t.Fatalf("rawStopReason = %v", output.RawStopReason)
		}
	})
}
