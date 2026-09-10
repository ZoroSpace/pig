// pi: test/session-manager/build-context.test.ts — buildSessionContext /
// buildContextEntries semantics over hand-built entry trees.
package core

import (
	"reflect"
	"strings"
	"testing"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/agent/harness"
	"github.com/ZoroSpace/pig/ai/types"
)

func bcMsg(id string, parentID *string, role types.Role, text string) *SessionMessageEntry {
	base := EntryBase{Type: "message", ID: id, ParentID: parentID, Timestamp: "2025-01-01T00:00:00Z"}
	if role == types.RoleUser {
		return &SessionMessageEntry{EntryBase: base, Message: types.UserMessage{
			Role: role, Content: types.UserContent{String: text}, Timestamp: 1,
		}}
	}
	return &SessionMessageEntry{EntryBase: base, Message: types.AssistantMessage{
		Role:       role,
		Content:    []types.Content{types.NewTextContent(text)},
		API:        "anthropic-messages",
		Provider:   "anthropic",
		Model:      "claude-test",
		Usage:      testUsage(),
		StopReason: types.StopReasonStop,
		Timestamp:  1,
	}}
}

func bcParentID(id string) *string { return &id }

func bcCompaction(id string, parentID *string, summary string, firstKeptEntryID string) *CompactionEntry {
	return &CompactionEntry{
		EntryBase:        EntryBase{Type: "compaction", ID: id, ParentID: parentID, Timestamp: "2025-01-01T00:00:00Z"},
		Summary:          summary,
		FirstKeptEntryID: firstKeptEntryID,
		TokensBefore:     1000,
	}
}

func bcBranchSummary(id string, parentID *string, summary string, fromID string) *BranchSummaryEntry {
	return &BranchSummaryEntry{
		EntryBase: EntryBase{Type: "branch_summary", ID: id, ParentID: parentID, Timestamp: "2025-01-01T00:00:00Z"},
		FromID:    fromID,
		Summary:   summary,
	}
}

func bcCustom(id string, parentID *string, customType string, data any) *CustomEntry {
	return &CustomEntry{
		EntryBase:  EntryBase{Type: "custom", ID: id, ParentID: parentID, Timestamp: "2025-01-01T00:00:00Z"},
		CustomType: customType,
		Data:       data,
	}
}

func bcThinkingLevel(id string, parentID *string, level string) *ThinkingLevelChangeEntry {
	return &ThinkingLevelChangeEntry{
		EntryBase:     EntryBase{Type: "thinking_level_change", ID: id, ParentID: parentID, Timestamp: "2025-01-01T00:00:00Z"},
		ThinkingLevel: level,
	}
}

func bcModelChange(id string, parentID *string, provider string, modelID string) *ModelChangeEntry {
	return &ModelChangeEntry{
		EntryBase: EntryBase{Type: "model_change", ID: id, ParentID: parentID, Timestamp: "2025-01-01T00:00:00Z"},
		Provider:  provider,
		ModelID:   modelID,
	}
}

// msgRole returns the role of the message in a context message slot.
func msgRole(m agent.AgentMessage) types.Role {
	return m.MessageRole()
}

func TestBuildSessionContextTrivial(t *testing.T) {
	t.Run("empty entries returns empty context", func(t *testing.T) {
		ctx := BuildSessionContext(nil, nil, nil)
		if len(ctx.Messages) != 0 {
			t.Fatalf("expected 0 messages, got %d", len(ctx.Messages))
		}
		if ctx.ThinkingLevel != "off" {
			t.Fatalf("expected thinkingLevel off, got %q", ctx.ThinkingLevel)
		}
		if ctx.Model != nil {
			t.Fatalf("expected nil model")
		}
	})

	t.Run("single user message", func(t *testing.T) {
		entries := []SessionEntry{bcMsg("1", nil, types.RoleUser, "hello")}
		ctx := BuildSessionContext(entries, nil, nil)
		if len(ctx.Messages) != 1 || msgRole(ctx.Messages[0]) != types.RoleUser {
			t.Fatalf("unexpected messages: %#v", ctx.Messages)
		}
	})

	t.Run("simple conversation", func(t *testing.T) {
		entries := []SessionEntry{
			bcMsg("1", nil, types.RoleUser, "hello"),
			bcMsg("2", bcParentID("1"), types.RoleAssistant, "hi there"),
			bcMsg("3", bcParentID("2"), types.RoleUser, "how are you"),
			bcMsg("4", bcParentID("3"), types.RoleAssistant, "great"),
		}
		ctx := BuildSessionContext(entries, nil, nil)
		if len(ctx.Messages) != 4 {
			t.Fatalf("expected 4 messages, got %d", len(ctx.Messages))
		}
		want := []types.Role{types.RoleUser, types.RoleAssistant, types.RoleUser, types.RoleAssistant}
		for i, m := range ctx.Messages {
			if msgRole(m) != want[i] {
				t.Fatalf("message %d role = %q, want %q", i, msgRole(m), want[i])
			}
		}
	})

	t.Run("tracks thinking level changes", func(t *testing.T) {
		entries := []SessionEntry{
			bcMsg("1", nil, types.RoleUser, "hello"),
			bcThinkingLevel("2", bcParentID("1"), "high"),
			bcMsg("3", bcParentID("2"), types.RoleAssistant, "thinking hard"),
		}
		ctx := BuildSessionContext(entries, nil, nil)
		if ctx.ThinkingLevel != "high" {
			t.Fatalf("expected high, got %q", ctx.ThinkingLevel)
		}
		if len(ctx.Messages) != 2 {
			t.Fatalf("expected 2 messages, got %d", len(ctx.Messages))
		}
	})

	t.Run("tracks model from assistant message", func(t *testing.T) {
		entries := []SessionEntry{
			bcMsg("1", nil, types.RoleUser, "hello"),
			bcMsg("2", bcParentID("1"), types.RoleAssistant, "hi"),
		}
		ctx := BuildSessionContext(entries, nil, nil)
		want := &SessionModelRef{Provider: "anthropic", ModelID: "claude-test"}
		if !reflect.DeepEqual(ctx.Model, want) {
			t.Fatalf("model = %#v, want %#v", ctx.Model, want)
		}
	})

	t.Run("tracks model from model change entry", func(t *testing.T) {
		entries := []SessionEntry{
			bcMsg("1", nil, types.RoleUser, "hello"),
			bcModelChange("2", bcParentID("1"), "openai", "gpt-4"),
			bcMsg("3", bcParentID("2"), types.RoleAssistant, "hi"),
		}
		ctx := BuildSessionContext(entries, nil, nil)
		// Assistant message overwrites model change
		want := &SessionModelRef{Provider: "anthropic", ModelID: "claude-test"}
		if !reflect.DeepEqual(ctx.Model, want) {
			t.Fatalf("model = %#v, want %#v", ctx.Model, want)
		}
	})
}

// compactionSummarySummary extracts the summary from a compaction/branch
// summary context message.
func compactionSummarySummary(m agent.AgentMessage) string {
	switch v := m.(type) {
	case harness.CompactionSummaryMessage:
		return v.Summary
	case harness.BranchSummaryMessage:
		return v.Summary
	}
	panic("not a summary message")
}

func userContentString(m agent.AgentMessage) string {
	return m.(types.UserMessage).Content.String
}

func assistantContentText(m agent.AgentMessage) string {
	return m.(types.AssistantMessage).Content[0].(types.TextContent).Text
}

func TestBuildSessionContextWithCompaction(t *testing.T) {
	t.Run("includes summary before kept messages", func(t *testing.T) {
		entries := []SessionEntry{
			bcMsg("1", nil, types.RoleUser, "first"),
			bcMsg("2", bcParentID("1"), types.RoleAssistant, "response1"),
			bcMsg("3", bcParentID("2"), types.RoleUser, "second"),
			bcMsg("4", bcParentID("3"), types.RoleAssistant, "response2"),
			bcCompaction("5", bcParentID("4"), "Summary of first two turns", "3"),
			bcMsg("6", bcParentID("5"), types.RoleUser, "third"),
			bcMsg("7", bcParentID("6"), types.RoleAssistant, "response3"),
		}
		ctx := BuildSessionContext(entries, nil, nil)

		// Should have: summary + kept (3,4) + after (6,7) = 5 messages
		if len(ctx.Messages) != 5 {
			t.Fatalf("expected 5 messages, got %d", len(ctx.Messages))
		}
		if !strings.Contains(compactionSummarySummary(ctx.Messages[0]), "Summary of first two turns") {
			t.Fatalf("summary = %q", compactionSummarySummary(ctx.Messages[0]))
		}
		if userContentString(ctx.Messages[1]) != "second" {
			t.Fatalf("msg 1 = %q", userContentString(ctx.Messages[1]))
		}
		if assistantContentText(ctx.Messages[2]) != "response2" {
			t.Fatalf("msg 2 = %q", assistantContentText(ctx.Messages[2]))
		}
		if userContentString(ctx.Messages[3]) != "third" {
			t.Fatalf("msg 3 = %q", userContentString(ctx.Messages[3]))
		}
		if assistantContentText(ctx.Messages[4]) != "response3" {
			t.Fatalf("msg 4 = %q", assistantContentText(ctx.Messages[4]))
		}
	})

	t.Run("handles compaction keeping from first message", func(t *testing.T) {
		entries := []SessionEntry{
			bcMsg("1", nil, types.RoleUser, "first"),
			bcMsg("2", bcParentID("1"), types.RoleAssistant, "response"),
			bcCompaction("3", bcParentID("2"), "Empty summary", "1"),
			bcMsg("4", bcParentID("3"), types.RoleUser, "second"),
		}
		ctx := BuildSessionContext(entries, nil, nil)

		// Summary + all messages (1,2,4)
		if len(ctx.Messages) != 4 {
			t.Fatalf("expected 4 messages, got %d", len(ctx.Messages))
		}
		if !strings.Contains(compactionSummarySummary(ctx.Messages[0]), "Empty summary") {
			t.Fatalf("summary = %q", compactionSummarySummary(ctx.Messages[0]))
		}
	})

	t.Run("multiple compactions uses latest", func(t *testing.T) {
		entries := []SessionEntry{
			bcMsg("1", nil, types.RoleUser, "a"),
			bcMsg("2", bcParentID("1"), types.RoleAssistant, "b"),
			bcCompaction("3", bcParentID("2"), "First summary", "1"),
			bcMsg("4", bcParentID("3"), types.RoleUser, "c"),
			bcMsg("5", bcParentID("4"), types.RoleAssistant, "d"),
			bcCompaction("6", bcParentID("5"), "Second summary", "4"),
			bcMsg("7", bcParentID("6"), types.RoleUser, "e"),
		}
		ctx := BuildSessionContext(entries, nil, nil)

		// Should use second summary, keep from 4
		if len(ctx.Messages) != 4 {
			t.Fatalf("expected 4 messages, got %d", len(ctx.Messages))
		}
		if !strings.Contains(compactionSummarySummary(ctx.Messages[0]), "Second summary") {
			t.Fatalf("summary = %q", compactionSummarySummary(ctx.Messages[0]))
		}
	})

	t.Run("buildContextEntries returns compaction-aware entries including custom entries", func(t *testing.T) {
		entries := []SessionEntry{
			bcMsg("1", nil, types.RoleUser, "first"),
			bcCustom("2", bcParentID("1"), "old-state", map[string]any{"hidden": true}),
			bcMsg("3", bcParentID("2"), types.RoleAssistant, "response1"),
			bcCustom("4", bcParentID("3"), "kept-card", map[string]any{"title": "Kept"}),
			bcMsg("5", bcParentID("4"), types.RoleUser, "second"),
			bcCompaction("6", bcParentID("5"), "Summary", "4"),
			bcCustom("7", bcParentID("6"), "after-card", map[string]any{"title": "After"}),
			bcMsg("8", bcParentID("7"), types.RoleAssistant, "response2"),
		}

		contextEntries := BuildContextEntries(entries, nil, nil)
		var ids []string
		for _, entry := range contextEntries {
			ids = append(ids, entry.Base().ID)
		}
		if !reflect.DeepEqual(ids, []string{"6", "4", "5", "7", "8"}) {
			t.Fatalf("ids = %#v", ids)
		}
		ctx := BuildSessionContext(entries, nil, nil)
		want := []types.Role{"compactionSummary", "user", "assistant"}
		for i, m := range ctx.Messages {
			if msgRole(m) != want[i] {
				t.Fatalf("message %d role = %q, want %q", i, msgRole(m), want[i])
			}
		}
	})

	t.Run("keeps settings from the full path after compaction", func(t *testing.T) {
		entries := []SessionEntry{
			bcMsg("1", nil, types.RoleUser, "first"),
			bcThinkingLevel("2", bcParentID("1"), "high"),
			bcMsg("3", bcParentID("2"), types.RoleAssistant, "response1"),
			bcMsg("4", bcParentID("3"), types.RoleUser, "second"),
			bcCompaction("5", bcParentID("4"), "Summary", "4"),
		}

		ctx := BuildSessionContext(entries, nil, nil)
		if ctx.ThinkingLevel != "high" {
			t.Fatalf("thinkingLevel = %q", ctx.ThinkingLevel)
		}
		want := []types.Role{"compactionSummary", "user"}
		for i, m := range ctx.Messages {
			if msgRole(m) != want[i] {
				t.Fatalf("message %d role = %q, want %q", i, msgRole(m), want[i])
			}
		}
	})
}

func TestBuildSessionContextWithBranches(t *testing.T) {
	t.Run("follows path to specified leaf", func(t *testing.T) {
		// Tree:
		//   1 -> 2 -> 3 (branch A)
		//         \-> 4 (branch B)
		entries := []SessionEntry{
			bcMsg("1", nil, types.RoleUser, "start"),
			bcMsg("2", bcParentID("1"), types.RoleAssistant, "response"),
			bcMsg("3", bcParentID("2"), types.RoleUser, "branch A"),
			bcMsg("4", bcParentID("2"), types.RoleUser, "branch B"),
		}

		leafA := "3"
		ctxA := BuildSessionContext(entries, &leafA, nil)
		if len(ctxA.Messages) != 3 || userContentString(ctxA.Messages[2]) != "branch A" {
			t.Fatalf("unexpected branch A context: %#v", ctxA.Messages)
		}

		leafB := "4"
		ctxB := BuildSessionContext(entries, &leafB, nil)
		if len(ctxB.Messages) != 3 || userContentString(ctxB.Messages[2]) != "branch B" {
			t.Fatalf("unexpected branch B context: %#v", ctxB.Messages)
		}
	})

	t.Run("includes branch summary in path", func(t *testing.T) {
		entries := []SessionEntry{
			bcMsg("1", nil, types.RoleUser, "start"),
			bcMsg("2", bcParentID("1"), types.RoleAssistant, "response"),
			bcMsg("3", bcParentID("2"), types.RoleUser, "abandoned path"),
			bcBranchSummary("4", bcParentID("2"), "Summary of abandoned work", "3"),
			bcMsg("5", bcParentID("4"), types.RoleUser, "new direction"),
		}
		leaf := "5"
		ctx := BuildSessionContext(entries, &leaf, nil)

		if len(ctx.Messages) != 4 {
			t.Fatalf("expected 4 messages, got %d", len(ctx.Messages))
		}
		if !strings.Contains(compactionSummarySummary(ctx.Messages[2]), "Summary of abandoned work") {
			t.Fatalf("msg 2 = %#v", ctx.Messages[2])
		}
		if userContentString(ctx.Messages[3]) != "new direction" {
			t.Fatalf("msg 3 = %q", userContentString(ctx.Messages[3]))
		}
	})

	t.Run("complex tree with multiple branches and compaction", func(t *testing.T) {
		// Tree:
		//   1 -> 2 -> 3 -> 4 -> compaction(5) -> 6 -> 7 (main path)
		//              \-> 8 -> 9 (abandoned branch)
		//                    \-> branchSummary(10) -> 11 (resumed from 3)
		entries := []SessionEntry{
			bcMsg("1", nil, types.RoleUser, "start"),
			bcMsg("2", bcParentID("1"), types.RoleAssistant, "r1"),
			bcMsg("3", bcParentID("2"), types.RoleUser, "q2"),
			bcMsg("4", bcParentID("3"), types.RoleAssistant, "r2"),
			bcCompaction("5", bcParentID("4"), "Compacted history", "3"),
			bcMsg("6", bcParentID("5"), types.RoleUser, "q3"),
			bcMsg("7", bcParentID("6"), types.RoleAssistant, "r3"),
			// Abandoned branch from 3
			bcMsg("8", bcParentID("3"), types.RoleUser, "wrong path"),
			bcMsg("9", bcParentID("8"), types.RoleAssistant, "wrong response"),
			// Branch summary resuming from 3
			bcBranchSummary("10", bcParentID("3"), "Tried wrong approach", "9"),
			bcMsg("11", bcParentID("10"), types.RoleUser, "better approach"),
		}

		// Main path to 7: summary + kept(3,4) + after(6,7)
		leafMain := "7"
		ctxMain := BuildSessionContext(entries, &leafMain, nil)
		if len(ctxMain.Messages) != 5 {
			t.Fatalf("expected 5 messages, got %d", len(ctxMain.Messages))
		}
		if !strings.Contains(compactionSummarySummary(ctxMain.Messages[0]), "Compacted history") {
			t.Fatalf("msg 0 = %#v", ctxMain.Messages[0])
		}
		if userContentString(ctxMain.Messages[1]) != "q2" ||
			assistantContentText(ctxMain.Messages[2]) != "r2" ||
			userContentString(ctxMain.Messages[3]) != "q3" ||
			assistantContentText(ctxMain.Messages[4]) != "r3" {
			t.Fatalf("unexpected main context: %#v", ctxMain.Messages)
		}

		// Branch path to 11: 1,2,3 + branch_summary + 11
		leafBranch := "11"
		ctxBranch := BuildSessionContext(entries, &leafBranch, nil)
		if len(ctxBranch.Messages) != 5 {
			t.Fatalf("expected 5 messages, got %d", len(ctxBranch.Messages))
		}
		if userContentString(ctxBranch.Messages[0]) != "start" ||
			assistantContentText(ctxBranch.Messages[1]) != "r1" ||
			userContentString(ctxBranch.Messages[2]) != "q2" {
			t.Fatalf("unexpected branch context head: %#v", ctxBranch.Messages)
		}
		if !strings.Contains(compactionSummarySummary(ctxBranch.Messages[3]), "Tried wrong approach") {
			t.Fatalf("msg 3 = %#v", ctxBranch.Messages[3])
		}
		if userContentString(ctxBranch.Messages[4]) != "better approach" {
			t.Fatalf("msg 4 = %q", userContentString(ctxBranch.Messages[4]))
		}
	})
}

func TestBuildSessionContextEdgeCases(t *testing.T) {
	t.Run("uses last entry when leafId not found", func(t *testing.T) {
		entries := []SessionEntry{
			bcMsg("1", nil, types.RoleUser, "hello"),
			bcMsg("2", bcParentID("1"), types.RoleAssistant, "hi"),
		}
		leaf := "nonexistent"
		ctx := BuildSessionContext(entries, &leaf, nil)
		if len(ctx.Messages) != 2 {
			t.Fatalf("expected 2 messages, got %d", len(ctx.Messages))
		}
	})

	t.Run("handles orphaned entries gracefully", func(t *testing.T) {
		entries := []SessionEntry{
			bcMsg("1", nil, types.RoleUser, "hello"),
			bcMsg("2", bcParentID("missing"), types.RoleAssistant, "orphan"), // parent doesn't exist
		}
		leaf := "2"
		ctx := BuildSessionContext(entries, &leaf, nil)
		// Should only get the orphan since parent chain is broken
		if len(ctx.Messages) != 1 {
			t.Fatalf("expected 1 message, got %d", len(ctx.Messages))
		}
	})
}
