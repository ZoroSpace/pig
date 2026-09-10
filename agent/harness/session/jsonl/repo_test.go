// pi: test/harness/session/jsonl.test.ts — JsonlSessionRepo conformance run
// plus the JSONL v4 persistence suite.
//
// Deviations:
//   - withDefaultSessionCwd becomes jsonlRepoAdapter: pi re-narrows
//     SessionRepo<JsonlSessionMetadata,...> to SessionRepo<JsonlSessionMetadata>
//     by injecting cwd; the Go base interface carries only base metadata, so
//     the adapter also resolves ids to JsonlSessionMetadata via List.
//   - The concurrent create/fork cases drop vi.useFakeTimers (no Go
//     counterpart); the shared-destination claim is exercised directly with
//     an explicit id and goroutines.
//   - "rejects non-JSON payloads" cases live in storage_test.go.
package jsonl_test

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/samber/lo"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/agent/harness"
	"github.com/ZoroSpace/pig/agent/harness/session"
	jsonl "github.com/ZoroSpace/pig/agent/harness/session/jsonl"
	sessiontesting "github.com/ZoroSpace/pig/agent/harness/session/testing"
)

// jsonlRepoAdapter injects a fixed cwd and bridges base-metadata calls to
// the widened JsonlSessionRepo (pi: withDefaultSessionCwd).
type jsonlRepoAdapter struct {
	repo *jsonl.JsonlSessionRepo
	cwd  string
}

func (a *jsonlRepoAdapter) Create(options *session.SessionCreateOptions) (*session.Session, error) {
	createOptions := &jsonl.JsonlSessionCreateOptions{Cwd: a.cwd}
	if options != nil {
		createOptions.SessionCreateOptions = *options
	}
	return a.repo.Create(createOptions)
}

func (a *jsonlRepoAdapter) Open(metadata session.SessionMetadata) (*session.Session, error) {
	full, err := a.resolve(metadata.ID)
	if err != nil {
		return nil, err
	}
	return a.repo.Open(full)
}

func (a *jsonlRepoAdapter) List() ([]session.SessionMetadata, error) {
	listed, err := a.repo.List(nil)
	if err != nil {
		return nil, err
	}
	out := make([]session.SessionMetadata, len(listed))
	for i := range listed {
		out[i] = listed[i].SessionMetadata
	}
	return out, nil
}

func (a *jsonlRepoAdapter) Delete(metadata session.SessionMetadata) error {
	full, err := a.resolve(metadata.ID)
	if err != nil {
		// pi's delete removes with force:true, so deleting an already-deleted
		// session succeeds; a failed resolve means it is already gone.
		var sessionErr *session.SessionError
		if asSessionErr(err, &sessionErr) && sessionErr.Code == session.CodeNotFound {
			return nil
		}
		return err
	}
	return a.repo.Delete(full)
}

func (a *jsonlRepoAdapter) Fork(source session.SessionMetadata, options *session.ForkCreateOptions) (*session.Session, error) {
	full, err := a.resolve(source.ID)
	if err != nil {
		return nil, err
	}
	forkOptions := &jsonl.JsonlForkCreateOptions{
		JsonlSessionCreateOptions: jsonl.JsonlSessionCreateOptions{Cwd: a.cwd},
	}
	if options != nil {
		forkOptions.ForkOptions = options.ForkOptions
		forkOptions.SessionCreateOptions = options.SessionCreateOptions
	}
	return a.repo.Fork(full, forkOptions)
}

// resolve maps a session id to its JsonlSessionMetadata via a listing.
func (a *jsonlRepoAdapter) resolve(id string) (*jsonl.JsonlSessionMetadata, error) {
	listed, err := a.repo.List(nil)
	if err != nil {
		return nil, err
	}
	for i := range listed {
		if listed[i].ID == id {
			return &listed[i], nil
		}
	}
	return nil, session.NewSessionError(session.CodeNotFound, "Session not found: "+id)
}

func TestJsonlSessionRepoConformance(t *testing.T) {
	factory := func(t *testing.T) session.SessionRepo {
		root := t.TempDir()
		return &jsonlRepoAdapter{repo: createRepository(root), cwd: root}
	}
	cases := sessiontesting.CreateSessionBackendConformance(factory)
	sessiontesting.RunConformance(t, cases, factory)
}

// writeRawSession writes a raw session file at root/id.jsonl (pi:
// writeRawSession).
func writeRawSession(t *testing.T, root string, id string, mutations []string) jsonl.JsonlSessionMetadata {
	t.Helper()
	path := filepath.Join(root, id+".jsonl")
	header := fmt.Sprintf(`{"kind":"header","version":4,"id":%q,"createdAt":1,"cwd":%q}`, id, root)
	lines := append([]string{header}, mutations...)
	content := strings.Join(lines, "\n") + "\n"
	writeSessionFile(t, path, content)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return jsonl.JsonlSessionMetadata{
		SessionMetadata: session.SessionMetadata{ID: id, CreatedAt: 1},
		Cwd:             root,
		Path:            path,
		ModifiedAt:      info.ModTime().UnixMilli(),
		SourceFormat:    jsonl.SourceFormatV4,
	}
}

// rawMetadata wraps an existing path into metadata (pi: inline metadata
// literals).
func rawMetadata(t *testing.T, root string, id string, path string, createdAt int64) jsonl.JsonlSessionMetadata {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return jsonl.JsonlSessionMetadata{
		SessionMetadata: session.SessionMetadata{ID: id, CreatedAt: createdAt},
		Cwd:             root,
		Path:            path,
		ModifiedAt:      info.ModTime().UnixMilli(),
		SourceFormat:    jsonl.SourceFormatV4,
	}
}

// createWithID creates a session with an explicit id through the repo.
func createWithID(t *testing.T, repository *jsonl.JsonlSessionRepo, id string, cwd string) *session.Session {
	t.Helper()
	s, err := repository.Create(&jsonl.JsonlSessionCreateOptions{
		SessionCreateOptions: session.SessionCreateOptions{ID: lo.ToPtr(id)},
		Cwd:                  cwd,
	})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestExposesTheCompleteMetadataContract(t *testing.T) {
	root := t.TempDir()
	repository := createRepository(root)
	cwd := filepath.Join(root, "workspace", "project")
	s, err := repository.Create(&jsonl.JsonlSessionCreateOptions{
		SessionCreateOptions: session.SessionCreateOptions{ID: lo.ToPtr("metadata"), ParentSessionID: lo.ToPtr("parent")},
		Cwd:                  cwd,
		Metadata:             map[string]any{"owner": "agent", "nested": map[string]any{"enabled": true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = s
	listed, err := repository.List(&jsonl.JsonlSessionListOptions{Cwd: &cwd})
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 {
		t.Fatalf("listed length = %d, want 1", len(listed))
	}
	metadata := listed[0]
	if metadata.ID != "metadata" || metadata.Cwd != cwd {
		t.Fatalf("metadata = %+v", metadata.SessionMetadata)
	}
	if metadata.ParentSessionID == nil || *metadata.ParentSessionID != "parent" {
		t.Fatal("parentSessionId mismatch")
	}
	if metadata.SourceFormat != jsonl.SourceFormatV4 {
		t.Fatalf("sourceFormat = %d, want 4", metadata.SourceFormat)
	}
	if metadata.Metadata["owner"] != "agent" {
		t.Fatalf("metadata owner = %v", metadata.Metadata["owner"])
	}
	if metadata.ModifiedAt == 0 {
		t.Fatal("modifiedAt must be set from the filesystem")
	}
	otherCwd := filepath.Join(root, "other", "project")
	other, err := repository.List(&jsonl.JsonlSessionListOptions{Cwd: &otherCwd})
	if err != nil {
		t.Fatal(err)
	}
	if len(other) != 0 {
		t.Fatalf("other listing = %d, want 0", len(other))
	}
}

func TestRejectsAMalformedJSONHeaderOnOpenAndSkipsItWhenListing(t *testing.T) {
	root := t.TempDir()
	repository := createRepository(root)
	createWithID(t, repository, "valid", root)
	createWithID(t, repository, "malformed-header", root)
	metadataPath := metadataPathFor(t, repository, "malformed-header")
	malformed := "not json\n"
	writeSessionFile(t, metadataPath, malformed)

	_, err := repository.Open(&jsonl.JsonlSessionMetadata{
		SessionMetadata: session.SessionMetadata{ID: "malformed-header", CreatedAt: 1},
		Cwd:             root,
		Path:            metadataPath,
	})
	requireSessionError(t, err, session.CodeInvalidEntry)
	listed, err := repository.List(&jsonl.JsonlSessionListOptions{Cwd: &root})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for i := range listed {
		ids = append(ids, listed[i].ID)
	}
	if strings.Join(ids, ",") != "valid" {
		t.Fatalf("listed ids = %v, want [valid]", ids)
	}
	if readSessionFile(t, metadataPath) != malformed {
		t.Fatal("malformed file was modified during listing")
	}
}

func TestRejectsNonObjectHeaderMetadataOnOpenAndSkipsItWhenListing(t *testing.T) {
	root := t.TempDir()
	repository := createRepository(root)
	createWithID(t, repository, "valid", root)
	createWithID(t, repository, "invalid-header-metadata", root)
	metadataPath := metadataPathFor(t, repository, "invalid-header-metadata")
	malformed := `{"kind":"header","version":4,"id":"invalid-header-metadata","createdAt":1,"cwd":` + quote(root) + `,"metadata":"invalid"}` + "\n"
	writeSessionFile(t, metadataPath, malformed)

	_, err := repository.Open(&jsonl.JsonlSessionMetadata{
		SessionMetadata: session.SessionMetadata{ID: "invalid-header-metadata", CreatedAt: 1},
		Cwd:             root,
		Path:            metadataPath,
	})
	requireSessionError(t, err, session.CodeInvalidEntry)
	listed, err := repository.List(&jsonl.JsonlSessionListOptions{Cwd: &root})
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].ID != "valid" {
		t.Fatalf("listing must skip the malformed session")
	}
	if readSessionFile(t, metadataPath) != malformed {
		t.Fatal("malformed file was modified during listing")
	}
}

func quote(s string) string {
	data, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(data)
}

func TestRejectsSessionIDsThatCannotBeUsedInFilenames(t *testing.T) {
	root := t.TempDir()
	repository := createRepository(root)
	_, err := repository.Create(&jsonl.JsonlSessionCreateOptions{
		SessionCreateOptions: session.SessionCreateOptions{ID: lo.ToPtr("../escape")},
		Cwd:                  root,
	})
	requireSessionError(t, err, session.CodeInvalidPayload)
}

func TestAllowsTheSameExplicitSessionIDInDifferentWorkingDirectories(t *testing.T) {
	root := t.TempDir()
	repository := createRepository(root)
	firstCwd := filepath.Join(root, "workspaces", "first")
	secondCwd := filepath.Join(root, "workspaces", "second")

	createWithID(t, repository, "shared", firstCwd)
	createWithID(t, repository, "shared", secondCwd)

	listed, err := repository.List(nil)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	var cwds []string
	for i := range listed {
		ids = append(ids, listed[i].ID)
		cwds = append(cwds, listed[i].Cwd)
	}
	if strings.Join(ids, ",") != "shared,shared" {
		t.Fatalf("listed ids = %v", ids)
	}
	sort.Strings(cwds)
	want := []string{firstCwd, secondCwd}
	sort.Strings(want)
	if strings.Join(cwds, "|") != strings.Join(want, "|") {
		t.Fatalf("cwds = %v, want %v", cwds, want)
	}
}

func TestRejectsConcurrentCreateAndForkCallsForTheSameDestination(t *testing.T) {
	kinds := [][2]string{{"create", "create"}, {"create", "fork"}, {"fork", "fork"}}
	for _, pair := range kinds {
		t.Run(pair[0]+" and "+pair[1], func(t *testing.T) {
			root := t.TempDir()
			repository := createRepository(root)
			cwd := filepath.Join(root, "workspace")
			source := createWithID(t, repository, "source", cwd)

			run := func(kind string) func() (*session.Session, error) {
				if kind == "create" {
					return func() (*session.Session, error) {
						return repository.Create(&jsonl.JsonlSessionCreateOptions{
							SessionCreateOptions: session.SessionCreateOptions{ID: lo.ToPtr("same")},
							Cwd:                  cwd,
						})
					}
				}
				sourceMetadata, err := source.GetMetadata()
				if err != nil {
					t.Fatal(err)
				}
				full, err := resolveMetadata(t, repository, sourceMetadata.ID)
				if err != nil {
					t.Fatal(err)
				}
				return func() (*session.Session, error) {
					return repository.Fork(full, &jsonl.JsonlForkCreateOptions{
						JsonlSessionCreateOptions: jsonl.JsonlSessionCreateOptions{
							SessionCreateOptions: session.SessionCreateOptions{ID: lo.ToPtr("same")},
							Cwd:                  cwd,
						},
					})
				}
			}

			firstRun, secondRun := run(pair[0]), run(pair[1])
			errs := make([]error, 2)
			var wg sync.WaitGroup
			wg.Add(2)
			go func() { defer wg.Done(); _, errs[0] = firstRun() }()
			go func() { defer wg.Done(); _, errs[1] = secondRun() }()
			wg.Wait()

			successes, failures := 0, 0
			for _, err := range errs {
				if err == nil {
					successes++
					continue
				}
				var sessionErr *session.SessionError
				if asSessionErr(err, &sessionErr) && sessionErr.Code == session.CodeAlreadyExists {
					failures++
				} else {
					t.Fatalf("unexpected failure: %v", err)
				}
			}
			if successes != 1 || failures != 1 {
				t.Fatalf("successes = %d, failures = %d", successes, failures)
			}
			listed, err := repository.List(&jsonl.JsonlSessionListOptions{Cwd: &cwd})
			if err != nil {
				t.Fatal(err)
			}
			same := 0
			for i := range listed {
				if listed[i].ID == "same" {
					same++
				}
			}
			if same != 1 {
				t.Fatalf("same sessions = %d, want 1", same)
			}
		})
	}
}

func TestReleasesADestinationReservationAfterAFailedCreateOrFork(t *testing.T) {
	for _, kind := range []string{"create", "fork"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			fs := newInjectedFS(root)
			repository := repoWithFS(root, fs)
			cwd := filepath.Join(root, "workspace")
			source := createWithID(t, repository, "source", cwd)
			sourceMetadata, err := source.GetMetadata()
			if err != nil {
				t.Fatal(err)
			}
			full, err := resolveMetadata(t, repository, sourceMetadata.ID)
			if err != nil {
				t.Fatal(err)
			}

			if kind == "create" {
				fs.failWriteFile = 1
			} else {
				fs.failRenameFile = 1
			}
			if kind == "create" {
				_, err = repository.Create(&jsonl.JsonlSessionCreateOptions{
					SessionCreateOptions: session.SessionCreateOptions{ID: lo.ToPtr("retry")},
					Cwd:                  cwd,
				})
			} else {
				_, err = repository.Fork(full, &jsonl.JsonlForkCreateOptions{
					JsonlSessionCreateOptions: jsonl.JsonlSessionCreateOptions{
						SessionCreateOptions: session.SessionCreateOptions{ID: lo.ToPtr("retry")},
						Cwd:                  cwd,
					},
				})
			}
			requireSessionError(t, err, session.CodeStorage)

			var retry *session.Session
			if kind == "create" {
				retry, err = repository.Create(&jsonl.JsonlSessionCreateOptions{
					SessionCreateOptions: session.SessionCreateOptions{ID: lo.ToPtr("retry")},
					Cwd:                  cwd,
				})
			} else {
				retry, err = repository.Fork(full, &jsonl.JsonlForkCreateOptions{
					JsonlSessionCreateOptions: jsonl.JsonlSessionCreateOptions{
						SessionCreateOptions: session.SessionCreateOptions{ID: lo.ToPtr("retry")},
						Cwd:                  cwd,
					},
				})
			}
			if err != nil {
				t.Fatal(err)
			}
			if retry == nil {
				t.Fatal("retry must succeed after a failed attempt released the reservation")
			}
		})
	}
}

func TestSortsListedSessionsByCurrentFilesystemModificationTime(t *testing.T) {
	root := t.TempDir()
	repository := createRepository(root)
	newestCwd := filepath.Join(root, "workspaces", "newest")
	oldestCwd := filepath.Join(root, "workspaces", "oldest")
	createWithID(t, repository, "newest", newestCwd)
	createWithID(t, repository, "oldest", oldestCwd)

	newestTime := time.UnixMilli(1_700_000_002_000)
	oldestTime := time.UnixMilli(1_700_000_001_000)
	for _, id := range []string{"newest", "oldest"} {
		path := metadataPathFor(t, repository, id)
		modTime := oldestTime
		if id == "newest" {
			modTime = newestTime
		}
		if err := os.Chtimes(path, modTime, modTime); err != nil {
			t.Fatal(err)
		}
	}

	listed, err := repository.List(nil)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for i := range listed {
		ids = append(ids, listed[i].ID)
	}
	if strings.Join(ids, ",") != "newest,oldest" {
		t.Fatalf("listed ids = %v, want [newest oldest]", ids)
	}
	cwdListed, err := repository.List(&jsonl.JsonlSessionListOptions{Cwd: &newestCwd})
	if err != nil {
		t.Fatal(err)
	}
	if len(cwdListed) != 1 || cwdListed[0].ID != "newest" {
		t.Fatal("cwd-filtered listing mismatch")
	}
}

func TestWritesOneLinePerMutationAndRestoresTheSharedSequence(t *testing.T) {
	root := t.TempDir()
	repository := createRepository(root)
	s := createWithID(t, repository, "session", root)
	metadataPath := metadataPathFor(t, repository, "session")
	entryID, err := s.AppendCustomEntry("note", map[string]any{"value": 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CreateLane("thread", &entryID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendRecord(&session.OperationStartedRecord{
		RecordBase:   session.RecordBase{Type: session.RecordTypeOperationStarted, ID: "run", Lane: "thread"},
		SourceLeafID: nil,
		Intent: &session.RunIntent{
			Kind:            session.OperationIntentRun,
			OriginalPrompt:  []agent.AgentMessage{},
			InitialMessages: []session.Entry{},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetName(lo.ToPtr("Example")); err != nil {
		t.Fatal(err)
	}
	if err := s.SetLabel(entryID, lo.ToPtr("checkpoint")); err != nil {
		t.Fatal(err)
	}
	if err := s.MoveLane("main", nil); err != nil {
		t.Fatal(err)
	}

	content := readSessionFile(t, metadataPath)
	kinds := sessionLineKinds(t, content)
	assertJSONEqual(t, kinds, []string{"header", "entry", "lane", "record", "fact", "fact", "lane"})
	assertJSONEqual(t, sessionLineSeqs(t, content), []int64{1, 2, 3, 4, 5, 6})

	reopened := openByID(t, createRepository(root), "session")
	lanes, err := reopened.GetLanes()
	if err != nil {
		t.Fatal(err)
	}
	assertJSONEqual(t, lanes, []session.LanePointer{
		{Lane: "main", LeafID: nil},
		{Lane: "thread", LeafID: &entryID},
	})
	name, err := reopened.GetName()
	if err != nil {
		t.Fatal(err)
	}
	if name == nil || *name != "Example" {
		t.Fatalf("name = %v", name)
	}
	label, err := reopened.GetLabel(entryID)
	if err != nil {
		t.Fatal(err)
	}
	if label == nil || *label != "checkpoint" {
		t.Fatalf("label = %v", label)
	}
	records, err := reopened.FindRecords(nil)
	if err != nil {
		t.Fatal(err)
	}
	assertJSONEqual(t, recordIDList(records), []string{"run"})

	runRecords, err := reopened.FindRecords(&session.RecordQuery{
		Type:          lo.ToPtr(session.RecordTypeOperationStarted),
		OperationKind: lo.ToPtr(session.OperationIntentRun),
	})
	if err != nil {
		t.Fatal(err)
	}
	assertJSONEqual(t, recordIDList(runRecords), []string{"run"})

	open, err := reopened.FindOpenOperations("thread", lo.ToPtr(2))
	if err != nil {
		t.Fatal(err)
	}
	var openIDs []string
	for _, record := range open {
		openIDs = append(openIDs, record.ID)
	}
	assertJSONEqual(t, openIDs, []string{"run"})

	log, err := reopened.GetLog(nil)
	if err != nil {
		t.Fatal(err)
	}
	var logSeqs []int64
	for _, item := range log {
		logSeqs = append(logSeqs, item.Seq)
	}
	assertJSONEqual(t, logSeqs, []int64{1, 2, 3, 4, 5, 6})

	finish, err := reopened.AppendRecord(&session.OperationFinishedRecord{
		RecordBase: session.RecordBase{Type: session.RecordTypeOperationFinished, ID: "finish", Lane: "thread"},
		RunID:      "run",
		Outcome:    "completed",
	})
	if err != nil {
		t.Fatal(err)
	}
	if finish.Base().Seq != 7 {
		t.Fatalf("finish seq = %d, want 7", finish.Base().Seq)
	}
	open, err = reopened.FindOpenOperations("thread", lo.ToPtr(2))
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 0 {
		t.Fatalf("open operations = %d, want 0", len(open))
	}
}

func TestRecomputesForkMessageCountsWhenReopening(t *testing.T) {
	root := t.TempDir()
	repository := createRepository(root)
	source := createWithID(t, repository, "source", root)
	if _, err := source.AppendMessage(userMessage("one")); err != nil {
		t.Fatal(err)
	}
	if _, err := source.AppendMessage(userMessage("two")); err != nil {
		t.Fatal(err)
	}
	sourceMetadata, err := source.GetMetadata()
	if err != nil {
		t.Fatal(err)
	}
	full, err := resolveMetadata(t, repository, sourceMetadata.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = repository.Fork(full, &jsonl.JsonlForkCreateOptions{
		JsonlSessionCreateOptions: jsonl.JsonlSessionCreateOptions{
			SessionCreateOptions: session.SessionCreateOptions{ID: lo.ToPtr("fork")},
			Cwd:                  root,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	reopened := openByID(t, createRepository(root), "fork")
	stats, err := reopened.GetStats()
	if err != nil {
		t.Fatal(err)
	}
	if stats.MessageCount != 2 {
		t.Fatalf("messageCount = %d, want 2", stats.MessageCount)
	}
	if _, err := reopened.AppendMessage(userMessage("three")); err != nil {
		t.Fatal(err)
	}
	stats, err = reopened.GetStats()
	if err != nil {
		t.Fatal(err)
	}
	if stats.MessageCount != 3 {
		t.Fatalf("messageCount after append = %d, want 3", stats.MessageCount)
	}

	verified := openByID(t, createRepository(root), "fork")
	stats, err = verified.GetStats()
	if err != nil {
		t.Fatal(err)
	}
	if stats.MessageCount != 3 {
		t.Fatalf("verified messageCount = %d, want 3", stats.MessageCount)
	}
}

func TestReopensATreeForkWithItsLanesAndFacts(t *testing.T) {
	root := t.TempDir()
	repository := createRepository(root)
	source := createWithID(t, repository, "source", root)
	rootID, err := source.AppendCustomEntry("root", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := source.CreateLane("thread", &rootID); err != nil {
		t.Fatal(err)
	}
	mainID, err := source.AppendCustomEntry("main", nil)
	if err != nil {
		t.Fatal(err)
	}
	threadEntry, err := source.AppendEntry(&session.CustomEntry{
		EntryBase:  session.EntryBase{Type: session.EntryTypeCustom, ID: "thread"},
		CustomType: "thread",
	}, "thread")
	if err != nil {
		t.Fatal(err)
	}
	threadID := threadEntry.Base().ID
	if err := source.SetName(lo.ToPtr("Source")); err != nil {
		t.Fatal(err)
	}
	if err := source.SetLabel(threadID, lo.ToPtr("tip")); err != nil {
		t.Fatal(err)
	}
	sourceMetadata, err := source.GetMetadata()
	if err != nil {
		t.Fatal(err)
	}
	full, err := resolveMetadata(t, repository, sourceMetadata.ID)
	if err != nil {
		t.Fatal(err)
	}
	forkSession, err := repository.Fork(full, &jsonl.JsonlForkCreateOptions{
		ForkOptions: session.ForkOptions{Scope: session.ForkScopeTree},
		JsonlSessionCreateOptions: jsonl.JsonlSessionCreateOptions{
			SessionCreateOptions: session.SessionCreateOptions{ID: lo.ToPtr("fork")},
			Cwd:                  root,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = forkSession

	forkPath := metadataPathFor(t, repository, "fork")
	var importedLanes []bool
	for _, line := range strings.Split(strings.TrimRight(readSessionFile(t, forkPath), "\n"), "\n") {
		var parsed map[string]any
		if err := json.Unmarshal([]byte(line), &parsed); err != nil {
			t.Fatal(err)
		}
		if parsed["kind"] == "entry" {
			_, hasLane := parsed["lane"]
			importedLanes = append(importedLanes, hasLane)
		}
	}
	assertJSONEqual(t, importedLanes, []bool{false, false, false})

	reopened := openByID(t, createRepository(root), "fork")
	entries, err := reopened.FindEntries(&session.EntryQuery{Order: session.OrderOldestFirst})
	if err != nil {
		t.Fatal(err)
	}
	assertJSONEqual(t, entryIDList(entries), []string{rootID, mainID, threadID})
	lanes, err := reopened.GetLanes()
	if err != nil {
		t.Fatal(err)
	}
	assertJSONEqual(t, lanes, []session.LanePointer{
		{Lane: "main", LeafID: &mainID},
		{Lane: "thread", LeafID: &threadID},
	})
	name, err := reopened.GetName()
	if err != nil {
		t.Fatal(err)
	}
	if name == nil || *name != "Source" {
		t.Fatalf("name = %v", name)
	}
	label, err := reopened.GetLabel(threadID)
	if err != nil {
		t.Fatal(err)
	}
	if label == nil || *label != "tip" {
		t.Fatalf("label = %v", label)
	}
	records, err := reopened.FindRecords(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 0 {
		t.Fatalf("records = %d, want 0", len(records))
	}
}

func TestDoesNotPublishAPartialForkWhenStagingFails(t *testing.T) {
	root := t.TempDir()
	fs := newInjectedFS(root)
	repository := repoWithFS(root, fs)
	source := createWithID(t, repository, "source", root)
	if _, err := source.AppendMessage(userMessage("one")); err != nil {
		t.Fatal(err)
	}
	if _, err := source.AppendMessage(userMessage("two")); err != nil {
		t.Fatal(err)
	}
	sourcePath := metadataPathFor(t, repository, "source")
	sourceMetadataID := "source"

	// Fail the second staging append: pi fails appendFile once after one
	// successful passthrough (the first fork mutation append).
	fs.passThroughAppendFile = 1
	fs.failAppendFile = 1

	full, err := resolveMetadata(t, repository, sourceMetadataID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = repository.Fork(full, &jsonl.JsonlForkCreateOptions{
		JsonlSessionCreateOptions: jsonl.JsonlSessionCreateOptions{
			SessionCreateOptions: session.SessionCreateOptions{ID: lo.ToPtr("fork")},
			Cwd:                  root,
		},
	})
	requireSessionError(t, err, session.CodeStorage)

	listed, err := repository.List(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].ID != "source" {
		t.Fatalf("listing after failed fork = %v", listed)
	}
	entries, err := os.ReadDir(filepath.Dir(sourcePath))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".tmp") {
			t.Fatalf("leftover temp file: %s", entry.Name())
		}
	}
}

func TestDoesNotPublishAForkWhenAtomicRenameFails(t *testing.T) {
	root := t.TempDir()
	fs := newInjectedFS(root)
	repository := repoWithFS(root, fs)
	source := createWithID(t, repository, "source", root)
	if _, err := source.AppendMessage(userMessage("one")); err != nil {
		t.Fatal(err)
	}
	sourcePath := metadataPathFor(t, repository, "source")

	full, err := resolveMetadata(t, repository, "source")
	if err != nil {
		t.Fatal(err)
	}

	fs.failRenameFile = 1
	_, err = repository.Fork(full, &jsonl.JsonlForkCreateOptions{
		JsonlSessionCreateOptions: jsonl.JsonlSessionCreateOptions{
			SessionCreateOptions: session.SessionCreateOptions{ID: lo.ToPtr("fork")},
			Cwd:                  root,
		},
	})
	requireSessionError(t, err, session.CodeStorage)

	listed, err := repository.List(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].ID != "source" {
		t.Fatalf("listing after failed fork = %v", listed)
	}
	entries, err := os.ReadDir(filepath.Dir(sourcePath))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".tmp") {
			t.Fatalf("leftover temp file: %s", entry.Name())
		}
	}
}

func TestRepairsAValidFinalLineMissingItsNewline(t *testing.T) {
	root := t.TempDir()
	repository := createRepository(root)
	s := createWithID(t, repository, "session", root)
	metadataPath := metadataPathFor(t, repository, "session")
	firstID, err := s.AppendCustomEntry("first", nil)
	if err != nil {
		t.Fatal(err)
	}
	unterminated := strings.TrimRight(readSessionFile(t, metadataPath), "\n")
	writeSessionFile(t, metadataPath, unterminated)

	reopened := openByID(t, createRepository(root), "session")
	if got := readSessionFile(t, metadataPath); got != unterminated+"\n" {
		t.Fatal("unterminated tail was not repaired")
	}
	secondID, err := reopened.AppendCustomEntry("second", nil)
	if err != nil {
		t.Fatal(err)
	}

	verified := openByID(t, createRepository(root), "session")
	entries, err := verified.FindEntries(&session.EntryQuery{Order: session.OrderOldestFirst})
	if err != nil {
		t.Fatal(err)
	}
	assertJSONEqual(t, entryIDList(entries), []string{firstID, secondID})
}

func TestFailsToOpenWhenRepairingAMissingFinalNewlineFails(t *testing.T) {
	root := t.TempDir()
	repository := createRepository(root)
	s := createWithID(t, repository, "session", root)
	metadataPath := metadataPathFor(t, repository, "session")
	if _, err := s.AppendCustomEntry("first", nil); err != nil {
		t.Fatal(err)
	}
	writeSessionFile(t, metadataPath, strings.TrimRight(readSessionFile(t, metadataPath), "\n"))

	fs := newInjectedFS(root)
	fs.failAppendFile = 1
	failingRepository := repoWithFS(root, fs)
	_, err := failingRepository.Open(&jsonl.JsonlSessionMetadata{
		SessionMetadata: session.SessionMetadata{ID: "session", CreatedAt: 1},
		Cwd:             root,
		Path:            metadataPath,
	})
	requireSessionError(t, err, session.CodeStorage)
	var sessionErr *session.SessionError
	if !asSessionErr(err, &sessionErr) {
		t.Fatal("expected session error")
	}
	if sessionErr.Unwrap() == nil {
		t.Fatal("storage error must carry the fs cause")
	}
}

func TestTruncatesAMalformedFinalLine(t *testing.T) {
	root := t.TempDir()
	repository := createRepository(root)
	s := createWithID(t, repository, "session", root)
	metadataPath := metadataPathFor(t, repository, "session")
	if _, err := s.AppendCustomEntry("note", map[string]any{"value": "kept"}); err != nil {
		t.Fatal(err)
	}
	validPrefix := readSessionFile(t, metadataPath)
	appendSessionFile(t, metadataPath, `{"kind":"entry"`)

	reopened := openByID(t, createRepository(root), "session")
	entries, err := reopened.FindEntries(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	if got := readSessionFile(t, metadataPath); got != validPrefix {
		t.Fatal("torn tail was not truncated to the valid prefix")
	}
	appendedID, err := reopened.AppendCustomEntry("after-recovery", nil)
	if err != nil {
		t.Fatal(err)
	}
	entry, err := reopened.GetEntry(appendedID)
	if err != nil {
		t.Fatal(err)
	}
	if entry.Base().Seq != 2 {
		t.Fatalf("appended seq = %d, want 2", entry.Base().Seq)
	}
}

func TestRejectsACompleteInvalidFinalMutationWithoutModifyingTheFile(t *testing.T) {
	root := t.TempDir()
	metadata := writeRawSession(t, root, "invalid-final-mutation", []string{`{"kind":"unknown","seq":1}`})
	corrupted := readSessionFile(t, metadata.Path)

	_, err := createRepository(root).Open(&metadata)
	requireSessionError(t, err, session.CodeInvalidEntry)
	if readSessionFile(t, metadata.Path) != corrupted {
		t.Fatal("corrupted file was modified")
	}
}

func TestRejectsAMalformedMiddleLineWithoutModifyingTheFile(t *testing.T) {
	root := t.TempDir()
	repository := createRepository(root)
	s := createWithID(t, repository, "session", root)
	metadataPath := metadataPathFor(t, repository, "session")
	if _, err := s.AppendCustomEntry("first", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendCustomEntry("second", nil); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(readSessionFile(t, metadataPath), "\n"), "\n")
	corrupted := lines[0] + "\n" + lines[1] + "\nnot-json\n" + lines[2] + "\n"
	writeSessionFile(t, metadataPath, corrupted)

	_, err := createRepository(root).Open(&jsonl.JsonlSessionMetadata{
		SessionMetadata: session.SessionMetadata{ID: "session", CreatedAt: 1},
		Cwd:             root,
		Path:            metadataPath,
	})
	requireSessionError(t, err, session.CodeInvalidEntry)
	if readSessionFile(t, metadataPath) != corrupted {
		t.Fatal("corrupted file was modified")
	}
}

func TestRejectsAnImportedEntryThatReferencesAMissingParent(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "session-missing-parent.jsonl")
	header := fmt.Sprintf(`{"kind":"header","version":4,"id":"missing-parent","createdAt":1,"cwd":%q}`, root)
	entry := `{"kind":"entry","type":"custom","id":"orphan","customType":"note","parentId":"missing","seq":1,"timestamp":1}`
	writeSessionFile(t, path, header+"\n"+entry+"\n")
	metadata := rawMetadata(t, root, "missing-parent", path, 1)

	_, err := createRepository(root).Open(&metadata)
	requireSessionError(t, err, session.CodeInvalidEntry)
	var sessionErr *session.SessionError
	if !asSessionErr(err, &sessionErr) {
		t.Fatal("expected session error")
	}
	want := fmt.Sprintf("Invalid JSONL v4 session %s: line 2 Invalid session mutation: references missing parent missing", path)
	// The Go SessionError.Error() appends a " (cause: ...)" suffix the TS
	// message property does not carry; compare the message prefix.
	if !strings.HasPrefix(sessionErr.Error(), want) {
		t.Fatalf("message = %q, want prefix %q", sessionErr.Error(), want)
	}
}

func TestRejectsALaneBoundEntryThatDoesNotChainToTheLaneLeaf(t *testing.T) {
	root := t.TempDir()
	repository := createRepository(root)
	s := createWithID(t, repository, "session", root)
	metadataPath := metadataPathFor(t, repository, "session")
	if _, err := s.AppendCustomEntry("first", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendCustomEntry("second", nil); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(readSessionFile(t, metadataPath), "\n"), "\n")
	// Rewrite the second entry's parentId to null so it does not chain to
	// the lane leaf (pi: lines[2].parentId = null via JSON.parse).
	var second map[string]any
	if err := json.Unmarshal([]byte(lines[2]), &second); err != nil {
		t.Fatal(err)
	}
	second["parentId"] = nil
	rewritten, err := json.Marshal(second)
	if err != nil {
		t.Fatal(err)
	}
	lines[2] = string(rewritten)
	writeSessionFile(t, metadataPath, strings.Join(lines, "\n")+"\n")

	_, err = createRepository(root).Open(&jsonl.JsonlSessionMetadata{
		SessionMetadata: session.SessionMetadata{ID: "session", CreatedAt: 1},
		Cwd:             root,
		Path:            metadataPath,
	})
	requireSessionError(t, err, session.CodeInvalidEntry)
	if !strings.Contains(err.Error(), "does not chain to the lane leaf") {
		t.Fatalf("message must name the chain failure: %v", err)
	}
}

func TestDoesNotMoveALaneForAnImportedEntryWithoutLaneMetadata(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "session-import.jsonl")
	header := fmt.Sprintf(`{"kind":"header","version":4,"id":"import","createdAt":1,"cwd":%q}`, root)
	importedEntry := `{"kind":"entry","type":"custom","id":"imported","customType":"note","parentId":null,"seq":1,"timestamp":1}`
	writeSessionFile(t, path, header+"\n"+importedEntry+"\n")
	metadata := rawMetadata(t, root, "import", path, 1)

	imported, err := createRepository(root).Open(&metadata)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := imported.GetLeafID()
	if err != nil {
		t.Fatal(err)
	}
	if leaf != nil {
		t.Fatalf("leaf = %v, want nil", *leaf)
	}
	entries, err := imported.FindEntries(nil)
	if err != nil {
		t.Fatal(err)
	}
	assertJSONEqual(t, entryIDList(entries), []string{"imported"})

	appendSessionFile(t, path, `{"kind":"lane","seq":2,"lane":"main","leafId":"imported"}`+"\n")
	moved, err := createRepository(root).Open(&metadata)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err = moved.GetLeafID()
	if err != nil {
		t.Fatal(err)
	}
	if leaf == nil || *leaf != "imported" {
		t.Fatalf("leaf = %v, want imported", leaf)
	}
}

func TestRejectsReplayCorruptions(t *testing.T) {
	runIntent := `{"kind":"run","originalPrompt":[],"initialMessages":[]}`
	cases := []struct {
		name      string
		message   string
		mutations []string
	}{
		{
			name:      "a non-consecutive sequence",
			message:   "non-consecutive seq",
			mutations: []string{`{"kind":"entry","type":"custom","id":"entry","customType":"note","parentId":null,"seq":2,"timestamp":1}`},
		},
		{
			name:    "a duplicate entry/record id",
			message: "duplicate id",
			mutations: []string{
				`{"kind":"entry","type":"custom","id":"duplicate","customType":"note","parentId":null,"seq":1,"timestamp":1}`,
				fmt.Sprintf(`{"kind":"record","type":"operation_started","id":"duplicate","lane":"main","seq":2,"timestamp":2,"sourceLeafId":null,"intent":%s}`, runIntent),
			},
		},
		{
			name:      "an entry with a missing parent",
			message:   "missing parent",
			mutations: []string{`{"kind":"entry","type":"custom","id":"entry","customType":"note","parentId":"missing","seq":1,"timestamp":1}`},
		},
		{
			name:      "an entry referencing a missing lane",
			message:   "missing lane",
			mutations: []string{`{"kind":"entry","lane":"thread","type":"custom","id":"entry","customType":"note","parentId":null,"seq":1,"timestamp":1}`},
		},
		{
			name:    "a record referencing a missing lane",
			message: "missing lane",
			mutations: []string{
				fmt.Sprintf(`{"kind":"record","type":"operation_started","id":"run","lane":"thread","seq":1,"timestamp":1,"sourceLeafId":null,"intent":%s}`, runIntent),
			},
		},
		{
			name:      "a lane move referencing a missing entry",
			message:   "missing lane target",
			mutations: []string{`{"kind":"lane","lane":"thread","leafId":"missing","seq":1}`},
		},
		{
			name:      "a label referencing a missing entry",
			message:   "missing label target",
			mutations: []string{`{"kind":"fact","fact":"label","targetId":"missing","label":"checkpoint","seq":1}`},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			root := t.TempDir()
			id := sanitizeID(testCase.name)
			metadata := writeRawSession(t, root, id, testCase.mutations)
			_, err := createRepository(root).Open(&metadata)
			requireSessionError(t, err, session.CodeInvalidEntry)
			if !strings.Contains(err.Error(), testCase.message) {
				t.Fatalf("message %q must contain %q", err.Error(), testCase.message)
			}
		})
	}
}

func sanitizeID(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
}

func TestRejectsACompleteMalformedInteriorMutationWithoutModifyingTheFile(t *testing.T) {
	root := t.TempDir()
	metadata := writeRawSession(t, root, "malformed-interior", []string{
		`{"kind":"record","type":"operation_started","id":"run","lane":"main","seq":1,"timestamp":1,"sourceLeafId":null}`,
		`{"kind":"fact","fact":"name","name":"after","seq":2}`,
	})
	corrupted := readSessionFile(t, metadata.Path)

	_, err := createRepository(root).Open(&metadata)
	requireSessionError(t, err, session.CodeInvalidEntry)
	if readSessionFile(t, metadata.Path) != corrupted {
		t.Fatal("corrupted file was modified")
	}
}

func TestPreservesTheSessionWhenStagingTornTailRepairFails(t *testing.T) {
	root := t.TempDir()
	repository := createRepository(root)
	s := createWithID(t, repository, "repair-failure", root)
	metadataPath := metadataPathFor(t, repository, "repair-failure")
	if _, err := s.AppendCustomEntry("kept", nil); err != nil {
		t.Fatal(err)
	}
	appendSessionFile(t, metadataPath, `{"kind":"entry"`)
	original := readSessionFile(t, metadataPath)

	fs := newInjectedFS(root)
	// pi's mock: perform the real writeFile (truncating the staged file),
	// then report failure — the repair aborts mid-publication.
	fs.writeFileHook = func(tempPath string) error {
		if err := fs.inner.WriteFile(context.Background(), tempPath, []byte{}); err != nil {
			return err
		}
		return harness.NewFileError(harness.FileCodeUnknown, "repair interrupted after truncation", tempPath, nil)
	}
	failingRepository := repoWithFS(root, fs)

	_, err := failingRepository.Open(&jsonl.JsonlSessionMetadata{
		SessionMetadata: session.SessionMetadata{ID: "repair-failure", CreatedAt: 1},
		Cwd:             root,
		Path:            metadataPath,
	})
	requireSessionError(t, err, session.CodeStorage)
	if readSessionFile(t, metadataPath) != original {
		t.Fatal("original session was modified")
	}
	if _, err := os.Stat(metadataPath + ".tmp"); !os.IsNotExist(err) {
		t.Fatal("leftover .tmp file after failed repair")
	}
}

func TestPreservesTheSessionWhenTornTailRepairCannotBePublished(t *testing.T) {
	root := t.TempDir()
	repository := createRepository(root)
	s := createWithID(t, repository, "repair-rename-failure", root)
	metadataPath := metadataPathFor(t, repository, "repair-rename-failure")
	if _, err := s.AppendCustomEntry("kept", nil); err != nil {
		t.Fatal(err)
	}
	appendSessionFile(t, metadataPath, `{"kind":"entry"`)
	original := readSessionFile(t, metadataPath)

	fs := newInjectedFS(root)
	fs.failRenameFile = 1
	failingRepository := repoWithFS(root, fs)

	_, err := failingRepository.Open(&jsonl.JsonlSessionMetadata{
		SessionMetadata: session.SessionMetadata{ID: "repair-rename-failure", CreatedAt: 1},
		Cwd:             root,
		Path:            metadataPath,
	})
	requireSessionError(t, err, session.CodeStorage)
	if readSessionFile(t, metadataPath) != original {
		t.Fatal("original session was modified")
	}
	if _, err := os.Stat(metadataPath + ".tmp"); !os.IsNotExist(err) {
		t.Fatal("leftover .tmp file after failed repair")
	}
}

// resolveMetadata lists the repo and returns the full metadata for id.
func resolveMetadata(t *testing.T, repository *jsonl.JsonlSessionRepo, id string) (*jsonl.JsonlSessionMetadata, error) {
	t.Helper()
	listed, err := repository.List(nil)
	if err != nil {
		return nil, err
	}
	for i := range listed {
		if listed[i].ID == id {
			return &listed[i], nil
		}
	}
	return nil, session.NewSessionError(session.CodeNotFound, "Session not found: "+id)
}
