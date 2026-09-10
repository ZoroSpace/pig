// pi: src/harness/session/state.ts — SessionState, the in-memory reducer over
// the unified mutation log: sequence assignment, lane bookkeeping, open-
// operation tracking, ledger statistics, and fork mutation generation.
//
// Deviations:
//   - pi's lanes Map preserves insertion order (main first); Go maps are
//     unordered, so laneOrder keeps the insertion sequence alongside
//     lanesByName.
//   - pi's openOperationsByLane Map preserves start order; Go keeps an
//     ordered slice per lane (openOperationsByLane) with the same newest-
//     first read semantics.
//   - SessionMutation / LogItem unions are single structs with a Kind
//     discriminant (per §3.4 event-union rule).
//   - TS throws SessionError synchronously; Go returns error.
package session

import (
	"encoding/json"
	"fmt"
)

// SessionMutationKind discriminates the mutation union (pi: SessionMutation
// discriminated union).
type SessionMutationKind string

const (
	MutationKindEntry  SessionMutationKind = "entry"
	MutationKindRecord SessionMutationKind = "record"
	MutationKindLane   SessionMutationKind = "lane"
	MutationKindFact   SessionMutationKind = "fact"
)

// SessionMutation is one applied mutation (pi: SessionMutation union),
// collapsed to a single struct. Field ownership by kind: Entry (+ Lane for
// lane-scoped appends; Lane nil replays a storage-assigned entry) for
// "entry"; Record for "record"; Seq+Lane+LeafID for "lane"; Seq+Fact+Name
// (fact "name") or Seq+Fact+TargetID+Label (fact "label") for "fact".
type SessionMutation struct {
	Kind     SessionMutationKind `json:"kind"`
	Seq      int64               `json:"seq"`
	Lane     *string             `json:"lane,omitempty"`
	Entry    Entry               `json:"entry,omitempty"`
	Record   LaneRecord          `json:"record,omitempty"`
	LeafID   *string             `json:"leafId,omitempty"`
	Fact     string              `json:"fact,omitempty"` // "name" | "label"
	Name     *string             `json:"name,omitempty"`
	TargetID string              `json:"targetId,omitempty"`
	Label    *string             `json:"label,omitempty"`
}

// SessionState is the in-memory session state machine (pi: SessionState
// class). It is not safe for concurrent use; callers must serialize access.
type SessionState struct {
	sequence             int64
	usedIDs              map[string]struct{}
	entries              []Entry
	entriesByID          map[string]Entry
	records              []LaneRecord
	openOperationsByLane map[string][]OperationStartedRecord
	lanesByName          map[string]*string
	laneOrder            []string
	log                  []LogItem
	stats                SessionStats
	name                 *string
	labels               map[string]string
}

// NewSessionState builds a state with the "main" lane pre-created (pi: field
// initializer lanes = new Map([["main", null]])).
func NewSessionState() *SessionState {
	s := &SessionState{
		usedIDs:              map[string]struct{}{},
		entriesByID:          map[string]Entry{},
		openOperationsByLane: map[string][]OperationStartedRecord{},
		lanesByName:          map[string]*string{},
		labels:               map[string]string{},
	}
	s.lanesByName["main"] = nil
	s.laneOrder = append(s.laneOrder, "main")
	return s
}

// NextSequence returns the sequence the next mutation must carry (pi:
// nextSequence accessor).
func (s *SessionState) NextSequence() int64 {
	return s.sequence + 1
}

func (s *SessionState) GetLanes() []LanePointer {
	pointers := make([]LanePointer, 0, len(s.laneOrder))
	for _, lane := range s.laneOrder {
		leafID := s.lanesByName[lane]
		pointers = append(pointers, LanePointer{Lane: lane, LeafID: leafID})
	}
	return pointers
}

// RequireLane returns the lane's leaf or an invalid_lane error (pi:
// requireLane).
func (s *SessionState) RequireLane(lane string) (*string, error) {
	leafID, ok := s.lanesByName[lane]
	if !ok {
		return nil, NewSessionError(CodeInvalidLane, fmt.Sprintf("Lane not found: %s", lane))
	}
	return leafID, nil
}

// ValidateNewLane rejects lanes that already exist (pi: validateNewLane).
func (s *SessionState) ValidateNewLane(lane string) error {
	if _, ok := s.lanesByName[lane]; ok {
		return NewSessionError(CodeAlreadyExists, fmt.Sprintf("Lane already exists: %s", lane))
	}
	return nil
}

// ValidateTarget rejects non-nil targets that are not known entries (pi:
// validateTarget).
func (s *SessionState) ValidateTarget(targetID *string) error {
	if targetID != nil {
		if _, ok := s.entriesByID[*targetID]; !ok {
			return NewSessionError(CodeNotFound, fmt.Sprintf("Entry not found: %s", *targetID))
		}
	}
	return nil
}

// ValidateUnusedID rejects ids already taken by any entry or record (pi:
// validateUnusedId).
func (s *SessionState) ValidateUnusedID(id string) error {
	if _, ok := s.usedIDs[id]; ok {
		return NewSessionError(CodeAlreadyExists, fmt.Sprintf("Session id already exists: %s", id))
	}
	return nil
}

// InvalidMutationFunc mirrors pi's InvalidMutation callback type: it reports
// the failure. Go encoding: return the error instead of throwing from a
// callback.
type InvalidMutationFunc func(message string) error

func defaultInvalidMutation(message string) error {
	return NewSessionError(CodeInvalidEntry, fmt.Sprintf("Invalid session mutation: %s", message))
}

// ApplyMutation applies one mutation to the state (pi: applyMutation). The
// invalid callback defaults to the standard invalid_entry error.
func (s *SessionState) ApplyMutation(mutation SessionMutation, invalid InvalidMutationFunc) error {
	if invalid == nil {
		invalid = defaultInvalidMutation
	}

	var seq int64
	switch mutation.Kind {
	case MutationKindEntry:
		seq = mutation.Entry.Base().Seq
	case MutationKindRecord:
		seq = mutation.Record.Base().Seq
	default:
		seq = mutation.Seq
	}
	if seq != s.sequence+1 {
		return invalid(fmt.Sprintf("has non-consecutive seq %d", seq))
	}

	switch mutation.Kind {
	case MutationKindEntry:
		base := mutation.Entry.Base()
		if _, dup := s.usedIDs[base.ID]; dup {
			return invalid(fmt.Sprintf("contains duplicate id %s", mutation.Entry.Base().ID))
		}
		if mutation.Lane != nil {
			leafID, ok := s.lanesByName[*mutation.Lane]
			if !ok {
				return invalid(fmt.Sprintf("references missing lane %s", *mutation.Lane))
			}
			if !stringPtrEqual(base.ParentID, leafID) {
				return invalid("does not chain to the lane leaf")
			}
		}
		if base.ParentID != nil {
			if _, ok := s.entriesByID[*base.ParentID]; !ok {
				return invalid(fmt.Sprintf("references missing parent %s", *base.ParentID))
			}
		}
		s.sequence = seq
		s.usedIDs[base.ID] = struct{}{}
		s.entries = append(s.entries, mutation.Entry)
		s.entriesByID[base.ID] = mutation.Entry
		if mutation.Lane != nil {
			leafCopy := base.ID
			s.lanesByName[*mutation.Lane] = &leafCopy
		}
		s.log = append(s.log, LogItem{Kind: "entry", Seq: seq, Entry: mutation.Entry})
		if base.Type == EntryTypeMessage {
			s.stats.MessageCount++
		}
	case MutationKindRecord:
		if _, ok := s.lanesByName[mutation.Record.Base().Lane]; !ok {
			return invalid(fmt.Sprintf("references missing lane %s", mutation.Record.Base().Lane))
		}
		if _, dup := s.usedIDs[mutation.Record.Base().ID]; dup {
			return invalid(fmt.Sprintf("contains duplicate id %s", mutation.Record.Base().ID))
		}
		s.sequence = seq
		s.usedIDs[mutation.Record.Base().ID] = struct{}{}
		s.records = append(s.records, mutation.Record)
		base := mutation.Record.Base()
		if base.Type == RecordTypeOperationStarted {
			started := mutation.Record.(*OperationStartedRecord)
			s.openOperationsByLane[base.Lane] = append(s.openOperationsByLane[base.Lane], *started)
		} else if base.Type == RecordTypeOperationFinished {
			finished := mutation.Record.(*OperationFinishedRecord)
			s.removeOpenOperation(base.Lane, finished.RunID)
		}
		s.log = append(s.log, LogItem{Kind: "record", Seq: seq, Record: mutation.Record})
		if base.Type == RecordTypeUsage {
			usage := mutation.Record.(*UsageRecord).Usage
			s.stats.CachedTokens += usage.CacheRead
			s.stats.UncachedTokens += usage.Input + usage.CacheWrite
			s.stats.TotalTokens += usage.TotalTokens
			s.stats.CostTotal += usage.Cost.Total
		}
	case MutationKindLane:
		if mutation.LeafID != nil {
			if _, ok := s.entriesByID[*mutation.LeafID]; !ok {
				return invalid(fmt.Sprintf("references missing lane target %s", *mutation.LeafID))
			}
		}
		s.sequence = seq
		if _, ok := s.lanesByName[mutationLaneName(mutation)]; !ok {
			s.laneOrder = append(s.laneOrder, mutationLaneName(mutation))
		}
		s.lanesByName[mutationLaneName(mutation)] = mutation.LeafID
		s.log = append(s.log, LogItem{Kind: "lane", Seq: seq, Lane: mutationLaneName(mutation), LeafID: mutation.LeafID})
	case MutationKindFact:
		if mutation.Fact == "label" {
			if _, ok := s.entriesByID[mutation.TargetID]; !ok {
				return invalid(fmt.Sprintf("references missing label target %s", mutation.TargetID))
			}
		}
		s.sequence = seq
		if mutation.Fact == "name" {
			s.name = mutation.Name
			s.log = append(s.log, LogItem{Kind: "fact", Seq: seq, Fact: "name", Name: mutation.Name})
		} else {
			if mutation.Label == nil {
				delete(s.labels, mutation.TargetID)
			} else {
				s.labels[mutation.TargetID] = *mutation.Label
			}
			s.log = append(s.log, LogItem{Kind: "fact", Seq: seq, Fact: "label", TargetID: mutation.TargetID, Label: mutation.Label})
		}
	}
	return nil
}

// mutationLaneName returns the mutation's lane name for lane/fact mutations
// (pi: mutation.lane; nil maps to "" which never matches a real lane).
func mutationLaneName(m SessionMutation) string {
	if m.Lane == nil {
		return ""
	}
	return *m.Lane
}

// removeOpenOperation deletes the open operation with the given run id from
// the lane's ordered set (pi: openOperationsByLane.get(lane)?.delete(runId)).
func (s *SessionState) removeOpenOperation(lane string, runID string) {
	ops := s.openOperationsByLane[lane]
	for i, op := range ops {
		if op.Base().ID == runID {
			s.openOperationsByLane[lane] = append(ops[:i], ops[i+1:]...)
			return
		}
	}
}

func (s *SessionState) GetEntry(id string) Entry {
	return s.entriesByID[id]
}

func (s *SessionState) FindEntries(query *EntryQuery) ([]Entry, error) {
	if query == nil {
		query = &EntryQuery{}
	}
	if err := assertValidLimit(query.Limit); err != nil {
		return nil, err
	}
	if err := assertValidCursorAfterSeq(query.Cursor); err != nil {
		return nil, err
	}
	var results []Entry
	for _, entry := range s.orderedEntries(query.Order) {
		if !s.matchesEntryQuery(entry, query) {
			continue
		}
		results = append(results, entry)
		if query.Limit != nil && int64(len(results)) == int64(*query.Limit) {
			break
		}
	}
	return results, nil
}

func (s *SessionState) FindEntriesOnBranch(query BranchEntryQuery) ([]Entry, error) {
	if err := assertValidLimit(query.Limit); err != nil {
		return nil, err
	}
	if err := assertValidCursorAfterSeq(query.Cursor); err != nil {
		return nil, err
	}
	var results []Entry
	if query.Order == OrderOldestFirst {
		// pi: the oldestFirst path walks the full path to root (no bounds)
		// before reversing; bounds are only applied in the newestFirst walk.
		walked, err := s.walkToRoot(&query.Start, nil, nil)
		if err != nil {
			return nil, err
		}
		for i := len(walked) - 1; i >= 0; i-- {
			entry := walked[i]
			reachedBound := (query.StopAtID != nil && entry.Base().ID == *query.StopAtID) ||
				(query.StopAtType != nil && entry.Base().Type == *query.StopAtType)
			if s.matchesEntryQuery(entry, &query.EntryQuery) {
				results = append(results, entry)
			}
			if reachedBound || (query.Limit != nil && int64(len(results)) == int64(*query.Limit)) {
				break
			}
		}
	} else {
		current := query.Start
		for {
			entry, ok := s.entriesByID[current]
			if !ok {
				return nil, NewSessionError(CodeNotFound, fmt.Sprintf("Entry not found: %s", current))
			}
			if s.matchesEntryQuery(entry, &query.EntryQuery) {
				results = append(results, entry)
			}
			if (query.StopAtID != nil && entry.Base().ID == *query.StopAtID) ||
				(query.StopAtType != nil && entry.Base().Type == *query.StopAtType) ||
				entry.Base().ParentID == nil ||
				(query.Limit != nil && int64(len(results)) == int64(*query.Limit)) {
				break
			}
			current = *entry.Base().ParentID
		}
	}
	return results, nil
}

func (s *SessionState) FindRecords(query *RecordQuery) ([]LaneRecord, error) {
	if query == nil {
		query = &RecordQuery{}
	}
	if err := assertValidLimit(query.Limit); err != nil {
		return nil, err
	}
	if err := assertValidCursorInt64(query.AfterSeq); err != nil {
		return nil, err
	}
	var results []LaneRecord
	for _, record := range s.orderedRecords(query.Order) {
		if !s.matchesRecordQuery(record, query) {
			continue
		}
		results = append(results, record)
		if query.Limit != nil && int64(len(results)) == int64(*query.Limit) {
			break
		}
	}
	return results, nil
}

// FindOpenOperations returns the lane's open operations, newest first (pi:
// findOpenOperations).
func (s *SessionState) FindOpenOperations(lane string, limit *int) ([]OperationStartedRecord, error) {
	if err := assertValidLimit(limit); err != nil {
		return nil, err
	}
	ops := s.openOperationsByLane[lane]
	results := make([]OperationStartedRecord, 0, len(ops))
	for i := len(ops) - 1; i >= 0; i-- {
		results = append(results, ops[i])
	}
	if limit != nil && len(results) > *limit {
		results = results[:*limit]
	}
	return results, nil
}

func (s *SessionState) GetLog(options *LogOptions) ([]LogItem, error) {
	if options == nil {
		options = &LogOptions{}
	}
	if err := assertValidLimit(options.Limit); err != nil {
		return nil, err
	}
	if err := assertValidCursorInt64(options.AfterSeq); err != nil {
		return nil, err
	}
	var results []LogItem
	for _, item := range s.log {
		if options.AfterSeq != nil && item.Seq <= *options.AfterSeq {
			continue
		}
		results = append(results, item)
		if options.Limit != nil && int64(len(results)) == int64(*options.Limit) {
			break
		}
	}
	return results, nil
}

func (s *SessionState) GetName() *string {
	return s.name
}

func (s *SessionState) GetLabel(id string) (string, bool) {
	label, ok := s.labels[id]
	return label, ok
}

func (s *SessionState) GetStats() SessionStats {
	return s.stats
}

// CreateForkMutations generates the mutation sequence that reproduces the
// selected scope in a new session (pi: createForkMutations).
func (s *SessionState) CreateForkMutations(options *ForkOptions) ([]SessionMutation, error) {
	if options == nil {
		options = &ForkOptions{}
	}
	var copiedEntries []Entry
	var forkLanes []LanePointer
	if options.Scope == ForkScopeTree {
		oldest := OrderOldestFirst
		entries, err := s.FindEntries(&EntryQuery{Order: oldest})
		if err != nil {
			return nil, err
		}
		copiedEntries = entries
		forkLanes = s.GetLanes()
	} else {
		selectedEntryID := options.EntryID
		if selectedEntryID == nil {
			leaf, err := s.RequireLane("main")
			if err != nil {
				return nil, err
			}
			selectedEntryID = leaf
		}
		var targetID *string
		if selectedEntryID != nil {
			entry, ok := s.entriesByID[*selectedEntryID]
			if !ok || entry.Base().Type != EntryTypeMessage {
				return nil, NewSessionError(CodeInvalidForkTarget, fmt.Sprintf("Fork target is not a message entry: %s", *selectedEntryID))
			}
			position := options.Position
			if position == "" {
				if options.EntryID == nil {
					position = ForkPositionAt
				} else {
					position = ForkPositionBefore
				}
			}
			if position == ForkPositionAt {
				id := entry.Base().ID
				targetID = &id
			} else {
				targetID = entry.Base().ParentID
			}
		}
		if targetID != nil {
			entries, err := s.FindEntriesOnBranch(BranchEntryQuery{
				BranchQuery: BranchQuery{EntryQuery: EntryQuery{Order: OrderOldestFirst}},
				Start:       *targetID,
			})
			if err != nil {
				return nil, err
			}
			copiedEntries = entries
		}
		forkLanes = []LanePointer{{Lane: "main", LeafID: targetID}}
	}

	var mutations []SessionMutation
	sequence := int64(1)
	for _, sourceEntry := range copiedEntries {
		clone := cloneEntry(sourceEntry)
		clone.Base().Seq = sequence
		sequence++
		mutations = append(mutations, SessionMutation{Kind: MutationKindEntry, Entry: clone})
	}
	for _, pointer := range forkLanes {
		lane := pointer.Lane
		mutations = append(mutations, SessionMutation{Kind: MutationKindLane, Seq: sequence, Lane: &lane, LeafID: pointer.LeafID})
		sequence++
	}
	if s.name != nil {
		mutations = append(mutations, SessionMutation{Kind: MutationKindFact, Seq: sequence, Fact: "name", Name: s.name})
		sequence++
	}
	for _, entry := range copiedEntries {
		if label, ok := s.labels[entry.Base().ID]; ok {
			l := label
			targetID := entry.Base().ID
			mutations = append(mutations, SessionMutation{Kind: MutationKindFact, Seq: sequence, Fact: "label", TargetID: targetID, Label: &l})
			sequence++
		}
	}
	return mutations, nil
}

// walkToRoot yields entries from start toward the root (pi: walkToRoot
// generator). The oldestFirst branch path materializes the full walk before
// reversing.
func (s *SessionState) walkToRoot(start *string, stopAtID *string, stopAtType *EntryType) ([]Entry, error) {
	if start == nil {
		return nil, nil
	}
	visited := map[string]struct{}{}
	current, ok := s.entriesByID[*start]
	if !ok {
		return nil, NewSessionError(CodeNotFound, fmt.Sprintf("Entry not found: %s", *start))
	}
	var walked []Entry
	for {
		id := current.Base().ID
		if _, cycle := visited[id]; cycle {
			return nil, NewSessionError(CodeInvalidEntry, fmt.Sprintf("Session branch contains a cycle at %s", id))
		}
		visited[id] = struct{}{}
		walked = append(walked, current)
		if (stopAtID != nil && id == *stopAtID) || (stopAtType != nil && current.Base().Type == *stopAtType) || current.Base().ParentID == nil {
			break
		}
		parentID := *current.Base().ParentID
		current, ok = s.entriesByID[parentID]
		if !ok {
			return nil, NewSessionError(CodeInvalidEntry, fmt.Sprintf("Entry not found: %s", parentID))
		}
	}
	return walked, nil
}

// orderedEntries iterates entries in query order (pi: ordered generator).
func (s *SessionState) orderedEntries(order EntryOrder) []Entry {
	if order == OrderOldestFirst {
		return s.entries
	}
	reversed := make([]Entry, len(s.entries))
	for i, entry := range s.entries {
		reversed[len(s.entries)-1-i] = entry
	}
	return reversed
}

// orderedRecords iterates records in query order (pi: ordered generator).
func (s *SessionState) orderedRecords(order EntryOrder) []LaneRecord {
	if order == OrderOldestFirst {
		return s.records
	}
	reversed := make([]LaneRecord, len(s.records))
	for i, record := range s.records {
		reversed[len(s.records)-1-i] = record
	}
	return reversed
}

func (s *SessionState) matchesEntryQuery(entry Entry, query *EntryQuery) bool {
	base := entry.Base()
	if query.Type != nil && base.Type != *query.Type {
		return false
	}
	if query.CustomType != nil {
		custom, ok := entry.(*CustomEntry)
		if !ok || custom.CustomType != *query.CustomType {
			return false
		}
	}
	if query.Cursor != nil {
		if query.Order == OrderOldestFirst {
			if base.Seq <= query.Cursor.AfterSeq {
				return false
			}
		} else if base.Seq >= query.Cursor.AfterSeq {
			return false
		}
	}
	return true
}

// recordRunID extracts the operation identity of a record for runId queries
// (pi: `"runId" in record && record.runId === query.runId`, with the
// operation_started special case matching on record.id). The bool result
// reports whether the record carries a runId at all.
func recordRunID(record LaneRecord) (string, bool) {
	switch r := record.(type) {
	case *OperationStartedRecord:
		return r.Base().ID, true
	case *AbortRequestedRecord:
		return r.RunID, true
	case *OperationFinishedRecord:
		return r.RunID, true
	case *StepAttemptRecord:
		return r.RunID, true
	case *ToolStartedRecord:
		return r.RunID, true
	case *QueueEnqueuedRecord:
		if r.RunID == nil {
			return "", false
		}
		return *r.RunID, true
	case *QueueCancelledRecord:
		if r.RunID == nil {
			return "", false
		}
		return *r.RunID, true
	case *WriteDeferredRecord:
		return r.RunID, true
	case *UsageRecord:
		if r.RunID == nil {
			return "", false
		}
		return *r.RunID, true
	default:
		return "", false
	}
}

func (s *SessionState) matchesRecordQuery(record LaneRecord, query *RecordQuery) bool {
	base := record.Base()
	if query.Lane != nil && base.Lane != *query.Lane {
		return false
	}
	if query.Type != nil && base.Type != *query.Type {
		return false
	}
	if query.RunID != nil {
		runID, hasRunID := recordRunID(record)
		if !hasRunID || runID != *query.RunID {
			return false
		}
	}
	if query.OperationKind != nil {
		started, ok := record.(*OperationStartedRecord)
		if !ok || started.Intent.OperationIntentKind() != *query.OperationKind {
			return false
		}
	}
	if query.AfterSeq != nil && base.Seq <= *query.AfterSeq {
		return false
	}
	return true
}

func stringPtrEqual(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// cloneEntry deep-copies an entry through its JSON representation (pi:
// structuredClone). Entries are JSON-validated on write, so this cannot
// fail for stored values; a failure indicates a programming error and panics.
func cloneEntry(entry Entry) Entry {
	data, err := json.Marshal(entry)
	if err != nil {
		panic(fmt.Sprintf("session: cloneEntry marshal failed: %v", err))
	}
	clone, err := DecodeEntry(data)
	if err != nil {
		panic(fmt.Sprintf("session: cloneEntry decode failed: %v", err))
	}
	return clone
}

// cloneRecord deep-copies a lane record through its JSON representation (pi:
// structuredClone); same panic rationale as cloneEntry.
func cloneRecord(record LaneRecord) LaneRecord {
	data, err := json.Marshal(record)
	if err != nil {
		panic(fmt.Sprintf("session: cloneRecord marshal failed: %v", err))
	}
	clone, err := DecodeRecord(data)
	if err != nil {
		panic(fmt.Sprintf("session: cloneRecord decode failed: %v", err))
	}
	return clone
}

// cloneLogItem deep-copies a log item, cloning its entry/record payload.
func cloneLogItem(item LogItem) LogItem {
	switch item.Kind {
	case "entry":
		item.Entry = cloneEntry(item.Entry)
	case "record":
		item.Record = cloneRecord(item.Record)
	}
	return item
}
