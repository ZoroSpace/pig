// Tests for the extensions consumption surface (pi: core/extensions/runner.ts
// + wrapper.ts behavior; the no-extension path plus the handler dispatch
// loops that Phase 7's loader will feed).
package core

import (
	"context"
	"strings"
	"testing"

	"github.com/ZoroSpace/pig/ai/types"
)

func newTestRunner(t *testing.T) *ExtensionRunner {
	t.Helper()
	runner := NewExtensionRunner(nil, CreateExtensionRuntime(), "/tmp/project", &SessionManager{}, &ModelRegistry{})
	t.Cleanup(func() { runner.Invalidate("test done") })
	return runner
}

// fullTestContextActions supplies every context action (pi: contextActions
// are required; a partial bindCore would nil-panic at CreateContext, same as
// pi's undefined getters).
func fullTestContextActions() *ExtensionContextActions {
	return &ExtensionContextActions{
		GetModel:           func() *types.Model { return nil },
		GetScopedModels:    func() []ScopedModel { return nil },
		IsIdle:             func() bool { return true },
		IsProjectTrusted:   func() bool { return true },
		GetSignal:          func() context.Context { return nil },
		Abort:              func() {},
		HasPendingMessages: func() bool { return false },
		Shutdown:           func() {},
		GetContextUsage:    func() *ContextUsage { return nil },
		Compact:            func(_ *CompactOptions) {},
		GetSystemPrompt:    func() string { return "" },
	}
}

func TestNoExtensionPathHasNoHandlers(t *testing.T) {
	runner := newTestRunner(t)

	if runner.HasHandlers("input") || runner.HasHandlers("message_end") || runner.HasHandlers("anything") {
		t.Fatal("expected no handlers")
	}
	if cmd := runner.GetCommand("anything"); cmd != nil {
		t.Fatalf("expected no command, got %v", cmd)
	}
	if commands := runner.GetRegisteredCommands(); len(commands) != 0 {
		t.Fatalf("expected no commands, got %d", len(commands))
	}
	if registered := runner.GetAllRegisteredTools(); len(registered) != 0 {
		t.Fatalf("expected no registered tools, got %d", len(registered))
	}
	if paths := runner.GetExtensionPaths(); len(paths) != 0 {
		t.Fatalf("expected no extension paths, got %d", len(paths))
	}
	if runner.HasUI() {
		t.Fatal("expected the no-op UI context to report HasUI false")
	}
}

func TestNoExtensionPathEmitDefaults(t *testing.T) {
	runner := newTestRunner(t)
	ctx := context.Background()

	result, err := runner.EmitInput(ctx, "hello", nil, InputSourceInteractive, "")
	if err != nil {
		t.Fatal(err)
	}
	if result.Action != "continue" {
		t.Fatalf("expected continue, got %+v", result)
	}

	combined, err := runner.EmitBeforeAgentStart(ctx, BeforeAgentStartTrigger{MessageType: "user", Prompt: "hello"}, BuildSystemPromptOptions{Cwd: "/tmp/project"})
	if err != nil {
		t.Fatal(err)
	}
	// pi v0.86.0: the combined result is always returned, with the
	// normalized options materialized.
	if combined == nil {
		t.Fatal("expected combined result, got nil")
	}
	if len(combined.Messages) != 0 {
		t.Fatalf("expected no messages, got %+v", combined.Messages)
	}
	if combined.SystemPromptOptions.Cwd != "/tmp/project" {
		t.Fatalf("expected normalized cwd %q, got %q", "/tmp/project", combined.SystemPromptOptions.Cwd)
	}
	if combined.SystemPromptOptions.Sections == nil {
		t.Fatal("expected normalized sections to be materialized")
	}

	toolCall, err := runner.EmitToolCall(ctx, &ToolCallEvent{Type: "tool_call", ToolName: "read", ToolCallID: "tc1"})
	if err != nil {
		t.Fatal(err)
	}
	if toolCall != nil {
		t.Fatalf("expected nil tool call result, got %+v", toolCall)
	}

	toolResult, err := runner.EmitToolResult(ctx, &ToolResultEvent{Type: "tool_result", ToolName: "read", ToolCallID: "tc1"})
	if err != nil {
		t.Fatal(err)
	}
	if toolResult != nil {
		t.Fatalf("expected nil tool result, got %+v", toolResult)
	}

	message, err := runner.EmitMessageEnd(ctx, &MessageEndEvent{Type: "message_end"})
	if err != nil {
		t.Fatal(err)
	}
	if message != nil {
		t.Fatalf("expected no replacement message, got %v", message)
	}

	discover, err := runner.EmitResourcesDiscover(ctx, "/tmp/project", "startup")
	if err != nil {
		t.Fatal(err)
	}
	if len(discover.SkillPaths) != 0 || len(discover.PromptPaths) != 0 || len(discover.ThemePaths) != 0 {
		t.Fatalf("expected empty discover result, got %+v", discover)
	}

	if _, err := runner.Emit(ctx, &AgentStartEvent{Type: "agent_start"}); err != nil {
		t.Fatal(err)
	}
}

func TestFlagValuesRoundTrip(t *testing.T) {
	runner := newTestRunner(t)

	runner.SetFlagValue("verbose", true)
	runner.SetFlagValue("mode", "fast")
	values := runner.GetFlagValues()
	if values["verbose"] != true || values["mode"] != "fast" {
		t.Fatalf("unexpected flag values: %v", values)
	}
}

func TestInvalidateMakesRunnerStale(t *testing.T) {
	runner := newTestRunner(t)
	runner.BindCore(&ExtensionActions{}, fullTestContextActions(), nil)

	runner.Invalidate("stale after test")
	func() {
		defer func() {
			if r := recover(); r == nil || !strings.Contains(r.(string), "stale after test") {
				t.Fatalf("expected stale panic, got %v", r)
			}
		}()
		runner.GetActiveTools()
	}()
}

func TestEmitErrorListeners(t *testing.T) {
	runner := newTestRunner(t)

	var received []ExtensionError
	unsubscribe := runner.OnError(func(err ExtensionError) {
		received = append(received, err)
	})
	runner.EmitError(ExtensionError{ExtensionPath: "<runtime>", Event: "send_message", Error: "boom"})
	if len(received) != 1 || received[0].Error != "boom" {
		t.Fatalf("unexpected errors: %v", received)
	}

	unsubscribe()
	runner.EmitError(ExtensionError{ExtensionPath: "<runtime>", Event: "send_message", Error: "boom2"})
	if len(received) != 1 {
		t.Fatalf("expected unsubscribe to stop delivery, got %v", received)
	}
}

func TestCreateContextReflectsBoundActions(t *testing.T) {
	runner := newTestRunner(t)
	model := &types.Model{ID: "m", Name: "M", API: "openai-completions", Provider: "deepseek"}
	runner.BindCore(&ExtensionActions{}, &ExtensionContextActions{
		GetModel:           func() *types.Model { return model },
		GetScopedModels:    func() []ScopedModel { return nil },
		IsIdle:             func() bool { return true },
		IsProjectTrusted:   func() bool { return false },
		GetSignal:          func() context.Context { return nil },
		Abort:              func() {},
		HasPendingMessages: func() bool { return false },
		Shutdown:           func() {},
		GetContextUsage:    func() *ContextUsage { return nil },
		Compact:            func(_ *CompactOptions) {},
		GetSystemPrompt:    func() string { return "sys" },
	}, nil)

	ctx := runner.CreateContext()
	if ctx.Model != model {
		t.Fatalf("expected bound model, got %v", ctx.Model)
	}
	if ctx.GetSystemPrompt() != "sys" {
		t.Fatalf("expected system prompt, got %q", ctx.GetSystemPrompt())
	}
	if ctx.HasUI {
		t.Fatal("expected no UI with the no-op context")
	}
}

func TestResolveRegisteredCommandsDeduplicatesInvocationNames(t *testing.T) {
	commandA := RegisteredCommand{Name: "deploy", Handler: func(_ context.Context, _ string, _ *ExtensionCommandContext) error { return nil }}
	commandB := RegisteredCommand{Name: "deploy", Handler: func(_ context.Context, _ string, _ *ExtensionCommandContext) error { return nil }}
	commandC := RegisteredCommand{Name: "unique", Handler: func(_ context.Context, _ string, _ *ExtensionCommandContext) error { return nil }}
	extensions := []*Extension{
		{Path: "<ext-a>", Commands: map[string]RegisteredCommand{"a": commandA}},
		{Path: "<ext-b>", Commands: map[string]RegisteredCommand{"b": commandB, "c": commandC}},
	}
	runner := NewExtensionRunner(extensions, CreateExtensionRuntime(), "/tmp", &SessionManager{}, &ModelRegistry{})

	commands := runner.GetRegisteredCommands()
	if len(commands) != 3 {
		t.Fatalf("expected 3 commands, got %d", len(commands))
	}
	invocations := map[string]bool{}
	for _, command := range commands {
		invocations[command.InvocationName] = true
	}
	// Same name from two extensions gets numbered invocation names; the
	// distinct name keeps its own.
	if !invocations["deploy:1"] || !invocations["deploy:2"] || !invocations["unique"] {
		t.Fatalf("expected deduplicated invocation names, got %v", invocations)
	}

	if cmd := runner.GetCommand("deploy:2"); cmd == nil || cmd.Name != "deploy" {
		t.Fatalf("expected deploy:2 lookup, got %v", cmd)
	}
	if cmd := runner.GetCommand("missing"); cmd != nil {
		t.Fatalf("expected nil for missing command, got %v", cmd)
	}
}

func TestEmitDispatchesToHandlersAndShortCircuitsOnCancel(t *testing.T) {
	handler := func(_ context.Context, _ *ExtensionContext, event ExtensionEvent) (any, error) {
		if _, ok := event.(*SessionBeforeCompactEvent); ok {
			return &SessionBeforeCompactResult{Cancel: true}, nil
		}
		return nil, nil
	}
	extensions := []*Extension{{
		Path:     "<ext>",
		Handlers: map[string][]ExtensionHandler{"session_before_compact": {handler}},
	}}
	runner := NewExtensionRunner(extensions, CreateExtensionRuntime(), "/tmp", &SessionManager{}, &ModelRegistry{})

	if !runner.HasHandlers("session_before_compact") {
		t.Fatal("expected handlers")
	}
	result, err := runner.Emit(context.Background(), &SessionBeforeCompactEvent{Type: "session_before_compact"})
	if err != nil {
		t.Fatal(err)
	}
	cancelled, ok := result.(*SessionBeforeCompactResult)
	if !ok || !cancelled.Cancel {
		t.Fatalf("expected cancel result, got %v", result)
	}
}

func TestEmitReportsHandlerPanicsAsExtensionErrors(t *testing.T) {
	runner := NewExtensionRunner([]*Extension{{
		Path: "<ext>",
		Handlers: map[string][]ExtensionHandler{"agent_start": {func(_ context.Context, _ *ExtensionContext, _ ExtensionEvent) (any, error) {
			return nil, context.Canceled
		}}},
	}}, CreateExtensionRuntime(), "/tmp", &SessionManager{}, &ModelRegistry{})

	var received []ExtensionError
	runner.OnError(func(err ExtensionError) { received = append(received, err) })

	if _, err := runner.Emit(context.Background(), &AgentStartEvent{Type: "agent_start"}); err != nil {
		t.Fatal(err)
	}
	if len(received) != 1 || received[0].Event != "agent_start" || received[0].ExtensionPath != "<ext>" {
		t.Fatalf("unexpected errors: %v", received)
	}
}

func TestCreateCommandContextBindsCommandActions(t *testing.T) {
	runner := newTestRunner(t)
	waitCalled := false
	runner.BindCommandContext(&ExtensionCommandContextActions{
		WaitForIdle: func(_ context.Context) error { waitCalled = true; return nil },
		NewSession:  func(_ context.Context, _ *NewSessionCommandOptions) (bool, error) { return false, nil },
		Fork:        func(_ context.Context, _ string, _ *ForkCommandOptions) (bool, error) { return false, nil },
		NavigateTree: func(_ context.Context, _ string, _ *NavigateTreeCommandOptions) (bool, error) {
			return false, nil
		},
		SwitchSession: func(_ context.Context, _ string, _ *SwitchSessionCommandOptions) (bool, error) {
			return false, nil
		},
		Reload: func(_ context.Context) error { return nil },
	})

	commandCtx := runner.CreateCommandContext()
	if err := commandCtx.WaitForIdle(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !waitCalled {
		t.Fatal("expected WaitForIdle to reach the bound action")
	}
	if commandCtx.GetSystemPromptOptions().Cwd != "/tmp/project" {
		t.Fatalf("expected default system prompt options cwd, got %q", commandCtx.GetSystemPromptOptions().Cwd)
	}
}
