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
