// interactive_tui_test.go (pi: test/interactive-tui.test.ts) — the TUI
// composition root and fullscreen copy flow. Ported in S5b (existed at the
// pi baseline; pig deferred the file to this batch, ported with the v0.85.1
// preferSelection / jump-to-end / copy-on-select deltas).
//
// Test adaptations (declared): pi fakes InteractiveMode.prototype methods by
// reassigning `this` fields; pig's methods are bound to the concrete struct,
// so the copy tests build a real session via the seam helpers and a
// partially-constructed InteractiveMode. The clipboard write goes through
// the interactiveCopyToClipboard seam (pi: vi.mock of utils/clipboard.ts).
package interactive

import (
	"strings"
	"testing"
	"time"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/ai/types"
	core "github.com/ZoroSpace/pig/codingagent/core"
	"github.com/ZoroSpace/pig/tui"
)

func newRecordingTerminal(cols, rows int) *tui.VirtualTerminal {
	return tui.NewVirtualTerminal(cols, rows)
}

// pi: "shows the configured jump-to-bottom shortcut while scrolled up".
func TestInteractiveTuiJumpToEndShortcut(t *testing.T) {
	initTestTheme(t)
	previous := tui.GetKeybindings()
	tui.SetKeybindings(core.NewKeybindingsManager(tui.KeybindingsConfig{
		"tui.altScreen.bottom": {"ctrl+j"},
	}, "").KeybindingsManager)
	t.Cleanup(func() { tui.SetKeybindings(previous) })

	terminal := newRecordingTerminal(50, 4)
	ui := createInteractiveTui(createInteractiveTuiOptions{
		tuiMode:            core.TuiModeFullscreen,
		showHardwareCursor: false,
		logDirectory:       t.TempDir(),
		terminal:           terminal,
	}).(*tui.TuiAltScreen)
	ui.SetLayoutRoot(tui.NewScrollView(
		tui.NewText("line 1\nline 2\nline 3\nline 4\nline 5\nline 6\nline 7\nline 8", 0, 0),
		&tui.ScrollViewOptions{Follow: "end", Primary: true},
	))
	ui.Start()
	t.Cleanup(func() { ui.Stop(nil) })

	terminal.WaitForRender()
	// Wheel up one line (SGR mouse press, button 64).
	terminal.SendInput("\x1b[<64;1;1M")
	terminal.WaitForRender()

	viewport := terminal.GetViewport()
	if len(viewport) < 4 {
		t.Fatalf("viewport too small: %d lines", len(viewport))
	}
	if !strings.Contains(viewport[3], "↓ Jump to latest message · Ctrl+J") {
		t.Fatalf("viewport[3] = %q, want jump-to-end indicator", viewport[3])
	}
}

// copySelectionFixture assembles the partial InteractiveMode and fullscreen
// renderer the copy tests drive (pi: the CopyCommandContext object + the
// prototype handleCopyCommand call).
type copySelectionFixture struct {
	ui       tui.TUI
	terminal *tui.VirtualTerminal
	mode     *InteractiveMode
	copied   []string
}

func newCopySelectionFixture(t *testing.T, fullscreenCopyOnSelect bool) *copySelectionFixture {
	t.Helper()
	initTestTheme(t)

	f := &copySelectionFixture{terminal: newRecordingTerminal(40, 4)}
	f.ui = createInteractiveTui(createInteractiveTuiOptions{
		tuiMode:                core.TuiModeFullscreen,
		showHardwareCursor:     false,
		logDirectory:           t.TempDir(),
		terminal:               f.terminal,
		fullscreenCopyOnSelect: fullscreenCopyOnSelect,
	})
	copyToClipboardForTest(t, func(text string) error {
		f.copied = append(f.copied, text)
		return nil
	})

	session := createInteractiveTestSession(t, &fixtureResourceLoader{}, nil)
	session.Agent.State().Messages = []agent.AgentMessage{
		types.AssistantMessage{
			Role:       types.RoleAssistant,
			Content:    []types.Content{types.TextContent{Kind: types.KindText, Text: "assistant response"}},
			StopReason: types.StopReasonStop,
		},
	}
	f.mode = newSeamTestMode(session, nil)
	f.mode.ui = f.ui
	f.mode.renderer = f.ui

	f.ui.AddChild(tui.NewText("alpha\nbeta\ngamma\ndelta", 0, 0))
	return f
}

// dragSelect performs a left-button press → drag → release across the
// document (pi: the SGR mouse input sequence).
func (f *copySelectionFixture) dragSelect() {
	f.terminal.WaitForRender()
	f.terminal.SendInput("\x1b[<0;1;1M")  // press (1,1)
	f.terminal.SendInput("\x1b[<32;4;2M") // drag (4,2)
	f.terminal.SendInput("\x1b[<0;4;2m")  // release (4,2)
	f.terminal.WaitForRender()
	// The copy-on-select write lands asynchronously relative to SendInput
	// (pig: input processing runs on a goroutine; pi is synchronous on the
	// event loop). Wait for it before clearing the mock
	// (pi: clipboardMocks.copyToClipboard.mockClear()).
	deadline := time.Now().Add(2 * time.Second)
	for len(f.copied) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	f.copied = nil
}

// pi: "copies an active fullscreen selection when copy-on-select is disabled".
func TestInteractiveTuiCopiesSelectionWhenCopyOnSelectDisabled(t *testing.T) {
	f := newCopySelectionFixture(t, false)
	f.ui.Start()
	t.Cleanup(func() { f.ui.Stop(nil) })

	f.dragSelect()
	f.mode.handleCopyCommand(true, true)
	f.terminal.WaitForRender()

	if len(f.copied) != 1 || f.copied[0] != "alpha\nbeta" {
		t.Fatalf("copied = %v, want the selection text", f.copied)
	}
}

// pi: "copies the last assistant message with an active fullscreen selection
// when copy-on-select is enabled".
func TestInteractiveTuiCopiesAssistantMessageWhenCopyOnSelectEnabled(t *testing.T) {
	f := newCopySelectionFixture(t, true)
	f.ui.Start()
	t.Cleanup(func() { f.ui.Stop(nil) })

	f.dragSelect()
	f.mode.handleCopyCommand(true, true)
	f.terminal.WaitForRender()

	if len(f.copied) != 1 || f.copied[0] != "assistant response" {
		t.Fatalf("copied = %v, want the assistant response", f.copied)
	}
}

// pi: "drops the fullscreen selection when session entries are re-rendered"
// (#9311 regression): selection coordinates survived session switches and
// other transcript rebuilds, highlighting unrelated text in the new
// transcript.
func TestInteractiveTuiRenderSessionEntriesDropsSelection(t *testing.T) {
	f := newCopySelectionFixture(t, false)
	f.ui.Start()
	t.Cleanup(func() { f.ui.Stop(nil) })

	f.terminal.WaitForRender()
	f.terminal.SendInput("\x1b[<0;1;1M")  // press (1,1)
	f.terminal.SendInput("\x1b[<32;4;2M") // drag (4,2)
	f.terminal.SendInput("\x1b[<0;4;2m")  // release (4,2)
	f.terminal.WaitForRender()
	altScreen, ok := f.ui.(*tui.TuiAltScreen)
	if !ok {
		t.Fatal("renderer is not the alt screen")
	}
	if !altScreen.HasActiveSelection() {
		t.Fatal("expected an active selection after drag")
	}

	f.mode.renderSessionEntries(nil, nil)

	if altScreen.HasActiveSelection() {
		t.Fatal("renderSessionEntries must drop the fullscreen selection")
	}
}

// pi: "flashes Copied! for the copy shortcut in fullscreen mode".
func TestInteractiveTuiFlashesCopiedInFullscreen(t *testing.T) {
	f := newCopySelectionFixture(t, true)
	f.ui.Start()
	t.Cleanup(func() { f.ui.Stop(nil) })

	f.terminal.WaitForRender()
	f.mode.handleCopyCommand(true, true)
	f.terminal.WaitForRender()

	if len(f.copied) != 1 || f.copied[0] != "assistant response" {
		t.Fatalf("copied = %v, want the assistant response", f.copied)
	}
	viewport := f.terminal.GetViewport()
	found := false
	for _, line := range viewport {
		if strings.Contains(line, "Copied!") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("viewport missing Copied! flash: %v", viewport)
	}
}
