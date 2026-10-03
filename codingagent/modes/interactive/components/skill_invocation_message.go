// skill_invocation_message.go (pi: components/skill-invocation-message.ts)
// — renders a skill invocation message with collapsed/expanded state. Uses
// the same accent styling as custom messages for visual consistency.
// Only renders the skill block itself - user message is rendered
// separately.
package components

import (
	"github.com/ZoroSpace/pig/codingagent/core"
	"github.com/ZoroSpace/pig/codingagent/core/tools"
	"github.com/ZoroSpace/pig/codingagent/modes/interactive/theme"
	"github.com/ZoroSpace/pig/tui"
)

// SkillInvocationMessageComponent renders a skill invocation (pi: class
// SkillInvocationMessageComponent extends Box; pig frames it with
// state-colored borders instead of the pi background fill —
// docs/feat/BUILTIN-THEME-PALETTES.md §BLOCK-BORDERS).
type SkillInvocationMessageComponent struct {
	tui.Container

	expanded      bool
	skillBlock    *core.ParsedSkillBlock
	markdownTheme tui.MarkdownTheme
}

// NewSkillInvocationMessageComponent builds the component (pi: constructor;
// markdownTheme nil = getMarkdownTheme()).
func NewSkillInvocationMessageComponent(skillBlock *core.ParsedSkillBlock, markdownTheme *tui.MarkdownTheme) *SkillInvocationMessageComponent {
	c := &SkillInvocationMessageComponent{skillBlock: skillBlock}
	if markdownTheme != nil {
		c.markdownTheme = *markdownTheme
	} else {
		c.markdownTheme = theme.GetMarkdownTheme()
	}
	c.updateDisplay()
	return c
}

// SetExpanded toggles the expanded state (pi: setExpanded).
func (c *SkillInvocationMessageComponent) SetExpanded(expanded bool) {
	c.expanded = expanded
	c.updateDisplay()
}

// Invalidate re-renders (pi: invalidate override).
func (c *SkillInvocationMessageComponent) Invalidate() {
	c.Container.Invalidate()
	c.updateDisplay()
}

func (c *SkillInvocationMessageComponent) updateDisplay() {
	c.Clear()
	content := &tui.Container{}

	if c.expanded {
		// Expanded: label + skill name header + full content
		label := theme.Fg("customMessageLabel", "\x1b[1m[skill]\x1b[22m")
		content.AddChild(tui.NewText(label, 0, 0))
		header := "**" + c.skillBlock.Name + "**\n\n"
		content.AddChild(tui.NewMarkdown(header+c.skillBlock.Content, 0, 0, c.markdownTheme, &tui.DefaultTextStyle{
			Color: func(text string) string { return theme.Fg("customMessageText", text) },
		}, nil))
	} else {
		// Collapsed: single line - [skill] name (hint to expand)
		line := theme.Fg("customMessageLabel", "\x1b[1m[skill]\x1b[22m ") +
			theme.Fg("customMessageText", c.skillBlock.Name) +
			theme.Fg("dim", " ("+tools.KeyText("app.tools.expand")+" to expand)")
		content.AddChild(tui.NewText(line, 0, 0))
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
	c.AddChild(NewDynamicBorderCorner(borderColor, '┌'))
	c.AddChild(NewLeftRail(contentBox, borderColor))
	c.AddChild(NewDynamicBorderCorner(borderColor, '└'))
}
