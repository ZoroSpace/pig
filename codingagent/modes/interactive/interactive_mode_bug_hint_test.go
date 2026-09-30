// interactive_mode_bug_hint_test.go — pi:
// test/interactive-mode-bug-report-hint.test.ts (v0.86.1). pi drives
// maybeSuggestBugReport via prototype reflection; pig calls the unexported
// method on a seam-constructed mode.
package interactive

import (
	"strings"
	"testing"

	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/codingagent"
	"github.com/ZoroSpace/pig/codingagent/core"
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

	t.Run("identifies extensions with frames in a crash stack", func(t *testing.T) {
		got := FormatCrashExtensionHint([]string{"npm:pi-observational-memory"})
		want := "A stack frame came from loaded extension `npm:pi-observational-memory`, which may be involved. Try disabling it with `pig config`, or run `pig -ne` to confirm."
		if got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
		if got := FormatCrashExtensionHint(nil); got != "" {
			t.Fatalf("got %q, want empty", got)
		}
	})

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

	// pig-only: /bug is not shipped (docs/fix/2026-09-30-remove-bug-command.md);
	// the hints must point at the pig issue tracker + the local crash record.
	t.Run("hints point at the issue tracker, not the removed command", func(t *testing.T) {
		m := newHintContext(t)
		m.suggestBugReport()
		hints := renderAllChildren(m.chatContainer, 100)
		if !strings.Contains(hints, codingagent.IssuesURL) {
			t.Fatalf("bug hint missing issue tracker URL:\n%s", hints)
		}
		if strings.Contains(hints, "/bug") {
			t.Fatalf("bug hint still references the removed /bug command:\n%s", hints)
		}
		instructions := m.crashReportInstructions()
		if !strings.Contains(instructions, codingagent.IssuesURL) {
			t.Fatalf("crash instructions missing issue tracker URL:\n%s", instructions)
		}
		if !strings.Contains(instructions, core.CrashLogPath()) {
			t.Fatalf("crash instructions missing crash log path %q:\n%s", core.CrashLogPath(), instructions)
		}
		if strings.Contains(instructions, "/bug") {
			t.Fatalf("crash instructions still reference the removed /bug command:\n%s", instructions)
		}
	})
}
