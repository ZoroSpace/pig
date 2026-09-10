// pi: test/harness/{memory-conformance,memory-session-repo,memory-storage}.test.ts
// and jsonl-session-repo-conformance.test.ts / jsonl-storage-conformance.test.ts
// wrappers — run the shared conformance case suites against each backend.
package session_test

import (
	"context"
	"testing"

	"github.com/ZoroSpace/pig/agent/harness/session"
	pigtesting "github.com/ZoroSpace/pig/agent/harness/session/testing"
	"github.com/ZoroSpace/pig/ai/types"
)

// memoryStorageFixture builds a fresh MemoryStorage per case (pi:
// MemoryStorage fixtures with now: () => 10).
func memoryStorageFixture(t *testing.T) pigtesting.StorageFixture {
	t.Helper()
	storage := session.NewMemoryStorage(session.MemoryStorageOptions{Now: func() int64 { return 10 }})
	return pigtesting.StorageFixture{Storage: storage, Close: func() {}}
}

func TestMemoryStorageConformance(t *testing.T) {
	pigtesting.RunConformanceCases(t, pigtesting.StorageConformanceCases(memoryStorageFixture))
}

// memoryRepoAdapter adapts MemorySessionRepo to the conformance interface
// (pi: MemorySessionRepo satisfies SessionRepo<SessionMetadata,
// SessionCreateOptions, undefined> structurally).
type memoryRepoAdapter struct{ repo *session.MemorySessionRepo }

func (a *memoryRepoAdapter) Metadata(sess session.Session) session.SessionMetadata {
	return sess.GetMetadata()
}

func (a *memoryRepoAdapter) MetadataID(metadata session.SessionMetadata) string { return metadata.ID }

func (a *memoryRepoAdapter) MetadataParentSessionID(metadata session.SessionMetadata) *string {
	return metadata.ParentSessionID
}

func (a *memoryRepoAdapter) Create(id *string, parentSessionID *string, ctx context.Context) (session.Session, error) {
	return a.repo.Create(session.SessionCreateOptions{ID: id, ParentSessionID: parentSessionID}, ctx)
}

func (a *memoryRepoAdapter) Open(metadata session.SessionMetadata, ctx context.Context) (session.Session, error) {
	return a.repo.Open(metadata, ctx)
}

func (a *memoryRepoAdapter) List(ctx context.Context) ([]session.SessionMetadata, error) {
	return a.repo.List(nil, ctx)
}

func (a *memoryRepoAdapter) Delete(metadata session.SessionMetadata, ctx context.Context) error {
	return a.repo.Delete(metadata, ctx)
}

func (a *memoryRepoAdapter) Fork(source session.SessionMetadata, options session.ForkOptions, ctx context.Context) (session.Session, error) {
	return a.repo.Fork(source, options, ctx)
}

func (a *memoryRepoAdapter) Close(ctx context.Context) error {
	return a.repo.Close(ctx)
}

// memoryRepoFixture builds a fresh MemorySessionRepo per case.
func memoryRepoFixture(t *testing.T) pigtesting.ConformanceRepo[session.SessionMetadata] {
	t.Helper()
	repo := session.NewMemorySessionRepo(session.MemorySessionRepoOptions{Now: func() int64 { return 10 }})
	t.Cleanup(func() { _ = repo.Close(context.Background()) })
	return &memoryRepoAdapter{repo: repo}
}

func TestMemorySessionRepoConformance(t *testing.T) {
	pigtesting.RunConformanceCases(t, pigtesting.SessionRepoConformanceCases(memoryRepoFixture))
}

// TestMemoryStorageCommitTimestampLocksForkBoundaries pins the now-injection
// semantics (pi: memory-storage.test.ts).
func TestMemoryStorageCommitTimestampLocksForkBoundaries(t *testing.T) {
	ctx := context.Background()
	storage := session.NewMemoryStorage(session.MemoryStorageOptions{Now: func() int64 { return 10 }})
	result, err := storage.Commit([]session.Write{session.InsertEntry(conformanceUserMessageEntry("entry", nil, "entry"))}, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Timestamp != 10 {
		t.Fatalf("timestamp = %d, want 10", result.Timestamp)
	}
	if _, err := storage.Commit(nil, ctx); err != nil {
		t.Fatal(err)
	}
	if err := storage.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := storage.Commit(nil, ctx); err == nil {
		t.Fatal("commit after close must reject")
	}
}

// TestMemoryRepoCreatesSessionsWithInjectedClock pins repo-level timestamps
// (pi: memory-session-repo.test.ts).
func TestMemoryRepoCreatesSessionsWithInjectedClock(t *testing.T) {
	ctx := context.Background()
	repo := session.NewMemorySessionRepo(session.MemorySessionRepoOptions{Now: func() int64 { return 42 }})
	sess, err := repo.Create(session.SessionCreateOptions{}, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if sess.GetMetadata().CreatedAt != 42 {
		t.Fatalf("createdAt = %d", sess.GetMetadata().CreatedAt)
	}
	_ = sess.Close(ctx)
	listed, err := repo.List(nil, ctx)
	if err != nil || len(listed) != 1 {
		t.Fatalf("list = %v, %v", listed, err)
	}
}

// conformanceUserMessageEntry mirrors the conformance suite's user entry
// helper (the testing package's helper is not exported).
func conformanceUserMessageEntry(id string, parentID *string, text string) *session.MessageEntry {
	entry := &session.MessageEntry{
		Message: types.UserMessage{
			Role:      types.RoleUser,
			Content:   types.UserContent{Parts: []types.Content{types.NewTextContent(text)}},
			Timestamp: 1_650_000_000_000,
		},
	}
	entry.ID = id
	entry.ParentID = parentID
	entry.Type = session.EntryTypeMessage
	return entry
}
