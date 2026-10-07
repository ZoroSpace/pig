package types

import (
	"iter"
	"sync"
	"time"

	"github.com/samber/lo"
)

// EventStream is a push-based stream with async iteration and a final result
// (pi: EventStream<T, R> from utils/event-stream.ts). It lives beside the
// event protocol in package types; pi re-exports it from types.ts and the
// agent package consumes it through pi-ai.
//
// Semantics match pi: Push is ignored once the stream is done; a terminal
// event still reaches consumers before iteration stops; End flushes waiting
// consumers while queued events remain consumable; Result resolves at most
// once (first resolution wins, like a TS Promise).
//
// One deviation: pi's End without a result leaves result() pending forever;
// Go resolves the zero R instead, because a blocked goroutine cannot be
// garbage-collected away.

type streamDelivery[E any] struct {
	value E
	done  bool
}

type EventStream[E any, R any] struct {
	queue   []E
	waiting []chan streamDelivery[E]
	done    bool

	isComplete    func(E) bool
	extractResult func(E) R

	mu       sync.Mutex
	resultCh chan R
	resolved bool
}

// NewEventStream creates a stream. isComplete marks the terminal event;
// extractResult derives the final result from it.
func NewEventStream[E any, R any](isComplete func(E) bool, extractResult func(E) R) *EventStream[E, R] {
	return &EventStream[E, R]{
		isComplete:    isComplete,
		extractResult: extractResult,
		resultCh:      make(chan R, 1),
	}
}

// Push delivers one event, resolving the final result when the event is
// terminal. Ignored once the stream is done.
func (s *EventStream[E, R]) Push(event E) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.done {
		return
	}
	if s.isComplete(event) {
		s.done = true
		s.resolve(s.extractResult(event))
	}
	if len(s.waiting) > 0 {
		ch := s.waiting[0]
		s.waiting = s.waiting[1:]
		ch <- streamDelivery[E]{value: event}
		return
	}
	s.queue = append(s.queue, event)
}

// End closes the stream. Waiting consumers see iteration stop; queued events
// remain consumable. If no terminal event or EndWith resolved the result
// first, Result resolves to the zero R (see the deviation note on EventStream).
func (s *EventStream[E, R]) End() {
	var zero R
	s.endWith(zero)
}

// EndWith closes the stream and resolves Result.
func (s *EventStream[E, R]) EndWith(result R) {
	s.endWith(result)
}

func (s *EventStream[E, R]) endWith(result R) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.done = true
	s.resolve(result)
	for _, ch := range s.waiting {
		ch <- streamDelivery[E]{done: true}
	}
	s.waiting = nil
}

func (s *EventStream[E, R]) resolve(result R) {
	if !s.resolved {
		s.resolved = true
		s.resultCh <- result
	}
}

// Result blocks until the final result is available.
func (s *EventStream[E, R]) Result() R {
	return <-s.resultCh
}

// Events iterates the stream. Breaking out of the range stops consuming; the
// producer is unaffected.
func (s *EventStream[E, R]) Events() iter.Seq[E] {
	return func(yield func(E) bool) {
		for {
			s.mu.Lock()
			if len(s.queue) > 0 {
				event := s.queue[0]
				s.queue = s.queue[1:]
				s.mu.Unlock()
				if !yield(event) {
					return
				}
				continue
			}
			if s.done {
				s.mu.Unlock()
				return
			}
			ch := make(chan streamDelivery[E], 1)
			s.waiting = append(s.waiting, ch)
			s.mu.Unlock()
			delivery := <-ch
			if delivery.done {
				return
			}
			if !yield(delivery.value) {
				return
			}
		}
	}
}

// AssistantMessageEventStream is an EventStream whose terminal events are
// done and error, and whose result is the final AssistantMessage (pi:
// AssistantMessageEventStream). Both done and error carry it in Message.
//
// It also times the response: the final message (done or error event, or the
// result passed to EndWith) gets DurationMs, measured with a monotonic clock
// from the stream's creation, unless the message already has one or its
// Timestamp predates the stream. A stream that forwards a response which
// started elsewhere, such as a deferred result fetched later, therefore
// leaves it untimed. pi keeps the start in a WeakMap so the class stays
// structurally a plain EventStream (3ba22ce17); Go's concrete struct has no
// assignability constraint, so plain fields carry it. One further concurrency
// deviation: pi stamps the message object in place, but pig's producer and
// consumer run on different goroutines, so Push stamps a copy before
// publishing it and the caller's object is left untouched — consumers see the
// stamp through the events and Result().
type AssistantMessageEventStream struct {
	*EventStream[AssistantMessageEvent, AssistantMessage]
	// startedAtWall is the wall-clock start, for messages created before the
	// response could start (pi: streamStarts WeakMap entry).
	startedAtWall int64
	// startedAtMonotonic is the monotonic start (pi: performance.now()).
	startedAtMonotonic time.Time
}

// NewAssistantMessageEventStream is the createAssistantMessageEventStream
// factory counterpart.
func NewAssistantMessageEventStream() *AssistantMessageEventStream {
	return &AssistantMessageEventStream{
		EventStream: NewEventStream(
			func(event AssistantMessageEvent) bool {
				return event.Type == EventDone || event.Type == EventError
			},
			func(event AssistantMessageEvent) AssistantMessage {
				if event.Message != nil {
					return *event.Message
				}
				return AssistantMessage{}
			},
		),
		startedAtWall:      time.Now().UnixMilli(),
		startedAtMonotonic: time.Now(),
	}
}

// Push delivers one event, timing the final message first (pi: override
// push). The stamp lands on a copy of the message (see shouldTime).
func (s *AssistantMessageEventStream) Push(event AssistantMessageEvent) {
	if event.Message != nil && (event.Type == EventDone || event.Type == EventError) && s.shouldTime(event.Message) {
		stamped := *event.Message
		s.stampDuration(&stamped)
		event.Message = &stamped
	}
	s.EventStream.Push(event)
}

// EndWith closes the stream and resolves Result, timing the result first (pi:
// override end). The by-value parameter is already a copy, so stamping it
// never races a consumer holding the caller's object.
func (s *AssistantMessageEventStream) EndWith(result AssistantMessage) {
	if s.shouldTime(&result) {
		s.stampDuration(&result)
	}
	s.EventStream.EndWith(result)
}

// shouldTime reports whether this stream is responsible for timing the final
// message (pi: timeResponse's guards — the stream saw the response start, the
// message carries no duration yet, and it was not created before the stream).
func (s *AssistantMessageEventStream) shouldTime(message *AssistantMessage) bool {
	return !s.EventStream.done && message.DurationMs == nil && message.Timestamp >= s.startedAtWall
}

// stampDuration measures the response duration into the message (pi:
// timeResponse's assignment; monotonic clock).
func (s *AssistantMessageEventStream) stampDuration(message *AssistantMessage) {
	elapsedMs := time.Since(s.startedAtMonotonic).Milliseconds()
	if elapsedMs < 0 {
		elapsedMs = 0
	}
	message.DurationMs = lo.ToPtr(elapsedMs)
}
