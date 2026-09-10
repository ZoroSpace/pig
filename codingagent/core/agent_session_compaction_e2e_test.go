// pi: test/agent-session-compaction.test.ts — AgentSession compaction e2e
// (real LLM calls; no mocking).
//
// pi gates these on ANTHROPIC_OAUTH_TOKEN/ANTHROPIC_API_KEY; pig additionally
// skips explicitly because the anthropic-messages adapter is deferred (Phase 2
// decision, same pattern as compaction_test.go's LLM integration tests). The
// bodies are ported so they unlock with the adapter.
package core

import (
	"os"
	"testing"

	"github.com/ZoroSpace/pig/ai"
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
		StreamFn:          ai.StreamSimple,
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
