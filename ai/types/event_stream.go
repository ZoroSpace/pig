package types

import (
	"iter"
	"sync"
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
type AssistantMessageEventStream struct {
	*EventStream[AssistantMessageEvent, AssistantMessage]
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
	}
}
