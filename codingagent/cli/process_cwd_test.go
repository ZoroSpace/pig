// pi: process.cwd() semantics lock (S10-4 bidirectional-compat finding).
// The helper itself is pig-only; the regression is that ProcessCwd must
// resolve symlinks so the session directory name matches pi's on a
// symlinked cwd (macOS /tmp → /private/tmp), or `--continue` never finds
// the other CLI's sessions.
package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestProcessCwdResolvesSymlinks(t *testing.T) {
	raw, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	got, err := ProcessCwd()
	if err != nil {
		t.Fatal(err)
	}

	// In a symlink-free working directory the raw and resolved spellings
	// agree; the helper must never wander away from the real working dir.
	if want := filepath.Clean(raw); got != want {
		t.Fatalf("ProcessCwd() = %q, want %q", got, want)
	}

	if runtime.GOOS == "windows" {
		return // windows keeps the raw value (declared in process_cwd.go)
	}

	// Symlinked directory: the resolved value must equal the target's real
	// path, which os.Getwd's $PWD-preserving behavior would not produce.
	target := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(link); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(raw) })

	// t.TempDir() itself may be under a symlinked prefix on macOS
	// (/var → /private/var); the expectation is its real path.
	want, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := ProcessCwd()
	if err != nil {
		t.Fatal(err)
	}
	if resolved != want {
		t.Fatalf("ProcessCwd() through symlink = %q, want %q", resolved, want)
	}
}
