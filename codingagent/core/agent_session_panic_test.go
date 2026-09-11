// codingagent/core/agent_session_panic_test.go — regression tests for pig
// panic containment in AgentSession.emit (pig-internal feature; see
// docs/feat/PANIC-RESILIENCE.md). Before the containment, a panicking
// listener killed the event pump goroutine and the whole process.
package core

import (
	"testing"
	"time"
)

func TestEmitContainsListenerPanic(t *testing.T) {
	// White-box: emit only touches mu/eventListeners/listenerPanicObserver,
	// so a zero-value session exercises the dispatch contract.
	session := &AgentSession{}

	got := make(chan *AgentSessionEvent, 1)
	session.Subscribe(func(event *AgentSessionEvent) { got <- event })
	session.Subscribe(func(event *AgentSessionEvent) { panic("listener boom") })

	observed := make(chan string, 1)
	session.SetListenerPanicObserver(func(_ *AgentSessionEvent, logPath string) { observed <- logPath })

	// Without the emit() containment this call panics and the test dies.
	session.emit(&AgentSessionEvent{})

	select {
	case event := <-got:
		if event == nil {
			t.Fatal("collected listener received a nil event")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("listener registered before the panicking one never received the event")
	}

	select {
	case logPath := <-observed:
		if logPath == "" {
			t.Fatal("observer received an empty panic log path")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("listener panic observer was not invoked")
	}
}

func TestEmitSurvivesMultiplePanickingListeners(t *testing.T) {
	session := &AgentSession{}
	finished := make(chan struct{}, 1)
	for i := 0; i < 3; i++ {
		session.Subscribe(func(event *AgentSessionEvent) { panic("boom") })
	}
	session.Subscribe(func(event *AgentSessionEvent) { finished <- struct{}{} })

	session.emit(&AgentSessionEvent{})

	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("listener after panicking listeners never ran")
	}
}
