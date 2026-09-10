// pi: test/compaction.test.ts — unit and fixture tests for the coding-agent
// compaction functions.
package core

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/agent/harness"
	"github.com/ZoroSpace/pig/ai/types"
)

// ============================================================================
// Test fixtures
// ============================================================================

func loadLargeSessionEntries(t *testing.T) []SessionEntry {
	t.Helper()
	const sessionPath = "testdata/fixtures/large-session.jsonl"
	content, err := os.ReadFile(sessionPath)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	entries := ParseSessionEntries(string(content))
	MigrateSessionEntries(entries) // Add id/parentId for v1 fixtures
	var out []SessionEntry
	for _, e := range entries {
		if e.EntryType() == "session" {
			continue
		}
		out = append(out, e.(SessionEntry))
	}
	return out
}

func createMockUsage(input int64, output int64, cacheRead int64, cacheWrite int64) types.Usage {
	return types.Usage{
		Input:       input,
		Output:      output,
		CacheRead:   cacheRead,
		CacheWrite:  cacheWrite,
		TotalTokens: input + output + cacheRead + cacheWrite,
		Cost:        types.Cost{},
	}
}

func createUserMessage(text string) agent.AgentMessage {
	return types.UserMessage{
		Role:      types.RoleUser,
		Content:   types.UserContent{String: text},
		Timestamp: time.Now().UnixMilli(),
	}
}

func createAssistantMessage(text string, usage *types.Usage) types.AssistantMessage {
	if usage == nil {
		u := createMockUsage(100, 50, 0, 0)
		usage = &u
	}
	return types.AssistantMessage{
		Role:       types.RoleAssistant,
		Content:    []types.Content{types.NewTextContent(text)},
		Usage:      *usage,
		StopReason: types.StopReasonStop,
		Timestamp:  time.Now().UnixMilli(),
		API:        "anthropic-messages",
		Provider:   "anthropic",
		Model:      "claude-sonnet-4-5",
	}
}

var entryCounter = 0
var lastID *string

// resetEntryCounter resets the id generators for predictable IDs.
func resetEntryCounter() {
	entryCounter = 0
	lastID = nil
}

func nextTestID() string {
	id := fmt.Sprintf("test-id-%d", entryCounter)
	entryCounter++
	return id
}

func testEntryBase() (EntryBase, string) {
	id := nextTestID()
	return EntryBase{Type: "entry", ID: id, ParentID: lastID, Timestamp: time.Now().UTC().Format("2006-01-02T15:04:05.000Z07:00")}, id
}

func createMessageEntry(message agent.AgentMessage) *SessionMessageEntry {
	base, id := testEntryBase()
	base.Type = "message"
	lastID = &id
	return &SessionMessageEntry{EntryBase: base, Message: message}
}

func createCompactionEntry(summary string, firstKeptEntryID string) *CompactionEntry {
	base, id := testEntryBase()
	base.Type = "compaction"
	lastID = &id
	return &CompactionEntry{EntryBase: base, Summary: summary, FirstKeptEntryID: firstKeptEntryID, TokensBefore: 10000}
}

func createModelChangeEntry(provider string, modelID string) *ModelChangeEntry {
	base, id := testEntryBase()
	base.Type = "model_change"
	lastID = &id
	return &ModelChangeEntry{EntryBase: base, Provider: provider, ModelID: modelID}
}

func createThinkingLevelEntry(thinkingLevel string) *ThinkingLevelChangeEntry {
	base, id := testEntryBase()
	base.Type = "thinking_level_change"
	lastID = &id
	return &ThinkingLevelChangeEntry{EntryBase: base, ThinkingLevel: thinkingLevel}
}

func createCustomMessageEntry(content string) *CustomMessageEntry {
	base, id := testEntryBase()
	base.Type = "custom_message"
	lastID = &id
	return &CustomMessageEntry{EntryBase: base, CustomType: "test", Content: content, Display: true}
}

// entryText extracts the text of one context message (pi: extractText's
// per-message switch).
func entryText(message agent.AgentMessage) string {
	switch m := message.(type) {
	case types.UserMessage:
		if m.Content.String != "" {
			return m.Content.String
		}
		return contentPartsText(m.Content.Parts)
	case types.AssistantMessage:
		return contentPartsText(m.Content)
	case harness.BranchSummaryMessage:
		return m.Summary
	case harness.CompactionSummaryMessage:
		return m.Summary
	case harness.CustomMessage:
		if s, ok := m.Content.(string); ok {
			return s
		}
		if parts, ok := m.Content.([]types.Content); ok {
			return contentPartsText(parts)
		}
		return ""
	case types.ToolResultMessage:
		return contentPartsText(m.Content)
	case harness.BashExecutionMessage:
		return fmt.Sprintf("%s\n%s", m.Command, m.Output)
	default:
		return ""
	}
}

func contentPartsText(parts []types.Content) string {
	out := ""
	for _, part := range parts {
		if text, ok := part.(types.TextContent); ok {
			if out != "" {
				out += " "
			}
			out += text.Text
		}
	}
	return out
}

func extractText(messages []agent.AgentMessage) string {
	out := ""
	for _, message := range messages {
		if out != "" {
			out += "\n"
		}
		out += entryText(message)
	}
	return out
}

func usagePtr(usage types.Usage) *types.Usage { return &usage }

func contains(haystack string, needle string) bool {
	return len(needle) == 0 || strings.Contains(haystack, needle)
}

// repeat mirrors JS `text.repeat(n)`.
func repeat(text string, n int) string {
	return strings.Repeat(text, n)
}

// ============================================================================
// Unit tests
// ============================================================================

func TestCalculatesTotalContextTokensFromUsage(t *testing.T) {
	usage := createMockUsage(1000, 500, 200, 100)
	if got := CalculateContextTokens(&usage); got != 1800 {
		t.Fatalf("expected 1800, got %d", got)
	}
}

func TestCalculatesContextTokensHandlesZeroValues(t *testing.T) {
	usage := createMockUsage(0, 0, 0, 0)
	if got := CalculateContextTokens(&usage); got != 0 {
		t.Fatalf("expected 0, got %d", got)
	}
}

func TestGetLastAssistantUsageFindsTheLastNonAbortedAssistantMessageUsage(t *testing.T) {
	resetEntryCounter()
	entries := []SessionEntry{
		createMessageEntry(createUserMessage("Hello")),
		createMessageEntry(createAssistantMessage("Hi", usagePtr(createMockUsage(100, 50, 0, 0)))),
		createMessageEntry(createUserMessage("How are you?")),
		createMessageEntry(createAssistantMessage("Good", usagePtr(createMockUsage(200, 100, 0, 0)))),
	}

	usage := GetLastAssistantUsage(entries)
	if usage == nil {
		t.Fatal("expected usage, got nil")
	}
	if usage.Input != 200 {
		t.Fatalf("expected input 200, got %d", usage.Input)
	}
}

func TestGetLastAssistantUsageSkipsAbortedMessages(t *testing.T) {
	resetEntryCounter()
	abortedMsg := createAssistantMessage("Aborted", usagePtr(createMockUsage(300, 150, 0, 0)))
	abortedMsg.StopReason = types.StopReasonAborted

	entries := []SessionEntry{
		createMessageEntry(createUserMessage("Hello")),
		createMessageEntry(createAssistantMessage("Hi", usagePtr(createMockUsage(100, 50, 0, 0)))),
		createMessageEntry(createUserMessage("How are you?")),
		createMessageEntry(abortedMsg),
	}

	usage := GetLastAssistantUsage(entries)
	if usage == nil {
		t.Fatal("expected usage, got nil")
	}
	if usage.Input != 100 {
		t.Fatalf("expected input 100, got %d", usage.Input)
	}
}

func TestGetLastAssistantUsageSkipsAllZeroAssistantUsage(t *testing.T) {
	resetEntryCounter()
	entries := []SessionEntry{
		createMessageEntry(createUserMessage("Hello")),
		createMessageEntry(createAssistantMessage("Hi", usagePtr(createMockUsage(100, 50, 0, 0)))),
		createMessageEntry(createUserMessage("continue")),
		createMessageEntry(createAssistantMessage("Partial", usagePtr(createMockUsage(0, 0, 0, 0)))),
	}

	usage := GetLastAssistantUsage(entries)
	if usage == nil {
		t.Fatal("expected usage, got nil")
	}
	if usage.Input != 100 {
		t.Fatalf("expected input 100, got %d", usage.Input)
	}
}

func TestGetLastAssistantUsageReturnsUndefinedIfNoAssistantMessages(t *testing.T) {
	resetEntryCounter()
	entries := []SessionEntry{createMessageEntry(createUserMessage("Hello"))}
	if usage := GetLastAssistantUsage(entries); usage != nil {
		t.Fatalf("expected nil, got %v", usage)
	}
}

func TestEstimateContextTokensUsesTheLastNonZeroAssistantUsageAsTheContextAnchor(t *testing.T) {
	resetEntryCounter()
	messages := []agent.AgentMessage{
		createUserMessage("Hello"),
		createAssistantMessage("Hi", usagePtr(createMockUsage(100, 50, 0, 0))),
		createUserMessage("continue"),
		createAssistantMessage("Partial thinking", usagePtr(createMockUsage(0, 0, 0, 0))),
	}

	estimate := EstimateContextTokens(messages)

	if estimate.UsageTokens != 150 {
		t.Fatalf("expected usageTokens 150, got %d", estimate.UsageTokens)
	}
	if estimate.LastUsageIndex == nil || *estimate.LastUsageIndex != 1 {
		t.Fatalf("expected lastUsageIndex 1, got %v", estimate.LastUsageIndex)
	}
	if estimate.TrailingTokens <= 0 {
		t.Fatalf("expected trailingTokens > 0, got %d", estimate.TrailingTokens)
	}
	if estimate.Tokens != 150+estimate.TrailingTokens {
		t.Fatalf("expected tokens %d, got %d", 150+estimate.TrailingTokens, estimate.Tokens)
	}
}

func TestShouldCompactReturnsTrueWhenContextExceedsThreshold(t *testing.T) {
	settings := CompactionSettingsValues{Enabled: true, ReserveTokens: 10000, KeepRecentTokens: 20000}

	if !ShouldCompact(95000, 100000, settings) {
		t.Fatal("expected true for 95000/100000")
	}
	if ShouldCompact(89000, 100000, settings) {
		t.Fatal("expected false for 89000/100000")
	}
}

func TestShouldCompactReturnsFalseWhenDisabled(t *testing.T) {
	settings := CompactionSettingsValues{Enabled: false, ReserveTokens: 10000, KeepRecentTokens: 20000}

	if ShouldCompact(95000, 100000, settings) {
		t.Fatal("expected false when disabled")
	}
}

func TestFindCutPointBasedOnActualTokenDifferences(t *testing.T) {
	resetEntryCounter()
	// Create entries with cumulative token counts
	var entries []SessionEntry
	for i := 0; i < 10; i++ {
		entries = append(entries, createMessageEntry(createUserMessage(fmt.Sprintf("User %d", i))))
		entries = append(entries, createMessageEntry(
			createAssistantMessage(fmt.Sprintf("Assistant %d", i), usagePtr(createMockUsage(0, 100, int64((i+1)*1000), 0)))))
	}

	// 20 entries, last assistant has 10000 tokens
	// keepRecentTokens = 2500: keep entries where diff < 2500
	result := FindCutPoint(entries, 0, len(entries), 2500)

	// Should cut at a valid cut point (user or assistant message)
	entry, ok := entries[result.FirstKeptEntryIndex].(*SessionMessageEntry)
	if !ok {
		t.Fatalf("expected message entry, got %T", entries[result.FirstKeptEntryIndex])
	}
	role := entry.Message.MessageRole()
	if role != types.RoleUser && role != types.RoleAssistant {
		t.Fatalf("expected user or assistant role, got %s", role)
	}
}

func TestFindCutPointReturnsStartIndexIfNoValidCutPointsInRange(t *testing.T) {
	resetEntryCounter()
	entries := []SessionEntry{createMessageEntry(createAssistantMessage("a", nil))}
	result := FindCutPoint(entries, 0, len(entries), 1000)
	if result.FirstKeptEntryIndex != 0 {
		t.Fatalf("expected 0, got %d", result.FirstKeptEntryIndex)
	}
}

func TestFindCutPointKeepsEverythingIfAllMessagesFitWithinBudget(t *testing.T) {
	resetEntryCounter()
	entries := []SessionEntry{
		createMessageEntry(createUserMessage("1")),
		createMessageEntry(createAssistantMessage("a", usagePtr(createMockUsage(0, 50, 500, 0)))),
		createMessageEntry(createUserMessage("2")),
		createMessageEntry(createAssistantMessage("b", usagePtr(createMockUsage(0, 50, 1000, 0)))),
	}

	result := FindCutPoint(entries, 0, len(entries), 50000)
	if result.FirstKeptEntryIndex != 0 {
		t.Fatalf("expected 0, got %d", result.FirstKeptEntryIndex)
	}
}

func TestFindCutPointIndicatesSplitTurnWhenCuttingAtAssistantMessage(t *testing.T) {
	resetEntryCounter()
	// Create a scenario where we cut at an assistant message mid-turn
	entries := []SessionEntry{
		createMessageEntry(createUserMessage("Turn 1")),
		createMessageEntry(createAssistantMessage("A1", usagePtr(createMockUsage(0, 100, 1000, 0)))),
		createMessageEntry(createUserMessage("Turn 2")),                                                 // index 2
		createMessageEntry(createAssistantMessage("A2-1", usagePtr(createMockUsage(0, 100, 5000, 0)))),  // index 3
		createMessageEntry(createAssistantMessage("A2-2", usagePtr(createMockUsage(0, 100, 8000, 0)))),  // index 4
		createMessageEntry(createAssistantMessage("A2-3", usagePtr(createMockUsage(0, 100, 10000, 0)))), // index 5
	}

	// With keepRecentTokens = 3000, should cut somewhere in Turn 2
	result := FindCutPoint(entries, 0, len(entries), 3000)

	// If cut at assistant message (not user), should indicate split turn
	cutEntry, ok := entries[result.FirstKeptEntryIndex].(*SessionMessageEntry)
	if !ok {
		t.Fatalf("expected message entry, got %T", entries[result.FirstKeptEntryIndex])
	}
	if cutEntry.Message.MessageRole() == types.RoleAssistant {
		if !result.IsSplitTurn {
			t.Fatal("expected isSplitTurn true")
		}
		if result.TurnStartIndex != 2 { // Turn 2 starts at index 2
			t.Fatalf("expected turnStartIndex 2, got %d", result.TurnStartIndex)
		}
	}
}

func TestFindCutPointBudgetsContextVisibleCustomMessageEntries(t *testing.T) {
	resetEntryCounter()
	entries := []SessionEntry{
		createMessageEntry(createUserMessage("hi")),
		createMessageEntry(createAssistantMessage("hello", nil)),
		createCustomMessageEntry(repeat("x", 4000)),
		createMessageEntry(createAssistantMessage("ok", nil)),
	}

	tinyBudget := FindCutPoint(entries, 0, len(entries), 1)
	if tinyBudget.FirstKeptEntryIndex != 3 {
		t.Fatalf("expected 3, got %d", tinyBudget.FirstKeptEntryIndex)
	}
	if !tinyBudget.IsSplitTurn {
		t.Fatal("expected isSplitTurn true")
	}
	if tinyBudget.TurnStartIndex != 2 {
		t.Fatalf("expected turnStartIndex 2, got %d", tinyBudget.TurnStartIndex)
	}

	customFitsBudget := FindCutPoint(entries, 0, len(entries), 2)
	if customFitsBudget.FirstKeptEntryIndex != 2 {
		t.Fatalf("expected 2, got %d", customFitsBudget.FirstKeptEntryIndex)
	}
	if customFitsBudget.IsSplitTurn {
		t.Fatal("expected isSplitTurn false")
	}
	if customFitsBudget.TurnStartIndex != -1 {
		t.Fatalf("expected turnStartIndex -1, got %d", customFitsBudget.TurnStartIndex)
	}
}

// ============================================================================
// buildSessionContext
// ============================================================================

func TestBuildSessionContextLoadsAllMessagesWhenNoCompaction(t *testing.T) {
	resetEntryCounter()
	entries := []SessionEntry{
		createMessageEntry(createUserMessage("1")),
		createMessageEntry(createAssistantMessage("a", nil)),
		createMessageEntry(createUserMessage("2")),
		createMessageEntry(createAssistantMessage("b", nil)),
	}

	loaded := BuildSessionContext(entries, nil, nil)
	if len(loaded.Messages) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(loaded.Messages))
	}
	if loaded.ThinkingLevel != "off" {
		t.Fatalf("expected thinkingLevel off, got %s", loaded.ThinkingLevel)
	}
	if loaded.Model == nil || loaded.Model.Provider != "anthropic" || loaded.Model.ModelID != "claude-sonnet-4-5" {
		t.Fatalf("unexpected model: %v", loaded.Model)
	}
}

func TestBuildSessionContextHandlesSingleCompaction(t *testing.T) {
	resetEntryCounter()
	u1 := createMessageEntry(createUserMessage("1"))
	a1 := createMessageEntry(createAssistantMessage("a", nil))
	u2 := createMessageEntry(createUserMessage("2"))
	a2 := createMessageEntry(createAssistantMessage("b", nil))
	compaction := createCompactionEntry("Summary of 1,a,2,b", u2.ID) // keep from u2 onwards
	u3 := createMessageEntry(createUserMessage("3"))
	a3 := createMessageEntry(createAssistantMessage("c", nil))

	entries := []SessionEntry{u1, a1, u2, a2, compaction, u3, a3}

	loaded := BuildSessionContext(entries, nil, nil)
	// summary + kept (u2, a2) + after (u3, a3) = 5
	if len(loaded.Messages) != 5 {
		t.Fatalf("expected 5 messages, got %d", len(loaded.Messages))
	}
	if loaded.Messages[0].MessageRole() != harness.RoleCompactionSummary {
		t.Fatalf("expected compactionSummary role, got %s", loaded.Messages[0].MessageRole())
	}
	if summary, ok := loaded.Messages[0].(harness.CompactionSummaryMessage); !ok || !contains(summary.Summary, "Summary of 1,a,2,b") {
		t.Fatalf("expected summary to contain text, got %v", loaded.Messages[0])
	}
}

func TestBuildSessionContextHandlesMultipleCompactions(t *testing.T) {
	resetEntryCounter()
	// First batch
	u1 := createMessageEntry(createUserMessage("1"))
	a1 := createMessageEntry(createAssistantMessage("a", nil))
	compact1 := createCompactionEntry("First summary", u1.ID)
	// Second batch
	u2 := createMessageEntry(createUserMessage("2"))
	b := createMessageEntry(createAssistantMessage("b", nil))
	u3 := createMessageEntry(createUserMessage("3"))
	c := createMessageEntry(createAssistantMessage("c", nil))
	compact2 := createCompactionEntry("Second summary", u3.ID) // keep from u3 onwards
	// After second compaction
	u4 := createMessageEntry(createUserMessage("4"))
	d := createMessageEntry(createAssistantMessage("d", nil))

	entries := []SessionEntry{u1, a1, compact1, u2, b, u3, c, compact2, u4, d}

	loaded := BuildSessionContext(entries, nil, nil)
	// summary + kept from u3 (u3, c) + after (u4, d) = 5
	if len(loaded.Messages) != 5 {
		t.Fatalf("expected 5 messages, got %d", len(loaded.Messages))
	}
	if summary, ok := loaded.Messages[0].(harness.CompactionSummaryMessage); !ok || !contains(summary.Summary, "Second summary") {
		t.Fatalf("expected second summary, got %v", loaded.Messages[0])
	}
}

func TestBuildSessionContextKeepsAllMessagesWhenFirstKeptEntryIdIsFirstEntry(t *testing.T) {
	resetEntryCounter()
	u1 := createMessageEntry(createUserMessage("1"))
	a1 := createMessageEntry(createAssistantMessage("a", nil))
	compact1 := createCompactionEntry("First summary", u1.ID) // keep from first entry
	u2 := createMessageEntry(createUserMessage("2"))
	b := createMessageEntry(createAssistantMessage("b", nil))

	entries := []SessionEntry{u1, a1, compact1, u2, b}

	loaded := BuildSessionContext(entries, nil, nil)
	// summary + all messages (u1, a1, u2, b) = 5
	if len(loaded.Messages) != 5 {
		t.Fatalf("expected 5 messages, got %d", len(loaded.Messages))
	}
}

func TestBuildSessionContextTracksModelAndThinkingLevelChanges(t *testing.T) {
	resetEntryCounter()
	entries := []SessionEntry{
		createMessageEntry(createUserMessage("1")),
		createModelChangeEntry("openai", "gpt-4"),
		createMessageEntry(createAssistantMessage("a", nil)),
		createThinkingLevelEntry("high"),
	}

	loaded := BuildSessionContext(entries, nil, nil)
	// model_change is later overwritten by assistant message's model info
	if loaded.Model == nil || loaded.Model.Provider != "anthropic" || loaded.Model.ModelID != "claude-sonnet-4-5" {
		t.Fatalf("unexpected model: %v", loaded.Model)
	}
	if loaded.ThinkingLevel != "high" {
		t.Fatalf("expected thinkingLevel high, got %s", loaded.ThinkingLevel)
	}
}

// ============================================================================
// prepareCompaction with previous compaction
// ============================================================================

func TestPrepareCompactionSkipsRepeatedCompactionsWhenKeptMessagesStillFit(t *testing.T) {
	resetEntryCounter()
	u1 := createMessageEntry(createUserMessage("user msg 1 (summarized by compaction1)"))
	a1 := createMessageEntry(createAssistantMessage("assistant msg 1", nil))
	u2 := createMessageEntry(createUserMessage("user msg 2 - kept by compaction1"))
	a2 := createMessageEntry(createAssistantMessage("assistant msg 2", nil))
	u3 := createMessageEntry(createUserMessage("user msg 3 - kept by compaction1"))
	a3 := createMessageEntry(createAssistantMessage("assistant msg 3", usagePtr(createMockUsage(5000, 1000, 0, 0))))
	compaction1 := createCompactionEntry("First summary", u2.ID)
	u4 := createMessageEntry(createUserMessage("user msg 4 (new after compaction1)"))
	a4 := createMessageEntry(createAssistantMessage("assistant msg 4", usagePtr(createMockUsage(8000, 2000, 0, 0))))

	pathEntries := []SessionEntry{u1, a1, u2, a2, u3, a3, compaction1, u4, a4}
	preparation := PrepareCompaction(pathEntries, DefaultCompactionSettings)

	if preparation != nil {
		t.Fatalf("expected nil preparation, got %+v", preparation)
	}
}

func TestPrepareCompactionReSummarizesPreviouslyKeptMessagesWhenTheRecentWindowMovesPastThem(t *testing.T) {
	resetEntryCounter()
	u1 := createMessageEntry(createUserMessage(repeat("user msg 1 (summarized by compaction1)", 4)))
	a1 := createMessageEntry(createAssistantMessage(repeat("assistant msg 1", 4), nil))
	u2 := createMessageEntry(createUserMessage(repeat("user msg 2 - kept by compaction1 ", 12)))
	a2 := createMessageEntry(createAssistantMessage(repeat("assistant msg 2 ", 12), nil))
	u3 := createMessageEntry(createUserMessage(repeat("user msg 3 - kept by compaction1 ", 12)))
	a3 := createMessageEntry(createAssistantMessage(repeat("assistant msg 3 ", 12), usagePtr(createMockUsage(5000, 1000, 0, 0))))
	compaction1 := createCompactionEntry("First summary", u2.ID)
	u4 := createMessageEntry(createUserMessage(repeat("user msg 4 (new after compaction1) ", 12)))
	a4 := createMessageEntry(createAssistantMessage(repeat("assistant msg 4 ", 12), usagePtr(createMockUsage(8000, 2000, 0, 0))))

	settings := DefaultCompactionSettings
	settings.KeepRecentTokens = 100
	preparation := PrepareCompaction([]SessionEntry{u1, a1, u2, a2, u3, a3, compaction1, u4, a4}, settings)

	if preparation == nil {
		t.Fatal("expected preparation, got nil")
	}
	summarizedText := extractText(preparation.MessagesToSummarize)
	if !contains(summarizedText, "user msg 2 - kept by compaction1") {
		t.Fatalf("summarized text missing user msg 2: %s", summarizedText)
	}
	if !contains(summarizedText, "user msg 3 - kept by compaction1") {
		t.Fatalf("summarized text missing user msg 3: %s", summarizedText)
	}
	if contains(summarizedText, "First summary") {
		t.Fatalf("summarized text should not contain previous summary: %s", summarizedText)
	}
	if preparation.PreviousSummary != "First summary" {
		t.Fatalf("expected previousSummary 'First summary', got %q", preparation.PreviousSummary)
	}
}

// ============================================================================
// Integration tests with real session data
// ============================================================================

func TestLargeSessionFixtureParsesTheLargeSession(t *testing.T) {
	entries := loadLargeSessionEntries(t)
	if len(entries) <= 100 {
		t.Fatalf("expected more than 100 entries, got %d", len(entries))
	}

	messageCount := 0
	for _, e := range entries {
		if _, ok := e.(*SessionMessageEntry); ok {
			messageCount++
		}
	}
	if messageCount <= 100 {
		t.Fatalf("expected more than 100 message entries, got %d", messageCount)
	}
}

func TestLargeSessionFixtureFindsCutPointInLargeSession(t *testing.T) {
	entries := loadLargeSessionEntries(t)
	result := FindCutPoint(entries, 0, len(entries), DefaultCompactionSettings.KeepRecentTokens)

	// Cut point should be at a message entry (user or assistant)
	entry, ok := entries[result.FirstKeptEntryIndex].(*SessionMessageEntry)
	if !ok {
		t.Fatalf("expected message entry, got %T", entries[result.FirstKeptEntryIndex])
	}
	role := entry.Message.MessageRole()
	if role != types.RoleUser && role != types.RoleAssistant {
		t.Fatalf("expected user or assistant role, got %s", role)
	}
}

func TestLargeSessionFixtureLoadsSessionCorrectly(t *testing.T) {
	entries := loadLargeSessionEntries(t)
	loaded := BuildSessionContext(entries, nil, nil)

	if len(loaded.Messages) <= 100 {
		t.Fatalf("expected more than 100 messages, got %d", len(loaded.Messages))
	}
	if loaded.Model == nil {
		t.Fatal("expected model, got nil")
	}
}

// ============================================================================
// LLM integration tests (skipped without API key; additionally require the
// anthropic-messages adapter, deferred with the rest of Phase 2)
// ============================================================================

func TestLLMSummarizationGeneratesACompactionResultForTheLargeSession(t *testing.T) {
	if os.Getenv("ANTHROPIC_OAUTH_TOKEN") == "" {
		t.Skip("ANTHROPIC_OAUTH_TOKEN not set")
	}
	t.Skip("anthropic-messages adapter not yet ported (Phase 2 deferral, see §4)")

	entries := loadLargeSessionEntries(t)
	model := llmTestModel()

	preparation := PrepareCompaction(entries, DefaultCompactionSettings)
	if preparation == nil {
		t.Fatal("expected preparation, got nil")
	}

	compactionResult, err := Compact(t.Context(), preparation, model, os.Getenv("ANTHROPIC_OAUTH_TOKEN"), nil, "", "", nil, nil, nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}

	if len(compactionResult.Summary) <= 100 {
		t.Fatalf("expected summary longer than 100 chars, got %d", len(compactionResult.Summary))
	}
	if compactionResult.FirstKeptEntryID == "" {
		t.Fatal("expected firstKeptEntryId")
	}
	if compactionResult.TokensBefore <= 0 {
		t.Fatalf("expected tokensBefore > 0, got %d", compactionResult.TokensBefore)
	}
}

func TestLLMSummarizationProducesValidSessionAfterCompaction(t *testing.T) {
	if os.Getenv("ANTHROPIC_OAUTH_TOKEN") == "" {
		t.Skip("ANTHROPIC_OAUTH_TOKEN not set")
	}
	t.Skip("anthropic-messages adapter not yet ported (Phase 2 deferral, see §4)")

	entries := loadLargeSessionEntries(t)
	loaded := BuildSessionContext(entries, nil, nil)
	model := llmTestModel()

	preparation := PrepareCompaction(entries, DefaultCompactionSettings)
	if preparation == nil {
		t.Fatal("expected preparation, got nil")
	}

	compactionResult, err := Compact(t.Context(), preparation, model, os.Getenv("ANTHROPIC_OAUTH_TOKEN"), nil, "", "", nil, nil, nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}

	// Simulate appending compaction to entries by creating a proper entry
	lastEntry := entries[len(entries)-1]
	parentID := lastEntry.Base().ID
	compactionEntry := &CompactionEntry{
		EntryBase:        EntryBase{Type: "compaction", ID: "compaction-test-id", ParentID: &parentID, Timestamp: time.Now().UTC().Format("2006-01-02T15:04:05.000Z07:00")},
		Summary:          compactionResult.Summary,
		FirstKeptEntryID: compactionResult.FirstKeptEntryID,
		TokensBefore:     compactionResult.TokensBefore,
	}
	newEntries := append(append([]SessionEntry{}, entries...), compactionEntry)
	reloaded := BuildSessionContext(newEntries, nil, nil)

	// Should have summary + kept messages
	if len(reloaded.Messages) >= len(loaded.Messages) {
		t.Fatalf("expected fewer messages after compaction: %d vs %d", len(reloaded.Messages), len(loaded.Messages))
	}
	if reloaded.Messages[0].MessageRole() != harness.RoleCompactionSummary {
		t.Fatalf("expected compactionSummary role, got %s", reloaded.Messages[0].MessageRole())
	}
	if summary, ok := reloaded.Messages[0].(harness.CompactionSummaryMessage); !ok || !contains(summary.Summary, compactionResult.Summary) {
		t.Fatal("expected first message to carry the compaction summary")
	}
}

// llmTestModel builds the anthropic model used by the (skipped) LLM
// integration tests (pi: getModel("anthropic", "claude-sonnet-4-5")).
func llmTestModel() *types.Model {
	return &types.Model{
		ID:            "claude-sonnet-4-5",
		Name:          "Claude Sonnet 4.5",
		API:           "anthropic-messages",
		Provider:      "anthropic",
		Reasoning:     true,
		Input:         []types.ModelInput{types.ModelInputText},
		ContextWindow: 200000,
		MaxTokens:     8192,
	}
}
