// compaction_summary_message.go (pi: components/compaction-summary-message.ts)
// — renders a compaction message with collapsed/expanded state. Uses the
// same accent styling as custom messages for visual consistency.
package components

import (
	"strconv"

	"github.com/ZoroSpace/pig/agent/harness"
	"github.com/ZoroSpace/pig/codingagent/core/tools"
	"github.com/ZoroSpace/pig/codingagent/modes/interactive/theme"
	"github.com/ZoroSpace/pig/tui"
)

// CompactionSummaryMessageComponent renders a compaction summary (pi:
// class CompactionSummaryMessageComponent extends Box; pig frames it with
// state-colored borders instead of the pi background fill —
// docs/feat/BUILTIN-THEME-PALETTES.md §BLOCK-BORDERS).
type CompactionSummaryMessageComponent struct {
	tui.Container

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
	c.Container.Invalidate()
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
	content := &tui.Container{}

	tokenStr := formatTokenCount(c.message.TokensBefore)
	label := theme.Fg("customMessageLabel", "\x1b[1m[compaction]\x1b[22m")
	content.AddChild(tui.NewText(label, 0, 0))
	content.AddChild(tui.NewSpacer(1))

	if c.expanded {
		header := "**Compacted from " + tokenStr + " tokens**\n\n"
		content.AddChild(tui.NewMarkdown(header+c.message.Summary, 0, 0, c.markdownTheme, &tui.DefaultTextStyle{
			Color: func(text string) string { return theme.Fg("customMessageText", text) },
		}, nil))
	} else {
		content.AddChild(tui.NewText(
			theme.Fg("customMessageText", "Compacted from "+tokenStr+" tokens (")+
				theme.Fg("dim", tools.KeyText("app.tools.expand"))+
				theme.Fg("customMessageText", " to expand)"),
			0,
			0,
		))
	}

	contentBox := tui.NewBox(1, 0, nil)
	// pi v0.86.0: a left click anywhere on the message toggles it.
	contentBox.AddChild(tui.NewMouseRegion(content, func(event *tui.TuiMouseEvent) *tui.TuiMouseEventResult {
		if event.Type != tui.MouseEventTypeClick || event.Button != tui.MouseButtonLeft {
			return nil
		}
		c.SetExpanded(!c.expanded)
		return &tui.TuiMouseEventResult{Handled: true}
	}))

	borderColor := func(t string) string { return theme.Fg("customMessageLabel", t) }
	c.AddChild(NewDynamicBorder(borderColor))
	c.AddChild(contentBox)
	c.AddChild(NewDynamicBorder(borderColor))
}
