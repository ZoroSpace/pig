// interactive_mode_bug_hint_test.go — pi:
// test/interactive-mode-bug-report-hint.test.ts (v0.86.1). pi drives
// maybeSuggestBugReport via prototype reflection; pig calls the unexported
// method on a seam-constructed mode.
package interactive

import (
	"testing"

	"github.com/ZoroSpace/pig/ai/types"
)

func bugHintErrorMessage(message string) *types.AssistantMessage {
	return &types.AssistantMessage{
		Role:         types.RoleAssistant,
		Content:      []types.Content{},
		StopReason:   types.StopReasonError,
		ErrorMessage: message,
	}
}

func TestMaybeSuggestBugReport(t *testing.T) {
	initTestTheme(t)

	newHintContext := func(t *testing.T) *InteractiveMode {
		session := createInteractiveTestSession(t, &fixtureResourceLoader{}, nil)
		return newSeamTestMode(session, nil)
	}

	t.Run("does not suggest reports for retryable provider failures", func(t *testing.T) {
		m := newHintContext(t)
		failures := []string{
			"500 Internal Server Error",
			"502 Bad Gateway",
			"503 Service Unavailable",
			"504 Gateway Timeout",
			"429 Too Many Requests",
			"Provider overloaded",
			"Network connection lost",
			"Request timed out",
		}
		for _, failure := range failures {
			m.maybeSuggestBugReport(bugHintErrorMessage(failure))
		}
		if len(m.chatContainer.Children) != 0 {
			t.Fatalf("expected no bug hint for retryable failures, got %d children", len(m.chatContainer.Children))
		}
	})

	t.Run("does not suggest reports for cancellations", func(t *testing.T) {
		m := newHintContext(t)
		m.maybeSuggestBugReport(bugHintErrorMessage("This operation was aborted"))
		m.maybeSuggestBugReport(bugHintErrorMessage("Request cancelled"))
		m.maybeSuggestBugReport(&types.AssistantMessage{
			Role:       types.RoleAssistant,
			Content:    []types.Content{},
			StopReason: types.StopReasonAborted,
		})
		if len(m.chatContainer.Children) != 0 {
			t.Fatalf("expected no bug hint for cancellations, got %d children", len(m.chatContainer.Children))
		}
	})

	t.Run("suggests reports for unexpected errors", func(t *testing.T) {
		m := newHintContext(t)
		m.maybeSuggestBugReport(bugHintErrorMessage("Unexpected internal state"))
		if len(m.chatContainer.Children) == 0 {
			t.Fatal("expected a bug hint for an unexpected error")
		}
	})
}
