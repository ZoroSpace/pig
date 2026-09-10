// pi: test/harness/branch-summarization.test.ts — branch summarization entry
// collection over the in-memory session backend.
package compaction

import (
	"fmt"
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

type fixedIDGenerator struct{ n int }

func (g *fixedIDGenerator) Next() string {
	g.n++
	return fmt.Sprintf("entry-%d", g.n)
}

func TestCollectsTheAbandonedSideOfABranchInChronologicalOrder(t *testing.T) {
	gen := &fixedIDGenerator{}
	sess := session.NewSession(session.NewInMemorySessionStorage(session.SessionMetadata{ID: "session", CreatedAt: 1}), &session.SessionOptions{IDGenerator: gen})
	rootID, err := sess.AppendMessage(branchTestMessage("root"))
	if err != nil {
		t.Fatal(err)
	}
	commonID, err := sess.AppendMessage(branchTestMessage("common"))
	if err != nil {
		t.Fatal(err)
	}
	var abandonedIDs []string
	for _, text := range []string{"abandoned 1", "abandoned 2"} {
		id, err := sess.AppendMessage(branchTestMessage(text))
		if err != nil {
			t.Fatal(err)
		}
		abandonedIDs = append(abandonedIDs, id)
	}
	if err := sess.CreateLane("target", &commonID); err != nil {
		t.Fatal(err)
	}
	targetID, err := sess.View("target").AppendMessage(branchTestMessage("target"))
	if err != nil {
		t.Fatal(err)
	}

	result, err := CollectEntriesForBranchSummary(sess, &abandonedIDs[1], targetID)
	if err != nil {
		t.Fatal(err)
	}
	if result.CommonAncestorID != commonID {
		t.Fatalf("expected common ancestor %q, got %q", commonID, result.CommonAncestorID)
	}
	if len(result.Entries) != len(abandonedIDs) {
		t.Fatalf("expected %d entries, got %d", len(abandonedIDs), len(result.Entries))
	}
	for i, entry := range result.Entries {
		if entry.Base().ID != abandonedIDs[i] {
			t.Fatalf("entry %d: expected %q, got %q", i, abandonedIDs[i], entry.Base().ID)
		}
	}
	for _, entry := range result.Entries {
		if entry.Base().ID == rootID {
			t.Fatal("root must not be included in the abandoned branch")
		}
	}
}

func TestReturnsNoEntriesWhenThereWasNoPreviousLeaf(t *testing.T) {
	sess := session.NewSession(session.NewInMemorySessionStorage(session.SessionMetadata{ID: "session", CreatedAt: 1}), nil)
	if _, err := sess.AppendMessage(branchTestMessage("target")); err != nil {
		t.Fatal(err)
	}
	entries, err := sess.FindEntries(nil)
	if err != nil {
		t.Fatal(err)
	}
	targetID := entries[0].Base().ID
	result, err := CollectEntriesForBranchSummary(sess, nil, targetID)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Entries) != 0 || result.CommonAncestorID != "" {
		t.Fatalf("expected empty result, got %+v", result)
	}
}
