// pi: core/output-guard.ts — stdout takeover for JSON/RPC output modes.
//
// Go adaptation (declared): Node swaps process.stdout.write so all writes
// route to stderr; pig reassigns the os.Stdout package variable (Go's fmt /
// os helpers resolve os.Stdout at call time) to the raw stderr file. Node's
// async write callbacks with ENOBUFS/EAGAIN retry have no Go equivalent —
// file writes are blocking — so the retry loop collapses to a direct write.
// The serialized write tail is kept: pig enqueues writes on a single
// goroutine and exposes the same backpressure/flush waits.
package core

import (
	"os"
	"sync"
)

// stdoutTakeoverState is the saved stdout (pi: StdoutTakeoverState).
type stdoutTakeoverState struct {
	rawStdout *os.File
	rawStderr *os.File
}

var (
	stdoutStateMu   sync.Mutex
	stdoutState     *stdoutTakeoverState
	rawStdoutMu     sync.Mutex
	rawStdoutTail   chan struct{}
	rawStdoutTailMu sync.Mutex
)

func getRawStdout() *os.File {
	stdoutStateMu.Lock()
	defer stdoutStateMu.Unlock()
	if stdoutState != nil {
		return stdoutState.rawStderr
	}
	return os.Stdout
}

// enqueueRawStdoutWrite runs fn after all previously enqueued writes.
func enqueueRawStdoutWrite(fn func()) chan struct{} {
	rawStdoutTailMu.Lock()
	prev := rawStdoutTail
	done := make(chan struct{})
	rawStdoutTail = done
	rawStdoutTailMu.Unlock()

	go func() {
		if prev != nil {
			<-prev
		}
		rawStdoutMu.Lock()
		fn()
		rawStdoutMu.Unlock()
		close(done)
	}()
	return done
}

// TakeOverStdout redirects process stdout to stderr (pi: takeOverStdout).
// Idempotent.
func TakeOverStdout() {
	stdoutStateMu.Lock()
	defer stdoutStateMu.Unlock()
	if stdoutState != nil {
		return
	}

	rawStdout := os.Stdout
	rawStderr := os.Stderr
	os.Stdout = rawStderr

	stdoutState = &stdoutTakeoverState{
		rawStdout: rawStdout,
		rawStderr: rawStderr,
	}
}

// RestoreStdout restores the original stdout (pi: restoreStdout).
func RestoreStdout() {
	stdoutStateMu.Lock()
	defer stdoutStateMu.Unlock()
	if stdoutState == nil {
		return
	}

	os.Stdout = stdoutState.rawStdout
	stdoutState = nil
}

// IsStdoutTakenOver reports whether stdout is currently taken over (pi:
// isStdoutTakenOver).
func IsStdoutTakenOver() bool {
	stdoutStateMu.Lock()
	defer stdoutStateMu.Unlock()
	return stdoutState != nil
}

// WriteRawStdout writes directly to the raw stdout (pi: writeRawStdout).
// pi exits the process when a write fails permanently; pig records the
// failure the same way (os.Exit(1)) inside the write goroutine.
func WriteRawStdout(text string) {
	if len(text) == 0 {
		return
	}
	enqueueRawStdoutWrite(func() {
		raw := getRawStdout()
		if _, err := raw.WriteString(text); err != nil {
			os.Exit(1)
		}
	})
}

// WaitForRawStdoutBackpressure waits for all queued writes to complete (pi:
// waitForRawStdoutBackpressure). pi rechecks for writes enqueued while
// waiting; pig samples the tail before and after the wait the same way.
func WaitForRawStdoutBackpressure() {
	for {
		rawStdoutTailMu.Lock()
		tail := rawStdoutTail
		rawStdoutTailMu.Unlock()
		if tail != nil {
			<-tail
		}
		rawStdoutTailMu.Lock()
		current := rawStdoutTail
		rawStdoutTailMu.Unlock()
		if tail == current {
			return
		}
	}
}

// FlushRawStdout waits for the tail and performs an empty write (pi:
// flushRawStdout).
func FlushRawStdout() {
	WaitForRawStdoutBackpressure()
	done := enqueueRawStdoutWrite(func() {
		raw := getRawStdout()
		_, _ = raw.WriteString("")
	})
	<-done
}
