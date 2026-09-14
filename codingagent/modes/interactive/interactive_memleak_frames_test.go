// Faithful per-frame pipeline probe (docs/fix/2026-09-14-tui-turn-memory-leak.md
// 引用链终审): closes two blind spots in TestMemLeakProbeStreaming —
//  1. that probe's per-frame partial texts are substrings of ONE backing
//     string (Go substrings share the backing array), while the real adapter
//     allocates a fresh string per frame (block.content.Text += delta), and
//  2. it never runs the real TuiMainScreen render loop.
//
// Per turn: user component; MessageStart → streaming component into chat;
// 8 growing frames, each a FRESH accumulated string + UpdateContent + a real
// RenderNow frame; MessageEnd (final content, not streaming) + RenderNow.
// After N turns the retention is bisected: chat.mouseLayout=nil, chat.Clear(),
// then dropping the screen — reporting the heap after each step.
//
// Run: go test ./codingagent/modes/interactive/ -run TestMemLeakProbeFrames -v -timeout 300s
package interactive

import (
	"testing"
	"time"

	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/codingagent/modes/interactive/components"
	"github.com/ZoroSpace/pig/tui"
)

func TestMemLeakProbeFrames(t *testing.T) {
	if testing.Short() {
		t.Skip("probe; skip in short mode")
	}
	initProbeTheme(t)
	full := probeMessageText()
	const turns = 120
	const frames = 8

	terminal := &discardTerminal{}
	screen := tui.NewTuiMainScreen(terminal, nil, "")
	chat := &tui.Container{}
	screen.AddChild(chat)
	screen.Start()
	defer screen.Stop(nil)

	// warmup: one render to allocate steady-state buffers
	for i := 0; i < 5; i++ {
		screen.RenderNow(false)
		time.Sleep(2 * time.Millisecond)
	}
	before := memSnapshotHeap(t)

	for i := 0; i < turns; i++ {
		chat.AddChild(components.NewUserMessageComponent("probe question", &mdThemeProbe, 1, nil))
		chat.AddChild(tui.NewSpacer(1))

		assistant := components.NewAssistantMessageComponent(nil, &components.AssistantMessageOptions{
			MarkdownTheme: &mdThemeProbe,
		})
		chat.AddChild(assistant)

		// Faithful to openai_completions_stream.go: ONE message struct whose
		// Text grows via fresh string allocation per frame (s += delta).
		partial := &types.AssistantMessage{
			Role:    types.RoleAssistant,
			Content: []types.Content{types.NewTextContent("")},
			API:     "openai-completions", Provider: "memprobe", Model: "mock-model",
		}
		acc := ""
		for c := 0; c < frames; c++ {
			acc = acc + full[len(full)*c/frames:len(full)*(c+1)/frames] // fresh backing array each frame
			partial.Content[0] = types.NewTextContent(acc)
			assistant.UpdateContent(partial, true)
			screen.RenderNow(false)
			time.Sleep(time.Millisecond)
		}
		partial.Content[0] = types.NewTextContent(full)
		assistant.UpdateContent(partial, false)
		screen.RenderNow(false)
		time.Sleep(time.Millisecond)
	}
	after := memSnapshotHeap(t)
	perTurn := float64(int64(after)-int64(before)) / 1024 / turns
	t.Logf("frames+render: %+8.1f KiB total, %+7.2f KiB/turn (%d chat children)", float64(int64(after)-int64(before))/1024, perTurn, len(chat.Children))

	// --- bisect 1: clear chat children (transcript component tree) ---
	chat.Clear()
	mid2 := memSnapshotHeap(t)
	t.Logf("after chat.Clear():    %+8.1f KiB total (reclaimed %+6.1f KiB)", float64(mid2)/1024, float64(after-mid2)/1024)

	// --- bisect 2: drop the screen ---
	screen.Stop(nil)
	screen = nil
	mid3 := memSnapshotHeap(t)
	t.Logf("after screen dropped:  %+8.1f KiB total (reclaimed %+6.1f KiB)", float64(mid3)/1024, float64(after-mid3)/1024)

	if perTurn > 16 {
		t.Errorf("faithful frame probe leaks %.1f KiB/turn (>16 KiB/turn by-design budget)", perTurn)
	}
}

// TestMemLeakProbeFramesNoRender is the control: same loop, renders only once
// per turn (not per frame) — isolates the per-frame-render factor.
func TestMemLeakProbeFramesNoRender(t *testing.T) {
	if testing.Short() {
		t.Skip("probe; skip in short mode")
	}
	initProbeTheme(t)
	full := probeMessageText()
	const turns = 120
	const frames = 8

	terminal := &discardTerminal{}
	screen := tui.NewTuiMainScreen(terminal, nil, "")
	chat := &tui.Container{}
	screen.AddChild(chat)
	screen.Start()
	defer screen.Stop(nil)

	for i := 0; i < 5; i++ {
		screen.RenderNow(false)
		time.Sleep(2 * time.Millisecond)
	}
	before := memSnapshotHeap(t)

	for i := 0; i < turns; i++ {
		chat.AddChild(components.NewUserMessageComponent("probe question", &mdThemeProbe, 1, nil))
		chat.AddChild(tui.NewSpacer(1))
		assistant := components.NewAssistantMessageComponent(nil, &components.AssistantMessageOptions{
			MarkdownTheme: &mdThemeProbe,
		})
		chat.AddChild(assistant)
		partial := &types.AssistantMessage{
			Role:    types.RoleAssistant,
			Content: []types.Content{types.NewTextContent("")},
			API:     "openai-completions", Provider: "memprobe", Model: "mock-model",
		}
		acc := ""
		for c := 0; c < frames; c++ {
			acc = acc + full[len(full)*c/frames:len(full)*(c+1)/frames]
			partial.Content[0] = types.NewTextContent(acc)
			assistant.UpdateContent(partial, true)
		}
		partial.Content[0] = types.NewTextContent(full)
		assistant.UpdateContent(partial, false)
		screen.RenderNow(false)
		time.Sleep(time.Millisecond)
	}
	after := memSnapshotHeap(t)
	perTurn := float64(int64(after)-int64(before)) / 1024 / turns
	t.Logf("no-per-frame-render: %+8.1f KiB total, %+7.2f KiB/turn", float64(int64(after)-int64(before))/1024, perTurn)
}
