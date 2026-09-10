// pi: harness/session/testing/instrumented-storage.ts — test-only
// transparent Storage decorator that records commit admission.
package testing

import (
	"context"

	"github.com/ZoroSpace/pig/agent/harness/session"
)

// InstrumentedStorage records every Commit admission (pi:
// InstrumentedStorage).
type InstrumentedStorage struct {
	StorageDecorator
	commitAttempts [][]session.Write
}

// NewInstrumentedStorage wraps one delegate.
func NewInstrumentedStorage(delegate session.Storage) *InstrumentedStorage {
	return &InstrumentedStorage{StorageDecorator: StorageDecorator{Delegate: delegate}}
}

func (s *InstrumentedStorage) Commit(writes []session.Write, ctx context.Context) (session.CommitResult, error) {
	s.commitAttempts = append(s.commitAttempts, writes)
	return s.StorageDecorator.Commit(writes, ctx)
}

// GetCommitAttempts returns a copy of the recorded attempts (pi:
// getCommitAttempts).
func (s *InstrumentedStorage) GetCommitAttempts() [][]session.Write {
	out := make([][]session.Write, len(s.commitAttempts))
	copy(out, s.commitAttempts)
	return out
}

// ClearCommitAttempts resets the recording (pi: clearCommitAttempts).
func (s *InstrumentedStorage) ClearCommitAttempts() {
	s.commitAttempts = nil
}
