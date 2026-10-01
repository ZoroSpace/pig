// settings_selector_test.go (pi: test/settings-selector.test.ts —
// "cycles through fullscreen settings").
package components

import (
	"reflect"
	"strings"
	"testing"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/codingagent/core"
	"github.com/ZoroSpace/pig/tui"
)

func TestSettingsSelectorCyclesThroughFullscreenSettings(t *testing.T) {
	initTestTheme(t)

	coreKeybindings := core.CreateKeybindingsManager("")
	tui.SetKeybindings(coreKeybindings.KeybindingsManager)
	defer tui.SetKeybindings(core.CreateKeybindingsManager("").KeybindingsManager)

	var exitOutputCalls []string
	var scrollbarCalls []string
	var copyOnSelectCalls []bool
	config := &SettingsConfig{
		FullscreenExitOutput:    "transcript",
		FullscreenScrollbar:     "auto",
		FullscreenCopyOnSelect:  true,
		Warnings:                core.WarningSettings{},
		DefaultModel:            "not set",
		AvailableDefaultModels:  nil,
		AvailableThinkingLevels: nil,
		ModelThinkingLevels:     map[string]agent.ThinkingLevel{},
		AvailableThemes:         nil,
	}
	callbacks := &SettingsCallbacks{
		OnFullscreenExitOutputChange: func(output core.FullscreenExitOutput) {
			exitOutputCalls = append(exitOutputCalls, string(output))
		},
		OnFullscreenScrollbarChange: func(mode core.ScrollViewScrollbar) {
			scrollbarCalls = append(scrollbarCalls, string(mode))
		},
		OnFullscreenCopyOnSelectChange: func(enabled bool) {
			copyOnSelectCalls = append(copyOnSelectCalls, enabled)
		},
	}

	cycle := func(label string, count int) {
		list := NewSettingsSelectorComponent(config, callbacks).GetSettingsList()
		for _, character := range label {
			list.HandleInput(string(character))
		}
		for i := 0; i < count; i++ {
			list.HandleInput("\r")
		}
	}

	cycle("Fullscreen exit output", 2)
	if want := []string{"resume-hint", "transcript"}; !reflect.DeepEqual(exitOutputCalls, want) {
		t.Fatalf("exit output calls = %v, want %v", exitOutputCalls, want)
	}
	cycle("Fullscreen scrollbar", 3)
	if want := []string{"always", "hidden", "auto"}; !reflect.DeepEqual(scrollbarCalls, want) {
		t.Fatalf("scrollbar calls = %v, want %v", scrollbarCalls, want)
	}
	cycle("Fullscreen copy on select", 2)
	if want := []bool{false, true}; !reflect.DeepEqual(copyOnSelectCalls, want) {
		t.Fatalf("copy-on-select calls = %v, want %v", copyOnSelectCalls, want)
	}
}

// pig-only item: cycles the CJK word-segmentation backend (bigram → sst →
// gse) and dispatches OnCJKSegmenterChange.
func TestSettingsSelectorCyclesCJKSegmenter(t *testing.T) {
	initTestTheme(t)

	coreKeybindings := core.CreateKeybindingsManager("")
	tui.SetKeybindings(coreKeybindings.KeybindingsManager)
	defer tui.SetKeybindings(core.CreateKeybindingsManager("").KeybindingsManager)

	var changes []string
	config := &SettingsConfig{
		Warnings:     core.WarningSettings{},
		CJKSegmenter: "bigram",
	}
	callbacks := &SettingsCallbacks{
		OnCJKSegmenterChange: func(mode string) {
			changes = append(changes, mode)
		},
	}

	list := NewSettingsSelectorComponent(config, callbacks).GetSettingsList()
	for _, character := range "CJK word segmentation" {
		list.HandleInput(string(character))
	}
	// The value column shows the decorated label (effect + memory), while
	// the callback receives the bare backend mode.
	output := stripVT(t, strings.Join(list.Render(120), "\n"))
	if !strings.Contains(output, "bigram (fixed 2-char groups, uses no RAM)") {
		t.Fatalf("expected decorated bigram label:\n%s", output)
	}
	list.HandleInput("\r")
	list.HandleInput("\r")
	if want := []string{"sst", "gse"}; !reflect.DeepEqual(changes, want) {
		t.Fatalf("cjk segmenter changes = %v, want %v", changes, want)
	}
}

// pig-only item: cycles the startup logo style (plain → gradient) and
// dispatches OnLogoStyleChange with the bare style value.
func TestSettingsSelectorCyclesLogoStyle(t *testing.T) {
	initTestTheme(t)

	coreKeybindings := core.CreateKeybindingsManager("")
	tui.SetKeybindings(coreKeybindings.KeybindingsManager)
	defer tui.SetKeybindings(core.CreateKeybindingsManager("").KeybindingsManager)

	var changes []string
	config := &SettingsConfig{
		Warnings:  core.WarningSettings{},
		LogoStyle: core.LogoStylePlain,
	}
	callbacks := &SettingsCallbacks{
		OnLogoStyleChange: func(style string) {
			changes = append(changes, style)
		},
	}

	list := NewSettingsSelectorComponent(config, callbacks).GetSettingsList()
	for _, character := range "Startup logo" {
		list.HandleInput(string(character))
	}
	output := stripVT(t, strings.Join(list.Render(120), "\n"))
	if !strings.Contains(output, "plain (theme text color)") {
		t.Fatalf("expected decorated plain label:\n%s", output)
	}
	list.HandleInput("\r")
	list.HandleInput("\r")
	if want := []string{core.LogoStyleGradient, core.LogoStylePlain}; !reflect.DeepEqual(changes, want) {
		t.Fatalf("logo style changes = %v, want %v", changes, want)
	}
}

// pi: "keeps the configured fixed theme marked while browsing" (v0.85.1).
func TestSettingsSelectorKeepsFixedThemeMarkedWhileBrowsing(t *testing.T) {
	initTestTheme(t)
	coreKeybindings := core.CreateKeybindingsManager("")
	tui.SetKeybindings(coreKeybindings.KeybindingsManager)

	config := &SettingsConfig{
		DefaultModel:            "not set",
		AvailableDefaultModels:  nil,
		ModelThinkingLevels:     map[string]agent.ThinkingLevel{},
		CurrentTheme:            "dark",
		AvailableThemes:         []string{"dark", "light"},
		Warnings:                core.WarningSettings{},
		AvailableThinkingLevels: nil,
	}
	callbacks := &SettingsCallbacks{}
	list := NewSettingsSelectorComponent(config, callbacks).GetSettingsList()

	list.SelectItem("theme")
	list.HandleInput("\r")
	output := stripVT(t, strings.Join(list.Render(120), "\n"))
	if !strings.Contains(output, "    Automatic") {
		t.Fatalf("expected padded Automatic row:\n%s", output)
	}
	if !strings.Contains(output, "→ ✓ dark") {
		t.Fatalf("expected marked dark theme:\n%s", output)
	}

	list.HandleInput("\x1b[B")
	output = stripVT(t, strings.Join(list.Render(120), "\n"))
	if !strings.Contains(output, "  ✓ dark") {
		t.Fatalf("expected dark to stay marked:\n%s", output)
	}
	if !strings.Contains(output, "→   light") {
		t.Fatalf("expected cursor on light:\n%s", output)
	}
}

// pi: "keeps the configured per-model thinking level marked while browsing"
// (v0.85.1).
func TestSettingsSelectorKeepsPerModelThinkingLevelMarkedWhileBrowsing(t *testing.T) {
	initTestTheme(t)
	coreKeybindings := core.CreateKeybindingsManager("")
	tui.SetKeybindings(coreKeybindings.KeybindingsManager)

	modelKey := "test-provider/thinking-model"
	fixtureModel := ai.Model{ID: "thinking-model", Name: "Thinking Model", Provider: types.ProviderID("test-provider"), Reasoning: true}
	config := &SettingsConfig{
		DefaultModel:            modelKey,
		AvailableDefaultModels:  []ai.Model{fixtureModel},
		AvailableThinkingLevels: nil,
		ThinkingLevel:           "high",
		ModelThinkingLevels:     map[string]agent.ThinkingLevel{modelKey: "medium"},
		Warnings:                core.WarningSettings{},
	}
	callbacks := &SettingsCallbacks{}
	list := NewSettingsSelectorComponent(config, callbacks).GetSettingsList()

	list.SelectItem("model-thinking")
	list.HandleInput("\r")
	list.HandleInput("\r")

	output := stripVT(t, strings.Join(list.Render(120), "\n"))
	if !strings.Contains(output, "→ ✓ medium") {
		t.Fatalf("expected marked medium override:\n%s", output)
	}
	if !strings.Contains(output, "    (clear override)") {
		t.Fatalf("expected padded clear override row:\n%s", output)
	}

	list.HandleInput("\x1b[B")
	output = stripVT(t, strings.Join(list.Render(120), "\n"))
	if !strings.Contains(output, "  ✓ medium") {
		t.Fatalf("expected medium to stay marked:\n%s", output)
	}
	if !strings.Contains(output, "→   high") {
		t.Fatalf("expected cursor on high:\n%s", output)
	}
}
