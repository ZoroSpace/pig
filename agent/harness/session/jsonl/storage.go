// pi: src/harness/session/jsonl/storage.ts — durable jsonl SessionStorage.
//
// Deviations:
//   - pi's tail-promise write chain → one mutex held across validate + fs
//     append + state apply, giving the same linearized write order (the
//     concurrent-write conformance cases pin linearization, not submission
//     order). Reads share the mutex (Go state is not safe for concurrent
//     use; pi relies on single-threadedness between microtask writes).
//   - Drain() awaits the same critical section (pi: drain() awaits the tail
//     chain); pending-operation errors were swallowed by the tail in TS and
//     were already reported by their own calls.
//   - pi's JsonlSessionStorage implements SessionStorage<JsonlSessionMetadata>
//     whose getMetadata returns the full metadata; the Go base interface
//     returns the base session.SessionMetadata, so the full value moves to
//     GetJsonlMetadata (repo code uses it; declared in the types header).
//   - FileSystem calls use context.Background(): pi's session layer never
//     forwards an abort signal, so the ctx parameter added at the
//     harness.FileSystem boundary is always "no signal" here.
//   - structuredClone → JSON round-trip clones (typed unions re-decode via
//     session.DecodeEntry/DecodeRecord; see codec header for the
//     unknown-property note).
//   - Date.now() → time.Now().UnixMilli().
//   - Statics: JsonlSessionStorage.create/load/fork → CreateJsonlSessionStorage /
//     LoadJsonlSessionStorage / (*JsonlSessionStorage).Fork (no ctx: pi's
//     session layer has no AbortSignal to map).
package jsonl

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/samber/lo"
	"strings"
	"sync"
	"time"

	"github.com/ZoroSpace/pig/agent/harness/session"
)

// context for filesystem calls inside storage operations (see header note).
var storageFSContext = context.Background()

// cloneEntry deep-copies a typed entry via JSON round-trip (pi:
// structuredClone).
func cloneEntry(entry session.Entry) (session.Entry, error) {
	raw, err := jsonMarshal(entry)
	if err != nil {
		return nil, err
	}
	cloned, err := session.DecodeEntry(raw)
	if err != nil {
		return nil, err
	}
	return cloned, nil
}

// cloneRecord deep-copies a typed lane record via JSON round-trip (pi:
// structuredClone).
func cloneRecord(record session.LaneRecord) (session.LaneRecord, error) {
	raw, err := jsonMarshal(record)
	if err != nil {
		return nil, err
	}
	cloned, err := session.DecodeRecord(raw)
	if err != nil {
		return nil, err
	}
	return cloned, nil
}

// publishFileAtomically builds a complete sibling temporary file, then
// atomically renames it over the destination. The populate callback must
// create or overwrite tempPath with the complete file. The destination is
// untouched until the rename commits, so a process crash while populating
// can leave only the ignored .tmp file behind.
//
// Returns an error when population or rename fails. On failure,
// temporary-file removal is best-effort and the original error is preserved.
// Callers must serialize publications to the same destination because they
// share its deterministic .tmp path. (pi: publishFileAtomically.)
func publishFileAtomically(fs JsonlSessionRepoFileSystem, destinationPath string, populate func(tempPath string) error) error {
	tempPath := destinationPath + ".tmp"
	err := populate(tempPath)
	if err == nil {
		err = fileResult(fs.RenameFile(storageFSContext, tempPath, destinationPath), "Failed to publish staged file "+destinationPath)
	}
	if err != nil {
		// Best-effort cleanup; the original error is preserved.
		_ = fs.Remove(storageFSContext, tempPath, nil, lo.ToPtr(true))
		return err
	}
	return nil
}

// JsonlSessionStorage is the file-backed session storage (pi:
// JsonlSessionStorage class).
type JsonlSessionStorage struct {
	fs       JsonlSessionRepoFileSystem
	metadata JsonlSessionMetadata
	state    *session.SessionState
	mu       sync.Mutex
}

// NewJsonlSessionStorage wraps an existing metadata + fresh state (pi:
// constructor; metadata is cloned).
func NewJsonlSessionStorage(fs JsonlSessionRepoFileSystem, metadata JsonlSessionMetadata) (*JsonlSessionStorage, error) {
	cloned, err := cloneMetadata(metadata)
	if err != nil {
		return nil, err
	}
	return &JsonlSessionStorage{fs: fs, metadata: cloned, state: session.NewSessionState()}, nil
}

// CreateJsonlSessionStorage initializes a new session file with one header
// line (pi: static create).
func CreateJsonlSessionStorage(fs JsonlSessionRepoFileSystem, path string, header JsonlV4Header) (*JsonlSessionStorage, error) {
	if err := fileResult(fs.WriteFile(storageFSContext, path, []byte(EncodeHeader(header))), "Failed to initialize session "+path); err != nil {
		return nil, err
	}
	info, err := fs.FileInfo(storageFSContext, path)
	if err != nil {
		return nil, fileResult(err, "Failed to read session metadata "+path)
	}
	return NewJsonlSessionStorage(fs, MetadataFromHeader(header, path, info.MtimeMs))
}

// LoadJsonlSessionStorage reads and replays one session file (pi: static
// load).
func LoadJsonlSessionStorage(fs JsonlSessionRepoFileSystem, path string) (*JsonlSessionStorage, error) {
	content, err := fs.ReadTextFile(storageFSContext, path)
	if err != nil {
		return nil, fileResult(err, "Failed to read session "+path)
	}
	physicalLines := strings.Split(content, "\n")
	if len(physicalLines) > 0 && physicalLines[len(physicalLines)-1] == "" {
		physicalLines = physicalLines[:len(physicalLines)-1]
	}
	if len(physicalLines) == 0 || physicalLines[0] == "" {
		return nil, invalidFile(path, 1, NewJsonlDecodeError("schema", "is missing a header", nil))
	}
	header, err := ParseHeader(physicalLines[0])
	if err != nil {
		return nil, invalidFile(path, 1, err)
	}
	info, err := fs.FileInfo(storageFSContext, path)
	if err != nil {
		return nil, fileResult(err, "Failed to read session metadata "+path)
	}
	storage, err := NewJsonlSessionStorage(fs, MetadataFromHeader(header, path, info.MtimeMs))
	if err != nil {
		return nil, err
	}
	for index := 1; index < len(physicalLines); index++ {
		line := physicalLines[index]
		mutation, err := ParseMutation(line)
		if err != nil {
			// Drop the unacknowledged partial append by atomically
			// publishing the valid prefix (pi: torn-tail repair).
			isTornTail := index == len(physicalLines)-1 && isSyntaxError(err)
			if isTornTail {
				validPrefix := strings.Join(physicalLines[:index], "\n") + "\n"
				err := publishFileAtomically(fs, path, func(tempPath string) error {
					return fileResult(fs.WriteFile(storageFSContext, tempPath, []byte(validPrefix)), "Failed to stage torn-tail repair "+path)
				})
				if err != nil {
					return nil, err
				}
				return storage, nil
			}
			return nil, invalidFile(path, index+1, err)
		}
		if err := storage.applyMutation(mutation); err != nil {
			var sessionErr *session.SessionError
			if asSessionError(err, &sessionErr) && sessionErr.Code == session.CodeInvalidEntry {
				return nil, invalidFile(path, index+1, err)
			}
			return nil, err
		}
	}
	if !strings.HasSuffix(content, "\n") {
		if err := fileResult(fs.AppendFile(storageFSContext, path, []byte("\n")), "Failed to repair unterminated session tail "+path); err != nil {
			return nil, err
		}
	}
	return storage, nil
}

// Fork copies this session's mutations into a new file (pi: fork).
func (s *JsonlSessionStorage) Fork(path string, header JsonlV4Header, options *session.ForkOptions) (*JsonlSessionStorage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	mutations, err := s.state.CreateForkMutations(options)
	if err != nil {
		return nil, err
	}
	err = publishFileAtomically(s.fs, path, func(tempPath string) error {
		targetStorage, err := CreateJsonlSessionStorage(s.fs, tempPath, header)
		if err != nil {
			return err
		}
		for _, mutation := range mutations {
			if err := targetStorage.appendMutation(mutation); err != nil {
				return err
			}
			if err := targetStorage.applyMutation(mutation); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return LoadJsonlSessionStorage(s.fs, path)
}

// Drain waits for pending write operations to complete (pi: drain). The
// mutex acquire is the wait; errors were reported by their own calls.
func (s *JsonlSessionStorage) Drain() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return nil
}

// GetMetadata returns the base session metadata (session.SessionStorage).
func (s *JsonlSessionStorage) GetMetadata() (session.SessionMetadata, error) {
	return s.metadata.SessionMetadata, nil
}

// GetJsonlMetadata returns the full jsonl metadata (pi: getMetadata).
func (s *JsonlSessionStorage) GetJsonlMetadata() (JsonlSessionMetadata, error) {
	cloned, err := cloneMetadata(s.metadata)
	if err != nil {
		return JsonlSessionMetadata{}, err
	}
	return cloned, nil
}

// GetLanes implements session.SessionStorage.
func (s *JsonlSessionStorage) GetLanes() ([]session.LanePointer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state.GetLanes(), nil
}

// CreateLane implements session.SessionStorage.
func (s *JsonlSessionStorage) CreateLane(lane string, at *string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.state.ValidateNewLane(lane); err != nil {
		return err
	}
	if err := s.state.ValidateTarget(at); err != nil {
		return err
	}
	mutation := session.SessionMutation{
		Kind:   session.MutationKindLane,
		Seq:    s.state.NextSequence(),
		Lane:   lo.ToPtr(lane),
		LeafID: at,
	}
	if err := s.appendMutation(mutation); err != nil {
		return err
	}
	return s.applyMutation(mutation)
}

// MoveLane implements session.SessionStorage.
func (s *JsonlSessionStorage) MoveLane(lane string, to *string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.state.RequireLane(lane); err != nil {
		return err
	}
	if err := s.state.ValidateTarget(to); err != nil {
		return err
	}
	mutation := session.SessionMutation{
		Kind:   session.MutationKindLane,
		Seq:    s.state.NextSequence(),
		Lane:   lo.ToPtr(lane),
		LeafID: to,
	}
	if err := s.appendMutation(mutation); err != nil {
		return err
	}
	return s.applyMutation(mutation)
}

// AppendEntry implements session.SessionStorage.
func (s *JsonlSessionStorage) AppendEntry(newEntry session.Entry, lane string) (session.Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	parentID, err := s.state.RequireLane(lane)
	if err != nil {
		return nil, err
	}
	if err := s.state.ValidateUnusedID(newEntry.Base().ID); err != nil {
		return nil, err
	}
	entry, err := cloneEntry(newEntry)
	if err != nil {
		return nil, err
	}
	base := entry.Base()
	base.ParentID = parentID
	base.Seq = s.state.NextSequence()
	base.Timestamp = time.Now().UnixMilli()
	mutation := session.SessionMutation{
		Kind:  session.MutationKindEntry,
		Lane:  lo.ToPtr(lane),
		Entry: entry,
	}
	if err := s.appendMutation(mutation); err != nil {
		return nil, err
	}
	if err := s.applyMutation(mutation); err != nil {
		return nil, err
	}
	return cloneEntry(entry)
}

// AppendRecord implements session.SessionStorage.
func (s *JsonlSessionStorage) AppendRecord(newRecord session.LaneRecord) (session.LaneRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.state.RequireLane(newRecord.Base().Lane); err != nil {
		return nil, err
	}
	if err := s.state.ValidateUnusedID(newRecord.Base().ID); err != nil {
		return nil, err
	}
	open, err := s.state.FindOpenOperations(newRecord.Base().Lane, lo.ToPtr(1))
	if err != nil {
		return nil, err
	}
	if newRecord.Base().Type == session.RecordTypeOperationStarted && len(open) > 0 {
		return nil, session.NewSessionError(
			session.CodeStorage,
			"Lane "+newRecord.Base().Lane+" already has an open operation "+open[0].ID,
		)
	}
	record, err := cloneRecord(newRecord)
	if err != nil {
		return nil, err
	}
	base := record.Base()
	base.Seq = s.state.NextSequence()
	base.Timestamp = time.Now().UnixMilli()
	mutation := session.SessionMutation{
		Kind:   session.MutationKindRecord,
		Record: record,
	}
	if err := s.appendMutation(mutation); err != nil {
		return nil, err
	}
	if err := s.applyMutation(mutation); err != nil {
		return nil, err
	}
	return cloneRecord(record)
}

// GetEntry implements session.SessionStorage; (nil, nil) when unknown.
func (s *JsonlSessionStorage) GetEntry(id string) (session.Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry := s.state.GetEntry(id)
	if entry == nil {
		return nil, nil
	}
	return cloneEntry(entry)
}

// FindEntries implements session.SessionStorage.
func (s *JsonlSessionStorage) FindEntries(query *session.EntryQuery) ([]session.Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := s.state.FindEntries(query)
	if err != nil {
		return nil, err
	}
	return cloneEntries(entries)
}

// FindEntriesOnBranch implements session.SessionStorage.
func (s *JsonlSessionStorage) FindEntriesOnBranch(query session.BranchEntryQuery) ([]session.Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := s.state.FindEntriesOnBranch(query)
	if err != nil {
		return nil, err
	}
	return cloneEntries(entries)
}

// FindRecords implements session.SessionStorage.
func (s *JsonlSessionStorage) FindRecords(query *session.RecordQuery) ([]session.LaneRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	records, err := s.state.FindRecords(query)
	if err != nil {
		return nil, err
	}
	return cloneRecords(records)
}

// FindOpenOperations implements session.SessionStorage.
func (s *JsonlSessionStorage) FindOpenOperations(lane string, limit *int) ([]session.OperationStartedRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	open, err := s.state.FindOpenOperations(lane, limit)
	if err != nil {
		return nil, err
	}
	records := make([]session.OperationStartedRecord, len(open))
	for i, record := range open {
		cloned, err := cloneRecord(&record)
		if err != nil {
			return nil, err
		}
		records[i] = *cloned.(*session.OperationStartedRecord)
	}
	return records, nil
}

// GetLog implements session.SessionStorage.
func (s *JsonlSessionStorage) GetLog(options *session.LogOptions) ([]session.LogItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	log, err := s.state.GetLog(options)
	if err != nil {
		return nil, err
	}
	items := make([]session.LogItem, len(log))
	for i, item := range log {
		items[i] = item
		if item.Entry != nil {
			cloned, err := cloneEntry(item.Entry)
			if err != nil {
				return nil, err
			}
			items[i].Entry = cloned
		}
		if item.Record != nil {
			cloned, err := cloneRecord(item.Record)
			if err != nil {
				return nil, err
			}
			items[i].Record = cloned
		}
	}
	return items, nil
}

// GetName implements session.SessionStorage.
func (s *JsonlSessionStorage) GetName() (*string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state.GetName(), nil
}

// SetName implements session.SessionStorage.
func (s *JsonlSessionStorage) SetName(name *string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	mutation := session.SessionMutation{
		Kind: session.MutationKindFact,
		Seq:  s.state.NextSequence(),
		Fact: "name",
		Name: name,
	}
	if err := s.appendMutation(mutation); err != nil {
		return err
	}
	return s.applyMutation(mutation)
}

// GetLabel implements session.SessionStorage.
func (s *JsonlSessionStorage) GetLabel(id string) (*string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	label, ok := s.state.GetLabel(id)
	if !ok {
		return nil, nil
	}
	return &label, nil
}

// SetLabel implements session.SessionStorage.
func (s *JsonlSessionStorage) SetLabel(id string, label *string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.state.ValidateTarget(&id); err != nil {
		return err
	}
	mutation := session.SessionMutation{
		Kind:     session.MutationKindFact,
		Seq:      s.state.NextSequence(),
		Fact:     "label",
		TargetID: id,
		Label:    label,
	}
	if err := s.appendMutation(mutation); err != nil {
		return err
	}
	return s.applyMutation(mutation)
}

// GetStats implements session.SessionStorage.
func (s *JsonlSessionStorage) GetStats() (session.SessionStats, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state.GetStats(), nil
}

// appendMutation appends one encoded line to the session file (pi:
// appendMutation). Callers hold mu.
func (s *JsonlSessionStorage) appendMutation(mutation session.SessionMutation) error {
	return fileResult(
		s.fs.AppendFile(storageFSContext, s.metadata.Path, []byte(EncodeMutation(mutation))),
		"Failed to append session "+s.metadata.Path,
	)
}

// applyMutation applies one mutation to the in-memory state (pi:
// applyMutation). Callers hold mu.
func (s *JsonlSessionStorage) applyMutation(mutation session.SessionMutation) error {
	return s.state.ApplyMutation(mutation, nil)
}

// jsonMarshal aliases json.Marshal for the clone helpers.
func jsonMarshal(value any) (json.RawMessage, error) {
	return json.Marshal(value)
}

// cloneMetadata deep-copies the jsonl metadata (pi: structuredClone of the
// constructor argument). Shallow copy suffices: callers treat the metadata
// map as immutable after construction, matching pi's clone-then-read usage.
func cloneMetadata(metadata JsonlSessionMetadata) (JsonlSessionMetadata, error) {
	raw, err := json.Marshal(metadata)
	if err != nil {
		return JsonlSessionMetadata{}, err
	}
	var cloned JsonlSessionMetadata
	if err := json.Unmarshal(raw, &cloned); err != nil {
		return JsonlSessionMetadata{}, err
	}
	return cloned, nil
}

// isSyntaxError reports whether err is a "syntax" JsonlDecodeError.
func isSyntaxError(err error) bool {
	var decodeErr *JsonlDecodeError
	if errors.As(err, &decodeErr) {
		return decodeErr.Kind == "syntax"
	}
	return false
}

// asSessionError is errors.As specialized for *session.SessionError.
func asSessionError(err error, target **session.SessionError) bool {
	return errors.As(err, target)
}

// cloneEntries deep-copies an entry slice.
func cloneEntries(entries []session.Entry) ([]session.Entry, error) {
	cloned := make([]session.Entry, len(entries))
	for i, entry := range entries {
		c, err := cloneEntry(entry)
		if err != nil {
			return nil, err
		}
		cloned[i] = c
	}
	return cloned, nil
}

// cloneRecords deep-copies a record slice.
func cloneRecords(records []session.LaneRecord) ([]session.LaneRecord, error) {
	cloned := make([]session.LaneRecord, len(records))
	for i, record := range records {
		c, err := cloneRecord(record)
		if err != nil {
			return nil, err
		}
		cloned[i] = c
	}
	return cloned, nil
}
