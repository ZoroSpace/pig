// thinking_selector_test.go (pi: test/thinking-selector.test.ts, v0.85.1) —
// current-level marker and the configured save binding.
package components

import (
	"strings"
	"testing"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/codingagent/core"
	"github.com/ZoroSpace/pig/codingagent/utils"
	"github.com/ZoroSpace/pig/tui"
)

func TestThinkingSelectorKeepsCurrentLevelMarkedWhileBrowsing(t *testing.T) {
	initTestTheme(t)
	tui.SetKeybindings(core.CreateKeybindingsManager("").KeybindingsManager)

	selector := NewThinkingSelectorComponent(
		"medium",
		[]agent.ThinkingLevel{"medium", "high"},
		func(agent.ThinkingLevel) {},
		func() {},
		nil,
		"",
	)

	getLevelRow := func(level string) string {
		for _, line := range selector.GetSelectList().Render(80) {
			if strings.Contains(utils.StripAnsi(line), level) {
				return utils.StripAnsi(line)
			}
		}
		return ""
	}

	item := selector.GetSelectList().GetSelectedItem()
	if item == nil || item.Label != "✓ medium" {
		t.Fatalf("expected selected item label %q, got %v", "✓ medium", item)
	}
	if row := getLevelRow("medium"); !strings.HasPrefix(row, "→ ✓ medium") {
		t.Fatalf("expected row to start with %q, got %q", "→ ✓ medium", row)
	}
	selector.HandleInput("\x1b[B")
	if row := getLevelRow("medium"); !strings.HasPrefix(row, "  ✓ medium") {
		t.Fatalf("expected row to start with %q, got %q", "  ✓ medium", row)
	}
	if row := getLevelRow("high"); !strings.HasPrefix(row, "→   high") {
		t.Fatalf("expected row to start with %q, got %q", "→   high", row)
	}
}

func TestThinkingSelectorUsesConfiguredSaveBinding(t *testing.T) {
	initTestTheme(t)
	tui.SetKeybindings(core.NewKeybindingsManager(tui.KeybindingsConfig{
		"app.thinking.save": {"ctrl+r"},
	}, "").KeybindingsManager)

	var saved []agent.ThinkingLevel
	selector := NewThinkingSelectorComponent(
		"medium",
		[]agent.ThinkingLevel{"medium", "high"},
		func(agent.ThinkingLevel) {},
		func() {},
		func(level agent.ThinkingLevel) { saved = append(saved, level) },
		"",
	)

	output := utils.StripAnsi(strings.Join(selector.Render(80), "\n"))
	if !strings.Contains(output, "Ctrl+R to set as default") {
		t.Fatalf("expected configured binding in hints, got:\n%s", output)
	}
	selector.HandleInput("\x13") // ctrl+s — must NOT save with the override bound to ctrl+r
	if len(saved) != 0 {
		t.Fatal("ctrl+s should not trigger save")
	}
	selector.HandleInput("\x12") // ctrl+r
	if len(saved) != 1 || saved[0] != "medium" {
		t.Fatalf("expected ctrl+r to save medium, got %v", saved)
	}
}
