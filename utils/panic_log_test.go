// utils/panic_log_test.go — regression tests for pig panic containment
// (pig-internal feature; see docs/feat/PANIC-RESILIENCE.md). Each test
// asserts behavior that fails loudly without the containment (the test
// process would die on the uncontained panic, or the mutex would
// deadlock in the tui lock test).
package utils

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// isolateLogDir points PIG_CODING_AGENT_DIR at a fresh temp dir so tests
// never touch the real ~/.pig.
func isolateLogDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("PIG_CODING_AGENT_DIR", dir)
	return dir
}

func TestLogPanicWritesReport(t *testing.T) {
	dir := isolateLogDir(t)
	path := LogPanic("test.component", fmt.Errorf("boom-value"))

	if path == "" {
		t.Fatal("LogPanic returned an empty path")
	}
	if want := filepath.Join(dir, "logs"); filepath.Dir(path) != want {
		t.Fatalf("report written to %q, want dir %q", path, want)
	}
	if base := filepath.Base(path); !strings.HasPrefix(base, "panic-") || !strings.HasSuffix(base, ".log") {
		t.Fatalf("report name %q does not match panic-*.log", base)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	for _, needle := range []string{
		"pig panic report",
		"component: test.component",
		"boom-value",          // panic value
		"*errors.errorString", // panic type
		"panicking goroutine stack",
		"all goroutines",
	} {
		if !strings.Contains(string(content), needle) {
			t.Fatalf("report missing %q:\n%s", needle, content)
		}
	}
}

func TestLogPanicPrunesOldReports(t *testing.T) {
	dir := isolateLogDir(t)
	logs := filepath.Join(dir, "logs")
	if err := os.MkdirAll(logs, 0o700); err != nil {
		t.Fatal(err)
	}
	base := time.Now()
	// 25 stale reports with descending mod times; oldest must be pruned.
	for i := 0; i < 25; i++ {
		name := filepath.Join(logs, fmt.Sprintf("panic-stale-%02d.log", i))
		if err := os.WriteFile(name, []byte("stale"), 0o600); err != nil {
			t.Fatal(err)
		}
		stamp := base.Add(-time.Duration(25-i) * time.Hour)
		if err := os.Chtimes(name, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	path := LogPanic("test.prune", "x")
	if path == "" {
		t.Fatal("LogPanic returned an empty path")
	}
	entries, err := os.ReadDir(logs)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 20 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("got %d reports after prune, want 20: %v", len(entries), names)
	}
	// The new report survives; the oldest stale one is gone.
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("new report missing after prune: %v", err)
	}
	if _, err := os.Stat(filepath.Join(logs, "panic-stale-00.log")); !os.IsNotExist(err) {
		t.Fatal("oldest stale report was not pruned")
	}
}

func TestGuardContainsPanic(t *testing.T) {
	isolateLogDir(t)
	path, panicked := Guard("test.guard", func() { panic("kaboom") })
	if !panicked {
		t.Fatal("Guard reported panicked=false for a panicking fn")
	}
	if path == "" {
		t.Fatal("Guard returned an empty log path for a panicking fn")
	}
	if path2, panicked2 := Guard("test.guard", func() {}); panicked2 || path2 != "" {
		t.Fatalf("Guard reported panic for a clean fn (panicked=%v path=%q)", panicked2, path2)
	}
}

func TestRecoverPanicSwallows(t *testing.T) {
	isolateLogDir(t)
	// Without RecoverPanic this function dies and takes the test process
	// with it — the test passing is the assertion.
	func() {
		defer RecoverPanic("test.recover")
		panic("contained")
	}()
}

func TestPanicLogDirectoryFallback(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PIG_CODING_AGENT_DIR", "")
	t.Setenv("HOME", home)
	want := filepath.Join(home, ".pig", "agent", "logs")
	if got := PanicLogDirectory(); got != want {
		t.Fatalf("PanicLogDirectory() = %q, want %q", got, want)
	}
	// Explicit env override wins.
	override := filepath.Join(home, "custom")
	t.Setenv("PIG_CODING_AGENT_DIR", override)
	if got := PanicLogDirectory(); got != filepath.Join(override, "logs") {
		t.Fatalf("PanicLogDirectory() = %q, want %q", got, filepath.Join(override, "logs"))
	}
}
