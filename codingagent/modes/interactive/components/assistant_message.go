// assistant_message.go (pi: components/assistant-message.ts) — component
// that renders a complete assistant message.
package components

import (
	"strings"

	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/codingagent/core"
	"github.com/ZoroSpace/pig/codingagent/modes/interactive/theme"
	"github.com/ZoroSpace/pig/tui"
)

// AssistantMessageComponent renders a complete assistant message (pi:
// class AssistantMessageComponent extends Container).
type AssistantMessageComponent struct {
	tui.Container

	contentContainer     *tui.Container
	hideThinkingBlock    bool
	markdownTheme        tui.MarkdownTheme
	hiddenThinkingLabel  string
	outputPad            int
	markdownTransformers []core.MarkdownTransformer
	lastMessage          *types.AssistantMessage
	hasToolCalls         bool
	isStreaming          bool
}

// AssistantMessageOptions carries the constructor's optional parameters
// (pi: default parameters; zero values mean defaults).
type AssistantMessageOptions struct {
	// HideThinkingBlock (pi: hideThinkingBlock, default false).
	HideThinkingBlock bool
	// MarkdownTheme nil = getMarkdownTheme().
	MarkdownTheme *tui.MarkdownTheme
	// HiddenThinkingLabel "" = "Thinking...".
	HiddenThinkingLabel string
	// OutputPad 0 = default 1 (pi: outputPad = 1).
	OutputPad int
	// MarkdownTransformers (pi: markdownTransformers).
	MarkdownTransformers []core.MarkdownTransformer
}

// NewAssistantMessageComponent builds the component (pi: constructor;
// message may be nil).
// asTextOrThinking normalizes an adapter Content entry to the pointer form
// the renderer consumes (pig addition — see UpdateContent deviation note).
func asTextOrThinking(content types.Content) types.Content {
	switch block := content.(type) {
	case *types.TextContent:
		return block
	case *types.ThinkingContent:
		return block
	case types.TextContent:
		return &block
	case types.ThinkingContent:
		return &block
	}
	return content
}

func NewAssistantMessageComponent(message *types.AssistantMessage, options *AssistantMessageOptions) *AssistantMessageComponent {
	c := &AssistantMessageComponent{
		hiddenThinkingLabel: "Thinking...",
		outputPad:           1,
	}
	if options != nil {
		c.hideThinkingBlock = options.HideThinkingBlock
		if options.MarkdownTheme != nil {
			c.markdownTheme = *options.MarkdownTheme
		} else {
			c.markdownTheme = theme.GetMarkdownTheme()
		}
		if options.HiddenThinkingLabel != "" {
			c.hiddenThinkingLabel = options.HiddenThinkingLabel
		}
		if options.OutputPad != 0 {
			c.outputPad = options.OutputPad
		}
		c.markdownTransformers = options.MarkdownTransformers
	} else {
		c.markdownTheme = theme.GetMarkdownTheme()
	}

	// Container for text/thinking content
	c.contentContainer = &tui.Container{}
	c.AddChild(c.contentContainer)

	if message != nil {
		c.UpdateContent(message, c.isStreaming)
	}
	return c
}

// Invalidate re-renders the last message (pi: invalidate override).
func (c *AssistantMessageComponent) Invalidate() {
	c.Container.Invalidate()
	if c.lastMessage != nil {
		c.UpdateContent(c.lastMessage, c.isStreaming)
	}
}

// SetHideThinkingBlock toggles the collapsed thinking label (pi:
// setHideThinkingBlock).
func (c *AssistantMessageComponent) SetHideThinkingBlock(hide bool) {
	c.hideThinkingBlock = hide
	if c.lastMessage != nil {
		c.UpdateContent(c.lastMessage, c.isStreaming)
	}
}

// SetHiddenThinkingLabel updates the collapsed label text (pi:
// setHiddenThinkingLabel).
func (c *AssistantMessageComponent) SetHiddenThinkingLabel(label string) {
	c.hiddenThinkingLabel = label
	if c.lastMessage != nil {
		c.UpdateContent(c.lastMessage, c.isStreaming)
	}
}

// SetOutputPad updates the padding (pi: setOutputPad).
func (c *AssistantMessageComponent) SetOutputPad(padding int) {
	c.outputPad = padding
	if c.lastMessage != nil {
		c.UpdateContent(c.lastMessage, c.isStreaming)
	}
}

// Render wraps the output in shell-prompt OSC 133 zones (pi: render
// override).
func (c *AssistantMessageComponent) Render(width int) []string {
	lines := c.Container.Render(width)
	if c.hasToolCalls || len(lines) == 0 {
		return lines
	}
	lines[0] = osc133ZoneStart + lines[0]
	lines[len(lines)-1] = osc133ZoneEnd + osc133ZoneFinal + lines[len(lines)-1]
	return lines
}

// UpdateContent rebuilds the content container from the message (pi:
// updateContent).
func (c *AssistantMessageComponent) UpdateContent(message *types.AssistantMessage, isStreaming bool) {
	c.lastMessage = message
	c.isStreaming = isStreaming

	// Clear content container
	c.contentContainer.Clear()

	hasVisibleContent := false
	for _, content := range message.Content {
		// Content entries arrive as values from the adapters (pig: value
		// receivers; pi's duck typing covers both), so accept each.
		switch block := asTextOrThinking(content).(type) {
		case *types.TextContent:
			if strings.TrimSpace(block.Text) != "" {
				hasVisibleContent = true
			}
		case *types.ThinkingContent:
			if strings.TrimSpace(block.Thinking) != "" {
				hasVisibleContent = true
			}
		}
		if hasVisibleContent {
			break
		}
	}

	if hasVisibleContent {
		c.contentContainer.AddChild(tui.NewSpacer(1))
	}

	// Render content in order. Blocks are normalized to their pointer forms
	// first: the adapters put value types into Content (pig: value
	// receivers), while the tests use pointers (pi accepts both).
	normalized := make([]types.Content, len(message.Content))
	for idx, block := range message.Content {
		normalized[idx] = asTextOrThinking(block)
	}
	message.Content = normalized
	for i := 0; i < len(message.Content); i++ {
		switch content := message.Content[i].(type) {
		case *types.TextContent:
			if strings.TrimSpace(content.Text) == "" {
				continue
			}
			// Assistant text messages with no background - trim the text
			// Set paddingY=0 to avoid extra spacing before tool executions
			c.contentContainer.AddChild(tui.NewMarkdown(
				strings.TrimSpace(content.Text),
				c.outputPad,
				0,
				c.markdownTheme,
				nil,
				&tui.MarkdownOptions{
					Transform: CreateMarkdownTransform("assistant", c.isStreaming, c.markdownTransformers),
				},
			))
		case *types.ThinkingContent:
			thinkingBlocks := make([]string, 0, 4)
			for ; i < len(message.Content); i++ {
				thinkingContent, ok := message.Content[i].(*types.ThinkingContent)
				if !ok {
					break
				}
				thinking := strings.TrimSpace(thinkingContent.Thinking)
				if thinking != "" {
					thinkingBlocks = append(thinkingBlocks, thinking)
				}
			}
			i--

			if len(thinkingBlocks) == 0 {
				continue
			}

			// Add spacing only when another visible assistant content block
			// follows. This avoids a superfluous blank line before
			// separately-rendered tool execution blocks.
			hasVisibleContentAfter := false
			for _, content := range message.Content[i+1:] {
				if text, ok := asTextOrThinking(content).(*types.TextContent); ok && strings.TrimSpace(text.Text) != "" {
					hasVisibleContentAfter = true
					break
				}
				if thinking, ok := content.(*types.ThinkingContent); ok && strings.TrimSpace(thinking.Thinking) != "" {
					hasVisibleContentAfter = true
					break
				}
			}

			if c.hideThinkingBlock {
				// Show one static label for each run of thinking blocks when
				// hidden.
				c.contentContainer.AddChild(tui.NewText(
					c.italicThinkingLabel(),
					c.outputPad,
					0,
				))
			} else {
				// Render each run of thinking blocks as one Markdown section.
				c.contentContainer.AddChild(tui.NewMarkdown(
					strings.Join(thinkingBlocks, "\n\n"),
					c.outputPad,
					0,
					c.markdownTheme,
					&tui.DefaultTextStyle{
						Color:  func(text string) string { return theme.Fg("thinkingText", text) },
						Italic: true,
					},
					&tui.MarkdownOptions{
						Transform: CreateMarkdownTransform("assistant-thinking", c.isStreaming, c.markdownTransformers),
					},
				))
			}
			if hasVisibleContentAfter {
				c.contentContainer.AddChild(tui.NewSpacer(1))
			}
		}
	}

	// Check if incomplete/failed - show after partial content.
	// For aborted/error tool calls, tool execution components show the error.
	// Length stops can happen before a tool call is complete, so surface them
	// here too.
	hasToolCalls := false
	for _, content := range message.Content {
		if _, ok := content.(*types.ToolCall); ok {
			hasToolCalls = true
			break
		}
	}
	c.hasToolCalls = hasToolCalls
	switch message.StopReason {
	case "length":
		c.contentContainer.AddChild(tui.NewSpacer(1))
		c.contentContainer.AddChild(tui.NewText(
			theme.Fg("error", "Response was truncated before completion."),
			c.outputPad,
			0,
		))
	default:
		if hasToolCalls {
			return
		}
		switch message.StopReason {
		case "aborted":
			abortMessage := "Operation aborted"
			if message.ErrorMessage != "" && message.ErrorMessage != "Request was aborted" {
				abortMessage = message.ErrorMessage
			}
			c.contentContainer.AddChild(tui.NewSpacer(1))
			c.contentContainer.AddChild(tui.NewText(theme.Fg("error", abortMessage), c.outputPad, 0))
		case "error":
			errorMsg := message.ErrorMessage
			if errorMsg == "" {
				errorMsg = "Unknown error"
			}
			c.contentContainer.AddChild(tui.NewSpacer(1))
			c.contentContainer.AddChild(tui.NewText(theme.Fg("error", "Error: "+errorMsg), c.outputPad, 0))
		}
	}
}

func (c *AssistantMessageComponent) italicThinkingLabel() string {
	return theme.Italic(theme.Fg("thinkingText", c.hiddenThinkingLabel))
}
