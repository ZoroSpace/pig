// collapsible_message_components_test.go (pi: test/collapsible-message-
// components.test.ts, pi v0.86.0) — compaction/branch/skill summaries toggle
// on left click (MouseRegion added in v0.86.0).
package components

import (
	"strings"
	"testing"
	"time"

	"github.com/ZoroSpace/pig/agent/harness"
	core "github.com/ZoroSpace/pig/codingagent/core"
	"github.com/ZoroSpace/pig/codingagent/utils"
	"github.com/ZoroSpace/pig/tui"
)

const collapsibleWidth = 80

func renderCollapsibleText(t *testing.T, component tui.Component) string {
	t.Helper()
	return utils.StripAnsi(strings.Join(component.Render(collapsibleWidth), "\n"))
}

func clickCollapsibleRow(t *testing.T, component tui.Component, marker string) {
	t.Helper()
	lines := component.Render(collapsibleWidth)
	row := -1
	for i, line := range lines {
		if strings.Contains(utils.StripAnsi(line), marker) {
			row = i
			break
		}
	}
	if row < 0 {
		t.Fatalf("marker %q not found in render", marker)
	}
	event := &tui.TuiMouseEvent{
		Type:       tui.MouseEventTypeClick,
		Button:     tui.MouseButtonLeft,
		X:          2,
		Y:          row,
		ScreenX:    2,
		ScreenY:    row,
		Width:      collapsibleWidth,
		Height:     len(lines),
		ClickCount: intPtrCollapsible(1),
	}
	mouseHandler, ok := component.(tui.MouseHandler)
	if !ok {
		t.Fatalf("component does not handle mouse events")
	}
	result := mouseHandler.HandleMouse(event)
	if result == nil || !result.Handled {
		t.Fatalf("click on row %d was not handled", row)
	}
}

func TestCollapsibleMessageComponentsToggleOnClick(t *testing.T) {
	initTestTheme(t)

	t.Run("toggles a compaction summary when clicked", func(t *testing.T) {
		component := NewCompactionSummaryMessageComponent(&harness.CompactionSummaryMessage{
			Role:         harness.RoleCompactionSummary,
			Summary:      "compaction details",
			TokensBefore: 1234,
			Timestamp:    time.Now().UnixMilli(),
		}, nil)

		if strings.Contains(renderCollapsibleText(t, &component.Box), "compaction details") {
			t.Fatalf("expanded before click")
		}
		clickCollapsibleRow(t, &component.Box, "[compaction]")
		if !strings.Contains(renderCollapsibleText(t, &component.Box), "compaction details") {
			t.Fatalf("did not expand on click")
		}
		clickCollapsibleRow(t, &component.Box, "[compaction]")
		if strings.Contains(renderCollapsibleText(t, &component.Box), "compaction details") {
			t.Fatalf("did not collapse on second click")
		}
	})

	t.Run("toggles a branch summary when clicked", func(t *testing.T) {
		component := NewBranchSummaryMessageComponent(&harness.BranchSummaryMessage{
			Role:      harness.RoleBranchSummary,
			Summary:   "branch details",
			FromID:    loStrPtr("entry-1"),
			Timestamp: time.Now().UnixMilli(),
		}, nil)

		if strings.Contains(renderCollapsibleText(t, &component.Box), "branch details") {
			t.Fatalf("expanded before click")
		}
		clickCollapsibleRow(t, &component.Box, "[branch]")
		if !strings.Contains(renderCollapsibleText(t, &component.Box), "branch details") {
			t.Fatalf("did not expand on click")
		}
		clickCollapsibleRow(t, &component.Box, "[branch]")
		if strings.Contains(renderCollapsibleText(t, &component.Box), "branch details") {
			t.Fatalf("did not collapse on second click")
		}
	})

	t.Run("toggles a skill invocation when clicked", func(t *testing.T) {
		component := NewSkillInvocationMessageComponent(&core.ParsedSkillBlock{
			Name:     "example-skill",
			Location: "/tmp/example-skill.md",
			Content:  "skill details",
		}, nil)

		if strings.Contains(renderCollapsibleText(t, &component.Box), "skill details") {
			t.Fatalf("expanded before click")
		}
		clickCollapsibleRow(t, &component.Box, "[skill]")
		if !strings.Contains(renderCollapsibleText(t, &component.Box), "skill details") {
			t.Fatalf("did not expand on click")
		}
		clickCollapsibleRow(t, &component.Box, "[skill]")
		if strings.Contains(renderCollapsibleText(t, &component.Box), "skill details") {
			t.Fatalf("did not collapse on second click")
		}
	})
}

// intPtrCollapsible is a local pointer helper (intPtr already exists in
// tool_execution_test.go).
func intPtrCollapsible(v int) *int { return &v }

// loStrPtr is a string pointer helper for the fixtures.
func loStrPtr(v string) *string { return &v }
