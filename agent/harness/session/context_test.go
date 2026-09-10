// pi: test/harness/session-context.test.ts — session context projection.
package session

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/agent/harness"
	"github.com/ZoroSpace/pig/ai/types"
)

const contextTestNow = int64(1_700_000_000_000)

func contextTestUsage() types.Usage {
	return types.Usage{Cost: types.Cost{}}
}

func contextUserMessage(text string) agent.AgentMessage {
	return types.UserMessage{
		Role:      types.RoleUser,
		Content:   types.UserContent{Parts: []types.Content{types.NewTextContent(text)}},
		Timestamp: contextTestNow,
	}
}

func contextAssistantMessage(stopReason types.StopReason, text string) types.AssistantMessage {
	var content []types.Content
	if stopReason == types.StopReasonToolUse {
		content = []types.Content{&types.ToolCall{
			Kind:      types.KindToolCall,
			ID:        "call",
			Name:      "read",
			Arguments: map[string]any{},
		}}
	} else {
		content = []types.Content{&types.TextContent{Kind: types.KindText, Text: text}}
	}
	message := types.AssistantMessage{
		Role:       types.RoleAssistant,
		Content:    content,
		API:        "anthropic-messages",
		Provider:   "anthropic",
		Model:      "claude-sonnet-4-5",
		Usage:      contextTestUsage(),
		StopReason: stopReason,
		Timestamp:  contextTestNow,
	}
	if stopReason == types.StopReasonDeferred {
		message.Deferred = &types.DeferredHandle{
			Provider: "anthropic",
			ModelID:  "claude-sonnet-4-5",
			API:      "anthropic-messages",
			ID:       "job",
		}
	}
	return message
}

func contextMessageEntry(id string, parentID *string, seq int64, message agent.AgentMessage) *MessageEntry {
	entry := &MessageEntry{Message: message}
	entry.ID = id
	entry.ParentID = parentID
	entry.Seq = seq
	entry.Timestamp = contextTestNow
	entry.Type = EntryTypeMessage
	return entry
}

func TestFiltersNonContextAssistantResponseEntriesWhilePreservingValidMessages(t *testing.T) {
	user := contextUserMessage("question")
	stopped := contextAssistantMessage(types.StopReasonStop, "answer")
	length := contextAssistantMessage(types.StopReasonLength, "truncated answer")
	toolUse := contextAssistantMessage(types.StopReasonToolUse, "")
	failed := contextAssistantMessage(types.StopReasonError, "failed")
	aborted := contextAssistantMessage(types.StopReasonAborted, "aborted")
	deferred := contextAssistantMessage(types.StopReasonDeferred, "")

	entries := []Entry{
		contextMessageEntry("user", nil, 1, user),
		contextMessageEntry("failed", strPtr("user"), 2, failed),
		contextMessageEntry("stopped", strPtr("failed"), 3, stopped),
		contextMessageEntry("aborted", strPtr("stopped"), 4, aborted),
		contextMessageEntry("tool-use", strPtr("aborted"), 5, toolUse),
		contextMessageEntry("deferred", strPtr("tool-use"), 6, deferred),
		contextMessageEntry("length", strPtr("deferred"), 7, length),
	}

	messages, err := BuildSessionContext(entries, nil, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []agent.AgentMessage{user, stopped, toolUse, length}
	if len(messages) != len(want) {
		t.Fatalf("got %d messages, want %d", len(messages), len(want))
	}
	for i := range want {
		if !sameMessage(messages[i], want[i]) {
			t.Fatalf("message %d mismatch", i)
		}
	}
}

func TestProjectsBranchSummariesInBranchOrder(t *testing.T) {
	before := contextMessageEntry("before", nil, 1, contextUserMessage("before summary"))
	summary := &BranchSummaryEntry{Summary: "work on the abandoned branch", FromHook: false}
	summary.ID = "branch-summary"
	summary.ParentID = &before.ID
	summary.Seq = 2
	summary.Timestamp = contextTestNow
	summary.Type = EntryTypeBranchSummary
	summary.FromID = strPtr("source-leaf")
	after := contextMessageEntry("after", &summary.ID, 3, contextUserMessage("after summary"))

	messages, err := BuildSessionContext([]Entry{before, summary, after}, nil, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 3 {
		t.Fatalf("got %d messages", len(messages))
	}
	branchSummary, ok := messages[1].(harness.BranchSummaryMessage)
	if !ok {
		t.Fatalf("expected branchSummary message, got %T", messages[1])
	}
	if branchSummary.Summary != "work on the abandoned branch" ||
		branchSummary.FromID == nil || *branchSummary.FromID != "source-leaf" ||
		branchSummary.Timestamp != contextTestNow {
		t.Fatalf("unexpected branch summary: %+v", branchSummary)
	}
	if !sameMessage(messages[0], contextUserMessage("before summary")) {
		t.Fatalf("unexpected first message: %T", messages[0])
	}
}

func TestFiltersRetainedTailResponsesWithoutHidingTheCompactionSummary(t *testing.T) {
	user := contextUserMessage("kept user")
	stopped := contextAssistantMessage(types.StopReasonStop, "kept answer")
	toolUse := contextAssistantMessage(types.StopReasonToolUse, "")
	length := contextAssistantMessage(types.StopReasonLength, "kept truncated answer")
	failed := contextAssistantMessage(types.StopReasonError, "failed")
	aborted := contextAssistantMessage(types.StopReasonAborted, "aborted")
	deferred := contextAssistantMessage(types.StopReasonDeferred, "")

	compaction := &CompactionEntry{
		Summary:      "summary",
		RetainedTail: []agent.AgentMessage{failed, user, aborted, stopped, deferred, toolUse, length},
		TokensBefore: 100,
		FromHook:     false,
	}
	compaction.ID = "compaction"
	compaction.Seq = 1
	compaction.Timestamp = contextTestNow
	compaction.Type = EntryTypeCompaction

	messages, err := BuildSessionContext([]Entry{compaction}, nil, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 5 {
		t.Fatalf("got %d messages, want 5", len(messages))
	}
	compactionSummary, ok := messages[0].(harness.CompactionSummaryMessage)
	if !ok || compactionSummary.Summary != "summary" || compactionSummary.TokensBefore != 100 {
		t.Fatalf("unexpected compaction summary: %+v", messages[0])
	}
	if !sameMessage(messages[1], user) || !sameMessage(messages[2], stopped) ||
		!sameMessage(messages[3], toolUse) || !sameMessage(messages[4], length) {
		t.Fatalf("unexpected retained tail projection")
	}
}

func TestUsesOnlyTheLatestCompactionCheckpointAndEntriesAfterIt(t *testing.T) {
	beforeFirst := contextMessageEntry("before-first", nil, 1, contextUserMessage("before first"))
	first := &CompactionEntry{Summary: "stale summary", TokensBefore: 100, FromHook: false,
		RetainedTail: []agent.AgentMessage{contextUserMessage("stale tail")}}
	first.ID = "first-compaction"
	first.ParentID = &beforeFirst.ID
	first.Seq = 2
	first.Timestamp = contextTestNow
	first.Type = EntryTypeCompaction
	between := contextMessageEntry("between", &first.ID, 3, contextUserMessage("between compactions"))
	latest := &CompactionEntry{Summary: "latest summary", TokensBefore: 200, FromHook: false,
		RetainedTail: []agent.AgentMessage{contextUserMessage("latest tail")}}
	latest.ID = "latest-compaction"
	latest.ParentID = &between.ID
	latest.Seq = 4
	latest.Timestamp = contextTestNow
	latest.Type = EntryTypeCompaction
	after := contextMessageEntry("after", &latest.ID, 5, contextUserMessage("after latest"))

	messages, err := BuildSessionContext([]Entry{beforeFirst, first, between, latest, after}, nil, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 3 {
		t.Fatalf("got %d messages, want 3", len(messages))
	}
	compactionSummary, ok := messages[0].(harness.CompactionSummaryMessage)
	if !ok || compactionSummary.Summary != "latest summary" || compactionSummary.TokensBefore != 200 {
		t.Fatalf("unexpected compaction summary: %+v", messages[0])
	}
	if !sameMessage(messages[1], contextUserMessage("latest tail")) {
		t.Fatalf("unexpected retained tail")
	}
	if !sameMessage(messages[2], contextUserMessage("after latest")) {
		t.Fatalf("unexpected after message")
	}
}

func TestProjectsCustomEntriesThroughProjectorsInBranchOrder(t *testing.T) {
	customEntry := func(id string, parentID *string, seq int64, customType string) *CustomEntry {
		entry := &CustomEntry{}
		entry.ID = id
		entry.ParentID = parentID
		entry.Seq = seq
		entry.Timestamp = contextTestNow
		entry.Type = EntryTypeCustom
		entry.CustomType = &customType
		return entry
	}
	oldCustom := customEntry("old-custom", nil, 1, "sync")
	compaction := &CompactionEntry{Summary: "summary", TokensBefore: 100, FromHook: false}
	compaction.ID = "compaction"
	compaction.ParentID = &oldCustom.ID
	compaction.Seq = 2
	compaction.Timestamp = contextTestNow
	compaction.Type = EntryTypeCompaction
	syncCustom := customEntry("sync-custom", &compaction.ID, 3, "sync")
	omittedCustom := customEntry("omitted-custom", &syncCustom.ID, 4, "omitted")
	asyncCustom := customEntry("async-custom", &omittedCustom.ID, 5, "async")

	var projectedIDs []string
	projectors := map[string]EntryProjector{
		"sync": func(entry CustomEntry, ctx context.Context) ([]agent.AgentMessage, error) {
			projectedIDs = append(projectedIDs, entry.Base().ID)
			return []agent.AgentMessage{contextUserMessage("projected:" + entry.Base().ID)}, nil
		},
		"async": func(entry CustomEntry, ctx context.Context) ([]agent.AgentMessage, error) {
			projectedIDs = append(projectedIDs, entry.Base().ID)
			return []agent.AgentMessage{contextUserMessage("projected:" + entry.Base().ID)}, nil
		},
	}

	messages, err := BuildSessionContext(
		[]Entry{oldCustom, compaction, syncCustom, omittedCustom, asyncCustom},
		&SessionContextBuildOptions{EntryProjectors: projectors},
		context.Background(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(projectedIDs) != 2 || projectedIDs[0] != "sync-custom" || projectedIDs[1] != "async-custom" {
		t.Fatalf("projected ids = %v", projectedIDs)
	}
	if len(messages) != 3 {
		t.Fatalf("got %d messages, want 3", len(messages))
	}
	compactionSummary, ok := messages[0].(harness.CompactionSummaryMessage)
	if !ok || compactionSummary.Summary != "summary" {
		t.Fatalf("unexpected first message: %+v", messages[0])
	}
}

func TestPropagatesCustomProjectorFailures(t *testing.T) {
	customType := "broken"
	custom := &CustomEntry{}
	custom.ID = "custom"
	custom.Seq = 1
	custom.Timestamp = contextTestNow
	custom.Type = EntryTypeCustom
	custom.CustomType = &customType
	failure := errors.New("projector failed")

	_, err := BuildSessionContext(
		[]Entry{custom},
		&SessionContextBuildOptions{
			EntryProjectors: map[string]EntryProjector{
				"broken": func(entry CustomEntry, ctx context.Context) ([]agent.AgentMessage, error) {
					return nil, failure
				},
			},
		},
		context.Background(),
	)
	if !errors.Is(err, failure) {
		t.Fatalf("err = %v, want the projector failure", err)
	}
}

func strPtr(s string) *string { return &s }

// sameMessage compares agent messages structurally (values contain slices
// and are not comparable with ==).
func sameMessage(a, b agent.AgentMessage) bool {
	return reflect.DeepEqual(a, b)
}
