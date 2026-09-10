// pi: harness/session/in-memory-storage-state.ts — complete materialized
// session state for MemoryStorage and JsonlStorage.
//
// "Intentionally unsuitable for database backends and long-running sessions
// that may not fit in memory" (pi's comment): those backends query indexed
// durable state and update durable aggregates within each commit
// transaction. Not thread-safe; owners (MemoryStorage / JsonlStorage) guard
// it with their commit mutex.
package session

import (
	"fmt"
	"sort"

	"github.com/ZoroSpace/pig/agent/harness/utils"
)

type storedListSnapshot struct {
	address  ValueList
	elements []ListElement
}

// physicalKey joins namespace and key (pi: physicalKey's \u0000 join).
func physicalKey(namespace, key string) string {
	return namespace + "\x00" + key
}

// compareKeys orders keys by Unicode code points (pi: compareKeys via
// Array.from code points).
func compareKeys(left, right string) int {
	l := []rune(left)
	r := []rune(right)
	n := len(l)
	if len(r) < n {
		n = len(r)
	}
	for i := 0; i < n; i++ {
		if l[i] != r[i] {
			if l[i] < r[i] {
				return -1
			}
			return 1
		}
	}
	return len(l) - len(r)
}

// InMemoryStorageState is the shared materialized state (pi:
// InMemoryStorageState).
type InMemoryStorageState struct {
	entries      map[string]Entry
	entriesBySeq []Entry
	scalarValues map[string]StoredValue
	listValues   map[string]*storedListSnapshot
	usage        map[string]UsageRow
	stats        SessionStats
	nextSeq      int64
}

// NewInMemoryStorageState builds an empty state.
func NewInMemoryStorageState() *InMemoryStorageState {
	return &InMemoryStorageState{
		entries:      map[string]Entry{},
		scalarValues: map[string]StoredValue{},
		listValues:   map[string]*storedListSnapshot{},
		usage:        map[string]UsageRow{},
		stats:        SessionStats{Usage: utils.EmptyUsage()},
		nextSeq:      1,
	}
}

// PrepareCommit materializes and validates a transaction at the next free
// sequence (pi: prepareCommit).
func (s *InMemoryStorageState) PrepareCommit(writes []Write, timestamp int64) (PreparedCommit, error) {
	prepared, err := PrepareStorageCommit(writes, s.nextSeq, timestamp)
	if err != nil {
		return PreparedCommit{}, err
	}
	if err := s.ValidateCommitted(prepared.Writes); err != nil {
		return PreparedCommit{}, err
	}
	return prepared, nil
}

// ValidateCommitted validates writes against the current state (pi:
// validateCommitted).
func (s *InMemoryStorageState) ValidateCommitted(writes []CommittedWrite) error {
	return ValidateCommittedWrites(writes, s.nextSeq, &stateValidationView{state: s})
}

type stateValidationView struct{ state *InMemoryStorageState }

func (v *stateValidationView) HasEntryOrUsageID(id string) bool {
	_, hasEntry := v.state.entries[id]
	_, hasUsage := v.state.usage[id]
	return hasEntry || hasUsage
}

func (v *stateValidationView) HasEntryID(id string) bool {
	_, ok := v.state.entries[id]
	return ok
}

// ApplyValidated applies writes already accepted by ValidateCommitted and
// returns the post-apply totals (pi: applyValidated).
func (s *InMemoryStorageState) ApplyValidated(writes []CommittedWrite) SessionStats {
	for _, write := range writes {
		switch w := write.(type) {
		case CommittedEntryWrite:
			base := w.Entry.Base()
			s.entries[base.ID] = w.Entry
			s.entriesBySeq = append(s.entriesBySeq, w.Entry)
			if w.Entry.EntryType() == EntryTypeMessage {
				s.stats.MessageCount++
			}
		case CommittedUsageWrite:
			s.usage[w.ID] = w.UsageRow
			s.stats.Usage = utils.AddUsage(s.stats.Usage, w.Usage)
		case CommittedValueSetWrite:
			key := physicalKey(w.Namespace, w.Key)
			s.scalarValues[key] = StoredValue{
				Address: Value{Namespace: w.Namespace, Key: w.Key, kind: ValueKindValue},
				Value:   w.Value,
				Seq:     w.Seq,
			}
		case CommittedValueDeleteWrite:
			delete(s.scalarValues, physicalKey(w.Namespace, w.Key))
		case CommittedListAppendWrite:
			key := physicalKey(w.Namespace, w.Key)
			stored, ok := s.listValues[key]
			element := ListElement{Seq: w.Seq, Value: w.Value}
			if !ok {
				s.listValues[key] = &storedListSnapshot{
					address:  ValueList{Namespace: w.Namespace, Key: w.Key, kind: ValueKindList},
					elements: []ListElement{element},
				}
			} else {
				stored.elements = append(stored.elements, element)
			}
		case CommittedListDeleteWrite:
			delete(s.listValues, physicalKey(w.Namespace, w.Key))
		}
		s.nextSeq = write.CommittedSeq() + 1
	}
	return s.stats
}

// AdvanceNextSeq raises the sequence high-water mark (pi: advanceNextSeq).
func (s *InMemoryStorageState) AdvanceNextSeq(nextSeq int64) error {
	if nextSeq < 1 {
		return fmt.Errorf("Invalid storage sequence high-water mark: %d", nextSeq)
	}
	if nextSeq > s.nextSeq {
		s.nextSeq = nextSeq
	}
	return nil
}

// GetEntries returns the requested entries that exist (pi: getEntries).
func (s *InMemoryStorageState) GetEntries(ids []string) map[string]Entry {
	found := map[string]Entry{}
	for _, id := range ids {
		if entry, ok := s.entries[id]; ok {
			found[id] = entry
		}
	}
	return found
}

// GetValue returns one scalar (pi: getValue).
func (s *InMemoryStorageState) GetValue(address Value) *StoredValue {
	if stored, ok := s.scalarValues[physicalKey(address.Namespace, address.Key)]; ok {
		return &stored
	}
	return nil
}

// ScanValues returns scalars in one namespace whose key starts with the
// prefix, ordered by key (pi: scanValues).
func (s *InMemoryStorageState) ScanValues(prefix Value) []StoredValue {
	var out []StoredValue
	for _, stored := range s.scalarValues {
		if stored.Address.Namespace == prefix.Namespace && hasKeyPrefix(stored.Address.Key, prefix.Key) {
			out = append(out, stored)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return compareKeys(out[i].Address.Key, out[j].Address.Key) < 0
	})
	return out
}

func hasKeyPrefix(key, prefix string) bool {
	return len(key) >= len(prefix) && key[:len(prefix)] == prefix
}

// ReadList reads one list with paging (pi: readList).
func (s *InMemoryStorageState) ReadList(address ValueList, options *ListReadOptions) ([]ListElement, error) {
	resolved, err := ResolveListReadOptions(options)
	if err != nil {
		return nil, err
	}
	var elements []ListElement
	if stored, ok := s.listValues[physicalKey(address.Namespace, address.Key)]; ok {
		elements = stored.elements
	}
	var filtered []ListElement
	for _, element := range elements {
		if resolved.Cursor != nil {
			if resolved.Order == "asc" && !(element.Seq > resolved.Cursor.Seq) {
				continue
			}
			if resolved.Order != "asc" && !(element.Seq < resolved.Cursor.Seq) {
				continue
			}
		}
		filtered = append(filtered, element)
	}
	if resolved.Order != "asc" {
		reversed := make([]ListElement, len(filtered))
		for i, element := range filtered {
			reversed[len(filtered)-1-i] = element
		}
		filtered = reversed
	}
	if len(filtered) > resolved.Limit {
		filtered = filtered[:resolved.Limit]
	}
	return filtered, nil
}

// ScanBranch walks one branch path from start toward the root (pi:
// scanBranch).
func (s *InMemoryStorageState) ScanBranch(query StorageBranchScan) ([]Entry, error) {
	start, ok := s.entries[query.Start]
	if !ok {
		return nil, fmt.Errorf("Unknown branch start: %s", query.Start)
	}

	var path []Entry
	entry := start
	for {
		path = append(path, entry)
		base := entry.Base()
		if base.ParentID == nil {
			break
		}
		parent, ok := s.entries[*base.ParentID]
		if !ok {
			return nil, fmt.Errorf("Corrupt branch: missing parent")
		}
		entry = parent
	}
	if query.Order == "oldestFirst" {
		reversed := make([]Entry, len(path))
		for i, e := range path {
			reversed[len(path)-1-i] = e
		}
		path = reversed
	}

	var stopped []Entry
	for _, candidate := range path {
		stopped = append(stopped, candidate)
		base := candidate.Base()
		if (query.StopAtID != nil && base.ID == *query.StopAtID) ||
			(query.StopAtType != nil && base.Type == *query.StopAtType) {
			break
		}
	}
	var filtered []Entry
	for _, candidate := range stopped {
		base := candidate.Base()
		if query.Type != nil && base.Type != *query.Type {
			continue
		}
		if query.CustomType != nil && !stringPtrEqual(base.CustomType, *query.CustomType) {
			continue
		}
		if query.Cursor != nil {
			if query.Order == "oldestFirst" && !(base.Seq > query.Cursor.Seq) {
				continue
			}
			if query.Order != "oldestFirst" && !(base.Seq < query.Cursor.Seq) {
				continue
			}
		}
		filtered = append(filtered, candidate)
	}
	if query.Limit != nil {
		limit := *query.Limit
		if limit < 0 {
			limit = 0
		}
		if len(filtered) > limit {
			filtered = filtered[:limit]
		}
	}
	return filtered, nil
}

func stringPtrEqual(p *string, s string) bool {
	if p == nil {
		return s == ""
	}
	return *p == s
}

// ScanBranchStructure projects a branch scan to entry structures (pi:
// scanBranchStructure).
func (s *InMemoryStorageState) ScanBranchStructure(query StorageBranchScan) ([]EntryStructure, error) {
	entries, err := s.ScanBranch(query)
	if err != nil {
		return nil, err
	}
	out := make([]EntryStructure, len(entries))
	for i, entry := range entries {
		base := entry.Base()
		out[i] = EntryStructure{
			ID:         base.ID,
			ParentID:   base.ParentID,
			Seq:        base.Seq,
			Timestamp:  base.Timestamp,
			Type:       base.Type,
			CustomType: base.CustomType,
		}
	}
	return out, nil
}

// ScanEntries scans all entries by sequence window (pi: scanEntries).
func (s *InMemoryStorageState) ScanEntries(query EntryScan) []Entry {
	limit := int64(-1)
	if query.Limit != nil {
		limit = int64(*query.Limit)
		if limit < 0 {
			limit = 0
		}
	}
	var entries []Entry
	descending := query.Order == "desc"
	count := int64(0)
	n := len(s.entriesBySeq)
	for i := 0; i < n; i++ {
		index := i
		if descending {
			index = n - 1 - i
		}
		if limit >= 0 && count >= limit {
			break
		}
		entry := s.entriesBySeq[index]
		base := entry.Base()
		if query.Type != nil && base.Type != *query.Type {
			continue
		}
		if query.CustomType != nil && !stringPtrEqual(base.CustomType, *query.CustomType) {
			continue
		}
		if query.FromSeq != nil && base.Seq < *query.FromSeq {
			continue
		}
		if query.ToSeq != nil && base.Seq > *query.ToSeq {
			continue
		}
		entries = append(entries, entry)
		count++
	}
	return entries
}

// ScanUsage scans usage rows by sequence window (pi: scanUsage).
func (s *InMemoryStorageState) ScanUsage(query UsageScan) []UsageRow {
	rows := make([]UsageRow, 0, len(s.usage))
	for _, row := range s.usage {
		if query.FromSeq != nil && row.Seq < *query.FromSeq {
			continue
		}
		if query.ToSeq != nil && row.Seq > *query.ToSeq {
			continue
		}
		rows = append(rows, row)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if query.Order == "desc" {
			return rows[i].Seq > rows[j].Seq
		}
		return rows[i].Seq < rows[j].Seq
	})
	if query.Limit != nil {
		limit := *query.Limit
		if limit < 0 {
			limit = 0
		}
		if len(rows) > limit {
			rows = rows[:limit]
		}
	}
	return rows
}

// GetStats returns the aggregate ledger (pi: getStats).
func (s *InMemoryStorageState) GetStats() SessionStats {
	return s.stats
}

// SnapshotEntriesAndValues captures the state for forking at a serialized
// boundary (pi: snapshotEntriesAndValues).
func (s *InMemoryStorageState) SnapshotEntriesAndValues() (ForkSourceSnapshot, error) {
	entries := make([]Entry, len(s.entriesBySeq))
	copy(entries, s.entriesBySeq)
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].Base().Seq < entries[j].Base().Seq
	})
	var values []StoredValue
	for _, stored := range s.scalarValues {
		values = append(values, stored)
	}
	return ForkSourceSnapshot{Entries: entries, ScalarValues: values}, nil
}

// ensure the types import stays used if Usage references change.
