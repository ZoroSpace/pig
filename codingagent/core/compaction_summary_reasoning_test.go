// pi: test/compaction-summary-reasoning.test.ts — reasoning options, routing
// and error handling of the summarization call path. pi mocks completeSimple
// at the pi-ai/compat module boundary; pig overrides the
// completeSimpleFallback seam in package core.
package core

import (
	"context"
	"strings"
	"testing"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/types"
)

// summaryMock records (context, options) per call and returns the queued
// responses in order (pi: completeSimpleMock).
type summaryMock struct {
	calls      []*ai.SimpleStreamOptions
	contexts   []*ai.Context
	responses  []*types.AssistantMessage
	sessionIDs []string
	originalFn func(ctx context.Context, model *ai.Model, llmContext *ai.Context, options *ai.SimpleStreamOptions) (*types.AssistantMessage, error)
}

func newSummaryMock(t *testing.T, responses ...*types.AssistantMessage) *summaryMock {
	t.Helper()
	mock := &summaryMock{responses: responses, originalFn: completeSimpleFallback}
	t.Cleanup(func() { completeSimpleFallback = mock.originalFn })
	completeSimpleFallback = func(ctx context.Context, model *ai.Model, llmContext *ai.Context, options *ai.SimpleStreamOptions) (*types.AssistantMessage, error) {
		mock.calls = append(mock.calls, options)
		mock.contexts = append(mock.contexts, llmContext)
		var response *types.AssistantMessage
		if len(mock.responses) > 0 {
			response = mock.responses[0]
			mock.responses = mock.responses[1:]
		} else {
			response = mock.responses[len(mock.responses)-1]
		}
		// The final options the request would go out with already carry the
		// fresh routing session assigned by CompleteSummarization.
		mock.sessionIDs = append(mock.sessionIDs, options.SessionID)
		return response, nil
	}
	return mock
}

func createTestModel(reasoning bool, maxTokens int64) *ai.Model {
	id := "non-reasoning-model"
	name := "Non-reasoning Model"
	if reasoning {
		id = "reasoning-model"
		name = "Reasoning Model"
	}
	return &ai.Model{
		ID:            id,
		Name:          name,
		API:           "anthropic-messages",
		Provider:      "anthropic",
		BaseURL:       "https://api.anthropic.com",
		Reasoning:     reasoning,
		Input:         []types.ModelInput{types.ModelInputText},
		Cost:          ai.ModelCost{},
		ContextWindow: 200000,
		MaxTokens:     maxTokens,
	}
}

var mockSummaryResponse = types.AssistantMessage{
	Role:       types.RoleAssistant,
	Content:    []types.Content{types.NewTextContent("## Goal\nTest summary")},
	API:        "anthropic-messages",
	Provider:   "anthropic",
	Model:      "claude-sonnet-4-5",
	Usage:      createMockUsage(10, 10, 0, 0),
	StopReason: types.StopReasonStop,
	Timestamp:  timeNowMillis(),
}

var mockToolCallResponse = func() types.AssistantMessage {
	msg := mockSummaryResponse
	msg.Content = []types.Content{types.ToolCall{
		Kind:      types.KindToolCall,
		ID:        "tool-call-1",
		Name:      "read",
		Arguments: map[string]any{"path": "README.md"},
	}}
	msg.StopReason = types.StopReasonToolUse
	return msg
}()

var summarizationMessages = []agent.AgentMessage{
	createUserMessage("Summarize this."),
}

func TestUsesTheProvidedThinkingLevelForReasoningCapableModels(t *testing.T) {
	mock := newSummaryMock(t, &mockSummaryResponse)

	result, err := GenerateSummaryWithUsage(
		t.Context(),
		summarizationMessages,
		createTestModel(true, 8192),
		2000,
		"test-key",
		nil,
		"", // customInstructions
		"", // previousSummary
		"medium",
		nil, // streamFn
		nil, // env
		nil, // retry
		nil, // callbacks
		"",  // sessionId
	)
	if err != nil {
		t.Fatal(err)
	}

	if result.Text != "## Goal\nTest summary" {
		t.Fatalf("unexpected text: %q", result.Text)
	}
	if result.Usage == nil || result.Usage.Input != 10 || result.Usage.Output != 10 {
		t.Fatalf("unexpected usage: %+v", result.Usage)
	}

	if len(mock.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(mock.calls))
	}
	options := mock.calls[0]
	if options.Reasoning != "medium" {
		t.Fatalf("expected reasoning medium, got %q", options.Reasoning)
	}
	if options.APIKey != "test-key" {
		t.Fatalf("expected apiKey test-key, got %q", options.APIKey)
	}
}

func TestPreservesTheStringResultFromGenerateSummary(t *testing.T) {
	newSummaryMock(t, &mockSummaryResponse)

	text, err := GenerateSummary(
		t.Context(),
		summarizationMessages,
		createTestModel(false, 8192),
		2000,
		"test-key",
		nil, "", "", "", nil, nil, nil, nil, "",
	)
	if err != nil {
		t.Fatal(err)
	}
	if text != "## Goal\nTest summary" {
		t.Fatalf("unexpected text: %q", text)
	}
}

func TestUsesFreshRoutingSessionsWithoutPromptCaching(t *testing.T) {
	mock := newSummaryMock(t, &mockSummaryResponse, &mockSummaryResponse)

	for i := 0; i < 2; i++ {
		if _, err := GenerateSummary(
			t.Context(),
			summarizationMessages,
			createTestModel(false, 8192),
			2000,
			"test-key",
			nil, "", "", "", nil, nil, nil, nil, "",
		); err != nil {
			t.Fatal(err)
		}
	}

	if len(mock.calls) != 2 {
		t.Fatalf("expected 2 calls, got %d", len(mock.calls))
	}
	for i, options := range mock.calls {
		if options.CacheRetention != types.CacheRetentionNone {
			t.Fatalf("call %d: expected cacheRetention none, got %q", i, options.CacheRetention)
		}
		if options.ToolChoice != types.ToolChoiceNone {
			t.Fatalf("call %d: expected toolChoice none, got %q", i, options.ToolChoice)
		}
	}
	if mock.sessionIDs[0] == mock.sessionIDs[1] {
		t.Fatal("expected distinct routing session IDs")
	}
}

func TestHonorsACallerSuppliedRoutingSessionWithoutPromptCaching(t *testing.T) {
	mock := newSummaryMock(t, &mockSummaryResponse)

	options := &ai.SimpleStreamOptions{}
	options.SessionID = "current-routing-session"
	options.CacheRetention = types.CacheRetentionLong
	options.ToolChoice = types.ToolChoiceAuto

	_, err := CompleteSummarization(
		t.Context(),
		createTestModel(false, 8192),
		&ai.Context{SystemPrompt: "Summarize"},
		options,
		nil, nil, nil,
	)
	if err != nil {
		t.Fatal(err)
	}

	got := mock.calls[0]
	if got.SessionID != "current-routing-session" {
		t.Fatalf("expected session to be preserved, got %q", got.SessionID)
	}
	if got.CacheRetention != types.CacheRetentionNone {
		t.Fatalf("expected cacheRetention none, got %q", got.CacheRetention)
	}
	if got.ToolChoice != types.ToolChoiceNone {
		t.Fatalf("expected toolChoice none, got %q", got.ToolChoice)
	}
}

func TestPreservesTheStandaloneSplitTurnSummaryPrompt(t *testing.T) {
	mock := newSummaryMock(t, &mockSummaryResponse)

	preparation := &CompactionPreparation{
		FirstKeptEntryID:    "entry-keep",
		MessagesToSummarize: nil,
		TurnPrefixMessages:  summarizationMessages,
		IsSplitTurn:         true,
		TokensBefore:        100,
		FileOps:             CreateFileOps(),
		Settings:            CompactionSettingsValues{Enabled: true, ReserveTokens: 2000, KeepRecentTokens: 20},
	}

	if _, err := Compact(t.Context(), preparation, createTestModel(false, 8192), "test-key", nil, "", "", nil, nil, nil, nil, ""); err != nil {
		t.Fatal(err)
	}

	if len(mock.contexts) == 0 || len(mock.contexts[0].Messages) == 0 {
		t.Fatal("expected captured request context")
	}
	userMsg, ok := mock.contexts[0].Messages[0].(types.UserMessage)
	if !ok {
		t.Fatalf("expected user message, got %T", mock.contexts[0].Messages[0])
	}
	prompt := ai.ContentText(userMsg.Content.Parts, "")
	if !strings.Contains(prompt, "This is the PREFIX of a turn that was too large to keep") {
		t.Fatalf("missing turn prefix prompt: %s", prompt)
	}
	if !strings.Contains(prompt, "<conversation>") {
		t.Fatalf("missing conversation tags: %s", prompt)
	}
}

func TestRejectsToolCallsFromConversationSummaries(t *testing.T) {
	newSummaryMock(t, &mockToolCallResponse)

	_, err := GenerateSummaryWithUsage(
		t.Context(),
		summarizationMessages,
		createTestModel(false, 8192),
		2000,
		"test-key",
		nil, "", "", "", nil, nil, nil, nil, "",
	)
	if err == nil || err.Error() != "Summarization attempted to call a tool" {
		t.Fatalf("expected tool call rejection, got %v", err)
	}
}

func TestRejectsToolCallsFromSplitTurnSummaries(t *testing.T) {
	newSummaryMock(t, &mockToolCallResponse)

	preparation := &CompactionPreparation{
		FirstKeptEntryID:    "entry-keep",
		MessagesToSummarize: nil,
		TurnPrefixMessages:  summarizationMessages,
		IsSplitTurn:         true,
		TokensBefore:        100,
		FileOps:             CreateFileOps(),
		Settings:            CompactionSettingsValues{Enabled: true, ReserveTokens: 2000, KeepRecentTokens: 20},
	}

	_, err := Compact(t.Context(), preparation, createTestModel(false, 8192), "test-key", nil, "", "", nil, nil, nil, nil, "")
	if err == nil || err.Error() != "Turn prefix summarization attempted to call a tool" {
		t.Fatalf("expected turn prefix tool call rejection, got %v", err)
	}
}

func TestDoesNotSetReasoningWhenThinkingIsOff(t *testing.T) {
	mock := newSummaryMock(t, &mockSummaryResponse)

	_, err := GenerateSummary(
		t.Context(),
		summarizationMessages,
		createTestModel(true, 8192),
		2000,
		"test-key",
		nil, "", "", "off", nil, nil, nil, nil, "",
	)
	if err != nil {
		t.Fatal(err)
	}

	if len(mock.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(mock.calls))
	}
	options := mock.calls[0]
	if options.APIKey != "test-key" {
		t.Fatalf("expected apiKey test-key, got %q", options.APIKey)
	}
	if options.Reasoning != "" {
		t.Fatalf("expected no reasoning, got %q", options.Reasoning)
	}
}

func TestDoesNotSetReasoningForNonReasoningModels(t *testing.T) {
	mock := newSummaryMock(t, &mockSummaryResponse)

	_, err := GenerateSummary(
		t.Context(),
		summarizationMessages,
		createTestModel(false, 8192),
		2000,
		"test-key",
		nil, "", "", "medium", nil, nil, nil, nil, "",
	)
	if err != nil {
		t.Fatal(err)
	}

	if len(mock.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(mock.calls))
	}
	options := mock.calls[0]
	if options.APIKey != "test-key" {
		t.Fatalf("expected apiKey test-key, got %q", options.APIKey)
	}
	if options.Reasoning != "" {
		t.Fatalf("expected no reasoning, got %q", options.Reasoning)
	}
}

func TestLeavesAnthropicRefusalFallbackHandlingToPiAiModelMetadata(t *testing.T) {
	mock := newSummaryMock(t, &mockSummaryResponse)

	// pi passes a model whose compat carries allowedFallbackModels; pig's
	// Model compat is decoded data with no request-side effect in this path,
	// so the plain reasoning model fixture is equivalent here.
	_, err := GenerateSummary(
		t.Context(),
		summarizationMessages,
		createTestModel(true, 8192),
		2000,
		"test-key",
		nil, "", "", "", nil, nil, nil, nil, "",
	)
	if err != nil {
		t.Fatal(err)
	}

	if len(mock.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(mock.calls))
	}
	// pi asserts the request options carry no refusalFallbacks member; pig's
	// options type has no such field, so absence holds trivially.
}

func TestDoesNotSetAnthropicRefusalFallbackForModelsWithoutAllowedFallbackTargets(t *testing.T) {
	mock := newSummaryMock(t, &mockSummaryResponse)

	_, err := GenerateSummary(
		t.Context(),
		summarizationMessages,
		createTestModel(true, 8192),
		2000,
		"test-key",
		nil, "", "", "", nil, nil, nil, nil, "",
	)
	if err != nil {
		t.Fatal(err)
	}

	if len(mock.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(mock.calls))
	}
}

func TestClampsCompactionSummaryMaxTokensToTheModelOutputCap(t *testing.T) {
	mock := newSummaryMock(t, &mockSummaryResponse, &mockSummaryResponse)

	preparation := &CompactionPreparation{
		FirstKeptEntryID:    "entry-keep",
		MessagesToSummarize: summarizationMessages,
		TurnPrefixMessages:  summarizationMessages,
		IsSplitTurn:         true,
		TokensBefore:        600000,
		FileOps:             CreateFileOps(),
		Settings:            CompactionSettingsValues{Enabled: true, ReserveTokens: 500000, KeepRecentTokens: 20000},
	}

	result, err := Compact(t.Context(), preparation, createTestModel(false, 128000), "test-key", nil, "", "", nil, nil, nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}

	// Combined usage: two mock responses of 10/10 merge to 20/20/40.
	if result.Usage == nil || result.Usage.Input != 20 || result.Usage.Output != 20 || result.Usage.TotalTokens != 40 {
		t.Fatalf("unexpected usage: %+v", result.Usage)
	}
	if len(mock.calls) != 2 {
		t.Fatalf("expected 2 calls, got %d", len(mock.calls))
	}
	for i, options := range mock.calls {
		if options.MaxTokens == nil || *options.MaxTokens != 128000 {
			t.Fatalf("call %d: expected maxTokens 128000, got %v", i, options.MaxTokens)
		}
	}
}
