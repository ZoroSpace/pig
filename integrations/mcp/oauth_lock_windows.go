//go:build windows

package mcp

import (
	"time"
)

// flockFile on windows is a process-local mutex: LockFileEx plumbing is not
// worth it for a leaf package (declared deviation, S12-4 §0.3) — refreshes
// serialize in-process only, and concurrent processes rely on the server
// tolerating an occasional doubled refresh.
type flockFile struct{}

func (f *flockFile) lock(path string) error { return nil }

func (f *flockFile) unlock() {}

func withFlockWait(path string, wait time.Duration, fn func() error) error {
	return fn()
}
