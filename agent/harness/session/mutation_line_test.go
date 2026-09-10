// pi: test/harness/mutation-line.test.ts — mutation-line serialization.
//
// Deviation: pi's promise-chained async jobs map to Go synchronous jobs;
// the queuing tests exercise the same ordering through goroutines.
// types.test.ts (type-level expectTypeOf assertions) has no Go counterpart
// and is not ported.
package session

import (
	"sync"
	"testing"
	"time"
)

func TestSerializesEverySessionMutation(t *testing.T) {
	line := NewMutationLine()
	gate := make(chan struct{})
	var mu sync.Mutex
	var order []string
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = line.Run(func() error {
			mu.Lock()
			order = append(order, "first:start")
			mu.Unlock()
			<-gate
			mu.Lock()
			order = append(order, "first:end")
			mu.Unlock()
			return nil
		})
	}()
	// Let the first job start.
	time.Sleep(10 * time.Millisecond)

	secondDone := make(chan struct{})
	go func() {
		defer close(secondDone)
		_ = line.Run(func() error {
			mu.Lock()
			order = append(order, "second")
			mu.Unlock()
			return nil
		})
	}()

	mu.Lock()
	if len(order) != 1 || order[0] != "first:start" {
		mu.Unlock()
		t.Fatalf("order before gate = %v", order)
	}
	mu.Unlock()
	close(gate)
	wg.Wait()
	<-secondDone
	mu.Lock()
	defer mu.Unlock()
	if len(order) != 3 || order[0] != "first:start" || order[1] != "first:end" || order[2] != "second" {
		t.Fatalf("order = %v", order)
	}
}

func TestContinuesAfterAFailedJobWhilePreservingTheOriginalFailure(t *testing.T) {
	line := NewMutationLine()
	rejection := "mutation failed"

	err := line.Run(func() error { return &sealedLineError{rejection} })
	if err == nil || err.Error() != rejection {
		t.Fatalf("first job err = %v", err)
	}
	err = line.Run(func() error { return nil })
	if err != nil {
		t.Fatalf("second job err = %v", err)
	}
}

type sealedLineError struct{ msg string }

func (e *sealedLineError) Error() string { return e.msg }

func TestSealsQueuedAndFutureJobsWhileDrainingTheRunningJob(t *testing.T) {
	line := NewMutationLine()
	gate := make(chan struct{})
	runningDone := make(chan error, 1)
	go func() {
		runningDone <- line.Run(func() error {
			<-gate
			return nil
		})
	}()
	time.Sleep(10 * time.Millisecond)

	queuedDone := make(chan error, 1)
	go func() {
		queuedDone <- line.Run(func() error { return nil })
	}()

	closedErr := &sealedLineError{"closed"}
	drained := line.Seal(closedErr)

	if err := line.Run(func() error { return nil }); err != closedErr {
		t.Fatalf("late job err = %v, want the sealed error", err)
	}
	close(gate)
	if err := <-runningDone; err != nil {
		t.Fatalf("running job err = %v", err)
	}
	if err := <-queuedDone; err != closedErr {
		t.Fatalf("queued job err = %v, want the sealed error", err)
	}
	// pi: `await drained` — resolved once the running job finished.
	select {
	case <-drained:
	default:
		t.Fatal("drained must resolve after the running job finished")
	}
}
