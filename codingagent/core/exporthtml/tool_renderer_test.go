// tool_renderer_test.go — CreateToolHtmlRenderer coverage (pi's
// tool-renderer.ts has no dedicated test file; P8-EH1 builds the coverage
// pig-side).
package exporthtml

import (
	"strings"
	"testing"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/ai/types"
	coretools "github.com/ZoroSpace/pig/codingagent/core/tools"
	"github.com/ZoroSpace/pig/codingagent/modes/interactive/theme"
	"github.com/ZoroSpace/pig/tui"
)

// tuiComponent is tui.Component (named alias for the fake signatures).
type tuiComponent = tui.Component

// fakeComponent renders canned lines (tui.Component).
type fakeComponent struct {
	lines []string
}

func (f *fakeComponent) Render(width int) []string { return f.lines }
func (f *fakeComponent) Invalidate()               {}

// fakeRendererState is what NewRenderState returns for the fake tool.
type fakeRendererState struct {
	Marker string
}

// fakeToolDefinition exercises the render pipeline with ANSI-bearing
// output.
func fakeToolDefinition(t *testing.T, opts struct {
	callLines   []string
	resultLines []string
	expanded    []string
	noRenderers bool
	panicCall   bool
}) *coretools.ToolDefinition {
	t.Helper()
	def := &coretools.ToolDefinition{
		Name:           "mytool",
		Description:    "A fake tool",
		NewRenderState: func() any { return &fakeRendererState{Marker: "init"} },
	}
	if opts.noRenderers {
		return def
	}
	def.RenderCall = func(args any, theme coretools.UITheme, context coretools.ToolRenderContext) tui.Component {
		if opts.panicCall {
			panic("renderer exploded")
		}
		// Prove the context shape reaches the renderer.
		if context.ToolCallID == "" || !context.ExecutionStarted || !context.ArgsComplete || !context.IsPartial {
			t.Errorf("unexpected render context: %+v", context)
		}
		if state, ok := context.State.(*fakeRendererState); !ok || state.Marker != "init" {
			t.Errorf("state not initialized via NewRenderState: %#v", context.State)
		}
		return &fakeComponent{lines: opts.callLines}
	}
	def.RenderResult = func(result agent.AgentToolResult, options coretools.ToolRenderResultOptions, theme coretools.UITheme, context coretools.ToolRenderContext) tui.Component {
		lines := opts.resultLines
		if options.Expanded {
			lines = opts.expanded
		}
		if context.IsError != false && !context.IsError {
			t.Errorf("isError not propagated")
		}
		if len(result.Content) != 1 {
			t.Errorf("result content not propagated: %#v", result.Content)
		}
		return &fakeComponent{lines: lines}
	}
	return def
}

func newTestTheme(t *testing.T) *theme.Theme {
	t.Helper()
	th, err := theme.NewTheme(map[string]any{
		"text": "#e5e5e7", "accent": "#0087ff", "muted": "#7a7a7a",
		"userMessageBg": "#343541",
	}, map[string]any{}, theme.ColorMode256, nil)
	if err != nil {
		t.Fatalf("NewTheme: %v", err)
	}
	return th
}

func newFakeRenderer(t *testing.T, def *coretools.ToolDefinition) ToolHtmlRenderer {
	t.Helper()
	return CreateToolHtmlRenderer(ToolHtmlRendererDeps{
		GetToolDefinition: func(name string) *coretools.ToolDefinition {
			if name != "mytool" {
				return nil
			}
			return def
		},
		ThemeFunc: func() *theme.Theme {
			return newTestTheme(t)
		},
		Cwd: "/tmp/workdir",
	})
}

func TestToolHtmlRendererRenderCall(t *testing.T) {
	def := fakeToolDefinition(t, struct {
		callLines   []string
		resultLines []string
		expanded    []string
		noRenderers bool
		panicCall   bool
	}{callLines: []string{"\x1b[36m◈ mytool\x1b[0m", "  arg summary"}, resultLines: nil, expanded: nil})
	r := newFakeRenderer(t, def)

	html := r.RenderCall("call-1", "mytool", map[string]any{"cmd": "ls"})
	want := `<div class="ansi-line"><span style="color:#008080">◈ mytool</span></div>` +
		`<div class="ansi-line">  arg summary</div>`
	if html != want {
		t.Fatalf("RenderCall html =\n%s\nwant:\n%s", html, want)
	}
}

func TestToolHtmlRendererRenderCallFallbacks(t *testing.T) {
	t.Run("unknown tool", func(t *testing.T) {
		r := newFakeRenderer(t, fakeToolDefinition(t, struct {
			callLines   []string
			resultLines []string
			expanded    []string
			noRenderers bool
			panicCall   bool
		}{}))
		if html := r.RenderCall("call-1", "unknown", nil); html != "" {
			t.Fatalf("unknown tool should yield empty html, got %q", html)
		}
	})
	t.Run("definition without renderCall", func(t *testing.T) {
		r := newFakeRenderer(t, fakeToolDefinition(t, struct {
			callLines   []string
			resultLines []string
			expanded    []string
			noRenderers bool
			panicCall   bool
		}{noRenderers: true}))
		if html := r.RenderCall("call-1", "mytool", nil); html != "" {
			t.Fatalf("renderer-less tool should yield empty html, got %q", html)
		}
	})
	t.Run("panicking renderer", func(t *testing.T) {
		r := newFakeRenderer(t, fakeToolDefinition(t, struct {
			callLines   []string
			resultLines []string
			expanded    []string
			noRenderers bool
			panicCall   bool
		}{panicCall: true}))
		if html := r.RenderCall("call-1", "mytool", nil); html != "" {
			t.Fatalf("panicking renderer should yield empty html, got %q", html)
		}
	})
}

func TestToolHtmlRendererRenderResult(t *testing.T) {
	def := fakeToolDefinition(t, struct {
		callLines   []string
		resultLines []string
		expanded    []string
		noRenderers bool
		panicCall   bool
	}{
		callLines: nil,
		resultLines: []string{
			"",
			"  \x1b[32moutput line\x1b[0m",
			"   ",
			"  tail",
			"",
		},
		expanded: []string{"  expanded output", ""},
	})
	r := newFakeRenderer(t, def)

	result := []types.Content{types.TextContent{Kind: types.KindText, Text: "hello"}}
	collapsed, expanded, ok := r.RenderResult("call-2", "mytool", result, map[string]any{"k": "v"}, false)
	if !ok {
		t.Fatal("RenderResult should succeed")
	}
	// Blank leading/trailing lines trimmed.
	if !strings.Contains(collapsed, `<div class="ansi-line">  <span style="color:#008000">output line</span></div>`) {
		t.Fatalf("collapsed missing styled line:\n%s", collapsed)
	}
	if strings.HasPrefix(collapsed, `<div class="ansi-line">&nbsp;</div>`) {
		t.Fatalf("collapsed should be trimmed of blank lines:\n%s", collapsed)
	}
	if !strings.Contains(collapsed, "  tail") {
		t.Fatalf("collapsed lost the tail line:\n%s", collapsed)
	}
	if !strings.Contains(expanded, "expanded output") {
		t.Fatalf("expanded missing content:\n%s", expanded)
	}

}

func TestToolHtmlRendererRenderResultOmitsIdenticalCollapsed(t *testing.T) {
	// pi: collapsed is spread in only when non-empty AND != expanded.
	def := fakeToolDefinition(t, struct {
		callLines   []string
		resultLines []string
		expanded    []string
		noRenderers bool
		panicCall   bool
	}{
		resultLines: []string{"  same output"},
		expanded:    []string{"  same output"},
	})
	r := newFakeRenderer(t, def)
	result := []types.Content{types.TextContent{Kind: types.KindText, Text: "hello"}}
	collapsed, expanded, ok := r.RenderResult("call-3", "mytool", result, nil, false)
	if !ok {
		t.Fatal("RenderResult should succeed")
	}
	if collapsed != "" {
		t.Fatalf("identical collapsed/expanded must omit collapsed, got %q", collapsed)
	}
	if expanded == "" {
		t.Fatal("expanded must survive")
	}
}

func TestToolHtmlRendererRenderResultFallbacks(t *testing.T) {
	t.Run("unknown tool", func(t *testing.T) {
		r := newFakeRenderer(t, fakeToolDefinition(t, struct {
			callLines   []string
			resultLines []string
			expanded    []string
			noRenderers bool
			panicCall   bool
		}{}))
		if _, _, ok := r.RenderResult("c", "nope", nil, nil, true); ok {
			t.Fatal("unknown tool should not render")
		}
	})
	t.Run("definition without renderResult", func(t *testing.T) {
		r := newFakeRenderer(t, fakeToolDefinition(t, struct {
			callLines   []string
			resultLines []string
			expanded    []string
			noRenderers bool
			panicCall   bool
		}{noRenderers: true}))
		if _, _, ok := r.RenderResult("c", "mytool", nil, nil, true); ok {
			t.Fatal("renderer-less tool should not render")
		}
	})
}

func TestTrimRenderedResultLines(t *testing.T) {
	lines := []string{"", "  \x1b[1mbold\x1b[0m  ", "\x1b[31m", "text", "\x1b[0m  "}
	got := trimRenderedResultLines(lines)
	if len(got) != 3 || got[0] != "  \x1b[1mbold\x1b[0m  " || got[1] != "\x1b[31m" || got[2] != "text" {
		t.Fatalf("trimRenderedResultLines = %#v", got)
	}
	if got := trimRenderedResultLines([]string{"", "\x1b[31m"}); len(got) != 0 {
		t.Fatalf("all-blank input should trim to empty, got %#v", got)
	}
}
