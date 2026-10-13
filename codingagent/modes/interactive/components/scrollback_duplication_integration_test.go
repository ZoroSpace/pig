package components

import (
	"fmt"
	"strings"
	"testing"

	"github.com/ZoroSpace/pig/ai/types"
	coretools "github.com/ZoroSpace/pig/codingagent/core/tools"
	"github.com/ZoroSpace/pig/codingagent/utils"
	"github.com/ZoroSpace/pig/tui"
)

// Integration guards for the scrollback-duplication fix
// (docs/fix/2026-10-13-scrollback-duplication-window-repaint.md): the real
// streaming paths must never emit a frame whose paint duplicates scrollback
// content. Growth frames ride the clamp path (repaint visible range only,
// rows above stay untouched — no rewrite of already-scrolled rows), so each
// buffer line must appear exactly once in the virtual terminal after every
// frame. If one of these starts failing, a streaming component learned to
// shrink-span the viewport and the main screen will duplicate history.

// assertBufferOnce verifies the virtual terminal buffer contains each
// rendered buffer line exactly once (ANSI stripped; OSC 133 markers are
// zero-width and never reach cells).
func assertBufferOnce(t *testing.T, terminal *tui.VirtualTerminal, want []string, phase string) {
	t.Helper()
	buf := terminal.GetScrollBuffer()
	if len(buf) < len(want) {
		t.Fatalf("%s: scroll buffer %d rows < expected %d", phase, len(buf), len(want))
	}
	for i, w := range want {
		if got := strings.TrimRight(buf[i], " "); got != w {
			t.Fatalf("%s: buffer row %d = %q, want %q\nfull buffer:\n%s", phase, i, got, w, dumpScrollBuffer(buf))
		}
	}
	for i := len(want); i < len(buf); i++ {
		if strings.TrimRight(buf[i], " ") != "" {
			t.Fatalf("%s: %d unexpected extra scrollback rows (first extra at %d = %q)\nfull buffer:\n%s",
				phase, len(buf)-len(want), i, buf[i], dumpScrollBuffer(buf))
		}
	}
}

func dumpScrollBuffer(buf []string) string {
	var b strings.Builder
	for i, row := range buf {
		fmt.Fprintf(&b, "buf[%03d]=%q\n", i, row)
	}
	return b.String()
}

// TestIntegrationStreamingMessageNoScrollbackDuplication drives a REAL
// AssistantMessageComponent through the reported turn shape (long streaming
// thinking → streamed text → completion) inside a REAL main screen with a
// realistic 40-row viewport.
func TestIntegrationStreamingMessageNoScrollbackDuplication(t *testing.T) {
	initTestTheme(t)

	terminal := tui.NewVirtualTerminal(100, 40)
	screen := tui.NewTuiMainScreen(terminal, nil, "")
	component := NewAssistantMessageComponent(nil, nil)
	screen.AddChild(component)
	screen.Start()
	t.Cleanup(func() { screen.Stop(nil) })

	phase := 0
	frame := func(tag string, msg *types.AssistantMessage, streaming bool) {
		// Mutate+render under the renderer lock (pig RunExclusive): the
		// component's mutation surface is serialized by the UI dispatch
		// discipline in production; the test must not bypass it while the
		// screen's throttled render loop is live.
		screen.RunExclusive(func() {
			component.UpdateContent(msg, streaming)
			screen.RenderNow(false)
		})
		phase++
		want := component.Render(100)
		for i := range want {
			want[i] = strings.TrimRight(utils.StripAnsi(want[i]), " ")
		}
		assertBufferOnce(t, terminal, want, fmt.Sprintf("frame%02d/%s", phase, tag))
	}

	// Long thinking streams in (taller than the 40-row viewport).
	thinking := ""
	for i := 0; i < 60; i++ {
		thinking += fmt.Sprintf("thinking line %02d with some prose to wrap around\n", i)
		msg := createAssistantMessage([]types.Content{
			&types.ThinkingContent{Kind: types.KindThinking, Thinking: thinking},
		}, "")
		frame("thinking", msg, true)
	}

	// Text starts streaming below the thinking block.
	text := ""
	for i := 0; i < 30; i++ {
		text += fmt.Sprintf("answer paragraph %02d streaming along nicely\n", i)
		msg := createAssistantMessage([]types.Content{
			&types.ThinkingContent{Kind: types.KindThinking, Thinking: thinking},
			&types.TextContent{Kind: types.KindText, Text: text},
		}, "")
		frame("text", msg, true)
	}

	// Message completes: streaming transform drops, content finalized.
	msg := createAssistantMessage([]types.Content{
		&types.ThinkingContent{Kind: types.KindThinking, Thinking: thinking},
		&types.TextContent{Kind: types.KindText, Text: text + "\n\nDone."},
	}, types.StopReasonStop)
	frame("end", msg, false)
}

// TestIntegrationBashToolCardNoScrollbackDuplication drives a REAL bash tool
// card (long heredoc command, streaming partial output, completion) inside a
// REAL main screen with a 40-row viewport.
func TestIntegrationBashToolCardNoScrollbackDuplication(t *testing.T) {
	initTestTheme(t)

	terminal := tui.NewVirtualTerminal(100, 40)
	screen := tui.NewTuiMainScreen(terminal, nil, "")

	definition := coretools.CreateBashToolDefinition(mustCwd(t), nil)
	component := NewToolExecutionComponent("bash", "tool-1",
		map[string]any{"command": "echo hi"},
		ToolExecutionOptions{}, definition, &toolExecutionFakeTUI{}, mustCwd(t))
	screen.AddChild(component)
	screen.Start()
	t.Cleanup(func() { screen.Stop(nil) })

	phase := 0
	frame := func(tag string, mutate func()) {
		// See the assistant-message test: mutations ride RunExclusive so the
		// async render loop cannot interleave with direct component calls.
		screen.RunExclusive(func() {
			mutate()
			screen.RenderNow(false)
		})
		phase++
		want := component.Render(100)
		for i := range want {
			want[i] = strings.TrimRight(utils.StripAnsi(want[i]), " ")
		}
		assertBufferOnce(t, terminal, want, fmt.Sprintf("frame%02d/%s", phase, tag))
	}

	// Long multi-line command streams in via UpdateArgs.
	command := "cat <<'EOF'\n"
	for i := 0; i < 30; i++ {
		command += fmt.Sprintf("commit line %02d\n", i)
	}
	command += "EOF"
	frame("args", func() { component.UpdateArgs(map[string]any{"command": command}) })

	// Output streams in (throttled partial updates).
	output := ""
	for i := 0; i < 20; i++ {
		output += fmt.Sprintf("output line %02d\n", i)
		frame("partial", func() {
			component.UpdateResult(ToolExecutionResult{
				Content: []types.Content{types.TextContent{Kind: types.KindText, Text: output}},
				IsError: false,
			}, true)
		})
	}

	// Completion.
	frame("final", func() {
		component.UpdateResult(ToolExecutionResult{
			Content: []types.Content{types.TextContent{Kind: types.KindText, Text: output}},
			IsError: false,
		}, false)
	})
}
