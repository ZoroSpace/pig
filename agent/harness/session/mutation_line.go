// pi: harness/session/mutation-line.ts — serializes complete read-modify-
// write jobs for one Session.
//
// Deviation: pi chains promises (jobs queue behind the previous tail); Go
// uses a condition variable. Run waits for the in-flight job, re-checks the
// seal, executes exclusively, and broadcasts on release. Seal records the
// error (first one wins) and waits for the in-flight job to drain, matching
// `seal()` returning this.tail. Jobs rejected by a seal fail with the sealed
// error exactly like pi's rejected promise chain.
package session

import "sync"

// MutationLine serializes mutation jobs for one Session (pi: MutationLine).
type MutationLine struct {
	mu          sync.Mutex
	cond        *sync.Cond
	inFlight    int
	sealedError error
	// drained is closed once the job in flight when Seal fired (or no job
	// at all) has finished — the promise `seal()` returns.
	drained chan struct{}
	drainMu sync.Mutex
}

// NewMutationLine builds an unsealed line.
func NewMutationLine() *MutationLine {
	l := &MutationLine{drained: make(chan struct{})}
	l.cond = sync.NewCond(&l.mu)
	return l
}

// Run executes one job under the line; a sealed line rejects every job (pi:
// run).
func (l *MutationLine) Run(fn func() error) error {
	l.mu.Lock()
	if l.sealedError != nil {
		err := l.sealedError
		l.mu.Unlock()
		return err
	}
	for l.inFlight > 0 {
		l.cond.Wait()
	}
	if l.sealedError != nil {
		err := l.sealedError
		l.mu.Unlock()
		return err
	}
	l.inFlight = 1
	l.mu.Unlock()

	err := fn()

	l.mu.Lock()
	l.inFlight = 0
	if l.sealedError != nil {
		l.signalDrainedLocked()
	}
	l.cond.Broadcast()
	l.mu.Unlock()
	return err
}

// signalDrainedLocked closes the drained channel exactly once; caller holds
// l.mu.
func (l *MutationLine) signalDrainedLocked() {
	select {
	case <-l.drained:
		// already closed
	default:
		close(l.drained)
	}
}

// Seal records the terminal error and returns a channel closed once the
// in-flight job has drained (pi: seal returning this.tail). The first
// sealed error wins.
func (l *MutationLine) Seal(err error) <-chan struct{} {
	l.mu.Lock()
	if l.sealedError == nil {
		l.sealedError = err
	}
	if l.inFlight == 0 {
		l.signalDrainedLocked()
	}
	drained := l.drained
	l.mu.Unlock()
	return drained
}

// SealAndWait blocks until the drained boundary (convenience for callers
// that need the promise await semantics inline).
func (l *MutationLine) SealAndWait(err error) {
	<-l.Seal(err)
}
