// pi: harness/session/testing/conformance/session-repo.ts — SessionRepo
// conformance case groups (lifecycle, ownership, messages, forks, fork
// coordination).
package testing

import (
	"context"
	"testing"
	"time"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/agent/harness/session"
	"github.com/ZoroSpace/pig/ai/types"
)

const (
	repoRootID      = "00000000-0000-7000-8000-000000000001"
	repoChildID     = "00000000-0000-7000-8000-000000000002"
	repoSiblingID   = "00000000-0000-7000-8000-000000000003"
	repoUsageID     = "00000000-0000-7000-8000-000000000004"
	repoOperationID = "00000000-0000-7000-8000-000000000005"
	repoPendingID   = "00000000-0000-7000-8000-000000000006"
	repoUnknownID   = "00000000-0000-7000-8000-000000000007"
)

func repoIdleLaneState() session.LaneState {
	return session.LaneState{Inbox: []session.InboxItem{}}
}

var (
	repoApplicationValue = session.MustValue("test.application.value", "")
	repoApplicationList  = session.MustList("test.application.list", "")
)

func repoConfiguration() session.LaneConfiguration {
	configuration := &session.LaneConfiguration{ThinkingLevel: "off", ActiveToolNames: []string{"read"}}
	configuration.Model.Provider = "provider"
	configuration.Model.ModelID = "model"
	return *configuration
}

func repoGetBranchTip(t *testing.T, sess session.Session, names ...string) *string {
	t.Helper()
	name := "main"
	if len(names) > 0 {
		name = names[0]
	}
	branch, err := sess.Branch(name, conformanceCtx)
	if err != nil {
		t.Fatal(err)
	}
	if branch == nil {
		return nil
	}
	tip, err := branch.GetTipID(conformanceCtx)
	if err != nil {
		t.Fatal(err)
	}
	return tip
}

func repoAssistantMessage(stopReason types.StopReason) *types.AssistantMessage {
	var content []types.Content
	if stopReason == types.StopReasonToolUse {
		content = []types.Content{&types.ToolCall{
			Kind: types.KindToolCall, ID: "call", Name: "read", Arguments: map[string]any{},
		}}
	} else {
		content = []types.Content{&types.TextContent{Kind: types.KindText, Text: string(stopReason)}}
	}
	message := &types.AssistantMessage{
		Role:       types.RoleAssistant,
		Content:    content,
		API:        "anthropic-messages",
		Provider:   "anthropic",
		Model:      "claude-sonnet-4-5",
		Usage:      types.Usage{},
		StopReason: stopReason,
		Timestamp:  1,
	}
	if stopReason == types.StopReasonDeferred {
		message.Deferred = &types.DeferredHandle{
			Provider: "anthropic", ModelID: "claude-sonnet-4-5", API: "anthropic-messages", ID: "job",
		}
	}
	return message
}

func repoUsageRow() session.UsageRow {
	return session.UsageRow{
		ID:         repoUsageID,
		Adjustment: true,
		Usage: types.Usage{
			Input: 1, Output: 2, CacheRead: 0, CacheWrite: 0, TotalTokens: 3,
			Cost: types.Cost{},
		},
	}
}

// ConformanceRepo is the per-backend adapter over SessionRepo[TMetadata,
// TCreate, TList] that the conformance cases drive (pi: the factory returns
// Pick<SessionRepo<TMetadata>, "create" | "open" | "list" | "delete"> and
// each backend wrapper adapts its concrete option/metadata shapes; the
// metadata accessors stand in for pi's typed `session.metadata` reads).
type ConformanceRepo[TMetadata any] interface {
	// Metadata reads the concrete metadata off a session handle.
	Metadata(sess session.Session) TMetadata
	// MetadataID returns the session id of one metadata value.
	MetadataID(metadata TMetadata) string
	// MetadataParentSessionID returns the optional parent link.
	MetadataParentSessionID(metadata TMetadata) *string
	// Create builds the backend's create options from the common fields.
	Create(id *string, parentSessionID *string, ctx context.Context) (session.Session, error)
	Open(metadata TMetadata, ctx context.Context) (session.Session, error)
	List(ctx context.Context) ([]TMetadata, error)
	Delete(metadata TMetadata, ctx context.Context) error
	Fork(source TMetadata, options session.ForkOptions, ctx context.Context) (session.Session, error)
	Close(ctx context.Context) error
}

// RepoFactory creates a fresh repo per case (pi's backendFactory).
type RepoFactory[TMetadata any] func(t *testing.T) ConformanceRepo[TMetadata]

// SessionRepoLifecycleConformance creates lifecycle cases (pi:
// createSessionRepoLifecycleConformance).
func SessionRepoLifecycleConformance[TMetadata any](factory RepoFactory[TMetadata]) []ConformanceCase {
	return []ConformanceCase{
		{
			Group: "lifecycle", Name: "creates a session with no implicit branch and rejects duplicate ids",
			Run: func(t *testing.T) {
				repo := factory(t)
				id := "session"
				sess, err := repo.Create(&id, nil, conformanceCtx)
				if err != nil {
					t.Fatal(err)
				}
				if sess.GetMetadata().ID != "session" || sess.GetMetadata().CreatedAt <= 0 || sess.GetMetadata().StorageVersion != 1 {
					t.Fatalf("metadata = %+v", sess.GetMetadata())
				}
				branch, err := sess.Branch("main", conformanceCtx)
				if err != nil || branch != nil {
					t.Fatalf("implicit branch = %v, %v", branch, err)
				}
				laneState, err := sess.GetValue(session.LaneStateAddress("main"), conformanceCtx)
				if err != nil || laneState != nil {
					t.Fatalf("implicit lane state = %v, %v", laneState, err)
				}
				laneConfig, err := sess.GetValue(session.LaneConfig("main"), conformanceCtx)
				if err != nil || laneConfig != nil {
					t.Fatalf("implicit lane config = %v, %v", laneConfig, err)
				}
				if _, err := repo.Create(&id, nil, conformanceCtx); err == nil {
					t.Fatal("duplicate id must reject")
				}
				_ = sess.Close(conformanceCtx)
			},
		},
		{
			Group: "lifecycle", Name: "close drains an acquired scope and rejects a queued mutation callback",
			Run: func(t *testing.T) {
				repo := factory(t)
				id := "session"
				sess, err := repo.Create(&id, nil, conformanceCtx)
				if err != nil {
					t.Fatal(err)
				}
				active, err := sess.BeginMutation(conformanceCtx)
				if err != nil {
					t.Fatal(err)
				}
				queuedDone := make(chan error, 1)
				go func() {
					_, err := sess.Mutate(func(mutator session.SessionMutator, ctx context.Context) (any, error) {
						t.Log("queued mutation must not start")
						return nil, nil
					}, conformanceCtx)
					queuedDone <- err
				}()
				closing := make(chan error, 1)
				go func() { closing <- sess.Close(conformanceCtx) }()
				// pi seals synchronously on the close() tick; yield so the
				// close goroutine seals before End releases the line.
				time.Sleep(20 * time.Millisecond)
				if err := active.End(conformanceCtx); err != nil {
					t.Fatal(err)
				}
				if err := <-queuedDone; err == nil {
					t.Fatal("queued mutation must reject after close")
				}
				if err := <-closing; err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			Group: "lifecycle", Name: "lists metadata and preserves state across close and reopen",
			Run: func(t *testing.T) {
				repo := factory(t)
				firstID := "first"
				first, err := repo.Create(&firstID, nil, conformanceCtx)
				if err != nil {
					t.Fatal(err)
				}
				if err := first.SetName(strPtr("preserved"), conformanceCtx); err != nil {
					t.Fatal(err)
				}
				secondID := "second"
				parentID := "parent"
				second, err := repo.Create(&secondID, &parentID, conformanceCtx)
				if err != nil {
					t.Fatal(err)
				}
				_ = second.Close(conformanceCtx)

				listed, err := repo.List(conformanceCtx)
				if err != nil {
					t.Fatal(err)
				}
				type idParent struct {
					ID              string
					ParentSessionID *string
				}
				var pairs []idParent
				for _, metadata := range listed {
					pairs = append(pairs, idParent{repo.MetadataID(metadata), repo.MetadataParentSessionID(metadata)})
				}
				// sort by id
				for i := 1; i < len(pairs); i++ {
					for j := i; j > 0 && pairs[j].ID < pairs[j-1].ID; j-- {
						pairs[j], pairs[j-1] = pairs[j-1], pairs[j]
					}
				}
				assertJSONEqual(t, "listed", pairs, []idParent{{"first", nil}, {"second", &parentID}})
				_ = first.Close(conformanceCtx)
				if _, err := first.GetName(conformanceCtx); err == nil {
					t.Fatal("closed session must reject reads")
				}
				reopened, err := repo.Open(listed[0], conformanceCtx)
				if err != nil {
					t.Fatal(err)
				}
				name, err := reopened.GetName(conformanceCtx)
				if err != nil || name == nil || *name != "preserved" {
					t.Fatalf("reopened name = %v, %v", name, err)
				}
				_ = reopened.Close(conformanceCtx)
			},
		},
		{
			Group: "lifecycle", Name: "deletes closed sessions without affecting other sessions",
			Run: func(t *testing.T) {
				repo := factory(t)
				removedID := "removed"
				removed, err := repo.Create(&removedID, nil, conformanceCtx)
				if err != nil {
					t.Fatal(err)
				}
				retainedID := "retained"
				retained, err := repo.Create(&retainedID, nil, conformanceCtx)
				if err != nil {
					t.Fatal(err)
				}
				_ = removed.Close(conformanceCtx)
				_ = retained.Close(conformanceCtx)

				if err := repo.Delete(repo.Metadata(removed), conformanceCtx); err != nil {
					t.Fatal(err)
				}
				listed, _ := repo.List(conformanceCtx)
				ids := []string{}
				for _, metadata := range listed {
					ids = append(ids, repo.MetadataID(metadata))
				}
				assertJSONEqual(t, "listed", ids, []string{"retained"})
				if _, err := repo.Open(repo.Metadata(removed), conformanceCtx); err == nil {
					t.Fatal("open of deleted session must reject")
				}
				if err := repo.Delete(repo.Metadata(removed), conformanceCtx); err == nil {
					t.Fatal("double delete must reject")
				}
			},
		},
	}
}

// SessionRepoOwnershipConformance creates exclusive-open cases (pi:
// createSessionRepoOwnershipConformance).
func SessionRepoOwnershipConformance[TMetadata any](factory RepoFactory[TMetadata]) []ConformanceCase {
	return []ConformanceCase{
		{
			Group: "ownership", Name: "rejects opening an already-open session",
			Run: func(t *testing.T) {
				repo := factory(t)
				id := "session"
				sess, err := repo.Create(&id, nil, conformanceCtx)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := repo.Open(repo.Metadata(sess), conformanceCtx); err == nil {
					t.Fatal("double open must reject")
				}
				_ = sess.Close(conformanceCtx)
				reopened, err := repo.Open(repo.Metadata(sess), conformanceCtx)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := repo.Open(repo.Metadata(sess), conformanceCtx); err == nil {
					t.Fatal("double open must reject")
				}
				_ = reopened.Close(conformanceCtx)
			},
		},
	}
}

// SessionRepoMessageConformance creates message cases (pi:
// createSessionRepoMessageConformance).
func SessionRepoMessageConformance[TMetadata any](factory RepoFactory[TMetadata]) []ConformanceCase {
	return []ConformanceCase{
		{
			Group: "messages", Name: "rejects pending assistant messages without changing the tree",
			Run: func(t *testing.T) {
				repo := factory(t)
				id := "session"
				sess, err := repo.Create(&id, nil, conformanceCtx)
				if err != nil {
					t.Fatal(err)
				}
				branch, err := sess.CreateBranch("main", nil, conformanceCtx)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := branch.AppendMessage(repoAssistantMessage(types.StopReasonPending), conformanceCtx); err == nil {
					t.Fatal("pending assistant message must reject")
				}
				if tip := repoGetBranchTip(t, sess); tip != nil {
					t.Fatalf("tip = %v", tip)
				}
				entries, err := sess.FindEntries(nil, conformanceCtx)
				if err != nil || len(entries) != 0 {
					t.Fatalf("entries = %v, %v", entries, err)
				}
				_ = sess.Close(conformanceCtx)
			},
		},
		{
			Group: "messages", Name: "preserves every settled assistant stop reason",
			Run: func(t *testing.T) {
				repo := factory(t)
				id := "session"
				sess, err := repo.Create(&id, nil, conformanceCtx)
				if err != nil {
					t.Fatal(err)
				}
				messages := []agentMessage{
					repoAssistantMessage(types.StopReasonStop),
					repoAssistantMessage(types.StopReasonLength),
					repoAssistantMessage(types.StopReasonToolUse),
					repoAssistantMessage(types.StopReasonError),
					repoAssistantMessage(types.StopReasonAborted),
					repoAssistantMessage(types.StopReasonDeferred),
				}
				branch, err := sess.CreateBranch("main", nil, conformanceCtx)
				if err != nil {
					t.Fatal(err)
				}
				var ids []string
				for _, message := range messages {
					entryID, err := branch.AppendMessage(message, conformanceCtx)
					if err != nil {
						t.Fatal(err)
					}
					ids = append(ids, entryID)
				}
				messageType := session.EntryTypeMessage
				entries, err := sess.FindEntries(&session.EntryQuery{Order: "asc", Type: &messageType}, conformanceCtx)
				if err != nil {
					t.Fatal(err)
				}
				var gotIDs []string
				for i, entry := range entries {
					gotIDs = append(gotIDs, entry.Base().ID)
					messageEntry, ok := entry.(*session.MessageEntry)
					if !ok {
						t.Fatalf("expected message entry, got %T", entry)
					}
					assertJSONEqual(t, "message", messageEntry.Message, messages[i])
				}
				assertJSONEqual(t, "ids", gotIDs, ids)
				tip := repoGetBranchTip(t, sess)
				if tip == nil || *tip != ids[len(ids)-1] {
					t.Fatalf("tip = %v", tip)
				}
				_ = sess.Close(conformanceCtx)
			},
		},
	}
}

type agentMessage = agent.AgentMessage

// SessionRepoForkBehaviorConformance creates fork-content cases (pi:
// createSessionRepoForkBehaviorConformance).
func SessionRepoForkBehaviorConformance[TMetadata any](factory RepoFactory[TMetadata]) []ConformanceCase {
	return []ConformanceCase{
		{
			Group: "forks", Name: "tree-forks a fresh session before first attachment",
			Run: func(t *testing.T) {
				repo := factory(t)
				sourceID := "source"
				source, err := repo.Create(&sourceID, nil, conformanceCtx)
				if err != nil {
					t.Fatal(err)
				}
				forkID := "fork"
				fork, err := repo.Fork(repo.Metadata(source), session.ForkOptions{ID: &forkID, Scope: session.ForkScopeTree}, conformanceCtx)
				if err != nil {
					t.Fatal(err)
				}
				if fork.GetMetadata().ID != "fork" || fork.GetMetadata().ParentSessionID == nil || *fork.GetMetadata().ParentSessionID != "source" {
					t.Fatalf("fork metadata = %+v", fork.GetMetadata())
				}
				if branch, err := fork.Branch("main", conformanceCtx); err != nil || branch != nil {
					t.Fatalf("implicit branch = %v, %v", branch, err)
				}
				if stored, _ := fork.GetValue(session.LaneConfig("main"), conformanceCtx); stored != nil {
					t.Fatal("implicit lane config")
				}
				if stored, _ := fork.GetValue(session.LaneStateAddress("main"), conformanceCtx); stored != nil {
					t.Fatal("implicit lane state")
				}
				entries, _ := fork.FindEntries(nil, conformanceCtx)
				if len(entries) != 0 {
					t.Fatalf("entries = %v", entries)
				}
				stats, _ := fork.GetStats(conformanceCtx)
				assertJSONEqual(t, "stats", stats, session.SessionStats{MessageCount: 0, Usage: types.Usage{}})
				_ = source.Close(conformanceCtx)
				_ = fork.Close(conformanceCtx)
			},
		},
		{
			Group: "forks", Name: "rejects a data-only branch and releases its destination id",
			Run: func(t *testing.T) {
				repo := factory(t)
				sourceID := "source"
				source, err := repo.Create(&sourceID, nil, conformanceCtx)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := source.CreateBranch("data", nil, conformanceCtx); err != nil {
					t.Fatal(err)
				}
				destinationID := "destination"
				if _, err := repo.Fork(repo.Metadata(source), session.ForkOptions{ID: &destinationID, Scope: session.ForkScopeBranch, Branch: "data"}, conformanceCtx); err == nil {
					t.Fatal("data-only branch fork must reject")
				}
				listed, _ := repo.List(conformanceCtx)
				ids := []string{}
				for _, metadata := range listed {
					ids = append(ids, repo.MetadataID(metadata))
				}
				assertJSONEqual(t, "listed", ids, []string{"source"})
				destination, err := repo.Create(&destinationID, nil, conformanceCtx)
				if err != nil {
					t.Fatalf("destination id must be reusable: %v", err)
				}
				_ = source.Close(conformanceCtx)
				_ = destination.Close(conformanceCtx)
			},
		},
		{
			Group: "forks", Name: "forks one named configured branch with scoped values and a zero ledger",
			Run: func(t *testing.T) {
				repo := factory(t)
				sourceID := "source"
				source, err := repo.Create(&sourceID, nil, conformanceCtx)
				if err != nil {
					t.Fatal(err)
				}
				_, err = source.Mutate(func(mutator session.SessionMutator, ctx context.Context) (any, error) {
					_, commitErr := mutator.Commit([]session.Write{
						session.InsertEntry(repoCustomEntry(repoRootID, "", "root")),
						session.InsertEntry(repoUserEntry(repoChildID, repoRootID, "child")),
						session.InsertEntry(repoCustomEntry(repoSiblingID, repoRootID, "sibling")),
						session.SetValue(session.BranchTip("main"), repoSiblingID),
						session.SetValue(session.BranchTip("review"), repoChildID),
						session.SetValue(session.LaneConfig("review"), repoConfiguration()),
						session.SetValue(session.LaneStateAddress("review"), session.LaneState{
							CurrentOperationID: strPtr(repoOperationID),
							LastOperationID:    strPtr("previous"),
							Inbox:              []session.InboxItem{{EntryID: repoPendingID, Kind: session.InboxItemWrite}},
						}),
						session.SetValue(session.OperationResult("previous"), session.OperationResultRecord{
							OperationID: "previous", Kind: session.OperationIntentNavigation, Status: session.TerminalStatusCompleted,
							FromTipID: strPtr(repoRootID), TipID: strPtr(repoChildID), StartedAt: 1, EndedAt: 2,
						}),
						session.SetValue(session.SessionNameAddress, "source name"),
						session.SetValue(repoApplicationValue, map[string]any{"copied": false}),
						session.AppendList(repoApplicationList, map[string]any{"copied": false}),
						session.SetValue(session.EntryLabel(repoRootID), "root label"),
						session.SetValue(session.EntryLabel(repoSiblingID), "sibling label"),
						session.SetValue(session.PendingEntryAddress(repoPendingID), map[string]any{"type": "custom", "customType": "pending"}),
						session.SetValue(session.OperationMetaAddress(repoOperationID), session.OperationMeta{
							OperationID: repoOperationID, Lane: "review", SourceTipID: strPtr(repoChildID), StartedAt: 1,
							Intent: session.CompactionIntent{Kind: session.OperationIntentCompaction},
						}),
						session.SetValue(session.OperationStateAddress(repoOperationID), session.SummaryDecidingOperation{
							At: "summary.deciding",
							OperationScope: session.OperationScope{
								Control:  session.Control{Status: "running"},
								Settings: session.RunSettings{SteeringMode: "all", FollowUpMode: "all", ToolExecution: "sequential"},
							},
							Task: session.SummaryTask{TaskID: repoOperationID, Reason: strPtr("manual"), Boundary: session.ResultBoundary{Kind: "finish"}},
						}),
						session.SetValue(session.OperationToolArgs(repoOperationID, repoRootID, 0), map[string]any{"argument": true}),
						session.SetValue(session.OperationPreparation(repoOperationID, repoOperationID), session.DurableStructuralPreparation{
							Kind: "compaction", FileOps: session.DurableFileOperations{Read: []string{}, Written: []string{}, Edited: []string{}},
						}),
						session.InsertUsage(repoUsageRow()),
					}, ctx)
					return nil, commitErr
				}, conformanceCtx)
				if err != nil {
					t.Fatal(err)
				}

				forkID := "fork"
				fork, err := repo.Fork(repo.Metadata(source), session.ForkOptions{
					ID: &forkID, Scope: session.ForkScopeBranch, Branch: "review", EntryID: strPtr(repoChildID), Position: session.ForkPositionAt,
				}, conformanceCtx)
				if err != nil {
					t.Fatal(err)
				}
				entries, _ := fork.FindEntries(&session.EntryQuery{Order: "asc"}, conformanceCtx)
				assertJSONEqual(t, "entries", entryIDs(entries), []string{repoRootID, repoChildID})
				if branch, _ := fork.Branch("main", conformanceCtx); branch != nil {
					t.Fatal("main must be absent")
				}
				if tip := repoGetBranchTip(t, fork, "review"); tip == nil || *tip != repoChildID {
					t.Fatalf("review tip = %v", tip)
				}
				config, _ := fork.GetValue(session.LaneConfig("review"), conformanceCtx)
				assertJSONEqual(t, "lane config", config.Value, repoConfiguration())
				state, _ := fork.GetValue(session.LaneStateAddress("review"), conformanceCtx)
				assertJSONEqual(t, "lane state", state.Value, repoIdleLaneState())
				if stored, _ := fork.GetValue(session.LaneConfig("main"), conformanceCtx); stored != nil {
					t.Fatal("main lane config must be absent")
				}
				if stored, _ := fork.GetValue(session.LaneStateAddress("main"), conformanceCtx); stored != nil {
					t.Fatal("main lane state must be absent")
				}
				name, _ := fork.GetName(conformanceCtx)
				if name == nil || *name != "source name" {
					t.Fatalf("name = %v", name)
				}
				if stored, _ := fork.GetValue(repoApplicationValue, conformanceCtx); stored != nil {
					t.Fatal("non-reserved value must be excluded on branch scope")
				}
				elements, _ := fork.ReadList(repoApplicationList, nil, conformanceCtx)
				if len(elements) != 0 {
					t.Fatal("non-reserved list must be excluded on branch scope")
				}
				if label, _ := fork.GetLabel(repoRootID, conformanceCtx); label == nil || *label != "root label" {
					t.Fatalf("root label = %v", label)
				}
				if label, _ := fork.GetLabel(repoSiblingID, conformanceCtx); label != nil {
					t.Fatalf("sibling label = %v", label)
				}
				for _, address := range []session.Value{
					session.OperationResult("previous"),
					session.PendingEntryAddress(repoPendingID),
					session.OperationMetaAddress(repoOperationID),
					session.OperationStateAddress(repoOperationID),
					session.OperationToolArgs(repoOperationID, repoRootID, 0),
					session.OperationPreparation(repoOperationID, repoOperationID),
				} {
					if stored, _ := fork.GetValue(address, conformanceCtx); stored != nil {
						t.Fatalf("operation state %s must be excluded", address.Key)
					}
				}
				stats, _ := fork.GetStats(conformanceCtx)
				assertJSONEqual(t, "stats", stats, session.SessionStats{MessageCount: 1, Usage: types.Usage{}})
				_ = source.Close(conformanceCtx)
				_ = fork.Close(conformanceCtx)
			},
		},
	}
}

func repoCustomEntry(id, parentID, customType string) *session.CustomEntry {
	entry := &session.CustomEntry{}
	entry.ID = id
	entry.Type = session.EntryTypeCustom
	if parentID != "" {
		entry.ParentID = strPtr(parentID)
	}
	entry.CustomType = &customType
	return entry
}

func repoUserEntry(id, parentID, text string) *session.MessageEntry {
	entry := &session.MessageEntry{
		Message: types.UserMessage{
			Role:      types.RoleUser,
			Content:   types.UserContent{Parts: []types.Content{types.NewTextContent(text)}},
			Timestamp: 1,
		},
	}
	entry.ID = id
	entry.Type = session.EntryTypeMessage
	if parentID != "" {
		entry.ParentID = strPtr(parentID)
	}
	return entry
}

func entryIDs(entries []session.Entry) []string {
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		out = append(out, entry.Base().ID)
	}
	return out
}

// SessionRepoForkAncestryConformance creates ancestry placement cases (pi:
// "enforces branch ancestry for at and before placement").
func SessionRepoForkAncestryConformance[TMetadata any](factory RepoFactory[TMetadata]) []ConformanceCase {
	return []ConformanceCase{
		{
			Group: "forks", Name: "enforces branch ancestry for at and before placement",
			Run: func(t *testing.T) {
				repo := factory(t)
				sourceID := "source"
				source, err := repo.Create(&sourceID, nil, conformanceCtx)
				if err != nil {
					t.Fatal(err)
				}
				_, err = source.Mutate(func(mutator session.SessionMutator, ctx context.Context) (any, error) {
					_, commitErr := mutator.Commit([]session.Write{
						session.InsertEntry(repoCustomEntry(repoRootID, "", "root")),
						session.InsertEntry(repoCustomEntry(repoChildID, repoRootID, "child")),
						session.InsertEntry(repoCustomEntry(repoSiblingID, repoRootID, "sibling")),
						session.SetValue(session.BranchTip("main"), repoChildID),
						session.SetValue(session.LaneConfig("main"), repoConfiguration()),
						session.SetValue(session.LaneStateAddress("main"), repoIdleLaneState()),
						session.SetValue(session.BranchTip("empty"), nil),
						session.SetValue(session.LaneConfig("empty"), repoConfiguration()),
						session.SetValue(session.LaneStateAddress("empty"), repoIdleLaneState()),
					}, ctx)
					return nil, commitErr
				}, conformanceCtx)
				if err != nil {
					t.Fatal(err)
				}

				forkOpts := func(id, branch string, entryID *string, position string) session.ForkOptions {
					return session.ForkOptions{ID: &id, Scope: session.ForkScopeBranch, Branch: branch, EntryID: entryID, Position: position}
				}
				beforeID := "before"
				before, err := repo.Fork(repo.Metadata(source), forkOpts(beforeID, "main", strPtr(repoChildID), session.ForkPositionBefore), conformanceCtx)
				if err != nil {
					t.Fatal(err)
				}
				if tip := repoGetBranchTip(t, before); tip == nil || *tip != repoRootID {
					t.Fatalf("before tip = %v", tip)
				}
				entries, _ := before.FindEntries(&session.EntryQuery{Order: "asc"}, conformanceCtx)
				assertJSONEqual(t, "before entries", entryIDs(entries), []string{repoRootID})

				midID := "mid"
				mid, err := repo.Fork(repo.Metadata(source), forkOpts(midID, "main", strPtr(repoRootID), session.ForkPositionAt), conformanceCtx)
				if err != nil {
					t.Fatal(err)
				}
				if tip := repoGetBranchTip(t, mid); tip == nil || *tip != repoRootID {
					t.Fatalf("mid tip = %v", tip)
				}
				entries, _ = mid.FindEntries(&session.EntryQuery{Order: "asc"}, conformanceCtx)
				assertJSONEqual(t, "mid entries", entryIDs(entries), []string{repoRootID})

				beforeRootID := "before-root"
				beforeRoot, err := repo.Fork(repo.Metadata(source), forkOpts(beforeRootID, "main", strPtr(repoRootID), session.ForkPositionBefore), conformanceCtx)
				if err != nil {
					t.Fatal(err)
				}
				if tip := repoGetBranchTip(t, beforeRoot); tip != nil {
					t.Fatalf("before-root tip = %v", tip)
				}
				entries, _ = beforeRoot.FindEntries(&session.EntryQuery{Order: "asc"}, conformanceCtx)
				if len(entries) != 0 {
					t.Fatalf("before-root entries = %v", entries)
				}

				emptyID := "empty"
				empty, err := repo.Fork(repo.Metadata(source), forkOpts(emptyID, "empty", nil, ""), conformanceCtx)
				if err != nil {
					t.Fatal(err)
				}
				if tip := repoGetBranchTip(t, empty, "empty"); tip != nil {
					t.Fatalf("empty tip = %v", tip)
				}

				for _, tc := range []struct {
					id, branch string
					entryID    *string
				}{
					{"off-branch", "main", strPtr(repoSiblingID)},
					{"unknown", "main", strPtr(repoUnknownID)},
					{"null-tip", "empty", strPtr(repoRootID)},
				} {
					if _, err := repo.Fork(repo.Metadata(source), forkOpts(tc.id, tc.branch, tc.entryID, ""), conformanceCtx); err == nil {
						t.Fatalf("fork %s must reject", tc.id)
					}
				}
				listed, _ := repo.List(conformanceCtx)
				ids := []string{}
				for _, metadata := range listed {
					ids = append(ids, repo.MetadataID(metadata))
				}
				sortStrings(ids)
				assertJSONEqual(t, "listed", ids, []string{"before", "before-root", "empty", "mid", "source"})
				for _, s := range []session.Session{source, before, beforeRoot, empty, mid} {
					_ = s.Close(conformanceCtx)
				}
			},
		},
		{
			Group: "forks", Name: "forks a closed source session",
			Run: func(t *testing.T) {
				repo := factory(t)
				sourceID := "source"
				source, err := repo.Create(&sourceID, nil, conformanceCtx)
				if err != nil {
					t.Fatal(err)
				}
				_, err = source.Mutate(func(mutator session.SessionMutator, ctx context.Context) (any, error) {
					_, commitErr := mutator.Commit([]session.Write{
						session.InsertEntry(repoCustomEntry(repoRootID, "", "root")),
						session.SetValue(session.BranchTip("main"), repoRootID),
						session.SetValue(session.LaneConfig("main"), repoConfiguration()),
						session.SetValue(session.LaneStateAddress("main"), repoIdleLaneState()),
						session.SetValue(repoApplicationValue, "excluded"),
						session.AppendList(repoApplicationList, "excluded"),
					}, ctx)
					return nil, commitErr
				}, conformanceCtx)
				if err != nil {
					t.Fatal(err)
				}
				_ = source.Close(conformanceCtx)

				forkID := "fork"
				fork, err := repo.Fork(repo.Metadata(source), session.ForkOptions{ID: &forkID, Scope: session.ForkScopeBranch, Branch: "main"}, conformanceCtx)
				if err != nil {
					t.Fatal(err)
				}
				if tip := repoGetBranchTip(t, fork); tip == nil || *tip != repoRootID {
					t.Fatalf("tip = %v", tip)
				}
				config, _ := fork.GetValue(session.LaneConfig("main"), conformanceCtx)
				assertJSONEqual(t, "config", config.Value, repoConfiguration())
				state, _ := fork.GetValue(session.LaneStateAddress("main"), conformanceCtx)
				assertJSONEqual(t, "state", state.Value, repoIdleLaneState())
				if stored, _ := fork.GetValue(repoApplicationValue, conformanceCtx); stored != nil {
					t.Fatal("application value must be excluded on branch scope")
				}
				elements, _ := fork.ReadList(repoApplicationList, nil, conformanceCtx)
				if len(elements) != 0 {
					t.Fatal("application list must be excluded on branch scope")
				}
				_ = fork.Close(conformanceCtx)
			},
		},
		{
			Group: "forks", Name: "forks the whole configured tree with fresh lane state",
			Run: func(t *testing.T) {
				repo := factory(t)
				sourceID := "source"
				source, err := repo.Create(&sourceID, nil, conformanceCtx)
				if err != nil {
					t.Fatal(err)
				}
				_, err = source.Mutate(func(mutator session.SessionMutator, ctx context.Context) (any, error) {
					_, commitErr := mutator.Commit([]session.Write{
						session.InsertEntry(repoCustomEntry(repoRootID, "", "root")),
						session.InsertEntry(repoCustomEntry(repoChildID, repoRootID, "child")),
						session.InsertEntry(repoCustomEntry(repoSiblingID, repoRootID, "sibling")),
						session.SetValue(session.BranchTip("main"), repoChildID),
						session.SetValue(session.LaneConfig("main"), repoConfiguration()),
						session.SetValue(session.LaneStateAddress("main"), repoIdleLaneState()),
						session.SetValue(session.BranchTip("review"), repoSiblingID),
						session.SetValue(session.LaneConfig("review"), repoConfiguration()),
						session.SetValue(session.LaneStateAddress("review"), repoIdleLaneState()),
						session.SetValue(session.BranchTip("notes"), repoRootID),
						session.SetValue(repoApplicationValue, map[string]any{"copied": true}),
					}, ctx)
					return nil, commitErr
				}, conformanceCtx)
				if err != nil {
					t.Fatal(err)
				}

				forkID := "fork"
				fork, err := repo.Fork(repo.Metadata(source), session.ForkOptions{ID: &forkID, Scope: session.ForkScopeTree}, conformanceCtx)
				if err != nil {
					t.Fatal(err)
				}
				entries, _ := fork.FindEntries(&session.EntryQuery{Order: "asc"}, conformanceCtx)
				assertJSONEqual(t, "entries", entryIDs(entries), []string{repoRootID, repoChildID, repoSiblingID})
				if tip := repoGetBranchTip(t, fork); tip == nil || *tip != repoChildID {
					t.Fatalf("main tip = %v", tip)
				}
				if tip := repoGetBranchTip(t, fork, "review"); tip == nil || *tip != repoSiblingID {
					t.Fatalf("review tip = %v", tip)
				}
				if tip := repoGetBranchTip(t, fork, "notes"); tip == nil || *tip != repoRootID {
					t.Fatalf("notes tip = %v", tip)
				}
				if stored, _ := fork.GetValue(session.LaneConfig("notes"), conformanceCtx); stored != nil {
					t.Fatal("data-only lane config must not reconstruct")
				}
				if stored, _ := fork.GetValue(session.LaneStateAddress("notes"), conformanceCtx); stored != nil {
					t.Fatal("data-only lane state must not reconstruct")
				}
				config, _ := fork.GetValue(session.LaneConfig("review"), conformanceCtx)
				assertJSONEqual(t, "review config", config.Value, repoConfiguration())
				state, _ := fork.GetValue(session.LaneStateAddress("review"), conformanceCtx)
				assertJSONEqual(t, "review state", state.Value, repoIdleLaneState())
				stored, _ := fork.GetValue(repoApplicationValue, conformanceCtx)
				assertJSONEqual(t, "application value", stored.Value, map[string]any{"copied": true})
				_ = source.Close(conformanceCtx)
				_ = fork.Close(conformanceCtx)
			},
		},
		{
			Group: "forks", Name: "rejects only surviving unknown reserved scalar state",
			Run: func(t *testing.T) {
				repo := factory(t)
				sourceID := "source"
				source, err := repo.Create(&sourceID, nil, conformanceCtx)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := source.CreateBranch("main", nil, conformanceCtx); err != nil {
					t.Fatal(err)
				}
				_, err = source.Mutate(func(mutator session.SessionMutator, ctx context.Context) (any, error) {
					return nil, mutatorCommit(mutator, []session.Write{
						session.SetValue(session.LaneConfig("main"), repoConfiguration()),
						session.SetValue(session.LaneStateAddress("main"), repoIdleLaneState()),
					}, ctx)
				}, conformanceCtx)
				if err != nil {
					t.Fatal(err)
				}

				for _, namespace := range []string{"pi", "pi.unknown"} {
					address := session.MustValue(namespace, "")
					if err := source.SetValue(address, true, conformanceCtx); err != nil {
						t.Fatal(err)
					}
					treeID := "tree"
					if _, err := repo.Fork(repo.Metadata(source), session.ForkOptions{ID: &treeID, Scope: session.ForkScopeTree}, conformanceCtx); err == nil {
						t.Fatalf("fork with unknown reserved namespace %s must reject", namespace)
					}
					branchID := "branch"
					if _, err := repo.Fork(repo.Metadata(source), session.ForkOptions{ID: &branchID, Scope: session.ForkScopeBranch, Branch: "main"}, conformanceCtx); err == nil {
						t.Fatalf("branch fork with unknown reserved namespace %s must reject", namespace)
					}
					if err := source.DeleteValue(address, conformanceCtx); err != nil {
						t.Fatal(err)
					}
				}
				_ = source.Close(conformanceCtx)

				treeID := "tree"
				tree, err := repo.Fork(repo.Metadata(source), session.ForkOptions{ID: &treeID, Scope: session.ForkScopeTree}, conformanceCtx)
				if err != nil {
					t.Fatal(err)
				}
				branchID := "branch"
				branch, err := repo.Fork(repo.Metadata(source), session.ForkOptions{ID: &branchID, Scope: session.ForkScopeBranch, Branch: "main"}, conformanceCtx)
				if err != nil {
					t.Fatal(err)
				}
				_ = tree.Close(conformanceCtx)
				_ = branch.Close(conformanceCtx)
			},
		},
	}
}

func mutatorCommit(mutator session.SessionMutator, writes []session.Write, ctx context.Context) error {
	_, err := mutator.Commit(writes, ctx)
	return err
}

func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}

// SessionRepoForkCoordinationConformance creates every fork coordination
// case (pi: createSessionRepoForkCoordinationConformance).
func SessionRepoForkCoordinationConformance[TMetadata any](factory RepoFactory[TMetadata]) []ConformanceCase {
	reservation := func(firstWins string) ConformanceCase {
		return ConformanceCase{
			Group: "fork coordination", Name: "publishes " + firstWins + " when it reserves a shared destination id first",
			Run: func(t *testing.T) {
				repo := factory(t)
				sourceID := "source"
				source, err := repo.Create(&sourceID, nil, conformanceCtx)
				if err != nil {
					t.Fatal(err)
				}
				destinationID := "destination"
				type outcome struct {
					sess session.Session
					err  error
				}
				firstCh := make(chan outcome, 1)
				secondCh := make(chan outcome, 1)
				forkOpts := session.ForkOptions{ID: &destinationID, Scope: session.ForkScopeTree}
				if firstWins == "create" {
					go func() {
						sess, err := repo.Create(&destinationID, nil, conformanceCtx)
						firstCh <- outcome{sess, err}
					}()
					// pi's promise admission order needs a yield in Go.
					time.Sleep(20 * time.Millisecond)
					go func() {
						sess, err := repo.Fork(repo.Metadata(source), forkOpts, conformanceCtx)
						secondCh <- outcome{sess, err}
					}()
				} else {
					go func() {
						sess, err := repo.Fork(repo.Metadata(source), forkOpts, conformanceCtx)
						firstCh <- outcome{sess, err}
					}()
					time.Sleep(20 * time.Millisecond)
					go func() {
						sess, err := repo.Create(&destinationID, nil, conformanceCtx)
						secondCh <- outcome{sess, err}
					}()
				}
				first := <-firstCh
				second := <-secondCh
				if first.err != nil {
					t.Fatalf("%s must win: %v", firstWins, first.err)
				}
				if second.err == nil {
					t.Fatal("the loser must reject")
				}
				_ = first.sess.Close(conformanceCtx)
				_ = source.Close(conformanceCtx)
			},
		}
	}
	return []ConformanceCase{
		reservation("create"),
		reservation("fork"),
		{
			Group: "fork coordination", Name: "captures one coherent boundary between source commits",
			Run: func(t *testing.T) {
				repo := factory(t)
				sourceID := "source"
				source, err := repo.Create(&sourceID, nil, conformanceCtx)
				if err != nil {
					t.Fatal(err)
				}
				// pi interleaves firstCommit, fork, and secondCommit on the
				// source commit queue so the fork snapshot lands between the
				// commits; pig needs one mutation per commit (one commit per
				// mutator), so the fork runs after the first commit's
				// mutation ends — the captured boundary is the same state.
				if _, err := source.Mutate(func(mutator session.SessionMutator, ctx context.Context) (any, error) {
					return nil, mutatorCommit(mutator, []session.Write{
						session.InsertEntry(repoCustomEntry(repoRootID, "", "first")),
						session.SetValue(session.BranchTip("main"), repoRootID),
						session.SetValue(session.LaneConfig("main"), repoConfiguration()),
						session.SetValue(session.LaneStateAddress("main"), repoIdleLaneState()),
						session.SetValue(session.SessionNameAddress, "first name"),
						session.SetValue(session.EntryLabel(repoRootID), "first label"),
					}, ctx)
				}, conformanceCtx); err != nil {
					t.Fatal(err)
				}
				forkID := "fork"
				forked, err := repo.Fork(repo.Metadata(source), session.ForkOptions{ID: &forkID, Scope: session.ForkScopeBranch, Branch: "main"}, conformanceCtx)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := source.Mutate(func(mutator session.SessionMutator, ctx context.Context) (any, error) {
					return nil, mutatorCommit(mutator, []session.Write{
						session.InsertEntry(repoCustomEntry(repoChildID, repoRootID, "second")),
						session.SetValue(session.BranchTip("main"), repoChildID),
						session.SetValue(session.SessionNameAddress, "second name"),
						session.SetValue(session.EntryLabel(repoRootID), "second label"),
					}, ctx)
				}, conformanceCtx); err != nil {
					t.Fatal(err)
				}
				if tip := repoGetBranchTip(t, forked); tip == nil || *tip != repoRootID {
					t.Fatalf("fork tip = %v", tip)
				}
				entries, _ := forked.FindEntries(&session.EntryQuery{Order: "asc"}, conformanceCtx)
				assertJSONEqual(t, "fork entries", entryIDs(entries), []string{repoRootID})
				name, _ := forked.GetName(conformanceCtx)
				if name == nil || *name != "first name" {
					t.Fatalf("fork name = %v", name)
				}
				if label, _ := forked.GetLabel(repoRootID, conformanceCtx); label == nil || *label != "first label" {
					t.Fatalf("fork label = %v", label)
				}
				_ = forked.Close(conformanceCtx)
				_ = source.Close(conformanceCtx)
			},
		},
	}
}

// SessionRepoConformanceCases creates every SessionRepo conformance case
// (pi: createSessionRepoConformance).
func SessionRepoConformanceCases[TMetadata any](factory RepoFactory[TMetadata]) []ConformanceCase {
	cases := []ConformanceCase{}
	cases = append(cases, SessionRepoLifecycleConformance(factory)...)
	cases = append(cases, SessionRepoOwnershipConformance(factory)...)
	cases = append(cases, SessionRepoMessageConformance(factory)...)
	cases = append(cases, SessionRepoForkConformance(factory)...)
	return cases
}

// SessionRepoForkConformance creates every fork conformance case (pi:
// createSessionRepoForkConformance).
func SessionRepoForkConformance[TMetadata any](factory RepoFactory[TMetadata]) []ConformanceCase {
	cases := []ConformanceCase{}
	cases = append(cases, SessionRepoForkBehaviorConformance(factory)...)
	cases = append(cases, SessionRepoForkAncestryConformance(factory)...)
	cases = append(cases, SessionRepoForkCoordinationConformance(factory)...)
	return cases
}
