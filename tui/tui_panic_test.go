// tui/tui_panic_test.go — regression tests for pig panic containment in the
// TUI lock (pig-internal feature; see docs/feat/PANIC-RESILIENCE.md). Before
// the panic-safe release in TuiBase.lock, a panic inside a locked callback
// left b.mu acquired forever and every later lock() call deadlocked.
package tui

import (
	"testing"
	"time"
)

func TestTuiLockReleasesMutexOnPanic(t *testing.T) {
	b := &TuiBase{}
	calls := 0

	func() {
		defer func() { _ = recover() }() // simulate the spawn-site containment
		b.lock(func() {
			calls++
			panic("boom")
		})
	}()
	if calls != 1 {
		t.Fatalf("callback ran %d times, want 1", calls)
	}

	// The mutex must be usable after the panic — with the pre-fix
	// non-deferred release this goroutine hangs and the test times out.
	done := make(chan struct{})
	go func() {
		b.lock(func() { calls++ })
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("TuiBase.lock deadlocked after a panic inside the callback")
	}
	if calls != 2 {
		t.Fatalf("post-panic callback ran %d times, want 2", calls)
	}
}

func TestTuiLockNestedReleaseOnPanic(t *testing.T) {
	// Re-entrant nesting: the inner inline call must not disturb the outer
	// frame's release, and a panic unwinding through several frames still
	// releases the mutex exactly once.
	b := &TuiBase{}
	func() {
		defer func() { _ = recover() }()
		b.lock(func() {
			b.lock(func() {
				panic("nested boom")
			})
		})
	}()
	done := make(chan struct{})
	go func() {
		b.lock(func() {})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("TuiBase.lock deadlocked after a nested panic")
	}
}
