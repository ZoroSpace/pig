// External test package: it needs core.SettingsManager (real settings
// manager fixture), and core → core/tools → theme makes an internal test
// package a compile cycle. All identifiers used here are exported.
package theme_test

import (
	"testing"

	core "github.com/ZoroSpace/pig/codingagent/core"
	"github.com/ZoroSpace/pig/codingagent/modes/interactive/theme"
	"github.com/ZoroSpace/pig/tui"
)

// Port of test/theme-controller.test.ts.
//
// Test adaptations (declared): pi spies on manager.setTheme/flush; pig
// asserts the settings manager's theme setting is unchanged (setTheme
// would overwrite it). The fake TUI implements the controller's
// consumption-face interface instead of casting a partial object.

// fakeControllerTUI records the controller's TUI interactions (pi:
// createUi's vi.fn() mocks).
type fakeControllerTUI struct {
	queryTerminalBackgroundColorCalls int
	queryTerminalColorSchemeCalls     int
	queryTerminalColorSchemeResult    *tui.TerminalColorScheme
	setNotificationsCalls             []bool
	colorSchemeListener               func(scheme tui.TerminalColorScheme)
}

func (f *fakeControllerTUI) Invalidate() {}

func (f *fakeControllerTUI) RequestRender(force bool) {}

func (f *fakeControllerTUI) SetTerminalColorSchemeNotifications(enabled bool) {
	f.setNotificationsCalls = append(f.setNotificationsCalls, enabled)
}

func (f *fakeControllerTUI) OnTerminalColorSchemeChange(listener func(scheme tui.TerminalColorScheme)) func() {
	f.colorSchemeListener = listener
	return func() {}
}

func (f *fakeControllerTUI) QueryTerminalBackgroundColor(timeoutMs int) *tui.RgbColor {
	f.queryTerminalBackgroundColorCalls++
	return nil
}

func (f *fakeControllerTUI) QueryTerminalColorScheme(timeoutMs int) *tui.TerminalColorScheme {
	f.queryTerminalColorSchemeCalls++
	return f.queryTerminalColorSchemeResult
}

func (f *fakeControllerTUI) emitTerminalColorScheme(terminalTheme tui.TerminalColorScheme) {
	if f.colorSchemeListener != nil {
		f.colorSchemeListener(terminalTheme)
	}
}

func newSettingsManager(t *testing.T, settings core.Settings) *core.SettingsManager {
	t.Helper()
	return core.InMemorySettingsManager(settings, nil)
}

func TestInteractiveThemeController(t *testing.T) {
	t.Cleanup(func() { theme.InitTheme("dark", false) })

	t.Run("uses the initial theme without persisting it", func(t *testing.T) {
		ui := &fakeControllerTUI{}
		manager := newSettingsManager(t, core.Settings{"theme": "dark"})
		controller := theme.NewInteractiveThemeController(ui, theme.InteractiveThemeControllerOptions{
			GetSettingsManager:  func() theme.ThemeSettingsManager { return manager },
			ShowError:           func(string) {},
			OnChanged:           func() {},
			InitialThemeSetting: "light",
		})

		if got := theme.Current().GetName(); got != "light" {
			t.Fatalf("theme.name = %q", got)
		}
		if selection, ok := controller.GetThemeSelection(); !ok || selection != "light" {
			t.Fatalf("selection = %q, %v", selection, ok)
		}
		if err := controller.ApplyFromSettings(); err != nil {
			t.Fatal(err)
		}

		if ui.queryTerminalBackgroundColorCalls != 0 {
			t.Fatal("expected no background query")
		}
		if setting, _ := manager.GetThemeSetting(); setting != "dark" {
			t.Fatalf("settings theme = %q (setTheme must not persist)", setting)
		}
	})

	t.Run("resolves a theme pair and follows terminal appearance changes", func(t *testing.T) {
		t.Setenv("COLORFGBG", "15;0")
		ui := &fakeControllerTUI{}
		scheme := tui.TerminalColorScheme("light")
		ui.queryTerminalColorSchemeResult = &scheme
		manager := newSettingsManager(t, core.Settings{"theme": "dark/light"})
		controller := theme.NewInteractiveThemeController(ui, theme.InteractiveThemeControllerOptions{
			GetSettingsManager:  func() theme.ThemeSettingsManager { return manager },
			ShowError:           func(string) {},
			OnChanged:           func() {},
			InitialThemeSetting: "light/dark",
		})

		if got := theme.Current().GetName(); got != "dark" {
			t.Fatalf("theme.name = %q", got)
		}
		if err := controller.ApplyFromSettings(); err != nil {
			t.Fatal(err)
		}
		if got := theme.Current().GetName(); got != "light" {
			t.Fatalf("theme.name after apply = %q", got)
		}
		if len(ui.setNotificationsCalls) == 0 || ui.setNotificationsCalls[len(ui.setNotificationsCalls)-1] != true {
			t.Fatalf("notifications calls = %v", ui.setNotificationsCalls)
		}

		ui.emitTerminalColorScheme("dark")
		if got := theme.Current().GetName(); got != "dark" {
			t.Fatalf("theme.name after scheme change = %q", got)
		}
	})

	t.Run("detects the current terminal appearance when selecting a theme pair", func(t *testing.T) {
		t.Setenv("COLORFGBG", "")
		ui := &fakeControllerTUI{}
		scheme := tui.TerminalColorScheme("light")
		ui.queryTerminalColorSchemeResult = &scheme
		manager := newSettingsManager(t, core.Settings{"theme": "dark"})
		controller := theme.NewInteractiveThemeController(ui, theme.InteractiveThemeControllerOptions{
			GetSettingsManager: func() theme.ThemeSettingsManager { return manager },
			ShowError:          func(string) {},
			OnChanged:          func() {},
		})

		if got := theme.Current().GetName(); got != "dark" {
			t.Fatalf("theme.name = %q", got)
		}
		if err := controller.SetThemeSetting("light/dark"); err != nil {
			t.Fatal(err)
		}
		if got := theme.Current().GetName(); got != "light" {
			t.Fatalf("theme.name = %q", got)
		}
		if ui.queryTerminalColorSchemeCalls != 1 {
			t.Fatalf("color scheme queries = %d", ui.queryTerminalColorSchemeCalls)
		}
	})

	t.Run("lets an explicit selection replace the initial theme", func(t *testing.T) {
		ui := &fakeControllerTUI{}
		firstManager := newSettingsManager(t, core.Settings{"theme": "dark"})
		secondManager := newSettingsManager(t, core.Settings{"theme": "light"})
		manager := firstManager
		controller := theme.NewInteractiveThemeController(ui, theme.InteractiveThemeControllerOptions{
			GetSettingsManager:  func() theme.ThemeSettingsManager { return manager },
			ShowError:           func(string) {},
			OnChanged:           func() {},
			InitialThemeSetting: "light",
		})
		if err := controller.ApplyFromSettings(); err != nil {
			t.Fatal(err)
		}

		if result := controller.SetThemeName("dark", false); !result.Success {
			t.Fatalf("setThemeName failed: %s", result.Error)
		}
		manager = secondManager
		if err := controller.ApplyFromSettings(); err != nil {
			t.Fatal(err)
		}

		if selection, ok := controller.GetThemeSelection(); !ok || selection != "dark" {
			t.Fatalf("selection = %q, %v", selection, ok)
		}
		if got := theme.Current().GetName(); got != "dark" {
			t.Fatalf("theme.name = %q", got)
		}
	})

	t.Run("reloads theme settings when no initial theme was supplied", func(t *testing.T) {
		ui := &fakeControllerTUI{}
		firstManager := newSettingsManager(t, core.Settings{"theme": "dark"})
		secondManager := newSettingsManager(t, core.Settings{"theme": "light"})
		manager := firstManager
		controller := theme.NewInteractiveThemeController(ui, theme.InteractiveThemeControllerOptions{
			GetSettingsManager: func() theme.ThemeSettingsManager { return manager },
			ShowError:          func(string) {},
			OnChanged:          func() {},
		})
		if err := controller.ApplyFromSettings(); err != nil {
			t.Fatal(err)
		}

		firstManager.ApplyOverrides(core.Settings{"theme": "light"})
		if err := controller.ApplyFromSettings(); err != nil {
			t.Fatal(err)
		}
		if got := theme.Current().GetName(); got != "light" {
			t.Fatalf("theme.name = %q", got)
		}

		secondManager.ApplyOverrides(core.Settings{"theme": "dark"})
		manager = secondManager
		if err := controller.ApplyFromSettings(); err != nil {
			t.Fatal(err)
		}
		if got := theme.Current().GetName(); got != "dark" {
			t.Fatalf("theme.name = %q", got)
		}
	})
}
