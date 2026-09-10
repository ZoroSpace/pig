// bordered_loader.go (pi: components/bordered-loader.ts) — loader wrapped
// with borders for extension UI.
package components

import (
	"context"

	"github.com/ZoroSpace/pig/codingagent/core/tools"
	"github.com/ZoroSpace/pig/codingagent/modes/interactive/theme"
	"github.com/ZoroSpace/pig/tui"
)

// BorderedLoader wraps a (cancellable) loader between dynamic borders (pi:
// class BorderedLoader extends Container).
type BorderedLoader struct {
	tui.Container

	loader interface {
		tui.Component
	}
	cancellable bool
	// abortCancel backs the non-cancellable signal (pi: signalController —
	// AbortSignal → context, per repo rules).
	abortCancel context.CancelFunc
	abortCtx    context.Context
}

// NewBorderedLoader builds the loader (pi: constructor; options nil =
// cancellable).
func NewBorderedLoader(ui tui.TUI, theme *theme.Theme, message string, options *struct{ Cancellable bool }) *BorderedLoader {
	cancellable := true
	if options != nil {
		cancellable = options.Cancellable
	}
	b := &BorderedLoader{cancellable: cancellable}
	borderColor := func(s string) string { return theme.Fg("border", s) }
	b.AddChild(NewDynamicBorder(borderColor))
	if cancellable {
		b.loader = tui.NewCancellableLoader(
			ui,
			func(s string) string { return theme.Fg("accent", s) },
			func(s string) string { return theme.Fg("muted", s) },
			message,
			nil,
		)
	} else {
		ctx, cancel := context.WithCancel(context.Background())
		b.abortCtx = ctx
		b.abortCancel = cancel
		b.loader = tui.NewLoader(
			ui,
			func(s string) string { return theme.Fg("accent", s) },
			func(s string) string { return theme.Fg("muted", s) },
			message,
			nil,
		)
	}
	b.AddChild(b.loader)
	if cancellable {
		b.AddChild(tui.NewSpacer(1))
		b.AddChild(tui.NewText(tools.KeyHint("tui.select.cancel", "cancel"), 1, 0))
	}
	b.AddChild(tui.NewSpacer(1))
	b.AddChild(NewDynamicBorder(borderColor))
	return b
}

// Signal returns the cancellation context (pi: get signal — AbortSignal →
// context per repo rules).
func (b *BorderedLoader) Signal() context.Context {
	if b.cancellable {
		return b.loader.(*tui.CancellableLoader).Signal()
	}
	if b.abortCtx == nil {
		// pi: `new AbortController().signal` — an already-independent signal
		ctx, cancel := context.WithCancel(context.Background())
		b.abortCtx = ctx
		b.abortCancel = cancel
	}
	return b.abortCtx
}

// SetOnAbort installs the abort callback (pi: set onAbort).
func (b *BorderedLoader) SetOnAbort(fn func()) {
	if b.cancellable {
		b.loader.(*tui.CancellableLoader).OnAbort = fn
	}
}

// HandleInput forwards input to the cancellable loader (pi: handleInput).
func (b *BorderedLoader) HandleInput(data string) {
	if b.cancellable {
		b.loader.(*tui.CancellableLoader).HandleInput(data)
	}
}

// Dispose stops the loader animation (pi: dispose — dispose() ?? stop()).
func (b *BorderedLoader) Dispose() {
	if cancellable, ok := b.loader.(*tui.CancellableLoader); ok {
		cancellable.Dispose()
		return
	}
	if loader, ok := b.loader.(*tui.Loader); ok {
		loader.Stop()
	}
}
