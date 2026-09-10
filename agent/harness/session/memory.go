// pi: harness/session/memory.ts — in-memory Storage and SessionRepo.
//
// Concurrency mapping: pi serializes commits through a promise queue; pig
// guards the state with a mutex (commit serialization is subsumed by it).
// The MemorySessionFacade's admitted-op tracking maps to a WaitGroup-guarded
// in-flight counter so Close waits for outstanding operations.
package session

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/ZoroSpace/pig/agent"
	aiutils "github.com/ZoroSpace/pig/ai/utils"
)

// MemoryStorageOptions parameterizes MemoryStorage (pi: MemoryStorageOptions).
type MemoryStorageOptions struct {
	// Now overrides the commit timestamp source.
	Now func() int64
}

// MemoryStorage implements Storage entirely in memory (pi: MemoryStorage).
type MemoryStorage struct {
	now func() int64
	mu  sync.Mutex
	// commitGate serializes Commit/CaptureForkSource in admission order
	// (pi: the commitQueue promise chain; Go channels wake blocked senders
	// FIFO, a plain mutex does not).
	commitGate chan struct{}
	// stateMu guards the InMemoryStorageState for readers.
	stateMu   sync.RWMutex
	state     *InMemoryStorageState
	stateName string // "open" | "closing" | "closed"
	closeErr  error
}

// NewMemoryStorage builds one memory storage (pi: constructor).
func NewMemoryStorage(options MemoryStorageOptions) *MemoryStorage {
	now := options.Now
	if now == nil {
		now = defaultNow
	}
	return &MemoryStorage{now: now, state: NewInMemoryStorageState(), stateName: "open", commitGate: make(chan struct{}, 1)}
}

// acquireCommitGate serializes against other commits in admission order.
func (m *MemoryStorage) acquireCommitGate() {
	m.commitGate <- struct{}{}
}

func (m *MemoryStorage) releaseCommitGate() {
	<-m.commitGate
}

func defaultNow() int64 { return time.Now().UnixMilli() }

// Commit applies one transaction (pi: commit). Storage guards state access
// with its mutex; commit order equals mutex acquisition order.
func (m *MemoryStorage) Commit(writes []Write, _ context.Context) (CommitResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stateName != "open" {
		return CommitResult{}, fmt.Errorf("MemoryStorage is closed")
	}
	m.acquireCommitGate()
	defer m.releaseCommitGate()
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	prepared, err := m.state.PrepareCommit(writes, m.now())
	if err != nil {
		return CommitResult{}, err
	}
	stats := m.state.ApplyValidated(prepared.Writes)
	result := CommitResult{
		FirstSeq:  prepared.Result.FirstSeq,
		Seqs:      prepared.Result.Seqs,
		Timestamp: prepared.Result.Timestamp,
		Stats:     stats,
	}
	return result, nil
}

func (m *MemoryStorage) assertOpen() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stateName != "open" {
		return fmt.Errorf("MemoryStorage is closed")
	}
	return nil
}

func (m *MemoryStorage) GetEntries(ids []string, _ context.Context) (map[string]Entry, error) {
	if err := m.assertOpen(); err != nil {
		return nil, err
	}
	m.stateMu.RLock()
	defer m.stateMu.RUnlock()
	return m.state.GetEntries(ids), nil
}

func (m *MemoryStorage) GetValue(address Value, _ context.Context) (*StoredValue, error) {
	if err := m.assertOpen(); err != nil {
		return nil, err
	}
	m.stateMu.RLock()
	defer m.stateMu.RUnlock()
	return m.state.GetValue(address), nil
}

func (m *MemoryStorage) ScanValues(prefix Value, _ context.Context) ([]StoredValue, error) {
	if err := m.assertOpen(); err != nil {
		return nil, err
	}
	m.stateMu.RLock()
	defer m.stateMu.RUnlock()
	return m.state.ScanValues(prefix), nil
}

func (m *MemoryStorage) ReadList(address ValueList, options *ListReadOptions, _ context.Context) ([]ListElement, error) {
	if err := m.assertOpen(); err != nil {
		return nil, err
	}
	m.stateMu.RLock()
	defer m.stateMu.RUnlock()
	return m.state.ReadList(address, options)
}

func (m *MemoryStorage) ScanBranch(query StorageBranchScan, _ context.Context) ([]Entry, error) {
	if err := m.assertOpen(); err != nil {
		return nil, err
	}
	m.stateMu.RLock()
	defer m.stateMu.RUnlock()
	return m.state.ScanBranch(query)
}

func (m *MemoryStorage) ScanBranchStructure(query StorageBranchScan, _ context.Context) ([]EntryStructure, error) {
	if err := m.assertOpen(); err != nil {
		return nil, err
	}
	m.stateMu.RLock()
	defer m.stateMu.RUnlock()
	return m.state.ScanBranchStructure(query)
}

func (m *MemoryStorage) ScanEntries(query EntryScan, _ context.Context) ([]Entry, error) {
	if err := m.assertOpen(); err != nil {
		return nil, err
	}
	m.stateMu.RLock()
	defer m.stateMu.RUnlock()
	return m.state.ScanEntries(query), nil
}

func (m *MemoryStorage) ScanUsage(query UsageScan, _ context.Context) ([]UsageRow, error) {
	if err := m.assertOpen(); err != nil {
		return nil, err
	}
	m.stateMu.RLock()
	defer m.stateMu.RUnlock()
	return m.state.ScanUsage(query), nil
}

func (m *MemoryStorage) GetStats(_ context.Context) (SessionStats, error) {
	if err := m.assertOpen(); err != nil {
		return SessionStats{}, err
	}
	m.stateMu.RLock()
	defer m.stateMu.RUnlock()
	return m.state.GetStats(), nil
}

// CaptureForkSource captures the state needed to fork at one serialized
// boundary between commits (pi: captureForkSource).
func (m *MemoryStorage) CaptureForkSource(_ context.Context) (ForkSourceSnapshot, error) {
	if err := m.assertOpen(); err != nil {
		return ForkSourceSnapshot{}, err
	}
	m.acquireCommitGate()
	defer m.releaseCommitGate()
	m.stateMu.RLock()
	defer m.stateMu.RUnlock()
	return m.state.SnapshotEntriesAndValues()
}

func (m *MemoryStorage) Close(_ context.Context) error {
	// Drain admitted commits before sealing (pi: close waits on the commit
	// queue).
	m.acquireCommitGate()
	defer m.releaseCommitGate()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stateName == "closed" || m.stateName == "closing" {
		return nil
	}
	m.stateName = "closing"
	m.stateName = "closed"
	return nil
}

// MemoryStorageFromSnapshot builds a memory storage from a complete prepared
// fork snapshot (pi: MemoryStorage.fromSnapshot static).
func MemoryStorageFromSnapshot(options MemoryStorageOptions, snapshot ForkDestinationSnapshot) (*MemoryStorage, error) {
	storage := NewMemoryStorage(options)
	writes := ForkSnapshotWrites(snapshot)
	storage.stateMu.Lock()
	defer storage.stateMu.Unlock()
	// Snapshot writes are sorted by seq starting at 1, so validation runs
	// against the fresh nextSeq=1 exactly like pi's fromSnapshot.
	if err := storage.state.ValidateCommitted(writes); err != nil {
		return nil, err
	}
	storage.state.ApplyValidated(writes)
	return storage, nil
}

// MemorySessionRepoOptions parameterizes MemorySessionRepo (pi:
// MemorySessionRepoOptions).
type MemorySessionRepoOptions struct {
	Now func() int64
}

// memoryStorageVersion is MEMORY_STORAGE_VERSION.
const memoryStorageVersion = 1

type memorySessionRecord struct {
	metadata SessionMetadata
	storage  *MemoryStorage
	session  *StorageBackedSession
	mu       sync.Mutex
	open     bool
}

// MemorySessionFacade guards a StorageBackedSession behind repo close
// semantics (pi: MemorySessionFacade).
type MemorySessionFacade struct {
	*StorageBackedSession
	// metadata is the facade-level metadata view (pi: readonly metadata).
	metadata SessionMetadata
	onClose  func()

	mu       sync.Mutex
	inFlight sync.WaitGroup
	state    string // "open" | "closing" | "closed"
	closed   error  // the shared "Session is closed" error
	closeMu  sync.Mutex
}

func newMemorySessionFacade(session *StorageBackedSession, onClose func()) *MemorySessionFacade {
	return &MemorySessionFacade{
		StorageBackedSession: session,
		metadata:             session.Metadata,
		onClose:              onClose,
		state:                "open",
		closed:               fmt.Errorf("Session is closed"),
	}
}

// GetMetadata returns the facade metadata (pi: metadata).
func (f *MemorySessionFacade) GetMetadata() SessionMetadata { return f.metadata }

func (f *MemorySessionFacade) assertOpen() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.state != "open" {
		return f.closed
	}
	return nil
}

// admit runs one operation if open, tracking it for Close (pi: admit).
func (f *MemorySessionFacade) admit(fn func() error) error {
	f.mu.Lock()
	if f.state != "open" {
		f.mu.Unlock()
		return f.closed
	}
	f.inFlight.Add(1)
	f.mu.Unlock()
	defer f.inFlight.Done()
	return fn()
}

// BeginMutation wraps the source mutation with close tracking (pi:
// beginMutation).
func (f *MemorySessionFacade) BeginMutation(ctx context.Context) (SessionMutation, error) {
	f.mu.Lock()
	if f.state != "open" {
		f.mu.Unlock()
		return nil, f.closed
	}
	f.inFlight.Add(1)
	f.mu.Unlock()

	source, err := f.StorageBackedSession.BeginMutation(ctx)
	if err != nil {
		f.inFlight.Done()
		return nil, err
	}
	f.mu.Lock()
	if f.state != "open" {
		f.mu.Unlock()
		_ = source.End(ctx)
		f.inFlight.Done()
		return nil, f.closed
	}
	f.mu.Unlock()
	return &facadeMutation{source: source, facade: f}, nil
}

type facadeMutation struct {
	source SessionMutation
	facade *MemorySessionFacade
	ended  bool
	mu     sync.Mutex
}

func (m *facadeMutation) finish() {
	m.mu.Lock()
	ended := m.ended
	m.ended = true
	m.mu.Unlock()
	if !ended {
		m.facade.inFlight.Done()
	}
}

func (m *facadeMutation) Commit(writes []Write, ctx context.Context) (CommitResult, error) {
	return m.source.Commit(writes, ctx)
}

func (m *facadeMutation) End(ctx context.Context) error {
	err := m.source.End(ctx)
	m.finish()
	return err
}

func (m *facadeMutation) GetEntries(ids []string, ctx context.Context) (map[string]Entry, error) {
	return m.source.GetEntries(ids, ctx)
}

func (m *facadeMutation) GetStats(ctx context.Context) (SessionStats, error) {
	return m.source.GetStats(ctx)
}

func (m *facadeMutation) GetValue(address Value, ctx context.Context) (*StoredValue, error) {
	return m.source.GetValue(address, ctx)
}

func (m *facadeMutation) ScanValues(prefix Value, ctx context.Context) ([]StoredValue, error) {
	return m.source.ScanValues(prefix, ctx)
}

func (m *facadeMutation) ReadList(address ValueList, options *ListReadOptions, ctx context.Context) ([]ListElement, error) {
	return m.source.ReadList(address, options, ctx)
}

func (m *facadeMutation) ScanBranch(query StorageBranchScan, ctx context.Context) ([]Entry, error) {
	return m.source.ScanBranch(query, ctx)
}

// Mutate runs the callback under the facade's open check. pi re-checks
// facade openness INSIDE the mutation callback (after the inner line is
// acquired) so a queued callback fails when close won the race.
func (f *MemorySessionFacade) Mutate(mutation SessionMutationCallback, ctx context.Context) (any, error) {
	var result any
	err := f.admit(func() error {
		var callbackErr error
		result, callbackErr = f.StorageBackedSession.Mutate(func(mutator SessionMutator, ctx context.Context) (any, error) {
			if err := f.assertOpen(); err != nil {
				return nil, err
			}
			return mutation(mutator, ctx)
		}, ctx)
		return callbackErr
	})
	return result, err
}

func (f *MemorySessionFacade) GetEntry(id string, ctx context.Context) (Entry, error) {
	var entry Entry
	err := f.admit(func() error {
		if err := f.assertOpen(); err != nil {
			return err
		}
		var callbackErr error
		entry, callbackErr = f.StorageBackedSession.GetEntry(id, ctx)
		return callbackErr
	})
	return entry, err
}

func (f *MemorySessionFacade) FindEntries(query *EntryQuery, ctx context.Context) ([]Entry, error) {
	var entries []Entry
	err := f.admit(func() error {
		if err := f.assertOpen(); err != nil {
			return err
		}
		var callbackErr error
		entries, callbackErr = f.StorageBackedSession.FindEntries(query, ctx)
		return callbackErr
	})
	return entries, err
}

func (f *MemorySessionFacade) FindEntry(query *EntryQuery, ctx context.Context) (Entry, error) {
	var entry Entry
	err := f.admit(func() error {
		if err := f.assertOpen(); err != nil {
			return err
		}
		var callbackErr error
		entry, callbackErr = f.StorageBackedSession.FindEntry(query, ctx)
		return callbackErr
	})
	return entry, err
}

func (f *MemorySessionFacade) Branch(name string, ctx context.Context) (Branch, error) {
	var branch Branch
	err := f.admit(func() error {
		if err := f.assertOpen(); err != nil {
			return err
		}
		source, callbackErr := f.StorageBackedSession.Branch(name, ctx)
		if callbackErr != nil || source == nil {
			return callbackErr
		}
		branch = &facadeBranch{source: source, facade: f}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if branch == nil {
		return nil, nil
	}
	return branch, nil
}

func (f *MemorySessionFacade) CreateBranch(name string, at *string, ctx context.Context) (Branch, error) {
	var branch Branch
	err := f.admit(func() error {
		if err := f.assertOpen(); err != nil {
			return err
		}
		source, callbackErr := f.StorageBackedSession.CreateBranch(name, at, ctx)
		if callbackErr != nil {
			return callbackErr
		}
		branch = &facadeBranch{source: source, facade: f}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return branch, nil
}

type facadeBranch struct {
	source Branch
	facade *MemorySessionFacade
}

func (b *facadeBranch) Name() string { return b.source.Name() }

func (b *facadeBranch) GetTipID(ctx context.Context) (*string, error) {
	var tip *string
	err := b.facade.admit(func() error {
		if err := b.facade.assertOpen(); err != nil {
			return err
		}
		var callbackErr error
		tip, callbackErr = b.source.GetTipID(ctx)
		return callbackErr
	})
	return tip, err
}

func (b *facadeBranch) FindEntries(query *BranchScan, ctx context.Context) ([]Entry, error) {
	var entries []Entry
	err := b.facade.admit(func() error {
		if err := b.facade.assertOpen(); err != nil {
			return err
		}
		var callbackErr error
		entries, callbackErr = b.source.FindEntries(query, ctx)
		return callbackErr
	})
	return entries, err
}

func (b *facadeBranch) FindEntry(query *BranchScan, ctx context.Context) (Entry, error) {
	var entry Entry
	err := b.facade.admit(func() error {
		if err := b.facade.assertOpen(); err != nil {
			return err
		}
		var callbackErr error
		entry, callbackErr = b.source.FindEntry(query, ctx)
		return callbackErr
	})
	return entry, err
}

func (b *facadeBranch) AppendMessage(message agent.AgentMessage, ctx context.Context) (string, error) {
	var id string
	err := b.facade.admit(func() error {
		if err := b.facade.assertOpen(); err != nil {
			return err
		}
		var callbackErr error
		id, callbackErr = b.source.AppendMessage(message, ctx)
		return callbackErr
	})
	return id, err
}

func (b *facadeBranch) AppendCustomEntry(customType string, data JsonValue, ctx context.Context) (string, error) {
	var id string
	err := b.facade.admit(func() error {
		if err := b.facade.assertOpen(); err != nil {
			return err
		}
		var callbackErr error
		id, callbackErr = b.source.AppendCustomEntry(customType, data, ctx)
		return callbackErr
	})
	return id, err
}

func (f *MemorySessionFacade) GetEntries(ids []string, ctx context.Context) (map[string]Entry, error) {
	var entries map[string]Entry
	err := f.admit(func() error {
		if err := f.assertOpen(); err != nil {
			return err
		}
		var callbackErr error
		entries, callbackErr = f.StorageBackedSession.GetEntries(ids, ctx)
		return callbackErr
	})
	return entries, err
}

func (f *MemorySessionFacade) GetStats(ctx context.Context) (SessionStats, error) {
	var stats SessionStats
	err := f.admit(func() error {
		if err := f.assertOpen(); err != nil {
			return err
		}
		var callbackErr error
		stats, callbackErr = f.StorageBackedSession.GetStats(ctx)
		return callbackErr
	})
	return stats, err
}

func (f *MemorySessionFacade) GetValue(address Value, ctx context.Context) (*StoredValue, error) {
	var stored *StoredValue
	err := f.admit(func() error {
		if err := f.assertOpen(); err != nil {
			return err
		}
		var callbackErr error
		stored, callbackErr = f.StorageBackedSession.GetValue(address, ctx)
		return callbackErr
	})
	return stored, err
}

func (f *MemorySessionFacade) ScanValues(prefix Value, ctx context.Context) ([]StoredValue, error) {
	var values []StoredValue
	err := f.admit(func() error {
		if err := f.assertOpen(); err != nil {
			return err
		}
		var callbackErr error
		values, callbackErr = f.StorageBackedSession.ScanValues(prefix, ctx)
		return callbackErr
	})
	return values, err
}

func (f *MemorySessionFacade) ReadList(address ValueList, options *ListReadOptions, ctx context.Context) ([]ListElement, error) {
	var elements []ListElement
	err := f.admit(func() error {
		if err := f.assertOpen(); err != nil {
			return err
		}
		var callbackErr error
		elements, callbackErr = f.StorageBackedSession.ReadList(address, options, ctx)
		return callbackErr
	})
	return elements, err
}

func (f *MemorySessionFacade) ScanBranch(query StorageBranchScan, ctx context.Context) ([]Entry, error) {
	var entries []Entry
	err := f.admit(func() error {
		if err := f.assertOpen(); err != nil {
			return err
		}
		var callbackErr error
		entries, callbackErr = f.StorageBackedSession.ScanBranch(query, ctx)
		return callbackErr
	})
	return entries, err
}

func (f *MemorySessionFacade) GetName(ctx context.Context) (*string, error) {
	var name *string
	err := f.admit(func() error {
		if err := f.assertOpen(); err != nil {
			return err
		}
		var callbackErr error
		name, callbackErr = f.StorageBackedSession.GetName(ctx)
		return callbackErr
	})
	return name, err
}

func (f *MemorySessionFacade) GetLabel(targetID string, ctx context.Context) (*string, error) {
	var label *string
	err := f.admit(func() error {
		if err := f.assertOpen(); err != nil {
			return err
		}
		var callbackErr error
		label, callbackErr = f.StorageBackedSession.GetLabel(targetID, ctx)
		return callbackErr
	})
	return label, err
}

func (f *MemorySessionFacade) SetValue(address Value, next any, ctx context.Context) error {
	return f.admit(func() error {
		if err := f.assertOpen(); err != nil {
			return err
		}
		return f.StorageBackedSession.SetValue(address, next, ctx)
	})
}

func (f *MemorySessionFacade) DeleteValue(address Value, ctx context.Context) error {
	return f.admit(func() error {
		if err := f.assertOpen(); err != nil {
			return err
		}
		return f.StorageBackedSession.DeleteValue(address, ctx)
	})
}

func (f *MemorySessionFacade) AppendList(address ValueList, element any, ctx context.Context) error {
	return f.admit(func() error {
		if err := f.assertOpen(); err != nil {
			return err
		}
		return f.StorageBackedSession.AppendList(address, element, ctx)
	})
}

func (f *MemorySessionFacade) DeleteList(address ValueList, ctx context.Context) error {
	return f.admit(func() error {
		if err := f.assertOpen(); err != nil {
			return err
		}
		return f.StorageBackedSession.DeleteList(address, ctx)
	})
}

func (f *MemorySessionFacade) SetName(name *string, ctx context.Context) error {
	return f.admit(func() error {
		if err := f.assertOpen(); err != nil {
			return err
		}
		return f.StorageBackedSession.SetName(name, ctx)
	})
}

func (f *MemorySessionFacade) SetLabel(targetID string, label *string, ctx context.Context) error {
	return f.admit(func() error {
		if err := f.assertOpen(); err != nil {
			return err
		}
		return f.StorageBackedSession.SetLabel(targetID, label, ctx)
	})
}

func (f *MemorySessionFacade) Close(_ context.Context) error {
	f.closeMu.Lock()
	defer f.closeMu.Unlock()
	f.mu.Lock()
	if f.state != "open" {
		f.mu.Unlock()
		return nil
	}
	f.state = "closing"
	f.mu.Unlock()
	f.inFlight.Wait()
	f.mu.Lock()
	f.state = "closed"
	f.mu.Unlock()
	f.onClose()
	return nil
}

// MemorySessionRepo is the in-memory SessionRepo (pi: MemorySessionRepo).
type MemorySessionRepo struct {
	now func() int64

	mu         sync.Mutex
	sessions   map[string]*memorySessionRecord
	pendingIDs map[string]struct{}
	closed     bool
}

// NewMemorySessionRepo builds one memory repo (pi: constructor).
func NewMemorySessionRepo(options MemorySessionRepoOptions) *MemorySessionRepo {
	now := options.Now
	if now == nil {
		now = defaultNow
	}
	return &MemorySessionRepo{
		now:        now,
		sessions:   map[string]*memorySessionRecord{},
		pendingIDs: map[string]struct{}{},
	}
}

func (r *MemorySessionRepo) assertOpen() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return fmt.Errorf("MemorySessionRepo is closed")
	}
	return nil
}

func (r *MemorySessionRepo) Create(options SessionCreateOptions, ctx context.Context) (Session, error) {
	opts := options
	if err := r.assertOpen(); err != nil {
		return nil, err
	}
	createdAt := r.now()
	id := ""
	if opts.ID != nil {
		id = *opts.ID
	} else {
		id = aiutils.UUIDv7WithTime(createdAt)
	}
	if err := r.reserveID(id); err != nil {
		return nil, err
	}
	defer func() {
		r.mu.Lock()
		delete(r.pendingIDs, id)
		r.mu.Unlock()
	}()

	metadata := SessionMetadata{ID: id, CreatedAt: createdAt, StorageVersion: memoryStorageVersion}
	if opts.ParentSessionID != nil {
		metadata.ParentSessionID = opts.ParentSessionID
	}
	storage := NewMemoryStorage(MemoryStorageOptions{Now: r.now})
	session := NewStorageBackedSession(metadata, storage, StorageBackedSessionOptions{})
	record := &memorySessionRecord{metadata: metadata, storage: storage, session: session, open: true}
	r.mu.Lock()
	r.sessions[id] = record
	r.mu.Unlock()
	return newMemorySessionFacade(session, func() {
		record.mu.Lock()
		record.open = false
		record.mu.Unlock()
	}), nil
}

func (r *MemorySessionRepo) Open(metadata SessionMetadata, _ context.Context) (Session, error) {
	meta := metadata
	if err := r.assertOpen(); err != nil {
		return nil, err
	}
	// Memory sessions are always created at the current storage version, so
	// persistent-backend version gating does not apply here.
	r.mu.Lock()
	record, ok := r.sessions[meta.ID]
	r.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("Unknown session: %s", meta.ID)
	}
	record.mu.Lock()
	defer record.mu.Unlock()
	if record.open {
		return nil, fmt.Errorf("Session is already open: %s", meta.ID)
	}
	record.open = true
	return newMemorySessionFacade(record.session, func() {
		record.mu.Lock()
		record.open = false
		record.mu.Unlock()
	}), nil
}

func (r *MemorySessionRepo) List(_ any, _ context.Context) ([]SessionMetadata, error) {
	if err := r.assertOpen(); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]SessionMetadata, 0, len(r.sessions))
	for _, record := range r.sessions {
		out = append(out, record.metadata)
	}
	// TS iterates Map values in insertion order; Go maps have none. Order by
	// id for determinism.
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (r *MemorySessionRepo) Delete(metadata SessionMetadata, ctx context.Context) error {
	meta := metadata
	if err := r.assertOpen(); err != nil {
		return err
	}
	r.mu.Lock()
	record, ok := r.sessions[meta.ID]
	r.mu.Unlock()
	if !ok {
		return fmt.Errorf("Unknown session: %s", meta.ID)
	}
	record.mu.Lock()
	open := record.open
	record.mu.Unlock()
	if open {
		return fmt.Errorf("Session is open: %s", meta.ID)
	}
	if err := record.session.Close(ctx); err != nil {
		return err
	}
	r.mu.Lock()
	delete(r.sessions, meta.ID)
	r.mu.Unlock()
	return nil
}

func (r *MemorySessionRepo) Fork(source SessionMetadata, options ForkOptions, ctx context.Context) (Session, error) {
	sourceMetadata := source
	if err := r.assertOpen(); err != nil {
		return nil, err
	}
	r.mu.Lock()
	sourceRecord, ok := r.sessions[sourceMetadata.ID]
	r.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("Unknown session: %s", sourceMetadata.ID)
	}
	createdAt := r.now()
	id := ""
	if options.ID != nil {
		id = *options.ID
	} else {
		id = aiutils.UUIDv7WithTime(createdAt)
	}
	if err := r.reserveID(id); err != nil {
		return nil, err
	}
	defer func() {
		r.mu.Lock()
		delete(r.pendingIDs, id)
		r.mu.Unlock()
	}()

	sourceSnapshot, err := sourceRecord.storage.CaptureForkSource(ctx)
	if err != nil {
		return nil, err
	}
	snapshot, err := CreateForkSnapshot(sourceSnapshot, options)
	if err != nil {
		return nil, err
	}
	storage, err := MemoryStorageFromSnapshot(MemoryStorageOptions{Now: r.now}, snapshot)
	if err != nil {
		return nil, err
	}
	sourceID := sourceRecord.metadata.ID
	metadata := SessionMetadata{
		ID:              id,
		CreatedAt:       createdAt,
		StorageVersion:  memoryStorageVersion,
		ParentSessionID: &sourceID,
	}
	session := NewStorageBackedSession(metadata, storage, StorageBackedSessionOptions{})
	record := &memorySessionRecord{metadata: metadata, storage: storage, session: session, open: true}
	r.mu.Lock()
	r.sessions[id] = record
	r.mu.Unlock()
	return newMemorySessionFacade(session, func() {
		record.mu.Lock()
		record.open = false
		record.mu.Unlock()
	}), nil
}

func (r *MemorySessionRepo) reserveID(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.sessions[id]; exists {
		return fmt.Errorf("Session already exists: %s", id)
	}
	if _, pending := r.pendingIDs[id]; pending {
		return fmt.Errorf("Session already exists: %s", id)
	}
	r.pendingIDs[id] = struct{}{}
	return nil
}

// Close closes the repo and every open session handle (pi: close). pi
// leaves a TODO on ownership semantics for JsonlSessionRepo; MemorySessionRepo
// closes all sessions, and pig mirrors that for both.
func (r *MemorySessionRepo) Close(ctx context.Context) error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil
	}
	r.closed = true
	records := make([]*memorySessionRecord, 0, len(r.sessions))
	for _, record := range r.sessions {
		records = append(records, record)
	}
	r.mu.Unlock()
	for _, record := range records {
		if err := record.session.Close(ctx); err != nil {
			return err
		}
	}
	return nil
}

// Compile-time interface instantiation (pi: MemorySessionRepo implements
// SessionRepo<SessionMetadata, SessionCreateOptions, undefined>; the TList
// void parameter maps to any with a always-nil value).
var _ SessionRepo[SessionMetadata, SessionCreateOptions, any] = (*MemorySessionRepo)(nil)
