// pi: src/harness/session/memory.ts — InMemorySessionStorage and
// InMemorySessionRepo.
//
// Deviations:
//   - TS relies on the single-threaded event loop for atomicity of each
//     storage call (no await before the state mutation); Go adds a mutex per
//     storage/repo so concurrent appenders linearize the same way (the
//     conformance suite asserts unique seqs across concurrent lane writes).
//   - structuredClone at every boundary → JSON round-trip clone helpers
//     (cloneEntry/cloneRecord/cloneLogItem in state.go, cloneMetadata here).
package session

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/samber/lo"
)

// InMemorySessionStorage is the volatile SessionStorage (pi:
// InMemorySessionStorage class).
type InMemorySessionStorage struct {
	mu       sync.Mutex
	metadata SessionMetadata
	state    *SessionState
}

var _ SessionStorage = (*InMemorySessionStorage)(nil)

// NewInMemorySessionStorage builds a storage over the given metadata.
func NewInMemorySessionStorage(metadata SessionMetadata) *InMemorySessionStorage {
	return &InMemorySessionStorage{
		metadata: cloneMetadata(metadata),
		state:    NewSessionState(),
	}
}

// Fork derives a new storage carrying the fork mutations of this one (pi:
// fork method).
func (s *InMemorySessionStorage) Fork(metadata SessionMetadata, options *ForkOptions) (*InMemorySessionStorage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	storage := NewInMemorySessionStorage(metadata)
	mutations, err := s.state.CreateForkMutations(options)
	if err != nil {
		return nil, err
	}
	for _, mutation := range mutations {
		if err := storage.state.ApplyMutation(mutation, nil); err != nil {
			return nil, err
		}
	}
	return storage, nil
}

func (s *InMemorySessionStorage) GetMetadata() (SessionMetadata, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneMetadata(s.metadata), nil
}

func (s *InMemorySessionStorage) GetLanes() ([]LanePointer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state.GetLanes(), nil
}

func (s *InMemorySessionStorage) CreateLane(lane string, at *string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.state.ValidateNewLane(lane); err != nil {
		return err
	}
	if err := s.state.ValidateTarget(at); err != nil {
		return err
	}
	return s.state.ApplyMutation(SessionMutation{
		Kind:   MutationKindLane,
		Seq:    s.state.NextSequence(),
		Lane:   &lane,
		LeafID: at,
	}, nil)
}

func (s *InMemorySessionStorage) MoveLane(lane string, to *string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.state.RequireLane(lane); err != nil {
		return err
	}
	if err := s.state.ValidateTarget(to); err != nil {
		return err
	}
	return s.state.ApplyMutation(SessionMutation{
		Kind:   MutationKindLane,
		Seq:    s.state.NextSequence(),
		Lane:   &lane,
		LeafID: to,
	}, nil)
}

func (s *InMemorySessionStorage) AppendEntry(newEntry Entry, lane string) (Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	parentID, err := s.state.RequireLane(lane)
	if err != nil {
		return nil, err
	}
	if err := s.state.ValidateUnusedID(newEntry.Base().ID); err != nil {
		return nil, err
	}
	entry := cloneEntry(newEntry)
	base := entry.Base()
	base.ParentID = parentID
	base.Seq = s.state.NextSequence()
	base.Timestamp = nowMillis()
	if err := s.state.ApplyMutation(SessionMutation{
		Kind:  MutationKindEntry,
		Lane:  &lane,
		Entry: entry,
	}, nil); err != nil {
		return nil, err
	}
	return cloneEntry(entry), nil
}

func (s *InMemorySessionStorage) AppendRecord(newRecord LaneRecord) (LaneRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.state.RequireLane(newRecord.Base().Lane); err != nil {
		return nil, err
	}
	if err := s.state.ValidateUnusedID(newRecord.Base().ID); err != nil {
		return nil, err
	}
	openOperations, err := s.state.FindOpenOperations(newRecord.Base().Lane, lo.ToPtr(1))
	if err != nil {
		return nil, err
	}
	if newRecord.Base().Type == RecordTypeOperationStarted && len(openOperations) > 0 {
		currentOpenOperationID := openOperations[0].Base().ID
		return nil, NewSessionError(CodeStorage, fmt.Sprintf(
			"Lane %s already has an open operation %s", newRecord.Base().Lane, currentOpenOperationID))
	}
	record := cloneRecord(newRecord)
	base := record.Base()
	base.Seq = s.state.NextSequence()
	base.Timestamp = nowMillis()
	if err := s.state.ApplyMutation(SessionMutation{
		Kind:   MutationKindRecord,
		Record: record,
	}, nil); err != nil {
		return nil, err
	}
	return cloneRecord(record), nil
}

func (s *InMemorySessionStorage) GetEntry(id string) (Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry := s.state.GetEntry(id)
	if entry == nil {
		return nil, nil
	}
	return cloneEntry(entry), nil
}

func (s *InMemorySessionStorage) FindEntries(query *EntryQuery) ([]Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := s.state.FindEntries(query)
	if err != nil {
		return nil, err
	}
	return cloneEntries(entries), nil
}

func (s *InMemorySessionStorage) FindEntriesOnBranch(query BranchEntryQuery) ([]Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := s.state.FindEntriesOnBranch(query)
	if err != nil {
		return nil, err
	}
	return cloneEntries(entries), nil
}

func (s *InMemorySessionStorage) FindRecords(query *RecordQuery) ([]LaneRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	records, err := s.state.FindRecords(query)
	if err != nil {
		return nil, err
	}
	return cloneRecords(records), nil
}

func (s *InMemorySessionStorage) FindOpenOperations(lane string, limit *int) ([]OperationStartedRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ops, err := s.state.FindOpenOperations(lane, limit)
	if err != nil {
		return nil, err
	}
	results := make([]OperationStartedRecord, len(ops))
	for i, op := range ops {
		value := op
		clone := cloneRecord(&value).(*OperationStartedRecord)
		results[i] = *clone
	}
	return results, nil
}

func (s *InMemorySessionStorage) GetLog(options *LogOptions) ([]LogItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	items, err := s.state.GetLog(options)
	if err != nil {
		return nil, err
	}
	results := make([]LogItem, len(items))
	for i, item := range items {
		results[i] = cloneLogItem(item)
	}
	return results, nil
}

func (s *InMemorySessionStorage) GetName() (*string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	name := s.state.GetName()
	if name == nil {
		return nil, nil
	}
	clone := *name
	return &clone, nil
}

func (s *InMemorySessionStorage) SetName(name *string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state.ApplyMutation(SessionMutation{
		Kind: MutationKindFact,
		Seq:  s.state.NextSequence(),
		Fact: "name",
		Name: name,
	}, nil)
}

func (s *InMemorySessionStorage) GetLabel(id string) (*string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	label, ok := s.state.GetLabel(id)
	if !ok {
		return nil, nil
	}
	return &label, nil
}

func (s *InMemorySessionStorage) SetLabel(id string, label *string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.state.ValidateTarget(&id); err != nil {
		return err
	}
	return s.state.ApplyMutation(SessionMutation{
		Kind:     MutationKindFact,
		Seq:      s.state.NextSequence(),
		Fact:     "label",
		TargetID: id,
		Label:    label,
	}, nil)
}

func (s *InMemorySessionStorage) GetStats() (SessionStats, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state.GetStats(), nil
}

// InMemorySessionRepo is the volatile SessionRepo (pi: InMemorySessionRepo
// class).
type InMemorySessionRepo struct {
	mu       sync.Mutex
	sessions map[string]*InMemorySessionStorage
}

var _ SessionRepo = (*InMemorySessionRepo)(nil)

// NewInMemorySessionRepo builds an empty repo.
func NewInMemorySessionRepo() *InMemorySessionRepo {
	return &InMemorySessionRepo{sessions: map[string]*InMemorySessionStorage{}}
}

func (r *InMemorySessionRepo) Create(options *SessionCreateOptions) (*Session, error) {
	if options == nil {
		options = &SessionCreateOptions{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	id := options.ID
	if id == nil {
		generated := uuidv7ID()
		id = &generated
	}
	if _, exists := r.sessions[*id]; exists {
		return nil, NewSessionError(CodeAlreadyExists, fmt.Sprintf("Session already exists: %s", *id))
	}
	storage := NewInMemorySessionStorage(SessionMetadata{
		ID:              *id,
		CreatedAt:       nowMillis(),
		ParentSessionID: options.ParentSessionID,
	})
	r.sessions[*id] = storage
	return NewSession(storage, nil), nil
}

func (r *InMemorySessionRepo) Open(metadata SessionMetadata) (*Session, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	storage, err := r.requireStorage(metadata.ID)
	if err != nil {
		return nil, err
	}
	return NewSession(storage, nil), nil
}

func (r *InMemorySessionRepo) List() ([]SessionMetadata, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	metadatas := make([]SessionMetadata, 0, len(r.sessions))
	// pi: Promise.all over insertion order; Go maps are unordered, so the
	// list order is unspecified here (pi's own order comes from Map
	// insertion; no consumer depends on it).
	for _, storage := range r.sessions {
		metadata, err := storage.GetMetadata()
		if err != nil {
			return nil, err
		}
		metadatas = append(metadatas, metadata)
	}
	return metadatas, nil
}

func (r *InMemorySessionRepo) Delete(metadata SessionMetadata) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.sessions, metadata.ID)
	return nil
}

func (r *InMemorySessionRepo) Fork(source SessionMetadata, options *ForkCreateOptions) (*Session, error) {
	if options == nil {
		options = &ForkCreateOptions{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	sourceStorage, err := r.requireStorage(source.ID)
	if err != nil {
		return nil, err
	}
	forkID := options.ID
	if forkID == nil {
		generated := uuidv7ID()
		forkID = &generated
	}
	if _, exists := r.sessions[*forkID]; exists {
		return nil, NewSessionError(CodeAlreadyExists, fmt.Sprintf("Session already exists: %s", *forkID))
	}
	// pi: parentSessionId: options.parentSessionId ?? source.id.
	parentSessionID := options.ParentSessionID
	if parentSessionID == nil {
		parentSessionID = &source.ID
	}
	storage, err := sourceStorage.Fork(SessionMetadata{
		ID:              *forkID,
		CreatedAt:       nowMillis(),
		ParentSessionID: parentSessionID,
	}, &options.ForkOptions)
	if err != nil {
		return nil, err
	}
	r.sessions[*forkID] = storage
	return NewSession(storage, nil), nil
}

func (r *InMemorySessionRepo) requireStorage(id string) (*InMemorySessionStorage, error) {
	storage, ok := r.sessions[id]
	if !ok {
		return nil, NewSessionError(CodeNotFound, fmt.Sprintf("Session not found: %s", id))
	}
	return storage, nil
}

func uuidv7ID() string { return uuidv7Generator{}.Next() }

// cloneMetadata deep-copies session metadata (pi: structuredClone).
func cloneMetadata(metadata SessionMetadata) SessionMetadata {
	data, err := json.Marshal(metadata)
	if err != nil {
		panic(fmt.Sprintf("session: cloneMetadata marshal failed: %v", err))
	}
	var clone SessionMetadata
	if err := json.Unmarshal(data, &clone); err != nil {
		panic(fmt.Sprintf("session: cloneMetadata decode failed: %v", err))
	}
	return clone
}

func cloneEntries(entries []Entry) []Entry {
	if entries == nil {
		return nil
	}
	results := make([]Entry, len(entries))
	for i, entry := range entries {
		results[i] = cloneEntry(entry)
	}
	return results
}

func cloneRecords(records []LaneRecord) []LaneRecord {
	if records == nil {
		return nil
	}
	results := make([]LaneRecord, len(records))
	for i, record := range records {
		results[i] = cloneRecord(record)
	}
	return results
}
