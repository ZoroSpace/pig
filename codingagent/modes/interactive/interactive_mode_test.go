// interactive_mode_test.go (pi: test/interactive-mode-startup-input.test.ts
// subset).
//
// pi calls the prototype's private methods with stub `this` objects; Go
// methods are bound to the concrete struct, so the cases that need a stub
// session (handleStartupSubmit's setText/showStatus assertions and
// processSubmitText's pendingUserInputs queueing) land with the T5b-2
// session test seams. The input gate — pig's pendingUserInputs +
// onInputCallback equivalent — is covered directly here.
package interactive

import (
	"testing"
	"time"
)

func TestUserInputGate(t *testing.T) {
	t.Run("returns queued startup input before installing a new input callback", func(t *testing.T) {
		gate := &userInputGate{pending: []string{"queued prompt"}}

		got := gate.wait()
		if got != "queued prompt" {
			t.Fatalf("wait() = %q, want %q", got, "queued prompt")
		}
		if len(gate.pending) != 0 {
			t.Fatalf("pending = %v, want empty", gate.pending)
		}
	})

	t.Run("delivers a submit to a waiting run loop", func(t *testing.T) {
		gate := &userInputGate{}
		gotCh := make(chan string, 1)
		go func() { gotCh <- gate.wait() }()

		// Bounded poll until the waiter is installed (repo precedent for
		// in-flight windows; do not sleep a fixed value first).
		deadline := time.Now().Add(2 * time.Second)
		for {
			gate.mu.Lock()
			installed := gate.waiter != nil
			gate.mu.Unlock()
			if installed || time.Now().After(deadline) {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}

		gate.submit("early prompt")

		select {
		case got := <-gotCh:
			if got != "early prompt" {
				t.Fatalf("wait() = %q, want %q", got, "early prompt")
			}
		case <-time.After(2 * time.Second):
			t.Fatal("wait() never returned")
		}
	})

	t.Run("queues submits when no run loop is waiting", func(t *testing.T) {
		gate := &userInputGate{}
		gate.submit("first")
		gate.submit("second")

		if got := gate.wait(); got != "first" {
			t.Fatalf("wait() = %q, want %q (FIFO)", got, "first")
		}
		if got := gate.wait(); got != "second" {
			t.Fatalf("wait() = %q, want %q", got, "second")
		}
	})
}
