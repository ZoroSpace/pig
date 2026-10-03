package components

import (
	"strings"
	"testing"

	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/codingagent/core"
	"github.com/ZoroSpace/pig/codingagent/utils"
	"github.com/ZoroSpace/pig/tui"
)

// Port of test/assistant-message.test.ts.

// createAssistantMessage is the pi test fixture helper.
func createAssistantMessage(content []types.Content, stopReason types.StopReason) *types.AssistantMessage {
	if stopReason == "" {
		stopReason = types.StopReasonStop
	}
	return &types.AssistantMessage{
		Role:     types.RoleAssistant,
		Content:  content,
		API:      "openai-responses",
		Provider: "openai",
		Model:    "gpt-4o-mini",
		Usage: types.Usage{
			Input:       0,
			Output:      0,
			CacheRead:   0,
			CacheWrite:  0,
			TotalTokens: 0,
			Cost: types.Cost{
				Input: 0, Output: 0, CacheRead: 0, CacheWrite: 0, Total: 0,
			},
		},
		StopReason: stopReason,
	}
}

func rendered(t *testing.T, lines []string) string {
	t.Helper()
	return utils.StripAnsi(strings.Join(lines, "\n"))
}

func TestAssistantMessageComponent(t *testing.T) {
	t.Run("adds OSC 133 zone markers to assistant messages without tool calls", func(t *testing.T) {
		initTestTheme(t)

		component := NewAssistantMessageComponent(createAssistantMessage([]types.Content{
			&types.TextContent{Kind: types.KindText, Text: "hello"},
		}, ""), nil)
		lines := component.Render(40)

		if len(lines) == 0 {
			t.Fatal("no lines")
		}
		if !strings.Contains(lines[0], osc133ZoneStart) {
			t.Fatalf("line 0 missing zone start: %q", lines[0])
		}
		if !strings.HasPrefix(lines[len(lines)-1], osc133ZoneEnd+osc133ZoneFinal) {
			t.Fatalf("last line missing final markers: %q", lines[len(lines)-1])
		}
	})

	t.Run("does not add OSC 133 zone markers when assistant message contains tool calls", func(t *testing.T) {
		initTestTheme(t)

		component := NewAssistantMessageComponent(createAssistantMessage([]types.Content{
			&types.TextContent{Kind: types.KindText, Text: "calling tool"},
			&types.ToolCall{Kind: types.KindToolCall, ID: "tool-1", Name: "read", Arguments: map[string]any{"path": "file.txt"}},
		}, ""), nil)
		out := strings.Join(component.Render(60), "\n")

		if strings.Contains(out, osc133ZoneStart) || strings.Contains(out, osc133ZoneEnd) || strings.Contains(out, osc133ZoneFinal) {
			t.Fatalf("unexpected OSC 133 markers: %q", out)
		}
	})

	t.Run("renders length stops with neutral truncation wording", func(t *testing.T) {
		initTestTheme(t)

		component := NewAssistantMessageComponent(
			createAssistantMessage([]types.Content{
				&types.ThinkingContent{Kind: types.KindThinking, Thinking: "private reasoning"},
			}, types.StopReasonLength),
			&AssistantMessageOptions{HideThinkingBlock: true},
		)
		out := rendered(t, component.Render(80))

		if !strings.Contains(out, "Thinking...") {
			t.Fatalf("missing hidden thinking label: %q", out)
		}
		if !strings.Contains(out, "Response was truncated before completion.") {
			t.Fatalf("missing truncation wording: %q", out)
		}
	})

	t.Run("coalesces adjacent thinking blocks into one hidden thinking label", func(t *testing.T) {
		initTestTheme(t)

		component := NewAssistantMessageComponent(
			createAssistantMessage([]types.Content{
				&types.ThinkingContent{Kind: types.KindThinking, Thinking: "first thought"},
				&types.ThinkingContent{Kind: types.KindThinking, Thinking: ""},
				&types.ThinkingContent{Kind: types.KindThinking, Thinking: "second thought"},
				&types.TextContent{Kind: types.KindText, Text: "answer"},
			}, ""),
			&AssistantMessageOptions{HideThinkingBlock: true},
		)
		out := rendered(t, component.Render(80))

		if got := strings.Count(out, "Thinking..."); got != 1 {
			t.Fatalf("Thinking... count = %d", got)
		}
		if !strings.Contains(out, "answer") {
			t.Fatalf("missing answer: %q", out)
		}
	})

	t.Run("uses configured output padding for text and thinking", func(t *testing.T) {
		initTestTheme(t)

		component := NewAssistantMessageComponent(
			createAssistantMessage([]types.Content{
				&types.TextContent{Kind: types.KindText, Text: "hello"},
				&types.ThinkingContent{Kind: types.KindThinking, Thinking: "reasoning"},
			}, ""),
			&AssistantMessageOptions{OutputPad: 1},
		)
		lines := component.Render(80)
		if !someLineContains(lines, " hello") || !someLineContains(lines, " reasoning") {
			t.Fatalf("padded lines missing: %v", lines)
		}

		component.SetOutputPad(0)
		updatedLines := component.Render(80)
		if !someLineStartsWith(updatedLines, "hello") || !someLineStartsWith(updatedLines, "reasoning") {
			t.Fatalf("unpadded lines missing: %v", updatedLines)
		}
	})

	t.Run("chains Markdown transformers in registration order", func(t *testing.T) {
		initTestTheme(t)
		var calls []string
		contextSeen := core.MarkdownTransformContext{}
		message := createAssistantMessage([]types.Content{
			&types.TextContent{Kind: types.KindText, Text: "The result is $x^2$."},
		}, "")
		component := NewAssistantMessageComponent(message, &AssistantMessageOptions{
			MarkdownTransformers: []core.MarkdownTransformer{
				func(markdown string, context core.MarkdownTransformContext) string {
					calls = append(calls, "formula")
					contextSeen = context
					return strings.Replace(markdown, "$x^2$", "x²", 1)
				},
				func(markdown string, _ core.MarkdownTransformContext) string {
					calls = append(calls, "suffix")
					return markdown + " Done."
				},
			},
		})

		if got := rendered(t, component.Render(80)); !strings.Contains(got, "The result is x². Done.") {
			t.Fatalf("rendered = %q", got)
		}
		if len(calls) != 2 || calls[0] != "formula" || calls[1] != "suffix" {
			t.Fatalf("calls = %v", calls)
		}
		if contextSeen.MessageType != "assistant" || contextSeen.IsStreaming || contextSeen.AvailableWidth != 78 {
			t.Fatalf("context = %+v", contextSeen)
		}
	})

	t.Run("identifies partial assistant Markdown as streaming", func(t *testing.T) {
		initTestTheme(t)
		var streamingStates []bool
		message := createAssistantMessage([]types.Content{
			&types.TextContent{Kind: types.KindText, Text: "partial"},
		}, "")
		component := NewAssistantMessageComponent(nil, &AssistantMessageOptions{
			MarkdownTransformers: []core.MarkdownTransformer{
				func(markdown string, context core.MarkdownTransformContext) string {
					streamingStates = append(streamingStates, context.IsStreaming)
					if context.IsStreaming {
						return markdown
					}
					return markdown + " transformed"
				},
			},
		})

		component.UpdateContent(message, true)
		if got := rendered(t, component.Render(80)); strings.Contains(got, "transformed") {
			t.Fatalf("streaming render should not apply transform: %q", got)
		}

		component.UpdateContent(message, false)
		if got := rendered(t, component.Render(80)); !strings.Contains(got, "partial transformed") {
			t.Fatalf("rendered = %q", got)
		}
		if len(streamingStates) != 2 || !streamingStates[0] || streamingStates[1] {
			t.Fatalf("streamingStates = %v", streamingStates)
		}
	})

	t.Run("reapplies Markdown transformers when available width changes", func(t *testing.T) {
		initTestTheme(t)
		var availableWidths []int
		component := NewAssistantMessageComponent(
			createAssistantMessage([]types.Content{
				&types.TextContent{Kind: types.KindText, Text: "answer"},
			}, ""),
			&AssistantMessageOptions{
				MarkdownTransformers: []core.MarkdownTransformer{
					func(markdown string, context core.MarkdownTransformContext) string {
						availableWidths = append(availableWidths, context.AvailableWidth)
						return markdown + " (" + itoa(context.AvailableWidth) + ")"
					},
				},
			},
		)

		if got := rendered(t, component.Render(80)); !strings.Contains(got, "answer (78)") {
			t.Fatalf("rendered = %q", got)
		}
		component.Render(80)
		if got := rendered(t, component.Render(60)); !strings.Contains(got, "answer (58)") {
			t.Fatalf("rendered = %q", got)
		}
		if len(availableWidths) != 2 || availableWidths[0] != 78 || availableWidths[1] != 58 {
			t.Fatalf("availableWidths = %v", availableWidths)
		}
	})

	t.Run("continues the Markdown transformer chain when a transformer throws", func(t *testing.T) {
		initTestTheme(t)
		var calls []string
		component := NewAssistantMessageComponent(
			createAssistantMessage([]types.Content{
				&types.TextContent{Kind: types.KindText, Text: "still visible"},
			}, ""),
			&AssistantMessageOptions{
				MarkdownTransformers: []core.MarkdownTransformer{
					func(markdown string, _ core.MarkdownTransformContext) string {
						calls = append(calls, "first")
						return strings.Replace(markdown, "still", "remains", 1)
					},
					func(string, core.MarkdownTransformContext) string {
						calls = append(calls, "throw")
						panic("broken transformer")
					},
					func(markdown string, _ core.MarkdownTransformContext) string {
						calls = append(calls, "last")
						return markdown + " after error"
					},
				},
			},
		)

		if got := rendered(t, component.Render(80)); !strings.Contains(got, "remains visible after error") {
			t.Fatalf("rendered = %q", got)
		}
		if len(calls) != 3 || calls[0] != "first" || calls[1] != "throw" || calls[2] != "last" {
			t.Fatalf("calls = %v", calls)
		}
	})

	t.Run("transforms text and thinking Markdown without mutating the original message", func(t *testing.T) {
		initTestTheme(t)
		message := createAssistantMessage([]types.Content{
			&types.TextContent{Kind: types.KindText, Text: "answer"},
			&types.ThinkingContent{Kind: types.KindThinking, Thinking: "reasoning"},
		}, "")
		component := NewAssistantMessageComponent(message, &AssistantMessageOptions{
			MarkdownTransformers: []core.MarkdownTransformer{
				func(markdown string, context core.MarkdownTransformContext) string {
					return context.MessageType + ":" + markdown
				},
			},
		})

		out := rendered(t, component.Render(80))
		if !strings.Contains(out, "assistant:answer") || !strings.Contains(out, "assistant-thinking:reasoning") {
			t.Fatalf("rendered = %q", out)
		}
		if len(message.Content) != 2 {
			t.Fatalf("message content mutated: %d blocks", len(message.Content))
		}
		if text, ok := message.Content[0].(*types.TextContent); !ok || text.Text != "answer" {
			t.Fatalf("content[0] mutated: %+v", message.Content[0])
		}
	})
}

// TestUserMessageComponentPadding ports assistant-message.test.ts's
// "uses configured output padding for user messages".
func TestUserMessageComponentPadding(t *testing.T) {
	initTestTheme(t)

	paddedComponent := NewUserMessageComponent("hello", nil, 1, nil, nil)
	paddedLines := paddedComponent.Render(40)
	if !someLineStartsWith(paddedLines, " hello") {
		t.Fatalf("padded lines missing: %v", paddedLines)
	}

	unpaddedComponent := NewUserMessageComponent("hello", nil, 0, nil, nil)
	unpaddedLines := unpaddedComponent.Render(40)
	if !someLineStartsWith(unpaddedLines, "hello") {
		t.Fatalf("unpadded lines missing: %v", unpaddedLines)
	}
}

// someLineContains/someLineStartsWith check the raw (ANSI-carrying) lines
// via stripAnsi.
func someLineContains(lines []string, needle string) bool {
	for _, line := range lines {
		if strings.Contains(utils.StripAnsi(line), needle) {
			return true
		}
	}
	return false
}

func someLineStartsWith(lines []string, prefix string) bool {
	for _, line := range lines {
		if strings.HasPrefix(utils.StripAnsi(line), prefix) {
			return true
		}
	}
	return false
}

var _ tui.Component = (*AssistantMessageComponent)(nil)

// pi: "collapses individual thinking runs when clicked" (v0.85.1).
func TestAssistantMessageCollapsesIndividualThinkingRunsWhenClicked(t *testing.T) {
	initTestTheme(t)

	component := NewAssistantMessageComponent(createAssistantMessage([]types.Content{
		&types.ThinkingContent{Kind: types.KindThinking, Thinking: "first reasoning"},
		&types.TextContent{Kind: types.KindText, Text: "answer"},
		&types.ThinkingContent{Kind: types.KindThinking, Thinking: "second reasoning"},
	}, types.StopReasonStop), nil)

	width := 80
	lines := component.Render(width)
	firstThinkingRow := -1
	for i, line := range lines {
		if strings.Contains(utils.StripAnsi(line), "first reasoning") {
			firstThinkingRow = i
			break
		}
	}
	if firstThinkingRow < 0 {
		t.Fatal("expected first thinking run to render")
	}

	clickCount := 1
	event := &tui.TuiMouseEvent{
		Type:       tui.MouseEventTypeClick,
		Button:     tui.MouseButtonLeft,
		X:          1,
		Y:          firstThinkingRow,
		ScreenX:    1,
		ScreenY:    firstThinkingRow,
		Width:      width,
		Height:     len(lines),
		ClickCount: &clickCount,
	}
	result := component.HandleMouse(event)
	if result == nil || !result.Handled {
		t.Fatal("expected click to be handled")
	}

	collapsed := rendered(t, component.Render(width))
	if strings.Contains(collapsed, "first reasoning") {
		t.Fatal("expected first thinking run to be collapsed")
	}
	if !strings.Contains(collapsed, "Thinking...") {
		t.Fatal("expected collapsed label")
	}
	if !strings.Contains(collapsed, "second reasoning") {
		t.Fatal("expected second thinking run to stay visible")
	}
}
