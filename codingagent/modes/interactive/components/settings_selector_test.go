// settings_selector_test.go (pi: test/settings-selector.test.ts —
// "cycles through fullscreen settings").
package components

import (
	"reflect"
	"testing"

	"github.com/ZoroSpace/pig/agent"
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
	config := &SettingsConfig{
		FullscreenExitOutput:    "transcript",
		FullscreenScrollbar:     "auto",
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
}
