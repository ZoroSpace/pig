// pi: utils/sleep.ts — sleep helper that respects the abort signal.
package utils

import (
	"context"
	"time"
)

// Sleep waits for ms, returning early when ctx is cancelled (pi: sleep(ms,
// signal?) — the optional AbortSignal becomes the ctx parameter; pi rejects
// with Error("Aborted"), pig returns ctx.Err()).
func Sleep(ctx context.Context, ms int64) error {
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	timer := time.NewTimer(time.Duration(ms) * time.Millisecond)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
