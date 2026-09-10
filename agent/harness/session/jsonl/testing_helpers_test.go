// Shared test helpers for the jsonl backend tests (pi: per-file helpers in
// jsonl.test.ts / jsonl-storage.test.ts).
//
// Deviations:
//   - injectedFS replaces vi.spyOn one-shot mocks with counted failures and
//     an optional writeFile hook (pi's repair-interruption mock).
package jsonl_test

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/ZoroSpace/pig/agent/harness"
	"github.com/ZoroSpace/pig/agent/harness/env"
	"github.com/ZoroSpace/pig/agent/harness/session"
	jsonl "github.com/ZoroSpace/pig/agent/harness/session/jsonl"
)

// createRepository builds a repo over an os-backed environment rooted at
// root (pi: createRepository).
func createRepository(root string) *jsonl.JsonlSessionRepo {
	return jsonl.NewJsonlSessionRepo(jsonl.JsonlSessionRepoOptions{
		Fs:           env.NewNodeExecutionEnv(env.NodeExecutionEnvOptions{Cwd: root}),
		SessionsRoot: root,
	})
}

// readSessionFile reads a session file's text.
func readSessionFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

// writeSessionFile overwrites a session file's text (pi: writeFileSync).
func writeSessionFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// appendSessionFile appends to a session file (pi: appendFileSync).
func appendSessionFile(t *testing.T, path string, content string) {
	t.Helper()
	current := readSessionFile(t, path)
	writeSessionFile(t, path, current+content)
}

// sessionLineKinds parses each line and returns its "kind" value.
func sessionLineKinds(t *testing.T, content string) []string {
	t.Helper()
	var kinds []string
	for _, line := range strings.Split(strings.TrimRight(content, "\n"), "\n") {
		var parsed struct {
			Kind string `json:"kind"`
		}
		if err := json.Unmarshal([]byte(line), &parsed); err != nil {
			t.Fatal(err)
		}
		kinds = append(kinds, parsed.Kind)
	}
	return kinds
}

// sessionLineSeqs parses each line after the header and returns seq values.
func sessionLineSeqs(t *testing.T, content string) []int64 {
	t.Helper()
	var seqs []int64
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	for _, line := range lines[1:] {
		var parsed struct {
			Seq int64 `json:"seq"`
		}
		if err := json.Unmarshal([]byte(line), &parsed); err != nil {
			t.Fatal(err)
		}
		seqs = append(seqs, parsed.Seq)
	}
	return seqs
}

// assertJSONEqual compares two values through canonical JSON bytes
// (conformance-style deep equality).
func assertJSONEqual(t *testing.T, actual, expected any) {
	t.Helper()
	actualJSON, err := json.Marshal(actual)
	if err != nil {
		t.Fatal(err)
	}
	expectedJSON, err := json.Marshal(expected)
	if err != nil {
		t.Fatal(err)
	}
	if string(actualJSON) != string(expectedJSON) {
		t.Fatalf("JSON mismatch:\n actual: %s\nexpected: %s", actualJSON, expectedJSON)
	}
}

// injectedFS delegates to an os-backed environment and fails selected
// methods a counted number of times (pi: vi.spyOn(env, ...).mock...).
type injectedFS struct {
	jsonl.JsonlSessionRepoFileSystem
	inner *env.NodeExecutionEnv

	failAppendFile int
	// passThroughAppendFile lets this many AppendFile calls through before
	// failAppendFile applies (pi's mockImplementationOnce passthrough).
	passThroughAppendFile int
	failWriteFile         int
	failRenameFile        int
	// writeFileHook, when set, runs after the real WriteFile for the next
	// call and its error replaces the result (pi's repair-interruption
	// mock: truncate the staged file, then fail).
	writeFileHook func(tempPath string) error
}

func newInjectedFS(root string) *injectedFS {
	inner := env.NewNodeExecutionEnv(env.NodeExecutionEnvOptions{Cwd: root})
	return &injectedFS{JsonlSessionRepoFileSystem: inner, inner: inner}
}

func (f *injectedFS) popFailure(count *int) bool {
	if *count > 0 {
		*count--
		return true
	}
	return false
}

func (f *injectedFS) WriteFile(ctx context.Context, path string, content []byte) error {
	// A counted failure replaces the whole call (pi: mockResolvedValueOnce —
	// the file is NOT written). When a hook is set it runs after the real
	// write and its error replaces the result (pi's repair-interruption
	// mock).
	if f.writeFileHook != nil {
		hook := f.writeFileHook
		f.writeFileHook = nil
		if err := f.inner.WriteFile(ctx, path, content); err != nil {
			return err
		}
		return hook(path)
	}
	if f.popFailure(&f.failWriteFile) {
		return harness.NewFileError(harness.FileCodeUnknown, "injected creation failure", path, nil)
	}
	return f.inner.WriteFile(ctx, path, content)
}

func (f *injectedFS) AppendFile(ctx context.Context, path string, content []byte) error {
	if f.passThroughAppendFile > 0 {
		f.passThroughAppendFile--
		return f.inner.AppendFile(ctx, path, content)
	}
	if f.popFailure(&f.failAppendFile) {
		return harness.NewFileError(harness.FileCodeUnknown, "injected append failure", path, nil)
	}
	return f.inner.AppendFile(ctx, path, content)
}

func (f *injectedFS) RenameFile(ctx context.Context, sourcePath string, destinationPath string) error {
	if f.popFailure(&f.failRenameFile) {
		return harness.NewFileError(harness.FileCodeUnknown, "injected rename failure", "", nil)
	}
	return f.inner.RenameFile(ctx, sourcePath, destinationPath)
}

// repoWithFS builds a repo over an injected fs rooted at root.
func repoWithFS(root string, fs *injectedFS) *jsonl.JsonlSessionRepo {
	return jsonl.NewJsonlSessionRepo(jsonl.JsonlSessionRepoOptions{Fs: fs, SessionsRoot: root})
}

// reopen opens a fresh Session by listing the repo and matching the id
// (pi: reopen(root, session)).
func reopen(t *testing.T, root string, s *session.Session) *session.Session {
	t.Helper()
	base, err := s.GetMetadata()
	if err != nil {
		t.Fatal(err)
	}
	repository := createRepository(root)
	return openByID(t, repository, base.ID)
}

// openByID lists the repo and opens the session whose metadata matches id.
func openByID(t *testing.T, repository *jsonl.JsonlSessionRepo, id string) *session.Session {
	t.Helper()
	listed, err := repository.List(nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := range listed {
		if listed[i].ID == id {
			opened, err := repository.Open(&listed[i])
			if err != nil {
				t.Fatal(err)
			}
			return opened
		}
	}
	t.Fatalf("session %s not found in listing", id)
	return nil
}

// requireSessionError asserts a *session.SessionError with the given code.
func requireSessionError(t *testing.T, err error, code session.SessionErrorCode) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected session error %s, got success", code)
	}
	var sessionErr *session.SessionError
	if !asSessionErr(err, &sessionErr) {
		t.Fatalf("expected *session.SessionError, got %T: %v", err, err)
	}
	if sessionErr.Code != code {
		t.Fatalf("session error code = %s, want %s", sessionErr.Code, code)
	}
}

func asSessionErr(err error, target **session.SessionError) bool {
	for err != nil {
		if e, ok := err.(*session.SessionError); ok {
			*target = e
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}
