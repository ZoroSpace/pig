// pi: test/session-manager/labels.test.ts — label set/clear/last-wins, tree
// nodes, and createBranchedSession label preservation.
package core

import (
	"strings"
	"testing"

	"github.com/samber/lo"

	"github.com/ZoroSpace/pig/ai/types"
)

func labelAssistantMsg(text string) types.AssistantMessage {
	msg := assistantMsg(text)
	msg.Timestamp = 2
	return msg
}

func TestSessionManagerLabels(t *testing.T) {
	t.Run("sets and gets labels", func(t *testing.T) {
		session, _ := InMemorySession("", nil)

		msgID, err := session.AppendMessage(userMsg("hello"))
		mustAppend(t, msgID, err)

		// No label initially
		if _, ok := session.GetLabel(msgID); ok {
			t.Fatal("expected no label initially")
		}

		// Set a label
		labelID, err := session.AppendLabelChange(msgID, lo.ToPtr("checkpoint"))
		mustAppend(t, labelID, err)
		if label, ok := session.GetLabel(msgID); !ok || label != "checkpoint" {
			t.Fatalf("label = %q, %v", label, ok)
		}

		// Label entry should be in entries
		entries := session.GetEntries()
		var labelEntry *LabelEntry
		for _, entry := range entries {
			if e, ok := entry.(*LabelEntry); ok {
				labelEntry = e
			}
		}
		if labelEntry == nil || labelEntry.Base().ID != labelID ||
			labelEntry.TargetID != msgID || *labelEntry.Label != "checkpoint" {
			t.Fatalf("label entry = %#v", labelEntry)
		}
	})

	t.Run("clears labels with undefined", func(t *testing.T) {
		session, _ := InMemorySession("", nil)

		msgID, err := session.AppendMessage(userMsg("hello"))
		mustAppend(t, msgID, err)

		_, _ = session.AppendLabelChange(msgID, lo.ToPtr("checkpoint"))
		if _, ok := session.GetLabel(msgID); !ok {
			t.Fatal("expected label")
		}

		// Clear the label
		_, _ = session.AppendLabelChange(msgID, nil)
		if _, ok := session.GetLabel(msgID); ok {
			t.Fatal("expected label cleared")
		}
	})

	t.Run("last label wins", func(t *testing.T) {
		session, _ := InMemorySession("", nil)

		msgID, err := session.AppendMessage(userMsg("hello"))
		mustAppend(t, msgID, err)

		_, _ = session.AppendLabelChange(msgID, lo.ToPtr("first"))
		_, _ = session.AppendLabelChange(msgID, lo.ToPtr("second"))
		lastLabelID, err := session.AppendLabelChange(msgID, lo.ToPtr("third"))
		mustAppend(t, lastLabelID, err)

		if label, _ := session.GetLabel(msgID); label != "third" {
			t.Fatalf("label = %q", label)
		}

		entries := session.GetEntries()
		var lastLabelEntry *LabelEntry
		for _, entry := range entries {
			if e, ok := entry.(*LabelEntry); ok && e.Base().ID == lastLabelID {
				lastLabelEntry = e
			}
		}
		tree := session.GetTree()
		var msgNode *SessionTreeNode
		for _, n := range tree {
			if n.Entry.Base().ID == msgID {
				msgNode = n
			}
		}
		if msgNode == nil || lastLabelEntry == nil ||
			msgNode.LabelTimestamp == nil || *msgNode.LabelTimestamp != lastLabelEntry.Base().Timestamp {
			t.Fatalf("label timestamp = %v, entry = %#v", msgNode, lastLabelEntry)
		}
	})

	t.Run("labels are included in tree nodes", func(t *testing.T) {
		session, _ := InMemorySession("", nil)

		msg1ID, err := session.AppendMessage(userMsg("hello"))
		mustAppend(t, msg1ID, err)
		msg2ID, err := session.AppendMessage(labelAssistantMsg("hi"))
		mustAppend(t, msg2ID, err)

		msg1LabelID, err := session.AppendLabelChange(msg1ID, lo.ToPtr("start"))
		mustAppend(t, msg1LabelID, err)
		msg2LabelID, err := session.AppendLabelChange(msg2ID, lo.ToPtr("response"))
		mustAppend(t, msg2LabelID, err)

		entries := session.GetEntries()
		var msg1LabelEntry, msg2LabelEntry *LabelEntry
		for _, entry := range entries {
			if e, ok := entry.(*LabelEntry); ok {
				if e.Base().ID == msg1LabelID {
					msg1LabelEntry = e
				}
				if e.Base().ID == msg2LabelID {
					msg2LabelEntry = e
				}
			}
		}
		tree := session.GetTree()

		// Find the message nodes (skip label entries)
		var msg1Node *SessionTreeNode
		for _, n := range tree {
			if n.Entry.Base().ID == msg1ID {
				msg1Node = n
			}
		}
		if msg1Node == nil || msg1Node.Label == nil || *msg1Node.Label != "start" ||
			msg1Node.LabelTimestamp == nil || *msg1Node.LabelTimestamp != msg1LabelEntry.Base().Timestamp {
			t.Fatalf("msg1 node = %#v", msg1Node)
		}

		// msg2 is a child of msg1
		var msg2Node *SessionTreeNode
		for _, n := range msg1Node.Children {
			if n.Entry.Base().ID == msg2ID {
				msg2Node = n
			}
		}
		if msg2Node == nil || msg2Node.Label == nil || *msg2Node.Label != "response" ||
			msg2Node.LabelTimestamp == nil || *msg2Node.LabelTimestamp != msg2LabelEntry.Base().Timestamp {
			t.Fatalf("msg2 node = %#v", msg2Node)
		}
	})

	t.Run("labels are preserved in createBranchedSession", func(t *testing.T) {
		session, _ := InMemorySession("", nil)

		msg1ID, err := session.AppendMessage(userMsg("hello"))
		mustAppend(t, msg1ID, err)
		msg2ID, err := session.AppendMessage(labelAssistantMsg("hi"))
		mustAppend(t, msg2ID, err)

		msg1LabelID, err := session.AppendLabelChange(msg1ID, lo.ToPtr("important"))
		mustAppend(t, msg1LabelID, err)
		msg2LabelID, err := session.AppendLabelChange(msg2ID, lo.ToPtr("also-important"))
		mustAppend(t, msg2LabelID, err)
		originalEntries := session.GetEntries()
		var msg1LabelEntry, msg2LabelEntry *LabelEntry
		for _, entry := range originalEntries {
			if e, ok := entry.(*LabelEntry); ok {
				if e.Base().ID == msg1LabelID {
					msg1LabelEntry = e
				}
				if e.Base().ID == msg2LabelID {
					msg2LabelEntry = e
				}
			}
		}

		// Branch from msg2 (in-memory mode returns null, but updates internal state)
		if _, err := session.CreateBranchedSession(msg2ID); err != nil {
			t.Fatal(err)
		}

		// Labels should be preserved
		if label, _ := session.GetLabel(msg1ID); label != "important" {
			t.Fatalf("msg1 label = %q", label)
		}
		if label, _ := session.GetLabel(msg2ID); label != "also-important" {
			t.Fatalf("msg2 label = %q", label)
		}

		// New label entries should exist
		entries := session.GetEntries()
		labelCount := 0
		for _, entry := range entries {
			if _, ok := entry.(*LabelEntry); ok {
				labelCount++
			}
		}
		if labelCount != 2 {
			t.Fatalf("expected 2 label entries, got %d", labelCount)
		}

		tree := session.GetTree()
		var msg1Node *SessionTreeNode
		for _, n := range tree {
			if n.Entry.Base().ID == msg1ID {
				msg1Node = n
			}
		}
		var msg2Node *SessionTreeNode
		for _, n := range msg1Node.Children {
			if n.Entry.Base().ID == msg2ID {
				msg2Node = n
			}
		}
		if msg1Node.LabelTimestamp == nil || *msg1Node.LabelTimestamp != msg1LabelEntry.Base().Timestamp ||
			msg2Node.LabelTimestamp == nil || *msg2Node.LabelTimestamp != msg2LabelEntry.Base().Timestamp {
			t.Fatalf("label timestamps drifted: %#v %#v", msg1Node, msg2Node)
		}
	})

	t.Run("rewires children of removed labels when forking", func(t *testing.T) {
		session, _ := InMemorySession("", nil)

		msg1ID, err := session.AppendMessage(userMsg("hello"))
		mustAppend(t, msg1ID, err)
		_, _ = session.AppendLabelChange(msg1ID, lo.ToPtr("checkpoint"))
		modelChangeID, err := session.AppendModelChange("anthropic", "claude-test")
		mustAppend(t, modelChangeID, err)
		msg2ID, err := session.AppendMessage(types.UserMessage{
			Role: types.RoleUser, Content: types.UserContent{String: "followup"}, Timestamp: 2,
		})
		mustAppend(t, msg2ID, err)

		if _, err := session.CreateBranchedSession(msg2ID); err != nil {
			t.Fatal(err)
		}

		entry := session.GetEntry(modelChangeID)
		if entry == nil || *entry.Base().ParentID != msg1ID {
			t.Fatalf("model change parent = %#v", entry)
		}
	})

	t.Run("labels not on path are not preserved in createBranchedSession", func(t *testing.T) {
		session, _ := InMemorySession("", nil)

		msg1ID, err := session.AppendMessage(userMsg("hello"))
		mustAppend(t, msg1ID, err)
		msg2ID, err := session.AppendMessage(labelAssistantMsg("hi"))
		mustAppend(t, msg2ID, err)
		msg3ID, err := session.AppendMessage(types.UserMessage{
			Role: types.RoleUser, Content: types.UserContent{String: "followup"}, Timestamp: 3,
		})
		mustAppend(t, msg3ID, err)

		// Label all messages
		_, _ = session.AppendLabelChange(msg1ID, lo.ToPtr("first"))
		_, _ = session.AppendLabelChange(msg2ID, lo.ToPtr("second"))
		_, _ = session.AppendLabelChange(msg3ID, lo.ToPtr("third"))

		// Branch from msg2 (excludes msg3)
		if _, err := session.CreateBranchedSession(msg2ID); err != nil {
			t.Fatal(err)
		}

		// Only labels for msg1 and msg2 should be preserved
		if label, _ := session.GetLabel(msg1ID); label != "first" {
			t.Fatalf("msg1 label = %q", label)
		}
		if label, _ := session.GetLabel(msg2ID); label != "second" {
			t.Fatalf("msg2 label = %q", label)
		}
		if _, ok := session.GetLabel(msg3ID); ok {
			t.Fatal("msg3 label should be cleared")
		}
	})

	t.Run("labels are not included in buildSessionContext", func(t *testing.T) {
		session, _ := InMemorySession("", nil)

		msgID, err := session.AppendMessage(userMsg("hello"))
		mustAppend(t, msgID, err)
		_, _ = session.AppendLabelChange(msgID, lo.ToPtr("checkpoint"))

		ctx := session.BuildSessionContext()
		if len(ctx.Messages) != 1 || ctx.Messages[0].MessageRole() != types.RoleUser {
			t.Fatalf("messages = %#v", ctx.Messages)
		}
	})

	t.Run("errors when labeling non-existent entry", func(t *testing.T) {
		session, _ := InMemorySession("", nil)

		_, err := session.AppendLabelChange("non-existent", lo.ToPtr("label"))
		if err == nil || !strings.Contains(err.Error(), "Entry non-existent not found") {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestForkPreservesCompactionWhenBoundaryLabelRemoved(t *testing.T) {
	// pi regression #8989: createBranchedSession re-chains the retained path
	// past removed labels; a compaction whose firstKeptEntryId pointed at the
	// removed boundary label must be remapped to the first real entry after
	// it, or the branch loses the pre-compaction context.
	session, err := InMemorySession("", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	oldID, err := session.AppendMessage(userMsg("old"))
	if err != nil {
		t.Fatal(err)
	}
	labelID, err := session.AppendLabelChange(oldID, lo.ToPtr("checkpoint"))
	if err != nil {
		t.Fatal(err)
	}
	keptID, err := session.AppendMessage(userMsg("kept"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.AppendCompaction("summary", lo.ToPtr(labelID), 100, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	leafID, err := session.AppendMessage(userMsg("after"))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := session.CreateBranchedSession(leafID); err != nil {
		t.Fatal(err)
	}

	compaction, ok := session.GetEntry(compactionIDOf(session, "summary")).(*CompactionEntry)
	if !ok || compaction == nil {
		t.Fatal("expected a compaction entry")
	}
	if compaction.FirstKeptEntryID != keptID {
		t.Fatalf("firstKeptEntryId = %q, want %q", compaction.FirstKeptEntryID, keptID)
	}
	context := session.BuildSessionContext()
	if len(context.Messages) != 3 {
		t.Fatalf("messages = %d", len(context.Messages))
	}
	roles := []string{}
	for _, message := range context.Messages {
		roles = append(roles, string(message.MessageRole()))
	}
	if roles[0] != "compactionSummary" || roles[1] != "user" || roles[2] != "user" {
		t.Fatalf("roles = %v", roles)
	}
}

// compactionIDOf finds the compaction entry id by its summary text.
func compactionIDOf(session *SessionManager, summary string) string {
	for _, entry := range session.GetEntries() {
		if compaction, ok := entry.(*CompactionEntry); ok && compaction.Summary == summary {
			return entry.Base().ID
		}
	}
	return ""
}
