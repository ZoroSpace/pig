// extension_input.go (pi: components/extension-input.ts) — simple text
// input component for extensions.
package components

import (
	coretools "github.com/ZoroSpace/pig/codingagent/core/tools"
	"github.com/ZoroSpace/pig/codingagent/modes/interactive/theme"
	"github.com/ZoroSpace/pig/tui"
)

// ExtensionInputOptions configure the input (pi: ExtensionInputOptions).
type ExtensionInputOptions struct {
	Tui     tui.TUI
	Timeout int
	// InitialValue pre-fills the input (pi v0.86.0: initialValue).
	InitialValue string
	// Description renders below the title (pi v0.86.0: description).
	Description string
}

// ExtensionInputComponent renders a one-line prompt with confirm/cancel.
type ExtensionInputComponent struct {
	tui.Container

	input     *tui.Input
	onSubmit  func(value string)
	onCancel  func()
	titleText *tui.Text
	baseTitle string
	countdown *CountdownTimer
	focused   bool
}

// NewExtensionInputComponent builds the input (pi: constructor; the
// placeholder parameter is unused there too).
func NewExtensionInputComponent(
	title string,
	_ *string,
	onSubmit func(value string),
	onCancel func(),
	opts *ExtensionInputOptions,
) *ExtensionInputComponent {
	c := &ExtensionInputComponent{
		onSubmit:  onSubmit,
		onCancel:  onCancel,
		baseTitle: title,
	}
	currentTheme := theme.Current()

	c.AddChild(NewDynamicBorder(nil))
	c.AddChild(tui.NewSpacer(1))

	c.titleText = tui.NewText(currentTheme.Fg("accent", title), 1, 0)
	c.AddChild(c.titleText)
	if opts != nil && opts.Description != "" {
		c.AddChild(tui.NewSpacer(1))
		c.AddChild(tui.NewText(currentTheme.Fg("text", opts.Description), 1, 0))
	}
	c.AddChild(tui.NewSpacer(1))

	if opts != nil && opts.Timeout > 0 && opts.Tui != nil {
		c.countdown = NewCountdownTimer(opts.Timeout, opts.Tui,
			func(s int) {
				c.titleText.SetText(theme.Current().Fg("accent", c.baseTitle+" ("+itoa(s)+"s)"))
			},
			func() { c.onCancel() },
		)
	}

	c.input = tui.NewInput()
	if opts != nil && opts.InitialValue != "" {
		c.input.SetValue(opts.InitialValue)
	}
	c.AddChild(c.input)
	c.AddChild(tui.NewSpacer(1))
	c.AddChild(tui.NewText(
		coretools.KeyHint("tui.select.confirm", "submit")+"  "+coretools.KeyHint("tui.select.cancel", "cancel"),
		1, 0,
	))
	c.AddChild(tui.NewSpacer(1))
	c.AddChild(NewDynamicBorder(nil))
	return c
}

// HandleInput processes keys (pi: handleInput).
func (c *ExtensionInputComponent) HandleInput(keyData string) {
	kb := tui.GetKeybindings()
	if kb.Matches(keyData, "tui.select.confirm") || keyData == "\n" {
		c.onSubmit(c.input.GetValue())
	} else if kb.Matches(keyData, "tui.select.cancel") {
		c.onCancel()
	} else {
		c.input.HandleInput(keyData)
	}
}

// Dispose stops the countdown timer (pi: dispose).
func (c *ExtensionInputComponent) Dispose() {
	if c.countdown != nil {
		c.countdown.Dispose()
	}
}

// GetFocused implements Focusable (propagates to the input for IME cursor
// positioning; pi: focused getter).
func (c *ExtensionInputComponent) GetFocused() bool { return c.focused }

// SetFocused implements Focusable (pi: focused setter).
func (c *ExtensionInputComponent) SetFocused(focused bool) {
	c.focused = focused
	if f := tui.AsFocusable(c.input); f != nil {
		f.SetFocused(focused)
	}
}
