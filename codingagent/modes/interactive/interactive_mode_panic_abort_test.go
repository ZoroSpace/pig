// interactive_mode_panic_abort_test.go — regression tests for the pig
// abort-on-panic wiring in subscribeToAgent (pig panic containment;
// docs/feat/PANIC-RESILIENCE.md). The contract under test: a panicking
// listener while the agent is streaming aborts the run (the programmatic
// equivalent of the user pressing Esc) instead of letting the model keep
// streaming into a broken renderer, and the session itself stays alive for
// the next message.
//
// The wiring lives in the observer registered by subscribeToAgent. Stash
// verification: remove the `go session.Abort(ctx)` block there and these
// tests FAIL (the abort-responsive mock stream never ends, so the first
// Prompt never returns); make the Abort synchronous and they FAIL with the
// production deadlock (the observer runs on the agent's event-dispatch
// chain, which must return before the agent can go idle).
package interactive

import (
	"context"
	"strings"
	"testing"
	"time"

	"sync/atomic"

	"github.com/ZoroSpace/pig/agent"
	harness "github.com/ZoroSpace/pig/agent/harness"
	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/codingagent/core"
)

// newPanicAbortTestSession mirrors core's createConcurrentSession (a stream
// function that streams until its context is cancelled — the abort path —
// and then reports the aborted stop reason) plus the auth/runtime assembly
// the Prompt path validates (core: createAgentTestSession's auth storage +
// GetApiKey). The first stream call is abort-responsive; later calls
// complete normally so the session-usability test can drive a clean run.
func newPanicAbortTestSession(t *testing.T) *core.AgentSession {
	t.Helper()
	model := &types.Model{
		ID:            "claude-sonnet-4-5",
		Name:          "Claude Sonnet 4.5",
		API:           types.ApiOpenAICompletions,
		Provider:      "anthropic",
		BaseURL:       "http://localhost:0",
		Reasoning:     true,
		ContextWindow: 200000,
		MaxTokens:     64000,
		Input:         []ai.ModelInput{ai.ModelInputText},
	}
	var streamCalls atomic.Int32
	streamFn := func(ctx context.Context, _ *types.Model, _ *types.TranscriptContext, _ *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
		stream := types.NewAssistantMessageEventStream()
		if streamCalls.Add(1) > 1 {
			// Later prompts complete normally (core:
			// createRetrySession's success branch).
			message := types.AssistantMessage{
				Role:       types.RoleAssistant,
				Content:    []types.Content{types.NewTextContent("Success")},
				API:        "anthropic-messages",
				Provider:   "anthropic",
				Model:      "mock",
				Usage:      types.Usage{Cost: types.Cost{}},
				StopReason: types.StopReasonStop,
				Timestamp:  time.Now().UnixMilli(),
			}
			stream.Push(types.AssistantMessageEvent{Type: types.EventStart, Partial: &message})
			stream.Push(types.AssistantMessageEvent{Type: types.EventDone, Reason: types.StopReasonStop, Message: &message})
			stream.End()
			return stream
		}
		go func() {
			partial := types.AssistantMessage{
				Role:       types.RoleAssistant,
				Content:    []types.Content{types.NewTextContent("")},
				API:        "anthropic-messages",
				Provider:   "anthropic",
				Model:      "mock",
				Usage:      types.Usage{Cost: types.Cost{}},
				StopReason: types.StopReasonStop,
				Timestamp:  time.Now().UnixMilli(),
			}
			stream.Push(types.AssistantMessageEvent{Type: types.EventStart, Partial: &partial})
			for {
				select {
				case <-ctx.Done():
					aborted := partial
					aborted.Content = []types.Content{types.NewTextContent("Aborted")}
					stream.Push(types.AssistantMessageEvent{Type: types.EventError, Reason: types.StopReasonAborted, Message: &aborted})
					return
				case <-time.After(5 * time.Millisecond):
				}
			}
		}()
		return stream
	}
	agentInstance, err := agent.NewAgent(agent.AgentOptions{
		StreamFn:     streamFn,
		ConvertToLlm: harness.ConvertToLlm,
		GetApiKey:    func(types.ProviderID) string { return "test-key" },
		InitialState: &agent.AgentState{Model: model, SystemPrompt: "Test"},
	})
	if err != nil {
		t.Fatal(err)
	}
	sessionManager, err := core.InMemorySession(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	emptyModelsPath := ""
	runtime := core.CreateModelRuntime(context.Background(), &core.CreateModelRuntimeOptions{
		Credentials:       core.NewInMemoryAuthStorageWithCredentials(map[string]string{"anthropic": "test-key"}),
		ModelsPath:        &emptyModelsPath,
		ModelsStore:       &ai.InMemoryModelsStore{},
		AllowModelNetwork: false,
	})
	session := core.NewAgentSession(core.AgentSessionConfig{
		Agent:           agentInstance,
		SessionManager:  sessionManager,
		SettingsManager: core.InMemorySettingsManager(core.Settings{}, nil),
		Cwd:             sessionManager.GetCwd(),
		ModelRuntime:    runtime,
		ResourceLoader:  &fixtureResourceLoader{},
	})
	t.Cleanup(session.Dispose)
	return session
}

// newPanicAbortTestMode wires a seam InteractiveMode with subscribeToAgent
// plus an explicit panicking listener (keeps the panic source independent
// of the seam UI state — handleEvent would nil-deref on the seam's missing
// footer, which is contained by emit() and feeds the observer too).
func newPanicAbortTestMode(t *testing.T, session *core.AgentSession) *InteractiveMode {
	t.Helper()
	initTestTheme(t)
	mode := newSeamTestMode(session, nil)
	// Skip Init() inside handleEvent (the seam has no TUI); listener panics
	// are contained by emit(), which is the design under test.
	mode.isInitialized = true
	mode.subscribeToAgent()
	session.Subscribe(func(*core.AgentSessionEvent) { panic("renderer boom") })
	return mode
}

// waitPanicAbortPrompt runs a prompt in the background and asserts it
// settles (the abort path reports a cancel-flavored error; a synchronous
// Abort in the observer or a missing abort shows up as the timeout).
func waitPanicAbortPrompt(t *testing.T, session *core.AgentSession, text string) error {
	t.Helper()
	promptDone := make(chan error, 1)
	go func() { promptDone <- session.Prompt(context.Background(), text, nil) }()
	select {
	case err := <-promptDone:
		if err != nil && !strings.Contains(err.Error(), "cancel") {
			t.Fatalf("prompt %q error after panic-triggered abort = %v", text, err)
		}
		return err
	case <-time.After(5 * time.Second):
		t.Fatalf("run did not stop after the listener panic (prompt %q): observer abort is missing (run stuck) or deadlocked", text)
		return nil
	}
}

// TestSubscribeToAgentAbortsRunOnListenerPanic drives a real streaming run
// through the wired mode and asserts the panic-triggered abort stops the
// run without deadlocking.
func TestSubscribeToAgentAbortsRunOnListenerPanic(t *testing.T) {
	session := newPanicAbortTestSession(t)
	newPanicAbortTestMode(t, session)

	// The abort can land before a poll would observe streaming (the
	// observer fires on the very first dispatched event), so streaming
	// state is only noted, never asserted.
	streamingObserved := false
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !streamingObserved {
		streamingObserved = session.IsStreaming()
		if !streamingObserved {
			time.Sleep(time.Millisecond)
		}
	}

	waitPanicAbortPrompt(t, session, "Hello")
	if session.IsStreaming() {
		t.Fatal("session still streaming after the panic-triggered abort")
	}
	// The run must actually have started — guards against a vacuous pass
	// where Prompt fails synchronously (e.g. auth) before any event flows.
	if len(session.Agent.State().Messages) == 0 {
		t.Fatal("no messages in agent state: the run never started, so the abort path was not exercised")
	}
}

// TestSubscribeToAgentSessionStaysUsableAfterPanicAbort locks the "session
// survives" half of the contract: after the panic-triggered abort settles,
// a follow-up prompt runs to normal completion (stopReason stop, no
// cancellation) — option A stops the run, not the session (no halt flag).
func TestSubscribeToAgentSessionStaysUsableAfterPanicAbort(t *testing.T) {
	session := newPanicAbortTestSession(t)
	newPanicAbortTestMode(t, session)

	waitPanicAbortPrompt(t, session, "Hello")
	if session.IsStreaming() {
		t.Fatal("session still streaming after the panic-triggered abort")
	}

	// Second prompt: the streamFn completes normally, so the ONLY way this
	// settles without a cancel error is a healthy session.
	if err := waitPanicAbortPrompt(t, session, "Again"); err != nil {
		t.Fatalf("follow-up prompt should complete normally, got %v", err)
	}
	if session.IsStreaming() {
		t.Fatal("session still streaming after the follow-up prompt settled")
	}
	last := session.GetLastAssistantText()
	if last != "Success" {
		t.Fatalf("follow-up prompt assistant text = %q, want %q", last, "Success")
	}
}
