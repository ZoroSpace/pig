// pi: test/harness/jsonl-storage-conformance.test.ts +
// jsonl-session-repo-conformance.test.ts — run the shared conformance case
// suites against the JSONL backend.
package jsonl_test

import (
	"context"
	"testing"

	"github.com/ZoroSpace/pig/agent/harness/env"
	"github.com/ZoroSpace/pig/agent/harness/session"
	jsonl "github.com/ZoroSpace/pig/agent/harness/session/jsonl"
	pigtesting "github.com/ZoroSpace/pig/agent/harness/session/testing"
)

func jsonlStorageFixture(t *testing.T) pigtesting.StorageFixture {
	t.Helper()
	root := t.TempDir()
	fileSystem := env.NewNodeExecutionEnv(env.NodeExecutionEnvOptions{Cwd: root})
	storage, err := jsonl.CreateJsonlStorage(
		&jsonl.JsonlStorageOptions{FileSystem: fileSystem, Path: "storage.jsonl", Now: func() int64 { return 10 }},
		jsonl.JsonlStorageHeader{
			V: jsonl.JSONLFormatVersion, Kind: "header", ID: "storage",
			StorageVersion: jsonl.JSONLStorageVersion, CreatedAt: 10, Cwd: "/workspace",
		},
		nil, context.Background(),
	)
	if err != nil {
		t.Fatal(err)
	}
	return pigtesting.StorageFixture{Storage: storage, Close: func() { _ = storage.Close(context.Background()) }}
}

func TestJsonlStorageConformance(t *testing.T) {
	pigtesting.RunConformanceCases(t, pigtesting.StorageConformanceCases(jsonlStorageFixture))
}

// jsonlRepoAdapter widens the typed surface to the conformance interface
// (pi's factory injects cwd and re-wraps metadata; pi:
// createConformanceRepo).
// jsonlRepoAdapter adapts JsonlSessionRepo to the conformance interface
// (pi: createConformanceRepo injects cwd into create options and filters
// list by cwd).
type jsonlRepoAdapter struct {
	repo *jsonl.JsonlSessionRepo
	cwd  string
}

func (a *jsonlRepoAdapter) Metadata(sess session.Session) jsonl.JsonlSessionMetadata {
	typed, ok := sess.(*session.StorageBackedSession).TypedMetadata().(jsonl.JsonlSessionMetadata)
	if !ok {
		panic("session metadata is not JsonlSessionMetadata")
	}
	return typed
}

func (a *jsonlRepoAdapter) MetadataID(metadata jsonl.JsonlSessionMetadata) string { return metadata.ID }

func (a *jsonlRepoAdapter) MetadataParentSessionID(metadata jsonl.JsonlSessionMetadata) *string {
	return metadata.ParentSessionID
}

func (a *jsonlRepoAdapter) Create(id *string, parentSessionID *string, ctx context.Context) (session.Session, error) {
	return a.repo.Create(jsonl.JsonlSessionCreateOptions{
		SessionCreateOptions: session.SessionCreateOptions{ID: id, ParentSessionID: parentSessionID},
		Cwd:                  a.cwd,
	}, ctx)
}

func (a *jsonlRepoAdapter) Open(metadata jsonl.JsonlSessionMetadata, ctx context.Context) (session.Session, error) {
	return a.repo.Open(metadata, ctx)
}

func (a *jsonlRepoAdapter) List(ctx context.Context) ([]jsonl.JsonlSessionMetadata, error) {
	return a.repo.List(&jsonl.JsonlSessionListOptions{Cwd: &a.cwd}, ctx)
}

func (a *jsonlRepoAdapter) Delete(metadata jsonl.JsonlSessionMetadata, ctx context.Context) error {
	return a.repo.Delete(metadata, ctx)
}

func (a *jsonlRepoAdapter) Fork(source jsonl.JsonlSessionMetadata, options session.ForkOptions, ctx context.Context) (session.Session, error) {
	return a.repo.Fork(source, options, ctx)
}

func (a *jsonlRepoAdapter) Close(ctx context.Context) error {
	return a.repo.Close(ctx)
}

func jsonlRepoFixture(t *testing.T) pigtesting.ConformanceRepo[jsonl.JsonlSessionMetadata] {
	t.Helper()
	root := t.TempDir()
	fileSystem := env.NewNodeExecutionEnv(env.NodeExecutionEnvOptions{Cwd: root})
	repo := jsonl.NewJsonlSessionRepo(jsonl.JsonlSessionRepoOptions{
		FileSystem: fileSystem, SessionsRoot: "sessions", Now: func() int64 { return 10 },
	})
	t.Cleanup(func() { _ = repo.Close(context.Background()) })
	return &jsonlRepoAdapter{repo: repo, cwd: "/workspace"}
}

func TestJsonlSessionRepoConformance(t *testing.T) {
	pigtesting.RunConformanceCases(t, pigtesting.SessionRepoConformanceCases(jsonlRepoFixture))
}
