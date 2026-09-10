// theme_controller.go (pi: src/modes/interactive/theme/theme-controller.ts).
//
// The controller declares a narrow TUI consumption-face interface
// (ThemeControllerTUI) instead of pi's full TUI type — repo precedent:
// ModelResolverRuntime. tui.TUI satisfies it; tests stub it.
//
// pi's async controller methods (applyFromSettings/setThemeSetting) become
// blocking calls with error returns; the terminal queries are blocking
// (see theme.go detection deviations).
package theme

import (
	"github.com/ZoroSpace/pig/tui"
)

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
// onTerminalColorSchemeChange/queryTerminalColorScheme;
// queryTerminalBackgroundColor reaches the detector through
// DetectTerminalBackgroundTheme).
type ThemeControllerTUI interface {
	Invalidate()
	RequestRender(force bool)
	SetTerminalColorSchemeNotifications(enabled bool)
	OnTerminalColorSchemeChange(listener func(scheme tui.TerminalColorScheme)) func()
	QueryTerminalBackgroundColor(timeoutMs int) *tui.RgbColor
	QueryTerminalColorScheme(timeoutMs int) *tui.TerminalColorScheme
}

// controllerThemeDetector adapts the controller TUI to the detection
// interfaces (pi passes the TUI directly; pig's detector interfaces carry
// explicit error returns).
type controllerThemeDetector struct{ ui ThemeControllerTUI }

// TuiThemeDetector adapts any tui.TUI to the theme detection interfaces
// (pig addition for the startup-ui/interactive-mode callers; pi passes the
// TUI directly).
func TuiThemeDetector(ui ThemeControllerTUI) TerminalAutoThemeDetector {
	return controllerThemeDetector{ui: ui}
}

func (d controllerThemeDetector) QueryTerminalBackgroundColor(timeoutMs int) (*tui.RgbColor, error) {
	// pig's blocking query reports failure as nil (pi: rejected promise →
	// catch → env fallback; TUI timeout → nil → same path).
	return d.ui.QueryTerminalBackgroundColor(timeoutMs), nil
}

func (d controllerThemeDetector) QueryTerminalColorScheme(timeoutMs int) (*TerminalTheme, error) {
	scheme := d.ui.QueryTerminalColorScheme(timeoutMs)
	if scheme == nil {
		return nil, nil
	}
	theme := TerminalTheme(*scheme)
	return &theme, nil
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
	currentThemeSetting            string // "" = undefined (pi: string | undefined)
	terminalTheme                  TerminalTheme
	activeThemeName                string // "" = undefined (pi: string | undefined)
	autoSyncEnabled                bool
	terminalColorSchemeUnsubscribe func()
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
		terminalTheme:       DetectTerminalBackgroundFromEnv(nil).Theme,
	}
	setting := c.currentThemeSetting
	if setting == "" {
		setting, _ = c.getSettingsManager().GetThemeSetting()
	}
	c.activeThemeName, _ = ResolveThemeSetting(setting, c.terminalTheme)
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

// ApplyFromSettings resolves the theme from the current setting and applies
// it (pi: applyFromSettings).
func (c *InteractiveThemeController) ApplyFromSettings() error {
	settingsManager := c.getSettingsManager()
	themeSetting := c.currentThemeSetting
	if themeSetting == "" {
		themeSetting, _ = settingsManager.GetThemeSetting()
	}
	if autoTheme := ParseAutoThemeSetting(themeSetting); autoTheme != nil {
		c.terminalTheme = DetectTerminalThemeForAuto(TerminalAutoThemeDetectionOptions{
			Ui:        controllerThemeDetector{ui: c.ui},
			TimeoutMs: 100,
		})
		c.setAutoSync(true)
		theme := autoTheme.DarkTheme
		if c.terminalTheme == TerminalThemeLight {
			theme = autoTheme.LightTheme
		}
		c.applyThemeName(theme, true)
		return nil
	}

	c.setAutoSync(false)
	if themeSetting != "" {
		c.applyThemeName(themeSetting, true)
		return nil
	}

	detection := DetectTerminalBackgroundTheme(TerminalBackgroundThemeDetectionOptions{
		Ui:        controllerThemeDetector{ui: c.ui},
		TimeoutMs: 100,
	})
	c.terminalTheme = detection.Theme
	if !c.applyThemeName(string(detection.Theme), false).Success {
		return nil
	}
	if detection.Confidence == "high" {
		settingsManager.SetTheme(string(detection.Theme))
		settingsManager.Flush()
	}
	return nil
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
	c.setAutoSync(false)
	result := c.applyThemeName(themeName, showError)
	if result.Success {
		c.currentThemeSetting = themeName
	}
	return ThemeResult{Success: result.Success, Error: result.Error}
}

// SetThemeSetting persists and applies a theme setting (pi:
// setThemeSetting).
func (c *InteractiveThemeController) SetThemeSetting(themeSetting string) error {
	c.currentThemeSetting = themeSetting
	return c.ApplyFromSettings()
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
	themeName, ok := ResolveThemeSetting(themeSettingOrName, c.terminalTheme)
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
	return c.terminalTheme
}

func (c *InteractiveThemeController) applyThemeName(themeName string, showError bool) SetThemeResult {
	result := SetTheme(themeName, true)
	if result.Success {
		c.activeThemeName = themeName
	} else {
		c.activeThemeName = "dark"
	}
	c.notifyChanged()
	if !result.Success && showError {
		c.showError("Failed to load theme \"" + themeName + "\": " + result.Error + "\nFell back to dark theme.")
	}
	return result
}

func (c *InteractiveThemeController) notifyChanged() {
	c.ui.Invalidate()
	c.onChanged()
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
		c.applyTerminalTheme(TerminalTheme(terminalTheme))
	})
}

func (c *InteractiveThemeController) applyTerminalTheme(terminalTheme TerminalTheme) {
	if !c.autoSyncEnabled {
		return
	}
	c.terminalTheme = terminalTheme
	setting := c.currentThemeSetting
	if setting == "" {
		setting, _ = c.getSettingsManager().GetThemeSetting()
	}
	autoTheme := ParseAutoThemeSetting(setting)
	if autoTheme == nil {
		c.setAutoSync(false)
		return
	}
	themeName := autoTheme.DarkTheme
	if terminalTheme == TerminalThemeLight {
		themeName = autoTheme.LightTheme
	}
	if themeName != c.activeThemeName {
		c.applyThemeName(themeName, false)
	}
}
