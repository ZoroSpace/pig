//go:build !windows

package mcp

import (
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// flockFile serializes credential-file access within the process and, via
// flock, across processes. The kernel releases the lock when the process
// dies, so there is no stale-lock takeover to implement (pi's proper-lockfile
// stale/renew semantics collapse into one syscall).
type flockFile struct {
	fd        int
	keepalive *os.File
}

func (f *flockFile) lock(path string) error {
	if f.fd != 0 {
		return nil // already held (reentrant within one goroutine chain)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	fd, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	if err := syscall.Flock(int(fd.Fd()), syscall.LOCK_EX); err != nil {
		fd.Close()
		return err
	}
	f.fd = int(fd.Fd())
	f.keepalive = fd
	return nil
}

func (f *flockFile) unlock() {
	if f.fd == 0 {
		return
	}
	fd := f.keepalive
	_ = syscall.Flock(f.fd, syscall.LOCK_UN)
	_ = fd.Close()
	f.fd = 0
	f.keepalive = nil
}

// withFlockWait takes an exclusive flock on path (creating it), waiting up to
// wait before giving up, then runs fn.
func withFlockWait(path string, wait time.Duration, fn func() error) error {
	fd, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer fd.Close()
	deadline := time.Now().Add(wait)
	for {
		err := syscall.Flock(int(fd.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			return errLockTimeout
		}
		time.Sleep(100 * time.Millisecond)
	}
	defer syscall.Flock(int(fd.Fd()), syscall.LOCK_UN)
	return fn()
}
