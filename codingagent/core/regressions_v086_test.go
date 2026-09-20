// regressions_v086_test.go — v0.86.0 regression suites ported from pi:
//   - test/suite/regressions/9178-tree-during-compaction.test.ts (tree
//     navigation during manual compaction; the before_tree-blocking second
//     case rides the same IsCompacting guard).
package core

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/ai/types"
)

// v086TreeHarness assembles a session whose extension stub can block the
// compaction/tree hooks (pi: createHarness + extensionFactories).
func v086TreeHarness(t *testing.T, settings Settings, handlers map[string]ExtensionHandler) (*AgentSession, *SessionManager) {
	t.Helper()
	session, sessionManager, _ := createAgentTestSession(t, &createAgentTestSessionOptions{
		GetAPIKey:            true,
		OmitSeedSystemPrompt: true,
		SettingsOverrides:    settings,
		StreamFn: func(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
			stream := newMockAssistantStream()
			msg := testAssistantMessage("ok", withModelRef(model))
			stream.Push(types.AssistantMessageEvent{Type: types.EventStart, Partial: &msg})
			stream.Push(types.AssistantMessageEvent{Type: types.EventDone, Reason: types.StopReasonStop, Message: &msg})
			stream.End()
			return stream.AssistantMessageEventStream
		},
		ExtensionsResult: []*LoadExtensionsResult{{
			Extensions: []*Extension{
				{
					Path:     "v086-regression-stub",
					Handlers: map[string][]ExtensionHandler{},
				},
			},
			Runtime: CreateExtensionRuntime(),
		}},
	})
	for event, handler := range handlers {
		session.extensionRunner.extensions[0].Handlers[event] = []ExtensionHandler{handler}
	}
	return session, sessionManager
}

// TestTreeNavigationRejectedDuringManualCompaction is #9178 case 1:
// navigation is rejected before the active leaf can change while a manual
// compaction is in flight.
func TestTreeNavigationRejectedDuringManualCompaction(t *testing.T) {
	compactionStarted := make(chan struct{})
	compactionReleased := make(chan struct{})
	session, sessionManager := v086TreeHarness(t, Settings{"compaction": Settings{"keepRecentTokens": float64(1)}}, map[string]ExtensionHandler{
		"session_before_compact": func(ctx context.Context, extCtx *ExtensionContext, event ExtensionEvent) (any, error) {
			compactionEvent, ok := event.(*SessionBeforeCompactEvent)
			if !ok || compactionEvent.Preparation == nil {
				return nil, nil
			}
			close(compactionStarted)
			<-compactionReleased
			return &SessionBeforeCompactResult{
				Compaction: &CompactionResult{
					Summary:          "summary",
					FirstKeptEntryID: compactionEvent.Preparation.FirstKeptEntryID,
					TokensBefore:     compactionEvent.Preparation.TokensBefore,
					Details:          map[string]any{},
				},
			}, nil
		},
	})
	defer session.Dispose()

	appendMsg := func(role types.Role, text string) string {
		var message agent.AgentMessage
		if role == types.RoleUser {
			message = types.UserMessage{Role: role, Content: types.UserContent{String: text}, Timestamp: time.Now().UnixMilli()}
		} else {
			message = types.AssistantMessage{Role: role, Content: []types.Content{types.NewTextContent(text)}, Timestamp: time.Now().UnixMilli(), API: "anthropic-messages", Provider: "anthropic", Model: "mock", StopReason: types.StopReasonStop}
		}
		id, _ := sessionManager.AppendMessage(message)
		return id
	}
	appendMsg(types.RoleUser, "first user")
	navigationTargetID := appendMsg(types.RoleAssistant, "first assistant")
	appendMsg(types.RoleUser, "second user")
	originalLeafID := appendMsg(types.RoleAssistant, "second assistant")
	session.Agent.SetMessages(sessionManager.BuildSessionContext().Messages)

	compactionDone := make(chan struct{})
	go func() {
		defer close(compactionDone)
		_, _ = session.Compact(context.Background(), "")
	}()

	select {
	case <-compactionStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("compaction never started")
	}

	if !session.IsCompacting() {
		t.Fatal("expected isCompacting true")
	}

	_, navErr := session.NavigateTree(context.Background(), navigationTargetID, &NavigateTreeOptions{})
	if navErr == nil || !strings.Contains(navErr.Error(), "Wait for the current compaction or tree navigation to finish before navigating the session tree.") {
		t.Fatalf("navigateTree error = %v", navErr)
	}
	if got := sessionManager.GetLeafID(); got == nil || *got != originalLeafID {
		t.Fatalf("leaf changed during compaction: %v", got)
	}

	close(compactionReleased)
	<-compactionDone

	entries := sessionManager.GetEntries()
	last := entries[len(entries)-1]
	compactionEntry, ok := last.(*CompactionEntry)
	if !ok {
		t.Fatalf("last entry = %T, want compaction", last)
	}
	if got := compactionEntry.Base().ParentID; got == nil || *got != originalLeafID {
		t.Fatalf("compaction parentId = %v, want original leaf %v", got, originalLeafID)
	}
	messagesContain := false
	for _, message := range session.Messages() {
		if strings.Contains(entryText(message), "second assistant") {
			messagesContain = true
			break
		}
	}
	if !messagesContain {
		t.Fatal("expected messages to contain the pre-compaction assistant text")
	}
}

// TestTreeNavigationRejectedWhileNavigationPending is #9178 case 2: a second
// navigation is rejected while the first waits on session_before_tree.
func TestTreeNavigationRejectedWhileNavigationPending(t *testing.T) {
	navigationStarted := make(chan struct{})
	navigationReleased := make(chan struct{})

	session, sessionManager := v086TreeHarness(t, nil, map[string]ExtensionHandler{
		"session_before_tree": func(ctx context.Context, extCtx *ExtensionContext, event ExtensionEvent) (any, error) {
			close(navigationStarted)
			<-navigationReleased
			return nil, nil
		},
	})
	defer session.Dispose()

	seed := func(role types.Role, text string) string {
		var message agent.AgentMessage
		if role == types.RoleUser {
			message = types.UserMessage{Role: role, Content: types.UserContent{String: text}, Timestamp: time.Now().UnixMilli()}
		} else {
			message = types.AssistantMessage{Role: role, Content: []types.Content{types.NewTextContent(text)}, Timestamp: time.Now().UnixMilli(), API: "anthropic-messages", Provider: "anthropic", Model: "mock", StopReason: types.StopReasonStop}
		}
		id, _ := sessionManager.AppendMessage(message)
		return id
	}
	secondTargetID := seed(types.RoleUser, "first user")
	firstTargetID := seed(types.RoleAssistant, "first assistant")
	seed(types.RoleUser, "second user")
	originalLeafID := seed(types.RoleAssistant, "second assistant")
	session.Agent.SetMessages(sessionManager.BuildSessionContext().Messages)

	firstNavigation := make(chan error, 1)
	go func() {
		_, err := session.NavigateTree(context.Background(), firstTargetID, &NavigateTreeOptions{})
		firstNavigation <- err
	}()

	select {
	case <-navigationStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("navigation never started")
	}

	_, secondErr := session.NavigateTree(context.Background(), secondTargetID, &NavigateTreeOptions{})
	if secondErr == nil || !strings.Contains(secondErr.Error(), "Wait for the current compaction or tree navigation to finish before navigating the session tree.") {
		t.Fatalf("second navigation error = %v", secondErr)
	}
	if got := sessionManager.GetLeafID(); got == nil || *got != originalLeafID {
		t.Fatalf("leaf = %v, want original", got)
	}
	close(navigationReleased)
	if err := <-firstNavigation; err != nil {
		t.Fatalf("first navigation = %v", err)
	}
	if got := sessionManager.GetLeafID(); got == nil || *got != firstTargetID {
		t.Fatalf("leaf = %v, want first target", got)
	}
}

// v086AutoCompactionSuites — pi:
// test/suite/regressions/9340-9777-auto-compaction-cancellation.test.ts.
// The two getAuth-spy subcases ("reports <error> as a failure") need an
// auth-resolution seam on ModelRuntime and stay with the S6-3b batch
// (declared); the remaining cases port here.

func v086SeedCompactableSession(t *testing.T, session *AgentSession, sessionManager *SessionManager) {
	t.Helper()
	_, _ = sessionManager.AppendMessage(types.UserMessage{
		Role:      types.RoleUser,
		Content:   types.UserContent{String: strings.Repeat("x", 500)},
		Timestamp: 1,
	})
	assistant := types.AssistantMessage{
		Role:       types.RoleAssistant,
		Content:    []types.Content{types.NewTextContent(strings.Repeat("y", 200))},
		API:        "anthropic-messages",
		Provider:   "anthropic",
		Model:      "mock",
		Usage:      createMockUsage(100, 0, 0, 0),
		StopReason: types.StopReasonStop,
		Timestamp:  2,
	}
	_, _ = sessionManager.AppendMessage(assistant)
	session.Agent.SetMessages(sessionManager.BuildSessionContext().Messages)
}

func v086CollectEvents(session *AgentSession, ofTypes ...string) (events *[]*AgentSessionEvent) {
	collected := &[]*AgentSessionEvent{}
	session.Subscribe(func(event *AgentSessionEvent) {
		for _, want := range ofTypes {
			if event.Type == agent.AgentEventType(want) {
				*collected = append(*collected, event)
			}
		}
	})
	return collected
}

// TestNoPostRunAutoCompactionAfterAbort is #9340: an abort requested during
// message_end must prevent the post-run auto-compaction from starting.
func TestNoPostRunAutoCompactionAfterAbort(t *testing.T) {
	session, sessionManager, _ := createAgentTestSession(t, &createAgentTestSessionOptions{
		GetAPIKey:            true,
		OmitSeedSystemPrompt: true,
		Model: &types.Model{
			Provider:      types.ProviderID("anthropic"),
			ID:            "faux-1",
			API:           types.ApiAnthropicMessages,
			ContextWindow: 200,
			MaxTokens:     50,
			Input:         []types.ModelInput{types.ModelInputText},
		},
		SettingsOverrides: Settings{
			"compaction": Settings{"enabled": true, "reserveTokens": float64(50), "keepRecentTokens": float64(1)},
			"retry":      Settings{"enabled": false},
		},
		ExtensionsResult: []*LoadExtensionsResult{{
			Extensions: []*Extension{{
				Path:     "cancel-compaction-stub",
				Handlers: map[string][]ExtensionHandler{},
			}},
			Runtime: CreateExtensionRuntime(),
		}},
	})
	defer session.Dispose()
	session.extensionRunner.extensions[0].Handlers["session_before_compact"] = []ExtensionHandler{
		func(ctx context.Context, extCtx *ExtensionContext, event ExtensionEvent) (any, error) {
			return &SessionBeforeCompactResult{Cancel: true}, nil
		},
	}

	v086SeedCompactableSession(t, session, sessionManager)

	compactionStarts := 0
	session.Subscribe(func(event *AgentSessionEvent) {
		if event.Type == agent.AgentEventType("compaction_start") {
			compactionStarts++
		}
		if event.Type == agent.AgentEventMessageEnd && event.Message != nil && event.Message.MessageRole() == types.RoleAssistant {
			session.AbortCompaction()
			_ = session.Abort(context.Background())
		}
	})

	// A streaming-mode agent tracks an active run; pig's session runs the
	// prompt synchronously, so wrap the abort request the same way pi's
	// subscriber does — the guard must hold regardless.
	prompt := strings.Repeat("z", 1000)
	if err := session.Prompt(context.Background(), prompt, nil); err != nil {
		t.Fatalf("prompt = %v", err)
	}

	if compactionStarts != 0 {
		t.Fatalf("compaction_start count = %d, want 0", compactionStarts)
	}
}

// TestAutoCompactionCancelsSynchronouslyFromStart is #9777: aborting during
// the compaction_start event leaves the summary request unsent and reports
// the run as aborted.
func TestAutoCompactionCancelsSynchronouslyFromStart(t *testing.T) {
	var streamCalls atomic.Int64
	session, sessionManager, _ := createAgentTestSession(t, &createAgentTestSessionOptions{
		GetAPIKey:            true,
		OmitSeedSystemPrompt: true,
		SettingsOverrides: Settings{
			"compaction": Settings{"keepRecentTokens": float64(1)},
		},
		StreamFn: func(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
			streamCalls.Add(1)
			stream := newMockAssistantStream()
			msg := testAssistantMessage("should not happen", withModelRef(model))
			stream.Push(types.AssistantMessageEvent{Type: types.EventStart, Partial: &msg})
			stream.Push(types.AssistantMessageEvent{Type: types.EventDone, Reason: types.StopReasonStop, Message: &msg})
			stream.End()
			return stream.AssistantMessageEventStream
		},
	})
	defer session.Dispose()
	v086SeedCompactableSession(t, session, sessionManager)

	session.Subscribe(func(event *AgentSessionEvent) {
		if event.Type == agent.AgentEventType("compaction_start") {
			session.AbortCompaction()
		}
	})

	shouldContinue, err := session.runAutoCompaction(context.Background(), "threshold", false)
	if err != nil {
		t.Fatalf("runAutoCompaction = %v", err)
	}
	if shouldContinue {
		t.Fatal("expected no continuation after cancellation")
	}
	if streamCalls.Load() != 0 {
		t.Fatalf("stream calls = %d, want 0 (summary request never sent)", streamCalls.Load())
	}
}

// TestAutoCompactionReportsExtensionCancellationAsAborted (pi: reports
// extension cancellation as aborted).
func TestAutoCompactionReportsExtensionCancellationAsAborted(t *testing.T) {
	session, sessionManager, _ := createAgentTestSession(t, &createAgentTestSessionOptions{
		GetAPIKey:            true,
		OmitSeedSystemPrompt: true,
		SettingsOverrides: Settings{
			"compaction": Settings{"keepRecentTokens": float64(1)},
		},
		ExtensionsResult: []*LoadExtensionsResult{{
			Extensions: []*Extension{{
				Path:     "cancel-stub",
				Handlers: map[string][]ExtensionHandler{},
			}},
			Runtime: CreateExtensionRuntime(),
		}},
	})
	defer session.Dispose()
	v086SeedCompactableSession(t, session, sessionManager)
	session.extensionRunner.extensions[0].Handlers["session_before_compact"] = []ExtensionHandler{
		func(ctx context.Context, extCtx *ExtensionContext, event ExtensionEvent) (any, error) {
			return &SessionBeforeCompactResult{Cancel: true}, nil
		},
	}

	compactionEnds := &[]*AgentSessionEvent{}
	session.Subscribe(func(event *AgentSessionEvent) {
		if event.Type == agent.AgentEventType("compaction_end") {
			*compactionEnds = append(*compactionEnds, event)
		}
	})

	shouldContinue, err := session.runAutoCompaction(context.Background(), "threshold", false)
	if err != nil {
		t.Fatalf("runAutoCompaction = %v", err)
	}
	if shouldContinue {
		t.Fatal("expected no continuation after cancellation")
	}
	if len(*compactionEnds) == 0 {
		t.Fatal("expected a compaction_end event")
	}
	last := (*compactionEnds)[len(*compactionEnds)-1]
	if !last.Aborted {
		t.Fatalf("compaction_end aborted = false, want true (%+v)", last)
	}
}
