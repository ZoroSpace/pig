// session_selector_path_delete_test.go (pi:
// test/session-selector-path-delete.test.ts) — /resume picker delete
// confirmation, scope-toggle load races, symlink threading, and current
// session protection tests.
package components

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ZoroSpace/pig/codingagent/core"
	"github.com/ZoroSpace/pig/tui"
)

// deferredSessions is a loader stub whose result is released manually (pi:
// createDeferred).
type deferredSessions struct {
	mu       sync.Mutex
	sessions []core.SessionInfo
	resolved chan struct{}
	once     sync.Once
	calls    int
}

func newDeferredSessions() *deferredSessions {
	return &deferredSessions{resolved: make(chan struct{})}
}

func (d *deferredSessions) resolve(sessions []core.SessionInfo) {
	d.mu.Lock()
	d.sessions = sessions
	d.mu.Unlock()
	d.once.Do(func() { close(d.resolved) })
}

func (d *deferredSessions) load(core.SessionListProgress) []core.SessionInfo {
	d.mu.Lock()
	d.calls++
	d.mu.Unlock()
	<-d.resolved
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.sessions
}

func (d *deferredSessions) callCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.calls
}

func newSessionSelectorWithLoaders(
	t *testing.T,
	currentLoader sessionsLoader,
	allLoader sessionsLoader,
	options *SessionSelectorOptions,
	currentSessionFilePath string,
) (*SessionSelectorComponent, *testMutator) {
	t.Helper()
	initTestTheme(t)
	tui.SetKeybindings(core.NewKeybindingsManager(nil, "").KeybindingsManager)
	mutator := &testMutator{}
	return NewSessionSelectorComponent(
		currentLoader, allLoader,
		func(string) {}, func() {}, func() {}, func() {},
		options, currentSessionFilePath,
		mutator.mutate,
	), mutator
}

// waitLoaded flushes until both scope loads settle (pi: flushPromises;
// load flags are written only inside mutate callbacks, so reading them on
// the test goroutine after a flush is race-free).
func waitLoaded(t *testing.T, mutator *testMutator, selector *SessionSelectorComponent) {
	waitForCondition(t, "scope loads settle", func() bool {
		mutator.flush()
		return !selector.currentLoading && !selector.allLoading
	})
}

func waitForCondition(t *testing.T, desc string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("condition not met: %s", desc)
}

func TestSessionSelectorPathDelete(t *testing.T) {
	initTestTheme(t)
	const ctrlD = "\x04"
	// Kitty keyboard protocol encoding for Ctrl+Backspace (pi:
	// "\x1b[127;5u") — pig matches the legacy byte in tests.
	const ctrlBackspace = "\x1b[127;5u"

	t.Run("does not treat Ctrl+Backspace as delete when search query is non-empty", func(t *testing.T) {
		sessions := []core.SessionInfo{
			makeSessionForSelector(func(s *core.SessionInfo) { s.ID = "a" }),
			makeSessionForSelector(func(s *core.SessionInfo) { s.ID = "b" }),
		}
		selector, mutator := newSessionSelectorWithLoaders(t,
			func(core.SessionListProgress) []core.SessionInfo { return sessions },
			func(core.SessionListProgress) []core.SessionInfo { return nil }, nil, "")
		waitLoaded(t, mutator, selector)

		list := selector.GetSessionList()
		var confirmationChanges []string
		list.OnDeleteConfirmationChange = func(path string) { confirmationChanges = append(confirmationChanges, path) }

		list.HandleInput("a")
		list.HandleInput(ctrlBackspace)

		if len(confirmationChanges) != 0 {
			t.Fatalf("confirmationChanges = %v, want empty", confirmationChanges)
		}
	})

	t.Run("enters confirmation mode on Ctrl+D even with a non-empty search query", func(t *testing.T) {
		sessions := []core.SessionInfo{
			makeSessionForSelector(func(s *core.SessionInfo) { s.ID = "a" }),
			makeSessionForSelector(func(s *core.SessionInfo) { s.ID = "b" }),
		}
		selector, mutator := newSessionSelectorWithLoaders(t,
			func(core.SessionListProgress) []core.SessionInfo { return sessions },
			func(core.SessionListProgress) []core.SessionInfo { return nil }, nil, "")
		waitLoaded(t, mutator, selector)

		list := selector.GetSessionList()
		var confirmationChanges []string
		list.OnDeleteConfirmationChange = func(path string) { confirmationChanges = append(confirmationChanges, path) }

		list.HandleInput("a")
		list.HandleInput(ctrlD)

		assertIDs(t, confirmationChanges, sessions[0].Path)
	})

	t.Run("enters confirmation mode on Ctrl+Backspace when search query is empty", func(t *testing.T) {
		sessions := []core.SessionInfo{
			makeSessionForSelector(func(s *core.SessionInfo) { s.ID = "a" }),
			makeSessionForSelector(func(s *core.SessionInfo) { s.ID = "b" }),
		}
		selector, mutator := newSessionSelectorWithLoaders(t,
			func(core.SessionListProgress) []core.SessionInfo { return sessions },
			func(core.SessionListProgress) []core.SessionInfo { return nil }, nil, "")
		waitLoaded(t, mutator, selector)

		list := selector.GetSessionList()
		var confirmationChanges []string
		list.OnDeleteConfirmationChange = func(path string) { confirmationChanges = append(confirmationChanges, path) }

		deletedPath := ""
		list.OnDeleteSession = func(sessionPath string) { deletedPath = sessionPath }

		list.HandleInput(ctrlBackspace)
		assertIDs(t, confirmationChanges, sessions[0].Path)

		list.HandleInput("\r")
		assertIDs(t, confirmationChanges, sessions[0].Path, "")
		if deletedPath != sessions[0].Path {
			t.Fatalf("deletedPath = %q, want %q", deletedPath, sessions[0].Path)
		}
	})

	t.Run("does not switch scope back to All when All load resolves after toggling back to Current", func(t *testing.T) {
		currentSessions := []core.SessionInfo{makeSessionForSelector(func(s *core.SessionInfo) { s.ID = "current" })}
		allDeferred := newDeferredSessions()
		selector, mutator := newSessionSelectorWithLoaders(t,
			func(core.SessionListProgress) []core.SessionInfo { return currentSessions },
			allDeferred.load, nil, "")
		waitLoaded(t, mutator, selector)

		list := selector.GetSessionList()
		list.HandleInput("\t") // current -> all (starts async load)
		list.HandleInput("\t") // all -> current

		allDeferred.resolve([]core.SessionInfo{makeSessionForSelector(func(s *core.SessionInfo) { s.ID = "all" })})
		waitLoaded(t, mutator, selector)

		if got := allDeferred.callCount(); got != 1 {
			t.Fatalf("all loader calls = %d, want 1", got)
		}
		output := strings.Join(selector.Render(120), "\n")
		if !strings.Contains(output, "Resume Session (Current Folder)") {
			t.Fatalf("render missing current-folder title:\n%s", output)
		}
		if strings.Contains(output, "Resume Session (All)") {
			t.Fatalf("render should not show All title:\n%s", output)
		}
	})

	t.Run("does not start redundant All loads when toggling scopes while All is already loading", func(t *testing.T) {
		currentSessions := []core.SessionInfo{makeSessionForSelector(func(s *core.SessionInfo) { s.ID = "current" })}
		allDeferred := newDeferredSessions()
		selector, mutator := newSessionSelectorWithLoaders(t,
			func(core.SessionListProgress) []core.SessionInfo { return currentSessions },
			allDeferred.load, nil, "")
		waitLoaded(t, mutator, selector)

		list := selector.GetSessionList()
		list.HandleInput("\t") // current -> all (starts async load)
		list.HandleInput("\t") // all -> current
		list.HandleInput("\t") // current -> all again while load pending

		// The All loader call happens on the load goroutine — wait for it (pi
		// calls the loader synchronously up to the first await).
		waitForCondition(t, "all load started", func() bool { return allDeferred.callCount() >= 1 })
		if got := allDeferred.callCount(); got != 1 {
			t.Fatalf("all loader calls = %d, want 1", got)
		}

		allDeferred.resolve([]core.SessionInfo{makeSessionForSelector(func(s *core.SessionInfo) { s.ID = "all" })})
		waitLoaded(t, mutator, selector)
	})

	t.Run("threads sessions when parent and child paths use different symlink aliases", func(t *testing.T) {
		paths := createSymlinkedSessionPaths(t)
		sessions := []core.SessionInfo{
			makeSessionForSelector(func(s *core.SessionInfo) {
				s.ID = "parent"
				s.Path = paths.parentAliasB
				s.Name = strPtr("Parent")
				s.Modified = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			}),
			makeSessionForSelector(func(s *core.SessionInfo) {
				s.ID = "child"
				s.Path = paths.childAliasB
				s.ParentSessionPath = &paths.parentAliasA
				s.Name = strPtr("Child")
				s.Modified = time.Date(2025, 12, 31, 0, 0, 0, 0, time.UTC)
			}),
		}

		selector, mutator := newSessionSelectorWithLoaders(t,
			func(core.SessionListProgress) []core.SessionInfo { return sessions },
			func(core.SessionListProgress) []core.SessionInfo { return nil }, nil, "")
		waitLoaded(t, mutator, selector)

		output := stripVT(t, strings.Join(selector.Render(120), "\n"))
		if !strings.Contains(output, "Parent") {
			t.Fatalf("render missing Parent:\n%s", output)
		}
		if !strings.Contains(output, "└─ Child") {
			t.Fatalf("render missing threaded Child:\n%s", output)
		}
	})

	t.Run("sorts threaded sessions by latest activity in their subtree", func(t *testing.T) {
		parentOne := makeSessionForSelector(func(s *core.SessionInfo) {
			s.ID = "parent-one"
			s.Name = strPtr("Parent one")
			s.Modified = time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
		})
		parentTwo := makeSessionForSelector(func(s *core.SessionInfo) {
			s.ID = "parent-two"
			s.Name = strPtr("Parent two")
			s.Modified = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		})
		childTwo := makeSessionForSelector(func(s *core.SessionInfo) {
			s.ID = "child-two"
			s.Name = strPtr("Child two")
			s.ParentSessionPath = &parentTwo.Path
			s.Modified = time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC)
		})

		selector, mutator := newSessionSelectorWithLoaders(t,
			func(core.SessionListProgress) []core.SessionInfo {
				return []core.SessionInfo{parentOne, parentTwo, childTwo}
			},
			func(core.SessionListProgress) []core.SessionInfo { return nil }, nil, "")
		waitLoaded(t, mutator, selector)

		output := stripVT(t, strings.Join(selector.Render(120), "\n"))
		parentTwoIndex := strings.Index(output, "Parent two")
		childTwoIndex := strings.Index(output, "└─ Child two")
		parentOneIndex := strings.Index(output, "Parent one")

		if parentTwoIndex < 0 || childTwoIndex <= parentTwoIndex || parentOneIndex <= childTwoIndex {
			t.Fatalf("thread order wrong: parentTwo=%d childTwo=%d parentOne=%d\n%s",
				parentTwoIndex, childTwoIndex, parentOneIndex, output)
		}
	})

	t.Run("treats the current session as active across symlink aliases", func(t *testing.T) {
		paths := createSymlinkedSessionPaths(t)
		sessions := []core.SessionInfo{
			makeSessionForSelector(func(s *core.SessionInfo) {
				s.ID = "parent"
				s.Path = paths.parentAliasB
				s.Name = strPtr("Parent")
			}),
		}
		selector, mutator := newSessionSelectorWithLoaders(t,
			func(core.SessionListProgress) []core.SessionInfo { return sessions },
			func(core.SessionListProgress) []core.SessionInfo { return nil }, nil, paths.parentAliasA)
		waitLoaded(t, mutator, selector)

		list := selector.GetSessionList()
		var confirmationChanges []string
		list.OnDeleteConfirmationChange = func(path string) { confirmationChanges = append(confirmationChanges, path) }
		errorMessage := ""
		list.OnError = func(message string) { errorMessage = message }

		list.HandleInput(ctrlD)

		if len(confirmationChanges) != 0 {
			t.Fatalf("confirmationChanges = %v, want empty", confirmationChanges)
		}
		if errorMessage != "Cannot delete the currently active session" {
			t.Fatalf("errorMessage = %q", errorMessage)
		}
	})
}

// symlinkedSessionPaths mirrors pi's createSymlinkedSessionPaths fixture.
type symlinkedSessionPaths struct {
	baseDir      string
	parentAliasA string
	parentAliasB string
	childAliasB  string
}

func createSymlinkedSessionPaths(t *testing.T) symlinkedSessionPaths {
	t.Helper()
	baseDir, err := os.MkdirTemp("", "pig-session-selector-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(baseDir) })

	realDir := filepath.Join(baseDir, "real")
	aliasADir := filepath.Join(baseDir, "alias-a")
	aliasBDir := filepath.Join(baseDir, "alias-b")
	for _, dir := range []string{realDir, aliasADir, aliasBDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	sharedDir := filepath.Join(realDir, "sessions")
	if err := os.MkdirAll(sharedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	aliasASessions := filepath.Join(aliasADir, "sessions")
	aliasBSessions := filepath.Join(aliasBDir, "sessions")
	if err := os.Symlink(sharedDir, aliasASessions); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(sharedDir, aliasBSessions); err != nil {
		t.Fatal(err)
	}

	parentRealPath := filepath.Join(sharedDir, "parent.jsonl")
	childRealPath := filepath.Join(sharedDir, "child.jsonl")
	if err := os.WriteFile(parentRealPath, []byte("parent\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(childRealPath, []byte("child\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	return symlinkedSessionPaths{
		baseDir:      baseDir,
		parentAliasA: filepath.Join(aliasASessions, "parent.jsonl"),
		parentAliasB: filepath.Join(aliasBSessions, "parent.jsonl"),
		childAliasB:  filepath.Join(aliasBSessions, "child.jsonl"),
	}
}
