// tui/alt_screen_flash.go (pi: packages/tui/src/components/alt-screen-flash.ts)
//
// Transient messages composited by the alternate-screen renderer.
// Porting note: the per-entry setTimeout expiry runs on a goroutine under
// the TUI lock via the injected requestRender callback's owner; pig keeps
// entries mutex-guarded because Flash/Dispose/Render may race with the
// expiry goroutine.
package tui

import (
	"sync"
	"time"
)

const altScreenFlashDefaultDurationMs = 1000

type altScreenFlashEntry struct {
	id      int
	message string
	timer   *time.Timer
}

// AltScreenFlashContainer is the flash message stack (pi:
// AltScreenFlashContainer implements Component).
type AltScreenFlashContainer struct {
	mu            sync.Mutex
	entries       []*altScreenFlashEntry
	nextID        int
	requestRender func()
}

// NewAltScreenFlashContainer creates the container; requestRender is invoked
// whenever the visible set changes.
func NewAltScreenFlashContainer(requestRender func()) *AltScreenFlashContainer {
	return &AltScreenFlashContainer{requestRender: requestRender}
}

// Flash shows a transient message (pi: flash).
func (f *AltScreenFlashContainer) Flash(message string, durationMs int) {
	f.mu.Lock()
	f.nextID++
	id := f.nextID
	if durationMs < 0 {
		durationMs = 0
	}
	entry := &altScreenFlashEntry{id: id, message: message}
	entry.timer = time.AfterFunc(time.Duration(durationMs)*time.Millisecond, func() {
		f.mu.Lock()
		index := -1
		for i, e := range f.entries {
			if e.id == id {
				index = i
				break
			}
		}
		if index == -1 {
			f.mu.Unlock()
			return
		}
		f.entries = append(f.entries[:index], f.entries[index+1:]...)
		f.mu.Unlock()
		if f.requestRender != nil {
			f.requestRender()
		}
	})
	f.entries = append(f.entries, entry)
	f.mu.Unlock()
	if f.requestRender != nil {
		f.requestRender()
	}
}

// Dispose clears all entries and cancels their timers (pi: dispose).
func (f *AltScreenFlashContainer) Dispose() {
	f.mu.Lock()
	for _, entry := range f.entries {
		if entry.timer != nil {
			entry.timer.Stop()
		}
	}
	f.entries = nil
	f.mu.Unlock()
}

// Invalidate implements Component (no cached state).
func (f *AltScreenFlashContainer) Invalidate() {}

// Render implements Component.
func (f *AltScreenFlashContainer) Render(width int) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	lines := make([]string, 0, len(f.entries))
	for _, entry := range f.entries {
		message := TruncateToWidth(" "+entry.message+" ", width, "", false)
		lines = append(lines, "\x1b[7m"+message+"\x1b[27m")
	}
	return lines
}
