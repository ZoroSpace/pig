// first_time_setup.go (pi: components/first-time-setup.ts) — first-time
// setup dialog: theme choice and analytics opt-in.
package components

import (
	"github.com/ZoroSpace/pig/codingagent"
	coretools "github.com/ZoroSpace/pig/codingagent/core/tools"
	"github.com/ZoroSpace/pig/codingagent/modes/interactive/theme"
	"github.com/ZoroSpace/pig/tui"
)

// FirstTimeSetupResult carries the dialog outcome (pi:
// FirstTimeSetupResult).
type FirstTimeSetupResult struct {
	Theme          theme.TerminalTheme
	ShareAnalytics bool
}

// FirstTimeSetupOptions wire the dialog (pi: FirstTimeSetupOptions).
type FirstTimeSetupOptions struct {
	DetectedTheme  theme.TerminalTheme
	OnThemePreview func(themeName theme.TerminalTheme)
	OnSubmit       func(result FirstTimeSetupResult)
	OnCancel       func()
}

type firstTimeSetupThemeOption struct {
	value theme.TerminalTheme
	label string
}

var firstTimeSetupThemeOptions = []firstTimeSetupThemeOption{
	{value: "dark", label: "Dark"},
	{value: "light", label: "Light"},
}

type firstTimeSetupAnalyticsOption struct {
	value bool
	label string
}

var firstTimeSetupAnalyticsOptions = []firstTimeSetupAnalyticsOption{
	{value: true, label: "Share anonymous usage data"},
	{value: false, label: "Don't share"},
}

var setupLogoLines = []string{"██████", "██  ██", "████  ██", "██    ██"}

// FirstTimeSetupComponent renders the setup dialog.
type FirstTimeSetupComponent struct {
	tui.Container

	step           string // "theme" | "analytics"
	themeIndex     int
	analyticsIndex int
	options        FirstTimeSetupOptions
}

// NewFirstTimeSetupComponent builds the dialog (pi: constructor).
func NewFirstTimeSetupComponent(options FirstTimeSetupOptions) *FirstTimeSetupComponent {
	c := &FirstTimeSetupComponent{
		step:    "theme",
		options: options,
	}
	c.themeIndex = 0
	for i, option := range firstTimeSetupThemeOptions {
		if option.value == options.DetectedTheme {
			if i > c.themeIndex {
				c.themeIndex = i
			}
			break
		}
	}
	c.update()
	return c
}

// update rebuilds the whole dialog on every change so theme previews
// recolor all text (pi: update).
func (c *FirstTimeSetupComponent) update() {
	c.Clear()
	currentTheme := theme.Current()
	c.AddChild(NewDynamicBorder(nil))
	c.AddChild(tui.NewSpacer(1))
	c.AddChild(tui.NewText(currentTheme.Fg("accent", joinLines(setupLogoLines)), 1, 0))
	c.AddChild(tui.NewSpacer(1))
	c.AddChild(tui.NewText(currentTheme.Fg("accent", currentTheme.Bold("Welcome to "+codingagent.AppName+", the minimal coding agent.")), 1, 0))
	c.AddChild(tui.NewSpacer(1))

	if c.step == "theme" {
		c.AddChild(tui.NewText(currentTheme.Fg("text", "Pick a theme."), 1, 0))
		c.AddChild(tui.NewText(currentTheme.Fg("muted", "Detected system appearance: "+string(c.options.DetectedTheme)), 1, 0))
		c.AddChild(tui.NewSpacer(1))
		labels := make([]string, len(firstTimeSetupThemeOptions))
		for i, option := range firstTimeSetupThemeOptions {
			labels[i] = option.label
		}
		c.addOptionList(labels, c.themeIndex)
	} else {
		c.AddChild(tui.NewText(currentTheme.Fg("text", "Opt-in to anonymous usage data sharing?"), 1, 0))
		c.AddChild(tui.NewText(currentTheme.Fg("muted",
			"Opting in stores a tracking identifier in settings.json and enables anonymous\nusage analytics. This helps us to better debug, reproduce, and resolve issues\nand bugs within Pig. You can observe what is shared using /privacy and make\nchanges anytime in settings.json.",
		), 1, 0))
		c.AddChild(tui.NewSpacer(1))
		labels := make([]string, len(firstTimeSetupAnalyticsOptions))
		for i, option := range firstTimeSetupAnalyticsOptions {
			labels[i] = option.label
		}
		c.addOptionList(labels, c.analyticsIndex)
	}

	c.AddChild(tui.NewSpacer(1))
	confirmLabel := "finish"
	if c.step == "theme" {
		confirmLabel = "continue"
	}
	c.AddChild(tui.NewText(
		coretools.RawKeyHint("↑↓", "navigate")+
			"  "+
			coretools.KeyHint("tui.select.confirm", confirmLabel)+
			"  "+
			coretools.KeyHint("tui.select.cancel", "skip setup"),
		1, 0,
	))
	c.AddChild(tui.NewSpacer(1))
	c.AddChild(NewDynamicBorder(nil))
}

func (c *FirstTimeSetupComponent) addOptionList(labels []string, selectedIndex int) {
	currentTheme := theme.Current()
	for i, label := range labels {
		isSelected := i == selectedIndex
		prefix := "  "
		if isSelected {
			prefix = currentTheme.Fg("accent", "→ ")
		}
		styled := currentTheme.Fg("text", label)
		if isSelected {
			styled = currentTheme.Fg("accent", label)
		}
		c.AddChild(tui.NewText(prefix+styled, 1, 0))
	}
}

func (c *FirstTimeSetupComponent) moveSelection(delta int) {
	if c.step == "theme" {
		next := c.themeIndex + delta
		if next < 0 {
			next = 0
		}
		if next > len(firstTimeSetupThemeOptions)-1 {
			next = len(firstTimeSetupThemeOptions) - 1
		}
		if next != c.themeIndex {
			c.themeIndex = next
			if c.options.OnThemePreview != nil {
				c.options.OnThemePreview(firstTimeSetupThemeOptions[c.themeIndex].value)
			}
		}
	} else {
		next := c.analyticsIndex + delta
		if next < 0 {
			next = 0
		}
		if next > len(firstTimeSetupAnalyticsOptions)-1 {
			next = len(firstTimeSetupAnalyticsOptions) - 1
		}
		c.analyticsIndex = next
	}
	c.update()
}

// HandleInput processes keys (pi: handleInput).
func (c *FirstTimeSetupComponent) HandleInput(keyData string) {
	kb := tui.GetKeybindings()
	switch {
	case kb.Matches(keyData, "tui.select.up") || keyData == "k":
		c.moveSelection(-1)
	case kb.Matches(keyData, "tui.select.down") || keyData == "j":
		c.moveSelection(1)
	case kb.Matches(keyData, "tui.select.confirm") || keyData == "\n":
		if c.step == "theme" {
			c.step = "analytics"
			c.update()
		} else if c.options.OnSubmit != nil {
			c.options.OnSubmit(FirstTimeSetupResult{
				Theme:          firstTimeSetupThemeOptions[c.themeIndex].value,
				ShareAnalytics: firstTimeSetupAnalyticsOptions[c.analyticsIndex].value,
			})
		}
	case kb.Matches(keyData, "tui.select.cancel"):
		if c.options.OnCancel != nil {
			c.options.OnCancel()
		}
	}
}

func joinLines(lines []string) string {
	out := ""
	for i, line := range lines {
		if i > 0 {
			out += "\n"
		}
		out += line
	}
	return out
}
