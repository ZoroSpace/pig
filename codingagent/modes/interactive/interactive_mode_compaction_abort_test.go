// interactive_mode_compaction_abort_test.go (pi:
// test/interactive-mode-compaction.test.ts "routes interactive response
// aborts through AgentSession", pi v0.86.0 #9340) — restoreQueuedMessages-
// ToEditor with abort:true must abort at the session level (cancelling an
// active compaction/branch summary), not just the raw agent.
package interactive

import (
	"testing"
	"time"
)

func TestRestoreQueuedMessagesToEditorAbortsSessionOperation(t *testing.T) {
	initTestTheme(t)
	// An in-flight compaction is the observable session operation: only the
	// session-level abort cancels its context (agent.Abort() alone leaves
	// the compaction running). The compaction handler parks until released,
	// so IsCompacting stays true — the cancellation of the handler's
	// context is the observable.
	session, _, _, compactionReleased, handlerCtx := treeBusySession(t)
	defer close(compactionReleased)

	m := newSeamTestMode(session, &InteractiveModeOptions{})
	if !session.IsCompacting() {
		t.Fatal("expected isCompacting true before abort")
	}

	restored := m.restoreQueuedMessagesToEditor(true)
	if restored != 0 {
		t.Fatalf("restored = %d, want 0 (no queued messages)", restored)
	}

	select {
	case ctx := <-handlerCtx:
		deadline := time.Now().Add(2 * time.Second)
		for ctx.Err() == nil && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		if ctx.Err() == nil {
			t.Fatal("session-level abort did not cancel the active compaction")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("compaction handler never ran")
	}
}
