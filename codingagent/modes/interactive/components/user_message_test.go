package components

import (
	"strings"
	"testing"

	"github.com/ZoroSpace/pig/codingagent/core"
	"github.com/ZoroSpace/pig/codingagent/utils"
)

// Port of test/user-message.test.ts.

func TestUserMessageComponent(t *testing.T) {
	t.Run("keeps user message height stable while moving closing OSC markers off line end", func(t *testing.T) {
		initTestTheme(t)

		// pig-only deviation from pi (2026-10-03): the user message renders
		// with border-only framing (top rule, content, bottom rule) instead
		// of the pi full-width background fill. Vertical padding is 0 so the
		// block keeps its pre-border height: 3 lines for a one-line message
		// (docs/feat/BUILTIN-THEME-PALETTES.md §BLOCK-BORDERS). The left
		// frame: solid corner rules ┌─┐ / └─┘, content unbarred.
		component := NewUserMessageComponent("hello", nil, 1, nil, nil)
		lines := component.Render(20)

		if len(lines) != 3 {
			t.Fatalf("lines = %d", len(lines))
		}
		if !strings.Contains(lines[0], osc133ZoneStart) {
			t.Fatalf("line 0 missing zone start: %q", lines[0])
		}
		if !strings.Contains(lines[0], "┌") || !strings.Contains(lines[0], "┐") {
			t.Fatalf("line 0 missing top border corner: %q", lines[0])
		}
		if strings.Contains(lines[0], osc133ZoneEnd) {
			t.Fatalf("line 0 must not contain zone end: %q", lines[0])
		}
		if got := utils.StripAnsi(lines[1]); !strings.Contains(got, "hello") {
			t.Fatalf("line 1 missing text: %q", got)
		}
		if !strings.HasPrefix(lines[2], osc133ZoneEnd+osc133ZoneFinal) {
			t.Fatalf("line 2 missing final markers: %q", lines[2])
		}
		if !strings.Contains(lines[2], "└") || !strings.Contains(lines[2], "┘") {
			t.Fatalf("line 2 missing bottom border corner: %q", lines[2])
		}
	})

	t.Run("chains Markdown transformers with user message context", func(t *testing.T) {
		initTestTheme(t)
		var calls []string
		contextSeen := core.MarkdownTransformContext{}
		component := NewUserMessageComponent("The input is $x^2$.", nil, 1, []core.MarkdownTransformer{
			func(markdown string, context core.MarkdownTransformContext) string {
				calls = append(calls, "formula")
				contextSeen = context
				return strings.Replace(markdown, "$x^2$", "x²", 1)
			},
			func(markdown string, _ core.MarkdownTransformContext) string {
				calls = append(calls, "suffix")
				return markdown + " Done."
			},
		}, nil)

		got := utils.StripAnsi(strings.Join(component.Render(80), "\n"))
		if !strings.Contains(got, "The input is x². Done.") {
			t.Fatalf("rendered = %q", got)
		}
		if len(calls) != 2 || calls[0] != "formula" || calls[1] != "suffix" {
			t.Fatalf("calls = %v", calls)
		}
		if contextSeen.MessageType != "user" || contextSeen.IsStreaming || contextSeen.AvailableWidth != 77 {
			t.Fatalf("context = %+v", contextSeen)
		}
	})

	t.Run("reapplies Markdown transformers when invalidated", func(t *testing.T) {
		initTestTheme(t)
		suffix := "before"
		component := NewUserMessageComponent("Message", nil, 1, []core.MarkdownTransformer{
			func(markdown string, _ core.MarkdownTransformContext) string {
				return markdown + " " + suffix
			},
		}, nil)

		if got := utils.StripAnsi(strings.Join(component.Render(80), "\n")); !strings.Contains(got, "Message before") {
			t.Fatalf("rendered = %q", got)
		}

		suffix = "after"
		component.Invalidate()

		if got := utils.StripAnsi(strings.Join(component.Render(80), "\n")); !strings.Contains(got, "Message after") {
			t.Fatalf("rendered = %q", got)
		}
	})
}
