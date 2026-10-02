// user_message.go (pi: components/user-message.ts) — component that
// renders a user message.
package components

import (
	"github.com/ZoroSpace/pig/codingagent/core"
	"github.com/ZoroSpace/pig/codingagent/modes/interactive/theme"
	"github.com/ZoroSpace/pig/tui"
)

const (
	osc133ZoneStart = "\x1b]133;A\x07"
	osc133ZoneEnd   = "\x1b]133;B\x07"
	osc133ZoneFinal = "\x1b]133;C\x07"
)

// UserMessageComponent renders a user message (pi: class
// UserMessageComponent extends Container).
type UserMessageComponent struct {
	tui.Container

	text                 string
	markdownTheme        tui.MarkdownTheme
	outputPad            int
	markdownTransformers []core.MarkdownTransformer
}

// NewUserMessageComponent builds the component (pi: constructor;
// markdownTheme nil = getMarkdownTheme(), outputPad 0 = default 1).
func NewUserMessageComponent(text string, markdownTheme *tui.MarkdownTheme, outputPad int, markdownTransformers []core.MarkdownTransformer) *UserMessageComponent {
	c := &UserMessageComponent{
		text:                 text,
		outputPad:            outputPad,
		markdownTransformers: markdownTransformers,
	}
	if markdownTheme != nil {
		c.markdownTheme = *markdownTheme
	} else {
		c.markdownTheme = theme.GetMarkdownTheme()
	}
	c.rebuild()
	return c
}

// SetOutputPad updates the padding and re-renders (pi: setOutputPad).
func (c *UserMessageComponent) SetOutputPad(padding int) {
	c.outputPad = padding
	c.rebuild()
}

func (c *UserMessageComponent) rebuild() {
	c.Clear()
	// The Markdown pads and colors its own background: a Box around it would
	// keep a second full-width copy of every line, with identical output
	// (pi e792ba131).
	c.AddChild(tui.NewMarkdown(
		c.text,
		c.outputPad,
		1,
		c.markdownTheme,
		&tui.DefaultTextStyle{
			Color:   func(content string) string { return theme.Fg("userMessageText", content) },
			BgColor: func(content string) string { return theme.Bg("userMessageBg", content) },
		},
		&tui.MarkdownOptions{
			PreserveOrderedListMarkers: true,
			PreserveBackslashEscapes:   true,
			Transform:                  CreateMarkdownTransform("user", false, c.markdownTransformers),
		},
	))
}

// Render wraps the output in shell-prompt OSC 133 zones (pi: render).
func (c *UserMessageComponent) Render(width int) []string {
	lines := c.Container.Render(width)
	if len(lines) == 0 {
		return lines
	}
	lines[0] = osc133ZoneStart + lines[0]
	lines[len(lines)-1] = osc133ZoneEnd + osc133ZoneFinal + lines[len(lines)-1]
	return lines
}
