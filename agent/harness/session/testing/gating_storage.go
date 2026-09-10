// pi: harness/session/testing/gating-storage.ts — test-only storage
// decorator that deterministically parks admitted commits.
package testing

import (
	"context"
	"fmt"
	"sync"

	"github.com/ZoroSpace/pig/agent/harness/session"
)

// CommitDiscarded is thrown for every commit rejected after simulated
// storage loss (pi: CommitDiscarded).
type CommitDiscarded struct{ Message string }

func (e *CommitDiscarded) Error() string { return e.Message }

type parkedCommit struct {
	release chan struct{}
	landing chan error
}

// GatingStorage parks admitted commits until released (pi: GatingStorage).
type GatingStorage struct {
	StorageDecorator

	mu        sync.Mutex
	armed     bool
	discarded bool
	queue     []*parkedCommit
	waiters   []*pendingWaiter
}

type pendingWaiter struct {
	count   int
	resolve chan struct{}
}

// NewGatingStorage wraps one delegate.
func NewGatingStorage(delegate session.Storage) *GatingStorage {
	return &GatingStorage{StorageDecorator: StorageDecorator{Delegate: delegate}}
}

// Arm lets fixture setup bypass gating until explicitly armed (pi: arm).
func (s *GatingStorage) Arm() {
	s.mu.Lock()
	s.armed = true
	s.mu.Unlock()
}

// Pending reports the number of parked commits (pi: pending).
func (s *GatingStorage) Pending() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.queue)
}

// WaitPending waits until at least count commits are parked (pi:
// waitPending).
func (s *GatingStorage) WaitPending(count int) error {
	if count < 1 {
		return fmt.Errorf("Pending commit count must be a positive safe integer")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.discarded {
		return &CommitDiscarded{"storage discarded"}
	}
	if len(s.queue) >= count {
		return nil
	}
	waiter := &pendingWaiter{count: count, resolve: make(chan struct{})}
	s.waiters = append(s.waiters, waiter)
	s.mu.Unlock()
	<-waiter.resolve
	s.mu.Lock()
	return nil
}

func (s *GatingStorage) Commit(writes []session.Write, ctx context.Context) (session.CommitResult, error) {
	s.mu.Lock()
	if s.discarded {
		s.mu.Unlock()
		return session.CommitResult{}, &CommitDiscarded{"commit rejected: storage discarded"}
	}
	if !s.armed {
		s.mu.Unlock()
		return s.StorageDecorator.Commit(writes, ctx)
	}
	parked := &parkedCommit{release: make(chan struct{}), landing: make(chan error, 1)}
	s.queue = append(s.queue, parked)
	s.notifyWaitersLocked()
	s.mu.Unlock()

	<-parked.release
	s.mu.Lock()
	discarded := s.discarded
	s.mu.Unlock()
	if discarded {
		parked.landing <- &CommitDiscarded{"commit rejected: storage discarded"}
		return session.CommitResult{}, &CommitDiscarded{"commit rejected: storage discarded"}
	}
	result, err := s.StorageDecorator.Commit(writes, ctx)
	parked.landing <- err
	return result, err
}

// Next releases count commits in FIFO order and waits until each write
// lands (pi: next).
func (s *GatingStorage) Next(count int) error {
	if count < 1 {
		return fmt.Errorf("Released commit count must be a positive safe integer")
	}
	for i := 0; i < count; i++ {
		if err := s.WaitPending(1); err != nil {
			return err
		}
		s.mu.Lock()
		parked := s.queue[0]
		s.queue = s.queue[1:]
		s.mu.Unlock()
		close(parked.release)
		if err := <-parked.landing; err != nil {
			return err
		}
	}
	return nil
}

// Discard drops parked commits and permanently rejects every later commit
// (pi: discard).
func (s *GatingStorage) Discard() {
	s.mu.Lock()
	if s.discarded {
		s.mu.Unlock()
		return
	}
	s.discarded = true
	queue := s.queue
	s.queue = nil
	waiters := s.waiters
	s.waiters = nil
	s.mu.Unlock()
	for _, parked := range queue {
		select {
		case <-parked.release:
		default:
			close(parked.release)
		}
	}
	for _, waiter := range waiters {
		select {
		case <-waiter.resolve:
		default:
			close(waiter.resolve)
		}
	}
}

func (s *GatingStorage) notifyWaitersLocked() {
	remaining := s.waiters[:0]
	for _, waiter := range s.waiters {
		if len(s.queue) >= waiter.count {
			close(waiter.resolve)
		} else {
			remaining = append(remaining, waiter)
		}
	}
	s.waiters = remaining
}
