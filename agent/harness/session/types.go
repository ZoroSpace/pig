// pi: src/harness/session/types.ts — the session storage type surface: Entry
// and LaneRecord unions (with their JSON wire formats), query types, and the
// SessionStorage / SessionTree / SessionRepo interfaces.
//
// Type mapping deviations, all declared here:
//
//   - Entry / LaneRecord discriminated unions → interface + concrete types +
//     DecodeEntry / DecodeRecord dispatch (per §3.4), with an unexported
//     base accessor so storage implementations can fill the storage-assigned
//     fields (seq, parentId, timestamp) in place. ProvisionedEntry<TEntry> /
//     NewRecord<TRecord> (TS Omit types) have no Go counterpart: callers pass
//     the concrete entry/record with those fields zero and storage fills them.
//   - SessionStopReason (pi: Exclude<StopReason, "pending"> | "deferred") is
//     an alias of types.StopReason, which already contains every member
//     including "deferred". The "pending" exclusion is type-level in TS;
//     Go relies on the runtime convention that "pending" is never persisted.
//   - Nested intent union of OperationStartedRecord → OperationIntent
//     interface + three concrete kinds (discriminant field "kind").
//   - StepAttemptRecord / QueueEnqueuedRecord / UsageRecord are closed
//     member unions sharing RecordBase; each collapses to a single struct
//     with pointer fields for the members-only properties (pi: `x?: never`
//     vs required). Field comments list which causes/steps own which field.
//   - JsonValue (recursive JSON union) → any; it only appears inside
//     resumeData / details payloads, which encoding/json already constrains
//     at append time (assertJsonSerializable).
//   - SessionStorage<TMetadata>/SessionRepo<TMetadata> generics drop to the
//     concrete SessionMetadata / Session (per the TModel generic rule); the
//     backend-specific metadata types (JsonlSessionMetadata) arrive with the
//     jsonl backend and re-wide the interface there if needed.
//   - pi's async methods return Promises; Go methods return (T, error).
//     pi takes no AbortSignal anywhere in this file, so no ctx parameter is
//     introduced (per the AbortSignal → context rule).
//   - "getEntry returns undefined" reads as (nil, nil); the nil-entry/nil-
//     record/error meaning is documented on each method.
package session

import (
	"encoding/json"
	"fmt"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/ai/types"
)

// JsonValue (pi: recursive JSON union) maps to any; see the file-header note.
type JsonValue = any

// SessionStopReason is the persisted stop-reason vocabulary (pi:
// Exclude<StopReason, "pending"> | "deferred"); see the file-header note.
type SessionStopReason = types.StopReason

// IdGenerator produces entry/record ids (pi: IdGenerator).
type IdGenerator interface {
	Next() string
}

// EntryType is the closed entry discriminant vocabulary (pi: Entry["type"]).
type EntryType string

const (
	EntryTypeMessage             EntryType = "message"
	EntryTypeModelChange         EntryType = "model_change"
	EntryTypeThinkingLevelChange EntryType = "thinking_level_change"
	EntryTypeActiveToolsChange   EntryType = "active_tools_change"
	EntryTypeCompaction          EntryType = "compaction"
	EntryTypeBranchSummary       EntryType = "branch_summary"
	EntryTypeCustom              EntryType = "custom"
)

// EntryBase carries the storage-assigned fields shared by every entry (pi:
// EntryBase). Seq is a shared sequence across entries and records; ParentID
// is the appending lane's leaf; Timestamp is Unix milliseconds. All three are
// assigned by storage on append and must be left zero by callers.
type EntryBase struct {
	Type      EntryType `json:"type"`
	ID        string    `json:"id"`
	Seq       int64     `json:"seq"`       // shared sequence; read-side, storage-assigned
	ParentID  *string   `json:"parentId"`  // storage-assigned: the appending lane's leaf; null = root
	Timestamp int64     `json:"timestamp"` // Unix ms, storage-assigned
}

// Entry is the persisted session-tree node union (pi: Entry).
type Entry interface {
	// EntryType returns the discriminant literal ("message", "custom", ...).
	EntryType() EntryType
	// Base exposes the shared fields for storage-side assignment (exported:
	// backend implementations and tests outside this package need it).
	Base() *EntryBase
}

func (b *EntryBase) Base() *EntryBase { return b }

type MessageEntry struct {
	EntryBase
	Message agent.AgentMessage `json:"message"`
	// Terminate is pi's `terminate?: true`: only ever true when present.
	Terminate *bool `json:"terminate,omitempty"`
}

func (e MessageEntry) EntryType() EntryType { return EntryTypeMessage }

type ModelChangeEntry struct {
	EntryBase
	Provider string `json:"provider"`
	ModelID  string `json:"modelId"`
}

func (e ModelChangeEntry) EntryType() EntryType { return EntryTypeModelChange }

type ThinkingLevelEntry struct {
	EntryBase
	ThinkingLevel string `json:"thinkingLevel"`
}

func (e ThinkingLevelEntry) EntryType() EntryType { return EntryTypeThinkingLevelChange }

type ActiveToolsEntry struct {
	EntryBase
	ActiveToolNames []string `json:"activeToolNames"`
}

func (e ActiveToolsEntry) EntryType() EntryType { return EntryTypeActiveToolsChange }

type CompactionEntry struct {
	EntryBase
	Summary      string               `json:"summary"`
	RetainedTail []agent.AgentMessage `json:"retainedTail"`
	TokensBefore int64                `json:"tokensBefore"`
	Details      any                  `json:"details,omitempty"`
	Usage        *types.Usage         `json:"usage,omitempty"`
}

func (e CompactionEntry) EntryType() EntryType { return EntryTypeCompaction }

type BranchSummaryEntry struct {
	EntryBase
	FromID  string       `json:"fromId"`
	Summary string       `json:"summary"`
	Details any          `json:"details,omitempty"`
	Usage   *types.Usage `json:"usage,omitempty"`
}

func (e BranchSummaryEntry) EntryType() EntryType { return EntryTypeBranchSummary }

type CustomEntry struct {
	EntryBase
	CustomType string `json:"customType"`
	Data       any    `json:"data,omitempty"`
}

func (e CustomEntry) EntryType() EntryType { return EntryTypeCustom }

// DecodeEntry decodes one entry, dispatching on its "type" field.
func DecodeEntry(data json.RawMessage) (Entry, error) {
	var probe struct {
		Type EntryType `json:"type"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, err
	}
	switch probe.Type {
	case EntryTypeMessage:
		var e MessageEntry
		if err := unmarshalEntryWithRawMessage(data, &e, "message"); err != nil {
			return nil, err
		}
		return &e, nil
	case EntryTypeModelChange:
		var e ModelChangeEntry
		if err := json.Unmarshal(data, &e); err != nil {
			return nil, err
		}
		return &e, nil
	case EntryTypeThinkingLevelChange:
		var e ThinkingLevelEntry
		if err := json.Unmarshal(data, &e); err != nil {
			return nil, err
		}
		return &e, nil
	case EntryTypeActiveToolsChange:
		var e ActiveToolsEntry
		if err := json.Unmarshal(data, &e); err != nil {
			return nil, err
		}
		return &e, nil
	case EntryTypeCompaction:
		var e CompactionEntry
		if err := unmarshalEntryWithRawMessage(data, &e, "retainedTail"); err != nil {
			return nil, err
		}
		return &e, nil
	case EntryTypeBranchSummary:
		var e BranchSummaryEntry
		if err := json.Unmarshal(data, &e); err != nil {
			return nil, err
		}
		return &e, nil
	case EntryTypeCustom:
		var e CustomEntry
		if err := json.Unmarshal(data, &e); err != nil {
			return nil, err
		}
		return &e, nil
	default:
		return nil, fmt.Errorf("unknown entry type %q", probe.Type)
	}
}

// DecodeEntryList decodes a heterogeneous entry array.
func DecodeEntryList(data json.RawMessage) ([]Entry, error) {
	var raws []json.RawMessage
	if err := json.Unmarshal(data, &raws); err != nil {
		return nil, err
	}
	entries := make([]Entry, len(raws))
	for i, r := range raws {
		e, err := DecodeEntry(r)
		if err != nil {
			return nil, err
		}
		entries[i] = e
	}
	return entries, nil
}

// unmarshalEntryWithRawMessage decodes an entry whose AgentMessage-typed field
// needs interface dispatch (pi: heterogeneous arrays re-decode via the alias
// trick). rawField names the field holding encoded agent messages.
func unmarshalEntryWithRawMessage(data json.RawMessage, entry any, rawField string) error {
	var aux map[string]json.RawMessage
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	raw, ok := aux[rawField]
	if !ok {
		// Let the main unmarshal surface the missing-field error shape.
		return json.Unmarshal(data, entry)
	}
	delete(aux, rawField)
	stripped, err := json.Marshal(aux)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(stripped, entry); err != nil {
		return err
	}
	switch e := entry.(type) {
	case *MessageEntry:
		msg, err := types.DecodeMessage(raw)
		if err != nil {
			return err
		}
		e.Message = msg
	case *CompactionEntry:
		tail, err := types.DecodeMessageList(raw)
		if err != nil {
			return err
		}
		e.RetainedTail = tail
	}
	return nil
}

// RecordType is the closed lane-record discriminant vocabulary (pi:
// LaneRecord["type"]).
type RecordType string

const (
	RecordTypeOperationStarted  RecordType = "operation_started"
	RecordTypeAbortRequested    RecordType = "abort_requested"
	RecordTypeOperationFinished RecordType = "operation_finished"
	RecordTypeStepAttempt       RecordType = "step_attempt"
	RecordTypeToolStarted       RecordType = "tool_started"
	RecordTypeQueueEnqueued     RecordType = "queue_enqueued"
	RecordTypeQueueCancelled    RecordType = "queue_cancelled"
	RecordTypeWriteDeferred     RecordType = "write_deferred"
	RecordTypeUsage             RecordType = "usage"
)

// RecordBase carries the storage-assigned fields shared by every lane record
// (pi: RecordBase). Seq/Timestamp are storage-assigned; Lane is caller-
// supplied and permanent for the record's lifetime.
type RecordBase struct {
	Type      RecordType `json:"type"`
	ID        string     `json:"id"`
	Seq       int64      `json:"seq"`
	Lane      string     `json:"lane"`
	Timestamp int64      `json:"timestamp"` // Unix ms, storage-assigned
}

// LaneRecord is the append-only lane operation log union (pi: LaneRecord).
type LaneRecord interface {
	// LaneRecordType returns the discriminant literal ("operation_started", ...).
	LaneRecordType() RecordType
	// Base exposes the shared fields for storage-side assignment (exported:
	// backend implementations and tests outside this package need it).
	Base() *RecordBase
}

func (b *RecordBase) Base() *RecordBase { return b }

// OperationIntentKind discriminates the operation_started intent union.
type OperationIntentKind string

const (
	OperationIntentRun        OperationIntentKind = "run"
	OperationIntentCompaction OperationIntentKind = "compaction"
	OperationIntentNavigation OperationIntentKind = "navigation"
)

// OperationIntent is the nested intent union of OperationStartedRecord (pi:
// inline union on `intent`).
type OperationIntent interface {
	OperationIntentKind() OperationIntentKind
}

type RunIntent struct {
	Kind OperationIntentKind `json:"kind"`
	// OriginalPrompt is the normalized caller input before before_run; kept
	// for suspended operations and before_resume.
	OriginalPrompt []agent.AgentMessage `json:"originalPrompt"`
	// InitialMessages holds captured nextRun items, then the prompt, then
	// before_run injections. Entries arrive provisioned (no seq/parentId/
	// timestamp).
	InitialMessages      []Entry        `json:"initialMessages"`
	SystemPromptOverride *string        `json:"systemPromptOverride,omitempty"`
	ResumeData           map[string]any `json:"resumeData,omitempty"`
}

func (i RunIntent) OperationIntentKind() OperationIntentKind { return i.Kind }

type CompactionIntent struct {
	Kind               OperationIntentKind `json:"kind"`
	CustomInstructions *string             `json:"customInstructions,omitempty"`
	ResultEntryID      string              `json:"resultEntryId"`
}

func (i CompactionIntent) OperationIntentKind() OperationIntentKind { return i.Kind }

type NavigationIntent struct {
	Kind               OperationIntentKind `json:"kind"`
	TargetID           *string             `json:"targetId"` // null allowed
	Summarize          bool                `json:"summarize"`
	CustomInstructions *string             `json:"customInstructions,omitempty"`
	Label              *string             `json:"label,omitempty"`
	SummaryEntryID     *string             `json:"summaryEntryId,omitempty"`
}

func (i NavigationIntent) OperationIntentKind() OperationIntentKind { return i.Kind }

// DecodeOperationIntent decodes one intent, dispatching on its "kind" field.
func DecodeOperationIntent(data json.RawMessage) (OperationIntent, error) {
	var probe struct {
		Kind OperationIntentKind `json:"kind"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, err
	}
	switch probe.Kind {
	case OperationIntentRun:
		var i RunIntent
		if err := unmarshalIntentWithRawMessages(data, &i); err != nil {
			return nil, err
		}
		return &i, nil
	case OperationIntentCompaction:
		var i CompactionIntent
		if err := json.Unmarshal(data, &i); err != nil {
			return nil, err
		}
		return &i, nil
	case OperationIntentNavigation:
		var i NavigationIntent
		if err := json.Unmarshal(data, &i); err != nil {
			return nil, err
		}
		return &i, nil
	default:
		return nil, fmt.Errorf("unknown operation intent kind %q", probe.Kind)
	}
}

// unmarshalIntentWithRawMessages decodes a RunIntent whose originalPrompt and
// initialMessages fields need interface dispatch.
func unmarshalIntentWithRawMessages(data json.RawMessage, intent *RunIntent) error {
	var aux map[string]json.RawMessage
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	var promptRaw, initialRaw json.RawMessage
	if r, ok := aux["originalPrompt"]; ok {
		promptRaw = r
		delete(aux, "originalPrompt")
	}
	if r, ok := aux["initialMessages"]; ok {
		initialRaw = r
		delete(aux, "initialMessages")
	}
	stripped, err := json.Marshal(aux)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(stripped, intent); err != nil {
		return err
	}
	if promptRaw != nil {
		prompt, err := types.DecodeMessageList(promptRaw)
		if err != nil {
			return err
		}
		intent.OriginalPrompt = prompt
	}
	if initialRaw != nil {
		initial, err := DecodeEntryList(initialRaw)
		if err != nil {
			return err
		}
		intent.InitialMessages = initial
	}
	return nil
}

type OperationStartedRecord struct {
	RecordBase
	SourceLeafID *string         `json:"sourceLeafId"` // null allowed
	Intent       OperationIntent `json:"intent"`
}

func (r OperationStartedRecord) LaneRecordType() RecordType { return RecordTypeOperationStarted }

// UnmarshalJSON decodes the nested intent union.
func (r *OperationStartedRecord) UnmarshalJSON(data []byte) error {
	var aux struct {
		RecordBase
		SourceLeafID *string         `json:"sourceLeafId"`
		Intent       json.RawMessage `json:"intent"`
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	intent, err := DecodeOperationIntent(aux.Intent)
	if err != nil {
		return err
	}
	r.RecordBase = aux.RecordBase
	r.SourceLeafID = aux.SourceLeafID
	r.Intent = intent
	return nil
}

type AbortRequestedRecord struct {
	RecordBase
	RunID string `json:"runId"`
}

func (r AbortRequestedRecord) LaneRecordType() RecordType { return RecordTypeAbortRequested }

type OperationFinishedRecord struct {
	RecordBase
	RunID string `json:"runId"`
	// Outcome is "completed" | "aborted" | "failed" | "declined".
	Outcome string `json:"outcome"`
	// Error is { code, message } when the operation failed. Kept as raw map
	// (pi: { code: string; message: string }) to preserve the wire shape; nil
	// when absent.
	Error map[string]string `json:"error,omitempty"`
}

func (r OperationFinishedRecord) LaneRecordType() RecordType { return RecordTypeOperationFinished }

// CompactionReason explains why a compaction summary generation started.
type CompactionReason string

const (
	CompactionReasonManual    CompactionReason = "manual"
	CompactionReasonThreshold CompactionReason = "threshold"
	CompactionReasonOverflow  CompactionReason = "overflow"
)

// OperationStep names the step a step_attempt belongs to (pi: inline union on
// `step`).
type OperationStep string

const (
	OperationStepAssistant     OperationStep = "assistant"
	OperationStepBranchSummary OperationStep = "branch_summary"
	OperationStepCompaction    OperationStep = "compaction"
)

// StepAttemptRecord collapses pi's two step_attempt member variants into one
// struct: CompactionReason is set only when Step is "compaction" (pi: it is
// `CompactionReason` there and `never` on the assistant/branch_summary
// variant).
type StepAttemptRecord struct {
	RecordBase
	RunID         string        `json:"runId"`
	Step          OperationStep `json:"step"`
	Attempt       int           `json:"attempt"`
	ResultEntryID string        `json:"resultEntryId"`
	// CompactionReason persists why compaction summary generation started so
	// recovery resumes the same work. Required when Step is "compaction".
	CompactionReason *CompactionReason `json:"compactionReason,omitempty"`
}

func (r StepAttemptRecord) LaneRecordType() RecordType { return RecordTypeStepAttempt }

// QueueKind discriminates the queue_enqueued queue union.
type QueueKind string

const (
	QueueSteer    QueueKind = "steer"
	QueueFollowUp QueueKind = "followUp"
	QueueNextRun  QueueKind = "nextRun"
)

// QueueEnqueuedRecord collapses pi's two member variants into one struct:
// RunID is set for steer/followUp enqueues and nil for nextRun (pi:
// `runId: string` vs `runId?: never`).
type QueueEnqueuedRecord struct {
	RecordBase
	Queue  QueueKind `json:"queue"`
	RunID  *string   `json:"runId,omitempty"`
	Target Entry     `json:"target"` // provisioned entry
}

func (r QueueEnqueuedRecord) LaneRecordType() RecordType { return RecordTypeQueueEnqueued }

// UnmarshalJSON decodes the provisioned target entry through the union
// dispatch.
func (r *QueueEnqueuedRecord) UnmarshalJSON(data []byte) error {
	var aux struct {
		RecordBase
		Queue  QueueKind       `json:"queue"`
		RunID  *string         `json:"runId,omitempty"`
		Target json.RawMessage `json:"target"`
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	target, err := DecodeEntry(aux.Target)
	if err != nil {
		return err
	}
	r.RecordBase = aux.RecordBase
	r.Queue = aux.Queue
	r.RunID = aux.RunID
	r.Target = target
	return nil
}

type QueueCancelledRecord struct {
	RecordBase
	// RunID is pi's `runId?: string` — nil when the cancellation is not bound
	// to an operation.
	RunID   *string `json:"runId,omitempty"`
	EntryID string  `json:"entryId"`
}

func (r QueueCancelledRecord) LaneRecordType() RecordType { return RecordTypeQueueCancelled }

type WriteDeferredRecord struct {
	RecordBase
	RunID  string `json:"runId"`
	Target Entry  `json:"target"` // provisioned entry
}

func (r WriteDeferredRecord) LaneRecordType() RecordType { return RecordTypeWriteDeferred }

// UnmarshalJSON decodes the provisioned target entry through the union
// dispatch.
func (r *WriteDeferredRecord) UnmarshalJSON(data []byte) error {
	var aux struct {
		RecordBase
		RunID  string          `json:"runId"`
		Target json.RawMessage `json:"target"`
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	target, err := DecodeEntry(aux.Target)
	if err != nil {
		return err
	}
	r.RecordBase = aux.RecordBase
	r.RunID = aux.RunID
	r.Target = target
	return nil
}

// UsageCause discriminates the usage record cause union (pi: inline union on
// `cause`).
type UsageCause string

const (
	UsageCauseAssistant     UsageCause = "assistant"
	UsageCauseCompaction    UsageCause = "compaction"
	UsageCauseBranchSummary UsageCause = "branch_summary"
	UsageCauseDeferredFetch UsageCause = "deferred_fetch"
	UsageCauseTool          UsageCause = "tool"
	UsageCauseHook          UsageCause = "hook"
	UsageCauseAdjustment    UsageCause = "adjustment"
)

// UsageRecord collapses pi's four cause member variants into one struct.
// Field ownership by cause (pi: `attempt`/`stopReason` only on the assistant/
// compaction/branch_summary/deferred_fetch variant; `toolCallId` only on
// "tool"; `details` only on "adjustment"; `entryId` optional only on
// "adjustment"; `runId` optional only on "adjustment"):
type UsageRecord struct {
	RecordBase
	Usage types.Usage `json:"usage"`
	Cause UsageCause  `json:"cause"`
	RunID *string     `json:"runId,omitempty"`
	// EntryID is the entry the usage belongs to; nil only for "adjustment"
	// corrections without an entry target.
	EntryID *string `json:"entryId,omitempty"`
	// Attempt is the operation attempt number (assistant/compaction/
	// branch_summary/deferred_fetch causes only).
	Attempt *int `json:"attempt,omitempty"`
	// StopReason is set for the assistant/compaction/branch_summary/
	// deferred_fetch causes only.
	StopReason *SessionStopReason `json:"stopReason,omitempty"`
	// ToolCallID is set for the "tool" cause only.
	ToolCallID string `json:"toolCallId,omitempty"`
	// Details carries adjustment details ("adjustment" cause only).
	Details any `json:"details,omitempty"`
}

func (r UsageRecord) LaneRecordType() RecordType { return RecordTypeUsage }

// DecodeRecord decodes one lane record, dispatching on its "type" field.
func DecodeRecord(data json.RawMessage) (LaneRecord, error) {
	var probe struct {
		Type RecordType `json:"type"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, err
	}
	switch probe.Type {
	case RecordTypeOperationStarted:
		var r OperationStartedRecord
		if err := json.Unmarshal(data, &r); err != nil {
			return nil, err
		}
		return &r, nil
	case RecordTypeAbortRequested:
		var r AbortRequestedRecord
		if err := json.Unmarshal(data, &r); err != nil {
			return nil, err
		}
		return &r, nil
	case RecordTypeOperationFinished:
		var r OperationFinishedRecord
		if err := json.Unmarshal(data, &r); err != nil {
			return nil, err
		}
		return &r, nil
	case RecordTypeStepAttempt:
		var r StepAttemptRecord
		if err := json.Unmarshal(data, &r); err != nil {
			return nil, err
		}
		return &r, nil
	case RecordTypeToolStarted:
		var r ToolStartedRecord
		if err := json.Unmarshal(data, &r); err != nil {
			return nil, err
		}
		return &r, nil
	case RecordTypeQueueEnqueued:
		var r QueueEnqueuedRecord
		if err := json.Unmarshal(data, &r); err != nil {
			return nil, err
		}
		return &r, nil
	case RecordTypeQueueCancelled:
		var r QueueCancelledRecord
		if err := json.Unmarshal(data, &r); err != nil {
			return nil, err
		}
		return &r, nil
	case RecordTypeWriteDeferred:
		var r WriteDeferredRecord
		if err := json.Unmarshal(data, &r); err != nil {
			return nil, err
		}
		return &r, nil
	case RecordTypeUsage:
		var r UsageRecord
		if err := json.Unmarshal(data, &r); err != nil {
			return nil, err
		}
		return &r, nil
	default:
		return nil, fmt.Errorf("unknown record type %q", probe.Type)
	}
}

// DecodeRecordList decodes a heterogeneous lane-record array.
func DecodeRecordList(data json.RawMessage) ([]LaneRecord, error) {
	var raws []json.RawMessage
	if err := json.Unmarshal(data, &raws); err != nil {
		return nil, err
	}
	records := make([]LaneRecord, len(raws))
	for i, r := range raws {
		record, err := DecodeRecord(r)
		if err != nil {
			return nil, err
		}
		records[i] = record
	}
	return records, nil
}

// ToolStartedRecord persists one tool execution start for replay decisions.
type ToolStartedRecord struct {
	RecordBase
	RunID            string         `json:"runId"`
	AssistantEntryID string         `json:"assistantEntryId"`
	ToolIndex        int            `json:"toolIndex"`
	ToolCallID       string         `json:"toolCallId"`
	ToolName         string         `json:"toolName"`
	EffectiveArgs    map[string]any `json:"effectiveArgs"`
	ResultEntryID    string         `json:"resultEntryId"`
	// Replay is "never" | "safe".
	Replay string `json:"replay"`
}

func (r ToolStartedRecord) LaneRecordType() RecordType { return RecordTypeToolStarted }

// EntryOrder is the query ordering vocabulary.
type EntryOrder string

const (
	OrderNewestFirst EntryOrder = "newestFirst"
	OrderOldestFirst EntryOrder = "oldestFirst"
)

// EntryCursor pages queries by sequence.
type EntryCursor struct {
	AfterSeq int64 `json:"afterSeq"`
}

// EntryQuery filters entry reads (pi: EntryQuery).
type EntryQuery struct {
	// Type filters by exact entry discriminant.
	Type *EntryType `json:"type,omitempty"`
	// CustomType filters custom entries by customType (only for Type "custom").
	CustomType *string `json:"customType,omitempty"`
	// Order defaults to newestFirst.
	Order EntryOrder `json:"order,omitempty"`
	// Limit caps the result count; must be a positive integer.
	Limit *int `json:"limit,omitempty"`
	// Cursor pages by sequence.
	Cursor *EntryCursor `json:"cursor,omitempty"`
}

// BranchBounds bounds a branch scan. Default: the whole path, leaf to root.
type BranchBounds struct {
	// Start defaults to the view's lane leaf; mandatory on
	// SessionStorage.FindEntriesOnBranch (storage has no view sugar).
	Start *string `json:"start,omitempty"`
	// StopAtType ends the scan after the first match, inclusive.
	StopAtType *EntryType `json:"stopAtType,omitempty"`
	StopAtID   *string    `json:"stopAtId,omitempty"`
}

// BranchQuery is pi's anonymous `EntryQuery & BranchBounds` intersection on
// Session/SessionTree.findEntriesOnBranch.
type BranchQuery struct {
	EntryQuery
	BranchBounds
}

// BranchEntryQuery is pi's anonymous `EntryQuery & BranchBounds & { start:
// string }` intersection on SessionStorage.findEntriesOnBranch; Start is
// mandatory there.
type BranchEntryQuery struct {
	BranchQuery
	Start string
}

// RecordQuery filters lane-record reads (pi: RecordQuery).
type RecordQuery struct {
	// Lane is an exact lane match; empty queries every lane.
	Lane *string `json:"lane,omitempty"`
	// Type is an exact record discriminant match; empty queries every record
	// type.
	Type *RecordType `json:"type,omitempty"`
	// RunID is the operation identity: matches OperationStartedRecord.id and
	// the runId property of operation-owned records. Records without an
	// operation identity do not match.
	RunID *string `json:"runId,omitempty"`
	// OperationKind is an exact operation intent kind; valid only with Type
	// "operation_started".
	OperationKind *OperationIntentKind `json:"operationKind,omitempty"`
	// AfterSeq is an exclusive chronological lower bound: seq > afterSeq,
	// regardless of order.
	AfterSeq *int64 `json:"afterSeq,omitempty"`
	// Order defaults to "newestFirst".
	Order EntryOrder `json:"order,omitempty"`
	// Limit is a positive maximum number of matching records.
	Limit *int `json:"limit,omitempty"`
}

// SessionMetadata identifies one session.
type SessionMetadata struct {
	ID        string `json:"id"`
	CreatedAt int64  `json:"createdAt"` // Unix ms
	// ParentSessionID links forked sessions to their source; nil when created
	// standalone.
	ParentSessionID *string `json:"parentSessionId,omitempty"`
}

// SessionStats is the usage ledger computed from usage records.
type SessionStats struct {
	MessageCount   int     `json:"messageCount"`
	CachedTokens   int64   `json:"cachedTokens"`
	UncachedTokens int64   `json:"uncachedTokens"`
	TotalTokens    int64   `json:"totalTokens"`
	CostTotal      float64 `json:"costTotal"`
}

// LanePointer points a lane at its current leaf (pi: LanePointer). LeafID is
// nil when the lane is empty.
type LanePointer struct {
	Lane   string  `json:"lane"`
	LeafID *string `json:"leafId"`
}

// LogItem is the unified mutation log entry union (pi: LogItem union),
// collapsed to a single struct with a Kind discriminant. Field ownership by
// kind: Entry for "entry"; Record for "record"; Lane+LeafID for "lane";
// Fact+Name (fact "name") or Fact+TargetID+Label (fact "label") for "fact".
type LogItem struct {
	Kind     string     `json:"kind"` // "entry" | "record" | "lane" | "fact"
	Seq      int64      `json:"seq"`
	Entry    Entry      `json:"entry,omitempty"`
	Record   LaneRecord `json:"record,omitempty"`
	Lane     string     `json:"lane,omitempty"`
	LeafID   *string    `json:"leafId,omitempty"`
	Fact     string     `json:"fact,omitempty"` // "name" | "label"
	Name     *string    `json:"name,omitempty"`
	TargetID string     `json:"targetId,omitempty"`
	Label    *string    `json:"label,omitempty"`
}

// LogOptions pages log reads.
type LogOptions struct {
	AfterSeq *int64 `json:"afterSeq,omitempty"`
	Limit    *int   `json:"limit,omitempty"`
}

// SessionStorage is the durable session-state interface (pi:
// SessionStorage<TMetadata>). All methods resolve errors via the error
// return; methods returning a single entry/record return (nil, nil) for "not
// found" unless documented otherwise.
type SessionStorage interface {
	GetMetadata() (SessionMetadata, error)

	// Lanes
	GetLanes() ([]LanePointer, error)
	CreateLane(lane string, at *string) error
	MoveLane(lane string, to *string) error

	// Entries and records. Callers pass provisioned entries/records with the
	// storage-assigned fields zero; implementations fill Seq, ParentID (by
	// lane leaf), and Timestamp and return the committed value.
	AppendEntry(entry Entry, lane string) (Entry, error)
	AppendRecord(record LaneRecord) (LaneRecord, error)

	// Reads. GetEntry returns (nil, nil) when the id is unknown.
	GetEntry(id string) (Entry, error)
	FindEntries(query *EntryQuery) ([]Entry, error)
	// FindEntriesOnBranch requires Start (as opposed to SessionTree's
	// FindEntriesOnBranch); defaulting to a lane's leaf is view sugar.
	FindEntriesOnBranch(query BranchEntryQuery) ([]Entry, error)
	FindRecords(query *RecordQuery) ([]LaneRecord, error)
	// FindOpenOperations returns unfinished operation starts newest first.
	// Recovery uses limit 2: zero results mean the lane is idle, one means it
	// is suspended, and two mean at least two operations are open, which is
	// corruption. Further results provide no additional recovery state.
	FindOpenOperations(lane string, limit *int) ([]OperationStartedRecord, error)
	GetLog(options *LogOptions) ([]LogItem, error)

	// Global facts
	GetName() (*string, error)
	SetName(name *string) error
	GetLabel(id string) (*string, error)
	SetLabel(id string, label *string) error
	GetStats() (SessionStats, error)
}

// SessionTree is the branch-scoped read/write view of a session (pi:
// SessionTree).
type SessionTree interface {
	GetLeafID() (*string, error)
	GetEntry(id string) (Entry, error)
	GetStats() (SessionStats, error)

	// Global facts. Latest wins; not branch-scoped. "set", not "append":
	// append vocabulary is reserved for tree writes.
	GetName() (*string, error)
	SetName(name *string) error
	GetLabel(targetID string) (*string, error)
	SetLabel(targetID string, label *string) error

	// FindEntries is session-wide, all branches, sequence order.
	FindEntries(query *EntryQuery) ([]Entry, error)
	FindEntry(query *EntryQuery) (Entry, error)

	// FindEntriesOnBranch is branch-scoped: the path from start toward root.
	FindEntriesOnBranch(query *BranchQuery) ([]Entry, error)
	FindEntryOnBranch(query *BranchQuery) (Entry, error)

	// Writes. Resolve on durable acceptance; the returned id is the entry's
	// id (provisioned when the write defers).
	AppendMessage(message agent.AgentMessage) (string, error)
	AppendCustomEntry(customType string, data any) (string, error)
}

// SessionCreateOptions parameterizes session creation.
type SessionCreateOptions struct {
	ID              *string `json:"id,omitempty"`
	ParentSessionID *string `json:"parentSessionId,omitempty"`
}

// ForkScope constants pick the fork copying strategy (pi: ForkOptions union).
const (
	// ForkScopeBranch copies one branch; default when Scope is empty.
	ForkScopeBranch = "branch"
	// ForkScopeTree copies the complete tree with all lanes.
	ForkScopeTree = "tree"
)

// ForkPosition constants pick where a branch fork places its leaf (pi:
// position union member).
const (
	ForkPositionBefore = "before"
	ForkPositionAt     = "at"
)

// ForkOptions collapses pi's ForkOptions union into one struct. Scope "" or
// "branch" copies the branch selected by EntryID/Position: when EntryID is
// nil the default target is main's leaf (position defaults to "at" there,
// "before" when EntryID is set — pi's `position ?? (entryId === undefined ?
// "at" : "before")`); Scope "tree" ignores EntryID/Position.
type ForkOptions struct {
	Scope    string  `json:"scope,omitempty"`
	EntryID  *string `json:"entryId,omitempty"`
	Position string  `json:"position,omitempty"`
}

// ForkCreateOptions is pi's `ForkOptions & TCreateOptions` intersection on
// SessionRepo.fork (TCreateOptions defaults to SessionCreateOptions).
type ForkCreateOptions struct {
	ForkOptions
	SessionCreateOptions
}

// SessionRepo creates, opens, lists, deletes, and forks sessions (pi:
// SessionRepo<TMetadata, TCreateOptions, TListOptions>; generics dropped, see
// file-header note).
type SessionRepo interface {
	Create(options *SessionCreateOptions) (*Session, error)
	// Open opens the session for writing and acquires any backend writer
	// claim.
	Open(metadata SessionMetadata) (*Session, error)
	// List lists session metadata without opening sessions or acquiring
	// writer claims.
	List() ([]SessionMetadata, error)
	Delete(metadata SessionMetadata) error
	Fork(source SessionMetadata, options *ForkCreateOptions) (*Session, error)
}

// SessionErrorCode is the stable error vocabulary (pi: SessionErrorCode).
type SessionErrorCode string

const (
	CodeNotFound          SessionErrorCode = "not_found"
	CodeAlreadyExists     SessionErrorCode = "already_exists"
	CodeInvalidEntry      SessionErrorCode = "invalid_entry"
	CodeInvalidPayload    SessionErrorCode = "invalid_payload"
	CodeInvalidLane       SessionErrorCode = "invalid_lane"
	CodeInvalidQuery      SessionErrorCode = "invalid_query"
	CodeInvalidForkTarget SessionErrorCode = "invalid_fork_target"
	CodeStorage           SessionErrorCode = "storage"
)

// SessionError is the session-layer error (pi: SessionError class).
type SessionError struct {
	Code  SessionErrorCode
	msg   string
	cause error
}

// NewSessionError builds a SessionError; an optional cause chains via Unwrap.
func NewSessionError(code SessionErrorCode, message string, cause ...error) *SessionError {
	e := &SessionError{Code: code, msg: message}
	if len(cause) > 0 {
		e.cause = cause[0]
	}
	return e
}

func (e *SessionError) Error() string {
	if e.cause != nil {
		return fmt.Sprintf("%s (cause: %v)", e.msg, e.cause)
	}
	return e.msg
}

func (e *SessionError) Unwrap() error { return e.cause }
