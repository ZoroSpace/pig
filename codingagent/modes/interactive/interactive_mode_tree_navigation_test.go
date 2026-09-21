// interactive_mode_tree_navigation_test.go (pi:
// test/interactive-mode-tree-navigation.test.ts, pi v0.86.0) — #9178/#9179:
// the post-dialog isCompacting recheck must fail closed before replacing
// another operation's UI.
package interactive

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ZoroSpace/pig/ai/types"
	core "github.com/ZoroSpace/pig/codingagent/core"
	"github.com/ZoroSpace/pig/codingagent/utils"
)

const treeBusyMessage = "Wait for the current compaction or tree navigation to finish before navigating the session tree."

// treeBusySession builds a session with a blocking session_before_compact
// handler so IsCompacting stays true until released (pi's fake
// session.isCompacting flag has no Go equivalent — the real flag rides the
// compaction cancel tokens).
func treeBusySession(t *testing.T) (session *core.AgentSession, originalLeafID string, targetID string, compactionReleased chan struct{}, handlerCtx chan context.Context) {
	t.Helper()
	compactionStarted := make(chan struct{})
	compactionReleased = make(chan struct{})
	handlerCtx = make(chan context.Context, 1)
	loader := &fixtureResourceLoader{}
	loader.extensions = &core.LoadExtensionsResult{
		Extensions: []*core.Extension{
			{
				Path: "tree-navigation-stub",
				Handlers: map[string][]core.ExtensionHandler{
					"session_before_compact": {func(ctx context.Context, extCtx *core.ExtensionContext, event core.ExtensionEvent) (any, error) {
						handlerCtx <- ctx
						compactionEvent, ok := event.(*core.SessionBeforeCompactEvent)
						if !ok || compactionEvent.Preparation == nil {
							return nil, nil
						}
						close(compactionStarted)
						<-compactionReleased
						return &core.SessionBeforeCompactResult{
							Compaction: &core.CompactionResult{
								Summary:          "summary",
								FirstKeptEntryID: compactionEvent.Preparation.FirstKeptEntryID,
								TokensBefore:     compactionEvent.Preparation.TokensBefore,
								Details:          map[string]any{},
							},
						}, nil
					}},
				},
			},
		},
		Runtime: core.CreateExtensionRuntime(),
	}
	session = createInteractiveTestSession(t, loader, func(m *core.SettingsManager) {
		m.ApplyOverrides(core.Settings{
			// Skip the summary dialog so the flow reaches the recheck
			// directly (the pi fake resolves the selector synchronously; a
			// real dialog needs the full UI stack).
			"branchSummary": core.Settings{"skipPrompt": true},
			// Make the small fixture session compactable (regressions_v086
			// harness precedent).
			"compaction": core.Settings{"keepRecentTokens": float64(1)},
		})
	})
	if err := session.BindExtensions(context.Background(), &core.ExtensionBindings{}); err != nil {
		t.Fatal(err)
	}

	appendMsg := func(text string) string {
		message := types.AssistantMessage{Role: types.RoleAssistant, Content: []types.Content{types.NewTextContent(text)}, Timestamp: time.Now().UnixMilli(), API: "anthropic-messages", Provider: "anthropic", Model: "mock", StopReason: types.StopReasonStop}
		id, _ := session.SessionManager.AppendMessage(message)
		return id
	}
	// A u/a/u/a turn sequence so the fixture session clears the compaction
	// cut-point floor (regressions_v086 harness shape).
	session.SessionManager.AppendMessage(types.UserMessage{Role: types.RoleUser, Content: types.UserContent{String: "first user"}, Timestamp: time.Now().UnixMilli()})
	targetID = appendMsg("first assistant")
	session.SessionManager.AppendMessage(types.UserMessage{Role: types.RoleUser, Content: types.UserContent{String: "second user"}, Timestamp: time.Now().UnixMilli()})
	originalLeaf := appendMsg("second assistant")
	session.Agent.SetMessages(session.SessionManager.BuildSessionContext().Messages)
	originalLeafID = originalLeaf

	go func() { _, _ = session.Compact(context.Background(), "") }()
	select {
	case <-compactionStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("compaction never started")
	}
	if !session.IsCompacting() {
		t.Fatal("expected isCompacting true")
	}
	originalLeafID = *session.SessionManager.GetLeafID()
	return session, originalLeafID, targetID, compactionReleased, handlerCtx
}

func TestTreeNavigationBusyFailClosed(t *testing.T) {
	initTestTheme(t)

	// pi: "preserves operation UI when choosing %s while busy" — the
	// compaction starts while the dialog is open; the recheck after the
	// dialogs must error out before touching the UI or the tree.
	session, originalLeafID, targetID, compactionReleased, _ := treeBusySession(t)
	defer close(compactionReleased)

	m := newSeamTestMode(session, &InteractiveModeOptions{})

	beforeChildren := len(m.chatContainer.Children)
	// Navigate to the first assistant entry (not the current leaf — selecting
	// the leaf is a no-op).
	m.handleTreeNavigation(targetID, func() {})

	output := utils.StripAnsi(strings.Join(m.chatContainer.Render(120), "\n"))
	if !strings.Contains(output, treeBusyMessage) {
		t.Fatalf("missing busy error:\n%s", output)
	}
	if len(m.chatContainer.Children) < beforeChildren {
		t.Fatal("chat container shrank")
	}
	// The active operation's UI (branch summary indicator) was never set up.
	if m.activeStatusIndicator != nil {
		t.Fatal("status indicator was created while busy")
	}
	if m.defaultEditor != nil && m.defaultEditor.OnEscape != nil {
		t.Fatal("escape handler was replaced while busy")
	}
	if got := session.SessionManager.GetLeafID(); got == nil || *got != originalLeafID {
		t.Fatalf("leaf changed while busy: %v", got)
	}
}

// pi's "allows navigation when compaction finishes while the dialog is open"
// case is not ported: the seam InteractiveMode cannot run the full
// post-navigation UI rebuild (renderCurrentSessionState needs the mounted
// editor/theme stack). The navigation-success path itself is covered by the
// core tree-navigation e2e suite; the regression this file locks is the
// fail-closed recheck above.
