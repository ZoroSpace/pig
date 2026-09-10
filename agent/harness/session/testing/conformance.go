// pi: src/harness/session/testing — session backend conformance suite.
//
// Package name deviation: pi's package is `testing` under
// harness/session/testing; Go's directory keeps the path but the package is
// named sessiontesting because a package named `testing` cannot import the
// standard-library testing package it drives.
//
// Deviations:
//   - pi's case.run() is an async thrower driven by vitest; Go cases take
//     *testing.T and report via t.Fatal/t.Fatalf.
//   - SessionBackendFixture[Symbol.asyncDispose] → the factory registers
//     cleanup with t.Cleanup and returns the repo.
//   - deepStrictEqual → JSON-marshal byte equality (encoding/json sorts map
//     keys, so struct-literal and decoded-generic values compare
//     canonically).
//   - rejectsWithCode asserts via errors.As on *session.SessionError; call
//     sites bind the (T, error) result first, mirroring pi's
//     `await rejects(operation, ...)`.
//   - The "rejects non-JSON entries/records" cases port only the inputs
//     representable in Go's JSON data model (NaN, cycles, unsupported
//     func values); TS-only rejections (undefined-valued properties,
//     BigInt, Map instances) are unrepresentable and noted in place.
package sessiontesting

import (
	"encoding/json"
	"errors"
	"math"
	"sync"
	"testing"

	"github.com/samber/lo"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/agent/harness/session"
	"github.com/ZoroSpace/pig/ai/types"
)

// SessionBackendFixture is one backend instance under test (pi:
// SessionBackendFixture; disposal moves to the factory's t.Cleanup).
type SessionBackendFixture struct {
	Repository session.SessionRepo
}

// SessionBackendFixtureFactory creates a fresh fixture per case (pi:
// SessionBackendFixtureFactory).
type SessionBackendFixtureFactory func(t *testing.T) session.SessionRepo

// SessionBackendConformanceCase is one conformance case (pi:
// SessionBackendConformanceCase).
type SessionBackendConformanceCase struct {
	Group string
	Name  string
	Run   func(t *testing.T, repository session.SessionRepo)
}

func createUserMessage(text string) agent.AgentMessage {
	return types.UserMessage{
		Role:      types.RoleUser,
		Content:   types.UserContent{Parts: []types.Content{types.NewTextContent(text)}},
		Timestamp: 1,
	}
}

func createAssistantMessage(text string) agent.AgentMessage {
	return types.AssistantMessage{
		Role:       types.RoleAssistant,
		Content:    []types.Content{types.NewTextContent(text)},
		API:        types.ApiAnthropicMessages,
		Provider:   "anthropic",
		Model:      "claude-sonnet-4-5",
		Usage:      createUsage(0),
		StopReason: types.StopReasonStop,
		Timestamp:  1,
	}
}

func createUsage(multiplier float64) types.Usage {
	return createUsageWithTotal(
		int64(multiplier), int64(multiplier*2), int64(multiplier*3), int64(multiplier*4), int64(multiplier*10),
		multiplier*0.1, multiplier*0.2, multiplier*0.3, multiplier*0.4, multiplier,
	)
}

func createUsageWithTotal(input, output, cacheRead, cacheWrite, totalTokens int64, costInput, costOutput, costCacheRead, costCacheWrite, costTotal float64) types.Usage {
	return types.Usage{
		Input:       input,
		Output:      output,
		CacheRead:   cacheRead,
		CacheWrite:  cacheWrite,
		TotalTokens: totalTokens,
		Cost: types.Cost{
			Input:      costInput,
			Output:     costOutput,
			CacheRead:  costCacheRead,
			CacheWrite: costCacheWrite,
			Total:      costTotal,
		},
	}
}

func zeroUsage() types.Usage {
	return types.Usage{Cost: types.Cost{}}
}

// operationStarted builds a provisioned operation_started record (pi:
// operationStarted helper).
func operationStarted(id string, lane string, kind session.OperationIntentKind) session.LaneRecord {
	var intent session.OperationIntent
	switch kind {
	case session.OperationIntentRun:
		intent = &session.RunIntent{
			Kind:            kind,
			OriginalPrompt:  []agent.AgentMessage{},
			InitialMessages: []session.Entry{},
		}
	case session.OperationIntentCompaction:
		intent = &session.CompactionIntent{Kind: kind, ResultEntryID: id + "-result"}
	case session.OperationIntentNavigation:
		intent = &session.NavigationIntent{Kind: kind, TargetID: nil, Summarize: false}
	}
	return &session.OperationStartedRecord{
		RecordBase:   session.RecordBase{Type: session.RecordTypeOperationStarted, ID: id, Lane: lane},
		SourceLeafID: nil,
		Intent:       intent,
	}
}

// entryIDs maps entries to their ids (pi: entryIds helper).
func entryIDs(entries []session.Entry) []string {
	ids := make([]string, len(entries))
	for i, entry := range entries {
		ids[i] = entry.Base().ID
	}
	return ids
}

func recordIDs(records []session.LaneRecord) []string {
	ids := make([]string, len(records))
	for i, record := range records {
		ids[i] = record.Base().ID
	}
	return ids
}

// deepEqualJSON compares two values through their canonical JSON encoding
// (pi: deepStrictEqual for JSON-shaped data).
func deepEqualJSON(t *testing.T, actual, expected any) {
	t.Helper()
	actualJSON, err := json.Marshal(actual)
	if err != nil {
		t.Fatalf("marshal actual: %v", err)
	}
	expectedJSON, err := json.Marshal(expected)
	if err != nil {
		t.Fatalf("marshal expected: %v", err)
	}
	if string(actualJSON) != string(expectedJSON) {
		t.Fatalf("deepEqual mismatch:\n  actual:   %s\n  expected: %s", actualJSON, expectedJSON)
	}
}

// rejectsWithCode asserts the error is a SessionError carrying exactly the
// expected code (pi: rejectsWithCode helper).
func rejectsWithCode(t *testing.T, err error, code session.SessionErrorCode) {
	t.Helper()
	if err == nil {
		t.Fatalf("Expected SessionError with code %s, got nil error", code)
	}
	var sessionErr *session.SessionError
	if !errors.As(err, &sessionErr) {
		t.Fatalf("Expected SessionError with code %s, got %T: %v", code, err, err)
	}
	if sessionErr.Code != code {
		t.Fatalf("Expected SessionError with code %s, got %s: %v", code, sessionErr.Code, err)
	}
}

func strPtr2(s string) *string { return &s } // second literal in one call

// CreateSessionBackendConformance creates the session backend conformance
// cases. Each case creates and disposes its own fixture (pi:
// createSessionBackendConformance).
func CreateSessionBackendConformance(factory SessionBackendFixtureFactory) []SessionBackendConformanceCase {
	createCase := func(group string, name string, test func(t *testing.T, repository session.SessionRepo)) SessionBackendConformanceCase {
		return SessionBackendConformanceCase{
			Group: group,
			Name:  name,
			Run: func(t *testing.T, repository session.SessionRepo) {
				t.Helper()
				test(t, repository)
			},
		}
	}

	return []SessionBackendConformanceCase{
		createCase("entries and lanes", "assigns parents and one sequence across every mutation", func(t *testing.T, repository session.SessionRepo) {
			s, err := repository.Create(&session.SessionCreateOptions{ID: lo.ToPtr("session")})
			if err != nil {
				t.Fatal(err)
			}
			root, err := s.AppendEntry(&session.MessageEntry{
				EntryBase: session.EntryBase{Type: session.EntryTypeMessage, ID: "root"},
				Message:   createUserMessage("root"),
			}, "main")
			if err != nil {
				t.Fatal(err)
			}
			err = s.CreateLane("thread", lo.ToPtr("root"))
			if err != nil {
				t.Fatal(err)
			}
			child, err := s.AppendEntry(&session.CustomEntry{
				EntryBase:  session.EntryBase{Type: session.EntryTypeCustom, ID: "child"},
				CustomType: "note",
				Data:       map[string]any{"value": float64(1)},
			}, "thread")
			if err != nil {
				t.Fatal(err)
			}
			record, err := s.AppendRecord(operationStarted("run", "thread", session.OperationIntentRun))
			if err != nil {
				t.Fatal(err)
			}
			err = s.SetName(lo.ToPtr("Example"))
			if err != nil {
				t.Fatal(err)
			}
			err = s.SetLabel("root", lo.ToPtr("checkpoint"))
			if err != nil {
				t.Fatal(err)
			}
			err = s.MoveLane("main", lo.ToPtr("child"))
			if err != nil {
				t.Fatal(err)
			}

			rootBase := root.(*session.MessageEntry).EntryBase
			deepEqualJSON(t, map[string]any{"parentId": rootBase.ParentID, "seq": rootBase.Seq},
				map[string]any{"parentId": nil, "seq": float64(1)})
			childBase := child.(*session.CustomEntry).EntryBase
			deepEqualJSON(t, map[string]any{"parentId": childBase.ParentID, "seq": childBase.Seq},
				map[string]any{"parentId": "root", "seq": float64(3)})
			if record.Base().Seq != 4 {
				t.Fatalf("record seq = %d, want 4", record.Base().Seq)
			}
			for _, timestamp := range []int64{rootBase.Timestamp, childBase.Timestamp, record.Base().Timestamp} {
				if timestamp < 0 {
					t.Fatalf("storage-assigned timestamps must be Unix milliseconds, got %d", timestamp)
				}
			}
			log, err := s.GetLog(nil)
			if err != nil {
				t.Fatal(err)
			}
			kindSeq := make([][]any, len(log))
			for i, item := range log {
				kindSeq[i] = []any{item.Kind, item.Seq}
			}
			deepEqualJSON(t, kindSeq, [][]any{
				{"entry", float64(1)},
				{"lane", float64(2)},
				{"entry", float64(3)},
				{"record", float64(4)},
				{"fact", float64(5)},
				{"fact", float64(6)},
				{"lane", float64(7)},
			})
			lanes, err := s.GetLanes()
			if err != nil {
				t.Fatal(err)
			}
			deepEqualJSON(t, lanes, []session.LanePointer{
				{Lane: "main", LeafID: lo.ToPtr("child")},
				{Lane: "thread", LeafID: lo.ToPtr("child")},
			})
		}),

		createCase("records and log", "commits records and lane moves as separate mutations", func(t *testing.T, repository session.SessionRepo) {
			s, err := repository.Create(&session.SessionCreateOptions{ID: lo.ToPtr("session")})
			if err != nil {
				t.Fatal(err)
			}
			root, err := s.AppendEntry(&session.MessageEntry{
				EntryBase: session.EntryBase{Type: session.EntryTypeMessage, ID: "root"},
				Message:   createUserMessage("root"),
			}, "main")
			if err != nil {
				t.Fatal(err)
			}
			finished, err := s.AppendRecord(&session.OperationFinishedRecord{
				RecordBase: session.RecordBase{Type: session.RecordTypeOperationFinished, ID: "finish", Lane: "main"},
				RunID:      "run",
				Outcome:    "completed",
			})
			if err != nil {
				t.Fatal(err)
			}

			if finished.Base().Seq != 2 {
				t.Fatalf("finished seq = %d, want 2", finished.Base().Seq)
			}
			lanes, err := s.GetLanes()
			if err != nil {
				t.Fatal(err)
			}
			deepEqualJSON(t, lanes, []session.LanePointer{{Lane: "main", LeafID: lo.ToPtr("root")}})
			err = s.MoveLane("main", nil)
			if err != nil {
				t.Fatal(err)
			}
			lanes, err = s.GetLanes()
			if err != nil {
				t.Fatal(err)
			}
			deepEqualJSON(t, lanes, []session.LanePointer{{Lane: "main", LeafID: nil}})
			log, err := s.GetLog(nil)
			if err != nil {
				t.Fatal(err)
			}
			deepEqualJSON(t, log, []session.LogItem{
				{Kind: "entry", Seq: 1, Entry: root},
				{Kind: "record", Seq: 2, Record: finished},
				{Kind: "lane", Seq: 3, Lane: "main", LeafID: nil},
			})

			err = s.MoveLane("main", lo.ToPtr("missing"))
			rejectsWithCode(t, err, session.CodeNotFound)
			records, err := s.FindRecords(nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(records) != 1 {
				t.Fatalf("records length = %d, want 1", len(records))
			}
			log, err = s.GetLog(nil)
			if err != nil {
				t.Fatal(err)
			}
			seqs := make([]int64, len(log))
			for i, item := range log {
				seqs[i] = item.Seq
			}
			deepEqualJSON(t, seqs, []int64{1, 2, 3})
		}),

		createCase("entries and lanes", "rejects duplicate ids without changing state", func(t *testing.T, repository session.SessionRepo) {
			s, err := repository.Create(&session.SessionCreateOptions{ID: lo.ToPtr("session")})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.AppendEntry(&session.MessageEntry{
				EntryBase: session.EntryBase{Type: session.EntryTypeMessage, ID: "shared"},
				Message:   createUserMessage("root"),
			}, "main"); err != nil {
				t.Fatal(err)
			}
			_, err = s.AppendRecord(operationStarted("shared", "main", session.OperationIntentRun))
			rejectsWithCode(t, err, session.CodeAlreadyExists)
			if _, err := s.AppendRecord(operationStarted("run", "main", session.OperationIntentRun)); err != nil {
				t.Fatal(err)
			}
			_, err = s.AppendEntry(&session.CustomEntry{
				EntryBase:  session.EntryBase{Type: session.EntryTypeCustom, ID: "run"},
				CustomType: "note",
			}, "main")
			rejectsWithCode(t, err, session.CodeAlreadyExists)
			log, err := s.GetLog(nil)
			if err != nil {
				t.Fatal(err)
			}
			seqs := make([]int64, len(log))
			for i, item := range log {
				seqs[i] = item.Seq
			}
			deepEqualJSON(t, seqs, []int64{1, 2})
		}),

		createCase("entries and lanes", "isolates lanes while sharing the tree", func(t *testing.T, repository session.SessionRepo) {
			s, err := repository.Create(&session.SessionCreateOptions{ID: lo.ToPtr("session")})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.AppendEntry(&session.MessageEntry{
				EntryBase: session.EntryBase{Type: session.EntryTypeMessage, ID: "root"},
				Message:   createUserMessage("root"),
			}, "main"); err != nil {
				t.Fatal(err)
			}
			err = s.CreateLane("thread", lo.ToPtr("root"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.AppendEntry(&session.MessageEntry{
				EntryBase: session.EntryBase{Type: session.EntryTypeMessage, ID: "main-child"},
				Message:   createUserMessage("main"),
			}, "main"); err != nil {
				t.Fatal(err)
			}
			if _, err := s.AppendEntry(&session.MessageEntry{
				EntryBase: session.EntryBase{Type: session.EntryTypeMessage, ID: "thread-child"},
				Message:   createUserMessage("thread"),
			}, "thread"); err != nil {
				t.Fatal(err)
			}

			lanes, err := s.GetLanes()
			if err != nil {
				t.Fatal(err)
			}
			deepEqualJSON(t, lanes, []session.LanePointer{
				{Lane: "main", LeafID: lo.ToPtr("main-child")},
				{Lane: "thread", LeafID: lo.ToPtr("thread-child")},
			})
			mainBranch, err := s.FindEntriesOnBranch(&session.BranchQuery{
				EntryQuery:   session.EntryQuery{Order: session.OrderOldestFirst},
				BranchBounds: session.BranchBounds{Start: lo.ToPtr("main-child")},
			})
			if err != nil {
				t.Fatal(err)
			}
			deepEqualJSON(t, entryIDs(mainBranch), []string{"root", "main-child"})
			threadBranch, err := s.FindEntriesOnBranch(&session.BranchQuery{
				EntryQuery:   session.EntryQuery{Order: session.OrderOldestFirst},
				BranchBounds: session.BranchBounds{Start: lo.ToPtr("thread-child")},
			})
			if err != nil {
				t.Fatal(err)
			}
			deepEqualJSON(t, entryIDs(threadBranch), []string{"root", "thread-child"})
		}),

		createCase("queries and facts", "rejects invalid queries before empty reads", func(t *testing.T, repository session.SessionRepo) {
			s, err := repository.Create(&session.SessionCreateOptions{ID: lo.ToPtr("invalid-queries")})
			if err != nil {
				t.Fatal(err)
			}
			err = s.CreateLane("thread", nil)
			if err != nil {
				t.Fatal(err)
			}
			thread := s.View("thread")
			zero := 0
			minusOne := -1
			_, err = s.FindEntries(&session.EntryQuery{Limit: &zero})
			rejectsWithCode(t, err, session.CodeInvalidQuery)
			_, err = s.FindEntry(&session.EntryQuery{Limit: &zero})
			rejectsWithCode(t, err, session.CodeInvalidQuery)
			_, err = s.FindEntriesOnBranch(&session.BranchQuery{EntryQuery: session.EntryQuery{Limit: &zero}})
			rejectsWithCode(t, err, session.CodeInvalidQuery)
			_, err = thread.FindEntriesOnBranch(&session.BranchQuery{EntryQuery: session.EntryQuery{Cursor: &session.EntryCursor{AfterSeq: -1}}})
			rejectsWithCode(t, err, session.CodeInvalidQuery)
			_, err = thread.FindEntryOnBranch(&session.BranchQuery{EntryQuery: session.EntryQuery{Limit: &zero}})
			rejectsWithCode(t, err, session.CodeInvalidQuery)
			_, err = s.FindRecords(&session.RecordQuery{Limit: &zero})
			rejectsWithCode(t, err, session.CodeInvalidQuery)
			runKind := session.OperationIntentRun
			_, err = s.FindRecords(&session.RecordQuery{OperationKind: &runKind})
			rejectsWithCode(t, err, session.CodeInvalidQuery)
			stepType := session.RecordTypeStepAttempt
			_, err = s.FindRecords(&session.RecordQuery{Type: &stepType, OperationKind: &runKind})
			rejectsWithCode(t, err, session.CodeInvalidQuery)
			_, err = s.FindOpenOperations("main", &zero)
			rejectsWithCode(t, err, session.CodeInvalidQuery)
			_, err = s.FindOpenOperations("main", &minusOne)
			rejectsWithCode(t, err, session.CodeInvalidQuery)
			_, err = s.GetLog(&session.LogOptions{AfterSeq: lo.ToPtr(int64(-1))})
			rejectsWithCode(t, err, session.CodeInvalidQuery)
		}),

		createCase("queries and facts", "supports bounded filtered and cursor-based queries", func(t *testing.T, repository session.SessionRepo) {
			s, err := repository.Create(&session.SessionCreateOptions{ID: lo.ToPtr("session")})
			if err != nil {
				t.Fatal(err)
			}
			mustAppend := func(entry session.Entry) {
				t.Helper()
				if _, err := s.AppendEntry(entry, "main"); err != nil {
					t.Fatal(err)
				}
			}
			mustAppend(&session.MessageEntry{EntryBase: session.EntryBase{Type: session.EntryTypeMessage, ID: "root"}, Message: createUserMessage("root")})
			mustAppend(&session.CustomEntry{EntryBase: session.EntryBase{Type: session.EntryTypeCustom, ID: "old-note"}, CustomType: "note", Data: float64(1)})
			mustAppend(&session.CompactionEntry{EntryBase: session.EntryBase{Type: session.EntryTypeCompaction, ID: "compact"}, Summary: "summary", TokensBefore: 10})
			mustAppend(&session.CustomEntry{EntryBase: session.EntryBase{Type: session.EntryTypeCustom, ID: "new-note"}, CustomType: "note", Data: float64(2)})
			mustAppend(&session.MessageEntry{EntryBase: session.EntryBase{Type: session.EntryTypeMessage, ID: "tail"}, Message: createAssistantMessage("tail")})

			entries, err := s.FindEntries(nil)
			if err != nil {
				t.Fatal(err)
			}
			deepEqualJSON(t, entryIDs(entries), []string{"tail", "new-note", "compact", "old-note", "root"})
			oldestFirst := session.OrderOldestFirst
			after2 := int64(2)
			limit2 := 2
			entries, err = s.FindEntries(&session.EntryQuery{Order: oldestFirst, Cursor: &session.EntryCursor{AfterSeq: after2}, Limit: &limit2})
			if err != nil {
				t.Fatal(err)
			}
			deepEqualJSON(t, entryIDs(entries), []string{"compact", "new-note"})
			noteType := session.EntryTypeCustom
			noteCustom := "note"
			entries, err = s.FindEntries(&session.EntryQuery{Type: &noteType, CustomType: &noteCustom})
			if err != nil {
				t.Fatal(err)
			}
			deepEqualJSON(t, entryIDs(entries), []string{"new-note", "old-note"})
			limit1 := 1
			entries, err = s.FindEntriesOnBranch(&session.BranchQuery{
				EntryQuery:   session.EntryQuery{CustomType: &noteCustom, Limit: &limit1},
				BranchBounds: session.BranchBounds{Start: lo.ToPtr("tail")},
			})
			if err != nil {
				t.Fatal(err)
			}
			deepEqualJSON(t, entryIDs(entries), []string{"new-note"})
			messageType := session.EntryTypeMessage
			compactionType := session.EntryTypeCompaction
			entries, err = s.FindEntriesOnBranch(&session.BranchQuery{
				EntryQuery:   session.EntryQuery{Type: &messageType},
				BranchBounds: session.BranchBounds{Start: lo.ToPtr("tail"), StopAtType: &compactionType},
			})
			if err != nil {
				t.Fatal(err)
			}
			deepEqualJSON(t, entryIDs(entries), []string{"tail"})
			entries, err = s.FindEntriesOnBranch(&session.BranchQuery{
				EntryQuery:   session.EntryQuery{Type: &noteType},
				BranchBounds: session.BranchBounds{Start: lo.ToPtr("tail"), StopAtID: lo.ToPtr("tail")},
			})
			if err != nil {
				t.Fatal(err)
			}
			deepEqualJSON(t, entryIDs(entries), []string{})
			entries, err = s.FindEntriesOnBranch(&session.BranchQuery{
				EntryQuery:   session.EntryQuery{Order: oldestFirst},
				BranchBounds: session.BranchBounds{Start: lo.ToPtr("tail"), StopAtType: &noteType},
			})
			if err != nil {
				t.Fatal(err)
			}
			deepEqualJSON(t, entryIDs(entries), []string{"root", "old-note"})
			zero := 0
			_, err = s.FindEntries(&session.EntryQuery{Limit: &zero})
			rejectsWithCode(t, err, session.CodeInvalidQuery)
			_, err = s.FindEntriesOnBranch(&session.BranchQuery{BranchBounds: session.BranchBounds{Start: lo.ToPtr("missing")}})
			rejectsWithCode(t, err, session.CodeNotFound)
		}),

		createCase("records and log", "keeps lane names permanent with their recovery records", func(t *testing.T, repository session.SessionRepo) {
			s, err := repository.Create(&session.SessionCreateOptions{ID: lo.ToPtr("session")})
			if err != nil {
				t.Fatal(err)
			}
			err = s.CreateLane("thread", nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.AppendRecord(operationStarted("old-run", "thread", session.OperationIntentRun)); err != nil {
				t.Fatal(err)
			}
			if _, err := s.AppendRecord(&session.QueueEnqueuedRecord{
				RecordBase: session.RecordBase{Type: session.RecordTypeQueueEnqueued, ID: "old-next-run", Lane: "thread"},
				Queue:      session.QueueNextRun,
				Target: &session.MessageEntry{
					EntryBase: session.EntryBase{Type: session.EntryTypeMessage, ID: "queued-message"},
					Message:   createUserMessage("queued"),
				},
			}); err != nil {
				t.Fatal(err)
			}

			threadLane := "thread"
			records, err := s.FindRecords(&session.RecordQuery{Lane: &threadLane})
			if err != nil {
				t.Fatal(err)
			}
			deepEqualJSON(t, recordIDs(records), []string{"old-next-run", "old-run"})
			log, err := s.GetLog(nil)
			if err != nil {
				t.Fatal(err)
			}
			var logRecordIDs []string
			for _, item := range log {
				if item.Kind == "record" {
					logRecordIDs = append(logRecordIDs, item.Record.Base().ID)
				}
			}
			deepEqualJSON(t, logRecordIDs, []string{"old-run", "old-next-run"})
			err = s.CreateLane("thread", nil)
			rejectsWithCode(t, err, session.CodeAlreadyExists)
		}),

		createCase("records and log", "persists queue cancellation without consuming its target", func(t *testing.T, repository session.SessionRepo) {
			s, err := repository.Create(&session.SessionCreateOptions{ID: lo.ToPtr("session")})
			if err != nil {
				t.Fatal(err)
			}
			enqueued, err := s.AppendRecord(&session.QueueEnqueuedRecord{
				RecordBase: session.RecordBase{Type: session.RecordTypeQueueEnqueued, ID: "enqueue", Lane: "main"},
				Queue:      session.QueueNextRun,
				Target: &session.MessageEntry{
					EntryBase: session.EntryBase{Type: session.EntryTypeMessage, ID: "queued-message"},
					Message:   createUserMessage("queued"),
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			cancelled, err := s.AppendRecord(&session.QueueCancelledRecord{
				RecordBase: session.RecordBase{Type: session.RecordTypeQueueCancelled, ID: "cancel", Lane: "main"},
				EntryID:    "queued-message",
			})
			if err != nil {
				t.Fatal(err)
			}
			cancelledRec := cancelled.(*session.QueueCancelledRecord)
			if cancelledRec.Seq != 2 || cancelledRec.EntryID != "queued-message" {
				t.Fatalf("cancelled = {seq: %d, entryId: %s}, want {seq: 2, entryId: queued-message}", cancelledRec.Seq, cancelledRec.EntryID)
			}
			if cancelledRec.RunID != nil {
				t.Fatalf("queue_cancelled must not carry runId, got %v", *cancelledRec.RunID)
			}
			entry, err := s.GetEntry("queued-message")
			if err != nil {
				t.Fatal(err)
			}
			if entry != nil {
				t.Fatalf("queued-message must not be consumed, got %+v", entry)
			}
			cancelType := session.RecordTypeQueueCancelled
			cancellations, err := s.FindRecords(&session.RecordQuery{Type: &cancelType})
			if err != nil {
				t.Fatal(err)
			}
			if len(cancellations) == 0 || cancellations[0].(*session.QueueCancelledRecord).EntryID != "queued-message" {
				t.Fatal("cancellations[0] missing queued-message entryId")
			}
			deepEqualJSON(t, cancellations, []session.LaneRecord{cancelled})
			log, err := s.GetLog(nil)
			if err != nil {
				t.Fatal(err)
			}
			deepEqualJSON(t, log, []session.LogItem{
				{Kind: "record", Seq: enqueued.Base().Seq, Record: enqueued},
				{Kind: "record", Seq: cancelled.Base().Seq, Record: cancelled},
			})
		}),

		createCase("records and log", "filters records by lane type run sequence and order", func(t *testing.T, repository session.SessionRepo) {
			s, err := repository.Create(&session.SessionCreateOptions{ID: lo.ToPtr("session")})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.AppendRecord(operationStarted("run-1", "main", session.OperationIntentRun)); err != nil {
				t.Fatal(err)
			}
			if _, err := s.AppendRecord(&session.StepAttemptRecord{
				RecordBase:    session.RecordBase{Type: session.RecordTypeStepAttempt, ID: "attempt-1", Lane: "main"},
				RunID:         "run-1",
				Step:          session.OperationStepAssistant,
				Attempt:       1,
				ResultEntryID: "assistant-1",
			}); err != nil {
				t.Fatal(err)
			}
			err = s.CreateLane("thread", nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.AppendRecord(operationStarted("run-2", "thread", session.OperationIntentRun)); err != nil {
				t.Fatal(err)
			}
			if _, err := s.AppendRecord(&session.StepAttemptRecord{
				RecordBase:    session.RecordBase{Type: session.RecordTypeStepAttempt, ID: "attempt-2", Lane: "thread"},
				RunID:         "run-2",
				Step:          session.OperationStepAssistant,
				Attempt:       1,
				ResultEntryID: "assistant-2",
			}); err != nil {
				t.Fatal(err)
			}

			threadLane := "thread"
			records, err := s.FindRecords(&session.RecordQuery{Lane: &threadLane})
			if err != nil {
				t.Fatal(err)
			}
			deepEqualJSON(t, recordIDs(records), []string{"attempt-2", "run-2"})
			oldestFirst := session.OrderOldestFirst
			stepType := session.RecordTypeStepAttempt
			records, err = s.FindRecords(&session.RecordQuery{Type: &stepType, Order: oldestFirst})
			if err != nil {
				t.Fatal(err)
			}
			deepEqualJSON(t, recordIDs(records), []string{"attempt-1", "attempt-2"})
			after1 := int64(1)
			run1 := "run-1"
			records, err = s.FindRecords(&session.RecordQuery{RunID: &run1, AfterSeq: &after1})
			if err != nil {
				t.Fatal(err)
			}
			deepEqualJSON(t, recordIDs(records), []string{"attempt-1"})
			limit1 := 1
			records, err = s.FindRecords(&session.RecordQuery{Limit: &limit1})
			if err != nil {
				t.Fatal(err)
			}
			deepEqualJSON(t, recordIDs(records), []string{"attempt-2"})
		}),

		createCase("records and log", "filters operation starts by operation kind", func(t *testing.T, repository session.SessionRepo) {
			s, err := repository.Create(&session.SessionCreateOptions{ID: lo.ToPtr("session")})
			if err != nil {
				t.Fatal(err)
			}
			appendFinished := func(startID string, kind session.OperationIntentKind) {
				t.Helper()
				if _, err := s.AppendRecord(operationStarted(startID, "main", kind)); err != nil {
					t.Fatal(err)
				}
				if _, err := s.AppendRecord(&session.OperationFinishedRecord{
					RecordBase: session.RecordBase{Type: session.RecordTypeOperationFinished, ID: startID + "-finished", Lane: "main"},
					RunID:      startID,
					Outcome:    "completed",
				}); err != nil {
					t.Fatal(err)
				}
			}
			appendFinished("run-old", session.OperationIntentRun)
			appendFinished("compaction", session.OperationIntentCompaction)
			appendFinished("navigation", session.OperationIntentNavigation)
			if _, err := s.AppendRecord(operationStarted("run-new", "main", session.OperationIntentRun)); err != nil {
				t.Fatal(err)
			}

			startedType := session.RecordTypeOperationStarted
			runKind := session.OperationIntentRun
			compactionKind := session.OperationIntentCompaction
			navigationKind := session.OperationIntentNavigation
			oldestFirst := session.OrderOldestFirst
			limit1 := 1
			records, err := s.FindRecords(&session.RecordQuery{Type: &startedType, OperationKind: &runKind, Order: oldestFirst})
			if err != nil {
				t.Fatal(err)
			}
			deepEqualJSON(t, recordIDs(records), []string{"run-old", "run-new"})
			records, err = s.FindRecords(&session.RecordQuery{Type: &startedType, OperationKind: &compactionKind})
			if err != nil {
				t.Fatal(err)
			}
			deepEqualJSON(t, recordIDs(records), []string{"compaction"})
			records, err = s.FindRecords(&session.RecordQuery{Type: &startedType, OperationKind: &navigationKind})
			if err != nil {
				t.Fatal(err)
			}
			deepEqualJSON(t, recordIDs(records), []string{"navigation"})
			records, err = s.FindRecords(&session.RecordQuery{Type: &startedType, OperationKind: &runKind, Limit: &limit1})
			if err != nil {
				t.Fatal(err)
			}
			deepEqualJSON(t, recordIDs(records), []string{"run-new"})
		}),

		createCase("records and log", "tracks and enforces one open operation per lane", func(t *testing.T, repository session.SessionRepo) {
			s, err := repository.Create(&session.SessionCreateOptions{ID: lo.ToPtr("session")})
			if err != nil {
				t.Fatal(err)
			}
			limit2 := 2
			ops, err := s.FindOpenOperations("main", &limit2)
			if err != nil {
				t.Fatal(err)
			}
			deepEqualJSON(t, ops, []session.OperationStartedRecord{})

			first, err := s.AppendRecord(operationStarted("first", "main", session.OperationIntentRun))
			if err != nil {
				t.Fatal(err)
			}
			ops, err = s.FindOpenOperations("main", &limit2)
			if err != nil {
				t.Fatal(err)
			}
			deepEqualJSON(t, ops, []session.OperationStartedRecord{*first.(*session.OperationStartedRecord)})
			_, err = s.AppendRecord(operationStarted("second", "main", session.OperationIntentRun))
			rejectsWithCode(t, err, session.CodeStorage)
			ops, err = s.FindOpenOperations("main", &limit2)
			if err != nil {
				t.Fatal(err)
			}
			deepEqualJSON(t, ops, []session.OperationStartedRecord{*first.(*session.OperationStartedRecord)})

			if _, err := s.AppendRecord(&session.OperationFinishedRecord{
				RecordBase: session.RecordBase{Type: session.RecordTypeOperationFinished, ID: "finish-first", Lane: "main"},
				RunID:      first.Base().ID,
				Outcome:    "completed",
			}); err != nil {
				t.Fatal(err)
			}
			ops, err = s.FindOpenOperations("main", &limit2)
			if err != nil {
				t.Fatal(err)
			}
			deepEqualJSON(t, ops, []session.OperationStartedRecord{})
		}),

		createCase("records and log", "does not let an earlier finish close a later start", func(t *testing.T, repository session.SessionRepo) {
			s, err := repository.Create(&session.SessionCreateOptions{ID: lo.ToPtr("session")})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.AppendRecord(&session.OperationFinishedRecord{
				RecordBase: session.RecordBase{Type: session.RecordTypeOperationFinished, ID: "finish-before-start", Lane: "main"},
				RunID:      "run",
				Outcome:    "completed",
			}); err != nil {
				t.Fatal(err)
			}
			started, err := s.AppendRecord(operationStarted("run", "main", session.OperationIntentRun))
			if err != nil {
				t.Fatal(err)
			}
			limit2 := 2
			ops, err := s.FindOpenOperations("main", &limit2)
			if err != nil {
				t.Fatal(err)
			}
			deepEqualJSON(t, ops, []session.OperationStartedRecord{*started.(*session.OperationStartedRecord)})
		}),

		createCase("records and log", "scopes open operations by lane and limit", func(t *testing.T, repository session.SessionRepo) {
			s, err := repository.Create(&session.SessionCreateOptions{ID: lo.ToPtr("session")})
			if err != nil {
				t.Fatal(err)
			}
			err = s.CreateLane("thread", nil)
			if err != nil {
				t.Fatal(err)
			}
			mainRun, err := s.AppendRecord(operationStarted("main-run", "main", session.OperationIntentRun))
			if err != nil {
				t.Fatal(err)
			}
			threadNavigation, err := s.AppendRecord(operationStarted("thread-navigation", "thread", session.OperationIntentNavigation))
			if err != nil {
				t.Fatal(err)
			}

			ops, err := s.FindOpenOperations("main", nil)
			if err != nil {
				t.Fatal(err)
			}
			deepEqualJSON(t, ops, []session.OperationStartedRecord{*mainRun.(*session.OperationStartedRecord)})
			limit1 := 1
			ops, err = s.FindOpenOperations("main", &limit1)
			if err != nil {
				t.Fatal(err)
			}
			deepEqualJSON(t, ops, []session.OperationStartedRecord{*mainRun.(*session.OperationStartedRecord)})
			limit2 := 2
			ops, err = s.FindOpenOperations("thread", &limit2)
			if err != nil {
				t.Fatal(err)
			}
			deepEqualJSON(t, ops, []session.OperationStartedRecord{*threadNavigation.(*session.OperationStartedRecord)})
		}),

		createCase("validation and immutability", "returns immutable open-operation records", func(t *testing.T, repository session.SessionRepo) {
			s, err := repository.Create(&session.SessionCreateOptions{ID: lo.ToPtr("session")})
			if err != nil {
				t.Fatal(err)
			}
			committed, err := s.AppendRecord(operationStarted("run", "main", session.OperationIntentRun))
			if err != nil {
				t.Fatal(err)
			}
			ops, err := s.FindOpenOperations("main", nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(ops) != 1 {
				t.Fatalf("ops length = %d, want 1", len(ops))
			}
			read := ops[0]
			runIntent, ok := read.Intent.(*session.RunIntent)
			if !ok {
				t.Fatal("Expected an open run operation")
			}
			// pi: read.intent.originalPrompt.push(...) must not affect storage.
			runIntent.OriginalPrompt = append(runIntent.OriginalPrompt, createUserMessage("mutated"))

			ops, err = s.FindOpenOperations("main", nil)
			if err != nil {
				t.Fatal(err)
			}
			deepEqualJSON(t, ops, []session.OperationStartedRecord{*committed.(*session.OperationStartedRecord)})
		}),

		createCase("queries and facts", "keeps latest-value facts and computes ledger statistics across lanes", func(t *testing.T, repository session.SessionRepo) {
			s, err := repository.Create(&session.SessionCreateOptions{ID: lo.ToPtr("session")})
			if err != nil {
				t.Fatal(err)
			}
			assistant := createAssistantMessage("answer").(types.AssistantMessage)
			assistant.Usage = createUsageWithTotal(10, 5, 3, 2, 20, 1, 2, 3, 4, 10)
			if _, err := s.AppendEntry(&session.MessageEntry{
				EntryBase: session.EntryBase{Type: session.EntryTypeMessage, ID: "user"},
				Message:   createUserMessage("question"),
			}, "main"); err != nil {
				t.Fatal(err)
			}
			if _, err := s.AppendEntry(&session.MessageEntry{
				EntryBase: session.EntryBase{Type: session.EntryTypeMessage, ID: "assistant"},
				Message:   assistant,
			}, "main"); err != nil {
				t.Fatal(err)
			}
			if _, err := s.AppendRecord(&session.UsageRecord{
				RecordBase: session.RecordBase{Type: session.RecordTypeUsage, ID: "assistant-usage", Lane: "main"},
				Usage:      assistant.Usage,
				Cause:      session.UsageCauseAssistant,
				RunID:      lo.ToPtr("run"),
				EntryID:    lo.ToPtr("assistant"),
				Attempt:    lo.ToPtr(1),
				StopReason: lo.ToPtr(types.StopReasonStop),
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := s.AppendRecord(&session.UsageRecord{
				RecordBase: session.RecordBase{Type: session.RecordTypeUsage, ID: "deferred-usage", Lane: "main"},
				Usage:      zeroUsage(),
				Cause:      session.UsageCauseDeferredFetch,
				RunID:      lo.ToPtr("run"),
				EntryID:    lo.ToPtr("deferred-result"),
				Attempt:    lo.ToPtr(1),
				StopReason: lo.ToPtr(types.StopReasonDeferred),
			}); err != nil {
				t.Fatal(err)
			}
			err = s.CreateLane("thread", lo.ToPtr("assistant"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.AppendRecord(&session.UsageRecord{
				RecordBase: session.RecordBase{Type: session.RecordTypeUsage, ID: "correction", Lane: "thread"},
				Usage: types.Usage{
					Input:       -2,
					TotalTokens: -2,
					Cost:        types.Cost{Input: -0.5, Total: -0.5},
				},
				Cause:   session.UsageCauseAdjustment,
				Details: map[string]any{"reason": "provider correction"},
			}); err != nil {
				t.Fatal(err)
			}
			err = s.SetName(lo.ToPtr("First"))
			if err != nil {
				t.Fatal(err)
			}
			err = s.SetName(lo.ToPtr("Second"))
			if err != nil {
				t.Fatal(err)
			}
			err = s.SetLabel("user", lo.ToPtr("keep"))
			if err != nil {
				t.Fatal(err)
			}
			err = s.SetLabel("user", nil)
			if err != nil {
				t.Fatal(err)
			}
			err = s.SetLabel("missing", lo.ToPtr("checkpoint"))
			rejectsWithCode(t, err, session.CodeNotFound)

			name, err := s.GetName()
			if err != nil {
				t.Fatal(err)
			}
			if name == nil || *name != "Second" {
				t.Fatalf("name = %v, want Second", name)
			}
			label, err := s.GetLabel("user")
			if err != nil {
				t.Fatal(err)
			}
			if label != nil {
				t.Fatalf("label = %v, want nil", *label)
			}
			usageType := session.RecordTypeUsage
			oldestFirst := session.OrderOldestFirst
			usageRecords, err := s.FindRecords(&session.RecordQuery{Type: &usageType, Order: oldestFirst})
			if err != nil {
				t.Fatal(err)
			}
			causes := make([]session.UsageCause, len(usageRecords))
			for i, record := range usageRecords {
				causes[i] = record.(*session.UsageRecord).Cause
			}
			deepEqualJSON(t, causes, []session.UsageCause{session.UsageCauseAssistant, session.UsageCauseDeferredFetch, session.UsageCauseAdjustment})
			var deferredUsage *session.UsageRecord
			for _, record := range usageRecords {
				if r, ok := record.(*session.UsageRecord); ok && r.Cause == session.UsageCauseDeferredFetch {
					deferredUsage = r
				}
			}
			if deferredUsage == nil {
				t.Fatal("Expected deferred_fetch usage record")
			}
			if deferredUsage.StopReason == nil || *deferredUsage.StopReason != types.StopReasonDeferred {
				t.Fatalf("deferredUsage.stopReason = %v, want deferred", deferredUsage.StopReason)
			}
			stats, err := s.GetStats()
			if err != nil {
				t.Fatal(err)
			}
			deepEqualJSON(t, stats, session.SessionStats{
				MessageCount:   2,
				CachedTokens:   3,
				UncachedTokens: 10,
				TotalTokens:    18,
				CostTotal:      9.5,
			})
		}),

		createCase("queries and facts", "clears session names durably", func(t *testing.T, repository session.SessionRepo) {
			s, err := repository.Create(&session.SessionCreateOptions{ID: lo.ToPtr("session")})
			if err != nil {
				t.Fatal(err)
			}
			err = s.SetName(lo.ToPtr("Temporary"))
			if err != nil {
				t.Fatal(err)
			}
			err = s.SetName(nil)
			if err != nil {
				t.Fatal(err)
			}

			name, err := s.GetName()
			if err != nil {
				t.Fatal(err)
			}
			if name != nil {
				t.Fatalf("name = %v, want nil", *name)
			}
			log, err := s.GetLog(nil)
			if err != nil {
				t.Fatal(err)
			}
			deepEqualJSON(t, log, []session.LogItem{
				{Kind: "fact", Seq: 1, Fact: "name", Name: lo.ToPtr("Temporary")},
				{Kind: "fact", Seq: 2, Fact: "name", Name: nil},
			})

			metadata, err := s.GetMetadata()
			if err != nil {
				t.Fatal(err)
			}
			reopened, err := repository.Open(metadata)
			if err != nil {
				t.Fatal(err)
			}
			name, err = reopened.GetName()
			if err != nil {
				t.Fatal(err)
			}
			if name != nil {
				t.Fatalf("reopened name = %v, want nil", *name)
			}
			log, err = reopened.GetLog(nil)
			if err != nil {
				t.Fatal(err)
			}
			deepEqualJSON(t, log, []session.LogItem{
				{Kind: "fact", Seq: 1, Fact: "name", Name: lo.ToPtr("Temporary")},
				{Kind: "fact", Seq: 2, Fact: "name", Name: nil},
			})

			fork, err := repository.Fork(metadata, &session.ForkCreateOptions{SessionCreateOptions: session.SessionCreateOptions{ID: lo.ToPtr("fork")}})
			if err != nil {
				t.Fatal(err)
			}
			name, err = fork.GetName()
			if err != nil {
				t.Fatal(err)
			}
			if name != nil {
				t.Fatalf("fork name = %v, want nil", *name)
			}
		}),

		createCase("validation and immutability", "returns immutable copies from reads", func(t *testing.T, repository session.SessionRepo) {
			s, err := repository.Create(&session.SessionCreateOptions{ID: lo.ToPtr("immutable")})
			if err != nil {
				t.Fatal(err)
			}
			metadata, err := s.GetMetadata()
			if err != nil {
				t.Fatal(err)
			}
			data := map[string]any{"nested": map[string]any{"value": float64(1)}}
			if _, err := s.AppendEntry(&session.CustomEntry{
				EntryBase:  session.EntryBase{Type: session.EntryTypeCustom, ID: "custom"},
				CustomType: "note",
				Data:       data,
			}, "main"); err != nil {
				t.Fatal(err)
			}
			// pi: mutates the caller-side object after append; storage must
			// hold its own copy.
			data["nested"].(map[string]any)["value"] = float64(50)
			read, err := s.GetEntry("custom")
			if err != nil {
				t.Fatal(err)
			}
			if read == nil || read.EntryType() != session.EntryTypeCustom {
				t.Fatal("Expected custom entry")
			}
			read.(*session.CustomEntry).Data.(map[string]any)["nested"].(map[string]any)["value"] = float64(99)
			readMetadata, err := s.GetMetadata()
			if err != nil {
				t.Fatal(err)
			}
			readMetadata.ID = "changed"
			log, err := s.GetLog(nil)
			if err != nil {
				t.Fatal(err)
			}
			if log[0].Kind != "entry" || log[0].Entry.EntryType() != session.EntryTypeCustom {
				t.Fatal("Expected entry log")
			}
			log[0].Entry.(*session.CustomEntry).Data.(map[string]any)["nested"].(map[string]any)["value"] = float64(100)

			currentMetadata, err := s.GetMetadata()
			if err != nil {
				t.Fatal(err)
			}
			deepEqualJSON(t, currentMetadata, metadata)
			current, err := s.GetEntry("custom")
			if err != nil {
				t.Fatal(err)
			}
			deepEqualJSON(t, current, &session.CustomEntry{
				EntryBase:  session.EntryBase{Type: session.EntryTypeCustom, ID: "custom", ParentID: nil, Seq: 1, Timestamp: current.(*session.CustomEntry).Timestamp},
				CustomType: "note",
				Data:       map[string]any{"nested": map[string]any{"value": float64(1)}},
			})
		}),

		createCase("entries and lanes", "validates lane lifecycle and targets", func(t *testing.T, repository session.SessionRepo) {
			s, err := repository.Create(&session.SessionCreateOptions{ID: lo.ToPtr("session")})
			if err != nil {
				t.Fatal(err)
			}
			err = s.CreateLane("main", nil)
			rejectsWithCode(t, err, session.CodeAlreadyExists)
			err = s.CreateLane("thread", lo.ToPtr("missing"))
			rejectsWithCode(t, err, session.CodeNotFound)
			err = s.MoveLane("missing", nil)
			rejectsWithCode(t, err, session.CodeInvalidLane)
		}),

		createCase("entries and lanes", "binds lane views without caching leaves", func(t *testing.T, repository session.SessionRepo) {
			s, err := repository.Create(&session.SessionCreateOptions{ID: lo.ToPtr("session")})
			if err != nil {
				t.Fatal(err)
			}
			root, err := s.AppendMessage(createUserMessage("root"))
			if err != nil {
				t.Fatal(err)
			}
			err = s.CreateLane("thread", &root)
			if err != nil {
				t.Fatal(err)
			}
			thread := s.View("thread")
			mainChild, err := s.AppendMessage(createUserMessage("main"))
			if err != nil {
				t.Fatal(err)
			}
			threadChild, err := thread.AppendMessage(createUserMessage("thread"))
			if err != nil {
				t.Fatal(err)
			}

			leaf, err := s.GetLeafID()
			if err != nil {
				t.Fatal(err)
			}
			if leaf == nil || *leaf != mainChild {
				t.Fatalf("main leaf = %v, want %s", leaf, mainChild)
			}
			threadLeaf, err := thread.GetLeafID()
			if err != nil {
				t.Fatal(err)
			}
			if threadLeaf == nil || *threadLeaf != threadChild {
				t.Fatalf("thread leaf = %v, want %s", threadLeaf, threadChild)
			}
			mainBranch, err := s.FindEntriesOnBranch(&session.BranchQuery{EntryQuery: session.EntryQuery{Order: session.OrderOldestFirst}})
			if err != nil {
				t.Fatal(err)
			}
			deepEqualJSON(t, entryIDs(mainBranch), []string{root, mainChild})
			threadBranch, err := thread.FindEntriesOnBranch(&session.BranchQuery{EntryQuery: session.EntryQuery{Order: session.OrderOldestFirst}})
			if err != nil {
				t.Fatal(err)
			}
			deepEqualJSON(t, entryIDs(threadBranch), []string{root, threadChild})
			empty, err := repository.Create(&session.SessionCreateOptions{ID: lo.ToPtr("empty")})
			if err != nil {
				t.Fatal(err)
			}
			emptyBranch, err := empty.FindEntriesOnBranch(nil)
			if err != nil {
				t.Fatal(err)
			}
			deepEqualJSON(t, entryIDs(emptyBranch), []string{})
		}),

		createCase("entries and lanes", "appends provisioned entries with their existing ids", func(t *testing.T, repository session.SessionRepo) {
			s, err := repository.Create(&session.SessionCreateOptions{ID: lo.ToPtr("session")})
			if err != nil {
				t.Fatal(err)
			}
			entry, err := s.AppendEntry(&session.CustomEntry{
				EntryBase:  session.EntryBase{Type: session.EntryTypeCustom, ID: "provisioned"},
				CustomType: "note",
				Data:       map[string]any{"value": float64(1)},
			}, "main")
			if err != nil {
				t.Fatal(err)
			}
			custom := entry.(*session.CustomEntry)
			if custom.CustomType != "note" {
				t.Fatalf("customType = %s, want note", custom.CustomType)
			}
			if custom.ID != "provisioned" || custom.ParentID != nil || custom.Seq != 1 {
				t.Fatalf("entry = {id: %s, parentId: %v, seq: %d}, want {provisioned, nil, 1}", custom.ID, custom.ParentID, custom.Seq)
			}
			leaf, err := s.GetLeafID()
			if err != nil {
				t.Fatal(err)
			}
			if leaf == nil || *leaf != "provisioned" {
				t.Fatalf("leaf = %v, want provisioned", leaf)
			}
		}),

		createCase("entries and lanes", "persists tool-result termination decisions", func(t *testing.T, repository session.SessionRepo) {
			s, err := repository.Create(&session.SessionCreateOptions{ID: lo.ToPtr("session")})
			if err != nil {
				t.Fatal(err)
			}
			entry, err := s.AppendEntry(&session.MessageEntry{
				EntryBase: session.EntryBase{Type: session.EntryTypeMessage, ID: "tool-result"},
				Message: types.ToolResultMessage{
					Role:       types.RoleToolResult,
					ToolCallID: "call-1",
					ToolName:   "example",
					Content:    []types.Content{types.NewTextContent("done")},
					IsError:    false,
					Timestamp:  1,
				},
				Terminate: lo.ToPtr(true),
			}, "main")
			if err != nil {
				t.Fatal(err)
			}
			if entry.(*session.MessageEntry).Terminate == nil || !*entry.(*session.MessageEntry).Terminate {
				t.Fatal("entry.terminate must be true")
			}
			stored, err := s.GetEntry(entry.Base().ID)
			if err != nil {
				t.Fatal(err)
			}
			if stored == nil || stored.EntryType() != session.EntryTypeMessage {
				t.Fatal("Expected message entry")
			}
			if stored.(*session.MessageEntry).Terminate == nil || !*stored.(*session.MessageEntry).Terminate {
				t.Fatal("stored.terminate must be true")
			}
			entries, err := s.FindEntries(nil)
			if err != nil {
				t.Fatal(err)
			}
			deepEqualJSON(t, entries, []session.Entry{entry})
			log, err := s.GetLog(nil)
			if err != nil {
				t.Fatal(err)
			}
			deepEqualJSON(t, log, []session.LogItem{{Kind: "entry", Seq: entry.Base().Seq, Entry: entry}})
		}),

		createCase("validation and immutability", "rejects non-JSON entries before storage mutation", func(t *testing.T, repository session.SessionRepo) {
			s, err := repository.Create(&session.SessionCreateOptions{ID: lo.ToPtr("session")})
			if err != nil {
				t.Fatal(err)
			}
			cyclic := map[string]any{}
			cyclic["self"] = cyclic

			// pi inputs {value: undefined}, [undefined], 1n, and new Map()
			// have no Go JSON-data-model equivalents (Go nil is JSON null,
			// which is valid); NaN, cycles, and unsupported func values cover
			// the same rejection path.
			invalidInputs := map[string]any{
				"NaN":    map[string]any{"value": math.NaN()},
				"cyclic": cyclic,
				"func":   map[string]any{"value": func() {}},
			}
			for name, data := range invalidInputs {
				_, err := s.AppendCustomEntry("invalid", data)
				if err == nil {
					t.Fatalf("%s input must be rejected", name)
				}
				rejectsWithCode(t, err, session.CodeInvalidPayload)
			}

			leaf, err := s.GetLeafID()
			if err != nil {
				t.Fatal(err)
			}
			if leaf != nil {
				t.Fatalf("leaf = %v, want nil", *leaf)
			}
			entries, err := s.FindEntries(nil)
			if err != nil {
				t.Fatal(err)
			}
			deepEqualJSON(t, entryIDs(entries), []string{})
			log, err := s.GetLog(nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(log) != 0 {
				t.Fatalf("log length = %d, want 0", len(log))
			}
			validID, err := s.AppendCustomEntry("valid", map[string]any{"value": float64(1)})
			if err != nil {
				t.Fatal(err)
			}
			valid, err := s.GetEntry(validID)
			if err != nil {
				t.Fatal(err)
			}
			if valid.Base().Seq != 1 {
				t.Fatalf("valid seq = %d, want 1", valid.Base().Seq)
			}
		}),

		createCase("validation and immutability", "rejects non-JSON records before storage mutation", func(t *testing.T, repository session.SessionRepo) {
			s, err := repository.Create(&session.SessionCreateOptions{ID: lo.ToPtr("session")})
			if err != nil {
				t.Fatal(err)
			}
			// pi also feeds undefined and 1n (BigInt) effectiveArgs; both are
			// unrepresentable in Go's JSON data model.
			_, err = s.AppendRecord(&session.ToolStartedRecord{
				RecordBase:       session.RecordBase{Type: session.RecordTypeToolStarted, ID: "func-record", Lane: "main"},
				RunID:            "run",
				AssistantEntryID: "assistant",
				ToolCallID:       "call",
				ToolName:         "example",
				EffectiveArgs:    map[string]any{"value": func() {}},
				ResultEntryID:    "result",
				Replay:           "never",
			})
			rejectsWithCode(t, err, session.CodeInvalidPayload)

			records, err := s.FindRecords(nil)
			if err != nil {
				t.Fatal(err)
			}
			deepEqualJSON(t, recordIDs(records), []string{})
			log, err := s.GetLog(nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(log) != 0 {
				t.Fatalf("log length = %d, want 0", len(log))
			}
			record, err := s.AppendRecord(operationStarted("valid-record", "main", session.OperationIntentRun))
			if err != nil {
				t.Fatal(err)
			}
			if record.Base().Seq != 1 {
				t.Fatalf("record seq = %d, want 1", record.Base().Seq)
			}
		}),

		createCase("entries and lanes", "linearizes concurrent writes across two lanes", func(t *testing.T, repository session.SessionRepo) {
			s, err := repository.Create(&session.SessionCreateOptions{ID: lo.ToPtr("session")})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.AppendEntry(&session.MessageEntry{
				EntryBase: session.EntryBase{Type: session.EntryTypeMessage, ID: "root"},
				Message:   createUserMessage("root"),
			}, "main"); err != nil {
				t.Fatal(err)
			}
			err = s.CreateLane("thread", lo.ToPtr("root"))
			if err != nil {
				t.Fatal(err)
			}

			writes := []struct {
				id   string
				lane string
			}{
				{"main-1", "main"},
				{"thread-1", "thread"},
				{"main-2", "main"},
				{"thread-2", "thread"},
			}
			// pi also asserts completionOrder == commitOrder, which tests the
			// JS event loop's FIFO microtask resolution — a runtime property
			// with no Go counterpart (goroutine finish order is unordered).
			// The storage-level linearization guarantees below (unique seqs,
			// log lists the writes in seq order, log seqs sorted) are the
			// portable subset.
			var wg sync.WaitGroup
			errs := make([]error, len(writes))
			entries := make([]session.Entry, len(writes))
			for i, w := range writes {
				wg.Add(1)
				go func(i int, w struct {
					id   string
					lane string
				}) {
					defer wg.Done()
					entry, err := s.AppendEntry(&session.CustomEntry{
						EntryBase:  session.EntryBase{Type: session.EntryTypeCustom, ID: w.id},
						CustomType: "note",
					}, w.lane)
					errs[i] = err
					if err != nil {
						return
					}
					entries[i] = entry
				}(i, w)
			}
			wg.Wait()
			for i, err := range errs {
				if err != nil {
					t.Fatalf("write %d (%s) failed: %v", i, writes[i].id, err)
				}
			}

			// Unique sequences across concurrent writes.
			seqs := make(map[int64]struct{})
			for _, entry := range entries {
				seq := entry.Base().Seq
				if _, dup := seqs[seq]; dup {
					t.Fatalf("duplicate seq %d across concurrent writes", seq)
				}
				seqs[seq] = struct{}{}
			}
			// The log must list the concurrent writes in seq order.
			sortedEntries := make([]session.Entry, len(entries))
			copy(sortedEntries, entries)
			sortBySeq(sortedEntries)
			expectedOrder := make([]string, len(sortedEntries))
			for i, entry := range sortedEntries {
				expectedOrder[i] = entry.Base().ID
			}
			log, err := s.GetLog(nil)
			if err != nil {
				t.Fatal(err)
			}
			concurrentIDs := map[string]struct{}{}
			for _, entry := range entries {
				concurrentIDs[entry.Base().ID] = struct{}{}
			}
			var logConcurrentIDs []string
			for _, item := range log {
				if item.Kind == "entry" {
					if _, ok := concurrentIDs[item.Entry.Base().ID]; ok {
						logConcurrentIDs = append(logConcurrentIDs, item.Entry.Base().ID)
					}
				}
			}
			deepEqualJSON(t, logConcurrentIDs, expectedOrder)
			logSeqs := make([]int64, len(log))
			for i, item := range log {
				logSeqs[i] = item.Seq
			}
			deepEqualJSON(t, logSeqs, sortedCopy(logSeqs))
		}),

		createCase("repository and forks", "creates lists and opens sessions", func(t *testing.T, repository session.SessionRepo) {
			s, err := repository.Create(&session.SessionCreateOptions{ID: lo.ToPtr("one")})
			if err != nil {
				t.Fatal(err)
			}
			entryID, err := s.AppendMessage(createUserMessage("persisted"))
			if err != nil {
				t.Fatal(err)
			}
			metadata, err := s.GetMetadata()
			if err != nil {
				t.Fatal(err)
			}

			listed, err := repository.List()
			if err != nil {
				t.Fatal(err)
			}
			if len(listed) != 1 {
				t.Fatalf("listed length = %d, want 1", len(listed))
			}
			if listed[0].ID != metadata.ID {
				t.Fatalf("listed id = %s, want %s", listed[0].ID, metadata.ID)
			}
			if listed[0].CreatedAt != metadata.CreatedAt {
				t.Fatalf("listed createdAt = %d, want %d", listed[0].CreatedAt, metadata.CreatedAt)
			}
			if listed[0].ParentSessionID != nil || metadata.ParentSessionID != nil {
				t.Fatal("parentSessionId must be nil for standalone sessions")
			}
			opened, err := repository.Open(metadata)
			if err != nil {
				t.Fatal(err)
			}
			entries, err := opened.FindEntries(nil)
			if err != nil {
				t.Fatal(err)
			}
			deepEqualJSON(t, entryIDs(entries), []string{entryID})
			_, err = repository.Create(&session.SessionCreateOptions{ID: lo.ToPtr("one")})
			rejectsWithCode(t, err, session.CodeAlreadyExists)
		}),

		createCase("repository and forks", "deletes sessions idempotently", func(t *testing.T, repository session.SessionRepo) {
			s, err := repository.Create(&session.SessionCreateOptions{ID: lo.ToPtr("one")})
			if err != nil {
				t.Fatal(err)
			}
			metadata, err := s.GetMetadata()
			if err != nil {
				t.Fatal(err)
			}

			err = repository.Delete(metadata)
			if err != nil {
				t.Fatal(err)
			}
			_, err = repository.Open(metadata)
			rejectsWithCode(t, err, session.CodeNotFound)
			err = repository.Delete(metadata)
			if err != nil {
				t.Fatal(err)
			}
		}),

		createCase("repository and forks", "forks one branch with selected facts and no records", func(t *testing.T, repository session.SessionRepo) {
			source, err := repository.Create(&session.SessionCreateOptions{ID: lo.ToPtr("source")})
			if err != nil {
				t.Fatal(err)
			}
			root, err := source.AppendMessage(createUserMessage("root"))
			if err != nil {
				t.Fatal(err)
			}
			shared, err := source.AppendMessage(createAssistantMessage("shared"))
			if err != nil {
				t.Fatal(err)
			}
			err = source.CreateLane("thread", &shared)
			if err != nil {
				t.Fatal(err)
			}
			threadChild, err := source.View("thread").AppendMessage(createUserMessage("thread"))
			if err != nil {
				t.Fatal(err)
			}
			mainChild, err := source.AppendMessage(createUserMessage("main"))
			if err != nil {
				t.Fatal(err)
			}
			err = source.SetName(lo.ToPtr("Source"))
			if err != nil {
				t.Fatal(err)
			}
			err = source.SetLabel(shared, lo.ToPtr("copied"))
			if err != nil {
				t.Fatal(err)
			}
			err = source.SetLabel(threadChild, lo.ToPtr("excluded"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := source.AppendRecord(operationStarted("run", "main", session.OperationIntentRun)); err != nil {
				t.Fatal(err)
			}
			if _, err := source.AppendRecord(&session.UsageRecord{
				RecordBase: session.RecordBase{Type: session.RecordTypeUsage, ID: "source-usage", Lane: "main"},
				Usage:      createUsageWithTotal(10, 5, 3, 2, 20, 1, 2, 3, 4, 10),
				Cause:      session.UsageCauseAdjustment,
			}); err != nil {
				t.Fatal(err)
			}

			sourceMetadata, err := source.GetMetadata()
			if err != nil {
				t.Fatal(err)
			}
			fork, err := repository.Fork(sourceMetadata, &session.ForkCreateOptions{
				ForkOptions:          session.ForkOptions{Scope: session.ForkScopeBranch, EntryID: &mainChild, Position: session.ForkPositionAt},
				SessionCreateOptions: session.SessionCreateOptions{ID: lo.ToPtr("branch-fork")},
			})
			if err != nil {
				t.Fatal(err)
			}

			entries, err := fork.FindEntries(&session.EntryQuery{Order: session.OrderOldestFirst})
			if err != nil {
				t.Fatal(err)
			}
			deepEqualJSON(t, entryIDs(entries), []string{root, shared, mainChild})
			lanes, err := fork.GetLanes()
			if err != nil {
				t.Fatal(err)
			}
			deepEqualJSON(t, lanes, []session.LanePointer{{Lane: "main", LeafID: &mainChild}})
			name, err := fork.GetName()
			if err != nil {
				t.Fatal(err)
			}
			if name == nil || *name != "Source" {
				t.Fatalf("fork name = %v, want Source", name)
			}
			label, err := fork.GetLabel(shared)
			if err != nil {
				t.Fatal(err)
			}
			if label == nil || *label != "copied" {
				t.Fatalf("fork label(shared) = %v, want copied", label)
			}
			label, err = fork.GetLabel(threadChild)
			if err != nil {
				t.Fatal(err)
			}
			if label != nil {
				t.Fatalf("fork label(threadChild) = %v, want nil", *label)
			}
			records, err := fork.FindRecords(nil)
			if err != nil {
				t.Fatal(err)
			}
			deepEqualJSON(t, recordIDs(records), []string{})
			stats, err := fork.GetStats()
			if err != nil {
				t.Fatal(err)
			}
			deepEqualJSON(t, stats, session.SessionStats{MessageCount: 3})
			if _, err := fork.AppendMessage(createUserMessage("after fork")); err != nil {
				t.Fatal(err)
			}
			stats, err = fork.GetStats()
			if err != nil {
				t.Fatal(err)
			}
			if stats.MessageCount != 4 {
				t.Fatalf("messageCount = %d, want 4", stats.MessageCount)
			}
			metadata, err := fork.GetMetadata()
			if err != nil {
				t.Fatal(err)
			}
			if metadata.ID != "branch-fork" || metadata.ParentSessionID == nil || *metadata.ParentSessionID != "source" {
				t.Fatalf("fork metadata = {id: %s, parentSessionId: %v}", metadata.ID, metadata.ParentSessionID)
			}
		}),

		createCase("repository and forks", "forks a complete tree with lanes and facts", func(t *testing.T, repository session.SessionRepo) {
			source, err := repository.Create(&session.SessionCreateOptions{ID: lo.ToPtr("source")})
			if err != nil {
				t.Fatal(err)
			}
			root, err := source.AppendMessage(createUserMessage("root"))
			if err != nil {
				t.Fatal(err)
			}
			err = source.CreateLane("thread", &root)
			if err != nil {
				t.Fatal(err)
			}
			mainChild, err := source.AppendMessage(createUserMessage("main"))
			if err != nil {
				t.Fatal(err)
			}
			threadChild, err := source.View("thread").AppendMessage(createUserMessage("thread"))
			if err != nil {
				t.Fatal(err)
			}
			err = source.SetLabel(threadChild, lo.ToPtr("thread-tip"))
			if err != nil {
				t.Fatal(err)
			}

			sourceMetadata, err := source.GetMetadata()
			if err != nil {
				t.Fatal(err)
			}
			fork, err := repository.Fork(sourceMetadata, &session.ForkCreateOptions{
				ForkOptions:          session.ForkOptions{Scope: session.ForkScopeTree},
				SessionCreateOptions: session.SessionCreateOptions{ID: lo.ToPtr("tree-fork")},
			})
			if err != nil {
				t.Fatal(err)
			}
			entries, err := fork.FindEntries(&session.EntryQuery{Order: session.OrderOldestFirst})
			if err != nil {
				t.Fatal(err)
			}
			deepEqualJSON(t, entryIDs(entries), []string{root, mainChild, threadChild})
			lanes, err := fork.GetLanes()
			if err != nil {
				t.Fatal(err)
			}
			deepEqualJSON(t, lanes, []session.LanePointer{
				{Lane: "main", LeafID: &mainChild},
				{Lane: "thread", LeafID: &threadChild},
			})
			label, err := fork.GetLabel(threadChild)
			if err != nil {
				t.Fatal(err)
			}
			if label == nil || *label != "thread-tip" {
				t.Fatalf("fork label = %v, want thread-tip", label)
			}
			stats, err := fork.GetStats()
			if err != nil {
				t.Fatal(err)
			}
			if stats.MessageCount != 3 {
				t.Fatalf("messageCount = %d, want 3", stats.MessageCount)
			}
			log, err := fork.GetLog(nil)
			if err != nil {
				t.Fatal(err)
			}
			var laneItems []session.LogItem
			for _, item := range log {
				if item.Kind == "lane" {
					laneItems = append(laneItems, item)
				}
			}
			deepEqualJSON(t, laneItems, []session.LogItem{
				{Kind: "lane", Seq: 4, Lane: "main", LeafID: &mainChild},
				{Kind: "lane", Seq: 5, Lane: "thread", LeafID: &threadChild},
			})
		}),

		createCase("repository and forks", "forks before an entry without modifying the source", func(t *testing.T, repository session.SessionRepo) {
			source, err := repository.Create(&session.SessionCreateOptions{ID: lo.ToPtr("source")})
			if err != nil {
				t.Fatal(err)
			}
			root, err := source.AppendMessage(createUserMessage("root"))
			if err != nil {
				t.Fatal(err)
			}
			tail, err := source.AppendMessage(createUserMessage("tail"))
			if err != nil {
				t.Fatal(err)
			}
			sourceMetadata, err := source.GetMetadata()
			if err != nil {
				t.Fatal(err)
			}
			fork, err := repository.Fork(sourceMetadata, &session.ForkCreateOptions{
				ForkOptions:          session.ForkOptions{EntryID: &tail},
				SessionCreateOptions: session.SessionCreateOptions{ID: lo.ToPtr("fork")},
			})
			if err != nil {
				t.Fatal(err)
			}
			entries, err := fork.FindEntries(&session.EntryQuery{Order: session.OrderOldestFirst})
			if err != nil {
				t.Fatal(err)
			}
			deepEqualJSON(t, entryIDs(entries), []string{root})
			leaf, err := fork.GetLeafID()
			if err != nil {
				t.Fatal(err)
			}
			if leaf == nil || *leaf != root {
				t.Fatalf("fork leaf = %v, want %s", leaf, root)
			}
			sourceLeaf, err := source.GetLeafID()
			if err != nil {
				t.Fatal(err)
			}
			if sourceLeaf == nil || *sourceLeaf != tail {
				t.Fatalf("source leaf = %v, want %s", sourceLeaf, tail)
			}
			beforeDefaultTarget, err := repository.Fork(sourceMetadata, &session.ForkCreateOptions{
				ForkOptions:          session.ForkOptions{Position: session.ForkPositionBefore},
				SessionCreateOptions: session.SessionCreateOptions{ID: lo.ToPtr("before-default-target")},
			})
			if err != nil {
				t.Fatal(err)
			}
			entries, err = beforeDefaultTarget.FindEntries(&session.EntryQuery{Order: session.OrderOldestFirst})
			if err != nil {
				t.Fatal(err)
			}
			deepEqualJSON(t, entryIDs(entries), []string{root})
			leaf, err = beforeDefaultTarget.GetLeafID()
			if err != nil {
				t.Fatal(err)
			}
			if leaf == nil || *leaf != root {
				t.Fatalf("before leaf = %v, want %s", leaf, root)
			}

			atDefaultTarget, err := repository.Fork(sourceMetadata, &session.ForkCreateOptions{
				ForkOptions:          session.ForkOptions{Position: session.ForkPositionAt},
				SessionCreateOptions: session.SessionCreateOptions{ID: lo.ToPtr("at-default-target")},
			})
			if err != nil {
				t.Fatal(err)
			}
			entries, err = atDefaultTarget.FindEntries(&session.EntryQuery{Order: session.OrderOldestFirst})
			if err != nil {
				t.Fatal(err)
			}
			deepEqualJSON(t, entryIDs(entries), []string{root, tail})
			leaf, err = atDefaultTarget.GetLeafID()
			if err != nil {
				t.Fatal(err)
			}
			if leaf == nil || *leaf != tail {
				t.Fatalf("at leaf = %v, want %s", leaf, tail)
			}
			_, err = repository.Fork(sourceMetadata, &session.ForkCreateOptions{
				ForkOptions:          session.ForkOptions{EntryID: strPtr2("missing")},
				SessionCreateOptions: session.SessionCreateOptions{ID: lo.ToPtr("bad-fork")},
			})
			rejectsWithCode(t, err, session.CodeInvalidForkTarget)
		}),

		createCase("repository and forks", "validates the default fork target", func(t *testing.T, repository session.SessionRepo) {
			source, err := repository.Create(&session.SessionCreateOptions{ID: lo.ToPtr("source-with-custom-leaf")})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := source.AppendCustomEntry("not-a-message", nil); err != nil {
				t.Fatal(err)
			}

			sourceMetadata, err := source.GetMetadata()
			if err != nil {
				t.Fatal(err)
			}
			_, err = repository.Fork(sourceMetadata, &session.ForkCreateOptions{
				SessionCreateOptions: session.SessionCreateOptions{ID: lo.ToPtr("fork")},
			})
			rejectsWithCode(t, err, session.CodeInvalidForkTarget)
		}),
	}
}

// RunConformance runs all cases against the factory (pi: memory.test.ts's
// group/name dispatch loop).
func RunConformance(t *testing.T, cases []SessionBackendConformanceCase, factory SessionBackendFixtureFactory) {
	t.Helper()
	groups := map[string]bool{}
	var groupOrder []string
	for _, c := range cases {
		if !groups[c.Group] {
			groups[c.Group] = true
			groupOrder = append(groupOrder, c.Group)
		}
	}
	for _, group := range groupOrder {
		group := group
		t.Run(group, func(t *testing.T) {
			for _, c := range cases {
				if c.Group != group {
					continue
				}
				c := c
				t.Run(c.Name, func(t *testing.T) {
					repository := factory(t)
					c.Run(t, repository)
				})
			}
		})
	}
}

func sortBySeq(entries []session.Entry) {
	// Insertion sort by seq; entries arrive unsorted from concurrent writes.
	for i := 1; i < len(entries); i++ {
		for j := i; j > 0 && entries[j].Base().Seq < entries[j-1].Base().Seq; j-- {
			entries[j], entries[j-1] = entries[j-1], entries[j]
		}
	}
}

func sortedCopy(values []int64) []int64 {
	out := make([]int64, len(values))
	copy(out, values)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}
