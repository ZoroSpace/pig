// pi: stream-fn.ts.
package agent

import (
	"errors"
	"sync"
)

var (
	defaultStreamFnMu sync.Mutex
	defaultStreamFn   StreamFn
)

// SetDefaultStreamFn configures the fallback used by Agent and low-level
// loops when callers omit streamFn.
//
// Hosts that provide a default model runtime can install its stream function
// here without making the agent package depend on a provider catalog or
// compatibility layer.
func SetDefaultStreamFn(streamFn StreamFn) {
	defaultStreamFnMu.Lock()
	defer defaultStreamFnMu.Unlock()
	defaultStreamFn = streamFn
}

// GetDefaultStreamFn returns the configured fallback. pi throws when none is
// configured; Go returns an error instead.
func GetDefaultStreamFn() (StreamFn, error) {
	defaultStreamFnMu.Lock()
	defer defaultStreamFnMu.Unlock()
	if defaultStreamFn == nil {
		return nil, errors.New("No default stream function configured. Pass streamFn explicitly or call setDefaultStreamFn().")
	}
	return defaultStreamFn, nil
}
