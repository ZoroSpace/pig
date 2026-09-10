// pi: test/harness/events.test.ts — harness event bus delivery semantics.
package harness_test

import (
	"testing"

	"github.com/ZoroSpace/pig/agent/harness"
)

func TestDeliversMatchingEventsToDirectListenersAndWatchers(t *testing.T) {
	events := harness.NewHarnessEventBus()
	var direct []*harness.RunStartEvent
	var watchEvents []harness.HarnessEvent
	off := harness.OnTyped(events, func(event *harness.RunStartEvent) {
		direct = append(direct, event)
	})
	watch := harness.Watch(events, func() any { return nil })
	watch.Start(func(event harness.HarnessEvent) {
		watchEvents = append(watchEvents, event)
	})

	runStartEvent := &harness.RunStartEvent{
		Type:  harness.HarnessEventRunStart,
		Lane:  "main",
		RunID: "run-1",
	}
	runEndEvent := &harness.RunEndEvent{
		Type:    harness.HarnessEventRunEnd,
		Lane:    "main",
		RunID:   "run-1",
		Outcome: harness.RunOutcomeCompleted,
		LeafID:  "entry-1",
	}

	events.Emit(runStartEvent)
	events.Emit(runEndEvent)
	off()
	events.Emit(runStartEvent)

	if len(direct) != 1 || direct[0] != runStartEvent {
		t.Fatalf("direct = %v, want [%v]", direct, runStartEvent)
	}
	if len(watchEvents) != 3 {
		t.Fatalf("watchEvents = %v, want 3 events", watchEvents)
	}
	if watchEvents[0] != harness.HarnessEvent(runStartEvent) ||
		watchEvents[1] != harness.HarnessEvent(runEndEvent) ||
		watchEvents[2] != harness.HarnessEvent(runStartEvent) {
		t.Fatalf("watchEvents order mismatch: %v", watchEvents)
	}
}

func TestCapturesSnapshotWithoutEventGapThenFlushesAndDeliversLiveEvents(t *testing.T) {
	events := harness.NewHarnessEventBus()
	runStartEvent := &harness.RunStartEvent{
		Type:  harness.HarnessEventRunStart,
		Lane:  "main",
		RunID: "run-1",
	}
	runEndEvent := &harness.RunEndEvent{
		Type:    harness.HarnessEventRunEnd,
		Lane:    "main",
		RunID:   "run-1",
		Outcome: harness.RunOutcomeCompleted,
		LeafID:  "entry-1",
	}
	type snapshot struct {
		LeafID *string
	}
	expectedSnapshot := snapshot{}

	var received []harness.HarnessEvent
	watch := harness.Watch(events, func() snapshot {
		// Events emitted during snapshot capture are buffered, not dropped.
		snap := expectedSnapshot
		events.Emit(runStartEvent)
		return snap
	})

	if watch.Snapshot != expectedSnapshot {
		t.Fatalf("watch.Snapshot = %v, want %v", watch.Snapshot, expectedSnapshot)
	}
	if len(received) != 0 {
		t.Fatalf("received = %v, want empty before Start", received)
	}

	watch.Start(func(event harness.HarnessEvent) {
		received = append(received, event)
	})
	if len(received) != 1 || received[0] != harness.HarnessEvent(runStartEvent) {
		t.Fatalf("received after Start = %v, want [runStartEvent]", received)
	}

	events.Emit(runEndEvent)
	if len(received) != 2 || received[1] != harness.HarnessEvent(runEndEvent) {
		t.Fatalf("received after live emit = %v, want [runStartEvent, runEndEvent]", received)
	}

	watch.Unsubscribe()
	events.Emit(runStartEvent)
	if len(received) != 2 {
		t.Fatalf("received after Unsubscribe = %v, want no further delivery", received)
	}
}
