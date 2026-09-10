// pi: test/harness/session/search.test.ts — scanning session search over
// in-memory and jsonl-backed storages.
//
// Deviations:
//   - collect() drains the iter.Seq2 and keeps the terminating error (pi:
//     await collect(iterable) + rejects).
//   - WorkspaceMetadata's extra cwd field collapses to the base
//     SessionMetadata (generic collapse per the header note).
//   - The `"apply" in search` structural assertion has no Go counterpart
//     (the interface is fixed).
//   - The abort case cancels the context; the yielded error is
//     context.Canceled (pi: AbortError rejection).
package search_test

import (
	"context"
	"errors"
	"iter"
	"testing"

	lo "github.com/samber/lo"

	"github.com/ZoroSpace/pig/agent/harness/env"
	"github.com/ZoroSpace/pig/agent/harness/session"
	jsonl "github.com/ZoroSpace/pig/agent/harness/session/jsonl"
	search "github.com/ZoroSpace/pig/agent/search"
	"github.com/ZoroSpace/pig/ai/types"
)

func searchMessage(text string) types.UserMessage {
	return types.UserMessage{
		Role:      types.RoleUser,
		Content:   types.UserContent{Parts: []types.Content{types.NewTextContent(text)}},
		Timestamp: 1,
	}
}

func createMemorySession(t *testing.T, id string, createdAt int64) *session.Session {
	t.Helper()
	return session.NewSession(session.NewInMemorySessionStorage(session.SessionMetadata{
		ID:        id,
		CreatedAt: createdAt,
	}), nil)
}

// collect drains a hit sequence, failing on its terminating error (pi:
// collect + rejects).
func collect(t *testing.T, seq search.SessionSearch, text string, options *search.SessionSearchOptions) ([]search.ScanningSessionSearchHit, error) {
	t.Helper()
	hits := []search.ScanningSessionSearchHit{}
	var seqErr error
	for hit, err := range seq.Search(context.Background(), text, options) {
		if err != nil {
			seqErr = err
			break
		}
		hits = append(hits, hit)
	}
	return hits, seqErr
}

func TestScansAnArbitraryInMemoryProjectedSource(t *testing.T) {
	root := createMemorySession(t, "root", 1)
	if _, err := root.AppendMessage(searchMessage("fix auth flow")); err != nil {
		t.Fatal(err)
	}
	other := createMemorySession(t, "other", 2)
	if _, err := other.AppendMessage(searchMessage("auth in another workspace")); err != nil {
		t.Fatal(err)
	}
	s := search.CreateScanningSessionSearch([]search.ScanningReadable{root, other}, nil)

	hits, err := collect(t, s, "auth", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 || hits[0].SessionID != "root" || hits[1].SessionID != "other" {
		t.Fatalf("hits = %+v", hits)
	}
	missing, err := collect(t, s, "missing", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) != 0 {
		t.Fatalf("missing hits = %+v", missing)
	}
}

func TestIncludesLabelsInMemoryScanningProjections(t *testing.T) {
	s := createMemorySession(t, "session", 1)
	entryID, err := s.AppendMessage(searchMessage("plain body"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetLabel(entryID, lo.ToPtr("important label")); err != nil {
		t.Fatal(err)
	}
	searcher := search.CreateScanningSessionSearch([]search.ScanningReadable{s}, nil)

	hits, err := collect(t, searcher, "important", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].SessionID != "session" || hits[0].EntryID != entryID {
		t.Fatalf("hits = %+v, want entry %s", hits, entryID)
	}
}

func TestHonorsEntryTypeFiltersAndAbortSignalsInScanningSearch(t *testing.T) {
	s := createMemorySession(t, "session", 1)
	messageEntryID, err := s.AppendMessage(searchMessage("auth message"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendCustomEntry("note", map[string]any{"text": "auth custom"}); err != nil {
		t.Fatal(err)
	}
	searcher := search.CreateScanningSessionSearch([]search.ScanningReadable{s}, nil)

	hits, err := collect(t, searcher, "auth", &search.SessionSearchOptions{
		EntryTypes: []session.EntryType{session.EntryTypeMessage},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].EntryID != messageEntryID {
		t.Fatalf("filtered hits = %+v, want message entry %s", hits, messageEntryID)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var abortErr error
	for _, err := range searcher.Search(ctx, "auth", nil) {
		abortErr = err
		break
	}
	if !errors.Is(abortErr, context.Canceled) {
		t.Fatalf("abort error = %v, want context.Canceled", abortErr)
	}
}

func TestScansJSONLSessionsFromDiskThroughTheJSONLScanningSource(t *testing.T) {
	t.Helper()
	root := t.TempDir()
	options := jsonl.JsonlSessionRepoOptions{
		Fs:           env.NewNodeExecutionEnv(env.NodeExecutionEnvOptions{Cwd: root}),
		SessionsRoot: root,
	}
	repository := jsonl.NewJsonlSessionRepo(options)
	cwd := root + "/workspace"
	otherCwd := root + "/other"
	s, err := repository.Create(&jsonl.JsonlSessionCreateOptions{
		SessionCreateOptions: session.SessionCreateOptions{ID: lo.ToPtr("jsonl")},
		Cwd:                  cwd,
	})
	if err != nil {
		t.Fatal(err)
	}
	entryID, err := s.AppendMessage(searchMessage("jsonl backed auth entry"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetLabel(entryID, lo.ToPtr("disk label")); err != nil {
		t.Fatal(err)
	}
	other, err := repository.Create(&jsonl.JsonlSessionCreateOptions{
		SessionCreateOptions: session.SessionCreateOptions{ID: lo.ToPtr("other")},
		Cwd:                  otherCwd,
	})
	if err != nil {
		t.Fatal(err)
	}
	otherEntryID, err := other.AppendMessage(searchMessage("jsonl backed auth entry in another cwd"))
	if err != nil {
		t.Fatal(err)
	}

	// pi: createScanningSessionSearch((query) => jsonlReadables(options,
	// query)) — the source function loads each listed session's storage.
	source := search.ScanningReadableSource(func(sourceOptions any) iter.Seq[search.ScanningReadable] {
		return jsonlReadables(t, options, sourceOptions)
	})
	searcher := search.CreateScanningSessionSearch(source, nil)

	authHits, err := collect(t, searcher, "auth", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(authHits) != 2 {
		t.Fatalf("auth hits = %+v, want 2", authHits)
	}
	bySession := map[string]string{}
	for _, hit := range authHits {
		bySession[hit.SessionID] = hit.EntryID
	}
	if bySession["jsonl"] != entryID || bySession["other"] != otherEntryID {
		t.Fatalf("hits by session = %+v", bySession)
	}

	diskHits, err := collect(t, searcher, "disk", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(diskHits) != 1 || diskHits[0].SessionID != "jsonl" || diskHits[0].EntryID != entryID {
		t.Fatalf("disk hits = %+v", diskHits)
	}
}

// jsonlReadables yields one loaded storage per listed session (pi:
// jsonlReadables generator).
func jsonlReadables(t *testing.T, options jsonl.JsonlSessionRepoOptions, sourceOptions any) iter.Seq[search.ScanningReadable] {
	return func(yield func(search.ScanningReadable) bool) {
		var query *jsonl.JsonlSessionListOptions
		if o, ok := sourceOptions.(*jsonl.JsonlSessionListOptions); ok {
			query = o
		}
		listed, err := jsonl.ListJsonlSessionMetadata(options, query)
		if err != nil {
			return
		}
		for i := range listed {
			storage, err := jsonl.LoadJsonlSessionStorageByMetadata(options, listed[i])
			if err != nil {
				continue
			}
			if !yield(storage) {
				return
			}
		}
	}
}
