// pi: test/harness/branch-summarization.test.ts — v4 branch summarization
// entry collection over stub branch/session readers.
package compaction

import (
	"context"
	"testing"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/agent/harness/session"
	"github.com/ZoroSpace/pig/ai/types"
)

func branchTestMessage(text string) agent.AgentMessage {
	return types.UserMessage{
		Role:      types.RoleUser,
		Content:   types.UserContent{Parts: []types.Content{types.NewTextContent(text)}},
		Timestamp: 1,
	}
}

func branchTestMessageEntry(id string, parentID *string, text string, seq int64) *session.MessageEntry {
	entry := &session.MessageEntry{Message: branchTestMessage(text)}
	entry.ID = id
	entry.Type = session.EntryTypeMessage
	entry.ParentID = parentID
	entry.Seq = seq
	entry.Timestamp = seq
	return entry
}

// branchTestReader is the stub branch/session pair (pi: branchReader).
type branchTestReader struct {
	entries []session.Entry
}

func (r *branchTestReader) GetEntry(id string, ctx context.Context) (session.Entry, error) {
	for _, entry := range r.entries {
		if entry.Base().ID == id {
			return entry, nil
		}
	}
	return nil, nil
}

func (r *branchTestReader) FindEntries(query *session.BranchScan, ctx context.Context) ([]session.Entry, error) {
	var path []session.Entry
	var currentID *string
	if query != nil {
		currentID = query.Start
	}
	for currentID != nil {
		entry, err := r.GetEntry(*currentID, ctx)
		if err != nil {
			return nil, err
		}
		if entry == nil {
			return nil, &unknownEntryError{id: *currentID}
		}
		path = append(path, entry)
		currentID = entry.Base().ParentID
	}
	return path, nil
}

type unknownEntryError struct{ id string }

func (e *unknownEntryError) Error() string { return "Unknown entry " + e.id }

func TestCollectsTheAbandonedSideOfABranchInChronologicalOrder(t *testing.T) {
	root := branchTestMessageEntry("root", nil, "root", 1)
	common := branchTestMessageEntry("common", &root.ID, "common", 2)
	abandoned1 := branchTestMessageEntry("abandoned-1", &common.ID, "abandoned 1", 3)
	abandoned2 := branchTestMessageEntry("abandoned-2", &abandoned1.ID, "abandoned 2", 4)
	target := branchTestMessageEntry("target", &common.ID, "target", 5)
	reader := &branchTestReader{entries: []session.Entry{root, common, abandoned1, abandoned2, target}}

	result, err := CollectEntriesForBranchSummary(reader, reader, &abandoned2.ID, target.ID, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.CommonAncestorID != common.ID {
		t.Fatalf("expected common ancestor %q, got %q", common.ID, result.CommonAncestorID)
	}
	if len(result.Entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(result.Entries))
	}
	if result.Entries[0].Base().ID != abandoned1.ID || result.Entries[1].Base().ID != abandoned2.ID {
		t.Fatalf("unexpected entries: %v, %v", result.Entries[0].Base().ID, result.Entries[1].Base().ID)
	}
	for _, entry := range result.Entries {
		if entry.Base().ID == root.ID {
			t.Fatal("root must not be included in the abandoned branch")
		}
	}
}

func TestReturnsNoEntriesWhenThereWasNoPreviousLeaf(t *testing.T) {
	target := branchTestMessageEntry("target", nil, "target", 1)
	reader := &branchTestReader{entries: []session.Entry{target}}
	result, err := CollectEntriesForBranchSummary(reader, reader, nil, target.ID, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Entries) != 0 || result.CommonAncestorID != "" {
		t.Fatalf("expected empty result, got %+v", result)
	}
}
