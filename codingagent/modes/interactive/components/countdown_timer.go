// countdown_timer.go (pi: components/countdown-timer.ts) — reusable
// countdown timer for dialog components.
//
// Concurrency adaptation (declared): pi uses setInterval on the JS event
// loop; pig runs a ticker goroutine. onTick/onExpire fire on the timer
// goroutine (pi fires on the event loop); callbacks must be safe for that.
package components

import (
	"sync"
	"time"

	"github.com/ZoroSpace/pig/tui"
)

// CountdownTimer ticks once per second until expiry (pi: class
// CountdownTimer).
type CountdownTimer struct {
	mu               sync.Mutex
	stopCh           chan struct{}
	stopped          bool
	remainingSeconds int
	tui              tui.TUI
	onTick           func(seconds int)
	onExpire         func()
}

// NewCountdownTimer starts the countdown (pi: constructor). tui may be
// nil; onTick fires immediately with the initial remaining seconds.
func NewCountdownTimer(timeoutMs int, ui tui.TUI, onTick func(seconds int), onExpire func()) *CountdownTimer {
	c := &CountdownTimer{
		stopCh:           make(chan struct{}),
		remainingSeconds: ceilDiv(timeoutMs, 1000),
		tui:              ui,
		onTick:           onTick,
		onExpire:         onExpire,
	}
	onTick(c.remainingSeconds)

	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-c.stopCh:
				return
			case <-ticker.C:
				c.mu.Lock()
				if c.stopped {
					c.mu.Unlock()
					return
				}
				c.remainingSeconds--
				remaining := c.remainingSeconds
				c.mu.Unlock()

				onTick(remaining)
				if c.tui != nil {
					c.tui.RequestRender(false)
				}

				if remaining <= 0 {
					c.Dispose()
					onExpire()
					return
				}
			}
		}
	}()
	return c
}

// Dispose stops the countdown (pi: dispose). Safe to call twice.
func (c *CountdownTimer) Dispose() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stopped {
		return
	}
	c.stopped = true
	close(c.stopCh)
}

// RemainingSeconds returns the current countdown value.
func (c *CountdownTimer) RemainingSeconds() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.remainingSeconds
}

// ceilDiv is Math.ceil(timeoutMs / 1000).
func ceilDiv(a, b int) int {
	return (a + b - 1) / b
}
