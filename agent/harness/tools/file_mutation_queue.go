// pi: src/harness/tools/file-mutation-queue.ts — serialization of file
// mutations targeting the same environment and canonical path.
//
// Segment deviations:
//   - pi keys a module-level WeakMap by the env object; pig keys a package
//     mutex-guarded map by the env interface value (concrete types are
//     pointers, so keying matches object identity; uncomparable dynamic
//     types would panic, which no harness env exhibits).
//   - pi chains promises per canonical path; pig uses per-key "token"
//     channels closed to release the next waiter, reproducing the same
//     registration → enqueue → wait → run → release order. The registration
//     (key computation) is serialized under the state mutex, matching pi's
//     state.registration promise chain; key computation failure aborts only
//     the caller, not the chain.
//   - fn is synchronous (pi: () => Promise<T>); the return value crosses the
//     closure, errors propagate.
package tools

import (
	"context"
	"sync"

	"github.com/ZoroSpace/pig/agent/harness"
)

// mutationQueueState is the per-environment queue bookkeeping (pi:
// MutationQueueState).
type mutationQueueState struct {
	mu     sync.Mutex
	queues map[string]chan struct{}
}

var (
	mutationStatesMu sync.Mutex
	// mutationStates keys by env identity (pi: WeakMap<ExecutionEnv, ...>).
	mutationStates = map[harness.ExecutionEnv]*mutationQueueState{}
)

// getMutationState returns the per-env state, creating it on first use (pi:
// getState).
func getMutationState(env harness.ExecutionEnv) *mutationQueueState {
	mutationStatesMu.Lock()
	defer mutationStatesMu.Unlock()
	state, ok := mutationStates[env]
	if !ok {
		state = &mutationQueueState{queues: map[string]chan struct{}{}}
		mutationStates[env] = state
	}
	return state
}

// getMutationQueueKey computes the canonical queue key for a path (pi:
// getMutationQueueKey). Non-canonicalizable paths fall back to the absolute
// path for not_found/not_supported; other failures propagate.
func getMutationQueueKey(ctx context.Context, env harness.ExecutionEnv, path string) (string, error) {
	absolutePath, err := env.AbsolutePath(ctx, path)
	if err != nil {
		return "", err
	}
	canonicalPath, err := env.CanonicalPath(ctx, absolutePath)
	if err == nil {
		return canonicalPath, nil
	}
	if fileErr, ok := harness.AsFileError(err); ok {
		if fileErr.Code == harness.FileCodeNotFound || fileErr.Code == harness.FileCodeNotSupported {
			return absolutePath, nil
		}
	}
	return "", err
}

// WithFileMutationQueue serializes file mutations targeting the same
// environment and canonical path (pi: withFileMutationQueue). fn runs while
// holding the path's queue slot; its result is returned directly.
func WithFileMutationQueue[T any](ctx context.Context, env harness.ExecutionEnv, path string, fn func() (T, error)) (T, error) {
	var zero T
	state := getMutationState(env)

	state.mu.Lock()
	key, err := getMutationQueueKey(ctx, env, path)
	if err != nil {
		state.mu.Unlock()
		return zero, err
	}
	current, ok := state.queues[key]
	if !ok {
		current = make(chan struct{})
		close(current) // no pending mutation: immediately runnable
	}
	next := make(chan struct{})
	state.queues[key] = next
	state.mu.Unlock()

	<-current // wait for the previous mutation to release the slot
	defer func() {
		close(next)
		state.mu.Lock()
		if state.queues[key] == next {
			delete(state.queues, key)
		}
		state.mu.Unlock()
	}()
	return fn()
}
