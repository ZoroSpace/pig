// pi: harness/session/jsonl/storage.ts — JSONL storage backed by an injected
// filesystem capability (format 4 transactions; torn-write repair; legacy v3
// lazy upgrade).
//
// Deviations:
//   - pi's promise commitQueue serializes commits; pig guards all state with
//     a mutex.
//   - fileValue(Result) error propagation collapses to Go error returns.
package jsonl

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/samber/lo"

	"github.com/ZoroSpace/pig/agent/harness"
	"github.com/ZoroSpace/pig/agent/harness/session"
	"github.com/ZoroSpace/pig/ai/types"
	aiutils "github.com/ZoroSpace/pig/ai/utils"
)

// harnessFileSystem aliases harness.FileSystem for local brevity.
type harnessFileSystem = harness.FileSystem

// jsonlBacking distinguishes the v4 and lazy-upgraded legacy v3 backings
// (pi: JsonlBacking).
type jsonlBacking struct {
	kind string // "v4" | "v3"
	// v3 members:
	importedUsage  types.Usage
	baselineWrites []session.CommittedWrite
}

// JsonlStorage implements session.Storage over one JSONL file (pi:
// JsonlStorage).
type JsonlStorage struct {
	fileSystem harnessFileSystem
	path       string
	now        func() int64

	mu sync.Mutex
	// commitGate serializes Commit/CaptureForkSource in admission order
	// (pi: the commitQueue promise chain).
	commitGate chan struct{}
	header     JsonlStorageHeader
	backing    jsonlBacking
	stateMu    sync.RWMutex
	state      *session.InMemoryStorageState
	stateName  string // "open" | "closing" | "closed"
}

// harnessFileSystem aliases harness.FileSystem (kept short in this file).

func newJsonlStorage(options *JsonlStorageOptions, header JsonlStorageHeader, backing jsonlBacking) *JsonlStorage {
	now := options.Now
	if now == nil {
		now = func() int64 { return time.Now().UnixMilli() }
	}
	return &JsonlStorage{
		fileSystem: options.FileSystem,
		path:       options.Path,
		now:        now,
		header:     header,
		backing:    backing,
		commitGate: make(chan struct{}, 1),
		state:      session.NewInMemoryStorageState(),
		stateName:  "open",
	}
}

// CreateJsonlStorage initializes a fresh storage file with initial writes
// (pi: JsonlStorage.create static).
func CreateJsonlStorage(options *JsonlStorageOptions, header JsonlStorageHeader, initialWrites []session.Write, ctx context.Context) (*JsonlStorage, error) {
	storage := newJsonlStorage(options, header, jsonlBacking{kind: "v4"})
	prepared, err := storage.state.PrepareCommit(initialWrites, storage.now())
	if err != nil {
		return nil, err
	}
	var transactions [][]session.CommittedWrite
	if len(prepared.Writes) != 0 {
		transactions = [][]session.CommittedWrite{prepared.Writes}
	}
	if err := publishFileAtomically(options.FileSystem, options.Path, serializeStorage(header, transactions), ctx); err != nil {
		return nil, err
	}
	storage.state.ApplyValidated(prepared.Writes)
	return storage, nil
}

// CreateJsonlStorageFromForkSnapshot atomically creates storage from a
// complete prepared snapshot (pi: JsonlStorage.createFromForkSnapshot).
func CreateJsonlStorageFromForkSnapshot(options *JsonlStorageOptions, header JsonlStorageHeader, snapshot session.ForkDestinationSnapshot, ctx context.Context) (*JsonlStorage, error) {
	writes := session.ForkSnapshotWrites(snapshot)
	snapshotHeader := header
	nextSeq := snapshot.NextSeq
	snapshotHeader.NextSeq = &nextSeq
	transactions := make([][]session.CommittedWrite, len(writes))
	for i, write := range writes {
		transactions[i] = []session.CommittedWrite{write}
	}
	if err := publishFileAtomically(options.FileSystem, options.Path, serializeStorage(snapshotHeader, transactions), ctx); err != nil {
		return nil, err
	}
	return OpenJsonlStorage(options, ctx)
}

// OpenJsonlStorage loads and replays one storage file (pi: JsonlStorage.open
// static).
func OpenJsonlStorage(options *JsonlStorageOptions, ctx context.Context) (*JsonlStorage, error) {
	content, err := options.FileSystem.ReadTextFile(ctx, options.Path)
	if err != nil {
		return nil, fmt.Errorf("Failed to read JSONL storage %s: %w", options.Path, err)
	}
	lines, torn := splitCompleteLines(content)
	if len(lines) == 0 || lines[0] == "" {
		return nil, fmt.Errorf("Invalid JSONL storage %s: missing header", options.Path)
	}
	parsedHeader, err := ParseJsonlSessionHeader(lines[0])
	if err != nil {
		return nil, fmt.Errorf("Invalid JSONL storage %s: invalid header: %w", options.Path, err)
	}
	if parsedHeader.Format == "v3-legacy" {
		return openLegacyV3(options, parsedHeader.V3Legacy, lines[1:], ctx)
	}

	header := *parsedHeader.V4
	if header.StorageVersion != JSONLStorageVersion {
		return nil, fmt.Errorf("Session %s uses unsupported storage version %d", header.ID, header.StorageVersion)
	}
	storage := newJsonlStorage(options, header, jsonlBacking{kind: "v4"})
	for index := 1; index < len(lines); index++ {
		line := lines[index]
		writes, err := parseTransaction(line)
		if err != nil {
			return nil, fmt.Errorf("Invalid JSONL storage %s: line %d: %w", options.Path, index+1, err)
		}
		if err := storage.replayCommitted(writes); err != nil {
			return nil, fmt.Errorf("Invalid JSONL storage %s: line %d: %w", options.Path, index+1, err)
		}
	}
	if header.NextSeq != nil {
		if err := storage.state.AdvanceNextSeq(*header.NextSeq); err != nil {
			return nil, err
		}
	}
	if torn {
		if err := publishFileAtomically(options.FileSystem, options.Path, strings.Join(lines, "\n")+"\n", ctx); err != nil {
			return nil, err
		}
	}
	return storage, nil
}

// openLegacyV3 lazily upgrades a legacy v3 file (pi: openLegacyV3).
func openLegacyV3(options *JsonlStorageOptions, header *LegacyV3SessionHeader, recordLines []string, ctx context.Context) (*JsonlStorage, error) {
	normalized, err := NormalizeLegacyV3Records(recordLines)
	if err != nil {
		return nil, err
	}
	targetHeader, err := NormalizeLegacyV3Header(options.FileSystem, header, ctx)
	if err != nil {
		return nil, err
	}
	nextSeq := normalized.NextSeq
	targetHeader.NextSeq = &nextSeq
	storage := newJsonlStorage(options, targetHeader, jsonlBacking{
		kind:           "v3",
		importedUsage:  normalized.ImportedUsage,
		baselineWrites: normalized.Writes,
	})
	if err := storage.replayCommitted(normalized.Writes); err != nil {
		return nil, err
	}
	return storage, nil
}

func (s *JsonlStorage) replayCommitted(writes []session.CommittedWrite) error {
	if err := s.state.ValidateCommitted(writes); err != nil {
		return err
	}
	s.state.ApplyValidated(writes)
	return nil
}

// Header returns the parsed storage header (pi: readonly header field).
func (s *JsonlStorage) Header() JsonlStorageHeader {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.header
}

// Commit applies one transaction (pi: commit).
func (s *JsonlStorage) Commit(writes []session.Write, ctx context.Context) (session.CommitResult, error) {
	s.commitGate <- struct{}{}
	defer func() { <-s.commitGate }()
	s.mu.Lock()
	if s.stateName != "open" {
		s.mu.Unlock()
		return session.CommitResult{}, fmt.Errorf("JsonlStorage is closed")
	}
	s.mu.Unlock()
	if s.backing.kind == "v3" && len(writes) != 0 {
		return s.upgradeLegacyV3ToV4(writes, ctx)
	}
	s.stateMu.Lock()
	prepared, err := s.state.PrepareCommit(writes, s.now())
	if err != nil {
		s.stateMu.Unlock()
		return session.CommitResult{}, err
	}
	if len(prepared.Writes) != 0 {
		if err := s.fileSystem.AppendFile(ctx, s.path, []byte(serializeTransaction(prepared.Writes)+"\n")); err != nil {
			s.stateMu.Unlock()
			return session.CommitResult{}, fmt.Errorf("Failed to append JSONL storage %s: %w", s.path, err)
		}
	}
	stats := s.state.ApplyValidated(prepared.Writes)
	s.stateMu.Unlock()
	result := session.CommitResult{
		FirstSeq:  prepared.Result.FirstSeq,
		Seqs:      prepared.Result.Seqs,
		Timestamp: prepared.Result.Timestamp,
		Stats:     s.withImportedUsage(stats),
	}
	return result, nil
}

// upgradeLegacyV3ToV4 atomically upgrades the legacy backing and preserves
// the first caller write as a v4 transaction (pi: upgradeLegacyV3ToV4).
func (s *JsonlStorage) upgradeLegacyV3ToV4(callerWrites []session.Write, ctx context.Context) (session.CommitResult, error) {
	timestamp := s.now()
	adjustment := session.UsageRow{
		ID:         aiutils.UUIDv7WithTime(timestamp),
		Usage:      s.backing.importedUsage,
		Adjustment: true,
		Details:    map[string]any{"source": "v3-import"},
	}
	writes := append([]session.Write{session.InsertUsage(adjustment)}, callerWrites...)
	s.stateMu.Lock()
	prepared, err := s.state.PrepareCommit(writes, timestamp)
	if err != nil {
		s.stateMu.Unlock()
		return session.CommitResult{}, err
	}
	s.stateMu.Unlock()

	nextSeq := prepared.Result.FirstSeq + int64(len(prepared.Writes))
	upgradedHeader := s.header
	upgradedHeader.NextSeq = &nextSeq
	var transactions [][]session.CommittedWrite
	for _, write := range s.backing.baselineWrites {
		transactions = append(transactions, []session.CommittedWrite{write})
	}
	transactions = append(transactions, prepared.Writes)
	if err := publishFileAtomically(s.fileSystem, s.path, serializeStorage(upgradedHeader, transactions), ctx); err != nil {
		return session.CommitResult{}, err
	}

	s.stateMu.Lock()
	stats := s.state.ApplyValidated(prepared.Writes)
	s.stateMu.Unlock()
	s.backing = jsonlBacking{kind: "v4"}
	s.header = upgradedHeader
	// The first sequence belongs to the internal usage adjustment; return
	// only caller-write sequences.
	result := session.CommitResult{
		FirstSeq:  prepared.Result.FirstSeq + 1,
		Seqs:      prepared.Result.Seqs[1:],
		Timestamp: prepared.Result.Timestamp,
		Stats:     stats,
	}
	return result, nil
}

func (s *JsonlStorage) assertOpen() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stateName != "open" {
		return fmt.Errorf("JsonlStorage is closed")
	}
	return nil
}

func (s *JsonlStorage) GetEntries(ids []string, _ context.Context) (map[string]session.Entry, error) {
	if err := s.assertOpen(); err != nil {
		return nil, err
	}
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	return s.state.GetEntries(ids), nil
}

func (s *JsonlStorage) GetValue(address session.Value, _ context.Context) (*session.StoredValue, error) {
	if err := s.assertOpen(); err != nil {
		return nil, err
	}
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	return s.state.GetValue(address), nil
}

func (s *JsonlStorage) ScanValues(prefix session.Value, _ context.Context) ([]session.StoredValue, error) {
	if err := s.assertOpen(); err != nil {
		return nil, err
	}
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	return s.state.ScanValues(prefix), nil
}

func (s *JsonlStorage) ReadList(address session.ValueList, options *session.ListReadOptions, _ context.Context) ([]session.ListElement, error) {
	if err := s.assertOpen(); err != nil {
		return nil, err
	}
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	return s.state.ReadList(address, options)
}

func (s *JsonlStorage) ScanBranch(query session.StorageBranchScan, _ context.Context) ([]session.Entry, error) {
	if err := s.assertOpen(); err != nil {
		return nil, err
	}
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	return s.state.ScanBranch(query)
}

func (s *JsonlStorage) ScanBranchStructure(query session.StorageBranchScan, _ context.Context) ([]session.EntryStructure, error) {
	if err := s.assertOpen(); err != nil {
		return nil, err
	}
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	return s.state.ScanBranchStructure(query)
}

func (s *JsonlStorage) ScanEntries(query session.EntryScan, _ context.Context) ([]session.Entry, error) {
	if err := s.assertOpen(); err != nil {
		return nil, err
	}
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	return s.state.ScanEntries(query), nil
}

func (s *JsonlStorage) ScanUsage(query session.UsageScan, _ context.Context) ([]session.UsageRow, error) {
	if err := s.assertOpen(); err != nil {
		return nil, err
	}
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	return s.state.ScanUsage(query), nil
}

func (s *JsonlStorage) GetStats(_ context.Context) (session.SessionStats, error) {
	if err := s.assertOpen(); err != nil {
		return session.SessionStats{}, err
	}
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	return s.withImportedUsage(s.state.GetStats()), nil
}

func (s *JsonlStorage) withImportedUsage(stats session.SessionStats) session.SessionStats {
	if s.backing.kind == "v4" {
		return stats
	}
	stats.Usage = s.backing.importedUsage
	return stats
}

// CaptureForkSource captures the state needed to fork at one serialized
// boundary between commits (pi: captureForkSource).
func (s *JsonlStorage) CaptureForkSource(_ context.Context) (session.ForkSourceSnapshot, error) {
	if err := s.assertOpen(); err != nil {
		return session.ForkSourceSnapshot{}, err
	}
	s.commitGate <- struct{}{}
	defer func() { <-s.commitGate }()
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	return s.state.SnapshotEntriesAndValues()
}

func (s *JsonlStorage) Close(_ context.Context) error {
	// Drain admitted commits before sealing (pi: close waits on the commit
	// queue).
	s.commitGate <- struct{}{}
	defer func() { <-s.commitGate }()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stateName == "closed" || s.stateName == "closing" {
		return nil
	}
	s.stateName = "closing"
	s.stateName = "closed"
	return nil
}

// parseCommittedWrite decodes one replay write (pi: parseCommittedWrite).
func parseCommittedWrite(raw json.RawMessage) (session.CommittedWrite, error) {
	var value map[string]json.RawMessage
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, fmt.Errorf("Invalid JSONL transaction write")
	}
	seqField, ok := value["seq"]
	if !ok {
		return nil, fmt.Errorf("Invalid JSONL write seq")
	}
	var seq float64
	if err := json.Unmarshal(seqField, &seq); err != nil || seq < 1 || seq != float64(int64(seq)) {
		return nil, fmt.Errorf("Invalid JSONL write seq")
	}
	var kind string
	if rawKind, ok := value["kind"]; ok {
		_ = json.Unmarshal(rawKind, &kind)
	}
	switch kind {
	case "entry":
		var timestamp float64
		if rawTimestamp, ok := value["timestamp"]; !ok || json.Unmarshal(rawTimestamp, &timestamp) != nil || timestamp < 0 {
			return nil, fmt.Errorf("Invalid JSONL entry timestamp")
		}
		entry, err := session.DecodeEntry(raw)
		if err != nil {
			return nil, err
		}
		return session.CommittedEntryWrite{Entry: entry}, nil
	case "usage":
		var write session.CommittedUsageWrite
		if err := json.Unmarshal(raw, &write); err != nil {
			return nil, err
		}
		return write, nil
	case "value":
		var op string
		if rawOp, ok := value["op"]; ok {
			_ = json.Unmarshal(rawOp, &op)
		}
		switch op {
		case "set":
			var write session.CommittedValueSetWrite
			if err := json.Unmarshal(raw, &write); err != nil {
				return nil, err
			}
			return write, nil
		case "delete":
			var write session.CommittedValueDeleteWrite
			if err := json.Unmarshal(raw, &write); err != nil {
				return nil, err
			}
			return write, nil
		default:
			return nil, fmt.Errorf("Invalid JSONL value operation: %v", op)
		}
	case "list":
		var op string
		if rawOp, ok := value["op"]; ok {
			_ = json.Unmarshal(rawOp, &op)
		}
		switch op {
		case "append":
			var write session.CommittedListAppendWrite
			if err := json.Unmarshal(raw, &write); err != nil {
				return nil, err
			}
			return write, nil
		case "delete":
			var write session.CommittedListDeleteWrite
			if err := json.Unmarshal(raw, &write); err != nil {
				return nil, err
			}
			return write, nil
		default:
			return nil, fmt.Errorf("Invalid JSONL list operation: %v", op)
		}
	default:
		return nil, fmt.Errorf("Invalid JSONL write kind: %v", kind)
	}
}

// parseTransaction decodes one replay line (pi: parseTransaction).
func parseTransaction(line string) ([]session.CommittedWrite, error) {
	var value any
	if err := json.Unmarshal([]byte(line), &value); err != nil {
		return nil, fmt.Errorf("Invalid JSONL transaction: not valid JSON")
	}
	var raws []json.RawMessage
	if array, ok := value.([]any); ok {
		raws = make([]json.RawMessage, len(array))
		for i, item := range array {
			raws[i], _ = json.Marshal(item)
		}
	} else {
		raws = []json.RawMessage{[]byte(line)}
	}
	writes := make([]session.CommittedWrite, len(raws))
	for i, raw := range raws {
		write, err := parseCommittedWrite(raw)
		if err != nil {
			return nil, err
		}
		writes[i] = write
	}
	return writes, nil
}

// serializeTransaction encodes one line (pi: serializeTransaction): a single
// write serializes as one object, several as an array.
func serializeTransaction(writes []session.CommittedWrite) string {
	if len(writes) == 1 {
		return string(mustJSON(writes[0]))
	}
	return string(mustJSON(writes))
}

func mustJSON(v any) []byte {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return data
}

// serializeStorage encodes header + transactions (pi: serializeStorage).
func serializeStorage(header JsonlStorageHeader, transactions [][]session.CommittedWrite) string {
	lines := []string{string(mustJSON(header))}
	for _, transaction := range transactions {
		lines = append(lines, serializeTransaction(transaction))
	}
	return strings.Join(lines, "\n") + "\n"
}

// splitCompleteLines splits full lines and reports a torn tail (pi:
// splitCompleteLines).
func splitCompleteLines(content string) ([]string, bool) {
	if strings.HasSuffix(content, "\n") {
		trimmed := strings.TrimSuffix(content, "\n")
		if trimmed == "" {
			return []string{}, false
		}
		return strings.Split(trimmed, "\n"), false
	}
	lastNewline := strings.LastIndex(content, "\n")
	if lastNewline == -1 {
		return []string{}, true
	}
	return strings.Split(content[:lastNewline], "\n"), true
}

// publishFileAtomically stages content and renames it into place (pi:
// publishFileAtomically).
func publishFileAtomically(fileSystem harnessFileSystem, destinationPath string, content string, ctx context.Context) error {
	tempPath := destinationPath + ".tmp"
	if err := fileSystem.WriteFile(ctx, tempPath, []byte(content)); err != nil {
		_ = fileSystem.Remove(ctx, tempPath, nil, lo.ToPtr(true))
		return fmt.Errorf("Failed to stage JSONL storage %s: %w", destinationPath, err)
	}
	if err := fileSystem.RenameFile(ctx, tempPath, destinationPath); err != nil {
		_ = fileSystem.Remove(ctx, tempPath, nil, lo.ToPtr(true))
		return fmt.Errorf("Failed to publish JSONL storage %s: %w", destinationPath, err)
	}
	return nil
}
