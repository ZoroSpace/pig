// startup_ui.go (pi: src/cli/startup-ui.ts) — startup TUI composition:
// theme registration, first-time setup, and blocking startup selectors/
// inputs used before interactive mode mounts.
package cli

import (
	"os"
	"time"

	"github.com/ZoroSpace/pig/codingagent"
	"github.com/ZoroSpace/pig/codingagent/core"
	"github.com/ZoroSpace/pig/codingagent/modes/interactive/components"
	"github.com/ZoroSpace/pig/codingagent/modes/interactive/theme"
	"github.com/ZoroSpace/pig/tui"
	"github.com/samber/lo"
)

const (
	officialPackageName   = "@earendil-works/pi-coding-agent"
	officialAppName       = "pi"
	officialConfigDirName = ".pig" // pig: renamed from pi's ".pi" to match codingagent.ConfigDirName
)

func isOfficialDistribution(packageName string, appName string, configDirName string) bool {
	return packageName == officialPackageName &&
		appName == officialAppName &&
		configDirName == officialConfigDirName
}

// loadStartupThemes collects themes from the resolved startup resources.
func loadStartupThemes(resources []core.ResolvedResource) []*theme.Theme {
	themes := []*theme.Theme{}
	seen := map[string]bool{}
	for _, resource := range resources {
		if !resource.Enabled {
			continue
		}
		loaded, err := theme.LoadThemeFromPath(resource.Path, "")
		if err != nil {
			// Startup prompts should not fail because a theme is broken. The
			// normal resource loader reports theme diagnostics later in
			// startup.
			continue
		}
		if name := loaded.GetName(); name != "" {
			if seen[name] {
				continue
			}
			seen[name] = true
		}
		themes = append(themes, loaded)
	}
	return themes
}

func loadStartupThemesFor(settingsManager *core.SettingsManager) []*theme.Theme {
	globalSettingsManager := core.InMemorySettingsManager(settingsManager.GetGlobalSettings(), &core.SettingsManagerCreateOptions{ProjectTrusted: lo.ToPtr(false)})
	resolvedPaths := core.ResolveSettingsResourcePaths(globalSettingsManager, mustCwd(), codingagent.GetAgentDir())
	return loadStartupThemes(resolvedPaths.Themes)
}

// CreateStartupTui builds the startup renderer with registered themes and
// keybindings (pi: createStartupTui).
func CreateStartupTui(settingsManager *core.SettingsManager) *tui.TuiMainScreen {
	theme.SetRegisteredThemes(loadStartupThemesFor(settingsManager))
	terminalTheme := theme.DetectTerminalBackgroundFromEnv(nil).Theme
	name := ""
	if resolved, ok := theme.ResolveThemeSetting(mustThemeSetting(settingsManager), terminalTheme); ok {
		name = resolved
	} else {
		name = string(terminalTheme)
	}
	theme.InitTheme(name, false)
	tui.SetKeybindings(core.CreateKeybindingsManager("").KeybindingsManager)
	showHardwareCursor := settingsManager.GetShowHardwareCursor()
	ui := tui.NewTuiMainScreen(tui.NewProcessTerminal(), &showHardwareCursor, codingagent.GetAgentDir())
	ui.SetClearOnShrink(settingsManager.GetClearOnShrink())
	return ui
}

// StartStartupTui starts the renderer and applies the detected auto theme
// (pi: startStartupTui).
func StartStartupTui(ui *tui.TuiMainScreen, settingsManager *core.SettingsManager) {
	ui.Start()
	applyDetectedStartupTheme(ui, settingsManager)
}

func applyDetectedStartupTheme(ui *tui.TuiMainScreen, settingsManager *core.SettingsManager) {
	themeSetting, ok := mustThemeSettingFull(settingsManager)
	if ok && theme.ParseAutoThemeSetting(themeSetting) == nil {
		return
	}

	terminalTheme := theme.DetectTerminalThemeForAuto(theme.TerminalAutoThemeDetectionOptions{
		Ui:        theme.TuiThemeDetector(ui),
		TimeoutMs: 100,
	})
	name := ""
	if ok {
		if resolved, resolvedOk := theme.ResolveThemeSetting(themeSetting, terminalTheme); resolvedOk {
			name = resolved
		} else {
			name = string(terminalTheme)
		}
	} else {
		name = string(terminalTheme)
	}
	theme.SetTheme(name, false)
	ui.Invalidate()
	ui.RequestRender(false)
}

func clearStartupTui(ui *tui.TuiMainScreen) {
	ui.Clear()
	ui.RequestRender(false)
	time.Sleep(25 * time.Millisecond)
}

// ShouldRunFirstTimeSetup reports whether the first-time setup dialog
// should run. First-time setup runs when all of these hold:
//   - this is the official Pi distribution (not a fork/rebrand)
//   - experimental features are enabled (PI_EXPERIMENTAL=1)
//   - the default agent directory is used (no custom agent dir override)
//   - setup was not completed before (settings.json does not exist)
func ShouldRunFirstTimeSetup(settingsPath string) bool {
	if settingsPath == "" {
		settingsPath = codingagent.GetSettingsPath()
	}
	if !isOfficialDistribution(codingagent.PackageName, codingagent.AppName, codingagent.ConfigDirName) {
		return false
	}
	if !core.AreExperimentalFeaturesEnabled() {
		return false
	}
	if os.Getenv(codingagent.EnvAgentDir) != "" {
		return false
	}
	if _, err := os.Stat(settingsPath); err != nil {
		return true
	}
	return false
}

// ShowStartupSelector shows a blocking option list (pi:
// showStartupSelector).
func ShowStartupSelector(settingsManager *core.SettingsManager, title string, options []StartupOption) *string {
	ui := CreateStartupTui(settingsManager)
	resultCh := make(chan *string, 1)
	settled := false
	finish := func(result *string) {
		if settled {
			return
		}
		settled = true
		resultCh <- result
	}

	labels := make([]string, len(options))
	for i, option := range options {
		labels[i] = option.Label
	}
	selector := components.NewExtensionSelectorComponent(title, labels,
		func(option string) {
			for i, label := range labels {
				if label == option {
					value := options[i].Value
					finish(&value)
					return
				}
			}
			finish(nil)
		},
		func() { finish(nil) },
		&components.ExtensionSelectorOptions{Tui: ui},
	)
	ui.AddChild(selector)
	ui.SetFocus(selector)
	StartStartupTui(ui, settingsManager)

	result := <-resultCh
	clearStartupTui(ui)
	ui.Stop(nil)
	return result
}

// StartupOption is one labeled selector entry (pi: the
// Array<{ label, value }> parameter).
type StartupOption struct {
	Label string
	Value string
}

// ShowFirstTimeSetup shows the first-time setup dialog and persists the
// result (pi: showFirstTimeSetup).
func ShowFirstTimeSetup(settingsManager *core.SettingsManager) {
	ui := CreateStartupTui(settingsManager)
	resultCh := make(chan *components.FirstTimeSetupResult, 1)
	settled := false
	finish := func(result *components.FirstTimeSetupResult) {
		if settled {
			return
		}
		settled = true
		resultCh <- result
	}

	go func() {
		ui.Start()
		detectedTheme := theme.DetectTerminalThemeForAuto(theme.TerminalAutoThemeDetectionOptions{
			Ui:        theme.TuiThemeDetector(ui),
			TimeoutMs: 100,
		})
		theme.SetTheme(string(detectedTheme), false)
		component := components.NewFirstTimeSetupComponent(components.FirstTimeSetupOptions{
			DetectedTheme: detectedTheme,
			OnThemePreview: func(themeName theme.TerminalTheme) {
				theme.SetTheme(string(themeName), false)
				ui.RequestRender(false)
			},
			OnSubmit: func(result components.FirstTimeSetupResult) { finish(&result) },
			OnCancel: func() { finish(nil) },
		})
		ui.AddChild(component)
		ui.SetFocus(component)
		ui.RequestRender(false)
	}()

	result := <-resultCh
	if result != nil {
		settingsManager.SetTheme(string(result.Theme))
		settingsManager.SetEnableAnalytics(result.ShareAnalytics)
		settingsManager.Flush()
	}
	clearStartupTui(ui)
	ui.Stop(nil)
}

// ShowStartupInput shows a blocking text prompt (pi: showStartupInput).
func ShowStartupInput(settingsManager *core.SettingsManager, title string, placeholder *string) *string {
	ui := CreateStartupTui(settingsManager)
	resultCh := make(chan *string, 1)
	settled := false
	finish := func(result *string) {
		if settled {
			return
		}
		settled = true
		resultCh <- result
	}

	input := components.NewExtensionInputComponent(title, placeholder,
		func(value string) { finish(&value) },
		func() { finish(nil) },
		&components.ExtensionInputOptions{Tui: ui},
	)
	ui.AddChild(input)
	ui.SetFocus(input)
	StartStartupTui(ui, settingsManager)

	result := <-resultCh
	input.Dispose()
	clearStartupTui(ui)
	ui.Stop(nil)
	return result
}

// mustCwd returns the process working directory ("" on error — resolution
// falls back to path-relative logic).
func mustCwd() string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return cwd
}

// mustThemeSetting reads the raw theme setting ("" = unset).
func mustThemeSetting(settingsManager *core.SettingsManager) string {
	setting, _ := settingsManager.GetThemeSetting()
	return setting
}

// mustThemeSettingFull is mustThemeSetting with the ok flag (pi: the
// undefined/empty distinction).
func mustThemeSettingFull(settingsManager *core.SettingsManager) (string, bool) {
	return settingsManager.GetThemeSetting()
}
