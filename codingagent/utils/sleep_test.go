// Tests for codingagent/utils/sleep.go (pi: utils/sleep.ts).
package utils

import (
	"context"
	"testing"
	"time"
)

func TestSleepResolvesAfterDuration(t *testing.T) {
	start := time.Now()
	if err := Sleep(context.Background(), 20); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed < 15*time.Millisecond {
		t.Fatalf("returned too early: %v", elapsed)
	}
}

func TestSleepReturnsImmediatelyOnCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	if err := Sleep(ctx, 60000); err == nil {
		t.Fatal("expected cancellation error")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("did not return immediately: %v", elapsed)
	}
}

func TestSleepIsInterruptedByCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	if err := Sleep(ctx, 60000); err == nil {
		t.Fatal("expected cancellation error")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("did not abort early: %v", elapsed)
	}
}
