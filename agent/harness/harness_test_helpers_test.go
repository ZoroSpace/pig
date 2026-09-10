// Shared test scaffolding for the harness loaders (pi's tests construct the
// full NodeExecutionEnv; see per-file notes).
package harness_test

import (
	"context"
	"testing"

	"github.com/ZoroSpace/pig/agent/harness"
	"github.com/ZoroSpace/pig/agent/harness/env"
)

// testEnv adapts the FileSystem-only NodeExecutionEnv to the full
// ExecutionEnv: the exec half of env/nodejs.ts is deferred to the tools
// batch, and the loaders never touch Shell members (pi's tests use the
// complete NodeExecutionEnv). Cleanup forwards to the FileSystem contract.
type testEnv struct {
	*env.NodeExecutionEnv
}

func (e testEnv) Exec(ctx context.Context, command string, options *harness.ShellExecOptions) (*harness.ShellExecResult, error) {
	panic("testEnv.Exec is not expected to be called")
}

func (e testEnv) Cleanup() error { return e.NodeExecutionEnv.Cleanup() }

func newTestEnv(root string) testEnv {
	return testEnv{NodeExecutionEnv: env.NewNodeExecutionEnv(env.NodeExecutionEnvOptions{Cwd: root})}
}

type testSource struct {
	Type string
}

func mustWriteFile(t *testing.T, e testEnv, path, content string) {
	t.Helper()
	if err := e.WriteFile(context.Background(), path, []byte(content)); err != nil {
		t.Fatal(err)
	}
}

func mustCreateDir(t *testing.T, e testEnv, path string) {
	t.Helper()
	if err := e.CreateDir(context.Background(), path, nil); err != nil {
		t.Fatal(err)
	}
}

// skillEqual compares skills with pi's deep-equality semantics (pointer
// fields compare by value).
func skillEqual(a, b harness.Skill) bool {
	if a.Name != b.Name || a.Description != b.Description || a.Content != b.Content || a.FilePath != b.FilePath {
		return false
	}
	if (a.DisableModelInvocation == nil) != (b.DisableModelInvocation == nil) {
		return false
	}
	return a.DisableModelInvocation == nil || *a.DisableModelInvocation == *b.DisableModelInvocation
}

// promptTemplateEqual compares templates with pi's deep-equality semantics.
func promptTemplateEqual(a, b harness.PromptTemplate) bool {
	if a.Name != b.Name || a.Content != b.Content {
		return false
	}
	if (a.Description == nil) != (b.Description == nil) {
		return false
	}
	return a.Description == nil || *a.Description == *b.Description
}
