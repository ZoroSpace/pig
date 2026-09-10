// pi: test/harness/reducer.test.ts — record-log validity, durable-prefix
// acceptance, and lane-state reduction.
package reducer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"testing"

	"github.com/samber/lo"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/ai/types"
)

var testUsage = types.Usage{
	Input:       1,
	Output:      1,
	CacheRead:   0,
	CacheWrite:  0,
	TotalTokens: 2,
	Cost:        types.Cost{Input: 0, Output: 0, CacheRead: 0, CacheWrite: 0, Total: 0},
}

func userMessage(text string) types.UserMessage {
	return types.UserMessage{Role: types.RoleUser, Content: types.UserContent{String: text}, Timestamp: 1}
}

func assistantMessage(content []types.Content, stopReason types.StopReason) types.AssistantMessage {
	if content == nil {
		// TS literals always pass an array (even empty); normalize so the
		// JSON shape matches a round-tripped entry ("[]", not null).
		content = []types.Content{}
	}
	message := types.AssistantMessage{
		Role:       types.RoleAssistant,
		Content:    content,
		API:        "openai-responses",
		Provider:   "openai",
		Model:      "test-model",
		Usage:      testUsage,
		StopReason: stopReason,
		Timestamp:  1,
	}
	if stopReason == types.StopReasonDeferred {
		message.Deferred = &types.DeferredHandle{
			Provider: "openai",
			ModelID:  "test-model",
			API:      "openai-responses",
			ID:       "deferred-1",
		}
	}
	return message
}

func toolResultMessage(toolCallID string, toolName string) types.ToolResultMessage {
	return types.ToolResultMessage{
		Role:       types.RoleToolResult,
		ToolCallID: toolCallID,
		ToolName:   toolName,
		Content:    []types.Content{types.NewTextContent("result")},
		Timestamp:  1,
	}
}

func messageTarget(id string, message types.Message) Entry {
	return &MessageEntry{
		EntryBase: EntryBase{Type: EntryTypeMessage, ID: id},
		Message:   message,
	}
}

// persistedEntry clones the provisioned target and fills the storage-assigned
// fields (TS spreads into a new object; the clone keeps shared targets
// unmutated).
func persistedEntry(target Entry, seq int64, parentID *string) Entry {
	entry := cloneEntry(target)
	base := entry.Base()
	base.Seq = seq
	base.ParentID = parentID
	base.Timestamp = seq
	return entry
}

func runStarted(seq int64, id string, initialMessages []Entry) LaneRecord {
	if id == "" {
		id = "run-1"
	}
	return &OperationStartedRecord{
		RecordBase: RecordBase{
			Type: RecordTypeOperationStarted, ID: id, Seq: seq, Lane: "main", Timestamp: seq,
		},
		Intent: &RunIntent{Kind: OperationIntentRun, InitialMessages: initialMessages},
	}
}

func compactionStarted(seq int64, resultEntryID string) LaneRecord {
	return &OperationStartedRecord{
		RecordBase: RecordBase{
			Type: RecordTypeOperationStarted, ID: "compact-1", Seq: seq, Lane: "main", Timestamp: seq,
		},
		SourceLeafID: lo.ToPtr("source"),
		Intent:       &CompactionIntent{Kind: OperationIntentCompaction, ResultEntryID: resultEntryID},
	}
}

func navigationStarted(seq int64, summaryEntryID string) LaneRecord {
	return &OperationStartedRecord{
		RecordBase: RecordBase{
			Type: RecordTypeOperationStarted, ID: "navigate-1", Seq: seq, Lane: "main", Timestamp: seq,
		},
		SourceLeafID: lo.ToPtr("source"),
		Intent: &NavigationIntent{
			Kind:           OperationIntentNavigation,
			TargetID:       lo.ToPtr("target"),
			Summarize:      true,
			SummaryEntryID: lo.ToPtr(summaryEntryID),
		},
	}
}

func attempt(
	seq int64,
	runID string,
	step OperationStep,
	attemptNumber int,
	resultEntryID string,
	compactionReason ...CompactionReason,
) LaneRecord {
	record := &StepAttemptRecord{
		RecordBase: RecordBase{
			Type: RecordTypeStepAttempt, ID: fmt.Sprintf("attempt-%d", seq), Seq: seq, Lane: "main", Timestamp: seq,
		},
		RunID:         runID,
		Step:          step,
		Attempt:       attemptNumber,
		ResultEntryID: resultEntryID,
	}
	if len(compactionReason) > 0 {
		record.CompactionReason = lo.ToPtr(compactionReason[0])
	}
	return record
}

func abortRequested(seq int64, runID string) LaneRecord {
	return &AbortRequestedRecord{
		RecordBase: RecordBase{
			Type: RecordTypeAbortRequested, ID: fmt.Sprintf("abort-%d", seq), Seq: seq, Lane: "main", Timestamp: seq,
		},
		RunID: runID,
	}
}

func operationFinished(seq int64, runID string, outcome string) LaneRecord {
	return &OperationFinishedRecord{
		RecordBase: RecordBase{
			Type: RecordTypeOperationFinished, ID: fmt.Sprintf("finish-%d", seq), Seq: seq, Lane: "main", Timestamp: seq,
		},
		RunID:   runID,
		Outcome: outcome,
	}
}

func toolStarted(seq int64, overrides ...func(*ToolStartedRecord)) LaneRecord {
	record := &ToolStartedRecord{
		RecordBase: RecordBase{
			Type: RecordTypeToolStarted, ID: fmt.Sprintf("tool-start-%d", seq), Seq: seq, Lane: "main", Timestamp: seq,
		},
		RunID:            "run-1",
		AssistantEntryID: "assistant-tools",
		ToolIndex:        0,
		ToolCallID:       "call-1",
		ToolName:         "tool-1",
		EffectiveArgs:    map[string]any{},
		ResultEntryID:    "tool-result-1",
		Replay:           "never",
	}
	for _, apply := range overrides {
		apply(record)
	}
	return record
}

func queueEnqueued(seq int64, target Entry, queue QueueKind) LaneRecord {
	record := &QueueEnqueuedRecord{
		RecordBase: RecordBase{
			Type: RecordTypeQueueEnqueued, ID: fmt.Sprintf("queue-%d", seq), Seq: seq, Lane: "main", Timestamp: seq,
		},
		Queue:  queue,
		Target: target,
	}
	if queue != QueueNextRun {
		record.RunID = lo.ToPtr("run-1")
	}
	return record
}

func queueCancelled(seq int64, entryID string, runID *string) LaneRecord {
	return &QueueCancelledRecord{
		RecordBase: RecordBase{
			Type: RecordTypeQueueCancelled, ID: fmt.Sprintf("cancel-%d", seq), Seq: seq, Lane: "main", Timestamp: seq,
		},
		RunID:   runID,
		EntryID: entryID,
	}
}

func writeDeferred(seq int64, target Entry) LaneRecord {
	return &WriteDeferredRecord{
		RecordBase: RecordBase{
			Type: RecordTypeWriteDeferred, ID: fmt.Sprintf("write-%d", seq), Seq: seq, Lane: "main", Timestamp: seq,
		},
		RunID:  "run-1",
		Target: target,
	}
}

func usageRecord(seq int64, resultEntryID string, stopReason types.StopReason, attemptNumber int) LaneRecord {
	return &UsageRecord{
		RecordBase: RecordBase{
			Type: RecordTypeUsage, ID: fmt.Sprintf("usage-%d", seq), Seq: seq, Lane: "main", Timestamp: seq,
		},
		Cause:      UsageCauseAssistant,
		RunID:      lo.ToPtr("run-1"),
		EntryID:    lo.ToPtr(resultEntryID),
		Attempt:    &attemptNumber,
		StopReason: &stopReason,
		Usage:      testUsage,
	}
}

func compactionEntry(id string, seq int64) Entry {
	return &CompactionEntry{
		EntryBase:    EntryBase{Type: EntryTypeCompaction, ID: id, Seq: seq, Timestamp: seq},
		Summary:      "summary",
		TokensBefore: 10,
	}
}

func branchSummaryEntry(id string, seq int64) Entry {
	return &BranchSummaryEntry{
		EntryBase: EntryBase{Type: EntryTypeBranchSummary, ID: id, ParentID: lo.ToPtr("target"), Seq: seq, Timestamp: seq},
		FromID:    "source",
		Summary:   "summary",
	}
}

func recoverySlice(records []LaneRecord, entries []Entry) *RecordLogSlice {
	finished := make(map[string]bool)
	for _, record := range records {
		if f, ok := record.(*OperationFinishedRecord); ok {
			finished[f.RunID] = true
		}
	}
	var openOperations []OperationStartedRecord
	for _, record := range records {
		if s, ok := record.(*OperationStartedRecord); ok && !finished[s.ID] {
			openOperations = append(openOperations, *s)
		}
	}
	sort.SliceStable(openOperations, func(i, j int) bool { return openOperations[i].Seq > openOperations[j].Seq })
	return &RecordLogSlice{Lane: "main", OpenOperations: openOperations, Records: records, Entries: entries}
}

var testDefaults = EffectiveLaneConfiguration{
	Model:           EffectiveModel{Provider: "default-provider", ModelID: "default-model"},
	ThinkingLevel:   types.ModelThinkingOff,
	ActiveToolNames: []string{"default-tool"},
}

type reductionOptions struct {
	entries              []Entry
	configurationEntries []Entry
	leafID               *string
	defaults             *EffectiveLaneConfiguration
}

func reductionInput(records []LaneRecord, ownEntries []Entry, apply func(*reductionOptions)) *LaneReductionInput {
	options := &reductionOptions{}
	if apply != nil {
		apply(options)
	}
	slice := recoverySlice(records, append(append([]Entry(nil), ownEntries...), options.entries...))
	leafID := options.leafID
	if leafID == nil && len(ownEntries) > 0 {
		leafID = lo.ToPtr(ownEntries[len(ownEntries)-1].Base().ID)
	}
	defaults := testDefaults
	if options.defaults != nil {
		defaults = *options.defaults
	}
	return &LaneReductionInput{
		RecordLogSlice:       *slice,
		LeafID:               leafID,
		OwnEntries:           ownEntries,
		ConfigurationEntries: options.configurationEntries,
		Defaults:             defaults,
	}
}

func expectCorruption(t *testing.T, input *RecordLogSlice, reason RecordLogCorruptionReason) {
	t.Helper()
	err := ValidateRecordLog(input)
	if err == nil {
		t.Fatalf("expected corruption %s", reason)
	}
	corruption, ok := err.(*RecordLogCorruption)
	if !ok {
		t.Fatalf("error type %T, want *RecordLogCorruption", err)
	}
	if corruption.Reason != reason {
		t.Fatalf("corruption reason = %s, want %s", corruption.Reason, reason)
	}
}

func toolCallContent(id string, name string) types.Content {
	return types.ToolCall{Kind: types.KindToolCall, ID: id, Name: name, Arguments: map[string]any{}}
}

func textCallContent() types.Content {
	return types.NewTextContent("done")
}

var assistantToolsEntry = persistedEntry(
	messageTarget("assistant-tools", assistantMessage([]types.Content{toolCallContent("call-1", "tool-1")}, types.StopReasonToolUse)),
	3,
	nil,
)

type corruptionCase struct {
	name   string
	reason RecordLogCorruptionReason
	input  *RecordLogSlice
}

var corruptionCases = []corruptionCase{
	{
		name:   "multiple operations are open",
		reason: CorruptionMultipleOpenOperations,
		input:  recoverySlice([]LaneRecord{runStarted(1, "run-1", nil), runStarted(2, "run-2", nil)}, nil),
	},
	{
		name:   "a record references an operation that does not exist",
		reason: CorruptionUnknownOperation,
		input:  recoverySlice([]LaneRecord{abortRequested(1, "missing")}, nil),
	},
	{
		name:   "a record follows its operation finish",
		reason: CorruptionRecordAfterFinish,
		input:  recoverySlice([]LaneRecord{runStarted(1, "run-1", nil), operationFinished(2, "run-1", "completed"), abortRequested(3, "run-1")}, nil),
	},
	{
		name:   "attempt numbers skip within one assistant step",
		reason: CorruptionNonConsecutiveAttempt,
		input: recoverySlice([]LaneRecord{
			runStarted(1, "run-1", nil),
			attempt(2, "run-1", OperationStepAssistant, 1, "assistant-1"),
			attempt(3, "run-1", OperationStepAssistant, 3, "assistant-2"),
		}, nil),
	},
	{
		name:   "a non-compaction attempt carries compactionReason",
		reason: CorruptionInvalidCompactionReason,
		input: recoverySlice([]LaneRecord{
			runStarted(1, "run-1", nil),
			&StepAttemptRecord{
				RecordBase:       RecordBase{Type: RecordTypeStepAttempt, ID: "attempt-2", Seq: 2, Lane: "main", Timestamp: 2},
				RunID:            "run-1",
				Step:             OperationStepAssistant,
				Attempt:          1,
				ResultEntryID:    "assistant-1",
				CompactionReason: lo.ToPtr(CompactionReasonManual),
			},
		}, nil),
	},
	{
		name:   "a compaction attempt omits compactionReason",
		reason: CorruptionInvalidCompactionReason,
		input: recoverySlice([]LaneRecord{
			runStarted(1, "run-1", nil),
			attempt(2, "run-1", OperationStepCompaction, 1, "compaction-1"),
		}, nil),
	},
	{
		name:   "steering is enqueued after abort",
		reason: CorruptionQueueAfterAbort,
		input: recoverySlice([]LaneRecord{
			runStarted(1, "run-1", nil),
			abortRequested(2, "run-1"),
			queueEnqueued(3, messageTarget("queue-1", userMessage("queued")), QueueSteer),
		}, nil),
	},
	{
		name:   "a queue cancellation has no enqueue",
		reason: CorruptionInvalidQueueCancellation,
		input: recoverySlice([]LaneRecord{
			runStarted(1, "run-1", nil),
			queueCancelled(2, "queue-1", lo.ToPtr("run-1")),
		}, nil),
	},
	{
		name:   "a queue cancellation targets an entry that exists",
		reason: CorruptionInvalidQueueCancellation,
		input: recoverySlice(
			[]LaneRecord{
				runStarted(1, "run-1", nil),
				queueEnqueued(2, messageTarget("queue-1", userMessage("queued")), QueueSteer),
				queueCancelled(4, "queue-1", lo.ToPtr("run-1")),
			},
			[]Entry{persistedEntry(messageTarget("queue-1", userMessage("queued")), 3, nil)},
		),
	},
	{
		name:   "structural attempts disagree on resultEntryId",
		reason: CorruptionInconsistentStep,
		input: recoverySlice([]LaneRecord{
			runStarted(1, "run-1", nil),
			attempt(2, "run-1", OperationStepCompaction, 1, "compaction-1", CompactionReasonThreshold),
			attempt(3, "run-1", OperationStepCompaction, 2, "compaction-2", CompactionReasonThreshold),
		}, nil),
	},
	{
		name:   "structural attempts disagree on compactionReason",
		reason: CorruptionInconsistentStep,
		input: recoverySlice([]LaneRecord{
			runStarted(1, "run-1", nil),
			attempt(2, "run-1", OperationStepCompaction, 1, "compaction-1", CompactionReasonThreshold),
			attempt(3, "run-1", OperationStepCompaction, 2, "compaction-1", CompactionReasonOverflow),
		}, nil),
	},
	{
		name:   "tool_started does not match the assistant tool call",
		reason: CorruptionToolCallMismatch,
		input: recoverySlice(
			[]LaneRecord{
				runStarted(1, "run-1", nil),
				toolStarted(4, func(r *ToolStartedRecord) { r.ToolCallID = "different-call" }),
			},
			[]Entry{assistantToolsEntry},
		),
	},
	{
		name:   "two tool_started records share an invocation identity",
		reason: CorruptionDuplicateToolInvocation,
		input: recoverySlice(
			[]LaneRecord{
				runStarted(1, "run-1", nil),
				toolStarted(4),
				toolStarted(5, func(r *ToolStartedRecord) {
					r.ID = "tool-start-duplicate"
					r.ResultEntryID = "tool-result-2"
				}),
			},
			[]Entry{assistantToolsEntry},
		),
	},
	{
		name:   "a provisioned id exists with different content",
		reason: CorruptionProvisionedEntryMismatch,
		input: recoverySlice(
			[]LaneRecord{runStarted(1, "run-1", []Entry{messageTarget("prompt-1", userMessage("expected"))})},
			[]Entry{persistedEntry(messageTarget("prompt-1", userMessage("different")), 2, nil)},
		),
	},
	{
		name:   "a deferred assistant message has no handle",
		reason: CorruptionInvalidDeferredHandle,
		input: recoverySlice(
			[]LaneRecord{runStarted(1, "run-1", nil)},
			[]Entry{
				persistedEntry(messageTarget("assistant-deferred", deferredMessageWithoutHandle()), 2, nil),
			},
		),
	},
}

func TestRecordLogValidity(t *testing.T) {
	for _, tc := range corruptionCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			expectCorruption(t, tc.input, tc.reason)
		})
	}
}

func TestDoesNotMutateItsBoundedRecoveryInputs(t *testing.T) {
	target := messageTarget("prompt-1", userMessage("hello"))
	start := runStarted(1, "run-1", []Entry{target})
	entry := persistedEntry(target, 2, nil)
	input := &RecordLogSlice{
		Lane:           "main",
		OpenOperations: []OperationStartedRecord{*(start.(*OperationStartedRecord))},
		Records:        []LaneRecord{start},
		Entries:        []Entry{entry},
	}

	if err := ValidateRecordLog(input); err != nil {
		t.Fatal(err)
	}
	if len(input.Records) != 1 || input.Records[0] != start {
		t.Fatalf("input.records changed")
	}
	if len(input.Entries) != 1 || input.Entries[0] != entry {
		t.Fatalf("input.entries changed")
	}
}

type durableAction struct {
	record LaneRecord
	entry  Entry
}

func recordAction(r LaneRecord) durableAction { return durableAction{record: r} }
func entryAction(e Entry) durableAction       { return durableAction{entry: e} }

type prefixCase struct {
	name  string
	input *RecordLogSlice
}

func validPrefixes(trace string, actions []durableAction) []prefixCase {
	var out []prefixCase
	for index := range actions {
		prefix := actions[:index+1]
		var records []LaneRecord
		var entries []Entry
		for _, action := range prefix {
			if action.record != nil {
				records = append(records, action.record)
			}
			if action.entry != nil {
				entries = append(entries, action.entry)
			}
		}
		out = append(out, prefixCase{
			name:  fmt.Sprintf("%s after action %d", trace, index+1),
			input: recoverySlice(records, entries),
		})
	}
	return out
}

var (
	promptTarget         = messageTarget("prompt-1", userMessage("fix the bug"))
	assistantToolTarget  = messageTarget("assistant-tools", assistantMessage([]types.Content{toolCallContent("call-1", "tool-1")}, types.StopReasonToolUse))
	toolResultTarget     = messageTarget("tool-result-1", toolResultMessage("call-1", "tool-1"))
	assistantFinalTarget = messageTarget("assistant-final", assistantMessage([]types.Content{textCallContent()}, types.StopReasonStop))
)

func validPrefixCases() []prefixCase {
	cases := []prefixCase{}
	cases = append(cases, validPrefixes("one-tool run X1-X5", []durableAction{
		recordAction(runStarted(1, "run-1", []Entry{promptTarget})),
		entryAction(persistedEntry(promptTarget, 2, nil)),
		recordAction(attempt(3, "run-1", OperationStepAssistant, 1, "assistant-tools")),
		entryAction(persistedEntry(assistantToolTarget, 4, lo.ToPtr("prompt-1"))),
		recordAction(toolStarted(5)),
		entryAction(persistedEntry(toolResultTarget, 6, lo.ToPtr("assistant-tools"))),
		recordAction(attempt(7, "run-1", OperationStepAssistant, 1, "assistant-final")),
		entryAction(persistedEntry(assistantFinalTarget, 8, lo.ToPtr("tool-result-1"))),
		recordAction(operationFinished(9, "run-1", "completed")),
	})...)
	cases = append(cases, validPrefixes("assistant retry", []durableAction{
		recordAction(runStarted(1, "run-1", nil)),
		recordAction(attempt(2, "run-1", OperationStepAssistant, 1, "assistant-attempt-1")),
		recordAction(usageRecord(3, "assistant-attempt-1", types.StopReasonError, 1)),
		recordAction(attempt(4, "run-1", OperationStepAssistant, 2, "assistant-attempt-2")),
		recordAction(usageRecord(5, "assistant-attempt-2", types.StopReasonStop, 2)),
		entryAction(persistedEntry(
			messageTarget("assistant-attempt-2", assistantMessage([]types.Content{textCallContent()}, types.StopReasonStop)),
			6, nil,
		)),
	})...)
	cases = append(cases, validPrefixes("terminal assistant failure", []durableAction{
		recordAction(runStarted(1, "run-1", nil)),
		recordAction(attempt(2, "run-1", OperationStepAssistant, 1, "assistant-error")),
		entryAction(persistedEntry(
			messageTarget("assistant-error", withErrorMessage(assistantMessage(nil, types.StopReasonError), "failed")),
			3, nil,
		)),
		recordAction(operationFinished(4, "run-1", "failed")),
	})...)
	cases = append(cases, validPrefixes("overflow compaction and retry", []durableAction{
		recordAction(runStarted(1, "run-1", nil)),
		recordAction(attempt(2, "run-1", OperationStepAssistant, 1, "discarded-overflow")),
		recordAction(usageRecord(3, "discarded-overflow", types.StopReasonLength, 1)),
		recordAction(attempt(4, "run-1", OperationStepCompaction, 1, "overflow-compaction", CompactionReasonOverflow)),
		entryAction(compactionEntry("overflow-compaction", 5)),
		recordAction(attempt(6, "run-1", OperationStepAssistant, 1, "assistant-after-compaction")),
		entryAction(persistedEntry(
			messageTarget("assistant-after-compaction", assistantMessage([]types.Content{textCallContent()}, types.StopReasonStop)),
			7, nil,
		)),
	})...)
	cases = append(cases, validPrefixes("steering acceptance and consumption", []durableAction{
		recordAction(runStarted(1, "run-1", nil)),
		recordAction(queueEnqueued(2, messageTarget("queue-1", userMessage("queued")), QueueSteer)),
		entryAction(persistedEntry(messageTarget("queue-1", userMessage("queued")), 3, nil)),
	})...)
	cases = append(cases, validPrefixes("queue cancellation", []durableAction{
		recordAction(runStarted(1, "run-1", nil)),
		recordAction(queueEnqueued(2, messageTarget("queue-1", userMessage("queued")), QueueSteer)),
		recordAction(queueCancelled(3, "queue-1", lo.ToPtr("run-1"))),
	})...)
	cases = append(cases, validPrefixes("deferred write acceptance and application", []durableAction{
		recordAction(runStarted(1, "run-1", nil)),
		recordAction(writeDeferred(2, messageTarget("write-1", userMessage("deferred write")))),
		entryAction(persistedEntry(messageTarget("write-1", userMessage("deferred write")), 3, nil)),
	})...)
	cases = append(cases, validPrefixes("abort during a tool", []durableAction{
		recordAction(runStarted(1, "run-1", nil)),
		recordAction(attempt(2, "run-1", OperationStepAssistant, 1, "assistant-tools")),
		entryAction(persistedEntry(assistantToolTarget, 3, nil)),
		recordAction(toolStarted(4)),
		recordAction(abortRequested(5, "run-1")),
		entryAction(persistedEntry(
			messageTarget("tool-result-1", interruptedToolResult()),
			6, nil,
		)),
	})...)
	cases = append(cases, validPrefixes("threshold auto-compaction", []durableAction{
		recordAction(runStarted(1, "run-1", nil)),
		recordAction(attempt(2, "run-1", OperationStepCompaction, 1, "threshold-compaction", CompactionReasonThreshold)),
		entryAction(compactionEntry("threshold-compaction", 3)),
		recordAction(attempt(4, "run-1", OperationStepAssistant, 1, "assistant-after-threshold")),
	})...)
	cases = append(cases, validPrefixes("manual compaction", []durableAction{
		recordAction(compactionStarted(1, "compaction-1")),
		recordAction(attempt(2, "compact-1", OperationStepCompaction, 1, "compaction-1", CompactionReasonManual)),
		entryAction(compactionEntry("compaction-1", 3)),
		recordAction(operationFinished(4, "compact-1", "completed")),
	})...)
	cases = append(cases, validPrefixes("move-first navigation summary", []durableAction{
		recordAction(navigationStarted(1, "summary-1")),
		recordAction(attempt(2, "navigate-1", OperationStepBranchSummary, 1, "summary-1")),
		entryAction(branchSummaryEntry("summary-1", 3)),
		recordAction(operationFinished(4, "navigate-1", "completed")),
	})...)
	cases = append(cases, validPrefixes("blocked tool without an intent record", []durableAction{
		recordAction(runStarted(1, "run-1", nil)),
		recordAction(attempt(2, "run-1", OperationStepAssistant, 1, "assistant-tools")),
		entryAction(persistedEntry(assistantToolTarget, 3, nil)),
		entryAction(persistedEntry(
			messageTarget("blocked-result", blockedToolResult()),
			4, nil,
		)),
	})...)
	cases = append(cases, validPrefixes("idle next-run cancellation", []durableAction{
		recordAction(queueEnqueued(1, messageTarget("next-1", userMessage("later")), QueueNextRun)),
		recordAction(queueCancelled(2, "next-1", nil)),
	})...)
	cases = append(cases, validPrefixes("next-run enqueue after abort", []durableAction{
		recordAction(runStarted(1, "run-1", nil)),
		recordAction(abortRequested(2, "run-1")),
		recordAction(queueEnqueued(3, messageTarget("next-1", userMessage("later")), QueueNextRun)),
	})...)
	cases = append(cases, validPrefixes("deferred write applied during abort reconciliation", []durableAction{
		recordAction(runStarted(1, "run-1", nil)),
		recordAction(writeDeferred(2, messageTarget("write-1", userMessage("deferred write")))),
		recordAction(abortRequested(3, "run-1")),
		entryAction(persistedEntry(messageTarget("write-1", userMessage("deferred write")), 4, nil)),
	})...)
	cases = append(cases, validPrefixes("accepted steering killed by abort", []durableAction{
		recordAction(runStarted(1, "run-1", nil)),
		recordAction(queueEnqueued(2, messageTarget("queue-1", userMessage("queued")), QueueSteer)),
		recordAction(abortRequested(3, "run-1")),
	})...)
	cases = append(cases, validPrefixes("compaction retry", []durableAction{
		recordAction(runStarted(1, "run-1", nil)),
		recordAction(attempt(2, "run-1", OperationStepCompaction, 1, "threshold-compaction", CompactionReasonThreshold)),
		recordAction(attempt(3, "run-1", OperationStepCompaction, 2, "threshold-compaction", CompactionReasonThreshold)),
		entryAction(compactionEntry("threshold-compaction", 4)),
	})...)
	cases = append(cases, validPrefixes("hook-supplied manual compaction", []durableAction{
		recordAction(compactionStarted(1, "compaction-1")),
		entryAction(compactionEntry("compaction-1", 2)),
		recordAction(operationFinished(3, "compact-1", "completed")),
	})...)
	cases = append(cases, validPrefixes("hook-supplied navigation summary", []durableAction{
		recordAction(navigationStarted(1, "summary-1")),
		entryAction(branchSummaryEntry("summary-1", 2)),
		recordAction(operationFinished(3, "navigate-1", "completed")),
	})...)
	cases = append(cases, validPrefixes("deferred provider suspension and redemption", []durableAction{
		recordAction(runStarted(1, "run-1", nil)),
		recordAction(attempt(2, "run-1", OperationStepAssistant, 1, "assistant-deferred")),
		entryAction(persistedEntry(messageTarget("assistant-deferred", assistantMessage(nil, types.StopReasonDeferred)), 3, nil)),
		entryAction(persistedEntry(
			messageTarget("assistant-redeemed", assistantMessage([]types.Content{textCallContent()}, types.StopReasonStop)),
			4, nil,
		)),
	})...)
	cases = append(cases, validPrefixes("abort of a deferred provider request", []durableAction{
		recordAction(runStarted(1, "run-1", nil)),
		recordAction(attempt(2, "run-1", OperationStepAssistant, 1, "assistant-deferred")),
		entryAction(persistedEntry(messageTarget("assistant-deferred", assistantMessage(nil, types.StopReasonDeferred)), 3, nil)),
		recordAction(abortRequested(4, "run-1")),
	})...)
	return cases
}

func withErrorMessage(message types.AssistantMessage, errorMessage string) types.AssistantMessage {
	message.ErrorMessage = errorMessage
	return message
}

// deferredMessageWithoutHandle is pi's `{...assistantMessage([], "deferred"),
// deferred: undefined}` — a deferred assistant message stripped of its
// handle.
func deferredMessageWithoutHandle() types.AssistantMessage {
	message := assistantMessage(nil, types.StopReasonDeferred)
	message.Deferred = nil
	return message
}

func interruptedToolResult() types.ToolResultMessage {
	result := toolResultMessage("call-1", "tool-1")
	result.Content = []types.Content{types.NewTextContent("interrupted")}
	result.IsError = true
	return result
}

func blockedToolResult() types.ToolResultMessage {
	result := toolResultMessage("call-1", "tool-1")
	result.Content = []types.Content{types.NewTextContent("blocked")}
	result.IsError = true
	return result
}

func TestValidSection6DurablePrefixes(t *testing.T) {
	for _, tc := range validPrefixCases() {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateRecordLog(tc.input); err != nil {
				t.Fatalf("unexpected corruption: %v", err)
			}
		})
	}
}

// entryPayloadMatches compares two entries with pi's deep-equality semantics
// (JSON-canonical payload comparison; storage-assigned fields excluded).
func entryPayloadMatches(t *testing.T, actual Entry, expected Entry) {
	t.Helper()
	if !bytes.Equal(provisionedPayloadJSON(actual), provisionedPayloadJSON(expected)) {
		t.Fatalf("entry mismatch:\nactual   %s\nexpected %s",
			provisionedPayloadJSON(actual), provisionedPayloadJSON(expected))
	}
}

func entryListPayloadMatches(t *testing.T, actual []Entry, expected []Entry) {
	t.Helper()
	if len(actual) != len(expected) {
		t.Fatalf("entry list length = %d, want %d", len(actual), len(expected))
	}
	for i := range expected {
		entryPayloadMatches(t, actual[i], expected[i])
	}
}

func assertStepState(t *testing.T, step *OperationStepState, kind OperationStep, attempts int, resultEntryID string, compactionReason *CompactionReason) {
	t.Helper()
	if step == nil {
		t.Fatalf("step = nil, want %v", kind)
	}
	if step.Kind != kind || step.Attempts != attempts || step.ResultEntryID != resultEntryID {
		t.Fatalf("step = %+v, want %v/%d/%s", step, kind, attempts, resultEntryID)
	}
	if compactionReason == nil {
		if step.CompactionReason != nil {
			t.Fatalf("step.CompactionReason = %v, want nil", *step.CompactionReason)
		}
	} else if step.CompactionReason == nil || *step.CompactionReason != *compactionReason {
		t.Fatalf("step.CompactionReason = %v, want %v", step.CompactionReason, *compactionReason)
	}
}

func TestReducesAnIdleLaneToPendingNextRunInputAndDefaultConfiguration(t *testing.T) {
	pending := messageTarget("next-pending", userMessage("pending"))
	cancelled := messageTarget("next-cancelled", userMessage("cancelled"))
	consumed := messageTarget("next-consumed", userMessage("consumed"))
	input := reductionInput(
		[]LaneRecord{
			queueEnqueued(1, pending, QueueNextRun),
			queueEnqueued(2, cancelled, QueueNextRun),
			queueCancelled(3, "next-cancelled", nil),
			queueEnqueued(4, consumed, QueueNextRun),
		},
		nil,
		func(o *reductionOptions) {
			o.entries = []Entry{persistedEntry(consumed, 5, nil)}
			o.leafID = lo.ToPtr("idle-leaf")
		},
	)

	result, err := ReduceLaneState(input)
	if err != nil {
		t.Fatal(err)
	}
	laneState := result.LaneState
	if laneState.Lane != "main" || laneState.LeafID == nil || *laneState.LeafID != "idle-leaf" {
		t.Fatalf("laneState lane/leaf = %s/%v", laneState.Lane, laneState.LeafID)
	}
	if laneState.Operation != nil {
		t.Fatalf("laneState.Operation = %+v, want nil", laneState.Operation)
	}
	entryListPayloadMatches(t, laneState.PendingNextRun, []Entry{pending})
	if !configurationsEqual(result.EffectiveConfiguration, testDefaults) {
		t.Fatalf("effectiveConfiguration = %+v, want defaults", result.EffectiveConfiguration)
	}
	if result.TerminalFailure != nil {
		t.Fatalf("terminalFailure = %+v, want nil", result.TerminalFailure)
	}
}

func configurationsEqual(a, b EffectiveLaneConfiguration) bool {
	if a.Model != b.Model || a.ThinkingLevel != b.ThinkingLevel {
		return false
	}
	if len(a.ActiveToolNames) != len(b.ActiveToolNames) {
		return false
	}
	for i := range a.ActiveToolNames {
		if a.ActiveToolNames[i] != b.ActiveToolNames[i] {
			return false
		}
	}
	return true
}

func TestFoldsPersistedConfigurationOverCopiedDefaultsInSequence(t *testing.T) {
	configurationEntries := []Entry{
		&ModelChangeEntry{
			EntryBase: EntryBase{Type: EntryTypeModelChange, ID: "model-change", Seq: 1, Timestamp: 1},
			Provider:  "persisted-provider",
			ModelID:   "persisted-model",
		},
		&ThinkingLevelEntry{
			EntryBase:     EntryBase{Type: EntryTypeThinkingLevelChange, ID: "thinking-change", ParentID: lo.ToPtr("model-change"), Seq: 2, Timestamp: 2},
			ThinkingLevel: string(types.ModelThinkingHigh),
		},
		&ActiveToolsEntry{
			EntryBase:       EntryBase{Type: EntryTypeActiveToolsChange, ID: "tools-change", ParentID: lo.ToPtr("thinking-change"), Seq: 3, Timestamp: 3},
			ActiveToolNames: []string{"persisted-tool"},
		},
	}
	input := reductionInput(nil, nil, func(o *reductionOptions) {
		o.configurationEntries = configurationEntries
	})

	result, err := ReduceLaneState(input)
	if err != nil {
		t.Fatal(err)
	}
	want := EffectiveLaneConfiguration{
		Model:           EffectiveModel{Provider: "persisted-provider", ModelID: "persisted-model"},
		ThinkingLevel:   types.ModelThinkingHigh,
		ActiveToolNames: []string{"persisted-tool"},
	}
	if !configurationsEqual(result.EffectiveConfiguration, want) {
		t.Fatalf("effectiveConfiguration = %+v, want %+v", result.EffectiveConfiguration, want)
	}
	if !configurationsEqual(input.Defaults, testDefaults) {
		t.Fatalf("input.Defaults mutated: %+v", input.Defaults)
	}
}

func TestAppliesCommittedOperationOwnedConfigurationAfterTheAnchor(t *testing.T) {
	assistantMessageWithProvider := assistantMessage([]types.Content{textCallContent()}, types.StopReasonStop)
	assistantMessageWithProvider.Provider = "response-provider"
	assistantMessageWithProvider.Model = "response-model"
	assistant := persistedEntry(messageTarget("assistant-config", assistantMessageWithProvider), 2, nil)
	tools := &ActiveToolsEntry{
		EntryBase:       EntryBase{Type: EntryTypeActiveToolsChange, ID: "operation-tools", ParentID: lo.ToPtr("assistant-config"), Seq: 3, Timestamp: 3},
		ActiveToolNames: []string{"operation-tool"},
	}
	result, err := ReduceLaneState(reductionInput([]LaneRecord{runStarted(1, "run-1", nil)}, []Entry{assistant, tools}, nil))
	if err != nil {
		t.Fatal(err)
	}

	want := EffectiveLaneConfiguration{
		Model:           EffectiveModel{Provider: "response-provider", ModelID: "response-model"},
		ThinkingLevel:   types.ModelThinkingOff,
		ActiveToolNames: []string{"operation-tool"},
	}
	if !configurationsEqual(result.EffectiveConfiguration, want) {
		t.Fatalf("effectiveConfiguration = %+v, want %+v", result.EffectiveConfiguration, want)
	}
}

func TestKeepsCapturedNextRunInputWithTheOpenRun(t *testing.T) {
	captured := messageTarget("next-captured", userMessage("captured"))
	later := messageTarget("next-later", userMessage("later"))
	start := runStarted(2, "run-1", []Entry{captured})

	result, err := ReduceLaneState(reductionInput(
		[]LaneRecord{
			queueEnqueued(1, captured, QueueNextRun),
			start,
			queueEnqueued(3, later, QueueNextRun),
		},
		nil, nil,
	))
	if err != nil {
		t.Fatal(err)
	}

	entryListPayloadMatches(t, result.LaneState.PendingNextRun, []Entry{later})
	entryListPayloadMatches(t, result.LaneState.Operation.MissingInitialMessages, []Entry{captured})
}

func TestDerivesMissingInputQueuesDeferredWritesAndUnfinishedAttempt(t *testing.T) {
	missingPrompt := messageTarget("prompt-missing", userMessage("missing"))
	committedPrompt := messageTarget("prompt-committed", userMessage("committed"))
	steer := messageTarget("steer-pending", userMessage("steer"))
	consumedFollowUp := messageTarget("follow-consumed", userMessage("follow"))
	nextRun := messageTarget("next-run", userMessage("next"))
	pendingWrite := messageTarget("write-pending", userMessage("write"))
	appliedWrite := messageTarget("write-applied", userMessage("applied"))
	start := runStarted(1, "run-1", []Entry{missingPrompt, committedPrompt})
	committedPromptEntry := persistedEntry(committedPrompt, 2, nil)
	consumedFollowUpEntry := persistedEntry(consumedFollowUp, 6, lo.ToPtr("prompt-committed"))
	appliedWriteEntry := persistedEntry(appliedWrite, 9, lo.ToPtr("follow-consumed"))
	input := reductionInput(
		[]LaneRecord{
			start,
			queueEnqueued(3, steer, QueueSteer),
			queueEnqueued(4, consumedFollowUp, QueueFollowUp),
			queueEnqueued(5, nextRun, QueueNextRun),
			writeDeferred(7, pendingWrite),
			writeDeferred(8, appliedWrite),
			attempt(10, "run-1", OperationStepAssistant, 1, "assistant-pending"),
		},
		[]Entry{committedPromptEntry, consumedFollowUpEntry, appliedWriteEntry},
		nil,
	)

	result, err := ReduceLaneState(input)
	if err != nil {
		t.Fatal(err)
	}
	entryListPayloadMatches(t, result.LaneState.PendingNextRun, []Entry{nextRun})
	operation := result.LaneState.Operation
	if operation.ID != "run-1" || operation.Kind != OperationIntentRun || operation.Aborting {
		t.Fatalf("operation header = %+v", operation)
	}
	entryListPayloadMatches(t, operation.MissingInitialMessages, []Entry{missingPrompt})
	entryListPayloadMatches(t, operation.PendingSteer, []Entry{steer})
	if len(operation.PendingFollowUp) != 0 {
		t.Fatalf("pendingFollowUp = %v, want empty", operation.PendingFollowUp)
	}
	entryListPayloadMatches(t, operation.PendingWrites, []Entry{pendingWrite})
	assertStepState(t, operation.Step, OperationStepAssistant, 1, "assistant-pending", nil)
	if operation.NewestOwn == nil ||
		operation.NewestOwn.EntryID != "write-applied" ||
		operation.NewestOwn.Type != EntryTypeMessage ||
		operation.NewestOwn.Role == nil || *operation.NewestOwn.Role != types.RoleUser {
		t.Fatalf("newestOwn = %+v, want write-applied/message/user", operation.NewestOwn)
	}
}

func TestKillsSteerAndFollowUpQueuesOnAbort(t *testing.T) {
	steer := messageTarget("steer-aborted", userMessage("steer"))
	followUp := messageTarget("follow-aborted", userMessage("follow"))
	nextRun := messageTarget("next-after-abort", userMessage("next"))
	pendingWrite := messageTarget("write-after-abort", userMessage("write"))
	input := reductionInput([]LaneRecord{
		runStarted(1, "run-1", nil),
		queueEnqueued(2, steer, QueueSteer),
		queueEnqueued(3, followUp, QueueFollowUp),
		queueEnqueued(4, nextRun, QueueNextRun),
		writeDeferred(5, pendingWrite),
		abortRequested(6, "run-1"),
	}, nil, nil)

	result, err := ReduceLaneState(input)
	if err != nil {
		t.Fatal(err)
	}
	entryListPayloadMatches(t, result.LaneState.PendingNextRun, []Entry{nextRun})
	operation := result.LaneState.Operation
	if !operation.Aborting || len(operation.PendingSteer) != 0 || len(operation.PendingFollowUp) != 0 {
		t.Fatalf("abort state = %+v", operation)
	}
	entryListPayloadMatches(t, operation.PendingWrites, []Entry{pendingWrite})
}

func TestReducesUnfinishedSteps(t *testing.T) {
	cases := []struct {
		name   string
		record LaneRecord
		kind   OperationStep
		reason *CompactionReason
	}{
		{
			name:   "assistant",
			record: attempt(2, "run-1", OperationStepAssistant, 1, "result"),
			kind:   OperationStepAssistant,
		},
		{
			name:   "compaction",
			record: attempt(2, "run-1", OperationStepCompaction, 1, "result", CompactionReasonOverflow),
			kind:   OperationStepCompaction,
			reason: lo.ToPtr(CompactionReasonOverflow),
		},
		{
			name:   "branch summary",
			record: attempt(2, "run-1", OperationStepBranchSummary, 1, "result"),
			kind:   OperationStepBranchSummary,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			result, err := ReduceLaneState(reductionInput([]LaneRecord{runStarted(1, "run-1", nil), tc.record}, nil, nil))
			if err != nil {
				t.Fatal(err)
			}
			assertStepState(t, result.LaneState.Operation.Step, tc.kind, 1, "result", tc.reason)
		})
	}
}

func TestClosesTheNewestAttemptOnlyWhenItsProvisionedResultExists(t *testing.T) {
	target := messageTarget("result", assistantMessage([]types.Content{textCallContent()}, types.StopReasonStop))
	result, err := ReduceLaneState(reductionInput(
		[]LaneRecord{runStarted(1, "run-1", nil), attempt(2, "run-1", OperationStepAssistant, 1, "result")},
		[]Entry{persistedEntry(target, 3, nil)},
		nil,
	))
	if err != nil {
		t.Fatal(err)
	}
	if result.LaneState.Operation.Step != nil {
		t.Fatalf("step = %+v, want nil", result.LaneState.Operation.Step)
	}
}

func TestIgnoresUnfulfilledResultIdsFromEarlierAttempts(t *testing.T) {
	target := messageTarget("attempt-2-result", assistantMessage([]types.Content{textCallContent()}, types.StopReasonStop))
	result, err := ReduceLaneState(reductionInput(
		[]LaneRecord{
			runStarted(1, "run-1", nil),
			attempt(2, "run-1", OperationStepAssistant, 1, "attempt-1-result"),
			attempt(3, "run-1", OperationStepAssistant, 2, "attempt-2-result"),
		},
		[]Entry{persistedEntry(target, 4, nil)},
		nil,
	))
	if err != nil {
		t.Fatal(err)
	}
	if result.LaneState.Operation.Step != nil {
		t.Fatalf("step = %+v, want nil", result.LaneState.Operation.Step)
	}
}

func TestReducesToolBatchState(t *testing.T) {
	cases := []struct {
		name      string
		records   func() []LaneRecord
		entries   func() []Entry
		started   bool
		terminate bool
	}{
		{
			name: "X1",
			records: func() []LaneRecord {
				return []LaneRecord{runStarted(1, "run-1", nil), attempt(2, "run-1", OperationStepAssistant, 1, "assistant-tools")}
			},
			entries: func() []Entry { return []Entry{assistantToolsEntry} },
		},
		{
			name: "X3",
			records: func() []LaneRecord {
				return []LaneRecord{
					runStarted(1, "run-1", nil),
					attempt(2, "run-1", OperationStepAssistant, 1, "assistant-tools"),
					toolStarted(4),
				}
			},
			entries: func() []Entry { return []Entry{assistantToolsEntry} },
			started: true,
		},
		{
			name: "X5",
			records: func() []LaneRecord {
				return []LaneRecord{
					runStarted(1, "run-1", nil),
					attempt(2, "run-1", OperationStepAssistant, 1, "assistant-tools"),
					toolStarted(4),
				}
			},
			entries: func() []Entry {
				result := persistedEntry(toolResultTarget, 5, lo.ToPtr("assistant-tools"))
				result.(*MessageEntry).Terminate = lo.ToPtr(true)
				return []Entry{assistantToolsEntry, result}
			},
			started:   true,
			terminate: true,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			reduction, err := ReduceLaneState(reductionInput(tc.records(), tc.entries(), nil))
			if err != nil {
				t.Fatal(err)
			}
			toolBatch := reduction.LaneState.Operation.ToolBatch
			if toolBatch == nil {
				t.Fatal("toolBatch = nil")
			}
			if toolBatch.AssistantEntryID != "assistant-tools" || toolBatch.Truncated || toolBatch.Unresolved == tc.terminate {
				t.Fatalf("toolBatch header = %+v", toolBatch)
			}
			if len(toolBatch.Calls) != 1 {
				t.Fatalf("calls = %+v", toolBatch.Calls)
			}
			call := toolBatch.Calls[0]
			if call.ToolIndex != 0 || call.ToolCall.ID != "call-1" || call.ToolCall.Name != "tool-1" {
				t.Fatalf("call = %+v", call)
			}
			if call.ResultExists != tc.terminate {
				t.Fatalf("call.ResultExists = %v, want %v", call.ResultExists, tc.terminate)
			}
			if call.Terminate != tc.terminate {
				t.Fatalf("call.Terminate = %v, want %v", call.Terminate, tc.terminate)
			}
			if (call.Started != nil) != tc.started {
				t.Fatalf("call.Started presence = %v, want %v", call.Started != nil, tc.started)
			}
		})
	}
}

func TestDoesNotResolveAToolBatchFromADeferredWriteToolResult(t *testing.T) {
	assistant := persistedEntry(assistantToolTarget, 3, nil)
	writtenResult := messageTarget("written-tool-result", toolResultMessage("call-1", "tool-1"))
	result, err := ReduceLaneState(reductionInput(
		[]LaneRecord{
			runStarted(1, "run-1", nil),
			attempt(2, "run-1", OperationStepAssistant, 1, "assistant-tools"),
			writeDeferred(4, writtenResult),
		},
		[]Entry{assistant, persistedEntry(writtenResult, 5, lo.ToPtr("assistant-tools"))},
		nil,
	))
	if err != nil {
		t.Fatal(err)
	}

	call := result.LaneState.Operation.ToolBatch.Calls[0]
	if call.ResultExists {
		t.Fatalf("call.ResultExists = true, want false")
	}
	if !result.LaneState.Operation.ToolBatch.Unresolved {
		t.Fatalf("toolBatch.Unresolved = false, want true")
	}
}

func TestMatchesBlockedResultsWithoutToolStartRecordsAndPreservesSourceOrder(t *testing.T) {
	assistant := persistedEntry(
		messageTarget("assistant-two-tools", assistantMessage(
			[]types.Content{toolCallContent("call-1", "tool-1"), toolCallContent("call-2", "tool-2")},
			types.StopReasonToolUse,
		)),
		3, nil,
	)
	blocked := persistedEntry(messageTarget("blocked-result", blockedToolResult()), 4, lo.ToPtr("assistant-two-tools"))
	secondStart := toolStarted(5, func(r *ToolStartedRecord) {
		r.AssistantEntryID = "assistant-two-tools"
		r.ToolIndex = 1
		r.ToolCallID = "call-2"
		r.ToolName = "tool-2"
		r.ResultEntryID = "call-2-result"
	})
	result, err := ReduceLaneState(reductionInput(
		[]LaneRecord{
			runStarted(1, "run-1", nil),
			attempt(2, "run-1", OperationStepAssistant, 1, "assistant-two-tools"),
			secondStart,
		},
		[]Entry{assistant, blocked},
		nil,
	))
	if err != nil {
		t.Fatal(err)
	}

	calls := result.LaneState.Operation.ToolBatch.Calls
	if len(calls) != 2 {
		t.Fatalf("calls = %+v", calls)
	}
	if calls[0].ToolIndex != 0 || calls[0].ToolCall.ID != "call-1" || !calls[0].ResultExists || calls[0].Started != nil {
		t.Fatalf("calls[0] = %+v", calls[0])
	}
	if calls[1].ToolIndex != 1 || calls[1].ToolCall.ID != "call-2" || calls[1].ResultExists || calls[1].Started == nil {
		t.Fatalf("calls[1] = %+v", calls[1])
	}
	if calls[1].Started.ToolCallID != "call-2" || calls[1].Started.ToolIndex != 1 {
		t.Fatalf("calls[1].Started = %+v", calls[1].Started)
	}
}

func TestMarksALengthStoppedToolBatchAsTruncatedWithoutResolvingIt(t *testing.T) {
	truncated := persistedEntry(
		messageTarget("assistant-truncated", assistantMessage([]types.Content{toolCallContent("call-1", "tool-1")}, types.StopReasonLength)),
		3, nil,
	)
	result, err := ReduceLaneState(reductionInput(
		[]LaneRecord{runStarted(1, "run-1", nil), attempt(2, "run-1", OperationStepAssistant, 1, "assistant-truncated")},
		[]Entry{truncated},
		nil,
	))
	if err != nil {
		t.Fatal(err)
	}
	toolBatch := result.LaneState.Operation.ToolBatch
	if !toolBatch.Truncated || !toolBatch.Unresolved {
		t.Fatalf("toolBatch = %+v, want truncated+unresolved", toolBatch)
	}
}

func TestDetectsAnUnredeemedDeferredHandleOnlyAtTheOperationTail(t *testing.T) {
	deferredMessage := assistantMessage(nil, types.StopReasonDeferred)
	deferredEntry := persistedEntry(messageTarget("assistant-deferred", deferredMessage), 3, nil)
	pending, err := ReduceLaneState(reductionInput(
		[]LaneRecord{runStarted(1, "run-1", nil), attempt(2, "run-1", OperationStepAssistant, 1, "assistant-deferred")},
		[]Entry{deferredEntry},
		nil,
	))
	if err != nil {
		t.Fatal(err)
	}
	if pending.LaneState.Operation.Deferred == nil || *pending.LaneState.Operation.Deferred != *deferredMessage.Deferred {
		t.Fatalf("deferred = %+v, want %+v", pending.LaneState.Operation.Deferred, deferredMessage.Deferred)
	}

	successor := persistedEntry(
		messageTarget("assistant-ready", assistantMessage([]types.Content{textCallContent()}, types.StopReasonStop)),
		4, lo.ToPtr("assistant-deferred"),
	)
	redeemed, err := ReduceLaneState(reductionInput(
		[]LaneRecord{runStarted(1, "run-1", nil), attempt(2, "run-1", OperationStepAssistant, 1, "assistant-deferred")},
		[]Entry{deferredEntry, successor},
		nil,
	))
	if err != nil {
		t.Fatal(err)
	}
	if redeemed.LaneState.Operation.Deferred != nil {
		t.Fatalf("deferred = %+v, want nil", redeemed.LaneState.Operation.Deferred)
	}
}

func TestDerivesTerminalFailureProvenance(t *testing.T) {
	cases := []struct {
		name           string
		records        func() []LaneRecord
		ownEntries     func() []Entry
		expectedSource TerminalFailureSource
	}{
		{
			name: "step",
			records: func() []LaneRecord {
				return []LaneRecord{runStarted(1, "run-1", nil), attempt(2, "run-1", OperationStepAssistant, 1, "assistant-error")}
			},
			ownEntries: func() []Entry {
				return []Entry{persistedEntry(
					messageTarget("assistant-error", withErrorMessage(assistantMessage(nil, types.StopReasonError), "failed")),
					3, nil,
				)}
			},
			expectedSource: TerminalFailureSourceStep,
		},
		{
			name: "deferred fetch",
			records: func() []LaneRecord {
				return []LaneRecord{runStarted(1, "run-1", nil), attempt(2, "run-1", OperationStepAssistant, 1, "assistant-deferred")}
			},
			ownEntries: func() []Entry {
				return []Entry{
					persistedEntry(messageTarget("assistant-deferred", assistantMessage(nil, types.StopReasonDeferred)), 3, nil),
					persistedEntry(
						messageTarget("deferred-error", withErrorMessage(assistantMessage(nil, types.StopReasonError), "expired")),
						4, lo.ToPtr("assistant-deferred"),
					),
				}
			},
			expectedSource: TerminalFailureSourceDeferredFetch,
		},
		{
			name: "deferred fetch usage record",
			records: func() []LaneRecord {
				return []LaneRecord{
					runStarted(1, "run-1", nil),
					&UsageRecord{
						RecordBase: RecordBase{Type: RecordTypeUsage, ID: "deferred-usage", Seq: 3, Lane: "main", Timestamp: 3},
						Cause:      UsageCauseDeferredFetch,
						RunID:      lo.ToPtr("run-1"),
						EntryID:    lo.ToPtr("deferred-error"),
						Attempt:    lo.ToPtr(1),
						StopReason: lo.ToPtr(types.StopReasonError),
						Usage:      testUsage,
					},
				}
			},
			ownEntries: func() []Entry {
				return []Entry{
					persistedEntry(
						messageTarget("deferred-error", withErrorMessage(assistantMessage(nil, types.StopReasonError), "expired")),
						2, nil,
					),
				}
			},
			expectedSource: TerminalFailureSourceDeferredFetch,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			result, err := ReduceLaneState(reductionInput(tc.records(), tc.ownEntries(), nil))
			if err != nil {
				t.Fatal(err)
			}
			if result.TerminalFailure == nil || result.TerminalFailure.Source != tc.expectedSource {
				t.Fatalf("terminalFailure = %+v, want source %s", result.TerminalFailure, tc.expectedSource)
			}
		})
	}
}

func TestDoesNotClassifyAnErrorShapedDeferredWriteAsTerminalFailure(t *testing.T) {
	target := messageTarget("written-error", withErrorMessage(assistantMessage(nil, types.StopReasonError), "note"))
	entry := persistedEntry(target, 3, nil)
	result, err := ReduceLaneState(reductionInput(
		[]LaneRecord{runStarted(1, "run-1", nil), writeDeferred(2, target)},
		[]Entry{entry},
		nil,
	))
	if err != nil {
		t.Fatal(err)
	}
	if result.TerminalFailure != nil {
		t.Fatalf("terminalFailure = %+v, want nil", result.TerminalFailure)
	}
}

func TestDerivesStructuralTargetState(t *testing.T) {
	cases := []struct {
		name     string
		records  func() []LaneRecord
		entries  func() []Entry
		expected OperationTargets
	}{
		{
			name:     "manual compaction result",
			records:  func() []LaneRecord { return []LaneRecord{compactionStarted(1, "compaction-1")} },
			entries:  func() []Entry { return nil },
			expected: OperationTargets{Result: lo.ToPtr(false)},
		},
		{
			name:     "completed manual compaction result",
			records:  func() []LaneRecord { return []LaneRecord{compactionStarted(1, "compaction-1")} },
			entries:  func() []Entry { return []Entry{compactionEntry("compaction-1", 2)} },
			expected: OperationTargets{Result: lo.ToPtr(true)},
		},
		{
			name:     "missing navigation summary",
			records:  func() []LaneRecord { return []LaneRecord{navigationStarted(1, "summary-1")} },
			entries:  func() []Entry { return nil },
			expected: OperationTargets{Summary: lo.ToPtr(false)},
		},
		{
			name:     "navigation summary",
			records:  func() []LaneRecord { return []LaneRecord{navigationStarted(1, "summary-1")} },
			entries:  func() []Entry { return []Entry{branchSummaryEntry("summary-1", 2)} },
			expected: OperationTargets{Summary: lo.ToPtr(true)},
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			result, err := ReduceLaneState(reductionInput(tc.records(), nil, func(o *reductionOptions) {
				o.entries = tc.entries()
			}))
			if err != nil {
				t.Fatal(err)
			}
			targets := result.LaneState.Operation.Targets
			if (targets.Result == nil) != (tc.expected.Result == nil) {
				t.Fatalf("targets.Result = %v, want %v", targets.Result, tc.expected.Result)
			}
			if targets.Result != nil && *targets.Result != *tc.expected.Result {
				t.Fatalf("targets.Result = %v, want %v", *targets.Result, *tc.expected.Result)
			}
			if (targets.Summary == nil) != (tc.expected.Summary == nil) {
				t.Fatalf("targets.Summary = %v, want %v", targets.Summary, tc.expected.Summary)
			}
			if targets.Summary != nil && *targets.Summary != *tc.expected.Summary {
				t.Fatalf("targets.Summary = %v, want %v", *targets.Summary, *tc.expected.Summary)
			}
		})
	}
}

func TestResetsTheOverflowGuardOnlyAfterNewerConversationalInputIsConsumed(t *testing.T) {
	initial := messageTarget("initial", userMessage("initial"))
	steer := messageTarget("steer", userMessage("steer"))
	start := runStarted(1, "run-1", []Entry{initial})
	initialEntry := persistedEntry(initial, 2, nil)
	records := []LaneRecord{
		start,
		attempt(3, "run-1", OperationStepCompaction, 1, "overflow-summary", CompactionReasonOverflow),
		queueEnqueued(5, steer, QueueSteer),
	}

	used, err := ReduceLaneState(reductionInput(records, []Entry{initialEntry}, nil))
	if err != nil {
		t.Fatal(err)
	}
	if !used.LaneState.Operation.OverflowRecoveryUsed {
		t.Fatalf("overflowRecoveryUsed = false, want true")
	}

	reset, err := ReduceLaneState(reductionInput(records, []Entry{initialEntry, persistedEntry(steer, 6, lo.ToPtr("initial"))}, nil))
	if err != nil {
		t.Fatal(err)
	}
	if reset.LaneState.Operation.OverflowRecoveryUsed {
		t.Fatalf("overflowRecoveryUsed = true, want false")
	}
}

func TestIsDeterministicAndDoesNotMutateOrAliasItsInputs(t *testing.T) {
	pending := messageTarget("next", userMessage("next"))
	input := reductionInput([]LaneRecord{queueEnqueued(1, pending, QueueNextRun)}, nil, nil)
	first, err := ReduceLaneState(input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ReduceLaneState(input)
	if err != nil {
		t.Fatal(err)
	}

	firstJSON, err1 := json.Marshal(first)
	secondJSON, err2 := json.Marshal(second)
	if err1 != nil || err2 != nil || !bytes.Equal(firstJSON, secondJSON) {
		t.Fatalf("reduction is not deterministic")
	}

	first.LaneState.PendingNextRun[0].Base().ID = "mutated-output"
	enqueued := input.Records[0].(*QueueEnqueuedRecord)
	if enqueued.Target.Base().ID != "next" {
		t.Fatalf("input record target mutated: %s", enqueued.Target.Base().ID)
	}
}

// agent is referenced via agent.AgentToolCall through the reducer's public
// surface; keep the import honest for the alias check below.
var _ agent.AgentToolCall = types.ToolCall{Kind: types.KindToolCall}
