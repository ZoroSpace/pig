// Ported from pi packages/coding-agent/test/mermaid.test.ts as part of T5b-4.
// The stub theme is possible because MermaidTheme is a pig-side interface
// (pi passes a stub object for the Theme type).
package components

import (
	"strings"
	"testing"

	"github.com/ZoroSpace/pig/codingagent/core"
)

type stubMermaidTheme struct{}

func (stubMermaidTheme) Fg(color string, text string) string {
	return "<" + color + ">" + text + "</" + color + ">"
}

func (stubMermaidTheme) Bold(text string) string { return "<bold>" + text + "</bold>" }

type transformOptions struct {
	maxWidth    int
	isStreaming bool
	messageType string
	mode        core.MermaidRenderingMode
	theme       MermaidTheme
}

func transformMermaid(markdown string, options transformOptions) string {
	mode := options.mode
	if mode == "" {
		mode = core.MermaidRenderingStreaming
	}
	messageType := options.messageType
	if messageType == "" {
		messageType = "assistant"
	}
	maxWidth := options.maxWidth
	if maxWidth == 0 {
		maxWidth = 100
	}
	transformer := CreateMermaidMarkdownTransformer(MermaidTransformerOptions{
		GetMode: func() core.MermaidRenderingMode { return mode },
		Theme: func() MermaidTheme {
			if options.theme == nil {
				return nil
			}
			return options.theme
		},
	})
	return transformer(markdown, core.MarkdownTransformContext{
		AvailableWidth: maxWidth,
		IsStreaming:    options.isStreaming,
		MessageType:    messageType,
	})
}

func TestMermaidReplacesCodeBlocksWithDiagrams(t *testing.T) {
	markdown := "Before\n\n```mermaid\nflowchart LR\n  A[Start] --> B[Done]\n```\nAfter"
	rendered := transformMermaid(markdown, transformOptions{})

	for _, want := range []string{"Before", "┌───────┐", "│ Start ├───▶│ Done │",
		"└───────┘    └──────┘`\nAfter", "After"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("expected rendered to contain %q\nrendered:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, "```mermaid") {
		t.Errorf("expected rendered to not contain ```mermaid\nrendered:\n%s", rendered)
	}
}

func TestMermaidLeavesUnsupportedAndOversizedUnchanged(t *testing.T) {
	unsupported := "```mermaid\npie\n  title Pets\n  \"Dogs\" : 4\n```"
	oversized := "```mermaid\nflowchart LR\n  A[Start] --> B[Done]\n```"

	if got := transformMermaid(unsupported, transformOptions{}); got != unsupported {
		t.Errorf("unsupported:\n got %q\nwant %q", got, unsupported)
	}
	if got := transformMermaid(oversized, transformOptions{maxWidth: 10}); got != oversized {
		t.Errorf("oversized:\n got %q\nwant %q", got, oversized)
	}
}

func TestMermaidMapsSemanticSpansThroughTheme(t *testing.T) {
	rendered := transformMermaid("```mermaid\nflowchart LR\n  A --> B\n```",
		transformOptions{theme: stubMermaidTheme{}})

	for _, want := range []string{"<borderMuted>", "<accent>"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("expected rendered to contain %q\nrendered:\n%s", want, rendered)
		}
	}
}

func TestMermaidRendersIncompleteBlocksDuringStreaming(t *testing.T) {
	partialMarkdown := "```mermaid\nflowchart LR\n  A --> B"

	if rendered := transformMermaid(partialMarkdown, transformOptions{isStreaming: true}); !strings.Contains(rendered, "───▶") {
		t.Errorf("expected rendered to contain ───▶\nrendered:\n%s", rendered)
	}
}

// pi v0.86.0 (grok-mermaid 0.2.3): `A:::class --> B` parses — the class tag
// is swallowed instead of dropping the rest of the line.
func TestMermaidRendersDiagramsClassAssignments(t *testing.T) {
	markdown := "```mermaid\nflowchart LR\n  A[Foo]:::highlight --> B[Bar]\n```"
	rendered := transformMermaid(markdown, transformOptions{})

	if strings.Contains(rendered, "```mermaid") {
		t.Errorf("expected rendered to not contain ```mermaid\nrendered:\n%s", rendered)
	}
	if strings.Contains(rendered, "Mermaid diagram not rendered") {
		t.Errorf("expected rendered to not contain the warning\nrendered:\n%s", rendered)
	}
	if !strings.Contains(rendered, "│ Foo ├───▶│ Bar │") {
		t.Errorf("expected rendered to contain the linked nodes\nrendered:\n%s", rendered)
	}
}

func TestMermaidFallsBackWithWarningAfterStreaming(t *testing.T) {
	markdown := "```mermaid\nflowchart LR\n  A[Foo] invalid\n```"
	final := transformMermaid(markdown, transformOptions{})
	followedByText := transformMermaid(markdown+"\nFollowing text", transformOptions{})
	streaming := transformMermaid(markdown, transformOptions{isStreaming: true})

	if !strings.Contains(final, markdown) {
		t.Errorf("expected final to contain the source markdown\nfinal:\n%s", final)
	}
	if !strings.Contains(final, "```\n`Mermaid diagram not rendered") {
		t.Errorf("expected final to contain the warning\nfinal:\n%s", final)
	}
	if !strings.Contains(final, `dropped, expected a link: "invalid"`) {
		t.Errorf("expected final to contain the dropped link warning\nfinal:\n%s", final)
	}
	if strings.Contains(final, "more)") {
		t.Errorf("expected final to not contain (more)\nfinal:\n%s", final)
	}
	if !strings.Contains(followedByText, "  \nFollowing text") {
		t.Errorf("expected followedByText to contain the hard break\nfollowedByText:\n%s", followedByText)
	}
	if strings.Contains(streaming, "Mermaid diagram not rendered") {
		t.Errorf("expected streaming to not contain the warning\nstreaming:\n%s", streaming)
	}
	if strings.Contains(streaming, "```mermaid") {
		t.Errorf("expected streaming to not contain ```mermaid\nstreaming:\n%s", streaming)
	}
	if strings.Contains(streaming, "Mermaid diagram not rendered") {
		t.Errorf("expected streaming to not contain the warning\nstreaming:\n%s", streaming)
	}
}

func TestMermaidSummarizesAdditionalPartialRenderWarnings(t *testing.T) {
	markdown := "```mermaid\nflowchart LR\n  A[Foo] invalid\n  B[Bar] also-invalid\n```"
	rendered := transformMermaid(markdown, transformOptions{})

	if !strings.Contains(rendered, markdown) {
		t.Errorf("expected rendered to contain the source markdown\nrendered:\n%s", rendered)
	}
	if !strings.Contains(rendered, `dropped, expected a link: "invalid"`) {
		t.Errorf("expected rendered to contain the first warning\nrendered:\n%s", rendered)
	}
	if !strings.Contains(rendered, "(+1 more)") {
		t.Errorf("expected rendered to contain (+1 more)\nrendered:\n%s", rendered)
	}
	if strings.Contains(rendered, `dropped, expected a link: "also-invalid"`) {
		t.Errorf("expected rendered to not contain the second warning\nrendered:\n%s", rendered)
	}
}

func TestMermaidRespectsRenderingModesAndSkipsThinking(t *testing.T) {
	markdown := "```mermaid\nflowchart LR\n  A --> B\n```"

	if got := transformMermaid(markdown, transformOptions{mode: core.MermaidRenderingOff}); got != markdown {
		t.Errorf("mode off:\n got %q\nwant %q", got, markdown)
	}
	if got := transformMermaid(markdown, transformOptions{mode: core.MermaidRenderingFinal, isStreaming: true}); got != markdown {
		t.Errorf("mode final while streaming:\n got %q\nwant %q", got, markdown)
	}
	if got := transformMermaid(markdown, transformOptions{mode: core.MermaidRenderingFinal}); strings.Contains(got, "```mermaid") {
		t.Errorf("mode final not streaming should render\n got:\n%s", got)
	}
	if got := transformMermaid(markdown, transformOptions{messageType: "assistant-thinking"}); got != markdown {
		t.Errorf("thinking block:\n got %q\nwant %q", got, markdown)
	}
}
