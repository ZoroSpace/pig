// Package reducer corresponds to pi's packages/agent/src/harness/reducer.ts —
// pure lane-state reconstruction and record-log validation over bounded
// recovery slices.
//
// Package placement deviation: pi's reducer.ts sits at the harness root, but
// pig's package-level dependency direction is reversed relative to pi's
// folder layout — session imports harness (capability types), and the
// reducer needs session's record/entry types, which would cycle if this file
// lived in package harness. Per the utils/event-stream.ts precedent (§3.4),
// the file lands in its own leaf package; the public surface keeps pi's
// names.
//
// Segment deviations:
//   - TS throws RecordLogCorruption; Go validators return *RecordLogCorruption
//     and ReduceLaneState propagates it as its error value.
//   - ProvisionedEntry (TS Omit<Entry, "parentId" | "seq" | "timestamp">)
//     has no Go counterpart (session/types.go note): provisioned entries are
//     Entry values whose storage-assigned fields stay zero. Matching ignores
//     those fields via a JSON-canonical payload comparison (the session
//     conformance suite's deepStrictEqual convention), and structuredClone
//     collapses to JSON round-trip clone helpers on the same convention.
//   - ToolBatchCall.Terminate is a bool standing for pi's `terminate?: true`
//     literal (absent = false; pi only ever sets it to true).
//   - pi's `.at(-1)` / `.at(-2)` over sorted slices → explicit index lookups.
//   - pi's inline `{ provider, modelId }` model shape is named EffectiveModel.
package reducer

import (
	"bytes"
	"encoding/json"
	"math"
	"sort"
	"strconv"

	"github.com/samber/lo"

	"github.com/ZoroSpace/pig/agent"

	"github.com/ZoroSpace/pig/ai/types"
)

// RecordLogCorruptionReason is a machine-readable category for a
// contradiction in a lane's durable recovery slice (pi:
// RecordLogCorruptionReason). These indicate states the single-writer record
// protocol cannot produce, not ordinary operation failures or incomplete-
// but-recoverable intent/result prefixes. Restore must reject such states
// rather than repair or continue it; the accompanying error message supplies
// human-readable detail.
type RecordLogCorruptionReason string

const (
	CorruptionMultipleOpenOperations   RecordLogCorruptionReason = "multiple_open_operations"
	CorruptionUnknownOperation         RecordLogCorruptionReason = "unknown_operation"
	CorruptionRecordAfterFinish        RecordLogCorruptionReason = "record_after_finish"
	CorruptionNonConsecutiveAttempt    RecordLogCorruptionReason = "non_consecutive_attempt"
	CorruptionInvalidCompactionReason  RecordLogCorruptionReason = "invalid_compaction_reason"
	CorruptionQueueAfterAbort          RecordLogCorruptionReason = "queue_after_abort"
	CorruptionInvalidQueueCancellation RecordLogCorruptionReason = "invalid_queue_cancellation"
	CorruptionInconsistentStep         RecordLogCorruptionReason = "inconsistent_step"
	CorruptionToolCallMismatch         RecordLogCorruptionReason = "tool_call_mismatch"
	CorruptionDuplicateToolInvocation  RecordLogCorruptionReason = "duplicate_tool_invocation"
	CorruptionProvisionedEntryMismatch RecordLogCorruptionReason = "provisioned_entry_mismatch"
	CorruptionInvalidDeferredHandle    RecordLogCorruptionReason = "invalid_deferred_handle"
)

// RecordLogCorruption marks a record-log contradiction (pi:
// RecordLogCorruption extends Error).
type RecordLogCorruption struct {
	Reason RecordLogCorruptionReason
	msg    string
}

func (e *RecordLogCorruption) Error() string { return e.msg }

// corrupt builds the corruption error (pi: corrupt throws; Go validators
// return the error value).
func corrupt(reason RecordLogCorruptionReason, message string) *RecordLogCorruption {
	return &RecordLogCorruption{Reason: reason, msg: message}
}

// RecordLogSlice is the bounded recovery slice of one lane (pi:
// RecordLogSlice).
type RecordLogSlice struct {
	Lane string
	// OpenOperations are the lane's unfinished operation_started records.
	OpenOperations []OperationStartedRecord
	Records        []LaneRecord
	// Entries are operation-owned entries plus entries fetched directly by
	// provisioned or referenced ids.
	Entries []Entry
}

// EffectiveModel is the anonymous `{ provider, modelId }` shape of
// EffectiveLaneConfiguration.model.
type EffectiveModel struct {
	Provider string
	ModelID  string
}

// EffectiveLaneConfiguration is the lane's effective model/thinking/tool
// configuration (pi: EffectiveLaneConfiguration).
type EffectiveLaneConfiguration struct {
	Model           EffectiveModel
	ThinkingLevel   agent.ThinkingLevel
	ActiveToolNames []string
}

// TerminalFailureSource discriminates how a terminal failure was produced
// (pi: TerminalFailureState["source"]).
type TerminalFailureSource string

const (
	TerminalFailureSourceStep          TerminalFailureSource = "step"
	TerminalFailureSourceDeferredFetch TerminalFailureSource = "deferred_fetch"
)

// TerminalFailureState describes the failed assistant entry that ended the
// open operation (pi: TerminalFailureState).
type TerminalFailureState struct {
	EntryID string
	Source  TerminalFailureSource
	Message types.AssistantMessage
}

// ToolBatchCall is one tool call of the open operation's pending tool batch
// (pi: the anonymous call shape inside ToolBatchState.calls). Started is nil
// when pi's `started?` is absent; Terminate stands for pi's `terminate?: true`.
type ToolBatchCall struct {
	ToolIndex    int
	ToolCall     agent.AgentToolCall
	Started      *ToolStartedRecord
	ResultExists bool
	Terminate    bool
}

// ToolBatchState is the open operation's in-flight tool batch (pi:
// ToolBatchState).
type ToolBatchState struct {
	AssistantEntryID string
	Calls            []ToolBatchCall
	Truncated        bool
	Unresolved       bool
}

// OperationStepState is the open operation's unfinished step (pi: the
// anonymous step shape inside LaneState.operation). CompactionReason is set
// only when Kind is "compaction" (pi: `compactionReason?`).
type OperationStepState struct {
	Kind             OperationStep
	Attempts         int
	ResultEntryID    string
	CompactionReason *CompactionReason
}

// NewestOwnState summarizes the newest operation-owned entry (pi: the
// anonymous newestOwn shape inside LaneState.operation). Role/StopReason are
// nil when pi's optional members are absent.
type NewestOwnState struct {
	EntryID    string
	Type       EntryType
	Role       *types.Role
	StopReason *types.StopReason
}

// OperationTargets reports whether the operation's structural result entry
// already exists (pi: the anonymous `{ result?, summary? }` shape). A nil
// field is pi's undefined.
type OperationTargets struct {
	Result  *bool
	Summary *bool
}

// LaneOperationState is the open operation's derived orchestration state
// (pi: the anonymous operation shape inside LaneState).
type LaneOperationState struct {
	ID                     string
	Kind                   OperationIntentKind
	Intent                 OperationIntent
	Aborting               bool
	Step                   *OperationStepState
	ToolBatch              *ToolBatchState
	MissingInitialMessages []Entry
	PendingSteer           []Entry
	PendingFollowUp        []Entry
	PendingWrites          []Entry
	Deferred               *types.DeferredHandle
	OverflowRecoveryUsed   bool
	NewestOwn              *NewestOwnState
	Targets                OperationTargets
}

// LaneState is one lane's reduced orchestration state (pi: LaneState).
type LaneState struct {
	Lane           string
	LeafID         *string
	Operation      *LaneOperationState
	PendingNextRun []Entry
}

// LaneReductionInput is the bounded recovery input of one lane (pi:
// LaneReductionInput extends RecordLogSlice; the TS extends clause becomes
// struct embedding).
type LaneReductionInput struct {
	RecordLogSlice
	// LeafID is the lane's current leaf.
	LeafID *string
	// OwnEntries are entries appended by the open operation, oldest first.
	// Empty when idle.
	OwnEntries []Entry
	// ConfigurationEntries are bounded effective-state lookups at the
	// operation anchor or idle leaf, oldest first.
	ConfigurationEntries []Entry
	// Defaults are harness option fallbacks used when no persisted value
	// exists.
	Defaults EffectiveLaneConfiguration
}

// LaneReductionResult is the outcome of one lane reduction (pi:
// LaneReductionResult).
type LaneReductionResult struct {
	LaneState              *LaneState
	EffectiveConfiguration EffectiveLaneConfiguration
	TerminalFailure        *TerminalFailureState
}

// jsonClone deep-copies a JSON-backed value (pi: structuredClone). The
// inputs are always JSONL-round-trippable, so marshal errors are treated as
// unreachable and yield the zero value.
func jsonClone[T any](value T) T {
	raw, err := json.Marshal(value)
	if err != nil {
		var zero T
		return zero
	}
	var out T
	if err := json.Unmarshal(raw, &out); err != nil {
		var zero T
		return zero
	}
	return out
}

// cloneEntry deep-copies an entry through the union dispatch (pi:
// structuredClone on entries).
func cloneEntry(entry Entry) Entry {
	raw, err := json.Marshal(entry)
	if err != nil {
		return nil
	}
	cloned, err := DecodeEntry(raw)
	if err != nil {
		return nil
	}
	return cloned
}

// cloneIntent deep-copies an operation intent through the union dispatch (pi:
// structuredClone).
func cloneIntent(intent OperationIntent) OperationIntent {
	raw, err := json.Marshal(intent)
	if err != nil {
		return nil
	}
	cloned, err := DecodeOperationIntent(raw)
	if err != nil {
		return nil
	}
	return cloned
}

// cloneAssistantMessage deep-copies an assistant message (pi:
// structuredClone; the concrete union type survives the round trip).
func cloneAssistantMessage(message types.AssistantMessage) (types.AssistantMessage, error) {
	raw, err := json.Marshal(message)
	if err != nil {
		return types.AssistantMessage{}, err
	}
	decoded, err := types.DecodeMessage(raw)
	if err != nil {
		return types.AssistantMessage{}, err
	}
	return decoded.(types.AssistantMessage), nil
}

func bySequenceRecords(values []LaneRecord) []LaneRecord {
	out := append([]LaneRecord(nil), values...)
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Base().Seq < out[j].Base().Seq
	})
	return out
}

func bySequenceEntries(values []Entry) []Entry {
	out := append([]Entry(nil), values...)
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Base().Seq < out[j].Base().Seq
	})
	return out
}

func entryValuesByID(m map[string]Entry) []Entry {
	out := make([]Entry, 0, len(m))
	for _, entry := range m {
		out = append(out, entry)
	}
	return out
}

// hasRunID reports whether the record carries a string runId (pi: hasRunId's
// `"runId" in record && typeof record.runId === "string"`). Optional runId
// members (queue_enqueued's nextRun variant, queue_cancelled, usage) map to
// pointer presence.
func hasRunID(record LaneRecord) (string, bool) {
	switch r := record.(type) {
	case *AbortRequestedRecord:
		return r.RunID, true
	case *OperationFinishedRecord:
		return r.RunID, true
	case *StepAttemptRecord:
		return r.RunID, true
	case *ToolStartedRecord:
		return r.RunID, true
	case *WriteDeferredRecord:
		return r.RunID, true
	case *QueueEnqueuedRecord:
		if r.RunID != nil {
			return *r.RunID, true
		}
	case *QueueCancelledRecord:
		if r.RunID != nil {
			return *r.RunID, true
		}
	case *UsageRecord:
		if r.RunID != nil {
			return *r.RunID, true
		}
	}
	return "", false
}

// assistantOf extracts the concrete assistant message from the Message union,
// accepting both value and pointer forms (decoded entries use pointers).
func assistantOf(message types.Message) (types.AssistantMessage, bool) {
	switch m := message.(type) {
	case types.AssistantMessage:
		return m, true
	case *types.AssistantMessage:
		if m != nil {
			return *m, true
		}
	}
	return types.AssistantMessage{}, false
}

// toolResultOf extracts the concrete tool-result message from the Message
// union (see assistantOf).
func toolResultOf(message types.Message) (types.ToolResultMessage, bool) {
	switch m := message.(type) {
	case types.ToolResultMessage:
		return m, true
	case *types.ToolResultMessage:
		if m != nil {
			return *m, true
		}
	}
	return types.ToolResultMessage{}, false
}

// assistantMessageOfEntry returns the assistant message when entry is a
// message entry whose role is "assistant".
func assistantMessageOfEntry(entry Entry) (types.AssistantMessage, bool) {
	m, ok := entry.(*MessageEntry)
	if !ok {
		return types.AssistantMessage{}, false
	}
	assistant, ok := assistantOf(m.Message)
	if !ok || assistant.Role != types.RoleAssistant {
		return types.AssistantMessage{}, false
	}
	return assistant, true
}

// toolResultMessageOfEntry returns the tool-result message when entry is a
// message entry whose role is "toolResult".
func toolResultMessageOfEntry(entry Entry) (types.ToolResultMessage, bool) {
	m, ok := entry.(*MessageEntry)
	if !ok {
		return types.ToolResultMessage{}, false
	}
	result, ok := toolResultOf(m.Message)
	if !ok || result.Role != types.RoleToolResult {
		return types.ToolResultMessage{}, false
	}
	return result, true
}

func isMessageWithRole(entry Entry, role types.Role) bool {
	m, ok := entry.(*MessageEntry)
	if !ok {
		return false
	}
	return m.Message.MessageRole() == role
}

func entryExists(entriesById map[string]Entry, id string) bool {
	_, ok := entriesById[id]
	return ok
}

func toolCallsOf(message types.AssistantMessage) []types.ToolCall {
	var out []types.ToolCall
	for _, content := range message.Content {
		if toolCall, ok := content.(types.ToolCall); ok {
			out = append(out, toolCall)
		}
	}
	return out
}

// provisionedPayloadJSON canonicalizes an entry's payload with the
// storage-assigned fields stripped (pi: matchesProvisionedEntry's destructuring
// rest plus Guard.IsDeepEqual; the JSON normalization is the session
// conformance suite's deep-equality convention). Both sides are first
// normalized through the union dispatch: pi compares two in-memory objects,
// while pig may pair an in-memory construct (nil slice → "content": null)
// with a JSONL-decoded entry (null → empty slice); one decode round trip
// erases that JSON-shape drift before comparison.
func provisionedPayloadJSON(entry Entry) []byte {
	if normalized := cloneEntry(entry); normalized != nil {
		entry = normalized
	}
	raw, err := json.Marshal(entry)
	if err != nil {
		return nil
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil
	}
	delete(payload, "parentId")
	delete(payload, "seq")
	delete(payload, "timestamp")
	canonical, err := json.Marshal(payload)
	if err != nil {
		return nil
	}
	return canonical
}

func matchesProvisionedEntry(entry Entry, target Entry) bool {
	return bytes.Equal(provisionedPayloadJSON(entry), provisionedPayloadJSON(target))
}

func validateExactProvisionedEntry(entriesById map[string]Entry, target Entry) error {
	if entry, ok := entriesById[target.Base().ID]; ok && !matchesProvisionedEntry(entry, target) {
		return corrupt(CorruptionProvisionedEntryMismatch,
			"Provisioned entry "+target.Base().ID+" exists with content different from its intent")
	}
	return nil
}

func validateResultEntry(
	entriesById map[string]Entry,
	resultEntryID string,
	matches func(Entry) bool,
	description string,
) error {
	if entry, ok := entriesById[resultEntryID]; ok && !matches(entry) {
		return corrupt(CorruptionProvisionedEntryMismatch,
			"Provisioned "+description+" entry "+resultEntryID+" exists with different content")
	}
	return nil
}

func validateAttemptReason(record *StepAttemptRecord) error {
	if record.Step == OperationStepCompaction {
		if record.CompactionReason == nil ||
			(*record.CompactionReason != CompactionReasonManual &&
				*record.CompactionReason != CompactionReasonThreshold &&
				*record.CompactionReason != CompactionReasonOverflow) {
			return corrupt(CorruptionInvalidCompactionReason,
				"Compaction attempt "+record.ID+" has no valid compaction reason")
		}
	} else if record.CompactionReason != nil {
		return corrupt(CorruptionInvalidCompactionReason,
			string(record.Step)+" attempt "+record.ID+" has a compaction reason")
	}
	return nil
}

// compactionReasonsEqual compares pi's possibly-undefined compactionReason
// values (pointer-nil = undefined).
func compactionReasonsEqual(a, b *CompactionReason) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func validateAttemptSequence(
	record *StepAttemptRecord,
	previous *StepAttemptRecord,
	entriesById map[string]Entry,
) error {
	var previousResult Entry
	if previous != nil {
		previousResult = entriesById[previous.ResultEntryID]
	}
	continuesSeries := previous != nil &&
		previous.Step == record.Step &&
		(previousResult == nil || previousResult.Base().Seq >= record.Base().Seq)
	expectedAttempt := 1
	if continuesSeries {
		expectedAttempt = previous.Attempt + 1
	}
	if record.Attempt != expectedAttempt {
		return corrupt(CorruptionNonConsecutiveAttempt,
			string(record.Step)+" attempt "+record.ID+" is "+strconv.Itoa(record.Attempt)+
				"; expected "+strconv.Itoa(expectedAttempt))
	}
	if !continuesSeries || record.Step == OperationStepAssistant || previous == nil {
		return nil
	}
	if record.ResultEntryID != previous.ResultEntryID {
		return corrupt(CorruptionInconsistentStep,
			string(record.Step)+" attempts disagree on their result entry id")
	}
	if !compactionReasonsEqual(record.CompactionReason, previous.CompactionReason) {
		return corrupt(CorruptionInconsistentStep,
			string(record.Step)+" attempts disagree on their compaction reason")
	}
	return nil
}

func validateAttemptResult(entriesById map[string]Entry, record *StepAttemptRecord) error {
	switch record.Step {
	case OperationStepAssistant:
		return validateResultEntry(entriesById, record.ResultEntryID,
			func(entry Entry) bool { return isMessageWithRole(entry, types.RoleAssistant) },
			"assistant result")
	case OperationStepCompaction:
		return validateResultEntry(entriesById, record.ResultEntryID,
			func(entry Entry) bool { return entry.EntryType() == EntryTypeCompaction },
			"compaction result")
	case OperationStepBranchSummary:
		return validateResultEntry(entriesById, record.ResultEntryID,
			func(entry Entry) bool { return entry.EntryType() == EntryTypeBranchSummary },
			"branch-summary result")
	}
	return nil
}

func validateToolStart(
	record *ToolStartedRecord,
	entriesById map[string]Entry,
	invocations map[string]bool,
) error {
	invocation := record.AssistantEntryID + "\x00" + strconv.Itoa(record.ToolIndex)
	if invocations[invocation] {
		return corrupt(CorruptionDuplicateToolInvocation,
			"Tool invocation "+record.AssistantEntryID+":"+strconv.Itoa(record.ToolIndex)+" is duplicated")
	}
	invocations[invocation] = true

	assistantEntry, ok := entriesById[record.AssistantEntryID]
	if !ok || !isMessageWithRole(assistantEntry, types.RoleAssistant) {
		return corrupt(CorruptionToolCallMismatch,
			"Tool start "+record.ID+" does not reference an assistant entry")
	}
	assistant, _ := assistantOf(assistantEntry.(*MessageEntry).Message)
	toolCalls := toolCallsOf(assistant)
	if record.ToolIndex >= len(toolCalls) {
		return corrupt(CorruptionToolCallMismatch,
			"Tool start "+record.ID+" does not match its assistant tool-call ordinal")
	}
	toolCall := toolCalls[record.ToolIndex]
	if toolCall.ID != record.ToolCallID || toolCall.Name != record.ToolName {
		return corrupt(CorruptionToolCallMismatch,
			"Tool start "+record.ID+" does not match its assistant tool-call ordinal")
	}

	return validateResultEntry(entriesById, record.ResultEntryID,
		func(entry Entry) bool {
			result, ok := toolResultMessageOfEntry(entry)
			return ok && result.ToolCallID == record.ToolCallID && result.ToolName == record.ToolName
		},
		"tool result")
}

func validateDeferredHandles(entriesById map[string]Entry) error {
	for _, entry := range entriesById {
		if assistant, ok := assistantMessageOfEntry(entry); ok &&
			assistant.StopReason == types.StopReasonDeferred && assistant.Deferred == nil {
			return corrupt(CorruptionInvalidDeferredHandle,
				"Deferred assistant entry "+entry.Base().ID+" does not carry a handle")
		}
	}
	return nil
}

func validateOperationResult(entriesById map[string]Entry, record *OperationStartedRecord) error {
	switch intent := record.Intent.(type) {
	case *RunIntent:
		for _, target := range intent.InitialMessages {
			if err := validateExactProvisionedEntry(entriesById, target); err != nil {
				return err
			}
		}
	case *CompactionIntent:
		return validateResultEntry(entriesById, intent.ResultEntryID,
			func(entry Entry) bool { return entry.EntryType() == EntryTypeCompaction },
			"manual compaction")
	case *NavigationIntent:
		if intent.SummaryEntryID != nil {
			return validateResultEntry(entriesById, *intent.SummaryEntryID,
				func(entry Entry) bool { return entry.EntryType() == EntryTypeBranchSummary },
				"navigation summary")
		}
	}
	return nil
}

// ValidateRecordLog validates a bounded lane recovery slice without reading
// or mutating session state (pi: validateRecordLog; the TS void throw /
// return becomes an error return).
func ValidateRecordLog(input *RecordLogSlice) error {
	if len(input.OpenOperations) > 1 {
		return corrupt(CorruptionMultipleOpenOperations,
			"Lane "+input.Lane+" has at least two open operations")
	}

	entriesById := make(map[string]Entry, len(input.Entries))
	for _, entry := range input.Entries {
		entriesById[entry.Base().ID] = entry
	}
	if err := validateDeferredHandles(entriesById); err != nil {
		return err
	}
	starts := make(map[string]*OperationStartedRecord)
	finishedAt := make(map[string]int64)
	abortedAt := make(map[string]int64)
	queueEnqueues := make(map[string]*QueueEnqueuedRecord)
	latestAttempt := make(map[string]*StepAttemptRecord)
	toolInvocations := make(map[string]bool)
	records := bySequenceRecords(input.Records)

	for _, record := range records {
		if started, ok := record.(*OperationStartedRecord); ok {
			starts[started.ID] = started
			if err := validateOperationResult(entriesById, started); err != nil {
				return err
			}
			continue
		}

		runID, hasRun := hasRunID(record)
		if hasRun {
			if _, known := starts[runID]; !known {
				return corrupt(CorruptionUnknownOperation,
					"Record "+record.Base().ID+" references unknown operation "+runID)
			}
			if finishSeq, finished := finishedAt[runID]; finished && record.Base().Seq > finishSeq {
				return corrupt(CorruptionRecordAfterFinish,
					"Record "+record.Base().ID+" follows the finish of operation "+runID)
			}
		}

		switch r := record.(type) {
		case *OperationFinishedRecord:
			finishedAt[r.RunID] = r.Base().Seq
		case *AbortRequestedRecord:
			abortedAt[r.RunID] = r.Base().Seq
		case *StepAttemptRecord:
			if err := validateAttemptReason(r); err != nil {
				return err
			}
			if err := validateAttemptSequence(r, latestAttempt[runID], entriesById); err != nil {
				return err
			}
			if err := validateAttemptResult(entriesById, r); err != nil {
				return err
			}
			latestAttempt[runID] = r
		case *ToolStartedRecord:
			if err := validateToolStart(r, entriesById, toolInvocations); err != nil {
				return err
			}
		case *QueueEnqueuedRecord:
			if r.Queue != QueueNextRun {
				if abortSeq, aborted := abortedAt[runID]; aborted && r.Base().Seq > abortSeq {
					return corrupt(CorruptionQueueAfterAbort,
						string(r.Queue)+" item "+r.Target.Base().ID+" was enqueued after abort")
				}
			}
			queueEnqueues[r.Target.Base().ID] = r
			if err := validateExactProvisionedEntry(entriesById, r.Target); err != nil {
				return err
			}
		case *QueueCancelledRecord:
			enqueue, known := queueEnqueues[r.EntryID]
			if !known ||
				enqueue.Base().Seq >= r.Base().Seq ||
				!optionalRunIDEqual(enqueue.RunID, r.RunID) ||
				entryExists(entriesById, r.EntryID) {
				return corrupt(CorruptionInvalidQueueCancellation,
					"Queue cancellation "+r.ID+" has no pending matching enqueue")
			}
		case *WriteDeferredRecord:
			if err := validateExactProvisionedEntry(entriesById, r.Target); err != nil {
				return err
			}
		case *UsageRecord:
			// Usage records carry no structural obligations.
		}
	}
	return nil
}

// optionalRunIDEqual compares pi's `runId?: string` members (nil =
// undefined; undefined only equals undefined).
func optionalRunIDEqual(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func cloneDefaults(defaults EffectiveLaneConfiguration) EffectiveLaneConfiguration {
	out := defaults
	out.ActiveToolNames = append([]string(nil), defaults.ActiveToolNames...)
	return out
}

func deriveEffectiveConfiguration(input *LaneReductionInput) EffectiveLaneConfiguration {
	configuration := cloneDefaults(input.Defaults)
	entriesById := make(map[string]Entry)
	for _, entry := range input.ConfigurationEntries {
		entriesById[entry.Base().ID] = entry
	}
	for _, entry := range input.OwnEntries {
		entriesById[entry.Base().ID] = entry
	}

	for _, entry := range bySequenceEntries(entryValuesByID(entriesById)) {
		switch e := entry.(type) {
		case *ModelChangeEntry:
			configuration.Model = EffectiveModel{Provider: e.Provider, ModelID: e.ModelID}
		case *ThinkingLevelEntry:
			configuration.ThinkingLevel = agent.ThinkingLevel(e.ThinkingLevel)
		case *ActiveToolsEntry:
			configuration.ActiveToolNames = append([]string(nil), e.ActiveToolNames...)
		case *MessageEntry:
			if assistant, ok := assistantOf(e.Message); ok {
				configuration.Model = EffectiveModel{
					Provider: string(assistant.Provider),
					ModelID:  assistant.Model,
				}
			}
		}
	}
	return configuration
}

func deriveNewestOwn(entry Entry) *NewestOwnState {
	if entry == nil {
		return nil
	}
	if message, ok := entry.(*MessageEntry); ok {
		if assistant, ok := assistantOf(message.Message); ok {
			return &NewestOwnState{
				EntryID:    entry.Base().ID,
				Type:       entry.EntryType(),
				Role:       lo.ToPtr(assistant.Role),
				StopReason: lo.ToPtr(assistant.StopReason),
			}
		}
		role := message.Message.MessageRole()
		return &NewestOwnState{
			EntryID: entry.Base().ID,
			Type:    entry.EntryType(),
			Role:    lo.ToPtr(role),
		}
	}
	return &NewestOwnState{EntryID: entry.Base().ID, Type: entry.EntryType()}
}

func deriveToolBatch(
	operationID string,
	records []LaneRecord,
	ownEntries []Entry,
	entriesById map[string]Entry,
	deferredWriteIds map[string]bool,
) *ToolBatchState {
	var assistantEntry *MessageEntry
	for i := len(ownEntries) - 1; i >= 0; i-- {
		if assistant, ok := assistantMessageOfEntry(ownEntries[i]); ok && len(toolCallsOf(assistant)) > 0 {
			assistantEntry = ownEntries[i].(*MessageEntry)
			break
		}
	}
	if assistantEntry == nil {
		return nil
	}
	assistant, _ := assistantOf(assistantEntry.Message)
	toolCalls := toolCallsOf(assistant)

	starts := make(map[int]*ToolStartedRecord)
	for _, record := range records {
		if started, ok := record.(*ToolStartedRecord); ok &&
			started.RunID == operationID && started.AssistantEntryID == assistantEntry.Base().ID {
			starts[started.ToolIndex] = started
		}
	}

	calls := make([]ToolBatchCall, 0, len(toolCalls))
	for toolIndex, toolCall := range toolCalls {
		started := starts[toolIndex]
		var result Entry
		if started != nil {
			result = entriesById[started.ResultEntryID]
		}
		if result == nil {
			for _, entry := range ownEntries {
				if entry.Base().Seq <= assistantEntry.Base().Seq || deferredWriteIds[entry.Base().ID] {
					continue
				}
				if toolResult, ok := toolResultMessageOfEntry(entry); ok && toolResult.ToolCallID == toolCall.ID {
					result = entry
					break
				}
			}
		}
		call := ToolBatchCall{
			ToolIndex:    toolIndex,
			ToolCall:     jsonClone(toolCall),
			ResultExists: result != nil,
		}
		if started != nil {
			call.Started = jsonClone(started)
		}
		if message, ok := result.(*MessageEntry); ok && message.Terminate != nil && *message.Terminate {
			call.Terminate = true
		}
		calls = append(calls, call)
	}

	unresolved := false
	for _, call := range calls {
		if !call.ResultExists {
			unresolved = true
			break
		}
	}

	return &ToolBatchState{
		AssistantEntryID: assistantEntry.Base().ID,
		Calls:            calls,
		Truncated:        assistant.StopReason == types.StopReasonLength,
		Unresolved:       unresolved,
	}
}

// ReduceLaneState purely reconstructs one lane's orchestration state from its
// bounded recovery inputs (pi: reduceLaneState; the thrown corruption error
// propagates as the error value).
func ReduceLaneState(input *LaneReductionInput) (*LaneReductionResult, error) {
	if err := ValidateRecordLog(&input.RecordLogSlice); err != nil {
		return nil, err
	}

	records := bySequenceRecords(input.Records)
	ownEntries := bySequenceEntries(input.OwnEntries)
	entriesById := make(map[string]Entry)
	for _, entry := range input.Entries {
		entriesById[entry.Base().ID] = entry
	}
	for _, entry := range ownEntries {
		entriesById[entry.Base().ID] = entry
	}
	cancelledQueueIds := make(map[string]bool)
	for _, record := range records {
		if cancelled, ok := record.(*QueueCancelledRecord); ok {
			cancelledQueueIds[cancelled.EntryID] = true
		}
	}
	var pendingQueueRecords []*QueueEnqueuedRecord
	for _, record := range records {
		if enqueued, ok := record.(*QueueEnqueuedRecord); ok &&
			!entryExists(entriesById, enqueued.Target.Base().ID) &&
			!cancelledQueueIds[enqueued.Target.Base().ID] {
			pendingQueueRecords = append(pendingQueueRecords, enqueued)
		}
	}
	var started *OperationStartedRecord
	if len(input.OpenOperations) > 0 {
		started = &input.OpenOperations[0]
	}
	capturedInitialMessageIds := make(map[string]bool)
	if started != nil {
		if run, ok := started.Intent.(*RunIntent); ok {
			for _, target := range run.InitialMessages {
				capturedInitialMessageIds[target.Base().ID] = true
			}
		}
	}
	var pendingNextRun []Entry
	for _, record := range pendingQueueRecords {
		if record.Queue == QueueNextRun && !capturedInitialMessageIds[record.Target.Base().ID] {
			pendingNextRun = append(pendingNextRun, cloneEntry(record.Target))
		}
	}
	effectiveConfiguration := deriveEffectiveConfiguration(input)

	if started == nil {
		return &LaneReductionResult{
			LaneState: &LaneState{
				Lane:           input.Lane,
				LeafID:         input.LeafID,
				Operation:      nil,
				PendingNextRun: pendingNextRun,
			},
			EffectiveConfiguration: effectiveConfiguration,
			TerminalFailure:        nil,
		}, nil
	}

	var operationRecords []LaneRecord
	for _, record := range records {
		if operationStart, ok := record.(*OperationStartedRecord); ok {
			if operationStart.ID == started.ID {
				operationRecords = append(operationRecords, record)
			}
			continue
		}
		if runID, hasRun := hasRunID(record); hasRun && runID == started.ID {
			operationRecords = append(operationRecords, record)
		}
	}
	aborting := false
	for _, record := range operationRecords {
		if _, ok := record.(*AbortRequestedRecord); ok {
			aborting = true
		}
	}
	var pendingSteer []Entry
	if !aborting {
		for _, record := range pendingQueueRecords {
			if record.Queue == QueueSteer && record.RunID != nil && *record.RunID == started.ID {
				pendingSteer = append(pendingSteer, cloneEntry(record.Target))
			}
		}
	}
	var pendingFollowUp []Entry
	if !aborting {
		for _, record := range pendingQueueRecords {
			if record.Queue == QueueFollowUp && record.RunID != nil && *record.RunID == started.ID {
				pendingFollowUp = append(pendingFollowUp, cloneEntry(record.Target))
			}
		}
	}
	var pendingWrites []Entry
	for _, record := range operationRecords {
		if write, ok := record.(*WriteDeferredRecord); ok && !entryExists(entriesById, write.Target.Base().ID) {
			pendingWrites = append(pendingWrites, cloneEntry(write.Target))
		}
	}
	var missingInitialMessages []Entry
	if run, ok := started.Intent.(*RunIntent); ok {
		for _, target := range run.InitialMessages {
			if !entryExists(entriesById, target.Base().ID) {
				missingInitialMessages = append(missingInitialMessages, cloneEntry(target))
			}
		}
	}

	var newestAttempt *StepAttemptRecord
	for _, record := range operationRecords {
		if attempt, ok := record.(*StepAttemptRecord); ok {
			newestAttempt = attempt
		}
	}
	var step *OperationStepState
	if newestAttempt != nil && !entryExists(entriesById, newestAttempt.ResultEntryID) {
		step = &OperationStepState{
			Kind:             newestAttempt.Step,
			Attempts:         newestAttempt.Attempt,
			ResultEntryID:    newestAttempt.ResultEntryID,
			CompactionReason: newestAttempt.CompactionReason,
		}
		if newestAttempt.Step != OperationStepCompaction {
			step.CompactionReason = nil
		}
	}

	consumedInputIds := make(map[string]bool)
	if run, ok := started.Intent.(*RunIntent); ok {
		for _, target := range run.InitialMessages {
			consumedInputIds[target.Base().ID] = true
		}
	}
	for _, record := range operationRecords {
		if enqueued, ok := record.(*QueueEnqueuedRecord); ok && enqueued.Queue != QueueNextRun {
			consumedInputIds[enqueued.Target.Base().ID] = true
		}
	}
	newestConsumedInputSequence := int64(math.MinInt64)
	for id := range consumedInputIds {
		if entry, ok := entriesById[id]; ok && entry.EntryType() == EntryTypeMessage {
			if entry.Base().Seq > newestConsumedInputSequence {
				newestConsumedInputSequence = entry.Base().Seq
			}
		}
	}
	overflowRecoveryUsed := false
	for _, record := range operationRecords {
		if attempt, ok := record.(*StepAttemptRecord); ok &&
			attempt.Step == OperationStepCompaction &&
			attempt.CompactionReason != nil &&
			*attempt.CompactionReason == CompactionReasonOverflow &&
			attempt.Base().Seq > newestConsumedInputSequence {
			overflowRecoveryUsed = true
		}
	}

	var newestOwnEntry Entry
	if len(ownEntries) > 0 {
		newestOwnEntry = ownEntries[len(ownEntries)-1]
	}
	newestOwn := deriveNewestOwn(newestOwnEntry)
	var deferred *types.DeferredHandle
	if newestOwnEntry != nil {
		if assistant, ok := assistantMessageOfEntry(newestOwnEntry); ok &&
			assistant.StopReason == types.StopReasonDeferred && assistant.Deferred != nil {
			cloned := jsonClone(*assistant.Deferred)
			deferred = &cloned
		}
	}
	targets := OperationTargets{}
	switch intent := started.Intent.(type) {
	case *CompactionIntent:
		targets.Result = lo.ToPtr(entryExists(entriesById, intent.ResultEntryID))
	case *NavigationIntent:
		if intent.SummaryEntryID != nil {
			targets.Summary = lo.ToPtr(entryExists(entriesById, *intent.SummaryEntryID))
		}
	}

	deferredWriteIds := make(map[string]bool)
	for _, record := range operationRecords {
		if write, ok := record.(*WriteDeferredRecord); ok {
			deferredWriteIds[write.Target.Base().ID] = true
		}
	}
	var terminalFailure *TerminalFailureState
	if newestOwnEntry != nil {
		if assistant, ok := assistantMessageOfEntry(newestOwnEntry); ok &&
			assistant.StopReason == types.StopReasonError &&
			!deferredWriteIds[newestOwnEntry.Base().ID] {
			producedByStep := false
			for _, record := range operationRecords {
				if attempt, ok := record.(*StepAttemptRecord); ok && attempt.ResultEntryID == newestOwnEntry.Base().ID {
					producedByStep = true
				}
			}
			producedByDeferredFetch := false
			for _, record := range operationRecords {
				if usage, ok := record.(*UsageRecord); ok &&
					usage.Cause == UsageCauseDeferredFetch &&
					usage.EntryID != nil && *usage.EntryID == newestOwnEntry.Base().ID {
					producedByDeferredFetch = true
				}
			}
			if len(ownEntries) >= 2 {
				if previousAssistant, ok := assistantMessageOfEntry(ownEntries[len(ownEntries)-2]); ok &&
					previousAssistant.StopReason == types.StopReasonDeferred {
					producedByDeferredFetch = true
				}
			}
			if producedByStep || producedByDeferredFetch {
				clonedMessage, err := cloneAssistantMessage(assistant)
				if err != nil {
					return nil, err
				}
				source := TerminalFailureSourceDeferredFetch
				if producedByStep {
					source = TerminalFailureSourceStep
				}
				terminalFailure = &TerminalFailureState{
					EntryID: newestOwnEntry.Base().ID,
					Source:  source,
					Message: clonedMessage,
				}
			}
		}
	}

	operation := &LaneOperationState{
		ID:                     started.ID,
		Kind:                   started.Intent.OperationIntentKind(),
		Intent:                 cloneIntent(started.Intent),
		Aborting:               aborting,
		Step:                   step,
		ToolBatch:              deriveToolBatch(started.ID, operationRecords, ownEntries, entriesById, deferredWriteIds),
		MissingInitialMessages: missingInitialMessages,
		PendingSteer:           pendingSteer,
		PendingFollowUp:        pendingFollowUp,
		PendingWrites:          pendingWrites,
		Deferred:               deferred,
		OverflowRecoveryUsed:   overflowRecoveryUsed,
		NewestOwn:              newestOwn,
		Targets:                targets,
	}
	return &LaneReductionResult{
		LaneState: &LaneState{
			Lane:           input.Lane,
			LeafID:         input.LeafID,
			Operation:      operation,
			PendingNextRun: pendingNextRun,
		},
		EffectiveConfiguration: effectiveConfiguration,
		TerminalFailure:        terminalFailure,
	}, nil
}
