package ai

import (
	"context"
)

// pi: utils/abort.ts. AbortSignal becomes context.Context throughout pig;
// operationSignal (wrap an optional signal so internal code can always call
// throwIfAborted) has no counterpart — a Go context is never nil.

// waitOperation waits for op and returns its result. If ctx is cancelled
// first, ctx.Err() is returned and op's eventual result or error is discarded
// (pi: raceWithAbortSignal, which likewise swallows a later rejection from the
// abandoned promise).
func waitOperation[T any](ctx context.Context, op func() (T, error)) (T, error) {
	type outcome struct {
		value T
		err   error
	}
	ch := make(chan outcome, 1)
	go func() {
		v, err := op()
		ch <- outcome{value: v, err: err}
	}()
	select {
	case <-ctx.Done():
		var zero T
		return zero, ctx.Err()
	case out := <-ch:
		return out.value, out.err
	}
}
