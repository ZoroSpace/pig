// pi: agent-session.ts prompt()/steer()/followUp() disposition reporting
// (v0.99.1) — accepted prompts report how they were dispatched ("handled" /
// "queued" / "started"); rejected prompts report nothing.
package core

import (
	"context"
	"testing"
	"time"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/ai/types"
)

// promptDispositionRecorder collects preflight dispositions (pi: the RPC
// mode's preflightResult hook).
type promptDispositionRecorder struct {
	mu   chan struct{}
	got  []PromptDisposition
	done chan struct{}
}

func TestAgentSessionPromptDispositionStarted(t *testing.T) {
	dispositions := make(chan PromptDisposition, 4)
	session, _, _ := createAgentTestSession(t, &createAgentTestSessionOptions{
		GetAPIKey:            true,
		OmitSeedSystemPrompt: true,
		StreamFn: func(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
			stream := newMockAssistantStream()
			msg := testAssistantMessage("ok", withModelRef(model))
			stream.Push(types.AssistantMessageEvent{Type: types.EventStart, Partial: &msg})
			stream.Push(types.AssistantMessageEvent{Type: types.EventDone, Reason: msg.StopReason, Message: &msg})
			stream.End()
			return stream.AssistantMessageEventStream
		},
	})
	defer session.Dispose()

	err := session.Prompt(context.Background(), "hello", &PromptOptions{
		PreflightResult: func(d PromptDisposition) { dispositions <- d },
	})
	if err != nil {
		t.Fatal(err)
	}
	session.WaitForIdle(context.Background())

	select {
	case got := <-dispositions:
		// Lock the wire-format value (pi: "started").
		if got != "started" {
			t.Fatalf("disposition = %q, want %q", got, "started")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("preflightResult never called")
	}
}

func TestAgentSessionPromptDispositionQueuedViaSteer(t *testing.T) {
	toolStarted := make(chan struct{})
	releaseTool := make(chan struct{})
	session, _, _ := createAgentTestSession(t, &createAgentTestSessionOptions{
		GetAPIKey:            true,
		OmitSeedSystemPrompt: true,
		BaseToolsOverride: map[string]agent.AgentTool{
			"wait": v086BlockingTool("wait", toolStarted, releaseTool),
		},
		StreamFn: v086CountedResponses([]func(call int) (func(*types.AssistantMessage), types.StopReason){
			func(_ int) (func(*types.AssistantMessage), types.StopReason) {
				return func(m *types.AssistantMessage) {
					m.StopReason = types.StopReasonToolUse
					m.Content = []types.Content{
						types.NewTextContent(""),
						types.ToolCall{Kind: types.KindToolCall, ID: "call_1", Name: "wait", Arguments: map[string]any{}},
					}
				}, types.StopReasonToolUse
			},
			func(_ int) (func(*types.AssistantMessage), types.StopReason) {
				return nil, types.StopReasonStop
			},
		}),
	})
	defer session.Dispose()
	close(releaseTool) // release on test end; the tool start gates the steer below

	promptErr := make(chan error, 1)
	go func() { promptErr <- session.Prompt(context.Background(), "start", nil) }()
	select {
	case <-toolStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("tool never started")
	}

	got, err := session.Steer(context.Background(), "queued text", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != PromptDispositionQueued {
		t.Fatalf("steer disposition = %q, want %q", got, PromptDispositionQueued)
	}
	if err := <-promptErr; err != nil {
		t.Fatal(err)
	}
	session.WaitForIdle(context.Background())
}

func TestAgentSessionPromptDispositionHandled(t *testing.T) {
	extensionsResult := &LoadExtensionsResult{
		Extensions: []*Extension{
			{Path: "disposition-input-extension", Handlers: map[string][]ExtensionHandler{}},
		},
		Runtime: CreateExtensionRuntime(),
	}
	extensionsResult.Extensions[0].Handlers["input"] = []ExtensionHandler{
		func(ctx context.Context, extCtx *ExtensionContext, event ExtensionEvent) (any, error) {
			return &InputEventResult{Action: "handled"}, nil
		},
	}
	session, _, _ := createAgentTestSession(t, &createAgentTestSessionOptions{
		GetAPIKey:            true,
		OmitSeedSystemPrompt: true,
		ExtensionsResult:     []*LoadExtensionsResult{extensionsResult},
		StreamFn: func(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
			stream := newMockAssistantStream()
			msg := testAssistantMessage("ok", withModelRef(model))
			stream.Push(types.AssistantMessageEvent{Type: types.EventStart, Partial: &msg})
			stream.Push(types.AssistantMessageEvent{Type: types.EventDone, Reason: msg.StopReason, Message: &msg})
			stream.End()
			return stream.AssistantMessageEventStream
		},
	})
	defer session.Dispose()

	dispositions := make(chan PromptDisposition, 4)
	err := session.Prompt(context.Background(), "intercepted", &PromptOptions{
		PreflightResult: func(d PromptDisposition) { dispositions <- d },
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-dispositions:
		if got != "handled" {
			t.Fatalf("prompt disposition = %q, want %q", got, "handled")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("preflightResult never called")
	}

	// Steer through the same handler reports "handled" too.
	got, err := session.Steer(context.Background(), "queued text", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != "handled" {
		t.Fatalf("steer disposition = %q, want %q", got, "handled")
	}
}

func TestAgentSessionPromptRejectedReportsNothing(t *testing.T) {
	// The compaction guard rejects the prompt; preflightResult must not fire
	// (pi v0.99.1: the throw is the only rejection signal).
	session, _, _ := createAgentTestSession(t, &createAgentTestSessionOptions{
		GetAPIKey:            true,
		OmitSeedSystemPrompt: true,
		StreamFn: func(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
			stream := newMockAssistantStream()
			stream.End()
			return stream.AssistantMessageEventStream
		},
	})
	defer session.Dispose()

	// Same-package test seam: mark a compaction as in-flight (pig-only —
	// pi's tests drive this through a real compaction).
	session.mu.Lock()
	session.compactionCancel = func() {}
	session.mu.Unlock()

	called := false
	err := session.Prompt(context.Background(), "boom", &PromptOptions{
		PreflightResult: func(d PromptDisposition) { called = true },
	})
	if err == nil {
		t.Fatal("expected the compaction guard to reject the prompt")
	}
	if called {
		t.Fatal("preflightResult must not be called for rejected prompts")
	}
}
