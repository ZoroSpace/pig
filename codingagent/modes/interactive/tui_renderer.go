// tui_renderer.go (pi: modes/interactive/tui-renderer.ts, v0.85.1) —
// composition root for the interactive terminal renderer, extracted from
// interactive-mode.ts so presentation variants can share it.
package interactive

import (
	"testing"

	"github.com/ZoroSpace/pig/codingagent/core"
	coretools "github.com/ZoroSpace/pig/codingagent/core/tools"
	"github.com/ZoroSpace/pig/codingagent/modes/interactive/theme"
	"github.com/ZoroSpace/pig/codingagent/utils"
	"github.com/ZoroSpace/pig/tui"
)

// createInteractiveTuiOptions mirror pi's InteractiveTuiOptions. Terminal is
// injectable for tests (pi: terminal?); nil = ProcessTerminal.
type createInteractiveTuiOptions struct {
	tuiMode                core.TuiMode
	showHardwareCursor     bool
	logDirectory           string
	terminal               tui.Terminal
	onRightClickPaste      func()
	fullscreenCopyOnSelect bool
}

// interactiveCopyToClipboard is the clipboard write used by the fullscreen
// renderer's CopySelection (pig test seam: pi mocks utils/clipboard.ts with
// vi.mock in interactive-tui.test.ts). Tests swap this out.
var interactiveCopyToClipboard = utils.CopyToClipboard

// createInteractiveTui is the composition root for the interactive terminal
// renderer (pi: createInteractiveTui).
func createInteractiveTui(options createInteractiveTuiOptions) tui.TUI {
	cursor := options.showHardwareCursor
	terminal := options.terminal
	if terminal == nil {
		terminal = tui.NewProcessTerminal()
	}
	if options.tuiMode == core.TuiModeFullscreen {
		// Styles resolve the global theme lazily at call time: the renderer is
		// built before theme.InitTheme runs (pi's `theme` import is a live
		// binding; pig's global is initialized later in Init).
		styleSearchMatch := func(text string) string {
			t := theme.Current()
			return t.Bg("searchMatchBg", t.Fg("searchMatchText", text))
		}
		copyOnSelect := options.fullscreenCopyOnSelect
		return tui.NewTuiAltScreen(terminal, &cursor, options.logDirectory, &tui.TuiAltScreenOptions{
			SearchMatchStyle: func(text string) string {
				t := theme.Current()
				return t.Underline(styleSearchMatch(text))
			},
			SearchCurrentMatchStyle: func(text string) string {
				t := theme.Current()
				return t.Bold(t.Inverse(styleSearchMatch(text)))
			},
			SearchNavigationButtonStyle: func(text string, hovered bool) string {
				t := theme.Current()
				if hovered {
					return t.Underline(text)
				}
				return text
			},
			ScrollToEndIndicator: func() string {
				t := theme.Current()
				shortcut := coretools.KeyDisplayText("tui.altScreen.bottom")
				label := " ↓ Jump to latest message"
				if shortcut != "" {
					label += " · " + shortcut
				}
				label += " "
				return t.Bg("selectedBg", t.Fg("text", label))
			},
			OpenUrl:           utils.OpenBrowser,
			OnRightClickPaste: options.onRightClickPaste,
			CopyOnSelect:      &copyOnSelect,
			CopySelection: func(text string) bool {
				if err := interactiveCopyToClipboard(text); err != nil {
					return false
				}
				return true
			},
		})
	}
	return tui.NewTuiMainScreen(terminal, &cursor, options.logDirectory)
}

// createInteractiveTuiReference returns a stable TUI reference whose method
// calls always forward to the active renderer (pi:
// createInteractiveTuiReference — a Proxy; pig needs an explicit forwarding
// type since components capture the reference at construction time).
type tuiReference struct{ getTui func() tui.TUI }

func createInteractiveTuiReference(getTui func() tui.TUI) tui.TUI {
	return &tuiReference{getTui: getTui}
}

func (r *tuiReference) Render(width int) []string          { return r.getTui().Render(width) }
func (r *tuiReference) Invalidate()                        { r.getTui().Invalidate() }
func (r *tuiReference) Mode() tui.TuiMode                  { return r.getTui().Mode() }
func (r *tuiReference) Terminal() tui.Terminal             { return r.getTui().Terminal() }
func (r *tuiReference) FullRedraws() int                   { return r.getTui().FullRedraws() }
func (r *tuiReference) AddChild(c tui.Component)           { r.getTui().AddChild(c) }
func (r *tuiReference) RemoveChild(c tui.Component)        { r.getTui().RemoveChild(c) }
func (r *tuiReference) Clear()                             { r.getTui().Clear() }
func (r *tuiReference) GetShowHardwareCursor() bool        { return r.getTui().GetShowHardwareCursor() }
func (r *tuiReference) SetShowHardwareCursor(enabled bool) { r.getTui().SetShowHardwareCursor(enabled) }
func (r *tuiReference) GetClearOnShrink() bool             { return r.getTui().GetClearOnShrink() }
func (r *tuiReference) SetClearOnShrink(enabled bool)      { r.getTui().SetClearOnShrink(enabled) }
func (r *tuiReference) SetFocus(c tui.Component)           { r.getTui().SetFocus(c) }
func (r *tuiReference) ShowOverlay(c tui.Component, o *tui.OverlayOptions) tui.OverlayHandle {
	return r.getTui().ShowOverlay(c, o)
}
func (r *tuiReference) HideOverlay()               { r.getTui().HideOverlay() }
func (r *tuiReference) HasOverlay() bool           { return r.getTui().HasOverlay() }
func (r *tuiReference) Start()                     { r.getTui().Start() }
func (r *tuiReference) Stop(o *tui.TuiStopOptions) { r.getTui().Stop(o) }
func (r *tuiReference) RenderNow(force bool)       { r.getTui().RenderNow(force) }
func (r *tuiReference) RequestRender(force bool)   { r.getTui().RequestRender(force) }
func (r *tuiReference) AddInputListener(l tui.TuiInputListener) func() {
	return r.getTui().AddInputListener(l)
}
func (r *tuiReference) RemoveInputListener(l tui.TuiInputListener) { r.getTui().RemoveInputListener(l) }
func (r *tuiReference) OnTerminalColorSchemeChange(l func(scheme tui.TerminalColorScheme)) func() {
	return r.getTui().OnTerminalColorSchemeChange(l)
}
func (r *tuiReference) SetTerminalColorSchemeNotifications(enabled bool) {
	r.getTui().SetTerminalColorSchemeNotifications(enabled)
}
func (r *tuiReference) QueryTerminalBackgroundColor(timeoutMs int) *tui.RgbColor {
	return r.getTui().QueryTerminalBackgroundColor(timeoutMs)
}
func (r *tuiReference) QueryTerminalColorScheme(timeoutMs int) *tui.TerminalColorScheme {
	return r.getTui().QueryTerminalColorScheme(timeoutMs)
}

// Convenience accessors (pi: getters).

// copyToClipboardForTest replaces the clipboard write used by the renderer
// and returns a restore function (pig test seam helper).
func copyToClipboardForTest(t *testing.T, fn func(string) error) {
	t.Helper()
	previous := interactiveCopyToClipboard
	interactiveCopyToClipboard = fn
	t.Cleanup(func() { interactiveCopyToClipboard = previous })
}
