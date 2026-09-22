// pi: test/session-manager/tree-traversal.test.ts — append operations, path
// walking, getTree, branching, createBranchedSession.
package core

import (
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/samber/lo"

	"github.com/ZoroSpace/pig/ai/types"
)

func mustAppend(t *testing.T, id string, err error) string {
	t.Helper()
	if err != nil {
		t.Fatalf("append failed: %v", err)
	}
	return id
}

func TestAppendOperations(t *testing.T) {
	t.Run("appendMessage creates entry with correct parentId chain", func(t *testing.T) {
		session, err := InMemorySession("", nil)
		if err != nil {
			t.Fatal(err)
		}

		id1, err := session.AppendMessage(userMsg("first"))
		mustAppend(t, id1, err)
		id2, err := session.AppendMessage(assistantMsg("second"))
		mustAppend(t, id2, err)
		id3, err := session.AppendMessage(userMsg("third"))
		mustAppend(t, id3, err)

		entries := session.GetEntries()
		if len(entries) != 3 {
			t.Fatalf("expected 3 entries, got %d", len(entries))
		}

		if entries[0].Base().ID != id1 || entries[0].Base().ParentID != nil || entries[0].EntryType() != "message" {
			t.Fatalf("entry 0 = %#v", entries[0].Base())
		}
		if entries[1].Base().ID != id2 || *entries[1].Base().ParentID != id1 {
			t.Fatalf("entry 1 = %#v", entries[1].Base())
		}
		if entries[2].Base().ID != id3 || *entries[2].Base().ParentID != id2 {
			t.Fatalf("entry 2 = %#v", entries[2].Base())
		}
	})

	t.Run("appendThinkingLevelChange integrates into tree", func(t *testing.T) {
		session, _ := InMemorySession("", nil)

		msgID, err := session.AppendMessage(userMsg("hello"))
		mustAppend(t, msgID, err)
		thinkingID, err := session.AppendThinkingLevelChange("high")
		mustAppend(t, thinkingID, err)
		_, _ = session.AppendMessage(assistantMsg("response"))

		entries := session.GetEntries()
		if len(entries) != 3 {
			t.Fatalf("expected 3 entries, got %d", len(entries))
		}

		var thinkingEntry *ThinkingLevelChangeEntry
		for _, entry := range entries {
			if e, ok := entry.(*ThinkingLevelChangeEntry); ok {
				thinkingEntry = e
			}
		}
		if thinkingEntry == nil || thinkingEntry.Base().ID != thinkingID || *thinkingEntry.Base().ParentID != msgID {
			t.Fatalf("thinking entry = %#v", thinkingEntry)
		}
		if *entries[2].Base().ParentID != thinkingID {
			t.Fatalf("entry 2 parent = %v", entries[2].Base().ParentID)
		}
	})

	t.Run("appendModelChange integrates into tree", func(t *testing.T) {
		session, _ := InMemorySession("", nil)

		msgID, err := session.AppendMessage(userMsg("hello"))
		mustAppend(t, msgID, err)
		modelID, err := session.AppendModelChange("openai", "gpt-4")
		mustAppend(t, modelID, err)
		_, _ = session.AppendMessage(assistantMsg("response"))

		entries := session.GetEntries()
		var modelEntry *ModelChangeEntry
		for _, entry := range entries {
			if e, ok := entry.(*ModelChangeEntry); ok {
				modelEntry = e
			}
		}
		if modelEntry == nil || modelEntry.Base().ID != modelID || *modelEntry.Base().ParentID != msgID {
			t.Fatalf("model entry = %#v", modelEntry)
		}
		if modelEntry.Provider != "openai" || modelEntry.ModelID != "gpt-4" {
			t.Fatalf("model change = %#v", modelEntry)
		}
		if *entries[2].Base().ParentID != modelID {
			t.Fatalf("entry 2 parent = %v", entries[2].Base().ParentID)
		}
	})

	t.Run("appendCompaction integrates into tree", func(t *testing.T) {
		session, _ := InMemorySession("", nil)

		id1, err := session.AppendMessage(userMsg("1"))
		mustAppend(t, id1, err)
		id2, err := session.AppendMessage(assistantMsg("2"))
		mustAppend(t, id2, err)
		usage := types.Usage{
			Input: 10, Output: 20, CacheRead: 30, CacheWrite: 40, TotalTokens: 100,
			Cost: types.Cost{Input: 0.1, Output: 0.2, CacheRead: 0.3, CacheWrite: 0.4, Total: 1},
		}
		compactionID, err := session.AppendCompaction("summary", lo.ToPtr(id1), 1000, nil, lo.ToPtr(false), &usage)
		mustAppend(t, compactionID, err)
		_, _ = session.AppendMessage(userMsg("3"))

		entries := session.GetEntries()
		var compactionEntry *CompactionEntry
		for _, entry := range entries {
			if e, ok := entry.(*CompactionEntry); ok {
				compactionEntry = e
			}
		}
		if compactionEntry == nil || compactionEntry.Base().ID != compactionID || *compactionEntry.Base().ParentID != id2 {
			t.Fatalf("compaction entry = %#v", compactionEntry)
		}
		if compactionEntry.Summary != "summary" || compactionEntry.FirstKeptEntryID != id1 ||
			compactionEntry.TokensBefore != 1000 || !reflect.DeepEqual(compactionEntry.Usage, &usage) {
			t.Fatalf("compaction = %#v", compactionEntry)
		}
		if *entries[3].Base().ParentID != compactionID {
			t.Fatalf("entry 3 parent = %v", entries[3].Base().ParentID)
		}
	})

	t.Run("appendCustomEntry integrates into tree", func(t *testing.T) {
		session, _ := InMemorySession("", nil)

		msgID, err := session.AppendMessage(userMsg("hello"))
		mustAppend(t, msgID, err)
		customID, err := session.AppendCustomEntry("my_data", map[string]any{"key": "value"})
		mustAppend(t, customID, err)
		_, _ = session.AppendMessage(assistantMsg("response"))

		entries := session.GetEntries()
		var customEntry *CustomEntry
		for _, entry := range entries {
			if e, ok := entry.(*CustomEntry); ok {
				customEntry = e
			}
		}
		if customEntry == nil || customEntry.Base().ID != customID || *customEntry.Base().ParentID != msgID {
			t.Fatalf("custom entry = %#v", customEntry)
		}
		if customEntry.CustomType != "my_data" {
			t.Fatalf("customType = %q", customEntry.CustomType)
		}
		if !reflect.DeepEqual(customEntry.Data, map[string]any{"key": "value"}) {
			t.Fatalf("data = %#v", customEntry.Data)
		}
		if *entries[2].Base().ParentID != customID {
			t.Fatalf("entry 2 parent = %v", entries[2].Base().ParentID)
		}
	})

	t.Run("leaf pointer advances after each append", func(t *testing.T) {
		session, _ := InMemorySession("", nil)

		if session.GetLeafID() != nil {
			t.Fatal("expected nil leaf initially")
		}

		id1, err := session.AppendMessage(userMsg("1"))
		mustAppend(t, id1, err)
		if *session.GetLeafID() != id1 {
			t.Fatal("leaf should be id1")
		}

		id2, err := session.AppendMessage(assistantMsg("2"))
		mustAppend(t, id2, err)
		if *session.GetLeafID() != id2 {
			t.Fatal("leaf should be id2")
		}

		id3, err := session.AppendThinkingLevelChange("high")
		mustAppend(t, id3, err)
		if *session.GetLeafID() != id3 {
			t.Fatal("leaf should be id3")
		}
	})
}

func TestGetPath(t *testing.T) {
	t.Run("returns empty array for empty session", func(t *testing.T) {
		session, _ := InMemorySession("", nil)
		if len(session.GetBranch("")) != 0 {
			t.Fatal("expected empty branch")
		}
	})

	t.Run("returns single entry path", func(t *testing.T) {
		session, _ := InMemorySession("", nil)
		id, err := session.AppendMessage(userMsg("hello"))
		mustAppend(t, id, err)

		path := session.GetBranch("")
		if len(path) != 1 || path[0].Base().ID != id {
			t.Fatalf("path = %#v", path)
		}
	})

	t.Run("returns full path from root to leaf", func(t *testing.T) {
		session, _ := InMemorySession("", nil)

		id1, err := session.AppendMessage(userMsg("1"))
		mustAppend(t, id1, err)
		id2, err := session.AppendMessage(assistantMsg("2"))
		mustAppend(t, id2, err)
		id3, err := session.AppendThinkingLevelChange("high")
		mustAppend(t, id3, err)
		id4, err := session.AppendMessage(userMsg("3"))
		mustAppend(t, id4, err)

		path := session.GetBranch("")
		if len(path) != 4 {
			t.Fatalf("expected 4 entries, got %d", len(path))
		}
		want := []string{id1, id2, id3, id4}
		for i, entry := range path {
			if entry.Base().ID != want[i] {
				t.Fatalf("path[%d] = %s, want %s", i, entry.Base().ID, want[i])
			}
		}
	})

	t.Run("returns path from specified entry to root", func(t *testing.T) {
		session, _ := InMemorySession("", nil)

		id1, err := session.AppendMessage(userMsg("1"))
		mustAppend(t, id1, err)
		id2, err := session.AppendMessage(assistantMsg("2"))
		mustAppend(t, id2, err)
		_, _ = session.AppendMessage(userMsg("3"))
		_, _ = session.AppendMessage(assistantMsg("4"))

		path := session.GetBranch(id2)
		if len(path) != 2 || path[0].Base().ID != id1 || path[1].Base().ID != id2 {
			t.Fatalf("path = %#v", path)
		}
	})
}

func treeEntryIDs(nodes []*SessionTreeNode) []string {
	ids := make([]string, 0, len(nodes))
	for _, n := range nodes {
		ids = append(ids, n.Entry.Base().ID)
	}
	return ids
}

func findNodeByID(nodes []*SessionTreeNode, id string) *SessionTreeNode {
	for _, n := range nodes {
		if n.Entry.Base().ID == id {
			return n
		}
	}
	return nil
}

func TestGetTree(t *testing.T) {
	t.Run("returns empty array for empty session", func(t *testing.T) {
		session, _ := InMemorySession("", nil)
		if len(session.GetTree()) != 0 {
			t.Fatal("expected empty tree")
		}
	})

	t.Run("returns single root for linear session", func(t *testing.T) {
		session, _ := InMemorySession("", nil)

		id1, err := session.AppendMessage(userMsg("1"))
		mustAppend(t, id1, err)
		id2, err := session.AppendMessage(assistantMsg("2"))
		mustAppend(t, id2, err)
		id3, err := session.AppendMessage(userMsg("3"))
		mustAppend(t, id3, err)

		tree := session.GetTree()
		if len(tree) != 1 {
			t.Fatalf("expected 1 root, got %d", len(tree))
		}

		root := tree[0]
		if root.Entry.Base().ID != id1 || len(root.Children) != 1 || root.Children[0].Entry.Base().ID != id2 {
			t.Fatalf("unexpected tree: %#v", tree)
		}
		child := root.Children[0]
		if len(child.Children) != 1 || child.Children[0].Entry.Base().ID != id3 {
			t.Fatalf("unexpected subtree: %#v", child)
		}
		if len(child.Children[0].Children) != 0 {
			t.Fatal("expected leaf")
		}
	})

	t.Run("returns tree with branches after branch", func(t *testing.T) {
		session, _ := InMemorySession("", nil)

		// Build: 1 -> 2 -> 3
		id1, err := session.AppendMessage(userMsg("1"))
		mustAppend(t, id1, err)
		id2, err := session.AppendMessage(assistantMsg("2"))
		mustAppend(t, id2, err)
		id3, err := session.AppendMessage(userMsg("3"))
		mustAppend(t, id3, err)

		// Branch from id2, add new path: 2 -> 4
		if err := session.Branch(id2); err != nil {
			t.Fatal(err)
		}
		id4, err := session.AppendMessage(userMsg("4-branch"))
		mustAppend(t, id4, err)

		tree := session.GetTree()
		if len(tree) != 1 {
			t.Fatalf("expected 1 root, got %d", len(tree))
		}
		root := tree[0]
		if root.Entry.Base().ID != id1 || len(root.Children) != 1 {
			t.Fatalf("unexpected root: %#v", root)
		}
		node2 := root.Children[0]
		if node2.Entry.Base().ID != id2 || len(node2.Children) != 2 {
			t.Fatalf("unexpected node2: %#v", node2)
		}
		childIDs := treeEntryIDs(node2.Children)
		sort.Strings(childIDs)
		want := []string{id3, id4}
		sort.Strings(want)
		if !reflect.DeepEqual(childIDs, want) {
			t.Fatalf("children = %#v, want %v", childIDs, want)
		}
	})

	t.Run("handles multiple branches at same point", func(t *testing.T) {
		session, _ := InMemorySession("", nil)

		_, _ = session.AppendMessage(userMsg("root"))
		id2, err := session.AppendMessage(assistantMsg("response"))
		mustAppend(t, id2, err)

		// Branch A
		_ = session.Branch(id2)
		idA, err := session.AppendMessage(userMsg("branch-A"))
		mustAppend(t, idA, err)

		// Branch B
		_ = session.Branch(id2)
		idB, err := session.AppendMessage(userMsg("branch-B"))
		mustAppend(t, idB, err)

		// Branch C
		_ = session.Branch(id2)
		idC, err := session.AppendMessage(userMsg("branch-C"))
		mustAppend(t, idC, err)

		tree := session.GetTree()
		node2 := tree[0].Children[0]
		if node2.Entry.Base().ID != id2 || len(node2.Children) != 3 {
			t.Fatalf("unexpected node2: %#v", node2)
		}
		branchIDs := treeEntryIDs(node2.Children)
		sort.Strings(branchIDs)
		want := []string{idA, idB, idC}
		sort.Strings(want)
		if !reflect.DeepEqual(branchIDs, want) {
			t.Fatalf("branches = %#v, want %v", branchIDs, want)
		}
	})

	t.Run("handles deep branching", func(t *testing.T) {
		session, _ := InMemorySession("", nil)

		// Main path: 1 -> 2 -> 3 -> 4
		_, _ = session.AppendMessage(userMsg("1"))
		id2, err := session.AppendMessage(assistantMsg("2"))
		mustAppend(t, id2, err)
		id3, err := session.AppendMessage(userMsg("3"))
		mustAppend(t, id3, err)
		_, _ = session.AppendMessage(assistantMsg("4"))

		// Branch from 2: 2 -> 5 -> 6
		_ = session.Branch(id2)
		id5, err := session.AppendMessage(userMsg("5"))
		mustAppend(t, id5, err)
		_, _ = session.AppendMessage(assistantMsg("6"))

		// Branch from 5: 5 -> 7
		_ = session.Branch(id5)
		_, _ = session.AppendMessage(userMsg("7"))

		tree := session.GetTree()

		// Verify structure
		node2 := tree[0].Children[0]
		if len(node2.Children) != 2 { // id3 and id5
			t.Fatalf("node2 children = %d", len(node2.Children))
		}

		node5 := findNodeByID(node2.Children, id5)
		if node5 == nil || len(node5.Children) != 2 { // id6 and id7
			t.Fatalf("node5 children = %v", node5)
		}

		node3 := findNodeByID(node2.Children, id3)
		if node3 == nil || len(node3.Children) != 1 { // id4
			t.Fatalf("node3 children = %v", node3)
		}
	})
}

func TestBranch(t *testing.T) {
	t.Run("moves leaf pointer to specified entry", func(t *testing.T) {
		session, _ := InMemorySession("", nil)

		id1, err := session.AppendMessage(userMsg("1"))
		mustAppend(t, id1, err)
		_, _ = session.AppendMessage(assistantMsg("2"))
		id3, err := session.AppendMessage(userMsg("3"))
		mustAppend(t, id3, err)

		if *session.GetLeafID() != id3 {
			t.Fatal("leaf should be id3")
		}

		if err := session.Branch(id1); err != nil {
			t.Fatal(err)
		}
		if *session.GetLeafID() != id1 {
			t.Fatal("leaf should be id1")
		}
	})

	t.Run("errors for non-existent entry", func(t *testing.T) {
		session, _ := InMemorySession("", nil)
		_, _ = session.AppendMessage(userMsg("hello"))

		err := session.Branch("nonexistent")
		if err == nil || !strings.Contains(err.Error(), "Entry nonexistent not found") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("new appends become children of branch point", func(t *testing.T) {
		session, _ := InMemorySession("", nil)

		id1, err := session.AppendMessage(userMsg("1"))
		mustAppend(t, id1, err)
		_, _ = session.AppendMessage(assistantMsg("2"))

		_ = session.Branch(id1)
		id3, err := session.AppendMessage(userMsg("branched"))
		mustAppend(t, id3, err)

		entries := session.GetEntries()
		var branchedEntry SessionEntry
		for _, entry := range entries {
			if entry.Base().ID == id3 {
				branchedEntry = entry
			}
		}
		if branchedEntry == nil || *branchedEntry.Base().ParentID != id1 {
			t.Fatalf("branched entry = %#v", branchedEntry)
		}
	})
}

func TestBranchWithSummary(t *testing.T) {
	t.Run("inserts branch summary with the source and destination and advances leaf", func(t *testing.T) {
		session, _ := InMemorySession("", nil)

		id1, err := session.AppendMessage(userMsg("1"))
		mustAppend(t, id1, err)
		_, _ = session.AppendMessage(assistantMsg("2"))
		id3, err := session.AppendMessage(userMsg("3"))
		mustAppend(t, id3, err)

		usage := types.Usage{
			Input: 10, Output: 20, CacheRead: 30, CacheWrite: 40, TotalTokens: 100,
			Cost: types.Cost{Input: 0.1, Output: 0.2, CacheRead: 0.3, CacheWrite: 0.4, Total: 1},
		}
		summaryID, err := session.BranchWithSummary(id1, "Summary of abandoned work", nil, lo.ToPtr(false), &usage)
		mustAppend(t, summaryID, err)

		if *session.GetLeafID() != summaryID {
			t.Fatal("leaf should be the summary entry")
		}

		entries := session.GetEntries()
		var summaryEntry *BranchSummaryEntry
		for _, entry := range entries {
			if e, ok := entry.(*BranchSummaryEntry); ok {
				summaryEntry = e
			}
		}
		if summaryEntry == nil || *summaryEntry.Base().ParentID != id1 {
			t.Fatalf("summary entry = %#v", summaryEntry)
		}
		if summaryEntry.FromID != id3 || summaryEntry.Summary != "Summary of abandoned work" ||
			!reflect.DeepEqual(summaryEntry.Usage, &usage) {
			t.Fatalf("summary = %#v", summaryEntry)
		}
	})

	t.Run("errors for non-existent entry", func(t *testing.T) {
		session, _ := InMemorySession("", nil)
		_, _ = session.AppendMessage(userMsg("hello"))

		_, err := session.BranchWithSummary("nonexistent", "summary", nil, nil, nil)
		if err == nil || !strings.Contains(err.Error(), "Entry nonexistent not found") {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestGetLeafEntryAndEntry(t *testing.T) {
	t.Run("returns nil for empty session", func(t *testing.T) {
		session, _ := InMemorySession("", nil)
		if session.GetLeafEntry() != nil {
			t.Fatal("expected nil leaf entry")
		}
	})

	t.Run("returns current leaf entry", func(t *testing.T) {
		session, _ := InMemorySession("", nil)

		_, _ = session.AppendMessage(userMsg("1"))
		id2, err := session.AppendMessage(assistantMsg("2"))
		mustAppend(t, id2, err)

		leaf := session.GetLeafEntry()
		if leaf == nil || leaf.Base().ID != id2 {
			t.Fatalf("leaf = %#v", leaf)
		}
	})

	t.Run("returns nil for non-existent id", func(t *testing.T) {
		session, _ := InMemorySession("", nil)
		if session.GetEntry("nonexistent") != nil {
			t.Fatal("expected nil entry")
		}
	})

	t.Run("returns entry by id", func(t *testing.T) {
		session, _ := InMemorySession("", nil)

		id1, err := session.AppendMessage(userMsg("first"))
		mustAppend(t, id1, err)
		id2, err := session.AppendMessage(assistantMsg("second"))
		mustAppend(t, id2, err)

		entry1 := session.GetEntry(id1)
		if entry1 == nil || entry1.EntryType() != "message" {
			t.Fatalf("entry1 = %#v", entry1)
		}
		if um, ok := entry1.(*SessionMessageEntry); !ok ||
			um.Message.(types.UserMessage).Content.String != "first" {
			t.Fatalf("entry1 message = %#v", entry1)
		}

		entry2 := session.GetEntry(id2)
		if entry2 == nil {
			t.Fatal("entry2 missing")
		}
		if am, ok := entry2.(*SessionMessageEntry); !ok ||
			am.Message.(types.AssistantMessage).Content[0].(types.TextContent).Text != "second" {
			t.Fatalf("entry2 message = %#v", entry2)
		}
	})
}

func TestBuildSessionContextWithBranchesOnManager(t *testing.T) {
	t.Run("returns messages from current branch only", func(t *testing.T) {
		session, _ := InMemorySession("", nil)

		// Main: 1 -> 2 -> 3
		_, _ = session.AppendMessage(userMsg("msg1"))
		id2, err := session.AppendMessage(assistantMsg("msg2"))
		mustAppend(t, id2, err)
		_, _ = session.AppendMessage(userMsg("msg3"))

		// Branch from 2: 2 -> 4
		_ = session.Branch(id2)
		_, _ = session.AppendMessage(assistantMsg("msg4-branch"))

		ctx := session.BuildSessionContext()
		if len(ctx.Messages) != 3 { // msg1, msg2, msg4-branch (not msg3)
			t.Fatalf("expected 3 messages, got %d", len(ctx.Messages))
		}

		if userContentString(ctx.Messages[0]) != "msg1" ||
			assistantContentText(ctx.Messages[1]) != "msg2" ||
			assistantContentText(ctx.Messages[2]) != "msg4-branch" {
			t.Fatalf("unexpected messages: %#v", ctx.Messages)
		}
	})
}

func TestCreateBranchedSession(t *testing.T) {
	t.Run("errors for non-existent entry", func(t *testing.T) {
		session, _ := InMemorySession("", nil)
		_, _ = session.AppendMessage(userMsg("hello"))

		_, err := session.CreateBranchedSession("nonexistent")
		if err == nil || !strings.Contains(err.Error(), "Entry nonexistent not found") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("creates new session with path to specified leaf (in-memory)", func(t *testing.T) {
		session, _ := InMemorySession("", nil)

		// Build: 1 -> 2 -> 3 -> 4
		id1, err := session.AppendMessage(userMsg("1"))
		mustAppend(t, id1, err)
		id2, err := session.AppendMessage(assistantMsg("2"))
		mustAppend(t, id2, err)
		id3, err := session.AppendMessage(userMsg("3"))
		mustAppend(t, id3, err)
		_, _ = session.AppendMessage(assistantMsg("4"))

		// Branch from 3: 3 -> 5
		_ = session.Branch(id3)
		_, _ = session.AppendMessage(userMsg("5"))

		// Create branched session from id2 (should only have 1 -> 2)
		result, err := session.CreateBranchedSession(id2)
		if err != nil {
			t.Fatal(err)
		}
		if result != "" { // in-memory returns undefined in pi
			t.Fatalf("expected empty result, got %q", result)
		}

		// Session should now only have entries 1 and 2
		entries := session.GetEntries()
		if len(entries) != 2 || entries[0].Base().ID != id1 || entries[1].Base().ID != id2 {
			t.Fatalf("entries = %#v", entries)
		}
	})

	t.Run("extracts correct path from branched tree", func(t *testing.T) {
		session, _ := InMemorySession("", nil)

		// Build: 1 -> 2 -> 3
		id1, err := session.AppendMessage(userMsg("1"))
		mustAppend(t, id1, err)
		id2, err := session.AppendMessage(assistantMsg("2"))
		mustAppend(t, id2, err)
		_, _ = session.AppendMessage(userMsg("3"))

		// Branch from 2: 2 -> 4 -> 5
		_ = session.Branch(id2)
		id4, err := session.AppendMessage(userMsg("4"))
		mustAppend(t, id4, err)
		id5, err := session.AppendMessage(assistantMsg("5"))
		mustAppend(t, id5, err)

		// Create branched session from id5 (should have 1 -> 2 -> 4 -> 5)
		if _, err := session.CreateBranchedSession(id5); err != nil {
			t.Fatal(err)
		}

		entries := session.GetEntries()
		if len(entries) != 4 {
			t.Fatalf("expected 4 entries, got %d", len(entries))
		}
		want := []string{id1, id2, id4, id5}
		for i, entry := range entries {
			if entry.Base().ID != want[i] {
				t.Fatalf("entries[%d] = %s, want %s", i, entry.Base().ID, want[i])
			}
		}
	})

	t.Run("does not duplicate entries when forking from first user message", func(t *testing.T) {
		tempDir := newTempDir(t)

		// Create a persisted session with a couple of turns
		session, err := CreateSessionManager(tempDir, tempDir, nil)
		if err != nil {
			t.Fatal(err)
		}
		id1, err := session.AppendMessage(userMsg("first question"))
		mustAppend(t, id1, err)
		_, _ = session.AppendMessage(assistantMsg("first answer"))
		_, _ = session.AppendMessage(userMsg("second question"))
		_, _ = session.AppendMessage(assistantMsg("second answer"))

		// Fork from the very first user message (no assistant in the branched path)
		newFile, err := session.CreateBranchedSession(id1)
		if err != nil {
			t.Fatal(err)
		}

		// The branched path has no assistant, so the file should not exist yet
		// (deferred to _persist on first assistant, matching newSession() contract)
		if _, err := os.Stat(newFile); !os.IsNotExist(err) {
			t.Fatalf("expected file to not exist: %s", newFile)
		}

		// Simulate extension adding entry before assistant (like preset on turn_start)
		_, _ = session.AppendCustomEntry("preset-state", map[string]any{"name": "plan"})

		// Now the assistant responds
		_, _ = session.AppendMessage(assistantMsg("new answer"))

		// File should now exist with exactly one header and no duplicate IDs
		if _, err := os.Stat(newFile); err != nil {
			t.Fatalf("expected file to exist: %v", err)
		}
		content, err := os.ReadFile(newFile)
		if err != nil {
			t.Fatal(err)
		}
		headers := 0
		entryIDs := 0
		idSet := map[string]bool{}
		for _, line := range strings.Split(strings.TrimSpace(string(content)), "\n") {
			if line == "" {
				continue
			}
			var rec struct {
				Type string `json:"type"`
				ID   string `json:"id"`
			}
			if err := json.Unmarshal([]byte(line), &rec); err != nil {
				t.Fatal(err)
			}
			if rec.Type == "session" {
				headers++
			} else if rec.ID != "" {
				entryIDs++
				if idSet[rec.ID] {
					t.Fatalf("duplicate id %s", rec.ID)
				}
				idSet[rec.ID] = true
			}
		}
		if headers != 1 {
			t.Fatalf("expected 1 header, got %d", headers)
		}
		if entryIDs == 0 {
			t.Fatal("expected entries")
		}
	})

	t.Run("preserves tool and summary usage across a file-backed reload", func(t *testing.T) {
		tempDir := newTempDir(t)

		session, err := CreateSessionManager(tempDir, tempDir, nil)
		if err != nil {
			t.Fatal(err)
		}
		rootID, err := session.AppendMessage(userMsg("question"))
		mustAppend(t, rootID, err)
		_, _ = session.AppendMessage(assistantMsg("answer"))
		usage := types.Usage{
			Input: 10, Output: 20, CacheRead: 30, CacheWrite: 40, TotalTokens: 100,
			Cost: types.Cost{Input: 0.1, Output: 0.2, CacheRead: 0.3, CacheWrite: 0.4, Total: 1},
		}
		_, _ = session.AppendMessage(types.ToolResultMessage{
			Role:       types.RoleToolResult,
			ToolCallID: "call-1",
			ToolName:   "nested-model",
			Content:    []types.Content{types.NewTextContent("result")},
			IsError:    false,
			Usage:      &usage,
			Timestamp:  timeNowMillis(),
		})
		_, _ = session.AppendCompaction("summary", lo.ToPtr(rootID), 100, nil, lo.ToPtr(false), &usage)
		_, _ = session.BranchWithSummary(rootID, "branch summary", nil, lo.ToPtr(false), &usage)

		file := session.GetSessionFile()
		if file == "" {
			t.Fatal("expected session file")
		}
		reopened, err := OpenSession(file, tempDir, nil)
		if err != nil {
			t.Fatal(err)
		}
		var foundCompaction, foundBranchSummary, foundToolResult bool
		for _, entry := range reopened.GetEntries() {
			switch e := entry.(type) {
			case *CompactionEntry:
				if reflect.DeepEqual(e.Usage, &usage) {
					foundCompaction = true
				}
			case *BranchSummaryEntry:
				if reflect.DeepEqual(e.Usage, &usage) {
					foundBranchSummary = true
				}
			case *SessionMessageEntry:
				if tr, ok := e.Message.(types.ToolResultMessage); ok && reflect.DeepEqual(tr.Usage, &usage) {
					foundToolResult = true
				}
			}
		}
		if !foundCompaction || !foundBranchSummary || !foundToolResult {
			t.Fatalf("usage round-trip: compaction=%v branch=%v toolResult=%v", foundCompaction, foundBranchSummary, foundToolResult)
		}
	})

	t.Run("writes file immediately when forking from a point with assistant messages", func(t *testing.T) {
		tempDir := newTempDir(t)

		session, err := CreateSessionManager(tempDir, tempDir, nil)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = session.AppendMessage(userMsg("first question"))
		id2, err := session.AppendMessage(assistantMsg("first answer"))
		mustAppend(t, id2, err)
		_, _ = session.AppendMessage(userMsg("second question"))
		_, _ = session.AppendMessage(assistantMsg("second answer"))

		// Fork including the assistant message
		newFile, err := session.CreateBranchedSession(id2)
		if err != nil {
			t.Fatal(err)
		}

		// Path includes an assistant, so file should be written immediately
		if _, err := os.Stat(newFile); err != nil {
			t.Fatalf("expected file to exist: %v", err)
		}
		content, err := os.ReadFile(newFile)
		if err != nil {
			t.Fatal(err)
		}
		headers := 0
		for _, line := range strings.Split(strings.TrimSpace(string(content)), "\n") {
			var rec struct {
				Type string `json:"type"`
			}
			if err := json.Unmarshal([]byte(line), &rec); err != nil {
				t.Fatal(err)
			}
			if rec.Type == "session" {
				headers++
			}
		}
		if headers != 1 {
			t.Fatalf("expected 1 header, got %d", headers)
		}
	})
}
