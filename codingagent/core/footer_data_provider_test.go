// footer_data_provider_test.go (pi: test/footer-data-provider.test.ts).
//
// pi mocks child_process (spawnSync/execFile) via vi.mock; pig stubs the
// footerResolveBranchSync/footerResolveBranchAsync function vars. The
// watcher-retry test drives real timers (pi: fake timers) since the poll
// watchers are goroutine-based.
package core

import (
	"os"
	"path/filepath"

	"sync/atomic"
	"testing"
	"time"
)

type footerTestRepo struct {
	tempDir string
}

func footerCreatePlainReftableRepo(t *testing.T, tempDir string) string {
	t.Helper()
	repoDir := filepath.Join(tempDir, "repo")
	if err := os.MkdirAll(filepath.Join(repoDir, ".git", "reftable"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, ".git", "HEAD"), []byte("ref: refs/heads/.invalid\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return repoDir
}

func footerCreatePlainRepo(t *testing.T, tempDir string) string {
	t.Helper()
	repoDir := filepath.Join(tempDir, "repo")
	if err := os.MkdirAll(filepath.Join(repoDir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return repoDir
}

func footerCreateReftableWorktree(t *testing.T, tempDir string) (worktreeDir string, reftableDir string) {
	t.Helper()
	repoDir := filepath.Join(tempDir, "repo")
	commonGitDir := filepath.Join(repoDir, ".git")
	gitDir := filepath.Join(commonGitDir, "worktrees", "src")
	worktreeDir = filepath.Join(tempDir, "worktree")
	reftableDir = filepath.Join(commonGitDir, "reftable")

	for _, dir := range []string{gitDir, reftableDir, worktreeDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(worktreeDir, ".git"), []byte("gitdir: "+gitDir+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte("ref: refs/heads/.invalid\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "commondir"), []byte("../..\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(reftableDir, "tables.list"), []byte("0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return worktreeDir, reftableDir
}

func footerWaitFor(t *testing.T, timeoutMs time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeoutMs)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("Timed out waiting for condition")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// footerBranchStub mirrors pi's `let resolvedBranch` mock state.
type footerBranchStub struct {
	syncCalls   atomic.Int64
	asyncCalls  atomic.Int64
	branch      atomic.Value // string
	deferBranch *string
}

func newFooterBranchStub(initial string) *footerBranchStub {
	s := &footerBranchStub{}
	s.branch.Store(initial)
	return s
}

func TestFooterDataProviderReftableBranchDetection(t *testing.T) {
	each := func(t *testing.T, stub *footerBranchStub, run func(t *testing.T, tempDir string)) {
		t.Helper()
		tempDir := t.TempDir()
		previousSync := footerResolveBranchSync
		previousAsync := footerResolveBranchAsync
		footerResolveBranchSync = func(repoDir string) *string {
			stub.syncCalls.Add(1)
			branch, _ := stub.branch.Load().(string)
			if branch == "" {
				return nil
			}
			return &branch
		}
		footerResolveBranchAsync = func(repoDir string) *string {
			stub.asyncCalls.Add(1)
			branch, _ := stub.branch.Load().(string)
			if branch == "" {
				return nil
			}
			return &branch
		}
		defer func() {
			footerResolveBranchSync = previousSync
			footerResolveBranchAsync = previousAsync
		}()
		run(t, tempDir)
	}

	t.Run("uses HEAD directly in a regular repo from a nested directory", func(t *testing.T) {
		stub := newFooterBranchStub("main")
		each(t, stub, func(t *testing.T, tempDir string) {
			repoDir := footerCreatePlainRepo(t, tempDir)
			nestedDir := filepath.Join(repoDir, "src", "nested")
			if err := os.MkdirAll(nestedDir, 0o755); err != nil {
				t.Fatal(err)
			}

			provider := NewFooterDataProvider(nestedDir)
			defer provider.Dispose()
			branch := provider.GetGitBranch()
			if branch == nil || *branch != "main" {
				t.Fatalf("branch = %v, want main", branch)
			}
			if stub.syncCalls.Load() != 0 {
				t.Fatalf("spawnSync called %d times, want 0", stub.syncCalls.Load())
			}
		})
	})

	t.Run("resolves the branch via git when HEAD is .invalid in a reftable repo", func(t *testing.T) {
		stub := newFooterBranchStub("main")
		each(t, stub, func(t *testing.T, tempDir string) {
			repoDir := footerCreatePlainReftableRepo(t, tempDir)

			provider := NewFooterDataProvider(repoDir)
			defer provider.Dispose()
			branch := provider.GetGitBranch()
			if branch == nil || *branch != "main" {
				t.Fatalf("branch = %v, want main", branch)
			}
			if stub.syncCalls.Load() != 1 {
				t.Fatalf("spawnSync called %d times, want 1", stub.syncCalls.Load())
			}
		})
	})

	t.Run("resolves the branch via git in a reftable-backed worktree", func(t *testing.T) {
		stub := newFooterBranchStub("main")
		each(t, stub, func(t *testing.T, tempDir string) {
			worktreeDir, _ := footerCreateReftableWorktree(t, tempDir)

			provider := NewFooterDataProvider(worktreeDir)
			defer provider.Dispose()
			branch := provider.GetGitBranch()
			if branch == nil || *branch != "main" {
				t.Fatalf("branch = %v, want main", branch)
			}
		})
	})

	t.Run("treats an unresolved .invalid reftable HEAD as detached", func(t *testing.T) {
		stub := newFooterBranchStub("")
		each(t, stub, func(t *testing.T, tempDir string) {
			repoDir := footerCreatePlainReftableRepo(t, tempDir)

			provider := NewFooterDataProvider(repoDir)
			defer provider.Dispose()
			branch := provider.GetGitBranch()
			if branch == nil || *branch != "detached" {
				t.Fatalf("branch = %v, want detached", branch)
			}
		})
	})

	t.Run("does not notify listeners when reftable updates keep the same branch", func(t *testing.T) {
		stub := newFooterBranchStub("main")
		each(t, stub, func(t *testing.T, tempDir string) {
			worktreeDir, reftableDir := footerCreateReftableWorktree(t, tempDir)

			provider := NewFooterDataProvider(worktreeDir)
			defer provider.Dispose()
			if branch := provider.GetGitBranch(); branch == nil || *branch != "main" {
				t.Fatalf("branch = %v, want main", branch)
			}
			stub.syncCalls.Store(0)
			stub.asyncCalls.Store(0)

			var notified atomic.Int64
			unsubscribe := provider.OnBranchChange(func() { notified.Add(1) })
			defer unsubscribe()

			// pig: the poll watcher compares mtime; the container fs has
			// coarse timestamp granularity, so let the initial stat settle
			// into an earlier bucket before writing.
			time.Sleep(50 * time.Millisecond)
			if err := os.WriteFile(filepath.Join(reftableDir, "tables.list"), []byte("1\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			footerWaitFor(t, 3*time.Second, func() bool { return stub.asyncCalls.Load() == 1 })

			if stub.asyncCalls.Load() != 1 {
				t.Fatalf("execFile called %d times, want 1", stub.asyncCalls.Load())
			}
			if stub.syncCalls.Load() != 0 {
				t.Fatalf("spawnSync called %d times, want 0", stub.syncCalls.Load())
			}
			if branch := provider.GetGitBranch(); branch == nil || *branch != "main" {
				t.Fatalf("branch = %v, want main", branch)
			}
			if notified.Load() != 0 {
				t.Fatalf("onBranchChange fired %d times, want 0", notified.Load())
			}
		})
	})

	t.Run("debounces rapid reftable updates into a single async refresh", func(t *testing.T) {
		stub := newFooterBranchStub("main")
		each(t, stub, func(t *testing.T, tempDir string) {
			worktreeDir, reftableDir := footerCreateReftableWorktree(t, tempDir)

			provider := NewFooterDataProvider(worktreeDir)
			defer provider.Dispose()
			if branch := provider.GetGitBranch(); branch == nil || *branch != "main" {
				t.Fatalf("branch = %v, want main", branch)
			}
			stub.asyncCalls.Store(0)
			time.Sleep(50 * time.Millisecond)

			for _, content := range []string{"1\n", "2\n", "3\n"} {
				if err := os.WriteFile(filepath.Join(reftableDir, "tables.list"), []byte(content), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			footerWaitFor(t, 3*time.Second, func() bool { return stub.asyncCalls.Load() == 1 })
			time.Sleep(650 * time.Millisecond)

			if stub.asyncCalls.Load() != 1 {
				t.Fatalf("execFile called %d times, want 1", stub.asyncCalls.Load())
			}
		})
	})

	t.Run("updates the cached branch when the reftable directory changes", func(t *testing.T) {
		stub := newFooterBranchStub("main")
		each(t, stub, func(t *testing.T, tempDir string) {
			worktreeDir, reftableDir := footerCreateReftableWorktree(t, tempDir)

			provider := NewFooterDataProvider(worktreeDir)
			defer provider.Dispose()
			if branch := provider.GetGitBranch(); branch == nil || *branch != "main" {
				t.Fatalf("branch = %v, want main", branch)
			}

			var notified atomic.Int64
			unsubscribe := provider.OnBranchChange(func() { notified.Add(1) })
			defer unsubscribe()

			stub.branch.Store("foo")
			time.Sleep(50 * time.Millisecond)
			if err := os.WriteFile(filepath.Join(reftableDir, "tables.list"), []byte("1\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			footerWaitFor(t, 3*time.Second, func() bool { return stub.asyncCalls.Load() == 1 })
			footerWaitFor(t, 3*time.Second, func() bool {
				branch := provider.GetGitBranch()
				return branch != nil && *branch == "foo"
			})

			if stub.asyncCalls.Load() != 1 {
				t.Fatalf("execFile called %d times, want 1", stub.asyncCalls.Load())
			}
			if branch := provider.GetGitBranch(); branch == nil || *branch != "foo" {
				t.Fatalf("branch = %v, want foo", branch)
			}
			if notified.Load() != 1 {
				t.Fatalf("onBranchChange fired %d times, want 1", notified.Load())
			}
		})
	})

	t.Run("retries git watchers 5 seconds after a watcher error", func(t *testing.T) {
		stub := newFooterBranchStub("main")
		each(t, stub, func(t *testing.T, tempDir string) {
			repoDir := footerCreatePlainRepo(t, tempDir)

			provider := NewFooterDataProvider(repoDir)
			defer provider.Dispose()

			provider.mu.Lock()
			originalWatcher := provider.headWatcher
			provider.mu.Unlock()
			if originalWatcher == nil {
				t.Fatal("headWatcher = nil, want set")
			}

			// pi: originalWatcher.emit("error") → handleGitWatcherError.
			provider.handleGitWatcherError()

			provider.mu.Lock()
			cleared := provider.headWatcher == nil
			provider.mu.Unlock()
			if !cleared {
				t.Fatal("headWatcher not cleared after error")
			}

			time.Sleep(5100 * time.Millisecond)

			provider.mu.Lock()
			restored := provider.headWatcher != nil && provider.headWatcher != originalWatcher
			provider.mu.Unlock()
			if !restored {
				t.Fatal("headWatcher not restored after retry delay")
			}
		})
	})
}
