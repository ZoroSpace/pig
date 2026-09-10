// pi: harness/session/types.ts — the session storage type surface: Entry
// union, the durable operation-state family, Write/commit shapes, scan
// queries, and the Storage / Session / SessionRepo interfaces (v0.85.1
// redesign: 4-entry union + value/list storage + transactional commits).
//
// Type mapping deviations, all declared here:
//
//   - Entry discriminated union → interface + concrete types + DecodeEntry
//     dispatch (per §3.4). NewEntry<TEntry> (TS Omit) has no Go counterpart:
//     callers pass the concrete entry with Seq/Timestamp zero and storage
//     fills them.
//   - SettledAssistantMessage is a type-level refinement of AssistantMessage;
//     no runtime counterpart, not ported.
//   - JsonValue (recursive JSON union from chord, type-only) → any.
//   - Value<T>/ValueList<T> phantom type parameters collapse to non-generic
//     Value / ValueList (phantom types are compile-time only; values cross
//     the Storage interface as any). See values.go.
//   - Storage / Session / SessionReader generic methods (getValue<T> etc.)
//     collapse to erased any-based signatures: Go interfaces cannot declare
//     type-parameterized methods (the TModel generic rule). Typed sugar
//     helpers live in values.go.
//   - Session<TMetadata> drops its generic parameter (per §3.4): the
//     interface exposes GetMetadata() SessionMetadata; JSONL consumers reach
//     JsonlSessionMetadata through the concrete *StorageBackedSession's
//     exported Metadata field plus a type assertion.
//   - pi's async methods return Promises; Go methods return (T, error) and
//     take ctx context.Context (AbortSignal → context rule; the TS
//     `context: Context` parameter carries cancellation/telemetry, mapped to
//     the Go ctx + harness.GetTelemetryContext).
//   - OperationMeta's inline intent union → OperationIntent interface + three
//     concrete kinds (discriminant field "kind"). The 13 flat OperationState
//     leaves are concrete structs with an At discriminant field behind the
//     OperationState marker interface; none are written by pig today (the
//     runtime subsystem is excluded), but the shapes are the durable wire
//     contract and stay 1:1.
//   - DurableStructuralPreparation references CompactionPreparation /
//     BranchPreparation type-only from harness/compaction, which imports
//     this package — Go would cycle. The referenced members are plain
//     AgentMessage slices / scalars, so they are inlined here (comment marks
//     the provenance of each field).
//   - "returns undefined" reads as (nil, nil) / nil; documented per method.
package session

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/ai/types"
)

// JsonValue (pi: recursive JSON union re-exported from chord) maps to any.
type JsonValue = any

// EntryType is the closed entry discriminant vocabulary (pi: EntryType).
type EntryType string

const (
	EntryTypeMessage        EntryType = "message"
	EntryTypeCompaction     EntryType = "compaction"
	EntryTypeBranchSummary  EntryType = "branch_summary"
	EntryTypeCustom         EntryType = "custom"
	entryTypeLegacyV3Unkown           = "" // guard: legacy v3 has more types; see jsonl/legacy_v3.go
)

// EntryBase carries the storage-assigned fields shared by every entry (pi:
// EntryBase). ID is caller-supplied (the session's IdGenerator); ParentID is
// the branch tip the entry appends to; Seq and Timestamp are storage-
// assigned and must be left zero by callers. CustomType is nominally shared
// across the union (pi declares it on EntryBase) but is only populated on
// custom entries.
type EntryBase struct {
	ID         string    `json:"id"`
	ParentID   *string   `json:"parentId"`
	Seq        int64     `json:"seq"`       // storage-assigned; zero on NewEntry input
	Timestamp  int64     `json:"timestamp"` // Unix ms, storage-assigned; zero on NewEntry input
	Type       EntryType `json:"type"`
	CustomType *string   `json:"customType,omitempty"`
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

type CompactionEntry struct {
	EntryBase
	Summary      string               `json:"summary"`
	RetainedTail []agent.AgentMessage `json:"retainedTail"`
	TokensBefore int64                `json:"tokensBefore"`
	Details      JsonValue            `json:"details,omitempty"`
	Usage        *types.Usage         `json:"usage,omitempty"`
	// FromHook reports whether the compaction was triggered by the
	// session_before_compact hook. Required on the wire (pi: fromHook:
	// boolean), never omitted.
	FromHook bool `json:"fromHook"`
}

func (e CompactionEntry) EntryType() EntryType { return EntryTypeCompaction }

type BranchSummaryEntry struct {
	EntryBase
	// FromID is the source branch tip the summary was taken from; nil
	// encodes pi's fromId: null (root source).
	FromID  *string      `json:"fromId"`
	Summary string       `json:"summary"`
	Details JsonValue    `json:"details,omitempty"`
	Usage   *types.Usage `json:"usage,omitempty"`
	// FromHook is required on the wire (pi: fromHook: boolean).
	FromHook bool `json:"fromHook"`
}

func (e BranchSummaryEntry) EntryType() EntryType { return EntryTypeBranchSummary }

type CustomEntry struct {
	EntryBase
	Data JsonValue `json:"data,omitempty"`
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

// EntryProjector converts an application-defined custom entry into model
// context (pi: EntryProjector). pi's Promise return collapses to a
// synchronous callback (§3.4); returning nil means "no contribution", a
// non-nil error propagates (pi's rejected promise).
type EntryProjector func(entry CustomEntry, ctx context.Context) ([]agent.AgentMessage, error)

// LaneConfiguration is the durable per-lane model configuration (pi:
// LaneConfiguration), reconstructed from legacy model_change entries during
// v3 migration and written by the (excluded) runtime.
type LaneConfiguration struct {
	Model struct {
		Provider string `json:"provider"`
		ModelID  string `json:"modelId"`
	} `json:"model"`
	ThinkingLevel   string   `json:"thinkingLevel"`
	ActiveToolNames []string `json:"activeToolNames"`
}

// OperationMeta records why and from where one operation started (pi:
// OperationMeta).
type OperationMeta struct {
	OperationID string          `json:"operationId"`
	Lane        string          `json:"lane"`
	SourceTipID *string         `json:"sourceTipId"`
	StartedAt   int64           `json:"startedAt"`
	Intent      OperationIntent `json:"intent"`
}

// OperationIntentKind discriminates the intent union (pi: inline union on
// `intent.kind`).
type OperationIntentKind string

const (
	OperationIntentRun        OperationIntentKind = "run"
	OperationIntentCompaction OperationIntentKind = "compaction"
	OperationIntentNavigation OperationIntentKind = "navigation"
)

// OperationIntent is the intent union of OperationMeta (pi: inline union).
type OperationIntent interface {
	OperationIntentKind() OperationIntentKind
}

type RunIntent struct {
	Kind           OperationIntentKind `json:"kind"`
	PromptEntryIDs []string            `json:"promptEntryIds"`
}

func (i RunIntent) OperationIntentKind() OperationIntentKind { return i.Kind }

type CompactionIntent struct {
	Kind               OperationIntentKind `json:"kind"`
	CustomInstructions *string             `json:"customInstructions,omitempty"`
}

func (i CompactionIntent) OperationIntentKind() OperationIntentKind { return i.Kind }

type NavigationIntent struct {
	Kind               OperationIntentKind `json:"kind"`
	TargetID           *string             `json:"targetId"` // null allowed
	Summarize          bool                `json:"summarize"`
	Label              *string             `json:"label,omitempty"`
	CustomInstructions *string             `json:"customInstructions,omitempty"`
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
		if err := json.Unmarshal(data, &i); err != nil {
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

// Control is the cancellation state of one running operation (pi: Control).
type Control struct {
	Status string `json:"status"` // "running" | "cancel_requested"
	// RequestedAt is set only when Status is "cancel_requested".
	RequestedAt *int64 `json:"requestedAt,omitempty"`
}

// OperationError is the structured failure payload of a terminal operation
// (pi: OperationError).
type OperationError struct {
	Code    string    `json:"code"`
	Message string    `json:"message"`
	Details JsonValue `json:"details,omitempty"`
}

// TerminalStatus is the terminal operation outcome vocabulary (pi:
// TerminalStatus).
type TerminalStatus string

const (
	TerminalStatusCompleted TerminalStatus = "completed"
	TerminalStatusDeclined  TerminalStatus = "declined"
	TerminalStatusAborted   TerminalStatus = "aborted"
	TerminalStatusFailed    TerminalStatus = "failed"
)

// OperationResultRecord is an immutable lane-lived observation record written
// by one terminal transaction (pi: OperationResultRecord).
type OperationResultRecord struct {
	OperationID string `json:"operationId"`
	// Kind mirrors OperationMeta.intent.kind ("run" | "compaction" |
	// "navigation").
	Kind      OperationIntentKind `json:"kind"`
	Status    TerminalStatus      `json:"status"`
	Error     *OperationError     `json:"error,omitempty"`
	FromTipID *string             `json:"fromTipId"`
	TipID     *string             `json:"tipId"`
	StartedAt int64               `json:"startedAt"`
	EndedAt   int64               `json:"endedAt"`
}

// Continuation discriminates the checkpoint continuation union (pi:
// Continuation).
type Continuation struct {
	// Kind is "need_assistant" | "may_finish".
	Kind string `json:"kind"`
	// OverflowRecoveryUsed is set only for "need_assistant".
	OverflowRecoveryUsed *bool `json:"overflowRecoveryUsed,omitempty"`
	// IncludeFinalAssistant is set only for "may_finish".
	IncludeFinalAssistant *bool `json:"includeFinalAssistant,omitempty"`
}

// CheckpointData is the checkpoint payload (pi: CheckpointData); the flat
// leaf literal replaces the old nested phase tag.
type CheckpointData struct {
	Continuation   Continuation `json:"continuation"`
	TriggerEntryID string       `json:"triggerEntryId"`
}

// InboxItemKind is the queue vocabulary (pi: InboxItemKind).
type InboxItemKind string

const (
	InboxItemSteer    InboxItemKind = "steer"
	InboxItemFollowUp InboxItemKind = "followUp"
	InboxItemNextRun  InboxItemKind = "nextRun"
	InboxItemWrite    InboxItemKind = "write"
)

// InboxItem is one queued item on a lane (pi: InboxItem).
type InboxItem struct {
	EntryID string        `json:"entryId"`
	Kind    InboxItemKind `json:"kind"`
}

// NormalizedRetryPolicy is the resolved retry configuration (pi:
// NormalizedRetryPolicy).
type NormalizedRetryPolicy struct {
	MaxAttempts int `json:"maxAttempts"`
	BaseDelayMs int `json:"baseDelayMs"`
}

// GenerationContext carries the full assistant-generation inputs (pi:
// GenerationContext).
type GenerationContext struct {
	StepID               string                `json:"stepId"`
	TriggerEntryID       string                `json:"triggerEntryId"`
	Configuration        LaneConfiguration     `json:"configuration"`
	StreamOptions        json.RawMessage       `json:"streamOptions"`
	RetryPolicy          NormalizedRetryPolicy `json:"retryPolicy"`
	OverflowRecoveryUsed bool                  `json:"overflowRecoveryUsed"`
}

// ToolCallSource locates one tool call in its assistant message (pi:
// ToolCallSource). SourceIndex is the zero-based index in the assistant
// message's complete content array, not a filtered tool-call ordinal.
type ToolCallSource struct {
	SourceIndex   int    `json:"sourceIndex"`
	ResultEntryID string `json:"resultEntryId"`
}

// ToolCall is one durable tool-call state (pi: ToolCall). Status vocabulary:
// "planned" | "effect_pending" | "outcome_ready" | "completed".
type ToolCall struct {
	ToolCallSource
	Status string `json:"status"`
	// Replay is set only for "effect_pending" ("never" | "safe").
	Replay *string `json:"replay,omitempty"`
	// Terminate is set for "outcome_ready" and "completed".
	Terminate *bool `json:"terminate,omitempty"`
}

// ToolBatch is the nested child collection state machine of the tools leaf
// (pi: ToolBatch).
type ToolBatch struct {
	AssistantEntryID string            `json:"assistantEntryId"`
	Configuration    LaneConfiguration `json:"configuration"`
	TurnID           string            `json:"turnId"`
	Calls            []ToolCall        `json:"calls"`
}

// SummaryContext carries the summary-generation inputs (pi: SummaryContext).
type SummaryContext struct {
	ResultEntryID string                `json:"resultEntryId"`
	Configuration LaneConfiguration     `json:"configuration"`
	StreamOptions json.RawMessage       `json:"streamOptions"`
	RetryPolicy   NormalizedRetryPolicy `json:"retryPolicy"`
}

// RunSettings is the uniform settings snapshot carried by every operation
// leaf (pi: RunSettings).
type RunSettings struct {
	// Compaction mirrors harness/compaction's CompactionSettings; kept as
	// json.RawMessage to avoid the import cycle (the excluded runtime owns
	// the typed shape).
	Compaction    json.RawMessage `json:"compaction"`
	SteeringMode  string          `json:"steeringMode"`
	FollowUpMode  string          `json:"followUpMode"`
	ToolExecution string          `json:"toolExecution"`
}

// OperationScope is the uniform scope carried by every operation leaf (pi:
// OperationScope).
type OperationScope struct {
	Control                Control     `json:"control"`
	Settings               RunSettings `json:"settings"`
	LatestAssistantEntryID *string     `json:"latestAssistantEntryId"`
}

// RetryWait is the shared backoff data for every retry-wait leaf (pi:
// RetryWait).
type RetryWait struct {
	NextAttempt  int    `json:"nextAttempt"`
	NotBefore    int64  `json:"notBefore"`
	ErrorMessage string `json:"errorMessage"`
}

// ResultBoundary is the result-boundary union of a summary task (pi:
// ResultBoundary). Kind: "resume_checkpoint" | "finish" | "commit_navigation".
type ResultBoundary struct {
	Kind string `json:"kind"`
	// ResumeAfter is set only for "resume_checkpoint".
	ResumeAfter *CheckpointData `json:"resumeAfter,omitempty"`
	// TargetID is set only for "commit_navigation".
	TargetID *string `json:"targetId,omitempty"`
	// Label is set only for "commit_navigation".
	Label *string `json:"label,omitempty"`
}

// SummaryTask identifies one pending summary (pi: SummaryTask). Reason
// vocabulary: "manual" | "threshold" | "overflow".
type SummaryTask struct {
	TaskID             string         `json:"taskId"`
	Reason             *string        `json:"reason,omitempty"`
	CustomInstructions *string        `json:"customInstructions,omitempty"`
	Boundary           ResultBoundary `json:"boundary"`
}

// The 13 flat durable operation-state leaves (pi: OperationState union).
// Exactly one At discriminant per leaf; field comments mark the members-only
// payloads.

type StartingOperation struct {
	OperationScope
	At string `json:"at"` // "starting"
}

type CheckpointOperation struct {
	OperationScope
	CheckpointData
	At string `json:"at"` // "checkpoint"
}

type AssistantReadyOperation struct {
	OperationScope
	GenerationContext GenerationContext `json:"generationContext"`
	NextAttempt       int               `json:"nextAttempt"`
	At                string            `json:"at"` // "assistant.ready"
}

type AssistantEffectPendingOperation struct {
	OperationScope
	GenerationContext   GenerationContext `json:"generationContext"`
	Attempt             int               `json:"attempt"`
	ResponseEntryID     string            `json:"responseEntryId"`
	UsageID             string            `json:"usageId"`
	IntendedOutputLimit int64             `json:"intendedOutputLimit"`
	ContextWindow       int64             `json:"contextWindow"`
	At                  string            `json:"at"` // "assistant.effect_pending"
}

type AssistantRetryWaitOperation struct {
	OperationScope
	GenerationContext GenerationContext `json:"generationContext"`
	RetryWait
	At string `json:"at"` // "assistant.retry_wait"
}

type ToolsOperation struct {
	OperationScope
	Batch ToolBatch `json:"batch"`
	At    string    `json:"at"` // "tools"
}

type DeferredSuspendedOperation struct {
	OperationScope
	StepID        string            `json:"stepId"`
	SourceEntryID string            `json:"sourceEntryId"`
	Poll          int               `json:"poll"`
	Configuration LaneConfiguration `json:"configuration"`
	StreamOptions json.RawMessage   `json:"streamOptions"`
	At            string            `json:"at"` // "deferred.suspended"
}

type DeferredEffectPendingOperation struct {
	OperationScope
	StepID          string            `json:"stepId"`
	SourceEntryID   string            `json:"sourceEntryId"`
	Poll            int               `json:"poll"`
	Configuration   LaneConfiguration `json:"configuration"`
	StreamOptions   json.RawMessage   `json:"streamOptions"`
	ResponseEntryID string            `json:"responseEntryId"`
	UsageID         string            `json:"usageId"`
	At              string            `json:"at"` // "deferred.effect_pending"
}

type SummaryDecidingOperation struct {
	OperationScope
	Task SummaryTask `json:"task"`
	At   string      `json:"at"` // "summary.deciding"
}

type SummaryReadyOperation struct {
	OperationScope
	Task           SummaryTask    `json:"task"`
	SummaryContext SummaryContext `json:"summaryContext"`
	NextAttempt    int            `json:"nextAttempt"`
	At             string         `json:"at"` // "summary.ready"
}

type SummaryEffectPendingOperation struct {
	OperationScope
	Task           SummaryTask    `json:"task"`
	SummaryContext SummaryContext `json:"summaryContext"`
	Attempt        int            `json:"attempt"`
	// Request is { index, usageId }; nil when absent.
	Request  *EffectPendingRequest `json:"request,omitempty"`
	UsageIDs []string              `json:"usageIds"`
	At       string                `json:"at"` // "summary.effect_pending"
}

// EffectPendingRequest is pi's inline `{ index: number; usageId: string }`.
type EffectPendingRequest struct {
	Index   int    `json:"index"`
	UsageID string `json:"usageId"`
}

type SummaryRetryWaitOperation struct {
	OperationScope
	Task           SummaryTask    `json:"task"`
	SummaryContext SummaryContext `json:"summaryContext"`
	RetryWait
	At string `json:"at"` // "summary.retry_wait"
}

type NavigationReadyToCommitOperation struct {
	OperationScope
	At string `json:"at"` // "navigation.ready_to_commit"
	// TargetID may target the branch root (null) for unsummarized
	// navigation.
	TargetID *string `json:"targetId"`
	Label    *string `json:"label,omitempty"`
}

// OperationState is the flat durable operation-state union (pi:
// OperationState): exactly 13 family-neutral dispatcher leaves.
type OperationState interface {
	OperationAt() string
}

func (s StartingOperation) OperationAt() string                { return s.At }
func (s CheckpointOperation) OperationAt() string              { return s.At }
func (s AssistantReadyOperation) OperationAt() string          { return s.At }
func (s AssistantEffectPendingOperation) OperationAt() string  { return s.At }
func (s AssistantRetryWaitOperation) OperationAt() string      { return s.At }
func (s ToolsOperation) OperationAt() string                   { return s.At }
func (s DeferredSuspendedOperation) OperationAt() string       { return s.At }
func (s DeferredEffectPendingOperation) OperationAt() string   { return s.At }
func (s SummaryDecidingOperation) OperationAt() string         { return s.At }
func (s SummaryReadyOperation) OperationAt() string            { return s.At }
func (s SummaryEffectPendingOperation) OperationAt() string    { return s.At }
func (s SummaryRetryWaitOperation) OperationAt() string        { return s.At }
func (s NavigationReadyToCommitOperation) OperationAt() string { return s.At }

// OperationScopeOf copies only the uniform operation scope when constructing
// a successor leaf (pi: operationScopeOf).
func OperationScopeOf(state OperationState) OperationScope {
	switch s := state.(type) {
	case StartingOperation:
		return s.OperationScope
	case CheckpointOperation:
		return s.OperationScope
	case AssistantReadyOperation:
		return s.OperationScope
	case AssistantEffectPendingOperation:
		return s.OperationScope
	case AssistantRetryWaitOperation:
		return s.OperationScope
	case ToolsOperation:
		return s.OperationScope
	case DeferredSuspendedOperation:
		return s.OperationScope
	case DeferredEffectPendingOperation:
		return s.OperationScope
	case SummaryDecidingOperation:
		return s.OperationScope
	case SummaryReadyOperation:
		return s.OperationScope
	case SummaryEffectPendingOperation:
		return s.OperationScope
	case SummaryRetryWaitOperation:
		return s.OperationScope
	case NavigationReadyToCommitOperation:
		return s.OperationScope
	}
	return OperationScope{}
}

// Operation pairs the durable meta with the durable state (pi: Operation).
type Operation struct {
	Meta  OperationMeta  `json:"meta"`
	State OperationState `json:"state"`
}

// LaneState is the idle/queueing state of one lane (pi: LaneState).
type LaneState struct {
	CurrentOperationID *string     `json:"currentOperationId"`
	LastOperationID    *string     `json:"lastOperationId"`
	Inbox              []InboxItem `json:"inbox"`
}

// PendingEntry is a message or custom payload queued for a later commit (pi:
// PendingEntry). Kind: "message" | "custom".
type PendingEntry struct {
	Kind    string             `json:"type"` // pi discriminates on "type"
	Message agent.AgentMessage `json:"message,omitempty"`
	// CustomType is set only for "custom".
	CustomType string    `json:"customType,omitempty"`
	Payload    JsonValue `json:"payload,omitempty"`
}

// DurableFileOperations accumulates file paths touched by summarized
// messages (pi: DurableFileOperations).
type DurableFileOperations struct {
	Read    []string `json:"read"`
	Written []string `json:"written"`
	Edited  []string `json:"edited"`
}

// DurableStructuralPreparation is the durable preparation payload of a
// compaction or branch-summary task (pi: DurableStructuralPreparation).
// Kind: "compaction" | "branch_summary". Fields inline the type-only
// references into harness/compaction (import-cycle note in the file header):
// MessagesToSummarize/TurnPrefixMessages/RetainedTail/Messages are
// AgentMessage slices from CompactionPreparation/BranchPreparation.
type DurableStructuralPreparation struct {
	Kind string `json:"kind"`
	// Compaction members:
	MessagesToSummarize []agent.AgentMessage `json:"messagesToSummarize,omitempty"`
	TurnPrefixMessages  []agent.AgentMessage `json:"turnPrefixMessages,omitempty"`
	RetainedTail        []agent.AgentMessage `json:"retainedTail,omitempty"`
	IsSplitTurn         bool                 `json:"isSplitTurn,omitempty"`
	TokensBefore        int64                `json:"tokensBefore,omitempty"`
	PreviousSummary     *string              `json:"previousSummary,omitempty"`
	// Compaction settings snapshot (pi: settings: CompactionSettings); kept
	// raw to avoid the import cycle.
	Settings json.RawMessage `json:"settings,omitempty"`
	// Branch-summary members:
	Messages    []agent.AgentMessage  `json:"messages,omitempty"`
	FileOps     DurableFileOperations `json:"fileOps"`
	TotalTokens int64                 `json:"totalTokens,omitempty"`
}

// UsageRow is one usage ledger row (pi: UsageRow).
type UsageRow struct {
	ID         string      `json:"id"`
	Seq        int64       `json:"seq"` // storage-assigned; zero on UsageWrite input
	Usage      types.Usage `json:"usage"`
	EntryID    *string     `json:"entryId,omitempty"`
	Adjustment bool        `json:"adjustment"`
	Details    JsonValue   `json:"details,omitempty"`
}

// Write is one mutation inside a transaction (pi: Write union). Concrete
// kinds: *EntryWrite, *UsageWrite, *ValueSetWrite, *ValueDeleteWrite,
// *ListAppendWrite, *ListDeleteWrite.
type Write interface {
	writeKind() string
}

// EntryWrite appends one provisioned entry (pi: EntryWrite).
type EntryWrite struct {
	Entry Entry `json:"entry"`
}

func (w EntryWrite) writeKind() string { return "entry" }

// UsageWrite appends one usage row (pi: UsageWrite).
type UsageWrite struct {
	Row UsageRow `json:"row"`
}

func (w UsageWrite) writeKind() string { return "usage" }

// CommitResult reports the assigned sequences of one commit (pi:
// CommitResult).
type CommitResult struct {
	FirstSeq  int64        `json:"firstSeq"`
	Seqs      []int64      `json:"seqs"`
	Timestamp int64        `json:"timestamp"`
	Stats     SessionStats `json:"stats"`
}

// EntryStructure is the structural projection of an entry for cheap branch
// scans (pi: EntryStructure).
type EntryStructure struct {
	ID         string    `json:"id"`
	ParentID   *string   `json:"parentId"`
	Seq        int64     `json:"seq"`
	Timestamp  int64     `json:"timestamp"`
	Type       EntryType `json:"type"`
	CustomType *string   `json:"customType,omitempty"`
}

// EntryCursor pages branch scans by sequence (pi: EntryCursor).
type EntryCursor struct {
	Seq int64 `json:"seq"`
}

// BranchScan queries one branch path (pi: BranchScan).
type BranchScan struct {
	Start      *string    `json:"start,omitempty"`
	StopAtType *EntryType `json:"stopAtType,omitempty"`
	StopAtID   *string    `json:"stopAtId,omitempty"`
	Type       *EntryType `json:"type,omitempty"`
	CustomType *string    `json:"customType,omitempty"`
	// Order is "newestFirst" | "oldestFirst"; the zero value means
	// newestFirst (Branch.FindEntries defaults explicitly).
	Order  string       `json:"order,omitempty"`
	Limit  *int         `json:"limit,omitempty"`
	Cursor *EntryCursor `json:"cursor,omitempty"`
}

// StorageBranchScan is BranchScan with a mandatory start (pi:
// StorageBranchScan).
type StorageBranchScan struct {
	BranchScan
	Start string
}

// EntryScan queries entries by sequence window (pi: EntryScan). Order:
// "asc" | "desc".
type EntryScan struct {
	Type       *EntryType `json:"type,omitempty"`
	CustomType *string    `json:"customType,omitempty"`
	FromSeq    *int64     `json:"fromSeq,omitempty"`
	ToSeq      *int64     `json:"toSeq,omitempty"`
	Order      string     `json:"order,omitempty"`
	Limit      *int       `json:"limit,omitempty"`
}

// UsageScan queries usage rows by sequence window (pi: UsageScan).
type UsageScan struct {
	FromSeq *int64 `json:"fromSeq,omitempty"`
	ToSeq   *int64 `json:"toSeq,omitempty"`
	Order   string `json:"order,omitempty"`
	Limit   *int   `json:"limit,omitempty"`
}

// SessionStats is the aggregate usage ledger (pi: SessionStats).
type SessionStats struct {
	MessageCount int         `json:"messageCount"`
	Usage        types.Usage `json:"usage"`
}

// Storage is the durable session-state interface (pi: Storage). All methods
// take ctx (pi's `context: Context`); single-entry lookups return (nil, nil)
// for "not found".
type Storage interface {
	Commit(writes []Write, ctx context.Context) (CommitResult, error)
	GetEntries(ids []string, ctx context.Context) (map[string]Entry, error)
	GetValue(address Value, ctx context.Context) (*StoredValue, error)
	ScanValues(prefix Value, ctx context.Context) ([]StoredValue, error)
	ReadList(address ValueList, options *ListReadOptions, ctx context.Context) ([]ListElement, error)
	ScanBranch(query StorageBranchScan, ctx context.Context) ([]Entry, error)
	ScanBranchStructure(query StorageBranchScan, ctx context.Context) ([]EntryStructure, error)
	ScanEntries(query EntryScan, ctx context.Context) ([]Entry, error)
	ScanUsage(query UsageScan, ctx context.Context) ([]UsageRow, error)
	GetStats(ctx context.Context) (SessionStats, error)
	Close(ctx context.Context) error
}

// SessionMetadata identifies one session (pi: SessionMetadata).
type SessionMetadata struct {
	ID              string  `json:"id"`
	CreatedAt       int64   `json:"createdAt"` // Unix ms
	StorageVersion  int     `json:"storageVersion"`
	Cwd             *string `json:"cwd,omitempty"`
	ParentSessionID *string `json:"parentSessionId,omitempty"`
	// LegacyParentSessionPath survives when the legacy v3 parent file could
	// not be resolved to an id.
	LegacyParentSessionPath *string `json:"legacyParentSessionPath,omitempty"`
}

// IdGenerator produces entry/session ids (pi: IdGenerator). timestampMs
// stamps the id's time component; 0 means the current time (pi's optional
// parameter).
type IdGenerator interface {
	Next(timestampMs int64) string
}

// EntryQuery filters session-wide entry reads (pi: EntryQuery). Order:
// "asc" | "desc" (default "desc" at the Session level).
type EntryQuery struct {
	Type       *EntryType   `json:"type,omitempty"`
	CustomType *string      `json:"customType,omitempty"`
	Order      string       `json:"order,omitempty"`
	Limit      *int         `json:"limit,omitempty"`
	Cursor     *EntryCursor `json:"cursor,omitempty"`
}

// SessionReader is the read capability shared by Session and SessionMutation
// (pi: SessionReader).
type SessionReader interface {
	GetEntries(ids []string, ctx context.Context) (map[string]Entry, error)
	GetStats(ctx context.Context) (SessionStats, error)
	GetValue(address Value, ctx context.Context) (*StoredValue, error)
	ScanValues(prefix Value, ctx context.Context) ([]StoredValue, error)
	ReadList(address ValueList, options *ListReadOptions, ctx context.Context) ([]ListElement, error)
	// ScanBranch scans a branch from an explicit entry while the capability
	// remains valid.
	ScanBranch(query StorageBranchScan, ctx context.Context) ([]Entry, error)
}

// SessionMutation is the exclusive keyless mutation barrier for one Session
// (pi: SessionMutation). Exactly zero or one commit attempt; End waits for
// the commit, invalidates the capability, and releases the barrier.
type SessionMutation interface {
	SessionReader
	Commit(writes []Write, ctx context.Context) (CommitResult, error)
	End(ctx context.Context) error
}

// SessionMutator is the callback-scoped mutation capability without the
// authority to release the barrier (pi: SessionMutator).
type SessionMutator = SessionMutation

// SessionMutationCallback is pi's SessionMutationCallback<T>; the generic
// result collapses to any (Go closures capture typed results directly).
type SessionMutationCallback func(mutator SessionMutator, ctx context.Context) (any, error)

// Branch is a branch-scoped read/write view (pi: Branch).
type Branch interface {
	Name() string
	GetTipID(ctx context.Context) (*string, error)
	FindEntries(query *BranchScan, ctx context.Context) ([]Entry, error)
	FindEntry(query *BranchScan, ctx context.Context) (Entry, error)
	AppendMessage(message agent.AgentMessage, ctx context.Context) (string, error)
	AppendCustomEntry(customType string, data JsonValue, ctx context.Context) (string, error)
}

// Session is the durable session handle (pi: Session).
type Session interface {
	SessionReader
	GetMetadata() SessionMetadata
	IdGenerator() IdGenerator
	GetEntry(id string, ctx context.Context) (Entry, error)
	GetName(ctx context.Context) (*string, error)
	GetLabel(targetID string, ctx context.Context) (*string, error)
	FindEntries(query *EntryQuery, ctx context.Context) ([]Entry, error)
	FindEntry(query *EntryQuery, ctx context.Context) (Entry, error)
	Branch(name string, ctx context.Context) (Branch, error)
	CreateBranch(name string, at *string, ctx context.Context) (Branch, error)
	BeginMutation(ctx context.Context) (SessionMutation, error)
	// Mutate runs the callback holding the exclusive mutation line. Calling
	// public Session writers from inside the callback queues them behind it;
	// awaiting that nested writer deadlocks. Use the supplied mutator for
	// the callback's sole commit.
	Mutate(mutation SessionMutationCallback, ctx context.Context) (any, error)
	SetValue(address Value, next any, ctx context.Context) error
	DeleteValue(address Value, ctx context.Context) error
	AppendList(address ValueList, element any, ctx context.Context) error
	DeleteList(address ValueList, ctx context.Context) error
	SetName(name *string, ctx context.Context) error
	SetLabel(targetID string, label *string, ctx context.Context) error
	Close(ctx context.Context) error
}

// SessionCreateOptions parameterizes session creation (pi:
// SessionCreateOptions).
type SessionCreateOptions struct {
	ID              *string `json:"id,omitempty"`
	ParentSessionID *string `json:"parentSessionId,omitempty"`
}

// ForkScope constants pick the fork copying strategy (pi: ForkOptions
// union).
const (
	// ForkScopeBranch copies one path from a configured source lane.
	ForkScopeBranch = "branch"
	// ForkScopeTree copies the whole conversation tree and every branch tip.
	ForkScopeTree = "tree"
)

// ForkPosition constants pick where a branch fork places its tip (pi:
// position union member). Default includes the selected entry ("at").
const (
	ForkPositionBefore = "before"
	ForkPositionAt     = "at"
)

// ForkOptions collapses pi's ForkOptions union into one struct. Scope ""
// behaves as "branch" (pi's discriminated union has no default; the repo
// layer validates). For branch scope: Branch selects the source branch,
// EntryID selects the entry on its tip ancestry (default: current tip), and
// Position selects whether the fork includes the selected entry ("at",
// default) or stops at its parent ("before"). ID optionally names the
// destination session.
type ForkOptions struct {
	Scope    string  `json:"scope,omitempty"`
	Branch   string  `json:"branch,omitempty"`
	EntryID  *string `json:"entryId,omitempty"`
	Position string  `json:"position,omitempty"`
	ID       *string `json:"id,omitempty"`
}

// SessionRepo creates, opens, lists, deletes, and forks sessions (pi:
// SessionRepo<TMetadata, TCreateOptions, TListOptions>). Go generic
// interfaces carry the instantiation: the memory backend instantiates
// SessionRepo[SessionMetadata, SessionCreateOptions, any] and the JSONL
// backend SessionRepo[JsonlSessionMetadata, JsonlSessionCreateOptions,
// *JsonlSessionListOptions] — exactly pi's concrete TMetadata shapes.
// Session itself stays non-generic (see the Session interface note): the
// concrete metadata reaches consumers through the repo's typed List return
// and StorageBackedSession.TypedMetadata.
type SessionRepo[TMetadata any, TCreate any, TList any] interface {
	Create(options TCreate, ctx context.Context) (Session, error)
	Open(metadata TMetadata, ctx context.Context) (Session, error)
	List(options TList, ctx context.Context) ([]TMetadata, error)
	Delete(metadata TMetadata, ctx context.Context) error
	Fork(source TMetadata, options ForkOptions, ctx context.Context) (Session, error)
}

// CustomTypeOrValue returns the custom entry type value (nil-safe accessor
// over EntryBase.CustomType).
func (b *EntryBase) CustomTypeOrValue() string {
	if b.CustomType == nil {
		return ""
	}
	return *b.CustomType
}
