// custom_message.go (pi: components/custom-message.ts) — renders a custom
// message entry from extensions. Uses distinct styling to differentiate
// from user messages.
package components

import (
	"strings"

	"github.com/ZoroSpace/pig/agent/harness"
	"github.com/ZoroSpace/pig/ai/types"
	core "github.com/ZoroSpace/pig/codingagent/core"
	"github.com/ZoroSpace/pig/codingagent/modes/interactive/theme"
	"github.com/ZoroSpace/pig/tui"
)

// CustomMessageComponent renders a custom message (pi: class
// CustomMessageComponent extends Container).
type CustomMessageComponent struct {
	tui.Container

	message         *harness.CustomMessage
	customRenderer  core.MessageRenderer
	box             *tui.Box
	customComponent tui.Component
	markdownTheme   tui.MarkdownTheme
	expanded        bool
	outputPad       int
}

// NewCustomMessageComponent builds the component (pi: constructor;
// markdownTheme nil = getMarkdownTheme(), outputPad 0 = default 1).
func NewCustomMessageComponent(message *harness.CustomMessage, customRenderer core.MessageRenderer, markdownTheme *tui.MarkdownTheme, outputPad int) *CustomMessageComponent {
	c := &CustomMessageComponent{
		message:        message,
		customRenderer: customRenderer,
		outputPad:      outputPad,
	}
	if markdownTheme != nil {
		c.markdownTheme = *markdownTheme
	} else {
		c.markdownTheme = theme.GetMarkdownTheme()
	}
	if outputPad == 0 {
		c.outputPad = 1
	}

	// Box without background fill (used for default rendering). Vertical
	// padding 0: the border rules give the block its frame, keeping the
	// pre-border block height (pi: 1).
	c.box = tui.NewBox(1, 0, nil)

	c.rebuild()
	return c
}

// SetExpanded toggles the expanded state (pi: setExpanded).
func (c *CustomMessageComponent) SetExpanded(expanded bool) {
	if c.expanded != expanded {
		c.expanded = expanded
		c.rebuild()
	}
}

// SetOutputPad updates the padding (pi: setOutputPad).
func (c *CustomMessageComponent) SetOutputPad(outputPad int) {
	if c.outputPad != outputPad {
		c.outputPad = outputPad
		c.rebuild()
	}
}

// Invalidate re-renders (pi: invalidate override).
func (c *CustomMessageComponent) Invalidate() {
	c.Container.Invalidate()
	c.rebuild()
}

func (c *CustomMessageComponent) rebuild() {
	c.Clear()
	c.customComponent = nil

	// Try custom renderer first - it handles its own styling
	if c.customRenderer != nil {
		func() {
			defer func() {
				// Fall through to default rendering
				recover()
			}()
			component := c.customRenderer(c.message, core.MessageRenderOptions{
				Expanded:  c.expanded,
				OutputPad: c.outputPad,
			}, theme.Current())
			if component != nil {
				// Custom renderer provides its own styled component
				c.customComponent = component
				c.AddChild(component)
			}
		}()
		if c.customComponent != nil {
			return
		}
	}

	// pig-only (2026-10-03): border-only framing in the custom-message label
	// color instead of the pi full-width background fill
	// (docs/feat/BUILTIN-THEME-PALETTES.md §BLOCK-BORDERS).
	borderColor := func(t string) string { return theme.Fg("customMessageLabel", t) }
	c.AddChild(tui.NewSpacer(1))
	c.AddChild(NewDynamicBorderCorner(borderColor, '┌'))
	c.AddChild(NewFrameBody(c.box))
	c.AddChild(NewDynamicBorderCorner(borderColor, '└'))

	// Default rendering: label + content
	c.box.Clear()
	label := theme.Fg("customMessageLabel", "\x1b[1m["+c.message.CustomType+"]\x1b[22m")
	c.box.AddChild(tui.NewText(label, 0, 0))
	c.box.AddChild(tui.NewSpacer(1))

	// Extract text content
	var text string
	if s, ok := c.message.Content.(string); ok {
		text = s
	} else if contents, ok := c.message.Content.([]any); ok {
		var texts []string
		for _, block := range contents {
			if textContent, ok := block.(*types.TextContent); ok {
				texts = append(texts, textContent.Text)
			}
		}
		text = strings.Join(texts, "\n")
	}

	c.box.AddChild(tui.NewMarkdown(text, 0, 0, c.markdownTheme, &tui.DefaultTextStyle{
		Color: func(t string) string { return theme.Fg("customMessageText", t) },
	}, nil))
}
