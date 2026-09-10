// pi: src/core/tools/file-mutation-queue.ts — coding-agent mutation
// serialization (global map keyed by absolute path, unlike the agent
// package's per-env WeakMap; pig mirrors the difference).
//
// Segment deviations:
//   - pi chains promises per absolute path; pig uses per-key "token"
//     channels closed to release the next waiter, with the registration
//     serialized under a package mutex (pi: registrationQueue chain).
//   - isMissingPathError checks ENOENT/ENOTDIR from node errors; pig checks
//     the os error against syscall.ENOENT/ENOTDIR.
package tools

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"syscall"
)

// fileMutationQueues keys the per-path queues (pi:
// fileMutationQueues: Map<string, Promise<void>>).
var fileMutationQueues = struct {
	sync.Mutex
	queues map[string]chan struct{}
}{queues: map[string]chan struct{}{}}

// isMissingPathError reports whether the error is a missing-path failure (pi:
// isMissingPathError).
func isMissingPathError(err error) bool {
	return errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.ENOTDIR)
}

// WithFileMutationQueue serializes file mutations targeting the same
// absolute path (pi: withFileMutationQueue). If the canonicalization fails
// with a missing-path error the raw absolute path is used (pi: the ENOENT/
// ENOTDIR fallback); other failures propagate.
func WithFileMutationQueue[T any](ctx context.Context, absolutePath string, fn func() (T, error)) (T, error) {
	var zero T
	// pi uses realpath, which also resolves path components; EvalSymlinks
	// is the Go equivalent (it errors with ENOENT-style errors for missing
	// components, matching isMissingPathError).
	canonical, err := filepath.EvalSymlinks(absolutePath)
	if err != nil {
		if isMissingPathError(err) {
			canonical = absolutePath
		} else {
			return zero, err
		}
	}

	fileMutationQueues.Lock()
	current, ok := fileMutationQueues.queues[canonical]
	if !ok {
		current = make(chan struct{})
		close(current) // no pending mutation: immediately runnable
	}
	next := make(chan struct{})
	fileMutationQueues.queues[canonical] = next
	fileMutationQueues.Unlock()

	<-current // wait for the previous mutation to release the slot
	defer func() {
		close(next)
		fileMutationQueues.Lock()
		if fileMutationQueues.queues[canonical] == next {
			delete(fileMutationQueues.queues, canonical)
		}
		fileMutationQueues.Unlock()
	}()
	return fn()
}
