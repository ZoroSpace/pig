// interactive_mode_entry_appended_test.go — pig-side regression for the
// v0.87.0 entry_appended handler (pi has no dedicated test file; the
// boundary-compaction rebuild, the custom_message display branch and the
// double-render guard are pure UI logic). Also pins the value/pointer
// double-form fix in addMessageToChat (docs/fix/2026-09-11 precedent).
package interactive

import (
	"context"
	"strings"
	"testing"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/codingagent/core"
	"github.com/ZoroSpace/pig/codingagent/modes/interactive/components"
	codingagentutils "github.com/ZoroSpace/pig/codingagent/utils"
)

// chatChildrenPlainText renders every chat child and strips ANSI.
func chatChildrenPlainText(t *testing.T, m *InteractiveMode) string {
	t.Helper()
	var builder strings.Builder
	for _, child := range m.chatContainer.Children {
		for _, line := range child.Render(120) {
			builder.WriteString(line)
			builder.WriteByte('\n')
		}
	}
	return codingagentutils.StripAnsi(builder.String())
}

func countOccurrences(haystack, needle string) int {
	return strings.Count(haystack, needle)
}

// TestEntryAppendedBoundaryCompaction drives the v0.87.0 compaction branch
// of entry_appended: the chat rebuilds around the boundary compaction
// (retained entries before it, the summary, entries appended after it), the
// after-entries are marked so their own entry_appended events skip
// re-rendering, and the summary actually renders (value/pointer double-form
// fix in addMessageToChat).
func TestEntryAppendedBoundaryCompaction(t *testing.T) {
	initTestTheme(t)
	session := createInteractiveTestSession(t, &fixtureResourceLoader{}, nil)
	mode := newSeamTestMode(session, nil)
	mode.isInitialized = true
	mode.footer = components.NewFooterComponent(session, nil)
	mode.pendingTools = map[string]*components.ToolExecutionComponent{}

	user1ID, err := session.SessionManager.AppendMessage(types.UserMessage{
		Role:    types.RoleUser,
		Content: types.UserContent{String: "first hello"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.SessionManager.AppendMessage(types.AssistantMessage{
		Role:       types.RoleAssistant,
		Content:    []types.Content{types.NewTextContent("first reply")},
		API:        "openai-completions",
		Provider:   "test",
		Model:      "test-model",
		Usage:      types.Usage{Cost: types.Cost{}},
		StopReason: types.StopReasonStop,
	}); err != nil {
		t.Fatal(err)
	}
	compactionID, err := session.SessionManager.AppendCompaction("the summary so far", &user1ID, 12345, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Appended after the compaction, like the real boundary flow: the
	// events fire once every draft entry is already in the branch.
	user2ID, err := session.SessionManager.AppendMessage(types.UserMessage{
		Role:    types.RoleUser,
		Content: types.UserContent{String: "after compaction"},
	})
	if err != nil {
		t.Fatal(err)
	}
	compactionEntry := session.SessionManager.GetEntry(compactionID)
	if compactionEntry == nil {
		t.Fatal("compaction entry missing")
	}

	mode.handleEvent(context.Background(), &core.AgentSessionEvent{
		AgentEvent: agent.AgentEvent{Type: core.AgentSessionEventEntryAppended},
		Entry:      compactionEntry,
	})

	plain := chatChildrenPlainText(t, mode)
	if !strings.Contains(plain, "first hello") {
		t.Fatalf("retained entry missing from rebuilt chat:\n%s", plain)
	}
	if !strings.Contains(plain, "after compaction") {
		t.Fatalf("post-compaction entry missing from rebuilt chat:\n%s", plain)
	}
	if got := countOccurrences(plain, "after compaction"); got != 1 {
		t.Fatalf("post-compaction entry rendered %d times, want 1:\n%s", got, plain)
	}
	if !strings.Contains(plain, "Compacted from 12,345 tokens") {
		t.Fatalf("compaction summary missing from rebuilt chat (double-form drop?):\n%s", plain)
	}
	if !mode.entriesRenderedByBoundaryCompaction[user2ID] {
		t.Fatalf("post-compaction entry %s not marked as rendered", user2ID)
	}

	// The user2 entry_appended event arrives right after the compaction's
	// own (commitBoundaryDrafts order) and must be skipped.
	mode.handleEvent(context.Background(), &core.AgentSessionEvent{
		AgentEvent: agent.AgentEvent{Type: core.AgentSessionEventEntryAppended},
		Entry:      session.SessionManager.GetEntry(user2ID),
	})
	plain = chatChildrenPlainText(t, mode)
	if got := countOccurrences(plain, "after compaction"); got != 1 {
		t.Fatalf("post-compaction entry rendered %d times after its own event, want 1:\n%s", got, plain)
	}
}

// TestEntryAppendedCustomMessageDisplay drives the v0.87.0 custom_message
// branch: display=true renders through createCustomMessage, display=false
// stays hidden.
func TestEntryAppendedCustomMessageDisplay(t *testing.T) {
	initTestTheme(t)
	session := createInteractiveTestSession(t, &fixtureResourceLoader{}, nil)
	mode := newSeamTestMode(session, nil)
	mode.isInitialized = true
	mode.footer = components.NewFooterComponent(session, nil)
	mode.pendingTools = map[string]*components.ToolExecutionComponent{}

	entryID, err := session.SessionManager.AppendCustomMessageEntry(
		"note", "custom probe text", true, map[string]any{"k": "v"},
	)
	if err != nil {
		t.Fatal(err)
	}

	mode.handleEvent(context.Background(), &core.AgentSessionEvent{
		AgentEvent: agent.AgentEvent{Type: core.AgentSessionEventEntryAppended},
		Entry:      session.SessionManager.GetEntry(entryID),
	})
	plain := chatChildrenPlainText(t, mode)
	if !strings.Contains(plain, "custom probe text") {
		t.Fatalf("displayed custom message missing from chat (double-form drop?):\n%s", plain)
	}

	hiddenID, err := session.SessionManager.AppendCustomMessageEntry(
		"note", "hidden probe text", false, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	mode.handleEvent(context.Background(), &core.AgentSessionEvent{
		AgentEvent: agent.AgentEvent{Type: core.AgentSessionEventEntryAppended},
		Entry:      session.SessionManager.GetEntry(hiddenID),
	})
	plain = chatChildrenPlainText(t, mode)
	if strings.Contains(plain, "hidden probe text") {
		t.Fatalf("hidden custom message rendered:\n%s", plain)
	}
}
