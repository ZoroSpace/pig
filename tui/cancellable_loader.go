// tui/cancellable_loader.go (pi: packages/tui/src/components/cancellable-loader.ts)
//
// Loader that can be cancelled with Escape. Extends Loader with a
// cancellation handle for cancelling async operations (pi: AbortController /
// AbortSignal → context, per repo rules).
package tui

import "context"

// CancellableLoader is the escape-cancellable loader
// (pi: class CancellableLoader extends Loader).
type CancellableLoader struct {
	// Embedded by pointer: Loader contains mutexes (pig concurrency
	// adaptation), and value embedding would copy locks (pi: extends Loader).
	*Loader

	abortContext context.Context
	abortCancel  context.CancelFunc

	/** Called when user presses Escape (pi: onAbort?: () => void). */
	OnAbort func()
}

// NewCancellableLoader mirrors pi's constructor.
func NewCancellableLoader(tui TUI, spinnerColorFn func(string) string, messageColorFn func(string) string, message string, indicator *LoaderIndicatorOptions) *CancellableLoader {
	ctx, cancel := context.WithCancel(context.Background())
	c := &CancellableLoader{
		Loader:       NewLoader(tui, spinnerColorFn, messageColorFn, message, indicator),
		abortContext: ctx,
		abortCancel:  cancel,
	}
	return c
}

// Signal returns the cancellation context, aborted when user presses Escape
// (pi: get signal — AbortSignal → context.Context).
func (c *CancellableLoader) Signal() context.Context {
	return c.abortContext
}

// Aborted reports whether the loader was aborted (pi: get aborted).
func (c *CancellableLoader) Aborted() bool {
	return c.abortContext.Err() != nil
}

// HandleInput implements InputHandler (pi: handleInput).
func (c *CancellableLoader) HandleInput(data string) {
	if GetKeybindings().Matches(data, "tui.select.cancel") {
		c.abortCancel()
		if c.OnAbort != nil {
			c.OnAbort()
		}
	}
}

// Dispose stops the animation (pi: dispose).
func (c *CancellableLoader) Dispose() {
	c.Stop()
}
