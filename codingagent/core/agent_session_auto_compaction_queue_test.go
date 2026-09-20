// pi: test/agent-session-auto-compaction-queue.test.ts — auto-compaction
// queue resume and stale-usage guards. pi spies on the private
// _runAutoCompaction/_checkCompaction methods; pig overwrites the
// runAutoCompactionFn seam (same package).
package core

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/samber/lo"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/agent/harness"
	"github.com/ZoroSpace/pig/ai/types"
)

// createAutoCompactionQueueSession mirrors the suite's beforeEach: a session
// whose agent uses the compat streamSimple default (pi: streamFn: streamSimple).
func createAutoCompactionQueueSession(t *testing.T) (*AgentSession, *SessionManager, *SettingsManager) {
	t.Helper()
	session, sessionManager, settingsManager := createAgentTestSession(t, &createAgentTestSessionOptions{
		GetAPIKey: true,
		StreamFn:  defaultStreamFn,
	})
	return session, sessionManager, settingsManager
}

// compactingStream builds the "compacted" summary stream the suite uses (pi:
// createAssistantMessageEventStream + fauxAssistantMessage).
func compactingStream(model *types.Model) *types.AssistantMessageEventStream {
	stream := newMockAssistantStream()
	message := testAssistantMessage("compacted", withModelRef(model), withUsage(types.Usage{Input: 10, TotalTokens: 10}))
	stream.Push(types.AssistantMessageEvent{Type: types.EventDone, Reason: types.StopReasonStop, Message: &message})
	return stream.AssistantMessageEventStream
}

func TestAutoCompactionQueueResumesAfterThresholdCompactionWithAgentQueuedMessages(t *testing.T) {
	session, sessionManager, settingsManager := createAutoCompactionQueueSession(t)
	settingsManager.ApplyOverrides(Settings{"compaction": map[string]any{"keepRecentTokens": float64(1)}})
	model := session.Model()
	now := time.Now().UnixMilli()
	if _, err := sessionManager.AppendMessage(types.UserMessage{
		Role:      types.RoleUser,
		Content:   types.UserContent{Parts: []types.Content{types.NewTextContent("message to compact")}},
		Timestamp: now - 1000,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := sessionManager.AppendMessage(types.AssistantMessage{
		Role:       types.RoleAssistant,
		Content:    []types.Content{types.NewTextContent("assistant response to compact")},
		API:        model.API,
		Provider:   model.Provider,
		Model:      model.ID,
		Usage:      types.Usage{Input: 100, TotalTokens: 100},
		StopReason: types.StopReasonStop,
		Timestamp:  now - 500,
	}); err != nil {
		t.Fatal(err)
	}
	session.Agent.SetMessages(sessionManager.BuildSessionContext().Messages)
	session.Agent.StreamFunction = func(ctx context.Context, summaryModel *types.Model, llmContext *types.TranscriptContext, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
		return compactingStream(summaryModel)
	}

	session.Agent.FollowUp(harness.CustomMessage{
		Role:       "custom",
		CustomType: "test",
		Content:    []types.Content{types.NewTextContent("Queued custom")},
		Display:    false,
		Timestamp:  time.Now().UnixMilli(),
	})

	if session.PendingMessageCount() != 0 {
		t.Fatalf("pendingMessageCount = %d", session.PendingMessageCount())
	}
	if !session.Agent.HasQueuedMessages() {
		t.Fatal("agent has no queued messages")
	}

	// pi spies agent.continue to assert it is never called; runAutoCompaction
	// never invokes Continue in pig either (the caller does).
	shouldContinue, err := session.runAutoCompactionFn(context.Background(), "threshold", false)
	if err != nil {
		t.Fatal(err)
	}
	if !shouldContinue {
		t.Fatal("runAutoCompaction = false, want true")
	}
}

func TestAutoCompactionQueueDoesNotCompactRepeatedlyAfterOverflowRecoveryAttempted(t *testing.T) {
	session, _, _ := createAutoCompactionQueueSession(t)
	model := session.Model()
	overflowMessage := types.AssistantMessage{
		Role:         types.RoleAssistant,
		Content:      []types.Content{types.NewTextContent("")},
		API:          model.API,
		Provider:     model.Provider,
		Model:        model.ID,
		Usage:        types.Usage{},
		StopReason:   types.StopReasonError,
		ErrorMessage: "prompt is too long",
		Timestamp:    time.Now().UnixMilli(),
	}

	runAutoCompactionCalls := 0
	var mu sync.Mutex
	session.runAutoCompactionFn = func(ctx context.Context, reason string, willRetry bool) (bool, error) {
		mu.Lock()
		runAutoCompactionCalls++
		mu.Unlock()
		return false, nil
	}

	var events []AgentSessionEvent
	session.Subscribe(func(event *AgentSessionEvent) {
		if event.Type == AgentSessionEventCompactionEnd {
			mu.Lock()
			events = append(events, *event)
			mu.Unlock()
		}
	})

	if _, err := session.checkCompaction(context.Background(), &overflowMessage, true); err != nil {
		t.Fatal(err)
	}
	second := overflowMessage
	second.Timestamp = time.Now().UnixMilli() + 1
	if _, err := session.checkCompaction(context.Background(), &second, true); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	if runAutoCompactionCalls != 1 {
		t.Fatalf("runAutoCompaction calls = %d, want 1", runAutoCompactionCalls)
	}
	found := false
	for _, event := range events {
		if event.Reason == "overflow" &&
			event.ErrorMessage == "Context overflow recovery failed after one compact-and-retry attempt. Try reducing context or switching to a larger-context model." {
			found = true
		}
	}
	if !found {
		t.Fatalf("events = %+v", events)
	}
}

func TestAutoCompactionQueueIgnoresStalePreCompactionAssistantUsageOnPrePromptChecks(t *testing.T) {
	session, sessionManager, _ := createAutoCompactionQueueSession(t)
	model := session.Model()
	staleAssistantTimestamp := time.Now().UnixMilli() - 10000
	staleAssistant := types.AssistantMessage{
		Role:       types.RoleAssistant,
		Content:    []types.Content{types.NewTextContent("large response before compaction")},
		API:        model.API,
		Provider:   model.Provider,
		Model:      model.ID,
		Usage:      types.Usage{Input: 600000, Output: 10000, TotalTokens: 610000},
		StopReason: types.StopReasonStop,
		Timestamp:  staleAssistantTimestamp,
	}

	if _, err := sessionManager.AppendMessage(types.UserMessage{
		Role:      types.RoleUser,
		Content:   types.UserContent{Parts: []types.Content{types.NewTextContent("before compaction")}},
		Timestamp: staleAssistantTimestamp - 1000,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := sessionManager.AppendMessage(staleAssistant); err != nil {
		t.Fatal(err)
	}

	firstKeptEntryID := sessionManager.GetEntries()[0].Base().ID
	if _, err := sessionManager.AppendCompaction("summary", firstKeptEntryID, staleAssistant.Usage.TotalTokens, nil, lo.ToPtr(false), nil); err != nil {
		t.Fatal(err)
	}

	if _, err := sessionManager.AppendMessage(types.UserMessage{
		Role:      types.RoleUser,
		Content:   types.UserContent{Parts: []types.Content{types.NewTextContent("session recovery payload")}},
		Timestamp: time.Now().UnixMilli(),
	}); err != nil {
		t.Fatal(err)
	}

	runAutoCompactionCalls := 0
	var mu sync.Mutex
	session.runAutoCompactionFn = func(ctx context.Context, reason string, willRetry bool) (bool, error) {
		mu.Lock()
		runAutoCompactionCalls++
		mu.Unlock()
		return false, nil
	}

	if _, err := session.checkCompaction(context.Background(), &staleAssistant, false); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	if runAutoCompactionCalls != 0 {
		t.Fatalf("runAutoCompaction calls = %d, want 0", runAutoCompactionCalls)
	}
}

func TestAutoCompactionQueueTriggersThresholdCompactionForErrorMessagesUsingLastSuccessfulUsage(t *testing.T) {
	session, _, settingsManager := createAutoCompactionQueueSession(t)
	model := session.Model()

	// A successful assistant message with token usage just over the compaction
	// threshold, computed from the model so catalog changes do not break it.
	compactionSettings := settingsManager.GetCompactionSettings()
	contextWindow := model.ContextWindow
	if contextWindow == 0 {
		contextWindow = 200000
	}
	thresholdTokens := contextWindow - compactionSettings.ReserveTokens + 1
	successfulAssistant := types.AssistantMessage{
		Role:       types.RoleAssistant,
		Content:    []types.Content{types.NewTextContent("large successful response")},
		API:        model.API,
		Provider:   model.Provider,
		Model:      model.ID,
		Usage:      types.Usage{Input: thresholdTokens - 10000, Output: 10000, TotalTokens: thresholdTokens},
		StopReason: types.StopReasonStop,
		Timestamp:  time.Now().UnixMilli(),
	}
	errorAssistant := types.AssistantMessage{
		Role:         types.RoleAssistant,
		Content:      []types.Content{types.NewTextContent("")},
		API:          model.API,
		Provider:     model.Provider,
		Model:        model.ID,
		Usage:        types.Usage{},
		StopReason:   types.StopReasonError,
		ErrorMessage: "529 overloaded",
		Timestamp:    time.Now().UnixMilli() + 1000,
	}

	session.Agent.SetMessages([]agent.AgentMessage{
		types.UserMessage{Role: types.RoleUser, Content: types.UserContent{Parts: []types.Content{types.NewTextContent("hello")}}, Timestamp: time.Now().UnixMilli() - 1000},
		successfulAssistant,
		types.UserMessage{Role: types.RoleUser, Content: types.UserContent{Parts: []types.Content{types.NewTextContent("another prompt")}}, Timestamp: time.Now().UnixMilli() + 500},
		errorAssistant,
	})

	var mu sync.Mutex
	var reasons []string
	session.runAutoCompactionFn = func(ctx context.Context, reason string, willRetry bool) (bool, error) {
		mu.Lock()
		reasons = append(reasons, reason)
		mu.Unlock()
		return false, nil
	}

	if _, err := session.checkCompaction(context.Background(), &errorAssistant, true); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(reasons) != 1 || reasons[0] != "threshold" {
		t.Fatalf("runAutoCompaction reasons = %v, want [threshold]", reasons)
	}
}

func TestAutoCompactionQueueNoThresholdCompactionForErrorMessagesWithoutPriorUsage(t *testing.T) {
	session, _, _ := createAutoCompactionQueueSession(t)
	model := session.Model()

	errorAssistant := types.AssistantMessage{
		Role:         types.RoleAssistant,
		Content:      []types.Content{types.NewTextContent("")},
		API:          model.API,
		Provider:     model.Provider,
		Model:        model.ID,
		Usage:        types.Usage{},
		StopReason:   types.StopReasonError,
		ErrorMessage: "529 overloaded",
		Timestamp:    time.Now().UnixMilli(),
	}

	session.Agent.SetMessages([]agent.AgentMessage{
		types.UserMessage{Role: types.RoleUser, Content: types.UserContent{Parts: []types.Content{types.NewTextContent("hello")}}, Timestamp: time.Now().UnixMilli() - 1000},
		errorAssistant,
	})

	runAutoCompactionCalls := 0
	var mu sync.Mutex
	session.runAutoCompactionFn = func(ctx context.Context, reason string, willRetry bool) (bool, error) {
		mu.Lock()
		runAutoCompactionCalls++
		mu.Unlock()
		return false, nil
	}

	if _, err := session.checkCompaction(context.Background(), &errorAssistant, true); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	if runAutoCompactionCalls != 0 {
		t.Fatalf("runAutoCompaction calls = %d, want 0", runAutoCompactionCalls)
	}
}

func TestAutoCompactionQueueNoThresholdCompactionWhenOnlyKeptPreCompactionUsageExists(t *testing.T) {
	session, sessionManager, _ := createAutoCompactionQueueSession(t)
	model := session.Model()
	preCompactionTimestamp := time.Now().UnixMilli() - 10000

	keptAssistant := types.AssistantMessage{
		Role:       types.RoleAssistant,
		Content:    []types.Content{types.NewTextContent("kept response from before compaction")},
		API:        model.API,
		Provider:   model.Provider,
		Model:      model.ID,
		Usage:      types.Usage{Input: 180000, Output: 10000, TotalTokens: 190000},
		StopReason: types.StopReasonStop,
		Timestamp:  preCompactionTimestamp,
	}

	if _, err := sessionManager.AppendMessage(types.UserMessage{
		Role:      types.RoleUser,
		Content:   types.UserContent{Parts: []types.Content{types.NewTextContent("before compaction")}},
		Timestamp: preCompactionTimestamp - 1000,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := sessionManager.AppendMessage(keptAssistant); err != nil {
		t.Fatal(err)
	}
	firstKeptEntryID := sessionManager.GetEntries()[0].Base().ID
	if _, err := sessionManager.AppendCompaction("summary", firstKeptEntryID, keptAssistant.Usage.TotalTokens, nil, lo.ToPtr(false), nil); err != nil {
		t.Fatal(err)
	}

	errorAssistant := types.AssistantMessage{
		Role:         types.RoleAssistant,
		Content:      []types.Content{types.NewTextContent("")},
		API:          model.API,
		Provider:     model.Provider,
		Model:        model.ID,
		Usage:        types.Usage{},
		StopReason:   types.StopReasonError,
		ErrorMessage: "529 overloaded",
		Timestamp:    time.Now().UnixMilli(),
	}

	session.Agent.SetMessages([]agent.AgentMessage{
		types.UserMessage{Role: types.RoleUser, Content: types.UserContent{Parts: []types.Content{types.NewTextContent("kept user msg")}}, Timestamp: preCompactionTimestamp - 1000},
		keptAssistant,
		types.UserMessage{Role: types.RoleUser, Content: types.UserContent{Parts: []types.Content{types.NewTextContent("new prompt")}}, Timestamp: time.Now().UnixMilli() - 500},
		errorAssistant,
	})

	runAutoCompactionCalls := 0
	var mu sync.Mutex
	session.runAutoCompactionFn = func(ctx context.Context, reason string, willRetry bool) (bool, error) {
		mu.Lock()
		runAutoCompactionCalls++
		mu.Unlock()
		return false, nil
	}

	if _, err := session.checkCompaction(context.Background(), &errorAssistant, true); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	if runAutoCompactionCalls != 0 {
		t.Fatalf("runAutoCompaction calls = %d, want 0", runAutoCompactionCalls)
	}
	_ = strings.TrimSpace
}
