// tui/undo_stack.go — ported from pi: packages/tui/src/undo-stack.ts
//
// Declared deviation (structuredClone): TS deep-clones snapshots via
// structuredClone; pig clones via a caller-supplied clone function (JSON
// round-trip for the editor's EditorSnapshot, which is JSON-safe). The
// clone-on-push / return-popped-directly semantics are preserved.

package tui

import "sync"

// UndoStack is a generic undo stack with clone-on-push semantics.
//
// Stores deep clones of state snapshots. Popped snapshots are returned
// directly (no re-cloning) since they are already detached.
type UndoStack[S any] struct {
	mu    sync.Mutex
	stack []S
	clone func(S) S
}

// NewUndoStack creates an undo stack that deep-clones snapshots with the
// given function on push.
func NewUndoStack[S any](clone func(S) S) *UndoStack[S] {
	return &UndoStack[S]{clone: clone}
}

// Push pushes a deep clone of the given state onto the stack.
func (u *UndoStack[S]) Push(state S) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.stack = append(u.stack, u.clone(state))
}

// Pop pops and returns the most recent snapshot, or false if empty.
func (u *UndoStack[S]) Pop() (S, bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	var zero S
	if len(u.stack) == 0 {
		return zero, false
	}
	top := u.stack[len(u.stack)-1]
	u.stack = u.stack[:len(u.stack)-1]
	return top, true
}

// Clear removes all snapshots.
func (u *UndoStack[S]) Clear() {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.stack = nil
}

// Len returns the number of snapshots on the stack.
func (u *UndoStack[S]) Len() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return len(u.stack)
}
