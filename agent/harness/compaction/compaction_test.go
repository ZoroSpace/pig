// pi: test/harness/compaction.test.ts — compaction helpers.
//
// Deviations:
//   - pi's fauxProvider (pi-ai test helper) is replaced by a stub ai.Models
//     recording CompleteSimple options and contexts; the assertions over
//     captured request options carry over unchanged.
//   - getOrThrow collapses to t.Fatal on error (Result → (T, error)).
package compaction

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/samber/lo"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/agent/harness"
	"github.com/ZoroSpace/pig/agent/harness/session"
	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/types"
)

var nextID = 0

func createID() string {
	nextID++
	return fmt.Sprintf("entry-%d", nextID-1)
}

func createMockUsage(input, output, cacheRead, cacheWrite int64) types.Usage {
	return types.Usage{
		Input:       input,
		Output:      output,
		CacheRead:   cacheRead,
		CacheWrite:  cacheWrite,
		TotalTokens: input + output + cacheRead + cacheWrite,
		Cost:        types.Cost{},
	}
}

func createUserMessage(text string) types.UserMessage {
	return types.UserMessage{
		Role:      types.RoleUser,
		Content:   types.UserContent{Parts: []types.Content{types.NewTextContent(text)}},
		Timestamp: 1700000000000,
	}
}

func createAssistantMessage(text string, usage types.Usage) types.AssistantMessage {
	return types.AssistantMessage{
		Role:       types.RoleAssistant,
		Content:    []types.Content{types.NewTextContent(text)},
		API:        ai.ApiAnthropicMessages,
		Provider:   ai.ProviderAnthropic,
		Model:      "claude-sonnet-4-5",
		Usage:      usage,
		StopReason: types.StopReasonStop,
		Timestamp:  1700000000000,
	}
}

func createMessageEntry(message agent.AgentMessage, parentID *string) *session.MessageEntry {
	nextID++
	return &session.MessageEntry{
		EntryBase: session.EntryBase{
			ID:        createID(),
			ParentID:  parentID,
			Seq:       int64(nextID - 1),
			Timestamp: 1700000000000,
		},
		Message: message,
	}
}

func createCompactionEntry(summary string, parentID *string, retainedTail []agent.AgentMessage) *session.CompactionEntry {
	nextID++
	return &session.CompactionEntry{
		EntryBase: session.EntryBase{
			ID:        createID(),
			ParentID:  parentID,
			Seq:       int64(nextID - 1),
			Timestamp: 1700000000000,
		},
		Summary:      summary,
		TokensBefore: 1234,
		RetainedTail: retainedTail,
	}
}

func createNonCutPointEntry(parentID *string) *session.CustomEntry {
	nextID++
	customType := "note"
	return &session.CustomEntry{
		EntryBase: session.EntryBase{
			ID:         createID(),
			ParentID:   parentID,
			Seq:        int64(nextID - 1),
			Timestamp:  1700000000000,
			Type:       session.EntryTypeCustom,
			CustomType: &customType,
		},
	}
}

// stubModels stands in for pi's fauxProvider-backed Models.
type stubModels struct {
	ai.Models
	responses []*types.AssistantMessage
	seen      []ai.ModelsSimpleStreamOptions
	contexts  []*ai.Context
}

func (s *stubModels) CompleteSimple(ctx context.Context, model *ai.Model, llmContext *ai.Context, options *ai.ModelsSimpleStreamOptions) (*types.AssistantMessage, error) {
	if options != nil {
		s.seen = append(s.seen, *options)
	}
	s.contexts = append(s.contexts, llmContext)
	if len(s.responses) == 0 {
		return nil, fmt.Errorf("No faux completeSimple response queued")
	}
	response := s.responses[0]
	s.responses = s.responses[1:]
	return response, nil
}

func createStubModel(reasoning bool, maxTokens int64) *ai.Model {
	return &ai.Model{
		ID:            map[bool]string{true: "reasoning-model", false: "non-reasoning-model"}[reasoning],
		Reasoning:     reasoning,
		ContextWindow: 200000,
		MaxTokens:     maxTokens,
	}
}

func fauxAssistantMessage(text string) *types.AssistantMessage {
	msg := createAssistantMessage(text, createMockUsage(10, 5, 0, 0))
	return &msg
}

func TestCalculatesTotalContextTokensFromUsage(t *testing.T) {
	usage := createMockUsage(1000, 500, 200, 100)
	if got := CalculateContextTokens(&usage); got != 1800 {
		t.Fatalf("expected 1800, got %d", got)
	}
	zero := createMockUsage(0, 0, 0, 0)
	if got := CalculateContextTokens(&zero); got != 0 {
		t.Fatalf("expected 0, got %d", got)
	}
}

func TestChecksCompactionThreshold(t *testing.T) {
	settings := CompactionSettings{Enabled: true, ReserveTokens: 10000, KeepRecentTokens: 20000}
	if !ShouldCompact(95000, 100000, settings) {
		t.Fatal("expected shouldCompact=true at 95000")
	}
	if ShouldCompact(89000, 100000, settings) {
		t.Fatal("expected shouldCompact=false at 89000")
	}
	disabled := settings
	disabled.Enabled = false
	if ShouldCompact(95000, 100000, disabled) {
		t.Fatal("expected shouldCompact=false when disabled")
	}
}

func TestFindsACutPointBasedOnTokenDifferences(t *testing.T) {
	var entries []session.Entry
	var parentID *string
	for i := 0; i < 10; i++ {
		user := createMessageEntry(createUserMessage(fmt.Sprintf("User %d", i)), parentID)
		entries = append(entries, user)
		assistant := createMessageEntry(
			createAssistantMessage(fmt.Sprintf("Assistant %d", i), createMockUsage(0, 100, int64((i+1)*1000), 0)),
			lo.ToPtr(user.ID),
		)
		entries = append(entries, assistant)
		parentID = lo.ToPtr(assistant.ID)
	}

	result := FindCutPoint(entries, 0, len(entries), 2500)
	if result.FirstKeptEntryIndex >= len(entries) {
		t.Fatalf("index out of range: %d", result.FirstKeptEntryIndex)
	}
	if _, ok := entries[result.FirstKeptEntryIndex].(*session.MessageEntry); !ok {
		t.Fatalf("expected a message entry at the cut, got %T", entries[result.FirstKeptEntryIndex])
	}
}

func TestCoversCutPointAndTurnStartEdgeCases(t *testing.T) {
	filler1 := createNonCutPointEntry(nil)
	filler2 := createNonCutPointEntry(lo.ToPtr(filler1.ID))
	result := FindCutPoint([]session.Entry{filler1, filler2}, 0, 2, 1)
	if result.FirstKeptEntryIndex != 0 || result.TurnStartIndex != -1 || result.IsSplitTurn {
		t.Fatalf("unexpected result: %+v", result)
	}

	branchSummary := &session.BranchSummaryEntry{
		EntryBase: session.EntryBase{
			ID:        createID(),
			ParentID:  lo.ToPtr(filler2.ID),
			Seq:       int64(nextID),
			Timestamp: 1700000000000,
		},
		FromID:  lo.ToPtr("branch"),
		Summary: "branch summary",
	}
	if got := FindTurnStartIndex([]session.Entry{filler1, branchSummary}, 1, 0); got != 1 {
		t.Fatalf("expected turn start 1, got %d", got)
	}
	if got := FindTurnStartIndex([]session.Entry{filler1, filler2}, 1, 0); got != -1 {
		t.Fatalf("expected turn start -1, got %d", got)
	}

	result = FindCutPoint([]session.Entry{filler1, branchSummary}, 0, 2, 1)
	if result.FirstKeptEntryIndex != 0 {
		t.Fatalf("expected first kept 0, got %d", result.FirstKeptEntryIndex)
	}

	toolResult := createMessageEntry(types.ToolResultMessage{
		Role:       types.RoleToolResult,
		ToolCallID: "call-1",
		ToolName:   "read",
		Content:    []types.Content{types.NewTextContent("tool output")},
		Timestamp:  1700000000000,
	}, nil)
	result = FindCutPoint([]session.Entry{toolResult}, 0, 1, 1)
	if result.FirstKeptEntryIndex != 0 || result.TurnStartIndex != -1 || result.IsSplitTurn {
		t.Fatalf("unexpected result: %+v", result)
	}

	user := createMessageEntry(createUserMessage("user"), nil)
	compactionEntry := createCompactionEntry("summary", lo.ToPtr(user.ID), nil)
	assistant := createMessageEntry(createAssistantMessage("assistant", createMockUsage(100, 50, 0, 0)), lo.ToPtr(compactionEntry.ID))
	if got := FindCutPoint([]session.Entry{user, compactionEntry, assistant}, 0, 3, 1).FirstKeptEntryIndex; got != 2 {
		t.Fatalf("expected first kept 2, got %d", got)
	}
}

func TestEstimatesTokensAndContextUsageAcrossSupportedMessageRoles(t *testing.T) {
	usage := createMockUsage(10, 5, 3, 2)
	assistant := createAssistantMessage("assistant", usage)
	assistantWithThinkingAndTool := assistant
	assistantWithThinkingAndTool.Content = []types.Content{
		types.ThinkingContent{Kind: types.KindThinking, Thinking: "thinking"},
		types.ToolCall{Kind: types.KindToolCall, ID: "call-1", Name: "read", Arguments: map[string]any{"path": "file.ts"}},
	}
	customString := harness.CustomMessage{
		Role:       harness.RoleCustomMessage,
		CustomType: "note",
		Content:    "custom text",
		Display:    true,
		Timestamp:  1700000000000,
	}
	toolResultWithImage := types.ToolResultMessage{
		Role:       types.RoleToolResult,
		ToolCallID: "call-1",
		ToolName:   "read",
		Content: []types.Content{
			types.NewTextContent("tool text"),
			types.ImageContent{Kind: types.KindImage, MimeType: "image/png", Data: "abc"},
		},
		Timestamp: 1700000000000,
	}
	bashExecution := harness.BashExecutionMessage{
		Role:      harness.RoleBashExecution,
		Command:   "npm run check",
		Output:    "ok",
		ExitCode:  lo.ToPtr(0),
		Timestamp: 1700000000000,
	}
	branchSummaryMessage := harness.BranchSummaryMessage{
		Role:      harness.RoleBranchSummary,
		Summary:   "branch",
		FromID:    lo.ToPtr("x"),
		Timestamp: 1700000000000,
	}
	compactionSummaryMessage := harness.CompactionSummaryMessage{
		Role:         harness.RoleCompactionSummary,
		Summary:      "compact",
		TokensBefore: 123,
		Timestamp:    1700000000000,
	}

	plainUser := createUserMessage("plain user")
	plainUser.Content = types.UserContent{String: "plain user"}
	if got := EstimateTokens(plainUser); got <= 0 {
		t.Fatalf("expected positive estimate, got %d", got)
	}
	if got := EstimateTokens(assistantWithThinkingAndTool); got <= 0 {
		t.Fatalf("expected positive estimate, got %d", got)
	}
	if got := EstimateTokens(customString); got <= 0 {
		t.Fatalf("expected positive estimate, got %d", got)
	}
	if got := EstimateTokens(toolResultWithImage); got <= 1000 {
		t.Fatalf("expected estimate > 1000, got %d", got)
	}
	if got := EstimateTokens(bashExecution); got <= 0 {
		t.Fatalf("expected positive estimate, got %d", got)
	}
	if got := EstimateTokens(branchSummaryMessage); got <= 0 {
		t.Fatalf("expected positive estimate, got %d", got)
	}
	if got := EstimateTokens(compactionSummaryMessage); got <= 0 {
		t.Fatalf("expected positive estimate, got %d", got)
	}
	if got := EstimateTokens(unknownMessage{}); got != 0 {
		t.Fatalf("expected 0 for unknown role, got %d", got)
	}
	if got := GetLastAssistantUsage([]session.Entry{
		createMessageEntry(createUserMessage("user"), nil),
		createMessageEntry(assistant, nil),
	}); got == nil || *got != usage {
		t.Fatalf("expected the assistant usage, got %+v", got)
	}
	aborted := assistant
	aborted.StopReason = types.StopReasonAborted
	errored := assistant
	errored.StopReason = types.StopReasonError
	if got := GetLastAssistantUsage([]session.Entry{
		createMessageEntry(aborted, nil),
		createMessageEntry(errored, nil),
	}); got != nil {
		t.Fatalf("expected no usage from failed messages, got %+v", got)
	}
	partial := createAssistantMessage("partial", createMockUsage(0, 0, 0, 0))
	if got := GetLastAssistantUsage([]session.Entry{
		createMessageEntry(createUserMessage("user"), nil),
		createMessageEntry(assistant, nil),
		createMessageEntry(partial, nil),
	}); got == nil || *got != usage {
		t.Fatalf("expected the first assistant usage, got %+v", got)
	}
	if got := EstimateContextTokens([]agent.AgentMessage{createUserMessage("no usage")}).LastUsageIndex; got != nil {
		t.Fatalf("expected nil lastUsageIndex, got %v", *got)
	}
	estimate := EstimateContextTokens([]agent.AgentMessage{assistant, createUserMessage("tail")})
	if estimate.UsageTokens != 20 || estimate.LastUsageIndex == nil || *estimate.LastUsageIndex != 0 {
		t.Fatalf("unexpected estimate: %+v", estimate)
	}
	estimate = EstimateContextTokens([]agent.AgentMessage{
		createUserMessage("Hello"),
		assistant,
		createUserMessage("continue"),
		createAssistantMessage("Partial thinking", createMockUsage(0, 0, 0, 0)),
	})
	if estimate.UsageTokens != 20 || estimate.LastUsageIndex == nil || *estimate.LastUsageIndex != 1 {
		t.Fatalf("unexpected estimate: %+v", estimate)
	}
	if estimate.TrailingTokens <= 0 {
		t.Fatalf("expected trailing tokens > 0, got %d", estimate.TrailingTokens)
	}
	if estimate.Tokens != 20+estimate.TrailingTokens {
		t.Fatalf("tokens mismatch: %d != %d + %d", estimate.Tokens, 20, estimate.TrailingTokens)
	}
}

// unknownMessage models pi's `{ role: "unknown" }` probe.
type unknownMessage struct{}

func (unknownMessage) MessageRole() types.Role { return "unknown" }

func TestPreparesCompactionUsingTheLatestCompactionSummaryAsPreviousSummary(t *testing.T) {
	u1 := createMessageEntry(createUserMessage("user msg 1"), nil)
	a1 := createMessageEntry(createAssistantMessage("assistant msg 1", createMockUsage(100, 50, 0, 0)), lo.ToPtr(u1.ID))
	u2 := createMessageEntry(createUserMessage("user msg 2"), lo.ToPtr(a1.ID))
	a2 := createMessageEntry(createAssistantMessage("assistant msg 2", createMockUsage(5000, 1000, 0, 0)), lo.ToPtr(u2.ID))
	compaction1 := createCompactionEntry("First summary", lo.ToPtr(a2.ID), nil)
	u3 := createMessageEntry(createUserMessage("user msg 3"), lo.ToPtr(compaction1.ID))
	a3 := createMessageEntry(createAssistantMessage("assistant msg 3", createMockUsage(8000, 2000, 0, 0)), lo.ToPtr(u3.ID))
	pathEntries := []session.Entry{u1, a1, u2, a2, compaction1, u3, a3}
	preparation, err := PrepareCompaction(pathEntries, DefaultCompactionSettings)
	if err != nil || preparation == nil {
		t.Fatalf("expected a preparation, got %v, %v", preparation, err)
	}
	if preparation.PreviousSummary != "First summary" {
		t.Fatalf("expected previous summary %q, got %q", "First summary", preparation.PreviousSummary)
	}
	if len(preparation.RetainedTail) == 0 {
		t.Fatal("expected a non-empty retained tail")
	}
	want := EstimateContextTokens(mustBuildContextMessages(t, pathEntries)).Tokens
	if preparation.TokensBefore != want {
		t.Fatalf("tokensBefore %d != %d", preparation.TokensBefore, want)
	}
}

func TestCarriesAPreviousCompactionsRetainedTailIntoTheNextPreparation(t *testing.T) {
	retainedUser := createUserMessage("retained user")
	retainedAssistant := createAssistantMessage("retained assistant", createMockUsage(100, 50, 0, 0))
	compactionEntry := createCompactionEntry("previous summary", nil, []agent.AgentMessage{retainedUser, &retainedAssistant})
	user := createMessageEntry(createUserMessage("new user"), lo.ToPtr(compactionEntry.ID))
	assistant := createMessageEntry(createAssistantMessage("new assistant", createMockUsage(100, 50, 0, 0)), lo.ToPtr(user.ID))

	preparation, err := PrepareCompaction([]session.Entry{compactionEntry, user, assistant}, CompactionSettings{
		Enabled:          true,
		ReserveTokens:    100,
		KeepRecentTokens: 1,
	})
	if err != nil || preparation == nil {
		t.Fatalf("expected a preparation, got %v, %v", preparation, err)
	}
	if preparation.PreviousSummary != "previous summary" {
		t.Fatalf("unexpected previous summary: %q", preparation.PreviousSummary)
	}
	var combined []agent.AgentMessage
	combined = append(combined, preparation.MessagesToSummarize...)
	combined = append(combined, preparation.TurnPrefixMessages...)
	combined = append(combined, preparation.RetainedTail...)
	want := []agent.AgentMessage{retainedUser, &retainedAssistant, user.Message, assistant.Message}
	if len(combined) != len(want) {
		t.Fatalf("expected %d messages, got %d", len(want), len(combined))
	}
	for i := range want {
		// Interface == panics on message values with slice members (UserContent.Parts).
		if !reflect.DeepEqual(combined[i], want[i]) {
			t.Fatalf("message %d mismatch: %#v != %#v", i, combined[i], want[i])
		}
	}
}

func TestPreparesSplitTurnCompactionWithPriorFileOperationDetails(t *testing.T) {
	u1 := createMessageEntry(createUserMessage("user msg 1"), nil)
	assistantMessage := createAssistantMessage("assistant msg 1", createMockUsage(100, 50, 0, 0))
	assistantMessage.Content = []types.Content{
		types.ToolCall{Kind: types.KindToolCall, ID: "tool-1", Name: "write", Arguments: map[string]any{"path": "written.ts"}},
	}
	a1 := createMessageEntry(assistantMessage, lo.ToPtr(u1.ID))
	compaction1 := createCompactionEntry("First summary", lo.ToPtr(a1.ID), nil)
	compaction1.Details = CompactionDetails{ReadFiles: []string{"old-read.ts"}, ModifiedFiles: []string{"old-edit.ts", "written.ts"}}
	u2 := createMessageEntry(createUserMessage("large turn"), lo.ToPtr(compaction1.ID))
	a2 := createMessageEntry(createAssistantMessage("large assistant message", createMockUsage(100, 50, 0, 0)), lo.ToPtr(u2.ID))
	preparation, err := PrepareCompaction([]session.Entry{u1, a1, compaction1, u2, a2}, CompactionSettings{
		Enabled:          true,
		ReserveTokens:    100,
		KeepRecentTokens: 1,
	})
	if err != nil || preparation == nil {
		t.Fatalf("expected a preparation, got %v, %v", preparation, err)
	}
	if preparation.PreviousSummary != "First summary" || !preparation.IsSplitTurn {
		t.Fatalf("unexpected preparation: %+v", preparation)
	}
	if len(preparation.TurnPrefixMessages) != 1 || preparation.TurnPrefixMessages[0].MessageRole() != types.RoleUser {
		t.Fatalf("unexpected turn prefix: %+v", preparation.TurnPrefixMessages)
	}
	if !preparation.FileOps.Read["old-read.ts"] {
		t.Fatal("expected old-read.ts in read files")
	}
	if !preparation.FileOps.Edited["old-edit.ts"] || !preparation.FileOps.Edited["written.ts"] {
		t.Fatal("expected old-edit.ts and written.ts in edited files")
	}
}

func TestDoesNotPrepareCompactionWhenThereIsNothingValidToCompact(t *testing.T) {
	compactionEntry := createCompactionEntry("already compacted", nil, nil)
	preparation, err := PrepareCompaction([]session.Entry{compactionEntry}, DefaultCompactionSettings)
	if err != nil || preparation != nil {
		t.Fatalf("expected no preparation, got %v, %v", preparation, err)
	}
	preparation, err = PrepareCompaction(nil, DefaultCompactionSettings)
	if err != nil || preparation != nil {
		t.Fatalf("expected no preparation, got %v, %v", preparation, err)
	}
}

func TestSerializesConversationWithTruncatedToolResults(t *testing.T) {
	longContent := strings.Repeat("x", 5000)
	messages := harness.ConvertToLlm([]agent.AgentMessage{
		types.ToolResultMessage{
			Role:       types.RoleToolResult,
			ToolCallID: "tc1",
			ToolName:   "read",
			Content:    []types.Content{types.NewTextContent(longContent)},
			Timestamp:  1700000000000,
		},
	})
	result := SerializeConversation(messages)
	if !strings.Contains(result, "[Tool result]:") {
		t.Fatal("missing tool result marker")
	}
	if !strings.Contains(result, "[... 3000 more characters truncated]") {
		t.Fatal("missing truncation marker")
	}
}

func TestPassesReasoningThroughGenerateSummaryOnlyForReasoningModelsWithThinkingEnabled(t *testing.T) {
	messages := []agent.AgentMessage{createUserMessage("Summarize this.")}

	stub := &stubModels{responses: []*types.AssistantMessage{fauxAssistantMessage("## Goal\nTest summary")}}
	reasoningModel := createStubModel(true, 8192)
	if _, err := GenerateSummary(context.Background(), messages, stub, reasoningModel, 2000, "", "", types.ModelThinkingMedium, nil, nil); err != nil {
		t.Fatal(err)
	}
	if len(stub.seen) != 1 || stub.seen[0].Reasoning != types.ThinkingLevelMedium {
		t.Fatalf("expected reasoning=medium, got %+v", stub.seen)
	}

	stub = &stubModels{responses: []*types.AssistantMessage{fauxAssistantMessage("## Goal\nTest summary")}}
	if _, err := GenerateSummary(context.Background(), messages, stub, reasoningModel, 2000, "", "", types.ModelThinkingOff, nil, nil); err != nil {
		t.Fatal(err)
	}
	if len(stub.seen) != 1 || stub.seen[0].Reasoning != "" {
		t.Fatalf("expected no reasoning for off, got %+v", stub.seen)
	}

	stub = &stubModels{responses: []*types.AssistantMessage{fauxAssistantMessage("## Goal\nTest summary")}}
	nonReasoningModel := createStubModel(false, 8192)
	if _, err := GenerateSummary(context.Background(), messages, stub, nonReasoningModel, 2000, "", "", types.ModelThinkingMedium, nil, nil); err != nil {
		t.Fatal(err)
	}
	if len(stub.seen) != 1 || stub.seen[0].Reasoning != "" {
		t.Fatalf("expected no reasoning for non-reasoning model, got %+v", stub.seen)
	}
}

func TestIncludesPreviousSummariesAndCustomInstructionsInGenerateSummaryPrompts(t *testing.T) {
	messages := []agent.AgentMessage{createUserMessage("Summarize this.")}
	stub := &stubModels{responses: []*types.AssistantMessage{fauxAssistantMessage("## Goal\nTest summary")}}
	model := createStubModel(false, 8192)

	summary, err := GenerateSummaryWithUsage(context.Background(), messages, stub, model, 2000, "focus", "old summary", "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(summary.Text, "Test summary") {
		t.Fatalf("unexpected summary text: %q", summary.Text)
	}
	if summary.Usage.Input <= 0 || summary.Usage.Output <= 0 {
		t.Fatalf("expected usage, got %+v", summary.Usage)
	}
	if summary.Usage.TotalTokens != summary.Usage.Input+summary.Usage.Output+summary.Usage.CacheRead+summary.Usage.CacheWrite {
		t.Fatalf("totalTokens mismatch: %+v", summary.Usage)
	}
	if len(stub.seen) != 1 {
		t.Fatalf("expected one call, got %d", len(stub.seen))
	}
	promptText := capturedPromptText(t, stub)
	if !strings.Contains(promptText, "<previous-summary>\nold summary\n</previous-summary>") {
		t.Fatalf("missing previous summary in prompt: %q", promptText)
	}
	if !strings.Contains(promptText, "Additional focus: focus") {
		t.Fatalf("missing custom instructions in prompt: %q", promptText)
	}
}

func capturedPromptText(t *testing.T, stub *stubModels) string {
	t.Helper()
	message, ok := stub.contexts[0].Messages[0].(types.UserMessage)
	if !ok || message.Content.Parts == nil {
		t.Fatal("expected a user text message in the llm context")
	}
	text, ok := message.Content.Parts[0].(types.TextContent)
	if !ok {
		t.Fatal("expected a text part")
	}
	return text.Text
}

func TestPreservesTheStringResultFromGenerateSummary(t *testing.T) {
	messages := []agent.AgentMessage{createUserMessage("Summarize this.")}
	stub := &stubModels{responses: []*types.AssistantMessage{fauxAssistantMessage("## Goal\nTest summary")}}
	model := createStubModel(false, 8192)

	summary, err := GenerateSummary(context.Background(), messages, stub, model, 2000, "", "", "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if summary != "## Goal\nTest summary" {
		t.Fatalf("unexpected summary: %q", summary)
	}
}

func TestReturnsErrorResultsForFailedOrAbortedSummaryGenerations(t *testing.T) {
	messages := []agent.AgentMessage{createUserMessage("Summarize this.")}
	model := createStubModel(false, 8192)

	errorResponse := fauxAssistantMessage("")
	errorResponse.StopReason = types.StopReasonError
	errorResponse.ErrorMessage = "boom"
	stub := &stubModels{responses: []*types.AssistantMessage{errorResponse}}
	_, err := GenerateSummary(context.Background(), messages, stub, model, 2000, "", "", "", nil, nil)
	var compactionErr *harness.CompactionError
	if !errors.As(err, &compactionErr) {
		t.Fatalf("expected CompactionError, got %v", err)
	}
	if compactionErr.Code != harness.CompactionCodeSummarizationFailed || compactionErr.Error() != "Summarization failed: boom" {
		t.Fatalf("unexpected error: %v", compactionErr)
	}

	abortedResponse := fauxAssistantMessage("")
	abortedResponse.StopReason = types.StopReasonAborted
	abortedResponse.ErrorMessage = "stopped"
	stub = &stubModels{responses: []*types.AssistantMessage{abortedResponse}}
	_, err = GenerateSummary(context.Background(), messages, stub, model, 2000, "", "", "", nil, nil)
	if !errors.As(err, &compactionErr) {
		t.Fatalf("expected CompactionError, got %v", err)
	}
	if compactionErr.Code != harness.CompactionCodeAborted || compactionErr.Error() != "stopped" {
		t.Fatalf("unexpected error: %v", compactionErr)
	}
}

func TestClampsCompactionSummaryMaxTokensToTheModelOutputCap(t *testing.T) {
	messages := []agent.AgentMessage{createUserMessage("Summarize this.")}
	model := createStubModel(false, 128000)
	stub := &stubModels{responses: []*types.AssistantMessage{
		fauxAssistantMessage("## Goal\nTest summary"),
		fauxAssistantMessage("## Goal\nTest summary"),
	}}
	preparation := &CompactionPreparation{
		MessagesToSummarize: messages,
		TurnPrefixMessages:  messages,
		RetainedTail:        messages,
		IsSplitTurn:         true,
		TokensBefore:        600000,
		FileOps:             CreateFileOps(),
		Settings:            CompactionSettings{Enabled: true, ReserveTokens: 500000, KeepRecentTokens: 20000},
	}

	if _, err := Compact(context.Background(), preparation, stub, model, "", "", nil, nil); err != nil {
		t.Fatal(err)
	}

	if len(stub.seen) != 2 {
		t.Fatalf("expected two calls, got %d", len(stub.seen))
	}
	for i, options := range stub.seen {
		if options.MaxTokens == nil || *options.MaxTokens != 128000 {
			t.Fatalf("call %d: expected maxTokens 128000, got %+v", i, options.MaxTokens)
		}
		if options.CacheRetention != types.CacheRetentionNone {
			t.Fatalf("call %d: expected cacheRetention none, got %q", i, options.CacheRetention)
		}
	}
	if stub.seen[0].SessionID == stub.seen[1].SessionID {
		t.Fatal("expected distinct session ids")
	}
}

func TestReturnsCompactionErrorResultsWithoutThrowing(t *testing.T) {
	messages := []agent.AgentMessage{createUserMessage("Summarize this.")}
	preparation := &CompactionPreparation{
		MessagesToSummarize: messages,
		TurnPrefixMessages:  nil,
		RetainedTail:        messages,
		IsSplitTurn:         false,
		TokensBefore:        100,
		FileOps:             CreateFileOps(),
		Settings:            CompactionSettings{Enabled: true, ReserveTokens: 2000, KeepRecentTokens: 20},
	}
	model := createStubModel(false, 8192)

	errorResponse := fauxAssistantMessage("")
	errorResponse.StopReason = types.StopReasonError
	errorResponse.ErrorMessage = "history failed"
	stub := &stubModels{responses: []*types.AssistantMessage{errorResponse}}
	_, err := Compact(context.Background(), preparation, stub, model, "", "", nil, nil)
	var compactionErr *harness.CompactionError
	if !errors.As(err, &compactionErr) {
		t.Fatalf("expected CompactionError, got %v", err)
	}
	if compactionErr.Code != harness.CompactionCodeSummarizationFailed || compactionErr.Error() != "Summarization failed: history failed" {
		t.Fatalf("unexpected error: %v", compactionErr)
	}
}

func TestCombinesUsageForSplitTurnCompactionSummaries(t *testing.T) {
	messages := []agent.AgentMessage{createUserMessage("Summarize this.")}
	model := createStubModel(false, 8192)
	historyUsage := createMockUsage(1, 2, 3, 4)
	turnPrefixUsage := createMockUsage(5, 6, 7, 8)
	historyResponse := fauxAssistantMessage("history summary")
	historyResponse.Usage = historyUsage
	prefixResponse := fauxAssistantMessage("turn prefix summary")
	prefixResponse.Usage = turnPrefixUsage
	stub := &stubModels{responses: []*types.AssistantMessage{historyResponse, prefixResponse}}
	preparation := &CompactionPreparation{
		MessagesToSummarize: messages,
		TurnPrefixMessages:  messages,
		IsSplitTurn:         true,
		TokensBefore:        100,
		RetainedTail:        messages,
		FileOps:             CreateFileOps(),
		Settings:            CompactionSettings{Enabled: true, ReserveTokens: 2000, KeepRecentTokens: 20},
	}

	result, err := Compact(context.Background(), preparation, stub, model, "", "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	want := createMockUsage(6, 8, 10, 12)
	if result.Usage == nil || *result.Usage != want {
		t.Fatalf("usage mismatch: %+v != %+v", result.Usage, want)
	}
}

func TestPassesReasoningThroughTurnPrefixSummariesWhenEnabled(t *testing.T) {
	messages := []agent.AgentMessage{createUserMessage("Summarize this.")}
	model := createStubModel(true, 8192)
	stub := &stubModels{responses: []*types.AssistantMessage{fauxAssistantMessage("## Original Request\nTest summary")}}
	preparation := &CompactionPreparation{
		MessagesToSummarize: nil,
		TurnPrefixMessages:  messages,
		RetainedTail:        messages,
		IsSplitTurn:         true,
		TokensBefore:        100,
		FileOps:             CreateFileOps(),
		Settings:            CompactionSettings{Enabled: true, ReserveTokens: 2000, KeepRecentTokens: 20},
	}

	if _, err := Compact(context.Background(), preparation, stub, model, "", types.ModelThinkingHigh, nil, nil); err != nil {
		t.Fatal(err)
	}

	if len(stub.seen) != 1 || stub.seen[0].Reasoning != types.ThinkingLevelHigh {
		t.Fatalf("expected reasoning=high, got %+v", stub.seen)
	}
}

func TestReturnsTurnPrefixCompactionErrorsWithoutThrowing(t *testing.T) {
	messages := []agent.AgentMessage{createUserMessage("Summarize this.")}
	preparation := &CompactionPreparation{
		MessagesToSummarize: nil,
		TurnPrefixMessages:  messages,
		RetainedTail:        messages,
		IsSplitTurn:         true,
		TokensBefore:        100,
		FileOps:             CreateFileOps(),
		Settings:            CompactionSettings{Enabled: true, ReserveTokens: 2000, KeepRecentTokens: 20},
	}
	model := createStubModel(false, 8192)

	errorResponse := fauxAssistantMessage("")
	errorResponse.StopReason = types.StopReasonError
	errorResponse.ErrorMessage = "prefix failed"
	stub := &stubModels{responses: []*types.AssistantMessage{errorResponse}}
	_, err := Compact(context.Background(), preparation, stub, model, "", "", nil, nil)
	var compactionErr *harness.CompactionError
	if !errors.As(err, &compactionErr) {
		t.Fatalf("expected CompactionError, got %v", err)
	}
	if compactionErr.Code != harness.CompactionCodeSummarizationFailed || compactionErr.Error() != "Turn prefix summarization failed: prefix failed" {
		t.Fatalf("unexpected error: %v", compactionErr)
	}

	abortedResponse := fauxAssistantMessage("")
	abortedResponse.StopReason = types.StopReasonAborted
	abortedResponse.ErrorMessage = "prefix stopped"
	stub = &stubModels{responses: []*types.AssistantMessage{abortedResponse}}
	_, err = Compact(context.Background(), preparation, stub, model, "", "", nil, nil)
	if !errors.As(err, &compactionErr) {
		t.Fatalf("expected CompactionError, got %v", err)
	}
	if compactionErr.Code != harness.CompactionCodeAborted || compactionErr.Error() != "prefix stopped" {
		t.Fatalf("unexpected error: %v", compactionErr)
	}
}

func TestReturnsACompactionResultWithFileDetails(t *testing.T) {
	u1 := createMessageEntry(createUserMessage("read a file"), nil)
	assistantMessage := createAssistantMessage("calling tool", createMockUsage(1000, 200, 0, 0))
	assistantMessage.Content = []types.Content{
		types.ToolCall{Kind: types.KindToolCall, ID: "tool-1", Name: "read", Arguments: map[string]any{"path": "src/index.ts"}},
	}
	a1 := createMessageEntry(assistantMessage, lo.ToPtr(u1.ID))
	u2 := createMessageEntry(createUserMessage("continue"), lo.ToPtr(a1.ID))
	a2 := createMessageEntry(createAssistantMessage("done", createMockUsage(4000, 500, 0, 0)), lo.ToPtr(u2.ID))
	preparation, err := PrepareCompaction([]session.Entry{u1, a1, u2, a2}, DefaultCompactionSettings)
	if err != nil || preparation == nil {
		t.Fatalf("expected a preparation, got %v, %v", preparation, err)
	}
	model := createStubModel(false, 8192)
	stub := &stubModels{responses: []*types.AssistantMessage{fauxAssistantMessage("## Goal\nTest summary")}}
	result, err := Compact(context.Background(), preparation, stub, model, "", "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Summary) == 0 {
		t.Fatal("expected a non-empty summary")
	}
	if result.Usage == nil || result.Usage.TotalTokens <= 0 {
		t.Fatalf("expected usage, got %+v", result.Usage)
	}
	if len(result.RetainedTail) == 0 {
		t.Fatal("expected a retained tail")
	}
	details, ok := result.Details.(CompactionDetails)
	if !ok {
		t.Fatalf("expected CompactionDetails, got %T", result.Details)
	}
	_ = details // pi asserts only `details` is defined here.
}

func mustBuildContextMessages(t *testing.T, pathEntries []session.Entry) []agent.AgentMessage {
	t.Helper()
	messages, err := session.BuildSessionContext(pathEntries, nil, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return messages
}
