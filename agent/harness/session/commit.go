// pi: harness/session/commit.ts — transaction write materialization and
// validation shared by every storage backend.
package session

import (
	"encoding/json"
	"fmt"
)

// CommittedWrite is one write with its storage-assigned sequence (pi:
// CommittedWrite union). Concrete kinds: CommittedEntryWrite,
// CommittedUsageWrite, CommittedValueSetWrite, CommittedValueDeleteWrite,
// CommittedListAppendWrite, CommittedListDeleteWrite.
type CommittedWrite interface {
	committedKind() string
	CommittedSeq() int64
}

// CommittedEntryWrite is an entry write with its assigned seq/timestamp (pi:
// Entry & { kind: "entry" } — the committed entry IS an Entry, flattened on
// the wire with a kind member).
type CommittedEntryWrite struct {
	Entry
}

func (w CommittedEntryWrite) committedKind() string { return "entry" }
func (w CommittedEntryWrite) CommittedSeq() int64   { return w.Base().Seq }

// MarshalJSON flattens the entry and adds kind (pi's Entry & { kind }).
func (w CommittedEntryWrite) MarshalJSON() ([]byte, error) {
	entryJSON, err := json.Marshal(w.Entry)
	if err != nil {
		return nil, err
	}
	var object map[string]any
	if err := json.Unmarshal(entryJSON, &object); err != nil {
		return nil, err
	}
	object["kind"] = "entry"
	return json.Marshal(object)
}

// UnmarshalJSON decodes the flattened shape through DecodeEntry.
func (w *CommittedEntryWrite) UnmarshalJSON(data []byte) error {
	entry, err := DecodeEntry(data)
	if err != nil {
		return err
	}
	w.Entry = entry
	return nil
}

// CommittedUsageWrite is a usage row with its assigned seq (pi: UsageRow &
// { kind: "usage" }).
type CommittedUsageWrite struct {
	UsageRow
	Kind string `json:"kind"`
}

func (w CommittedUsageWrite) committedKind() string { return "usage" }
func (w CommittedUsageWrite) CommittedSeq() int64   { return w.Seq }

// CommittedValueSetWrite is a scalar set with its assigned seq (pi:
// CommittedValueSetWrite).
type CommittedValueSetWrite struct {
	Kind      string `json:"kind"`
	Op        string `json:"op"`
	Seq       int64  `json:"seq"`
	Namespace string `json:"namespace"`
	Key       string `json:"key"`
	Value     any    `json:"value"`
}

func (w CommittedValueSetWrite) committedKind() string { return "value" }
func (w CommittedValueSetWrite) CommittedSeq() int64   { return w.Seq }

// CommittedValueDeleteWrite is a scalar delete with its assigned seq (pi:
// CommittedValueDeleteWrite).
type CommittedValueDeleteWrite struct {
	Kind      string `json:"kind"`
	Op        string `json:"op"`
	Seq       int64  `json:"seq"`
	Namespace string `json:"namespace"`
	Key       string `json:"key"`
}

func (w CommittedValueDeleteWrite) committedKind() string { return "value" }
func (w CommittedValueDeleteWrite) CommittedSeq() int64   { return w.Seq }

// CommittedListAppendWrite is a list append with its assigned seq (pi:
// CommittedListAppendWrite).
type CommittedListAppendWrite struct {
	Kind      string `json:"kind"`
	Op        string `json:"op"`
	Seq       int64  `json:"seq"`
	Namespace string `json:"namespace"`
	Key       string `json:"key"`
	Value     any    `json:"value"`
}

func (w CommittedListAppendWrite) committedKind() string { return "list" }
func (w CommittedListAppendWrite) CommittedSeq() int64   { return w.Seq }

// CommittedListDeleteWrite is a list delete with its assigned seq (pi:
// CommittedListDeleteWrite).
type CommittedListDeleteWrite struct {
	Kind      string `json:"kind"`
	Op        string `json:"op"`
	Seq       int64  `json:"seq"`
	Namespace string `json:"namespace"`
	Key       string `json:"key"`
}

func (w CommittedListDeleteWrite) committedKind() string { return "list" }
func (w CommittedListDeleteWrite) CommittedSeq() int64   { return w.Seq }

// PreparedCommit pairs the materialized writes with their commit result
// minus stats (pi: PreparedCommit).
type PreparedCommit struct {
	Writes []CommittedWrite
	Result struct {
		FirstSeq  int64   `json:"firstSeq"`
		Seqs      []int64 `json:"seqs"`
		Timestamp int64   `json:"timestamp"`
	}
}

// CommitValidationState is the duplicate/parent lookup surface used by
// ValidateCommittedWrites (pi: CommitValidationState).
type CommitValidationState interface {
	HasEntryOrUsageID(id string) bool
	HasEntryID(id string) bool
}

// InsertEntry wraps one provisioned entry as an entry write (pi:
// insertEntry).
func InsertEntry(entry Entry) EntryWrite { return EntryWrite{Entry: entry} }

// InsertUsage wraps one usage row (without seq) as a usage write (pi:
// insertUsage).
func InsertUsage(row UsageRow) UsageWrite { return UsageWrite{Row: row} }

// CommitWrite assigns seq (and, for entries, timestamp) to one write (pi:
// commitWrite).
func CommitWrite(write Write, seq int64, timestamp int64) (CommittedWrite, error) {
	switch w := write.(type) {
	case EntryWrite:
		base := w.Entry.Base()
		base.Seq = seq
		base.Timestamp = timestamp
		return CommittedEntryWrite{Entry: w.Entry}, nil
	case UsageWrite:
		w.Row.Seq = seq
		return CommittedUsageWrite{UsageRow: w.Row, Kind: "usage"}, nil
	case ValueSetWrite:
		return CommittedValueSetWrite{Kind: "value", Op: "set", Seq: seq, Namespace: w.Namespace, Key: w.Key, Value: w.Value}, nil
	case ValueDeleteWrite:
		return CommittedValueDeleteWrite{Kind: "value", Op: "delete", Seq: seq, Namespace: w.Namespace, Key: w.Key}, nil
	case ListAppendWrite:
		return CommittedListAppendWrite{Kind: "list", Op: "append", Seq: seq, Namespace: w.Namespace, Key: w.Key, Value: w.Value}, nil
	case ListDeleteWrite:
		return CommittedListDeleteWrite{Kind: "list", Op: "delete", Seq: seq, Namespace: w.Namespace, Key: w.Key}, nil
	default:
		return nil, fmt.Errorf("unknown write kind %T", write)
	}
}

// PrepareStorageCommit assigns consecutive sequences starting at firstSeq to
// every write (pi: prepareStorageCommit).
func PrepareStorageCommit(writes []Write, firstSeq int64, timestamp int64) (PreparedCommit, error) {
	committedWrites := make([]CommittedWrite, len(writes))
	seqs := make([]int64, len(writes))
	for i, write := range writes {
		cw, err := CommitWrite(write, firstSeq+int64(i), timestamp)
		if err != nil {
			return PreparedCommit{}, err
		}
		committedWrites[i] = cw
		seqs[i] = firstSeq + int64(i)
	}
	prepared := PreparedCommit{Writes: committedWrites}
	prepared.Result.FirstSeq = firstSeq
	prepared.Result.Seqs = seqs
	prepared.Result.Timestamp = timestamp
	return prepared, nil
}

// ValidateCommittedWrites enforces monotonic sequences, entry/usage id
// uniqueness, and parent existence (pi: validateCommittedWrites).
func ValidateCommittedWrites(writes []CommittedWrite, firstSeq int64, state CommitValidationState) error {
	previousSeq := firstSeq - 1
	transactionIDs := map[string]struct{}{}
	transactionEntryIDs := map[string]struct{}{}
	for _, write := range writes {
		seq := write.CommittedSeq()
		if seq <= previousSeq {
			return fmt.Errorf("Non-monotonic storage sequence: %d", seq)
		}
		previousSeq = seq
		var id string
		isEntry := false
		switch w := write.(type) {
		case CommittedEntryWrite:
			id = w.Base().ID
			isEntry = true
		case CommittedUsageWrite:
			id = w.ID
		default:
			continue
		}
		if state.HasEntryOrUsageID(id) {
			return fmt.Errorf("Duplicate entry or usage id: %s", id)
		}
		if _, dup := transactionIDs[id]; dup {
			return fmt.Errorf("Duplicate entry or usage id: %s", id)
		}
		if isEntry {
			parentID := write.(CommittedEntryWrite).Base().ParentID
			if parentID != nil {
				if !state.HasEntryID(*parentID) {
					if _, ok := transactionEntryIDs[*parentID]; !ok {
						return fmt.Errorf("Missing parent entry: %s", *parentID)
					}
				}
			}
			transactionEntryIDs[id] = struct{}{}
		}
		transactionIDs[id] = struct{}{}
	}
	return nil
}
