// pi: src/harness/session/session.ts — Session, the SessionTree facade over a
// SessionStorage with provisioned-entry validation and lane views.
//
// Deviations:
//   - TS async methods return Promises; Go methods return (T, error).
//   - TS `getEntry(): Promise<Entry | undefined>` reads as (nil, nil).
//   - view(lane) returns the session itself for "main" (TS returns this);
//     other lanes get a laneView struct implementing SessionTree.
//   - assertJsonSerializable collapses to a JSON marshal check with an
//     explicit reflection-based cycle probe: Go typed values cannot be
//     sparse arrays, accessor-bearing objects, or symbol-keyed, and
//     encoding/json rejects non-finite numbers and unsupported types — but
//     it does NOT detect cycles (json.Marshal on cyclic values exhausts
//     the stack fatally), so cycles are checked separately.
//     TS-only rejections (undefined-valued properties, BigInt, Map/class
//     instances) are unrepresentable in Go's JSON data model.
package session

import (
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/ai"
)

// SessionOptions parameterizes session construction (pi: constructor options
// object { idGenerator?: IdGenerator }).
type SessionOptions struct {
	IDGenerator IdGenerator
}

// AssertJsonSerializable rejects values encoding/json cannot round-trip
// (pi: assertJsonSerializable). Cycle detection is explicit (reflection
// walk): encoding/json does NOT detect cycles — json.Marshal on a cyclic
// map/slice exhausts the stack fatally, so it must be rejected before
// marshal. See the file-header deviation note for the TS-only rejections Go
// cannot express.
func AssertJsonSerializable(value any) error {
	if hasJSONCycle(reflect.ValueOf(value), map[uintptr]struct{}{}) {
		return NewSessionError(CodeInvalidPayload, "Durable payload contains a cyclic reference")
	}
	if _, err := json.Marshal(value); err != nil {
		return NewSessionError(CodeInvalidPayload, fmt.Sprintf("Durable payload %v", err))
	}
	return nil
}

// hasJSONCycle walks a reflect.Value recursively, reporting whether the
// value contains a reference cycle (pointers, maps, slices, and interfaces
// are tracked along the current descent path).
func hasJSONCycle(v reflect.Value, visiting map[uintptr]struct{}) bool {
	switch v.Kind() {
	case reflect.Interface, reflect.Pointer:
		if v.IsNil() {
			return false
		}
		if v.Kind() == reflect.Pointer {
			key := v.Pointer()
			if _, seen := visiting[key]; seen {
				return true
			}
			visiting[key] = struct{}{}
			defer delete(visiting, key)
		}
		return hasJSONCycle(v.Elem(), visiting)
	case reflect.Map:
		if v.IsNil() {
			return false
		}
		key := v.Pointer()
		if _, seen := visiting[key]; seen {
			return true
		}
		visiting[key] = struct{}{}
		defer delete(visiting, key)
		iter := v.MapRange()
		for iter.Next() {
			if hasJSONCycle(iter.Key(), visiting) || hasJSONCycle(iter.Value(), visiting) {
				return true
			}
		}
		return false
	case reflect.Slice, reflect.Array:
		if v.Kind() == reflect.Slice {
			if v.IsNil() {
				return false
			}
			key := v.Pointer()
			if _, seen := visiting[key]; seen {
				return true
			}
			visiting[key] = struct{}{}
			defer delete(visiting, key)
		}
		for i := 0; i < v.Len(); i++ {
			if hasJSONCycle(v.Index(i), visiting) {
				return true
			}
		}
		return false
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if hasJSONCycle(v.Field(i), visiting) {
				return true
			}
		}
		return false
	default:
		return false
	}
}

// Session is the read/write facade over one session's storage (pi: Session
// class).
type Session struct {
	storage SessionStorage
	// IDGenerator is the injectable id source; defaults to UUIDv7.
	IDGenerator IdGenerator
}

// NewSession builds a Session over storage; options may be nil.
func NewSession(storage SessionStorage, options *SessionOptions) *Session {
	s := &Session{storage: storage}
	if options != nil && options.IDGenerator != nil {
		s.IDGenerator = options.IDGenerator
	} else {
		s.IDGenerator = uuidv7Generator{}
	}
	return s
}

// uuidv7Generator is the default IdGenerator (pi: { next: () => uuidv7() }).
type uuidv7Generator struct{}

func (uuidv7Generator) Next() string { return ai.UUIDv7() }
func (s *Session) GetMetadata() (SessionMetadata, error) {
	return s.storage.GetMetadata()
}

// View returns a SessionTree scoped to lane; "main" returns the session
// itself.
func (s *Session) View(lane string) SessionTree {
	if lane == "main" {
		return s
	}
	return &laneView{session: s, lane: lane}
}

func (s *Session) GetLeafID() (*string, error) {
	return s.getLeafIDForLane("main")
}

func (s *Session) GetEntry(id string) (Entry, error) {
	return s.storage.GetEntry(id)
}

func (s *Session) GetStats() (SessionStats, error) {
	return s.storage.GetStats()
}

func (s *Session) GetName() (*string, error) {
	return s.storage.GetName()
}

func (s *Session) SetName(name *string) error {
	return s.storage.SetName(name)
}

func (s *Session) GetLabel(targetID string) (*string, error) {
	return s.storage.GetLabel(targetID)
}

func (s *Session) SetLabel(targetID string, label *string) error {
	return s.storage.SetLabel(targetID, label)
}

func (s *Session) FindEntries(query *EntryQuery) ([]Entry, error) {
	return s.queryEntries(query, nil)
}

func (s *Session) FindEntry(query *EntryQuery) (Entry, error) {
	one := 1
	entries, err := s.queryEntries(query, &one)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, nil
	}
	return entries[0], nil
}

func (s *Session) FindEntriesOnBranch(query *BranchQuery) ([]Entry, error) {
	return s.queryBranchEntries("main", query, nil)
}

func (s *Session) FindEntryOnBranch(query *BranchQuery) (Entry, error) {
	one := 1
	entries, err := s.queryBranchEntries("main", query, &one)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, nil
	}
	return entries[0], nil
}

func (s *Session) AppendMessage(message agent.AgentMessage) (string, error) {
	return s.appendMessageToLane("main", message)
}

func (s *Session) AppendCustomEntry(customType string, data any) (string, error) {
	return s.appendCustomEntryToLane("main", customType, data)
}

func (s *Session) GetLanes() ([]LanePointer, error) {
	return s.storage.GetLanes()
}

func (s *Session) CreateLane(lane string, at *string) error {
	return s.storage.CreateLane(lane, at)
}

func (s *Session) MoveLane(lane string, to *string) error {
	return s.storage.MoveLane(lane, to)
}

// AppendEntry commits a provisioned entry (storage-assigned fields zero) to
// lane and returns the committed entry.
func (s *Session) AppendEntry(entry Entry, lane string) (Entry, error) {
	return s.commitEntry(entry, lane)
}

// AppendRecord commits a new record (seq/timestamp zero) and returns it with
// the storage-assigned fields filled.
func (s *Session) AppendRecord(record LaneRecord) (LaneRecord, error) {
	return s.commitRecord(record)
}

func (s *Session) FindRecords(query *RecordQuery) ([]LaneRecord, error) {
	return s.queryRecords(query)
}

func (s *Session) FindOpenOperations(lane string, limit *int) ([]OperationStartedRecord, error) {
	if err := assertValidLimit(limit); err != nil {
		return nil, err
	}
	return s.storage.FindOpenOperations(lane, limit)
}

func (s *Session) GetLog(options *LogOptions) ([]LogItem, error) {
	return s.queryLog(options)
}

// getLeafIDForLane returns the lane's current leaf, or nil when empty.
// Returns an invalid_lane error when the lane does not exist.
func (s *Session) getLeafIDForLane(lane string) (*string, error) {
	pointers, err := s.GetLanes()
	if err != nil {
		return nil, err
	}
	for _, candidate := range pointers {
		if candidate.Lane == lane {
			return candidate.LeafID, nil
		}
	}
	return nil, NewSessionError(CodeInvalidLane, fmt.Sprintf("Lane not found: %s", lane))
}

func (s *Session) queryEntries(query *EntryQuery, resultLimit *int) ([]Entry, error) {
	if query == nil {
		query = &EntryQuery{}
	}
	if err := assertValidLimit(query.Limit); err != nil {
		return nil, err
	}
	if err := assertValidCursorAfterSeq(query.Cursor); err != nil {
		return nil, err
	}
	storageQuery := query
	// TS: resultLimit === query.limit ? query : { ...query, limit: resultLimit }
	// — the cap only applies when a resultLimit was passed (findEntry's 1);
	// nil means "keep the caller's limit".
	if resultLimit != nil && !limitsEqual(query.Limit, resultLimit) {
		effective := *query
		effective.Limit = resultLimit
		storageQuery = &effective
	}
	return s.storage.FindEntries(storageQuery)
}

// queryBranchEntries queries from query.Start toward the root, defaulting to
// defaultLane's current leaf. resultLimit lets single-entry queries cap
// results without changing the caller's query.
func (s *Session) queryBranchEntries(defaultLane string, query *BranchQuery, resultLimit *int) ([]Entry, error) {
	if query == nil {
		query = &BranchQuery{}
	}
	if err := assertValidLimit(query.Limit); err != nil {
		return nil, err
	}
	if err := assertValidCursorAfterSeq(query.Cursor); err != nil {
		return nil, err
	}
	start := query.Start
	if start == nil {
		leaf, err := s.getLeafIDForLane(defaultLane)
		if err != nil {
			return nil, err
		}
		start = leaf
	}
	if start == nil {
		return []Entry{}, nil
	}
	storageQuery := BranchEntryQuery{BranchQuery: *query, Start: *start}
	if resultLimit != nil && !limitsEqual(query.Limit, resultLimit) {
		storageQuery.Limit = resultLimit
	}
	return s.storage.FindEntriesOnBranch(storageQuery)
}

func (s *Session) queryRecords(query *RecordQuery) ([]LaneRecord, error) {
	if query == nil {
		query = &RecordQuery{}
	}
	if err := assertValidLimit(query.Limit); err != nil {
		return nil, err
	}
	if err := assertValidCursorInt64(query.AfterSeq); err != nil {
		return nil, err
	}
	if query.OperationKind != nil && (query.Type == nil || *query.Type != RecordTypeOperationStarted) {
		return nil, NewSessionError(CodeInvalidQuery, `operationKind requires type "operation_started"`)
	}
	return s.storage.FindRecords(query)
}

func (s *Session) queryLog(options *LogOptions) ([]LogItem, error) {
	if options == nil {
		options = &LogOptions{}
	}
	if err := assertValidLimit(options.Limit); err != nil {
		return nil, err
	}
	if err := assertValidCursorInt64(options.AfterSeq); err != nil {
		return nil, err
	}
	return s.storage.GetLog(options)
}

func (s *Session) appendMessageToLane(lane string, message agent.AgentMessage) (string, error) {
	entry, err := s.commitEntry(
		&MessageEntry{EntryBase: EntryBase{Type: EntryTypeMessage, ID: s.IDGenerator.Next()}, Message: message},
		lane,
	)
	if err != nil {
		return "", err
	}
	return entry.Base().ID, nil
}

func (s *Session) appendCustomEntryToLane(lane string, customType string, data any) (string, error) {
	entry, err := s.commitEntry(
		&CustomEntry{EntryBase: EntryBase{Type: EntryTypeCustom, ID: s.IDGenerator.Next()}, CustomType: customType, Data: data},
		lane,
	)
	if err != nil {
		return "", err
	}
	return entry.Base().ID, nil
}

func (s *Session) commitEntry(entry Entry, lane string) (Entry, error) {
	if err := AssertJsonSerializable(entry); err != nil {
		return nil, err
	}
	return s.storage.AppendEntry(entry, lane)
}

func (s *Session) commitRecord(record LaneRecord) (LaneRecord, error) {
	if err := AssertJsonSerializable(record); err != nil {
		return nil, err
	}
	return s.storage.AppendRecord(record)
}

// laneView is the non-main SessionTree view (pi: the object literal returned
// by Session.view). It rebinds leaf resolution and appends to the lane while
// sharing everything else with the session.
type laneView struct {
	session *Session
	lane    string
}

func (v *laneView) GetLeafID() (*string, error) {
	return v.session.getLeafIDForLane(v.lane)
}

func (v *laneView) GetEntry(id string) (Entry, error) {
	return v.session.GetEntry(id)
}

func (v *laneView) GetStats() (SessionStats, error) {
	return v.session.GetStats()
}

func (v *laneView) GetName() (*string, error) {
	return v.session.GetName()
}

func (v *laneView) SetName(name *string) error {
	return v.session.SetName(name)
}

func (v *laneView) GetLabel(targetID string) (*string, error) {
	return v.session.GetLabel(targetID)
}

func (v *laneView) SetLabel(targetID string, label *string) error {
	return v.session.SetLabel(targetID, label)
}

func (v *laneView) FindEntries(query *EntryQuery) ([]Entry, error) {
	return v.session.queryEntries(query, nil)
}

func (v *laneView) FindEntry(query *EntryQuery) (Entry, error) {
	return v.session.FindEntry(query)
}

func (v *laneView) FindEntriesOnBranch(query *BranchQuery) ([]Entry, error) {
	return v.session.queryBranchEntries(v.lane, query, nil)
}

func (v *laneView) FindEntryOnBranch(query *BranchQuery) (Entry, error) {
	one := 1
	entries, err := v.session.queryBranchEntries(v.lane, query, &one)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, nil
	}
	return entries[0], nil
}

func (v *laneView) AppendMessage(message agent.AgentMessage) (string, error) {
	return v.session.appendMessageToLane(v.lane, message)
}

func (v *laneView) AppendCustomEntry(customType string, data any) (string, error) {
	return v.session.appendCustomEntryToLane(v.lane, customType, data)
}

// assertValidLimit validates a query limit (pi: assertValidLimit).
func assertValidLimit(limit *int) error {
	if limit != nil && (*limit <= 0) {
		return NewSessionError(CodeInvalidQuery, "limit must be a positive integer")
	}
	return nil
}

// assertValidCursorAfterSeq validates an entry-query cursor (pi:
// assertValidCursor).
func assertValidCursorAfterSeq(cursor *EntryCursor) error {
	if cursor != nil && cursor.AfterSeq < 0 {
		return NewSessionError(CodeInvalidQuery, "cursor sequence must be a non-negative integer")
	}
	return nil
}

// assertValidCursorInt64 validates a record/log afterSeq bound (pi:
// assertValidCursor). Note: pi checks Number.isInteger here too; Go int64 is
// always integral.
func assertValidCursorInt64(afterSeq *int64) error {
	if afterSeq != nil && *afterSeq < 0 {
		return NewSessionError(CodeInvalidQuery, "cursor sequence must be a non-negative integer")
	}
	return nil
}

func limitsEqual(a, b *int) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// nowMillis is the storage timestamp source (pi: Date.now()).
func nowMillis() int64 { return time.Now().UnixMilli() }
