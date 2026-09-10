// tui/tui_alt_screen_test.go (pi: packages/tui/test/tui-alt-screen.test.ts)
//
// Declared skips (README §4 Phase 8): the two tests constructing the Image
// component ("does not emit Kitty graphics commands or OSC 133 zones in
// iTerm2" / "reuses moved Kitty images without dropping HStack siblings")
// require the Phase 8 image pipeline. The placement/crop/eviction machinery
// they exercise is covered by the encodeKitty-based tests which are ported.
package tui

import (
	"encoding/base64"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/samber/lo"
)

const altTestOsc133ZoneStart = "\x1b]133;A\x07"

// inputOverlay is pi's InputOverlay test class.
type inputOverlay struct {
	mu      sync.Mutex
	focused bool
	inputs  []string
}

func (o *inputOverlay) HandleInput(data string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.inputs = append(o.inputs, data)
}

func (o *inputOverlay) Inputs() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]string(nil), o.inputs...)
}

func (o *inputOverlay) Render(width int) []string { return []string{"overlay"} }

func (o *inputOverlay) Invalidate() {}

func (o *inputOverlay) GetFocused() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.focused
}

func (o *inputOverlay) SetFocused(focused bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.focused = focused
}

// altEditorStub is pi's inline editor object literal (render/handleInput/focused).
type altEditorStub struct {
	mu      sync.Mutex
	focused bool
	inputs  []string
}

func (e *altEditorStub) Render(width int) []string { return []string{"editor"} }
func (e *altEditorStub) Invalidate()               {}
func (e *altEditorStub) HandleInput(data string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.inputs = append(e.inputs, data)
}
func (e *altEditorStub) Inputs() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.inputs...)
}
func (e *altEditorStub) GetFocused() bool { return false }
func (e *altEditorStub) SetFocused(bool)  {}

// recordingTerminal is pi's RecordingTerminal (VirtualTerminal subclass
// recording start/write/stop events).
type altScreenEvent struct {
	typ  string // "write" | "start" | "stop"
	data string
}

type recordingTerminal struct {
	*VirtualTerminal
	mu     sync.Mutex
	events []altScreenEvent
}

func (r *recordingTerminal) Start(onInput func(data string), onResize func()) {
	r.mu.Lock()
	r.events = append(r.events, altScreenEvent{typ: "start"})
	r.mu.Unlock()
	r.VirtualTerminal.Start(onInput, onResize)
}

func (r *recordingTerminal) Write(data string) {
	r.mu.Lock()
	r.events = append(r.events, altScreenEvent{typ: "write", data: data})
	r.mu.Unlock()
	r.VirtualTerminal.Write(data)
}

func (r *recordingTerminal) Stop() {
	r.mu.Lock()
	r.events = append(r.events, altScreenEvent{typ: "stop"})
	r.mu.Unlock()
	r.VirtualTerminal.Stop()
}

func (r *recordingTerminal) Events() []altScreenEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]altScreenEvent(nil), r.events...)
}

// eventsJoined concatenates all recorded write payloads.
func (r *recordingTerminal) eventsJoined() string {
	var b strings.Builder
	for _, e := range r.Events() {
		if e.typ == "write" {
			b.WriteString(e.data)
		}
	}
	return b.String()
}

type viewportSource interface{ GetViewport() []string }

func altTestLines(n int) string {
	parts := make([]string, n)
	for i := range parts {
		parts[i] = "line " + itoa(i+1)
	}
	return strings.Join(parts, "\n")
}

func altTrimmedViewport(v viewportSource) []string {
	lines := v.GetViewport()
	out := make([]string, len(lines))
	for i, line := range lines {
		out[i] = strings.TrimRight(line, " ")
	}
	return out
}

func altAssertViewport(t *testing.T, v viewportSource, want []string) {
	t.Helper()
	got := altTrimmedViewport(v)
	if !stringSlicesEqual(got, want) {
		t.Fatalf("viewport = %q, want %q", got, want)
	}
}

func altEventWritesJoined(events []altScreenEvent) string {
	var b strings.Builder
	for _, e := range events {
		if e.typ == "write" {
			b.WriteString(e.data)
		}
	}
	return b.String()
}

func TestTuiAltScreenRendersTerminalHeightViewportAndPreservesManualScrollPosition(t *testing.T) {
	terminal := NewVirtualTerminal(20, 4)
	tui := NewTuiAltScreen(terminal, nil, "", nil)
	text := NewText(altTestLines(10), 0, 0)
	tui.AddChild(text)
	tui.Start()
	terminal.WaitForRender()

	altAssertViewport(t, terminal, []string{"line 7", "line 8", "line 9", "line 10"})
	if !tui.IsFollowingOutput() {
		t.Fatal("expected following output")
	}

	terminal.SendInput("\x1b[<64;1;1M")
	terminal.WaitForRender()
	altAssertViewport(t, terminal, []string{"line 6", "line 7", "line 8", "line 9"})
	if got := tui.ViewportTop(); got != 5 {
		t.Fatalf("viewportTop = %d, want 5", got)
	}
	if tui.IsFollowingOutput() {
		t.Fatal("expected not following output")
	}

	text.SetText(altTestLines(12))
	tui.RequestRender(false)
	terminal.WaitForRender()
	altAssertViewport(t, terminal, []string{"line 6", "line 7", "line 8", "line 9"})

	tui.Stop(nil)
}

func TestTuiAltScreenKeepsAnExplicitDockFixedWhileTheTranscriptScrolls(t *testing.T) {
	terminal := NewVirtualTerminal(20, 6)
	tui := NewTuiAltScreen(terminal, nil, "", nil)
	transcriptText := NewText(altTestLines(8), 0, 0)
	transcript := NewScrollView(transcriptText, &ScrollViewOptions{Follow: "end", Primary: true})
	dock := NewVStack([]StackChild{NewText("editor", 0, 0), NewText("footer", 0, 0)}, nil)
	tui.SetLayoutRoot(
		NewVStack([]StackChild{
			StackEntryOf(transcript, &StackEntryOptions{Basis: 0, Grow: 1, MinSize: 1}),
			StackEntryOf(dock, &StackEntryOptions{Basis: "auto", MinSize: 1}),
		}, nil),
	)
	tui.Start()
	terminal.WaitForRender()

	altAssertViewport(t, terminal, []string{"line 5", "line 6", "line 7", "line 8", "editor", "footer"})

	// Wheel over the dock falls back to the primary transcript scroll view.
	terminal.SendInput("\x1b[<64;1;6M")
	terminal.WaitForRender()
	altAssertViewport(t, terminal, []string{"line 4", "line 5", "line 6", "line 7", "editor", "footer"})
	if transcript.IsFollowingEnd() {
		t.Fatal("expected transcript not following end")
	}

	transcriptText.SetText(altTestLines(10))
	tui.RequestRender(false)
	terminal.WaitForRender()
	altAssertViewport(t, terminal, []string{"line 4", "line 5", "line 6", "line 7", "editor", "footer"})

	tui.ScrollToBottom()
	terminal.WaitForRender()
	altAssertViewport(t, terminal, []string{"line 7", "line 8", "line 9", "line 10", "editor", "footer"})
	tui.Stop(nil)
}

func TestTuiAltScreenInvalidatesOverlaysWithAnExplicitLayoutRoot(t *testing.T) {
	tui := NewTuiAltScreen(NewVirtualTerminal(20, 6), nil, "", nil)
	invalidated := false
	overlay := &invalidationProbe{onInvalidate: func() { invalidated = true }}
	tui.SetLayoutRoot(NewText("root", 0, 0))
	tui.ShowOverlay(overlay, nil)

	tui.Invalidate()

	if !invalidated {
		t.Fatal("expected overlay invalidation")
	}
	tui.Stop(nil)
}

type invalidationProbe struct {
	onInvalidate func()
}

func (p *invalidationProbe) Render(width int) []string { return []string{"overlay"} }
func (p *invalidationProbe) Invalidate()               { p.onInvalidate() }

func TestTuiAltScreenRoutesWheelInputToTheScrollViewUnderThePointer(t *testing.T) {
	terminal := NewVirtualTerminal(20, 4)
	tui := NewTuiAltScreen(terminal, nil, "", nil)
	left := NewScrollView(NewText("a1\na2\na3\na4\na5\na6\na7", 0, 0), &ScrollViewOptions{
		Follow:  "end",
		Primary: true,
	})
	right := NewScrollView(NewText("b1\nb2\nb3\nb4\nb5\nb6\nb7", 0, 0), &ScrollViewOptions{Follow: "end"})
	tui.SetLayoutRoot(
		NewHStack([]StackChild{
			StackEntryOf(left, &StackEntryOptions{Basis: 10, Shrink: lo.ToPtr(0)}),
			StackEntryOf(right, &StackEntryOptions{Basis: 10, Shrink: lo.ToPtr(0)}),
		}, nil),
	)
	tui.Start()
	terminal.WaitForRender()

	terminal.SendInput("\x1b[<64;15;1M")
	terminal.WaitForRender()
	if left.ScrollTop() != 3 {
		t.Fatalf("left scrollTop = %d, want 3", left.ScrollTop())
	}
	if right.ScrollTop() != 2 {
		t.Fatalf("right scrollTop = %d, want 2", right.ScrollTop())
	}
	altAssertViewport(t, terminal, []string{"a4        b3", "a5        b4", "a6        b5", "a7        b6"})
	tui.Stop(nil)
}

func TestTuiAltScreenUsesButtonMotionTrackingInsideTerminalMultiplexers(t *testing.T) {
	for _, tc := range []struct {
		name       string
		env        map[string]string
		wantMotion string
		notMotion  string
	}{
		{name: "direct terminal", env: map[string]string{"TERM": "xterm-256color"}, wantMotion: "\x1b[?1003h"},
		{name: "tmux environment", env: map[string]string{"TMUX": "/tmp/tmux/default,1,0"}, wantMotion: "\x1b[?1002h", notMotion: "\x1b[?1003h"},
		{name: "tmux TERM", env: map[string]string{"TERM": "tmux-256color"}, wantMotion: "\x1b[?1002h", notMotion: "\x1b[?1003h"},
		{name: "Zellij environment", env: map[string]string{"ZELLIJ": "0"}, wantMotion: "\x1b[?1002h", notMotion: "\x1b[?1003h"},
		{name: "Screen environment", env: map[string]string{"STY": "123.session"}, wantMotion: "\x1b[?1002h", notMotion: "\x1b[?1003h"},
		{name: "Screen TERM", env: map[string]string{"TERM": "screen-256color"}, wantMotion: "\x1b[?1002h", notMotion: "\x1b[?1003h"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, key := range []string{"TMUX", "ZELLIJ", "STY", "TERM"} {
				os.Unsetenv(key)
				t.Cleanup(func() { os.Unsetenv(key) })
			}
			for key, value := range tc.env {
				t.Setenv(key, value)
			}
			terminal := &recordingTerminal{VirtualTerminal: NewVirtualTerminal(20, 6)}
			tui := NewTuiAltScreen(terminal, nil, "", nil)
			tui.Start()
			writes := terminal.eventsJoined()
			if !strings.Contains(writes, tc.wantMotion) {
				t.Fatalf("writes missing %q: %.200s", tc.wantMotion, writes)
			}
			if tc.notMotion != "" && strings.Contains(writes, tc.notMotion) {
				t.Fatalf("writes should not contain %q", tc.notMotion)
			}
			if !strings.Contains(writes, "\x1b[?1006h") {
				t.Fatal("writes should enable SGR mouse encoding")
			}
			tui.Stop(nil)
		})
	}
}

func TestTuiAltScreenInvokesTheRightClickPasteHandlerOnlyOnWindowsOutsideVSCode(t *testing.T) {
	previousPlatform := altScreenPlatform
	defer func() { altScreenPlatform = previousPlatform }()
	terminal := NewVirtualTerminal(20, 6)
	pasteCount := 0
	tui := NewTuiAltScreen(terminal, nil, "", &TuiAltScreenOptions{
		OnRightClickPaste: func() { pasteCount++ },
	})
	func() {
		defer func() {
			altScreenPlatform = previousPlatform
			os.Unsetenv("TERM_PROGRAM")
		}()
		altScreenPlatform = "windows"
		os.Unsetenv("TERM_PROGRAM")
		tui.Start()
		terminal.SendInput("\x1b[<2;1;1M")
		terminal.SendInput("\x1b[<2;1;1m")
		if pasteCount != 1 {
			t.Fatalf("pasteCount = %d, want 1", pasteCount)
		}

		t.Setenv("TERM_PROGRAM", "vscode")
		terminal.SendInput("\x1b[<2;1;1M")
		if pasteCount != 1 {
			t.Fatalf("pasteCount after vscode = %d, want 1", pasteCount)
		}

		altScreenPlatform = "linux"
		os.Unsetenv("TERM_PROGRAM")
		terminal.SendInput("\x1b[<2;1;1M")
		if pasteCount != 1 {
			t.Fatalf("pasteCount after linux = %d, want 1", pasteCount)
		}
	}()
	tui.Stop(nil)
}

func TestTuiAltScreenRevealsAnAutoScrollbarWhenThePointerEntersItsHiddenTrack(t *testing.T) {
	terminal := &recordingTerminal{VirtualTerminal: NewVirtualTerminal(10, 5)}
	tui := NewTuiAltScreen(terminal, nil, "", nil)
	scrollView := NewScrollView(
		NewText(altTestLines(20), 0, 0),
		&ScrollViewOptions{
			Primary:              true,
			Scrollbar:            ScrollViewScrollbarAuto,
			ScrollbarHideDelayMs: lo.ToPtr(20),
		},
	)
	tui.SetLayoutRoot(scrollView)
	tui.Start()
	terminal.WaitForRender()
	if scrollView.IsScrollbarVisible() {
		t.Fatal("expected scrollbar hidden initially")
	}

	terminal.SendInput("\x1b[<35;10;3M")
	terminal.WaitForRender()
	if !scrollView.IsScrollbarVisible() {
		t.Fatal("expected scrollbar visible on track hover")
	}
	if !scrollView.IsScrollbarActive() {
		t.Fatal("expected scrollbar active on track hover")
	}
	if !altViewportSome(terminal, func(line string) bool {
		return strings.ContainsAny(line, "│█")
	}) {
		t.Fatalf("expected a scrollbar glyph in viewport: %q", terminal.GetViewport())
	}

	terminal.SendInput("\x1b[<35;9;3M")
	time.Sleep(40 * time.Millisecond)
	terminal.WaitForRender()
	if scrollView.IsScrollbarVisible() {
		t.Fatal("expected scrollbar hidden after unhover")
	}
	tui.Stop(nil)
}

func TestTuiAltScreenJumpsToAScrollbarTrackPositionAndContinuesDraggingFromThere(t *testing.T) {
	terminal := &recordingTerminal{VirtualTerminal: NewVirtualTerminal(10, 10)}
	tui := NewTuiAltScreen(terminal, nil, "", nil)
	scrollView := NewScrollView(
		NewText(altTestLines(50), 0, 0),
		&ScrollViewOptions{Primary: true, Scrollbar: ScrollViewScrollbarAlways},
	)
	tui.SetLayoutRoot(scrollView)
	tui.Start()
	terminal.WaitForRender()
	if scrollView.ScrollTop() != 0 {
		t.Fatalf("scrollTop = %d, want 0", scrollView.ScrollTop())
	}

	terminal.SendInput("\x1b[<0;10;6M")
	terminal.WaitForRender()
	if scrollView.ScrollTop() != 20 {
		t.Fatalf("scrollTop = %d, want 20", scrollView.ScrollTop())
	}

	terminal.SendInput("\x1b[<32;10;10M")
	terminal.WaitForRender()
	if scrollView.ScrollTop() != 40 {
		t.Fatalf("scrollTop = %d, want 40", scrollView.ScrollTop())
	}

	terminal.SendInput("\x1b[<0;10;10m")
	terminal.WaitForRender()
	if strings.Contains(terminal.eventsJoined(), "\x1b]52;c;") {
		t.Fatal("drag must not copy to clipboard")
	}
	tui.Stop(nil)
}

func TestTuiAltScreenKeepsTheScrollbarColumnSelectableWhileTheThumbIsHidden(t *testing.T) {
	terminal := &recordingTerminal{VirtualTerminal: NewVirtualTerminal(10, 2)}
	tui := NewTuiAltScreen(terminal, nil, "", nil)
	scrollView := NewScrollView(NewText("123456789A\nabcdefghij\nmore\nlines", 0, 0), &ScrollViewOptions{
		Scrollbar: ScrollViewScrollbarAuto,
	})
	tui.SetLayoutRoot(scrollView)
	tui.Start()
	terminal.WaitForRender()
	if scrollView.IsScrollbarVisible() {
		t.Fatal("expected scrollbar hidden")
	}

	terminal.SendInput("\x1b[<0;10;1M")
	terminal.SendInput("\x1b[<32;10;2M")
	terminal.SendInput("\x1b[<0;10;2m")
	terminal.WaitForRender()

	expected := "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte("A\nabcdefghij")) + "\x07"
	found := false
	for _, e := range terminal.Events() {
		if e.typ == "write" && strings.Contains(e.data, expected) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("missing clipboard write %q in %q", expected, terminal.eventsJoined())
	}
	tui.Stop(nil)
}

func TestTuiAltScreenChainsUnusedWheelDeltaToAnOuterScrollView(t *testing.T) {
	terminal := NewVirtualTerminal(20, 4)
	tui := NewTuiAltScreen(terminal, nil, "", &TuiAltScreenOptions{WheelScrollLines: 3})
	inner := NewScrollView(NewText("i1\ni2\ni3\ni4\ni5\ni6", 0, 0), nil)
	outer := NewScrollView(
		NewVStack([]StackChild{
			StackEntryOf(inner, &StackEntryOptions{Basis: 2}),
			NewText("tail1\ntail2\ntail3\ntail4\ntail5", 0, 0),
		}, nil),
		&ScrollViewOptions{Primary: true},
	)
	tui.SetLayoutRoot(outer)
	tui.Start()
	terminal.WaitForRender()

	terminal.SendInput("\x1b[<65;1;1M")
	terminal.WaitForRender()
	if inner.ScrollTop() != 3 {
		t.Fatalf("inner scrollTop = %d, want 3", inner.ScrollTop())
	}
	if outer.ScrollTop() != 0 {
		t.Fatalf("outer scrollTop = %d, want 0", outer.ScrollTop())
	}

	terminal.SendInput("\x1b[<65;1;1M")
	terminal.WaitForRender()
	if inner.ScrollTop() != 4 {
		t.Fatalf("inner scrollTop = %d, want 4", inner.ScrollTop())
	}
	if outer.ScrollTop() != 2 {
		t.Fatalf("outer scrollTop = %d, want 2", outer.ScrollTop())
	}
	tui.Stop(nil)
}

func TestTuiAltScreenSupportsConfigurableKeyboardViewportNavigationWithFourRowsOfPageOverlap(t *testing.T) {
	terminal := NewVirtualTerminal(20, 8)
	tui := NewTuiAltScreen(terminal, nil, "", nil)
	tui.AddChild(NewText(altTestLines(12), 0, 0))
	tui.Start()
	terminal.WaitForRender()

	terminal.SendInput("\x1b[57421u")
	terminal.SendInput("\x1b[57421;1:3u")
	terminal.WaitForRender()
	altAssertViewport(t, terminal, []string{"line 1", "line 2", "line 3", "line 4", "line 5", "line 6", "line 7", "line 8"})

	terminal.SendInput("\x1b[57422u")
	terminal.SendInput("\x1b[57422;1:3u")
	terminal.WaitForRender()
	altAssertViewport(t, terminal, []string{"line 5", "line 6", "line 7", "line 8", "line 9", "line 10", "line 11", "line 12"})

	terminal.SendInput("\x1bOH")
	terminal.WaitForRender()
	altAssertViewport(t, terminal, []string{"line 1", "line 2", "line 3", "line 4", "line 5", "line 6", "line 7", "line 8"})

	terminal.SendInput("\x1bOF")
	terminal.WaitForRender()
	altAssertViewport(t, terminal, []string{"line 5", "line 6", "line 7", "line 8", "line 9", "line 10", "line 11", "line 12"})

	tui.Stop(nil)
}

func TestTuiAltScreenSearchesNormalizedRenderedTranscriptTextAcrossRows(t *testing.T) {
	got := FindAltScreenSearchMatches([]string{"alpha QUICK", "brown fox"}, "quick brown")
	want := []AltScreenSearchMatch{{
		Segments: []AltScreenSearchSegment{
			{Row: 0, StartCol: 6, EndCol: 11},
			{Row: 1, StartCol: 0, EndCol: 5},
		},
	}}
	if len(got) != len(want) {
		t.Fatalf("matches = %+v, want %+v", got, want)
	}
	for i := range want {
		if !reflect.DeepEqual(got[i], want[i]) {
			t.Fatalf("match %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestTuiAltScreenUsesConfiguredStylesForCurrentAndNonCurrentSearchMatches(t *testing.T) {
	terminal := &recordingTerminal{VirtualTerminal: NewVirtualTerminal(60, 4)}
	tui := NewTuiAltScreen(terminal, nil, "", &TuiAltScreenOptions{
		SearchMatchStyle:        func(text string) string { return "\x1b[41m" + text + "\x1b[49m" },
		SearchCurrentMatchStyle: func(text string) string { return "\x1b[42m" + text + "\x1b[49m" },
	})
	tui.AddChild(NewText("needle first\nmiddle\nneedle second\nend", 0, 0))
	tui.Start()
	terminal.WaitForRender()

	terminal.SendInput("\x1b[102;6u")
	terminal.SendInput("needle")
	terminal.WaitForRender()

	writes := terminal.eventsJoined()
	if !strings.Contains(writes, "\x1b[42mneedle\x1b[49m") {
		t.Fatal("missing current-match style")
	}
	if !strings.Contains(writes, "\x1b[41mneedle\x1b[49m") {
		t.Fatal("missing non-current-match style")
	}
	tui.Stop(nil)
}

func TestTuiAltScreenSearchesTheTranscriptWithCtrlShiftFAndRestoresEditorFocusOnClose(t *testing.T) {
	terminal := &recordingTerminal{VirtualTerminal: NewVirtualTerminal(60, 8)}
	tui := NewTuiAltScreen(terminal, nil, "", nil)
	var lines []string
	for i := 0; i < 12; i++ {
		switch i {
		case 4:
			lines = append(lines, "line 5 needle one")
		case 9:
			lines = append(lines, "line 10 needle two")
		default:
			lines = append(lines, "line "+itoa(i+1))
		}
	}
	transcriptText := NewText(strings.Join(lines, "\n"), 0, 0)
	transcript := NewScrollView(transcriptText, &ScrollViewOptions{Follow: "end", Primary: true})
	editor := &altEditorStub{}
	tui.SetLayoutRoot(
		NewVStack([]StackChild{
			StackEntryOf(transcript, &StackEntryOptions{Basis: 0, Grow: 1, MinSize: 1}),
			StackEntryOf(editor, &StackEntryOptions{Basis: 1, Shrink: lo.ToPtr(0)}),
		}, nil),
	)
	tui.SetFocus(editor)
	tui.Start()
	terminal.WaitForRender()

	terminal.SendInput("\x1b[102;6u")
	terminal.SendInput("needle")
	terminal.WaitForRender()
	if transcript.IsFollowingEnd() {
		t.Fatal("expected transcript not following end during search")
	}
	if !altViewportSome(terminal, func(line string) bool {
		return strings.Contains(line, "2/2")
	}) {
		t.Fatalf("missing search overlay 2/2: %q", terminal.GetViewport())
	}
	if !altViewportSome(terminal, func(line string) bool { return strings.Contains(line, "line 10 needle two") }) {
		t.Fatalf("missing revealed match: %q", terminal.GetViewport())
	}
	if got := editor.Inputs(); len(got) != 0 {
		t.Fatalf("editor inputs = %q, want none", got)
	}
	if !strings.Contains(terminal.eventsJoined(), "\x1b[1;7mneedle\x1b[22;27m") {
		t.Fatal("missing default current-match style")
	}

	for i := 0; i < 6; i++ {
		terminal.SendInput("\x1b[<64;1;4M")
	}
	terminal.WaitForRender()
	if transcript.ScrollTop() != 0 {
		t.Fatalf("transcript scrollTop = %d, want 0", transcript.ScrollTop())
	}
	if !altViewportSome(terminal, func(line string) bool {
		return strings.Contains(line, "needle") && strings.Contains(line, "2/2")
	}) {
		t.Fatalf("missing search query line: %q", terminal.GetViewport())
	}

	terminal.SendInput("\x07")
	terminal.WaitForRender()
	if !altViewportSome(terminal, func(line string) bool {
		return strings.Contains(line, "1/2")
	}) {
		t.Fatalf("missing search overlay 1/2: %q", terminal.GetViewport())
	}
	if !altViewportSome(terminal, func(line string) bool { return strings.Contains(line, "line 5 needle one") }) {
		t.Fatalf("missing previous match: %q", terminal.GetViewport())
	}

	terminal.SendInput("\x1b[103;6u")
	terminal.WaitForRender()
	if !altViewportSome(terminal, func(line string) bool {
		return strings.Contains(line, "2/2")
	}) {
		t.Fatalf("missing search overlay 2/2 after previous: %q", terminal.GetViewport())
	}
	if !altViewportSome(terminal, func(line string) bool { return strings.Contains(line, "line 10 needle two") }) {
		t.Fatalf("missing next match after previous: %q", terminal.GetViewport())
	}

	terminal.SendInput("\x1b")
	terminal.SendInput("x")
	terminal.WaitForRender()
	if altViewportSome(terminal, func(line string) bool { return strings.Contains(line, "┌") }) {
		t.Fatalf("search overlay should be closed: %q", terminal.GetViewport())
	}
	if got := editor.Inputs(); !stringSlicesEqual(got, []string{"x"}) {
		t.Fatalf("editor inputs = %q, want [x]", got)
	}

	tui.Stop(nil)
}

func altViewportSome(v viewportSource, pred func(line string) bool) bool {
	for _, line := range v.GetViewport() {
		if pred(line) {
			return true
		}
	}
	return false
}

func TestTuiAltScreenScrollsTheTranscriptByHalfAPageWithCustomBindings(t *testing.T) {
	originalKeybindings := GetKeybindings()
	defer SetKeybindings(originalKeybindings)
	terminal := NewVirtualTerminal(20, 10)
	tui := NewTuiAltScreen(terminal, nil, "", nil)
	SetKeybindings(NewKeybindingsManager(TUIKeyBindings, KeybindingsConfig{
		"tui.altScreen.halfPageUp":   {"ctrl+u"},
		"tui.altScreen.halfPageDown": {"ctrl+d"},
	}))
	tui.AddChild(NewText(altTestLines(30), 0, 0))
	tui.Start()
	terminal.WaitForRender()
	if got := tui.ViewportTop(); got != 20 {
		t.Fatalf("viewportTop = %d, want 20", got)
	}

	terminal.SendInput("\x15")
	terminal.WaitForRender()
	if got := tui.ViewportTop(); got != 15 {
		t.Fatalf("viewportTop = %d, want 15", got)
	}

	terminal.SendInput("\x04")
	terminal.WaitForRender()
	if got := tui.ViewportTop(); got != 20 {
		t.Fatalf("viewportTop = %d, want 20", got)
	}
	tui.Stop(nil)
}

func TestTuiAltScreenScrollsTheTranscriptByOneLineWithCustomBindings(t *testing.T) {
	originalKeybindings := GetKeybindings()
	defer SetKeybindings(originalKeybindings)
	terminal := NewVirtualTerminal(20, 10)
	tui := NewTuiAltScreen(terminal, nil, "", nil)
	SetKeybindings(NewKeybindingsManager(TUIKeyBindings, KeybindingsConfig{
		"tui.altScreen.lineUp":   {"ctrl+y"},
		"tui.altScreen.lineDown": {"ctrl+e"},
	}))
	tui.AddChild(NewText(altTestLines(30), 0, 0))
	tui.Start()
	terminal.WaitForRender()
	if got := tui.ViewportTop(); got != 20 {
		t.Fatalf("viewportTop = %d, want 20", got)
	}

	terminal.SendInput("\x19")
	terminal.WaitForRender()
	if got := tui.ViewportTop(); got != 19 {
		t.Fatalf("viewportTop = %d, want 19", got)
	}

	terminal.SendInput("\x05")
	terminal.WaitForRender()
	if got := tui.ViewportTop(); got != 20 {
		t.Fatalf("viewportTop = %d, want 20", got)
	}
	tui.Stop(nil)
}

func TestTuiAltScreenRoutesCtrlModifiedViewportNavigationToTheFocusedComponent(t *testing.T) {
	terminal := NewVirtualTerminal(20, 6)
	tui := NewTuiAltScreen(terminal, nil, "", nil)
	transcript := NewScrollView(
		NewText(altTestLines(12), 0, 0),
		&ScrollViewOptions{Follow: "end", Primary: true},
	)
	editor := &altEditorStub{}
	tui.SetLayoutRoot(
		NewVStack([]StackChild{
			StackEntryOf(transcript, &StackEntryOptions{Basis: 0, Grow: 1, MinSize: 1}),
			StackEntryOf(editor, &StackEntryOptions{Basis: 1, Shrink: lo.ToPtr(0)}),
		}, nil),
	)
	tui.SetFocus(editor)
	tui.Start()
	terminal.WaitForRender()

	terminal.SendInput("\x1bOH")
	terminal.WaitForRender()
	if transcript.ScrollTop() != 0 {
		t.Fatalf("transcript scrollTop = %d, want 0", transcript.ScrollTop())
	}
	if got := editor.Inputs(); len(got) != 0 {
		t.Fatalf("editor inputs = %q, want none", got)
	}

	modifiedInputs := []string{"\x1b[1;5H", "\x1b[1;5F", "\x1b[5;5~", "\x1b[6;5~", "\x1b[57423;5u"}
	for _, input := range modifiedInputs {
		terminal.SendInput(input)
	}
	terminal.SendInput("\x1b[57423;5:3u")
	terminal.WaitForRender()
	if transcript.ScrollTop() != 0 {
		t.Fatalf("transcript scrollTop = %d, want 0", transcript.ScrollTop())
	}
	if got := editor.Inputs(); !stringSlicesEqual(got, modifiedInputs) {
		t.Fatalf("editor inputs = %q, want %q", got, modifiedInputs)
	}

	terminal.SendInput("\x1b[6~")
	terminal.WaitForRender()
	if transcript.ScrollTop() != 1 {
		t.Fatalf("transcript scrollTop = %d, want 1", transcript.ScrollTop())
	}
	if got := editor.Inputs(); !stringSlicesEqual(got, modifiedInputs) {
		t.Fatalf("editor inputs = %q, want %q", got, modifiedInputs)
	}

	tui.Stop(nil)
}

func TestTuiAltScreenJumpsBetweenOsc133SemanticPromptMarkers(t *testing.T) {
	terminal := NewVirtualTerminal(20, 3)
	tui := NewTuiAltScreen(terminal, nil, "", nil)
	var parts []string
	for message := 1; message <= 4; message++ {
		parts = append(parts, altTestOsc133ZoneStart+"message "+itoa(message), "detail")
	}
	tui.AddChild(NewText(strings.Join(parts, "\n"), 0, 0))
	tui.Start()
	terminal.WaitForRender()
	if got := tui.ViewportTop(); got != 5 {
		t.Fatalf("viewportTop = %d, want 5", got)
	}

	terminal.SendInput("\x1b[57419;6u")
	terminal.SendInput("\x1b[57419;6:3u")
	terminal.WaitForRender()
	if got := tui.ViewportTop(); got != 4 {
		t.Fatalf("viewportTop = %d, want 4", got)
	}
	if got := strings.TrimRight(terminal.GetViewport()[0], " "); got != "message 3" {
		t.Fatalf("top line = %q, want message 3", got)
	}

	terminal.SendInput("\x1b[1;6A")
	terminal.WaitForRender()
	if got := tui.ViewportTop(); got != 2 {
		t.Fatalf("viewportTop = %d, want 2", got)
	}
	if got := strings.TrimRight(terminal.GetViewport()[0], " "); got != "message 2" {
		t.Fatalf("top line = %q, want message 2", got)
	}

	terminal.SendInput("\x1b[57420;6u")
	terminal.SendInput("\x1b[57420;6:3u")
	terminal.WaitForRender()
	if got := tui.ViewportTop(); got != 4 {
		t.Fatalf("viewportTop = %d, want 4", got)
	}
	if got := strings.TrimRight(terminal.GetViewport()[0], " "); got != "message 3" {
		t.Fatalf("top line = %q, want message 3", got)
	}

	terminal.SendInput("\x1b[1;6B")
	terminal.WaitForRender()
	if got := tui.ViewportTop(); got != 5 {
		t.Fatalf("viewportTop = %d, want 5", got)
	}
	if got := strings.TrimRight(terminal.GetViewport()[1], " "); got != "message 4" {
		t.Fatalf("second line = %q, want message 4", got)
	}
	if !tui.IsFollowingOutput() {
		t.Fatal("expected following output")
	}

	tui.Stop(nil)
}

func TestTuiAltScreenClearsStaleIterm2ImagePlacementsWhenTheyLeaveTheViewport(t *testing.T) {
	SetCapabilities(TerminalCapabilities{Images: "iterm2", TrueColor: true, Hyperlinks: true})
	defer ResetCapabilitiesCache()
	terminal := &recordingTerminal{VirtualTerminal: NewVirtualTerminal(20, 3)}
	tui := NewTuiAltScreen(terminal, nil, "", nil)
	imageLine := "\x1b]1337;File=inline=1;width=2;height=auto:AAAA\x07"
	tui.AddChild(&countingComponent{lines: []string{imageLine, "", "", "after", "more", "end"}})
	tui.Start()
	terminal.WaitForRender()
	tui.ScrollToTop()
	terminal.WaitForRender()
	eventCount := len(terminal.Events())

	tui.ScrollBy(1)
	terminal.WaitForRender()
	found := false
	for _, e := range terminal.Events()[eventCount:] {
		if e.typ == "write" && strings.Contains(e.data, "\x1b[2J") {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected full clear write after image leaves viewport")
	}
	tui.Stop(nil)
}

func TestTuiAltScreenCropsAKittyImageWhoseFirstLineIsAboveTheViewport(t *testing.T) {
	terminal := &recordingTerminal{VirtualTerminal: NewVirtualTerminal(20, 3)}
	tui := NewTuiAltScreen(terminal, nil, "", nil)
	imageId := 123
	imageLine := EncodeKitty("AAAA", EncodeKittyParams{Columns: 2, Rows: 3, ImageId: imageId, MoveCursorOff: true})
	RegisterKittyImageMetadata(KittyImageMetadata{ImageId: imageId, Columns: 2, Rows: 3, WidthPx: 100, HeightPx: 100})
	tui.AddChild(&countingComponent{lines: []string{"before", imageLine, "", "", "after", "end"}})
	tui.Start()
	terminal.WaitForRender()

	if got := tui.ViewportTop(); got != 3 {
		t.Fatalf("viewportTop = %d, want 3", got)
	}
	found := false
	for _, e := range terminal.Events() {
		if e.typ == "write" && strings.Contains(e.data, "i=123") && strings.Contains(e.data, "y=66,h=34,r=1") {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("missing cropped image write")
	}

	tui.Stop(nil)
}

func TestTuiAltScreenRetainsRecentlyOffscreenKittyImagesForPlacementOnlyReuse(t *testing.T) {
	SetCapabilities(TerminalCapabilities{Images: "kitty", TrueColor: true, Hyperlinks: true})
	defer ResetCapabilitiesCache()
	terminal := &recordingTerminal{VirtualTerminal: NewVirtualTerminal(20, 1)}
	tui := NewTuiAltScreen(terminal, nil, "", nil)
	imageId := 321
	imageLine := EncodeKitty("AAAA", EncodeKittyParams{Columns: 2, Rows: 1, ImageId: imageId, MoveCursorOff: true})
	RegisterKittyImageMetadata(KittyImageMetadata{ImageId: imageId, Columns: 2, Rows: 1, WidthPx: 100, HeightPx: 50})
	tui.SetLayoutRoot(NewScrollView(&countingComponent{lines: []string{imageLine, "after"}}, &ScrollViewOptions{Primary: true}))
	tui.Start()
	terminal.WaitForRender()
	if !strings.Contains(terminal.eventsJoined(), "\x1b_Ga=T") {
		t.Fatal("expected initial transmission")
	}

	eventCount := len(terminal.Events())
	tui.ScrollBy(1)
	terminal.WaitForRender()
	tui.ScrollBy(-1)
	terminal.WaitForRender()
	reentryWrites := altEventWritesJoined(terminal.Events()[eventCount:])
	if !strings.Contains(reentryWrites, "\x1b_Ga=p,q=2") {
		t.Fatal("expected placement-only reuse")
	}
	if strings.Contains(reentryWrites, "\x1b_Ga=T") {
		t.Fatal("expected no retransmission")
	}
	if strings.Contains(reentryWrites, "\x1b_Ga=d,d=I,i="+itoa(imageId)+",q=2\x1b\\") {
		t.Fatal("expected image retention")
	}
	tui.Stop(nil)
}

func TestTuiAltScreenEvictsTheLeastRecentlyVisibleKittyImageWhenTheCacheIsFull(t *testing.T) {
	SetCapabilities(TerminalCapabilities{Images: "kitty", TrueColor: true, Hyperlinks: true})
	defer ResetCapabilitiesCache()
	terminal := &recordingTerminal{VirtualTerminal: NewVirtualTerminal(20, 1)}
	tui := NewTuiAltScreen(terminal, nil, "", nil)
	firstImageId := 500
	var imageLines []string
	for i := 0; i < 18; i++ {
		imageId := firstImageId + i
		RegisterKittyImageMetadata(KittyImageMetadata{ImageId: imageId, Columns: 2, Rows: 1, WidthPx: 100, HeightPx: 50})
		imageLines = append(imageLines, EncodeKitty("AAAA", EncodeKittyParams{Columns: 2, Rows: 1, ImageId: imageId, MoveCursorOff: true}))
	}
	tui.SetLayoutRoot(NewScrollView(&countingComponent{lines: imageLines}, &ScrollViewOptions{Primary: true}))
	tui.Start()
	terminal.WaitForRender()
	for i := 1; i < len(imageLines); i++ {
		tui.ScrollBy(1)
		terminal.WaitForRender()
	}
	if !strings.Contains(terminal.eventsJoined(), "\x1b_Ga=d,d=I,i="+itoa(firstImageId)+",q=2\x1b\\") {
		t.Fatal("expected least-recently-visible image eviction")
	}

	eventCount := len(terminal.Events())
	tui.ScrollToTop()
	terminal.WaitForRender()
	reentryWrites := altEventWritesJoined(terminal.Events()[eventCount:])
	if !strings.Contains(reentryWrites, "\x1b_Ga=T") {
		t.Fatal("expected retransmission after eviction")
	}
	tui.Stop(nil)
}

func TestTuiAltScreenEvictsOffscreenKittyImagesWhenDecodedRasterMemoryExceedsTheCacheQuota(t *testing.T) {
	SetCapabilities(TerminalCapabilities{Images: "kitty", TrueColor: true, Hyperlinks: true})
	defer ResetCapabilitiesCache()
	terminal := &recordingTerminal{VirtualTerminal: NewVirtualTerminal(20, 1)}
	tui := NewTuiAltScreen(terminal, nil, "", nil)
	firstImageId := 600
	var imageLines []string
	for i := 0; i < 4; i++ {
		imageId := firstImageId + i
		RegisterKittyImageMetadata(KittyImageMetadata{ImageId: imageId, Columns: 2, Rows: 1, WidthPx: 3840, HeightPx: 2160})
		imageLines = append(imageLines, EncodeKitty("AAAA", EncodeKittyParams{Columns: 2, Rows: 1, ImageId: imageId, MoveCursorOff: true}))
	}
	tui.SetLayoutRoot(NewScrollView(&countingComponent{lines: imageLines}, &ScrollViewOptions{Primary: true}))
	tui.Start()
	terminal.WaitForRender()
	for i := 1; i < len(imageLines); i++ {
		tui.ScrollBy(1)
		terminal.WaitForRender()
	}
	if !strings.Contains(terminal.eventsJoined(), "\x1b_Ga=d,d=I,i="+itoa(firstImageId)+",q=2\x1b\\") {
		t.Fatal("expected decoded-bytes quota eviction")
	}
	tui.Stop(nil)
}

func TestTuiAltScreenOpensAnOsc8HyperlinkWithSpecificOrGenericReleaseCodesButNotOnDrag(t *testing.T) {
	terminal := &recordingTerminal{VirtualTerminal: NewVirtualTerminal(20, 3)}
	var openedUrls []string
	var mu sync.Mutex
	tui := NewTuiAltScreen(terminal, nil, "", &TuiAltScreenOptions{
		OpenUrl: func(url string) {
			mu.Lock()
			openedUrls = append(openedUrls, url)
			mu.Unlock()
		},
	})
	url := "https://example.com/path?q=1"
	belUrl := "https://example.com/bel"
	emojiUrl := "https://example.com/emoji"
	tui.AddChild(NewText(
		Hyperlink("link", url)+"\n\x1b]8;;"+belUrl+"\x07link\x1b]8;;\x07\n"+Hyperlink("🙂", emojiUrl),
		0, 0,
	))
	tui.Start()
	terminal.WaitForRender()

	terminal.SendInput("\x1b[<0;2;1M")
	terminal.SendInput("\x1b[<3;2;1m")
	terminal.WaitForRender()
	mu.Lock()
	if !stringSlicesEqual(openedUrls, []string{url}) {
		mu.Unlock()
		t.Fatalf("openedUrls = %q, want [%s]", openedUrls, url)
	}
	mu.Unlock()

	terminal.SendInput("\x1b[<0;2;2M")
	terminal.SendInput("\x1b[<0;2;2m")
	terminal.WaitForRender()
	mu.Lock()
	if !stringSlicesEqual(openedUrls, []string{url, belUrl}) {
		mu.Unlock()
		t.Fatalf("openedUrls = %q, want bel url", openedUrls)
	}
	mu.Unlock()

	terminal.SendInput("\x1b[<0;2;3M")
	terminal.SendInput("\x1b[<0;2;3m")
	terminal.WaitForRender()
	mu.Lock()
	if !stringSlicesEqual(openedUrls, []string{url, belUrl, emojiUrl}) {
		mu.Unlock()
		t.Fatalf("openedUrls = %q, want emoji url", openedUrls)
	}
	mu.Unlock()

	terminal.SendInput("\x1b[<0;2;1M")
	terminal.SendInput("\x1b[<32;4;1M")
	terminal.SendInput("\x1b[<0;4;1m")
	terminal.WaitForRender()
	mu.Lock()
	defer mu.Unlock()
	if !stringSlicesEqual(openedUrls, []string{url, belUrl, emojiUrl}) {
		t.Fatalf("drag must not open hyperlink: %q", openedUrls)
	}

	tui.Stop(nil)
}

func TestTuiAltScreenSelectsVisibleTextWithTheMouseAndCopiesItWithOsc52AfterAGenericRelease(t *testing.T) {
	terminal := &recordingTerminal{VirtualTerminal: NewVirtualTerminal(20, 4)}
	tui := NewTuiAltScreen(terminal, nil, "", nil)
	tui.AddChild(NewText("\x1b[1mal\x1b[0mpha\nbeta\ngamma\ndelta", 0, 0))
	tui.Start()
	terminal.WaitForRender()

	terminal.SendInput("\x1b[<0;1;1M")
	terminal.SendInput("\x1b[<32;4;2M")
	terminal.SendInput("\x1b[<3;4;2m")
	terminal.WaitForRender()

	expectedClipboardSequence := "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte("alpha\nbeta")) + "\x07"
	writes := terminal.eventsJoined()
	if !strings.Contains(writes, expectedClipboardSequence) {
		t.Fatalf("missing clipboard write %q", expectedClipboardSequence)
	}
	if !strings.Contains(writes, "\x1b[7m") {
		t.Fatal("missing selection highlight")
	}
	if !strings.Contains(writes, "al\x1b[0m\x1b[7mpha") {
		t.Fatal("selection inverse must be reapplied after a reset inside the selection")
	}
	if !altViewportSome(terminal, func(line string) bool { return strings.Contains(line, "Copied!") }) {
		t.Fatalf("missing Copied! flash: %q", terminal.GetViewport())
	}

	tui.Stop(nil)
}

func TestTuiAltScreenUsesAnInjectedCopySelectionHandlerInsteadOfOsc52AndReportsSuccess(t *testing.T) {
	terminal := &recordingTerminal{VirtualTerminal: NewVirtualTerminal(20, 4)}
	var copied []string
	tui := NewTuiAltScreen(terminal, nil, "", &TuiAltScreenOptions{
		CopySelection: func(text string) bool {
			copied = append(copied, text)
			return true
		},
	})
	tui.AddChild(NewText("alpha\nbeta\ngamma\ndelta", 0, 0))
	tui.Start()
	terminal.WaitForRender()

	terminal.SendInput("\x1b[<0;1;1M")
	terminal.SendInput("\x1b[<32;4;2M")
	terminal.SendInput("\x1b[<0;4;2m")
	terminal.WaitForRender()

	if !stringSlicesEqual(copied, []string{"alpha\nbeta"}) {
		t.Fatalf("copied = %q, want [alpha\\nbeta]", copied)
	}
	if strings.Contains(terminal.eventsJoined(), "\x1b]52;c;") {
		t.Fatal("must not emit OSC 52 when a copySelection handler is provided")
	}
	if !altViewportSome(terminal, func(line string) bool { return strings.Contains(line, "Copied!") }) {
		t.Fatal("missing Copied! flash")
	}

	tui.Stop(nil)
}

func TestTuiAltScreenFlashesAnErrorWhenTheInjectedCopySelectionHandlerFails(t *testing.T) {
	terminal := &recordingTerminal{VirtualTerminal: NewVirtualTerminal(20, 4)}
	tui := NewTuiAltScreen(terminal, nil, "", &TuiAltScreenOptions{
		CopySelection: func(text string) bool { return false },
	})
	tui.AddChild(NewText("alpha\nbeta\ngamma\ndelta", 0, 0))
	tui.Start()
	terminal.WaitForRender()

	terminal.SendInput("\x1b[<0;1;1M")
	terminal.SendInput("\x1b[<32;4;2M")
	terminal.SendInput("\x1b[<0;4;2m")
	terminal.WaitForRender()

	if !altViewportSome(terminal, func(line string) bool { return strings.Contains(line, "Copy failed") }) {
		t.Fatal("missing Copy failed flash")
	}
	if strings.Contains(terminal.eventsJoined(), "\x1b]52;c;") {
		t.Fatal("must not emit OSC 52 when a copySelection handler is provided")
	}

	tui.Stop(nil)
}

func TestTuiAltScreenDoesNotAppendWhitespaceToDoubleClickWordHighlighting(t *testing.T) {
	terminal := &recordingTerminal{VirtualTerminal: NewVirtualTerminal(20, 1)}
	tui := NewTuiAltScreen(terminal, nil, "", nil)
	tui.AddChild(NewText("foo  bar", 0, 0))
	tui.Start()
	terminal.WaitForRender()

	terminal.SendInput("\x1b[<0;1;1M")
	terminal.SendInput("\x1b[<0;1;1m")
	terminal.SendInput("\x1b[<0;3;1M")
	terminal.WaitForRender()

	if !strings.Contains(terminal.eventsJoined(), "foo\x1b[27m") {
		t.Fatal("expected highlight ending exactly at word boundary")
	}
	tui.Stop(nil)
}

func TestTuiAltScreenHighlightsACompleteWhitespaceSegmentDuringAWordDrag(t *testing.T) {
	terminal := &recordingTerminal{VirtualTerminal: NewVirtualTerminal(20, 1)}
	tui := NewTuiAltScreen(terminal, nil, "", nil)
	tui.AddChild(NewText("foo  bar", 0, 0))
	tui.Start()
	terminal.WaitForRender()

	terminal.SendInput("\x1b[<0;1;1M")
	terminal.SendInput("\x1b[<0;1;1m")
	terminal.SendInput("\x1b[<0;2;1M")
	terminal.SendInput("\x1b[<32;4;1M")
	terminal.WaitForRender()

	if !strings.Contains(terminal.eventsJoined(), "foo  \x1b[27m") {
		t.Fatal("expected whitespace segment included in word drag highlight")
	}
	tui.Stop(nil)
}

func TestTuiAltScreenSelectsWholeWordsOnDoubleClickExtendsWordDragsAndSelectsLinesOnTripleClick(t *testing.T) {
	terminal := &recordingTerminal{VirtualTerminal: NewVirtualTerminal(20, 2)}
	tui := NewTuiAltScreen(terminal, nil, "", nil)
	tui.AddChild(NewText("zero alpha beta\ngamma delta", 0, 0))
	tui.Start()
	terminal.WaitForRender()

	// The second click lands on a different character in alpha.
	terminal.SendInput("\x1b[<0;6;1M")
	terminal.SendInput("\x1b[<0;6;1m")
	terminal.SendInput("\x1b[<0;10;1M")
	terminal.SendInput("\x1b[<0;10;1m")
	terminal.WaitForRender()
	alpha := "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte("alpha")) + "\x07"
	if !strings.Contains(terminal.eventsJoined(), alpha) {
		t.Fatal("expected double-click word selection of alpha")
	}

	// A double-click drag includes each word touched, rather than partial words.
	terminal.SendInput("\x1b[<0;12;1M")
	terminal.SendInput("\x1b[<0;12;1m")
	terminal.SendInput("\x1b[<0;14;1M")
	terminal.SendInput("\x1b[<32;3;2M")
	terminal.SendInput("\x1b[<0;3;2m")
	terminal.WaitForRender()
	words := "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte("beta\ngamma")) + "\x07"
	if !strings.Contains(terminal.eventsJoined(), words) {
		t.Fatal("expected word-granularity drag selection")
	}

	terminal.SendInput("\x1b[<0;7;2M")
	terminal.SendInput("\x1b[<0;7;2m")
	terminal.SendInput("\x1b[<0;9;2M")
	terminal.SendInput("\x1b[<0;9;2m")
	terminal.SendInput("\x1b[<0;11;2M")
	terminal.SendInput("\x1b[<0;11;2m")
	terminal.WaitForRender()
	line := "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte("gamma delta")) + "\x07"
	if !strings.Contains(terminal.eventsJoined(), line) {
		t.Fatal("expected triple-click line selection")
	}

	tui.Stop(nil)
}

func TestTuiAltScreenDoesNotRepaintIdleOrZeroWidthSelectionsOnFocusLoss(t *testing.T) {
	terminal := &recordingTerminal{VirtualTerminal: NewVirtualTerminal(20, 4)}
	tui := NewTuiAltScreen(terminal, nil, "", nil)
	tui.AddChild(NewText("alpha\nbeta\ngamma\ndelta", 0, 0))
	tui.Start()
	terminal.WaitForRender()

	writeCount := func() int {
		count := 0
		for _, e := range terminal.Events() {
			if e.typ == "write" {
				count++
			}
		}
		return count
	}
	clipboardWriteCount := func() int {
		count := 0
		for _, e := range terminal.Events() {
			if e.typ == "write" && strings.Contains(e.data, "\x1b]52;c;") {
				count++
			}
		}
		return count
	}

	idleWriteCount := writeCount()
	terminal.SendInput("\x1b[O")
	terminal.SendInput("\x1b[I")
	terminal.WaitForRender()
	if writeCount() != idleWriteCount {
		t.Fatal("focus cycling must not repaint idle selection")
	}

	// A completed click leaves a zero-width anchor, but later orphaned drag/release events must not extend it.
	terminal.SendInput("\x1b[<0;1;1M")
	terminal.SendInput("\x1b[<0;1;1m")
	terminal.SendInput("\x1b[<32;4;2M")
	terminal.SendInput("\x1b[<0;4;2m")
	terminal.WaitForRender()
	if clipboardWriteCount() != 0 {
		t.Fatal("zero-width selection must not copy")
	}

	// Losing focus after a press without a drag cancels the press without repainting.
	terminal.SendInput("\x1b[<0;1;3M")
	terminal.WaitForRender()
	pressedWriteCount := writeCount()
	terminal.SendInput("\x1b[O")
	terminal.SendInput("\x1b[I")
	terminal.WaitForRender()
	if writeCount() != pressedWriteCount {
		t.Fatal("focus loss after press must not repaint")
	}
	terminal.SendInput("\x1b[<32;4;2M")
	terminal.SendInput("\x1b[<0;4;2m")
	terminal.WaitForRender()
	if clipboardWriteCount() != 0 {
		t.Fatal("cancelled press must not copy")
	}
	if !strings.Contains(terminal.eventsJoined(), "\x1b[?1004h") {
		t.Fatal("expected focus reporting enabled")
	}

	tui.Stop(nil)
	if !strings.Contains(terminal.eventsJoined(), "\x1b[?1004l") {
		t.Fatal("expected focus reporting disabled on stop")
	}
}

func TestTuiAltScreenClearsAnActiveVisibleSelectionOnFocusLossAndIgnoresOrphanEvents(t *testing.T) {
	terminal := &recordingTerminal{VirtualTerminal: NewVirtualTerminal(20, 4)}
	tui := NewTuiAltScreen(terminal, nil, "", nil)
	tui.AddChild(NewText("alpha\nbeta\ngamma\ndelta", 0, 0))
	tui.Start()
	terminal.WaitForRender()

	terminal.SendInput("\x1b[<0;1;1M")
	terminal.SendInput("\x1b[<32;4;2M")
	terminal.WaitForRender()
	focusLossEventCount := len(terminal.Events())
	terminal.SendInput("\x1b[O")
	terminal.SendInput("\x1b[I")
	terminal.WaitForRender()
	focusLossWrites := altEventWritesJoined(terminal.Events()[focusLossEventCount:])
	if !strings.Contains(focusLossWrites, "alpha") {
		t.Fatal("expected repaint on selection clear")
	}
	if !strings.Contains(focusLossWrites, "beta") {
		t.Fatal("expected repaint on selection clear")
	}
	if strings.Contains(focusLossWrites, "\x1b[7m") {
		t.Fatal("selection highlight must be gone after focus loss")
	}

	terminal.SendInput("\x1b[<32;4;2M")
	terminal.SendInput("\x1b[<0;4;2m")
	terminal.WaitForRender()
	if strings.Contains(terminal.eventsJoined(), "\x1b]52;c;") {
		t.Fatal("orphan release after focus loss must not copy")
	}
	tui.Stop(nil)
}

func TestTuiAltScreenRetainsACompletedVisibleSelectionAcrossFocusChanges(t *testing.T) {
	terminal := &recordingTerminal{VirtualTerminal: NewVirtualTerminal(20, 4)}
	tui := NewTuiAltScreen(terminal, nil, "", nil)
	tui.AddChild(NewText("alpha\nbeta\ngamma\ndelta", 0, 0))
	tui.Start()
	terminal.WaitForRender()

	terminal.SendInput("\x1b[<0;1;1M")
	terminal.SendInput("\x1b[<32;4;2M")
	terminal.SendInput("\x1b[<0;4;2m")
	terminal.WaitForRender()
	completedWriteCount := func() int {
		count := 0
		for _, e := range terminal.Events() {
			if e.typ == "write" {
				count++
			}
		}
		return count
	}()
	terminal.SendInput("\x1b[O")
	terminal.SendInput("\x1b[I")
	terminal.WaitForRender()
	if count := func() int {
		count := 0
		for _, e := range terminal.Events() {
			if e.typ == "write" {
				count++
			}
		}
		return count
	}(); count != completedWriteCount {
		t.Fatalf("focus cycling with completed selection must not repaint: %d vs %d", count, completedWriteCount)
	}

	redrawEventCount := len(terminal.Events())
	tui.RenderNow(true)
	redrawWrites := altEventWritesJoined(terminal.Events()[redrawEventCount:])
	if !strings.Contains(redrawWrites, "alpha") {
		t.Fatal("expected selection retained on forced redraw")
	}
	if !strings.Contains(redrawWrites, "beta") {
		t.Fatal("expected selection retained on forced redraw")
	}
	if !strings.Contains(redrawWrites, "\x1b[7m") {
		t.Fatal("expected selection highlight on forced redraw")
	}
	tui.Stop(nil)
}

func TestTuiAltScreenStacksFlashMessagesAndCollapsesThemAsTheyExpire(t *testing.T) {
	terminal := NewVirtualTerminal(20, 4)
	tui := NewTuiAltScreen(terminal, nil, "", nil)
	tui.AddChild(NewText("one\ntwo\nthree\nfour", 0, 0))
	tui.Start()
	terminal.WaitForRender()

	tui.Flash("First", 80)
	tui.Flash("Second", 500)
	terminal.WaitForRender()
	viewport := terminal.GetViewport()
	if !strings.HasSuffix(viewport[0], " First ") {
		t.Fatalf("first flash line = %q", viewport[0])
	}
	if !strings.HasSuffix(viewport[1], " Second ") {
		t.Fatalf("second flash line = %q", viewport[1])
	}

	time.Sleep(100 * time.Millisecond)
	terminal.WaitForRender()
	viewport = terminal.GetViewport()
	if !strings.HasSuffix(viewport[0], " Second ") {
		t.Fatalf("after expiry first line = %q", viewport[0])
	}
	for _, line := range viewport {
		if strings.Contains(line, "First") {
			t.Fatalf("expired flash still visible: %q", line)
		}
	}

	tui.Stop(nil)
}

func TestTuiAltScreenAutoScrollsAndExtendsADragSelectionHeldAtTheViewportEdge(t *testing.T) {
	terminal := &recordingTerminal{VirtualTerminal: NewVirtualTerminal(20, 4)}
	tui := NewTuiAltScreen(terminal, nil, "", nil)
	tui.AddChild(NewText(altTestLines(10), 0, 0))
	tui.Start()
	terminal.WaitForRender()
	if got := tui.ViewportTop(); got != 6 {
		t.Fatalf("viewportTop = %d, want 6", got)
	}

	terminal.SendInput("\x1b[<0;1;3M")
	terminal.SendInput("\x1b[<32;1;1M")
	time.Sleep(130 * time.Millisecond)
	terminal.WaitForRender()

	selectionTop := tui.ViewportTop()
	if selectionTop >= 6 {
		t.Fatalf("expected auto-scroll above row 6, got %d", selectionTop)
	}
	terminal.SendInput("\x1b[<0;1;1m")
	terminal.WaitForRender()

	var selectedLines []string
	for i := 0; i < 8-selectionTop; i++ {
		selectedLines = append(selectedLines, "line "+itoa(selectionTop+i+1))
	}
	selectedLines = append(selectedLines, "l")
	expectedClipboardSequence := "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte(strings.Join(selectedLines, "\n"))) + "\x07"
	if !strings.Contains(terminal.eventsJoined(), expectedClipboardSequence) {
		t.Fatalf("missing clipboard write %q", expectedClipboardSequence)
	}
	tui.Stop(nil)
}

func TestTuiAltScreenSnapsMouseSelectionToCjkEmojiAndCombiningGraphemeBoundaries(t *testing.T) {
	terminal := &recordingTerminal{VirtualTerminal: NewVirtualTerminal(20, 2)}
	tui := NewTuiAltScreen(terminal, nil, "", nil)
	tui.AddChild(NewText("A界🙂éZ", 0, 0))
	tui.Start()
	terminal.WaitForRender()

	wideSelection := "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte("界🙂")) + "\x07"
	terminal.SendInput("\x1b[<0;3;1M")
	terminal.SendInput("\x1b[<32;4;1M")
	terminal.SendInput("\x1b[<0;4;1m")
	terminal.WaitForRender()
	if count := altClipboardWriteCount(terminal, wideSelection); count != 1 {
		t.Fatalf("wide selection clipboard writes = %d, want 1", count)
	}

	terminal.SendInput("\x1b[<0;5;1M")
	terminal.SendInput("\x1b[<32;2;1M")
	terminal.SendInput("\x1b[<0;2;1m")
	terminal.WaitForRender()
	if count := altClipboardWriteCount(terminal, wideSelection); count != 2 {
		t.Fatalf("wide selection clipboard writes = %d, want 2", count)
	}

	combiningSelection := "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte("éZ")) + "\x07"
	terminal.SendInput("\x1b[<0;6;1M")
	terminal.SendInput("\x1b[<32;7;1M")
	terminal.SendInput("\x1b[<0;7;1m")
	terminal.WaitForRender()
	if !strings.Contains(terminal.eventsJoined(), combiningSelection) {
		t.Fatal("missing combining-grapheme selection")
	}

	tui.Stop(nil)
}

func altClipboardWriteCount(r *recordingTerminal, needle string) int {
	count := 0
	for _, e := range r.Events() {
		if e.typ == "write" && strings.Contains(e.data, needle) {
			count++
		}
	}
	return count
}

func TestTuiAltScreenIgnoresHorizontalTrackpadWheelEvents(t *testing.T) {
	terminal := NewVirtualTerminal(20, 4)
	tui := NewTuiAltScreen(terminal, nil, "", nil)
	tui.AddChild(NewText(altTestLines(8), 0, 0))
	tui.Start()
	terminal.WaitForRender()

	terminal.SendInput("\x1b[<66;1;1M")
	terminal.SendInput("\x1b[<67;1;1M")
	terminal.WaitForRender()
	if got := tui.ViewportTop(); got != 4 {
		t.Fatalf("viewportTop = %d, want 4", got)
	}
	altAssertViewport(t, terminal, []string{"line 5", "line 6", "line 7", "line 8"})

	tui.Stop(nil)
}

func TestTuiAltScreenRestoresKeyboardStateBeforeLeavingAltModeAndPrintsTheFullDocument(t *testing.T) {
	terminal := &recordingTerminal{VirtualTerminal: NewVirtualTerminal(20, 3)}
	tui := NewTuiAltScreen(terminal, nil, "", nil)
	tui.AddChild(NewText("first\nsecond\nthird\nfourth\nfifth\nsixth", 0, 0))
	tui.Start()
	terminal.WaitForRender()
	tui.Stop(nil)

	events := terminal.Events()
	startIndex := -1
	stopIndex := -1
	altScreenEnterIndex := -1
	mouseDisableIndex := -1
	mainScreenRestoreIndex := -1
	for i, e := range events {
		switch {
		case e.typ == "start" && startIndex == -1:
			startIndex = i
		case e.typ == "stop" && stopIndex == -1:
			stopIndex = i
		case e.typ == "write" && strings.Contains(e.data, "\x1b[?1049h") && altScreenEnterIndex == -1:
			altScreenEnterIndex = i
		case e.typ == "write" && strings.Contains(e.data, "\x1b[?1006l") && mouseDisableIndex == -1:
			mouseDisableIndex = i
		case e.typ == "write" && strings.Contains(e.data, "\x1b[?1049l") && mainScreenRestoreIndex == -1:
			mainScreenRestoreIndex = i
		}
	}
	if altScreenEnterIndex < 0 || startIndex < 0 || altScreenEnterIndex >= startIndex {
		t.Fatalf("alt screen enter must precede start: enter=%d start=%d", altScreenEnterIndex, startIndex)
	}
	if mouseDisableIndex < 0 || stopIndex < 0 || mouseDisableIndex >= stopIndex {
		t.Fatalf("mouse disable must precede stop: disable=%d stop=%d", mouseDisableIndex, stopIndex)
	}
	if mainScreenRestoreIndex <= stopIndex {
		t.Fatalf("main screen restore must follow stop: restore=%d stop=%d", mainScreenRestoreIndex, stopIndex)
	}

	restoreEvent := events[mainScreenRestoreIndex]
	for _, want := range []string{"first", "second", "third", "fourth", "fifth", "sixth"} {
		if !strings.Contains(restoreEvent.data, want) {
			t.Fatalf("restore output missing %q", want)
		}
	}
	if strings.Index(restoreEvent.data, "first") >= strings.Index(restoreEvent.data, "sixth") {
		t.Fatal("restore output must be in document order")
	}
}

func TestTuiAltScreenGivesWheelAndViewportKeysToAFocusedOverlay(t *testing.T) {
	terminal := NewVirtualTerminal(20, 6)
	tui := NewTuiAltScreen(terminal, nil, "", nil)
	tui.AddChild(NewText(altTestLines(12), 0, 0))
	overlay := &inputOverlay{}
	tui.Start()
	terminal.WaitForRender()
	topBefore := tui.ViewportTop()
	handle := tui.ShowOverlay(overlay, nil)
	terminal.WaitForRender()
	if !overlay.GetFocused() {
		t.Fatal("expected overlay focused")
	}

	wheel := "\x1b[<64;10;3M"
	keys := []string{"\x1b[5~", "\x1b[6~", "\x1bOH", "\x1bOF", wheel}
	for _, key := range keys {
		terminal.SendInput(key)
	}
	terminal.WaitForRender()

	if got := overlay.Inputs(); !stringSlicesEqual(got, keys) {
		t.Fatalf("overlay inputs = %q, want %q", got, keys)
	}
	if got := tui.ViewportTop(); got != topBefore {
		t.Fatalf("viewportTop = %d, want %d", got, topBefore)
	}

	handle.Hide()
	terminal.WaitForRender()
	terminal.SendInput("\x1b[5~")
	terminal.WaitForRender()
	if got := tui.ViewportTop(); got >= topBefore {
		t.Fatalf("viewportTop = %d, want < %d", got, topBefore)
	}
	tui.Stop(nil)
}

func TestTuiAltScreenKeepsViewportScrollingWhenAnOverlayIsNotFocused(t *testing.T) {
	terminal := NewVirtualTerminal(20, 6)
	tui := NewTuiAltScreen(terminal, nil, "", nil)
	editor := &inputOverlay{}
	tui.AddChild(NewText(altTestLines(12), 0, 0))
	tui.SetFocus(editor)
	tui.Start()
	terminal.WaitForRender()
	topBefore := tui.ViewportTop()

	hidden := tui.ShowOverlay(&inputOverlay{}, nil)
	hidden.SetHidden(true)
	nonCapturing := &inputOverlay{}
	tui.ShowOverlay(nonCapturing, &OverlayOptions{NonCapturing: true})
	unfocused := &inputOverlay{}
	unfocusedHandle := tui.ShowOverlay(unfocused, nil)
	unfocusedHandle.Unfocus(nil)
	terminal.WaitForRender()
	if nonCapturing.GetFocused() {
		t.Fatal("non-capturing overlay must not be focused")
	}
	if unfocused.GetFocused() {
		t.Fatal("unfocused overlay must not be focused")
	}

	terminal.SendInput("\x1b[5~")
	terminal.SendInput("\x1b[<64;10;3M")
	terminal.WaitForRender()
	if got := tui.ViewportTop(); got >= topBefore {
		t.Fatalf("viewportTop = %d, want < %d", got, topBefore)
	}
	if got := nonCapturing.Inputs(); len(got) != 0 {
		t.Fatalf("non-capturing overlay inputs = %q, want none", got)
	}
	if got := unfocused.Inputs(); len(got) != 0 {
		t.Fatalf("unfocused overlay inputs = %q, want none", got)
	}
	tui.Stop(nil)
}

func TestTuiAltScreenKeepsViewportScrollingWhileTranscriptSearchIsFocused(t *testing.T) {
	terminal := NewVirtualTerminal(20, 6)
	tui := NewTuiAltScreen(terminal, nil, "", nil)
	tui.AddChild(NewText(altTestLines(12), 0, 0))
	tui.Start()
	terminal.WaitForRender()
	topBefore := tui.ViewportTop()

	terminal.SendInput("\x1b[102;6u")
	terminal.WaitForRender()
	if !altViewportSome(terminal, func(line string) bool { return strings.Contains(line, "↑ ↓") }) {
		t.Fatal("expected search overlay")
	}

	terminal.SendInput("\x1b[5~")
	terminal.SendInput("\x1b[<64;1;4M")
	terminal.WaitForRender()
	if got := tui.ViewportTop(); got >= topBefore {
		t.Fatalf("viewportTop = %d, want < %d", got, topBefore)
	}
	if !altViewportSome(terminal, func(line string) bool { return strings.Contains(line, "↑ ↓") }) {
		t.Fatal("expected search overlay to stay open")
	}
	tui.Stop(nil)
}
