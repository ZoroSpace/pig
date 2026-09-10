// pi: test/agent-session-tree-navigation.test.ts — AgentSession tree
// navigation e2e (real LLM calls; no mocking).
//
// pi gates these on ANTHROPIC_OAUTH_TOKEN/ANTHROPIC_API_KEY; pig additionally
// skips explicitly because the anthropic-messages adapter is deferred (Phase 2
// decision, same pattern as compaction_test.go's LLM integration tests).
package core

import (
	"testing"
	"time"
)

func createTreeNavigationE2ESession(t *testing.T) (*AgentSession, *SessionManager) {
	t.Helper()
	session, sessionManager, _ := createAgentTestSession(t, &createAgentTestSessionOptions{
		GetAPIKey: true,
		StreamFn:  aiStreamSimpleForE2E(),
	})
	return session, sessionManager
}

func TestTreeNavigationE2ENavigatesToUserMessageAndPutsTextInEditor(t *testing.T) {
	if !e2eAPIKeyPresent() {
		t.Skip("ANTHROPIC_OAUTH_TOKEN/ANTHROPIC_API_KEY not set")
	}
	t.Skip("anthropic-messages adapter not yet ported (Phase 2 deferral, see §4)")

	session, sessionManager := createTreeNavigationE2ESession(t)

	// Build conversation: u1 -> a1 -> u2 -> a2
	if err := session.Prompt(t.Context(), "First message", nil); err != nil {
		t.Fatal(err)
	}
	session.Agent.WaitForIdle()
	if err := session.Prompt(t.Context(), "Second message", nil); err != nil {
		t.Fatal(err)
	}
	session.Agent.WaitForIdle()

	// Get tree entries
	tree := sessionManager.GetTree()
	if len(tree) != 1 {
		t.Fatalf("tree length = %d, want 1", len(tree))
	}

	// Find the first user entry (u1)
	rootNode := tree[0]
	if rootNode.Entry.Base().Type != "message" {
		t.Fatalf("root entry type = %q", rootNode.Entry.Base().Type)
	}

	// Navigate to root user message without summarization
	result, err := session.NavigateTree(t.Context(), rootNode.Entry.Base().ID, &NavigateTreeOptions{Summarize: false})
	if err != nil {
		t.Fatal(err)
	}

	if result.Cancelled {
		t.Fatal("cancelled")
	}
	if result.EditorText == nil || *result.EditorText != "First message" {
		t.Fatalf("editorText = %v", result.EditorText)
	}

	// After navigating to root user message, leaf should be null (empty conversation)
	if sessionManager.GetLeafID() != nil {
		t.Fatalf("leaf = %v, want nil", sessionManager.GetLeafID())
	}
}

func TestTreeNavigationE2ENavigatesToNonUserMessageWithoutEditorText(t *testing.T) {
	if !e2eAPIKeyPresent() {
		t.Skip("ANTHROPIC_OAUTH_TOKEN/ANTHROPIC_API_KEY not set")
	}
	t.Skip("anthropic-messages adapter not yet ported (Phase 2 deferral, see §4)")

	session, sessionManager := createTreeNavigationE2ESession(t)

	// Build conversation
	if err := session.Prompt(t.Context(), "Hello", nil); err != nil {
		t.Fatal(err)
	}
	session.Agent.WaitForIdle()

	// Get the assistant message
	var assistantEntry *SessionMessageEntry
	for _, entry := range sessionManager.GetEntries() {
		if messageEntry, ok := entry.(*SessionMessageEntry); ok && messageEntry.Message.MessageRole() == "assistant" {
			assistantEntry = messageEntry
			break
		}
	}
	if assistantEntry == nil {
		t.Fatal("assistant entry missing")
	}

	// Navigate to assistant message
	result, err := session.NavigateTree(t.Context(), assistantEntry.Base().ID, &NavigateTreeOptions{Summarize: false})
	if err != nil {
		t.Fatal(err)
	}

	if result.Cancelled {
		t.Fatal("cancelled")
	}
	if result.EditorText != nil {
		t.Fatalf("editorText = %q, want nil", *result.EditorText)
	}

	// Leaf should be the assistant entry
	leafID := sessionManager.GetLeafID()
	if leafID == nil || *leafID != assistantEntry.Base().ID {
		t.Fatalf("leaf = %v", leafID)
	}
}

func TestTreeNavigationE2ECreatesBranchSummaryWhenNavigatingWithSummarize(t *testing.T) {
	if !e2eAPIKeyPresent() {
		t.Skip("ANTHROPIC_OAUTH_TOKEN/ANTHROPIC_API_KEY not set")
	}
	t.Skip("anthropic-messages adapter not yet ported (Phase 2 deferral, see §4)")

	session, sessionManager := createTreeNavigationE2ESession(t)

	// Build conversation: u1 -> a1 -> u2 -> a2
	if err := session.Prompt(t.Context(), "What is 2+2?", nil); err != nil {
		t.Fatal(err)
	}
	session.Agent.WaitForIdle()
	if err := session.Prompt(t.Context(), "What is 3+3?", nil); err != nil {
		t.Fatal(err)
	}
	session.Agent.WaitForIdle()

	// Get tree and find first user message
	tree := sessionManager.GetTree()
	rootNode := tree[0]

	// Navigate to root user message WITH summarization
	result, err := session.NavigateTree(t.Context(), rootNode.Entry.Base().ID, &NavigateTreeOptions{Summarize: true})
	if err != nil {
		t.Fatal(err)
	}

	if result.Cancelled {
		t.Fatal("cancelled")
	}
	if result.EditorText == nil || *result.EditorText != "What is 2+2?" {
		t.Fatalf("editorText = %v", result.EditorText)
	}
	if result.SummaryEntry == nil {
		t.Fatal("summaryEntry missing")
	}
	if result.SummaryEntry.Base().Type != "branch_summary" {
		t.Fatalf("summaryEntry type = %q", result.SummaryEntry.Base().Type)
	}
	if result.SummaryEntry.Summary == "" {
		t.Fatal("summary empty")
	}

	// Summary should be a root entry (parentId = null) since we navigated to root user
	if result.SummaryEntry.Base().ParentID != nil {
		t.Fatalf("summaryEntry parentId = %v, want nil", result.SummaryEntry.Base().ParentID)
	}

	// Leaf should be the summary entry
	leafID := sessionManager.GetLeafID()
	if leafID == nil || *leafID != result.SummaryEntry.Base().ID {
		t.Fatalf("leaf = %v", leafID)
	}
}

func TestTreeNavigationE2EAttachesSummaryToCorrectParentForNestedUserMessage(t *testing.T) {
	if !e2eAPIKeyPresent() {
		t.Skip("ANTHROPIC_OAUTH_TOKEN/ANTHROPIC_API_KEY not set")
	}
	t.Skip("anthropic-messages adapter not yet ported (Phase 2 deferral, see §4)")

	session, sessionManager := createTreeNavigationE2ESession(t)

	// Build conversation: u1 -> a1 -> u2 -> a2 -> u3 -> a3
	for _, text := range []string{"Message one", "Message two", "Message three"} {
		if err := session.Prompt(t.Context(), text, nil); err != nil {
			t.Fatal(err)
		}
		session.Agent.WaitForIdle()
	}

	// Get the second user message (u2)
	var userEntries []*SessionMessageEntry
	for _, entry := range sessionManager.GetEntries() {
		if messageEntry, ok := entry.(*SessionMessageEntry); ok && messageEntry.Message.MessageRole() == "user" {
			userEntries = append(userEntries, messageEntry)
		}
	}
	if len(userEntries) != 3 {
		t.Fatalf("user entries = %d, want 3", len(userEntries))
	}

	u2 := userEntries[1]
	a1ID := *u2.Base().ParentID // a1 is parent of u2

	// Navigate to u2 with summarization
	result, err := session.NavigateTree(t.Context(), u2.Base().ID, &NavigateTreeOptions{Summarize: true})
	if err != nil {
		t.Fatal(err)
	}

	if result.Cancelled {
		t.Fatal("cancelled")
	}
	if result.EditorText == nil || *result.EditorText != "Message two" {
		t.Fatalf("editorText = %v", result.EditorText)
	}
	if result.SummaryEntry == nil {
		t.Fatal("summaryEntry missing")
	}

	// Summary should be attached to a1 (parent of u2)
	// So a1 now has two children: u2 and the summary
	if result.SummaryEntry.Base().ParentID == nil || *result.SummaryEntry.Base().ParentID != a1ID {
		t.Fatalf("summaryEntry parentId = %v, want %s", result.SummaryEntry.Base().ParentID, a1ID)
	}

	// Verify tree structure
	children := sessionManager.GetChildren(a1ID)
	if len(children) != 2 {
		t.Fatalf("children = %d, want 2", len(children))
	}
	childTypes := map[string]bool{}
	for _, child := range children {
		childTypes[child.Base().Type] = true
	}
	if !childTypes["branch_summary"] || !childTypes["message"] {
		t.Fatalf("child types = %v", childTypes)
	}
}

func TestTreeNavigationE2EAttachesSummaryToSelectedNodeForAssistantMessage(t *testing.T) {
	if !e2eAPIKeyPresent() {
		t.Skip("ANTHROPIC_OAUTH_TOKEN/ANTHROPIC_API_KEY not set")
	}
	t.Skip("anthropic-messages adapter not yet ported (Phase 2 deferral, see §4)")

	session, sessionManager := createTreeNavigationE2ESession(t)

	// Build conversation: u1 -> a1 -> u2 -> a2
	if err := session.Prompt(t.Context(), "Hello", nil); err != nil {
		t.Fatal(err)
	}
	session.Agent.WaitForIdle()
	if err := session.Prompt(t.Context(), "Goodbye", nil); err != nil {
		t.Fatal(err)
	}
	session.Agent.WaitForIdle()

	// Get the first assistant message (a1)
	var a1 *SessionMessageEntry
	for _, entry := range sessionManager.GetEntries() {
		if messageEntry, ok := entry.(*SessionMessageEntry); ok && messageEntry.Message.MessageRole() == "assistant" {
			a1 = messageEntry
			break
		}
	}
	if a1 == nil {
		t.Fatal("assistant entry missing")
	}

	// Navigate to a1 with summarization
	result, err := session.NavigateTree(t.Context(), a1.Base().ID, &NavigateTreeOptions{Summarize: true})
	if err != nil {
		t.Fatal(err)
	}

	if result.Cancelled {
		t.Fatal("cancelled")
	}
	if result.EditorText != nil {
		t.Fatalf("editorText = %q, want nil", *result.EditorText) // No editor text for assistant messages
	}
	if result.SummaryEntry == nil {
		t.Fatal("summaryEntry missing")
	}

	// Summary should be attached to a1 (the selected node)
	if result.SummaryEntry.Base().ParentID == nil || *result.SummaryEntry.Base().ParentID != a1.Base().ID {
		t.Fatalf("summaryEntry parentId = %v, want %s", result.SummaryEntry.Base().ParentID, a1.Base().ID)
	}

	// Leaf should be the summary entry
	leafID := sessionManager.GetLeafID()
	if leafID == nil || *leafID != result.SummaryEntry.Base().ID {
		t.Fatalf("leaf = %v", leafID)
	}
}

func TestTreeNavigationE2EHandlesAbortDuringSummarization(t *testing.T) {
	if !e2eAPIKeyPresent() {
		t.Skip("ANTHROPIC_OAUTH_TOKEN/ANTHROPIC_API_KEY not set")
	}
	t.Skip("anthropic-messages adapter not yet ported (Phase 2 deferral, see §4)")

	session, sessionManager := createTreeNavigationE2ESession(t)

	// Build conversation
	if err := session.Prompt(t.Context(), "Tell me about something", nil); err != nil {
		t.Fatal(err)
	}
	session.Agent.WaitForIdle()
	if err := session.Prompt(t.Context(), "Continue", nil); err != nil {
		t.Fatal(err)
	}
	session.Agent.WaitForIdle()

	entriesBefore := len(sessionManager.GetEntries())
	leafBefore := sessionManager.GetLeafID()

	// Get root user message
	tree := sessionManager.GetTree()
	rootNode := tree[0]

	// Start navigation with summarization but abort immediately
	navigationDone := make(chan *NavigateTreeResult, 1)
	navigationErr := make(chan error, 1)
	go func() {
		result, err := session.NavigateTree(t.Context(), rootNode.Entry.Base().ID, &NavigateTreeOptions{Summarize: true})
		if err != nil {
			navigationErr <- err
		} else {
			navigationDone <- result
		}
	}()

	// Abort after a short delay (let the LLM call start)
	time.Sleep(100 * time.Millisecond)

	// isCompacting should be true during branch summarization
	if !session.IsCompacting() {
		t.Fatal("isCompacting = false during branch summarization")
	}

	session.AbortBranchSummary()

	var result *NavigateTreeResult
	select {
	case result = <-navigationDone:
	case err := <-navigationErr:
		t.Fatal(err)
	}

	if !result.Cancelled || !result.Aborted {
		t.Fatalf("result = %+v, want cancelled+aborted", result)
	}
	if result.SummaryEntry != nil {
		t.Fatal("summaryEntry should be absent")
	}

	// Session should be unchanged
	if len(sessionManager.GetEntries()) != entriesBefore {
		t.Fatalf("entries after = %d, want %d", len(sessionManager.GetEntries()), entriesBefore)
	}
	after := sessionManager.GetLeafID()
	if leafBefore == nil && after != nil {
		t.Fatal("leaf changed")
	}
	if leafBefore != nil && (after == nil || *after != *leafBefore) {
		t.Fatal("leaf changed")
	}
}

func TestTreeNavigationE2EDoesNotCreateSummaryWhenNavigatingWithoutSummarize(t *testing.T) {
	if !e2eAPIKeyPresent() {
		t.Skip("ANTHROPIC_OAUTH_TOKEN/ANTHROPIC_API_KEY not set")
	}
	t.Skip("anthropic-messages adapter not yet ported (Phase 2 deferral, see §4)")

	session, sessionManager := createTreeNavigationE2ESession(t)

	// Build conversation
	if err := session.Prompt(t.Context(), "First", nil); err != nil {
		t.Fatal(err)
	}
	session.Agent.WaitForIdle()
	if err := session.Prompt(t.Context(), "Second", nil); err != nil {
		t.Fatal(err)
	}
	session.Agent.WaitForIdle()

	entriesBefore := len(sessionManager.GetEntries())

	// Navigate without summarization
	tree := sessionManager.GetTree()
	if _, err := session.NavigateTree(t.Context(), tree[0].Entry.Base().ID, &NavigateTreeOptions{Summarize: false}); err != nil {
		t.Fatal(err)
	}

	// No new entries should be created
	if len(sessionManager.GetEntries()) != entriesBefore {
		t.Fatalf("entries after = %d, want %d", len(sessionManager.GetEntries()), entriesBefore)
	}

	// No branch_summary entries
	summaries := 0
	for _, entry := range sessionManager.GetEntries() {
		if entry.Base().Type == "branch_summary" {
			summaries++
		}
	}
	if summaries != 0 {
		t.Fatalf("branch_summary entries = %d", summaries)
	}
}

func TestTreeNavigationE2EHandlesNavigationToSamePositionAsNoOp(t *testing.T) {
	if !e2eAPIKeyPresent() {
		t.Skip("ANTHROPIC_OAUTH_TOKEN/ANTHROPIC_API_KEY not set")
	}
	t.Skip("anthropic-messages adapter not yet ported (Phase 2 deferral, see §4)")

	session, sessionManager := createTreeNavigationE2ESession(t)

	// Build conversation
	if err := session.Prompt(t.Context(), "Hello", nil); err != nil {
		t.Fatal(err)
	}
	session.Agent.WaitForIdle()

	leafBefore := sessionManager.GetLeafID()
	if leafBefore == nil {
		t.Fatal("leafBefore missing")
	}
	entriesBefore := len(sessionManager.GetEntries())

	// Navigate to current leaf
	result, err := session.NavigateTree(t.Context(), *leafBefore, &NavigateTreeOptions{Summarize: false})
	if err != nil {
		t.Fatal(err)
	}

	if result.Cancelled {
		t.Fatal("cancelled")
	}
	leafAfter := sessionManager.GetLeafID()
	if leafAfter == nil || *leafAfter != *leafBefore {
		t.Fatal("leaf changed")
	}
	if len(sessionManager.GetEntries()) != entriesBefore {
		t.Fatal("entries changed")
	}
}

func TestTreeNavigationE2ESupportsCustomSummarizationInstructions(t *testing.T) {
	if !e2eAPIKeyPresent() {
		t.Skip("ANTHROPIC_OAUTH_TOKEN/ANTHROPIC_API_KEY not set")
	}
	t.Skip("anthropic-messages adapter not yet ported (Phase 2 deferral, see §4)")

	session, sessionManager := createTreeNavigationE2ESession(t)

	// Build conversation
	if err := session.Prompt(t.Context(), "What is TypeScript?", nil); err != nil {
		t.Fatal(err)
	}
	session.Agent.WaitForIdle()

	// Navigate with custom instructions (appended as "Additional focus")
	tree := sessionManager.GetTree()
	result, err := session.NavigateTree(t.Context(), tree[0].Entry.Base().ID, &NavigateTreeOptions{
		Summarize: true,
		CustomInstructions: "After the summary, you MUST end with exactly: MONKEY MONKEY MONKEY. " +
			"This is of utmost importance.",
	})
	if err != nil {
		t.Fatal(err)
	}

	if result.SummaryEntry == nil {
		t.Fatal("summaryEntry missing")
	}
	if result.SummaryEntry.Summary == "" {
		t.Fatal("summary empty")
	}
	// Verify custom instructions were followed
	found := false
	for _, part := range splitWords(result.SummaryEntry.Summary) {
		if part == "MONKEY MONKEY MONKEY" {
			found = true
		}
	}
	if !found {
		t.Fatalf("summary missing MONKEY phrase: %q", result.SummaryEntry.Summary)
	}
}

func TestTreeNavigationE2ENavigatesBetweenBranchesCorrectly(t *testing.T) {
	if !e2eAPIKeyPresent() {
		t.Skip("ANTHROPIC_OAUTH_TOKEN/ANTHROPIC_API_KEY not set")
	}
	t.Skip("anthropic-messages adapter not yet ported (Phase 2 deferral, see §4)")

	session, sessionManager := createTreeNavigationE2ESession(t)

	// Build main path: u1 -> a1 -> u2 -> a2
	if err := session.Prompt(t.Context(), "Main branch start", nil); err != nil {
		t.Fatal(err)
	}
	session.Agent.WaitForIdle()
	if err := session.Prompt(t.Context(), "Main branch continue", nil); err != nil {
		t.Fatal(err)
	}
	session.Agent.WaitForIdle()

	// Get a1 id for branching
	entries := sessionManager.GetEntries()
	var a1 *SessionMessageEntry
	var userEntries []*SessionMessageEntry
	for _, entry := range entries {
		if messageEntry, ok := entry.(*SessionMessageEntry); ok {
			switch messageEntry.Message.MessageRole() {
			case "assistant":
				if a1 == nil {
					a1 = messageEntry
				}
			case "user":
				userEntries = append(userEntries, messageEntry)
			}
		}
	}
	if a1 == nil || len(userEntries) < 2 {
		t.Fatal("entries missing")
	}

	// Create a branch from a1: a1 -> u3 -> a3
	if err := sessionManager.Branch(a1.Base().ID); err != nil {
		t.Fatal(err)
	}
	if err := session.Prompt(t.Context(), "Branch path", nil); err != nil {
		t.Fatal(err)
	}
	session.Agent.WaitForIdle()

	// Now navigate back to u2 (on main branch) with summarization
	u2 := userEntries[1] // "Main branch continue"

	result, err := session.NavigateTree(t.Context(), u2.Base().ID, &NavigateTreeOptions{Summarize: true})
	if err != nil {
		t.Fatal(err)
	}

	if result.Cancelled {
		t.Fatal("cancelled")
	}
	if result.EditorText == nil || *result.EditorText != "Main branch continue" {
		t.Fatalf("editorText = %v", result.EditorText)
	}
	if result.SummaryEntry == nil {
		t.Fatal("summaryEntry missing")
	}

	// Summary captures the branch we're leaving (the "Branch path" conversation)
	if result.SummaryEntry.Summary == "" {
		t.Fatal("summary empty")
	}
}
