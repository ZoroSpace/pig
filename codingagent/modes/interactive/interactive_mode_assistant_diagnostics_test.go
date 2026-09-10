// interactive_mode_assistant_diagnostics_test.go (pi:
// test/interactive-mode-assistant-diagnostics.test.ts, v0.85.1) — transcript
// notices for Anthropic thinking blocks dropped by input transformations.
package interactive

import (
	"strings"
	"testing"

	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/codingagent/core"
	"github.com/ZoroSpace/pig/codingagent/utils"
)

func diagnosticsTestMessage() *types.AssistantMessage {
	return &types.AssistantMessage{
		Role:       types.RoleAssistant,
		Content:    []types.Content{types.TextContent{Kind: types.KindText, Text: "survived"}},
		API:        types.ApiAnthropicMessages,
		Provider:   "anthropic",
		Model:      "claude-fable-5-1",
		Usage:      types.Usage{Input: 1, Output: 1, CacheRead: 0, CacheWrite: 0, TotalTokens: 2},
		StopReason: types.StopReasonStop,
		Diagnostics: []types.AssistantMessageDiagnostic{
			{
				Type:      "anthropic_input_transformations",
				Timestamp: 1,
				Details: map[string]any{
					"transformations": []any{
						map[string]any{
							"type":   "thinking_dropped",
							"path":   "messages.2.content.0",
							"reason": "prefix_binding_mismatch",
						},
					},
				},
			},
		},
	}
}

func TestMaybeShowAssistantDiagnostics(t *testing.T) {
	initTestTheme(t)

	t.Run("shows Anthropic thinking drops when cache miss notices are enabled", func(t *testing.T) {
		session := createInteractiveTestSession(t, &fixtureResourceLoader{}, func(m *core.SettingsManager) {
			m.ApplyOverrides(core.Settings{"showCacheMissNotices": true})
		})
		m := newSeamTestMode(session, nil)

		m.maybeShowAssistantDiagnostics(diagnosticsTestMessage())
		output := utils.StripAnsi(strings.Join(m.chatContainer.Render(120), "\n"))
		if !strings.Contains(output, "Anthropic dropped thinking block: prefix_binding_mismatch at messages.2.content.0") {
			t.Fatalf("missing diagnostics notice:\n%s", output)
		}
	})

	t.Run("shows nothing when cache miss notices are disabled", func(t *testing.T) {
		session := createInteractiveTestSession(t, &fixtureResourceLoader{}, func(m *core.SettingsManager) {
			m.ApplyOverrides(core.Settings{"showCacheMissNotices": false})
		})
		m := newSeamTestMode(session, nil)

		m.maybeShowAssistantDiagnostics(diagnosticsTestMessage())
		if len(m.chatContainer.Children) != 0 {
			t.Fatalf("expected empty chat container, got %d children", len(m.chatContainer.Children))
		}
	})
}
