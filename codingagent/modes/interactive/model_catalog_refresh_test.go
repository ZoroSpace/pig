// Ported from pi's test/model-catalog-refresh.test.ts (stub runtime instead
// of vi.fn).
package interactive

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/ZoroSpace/pig/ai"
)

type stubRefreshRuntime struct {
	mu       sync.Mutex
	calls    int
	signal   context.Context
	blocking bool
	// stall signals the first refresh's return channel construction.
	held chan ai.ModelsRefreshResult
}

func (s *stubRefreshRuntime) Refresh(ctx context.Context, options *ai.ModelsRefreshOptions) ai.ModelsRefreshResult {
	s.mu.Lock()
	s.calls++
	call := s.calls
	s.signal = ctx
	s.mu.Unlock()
	if s.blocking {
		<-ctx.Done()
		return ai.ModelsRefreshResult{Aborted: true, Errors: map[string]error{}}
	}
	if s.held != nil && call == 1 {
		select {
		case result := <-s.held:
			return result
		case <-ctx.Done():
			return ai.ModelsRefreshResult{Aborted: true, Errors: map[string]error{}}
		}
	}
	return ai.ModelsRefreshResult{Aborted: false, Errors: map[string]error{}}
}

func successfulRefresh() ai.ModelsRefreshResult {
	return ai.ModelsRefreshResult{Aborted: false, Errors: map[string]error{}}
}

func TestRefreshModelCatalogsSharesOneRuntimeRefreshBetweenConcurrentCallers(t *testing.T) {
	runtime := &stubRefreshRuntime{}
	held := make(chan ai.ModelsRefreshResult, 1)
	runtime.held = held

	firstCtx := context.Background()
	secondCtx, cancelSecond := context.WithCancel(context.Background())
	defer cancelSecond()

	firstDone := make(chan error, 1)
	secondDone := make(chan error, 1)
	go func() {
		_, err := RefreshModelCatalogs(firstCtx, runtime)
		firstDone <- err
	}()
	go func() {
		_, err := RefreshModelCatalogs(secondCtx, runtime)
		secondDone <- err
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && runtime.callCount() < 1 {
		time.Sleep(time.Millisecond)
	}
	// Give the second caller time to enter RefreshModelCatalogs and attach
	// to the in-flight refresh before releasing it; without this the
	// scheduler can schedule the first completion (and active-entry
	// teardown) before the second goroutine starts, causing a second
	// refresh (observed flaky under the full -race suite).
	time.Sleep(10 * time.Millisecond)
	held <- successfulRefresh()
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	if err := <-secondDone; err != nil {
		t.Fatal(err)
	}
	if calls := runtime.callCount(); calls != 1 {
		t.Fatalf("refresh calls = %d, want 1", calls)
	}
}

func (s *stubRefreshRuntime) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func TestRefreshModelCatalogsKeepsTheSharedRefreshAliveWhenOneCallerStopsWaiting(t *testing.T) {
	runtime := &stubRefreshRuntime{}
	held := make(chan ai.ModelsRefreshResult, 1)
	runtime.held = held

	firstCtx, cancelFirst := context.WithCancel(context.Background())
	secondCtx, cancelSecond := context.WithCancel(context.Background())
	defer cancelSecond()

	firstDone := make(chan error, 1)
	secondDone := make(chan error, 1)
	go func() {
		_, err := RefreshModelCatalogs(firstCtx, runtime)
		firstDone <- err
	}()
	go func() {
		_, err := RefreshModelCatalogs(secondCtx, runtime)
		secondDone <- err
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && runtime.callCount() < 1 {
		time.Sleep(time.Millisecond)
	}
	cancelFirst()
	if err := <-firstDone; err == nil {
		t.Fatal("first caller should fail with cancellation")
	}
	// The shared refresh keeps running: its context is not cancelled.
	runtime.mu.Lock()
	sharedAborted := runtime.signal != nil && runtime.signal.Err() != nil
	runtime.mu.Unlock()
	if sharedAborted {
		t.Fatal("shared refresh was cancelled while another caller waits")
	}
	held <- successfulRefresh()
	if err := <-secondDone; err != nil {
		t.Fatal(err)
	}
}

func TestRefreshModelCatalogsAbortsAnAbandonedRefreshAndAllowsALaterRefreshToStart(t *testing.T) {
	runtime := &stubRefreshRuntime{blocking: true}

	firstCtx, cancelFirst := context.WithCancel(context.Background())
	firstDone := make(chan error, 1)
	go func() {
		_, err := RefreshModelCatalogs(firstCtx, runtime)
		firstDone <- err
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && runtime.callCount() < 1 {
		time.Sleep(time.Millisecond)
	}
	cancelFirst()
	if err := <-firstDone; err == nil {
		t.Fatal("first caller should fail with cancellation")
	}
	// The abandoned refresh's context is cancelled once the last waiter
	// leaves, which unblocks the blocking stub and lets the next refresh
	// start.
	waitDeadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(waitDeadline) {
		runtime.mu.Lock()
		aborted := runtime.signal != nil && runtime.signal.Err() != nil
		runtime.mu.Unlock()
		if aborted {
			break
		}
		time.Sleep(time.Millisecond)
	}

	secondCtx, cancelSecond := context.WithCancel(context.Background())
	secondDone := make(chan error, 1)
	go func() {
		_, err := RefreshModelCatalogs(secondCtx, runtime)
		secondDone <- err
	}()

	secondDeadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(secondDeadline) && runtime.callCount() < 2 {
		time.Sleep(time.Millisecond)
	}
	cancelSecond()
	if err := <-secondDone; err == nil {
		t.Fatal("second caller should fail with cancellation")
	}
	if calls := runtime.callCount(); calls != 2 {
		t.Fatalf("refresh calls = %d, want 2", calls)
	}
}
