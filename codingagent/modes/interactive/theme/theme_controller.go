// theme_controller.go (pi: src/modes/interactive/theme/theme-controller.ts).
//
// The controller declares a narrow TUI consumption-face interface
// (ThemeControllerTUI) instead of pi's full TUI type — repo precedent:
// ModelResolverRuntime. tui.TUI satisfies it; tests stub it.
//
// pi's async controller methods (applyFromSettings/setThemeSetting) become
// blocking calls with error returns; the terminal color query is blocking
// (see the QueryTerminalColors dispatch-safety notes in tui/tui.go).
// waitForTerminalColors becomes WaitTerminalColors (blocking on a channel).
package theme

import (
	"sync"

	"github.com/ZoroSpace/pig/tui"
)

// terminalQueryTimeoutMs is how long the system theme stays grayscale before
// falling back to palette indices. Terminals answer the trailing DA1 request
// right after the color replies, so this only matters for terminals that
// answer neither. Replies arriving later still apply (pi:
// TERMINAL_QUERY_TIMEOUT_MS).
const terminalQueryTimeoutMs = 100

// ThemeSettingsManager is the SettingsManager surface the controller
// consumes (pi: SettingsManager). Declared deviation: package theme cannot
// import core (core → core/tools → theme would cycle); *core.SettingsManager
// satisfies this interface structurally.
type ThemeSettingsManager interface {
	// GetThemeSetting is pi's settingsManager.getThemeSetting().
	GetThemeSetting() (string, bool)
	// SetTheme is pi's settingsManager.setTheme().
	SetTheme(theme string)
	// Flush is pi's settingsManager.flush().
	Flush()
}

// ThemeControllerTUI is the TUI surface consumed by the controller (pi:
// TUI used via invalidate/requestRender/setTerminalColorSchemeNotifications/
// onTerminalColorSchemeChange/queryTerminalColors).
type ThemeControllerTUI interface {
	Invalidate()
	RequestRender(force bool)
	SetTerminalColorSchemeNotifications(enabled bool)
	OnTerminalColorSchemeChange(listener func(scheme tui.TerminalColorScheme)) func()
	QueryTerminalColors(timeoutMs int, onLateReply func(colors tui.TerminalColors)) tui.TerminalColors
}

// TerminalColorQuerier is the narrow face requestTerminalColors needs (pi
// passes the full TUI; pig narrows so the startup selectors can share the
// helper without a full controller TUI).
type TerminalColorQuerier interface {
	QueryTerminalColors(timeoutMs int, onLateReply func(colors tui.TerminalColors)) tui.TerminalColors
}

// RequestTerminalColors queries the terminal's colors and passes them to
// apply when the query completes or times out, and again if the terminal
// answers after the timeout. A failed query applies no colors. Settles after
// the first apply (pi: requestTerminalColors; pig blocks and returns the
// first result).
func RequestTerminalColors(ui TerminalColorQuerier, apply func(colors tui.TerminalColors)) tui.TerminalColors {
	return ui.QueryTerminalColors(terminalQueryTimeoutMs, apply)
}

func sameRgb(a, b *tui.RgbColor) bool {
	if a == b {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return a.R == b.R && a.G == b.G && a.B == b.B
}

func sameTerminalColors(a, b tui.TerminalColors) bool {
	if !sameRgb(a.Foreground, b.Foreground) || !sameRgb(a.Background, b.Background) {
		return false
	}
	if len(a.Palette) == 0 && len(b.Palette) == 0 {
		return true
	}
	if len(a.Palette) != len(b.Palette) {
		return false
	}
	for i := range a.Palette {
		if a.Palette[i] != b.Palette[i] {
			return false
		}
	}
	return true
}

// ThemeResult is pi's ThemeResult = { success: boolean; error?: string }.
type ThemeResult struct {
	Success bool
	Error   string
}

// InteractiveThemeController manages the global theme for the interactive
// session (pi: class InteractiveThemeController).
type InteractiveThemeController struct {
	ui                             ThemeControllerTUI
	getSettingsManager             func() ThemeSettingsManager
	showError                      func(message string)
	onChanged                      func()
	currentThemeSetting            string              // "" = undefined (pi: string | undefined)
	terminalColors                 *tui.TerminalColors // nil = unset; last reported colors
	activeThemeName                string              // "" = undefined (pi: string | undefined)
	autoSyncEnabled                bool
	terminalColorSchemeUnsubscribe func()
	// terminalColorQuery settles when the latest color query completed or
	// timed out, and its colors applied (pi: terminalColorQuery promise →
	// pig: a fresh channel per query; WaitTerminalColors blocks on it).
	terminalColorQuery chan struct{}
}

// InteractiveThemeControllerOptions carries the constructor's callbacks
// (pi: options object; initialThemeSetting "" = undefined).
type InteractiveThemeControllerOptions struct {
	GetSettingsManager  func() ThemeSettingsManager
	ShowError           func(message string)
	OnChanged           func()
	InitialThemeSetting string
}

// NewInteractiveThemeController builds the controller (pi: constructor).
func NewInteractiveThemeController(ui ThemeControllerTUI, options InteractiveThemeControllerOptions) *InteractiveThemeController {
	c := &InteractiveThemeController{
		ui:                  ui,
		getSettingsManager:  options.GetSettingsManager,
		showError:           options.ShowError,
		onChanged:           options.OnChanged,
		currentThemeSetting: options.InitialThemeSetting,
		terminalColorQuery:  make(chan struct{}),
	}
	c.activeThemeName = c.resolveThemeName()
	// The system theme starts in grayscale; color follows once the terminal
	// reports its colors.
	MarkTerminalColorsPending()
	InitTheme(c.activeThemeName, true)
	c.bindTerminalColorSchemeListener()
	return c
}

// RebindTui re-subscribes the color-scheme listener after a TUI swap (pi:
// rebindTui).
func (c *InteractiveThemeController) RebindTui() {
	if c.terminalColorSchemeUnsubscribe != nil {
		c.terminalColorSchemeUnsubscribe()
	}
	c.bindTerminalColorSchemeListener()
	c.ui.SetTerminalColorSchemeNotifications(c.autoSyncEnabled)
}

// Dispose stops terminal appearance updates and drops the color-scheme
// listener subscription (pi: dispose).
func (c *InteractiveThemeController) Dispose() {
	c.setAutoSync(false)
	if c.terminalColorSchemeUnsubscribe != nil {
		c.terminalColorSchemeUnsubscribe()
		c.terminalColorSchemeUnsubscribe = nil
	}
}

// ApplyFromSettings applies the theme setting now and queries the terminal's
// colors, which update the theme when they arrive. Theme pairs and the system
// theme follow terminal appearance changes (pi: applyFromSettings).
func (c *InteractiveThemeController) ApplyFromSettings() {
	themeSetting := c.getThemeSetting()
	themeName := c.resolveThemeName()
	c.setAutoSync(ParseAutoThemeSetting(themeSetting) != nil || themeName == SystemThemeName)
	c.applyThemeName(themeName, themeSetting != "")
	c.queryTerminalColors()
}

// WaitTerminalColors blocks until the latest color query completed or timed
// out. Content that bakes theme colors into strings, such as the startup
// header, should be built after this. Terminals answer the DA1 request right
// after the color replies, so this only takes the full timeout when a
// terminal answers nothing (pi: waitForTerminalColors).
func (c *InteractiveThemeController) WaitTerminalColors() {
	<-c.terminalColorQuery
}

// GetThemeSelection returns the effective theme setting (pi:
// getThemeSelection; second return false = undefined).
func (c *InteractiveThemeController) GetThemeSelection() (string, bool) {
	if c.currentThemeSetting != "" {
		return c.currentThemeSetting, true
	}
	if setting, ok := c.getSettingsManager().GetThemeSetting(); ok {
		return setting, true
	}
	if c.activeThemeName != "" {
		return c.activeThemeName, true
	}
	return "", false
}

// SetThemeName selects a theme explicitly (pi: setThemeName).
func (c *InteractiveThemeController) SetThemeName(themeName string, showError bool) ThemeResult {
	c.setAutoSync(themeName == SystemThemeName)
	result := c.applyThemeName(themeName, showError)
	if result.Success {
		c.currentThemeSetting = themeName
	}
	return ThemeResult{Success: result.Success, Error: result.Error}
}

// SetThemeSetting persists and applies a theme setting (pi:
// setThemeSetting).
func (c *InteractiveThemeController) SetThemeSetting(themeSetting string) {
	c.currentThemeSetting = themeSetting
	c.ApplyFromSettings()
}

// SetThemeInstance installs a direct theme instance (pi: setThemeInstance).
func (c *InteractiveThemeController) SetThemeInstance(themeInstance *Theme) ThemeResult {
	c.setAutoSync(false)
	SetThemeInstance(themeInstance)
	c.activeThemeName = "<in-memory>"
	c.notifyChanged()
	return ThemeResult{Success: true}
}

// Preview applies a theme without persisting the selection (pi: preview).
func (c *InteractiveThemeController) Preview(themeSettingOrName string) {
	themeName, ok := ResolveThemeSetting(themeSettingOrName, GetTerminalTheme())
	if !ok {
		if c.activeThemeName == "" {
			return
		}
		themeName = c.activeThemeName
	}
	if SetTheme(themeName, true).Success {
		c.ui.Invalidate()
		c.ui.RequestRender(false)
	}
}

// DisableAutoSync turns off light/dark auto sync (pi: disableAutoSync).
func (c *InteractiveThemeController) DisableAutoSync() {
	c.setAutoSync(false)
}

// GetTerminalTheme returns the detected terminal appearance (pi:
// getTerminalTheme).
func (c *InteractiveThemeController) GetTerminalTheme() TerminalTheme {
	return GetTerminalTheme()
}

func (c *InteractiveThemeController) getThemeSetting() string {
	if c.currentThemeSetting != "" {
		return c.currentThemeSetting
	}
	setting, _ := c.getSettingsManager().GetThemeSetting()
	return setting
}

// resolveThemeName returns the theme for the current setting and terminal
// appearance. Without a setting, pi uses the system theme.
func (c *InteractiveThemeController) resolveThemeName() string {
	if resolved, ok := ResolveThemeSetting(c.getThemeSetting(), GetTerminalTheme()); ok {
		return resolved
	}
	return SystemThemeName
}

func (c *InteractiveThemeController) applyThemeName(themeName string, showError bool) SetThemeResult {
	result := SetTheme(themeName, true)
	if result.Success {
		c.activeThemeName = themeName
	} else {
		c.activeThemeName = SystemThemeName
	}
	c.notifyChanged()
	if !result.Success && showError {
		c.showError("Failed to load theme \"" + themeName + "\": " + result.Error + "\nFell back to the system theme.")
	}
	return result
}

// queryTerminalColors queries the terminal's colors without waiting for them;
// WaitTerminalColors waits for this query (pi: queryTerminalColors).
func (c *InteractiveThemeController) queryTerminalColors() {
	settled := make(chan struct{})
	var once sync.Once
	closeSettled := func() { once.Do(func() { close(settled) }) }
	c.terminalColorQuery = settled
	first := c.ui.QueryTerminalColors(terminalQueryTimeoutMs, func(colors tui.TerminalColors) {
		c.applyTerminalColors(colors)
		closeSettled()
	})
	// The blocking query returns the colors reported by the timeout; the
	// late-reply callback covers replies that arrive afterwards (pi:
	// query.then(apply, ...) with onLateReply: apply).
	c.applyTerminalColors(first)
	closeSettled()
}

// applyTerminalColors records reported colors: themes use the default colors
// for tokens set to "", the system theme is generated from all of them, and
// light/dark detection uses them. Re-renders only when they changed (pi:
// applyTerminalColors).
func (c *InteractiveThemeController) applyTerminalColors(reported tui.TerminalColors) {
	previous := c.terminalColors
	next := tui.TerminalColors{}
	if reported.Foreground != nil || (previous != nil && previous.Foreground != nil) {
		if reported.Foreground != nil {
			fg := *reported.Foreground
			next.Foreground = &fg
		} else {
			next.Foreground = previous.Foreground
		}
	}
	if reported.Background != nil || (previous != nil && previous.Background != nil) {
		if reported.Background != nil {
			bg := *reported.Background
			next.Background = &bg
		} else {
			next.Background = previous.Background
		}
	}
	if len(reported.Palette) > 0 || (previous != nil && len(previous.Palette) > 0) {
		if len(reported.Palette) > 0 {
			next.Palette = reported.Palette
		} else {
			next.Palette = previous.Palette
		}
	}
	// Re-rendering rebuilds every component, so skip it when nothing changed
	// (including timeouts).
	if previous != nil && sameTerminalColors(*previous, next) {
		return
	}
	c.terminalColors = &next
	SetTerminalColors(next)
	c.reapplyForTerminal()
	c.ui.Invalidate()
	c.ui.RequestRender(false)
}

// reapplyForTerminal re-applies the setting after the terminal's colors or
// appearance changed: regenerate the system theme, or switch the theme of a
// pair. Themes set through extensions or previews are left alone (pi:
// reapplyForTerminal).
func (c *InteractiveThemeController) reapplyForTerminal() {
	if c.activeThemeName == "<in-memory>" {
		return
	}
	themeName := c.resolveThemeName()
	if themeName == SystemThemeName || themeName != c.activeThemeName {
		c.applyThemeName(themeName, false)
	}
}

func (c *InteractiveThemeController) setAutoSync(enabled bool) {
	if c.autoSyncEnabled == enabled {
		return
	}
	c.autoSyncEnabled = enabled
	c.ui.SetTerminalColorSchemeNotifications(enabled)
}

func (c *InteractiveThemeController) bindTerminalColorSchemeListener() {
	c.terminalColorSchemeUnsubscribe = c.ui.OnTerminalColorSchemeChange(func(terminalTheme tui.TerminalColorScheme) {
		c.applyTerminalColorSchemeChange(TerminalTheme(terminalTheme))
	})
}

// applyTerminalColorSchemeChange handles the terminal reporting a light/dark
// switch. Its colors changed too, so query them again: they decide the
// appearance. The reported scheme only matters for terminals that do not
// report their background (pi: applyTerminalColorSchemeChange).
func (c *InteractiveThemeController) applyTerminalColorSchemeChange(terminalTheme TerminalTheme) {
	if !c.autoSyncEnabled {
		return
	}
	previous := GetTerminalTheme()
	SetTerminalColorScheme(terminalTheme)
	if GetTerminalTheme() != previous {
		c.reapplyForTerminal()
	}
	c.queryTerminalColors()
}

func (c *InteractiveThemeController) notifyChanged() {
	c.ui.Invalidate()
	c.onChanged()
}
