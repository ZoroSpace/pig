package components

import "sync"

// testMutator queues component mutations and lets tests apply them
// deterministically on the test goroutine — the pig equivalent of pi's
// single-threaded event loop (background goroutines post mutations; the
// test "event loop" flush applies them).
type testMutator struct {
	mu      sync.Mutex
	pending []func()
}

func (m *testMutator) mutate(fn func()) {
	m.mu.Lock()
	m.pending = append(m.pending, fn)
	m.mu.Unlock()
}

// flush applies all queued mutations on the calling goroutine.
func (m *testMutator) flush() {
	for {
		m.mu.Lock()
		fns := m.pending
		m.pending = nil
		m.mu.Unlock()
		if len(fns) == 0 {
			return
		}
		for _, fn := range fns {
			fn()
		}
	}
}

// settled reports whether no mutations are queued or running.
func (m *testMutator) settled() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.pending) == 0
}

// flushUntilSettled flushes until no more mutations arrive from background
// goroutines, then verifies a quiet state.
func (m *testMutator) flushUntilSettled() {
	for {
		m.flush()
		if m.settled() {
			return
		}
	}
}
