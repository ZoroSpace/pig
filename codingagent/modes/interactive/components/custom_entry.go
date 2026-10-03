// custom_entry.go (pi: components/custom-entry.ts) — renders a custom
// session entry from extensions. The host owns transcript spacing; renderer
// output should provide only its content.
package components

import (
	core "github.com/ZoroSpace/pig/codingagent/core"
	"github.com/ZoroSpace/pig/codingagent/modes/interactive/theme"
	"github.com/ZoroSpace/pig/tui"
)

// CustomEntryComponent renders a custom session entry (pi: class
// CustomEntryComponent extends Container).
type CustomEntryComponent struct {
	tui.Container

	entry           *core.CustomEntry
	renderer        core.EntryRenderer
	customComponent tui.Component
	expanded        bool
}

// NewCustomEntryComponent builds the component (pi: constructor).
func NewCustomEntryComponent(entry *core.CustomEntry, renderer core.EntryRenderer) *CustomEntryComponent {
	c := &CustomEntryComponent{
		entry:    entry,
		renderer: renderer,
	}
	c.rebuild()
	return c
}

// HasContent reports whether the renderer produced a component (pi:
// hasContent).
func (c *CustomEntryComponent) HasContent() bool {
	return c.customComponent != nil
}

// SetExpanded toggles the expanded state (pi: setExpanded).
func (c *CustomEntryComponent) SetExpanded(expanded bool) {
	if c.expanded != expanded {
		c.expanded = expanded
		c.rebuild()
	}
}

// Invalidate re-renders (pi: invalidate override).
func (c *CustomEntryComponent) Invalidate() {
	c.Container.Invalidate()
	c.rebuild()
}

func (c *CustomEntryComponent) rebuild() {
	c.Clear()
	c.customComponent = nil

	var component tui.Component
	func() {
		defer func() {
			if err := recover(); err != nil {
				message, _ := err.(error)
				messageText := ""
				if message != nil {
					messageText = message.Error()
				} else {
					messageText = "renderer panicked"
				}
				// pig-only: no background fill on the failure box (see
				// docs/feat/BUILTIN-THEME-PALETTES.md §BLOCK-BORDERS).
				box := tui.NewBox(1, 1, nil)
				box.AddChild(tui.NewText(theme.Fg("error", "["+c.entry.CustomType+"] renderer failed: "+messageText), 0, 0))
				component = box
			}
		}()
		component = c.renderer(c.entry, core.EntryRenderOptions{Expanded: c.expanded}, theme.Current())
	}()

	if component == nil {
		return
	}

	c.customComponent = component
	c.AddChild(tui.NewSpacer(1))
	c.AddChild(component)
}
