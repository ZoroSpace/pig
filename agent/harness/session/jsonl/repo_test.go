// pi: test/harness/jsonl-session-repo.test.ts — cwd-scoped repo lifecycle.
package jsonl_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ZoroSpace/pig/agent/harness"
	"github.com/ZoroSpace/pig/agent/harness/env"
	"github.com/ZoroSpace/pig/agent/harness/session"
	jsonl "github.com/ZoroSpace/pig/agent/harness/session/jsonl"
	pigtesting "github.com/ZoroSpace/pig/agent/harness/session/testing"
)

const repoNow = int64(1_700_000_000_000)

func repoOptions(t *testing.T) (harness.FileSystem, *jsonl.JsonlSessionRepo) {
	t.Helper()
	root := t.TempDir()
	fileSystem := env.NewNodeExecutionEnv(env.NodeExecutionEnvOptions{Cwd: root})
	repo := jsonl.NewJsonlSessionRepo(jsonl.JsonlSessionRepoOptions{
		FileSystem:   fileSystem,
		SessionsRoot: "sessions",
		Now:          func() int64 { return repoNow },
	})
	return fileSystem, repo
}

func createSession(t *testing.T, repo *jsonl.JsonlSessionRepo, id, cwd, parentSessionID string) session.Session {
	t.Helper()
	options := jsonl.JsonlSessionCreateOptions{
		SessionCreateOptions: session.SessionCreateOptions{ID: strPtr(id)},
		Cwd:                  cwd,
	}
	if parentSessionID != "" {
		options.ParentSessionID = strPtr(parentSessionID)
	}
	sess, err := repo.Create(options, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return sess
}

// jsonlMetadata reads the concrete metadata off a session handle.
func jsonlMetadata(sess session.Session) jsonl.JsonlSessionMetadata {
	typed, ok := sess.(*session.StorageBackedSession).TypedMetadata().(jsonl.JsonlSessionMetadata)
	if !ok {
		panic("session metadata is not JsonlSessionMetadata")
	}
	return typed
}

func TestPersistsMetadataAndFiltersDiscoveryByCwd(t *testing.T) {
	ctx := context.Background()
	fileSystem, repo := repoOptions(t)
	sess := createSession(t, repo, "child", "/workspace", "parent")
	metadata := jsonlMetadata(sess)

	if metadata.ID != "child" || metadata.CreatedAt != repoNow || metadata.StorageVersion != jsonl.JSONLStorageVersion ||
		metadata.Cwd != "/workspace" || metadata.ParentSessionID == nil || *metadata.ParentSessionID != "parent" {
		t.Fatalf("metadata = %+v", metadata)
	}
	if !strings.Contains(metadata.Path, "/sessions/--workspace--/") {
		t.Fatalf("path = %s", metadata.Path)
	}
	if !strings.HasSuffix(metadata.Path, "_child.jsonl") {
		t.Fatalf("path = %s", metadata.Path)
	}
	if metadata.ModifiedAt <= 0 {
		t.Fatalf("modifiedAt = %d", metadata.ModifiedAt)
	}
	if err := sess.Close(ctx); err != nil {
		t.Fatal(err)
	}

	other := &jsonl.JsonlSessionListOptions{Cwd: strPtr("/other")}
	listed, err := repo.List(other, ctx)
	if err != nil || len(listed) != 0 {
		t.Fatalf("other list = %v, %v", listed, err)
	}
	workspace := &jsonl.JsonlSessionListOptions{Cwd: strPtr("/workspace")}
	listed, err = repo.List(workspace, ctx)
	if err != nil || len(listed) != 1 || listed[0].ID != "child" {
		t.Fatalf("workspace list = %v, %v", listed, err)
	}
	lines, err := fileSystem.ReadTextLines(ctx, metadata.Path, intPtr(1))
	if err != nil {
		t.Fatal(err)
	}
	var header jsonl.JsonlStorageHeader
	if err := json.Unmarshal([]byte(lines[0]), &header); err != nil {
		t.Fatal(err)
	}
	assertHeaderEqual(t, header, jsonl.JsonlStorageHeader{
		V: jsonl.JSONLFormatVersion, Kind: "header", ID: "child",
		StorageVersion: jsonl.JSONLStorageVersion, CreatedAt: repoNow, Cwd: "/workspace",
		ParentSessionID: strPtr("parent"),
	})
	_ = repo.Close(ctx)
}

func assertHeaderEqual(t *testing.T, got, want jsonl.JsonlStorageHeader) {
	t.Helper()
	if got.V != want.V || got.Kind != want.Kind || got.ID != want.ID ||
		got.StorageVersion != want.StorageVersion || got.CreatedAt != want.CreatedAt ||
		got.Cwd != want.Cwd || (got.ParentSessionID == nil) != (want.ParentSessionID == nil) {
		t.Fatalf("header = %+v, want %+v", got, want)
	}
	if got.ParentSessionID != nil && *got.ParentSessionID != *want.ParentSessionID {
		t.Fatalf("parentSessionId = %v", *got.ParentSessionID)
	}
}

// atomicPublicationEnv records the staged rename (pi:
// AtomicPublicationNodeExecutionEnv).
type atomicPublicationEnv struct {
	harness.FileSystem
	publication *stagedPublication
}

type stagedPublication struct {
	sourcePath      string
	destinationPath string
	stagedContent   string
}

func (e *atomicPublicationEnv) RenameFile(ctx context.Context, sourcePath, destinationPath string) error {
	staged, readErr := e.FileSystem.ReadTextFile(ctx, sourcePath)
	_, existsErr := e.FileSystem.Exists(ctx, destinationPath)
	if readErr == nil && existsErr == nil {
		e.publication = &stagedPublication{
			sourcePath: sourcePath, destinationPath: destinationPath,
			stagedContent: staged,
		}
	}
	return e.FileSystem.RenameFile(ctx, sourcePath, destinationPath)
}

func TestAtomicallyPublishesABranchlessSessionHeader(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	inner := env.NewNodeExecutionEnv(env.NodeExecutionEnvOptions{Cwd: root})
	fileSystem := &atomicPublicationEnv{FileSystem: inner}
	repo := jsonl.NewJsonlSessionRepo(jsonl.JsonlSessionRepoOptions{
		FileSystem: fileSystem, SessionsRoot: "sessions", Now: func() int64 { return repoNow },
	})
	sess := createSession(t, repo, "session", "/workspace", "")

	publication := fileSystem.publication
	if publication == nil {
		t.Fatal("expected atomic session publication")
	}
	metadata := jsonlMetadata(sess)
	if publication.destinationPath != metadata.Path {
		t.Fatalf("destination = %s, want %s", publication.destinationPath, metadata.Path)
	}
	stagedContent := publication.stagedContent
	lines := strings.Split(strings.TrimRight(stagedContent, "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("staged lines = %d", len(lines))
	}
	var header jsonl.JsonlStorageHeader
	if err := json.Unmarshal([]byte(lines[0]), &header); err != nil {
		t.Fatal(err)
	}
	if header.Kind != "header" || header.ID != "session" {
		t.Fatalf("header = %+v", header)
	}
	if got := readTextFile(t, inner, metadata.Path); got != stagedContent {
		t.Fatal("published content must equal staged content")
	}
	if fileExists(t, inner, publication.sourcePath) {
		t.Fatal("staged file must be renamed away")
	}
	_ = sess.Close(ctx)
	_ = repo.Close(ctx)
}

func TestKeepsAnExplicitSessionMutationThroughCommitUntilEnd(t *testing.T) {
	ctx := context.Background()
	_, repo := repoOptions(t)
	sess := createSession(t, repo, "session", "/workspace", "")
	mutation, err := sess.BeginMutation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	queuedDone := make(chan struct{})
	go func() {
		_, _ = sess.Mutate(func(session.SessionMutator, context.Context) (any, error) {
			t.Log("queued mutation must not start")
			close(queuedDone)
			return nil, nil
		}, ctx)
	}()

	result, err := mutation.Commit([]session.Write{session.SetValue(session.SessionNameAddress, "explicit")}, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Seqs) != 1 {
		t.Fatalf("seqs = %v", result.Seqs)
	}
	select {
	case <-queuedDone:
		t.Fatal("queued mutation started before end")
	default:
	}
	stored, err := mutation.GetValue(session.SessionNameAddress, ctx)
	if err != nil || stored == nil || stored.Value != "explicit" {
		t.Fatalf("value = %v, %v", stored, err)
	}
	if err := mutation.End(ctx); err != nil {
		t.Fatal(err)
	}
	<-queuedDone

	if err := sess.Close(ctx); err != nil {
		t.Fatal(err)
	}
	reopened, err := repo.Open(jsonlMetadata(sess), ctx)
	if err != nil {
		t.Fatal(err)
	}
	name, err := reopened.GetName(ctx)
	if err != nil || name == nil || *name != "explicit" {
		t.Fatalf("name = %v, %v", name, err)
	}
	_ = reopened.Close(ctx)
	_ = repo.Close(ctx)
}

func TestRejectsUnsupportedStorageVersionsWithoutRepairingATornTail(t *testing.T) {
	ctx := context.Background()
	fileSystem, repo := repoOptions(t)
	sess := createSession(t, repo, "future", "/workspace", "")
	metadata := jsonlMetadata(sess)
	if err := sess.Close(ctx); err != nil {
		t.Fatal(err)
	}

	content := readTextFile(t, fileSystem, metadata.Path)
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	var header map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &header); err != nil {
		t.Fatal(err)
	}
	unsupportedVersion := jsonl.JSONLStorageVersion + 1
	header["storageVersion"] = unsupportedVersion
	updated, err := json.Marshal(header)
	if err != nil {
		t.Fatal(err)
	}
	unsupportedContent := string(updated) + "\n" + `{"kind":"entry"`
	if err := fileSystem.WriteFile(ctx, metadata.Path, []byte(unsupportedContent)); err != nil {
		t.Fatal(err)
	}

	if _, err := repo.Open(metadata, ctx); err == nil || !strings.Contains(err.Error(), "unsupported storage version") {
		t.Fatalf("err = %v", err)
	}
	if got := readTextFile(t, fileSystem, metadata.Path); got != unsupportedContent {
		t.Fatal("torn file must not be repaired")
	}
	_ = repo.Close(ctx)
}

func TestKeepsForkDestinationsClaimedUntilCloseAndRejectsDeletingOpenSessions(t *testing.T) {
	ctx := context.Background()
	_, repo := repoOptions(t)
	sess := createSession(t, repo, "source", "/workspace", "")
	forkID := "fork"
	fork, err := repo.Fork(jsonlMetadata(sess), session.ForkOptions{ID: &forkID, Scope: session.ForkScopeTree}, ctx)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := repo.Open(jsonlMetadata(fork), ctx); err == nil || !strings.Contains(err.Error(), "already open") {
		t.Fatalf("err = %v", err)
	}
	if err := repo.Delete(jsonlMetadata(fork), ctx); err == nil || !strings.Contains(err.Error(), "open") {
		t.Fatalf("err = %v", err)
	}
	if err := fork.Close(ctx); err != nil {
		t.Fatal(err)
	}

	reopened, err := repo.Open(jsonlMetadata(fork), ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := reopened.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := repo.Delete(jsonlMetadata(fork), ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Open(jsonlMetadata(fork), ctx); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("err = %v", err)
	}
	_ = sess.Close(ctx)
	_ = repo.Close(ctx)
}

func TestRejectsConcurrentCreatesForTheSameWorkingDirectoryID(t *testing.T) {
	ctx := context.Background()
	_, repo := repoOptions(t)
	type outcome struct {
		sess session.Session
		err  error
	}
	firstCh := make(chan outcome, 1)
	secondCh := make(chan outcome, 1)
	options := jsonl.JsonlSessionCreateOptions{SessionCreateOptions: session.SessionCreateOptions{ID: strPtr("session")}, Cwd: "/workspace"}
	go func() {
		sess, err := repo.Create(options, ctx)
		firstCh <- outcome{sess, err}
	}()
	go func() {
		sess, err := repo.Create(options, ctx)
		secondCh <- outcome{sess, err}
	}()
	var successes, failures int
	for _, ch := range []chan outcome{firstCh, secondCh} {
		out := <-ch
		if out.err != nil {
			failures++
		} else {
			successes++
			_ = out.sess.Close(ctx)
		}
	}
	if successes != 1 || failures != 1 {
		t.Fatalf("successes = %d, failures = %d", successes, failures)
	}
	listed, err := repo.List(&jsonl.JsonlSessionListOptions{Cwd: strPtr("/workspace")}, ctx)
	if err != nil || len(listed) != 1 {
		t.Fatalf("list = %v, %v", listed, err)
	}
	_ = repo.Close(ctx)
}

func TestAllowsTheSameIDToBeActiveInDifferentWorkingDirectories(t *testing.T) {
	ctx := context.Background()
	_, repo := repoOptions(t)
	first := createSession(t, repo, "shared", "/workspace-a", "")
	second := createSession(t, repo, "shared", "/workspace-b", "")

	firstMetadata := jsonlMetadata(first)
	secondMetadata := jsonlMetadata(second)
	if firstMetadata.Path == secondMetadata.Path {
		t.Fatal("paths must differ")
	}
	dupOptions := jsonl.JsonlSessionCreateOptions{SessionCreateOptions: session.SessionCreateOptions{ID: strPtr("shared")}, Cwd: "/workspace-a"}
	if _, err := repo.Create(dupOptions, ctx); err == nil {
		t.Fatal("duplicate id must reject")
	}
	listed, err := repo.List(nil, ctx)
	if err != nil {
		t.Fatal(err)
	}
	type cwdID struct{ cwd, id string }
	var pairs []cwdID
	for _, metadata := range listed {
		pairs = append(pairs, cwdID{metadata.Cwd, metadata.ID})
	}
	pigtesting.AssertJSONEqual(t, "listed", pairs, []cwdID{{"/workspace-a", "shared"}, {"/workspace-b", "shared"}})

	_ = first.Close(ctx)
	_ = second.Close(ctx)
	reopenedFirst, err := repo.Open(jsonlMetadata(first), ctx)
	if err != nil {
		t.Fatal(err)
	}
	reopenedSecond, err := repo.Open(jsonlMetadata(second), ctx)
	if err != nil {
		t.Fatal(err)
	}
	_ = reopenedFirst.Close(ctx)
	_ = reopenedSecond.Close(ctx)
	_ = repo.Close(ctx)
}

func intPtr(v int) *int { return &v }
