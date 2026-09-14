// Discard-terminal frame probe: real TuiMainScreen render pipeline with real
// RequestRender throttling, but terminal output is discarded — isolates pig
// process retention from VirtualTerminal scrollback growth (the probe
// artifact that contaminated TestMemLeakProbeTuiFrames).
//
// Run: go test ./codingagent/modes/interactive/ -run TestMemLeakProbeDiscard -v -timeout 180s
package interactive

import (
	"testing"
	"time"

	"github.com/ZoroSpace/pig/codingagent/modes/interactive/components"
	"github.com/ZoroSpace/pig/tui"
)

// discardTerminal implements Terminal with all output discarded.
type discardTerminal struct{}

func (d *discardTerminal) Start(onInput func(string), onResize func()) {}
func (d *discardTerminal) Stop()                                       {}
func (d *discardTerminal) DrainInput(maxMs, idleMs int)                {}
func (d *discardTerminal) Write(data string)                           {}
func (d *discardTerminal) Columns() int                                { return 220 }
func (d *discardTerminal) Rows() int                                   { return 50 }
func (d *discardTerminal) KittyProtocolActive() bool                   { return false }
func (d *discardTerminal) MoveBy(lines int)                            {}
func (d *discardTerminal) HideCursor()                                 {}
func (d *discardTerminal) ShowCursor()                                 {}
func (d *discardTerminal) ClearLine()                                  {}
func (d *discardTerminal) ClearFromCursor()                            {}
func (d *discardTerminal) ClearScreen()                                {}
func (d *discardTerminal) SetTitle(title string)                       {}
func (d *discardTerminal) SetProgress(active bool)                     {}

func waitForRenders(t *testing.T, screen *tui.TuiMainScreen, n int) {
	t.Helper()
	// crude pacing: RenderNow forces a synchronous frame regardless of the
	// throttled scheduler, so drive frames directly but keep the async
	// scheduler quiescent.
	for i := 0; i < n; i++ {
		screen.RenderNow(false)
		time.Sleep(2 * time.Millisecond)
	}
}

func TestMemLeakProbeDiscard(t *testing.T) {
	if testing.Short() {
		t.Skip("probe; skip in short mode")
	}
	initProbeTheme(t)
	text := probeMessageText()

	terminal := &discardTerminal{}
	screen := tui.NewTuiMainScreen(terminal, nil, "")
	chat := &tui.Container{}
	screen.AddChild(chat)
	screen.Start()
	defer screen.Stop(nil)

	before := memSnapshotHeap(t)
	const turns = 200
	for i := 0; i < turns; i++ {
		user := components.NewUserMessageComponent("probe question", &mdThemeProbe, 1, nil)
		assistant := components.NewAssistantMessageComponent(testAssistantMessageForProbe(text), &components.AssistantMessageOptions{
			MarkdownTheme: &mdThemeProbe,
		})
		chat.AddChild(user)
		chat.AddChild(assistant)
		waitForRenders(t, screen, 1)
	}
	after := memSnapshotHeap(t)
	delta := int64(after) - int64(before)
	t.Logf("discard-terminal growing: %+8.1f KiB total, %+7.2f KiB/turn", float64(delta)/1024, float64(delta)/1024/turns)
}
