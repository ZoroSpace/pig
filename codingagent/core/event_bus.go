// pi: core/event-bus.ts — generic channel pub/sub used by the extension
// loader and resource loader. pi wraps handlers in an async function and
// swallows errors via console.error; pig keeps the same observable contract
// with a synchronous dispatch and recover (declared deviation: no event-loop
// deferral — the emit caller's goroutine runs handlers directly).
package core

import (
	"fmt"
	"sync"
)

// EventBus is the channel pub/sub interface (pi: EventBus).
type EventBus interface {
	Emit(channel string, data any)
	On(channel string, handler func(data any)) (unsubscribe func())
}

// EventBusController adds clearing all listeners (pi: EventBusController).
type EventBusController interface {
	EventBus
	Clear()
}

type eventBus struct {
	mu        sync.Mutex
	listeners map[string][]func(data any)
}

// CreateEventBus creates the shared event bus (pi: createEventBus).
func CreateEventBus() EventBusController {
	return &eventBus{listeners: map[string][]func(data any){}}
}

func (b *eventBus) Emit(channel string, data any) {
	b.mu.Lock()
	handlers := make([]func(data any), len(b.listeners[channel]))
	copy(handlers, b.listeners[channel])
	b.mu.Unlock()
	for _, handler := range handlers {
		func() {
			// pi's async safeHandler swallows handler errors via
			// console.error; pig recovers panics the same way.
			defer func() {
				if r := recover(); r != nil {
					fmt.Printf("Event handler error (%s): %v\n", channel, r)
				}
			}()
			handler(data)
		}()
	}
}

func (b *eventBus) On(channel string, handler func(data any)) (unsubscribe func()) {
	b.mu.Lock()
	b.listeners[channel] = append(b.listeners[channel], handler)
	index := len(b.listeners[channel]) - 1
	b.mu.Unlock()
	return func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		// pi's off removes the exact handler; pig removes by slot (handlers
		// are never reordered within a channel).
		if handlers, ok := b.listeners[channel]; ok && index < len(handlers) {
			b.listeners[channel] = append(handlers[:index], handlers[index+1:]...)
		}
	}
}

func (b *eventBus) Clear() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.listeners = map[string][]func(data any){}
}
