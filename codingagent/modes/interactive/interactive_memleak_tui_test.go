// Headless TUI frame-loop probe: reproduces the per-frame retention inside
// the real render pipeline (VirtualTerminal + TuiMainScreen + live chat
// components) without a TTY.
//
// Variants:
//   - growing:   append user+assistant components each frame (real cadence)
//   - fixed:     transcript stays constant; only re-render N frames
//     (isolates pure re-render retention from content growth)
//
// Run: go test ./codingagent/modes/interactive/ -run TestMemLeakProbeTuiFrames -v -timeout 120s
package interactive

import (
	"testing"

	"github.com/ZoroSpace/pig/codingagent/modes/interactive/components"
	"github.com/ZoroSpace/pig/tui"
)

func renderNFrames(t *testing.T, screen *tui.TuiMainScreen, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		screen.RenderNow(false)
	}
}

func TestMemLeakProbeTuiFrames(t *testing.T) {
	if testing.Short() {
		t.Skip("probe; skip in short mode")
	}
	initProbeTheme(t)
	text := probeMessageText()

	// --- variant A: growing transcript, one frame per turn ---
	terminalA := tui.NewVirtualTerminal(220, 50)
	screenA := tui.NewTuiMainScreen(terminalA, nil, "")
	chatA := &tui.Container{}
	screenA.AddChild(chatA)
	screenA.Start()
	defer screenA.Stop(nil)

	before := memSnapshotHeap(t)
	const turns = 200
	for i := 0; i < turns; i++ {
		user := components.NewUserMessageComponent("probe question "+string(rune('a'+i%26)), &mdThemeProbe, 1, nil)
		assistant := components.NewAssistantMessageComponent(testAssistantMessageForProbe(text), &components.AssistantMessageOptions{
			MarkdownTheme: &mdThemeProbe,
		})
		chatA.AddChild(user)
		chatA.AddChild(assistant)
		renderNFrames(t, screenA, 1)
	}
	afterA := memSnapshotHeap(t)
	deltaA := int64(afterA) - int64(before)
	t.Logf("growing: %+8.1f KiB total, %+7.2f KiB/turn", float64(deltaA)/1024, float64(deltaA)/1024/turns)

	// --- variant B: fixed content, re-render 200 frames ---
	terminalB := tui.NewVirtualTerminal(220, 50)
	screenB := tui.NewTuiMainScreen(terminalB, nil, "")
	chatB := &tui.Container{}
	screenB.AddChild(chatB)
	screenB.Start()
	defer screenB.Stop(nil)
	// pre-build a transcript equal to ~50 turns
	for i := 0; i < 50; i++ {
		chatB.AddChild(components.NewUserMessageComponent("probe", &mdThemeProbe, 1, nil))
		chatB.AddChild(components.NewAssistantMessageComponent(testAssistantMessageForProbe(text), &components.AssistantMessageOptions{
			MarkdownTheme: &mdThemeProbe,
		}))
	}
	renderNFrames(t, screenB, 2)
	beforeB := memSnapshotHeap(t)
	const frames = 200
	renderNFrames(t, screenB, frames)
	afterB := memSnapshotHeap(t)
	deltaB := int64(afterB) - int64(beforeB)
	t.Logf("fixed re-render: %+8.1f KiB total, %+7.2f KiB/frame", float64(deltaB)/1024, float64(deltaB)/1024/frames)
}
