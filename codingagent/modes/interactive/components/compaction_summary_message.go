// compaction_summary_message.go (pi: components/compaction-summary-message.ts)
// — renders a compaction message with collapsed/expanded state. Uses the
// same background color as custom messages for visual consistency.
package components

import (
	"strconv"

	"github.com/ZoroSpace/pig/agent/harness"
	"github.com/ZoroSpace/pig/codingagent/core/tools"
	"github.com/ZoroSpace/pig/codingagent/modes/interactive/theme"
	"github.com/ZoroSpace/pig/tui"
)

// CompactionSummaryMessageComponent renders a compaction summary (pi:
// class CompactionSummaryMessageComponent extends Box).
type CompactionSummaryMessageComponent struct {
	tui.Box

	expanded      bool
	message       *harness.CompactionSummaryMessage
	markdownTheme tui.MarkdownTheme
}

// NewCompactionSummaryMessageComponent builds the component (pi:
// constructor; markdownTheme nil = getMarkdownTheme()).
func NewCompactionSummaryMessageComponent(message *harness.CompactionSummaryMessage, markdownTheme *tui.MarkdownTheme) *CompactionSummaryMessageComponent {
	c := &CompactionSummaryMessageComponent{message: message}
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
func (c *CompactionSummaryMessageComponent) SetExpanded(expanded bool) {
	c.expanded = expanded
	c.updateDisplay()
}

// Invalidate re-renders (pi: invalidate override).
func (c *CompactionSummaryMessageComponent) Invalidate() {
	c.Box.Invalidate()
	c.updateDisplay()
}

// formatTokenCount is toLocaleString() for the token count (thousands
// separators, US grouping — the only locale shape pi's transcript relies
// on).
func formatTokenCount(n int64) string {
	s := strconv.FormatInt(n, 10)
	start := len(s) % 3
	if start == 0 {
		start = 3
	}
	out := s[:start]
	for i := start; i < len(s); i += 3 {
		out += "," + s[i:i+3]
	}
	return out
}

func (c *CompactionSummaryMessageComponent) updateDisplay() {
	c.Clear()

	tokenStr := formatTokenCount(c.message.TokensBefore)
	label := theme.Fg("customMessageLabel", "\x1b[1m[compaction]\x1b[22m")
	c.AddChild(tui.NewText(label, 0, 0))
	c.AddChild(tui.NewSpacer(1))

	if c.expanded {
		header := "**Compacted from " + tokenStr + " tokens**\n\n"
		c.AddChild(tui.NewMarkdown(header+c.message.Summary, 0, 0, c.markdownTheme, &tui.DefaultTextStyle{
			Color: func(text string) string { return theme.Fg("customMessageText", text) },
		}, nil))
	} else {
		c.AddChild(tui.NewText(
			theme.Fg("customMessageText", "Compacted from "+tokenStr+" tokens (")+
				theme.Fg("dim", tools.KeyText("app.tools.expand"))+
				theme.Fg("customMessageText", " to expand)"),
			0,
			0,
		))
	}
}
