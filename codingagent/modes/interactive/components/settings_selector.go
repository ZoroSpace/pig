// settings_selector.go (pi: components/settings-selector.ts) — the /settings
// panel: SettingsSelectorComponent (a bordered SettingsList with value
// cycling, search, and nested submenus), the theme picker submenu
// (single/automatic mode), and the warnings submenu.
package components

import (
	"sort"
	"strconv"
	"strings"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/codingagent/core"
	coretools "github.com/ZoroSpace/pig/codingagent/core/tools"
	"github.com/ZoroSpace/pig/codingagent/modes/interactive/theme"
	"github.com/ZoroSpace/pig/tui"
	"github.com/samber/lo"
)

var modelPickerLayout = tui.SelectListLayoutOptions{
	MinPrimaryColumnWidth: 12,
	MaxPrimaryColumnWidth: 46,
}

// thinkingDescriptions maps each thinking level to its picker description
// (pi: THINKING_DESCRIPTIONS).
var thinkingDescriptions = map[agent.ThinkingLevel]string{
	ai.ModelThinkingOff:     "No reasoning",
	ai.ModelThinkingMinimal: "Very brief reasoning (~1k tokens)",
	ai.ModelThinkingLow:     "Light reasoning (~2k tokens)",
	ai.ModelThinkingMedium:  "Moderate reasoning (~8k tokens)",
	ai.ModelThinkingHigh:    "Deep reasoning (~16k tokens)",
	ai.ModelThinkingXHigh:   "Extra-high reasoning (~32k tokens)",
	ai.ModelThinkingMax:     "Maximum reasoning",
}

// defaultProjectTrustLabels pairs each DefaultProjectTrust with its picker
// label (pi: DEFAULT_PROJECT_TRUST_LABELS; the slice preserves pi's
// Object.values insertion order — Go maps are unordered).
var defaultProjectTrustLabels = []struct {
	Value core.DefaultProjectTrust
	Label string
}{
	{core.DefaultProjectTrustAsk, "Ask"},
	{core.DefaultProjectTrustAlways, "Always trust"},
	{core.DefaultProjectTrustNever, "Never trust"},
}

var defaultProjectTrustByLabel = func() map[string]core.DefaultProjectTrust {
	m := make(map[string]core.DefaultProjectTrust, len(defaultProjectTrustLabels))
	for _, pair := range defaultProjectTrustLabels {
		m[pair.Label] = pair.Value
	}
	return m
}()

// cjkSegmenterLabels pairs each CJK backend with its picker label (pig-only;
// same label-mapping pattern as defaultProjectTrustLabels): the parenthetical
// states the effect and the memory the backend will occupy (dictbench
// steady-state numbers, docs/feat/CJK-WORD-SEGMENTATION.md). The slice
// preserves cycle order.
var cjkSegmenterLabels = []struct {
	Value string
	Label string
}{
	{"bigram", "bigram (fixed 2-char groups, uses no RAM)"},
	{"sst", "sst (Chinese dictionary, uses ~12 MB RAM)"},
	{"gse", "gse (Chinese dictionary, uses ~206 MB RAM)"},
}

// cjkSegmenterLabel returns the picker label for a backend mode (unknown
// modes render as the bigram default, matching the resolver).
func cjkSegmenterLabel(mode string) string {
	for _, pair := range cjkSegmenterLabels {
		if pair.Value == mode {
			return pair.Label
		}
	}
	return cjkSegmenterLabels[0].Label
}

// SettingsConfig is the settings snapshot the selector renders (pi:
// SettingsConfig).
type SettingsConfig struct {
	AutoCompact                bool
	DefaultModel               string
	CurrentModel               *ai.Model
	AvailableDefaultModels     []ai.Model
	ShowImages                 bool
	ImageWidthCells            int64
	AutoResizeImages           bool
	BlockImages                bool
	EnableSkillCommands        bool
	SteeringMode               string // "all" | "one-at-a-time"
	FollowUpMode               string // "all" | "one-at-a-time"
	Transport                  ai.Transport
	HTTPIdleTimeoutMs          int64
	CacheWarmingMode           core.CacheWarmingMode
	ThinkingLevel              agent.ThinkingLevel
	AvailableThinkingLevels    []agent.ThinkingLevel
	ModelThinkingLevels        map[string]agent.ThinkingLevel
	CurrentTheme               string
	TerminalTheme              theme.TerminalTheme
	AvailableThemes            []string
	HideThinkingBlock          bool
	MermaidRenderingMode       core.MermaidRenderingMode
	ShowCacheMissNotices       bool
	CollapseChangelog          bool
	EnableInstallTelemetry     bool
	DoubleEscapeAction         string // "fork" | "tree" | "none"
	TreeFilterMode             string // "default" | "no-tools" | "user-only" | "labeled-only" | "all"
	ShowHardwareCursor         bool
	EditorPaddingX             int64
	OutputPad                  int // 0 | 1
	AutocompleteMaxVisible     int64
	QuietStartup               bool
	DefaultProjectTrust        core.DefaultProjectTrust
	ClearOnShrink              bool
	ShowTerminalProgress       bool
	TuiMode                    core.TuiMode
	FullscreenExitOutput       core.FullscreenExitOutput
	FullscreenScrollbar        core.ScrollViewScrollbar
	FullscreenCopyOnSelect     bool
	FullscreenWheelScrollLines int
	Warnings                   core.WarningSettings
	CJKSegmenter               string // pig-only: "bigram" | "sst" | "gse"
	ShowResourceStats          bool   // pig-only: footer resource segment (MEM now, more later)
}

// SettingsCallbacks carries the change handlers (pi: SettingsCallbacks).
// Optional members (pi: onThemePreview?) stay nil-able.
type SettingsCallbacks struct {
	OnAutoCompactChange                func(enabled bool)
	OnShowImagesChange                 func(enabled bool)
	OnImageWidthCellsChange            func(width int64)
	OnAutoResizeImagesChange           func(enabled bool)
	OnBlockImagesChange                func(blocked bool)
	OnEnableSkillCommandsChange        func(enabled bool)
	OnSteeringModeChange               func(mode string)
	OnFollowUpModeChange               func(mode string)
	OnTransportChange                  func(transport ai.Transport)
	OnHTTPIdleTimeoutMsChange          func(timeoutMs int64)
	OnCacheWarmingModeChange           func(mode core.CacheWarmingMode)
	OnModelThinkingLevelChange         func(provider string, modelID string, level agent.ThinkingLevel)
	OnModelThinkingLevelRemove         func(provider string, modelID string)
	OnThemeChange                      func(themeSetting string)
	OnThemePreview                     func(themeName string)
	OnHideThinkingBlockChange          func(hidden bool)
	OnMermaidRenderingModeChange       func(mode core.MermaidRenderingMode)
	OnShowCacheMissNoticesChange       func(shown bool)
	OnCollapseChangelogChange          func(collapsed bool)
	OnEnableInstallTelemetryChange     func(enabled bool)
	OnDoubleEscapeActionChange         func(action string)
	OnTreeFilterModeChange             func(mode string)
	OnShowHardwareCursorChange         func(enabled bool)
	OnEditorPaddingXChange             func(padding int64)
	OnOutputPadChange                  func(padding int)
	OnAutocompleteMaxVisibleChange     func(maxVisible int64)
	OnQuietStartupChange               func(enabled bool)
	OnDefaultProjectTrustChange        func(defaultProjectTrust core.DefaultProjectTrust)
	OnClearOnShrinkChange              func(enabled bool)
	OnShowTerminalProgressChange       func(enabled bool)
	OnTuiModeChange                    func(mode core.TuiMode)
	OnFullscreenExitOutputChange       func(output core.FullscreenExitOutput)
	OnFullscreenScrollbarChange        func(mode core.ScrollViewScrollbar)
	OnFullscreenCopyOnSelectChange     func(enabled bool)
	OnFullscreenWheelScrollLinesChange func(lines int)
	OnWarningsChange                   func(warnings core.WarningSettings)
	OnCJKSegmenterChange               func(mode string)  // pig-only
	OnShowResourceStatsChange          func(enabled bool) // pig-only
	OnCancel                           func()
}

// =============================================================================
// WarningSettingsSubmenu
// =============================================================================

// WarningSettingsSubmenu is a submenu component for editing the warning
// toggles (pi: WarningSettingsSubmenu extends Container).
type WarningSettingsSubmenu struct {
	tui.Container

	settingsList *tui.SettingsList
	state        core.WarningSettings
}

// NewWarningSettingsSubmenu builds the warnings submenu (pi: constructor).
func NewWarningSettingsSubmenu(warnings core.WarningSettings, onChange func(warnings core.WarningSettings), onCancel func()) *WarningSettingsSubmenu {
	w := &WarningSettingsSubmenu{state: warnings}
	if warnings.AnthropicExtraUsage != nil {
		w.state.AnthropicExtraUsage = lo.ToPtr(*warnings.AnthropicExtraUsage)
	}

	items := []*tui.SettingItem{
		{
			Id:           "anthropic-extra-usage",
			Label:        "Anthropic extra usage",
			Description:  "Warn when Anthropic subscription auth may use paid extra usage",
			CurrentValue: boolSettingValue(w.state.AnthropicExtraUsage == nil || *w.state.AnthropicExtraUsage),
			Values:       []string{"true", "false"},
		},
	}

	w.settingsList = tui.NewSettingsList(
		items,
		minInt(len(items), 10),
		theme.GetSettingsListTheme(),
		func(id string, newValue string) {
			switch id {
			case "anthropic-extra-usage":
				w.state.AnthropicExtraUsage = lo.ToPtr(newValue == "true")
				onChange(w.state)
			}
		},
		onCancel,
		nil,
	)

	w.AddChild(w.settingsList)
	return w
}

// fullscreenWheelScrollValues builds the picker values: "auto" plus the
// preset lines and the current value, deduped, sorted (pi: the values array
// in the fullscreen-wheel-scroll-lines entry).
func fullscreenWheelScrollValues(current int) []string {
	seen := map[string]bool{"auto": true}
	values := []string{"auto"}
	add := func(lines int) {
		key := strconv.Itoa(lines)
		if !seen[key] {
			seen[key] = true
			values = append(values, key)
		}
	}
	for _, preset := range []int{1, 2, 3, 5, 10, current} {
		if preset != core.WheelScrollLinesAutoSetting {
			add(preset)
		}
	}
	numbers := values[1:]
	sort.Slice(numbers, func(i, j int) bool {
		a, _ := strconv.Atoi(numbers[i])
		b, _ := strconv.Atoi(numbers[j])
		return a < b
	})
	return append([]string{"auto"}, numbers...)
}

func boolSettingValue(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

// HandleInput implements InputHandler (pi: handleInput).
func (w *WarningSettingsSubmenu) HandleInput(data string) {
	w.settingsList.HandleInput(data)
}

// =============================================================================
// Model/thinking helpers
// =============================================================================

const clearOverrideValue = "__clear__"

func modelSettingKey(model ai.Model) string {
	return string(model.Provider) + "/" + model.ID
}

func modelDisplayLabel(model ai.Model) string {
	return model.ID + " [" + string(model.Provider) + "]"
}

func modelThinkingOverridesSummary(overrides map[string]agent.ThinkingLevel) string {
	count := len(overrides)
	if count == 0 {
		return "none"
	}
	return itoa(count) + " configured"
}

func modelItemLabel(model ai.Model) string {
	return model.ID + " " + theme.Current().Fg("muted", "["+string(model.Provider)+"]")
}

// =============================================================================
// Theme submenu support
// =============================================================================

func themeItems(availableThemes []string, currentTheme string) []tui.SelectItem {
	items := make([]tui.SelectItem, 0, len(availableThemes))
	for _, name := range availableThemes {
		// Mark the current theme in the label (v0.85.1) so it stays visible
		// while browsing.
		label := "  " + name
		if name == currentTheme {
			label = "✓ " + name
		}
		item := tui.SelectItem{Value: name, Label: label}
		if name == theme.SystemThemeName {
			item.Description = "Theme created from your terminal's colors"
		}
		items = append(items, item)
	}
	return items
}

const automaticThemeValue = "/"

// singleModeThemeItems orders the list: the system theme first, then
// automatic mode, then the remaining themes (pi v0.99.1).
func singleModeThemeItems(availableThemes []string, currentTheme string) []tui.SelectItem {
	items := themeItems(availableThemes, currentTheme)
	system := []tui.SelectItem{}
	rest := make([]tui.SelectItem, 0, len(items))
	for _, item := range items {
		if item.Value == theme.SystemThemeName {
			system = append(system, item)
		} else {
			rest = append(rest, item)
		}
	}
	out := append([]tui.SelectItem{}, system...)
	out = append(out, tui.SelectItem{
		Value:       automaticThemeValue,
		Label:       "  Automatic",
		Description: "Use separate themes for light and dark terminal appearance",
	})
	return append(out, rest...)
}

func preferredTheme(availableThemes []string, preferred string, fallback string) string {
	if preferred != "" && containsString(availableThemes, preferred) {
		return preferred
	}
	if containsString(availableThemes, fallback) {
		return fallback
	}
	if len(availableThemes) > 0 {
		return availableThemes[0]
	}
	return fallback
}

func containsString(list []string, v string) bool {
	for _, item := range list {
		if item == v {
			return true
		}
	}
	return false
}

func defaultAutomaticThemes(currentThemeSetting string, availableThemes []string) (lightTheme string, darkTheme string) {
	if autoTheme := theme.ParseAutoThemeSetting(currentThemeSetting); autoTheme != nil {
		return autoTheme.LightTheme, autoTheme.DarkTheme
	}

	currentFixedTheme := ""
	if !strings.Contains(currentThemeSetting, "/") {
		currentFixedTheme = currentThemeSetting
	}
	themeName := preferredTheme(availableThemes, currentFixedTheme, theme.SystemThemeName)
	return themeName, themeName
}

// =============================================================================
// ThemeSubmenu
// =============================================================================

// ThemeSubmenu is the theme picker submenu with single-theme and
// light/dark-automatic modes (pi: ThemeSubmenu extends Container).
type ThemeSubmenu struct {
	tui.Container

	inputComponent       tui.Component
	callbacks            *SettingsCallbacks
	availableThemes      []string
	terminalTheme        theme.TerminalTheme
	onDone               func(selectedValue string)
	originalThemeSetting string
	mode                 string // "single" | "automatic"
	singleTheme          string
	lightTheme           string
	darkTheme            string
}

// NewThemeSubmenu builds the theme submenu (pi: constructor). onDone
// receives the selected setting, or "" when cancelled (pi: onDone with
// undefined).
func NewThemeSubmenu(
	currentThemeSetting string,
	terminalTheme theme.TerminalTheme,
	availableThemes []string,
	callbacks *SettingsCallbacks,
	onDone func(selectedValue string),
) *ThemeSubmenu {
	t := &ThemeSubmenu{
		callbacks:            callbacks,
		availableThemes:      availableThemes,
		terminalTheme:        terminalTheme,
		onDone:               onDone,
		originalThemeSetting: currentThemeSetting,
	}
	autoTheme := theme.ParseAutoThemeSetting(currentThemeSetting)
	lightTheme, darkTheme := defaultAutomaticThemes(currentThemeSetting, availableThemes)
	fixedTheme := ""
	if autoTheme == nil && !strings.Contains(currentThemeSetting, "/") {
		fixedTheme = currentThemeSetting
	}
	if autoTheme != nil {
		t.mode = "automatic"
	} else {
		t.mode = "single"
	}
	t.lightTheme = lightTheme
	t.darkTheme = darkTheme

	preferred := fixedTheme
	if preferred == "" && autoTheme != nil {
		preferred = t.getActiveAutomaticTheme()
	}
	t.singleTheme = preferredTheme(availableThemes, preferred, theme.SystemThemeName)

	if t.mode == "automatic" {
		t.showAutomaticMenu()
	} else {
		t.showSingleMenu()
	}
	return t
}

// HandleInput implements InputHandler (pi: handleInput).
func (t *ThemeSubmenu) HandleInput(data string) {
	if t.inputComponent != nil {
		if handler, ok := t.inputComponent.(tui.InputHandler); ok {
			handler.HandleInput(data)
		}
	}
}

func (t *ThemeSubmenu) setContent(renderComponent tui.Component, inputComponent tui.Component) {
	t.Clear()
	t.AddChild(renderComponent)
	t.inputComponent = inputComponent
}

func (t *ThemeSubmenu) showSingleMenu() {
	t.mode = "single"
	menu := NewSelectSubmenu(
		"Theme",
		"Select a theme, or choose Automatic to follow terminal appearance.",
		singleModeThemeItems(t.availableThemes, t.singleTheme),
		t.singleTheme,
		func(value string) {
			if value == automaticThemeValue {
				t.mode = "automatic"
				if t.callbacks.OnThemePreview != nil {
					t.callbacks.OnThemePreview(t.getThemeSetting())
				}
				t.showAutomaticMenu()
				return
			}

			t.singleTheme = value
			t.apply(value)
		},
		func() { t.cancel() },
		func(value string) {
			if t.callbacks.OnThemePreview != nil {
				if value == automaticThemeValue {
					t.callbacks.OnThemePreview(t.getAutomaticThemeSetting())
				} else {
					t.callbacks.OnThemePreview(value)
				}
			}
		},
		nil,
	)
	t.setContent(menu, menu)
}

func (t *ThemeSubmenu) showAutomaticMenu() {
	t.mode = "automatic"
	currentTheme := theme.Current()
	content := &tui.Container{}
	content.AddChild(tui.NewText(currentTheme.Bold(currentTheme.Fg("accent", "Automatic Theme")), 0, 0))
	content.AddChild(tui.NewSpacer(1))
	content.AddChild(tui.NewText(currentTheme.Fg("muted", "Choose themes for terminal light and dark appearance."), 0, 0))
	content.AddChild(tui.NewText(currentTheme.Fg("muted", "Light/dark detection requires terminal support."), 0, 0))
	content.AddChild(tui.NewSpacer(1))

	lightTheme := t.lightTheme
	darkTheme := t.darkTheme
	items := []*tui.SettingItem{
		{
			Id:           "light-theme",
			Label:        "Light theme",
			Description:  "Theme to use in automatic mode when the terminal is light",
			CurrentValue: lightTheme,
			Submenu: func(currentValue string, done func(selectedValue string, options *tui.SettingsSubmenuDoneOptions)) tui.Component {
				return t.createThemeSelect(
					"Light Theme",
					"Select the theme to use for light terminal appearance",
					currentValue,
					done,
					func(value string) {
						t.lightTheme = value
						if t.callbacks.OnThemePreview != nil {
							t.callbacks.OnThemePreview(t.getThemeSetting())
						}
						done(value, nil)
					},
				)
			},
		},
		{
			Id:           "dark-theme",
			Label:        "Dark theme",
			Description:  "Theme to use in automatic mode when the terminal is dark",
			CurrentValue: darkTheme,
			Submenu: func(currentValue string, done func(selectedValue string, options *tui.SettingsSubmenuDoneOptions)) tui.Component {
				return t.createThemeSelect(
					"Dark Theme",
					"Select the theme to use for dark terminal appearance",
					currentValue,
					done,
					func(value string) {
						t.darkTheme = value
						if t.callbacks.OnThemePreview != nil {
							t.callbacks.OnThemePreview(t.getThemeSetting())
						}
						done(value, nil)
					},
				)
			},
		},
		{
			Id:           "apply",
			Label:        "Apply",
			Description:  "Save and go back",
			CurrentValue: "save and go back",
			Values:       []string{"save and go back"},
		},
		{
			Id:           "single-mode",
			Label:        "Change mode",
			Description:  "Switch to one theme for light and dark",
			CurrentValue: "switch to single theme",
			Values:       []string{"switch to single theme"},
		},
	}

	settingsList := tui.NewSettingsList(
		items,
		minInt(len(items), 10),
		theme.GetSettingsListTheme(),
		func(id string, newValue string) {
			switch id {
			case "single-mode":
				t.mode = "single"
				t.singleTheme = t.getActiveAutomaticTheme()
				if t.callbacks.OnThemePreview != nil {
					t.callbacks.OnThemePreview(t.singleTheme)
				}
				t.showSingleMenu()
			case "apply":
				t.apply(t.getAutomaticThemeSetting())
			}
		},
		func() { t.cancel() },
		nil,
	)
	content.AddChild(settingsList)
	t.setContent(content, settingsList)
}

func (t *ThemeSubmenu) createThemeSelect(
	title string,
	description string,
	currentValue string,
	done func(selectedValue string, options *tui.SettingsSubmenuDoneOptions),
	onSelect func(value string),
) *SelectSubmenu {
	return NewSelectSubmenu(
		title,
		description,
		themeItems(t.availableThemes, currentValue),
		currentValue,
		onSelect,
		func() {
			if t.callbacks.OnThemePreview != nil {
				t.callbacks.OnThemePreview(t.getThemeSetting())
			}
			done("", nil)
		},
		func(value string) {
			if t.callbacks.OnThemePreview != nil {
				t.callbacks.OnThemePreview(value)
			}
		},
		nil,
	)
}

func (t *ThemeSubmenu) getThemeSetting() string {
	if t.mode == "automatic" {
		return t.getAutomaticThemeSetting()
	}
	return t.singleTheme
}

func (t *ThemeSubmenu) getActiveAutomaticTheme() string {
	if t.terminalTheme == theme.TerminalThemeLight {
		return t.lightTheme
	}
	return t.darkTheme
}

func (t *ThemeSubmenu) getAutomaticThemeSetting() string {
	return t.lightTheme + "/" + t.darkTheme
}

func (t *ThemeSubmenu) apply(themeSetting string) {
	t.onDone(themeSetting)
}

func (t *ThemeSubmenu) cancel() {
	if t.callbacks.OnThemePreview != nil {
		t.callbacks.OnThemePreview(t.originalThemeSetting)
	}
	t.onDone("")
}

// =============================================================================
// Main settings selector component
// =============================================================================

// capabilitiesHaveImages mirrors pi's getCapabilities().images truthy check
// (tui's equivalent helper is unexported).
func capabilitiesHaveImages() bool {
	switch v := tui.GetCapabilities().Images.(type) {
	case bool:
		return v
	case string:
		return v != ""
	default:
		return false
	}
}

// SettingsSelectorComponent is the main settings selector component (pi:
// SettingsSelectorComponent extends Container).
type SettingsSelectorComponent struct {
	tui.Container

	settingsList *tui.SettingsList
}

// NewSettingsSelectorComponent builds the settings panel (pi: constructor).
func NewSettingsSelectorComponent(config *SettingsConfig, callbacks *SettingsCallbacks) *SettingsSelectorComponent {
	s := &SettingsSelectorComponent{}

	supportsImages := capabilitiesHaveImages()
	followUpKey := coretools.KeyDisplayText(core.AppKeybindingMessageFollowUp)
	cycleThinkingKey := coretools.KeyDisplayText(core.AppKeybindingThinkingCycle)
	currentWarnings := config.Warnings
	if config.Warnings.AnthropicExtraUsage != nil {
		currentWarnings.AnthropicExtraUsage = lo.ToPtr(*config.Warnings.AnthropicExtraUsage)
	}
	currentModelThinkingLevels := map[string]agent.ThinkingLevel{}
	for k, v := range config.ModelThinkingLevels {
		currentModelThinkingLevels[k] = v
	}
	defaultModelByValue := map[string]ai.Model{}
	for _, model := range config.AvailableDefaultModels {
		defaultModelByValue[modelSettingKey(model)] = model
	}
	currentDefaultModelKey := config.DefaultModel
	if _, ok := defaultModelByValue[config.DefaultModel]; !ok {
		currentDefaultModelKey = ""
	}
	currentModelKey := ""
	if config.CurrentModel != nil {
		currentModelKey = modelSettingKey(*config.CurrentModel)
	}

	// pi builds a SettingItem[] literal then splices the image/editor items
	// in at computed indexes; the order below is the same result.
	items := []*tui.SettingItem{
		{
			Id:           "autocompact",
			Label:        "Auto-compact",
			Description:  "Automatically compact context when it gets too large",
			CurrentValue: boolSettingValue(config.AutoCompact),
			Values:       []string{"true", "false"},
		},
		{
			Id:           "steering-mode",
			Label:        "Steering mode",
			Description:  "Enter while streaming queues steering messages. 'one-at-a-time': deliver one, wait for response. 'all': deliver all at once.",
			CurrentValue: config.SteeringMode,
			Values:       []string{"one-at-a-time", "all"},
		},
		{
			Id:           "follow-up-mode",
			Label:        "Follow-up mode",
			Description:  followUpKey + " queues follow-up messages until agent stops. 'one-at-a-time': deliver one, wait for response. 'all': deliver all at once.",
			CurrentValue: config.FollowUpMode,
			Values:       []string{"one-at-a-time", "all"},
		},
		{
			Id:           "transport",
			Label:        "Transport",
			Description:  "Preferred transport for providers that support multiple transports",
			CurrentValue: string(config.Transport),
			Values:       []string{"sse", "websocket", "websocket-cached", "auto"},
		},
		{
			Id:           "http-idle-timeout",
			Label:        "HTTP idle timeout",
			Description:  "Maximum idle gap while waiting for HTTP headers or body chunks. Disable for local models that pause longer than five minutes.",
			CurrentValue: core.FormatHTTPIdleTimeoutMs(config.HTTPIdleTimeoutMs),
		},
		{
			Id:           "cache-warming-mode",
			Label:        "Cache warming",
			Description:  "off; streaming while the agent runs; idle also between runs while continuation stays profitable",
			CurrentValue: string(config.CacheWarmingMode),
			Values:       cacheWarmingModeStrings(),
		},
		{
			Id:           "hide-thinking",
			Label:        "Hide thinking",
			Description:  "Hide thinking blocks in assistant responses",
			CurrentValue: boolSettingValue(config.HideThinkingBlock),
			Values:       []string{"true", "false"},
		},
		{
			Id:           "mermaid-rendering",
			Label:        "Mermaid diagrams",
			Description:  "Render Mermaid code blocks as Unicode diagrams",
			CurrentValue: string(config.MermaidRenderingMode),
			Values:       []string{"off", "final", "streaming"},
		},
		{
			Id:           "cache-miss-notices",
			Label:        "Cache miss notices",
			Description:  "Show transcript notices for cache costs and provider recovery diagnostics",
			CurrentValue: boolSettingValue(config.ShowCacheMissNotices),
			Values:       []string{"true", "false"},
		},
		{
			Id:           "collapse-changelog",
			Label:        "Collapse changelog",
			Description:  "Show condensed changelog after updates",
			CurrentValue: boolSettingValue(config.CollapseChangelog),
			Values:       []string{"true", "false"},
		},
		{
			Id:           "quiet-startup",
			Label:        "Quiet startup",
			Description:  "Disable verbose printing at startup",
			CurrentValue: boolSettingValue(config.QuietStartup),
			Values:       []string{"true", "false"},
		},
		{
			Id:           "install-telemetry",
			Label:        "Install telemetry",
			Description:  "Send an anonymous version/update ping after changelog-detected updates",
			CurrentValue: boolSettingValue(config.EnableInstallTelemetry),
			Values:       []string{"true", "false"},
		},
		{
			Id:           "default-project-trust",
			Label:        "Default project trust",
			Description:  "Fallback behavior when no extension or saved trust decision decides project trust",
			CurrentValue: defaultProjectTrustLabel(config.DefaultProjectTrust),
		},
		{
			Id:           "double-escape-action",
			Label:        "Double-escape action",
			Description:  "Action when pressing Escape twice with empty editor",
			CurrentValue: config.DoubleEscapeAction,
			Values:       []string{"tree", "fork", "none"},
		},
		{
			Id:           "tree-filter-mode",
			Label:        "Tree filter mode",
			Description:  "Default filter when opening /tree",
			CurrentValue: config.TreeFilterMode,
			Values:       []string{"default", "no-tools", "user-only", "labeled-only", "all"},
		},
		{
			Id:           "warnings",
			Label:        "Warnings",
			Description:  "Enable or disable individual warnings",
			CurrentValue: "configure",
			Submenu: func(_currentValue string, done func(selectedValue string, options *tui.SettingsSubmenuDoneOptions)) tui.Component {
				return NewWarningSettingsSubmenu(
					currentWarnings,
					func(warnings core.WarningSettings) {
						currentWarnings = warnings
						if callbacks.OnWarningsChange != nil {
							callbacks.OnWarningsChange(warnings)
						}
					},
					func() { done("", nil) },
				)
			},
		},
		{
			Id:           "model-thinking",
			Label:        "Default thinking level per model",
			Description:  "Override the default thinking level for specific models. " + cycleThinkingKey + " cycles in-session.",
			CurrentValue: modelThinkingOverridesSummary(currentModelThinkingLevels),
			Submenu: func(_currentValue string, done func(selectedValue string, options *tui.SettingsSubmenuDoneOptions)) tui.Component {
				steps := []SteppedSubmenuStep{
					{
						Key:         "model",
						Title:       "Per-Model Thinking Level",
						Description: "Select a model to configure",
						Options: func(context map[string]string) []tui.SelectItem {
							sorted := append([]ai.Model(nil), config.AvailableDefaultModels...)
							sort.SliceStable(sorted, func(i, j int) bool {
								aKey := modelSettingKey(sorted[i])
								bKey := modelSettingKey(sorted[j])
								if aKey == currentModelKey && bKey != currentModelKey {
									return true
								}
								if bKey == currentModelKey && aKey != currentModelKey {
									return false
								}
								if aKey == currentDefaultModelKey && bKey != currentDefaultModelKey {
									return true
								}
								if bKey == currentDefaultModelKey && aKey != currentDefaultModelKey {
									return false
								}
								return string(sorted[i].Provider) < string(sorted[j].Provider)
							})
							items := make([]tui.SelectItem, 0, len(sorted)+1)
							for _, model := range sorted {
								key := modelSettingKey(model)
								override := currentModelThinkingLevels[key]
								item := tui.SelectItem{
									Value: key,
									Label: modelItemLabel(model),
								}
								if override != "" {
									item.Description = string(override)
								}
								items = append(items, item)
							}
							if len(items) == 0 {
								items = append(items, tui.SelectItem{
									Value:       "__none__",
									Label:       "No models available",
									Description: "Log in to a provider or configure an API key first",
								})
							}
							return items
						},
						Preselect: func(context map[string]string) string {
							if currentModelKey != "" {
								return currentModelKey
							}
							return currentDefaultModelKey
						},
						Searchable: true,
						Layout:     &modelPickerLayout,
					},
					{
						Key: "level",
						TitleFunc: func(context map[string]string) string {
							m, ok := defaultModelByValue[context["model"]]
							if !ok {
								return "Thinking Level for " + context["model"]
							}
							return "Thinking Level for " + modelDisplayLabel(m)
						},
						Description: "Select default thinking level for this model",
						Options: func(context map[string]string) []tui.SelectItem {
							model, ok := defaultModelByValue[context["model"]]
							if !ok {
								return nil
							}
							var levels []agent.ThinkingLevel
							if model.Reasoning {
								levels = ai.GetSupportedThinkingLevels(&model)
							} else {
								levels = []agent.ThinkingLevel{ai.ModelThinkingOff}
							}
							activeLevel := currentModelThinkingLevels[context["model"]]
							items := make([]tui.SelectItem, 0, len(levels)+1)
							for _, level := range levels {
								label := "  " + string(level)
								if level == activeLevel {
									label = "✓ " + string(level)
								}
								items = append(items, tui.SelectItem{
									Value:       string(level),
									Label:       label,
									Description: thinkingDescriptions[level],
								})
							}
							if _, has := currentModelThinkingLevels[context["model"]]; has {
								items = append(items, tui.SelectItem{
									Value:       clearOverrideValue,
									Label:       "  (clear override)",
									Description: "Revert to global default (" + string(config.ThinkingLevel) + ")",
								})
							}
							return items
						},
						Preselect: func(context map[string]string) string {
							return string(currentModelThinkingLevels[context["model"]])
						},
					},
				}

				return NewSteppedSubmenu(
					steps,
					func(selections map[string]string) {
						model, ok := defaultModelByValue[selections["model"]]
						if !ok {
							return
						}
						if selections["level"] == clearOverrideValue {
							if callbacks.OnModelThinkingLevelRemove != nil {
								callbacks.OnModelThinkingLevelRemove(string(model.Provider), model.ID)
							}
							delete(currentModelThinkingLevels, selections["model"])
						} else {
							if callbacks.OnModelThinkingLevelChange != nil {
								callbacks.OnModelThinkingLevelChange(string(model.Provider), model.ID, agent.ThinkingLevel(selections["level"]))
							}
							currentModelThinkingLevels[selections["model"]] = agent.ThinkingLevel(selections["level"])
						}
					},
					func() {
						done(modelThinkingOverridesSummary(currentModelThinkingLevels), nil)
					},
					&SteppedSubmenuOptions{Loop: true},
				)
			},
		},
		{
			Id:           "tui-mode",
			Label:        "TUI mode",
			Description:  "Interface layout; fullscreen mode is experimental",
			CurrentValue: string(config.TuiMode),
			Values:       []string{"regular", "fullscreen"},
		},
		{
			Id:           "fullscreen-exit-output",
			Label:        "Fullscreen exit output",
			Description:  "Print the transcript or only a session resume hint when exiting fullscreen mode",
			CurrentValue: string(config.FullscreenExitOutput),
			Values:       []string{"transcript", "resume-hint"},
		},
		{
			Id:           "fullscreen-scrollbar",
			Label:        "Fullscreen scrollbar",
			Description:  "Scrollbar behavior in fullscreen mode; has no effect in regular mode",
			CurrentValue: string(config.FullscreenScrollbar),
			Values:       []string{"auto", "always", "hidden"},
		},
		{
			Id:           "fullscreen-copy-on-select",
			Label:        "Fullscreen copy on select",
			Description:  "Automatically copy selected text in fullscreen mode; disable to copy selections with Ctrl+X",
			CurrentValue: boolSettingValue(config.FullscreenCopyOnSelect),
			Values:       []string{"true", "false"},
		},
		{
			Id:          "fullscreen-wheel-scroll-lines",
			Label:       "Fullscreen wheel scrolling",
			Description: "Lines per mouse-wheel event in fullscreen mode; 'auto' speeds up fast wheel spins where the terminal does not",
			CurrentValue: func() string {
				if config.FullscreenWheelScrollLines == core.WheelScrollLinesAutoSetting {
					return "auto"
				}
				return strconv.Itoa(config.FullscreenWheelScrollLines)
			}(),
			Values: fullscreenWheelScrollValues(config.FullscreenWheelScrollLines),
		},
		{
			Id:           "theme",
			Label:        "Theme",
			Description:  "Color theme for the interface",
			CurrentValue: config.CurrentTheme,
			Submenu: func(currentValue string, done func(selectedValue string, options *tui.SettingsSubmenuDoneOptions)) tui.Component {
				return NewThemeSubmenu(currentValue, config.TerminalTheme, config.AvailableThemes, callbacks, func(selectedValue string) {
					done(selectedValue, nil)
				})
			},
		},
	}

	// Only show image toggle if terminal supports it
	if supportsImages {
		// Insert after autocompact
		items = append(items, nil)
		copy(items[2:], items[1:])
		items[1] = &tui.SettingItem{
			Id:           "show-images",
			Label:        "Show images",
			Description:  "Render images inline in terminal",
			CurrentValue: boolSettingValue(config.ShowImages),
			Values:       []string{"true", "false"},
		}
		items = append(items, nil)
		copy(items[3:], items[2:])
		items[2] = &tui.SettingItem{
			Id:           "image-width-cells",
			Label:        "Image width",
			Description:  "Preferred inline image width in terminal cells",
			CurrentValue: itoa(int(config.ImageWidthCells)),
			Values:       []string{"60", "80", "120"},
		}
	}

	// Image auto-resize toggle (always available, affects both attached and
	// read images)
	autoResizeIndex := 1
	if supportsImages {
		autoResizeIndex = 3
	}
	items = append(items, nil)
	copy(items[autoResizeIndex+1:], items[autoResizeIndex:])
	items[autoResizeIndex] = &tui.SettingItem{
		Id:           "auto-resize-images",
		Label:        "Auto-resize images",
		Description:  "Resize large images to 2000x2000 max for better model compatibility",
		CurrentValue: boolSettingValue(config.AutoResizeImages),
		Values:       []string{"true", "false"},
	}

	// Block images toggle (always available, insert after auto-resize-images)
	autoResizeIndex = findItemIndex(items, "auto-resize-images")
	items = append(items, nil)
	copy(items[autoResizeIndex+1:], items[autoResizeIndex:])
	items[autoResizeIndex+1] = &tui.SettingItem{
		Id:           "block-images",
		Label:        "Block images",
		Description:  "Prevent images from being sent to LLM providers",
		CurrentValue: boolSettingValue(config.BlockImages),
		Values:       []string{"true", "false"},
	}

	// Skill commands toggle (insert after block-images)
	blockImagesIndex := findItemIndex(items, "block-images")
	items = append(items, nil)
	copy(items[blockImagesIndex+1:], items[blockImagesIndex:])
	items[blockImagesIndex+1] = &tui.SettingItem{
		Id:           "skill-commands",
		Label:        "Skill commands",
		Description:  "Register skills as /skill:name commands",
		CurrentValue: boolSettingValue(config.EnableSkillCommands),
		Values:       []string{"true", "false"},
	}

	// Hardware cursor toggle (insert after skill-commands)
	skillCommandsIndex := findItemIndex(items, "skill-commands")
	items = append(items, nil)
	copy(items[skillCommandsIndex+1:], items[skillCommandsIndex:])
	items[skillCommandsIndex+1] = &tui.SettingItem{
		Id:           "show-hardware-cursor",
		Label:        "Show hardware cursor",
		Description:  "Show the terminal cursor while still positioning it for IME support",
		CurrentValue: boolSettingValue(config.ShowHardwareCursor),
		Values:       []string{"true", "false"},
	}

	// Editor padding toggle (insert after show-hardware-cursor)
	hardwareCursorIndex := findItemIndex(items, "show-hardware-cursor")
	items = append(items, nil)
	copy(items[hardwareCursorIndex+1:], items[hardwareCursorIndex:])
	items[hardwareCursorIndex+1] = &tui.SettingItem{
		Id:           "editor-padding",
		Label:        "Editor padding",
		Description:  "Horizontal padding for input editor (0-3)",
		CurrentValue: itoa(int(config.EditorPaddingX)),
		Values:       []string{"0", "1", "2", "3"},
	}

	// Output padding toggle (insert after editor-padding)
	editorPaddingIndex := findItemIndex(items, "editor-padding")
	items = append(items, nil)
	copy(items[editorPaddingIndex+1:], items[editorPaddingIndex:])
	items[editorPaddingIndex+1] = &tui.SettingItem{
		Id:           "output-padding",
		Label:        "Output padding",
		Description:  "Horizontal padding for user messages, assistant messages, and thinking",
		CurrentValue: itoa(config.OutputPad),
		Values:       []string{"0", "1"},
	}

	// CJK word segmentation (pig-only; pi has no counterpart — Intl.Segmenter
	// ships inside the JS runtime). Inserted after output-padding, inside the
	// editor cluster: it controls editor word navigation over CJK text.
	cjkInsertIndex := findItemIndex(items, "output-padding")
	items = append(items, nil)
	copy(items[cjkInsertIndex+1:], items[cjkInsertIndex:])
	items[cjkInsertIndex+1] = &tui.SettingItem{
		Id:           "cjk-segmenter",
		Label:        "CJK word segmentation",
		Description:  "Editor word-navigation backend for CJK text. Applies from the next start.",
		CurrentValue: cjkSegmenterLabel(config.CJKSegmenter),
	}
	for _, pair := range cjkSegmenterLabels {
		items[cjkInsertIndex+1].Values = append(items[cjkInsertIndex+1].Values, pair.Label)
	}

	// Autocomplete max visible toggle (insert after output-padding)
	outputPaddingIndex := findItemIndex(items, "output-padding")
	items = append(items, nil)
	copy(items[outputPaddingIndex+1:], items[outputPaddingIndex:])
	items[outputPaddingIndex+1] = &tui.SettingItem{
		Id:           "autocomplete-max-visible",
		Label:        "Autocomplete max items",
		Description:  "Max visible items in autocomplete dropdown (3-20)",
		CurrentValue: itoa(int(config.AutocompleteMaxVisible)),
		Values:       []string{"3", "5", "7", "10", "15", "20"},
	}

	// Clear on shrink toggle (insert after autocomplete-max-visible)
	autocompleteIndex := findItemIndex(items, "autocomplete-max-visible")
	items = append(items, nil)
	copy(items[autocompleteIndex+1:], items[autocompleteIndex:])
	items[autocompleteIndex+1] = &tui.SettingItem{
		Id:           "clear-on-shrink",
		Label:        "Clear on shrink",
		Description:  "Clear empty rows when content shrinks (may cause flicker)",
		CurrentValue: boolSettingValue(config.ClearOnShrink),
		Values:       []string{"true", "false"},
	}

	// Terminal progress toggle (insert after clear-on-shrink)
	clearOnShrinkIndex := findItemIndex(items, "clear-on-shrink")
	items = append(items, nil)
	copy(items[clearOnShrinkIndex+1:], items[clearOnShrinkIndex:])
	items[clearOnShrinkIndex+1] = &tui.SettingItem{
		Id:           "terminal-progress",
		Label:        "Terminal progress",
		Description:  "Show OSC 9;4 progress indicators in the terminal tab bar",
		CurrentValue: boolSettingValue(config.ShowTerminalProgress),
		Values:       []string{"true", "false"},
	}

	// Resource stats toggle (pig-only; insert after terminal-progress).
	// Name is deliberately generic: the segment may grow more process
	// metrics (CPU, goroutines, …) beyond MEM.
	terminalProgressIndex := findItemIndex(items, "terminal-progress")
	items = append(items, nil)
	copy(items[terminalProgressIndex+1:], items[terminalProgressIndex:])
	items[terminalProgressIndex+1] = &tui.SettingItem{
		Id:           "resource-stats",
		Label:        "Resource stats",
		Description:  "Show process resource stats in the footer: memory; goroutine, FD and CPU alerts appear only on anomaly",
		CurrentValue: boolSettingValue(config.ShowResourceStats),
		Values:       []string{"true", "false"},
	}

	// HTTP idle timeout values (labels of the picker choices)
	for _, item := range items {
		if item.Id == "http-idle-timeout" {
			for _, choice := range core.HTTPIdleTimeoutChoices {
				item.Values = append(item.Values, choice.Label)
			}
		}
	}
	// Default project trust values (labels, pi's Object.values order)
	for _, item := range items {
		if item.Id == "default-project-trust" {
			for _, pair := range defaultProjectTrustLabels {
				item.Values = append(item.Values, pair.Label)
			}
		}
	}

	// Add borders
	s.AddChild(NewDynamicBorder(nil))

	s.settingsList = tui.NewSettingsList(
		items,
		10,
		theme.GetSettingsListTheme(),
		func(id string, newValue string) {
			switch id {
			case "autocompact":
				if callbacks.OnAutoCompactChange != nil {
					callbacks.OnAutoCompactChange(newValue == "true")
				}
			case "show-images":
				if callbacks.OnShowImagesChange != nil {
					callbacks.OnShowImagesChange(newValue == "true")
				}
			case "image-width-cells":
				if callbacks.OnImageWidthCellsChange != nil {
					callbacks.OnImageWidthCellsChange(parseNonNegativeInt(newValue))
				}
			case "auto-resize-images":
				if callbacks.OnAutoResizeImagesChange != nil {
					callbacks.OnAutoResizeImagesChange(newValue == "true")
				}
			case "block-images":
				if callbacks.OnBlockImagesChange != nil {
					callbacks.OnBlockImagesChange(newValue == "true")
				}
			case "skill-commands":
				if callbacks.OnEnableSkillCommandsChange != nil {
					callbacks.OnEnableSkillCommandsChange(newValue == "true")
				}
			case "steering-mode":
				if callbacks.OnSteeringModeChange != nil {
					callbacks.OnSteeringModeChange(newValue)
				}
			case "follow-up-mode":
				if callbacks.OnFollowUpModeChange != nil {
					callbacks.OnFollowUpModeChange(newValue)
				}
			case "transport":
				if callbacks.OnTransportChange != nil {
					callbacks.OnTransportChange(ai.Transport(newValue))
				}
			case "http-idle-timeout":
				for _, choice := range core.HTTPIdleTimeoutChoices {
					if choice.Label == newValue {
						if callbacks.OnHTTPIdleTimeoutMsChange != nil {
							callbacks.OnHTTPIdleTimeoutMsChange(choice.TimeoutMs)
						}
						break
					}
				}
			case "cache-warming-mode":
				if callbacks.OnCacheWarmingModeChange != nil {
					callbacks.OnCacheWarmingModeChange(core.CacheWarmingMode(newValue))
				}
			case "hide-thinking":
				if callbacks.OnHideThinkingBlockChange != nil {
					callbacks.OnHideThinkingBlockChange(newValue == "true")
				}
			case "mermaid-rendering":
				if callbacks.OnMermaidRenderingModeChange != nil {
					callbacks.OnMermaidRenderingModeChange(core.MermaidRenderingMode(newValue))
				}
			case "cache-miss-notices":
				if callbacks.OnShowCacheMissNoticesChange != nil {
					callbacks.OnShowCacheMissNoticesChange(newValue == "true")
				}
			case "collapse-changelog":
				if callbacks.OnCollapseChangelogChange != nil {
					callbacks.OnCollapseChangelogChange(newValue == "true")
				}
			case "quiet-startup":
				if callbacks.OnQuietStartupChange != nil {
					callbacks.OnQuietStartupChange(newValue == "true")
				}
			case "install-telemetry":
				if callbacks.OnEnableInstallTelemetryChange != nil {
					callbacks.OnEnableInstallTelemetryChange(newValue == "true")
				}
			case "default-project-trust":
				if defaultProjectTrust, ok := defaultProjectTrustByLabel[newValue]; ok {
					if callbacks.OnDefaultProjectTrustChange != nil {
						callbacks.OnDefaultProjectTrustChange(defaultProjectTrust)
					}
				}
			case "double-escape-action":
				if callbacks.OnDoubleEscapeActionChange != nil {
					callbacks.OnDoubleEscapeActionChange(newValue)
				}
			case "tree-filter-mode":
				if callbacks.OnTreeFilterModeChange != nil {
					callbacks.OnTreeFilterModeChange(newValue)
				}
			case "show-hardware-cursor":
				if callbacks.OnShowHardwareCursorChange != nil {
					callbacks.OnShowHardwareCursorChange(newValue == "true")
				}
			case "editor-padding":
				if callbacks.OnEditorPaddingXChange != nil {
					callbacks.OnEditorPaddingXChange(parseNonNegativeInt(newValue))
				}
			case "output-padding":
				if callbacks.OnOutputPadChange != nil {
					padding := 1
					if newValue == "0" {
						padding = 0
					}
					callbacks.OnOutputPadChange(padding)
				}
			case "autocomplete-max-visible":
				if callbacks.OnAutocompleteMaxVisibleChange != nil {
					callbacks.OnAutocompleteMaxVisibleChange(parseNonNegativeInt(newValue))
				}
			case "clear-on-shrink":
				if callbacks.OnClearOnShrinkChange != nil {
					callbacks.OnClearOnShrinkChange(newValue == "true")
				}
			case "terminal-progress":
				if callbacks.OnShowTerminalProgressChange != nil {
					callbacks.OnShowTerminalProgressChange(newValue == "true")
				}
			case "resource-stats":
				if callbacks.OnShowResourceStatsChange != nil {
					callbacks.OnShowResourceStatsChange(newValue == "true")
				}
			case "tui-mode":
				if callbacks.OnTuiModeChange != nil {
					callbacks.OnTuiModeChange(core.TuiMode(newValue))
				}
			case "fullscreen-exit-output":
				if callbacks.OnFullscreenExitOutputChange != nil {
					callbacks.OnFullscreenExitOutputChange(core.FullscreenExitOutput(newValue))
				}
			case "fullscreen-scrollbar":
				if callbacks.OnFullscreenScrollbarChange != nil {
					callbacks.OnFullscreenScrollbarChange(core.ScrollViewScrollbar(newValue))
				}
			case "fullscreen-copy-on-select":
				if callbacks.OnFullscreenCopyOnSelectChange != nil {
					callbacks.OnFullscreenCopyOnSelectChange(newValue == "true")
				}
			case "fullscreen-wheel-scroll-lines":
				if callbacks.OnFullscreenWheelScrollLinesChange != nil {
					if newValue == "auto" {
						callbacks.OnFullscreenWheelScrollLinesChange(core.WheelScrollLinesAutoSetting)
					} else {
						lines, err := strconv.Atoi(newValue)
						if err == nil {
							callbacks.OnFullscreenWheelScrollLinesChange(lines)
						}
					}
				}
			case "cjk-segmenter":
				if callbacks.OnCJKSegmenterChange != nil {
					// The picker cycles display labels; map back to the
					// backend mode (same pattern as default-project-trust).
					for _, pair := range cjkSegmenterLabels {
						if pair.Label == newValue {
							callbacks.OnCJKSegmenterChange(pair.Value)
							break
						}
					}
				}
			case "theme":
				if callbacks.OnThemeChange != nil {
					callbacks.OnThemeChange(newValue)
				}
			}
		},
		callbacks.OnCancel,
		&tui.SettingsListOptions{EnableSearch: true},
	)

	s.AddChild(s.settingsList)
	s.AddChild(NewDynamicBorder(nil))
	return s
}

func defaultProjectTrustLabel(value core.DefaultProjectTrust) string {
	for _, pair := range defaultProjectTrustLabels {
		if pair.Value == value {
			return pair.Label
		}
	}
	return string(value)
}

func findItemIndex(items []*tui.SettingItem, id string) int {
	for i, item := range items {
		if item.Id == id {
			return i
		}
	}
	return -1
}

// parseNonNegativeInt is pi's parseInt(newValue, 10) for value strings that
// are always numeric literals in the value lists.
func parseNonNegativeInt(value string) int64 {
	n := int64(0)
	for _, r := range value {
		if r < '0' || r > '9' {
			break
		}
		n = n*10 + int64(r-'0')
	}
	return n
}

// GetSettingsList returns the underlying settings list (pi:
// getSettingsList).
func (s *SettingsSelectorComponent) GetSettingsList() *tui.SettingsList {
	return s.settingsList
}

// cacheWarmingModeStrings converts the mode list for the value picker (pi:
// [...CACHE_WARMING_MODES]).
func cacheWarmingModeStrings() []string {
	out := make([]string, 0, len(core.CacheWarmingModes))
	for _, mode := range core.CacheWarmingModes {
		out = append(out, string(mode))
	}
	return out
}
