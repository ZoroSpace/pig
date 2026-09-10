// pi: harness/session/values.ts — typed storage addresses (scalars and
// lists), the value write shapes, and the well-known address helpers.
//
// Deviations:
//   - Value<T>/ValueList<T> phantom type parameters collapse to non-generic
//     Value / ValueList (Go interfaces cannot carry generic methods, so the
//     Storage/Session surface is erased to any; see types.go). The declared
//     value type survives only in the helper constructors' doc comments.
//   - TS's `storedValueType` unique-symbol brand and Object.freeze have no
//     runtime counterpart; Go value objects are plain structs.
//   - Methods named `value`/`list` in TS become NewValue/NewList (Go
//     keywords/naming).
package session

import "fmt"

// ValueKind discriminates the two address kinds.
type ValueKind string

const (
	ValueKindValue ValueKind = "value"
	ValueKindList  ValueKind = "list"
)

// Value is one scalar storage address (pi: Value<T>). Namespace + Key form
// the physical key; kind is always "value".
type Value struct {
	Namespace string `json:"namespace"`
	Key       string `json:"key"`
	kind      ValueKind
}

// ValueList is one list storage address (pi: ValueList<T>).
type ValueList struct {
	Namespace string `json:"namespace"`
	Key       string `json:"key"`
	kind      ValueKind
}

// Kind reports the address kind.
func (v Value) Kind() ValueKind { return v.kind }

// Kind reports the address kind.
func (v ValueList) Kind() ValueKind { return v.kind }

func validateAddress(namespace, key string) error {
	if len(namespace) == 0 {
		return fmt.Errorf("Value namespace must not be empty")
	}
	if containsNullByte(namespace) {
		return fmt.Errorf(`Value namespace must not contain \u0000`)
	}
	if containsNullByte(key) {
		return fmt.Errorf(`Value key must not contain \u0000`)
	}
	return nil
}

func containsNullByte(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == 0 {
			return true
		}
	}
	return false
}

// NewValue builds a scalar address (pi: value<T>(namespace, key)).
func NewValue(namespace, key string) (Value, error) {
	if err := validateAddress(namespace, key); err != nil {
		return Value{}, err
	}
	return Value{Namespace: namespace, Key: key, kind: ValueKindValue}, nil
}

// NewList builds a list address (pi: list<T>(namespace, key)).
func NewList(namespace, key string) (ValueList, error) {
	if err := validateAddress(namespace, key); err != nil {
		return ValueList{}, err
	}
	return ValueList{Namespace: namespace, Key: key, kind: ValueKindList}, nil
}

// MustValue is NewValue panicking on invalid addresses; the well-known
// helpers below only receive compile-time-constant namespaces.
func MustValue(namespace, key string) Value {
	v, err := NewValue(namespace, key)
	if err != nil {
		panic(err)
	}
	return v
}

// MustList is NewList panicking on invalid addresses.
func MustList(namespace, key string) ValueList {
	v, err := NewList(namespace, key)
	if err != nil {
		panic(err)
	}
	return v
}

// StoredValue is one stored scalar with its committing sequence (pi:
// StoredValue<T>).
type StoredValue struct {
	Address Value `json:"address"`
	Value   any   `json:"value"`
	Seq     int64 `json:"seq"`
}

// ListElement is one stored list element (pi: ListElement<T>).
type ListElement struct {
	Seq   int64 `json:"seq"`
	Value any   `json:"value"`
}

// ListCursor pages list reads by sequence (pi: ListCursor).
type ListCursor struct {
	Seq int64 `json:"seq"`
}

// ListReadOptions parameterizes list reads (pi: ListReadOptions).
type ListReadOptions struct {
	Cursor *ListCursor `json:"cursor,omitempty"`
	Order  string      `json:"order,omitempty"` // "asc" | "desc"
	Limit  *int        `json:"limit,omitempty"`
}

// ResolvedListReadOptions is ListReadOptions with defaults applied (pi:
// ResolvedListReadOptions).
type ResolvedListReadOptions struct {
	Cursor *ListCursor
	Order  string
	Limit  int
}

// ValueSetWrite sets one scalar (pi: ValueSetWrite).
type ValueSetWrite struct {
	Namespace string `json:"namespace"`
	Key       string `json:"key"`
	Value     any    `json:"value"`
}

func (w ValueSetWrite) writeKind() string { return "value" }

// Op returns the value operation.
func (w ValueSetWrite) Op() string { return "set" }

// ValueDeleteWrite deletes one scalar (pi: ValueDeleteWrite).
type ValueDeleteWrite struct {
	Namespace string `json:"namespace"`
	Key       string `json:"key"`
}

func (w ValueDeleteWrite) writeKind() string { return "value" }

// Op returns the value operation.
func (w ValueDeleteWrite) Op() string { return "delete" }

// ListAppendWrite appends one list element (pi: ListAppendWrite).
type ListAppendWrite struct {
	Namespace string `json:"namespace"`
	Key       string `json:"key"`
	Value     any    `json:"value"`
}

func (w ListAppendWrite) writeKind() string { return "list" }

// Op returns the list operation.
func (w ListAppendWrite) Op() string { return "append" }

// ListDeleteWrite deletes one list (pi: ListDeleteWrite).
type ListDeleteWrite struct {
	Namespace string `json:"namespace"`
	Key       string `json:"key"`
}

func (w ListDeleteWrite) writeKind() string { return "list" }

// Op returns the list operation.
func (w ListDeleteWrite) Op() string { return "delete" }

// SetValue builds a scalar set write (pi: setValue).
func SetValue(address Value, next any) ValueSetWrite {
	return ValueSetWrite{Namespace: address.Namespace, Key: address.Key, Value: next}
}

// DeleteValue builds a scalar delete write (pi: deleteValue).
func DeleteValue(address Value) ValueDeleteWrite {
	return ValueDeleteWrite{Namespace: address.Namespace, Key: address.Key}
}

// AppendList builds a list append write (pi: appendList).
func AppendList(address ValueList, element any) ListAppendWrite {
	return ListAppendWrite{Namespace: address.Namespace, Key: address.Key, Value: element}
}

// DeleteList builds a list delete write (pi: deleteList).
func DeleteList(address ValueList) ListDeleteWrite {
	return ListDeleteWrite{Namespace: address.Namespace, Key: address.Key}
}

// ResolveListReadOptions applies the list-read defaults (pi:
// resolveListReadOptions): limit default 1000, capped at 10000; order
// default "asc".
func ResolveListReadOptions(options *ListReadOptions) (ResolvedListReadOptions, error) {
	requestedLimit := 1_000
	if options != nil && options.Limit != nil {
		requestedLimit = *options.Limit
	}
	// pi rejects non-safe integers too; Go ints cap far below 2^53 on
	// 64-bit platforms only in the negative direction, so mirror the upper
	// bound explicitly.
	const maxSafeInteger = int64(1<<53 - 1)
	if requestedLimit <= 0 || int64(requestedLimit) > maxSafeInteger {
		return ResolvedListReadOptions{}, fmt.Errorf("List read limit must be a positive safe integer")
	}
	resolved := ResolvedListReadOptions{Order: "asc", Limit: min(requestedLimit, 10_000)}
	if options != nil {
		resolved.Cursor = options.Cursor
		if options.Order != "" {
			resolved.Order = options.Order
		}
	}
	return resolved, nil
}

// Well-known namespaces (pi: inline literals in the address helpers).
const (
	namespaceBranchTip              = "pi.branch.tip"
	namespaceLaneConfig             = "pi.lane.config"
	namespaceLaneState              = "pi.lane.state"
	namespaceOperationResult        = "pi.result"
	namespaceOperationMeta          = "pi.op.meta"
	namespaceOperationState         = "pi.op.state"
	namespaceOperationToolArgs      = "pi.op.tool_args"
	namespaceOperationToolMemo      = "pi.op.tool_memo"
	namespaceOperationPrep          = "pi.op.preparation"
	namespacePendingEntry           = "pi.pending.entry"
	namespacePendingToolOutput      = "pi.pending.tool_output"
	namespacePendingAssistantFrames = "pi.pending.assistant_frame"
	namespaceSessionName            = "pi.session.name"
	namespaceEntryLabel             = "pi.entry.label"
)

// BranchTip is the tip value of one branch (pi: branchTip; Value<string |
// null>).
func BranchTip(branch string) Value { return MustValue(namespaceBranchTip, branch) }

// BranchTipPrefix is the branch-tip namespace prefix for scans (pi:
// branchTipInventoryPrefix).
func BranchTipPrefix() Value { return MustValue(namespaceBranchTip, "") }

// LaneConfig is one lane's durable configuration (pi: laneConfig;
// Value<LaneConfiguration>).
func LaneConfig(lane string) Value { return MustValue(namespaceLaneConfig, lane) }

// LaneState is one lane's idle/queueing state (pi: laneState; Value<LaneState>).
func LaneStateAddress(lane string) Value { return MustValue(namespaceLaneState, lane) }

// OperationResult is one terminal operation record (pi: operationResult;
// Value<OperationResultRecord>).
func OperationResult(operationID string) Value {
	return MustValue(namespaceOperationResult, operationID)
}

// OperationMetaAddress is one operation's meta (pi: operationMeta;
// Value<OperationMeta>).
func OperationMetaAddress(operationID string) Value {
	return MustValue(namespaceOperationMeta, operationID)
}

// OperationStateAddress is one operation's durable state (pi: operationState;
// Value<OperationState>).
func OperationStateAddress(operationID string) Value {
	return MustValue(namespaceOperationState, operationID)
}

// OperationToolArgs is one tool call's effective arguments (pi:
// operationToolArgs; Value<Record<string, JsonValue>>).
func OperationToolArgs(operationID, stepID string, sourceIndex int) Value {
	return MustValue(namespaceOperationToolArgs, fmt.Sprintf("%s:%s:%d", operationID, stepID, sourceIndex))
}

// OperationToolMemo is one tool invocation's memo (pi: operationToolMemo;
// Value<JsonValue>).
func OperationToolMemo(operationID, invocationID, name string) Value {
	return MustValue(namespaceOperationToolMemo, fmt.Sprintf("%s:%s:%s", operationID, invocationID, name))
}

// OperationPreparation is one structural task's preparation (pi:
// operationPreparation; Value<DurableStructuralPreparation>).
func OperationPreparation(operationID, taskID string) Value {
	return MustValue(namespaceOperationPrep, fmt.Sprintf("%s:%s", operationID, taskID))
}

// OperationToolArgsPrefix is the tool-args scan prefix (pi:
// operationToolArgsPrefix).
func OperationToolArgsPrefix(operationID, stepID string) Value {
	key := operationID + ":"
	if stepID != "" {
		key += stepID + ":"
	}
	return MustValue(namespaceOperationToolArgs, key)
}

// OperationToolMemoPrefix is the tool-memo scan prefix (pi:
// operationToolMemoPrefix).
func OperationToolMemoPrefix(operationID, invocationID string) Value {
	key := operationID + ":"
	if invocationID != "" {
		key += invocationID + ":"
	}
	return MustValue(namespaceOperationToolMemo, key)
}

// OperationPreparationPrefix is the preparation scan prefix (pi:
// operationPreparationPrefix).
func OperationPreparationPrefix(operationID string) Value {
	return MustValue(namespaceOperationPrep, operationID+":")
}

// PendingEntryAddress is one pending entry payload (pi: pendingEntry;
// Value<PendingEntry>).
func PendingEntryAddress(entryID string) Value { return MustValue(namespacePendingEntry, entryID) }

// PendingToolOutput is one pending tool output (pi: pendingToolOutput;
// Value<AgentToolResult>).
func PendingToolOutput(operationID, invocationID string) Value {
	return MustValue(namespacePendingToolOutput, fmt.Sprintf("%s:%s", operationID, invocationID))
}

// PendingToolOutputPrefix is the pending-tool-output scan prefix (pi:
// pendingToolOutputPrefix).
func PendingToolOutputPrefix(operationID string) Value {
	return MustValue(namespacePendingToolOutput, operationID+":")
}

// PendingAssistantFrames is one response's pending assistant frames (pi:
// pendingAssistantFrames; ValueList<AssistantMessageFrame>).
func PendingAssistantFrames(operationID, responseEntryID string) ValueList {
	return MustList(namespacePendingAssistantFrames, fmt.Sprintf("%s:%s", operationID, responseEntryID))
}

// SessionNameAddress is the session name (pi: sessionName; Value<string>).
var SessionNameAddress = MustValue(namespaceSessionName, "")

// EntryLabel is one entry's label (pi: entryLabel; Value<string>).
func EntryLabel(entryID string) Value { return MustValue(namespaceEntryLabel, entryID) }
