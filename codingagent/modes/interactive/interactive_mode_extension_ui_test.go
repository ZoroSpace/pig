// interactive_mode_extension_ui_test.go (pi:
// test/interactive-mode-status.test.ts — the
// createExtensionUIContext.setTheme and showExtensionCustom describes).
package interactive

import (
	"context"
	"sync"
	"testing"

	core "github.com/ZoroSpace/pig/codingagent/core"
	"github.com/ZoroSpace/pig/codingagent/modes/interactive/theme"
	"github.com/ZoroSpace/pig/tui"
)

// testFocusableComponent is pi's TestFocusableComponent. pig: the TUI
// touches it from its input/render goroutine, so state is mutex-guarded
// (-race; pi is single-threaded).
type testFocusableComponent struct {
	tui.Container
	label string
	mu    sync.Mutex
	text  string
	focus bool
	input []string
}

func newTestFocusableComponent(label string) *testFocusableComponent {
	return &testFocusableComponent{label: label}
}

func (c *testFocusableComponent) HandleInput(data string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.input = append(c.input, data)
}
func (c *testFocusableComponent) GetText() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.text
}
func (c *testFocusableComponent) SetText(text string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.text = text
}
func (c *testFocusableComponent) Render(int) []string { return []string{c.label} }
func (c *testFocusableComponent) GetFocused() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.focus
}
func (c *testFocusableComponent) SetFocused(v bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.focus = v
}

// inputs returns the received inputs (race-safe read).
func (c *testFocusableComponent) inputs() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.input...)
}

// settingsStub is the fake theme settings manager for the controller (pi:
// the settingsManager stub with vi.fn getters/setters).
type settingsStub struct {
	current  string
	setCalls []string
}

func (s *settingsStub) GetThemeSetting() (string, bool) { return s.current, s.current != "" }
func (s *settingsStub) SetTheme(theme string) {
	s.current = theme
	s.setCalls = append(s.setCalls, theme)
}
func (s *settingsStub) Flush() {}

// pi: "createExtensionUIContext setTheme > persists theme changes to
// settings manager".
func TestSetThemePersistsToSettings(t *testing.T) {
	initTestTheme(t)

	session := createInteractiveTestSession(t, &fixtureResourceLoader{}, nil)
	mode := newSeamTestMode(session, nil)
	stub := &settingsStub{current: "dark"}
	changed := 0
	mode.themeController = theme.NewInteractiveThemeController(mode.ui, theme.InteractiveThemeControllerOptions{
		GetSettingsManager: func() theme.ThemeSettingsManager { return stub },
		ShowError:          func(string) {},
		OnChanged:          func() { changed++ },
	})

	uiContext := mode.createExtensionUIContext()
	result := uiContext.SetTheme(context.Background(), "light")

	if !result.Success {
		t.Fatalf("expected success, got error %q", result.Error)
	}
	// pi's fakeThis routes both the controller and the persist path through
	// the same stub settings manager; pig's persist path goes to the
	// session's SettingsManager (the same object the real controller sees).
	if name, _ := session.SettingsManager.GetTheme(); name != "light" {
		t.Fatalf("expected settings theme light, got %q", name)
	}
	_ = stub
	// pi asserts ui.requestRender was called exactly once (via the stubbed
	// controller); pig's real controller reports the change via onChanged.
	if changed != 1 {
		t.Fatalf("expected exactly 1 onChanged call, got %d", changed)
	}
}

// pi: "does not persist invalid theme names".
func TestSetThemeDoesNotPersistInvalid(t *testing.T) {
	initTestTheme(t)

	session := createInteractiveTestSession(t, &fixtureResourceLoader{}, nil)
	mode := newSeamTestMode(session, nil)
	stub := &settingsStub{current: "dark"}
	mode.themeController = theme.NewInteractiveThemeController(mode.ui, theme.InteractiveThemeControllerOptions{
		GetSettingsManager: func() theme.ThemeSettingsManager { return stub },
		ShowError:          func(string) {},
		OnChanged:          func() {},
	})

	uiContext := mode.createExtensionUIContext()
	result := uiContext.SetTheme(context.Background(), "__missing_theme__")

	if result.Success {
		t.Fatal("expected failure for unknown theme")
	}
	if name, _ := session.SettingsManager.GetTheme(); name == "__missing_theme__" {
		t.Fatal("expected no setTheme persistence for unknown theme")
	}
}

// pi: "showExtensionCustom > overlay custom UI reclaims input after
// non-overlay custom UI closes".
func TestShowExtensionCustomOverlayReclaimsInput(t *testing.T) {
	initTestTheme(t)

	terminal := tui.NewVirtualTerminal(80, 24)
	ui := tui.NewTuiMainScreen(terminal, nil, "")
	editorContainer := &tui.Container{}
	editor := newTestFocusableComponent("EDITOR")
	palette := newTestFocusableComponent("PALETTE")
	overlay := newTestFocusableComponent("OVERLAY")
	replacement := newTestFocusableComponent("REPLACEMENT")

	closeOverlay := make(chan string, 1)
	closeReplacement := make(chan string, 1)
	factoryDone := func(component *testFocusableComponent, closeCh chan string) (core.ExtensionCustomFactory, chan struct{}) {
		closed := make(chan struct{})
		return func(tui_ tui.TUI, keybindings *core.KeybindingsManager, done func(result any)) (tui.Component, func()) {
			go func() {
				value := <-closeCh
				done(value)
				close(closed)
			}()
			return component, nil
		}, closed
	}

	mode := &InteractiveMode{
		editor:          editor,
		editorContainer: editorContainer,
		keybindings:     core.CreateKeybindingsManager(""),
	}
	mode.ui = ui

	editorContainer.AddChild(editor)
	ui.AddChild(editorContainer)
	ui.AddChild(palette)
	ui.SetFocus(palette)
	ui.Start()
	defer ui.Stop(&tui.TuiStopOptions{})

	overlayFactory, overlayClosed := factoryDone(overlay, closeOverlay)
	replacementFactory, replacementClosed := factoryDone(replacement, closeReplacement)

	flush := func() {
		ui.RequestRender(true)
		terminal.WaitForRender()
	}

	overlayDone := make(chan any, 1)
	go func() {
		overlayDone <- mode.showExtensionCustom(nil, overlayFactory, &core.ExtensionCustomOptions{Overlay: true})
	}()
	flush()
	if !overlay.GetFocused() {
		t.Fatal("expected overlay to be focused")
	}

	replacementDone := make(chan any, 1)
	go func() { replacementDone <- mode.showExtensionCustom(nil, replacementFactory, nil) }()
	flush()
	if !replacement.GetFocused() {
		t.Fatal("expected replacement to be focused")
	}

	closeReplacement <- "done"
	<-replacementDone
	<-replacementClosed
	flush()
	terminal.SendInput("x")
	flush()

	overlayInputs := overlay.inputs()
	if len(overlayInputs) != 1 || overlayInputs[0] != "x" {
		t.Fatalf("expected overlay to reclaim input, got %v", overlayInputs)
	}
	if editorInputs := editor.inputs(); len(editorInputs) != 0 {
		t.Fatalf("expected editor to receive no input, got %v", editorInputs)
	}
	if !overlay.GetFocused() {
		t.Fatal("expected overlay to stay focused")
	}

	closeOverlay <- "closed"
	<-overlayDone
	<-overlayClosed
}
