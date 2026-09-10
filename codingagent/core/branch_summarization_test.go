// pi: test/branch-summarization.test.ts — tool-call rejection and option
// handling of the coding-agent branch summarizer.
package core

import (
	"context"
	"testing"
	"time"

	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/types"
)

var branchTestModel = &ai.Model{
	ID:            "test-model",
	Name:          "Test Model",
	API:           "anthropic-messages",
	Provider:      "anthropic",
	BaseURL:       "https://api.anthropic.com",
	Reasoning:     false,
	Input:         []types.ModelInput{types.ModelInputText},
	Cost:          ai.ModelCost{},
	ContextWindow: 200000,
	MaxTokens:     8192,
}

var branchTestEntries = []SessionEntry{
	&SessionMessageEntry{
		EntryBase: EntryBase{
			Type:      "message",
			ID:        "branch-user",
			ParentID:  nil,
			Timestamp: time.UnixMilli(1).UTC().Format("2006-01-02T15:04:05.000Z07:00"),
		},
		Message: types.UserMessage{
			Role:      types.RoleUser,
			Content:   types.UserContent{String: "Abandoned request"},
			Timestamp: 1,
		},
	},
}

// branchResponse builds the assistant message a fake stream resolves with
// (pi: response(content) spread over fauxAssistantMessage("")).
func branchResponse(content []types.Content) *types.AssistantMessage {
	return &types.AssistantMessage{
		Role:       types.RoleAssistant,
		Content:    content,
		API:        branchTestModel.API,
		Provider:   branchTestModel.Provider,
		Model:      branchTestModel.ID,
		StopReason: types.StopReasonStop,
		Timestamp:  time.Now().UnixMilli(),
	}
}

// pushDone resolves the stream with a done event (pi: queueMicrotask(() =>
// stream.push({ type: "done", ... })) — the push lands before the caller can
// observe the stream, which Go expresses synchronously).
func pushDone(stream *types.AssistantMessageEventStream, reason types.StopReason, message *types.AssistantMessage) *types.AssistantMessageEventStream {
	stream.Push(types.AssistantMessageEvent{Type: types.EventDone, Reason: reason, Message: message})
	return stream
}

func TestDoesNotOverrideToolChoiceForBranchSummaries(t *testing.T) {
	var requestOptions *ai.SimpleStreamOptions
	streamFn := func(ctx context.Context, model *types.Model, llmContext *types.Context, options *ai.SimpleStreamOptions) *types.AssistantMessageEventStream {
		requestOptions = options
		stream := types.NewAssistantMessageEventStream()
		return pushDone(stream, types.StopReasonStop, branchResponse([]types.Content{types.NewTextContent("summary")}))
	}

	result, err := GenerateBranchSummary(t.Context(), branchTestEntries, &GenerateBranchSummaryOptions{
		Model:    branchTestModel,
		StreamFn: streamFn,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Error != "" || result.Aborted {
		t.Fatalf("unexpected failure: %+v", result)
	}

	if requestOptions == nil {
		t.Fatal("expected captured request options")
	}
	if requestOptions.ToolChoice != "" {
		t.Fatalf("expected toolChoice undefined, got %q", requestOptions.ToolChoice)
	}
	// pi (v0.85.1): the branch history output cap is 4096 (model.maxTokens 8192 here).
	if requestOptions.MaxTokens == nil || *requestOptions.MaxTokens != 4096 {
		t.Fatalf("expected maxTokens 4096, got %v", requestOptions.MaxTokens)
	}
}

func TestClampsBranchSummaryOutputCapToModelLimit(t *testing.T) {
	var requestOptions *ai.SimpleStreamOptions
	streamFn := func(ctx context.Context, model *types.Model, llmContext *types.Context, options *ai.SimpleStreamOptions) *types.AssistantMessageEventStream {
		requestOptions = options
		stream := types.NewAssistantMessageEventStream()
		return pushDone(stream, types.StopReasonStop, branchResponse([]types.Content{types.NewTextContent("summary")}))
	}

	clampedModel := *branchTestModel
	clampedModel.MaxTokens = 1024
	result, err := GenerateBranchSummary(t.Context(), branchTestEntries, &GenerateBranchSummaryOptions{
		Model:    &clampedModel,
		StreamFn: streamFn,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Error != "" || result.Aborted {
		t.Fatalf("unexpected failure: %+v", result)
	}

	if requestOptions == nil {
		t.Fatal("expected captured request options")
	}
	if requestOptions.MaxTokens == nil || *requestOptions.MaxTokens != 1024 {
		t.Fatalf("expected maxTokens 1024, got %v", requestOptions.MaxTokens)
	}
}

func TestRejectsLengthLimitedBranchSummaries(t *testing.T) {
	streamFn := func(ctx context.Context, model *types.Model, llmContext *types.Context, options *ai.SimpleStreamOptions) *types.AssistantMessageEventStream {
		stream := types.NewAssistantMessageEventStream()
		message := branchResponse([]types.Content{types.NewTextContent("partial")})
		message.StopReason = types.StopReasonLength
		return pushDone(stream, types.StopReasonLength, message)
	}

	result, err := GenerateBranchSummary(t.Context(), branchTestEntries, &GenerateBranchSummaryOptions{
		Model:    branchTestModel,
		StreamFn: streamFn,
	})
	if err != nil {
		t.Fatal(err)
	}

	if result.Error != "Branch summarization failed: generation hit the token cap and the summary is incomplete" {
		t.Fatalf("expected length rejection, got %+v", result)
	}
}

func TestRejectsToolCallsFromBranchSummaries(t *testing.T) {
	streamFn := func(ctx context.Context, model *types.Model, llmContext *types.Context, options *ai.SimpleStreamOptions) *types.AssistantMessageEventStream {
		stream := types.NewAssistantMessageEventStream()
		return pushDone(stream, types.StopReasonToolUse, branchResponse([]types.Content{types.ToolCall{
			Kind:      types.KindToolCall,
			ID:        "tool-call-1",
			Name:      "read",
			Arguments: map[string]any{"path": "README.md"},
		}}))
	}

	result, err := GenerateBranchSummary(t.Context(), branchTestEntries, &GenerateBranchSummaryOptions{
		Model:    branchTestModel,
		StreamFn: streamFn,
	})
	if err != nil {
		t.Fatal(err)
	}

	if result.Error != "Branch summarization attempted to call a tool" {
		t.Fatalf("expected tool call rejection, got %+v", result)
	}
}
