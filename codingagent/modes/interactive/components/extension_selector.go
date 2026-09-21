// extension_selector.go (pi: components/extension-selector.ts) — generic
// selector component for extensions. Displays a list of string options with
// keyboard navigation.
package components

import (
	coretools "github.com/ZoroSpace/pig/codingagent/core/tools"
	"github.com/ZoroSpace/pig/codingagent/modes/interactive/theme"
	"github.com/ZoroSpace/pig/tui"
)

// ExtensionSelectorOptions configure the selector (pi:
// ExtensionSelectorOptions).
type ExtensionSelectorOptions struct {
	Tui                   tui.TUI
	Timeout               int
	OnToggleToolsExpanded func()
	// Description renders below the title (pi v0.86.0: description).
	Description string
}

// ExtensionSelectorComponent renders an extension-provided option list.
type ExtensionSelectorComponent struct {
	tui.Container

	options               []string
	selectedIndex         int
	listContainer         *tui.Container
	onSelect              func(option string)
	onCancel              func()
	titleText             *tui.Text
	baseTitle             string
	countdown             *CountdownTimer
	onToggleToolsExpanded func()
}

// NewExtensionSelectorComponent builds the selector (pi: constructor).
func NewExtensionSelectorComponent(
	title string,
	options []string,
	onSelect func(option string),
	onCancel func(),
	opts *ExtensionSelectorOptions,
) *ExtensionSelectorComponent {
	c := &ExtensionSelectorComponent{
		options:               options,
		onSelect:              onSelect,
		onCancel:              onCancel,
		baseTitle:             title,
		onToggleToolsExpanded: optsOnToggle(opts),
	}
	currentTheme := theme.Current()

	c.AddChild(NewDynamicBorder(nil))
	c.AddChild(tui.NewSpacer(1))

	c.titleText = tui.NewText(currentTheme.Fg("accent", currentTheme.Bold(title)), 1, 0)
	c.AddChild(c.titleText)
	// Description renders below the title (pi: opts?.description).
	if opts != nil && opts.Description != "" {
		c.AddChild(tui.NewSpacer(1))
		c.AddChild(tui.NewText(currentTheme.Fg("text", opts.Description), 1, 0))
	}
	c.AddChild(tui.NewSpacer(1))

	if opts != nil && opts.Timeout > 0 && opts.Tui != nil {
		c.countdown = NewCountdownTimer(opts.Timeout, opts.Tui,
			func(s int) {
				c.titleText.SetText(theme.Current().Fg("accent", theme.Current().Bold(c.baseTitle+" ("+itoa(s)+"s)")))
			},
			func() { c.onCancel() },
		)
	}

	c.listContainer = &tui.Container{}
	c.AddChild(c.listContainer)
	c.AddChild(tui.NewSpacer(1))
	c.AddChild(tui.NewText(
		coretools.RawKeyHint("↑↓", "navigate")+
			"  "+
			coretools.KeyHint("tui.select.confirm", "select")+
			"  "+
			coretools.KeyHint("tui.select.cancel", "cancel"),
		1, 0,
	))
	c.AddChild(tui.NewSpacer(1))
	c.AddChild(NewDynamicBorder(nil))

	c.updateList()
	return c
}

func optsOnToggle(opts *ExtensionSelectorOptions) func() {
	if opts == nil {
		return nil
	}
	return opts.OnToggleToolsExpanded
}

func (c *ExtensionSelectorComponent) updateList() {
	c.listContainer.Clear()
	currentTheme := theme.Current()
	for i, option := range c.options {
		isSelected := i == c.selectedIndex
		var text string
		if isSelected {
			text = currentTheme.Fg("accent", "→ ") + currentTheme.Fg("accent", option)
		} else {
			text = "  " + currentTheme.Fg("text", option)
		}
		c.listContainer.AddChild(tui.NewText(text, 1, 0))
	}
}

// HandleInput processes keys (pi: handleInput).
func (c *ExtensionSelectorComponent) HandleInput(keyData string) {
	kb := tui.GetKeybindings()
	currentTheme := theme.Current()
	switch {
	case kb.Matches(keyData, "app.tools.expand"):
		if c.onToggleToolsExpanded != nil {
			c.onToggleToolsExpanded()
		}
	case kb.Matches(keyData, "tui.select.up") || keyData == "k":
		if c.selectedIndex > 0 {
			c.selectedIndex--
		}
		c.updateList()
	case kb.Matches(keyData, "tui.select.down") || keyData == "j":
		if c.selectedIndex < len(c.options)-1 {
			c.selectedIndex++
		}
		c.updateList()
	case kb.Matches(keyData, "tui.select.confirm") || keyData == "\n":
		if c.selectedIndex < len(c.options) {
			if selected := c.options[c.selectedIndex]; selected != "" {
				c.onSelect(selected)
			}
		}
	case kb.Matches(keyData, "tui.select.cancel"):
		_ = currentTheme
		c.onCancel()
	}
}

// Dispose stops the countdown timer (pi: dispose).
func (c *ExtensionSelectorComponent) Dispose() {
	if c.countdown != nil {
		c.countdown.Dispose()
	}
}
