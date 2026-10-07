package types

import (
	"iter"
	"reflect"
	"testing"
	"time"

	"github.com/samber/lo"
)

func TestEventStreamPushIterateResult(t *testing.T) {
	s := NewEventStream(
		func(ev int) bool { return ev == 3 },
		func(ev int) int { return ev * 10 },
	)
	go func() {
		s.Push(1)
		s.Push(2)
		s.Push(3)
		s.Push(4) // ignored: stream is done
	}()
	var got []int
	for ev := range s.Events() {
		got = append(got, ev)
	}
	if !reflect.DeepEqual(got, []int{1, 2, 3}) {
		t.Fatalf("events: %v", got)
	}
	if r := s.Result(); r != 30 {
		t.Fatalf("result: %d", r)
	}
}

func TestEventStreamEndFlushesWaiter(t *testing.T) {
	s := NewEventStream(func(ev int) bool { return false }, func(ev int) int { return ev })
	got := make(chan []int, 1)
	go func() {
		var out []int
		for ev := range s.Events() {
			out = append(out, ev)
		}
		got <- out
	}()
	s.Push(7)
	s.End()
	if out := <-got; len(out) != 1 || out[0] != 7 {
		t.Fatalf("events: %v", out)
	}
	var zero int
	if r := s.Result(); r != zero {
		t.Fatalf("result: %d", r)
	}
}

func TestEventStreamEndWith(t *testing.T) {
	s := NewEventStream(func(ev string) bool { return false }, func(ev string) string { return ev })
	s.EndWith("final")
	var got []string
	for ev := range s.Events() {
		got = append(got, ev)
	}
	if len(got) != 0 {
		t.Fatalf("events: %v", got)
	}
	if r := s.Result(); r != "final" {
		t.Fatalf("result: %q", r)
	}
}

func TestEventStreamFirstResolutionWins(t *testing.T) {
	s := NewEventStream(func(ev int) bool { return ev == 1 }, func(ev int) int { return ev })
	s.Push(1) // resolves result to 1
	s.EndWith(99)
	if r := s.Result(); r != 1 {
		t.Fatalf("result: %d", r)
	}
}

func TestAssistantMessageEventStream(t *testing.T) {
	s := NewAssistantMessageEventStream()
	msg := &AssistantMessage{
		Role:       RoleAssistant,
		Content:    []Content{NewTextContent("hi")},
		API:        ApiOpenAICompletions,
		Provider:   "openai",
		Model:      "gpt-x",
		StopReason: StopReasonStop,
	}
	go func() {
		s.Push(AssistantMessageEvent{Type: EventTextDelta, ContentIndex: 0, Delta: "h", Partial: msg})
		s.Push(AssistantMessageEvent{Type: EventDone, Reason: StopReasonStop, Message: msg})
	}()
	var deltas int
	for ev := range s.Events() {
		if ev.Type == EventTextDelta {
			deltas++
		}
	}
	if deltas != 1 {
		t.Fatalf("deltas: %d", deltas)
	}
	r := s.Result()
	if r.StopReason != StopReasonStop || r.Text() != "hi" {
		t.Fatalf("result: %+v", r)
	}
}

func TestAssistantMessageEventStreamError(t *testing.T) {
	s := NewAssistantMessageEventStream()
	errMsg := &AssistantMessage{
		Role:         RoleAssistant,
		Content:      []Content{},
		API:          ApiOpenAICompletions,
		Provider:     "openai",
		Model:        "gpt-x",
		StopReason:   StopReasonError,
		ErrorMessage: "boom",
	}
	s.Push(AssistantMessageEvent{Type: EventError, Reason: StopReasonError, Message: errMsg})
	r := s.Result()
	if r.StopReason != StopReasonError || r.ErrorMessage != "boom" {
		t.Fatalf("result: %+v", r)
	}
}

// --- pi v0.86.0 test/event-stream.test.ts (regression tests for pi #9055).
// pig's EventStream already uses the queue + waiting-consumer shape these
// tests pin; the FifoQueue optimization itself is N/A (channel-based
// implementation, see the S6-1 handoff).

// Puller turns Events() into single-step pulls for interleaved assertions.
type puller[E any] struct {
	events <-chan pullEvent[E]
}

type pullEvent[E any] struct {
	value E
	done  bool
}

func newPuller[E any](seq iter.Seq[E]) *puller[E] {
	events := make(chan pullEvent[E])
	go func() {
		defer close(events)
		seq(func(ev E) bool {
			events <- pullEvent[E]{value: ev}
			return true
		})
	}()
	return &puller[E]{events: events}
}

// waitForWaiters blocks until n consumers registered as waiting (pi's test
// relies on synchronous promise registration; pig registers from goroutines,
// so the test polls the internal state for determinism).
func waitForWaiters[E any, R any](t *testing.T, s *EventStream[E, R], n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		s.mu.Lock()
		count := len(s.waiting)
		s.mu.Unlock()
		if count >= n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("only %d/%d waiters registered", count, n)
		}
		time.Sleep(time.Millisecond)
	}
}

func (p *puller[E]) next(t *testing.T) (E, bool) {
	t.Helper()
	ev, ok := <-p.events
	if !ok {
		var zero E
		return zero, true
	}
	return ev.value, ev.done
}

func TestEventStreamDrainsBufferedInOrderAndIgnoresAfterCompletion(t *testing.T) {
	s := NewEventStream(
		func(event int) bool { return event == 3 },
		func(event int) int { return event },
	)
	s.Push(1)
	s.Push(2)
	s.Push(3)
	s.Push(4)

	if r := s.Result(); r != 3 {
		t.Fatalf("result = %d, want 3", r)
	}
	var events []int
	for event := range s.Events() {
		events = append(events, event)
	}
	if !reflect.DeepEqual(events, []int{1, 2, 3}) {
		t.Fatalf("events = %v", events)
	}
}

func TestEventStreamPreservesOrderWhenEventsArriveAfterDrainingStarts(t *testing.T) {
	s := NewEventStream(
		func(int) bool { return false },
		func(event int) int { return event },
	)
	s.Push(1)
	s.Push(2)

	puller := newPuller(s.Events())
	if v, done := puller.next(t); done || v != 1 {
		t.Fatalf("first = %d done=%v", v, done)
	}

	s.Push(3)
	if v, done := puller.next(t); done || v != 2 {
		t.Fatalf("second = %d done=%v", v, done)
	}
	if v, done := puller.next(t); done || v != 3 {
		t.Fatalf("third = %d done=%v", v, done)
	}

	s.EndWith(3)
	if _, done := puller.next(t); !done {
		t.Fatal("expected done after EndWith")
	}
}

func TestEventStreamDeliversToWaitingConsumersInRegistrationOrder(t *testing.T) {
	s := NewEventStream(
		func(int) bool { return false },
		func(event int) int { return event },
	)
	// Start the consumers one at a time and wait for each registration so
	// the waiter order is deterministic (pi registers synchronously).
	first := newPuller(s.Events())
	waitForWaiters(t, s, 1)
	second := newPuller(s.Events())
	waitForWaiters(t, s, 2)

	s.Push(1)
	s.Push(2)

	if v, done := first.next(t); done || v != 1 {
		t.Fatalf("first consumer = %d done=%v", v, done)
	}
	if v, done := second.next(t); done || v != 2 {
		t.Fatalf("second consumer = %d done=%v", v, done)
	}
}

func TestEventStreamDrainsBufferedAfterEndAndResolvesExplicitResult(t *testing.T) {
	s := NewEventStream(
		func(int) bool { return false },
		func(event int) string { return string(rune('0' + event)) },
	)
	s.Push(1)
	s.Push(2)
	s.EndWith("complete")

	if r := s.Result(); r != "complete" {
		t.Fatalf("result = %q", r)
	}
	var events []int
	for event := range s.Events() {
		events = append(events, event)
	}
	if !reflect.DeepEqual(events, []int{1, 2}) {
		t.Fatalf("events = %v", events)
	}
}

func TestEventStreamWakesAllWaitingConsumersOnEndWithoutResult(t *testing.T) {
	s := NewEventStream(
		func(int) bool { return false },
		func(event int) int { return event },
	)
	first := newPuller(s.Events())
	second := newPuller(s.Events())
	waitForWaiters(t, s, 2)

	s.End()

	if _, done := first.next(t); !done {
		t.Fatal("first consumer should see done")
	}
	if _, done := second.next(t); !done {
		t.Fatal("second consumer should see done")
	}
}

// message builds a minimal assistant message for timing tests (pi: the
// message() helper in event-stream.test.ts).
func timingTestMessage(timestamp int64, durationMs *int64) AssistantMessage {
	return AssistantMessage{
		Role:       RoleAssistant,
		Content:    []Content{},
		API:        ApiOpenAIResponses,
		Provider:   "openai",
		Model:      "m",
		Usage:      Usage{},
		StopReason: StopReasonStop,
		Timestamp:  timestamp,
		DurationMs: durationMs,
	}
}

// Timing tests from pi 36a686ee8 (#10549).
func TestAssistantMessageEventStreamTimesResponse(t *testing.T) {
	t.Run("sets durationMs on the final done or error message of a response it saw start", func(t *testing.T) {
		done := NewAssistantMessageEventStream()
		answer := timingTestMessage(time.Now().UnixMilli(), nil)
		time.Sleep(20 * time.Millisecond)
		done.Push(AssistantMessageEvent{Type: EventDone, Reason: StopReasonStop, Message: &answer})
		// pig stamps a copy before publishing (see the stream's doc comment);
		// consumers read the stamp through Result() and the events.
		measured := done.Result().DurationMs
		if measured == nil || *measured < 15 {
			t.Fatalf("result().DurationMs = %v, want >= 15", measured)
		}
		if answer.DurationMs != nil {
			t.Fatalf("answer.DurationMs = %v, want unset (the caller's object is not stamped)", *answer.DurationMs)
		}

		failed := NewAssistantMessageEventStream()
		errorMessage := timingTestMessage(time.Now().UnixMilli(), nil)
		errorMessage.StopReason = StopReasonError
		failed.Push(AssistantMessageEvent{Type: EventError, Reason: StopReasonError, Message: &errorMessage})
		if got := failed.Result().DurationMs; got == nil || *got < 0 {
			t.Fatalf("error result().DurationMs = %v, want set", got)
		}

		ended := NewAssistantMessageEventStream()
		result := timingTestMessage(time.Now().UnixMilli(), nil)
		ended.EndWith(result)
		// EndWith takes the message by value; consumers see the stamped copy
		// through Result() (pi mutates the same object reference).
		if got := ended.Result().DurationMs; got == nil || *got < 0 {
			t.Fatalf("result.DurationMs = %v, want set", got)
		}
	})

	t.Run("keeps an existing duration, so a forwarding stream keeps the inner measurement", func(t *testing.T) {
		outer := NewAssistantMessageEventStream()
		time.Sleep(20 * time.Millisecond)
		inner := NewAssistantMessageEventStream()
		answer := timingTestMessage(time.Now().UnixMilli(), nil)
		inner.Push(AssistantMessageEvent{Type: EventDone, Reason: StopReasonStop, Message: &answer})
		innerResult := inner.Result()
		innerMeasured := *innerResult.DurationMs
		// The already-stamped message is forwarded as-is, so the outer stream
		// keeps the inner measurement instead of re-timing.
		outer.Push(AssistantMessageEvent{Type: EventDone, Reason: StopReasonStop, Message: &innerResult})
		if got := outer.Result().DurationMs; got == nil || *got != innerMeasured {
			t.Fatalf("outer result().DurationMs = %v, want %d", got, innerMeasured)
		}
		if innerMeasured >= 20 {
			t.Fatalf("inner duration %d should stay under the outer stream's 20ms head start", innerMeasured)
		}

		preset := timingTestMessage(time.Now().UnixMilli(), lo.ToPtr(int64(1234)))
		stream := NewAssistantMessageEventStream()
		stream.Push(AssistantMessageEvent{Type: EventDone, Reason: StopReasonStop, Message: &preset})
		if got := stream.Result().DurationMs; got == nil || *got != 1234 {
			t.Fatalf("preset result().DurationMs = %v, want 1234", got)
		}
	})

	t.Run("leaves a message untimed when it started before the stream, such as a fetched deferred result", func(t *testing.T) {
		fetched := timingTestMessage(time.Now().Add(-time.Minute).UnixMilli(), nil)
		stream := NewAssistantMessageEventStream()
		stream.Push(AssistantMessageEvent{Type: EventDone, Reason: StopReasonStop, Message: &fetched})
		if fetched.DurationMs != nil {
			t.Fatalf("fetched.DurationMs = %v, want unset", *fetched.DurationMs)
		}
	})

	t.Run("does not time a message pushed after the stream completed", func(t *testing.T) {
		stream := NewAssistantMessageEventStream()
		answer := timingTestMessage(time.Now().UnixMilli(), nil)
		stream.Push(AssistantMessageEvent{Type: EventDone, Reason: StopReasonStop, Message: &answer})
		late := timingTestMessage(time.Now().UnixMilli(), nil)
		stream.Push(AssistantMessageEvent{Type: EventDone, Reason: StopReasonStop, Message: &late})
		if late.DurationMs != nil {
			t.Fatalf("late.DurationMs = %v, want unset", *late.DurationMs)
		}
	})
}
