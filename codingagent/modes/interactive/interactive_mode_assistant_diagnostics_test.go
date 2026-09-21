// interactive_mode_assistant_diagnostics_test.go (pi:
// test/interactive-mode-assistant-diagnostics.test.ts, v0.86.0) — the
// Anthropic thinking-drop notice now reports a count with a previous-response
// comparison (maybeShowThinkingDropNotice replacing
// maybeShowAssistantDiagnostics).
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
						map[string]any{
							"type":   "thinking_dropped",
							"path":   "messages.5.content.0",
							"reason": "prefix_binding_mismatch",
						},
						map[string]any{
							"type":   "thinking_dropped",
							"path":   "messages.8.content.0",
							"reason": "prefix_binding_mismatch",
						},
					},
				},
			},
		},
	}
}

func TestMaybeShowThinkingDropNotice(t *testing.T) {
	initTestTheme(t)

	t.Run("shows Anthropic thinking drops when cache miss notices are enabled", func(t *testing.T) {
		session := createInteractiveTestSession(t, &fixtureResourceLoader{}, func(m *core.SettingsManager) {
			m.ApplyOverrides(core.Settings{"showCacheMissNotices": true})
		})
		m := newSeamTestMode(session, nil)

		m.maybeShowThinkingDropNotice(diagnosticsTestMessage())
		output := utils.StripAnsi(strings.Join(m.chatContainer.Render(120), "\n"))
		if !strings.Contains(output, "Anthropic dropped 3 thinking blocks (details in session)") {
			t.Fatalf("missing diagnostics notice:\n%s", output)
		}
	})

	t.Run("shows nothing when cache miss notices are disabled", func(t *testing.T) {
		session := createInteractiveTestSession(t, &fixtureResourceLoader{}, func(m *core.SettingsManager) {
			m.ApplyOverrides(core.Settings{"showCacheMissNotices": false})
		})
		m := newSeamTestMode(session, nil)

		m.maybeShowThinkingDropNotice(diagnosticsTestMessage())
		if len(m.chatContainer.Children) != 0 {
			t.Fatalf("expected empty chat container, got %d children", len(m.chatContainer.Children))
		}
	})

	// pi v0.86.0: an unchanged drop count versus the branch's previous
	// assistant response is silent.
	t.Run("does not repeat unchanged Anthropic thinking drops", func(t *testing.T) {
		session := createInteractiveTestSession(t, &fixtureResourceLoader{}, func(m *core.SettingsManager) {
			m.ApplyOverrides(core.Settings{"showCacheMissNotices": true})
		})
		m := newSeamTestMode(session, nil)

		previous := *diagnosticsTestMessage()
		previous.Timestamp = 1
		if _, err := session.SessionManager.AppendMessage(previous); err != nil {
			t.Fatalf("AppendMessage: %v", err)
		}

		current := diagnosticsTestMessage()
		current.Timestamp = 2
		m.maybeShowThinkingDropNotice(current)

		if len(m.chatContainer.Children) != 0 {
			t.Fatalf("expected empty chat container, got %d children", len(m.chatContainer.Children))
		}
	})
}
