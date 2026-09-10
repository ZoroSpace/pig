// pi: src/harness/events.ts — harness run lifecycle events and event bus.
//
// Segment deviations:
//   - TS discriminated union HarnessEvent → HarnessEvent interface +
//     *RunStartEvent/*RunEndEvent concrete types (pointer receivers keep the
//     EventType probe callable on the nil typed-nil used by OnTyped).
//   - pi's generic on<TType> / watch<TSnapshot> → non-generic Events.On plus
//     package-level generic helpers OnTyped[TEvent] / Watch[TSnapshot]: Go
//     interfaces cannot declare generic methods, and methods cannot add type
//     parameters. The generic helpers preserve pi's call-site shape.
//   - pi's Promise<void> listeners → synchronous callbacks (per the callback
//     rule); emit stays synchronous and never awaits.
//   - pi relies on the single-threaded event loop for bus state; pig guards
//     listener/watch state with a mutex. Listeners are snapshotted under the
//     lock and invoked outside it, so listeners added or removed during
//     delivery are not observed by the in-flight emit (matching Set
//     iteration semantics) and reentrant Emit from a listener preserves the
//     nested-delivery order of TS's synchronous loop.
//   - pi's WatchHandle<TSnapshot> object literal → exported Snapshot field +
//     Start/Unsubscribe methods.
package harness

import "sync"

// HarnessEventType discriminates the HarnessEvent union (pi:
// HarnessEventType).
type HarnessEventType string

const (
	HarnessEventRunStart HarnessEventType = "run_start"
	HarnessEventRunEnd   HarnessEventType = "run_end"
)

// RunOutcome is the terminal outcome of a run (pi: RunEndEvent["outcome"]).
type RunOutcome string

const (
	RunOutcomeCompleted RunOutcome = "completed"
	RunOutcomeAborted   RunOutcome = "aborted"
	RunOutcomeFailed    RunOutcome = "failed"
)

// RunStartEvent is emitted when a run starts (pi: RunStartEvent).
type RunStartEvent struct {
	Type  HarnessEventType `json:"type"`
	Lane  string           `json:"lane"`
	RunID string           `json:"runId"`
}

// EventType implements HarnessEvent (pi: the "run_start" discriminator).
func (*RunStartEvent) EventType() HarnessEventType { return HarnessEventRunStart }

// RunEndEvent is emitted when a run ends (pi: RunEndEvent).
type RunEndEvent struct {
	Type    HarnessEventType `json:"type"`
	Lane    string           `json:"lane"`
	RunID   string           `json:"runId"`
	Outcome RunOutcome       `json:"outcome"`
	LeafID  string           `json:"leafId"`
}

// EventType implements HarnessEvent (pi: the "run_end" discriminator).
func (*RunEndEvent) EventType() HarnessEventType { return HarnessEventRunEnd }

// HarnessEvent is the union of run lifecycle events (pi: HarnessEvent).
type HarnessEvent interface {
	EventType() HarnessEventType
}

// HarnessEventListener observes harness events (pi: HarnessEventListener).
// pi's Promise<void> return collapses to a synchronous callback.
type HarnessEventListener func(event HarnessEvent)

// Events is the passive event subscription surface (pi: interface Events).
type Events interface {
	// On registers a passive listener for future events and returns its
	// unsubscribe function. Earlier events are not replayed and no
	// current-state snapshot is provided; use a lane or session watch for
	// both. pi's generic on<TType> collapses to a type-erased listener;
	// OnTyped restores the typed call shape.
	On(eventType HarnessEventType, listener HarnessEventListener) (unsubscribe func())
}

// OnTyped is pi's generic Events.on<TType> with the type parameter restored
// at the call site: Go interfaces cannot declare generic methods, so the
// type assertion moves into this package-level helper. TEvent must be a
// concrete event type (*RunStartEvent or *RunEndEvent).
func OnTyped[TEvent HarnessEvent](e Events, listener func(TEvent)) func() {
	var probe TEvent
	return e.On(probe.EventType(), func(event HarnessEvent) {
		if typed, ok := event.(TEvent); ok {
			listener(typed)
		}
	})
}

// WatchHandle is a watch subscription handle (pi: WatchHandle<TSnapshot>).
type WatchHandle[TSnapshot any] struct {
	// Snapshot is the captured at-subscription state snapshot (pi:
	// watch's captureSnapshot result, taken after the receive function is
	// registered so events emitted during capture are buffered, not lost).
	Snapshot TSnapshot

	mu       sync.Mutex
	listener HarnessEventListener
	buffered []HarnessEvent

	bus *HarnessEventBus
	id  int
}

// Start delivers buffered events and then live events to listener (pi:
// WatchHandle.start).
func (h *WatchHandle[TSnapshot]) Start(listener HarnessEventListener) {
	h.mu.Lock()
	buffered := h.buffered
	h.buffered = nil
	for len(buffered) > 0 {
		// Stay in buffering mode while flushing so reentrant emissions
		// preserve order (pi's while (buffered.length > 0) loop).
		h.mu.Unlock()
		for _, event := range buffered {
			listener(event)
		}
		h.mu.Lock()
		buffered = h.buffered
		h.buffered = nil
	}
	h.listener = listener
	h.mu.Unlock()
}

// Unsubscribe removes the watch from the bus and discards the buffer (pi:
// WatchHandle.unsubscribe). The live listener is not cleared, matching pi.
func (h *WatchHandle[TSnapshot]) Unsubscribe() {
	h.bus.removeWatch(h.id)
	h.mu.Lock()
	h.buffered = nil
	h.mu.Unlock()
}

// Watch registers a watch subscription and captures the snapshot (pi:
// HarnessEventBus.watch<TSnapshot>). The generic type parameter moves to this
// package-level function because Go methods cannot be generic.
func Watch[TSnapshot any](bus *HarnessEventBus, captureSnapshot func() TSnapshot) *WatchHandle[TSnapshot] {
	h := &WatchHandle[TSnapshot]{bus: bus}
	h.id = bus.addWatch(func(event HarnessEvent) {
		h.mu.Lock()
		if h.listener != nil {
			listener := h.listener
			h.mu.Unlock()
			listener(event)
			return
		}
		h.buffered = append(h.buffered, event)
		h.mu.Unlock()
	})
	h.Snapshot = captureSnapshot()
	return h
}

// HarnessEventBus fans run lifecycle events out to direct per-type listeners
// and watch subscriptions (pi: HarnessEventBus).
type HarnessEventBus struct {
	mu sync.Mutex
	// listeners keeps insertion order per event type (pi: Set iteration).
	listeners map[HarnessEventType][]listenerEntry
	// watchListeners keeps insertion order (pi: Set of receive functions).
	watchListeners []watchEntry
	nextID         int
}

type listenerEntry struct {
	id       int
	listener HarnessEventListener
}

type watchEntry struct {
	id      int
	receive func(HarnessEvent)
}

// NewHarnessEventBus builds an empty bus (pi: constructor).
func NewHarnessEventBus() *HarnessEventBus {
	return &HarnessEventBus{listeners: make(map[HarnessEventType][]listenerEntry)}
}

// On registers a listener for future events of one type and returns its
// unsubscribe function. Earlier events are not replayed, and no snapshot or
// event buffer is provided (pi: HarnessEventBus.on<TType>).
func (b *HarnessEventBus) On(eventType HarnessEventType, listener HarnessEventListener) func() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.nextID++
	id := b.nextID
	b.listeners[eventType] = append(b.listeners[eventType], listenerEntry{id: id, listener: listener})
	return func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		b.removeLocked(eventType, id)
	}
}

// removeLocked deletes one listener and drops the type key when its set
// empties (pi: "if (listeners.size === 0) this.listeners.delete(type)").
func (b *HarnessEventBus) removeLocked(eventType HarnessEventType, id int) {
	entries := b.listeners[eventType]
	for i, entry := range entries {
		if entry.id == id {
			entries = append(entries[:i], entries[i+1:]...)
			break
		}
	}
	if len(entries) == 0 {
		delete(b.listeners, eventType)
	} else {
		b.listeners[eventType] = entries
	}
}

// Emit publishes an event to current event subscriptions and watch
// subscriptions (pi: HarnessEventBus.emit). Delivery is synchronous:
// per-type listeners first, then every watcher.
func (b *HarnessEventBus) Emit(event HarnessEvent) {
	b.mu.Lock()
	direct := make([]HarnessEventListener, len(b.listeners[event.EventType()]))
	for i, entry := range b.listeners[event.EventType()] {
		direct[i] = entry.listener
	}
	watchers := make([]func(HarnessEvent), len(b.watchListeners))
	for i, entry := range b.watchListeners {
		watchers[i] = entry.receive
	}
	b.mu.Unlock()

	// Deliver only to direct listeners registered for this event type.
	for _, listener := range direct {
		listener(event)
	}
	// Deliver every event to each watcher; Watch buffers until Start.
	for _, receive := range watchers {
		receive(event)
	}
}

// addWatch registers a watch receive function before the snapshot is
// captured and returns its identity (pi: watchListeners.add(receive)).
func (b *HarnessEventBus) addWatch(receive func(HarnessEvent)) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.nextID++
	id := b.nextID
	b.watchListeners = append(b.watchListeners, watchEntry{id: id, receive: receive})
	return id
}

// removeWatch drops a watch receive function (pi:
// watchListeners.delete(receive)).
func (b *HarnessEventBus) removeWatch(id int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i, entry := range b.watchListeners {
		if entry.id == id {
			b.watchListeners = append(b.watchListeners[:i], b.watchListeners[i+1:]...)
			break
		}
	}
}
