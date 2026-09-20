// pi: test/agent-session-stats.test.ts — AgentSession.getSessionStats and
// context usage attribution.
package core

import (
	"testing"
	"time"

	"github.com/ZoroSpace/pig/agent"
	"github.com/samber/lo"

	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/types"
)

func statsTestUsage(totalTokens int64) types.Usage {
	return types.Usage{
		Input:       totalTokens,
		TotalTokens: totalTokens,
	}
}

func statsTestAssistantMessage(text string, totalTokens int64, timestamp int64) types.AssistantMessage {
	model := testModel()
	return types.AssistantMessage{
		Role:       types.RoleAssistant,
		Content:    []types.Content{types.NewTextContent(text)},
		API:        model.API,
		Provider:   model.Provider,
		Model:      model.ID,
		Usage:      statsTestUsage(totalTokens),
		StopReason: types.StopReasonStop,
		Timestamp:  timestamp,
	}
}

func statsTestUserMessage(text string, timestamp int64) types.UserMessage {
	return types.UserMessage{
		Role:      types.RoleUser,
		Content:   types.UserContent{String: text},
		Timestamp: timestamp,
	}
}

func statsTestToolResultMessage(usage types.Usage) types.ToolResultMessage {
	return types.ToolResultMessage{
		Role:       types.RoleToolResult,
		ToolCallID: "tool-call-1",
		ToolName:   "test_tool",
		Content:    []types.Content{types.NewTextContent("tool result")},
		Usage:      &usage,
		Timestamp:  1,
	}
}

func createStatsSession(t *testing.T) (*AgentSession, *SessionManager) {
	t.Helper()
	session, sessionManager, _ := createAgentTestSession(t, &createAgentTestSessionOptions{
		GetAPIKey:     true,
		StreamFn:      defaultStreamFn,
		ThinkingLevel: "high",
	})
	return session, sessionManager
}

func syncAgentMessages(session *AgentSession, sessionManager *SessionManager) {
	session.Agent.SetMessages(sessionManager.BuildSessionContext().Messages)
}

func TestSessionStatsExposesContextUsageAlongsideTokenTotals(t *testing.T) {
	session, sessionManager := createStatsSession(t)

	_, _ = sessionManager.AppendMessage(statsTestUserMessage("hello", 1))
	_, _ = sessionManager.AppendMessage(statsTestAssistantMessage("hi", 200, 2))
	syncAgentMessages(session, sessionManager)

	stats := session.GetSessionStats()
	contextUsage := session.GetContextUsage()
	if stats.ContextUsage == nil || contextUsage == nil {
		t.Fatalf("contextUsage missing: %+v / %+v", stats.ContextUsage, contextUsage)
	}
	model := testModel()
	if *stats.ContextUsage.Tokens != *contextUsage.Tokens || stats.ContextUsage.Tokens == nil {
		t.Fatalf("tokens = %+v", stats.ContextUsage)
	}
	if *stats.ContextUsage.Tokens != 200 {
		t.Fatalf("tokens = %d, want 200", *stats.ContextUsage.Tokens)
	}
	if stats.ContextUsage.ContextWindow != model.ContextWindow {
		t.Fatalf("contextWindow = %d", stats.ContextUsage.ContextWindow)
	}
	wantPercent := (float64(200) / float64(model.ContextWindow)) * 100
	if *stats.ContextUsage.Percent != wantPercent {
		t.Fatalf("percent = %f, want %f", *stats.ContextUsage.Percent, wantPercent)
	}
}

func TestSessionStatsReportsUnknownContextUsageImmediatelyAfterCompaction(t *testing.T) {
	session, sessionManager := createStatsSession(t)

	_, _ = sessionManager.AppendMessage(statsTestUserMessage("first", 1))
	_, _ = sessionManager.AppendMessage(statsTestAssistantMessage("response1", 180000, 2))
	keptUserID, _ := sessionManager.AppendMessage(statsTestUserMessage("second", 3))
	_, _ = sessionManager.AppendMessage(statsTestAssistantMessage("response2", 195000, 4))
	if _, err := sessionManager.AppendCompaction("summary", keptUserID, 195000, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	_, _ = sessionManager.AppendMessage(statsTestUserMessage("third", 5))
	syncAgentMessages(session, sessionManager)

	stats := session.GetSessionStats()
	// Totals cover ALL entries, including history compacted away (180k + 195k).
	if stats.Tokens.Input != 375000 {
		t.Fatalf("input = %d, want 375000", stats.Tokens.Input)
	}
	if stats.ContextUsage == nil {
		t.Fatal("contextUsage missing")
	}
	if stats.ContextUsage.Tokens != nil {
		t.Fatalf("tokens = %v, want nil", stats.ContextUsage.Tokens)
	}
	if stats.ContextUsage.Percent != nil {
		t.Fatalf("percent = %v, want nil", stats.ContextUsage.Percent)
	}
}

func TestSessionStatsUsesPostCompactionUsageInsteadOfStaleKeptUsage(t *testing.T) {
	session, sessionManager := createStatsSession(t)

	_, _ = sessionManager.AppendMessage(statsTestUserMessage("first", 1))
	_, _ = sessionManager.AppendMessage(statsTestAssistantMessage("response1", 180000, 2))
	keptUserID, _ := sessionManager.AppendMessage(statsTestUserMessage("second", 3))
	_, _ = sessionManager.AppendMessage(statsTestAssistantMessage("response2", 195000, 4))
	if _, err := sessionManager.AppendCompaction("summary", keptUserID, 195000, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	_, _ = sessionManager.AppendMessage(statsTestUserMessage("third", 5))
	_, _ = sessionManager.AppendMessage(statsTestAssistantMessage("response3", 25000, 6))
	syncAgentMessages(session, sessionManager)

	stats := session.GetSessionStats()
	// Totals cover ALL entries, including history compacted away (180k + 195k + 25k).
	if stats.Tokens.Input != 400000 {
		t.Fatalf("input = %d, want 400000", stats.Tokens.Input)
	}
	if stats.ContextUsage == nil || stats.ContextUsage.Tokens == nil {
		t.Fatal("contextUsage tokens missing")
	}
	if *stats.ContextUsage.Tokens != 25000 {
		t.Fatalf("tokens = %d, want 25000", *stats.ContextUsage.Tokens)
	}
	model := testModel()
	wantPercent := (float64(25000) / float64(model.ContextWindow)) * 100
	if *stats.ContextUsage.Percent != wantPercent {
		t.Fatalf("percent = %f, want %f", *stats.ContextUsage.Percent, wantPercent)
	}
}

func TestSessionStatsIncludesBranchSummaryUsageInTotals(t *testing.T) {
	session, sessionManager := createStatsSession(t)

	usage := types.Usage{
		Input: 10, Output: 20, CacheRead: 30, CacheWrite: 40, TotalTokens: 100,
		Cost: types.Cost{Input: 0.1, Output: 0.2, CacheRead: 0.3, CacheWrite: 0.4, Total: 1},
	}
	if _, err := sessionManager.BranchWithSummary("", "summary", nil, lo.ToPtr(false), &usage); err != nil {
		t.Fatal(err)
	}
	syncAgentMessages(session, sessionManager)

	stats := session.GetSessionStats()
	if stats.Tokens.Input != 10 || stats.Tokens.Output != 20 || stats.Tokens.CacheRead != 30 || stats.Tokens.CacheWrite != 40 || stats.Tokens.Total != 100 {
		t.Fatalf("tokens = %+v", stats.Tokens)
	}
	if stats.Cost != 1 {
		t.Fatalf("cost = %f, want 1", stats.Cost)
	}
}

func TestSessionStatsIncludesCompactionUsageInTotals(t *testing.T) {
	session, sessionManager := createStatsSession(t)

	firstKeptEntryID, _ := sessionManager.AppendMessage(statsTestUserMessage("hello", 1))
	usage := types.Usage{
		Input: 10, Output: 20, CacheRead: 30, CacheWrite: 40, TotalTokens: 100,
		Cost: types.Cost{Input: 0.1, Output: 0.2, CacheRead: 0.3, CacheWrite: 0.4, Total: 1},
	}
	if _, err := sessionManager.AppendCompaction("summary", firstKeptEntryID, 100, nil, lo.ToPtr(false), &usage); err != nil {
		t.Fatal(err)
	}
	syncAgentMessages(session, sessionManager)

	stats := session.GetSessionStats()
	if stats.Tokens.Input != 10 || stats.Tokens.Output != 20 || stats.Tokens.CacheRead != 30 || stats.Tokens.CacheWrite != 40 || stats.Tokens.Total != 100 {
		t.Fatalf("tokens = %+v", stats.Tokens)
	}
	if stats.Cost != 1 {
		t.Fatalf("cost = %f, want 1", stats.Cost)
	}
}

func TestSessionStatsIncludesToolResultUsageInTotals(t *testing.T) {
	session, sessionManager := createStatsSession(t)

	usage := types.Usage{
		Input: 10, Output: 20, CacheRead: 30, CacheWrite: 40, TotalTokens: 100,
		Cost: types.Cost{Input: 0.1, Output: 0.2, CacheRead: 0.3, CacheWrite: 0.4, Total: 1},
	}
	if _, err := sessionManager.AppendMessage(statsTestToolResultMessage(usage)); err != nil {
		t.Fatal(err)
	}
	syncAgentMessages(session, sessionManager)

	stats := session.GetSessionStats()
	if stats.Tokens.Input != 10 || stats.Tokens.Output != 20 || stats.Tokens.CacheRead != 30 || stats.Tokens.CacheWrite != 40 || stats.Tokens.Total != 100 {
		t.Fatalf("tokens = %+v", stats.Tokens)
	}
	if stats.Cost != 1 {
		t.Fatalf("cost = %f, want 1", stats.Cost)
	}
}

func TestSessionStatsGroupsToolAndSummaryUsageSeparately(t *testing.T) {
	_, sessionManager := createStatsSession(t)

	rootID, _ := sessionManager.AppendMessage(statsTestUserMessage("hello", 1))
	response := statsTestAssistantMessage("response", 100, 2)
	response.Usage.Cost = types.Cost{Total: 0.5}
	_, _ = sessionManager.AppendMessage(response)
	toolResult := statsTestToolResultMessage(statsTestUsage(100))
	toolResult.Usage.Cost = types.Cost{Total: 1}
	_, _ = sessionManager.AppendMessage(toolResult)
	compactionUsage := statsTestUsage(100)
	compactionUsage.Cost = types.Cost{Total: 2}
	if _, err := sessionManager.AppendCompaction("summary", rootID, 100, nil, lo.ToPtr(false), &compactionUsage); err != nil {
		t.Fatal(err)
	}
	branchUsage := statsTestUsage(100)
	branchUsage.Cost = types.Cost{Total: 3}
	if _, err := sessionManager.BranchWithSummary("", "branch summary", nil, lo.ToPtr(false), &branchUsage); err != nil {
		t.Fatal(err)
	}

	model := testModel()
	breakdown := GetUsageCostBreakdown(sessionManager.GetEntries())
	if len(breakdown) != 2 {
		t.Fatalf("breakdown = %+v, want 2", breakdown)
	}
	// Sorted by cost descending.
	if breakdown[0].Key != "Tools/summaries" || breakdown[0].Cost != 6 || breakdown[0].Tokens != 300 {
		t.Fatalf("entry 0 = %+v", breakdown[0])
	}
	if breakdown[1].Key != string(model.Provider)+"/"+model.ID || breakdown[1].Cost != 0.5 || breakdown[1].Tokens != 100 {
		t.Fatalf("entry 1 = %+v", breakdown[1])
	}
}

func TestSessionStatsIgnoresZeroUsageMessagesForPostCompactionContextUsage(t *testing.T) {
	session, sessionManager := createStatsSession(t)

	_, _ = sessionManager.AppendMessage(statsTestUserMessage("first", 1))
	_, _ = sessionManager.AppendMessage(statsTestAssistantMessage("response1", 180000, 2))
	keptUserID, _ := sessionManager.AppendMessage(statsTestUserMessage("second", 3))
	_, _ = sessionManager.AppendMessage(statsTestAssistantMessage("response2", 195000, 4))
	if _, err := sessionManager.AppendCompaction("summary", keptUserID, 195000, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	_, _ = sessionManager.AppendMessage(statsTestUserMessage("third", 5))
	_, _ = sessionManager.AppendMessage(statsTestAssistantMessage("response3", 25000, 6))
	_, _ = sessionManager.AppendMessage(statsTestUserMessage("continue", 7))
	_, _ = sessionManager.AppendMessage(statsTestAssistantMessage("partial", 0, 8))
	syncAgentMessages(session, sessionManager)

	stats := session.GetSessionStats()
	if stats.ContextUsage == nil || stats.ContextUsage.Tokens == nil {
		t.Fatal("contextUsage tokens missing")
	}
	if *stats.ContextUsage.Tokens <= 25000 {
		t.Fatalf("tokens = %d, want > 25000", *stats.ContextUsage.Tokens)
	}
	_ = time.Now
	_ = agent.AgentTool{}
}
