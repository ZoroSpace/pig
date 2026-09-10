// pi: test/harness/session/jsonl-storage.test.ts — per-session jsonl storage
// round trips, concurrency, failure injection, and payload validation.
//
// Deviations:
//   - reopen() resolves the session's JsonlSessionMetadata via repo.List()
//     (pi uses the generic session.getMetadata(), whose jsonl re-widening
//     the Go base interface does not carry).
//   - "rejects non-JSON payloads": the cyclic-object case ports via a
//     self-referencing map (encoding/json rejects cycles); the
//     effectiveArgs-with-undefined case is TS-only (undefined is not a JSON
//     value in Go's data model) and is skipped.
package jsonl_test

import (
	"github.com/samber/lo"
	"sort"
	"sync"
	"testing"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/agent/harness/session"
	jsonl "github.com/ZoroSpace/pig/agent/harness/session/jsonl"
	"github.com/ZoroSpace/pig/ai/types"
)

// userMessage builds a user message (pi: userMessage).
func userMessage(text string) agent.AgentMessage {
	return types.UserMessage{
		Role:      types.RoleUser,
		Content:   types.UserContent{Parts: []types.Content{types.NewTextContent(text)}},
		Timestamp: 1,
	}
}

// storageCreateUsage mirrors the TS storage-test usage builder.
func storageCreateUsage(multiplier float64) types.Usage {
	return types.Usage{
		Input:       int64(multiplier),
		Output:      int64(multiplier * 2),
		CacheRead:   int64(multiplier * 3),
		CacheWrite:  int64(multiplier * 4),
		TotalTokens: int64(multiplier * 10),
		Cost: types.Cost{
			Input:      multiplier * 0.1,
			Output:     multiplier * 0.2,
			CacheRead:  multiplier * 0.3,
			CacheWrite: multiplier * 0.4,
			Total:      multiplier,
		},
	}
}

func TestRoundTripsEveryEntryTypeAndBoundedBranchQueries(t *testing.T) {
	root := t.TempDir()
	repository := createRepository(root)
	s, err := repository.Create(&jsonl.JsonlSessionCreateOptions{
		SessionCreateOptions: session.SessionCreateOptions{ID: lo.ToPtr("entries")},
		Cwd:                  root,
	})
	if err != nil {
		t.Fatal(err)
	}
	var committed []session.Entry
	appendEntry := func(entry session.Entry) {
		t.Helper()
		committedEntry, err := s.AppendEntry(entry, "main")
		if err != nil {
			t.Fatal(err)
		}
		committed = append(committed, committedEntry)
	}

	appendEntry(&session.MessageEntry{
		EntryBase: session.EntryBase{Type: session.EntryTypeMessage, ID: "message"},
		Message:   userMessage("question"),
	})
	appendEntry(&session.MessageEntry{
		EntryBase: session.EntryBase{Type: session.EntryTypeMessage, ID: "assistant-tool-call"},
		Message: types.AssistantMessage{
			Role: types.RoleAssistant,
			Content: []types.Content{
				types.NewTextContent("I'll inspect it."),
				types.NewToolCall("call-1", "read", map[string]any{"path": "README.md"}),
			},
			API:        types.ApiAnthropicMessages,
			Provider:   "anthropic",
			Model:      "claude-sonnet-4-5",
			Usage:      storageCreateUsage(1),
			StopReason: types.StopReasonToolUse,
			Timestamp:  2,
		},
	})
	terminate := true
	appendEntry(&session.MessageEntry{
		EntryBase: session.EntryBase{Type: session.EntryTypeMessage, ID: "tool-result"},
		Message: types.ToolResultMessage{
			Role:       types.RoleToolResult,
			ToolCallID: "call-1",
			ToolName:   "read",
			Content:    []types.Content{types.NewTextContent("contents")},
			Details:    map[string]any{"path": "README.md"},
			Usage:      lo.ToPtr(storageCreateUsage(2)),
			IsError:    false,
			Timestamp:  3,
		},
		Terminate: &terminate,
	})
	appendEntry(&session.ModelChangeEntry{
		EntryBase: session.EntryBase{Type: session.EntryTypeModelChange, ID: "model"},
		Provider:  "anthropic",
		ModelID:   "claude-sonnet-4-5",
	})
	appendEntry(&session.ThinkingLevelEntry{
		EntryBase:     session.EntryBase{Type: session.EntryTypeThinkingLevelChange, ID: "thinking"},
		ThinkingLevel: "high",
	})
	appendEntry(&session.ActiveToolsEntry{
		EntryBase:       session.EntryBase{Type: session.EntryTypeActiveToolsChange, ID: "tools"},
		ActiveToolNames: []string{"read", "bash"},
	})
	appendEntry(&session.CompactionEntry{
		EntryBase:    session.EntryBase{Type: session.EntryTypeCompaction, ID: "compaction"},
		Summary:      "summary",
		RetainedTail: []agent.AgentMessage{userMessage("retained")},
		TokensBefore: 123,
		Details:      map[string]any{"source": "test"},
		Usage:        lo.ToPtr(storageCreateUsage(1)),
	})
	appendEntry(&session.BranchSummaryEntry{
		EntryBase: session.EntryBase{Type: session.EntryTypeBranchSummary, ID: "branch-summary"},
		FromID:    "message",
		Summary:   "branch",
		Details:   map[string]any{"reason": "navigation"},
		Usage:     lo.ToPtr(storageCreateUsage(2)),
	})
	appendEntry(&session.CustomEntry{
		EntryBase:  session.EntryBase{Type: session.EntryTypeCustom, ID: "custom"},
		CustomType: "note",
		Data:       map[string]any{"nested": map[string]any{"value": 1}},
	})

	restored := reopen(t, root, s)
	restoredEntries, err := restored.FindEntries(&session.EntryQuery{Order: session.OrderOldestFirst})
	if err != nil {
		t.Fatal(err)
	}
	assertJSONEqual(t, restoredEntries, committed)

	branchEntries, err := restored.FindEntriesOnBranch(&session.BranchQuery{
		BranchBounds: session.BranchBounds{StopAtType: lo.ToPtr(session.EntryTypeCompaction)},
	})
	if err != nil {
		t.Fatal(err)
	}
	assertJSONEqual(t, entryIDList(branchEntries), []string{"custom", "branch-summary", "compaction"})

	pagedEntries, err := restored.FindEntries(&session.EntryQuery{
		Order:  session.OrderOldestFirst,
		Cursor: &session.EntryCursor{AfterSeq: committed[5].Base().Seq},
		Limit:  lo.ToPtr(2),
	})
	if err != nil {
		t.Fatal(err)
	}
	assertJSONEqual(t, entryIDList(pagedEntries), []string{"compaction", "branch-summary"})

	noteEntries, err := restored.FindEntries(&session.EntryQuery{CustomType: lo.ToPtr("note")})
	if err != nil {
		t.Fatal(err)
	}
	assertJSONEqual(t, entryIDList(noteEntries), []string{"custom"})

	stats, err := restored.GetStats()
	if err != nil {
		t.Fatal(err)
	}
	assertJSONEqual(t, stats, session.SessionStats{MessageCount: 3})

	// Mutating a returned entry must not touch storage state (pi: the
	// structuredClone boundary checks).
	custom, err := restored.GetEntry("custom")
	if err != nil {
		t.Fatal(err)
	}
	customData := custom.(*session.CustomEntry).Data.(map[string]any)
	customData["nested"].(map[string]any)["value"] = 99
	again, err := restored.GetEntry("custom")
	if err != nil {
		t.Fatal(err)
	}
	assertJSONEqual(t, again, committed[len(committed)-1])
	refetched, err := restored.FindEntries(&session.EntryQuery{Order: session.OrderOldestFirst})
	if err != nil {
		t.Fatal(err)
	}
	assertJSONEqual(t, refetched, committed)
}

func TestRoundTripsEveryRecordTypeRecoveryProjectionAndLedgerStatistics(t *testing.T) {
	root := t.TempDir()
	repository := createRepository(root)
	s, err := repository.Create(&jsonl.JsonlSessionCreateOptions{
		SessionCreateOptions: session.SessionCreateOptions{ID: lo.ToPtr("records")},
		Cwd:                  root,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendCustomEntry("anchor", nil); err != nil {
		t.Fatal(err)
	}
	var records []session.LaneRecord
	appendRecord := func(record session.LaneRecord) {
		t.Helper()
		committedRecord, err := s.AppendRecord(record)
		if err != nil {
			t.Fatal(err)
		}
		records = append(records, committedRecord)
	}

	appendRecord(&session.OperationStartedRecord{
		RecordBase:   session.RecordBase{Type: session.RecordTypeOperationStarted, ID: "run", Lane: "main"},
		SourceLeafID: lo.ToPtr("anchor"),
		Intent: &session.RunIntent{
			Kind:           session.OperationIntentRun,
			OriginalPrompt: []agent.AgentMessage{userMessage("prompt")},
			InitialMessages: []session.Entry{&session.MessageEntry{
				EntryBase: session.EntryBase{Type: session.EntryTypeMessage, ID: "initial"},
				Message:   userMessage("initial"),
			}},
			SystemPromptOverride: lo.ToPtr("system"),
			ResumeData:           map[string]any{"extension": map[string]any{"version": 1}},
		},
	})
	appendRecord(&session.QueueEnqueuedRecord{
		RecordBase: session.RecordBase{Type: session.RecordTypeQueueEnqueued, ID: "steer", Lane: "main"},
		Queue:      session.QueueSteer,
		RunID:      lo.ToPtr("run"),
		Target: &session.MessageEntry{
			EntryBase: session.EntryBase{Type: session.EntryTypeMessage, ID: "steer-message"},
			Message:   userMessage("steer"),
		},
	})
	appendRecord(&session.QueueEnqueuedRecord{
		RecordBase: session.RecordBase{Type: session.RecordTypeQueueEnqueued, ID: "follow-up", Lane: "main"},
		Queue:      session.QueueFollowUp,
		RunID:      lo.ToPtr("run"),
		Target: &session.MessageEntry{
			EntryBase: session.EntryBase{Type: session.EntryTypeMessage, ID: "follow-up-message"},
			Message:   userMessage("follow up"),
		},
	})
	appendRecord(&session.StepAttemptRecord{
		RecordBase:    session.RecordBase{Type: session.RecordTypeStepAttempt, ID: "assistant-attempt", Lane: "main"},
		RunID:         "run",
		Step:          session.OperationStepAssistant,
		Attempt:       1,
		ResultEntryID: "assistant-result",
	})
	appendRecord(&session.ToolStartedRecord{
		RecordBase:       session.RecordBase{Type: session.RecordTypeToolStarted, ID: "tool", Lane: "main"},
		RunID:            "run",
		AssistantEntryID: "assistant-result",
		ToolIndex:        0,
		ToolCallID:       "call-1",
		ToolName:         "read",
		EffectiveArgs:    map[string]any{"path": "README.md"},
		ResultEntryID:    "tool-result",
		Replay:           "safe",
	})
	appendRecord(&session.WriteDeferredRecord{
		RecordBase: session.RecordBase{Type: session.RecordTypeWriteDeferred, ID: "deferred-write", Lane: "main"},
		RunID:      "run",
		Target: &session.CustomEntry{
			EntryBase:  session.EntryBase{Type: session.EntryTypeCustom, ID: "deferred-entry"},
			CustomType: "fact",
			Data:       map[string]any{"value": true},
		},
	})
	stop := types.StopReasonStop
	deferred := types.StopReasonDeferred
	appendRecord(&session.UsageRecord{
		RecordBase: session.RecordBase{Type: session.RecordTypeUsage, ID: "assistant-usage", Lane: "main"},
		Usage:      storageCreateUsage(1),
		Cause:      session.UsageCauseAssistant,
		RunID:      lo.ToPtr("run"),
		EntryID:    lo.ToPtr("assistant-result"),
		Attempt:    lo.ToPtr(1),
		StopReason: &stop,
	})
	appendRecord(&session.UsageRecord{
		RecordBase: session.RecordBase{Type: session.RecordTypeUsage, ID: "deferred-usage", Lane: "main"},
		Usage:      storageCreateUsage(2),
		Cause:      session.UsageCauseDeferredFetch,
		RunID:      lo.ToPtr("run"),
		EntryID:    lo.ToPtr("deferred-result"),
		Attempt:    lo.ToPtr(1),
		StopReason: &deferred,
	})
	appendRecord(&session.UsageRecord{
		RecordBase: session.RecordBase{Type: session.RecordTypeUsage, ID: "tool-usage", Lane: "main"},
		Usage:      storageCreateUsage(3),
		Cause:      session.UsageCauseTool,
		RunID:      lo.ToPtr("run"),
		EntryID:    lo.ToPtr("tool-result"),
		ToolCallID: "call-1",
	})
	appendRecord(&session.UsageRecord{
		RecordBase: session.RecordBase{Type: session.RecordTypeUsage, ID: "hook-usage", Lane: "main"},
		Usage:      storageCreateUsage(4),
		Cause:      session.UsageCauseHook,
		EntryID:    lo.ToPtr("hook-result"),
	})
	appendRecord(&session.UsageRecord{
		RecordBase: session.RecordBase{Type: session.RecordTypeUsage, ID: "adjustment", Lane: "main"},
		Usage:      storageCreateUsage(5),
		Cause:      session.UsageCauseAdjustment,
		Details:    map[string]any{"reason": "correction"},
	})
	appendRecord(&session.AbortRequestedRecord{
		RecordBase: session.RecordBase{Type: session.RecordTypeAbortRequested, ID: "abort", Lane: "main"},
		RunID:      "run",
	})
	appendRecord(&session.OperationFinishedRecord{
		RecordBase: session.RecordBase{Type: session.RecordTypeOperationFinished, ID: "run-finished", Lane: "main"},
		RunID:      "run",
		Outcome:    "aborted",
	})
	appendRecord(&session.QueueEnqueuedRecord{
		RecordBase: session.RecordBase{Type: session.RecordTypeQueueEnqueued, ID: "next-run", Lane: "main"},
		Queue:      session.QueueNextRun,
		Target: &session.MessageEntry{
			EntryBase: session.EntryBase{Type: session.EntryTypeMessage, ID: "next-message"},
			Message:   userMessage("next"),
		},
	})
	appendRecord(&session.QueueCancelledRecord{
		RecordBase: session.RecordBase{Type: session.RecordTypeQueueCancelled, ID: "queue-cancelled", Lane: "main"},
		EntryID:    "next-message",
	})
	appendRecord(&session.OperationStartedRecord{
		RecordBase:   session.RecordBase{Type: session.RecordTypeOperationStarted, ID: "compaction", Lane: "main"},
		SourceLeafID: lo.ToPtr("anchor"),
		Intent: &session.CompactionIntent{
			Kind:               session.OperationIntentCompaction,
			CustomInstructions: lo.ToPtr("short"),
			ResultEntryID:      "compaction-result",
		},
	})
	manual := session.CompactionReasonManual
	appendRecord(&session.StepAttemptRecord{
		RecordBase:       session.RecordBase{Type: session.RecordTypeStepAttempt, ID: "compaction-attempt", Lane: "main"},
		RunID:            "compaction",
		Step:             session.OperationStepCompaction,
		Attempt:          1,
		ResultEntryID:    "compaction-result",
		CompactionReason: &manual,
	})
	appendRecord(&session.OperationFinishedRecord{
		RecordBase: session.RecordBase{Type: session.RecordTypeOperationFinished, ID: "compaction-finished", Lane: "main"},
		RunID:      "compaction",
		Outcome:    "completed",
	})
	appendRecord(&session.OperationStartedRecord{
		RecordBase:   session.RecordBase{Type: session.RecordTypeOperationStarted, ID: "navigation", Lane: "main"},
		SourceLeafID: lo.ToPtr("anchor"),
		Intent: &session.NavigationIntent{
			Kind:               session.OperationIntentNavigation,
			TargetID:           nil,
			Summarize:          true,
			CustomInstructions: lo.ToPtr("summarize"),
			Label:              lo.ToPtr("checkpoint"),
			SummaryEntryID:     lo.ToPtr("navigation-summary"),
		},
	})
	appendRecord(&session.StepAttemptRecord{
		RecordBase:    session.RecordBase{Type: session.RecordTypeStepAttempt, ID: "branch-attempt", Lane: "main"},
		RunID:         "navigation",
		Step:          session.OperationStepBranchSummary,
		Attempt:       1,
		ResultEntryID: "navigation-summary",
	})

	restored := reopen(t, root, s)
	restoredRecords, err := restored.FindRecords(&session.RecordQuery{Order: session.OrderOldestFirst})
	if err != nil {
		t.Fatal(err)
	}
	assertJSONEqual(t, restoredRecords, records)

	runRecords, err := restored.FindRecords(&session.RecordQuery{
		Type:          lo.ToPtr(session.RecordTypeOperationStarted),
		OperationKind: lo.ToPtr(session.OperationIntentRun),
		Limit:         lo.ToPtr(1),
	})
	if err != nil {
		t.Fatal(err)
	}
	assertJSONEqual(t, recordIDList(runRecords), []string{"run"})

	compactionRecords, err := restored.FindRecords(&session.RecordQuery{
		RunID: lo.ToPtr("compaction"),
		Order: session.OrderOldestFirst,
	})
	if err != nil {
		t.Fatal(err)
	}
	assertJSONEqual(t, recordIDList(compactionRecords), []string{"compaction", "compaction-attempt", "compaction-finished"})

	usageRecords, err := restored.FindRecords(&session.RecordQuery{
		Type:     lo.ToPtr(session.RecordTypeUsage),
		AfterSeq: lo.ToPtr(records[6].Base().Seq),
		Limit:    lo.ToPtr(2),
	})
	if err != nil {
		t.Fatal(err)
	}
	assertJSONEqual(t, recordIDList(usageRecords), []string{"adjustment", "hook-usage"})

	openOperations, err := restored.FindOpenOperations("main", lo.ToPtr(2))
	if err != nil {
		t.Fatal(err)
	}
	var openIDs []string
	for _, record := range openOperations {
		openIDs = append(openIDs, record.ID)
	}
	assertJSONEqual(t, openIDs, []string{"navigation"})

	stats, err := restored.GetStats()
	if err != nil {
		t.Fatal(err)
	}
	assertJSONEqual(t, stats, session.SessionStats{
		CachedTokens:   45,
		UncachedTokens: 75,
		TotalTokens:    150,
		CostTotal:      15,
	})

	// Mutating the decoded intent must not touch storage state.
	started, err := restored.FindRecords(&session.RecordQuery{
		Type:          lo.ToPtr(session.RecordTypeOperationStarted),
		OperationKind: lo.ToPtr(session.OperationIntentRun),
	})
	if err != nil {
		t.Fatal(err)
	}
	startedRecord := started[0].(*session.OperationStartedRecord)
	startedRecord.Intent.(*session.RunIntent).OriginalPrompt = append(
		startedRecord.Intent.(*session.RunIntent).OriginalPrompt, userMessage("mutated"))
	refetched, err := restored.FindRecords(&session.RecordQuery{Order: session.OrderOldestFirst})
	if err != nil {
		t.Fatal(err)
	}
	assertJSONEqual(t, refetched, records)
}

func TestPersistsConcurrentCrossLaneWritesInSharedSequenceOrder(t *testing.T) {
	root := t.TempDir()
	repository := createRepository(root)
	s, err := repository.Create(&jsonl.JsonlSessionCreateOptions{
		SessionCreateOptions: session.SessionCreateOptions{ID: lo.ToPtr("concurrent")},
		Cwd:                  root,
	})
	if err != nil {
		t.Fatal(err)
	}
	rootCommitted, err := s.AppendEntry(&session.CustomEntry{
		EntryBase:  session.EntryBase{Type: session.EntryTypeCustom, ID: "root"},
		CustomType: "root",
	}, "main")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CreateLane("thread", &rootCommitted.Base().ID); err != nil {
		t.Fatal(err)
	}

	type result struct {
		id  string
		seq int64
	}
	results := make([]result, 4)
	var wg sync.WaitGroup
	specs := []struct {
		id   string
		lane string
	}{
		{"main-1", "main"},
		{"thread-1", "thread"},
		{"main-2", "main"},
		{"thread-2", "thread"},
	}
	for i, spec := range specs {
		wg.Add(1)
		go func(i int, spec struct {
			id   string
			lane string
		}) {
			defer wg.Done()
			entry, err := s.AppendEntry(&session.CustomEntry{
				EntryBase:  session.EntryBase{Type: session.EntryTypeCustom, ID: spec.id},
				CustomType: "note",
			}, spec.lane)
			if err != nil {
				t.Error(err)
				return
			}
			results[i] = result{id: entry.Base().ID, seq: entry.Base().Seq}
		}(i, spec)
	}
	wg.Wait()
	seqs := []int64{results[0].seq, results[1].seq, results[2].seq, results[3].seq}
	idsBySeq := []string{}
	sortedSeqs := append([]int64{}, seqs...)
	sort.Slice(sortedSeqs, func(a, b int) bool { return sortedSeqs[a] < sortedSeqs[b] })
	for _, seq := range sortedSeqs {
		for _, r := range results {
			if r.seq == seq {
				idsBySeq = append(idsBySeq, r.id)
			}
		}
	}

	restored := reopen(t, root, s)
	log, err := restored.GetLog(nil)
	if err != nil {
		t.Fatal(err)
	}
	var concurrent []string
	var logSeqs []int64
	for _, item := range log {
		logSeqs = append(logSeqs, item.Seq)
		if item.Kind == "entry" && item.Entry.Base().ID != "root" {
			concurrent = append(concurrent, item.Entry.Base().ID)
		}
	}
	assertJSONEqual(t, concurrent, idsBySeq)
	uniqueSeqs := map[int64]struct{}{}
	for _, item := range log {
		if item.Kind == "entry" && item.Entry.Base().ID != "root" {
			uniqueSeqs[item.Entry.Base().Seq] = struct{}{}
		}
	}
	if len(uniqueSeqs) != 4 {
		t.Fatalf("unique seqs = %d, want 4", len(uniqueSeqs))
	}
	assertJSONEqual(t, logSeqs, []int64{1, 2, 3, 4, 5, 6})
}

func TestRejectsNonJSONPayloadsWithoutChangingTheDurablePrefix(t *testing.T) {
	root := t.TempDir()
	repository := createRepository(root)
	s, err := repository.Create(&jsonl.JsonlSessionCreateOptions{
		SessionCreateOptions: session.SessionCreateOptions{ID: lo.ToPtr("validation")},
		Cwd:                  root,
	})
	if err != nil {
		t.Fatal(err)
	}
	metadataPath := metadataPathFor(t, repository, "validation")
	prefix := readSessionFile(t, metadataPath)

	cyclic := map[string]any{}
	cyclic["self"] = cyclic
	_, err = s.AppendCustomEntry("invalid", cyclic)
	requireSessionError(t, err, session.CodeInvalidPayload)
	if readSessionFile(t, metadataPath) != prefix {
		t.Fatal("durable prefix changed after rejected payload")
	}

	restored := reopen(t, root, s)
	log, err := restored.GetLog(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(log) != 0 {
		t.Fatalf("log length = %d, want 0", len(log))
	}
	valid, err := restored.AppendEntry(&session.CustomEntry{
		EntryBase:  session.EntryBase{Type: session.EntryTypeCustom, ID: "valid"},
		CustomType: "note",
		Data:       map[string]any{"value": 1},
	}, "main")
	if err != nil {
		t.Fatal(err)
	}
	if valid.Base().Seq != 1 {
		t.Fatalf("valid seq = %d, want 1", valid.Base().Seq)
	}
	verified := reopen(t, root, restored)
	entry, err := verified.GetEntry("valid")
	if err != nil {
		t.Fatal(err)
	}
	assertJSONEqual(t, entry, valid)
}

func TestDoesNotAdvanceStateOrPoisonTheWriteQueueAfterAnAppendFailure(t *testing.T) {
	root := t.TempDir()
	fs := newInjectedFS(root)
	fs.failAppendFile = 1
	repository := repoWithFS(root, fs)
	s, err := repository.Create(&jsonl.JsonlSessionCreateOptions{
		SessionCreateOptions: session.SessionCreateOptions{ID: lo.ToPtr("append-failure")},
		Cwd:                  root,
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := s.AppendCustomEntry("rejected", nil); err == nil {
		t.Fatal("expected storage failure")
	} else {
		requireSessionError(t, err, session.CodeStorage)
	}
	log, err := s.GetLog(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(log) != 0 {
		t.Fatalf("log length = %d, want 0", len(log))
	}
	committed, err := s.AppendEntry(&session.CustomEntry{
		EntryBase:  session.EntryBase{Type: session.EntryTypeCustom, ID: "committed"},
		CustomType: "note",
	}, "main")
	if err != nil {
		t.Fatal(err)
	}
	if committed.Base().Seq != 1 {
		t.Fatalf("committed seq = %d, want 1", committed.Base().Seq)
	}

	reopened := openByID(t, createRepository(root), "append-failure")
	reopenedLog, err := reopened.GetLog(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(reopenedLog) != 1 || reopenedLog[0].Kind != "entry" || reopenedLog[0].Seq != 1 {
		t.Fatalf("reopened log = %+v, want single entry seq 1", reopenedLog)
	}
	assertJSONEqual(t, reopenedLog[0].Entry, committed)
}

// --- shared small helpers used by both storage and repo tests ---

func entryIDList(entries []session.Entry) []string {
	ids := make([]string, len(entries))
	for i, entry := range entries {
		ids[i] = entry.Base().ID
	}
	return ids
}

func recordIDList(records []session.LaneRecord) []string {
	ids := make([]string, len(records))
	for i, record := range records {
		ids[i] = record.Base().ID
	}
	return ids
}

// metadataPathFor lists the repo and returns the session file path for id.
func metadataPathFor(t *testing.T, repository *jsonl.JsonlSessionRepo, id string) string {
	t.Helper()
	listed, err := repository.List(nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := range listed {
		if listed[i].ID == id {
			return listed[i].Path
		}
	}
	t.Fatalf("session %s not found", id)
	return ""
}
