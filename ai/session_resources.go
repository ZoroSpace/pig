// pi: src/session-resources.ts — registry of per-session resource cleanups
// invoked when a session is disposed.
package ai

import (
	"fmt"
	"sync"
)

// SessionResourceCleanup is one per-session cleanup callback (pi:
// SessionResourceCleanup). sessionId "" = pi's undefined.
type SessionResourceCleanup func(sessionID string)

var (
	sessionResourceCleanupsMu sync.Mutex
	sessionResourceCleanups   = map[*SessionResourceCleanup]struct{}{}
)

// RegisterSessionResourceCleanup registers a cleanup and returns the
// unregister function (pi: registerSessionResourceCleanup). The returned
// function removes exactly this registration (Set.delete semantics).
func RegisterSessionResourceCleanup(cleanup SessionResourceCleanup) func() {
	sessionResourceCleanupsMu.Lock()
	defer sessionResourceCleanupsMu.Unlock()
	sessionResourceCleanups[&cleanup] = struct{}{}
	key := &cleanup
	return func() {
		sessionResourceCleanupsMu.Lock()
		defer sessionResourceCleanupsMu.Unlock()
		delete(sessionResourceCleanups, key)
	}
}

// CleanupSessionResources runs every registered cleanup for the session (pi:
// cleanupSessionResources). pi throws an AggregateError when cleanups fail;
// pig collects the errors into a single joined error.
func CleanupSessionResources(sessionID string) error {
	sessionResourceCleanupsMu.Lock()
	keys := make([]*SessionResourceCleanup, 0, len(sessionResourceCleanups))
	for key := range sessionResourceCleanups {
		keys = append(keys, key)
	}
	sessionResourceCleanupsMu.Unlock()

	var errs []error
	for _, key := range keys {
		func() {
			defer func() {
				if recovered := recover(); recovered != nil {
					errs = append(errs, fmt.Errorf("%v", recovered))
				}
			}()
			(*key)(sessionID)
		}()
	}
	if len(errs) > 0 {
		return fmt.Errorf("Failed to cleanup session resources: %v", errs)
	}
	return nil
}
