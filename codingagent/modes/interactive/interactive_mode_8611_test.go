// interactive_mode_8611_test.go (pi:
// test/suite/regressions/8611-thinking-toggle-pending-bash-output.test.ts,
// v0.85.1) — toggling thinking visibility must not destroy live tool
// components; the updateThinkingBlockVisibility in-place path preserves the
// partial bash output.
package interactive

import (
	"strings"
	"testing"

	"github.com/samber/lo"

	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/codingagent/modes/interactive/components"
	"github.com/ZoroSpace/pig/codingagent/utils"
)

func TestThinkingTogglePreservesPartialBashOutput(t *testing.T) {
	initTestTheme(t)
	session := createInteractiveTestSession(t, &fixtureResourceLoader{}, nil)
	m := newSeamTestMode(session, nil)
	m.isInitialized = true

	component := components.NewToolExecutionComponent(
		"bash",
		"tool-8611",
		map[string]any{"command": "echo first; sleep 10"},
		components.ToolExecutionOptions{ShowImages: lo.ToPtr(false)},
		nil,
		m.ui,
		".",
	)
	component.MarkExecutionStarted()
	component.UpdateResult(components.ToolExecutionResult{
		Content: []types.Content{types.TextContent{Kind: types.KindText, Text: "first"}},
		IsError: false,
	}, true)
	m.chatContainer.AddChild(component)

	renderChat := func() string {
		return utils.StripAnsi(strings.Join(m.chatContainer.Render(120), "\n"))
	}
	if !strings.Contains(renderChat(), "first") {
		t.Fatal("expected partial bash output before toggle")
	}

	m.toggleThinkingBlockVisibility()

	// pi asserts setHideThinkingBlock was called with true; pig asserts the
	// setting flipped on the real settings manager.
	if m.settingsManager().GetHideThinkingBlock() != true {
		t.Fatal("expected hideThinkingBlock setting to be persisted")
	}
	if got := len(m.chatContainer.Children); got == 0 {
		t.Fatal("expected the live tool component to survive the toggle")
	}
	if !strings.Contains(renderChat(), "first") {
		t.Fatal("expected partial bash output after toggle")
	}
}
