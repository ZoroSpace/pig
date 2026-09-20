// pi: test/agent-session-compaction.test.ts — AgentSession compaction e2e
// (real LLM calls; no mocking).
//
// pi gates these on ANTHROPIC_OAUTH_TOKEN/ANTHROPIC_API_KEY; pig additionally
// skips explicitly because the anthropic-messages adapter is deferred (Phase 2
// decision, same pattern as compaction_test.go's LLM integration tests). The
// bodies are ported so they unlock with the adapter.
package core

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/types"
)

func e2eAPIKeyPresent() bool {
	return os.Getenv("ANTHROPIC_OAUTH_TOKEN") != "" || os.Getenv("ANTHROPIC_API_KEY") != ""
}

func createCompactionE2ESession(t *testing.T, inMemory bool) (*AgentSession, *SessionManager) {
	t.Helper()
	// pi: getModel("anthropic", "claude-sonnet-4-5") + streamSimple; pig uses
	// the test model (anthropic-mock shape) — the anthropic-messages adapter
	// unlocks this suite.
	session, sessionManager, settingsManager := createAgentTestSession(t, &createAgentTestSessionOptions{
		GetAPIKey:         true,
		StreamFn:          defaultStreamFn,
		SettingsOverrides: Settings{"compaction": map[string]any{"keepRecentTokens": float64(1)}},
	})
	_ = settingsManager
	_ = inMemory // pi: SessionManager.inMemory() for --no-session mode
	return session, sessionManager
}

func TestCompactionE2ETriggersManualCompactionViaCompact(t *testing.T) {
	if !e2eAPIKeyPresent() {
		t.Skip("ANTHROPIC_OAUTH_TOKEN/ANTHROPIC_API_KEY not set")
	}
	t.Skip("anthropic-messages adapter not yet ported (Phase 2 deferral, see §4)")

	session, _ := createCompactionE2ESession(t, false)

	// Send a few prompts to build up history
	if err := session.Prompt(t.Context(), "What is 2+2? Reply with just the number.", nil); err != nil {
		t.Fatal(err)
	}
	session.Agent.WaitForIdle()
	if err := session.Prompt(t.Context(), "What is 3+3? Reply with just the number.", nil); err != nil {
		t.Fatal(err)
	}
	session.Agent.WaitForIdle()

	// Manually compact
	result, err := session.Compact(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}

	if result.Summary == "" || len(result.Summary) == 0 {
		t.Fatal("summary missing")
	}
	if len(result.Summary) <= 0 {
		t.Fatal("summary length = 0")
	}
	if result.TokensBefore <= 0 {
		t.Fatalf("tokensBefore = %d", result.TokensBefore)
	}

	// Verify messages were compacted (should have summary + recent)
	messages := session.Messages()
	if len(messages) == 0 {
		t.Fatal("no messages after compaction")
	}
	// First message should be the summary (a compactionSummary message)
	if messages[0].MessageRole() != "compactionSummary" {
		t.Fatalf("first message role = %q", messages[0].MessageRole())
	}
}

func TestCompactionE2EMaintainsValidSessionStateAfterCompaction(t *testing.T) {
	if !e2eAPIKeyPresent() {
		t.Skip("ANTHROPIC_OAUTH_TOKEN/ANTHROPIC_API_KEY not set")
	}
	t.Skip("anthropic-messages adapter not yet ported (Phase 2 deferral, see §4)")

	session, _ := createCompactionE2ESession(t, false)

	if err := session.Prompt(t.Context(), "What is the capital of France? One word answer.", nil); err != nil {
		t.Fatal(err)
	}
	session.Agent.WaitForIdle()
	if err := session.Prompt(t.Context(), "What is the capital of Germany? One word answer.", nil); err != nil {
		t.Fatal(err)
	}
	session.Agent.WaitForIdle()

	// Compact
	if _, err := session.Compact(t.Context(), ""); err != nil {
		t.Fatal(err)
	}

	// Session should still be usable
	if err := session.Prompt(t.Context(), "What is the capital of Italy? One word answer.", nil); err != nil {
		t.Fatal(err)
	}
	session.Agent.WaitForIdle()

	// Should have messages after compaction
	if len(session.Messages()) == 0 {
		t.Fatal("no messages after compaction")
	}

	// The agent should have responded
	assistantMessages := 0
	for _, message := range session.Messages() {
		if message.MessageRole() == "assistant" {
			assistantMessages++
		}
	}
	if assistantMessages == 0 {
		t.Fatal("no assistant messages")
	}
}

func TestCompactionE2EPersistsCompactionToSessionFile(t *testing.T) {
	if !e2eAPIKeyPresent() {
		t.Skip("ANTHROPIC_OAUTH_TOKEN/ANTHROPIC_API_KEY not set")
	}
	t.Skip("anthropic-messages adapter not yet ported (Phase 2 deferral, see §4)")

	session, sessionManager := createCompactionE2ESession(t, false)

	if err := session.Prompt(t.Context(), "Say hello", nil); err != nil {
		t.Fatal(err)
	}
	session.Agent.WaitForIdle()
	if err := session.Prompt(t.Context(), "Say goodbye", nil); err != nil {
		t.Fatal(err)
	}
	session.Agent.WaitForIdle()

	// Compact
	if _, err := session.Compact(t.Context(), ""); err != nil {
		t.Fatal(err)
	}

	// Load entries from session manager
	entries := sessionManager.GetEntries()

	// Should have a compaction entry
	var compactionEntries []*CompactionEntry
	for _, entry := range entries {
		if compactionEntry, ok := entry.(*CompactionEntry); ok {
			compactionEntries = append(compactionEntries, compactionEntry)
		}
	}
	if len(compactionEntries) != 1 {
		t.Fatalf("compaction entries = %d, want 1", len(compactionEntries))
	}
	compaction := compactionEntries[0]
	if compaction.Summary == "" {
		t.Fatal("summary empty")
	}
	if compaction.FirstKeptEntryID == "" {
		t.Fatal("firstKeptEntryId empty")
	}
	if compaction.TokensBefore <= 0 {
		t.Fatalf("tokensBefore = %d", compaction.TokensBefore)
	}
}

func TestCompactionE2EWorksWithNoSessionMode(t *testing.T) {
	if !e2eAPIKeyPresent() {
		t.Skip("ANTHROPIC_OAUTH_TOKEN/ANTHROPIC_API_KEY not set")
	}
	t.Skip("anthropic-messages adapter not yet ported (Phase 2 deferral, see §4)")

	session, sessionManager := createCompactionE2ESession(t, true) // in-memory mode

	if err := session.Prompt(t.Context(), "What is 2+2? Reply with just the number.", nil); err != nil {
		t.Fatal(err)
	}
	session.Agent.WaitForIdle()
	if err := session.Prompt(t.Context(), "What is 3+3? Reply with just the number.", nil); err != nil {
		t.Fatal(err)
	}
	session.Agent.WaitForIdle()

	// Compact should work even without file persistence
	result, err := session.Compact(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	if result.Summary == "" {
		t.Fatal("summary empty")
	}

	// In-memory entries should have the compaction
	entries := sessionManager.GetEntries()
	compactionEntries := 0
	for _, entry := range entries {
		if _, ok := entry.(*CompactionEntry); ok {
			compactionEntries++
		}
	}
	if compactionEntries != 1 {
		t.Fatalf("compaction entries = %d, want 1", compactionEntries)
	}
}

func TestCompactionE2EEmitsCompactionEventsDuringManualCompaction(t *testing.T) {
	if !e2eAPIKeyPresent() {
		t.Skip("ANTHROPIC_OAUTH_TOKEN/ANTHROPIC_API_KEY not set")
	}
	t.Skip("anthropic-messages adapter not yet ported (Phase 2 deferral, see §4)")

	session, _ := createCompactionE2ESession(t, false)
	var events []AgentSessionEvent
	session.Subscribe(func(event *AgentSessionEvent) {
		events = append(events, *event)
	})

	// Build some history
	if err := session.Prompt(t.Context(), "Say hello", nil); err != nil {
		t.Fatal(err)
	}
	session.Agent.WaitForIdle()

	// Manually trigger compaction and check events
	if _, err := session.Compact(t.Context(), ""); err != nil {
		t.Fatal(err)
	}

	var compactionEvents []AgentSessionEvent
	for _, event := range events {
		if event.Type == AgentSessionEventCompactionStart || event.Type == AgentSessionEventCompactionEnd {
			compactionEvents = append(compactionEvents, event)
		}
	}
	if len(compactionEvents) != 2 {
		t.Fatalf("compaction events = %d, want 2", len(compactionEvents))
	}
	if compactionEvents[0].Type != AgentSessionEventCompactionStart || compactionEvents[0].Reason != "manual" {
		t.Fatalf("first event = %+v", compactionEvents[0])
	}
	if compactionEvents[1].Type != AgentSessionEventCompactionEnd || compactionEvents[1].Reason != "manual" || compactionEvents[1].Aborted {
		t.Fatalf("second event = %+v", compactionEvents[1])
	}

	// Regular events should have been emitted
	messageEndEvents := 0
	for _, event := range events {
		if event.Type == "message_end" {
			messageEndEvents++
		}
	}
	if messageEndEvents == 0 {
		t.Fatal("no message_end events")
	}
}

func TestCompactDoesNotPersistLengthLimitedSummary(t *testing.T) {
	// pi regression #7048: a length-stop summary contains partial text and
	// must fail instead of becoming a session checkpoint.
	session, sessionManager, settingsManager := createAgentTestSession(t, &createAgentTestSessionOptions{
		GetAPIKey: true,
		StreamFn: func(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
			stream := newMockAssistantStream()
			msg := testAssistantMessage("partial summar", withStopReason(types.StopReasonLength), withModelRef(model))
			stream.Push(types.AssistantMessageEvent{Type: types.EventStart, Partial: &msg})
			stream.Push(types.AssistantMessageEvent{Type: types.EventDone, Reason: types.StopReasonLength, Message: &msg})
			stream.End()
			return stream.AssistantMessageEventStream
		},
	})
	settingsManager.ApplyOverrides(Settings{"compaction": map[string]any{"keepRecentTokens": float64(1)}})

	now := time.Now().UnixMilli()
	if _, err := sessionManager.AppendMessage(types.UserMessage{
		Role:      types.RoleUser,
		Content:   types.UserContent{Parts: []types.Content{types.NewTextContent("message to compact")}},
		Timestamp: now - 1000,
	}); err != nil {
		t.Fatal(err)
	}
	assistant := testAssistantMessage("assistant response to compact", func(m *types.AssistantMessage) {
		m.Timestamp = now - 500
		m.Usage = types.Usage{
			Input:       100,
			Output:      0,
			TotalTokens: 100,
			Cost:        types.Cost{},
		}
	})
	if _, err := sessionManager.AppendMessage(assistant); err != nil {
		t.Fatal(err)
	}
	state := session.Agent.State()
	state.Messages = sessionManager.BuildSessionContext().Messages

	_, err := session.Compact(context.Background(), "")
	if err == nil || !strings.Contains(err.Error(), "generation hit the token cap") {
		t.Fatalf("err = %v, want token cap rejection", err)
	}
	compactions := 0
	for _, entry := range sessionManager.GetEntries() {
		if _, ok := entry.(*CompactionEntry); ok {
			compactions++
		}
	}
	if compactions != 0 {
		t.Fatalf("compaction entries = %d, want 0", compactions)
	}
}

func TestCompactsAfterToolResultBeforeNextAssistantRequest(t *testing.T) {
	// pi (v0.85.1): _compactBeforeNextAssistantResponse runs threshold
	// compaction between a tool result and the resumed assistant request
	// inside the same agent run.
	toolResultText := "large-tool-result:" + strings.Repeat("x", 6800)
	var mu sync.Mutex
	var order []string
	var resumedRequest string

	largeTool := agent.AgentTool{
		Tool: types.Tool{
			Name:        "large_result",
			Description: "Returns enough content to cross the compaction threshold",
			Parameters:  rawJSON(`{"type":"object","properties":{}}`),
		},
		Label: "Large result",
		Execute: func(ctx context.Context, toolCallID string, params any, onUpdate agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
			return agent.AgentToolResult{
				Content: []types.Content{types.NewTextContent(toolResultText)},
				Details: map[string]any{},
			}, nil
		},
	}

	requestCount := 0
	session, _, _ := createAgentTestSession(t, &createAgentTestSessionOptions{
		GetAPIKey:         true,
		BaseToolsOverride: map[string]agent.AgentTool{"large_result": largeTool},
		Model: func() *types.Model {
			m := testModel()
			m.ContextWindow = 2000
			return m
		}(),
		SettingsOverrides: Settings{"compaction": map[string]any{"enabled": true, "reserveTokens": float64(400), "keepRecentTokens": float64(1750)}},
		StreamFn: func(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
			mu.Lock()
			requestCount++
			n := requestCount
			mu.Unlock()
			stream := newMockAssistantStream()
			go func() {
				switch n {
				case 1:
					msg := testAssistantMessage("old-history:"+strings.Repeat("a", 800), withModelRef(model))
					stream.Push(types.AssistantMessageEvent{Type: types.EventStart, Partial: &msg})
					stream.Push(types.AssistantMessageEvent{Type: types.EventDone, Reason: types.StopReasonStop, Message: &msg})
				case 2:
					msg := testAssistantMessage("recent-history:"+strings.Repeat("b", 800), withModelRef(model))
					stream.Push(types.AssistantMessageEvent{Type: types.EventStart, Partial: &msg})
					stream.Push(types.AssistantMessageEvent{Type: types.EventDone, Reason: types.StopReasonStop, Message: &msg})
				case 3:
					message := testAssistantMessage("", withModelRef(model), withStopReason(types.StopReasonToolUse))
					message.Content = []types.Content{
						types.ToolCall{Kind: types.KindToolCall, ID: "toolu_large", Name: "large_result", Arguments: map[string]any{}},
					}
					partial := message
					partial.Content = nil
					stream.Push(types.AssistantMessageEvent{Type: types.EventStart, Partial: &partial})
					stream.Push(types.AssistantMessageEvent{Type: types.EventDone, Reason: types.StopReasonToolUse, Message: &message})
				default:
					mu.Lock()
					data, _ := json.Marshal(llmContext.Messages)
					resumedRequest = string(data)
					mu.Unlock()
					msg := testAssistantMessage("finished after compaction", withModelRef(model))
					stream.Push(types.AssistantMessageEvent{Type: types.EventStart, Partial: &msg})
					stream.Push(types.AssistantMessageEvent{Type: types.EventDone, Reason: types.StopReasonStop, Message: &msg})
				}
				stream.End()
			}()
			return stream.AssistantMessageEventStream
		},
		ExtensionsResult: []*LoadExtensionsResult{{
			Extensions: []*Extension{
				{
					Path:     "compaction-stub",
					Handlers: map[string][]ExtensionHandler{},
				},
			},
			Runtime: CreateExtensionRuntime(),
		}},
	})
	session.extensionRunner.extensions[0].Handlers["session_before_compact"] = []ExtensionHandler{
		func(ctx context.Context, extCtx *ExtensionContext, event ExtensionEvent) (any, error) {
			compactionEvent, ok := event.(*SessionBeforeCompactEvent)
			if !ok || compactionEvent.Preparation == nil {
				return nil, fmt.Errorf("unexpected event %T", event)
			}
			mu.Lock()
			order = append(order, "compaction")
			mu.Unlock()
			return &SessionBeforeCompactResult{
				Compaction: &CompactionResult{
					Summary:          "compacted history",
					FirstKeptEntryID: compactionEvent.Preparation.FirstKeptEntryID,
					TokensBefore:     compactionEvent.Preparation.TokensBefore,
					Details:          map[string]any{},
				},
			}, nil
		},
	}

	if err := session.Prompt(context.Background(), "seed old history", nil); err != nil {
		t.Fatal(err)
	}
	session.Agent.WaitForIdle()
	if err := session.Prompt(context.Background(), "seed recent history", nil); err != nil {
		t.Fatal(err)
	}
	session.Agent.WaitForIdle()

	if err := session.Prompt(context.Background(), "run the large tool", nil); err != nil {
		t.Fatal(err)
	}
	session.Agent.WaitForIdle()

	mu.Lock()
	defer mu.Unlock()
	if strings.Join(order, ",") != "compaction" {
		t.Fatalf("order = %v, want [compaction]", order)
	}
	if !strings.Contains(resumedRequest, "compacted history") {
		t.Fatalf("resumed request missing compacted summary: %q", resumedRequest)
	}
	if !strings.Contains(resumedRequest, "large-tool-result") {
		t.Fatalf("resumed request missing the kept tool result: %q", resumedRequest)
	}
	if session.GetLastAssistantText() != "finished after compaction" {
		t.Fatalf("last assistant text = %q", session.GetLastAssistantText())
	}
}
