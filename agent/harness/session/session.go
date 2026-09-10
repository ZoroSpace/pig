// pi: harness/session/session.ts — StorageBackedSession, the branch view,
// the mutation barrier, and the session-level error types.
//
// Concurrency mapping: pi relies on the single-threaded event loop; pig
// guards the session state fields with a mutex and serializes mutations
// through MutationLine. Reads run against the storage's own synchronization.
package session

import (
	"context"
	"fmt"
	"sync"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/ai/types"
	aiutils "github.com/ZoroSpace/pig/ai/utils"
)

// StorageBackedSessionOptions parameterizes StorageBackedSession (pi:
// StorageBackedSessionOptions).
type StorageBackedSessionOptions struct {
	MutationLine *MutationLine
	IDGenerator  IdGenerator
	OnClose      func()
}

// SessionInvariantError: durable session state is internally inconsistent
// and cannot be safely advanced (pi: SessionInvariantError).
type SessionInvariantError struct{ Message string }

func (e *SessionInvariantError) Error() string { return e.Message }

// SessionInvalidBranchError: a requested Branch name is invalid (pi:
// SessionInvalidBranchError).
type SessionInvalidBranchError struct {
	Branch string
	Reason string
}

func (e *SessionInvalidBranchError) Error() string {
	return fmt.Sprintf("Invalid branch %q: %s", e.Branch, e.Reason)
}

// SessionBranchExistsError: a requested branch already exists (pi:
// SessionBranchExistsError).
type SessionBranchExistsError struct{ Branch string }

func (e *SessionBranchExistsError) Error() string {
	return fmt.Sprintf("Branch already exists: %s", e.Branch)
}

// SessionPendingAssistantMessageError: a pending assistant message cannot be
// persisted as a session entry (pi: SessionPendingAssistantMessageError).
type SessionPendingAssistantMessageError struct{}

func (e *SessionPendingAssistantMessageError) Error() string {
	return "Cannot persist a pending assistant message"
}

// SessionUnknownTargetError: a requested session entry target does not exist
// (pi: SessionUnknownTargetError).
type SessionUnknownTargetError struct{ TargetID string }

func (e *SessionUnknownTargetError) Error() string {
	return fmt.Sprintf("Unknown target: %s", e.TargetID)
}

// assistantMessageOf extracts an assistant message from the open
// AgentMessage channel (pig message types appear as values or pointers).
func assistantMessageOf(message agent.AgentMessage) (types.AssistantMessage, bool) {
	if assistant, ok := message.(types.AssistantMessage); ok {
		return assistant, true
	}
	if assistant, ok := message.(*types.AssistantMessage); ok {
		return *assistant, true
	}
	return types.AssistantMessage{}, false
}

// isPendingAssistantMessage reports whether the entry write would persist a
// pending assistant message.
func isPendingAssistantMessage(entry Entry) bool {
	messageEntry, ok := entry.(*MessageEntry)
	if !ok {
		return false
	}
	assistant, ok := assistantMessageOf(messageEntry.Message)
	if !ok {
		// Custom roles (bashExecution, compactionSummary, ...) are never
		// pending; only assistant-role messages carry a stopReason.
		return false
	}
	return assistant.StopReason == types.StopReasonPending
}

// storageBackedSessionMutation implements SessionMutation (pi:
// StorageBackedSessionMutation).
type storageBackedSessionMutation struct {
	storage      Storage
	release      func()
	mu           sync.Mutex
	active       bool
	commitResult *commitOutcome
	ended        bool
}

type commitOutcome struct {
	result CommitResult
	err    error
}

func newStorageBackedSessionMutation(storage Storage, release func()) *storageBackedSessionMutation {
	return &storageBackedSessionMutation{storage: storage, release: release, active: true}
}

func (m *storageBackedSessionMutation) assertActive() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.active {
		return fmt.Errorf("SessionMutator cannot be used outside its mutation callback")
	}
	return nil
}

func (m *storageBackedSessionMutation) Commit(writes []Write, ctx context.Context) (CommitResult, error) {
	if err := m.assertActive(); err != nil {
		return CommitResult{}, err
	}
	m.mu.Lock()
	if m.commitResult != nil {
		m.mu.Unlock()
		return CommitResult{}, fmt.Errorf("SessionMutator commit already attempted")
	}
	var result CommitResult
	var err error
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
		m.commitResult = &commitOutcome{result: result, err: err}
		m.mu.Unlock()
	}()
	for _, write := range writes {
		if entryWrite, ok := write.(EntryWrite); ok && isPendingAssistantMessage(entryWrite.Entry) {
			err = &SessionPendingAssistantMessageError{}
			return CommitResult{}, err
		}
	}
	result, err = m.storage.Commit(writes, ctx)
	return result, err
}

func (m *storageBackedSessionMutation) End(_ context.Context) error {
	m.mu.Lock()
	if m.ended {
		outcome := m.commitResult
		m.mu.Unlock()
		if outcome != nil && outcome.err != nil {
			return nil // settle(): commit errors are swallowed on End
		}
		return nil
	}
	m.active = false
	m.ended = true
	outcome := m.commitResult
	m.mu.Unlock()
	m.release()
	if outcome != nil && outcome.err != nil {
		return nil // settle(): commit errors are swallowed on End
	}
	return nil
}

func (m *storageBackedSessionMutation) GetEntries(ids []string, ctx context.Context) (map[string]Entry, error) {
	if err := m.assertActive(); err != nil {
		return nil, err
	}
	return m.storage.GetEntries(ids, ctx)
}

func (m *storageBackedSessionMutation) GetStats(ctx context.Context) (SessionStats, error) {
	if err := m.assertActive(); err != nil {
		return SessionStats{}, err
	}
	return m.storage.GetStats(ctx)
}

func (m *storageBackedSessionMutation) GetValue(address Value, ctx context.Context) (*StoredValue, error) {
	if err := m.assertActive(); err != nil {
		return nil, err
	}
	return m.storage.GetValue(address, ctx)
}

func (m *storageBackedSessionMutation) ScanValues(prefix Value, ctx context.Context) ([]StoredValue, error) {
	if err := m.assertActive(); err != nil {
		return nil, err
	}
	return m.storage.ScanValues(prefix, ctx)
}

func (m *storageBackedSessionMutation) ReadList(address ValueList, options *ListReadOptions, ctx context.Context) ([]ListElement, error) {
	if err := m.assertActive(); err != nil {
		return nil, err
	}
	return m.storage.ReadList(address, options, ctx)
}

func (m *storageBackedSessionMutation) ScanBranch(query StorageBranchScan, ctx context.Context) ([]Entry, error) {
	if err := m.assertActive(); err != nil {
		return nil, err
	}
	return m.storage.ScanBranch(query, ctx)
}

// storageBackedBranch implements Branch (pi: StorageBackedBranch).
type storageBackedBranch struct {
	name    string
	session *StorageBackedSession
}

func (b *storageBackedBranch) Name() string { return b.name }

func (b *storageBackedBranch) GetTipID(ctx context.Context) (*string, error) {
	return b.session.getBranchTip(b.name, ctx)
}

func (b *storageBackedBranch) FindEntries(query *BranchScan, ctx context.Context) ([]Entry, error) {
	if query == nil {
		query = &BranchScan{}
	}
	tip, err := b.GetTipID(ctx)
	if err != nil {
		return nil, err
	}
	start := ""
	if query.Start != nil {
		start = *query.Start
	} else if tip != nil {
		start = *tip
	} else {
		return []Entry{}, nil
	}
	order := query.Order
	if order == "" {
		order = "newestFirst"
	}
	storageQuery := *query
	storageQuery.Start = &start
	storageQuery.Order = order
	return b.session.ScanBranch(StorageBranchScan{BranchScan: storageQuery, Start: start}, ctx)
}

func (b *storageBackedBranch) FindEntry(query *BranchScan, ctx context.Context) (Entry, error) {
	if query == nil {
		query = &BranchScan{}
	}
	limited := *query
	if query.Limit == nil {
		one := 1
		limited.Limit = &one
	} else if *query.Limit > 1 {
		one := 1
		limited.Limit = &one
	}
	entries, err := b.FindEntries(&limited, ctx)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, nil
	}
	return entries[0], nil
}

func (b *storageBackedBranch) AppendMessage(message agent.AgentMessage, ctx context.Context) (string, error) {
	return b.session.appendToBranch(b.name, messageEntryPayload{message: message}, ctx)
}

func (b *storageBackedBranch) AppendCustomEntry(customType string, data JsonValue, ctx context.Context) (string, error) {
	return b.session.appendToBranch(b.name, messageEntryPayload{customType: customType, data: data}, ctx)
}

// messageEntryPayload is appendToBranch's inline entry union (pi:
// `{ type: "message"; message } | { type: "custom"; customType; data? }`).
type messageEntryPayload struct {
	message    agent.AgentMessage // set for message entries
	customType string             // set for custom entries
	data       JsonValue
}

// StorageBackedSession is the package-internal typed boundary shared by
// concrete session repositories (pi: StorageBackedSession).
type StorageBackedSession struct {
	// Metadata is the session's metadata (pi: readonly metadata). JSONL
	// consumers read JsonlSessionMetadata through this field plus a type
	// assertion (generic-parameter drop, see types.go).
	Metadata SessionMetadata
	// idGenerator produces entry ids (pi: readonly idGenerator); access via
	// IdGenerator().
	idGen IdGenerator

	// typedMetadata carries the concrete metadata shape (pi: the generic
	// TMetadata parameter, dropped per the types.go note); nil for base
	// SessionMetadata sessions.
	typedMetadata any

	storage      Storage
	mutationLine *MutationLine
	onClose      func()

	mu       sync.Mutex
	branches map[string]*storageBackedBranch
	state    string // "open" | "closing" | "closed"
	closed   error  // the shared "Session is closed" error
}

// NewStorageBackedSession builds one session handle (pi: constructor).
func NewStorageBackedSession(metadata SessionMetadata, storage Storage, options StorageBackedSessionOptions) *StorageBackedSession {
	idGen := options.IDGenerator
	if idGen == nil {
		idGen = uuidv7Generator{}
	}
	line := options.MutationLine
	if line == nil {
		line = NewMutationLine()
	}
	return &StorageBackedSession{
		Metadata:     metadata,
		idGen:        idGen,
		storage:      storage,
		mutationLine: line,
		onClose:      options.OnClose,
		branches:     map[string]*storageBackedBranch{},
		state:        "open",
		closed:       fmt.Errorf("Session is closed"),
	}
}

type uuidv7Generator struct{}

func (uuidv7Generator) Next(timestampMs int64) string {
	if timestampMs == 0 {
		return aiutils.UUIDv7()
	}
	return aiutils.UUIDv7WithTime(timestampMs)
}

// GetMetadata returns the session metadata (pi: metadata field).
func (s *StorageBackedSession) GetMetadata() SessionMetadata { return s.Metadata }

// TypedMetadata returns the concrete metadata shape (JsonlSessionMetadata
// for JSONL-backed sessions; nil otherwise).
func (s *StorageBackedSession) TypedMetadata() any { return s.typedMetadata }

// SetTypedMetadata attaches the concrete metadata (package-internal wiring).
func (s *StorageBackedSession) SetTypedMetadata(metadata any) { s.typedMetadata = metadata }

// IdGenerator returns the session's id generator (pi: idGenerator field).
func (s *StorageBackedSession) IdGenerator() IdGenerator { return s.idGen }

func (s *StorageBackedSession) assertOpen() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state != "open" {
		return s.closed
	}
	return nil
}

// BeginMutation acquires the exclusive mutation barrier (pi: beginMutation).
func (s *StorageBackedSession) BeginMutation(ctx context.Context) (SessionMutation, error) {
	if err := s.assertOpen(); err != nil {
		return nil, err
	}
	release := make(chan struct{})
	var once sync.Once
	releaseFn := func() { once.Do(func() { close(release) }) }
	granted := make(chan *storageBackedSessionMutation, 1)
	grantErr := make(chan error, 1)

	go func() {
		err := s.mutationLine.Run(func() error {
			mutation := newStorageBackedSessionMutation(s.storage, releaseFn)
			granted <- mutation
			<-release
			return nil
		})
		if err != nil {
			grantErr <- err
		}
	}()

	select {
	case mutation := <-granted:
		return mutation, nil
	case err := <-grantErr:
		return nil, err
	}
}

// Mutate runs the callback holding the exclusive mutation line (pi: mutate).
func (s *StorageBackedSession) Mutate(mutation SessionMutationCallback, ctx context.Context) (any, error) {
	mutator, err := s.BeginMutation(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = mutator.End(ctx) }()
	return mutation(mutator, ctx)
}

func (s *StorageBackedSession) GetEntries(ids []string, ctx context.Context) (map[string]Entry, error) {
	if err := s.assertOpen(); err != nil {
		return nil, err
	}
	return s.storage.GetEntries(ids, ctx)
}

func (s *StorageBackedSession) GetEntry(id string, ctx context.Context) (Entry, error) {
	entries, err := s.GetEntries([]string{id}, ctx)
	if err != nil {
		return nil, err
	}
	return entries[id], nil
}

func (s *StorageBackedSession) GetValue(address Value, ctx context.Context) (*StoredValue, error) {
	if err := s.assertOpen(); err != nil {
		return nil, err
	}
	return s.storage.GetValue(address, ctx)
}

func (s *StorageBackedSession) ScanValues(prefix Value, ctx context.Context) ([]StoredValue, error) {
	if err := s.assertOpen(); err != nil {
		return nil, err
	}
	return s.storage.ScanValues(prefix, ctx)
}

func (s *StorageBackedSession) ReadList(address ValueList, options *ListReadOptions, ctx context.Context) ([]ListElement, error) {
	if err := s.assertOpen(); err != nil {
		return nil, err
	}
	return s.storage.ReadList(address, options, ctx)
}

func (s *StorageBackedSession) ScanBranch(query StorageBranchScan, ctx context.Context) ([]Entry, error) {
	if err := s.assertOpen(); err != nil {
		return nil, err
	}
	return s.storage.ScanBranch(query, ctx)
}

func (s *StorageBackedSession) GetStats(ctx context.Context) (SessionStats, error) {
	if err := s.assertOpen(); err != nil {
		return SessionStats{}, err
	}
	return s.storage.GetStats(ctx)
}

func (s *StorageBackedSession) GetName(ctx context.Context) (*string, error) {
	stored, err := s.GetValue(SessionNameAddress, ctx)
	if err != nil || stored == nil {
		return nil, err
	}
	name, _ := stored.Value.(string)
	return &name, nil
}

func (s *StorageBackedSession) GetLabel(targetID string, ctx context.Context) (*string, error) {
	stored, err := s.GetValue(EntryLabel(targetID), ctx)
	if err != nil || stored == nil {
		return nil, err
	}
	label, _ := stored.Value.(string)
	return &label, nil
}

func (s *StorageBackedSession) FindEntries(query *EntryQuery, ctx context.Context) ([]Entry, error) {
	if query == nil {
		query = &EntryQuery{}
	}
	if err := s.assertOpen(); err != nil {
		return nil, err
	}
	order := query.Order
	if order == "" {
		order = "desc"
	}
	if query.Cursor != nil {
		if order == "asc" && query.Cursor.Seq == int64(^uint64(0)>>1) {
			return []Entry{}, nil
		}
		if order == "desc" && query.Cursor.Seq <= 1 {
			return []Entry{}, nil
		}
	}
	scan := EntryScan{
		Type:       query.Type,
		CustomType: query.CustomType,
		Order:      order,
		Limit:      query.Limit,
	}
	if query.Cursor != nil {
		if order == "asc" {
			from := query.Cursor.Seq + 1
			scan.FromSeq = &from
		} else {
			to := query.Cursor.Seq - 1
			scan.ToSeq = &to
		}
	}
	return s.storage.ScanEntries(scan, ctx)
}

func (s *StorageBackedSession) FindEntry(query *EntryQuery, ctx context.Context) (Entry, error) {
	if query == nil {
		query = &EntryQuery{}
	}
	limited := *query
	if query.Limit == nil {
		one := 1
		limited.Limit = &one
	} else if *query.Limit > 1 {
		one := 1
		limited.Limit = &one
	}
	entries, err := s.FindEntries(&limited, ctx)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, nil
	}
	return entries[0], nil
}

func (s *StorageBackedSession) Branch(name string, ctx context.Context) (Branch, error) {
	if err := assertValidBranchName(name); err != nil {
		return nil, err
	}
	stored, err := s.GetValue(BranchTip(name), ctx)
	if err != nil {
		return nil, err
	}
	if stored == nil {
		return nil, nil
	}
	return s.getOrCreateBranchObject(name), nil
}

func (s *StorageBackedSession) CreateBranch(name string, at *string, ctx context.Context) (Branch, error) {
	if err := s.assertOpen(); err != nil {
		return nil, err
	}
	if err := assertValidBranchName(name); err != nil {
		return nil, err
	}
	if _, err := s.Mutate(func(mutator SessionMutator, ctx context.Context) (any, error) {
		stored, err := mutator.GetValue(BranchTip(name), ctx)
		if err != nil {
			return nil, err
		}
		if stored != nil {
			return nil, &SessionBranchExistsError{Branch: name}
		}
		if at != nil {
			entries, err := mutator.GetEntries([]string{*at}, ctx)
			if err != nil {
				return nil, err
			}
			if _, ok := entries[*at]; !ok {
				return nil, &SessionUnknownTargetError{TargetID: *at}
			}
		}
		var tipValue any
		if at != nil {
			tipValue = *at
		}
		_, err = mutator.Commit([]Write{SetValue(BranchTip(name), tipValue)}, ctx)
		return nil, err
	}, ctx); err != nil {
		return nil, err
	}
	return s.getOrCreateBranchObject(name), nil
}

func (s *StorageBackedSession) mutateCommit(writes []Write, ctx context.Context) error {
	_, err := s.Mutate(func(mutator SessionMutator, ctx context.Context) (any, error) {
		_, err := mutator.Commit(writes, ctx)
		return nil, err
	}, ctx)
	return err
}

func (s *StorageBackedSession) SetValue(address Value, next any, ctx context.Context) error {
	return s.mutateCommit([]Write{SetValue(address, next)}, ctx)
}

func (s *StorageBackedSession) DeleteValue(address Value, ctx context.Context) error {
	return s.mutateCommit([]Write{DeleteValue(address)}, ctx)
}

func (s *StorageBackedSession) AppendList(address ValueList, element any, ctx context.Context) error {
	return s.mutateCommit([]Write{AppendList(address, element)}, ctx)
}

func (s *StorageBackedSession) DeleteList(address ValueList, ctx context.Context) error {
	return s.mutateCommit([]Write{DeleteList(address)}, ctx)
}

func (s *StorageBackedSession) SetName(name *string, ctx context.Context) error {
	if name == nil {
		return s.DeleteValue(SessionNameAddress, ctx)
	}
	return s.SetValue(SessionNameAddress, *name, ctx)
}

func (s *StorageBackedSession) SetLabel(targetID string, label *string, ctx context.Context) error {
	address := EntryLabel(targetID)
	if label == nil {
		return s.DeleteValue(address, ctx)
	}
	return s.SetValue(address, *label, ctx)
}

func (s *StorageBackedSession) Close(ctx context.Context) error {
	s.mu.Lock()
	if s.state == "closed" || s.state == "closing" {
		s.mu.Unlock()
		// pi returns the memoized closePromise; concurrent Close callers
		// wait for the same completion. pig's Close is idempotent-fast:
		// closing is driven by the first caller.
		return nil
	}
	s.state = "closing"
	s.mu.Unlock()

	s.mutationLine.Seal(s.closed)
	err := s.storage.Close(ctx)
	s.mu.Lock()
	s.state = "closed"
	s.mu.Unlock()
	if s.onClose != nil {
		s.onClose()
	}
	return err
}

func (s *StorageBackedSession) getBranchTip(name string, ctx context.Context) (*string, error) {
	stored, err := s.GetValue(BranchTip(name), ctx)
	if err != nil {
		return nil, err
	}
	if stored == nil {
		return nil, &SessionInvariantError{Message: fmt.Sprintf("Unknown branch: %s", name)}
	}
	tip, _ := stored.Value.(string)
	if tip == "" {
		return nil, nil
	}
	return &tip, nil
}

func (s *StorageBackedSession) appendToBranch(name string, payload messageEntryPayload, ctx context.Context) (string, error) {
	if err := s.assertOpen(); err != nil {
		return "", err
	}
	if payload.message != nil && isPendingAssistantMessage(&MessageEntry{Message: payload.message}) {
		return "", &SessionPendingAssistantMessageError{}
	}
	id := s.idGen.Next(0)
	return s.mutateAppend(name, id, payload, ctx)
}

func (s *StorageBackedSession) mutateAppend(name, id string, payload messageEntryPayload, ctx context.Context) (string, error) {
	_, err := s.Mutate(func(mutator SessionMutator, ctx context.Context) (any, error) {
		tip, err := mutator.GetValue(BranchTip(name), ctx)
		if err != nil {
			return nil, err
		}
		if tip == nil {
			return nil, &SessionInvariantError{Message: fmt.Sprintf("Unknown branch: %s", name)}
		}
		tipID, _ := tip.Value.(string)
		var entry Entry
		if payload.message != nil {
			entry = &MessageEntry{EntryBase: EntryBase{ID: id, ParentID: stringOrNil(tipID), Type: EntryTypeMessage}, Message: payload.message}
		} else {
			entry = &CustomEntry{EntryBase: EntryBase{ID: id, ParentID: stringOrNil(tipID), Type: EntryTypeCustom, CustomType: &payload.customType}, Data: payload.data}
		}
		_, err = mutator.Commit([]Write{
			InsertEntry(entry),
			SetValue(BranchTip(name), id),
		}, ctx)
		return nil, err
	}, ctx)
	if err != nil {
		return "", err
	}
	return id, nil
}

func (s *StorageBackedSession) getOrCreateBranchObject(name string) Branch {
	s.mu.Lock()
	defer s.mu.Unlock()
	if branch, ok := s.branches[name]; ok {
		return branch
	}
	branch := &storageBackedBranch{name: name, session: s}
	s.branches[name] = branch
	return branch
}

func assertValidBranchName(name string) error {
	if len(name) == 0 {
		return &SessionInvalidBranchError{Branch: name, Reason: "branch name must not be empty"}
	}
	if containsNullByte(name) {
		return &SessionInvalidBranchError{Branch: name, Reason: `branch name must not contain \u0000`}
	}
	return nil
}

func stringOrNil(s string) *string {
	if s == "" {
		return nil
	}
	v := s
	return &v
}
