// branch_summary_message.go (pi: components/branch-summary-message.ts) —
// renders a branch summary message with collapsed/expanded state. Uses the
// same background color as custom messages for visual consistency.
package components

import (
	"github.com/ZoroSpace/pig/agent/harness"
	"github.com/ZoroSpace/pig/codingagent/core/tools"
	"github.com/ZoroSpace/pig/codingagent/modes/interactive/theme"
	"github.com/ZoroSpace/pig/tui"
)

// BranchSummaryMessageComponent renders a branch summary (pi: class
// BranchSummaryMessageComponent extends Box).
type BranchSummaryMessageComponent struct {
	tui.Box

	expanded      bool
	message       *harness.BranchSummaryMessage
	markdownTheme tui.MarkdownTheme
}

// NewBranchSummaryMessageComponent builds the component (pi: constructor;
// markdownTheme nil = getMarkdownTheme()).
func NewBranchSummaryMessageComponent(message *harness.BranchSummaryMessage, markdownTheme *tui.MarkdownTheme) *BranchSummaryMessageComponent {
	c := &BranchSummaryMessageComponent{message: message}
	if markdownTheme != nil {
		c.markdownTheme = *markdownTheme
	} else {
		c.markdownTheme = theme.GetMarkdownTheme()
	}
	c.Box = *tui.NewBox(1, 1, func(t string) string { return theme.Bg("customMessageBg", t) })
	c.updateDisplay()
	return c
}

// SetExpanded toggles the expanded state (pi: setExpanded).
func (c *BranchSummaryMessageComponent) SetExpanded(expanded bool) {
	c.expanded = expanded
	c.updateDisplay()
}

// Invalidate re-renders (pi: invalidate override).
func (c *BranchSummaryMessageComponent) Invalidate() {
	c.Box.Invalidate()
	c.updateDisplay()
}

func (c *BranchSummaryMessageComponent) updateDisplay() {
	c.Clear()

	label := theme.Fg("customMessageLabel", "\x1b[1m[branch]\x1b[22m")
	c.AddChild(tui.NewText(label, 0, 0))
	c.AddChild(tui.NewSpacer(1))

	if c.expanded {
		header := "**Branch Summary**\n\n"
		c.AddChild(tui.NewMarkdown(header+c.message.Summary, 0, 0, c.markdownTheme, &tui.DefaultTextStyle{
			Color: func(text string) string { return theme.Fg("customMessageText", text) },
		}, nil))
	} else {
		c.AddChild(tui.NewText(
			theme.Fg("customMessageText", "Branch summary (")+
				theme.Fg("dim", tools.KeyText("app.tools.expand"))+
				theme.Fg("customMessageText", " to expand)"),
			0,
			0,
		))
	}
}
