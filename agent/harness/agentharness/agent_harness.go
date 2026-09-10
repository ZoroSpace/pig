// Package agentharness corresponds to pi's
// packages/agent/src/harness/agent-harness.ts.
//
// Package location exception (same precedent as agent/harness/reducer): pi's
// agent-harness.ts lives in the harness root and consumes the session types,
// but pig's session package imports harness for the capability types, so
// placing this file in package harness would form an import cycle. It lands
// in this leaf subpackage with pi's public naming preserved.
//
// pi: src/harness/agent-harness.ts — the AgentHarness assembly scaffold.
//
// Segment deviations:
//   - TaggedError classes → concrete structs embedding harness.TaggedErrorBase
//     (see result.go); each carries the pi payload fields. Error unions
//     (RunRejected, CompactionRejected, ...) are the error return channel:
//     callers type-switch on the concrete structs (pi: Result union).
//   - Result<...> unions collapse to (T, error): RunResult → (*RunSuccess,
//     error) and so on. CancelQueuedResult's outcome union becomes a string
//     field; ResumeOutcome's three intersections become one struct with
//     exactly one outcome pointer set.
//   - RunOutcome collides in package harness with events.go's RunOutcome
//     (the run_end event enum, same name in pi's events.ts). The
//     agent-harness unions are therefore named RunInvocationOutcome /
//     CompactionInvocationOutcome / NavigationInvocationOutcome — pi's span
//     descriptions call these "invocations" too.
//   - Overloaded methods collapse: prompt(text, images?) / prompt(message |
//     messages) → variadic Prompt; the (text, images) sugar is caller-side
//     UserMessage construction. steer/followUp/nextRun collapse the same
//     way. skill/promptFromTemplate/compact/recordUsage keep their optional
//     members as "" = undefined (compaction precedent) or pointers.
//   - TS async methods → synchronous methods (per the callback rule); pi
//     passes no abort signals on AgentLane, so no ctx parameters are added
//     (§3.4). Unavailable operations surface their throw as a non-nil error.
//   - `object | (() => object | Promise<object>)` option unions → paired
//     fields (ToolContext/ToolContextProvider, SystemPrompt/
//     SystemPromptProvider); exactly one may be set.
//   - pi re-declares interface Events in agent-harness.ts; package agentharness
//     already uses that name for events.ts's port, so the local surface is
//     named HarnessEvents. Its On returns (func(), error) because
//     UnavailableRegistry.on throws (HarnessNotImplemented/HarnessClosed);
//     the event-bus implementation never errors.
//   - watch()'s WatchHandle<TSnapshot> is structurally the same shape as
//     events.go's generic WatchHandle (Snapshot field + Start +
//     Unsubscribe), so it is reused.
//   - Mutable harness state is guarded by a single mutex (pig addition, same
//     rationale as the Agent class: TS relies on the single-threaded event
//     loop).
package agentharness

import (
	"context"
	"fmt"
	"sync"

	"github.com/samber/lo"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/agent/harness"
	"github.com/ZoroSpace/pig/agent/harness/session"
	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/telemetry"
)

// OperationKind names the admitted operation kinds (pi: the
// operationKind: "run" | "compaction" | "navigation" literal unions).
type OperationKind = session.OperationIntentKind

const (
	OperationKindRun        = session.OperationIntentRun
	OperationKindCompaction = session.OperationIntentCompaction
	OperationKindNavigation = session.OperationIntentNavigation
)

// LaneBusy is raised when a lane already hosts an active operation (pi:
// LaneBusy).
type LaneBusy struct {
	harness.TaggedErrorBase
	Lane          string `json:"lane"`
	OperationID   string `json:"operationId"`
	OperationKind string `json:"operationKind"` // "run" | "compaction" | "navigation"
}

// NewLaneBusy builds the error (pi: the class constructor payload).
func NewLaneBusy(lane string, operationID string, operationKind string, message string) *LaneBusy {
	return &LaneBusy{TaggedErrorBase: harness.TaggedErrorBase{Tag: "LaneBusy", Message: message}, Lane: lane, OperationID: operationID, OperationKind: operationKind}
}

// MissingIdentities is raised when suspended work references tools or models
// the harness cannot resolve (pi: MissingIdentities).
type MissingIdentities struct {
	harness.TaggedErrorBase
	Lane   string   `json:"lane"`
	Tools  []string `json:"tools"`
	Models []string `json:"models"`
}

func NewMissingIdentities(lane string, tools []string, models []string, message string) *MissingIdentities {
	return &MissingIdentities{TaggedErrorBase: harness.TaggedErrorBase{Tag: "MissingIdentities", Message: message}, Lane: lane, Tools: tools, Models: models}
}

// NoActiveRun is raised when a run-scoped action has no active run (pi:
// NoActiveRun).
type NoActiveRun struct {
	harness.TaggedErrorBase
	Lane string `json:"lane"`
}

func NewNoActiveRun(lane string, message string) *NoActiveRun {
	return &NoActiveRun{TaggedErrorBase: harness.TaggedErrorBase{Tag: "NoActiveRun", Message: message}, Lane: lane}
}

// NoActiveOperation is raised when no operation is active (pi:
// NoActiveOperation).
type NoActiveOperation struct {
	harness.TaggedErrorBase
	Lane string `json:"lane"`
}

func NewNoActiveOperation(lane string, message string) *NoActiveOperation {
	return &NoActiveOperation{TaggedErrorBase: harness.TaggedErrorBase{Tag: "NoActiveOperation", Message: message}, Lane: lane}
}

// NothingToResume is raised when resume finds no suspended work (pi:
// NothingToResume).
type NothingToResume struct {
	harness.TaggedErrorBase
	Lane string `json:"lane"`
}

func NewNothingToResume(lane string, message string) *NothingToResume {
	return &NothingToResume{TaggedErrorBase: harness.TaggedErrorBase{Tag: "NothingToResume", Message: message}, Lane: lane}
}

// InvalidMessage is raised for malformed prompt input (pi: InvalidMessage).
type InvalidMessage struct {
	harness.TaggedErrorBase
	Lane   string `json:"lane"`
	Reason string `json:"reason"`
}

func NewInvalidMessage(lane string, reason string, message string) *InvalidMessage {
	return &InvalidMessage{TaggedErrorBase: harness.TaggedErrorBase{Tag: "InvalidMessage", Message: message}, Lane: lane, Reason: reason}
}

// UnknownSkill is raised for unresolvable skill names (pi: UnknownSkill).
type UnknownSkill struct {
	harness.TaggedErrorBase
	Name string `json:"name"`
}

func NewUnknownSkill(name string, message string) *UnknownSkill {
	return &UnknownSkill{TaggedErrorBase: harness.TaggedErrorBase{Tag: "UnknownSkill", Message: message}, Name: name}
}

// UnknownTemplate is raised for unresolvable prompt template names (pi:
// UnknownTemplate).
type UnknownTemplate struct {
	harness.TaggedErrorBase
	Name string `json:"name"`
}

func NewUnknownTemplate(name string, message string) *UnknownTemplate {
	return &UnknownTemplate{TaggedErrorBase: harness.TaggedErrorBase{Tag: "UnknownTemplate", Message: message}, Name: name}
}

// UnknownTarget is raised for unresolvable navigation targets (pi:
// UnknownTarget).
type UnknownTarget struct {
	harness.TaggedErrorBase
	TargetID string `json:"targetId"`
}

func NewUnknownTarget(targetID string, message string) *UnknownTarget {
	return &UnknownTarget{TaggedErrorBase: harness.TaggedErrorBase{Tag: "UnknownTarget", Message: message}, TargetID: targetID}
}

// UnknownQueueItem is raised for queue entries that no longer exist (pi:
// UnknownQueueItem).
type UnknownQueueItem struct {
	harness.TaggedErrorBase
	Lane    string `json:"lane"`
	EntryID string `json:"entryId"`
}

func NewUnknownQueueItem(lane string, entryID string, message string) *UnknownQueueItem {
	return &UnknownQueueItem{TaggedErrorBase: harness.TaggedErrorBase{Tag: "UnknownQueueItem", Message: message}, Lane: lane, EntryID: entryID}
}

// LaneExists is raised when creating a lane that already exists (pi:
// LaneExists).
type LaneExists struct {
	harness.TaggedErrorBase
	Lane string `json:"lane"`
}

func NewLaneExists(lane string, message string) *LaneExists {
	return &LaneExists{TaggedErrorBase: harness.TaggedErrorBase{Tag: "LaneExists", Message: message}, Lane: lane}
}

// InvalidLane is raised for malformed lane names (pi: InvalidLane).
type InvalidLane struct {
	harness.TaggedErrorBase
	Lane   string `json:"lane"`
	Reason string `json:"reason"`
}

func NewInvalidLane(lane string, reason string, message string) *InvalidLane {
	return &InvalidLane{TaggedErrorBase: harness.TaggedErrorBase{Tag: "InvalidLane", Message: message}, Lane: lane, Reason: reason}
}

// NothingToCompact is raised when compaction finds nothing to compact (pi:
// NothingToCompact).
type NothingToCompact struct {
	harness.TaggedErrorBase
	Lane string `json:"lane"`
}

func NewNothingToCompact(lane string, message string) *NothingToCompact {
	return &NothingToCompact{TaggedErrorBase: harness.TaggedErrorBase{Tag: "NothingToCompact", Message: message}, Lane: lane}
}

// Closed is raised after the harness has closed (pi: Closed).
type Closed struct {
	harness.TaggedErrorBase
}

func NewClosed(message string) *Closed {
	return &Closed{TaggedErrorBase: harness.TaggedErrorBase{Tag: "Closed", Message: message}}
}

// HarnessFault wraps an unexpected internal failure (pi: HarnessFault
// extends Error with cause).
type HarnessFault struct {
	Message string
	Cause   error
}

func (e *HarnessFault) Error() string { return e.Message }
func (e *HarnessFault) Unwrap() error { return e.Cause }

// NewHarnessFault builds the fault (pi: constructor).
func NewHarnessFault(message string, cause error) *HarnessFault {
	return &HarnessFault{Message: message, Cause: cause}
}

// HarnessClosed is returned when operations are attempted after close (pi:
// HarnessClosed).
type HarnessClosed struct{}

func (e *HarnessClosed) Error() string {
	return "AgentHarness was closed while the operation was active"
}

// HarnessNotImplemented is returned for scaffold operations that pi has not
// implemented yet (pi: HarnessNotImplemented).
type HarnessNotImplemented struct {
	Operation string
}

func (e *HarnessNotImplemented) Error() string {
	return fmt.Sprintf("AgentHarness.%s is not implemented yet", e.Operation)
}

// NewHarnessNotImplemented builds the error (pi: constructor).
func NewHarnessNotImplemented(operation string) *HarnessNotImplemented {
	return &HarnessNotImplemented{Operation: operation}
}

// OperationError is a normalized operation failure (pi: interface
// OperationError).
type OperationError struct {
	Code    string
	Message string
}

// RunInvocationOutcome is the terminal outcome of a run invocation (pi:
// RunOutcome union → single struct; pi's member-specific fields are
// documented per field and nil/string-empty when absent).
type RunInvocationOutcome struct {
	// Kind is "completed", "aborted", "failed", or "suspended".
	Kind string
	// LeafID is the leaf after the run.
	LeafID string
	// FinalEntryID is the final assistant entry id (present on
	// completed/aborted/suspended; absent on failed).
	FinalEntryID string
	// FinalMessage is the final assistant message (absent on failed).
	FinalMessage *types.AssistantMessage
	// Error is set on failed.
	Error *OperationError
	// Deferred is set on suspended.
	Deferred *types.DeferredHandle
}

// Run invocation outcome kinds (pi: the kind literals).
const (
	RunInvocationCompleted = "completed"
	RunInvocationAborted   = "aborted"
	RunInvocationFailed    = "failed"
	RunInvocationSuspended = "suspended"
)

// CompactionInvocationOutcome is the terminal outcome of a compaction
// invocation (pi: CompactionOutcome union → single struct).
type CompactionInvocationOutcome struct {
	// Kind is "completed", "declined", "aborted", or "failed".
	Kind string
	// LeafID is the leaf after the compaction.
	LeafID string
	// Entry is the compaction entry (completed only).
	Entry *session.CompactionEntry
	// Error is set on failed.
	Error *OperationError
}

// NavigationInvocationOutcome is the terminal outcome of a navigation
// invocation (pi: NavigationOutcome union → single struct).
type NavigationInvocationOutcome struct {
	// Kind is "completed", "declined", "aborted", or "failed".
	Kind string
	// LeafID is pi's newLeafId on completed and leafId on the other members
	// (string | null → *string; nil = null).
	LeafID *string
	// SummaryEntry is the branch summary entry (completed with summarize
	// only).
	SummaryEntry *session.BranchSummaryEntry
	// Error is set on failed.
	Error *OperationError
}

// The rejected unions (pi: RunRejected etc.) are the error returns of the
// operations below; callers type-switch on LaneBusy, InvalidMessage,
// UnknownSkill, UnknownTemplate, Closed, NothingToCompact, UnknownTarget,
// NothingToResume, MissingIdentities, NoActiveRun, NoActiveOperation.

// RunSuccess is the success payload of run-producing operations (pi:
// RunResult = ResultValue<{ runId } & RunOutcome, RunRejected>).
type RunSuccess struct {
	RunID string
	RunInvocationOutcome
}

// CompactionSuccess is the success payload of compact (pi: CompactionResult).
type CompactionSuccess struct {
	RunID string
	CompactionInvocationOutcome
}

// NavigationSuccess is the success payload of navigateTree (pi:
// NavigationResult).
type NavigationSuccess struct {
	RunID string
	NavigationInvocationOutcome
}

// QueueSuccess is the success payload of the queue operations (pi:
// QueueResult = ResultValue<{ entryId }, QueueRejected>).
type QueueSuccess struct {
	EntryID string
}

// CancelQueuedSuccess is the success payload of cancelQueued (pi:
// CancelQueuedResult; the outcome union becomes a string).
type CancelQueuedSuccess struct {
	// Outcome is "cancelled", "already_consumed", or "already_cleared".
	Outcome string
}

// Cancel-queued outcome literals (pi: the union members).
const (
	CancelQueuedCancelled       = "cancelled"
	CancelQueuedAlreadyConsumed = "already_consumed"
	CancelQueuedAlreadyCleared  = "already_cleared"
)

// AbortSuccess is the success payload of abort (pi: AbortResult).
type AbortSuccess struct {
	RunID    string
	Steer    []agent.AgentMessage
	FollowUp []agent.AgentMessage
}

// ResumeSuccess is the success payload of resume (pi: ResumeOutcome union of
// three intersections → one struct; exactly the outcome pointer matching
// Operation is set).
type ResumeSuccess struct {
	// Operation is "run", "compaction", or "navigation".
	Operation string
	// RunID is the resumed operation id.
	RunID      string
	Run        *RunInvocationOutcome
	Compaction *CompactionInvocationOutcome
	Navigation *NavigationInvocationOutcome
}

// NavigateOptions parameterize navigateTree (pi: NavigateOptions).
type NavigateOptions struct {
	// Summarize defaults to false; nil = pi's undefined.
	Summarize *bool
	// CustomInstructions feeds the branch summary; "" = pi's undefined.
	CustomInstructions string
	// Label names the navigation; "" = pi's undefined.
	Label string
}

// AbortQueuedMessages holds the buffered queue contents captured for a
// suspended operation (pi: the aborting?: { steer, followUp } member).
type AbortQueuedMessages struct {
	Steer    []agent.AgentMessage
	FollowUp []agent.AgentMessage
}

// MissingIdentitiesInfo names the identities a suspended operation lacks
// (pi: the missing: { tools, models } member).
type MissingIdentitiesInfo struct {
	Tools  []string
	Models []string
}

// SuspendedOperation describes an operation interrupted by crash or deferral
// (pi: SuspendedOperation).
type SuspendedOperation struct {
	Lane      string
	Kind      OperationKind
	ID        string
	StartedAt int64
	// Reason is "crash" or "deferred".
	Reason string
	// Prompt is the buffered prompt (crash-suspended runs).
	Prompt []agent.AgentMessage
	// Deferred is the deferred handle (deferred-suspended runs).
	Deferred *types.DeferredHandle
	// Aborting holds queued messages captured during abort reconciliation.
	Aborting *AbortQueuedMessages
	// Missing names the identities the harness cannot resolve.
	Missing MissingIdentitiesInfo
}

// LaneOperationInfo describes a lane's active operation (pi: the operation
// member of LaneInfo).
type LaneOperationInfo struct {
	ID   string
	Kind OperationKind
	// Status is "running", "suspended", or "aborting".
	Status string
}

// LaneInfo summarizes one lane (pi: LaneInfo).
type LaneInfo struct {
	Name      string
	LeafID    *string
	Operation *LaneOperationInfo
}

// QueuedItem is one queued message (pi: QueuedItem).
type QueuedItem struct {
	EntryID string
	Message agent.AgentMessage
}

// LaneSnapshot is the watch snapshot of one lane (pi: LaneSnapshot).
type LaneSnapshot struct {
	Lane       string
	Transcript []session.Entry
	LeafID     *string
	Operation  *LaneOperationInfo
	Queues     struct {
		Steer    []QueuedItem
		FollowUp []QueuedItem
		NextRun  []QueuedItem
	}
	// PendingWrites holds provisioned entries awaiting durable commit (pi:
	// { id, entry: ProvisionedEntry }[]; pig has no separate provisioned
	// type — see session/types.go's header note).
	PendingWrites []PendingWrite
	Faulted       bool
}

// PendingWrite is one deferred session write (pi: the pendingWrites member).
type PendingWrite struct {
	ID    string
	Entry session.Entry
}

// SessionSnapshot is the watch snapshot of the whole session (pi:
// SessionSnapshot).
type SessionSnapshot struct {
	Lanes   []SessionSnapshotLane
	Faulted bool
}

// SessionSnapshotLane is one lane in a session snapshot (pi: LaneInfo & {
// suspended?: SuspendedOperation }).
type SessionSnapshotLane struct {
	LaneInfo
	Suspended *SuspendedOperation
}

// ActionKind discriminates ActionInfo (pi: the ActionInfo union members).
type ActionKind string

const (
	ActionAppendEntry       ActionKind = "append_entry"
	ActionAppendRecord      ActionKind = "append_record"
	ActionMoveLane          ActionKind = "move_lane"
	ActionSetFact           ActionKind = "set_fact"
	ActionTryFinishRun      ActionKind = "try_finish_run"
	ActionFinishOperation   ActionKind = "finish_operation"
	ActionCommitFollowUp    ActionKind = "commit_follow_up"
	ActionConsumeQueueItem  ActionKind = "consume_queue_item"
	ActionApplyPendingWrite ActionKind = "apply_pending_write"
	ActionStreamAssistant   ActionKind = "stream_assistant"
	ActionExecuteTool       ActionKind = "execute_tool"
	ActionFetchDeferred     ActionKind = "fetch_deferred"
	ActionCancelDeferred    ActionKind = "cancel_deferred"
	ActionHook              ActionKind = "hook"
	ActionSleep             ActionKind = "sleep"
)

// ActionInfo describes one executable durable action (pi: ActionInfo union →
// single struct; member-specific fields documented per field).
type ActionInfo struct {
	Kind ActionKind
	// EntryType is the appended entry type (append_entry).
	EntryType session.EntryType
	// EntryID is the target entry id (append_entry, consume_queue_item,
	// apply_pending_write).
	EntryID string
	// RecordType is the appended record type (append_record).
	RecordType string
	// To is the move destination (move_lane; nil = pi's null).
	To *string
	// Fact is the fact key (set_fact): "name" or "label".
	Fact string
	// Outcome is the finish outcome (try_finish_run: "completed"|"failed";
	// finish_operation: "completed"|"declined"|"failed"|"aborted").
	Outcome string
	// Queue is the drained queue (consume_queue_item): "steer" or
	// "followUp".
	Queue string
	// Step is the streamed step kind (stream_assistant): "assistant",
	// "compaction", or "branch_summary".
	Step string
	// Attempt is the one-based attempt number (stream_assistant).
	Attempt int
	// ToolCallID is the executed call id (execute_tool).
	ToolCallID string
	// ToolName is the executed tool name (execute_tool).
	ToolName string
	// Provider is the deferred provider id (fetch_deferred /
	// cancel_deferred).
	Provider string
	// ID is the deferred handle id (fetch_deferred / cancel_deferred).
	ID string
	// Name is the hook name (hook).
	Name HookName
	// DelayMs is the sleep duration (sleep).
	DelayMs int64
}

// HookName names the harness hook points (pi: HookName union).
type HookName string

const (
	HookBeforeRun        HookName = "before_run"
	HookBeforeResume     HookName = "before_resume"
	HookBeforeRunEnd     HookName = "before_run_end"
	HookTransformContext HookName = "transform_context"
	HookBeforeRequest    HookName = "before_request"
	HookBeforePayload    HookName = "before_payload"
	HookAfterResponse    HookName = "after_response"
	HookBeforeTool       HookName = "before_tool"
	HookAfterTool        HookName = "after_tool"
	HookBeforeCompaction HookName = "before_compaction"
	HookBeforeNavigation HookName = "before_navigation"
)

// HookHandler is one hook callback (pi: (event: unknown) => unknown |
// Promise<unknown> → synchronous).
type HookHandler func(event any) any

// HookRegistrationOptions parameterize hook registration (pi: the options
// argument of Hooks.on).
type HookRegistrationOptions struct {
	// ID is the stable registration id; nil = pi's undefined.
	ID *string
}

// Hooks is the hook registration surface (pi: interface Hooks). The error
// return encodes the UnavailableRegistry throw.
type Hooks interface {
	On(name HookName, handler HookHandler, options *HookRegistrationOptions) (func(), error)
}

// HarnessEvents is the passive event subscription surface re-declared by pi
// in agent-harness.ts (interface Events). The method is named OnEvent (pi:
// on) because UnavailableRegistry also implements Hooks.On; the error return
// encodes the UnavailableRegistry throw.
type HarnessEvents interface {
	OnEvent(eventType string, listener func(event any)) (func(), error)
}

// UnavailableRegistry is the closed-harness hooks/events registry (pi: class
// UnavailableRegistry). On throws HarnessClosed or HarnessNotImplemented;
// Go surfaces the throw as the error return.
type UnavailableRegistry struct {
	operation string
	isClosed  func() bool
}

// NewUnavailableRegistry builds the registry (pi: constructor).
func NewUnavailableRegistry(operation string, isClosed func() bool) *UnavailableRegistry {
	return &UnavailableRegistry{operation: operation, isClosed: isClosed}
}

// On implements Hooks.
func (r *UnavailableRegistry) On(_ HookName, _ HookHandler, _ *HookRegistrationOptions) (func(), error) {
	if r.isClosed() {
		return nil, &HarnessClosed{}
	}
	return nil, NewHarnessNotImplemented(r.operation)
}

// On implements HarnessEvents.
func (r *UnavailableRegistry) OnEvent(_ string, _ func(event any)) (func(), error) {
	if r.isClosed() {
		return nil, &HarnessClosed{}
	}
	return nil, NewHarnessNotImplemented(r.operation)
}

// HarnessTool is a harness tool with a declared replay policy (pi:
// HarnessTool = AgentTool & { replay?: "never" | "safe" }; "" = undefined).
type HarnessTool struct {
	agent.AgentTool
	Replay string
}

// Resources is the harness resource bundle (pi: type Resources =
// harness.AgentHarnessResources<harness.Skill, harness.PromptTemplate>; generics dropped, see
// types.go).
type Resources = harness.AgentHarnessResources

// StreamOptions is the harness stream options (pi: type StreamOptions =
// SimpleStreamOptions).
type StreamOptions = ai.SimpleStreamOptions

// StreamOptionsPatch is pi's Partial<SimpleStreamOptions>. Partial has no
// Go counterpart; it arrives with the harness assembly consumers as a
// pointer-field struct declared next to them (placeholder noted here).
// type StreamOptionsPatch — pending consumers.

// EntryProjector projects a session entry into agent messages (pi:
// EntryProjector; the async return collapses to sync).
type EntryProjector func(entry session.Entry) []agent.AgentMessage

// RecordUsageOptions parameterize recordUsage (pi: the options argument).
type RecordUsageOptions struct {
	// EntryID attributes the usage to an existing entry; nil = pi's
	// undefined.
	EntryID *string
	// Details is arbitrary structured data (pi: details?: JsonValue).
	Details session.JsonValue
}

// AgentHarnessOptions parameterize the harness (pi: AgentHarnessOptions).
type AgentHarnessOptions struct {
	// Session is the durable session.
	Session session.Session
	// Models resolves providers and streams.
	Models ai.Models
	// Model is the active model.
	Model *types.Model
	// ThinkingLevel is the default thinking level; "" = pi's undefined →
	// the constructor default "off".
	ThinkingLevel agent.ThinkingLevel
	// ActiveToolNames restricts the active tools; empty with Tools set
	// defaults to all tool names (pi: activeToolNames?).
	ActiveToolNames []string
	// Tools are the available tools.
	Tools []HarnessTool
	// ToolContext is the static tool context (pi: toolContext?: object).
	ToolContext any
	// ToolContextProvider resolves the tool context per turn (pi: the
	// () => object | Promise<object> union member). Exactly one of
	// ToolContext / ToolContextProvider may be set.
	ToolContextProvider func() (any, error)
	// SystemPrompt is the static system prompt (pi: systemPrompt?: string).
	SystemPrompt string
	// SystemPromptProvider resolves the system prompt per turn (pi: the
	// () => string | Promise<string> union member). Exactly one of
	// SystemPrompt / SystemPromptProvider may be set.
	SystemPromptProvider func() (string, error)
	// Resources are skills and prompt templates.
	Resources *Resources
	// StreamOptions are the curated provider request options.
	StreamOptions *StreamOptions
	// Retry is the provider retry policy (pi: retry?: RetryPolicy).
	Retry *ai.RetryPolicy
	// Compaction are the compaction settings (pi: compaction?:
	// CompactionSettings).
	Compaction *harness.CompactionSettings
	// SteeringMode is the steer queue drain mode; "" = pi's undefined →
	// default "one-at-a-time".
	SteeringMode agent.QueueMode
	// FollowUpMode is the follow-up queue drain mode; "" = pi's undefined →
	// default "one-at-a-time".
	FollowUpMode agent.QueueMode
	// ToolExecution selects "sequential" or "parallel" execution (pi:
	// toolExecution?).
	ToolExecution agent.ToolExecutionMode
	// Drive selects "automatic" or "manual" driving (pi: drive?).
	Drive string
	// ToProviderMessages overrides agent-message conversion (pi:
	// toProviderMessages?).
	ToProviderMessages func(messages []agent.AgentMessage) []types.Message
	// EntryProjectors project custom entry types (pi: entryProjectors?).
	EntryProjectors map[string]EntryProjector
	// Telemetry is the telemetry context (pi: context?: TelemetryContext;
	// renamed to avoid the Go ctx confusion).
	Telemetry telemetry.TelemetryContext
}

// AgentLane is the per-lane operation surface (pi: interface AgentLane).
type AgentLane interface {
	// Name is the lane name (pi: readonly name).
	Name() string
	// Session is the durable session handle (pi v0.84.3: readonly session
	// SessionTree; the v0.85.1 AgentLane no longer exposes the session —
	// that rewrite lands with the S2 agent-harness batch, so the scaffold
	// keeps the accessor over the new Session interface).
	Session() session.Session

	GetLeafID() (*string, error)
	Prompt(messages ...agent.AgentMessage) (*RunSuccess, error)
	Skill(name string, additionalInstructions string) (*RunSuccess, error)
	PromptFromTemplate(name string, args []string) (*RunSuccess, error)
	Compact(customInstructions string) (*CompactionSuccess, error)
	NavigateTree(targetID *string, options *NavigateOptions) (*NavigationSuccess, error)
	Resume() (*ResumeSuccess, error)
	Abort() (*AbortSuccess, error)
	Steer(message agent.AgentMessage) (*QueueSuccess, error)
	FollowUp(message agent.AgentMessage) (*QueueSuccess, error)
	NextRun(message agent.AgentMessage) (*QueueSuccess, error)
	CancelQueued(entryID string) (*CancelQueuedSuccess, error)
	RecordUsage(usage *types.Usage, options *RecordUsageOptions) error
	WaitForIdle() error
	RunWhenIdle(callback func()) error
	PeekAction() (*ActionInfo, error)
	ExecuteAction() (*ActionInfo, error)
	RunToCompletion() error
	GetModel() *types.Model
	SetModel(model *types.Model)
	GetThinkingLevel() agent.ThinkingLevel
	SetThinkingLevel(level agent.ThinkingLevel)
	GetActiveTools() []string
	SetActiveTools(names []string)
	Watch() (*harness.WatchHandle[LaneSnapshot], error)
}

// AgentHarness is the main-lane harness assembly (pi: class AgentHarness
// implements AgentLane). Run/compaction/navigation execution is a scaffold:
// pi's v0.84.3 implementation returns HarnessNotImplemented until the
// assembly lands, and pig mirrors that.
type AgentHarness struct {
	mu sync.Mutex

	// hooks and events are the UnavailableRegistry-backed surfaces (pi:
	// readonly hooks / readonly events).
	hooks  Hooks
	events HarnessEvents

	durableSession     session.Session
	model              *types.Model
	thinkingLevel      agent.ThinkingLevel
	activeToolNames    []string
	tools              []HarnessTool
	resources          Resources
	streamOptions      StreamOptions
	retryPolicy        ai.RetryPolicy
	compactionSettings harness.CompactionSettings
	steeringMode       agent.QueueMode
	followUpMode       agent.QueueMode
	closed             bool
}

// Name implements AgentLane (pi: readonly name = "main").
func (h *AgentHarness) Name() string { return "main" }

// Session implements AgentLane (pi v0.84.3: readonly session; see the
// interface note — the v0.85.1 surface drops it, deferred to S2).
func (h *AgentHarness) Session() session.Session { return h.durableSession }

// newAgentHarness builds the harness (pi: private constructor).
func newAgentHarness(options *AgentHarnessOptions) *AgentHarness {
	h := &AgentHarness{
		durableSession:     options.Session,
		model:              options.Model,
		thinkingLevel:      options.ThinkingLevel,
		compactionSettings: harness.DefaultCompactionSettings,
		steeringMode:       agent.QueueModeOneAtATime,
		followUpMode:       agent.QueueModeOneAtATime,
	}
	h.hooks = NewUnavailableRegistry("hooks.on", h.isClosed)
	h.events = NewUnavailableRegistry("events.on", h.isClosed)
	if h.thinkingLevel == "" {
		h.thinkingLevel = "off"
	}
	if options.ActiveToolNames != nil {
		h.activeToolNames = append([]string{}, options.ActiveToolNames...)
	} else if options.Tools != nil {
		h.activeToolNames = make([]string, len(options.Tools))
		for i, tool := range options.Tools {
			h.activeToolNames[i] = tool.Name
		}
	} else {
		h.activeToolNames = []string{}
	}
	if options.Tools != nil {
		h.tools = append([]HarnessTool{}, options.Tools...)
	} else {
		h.tools = []HarnessTool{}
	}
	if options.Resources != nil {
		if options.Resources.Skills != nil {
			h.resources.Skills = append([]harness.Skill{}, options.Resources.Skills...)
		}
		if options.Resources.PromptTemplates != nil {
			h.resources.PromptTemplates = append([]harness.PromptTemplate{}, options.Resources.PromptTemplates...)
		}
	}
	if options.StreamOptions != nil {
		h.streamOptions = *options.StreamOptions
	}
	if options.Retry != nil {
		h.retryPolicy = *options.Retry
	} else {
		h.retryPolicy = ai.RetryPolicy{Enabled: false, MaxRetries: 0, BaseDelayMs: 1000}
	}
	if options.Compaction != nil {
		h.compactionSettings = *options.Compaction
	}
	if options.SteeringMode != "" {
		h.steeringMode = options.SteeringMode
	}
	if options.FollowUpMode != "" {
		h.followUpMode = options.FollowUpMode
	}
	return h
}

// CreateAgentHarness opens a harness on an existing session (pi: static
// create). Restoring sessions with records is not implemented yet (pi:
// throws HarnessNotImplemented("create.restore")).
func CreateAgentHarness(options *AgentHarnessOptions) (*AgentHarness, []SuspendedOperation, error) {
	// pi v0.84.3 checked for existing lane records; v0.85.1 Session has no
	// record log, so restore detection keys on any existing entry.
	entries, err := options.Session.FindEntries(&session.EntryQuery{Limit: lo.ToPtr(1)}, context.Background())
	if err != nil {
		return nil, nil, err
	}
	if len(entries) != 0 {
		return nil, nil, NewHarnessNotImplemented("create.restore")
	}
	return newAgentHarness(options), []SuspendedOperation{}, nil
}

// Hooks is the hook registration surface (pi: readonly hooks). On throws
// HarnessClosed/HarnessNotImplemented while the assembly is a scaffold.
func (h *AgentHarness) Hooks() Hooks { return h.hooks }

// Events is the passive event surface (pi: readonly events).
func (h *AgentHarness) Events() HarnessEvents { return h.events }

// isClosed reports closure under the state mutex (pi: () => this.closed).
func (h *AgentHarness) isClosed() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.closed
}

// unavailable returns the scaffold rejection (pi: private unavailable).
func (h *AgentHarness) unavailable(operation string) error {
	if h.closed {
		return &HarnessClosed{}
	}
	return NewHarnessNotImplemented(operation)
}

// GetLeafID implements AgentLane (pi v0.84.3: getLeafId; the v0.85.1
// equivalent is the main branch tip).
func (h *AgentHarness) GetLeafID() (*string, error) {
	return h.GetTip(context.Background())
}

// GetTip returns the main branch tip (v0.85.1 lane equivalent of
// getLeafId; S2 rewrites this surface).
func (h *AgentHarness) GetTip(ctx context.Context) (*string, error) {
	if h.closed {
		return nil, &HarnessClosed{}
	}
	branch, err := h.durableSession.Branch("main", ctx)
	if err != nil {
		return nil, err
	}
	if branch == nil {
		return nil, nil
	}
	return branch.GetTipID(ctx)
}

// Prompt implements AgentLane (pi: prompt overloads collapsed; see header).
func (h *AgentHarness) Prompt(_ ...agent.AgentMessage) (*RunSuccess, error) {
	return nil, h.unavailable("prompt")
}

// Skill implements AgentLane (pi: skill).
func (h *AgentHarness) Skill(_ string, _ string) (*RunSuccess, error) {
	return nil, h.unavailable("skill")
}

// PromptFromTemplate implements AgentLane (pi: promptFromTemplate).
func (h *AgentHarness) PromptFromTemplate(_ string, _ []string) (*RunSuccess, error) {
	return nil, h.unavailable("promptFromTemplate")
}

// Compact implements AgentLane (pi: compact; the options object collapses to
// the customInstructions string, "" = undefined).
func (h *AgentHarness) Compact(_ string) (*CompactionSuccess, error) {
	return nil, h.unavailable("compact")
}

// NavigateTree implements AgentLane (pi: navigateTree).
func (h *AgentHarness) NavigateTree(_ *string, _ *NavigateOptions) (*NavigationSuccess, error) {
	return nil, h.unavailable("navigateTree")
}

// Resume implements AgentLane (pi: resume).
func (h *AgentHarness) Resume() (*ResumeSuccess, error) {
	return nil, h.unavailable("resume")
}

// Abort implements AgentLane (pi: abort).
func (h *AgentHarness) Abort() (*AbortSuccess, error) {
	return nil, h.unavailable("abort")
}

// Steer implements AgentLane (pi: steer overloads collapsed; see header).
func (h *AgentHarness) Steer(_ agent.AgentMessage) (*QueueSuccess, error) {
	return nil, h.unavailable("steer")
}

// FollowUp implements AgentLane (pi: followUp overloads collapsed).
func (h *AgentHarness) FollowUp(_ agent.AgentMessage) (*QueueSuccess, error) {
	return nil, h.unavailable("followUp")
}

// NextRun implements AgentLane (pi: nextRun overloads collapsed).
func (h *AgentHarness) NextRun(_ agent.AgentMessage) (*QueueSuccess, error) {
	return nil, h.unavailable("nextRun")
}

// CancelQueued implements AgentLane (pi: cancelQueued).
func (h *AgentHarness) CancelQueued(_ string) (*CancelQueuedSuccess, error) {
	return nil, h.unavailable("cancelQueued")
}

// RecordUsage implements AgentLane (pi: recordUsage; Result<void, Closed> →
// error only).
func (h *AgentHarness) RecordUsage(_ *types.Usage, _ *RecordUsageOptions) error {
	return h.unavailable("recordUsage")
}

// WaitForIdle implements AgentLane (pi: waitForIdle).
func (h *AgentHarness) WaitForIdle() error {
	return h.unavailable("waitForIdle")
}

// RunWhenIdle implements AgentLane (pi: runWhenIdle).
func (h *AgentHarness) RunWhenIdle(_ func()) error {
	return h.unavailable("runWhenIdle")
}

// PeekAction implements AgentLane (pi: peekAction).
func (h *AgentHarness) PeekAction() (*ActionInfo, error) {
	return nil, h.unavailable("peekAction")
}

// ExecuteAction implements AgentLane (pi: executeAction).
func (h *AgentHarness) ExecuteAction() (*ActionInfo, error) {
	return nil, h.unavailable("executeAction")
}

// RunToCompletion implements AgentLane (pi: runToCompletion).
func (h *AgentHarness) RunToCompletion() error {
	return h.unavailable("runToCompletion")
}

// GetModel implements AgentLane (pi: getModel).
func (h *AgentHarness) GetModel() *types.Model {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.model
}

// SetModel implements AgentLane (pi: setModel).
func (h *AgentHarness) SetModel(model *types.Model) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.model = model
}

// GetThinkingLevel implements AgentLane (pi: getThinkingLevel).
func (h *AgentHarness) GetThinkingLevel() agent.ThinkingLevel {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.thinkingLevel
}

// SetThinkingLevel implements AgentLane (pi: setThinkingLevel).
func (h *AgentHarness) SetThinkingLevel(level agent.ThinkingLevel) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.thinkingLevel = level
}

// GetActiveTools implements AgentLane (pi: getActiveTools; defensive copy).
func (h *AgentHarness) GetActiveTools() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string{}, h.activeToolNames...)
}

// SetActiveTools implements AgentLane (pi: setActiveTools; defensive copy).
func (h *AgentHarness) SetActiveTools(names []string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.activeToolNames = append([]string{}, names...)
}

// Watch implements AgentLane (pi: watch).
func (h *AgentHarness) Watch() (*harness.WatchHandle[LaneSnapshot], error) {
	return nil, h.unavailable("watch")
}

// Lane looks up a lane by name (pi: lane; AgentLane | undefined → second
// return false for undefined).
func (h *AgentHarness) Lane(_ string) (AgentLane, bool, error) {
	return nil, false, h.unavailable("lane")
}

// CreateLane creates a lane (pi: createLane; CreateLaneResult = ResultValue<
// AgentLane, ...>).
func (h *AgentHarness) CreateLane(_ string, _ *string) (AgentLane, error) {
	return nil, h.unavailable("createLane")
}

// Lanes lists lane infos (pi: lanes).
func (h *AgentHarness) Lanes() ([]LaneInfo, error) {
	return nil, h.unavailable("lanes")
}

// GetTools returns the tools (pi: getTools; defensive copy).
func (h *AgentHarness) GetTools() []HarnessTool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]HarnessTool{}, h.tools...)
}

// SetTools replaces the tools (pi: setTools; defensive copies).
func (h *AgentHarness) SetTools(tools []HarnessTool, activeNames []string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.tools = append([]HarnessTool{}, tools...)
	if activeNames != nil {
		h.activeToolNames = append([]string{}, activeNames...)
	} else {
		names := make([]string, len(tools))
		for i, tool := range tools {
			names[i] = tool.Name
		}
		h.activeToolNames = names
	}
}

// GetResources returns the resources (pi: getResources; defensive copies).
func (h *AgentHarness) GetResources() Resources {
	h.mu.Lock()
	defer h.mu.Unlock()
	var resources Resources
	if h.resources.Skills != nil {
		resources.Skills = append([]harness.Skill{}, h.resources.Skills...)
	}
	if h.resources.PromptTemplates != nil {
		resources.PromptTemplates = append([]harness.PromptTemplate{}, h.resources.PromptTemplates...)
	}
	return resources
}

// SetResources replaces the resources (pi: setResources; defensive copies).
func (h *AgentHarness) SetResources(resources Resources) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if resources.Skills != nil {
		h.resources.Skills = append([]harness.Skill{}, resources.Skills...)
	} else {
		h.resources.Skills = nil
	}
	if resources.PromptTemplates != nil {
		h.resources.PromptTemplates = append([]harness.PromptTemplate{}, resources.PromptTemplates...)
	} else {
		h.resources.PromptTemplates = nil
	}
}

// GetStreamOptions returns the stream options (pi: getStreamOptions;
// defensive copy).
func (h *AgentHarness) GetStreamOptions() StreamOptions {
	h.mu.Lock()
	defer h.mu.Unlock()
	options := h.streamOptions
	return options
}

// SetStreamOptions replaces the stream options (pi: setStreamOptions;
// defensive copy).
func (h *AgentHarness) SetStreamOptions(options StreamOptions) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.streamOptions = options
}

// GetRetryPolicy returns the retry policy (pi: getRetryPolicy; defensive
// copy).
func (h *AgentHarness) GetRetryPolicy() ai.RetryPolicy {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.retryPolicy
}

// SetRetryPolicy replaces the retry policy (pi: setRetryPolicy; defensive
// copy).
func (h *AgentHarness) SetRetryPolicy(policy ai.RetryPolicy) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.retryPolicy = policy
}

// GetCompactionSettings returns the compaction settings (pi:
// getCompactionSettings; defensive copy).
func (h *AgentHarness) GetCompactionSettings() harness.CompactionSettings {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.compactionSettings
}

// SetCompactionSettings replaces the compaction settings (pi:
// setCompactionSettings; defensive copy).
func (h *AgentHarness) SetCompactionSettings(settings harness.CompactionSettings) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.compactionSettings = settings
}

// GetSteeringMode returns the steer queue mode (pi: getSteeringMode).
func (h *AgentHarness) GetSteeringMode() agent.QueueMode {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.steeringMode
}

// SetSteeringMode replaces the steer queue mode (pi: setSteeringMode).
func (h *AgentHarness) SetSteeringMode(mode agent.QueueMode) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.steeringMode = mode
}

// GetFollowUpMode returns the follow-up queue mode (pi: getFollowUpMode).
func (h *AgentHarness) GetFollowUpMode() agent.QueueMode {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.followUpMode
}

// SetFollowUpMode replaces the follow-up queue mode (pi: setFollowUpMode).
func (h *AgentHarness) SetFollowUpMode(mode agent.QueueMode) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.followUpMode = mode
}

// WatchSession watches the whole session (pi: watchSession).
func (h *AgentHarness) WatchSession() (*harness.WatchHandle[SessionSnapshot], error) {
	return nil, h.unavailable("watchSession")
}

// Close marks the harness closed (pi: close).
func (h *AgentHarness) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closed = true
}
