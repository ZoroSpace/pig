// tui/goid.go — pig-internal concurrency infrastructure (no pi counterpart).
//
// currentGoroutineID parses the goroutine ID out of a minimal runtime.Stack
// dump ("goroutine 123 [running]:"). It backs the reentrant-lock owner check
// in TuiBase.lock/Editor.lock: pi relies on the JS event loop (all
// component/tui API calls run on one thread), while pig legitimately calls
// those APIs from several goroutines (input dispatch, render scheduling,
// editor autocomplete continuations). A shared flag cannot tell "re-entry
// from the same goroutine" apart from "concurrent call from another
// goroutine" — the owner ID can.
//
// Cost is ~1µs per call (one small Stack dump); the hot paths pay it once
// per locked API call, which is human-rate for a TUI.

package tui

import (
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
)

// reentrantLockOwner tracks the goroutine that currently holds a mutex for
// reentrancy detection (0 = unlocked). Store/Load must be atomic: the owner
// writes it while holding the mutex, other goroutines read it without.
type reentrantLockOwner struct{ v atomic.Uint64 }

// isCurrent reports whether the calling goroutine owns the lock.
func (o *reentrantLockOwner) isCurrent() bool {
	owner := o.v.Load()
	return owner != 0 && owner == currentGoroutineID()
}

// acquire records the calling goroutine as the lock owner. Must be called
// while holding the mutex.
func (o *reentrantLockOwner) acquire() { o.v.Store(currentGoroutineID()) }

// release clears the lock owner. Must be called while holding the mutex,
// before unlocking.
func (o *reentrantLockOwner) release() { o.v.Store(0) }

// currentGoroutineID returns the calling goroutine's ID (runtime-internal
// "goroutine N" header of a minimal stack dump).
func currentGoroutineID() uint64 {
	var buf [64]byte
	n := runtime.Stack(buf[:], false)
	// Format: "goroutine 123 [running]:\n..."
	fields := strings.Fields(string(buf[:n]))
	if len(fields) < 2 {
		return 0
	}
	id, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil {
		return 0
	}
	return id
}
