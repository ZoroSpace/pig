// interactive_mode_commands_test.go — submit-routing and rendering tests for
// the slash commands wired in the 2026-09-18 batch (/import /copy /session
// /changelog /hotkeys /clone /reload /debug + the two easter eggs), plus the
// /reload leak regression. pi: setupEditorSubmitHandler branches.
//
// See docs/feat/SLASH-COMMANDS-COMPLETION.md and
// docs/fix/2026-09-18-reload-command-leaks-to-model.md.
package interactive

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/codingagent"
	"github.com/ZoroSpace/pig/codingagent/core"
	"github.com/ZoroSpace/pig/codingagent/modes/interactive/components"
	"github.com/ZoroSpace/pig/codingagent/modes/interactive/theme"
	"github.com/ZoroSpace/pig/tui"
)

// TestWiredSubmitCommandsReachTheirHandlers drives each newly wired command
// through processSubmitText and locks two things: nothing reaches the model
// (input gate empty — these are commands, not messages) and the expected
// transcript content renders. The full-dialog paths (/import with an arg)
// need a real selector UI and are covered by the tmux smoke pass instead.
func TestWiredSubmitCommandsReachTheirHandlers(t *testing.T) {
	tests := []struct {
		name string
		text string
		want string // rendered headline to expect
	}{
		{"copy without messages errors", "/copy", "No agent messages to copy yet."},
		{"session info renders", "/session", "Session Info"},
		{"changelog fallback renders", "/changelog", "No changelog entries found."},
		{"hotkeys renders", "/hotkeys", "Keyboard Shortcuts"},
		{"clone without entries statuses", "/clone", "Nothing to clone yet"},
		{"armin easter egg renders", "/arminsayshi", "ARMIN SAYS HI"},
		{"dementedelves easter egg renders", "/dementedelves", "pi has joined Earendil"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newSubmitTestMode(t)
			m.keybindings = core.CreateKeybindingsManager("") // handleHotkeysCommand reads the effective config
			m.processSubmitText(context.Background(), tt.text)

			if !inputGateEmpty(m) {
				t.Fatalf("%q leaked to submission", tt.text)
			}
			rendered := normalizeRenderedOutput(renderAllChildren(m.chatContainer, 120))
			if !strings.Contains(rendered, tt.want) {
				t.Fatalf("expected %q in transcript after %q, got:\n%s", tt.want, tt.text, rendered)
			}
		})
	}
}

func TestImportCommandWithoutPathShowsUsage(t *testing.T) {
	m := newSubmitTestMode(t)
	m.processSubmitText(context.Background(), "/import")

	if !inputGateEmpty(m) {
		t.Fatal("/import leaked to submission")
	}
	rendered := normalizeRenderedOutput(renderAllChildren(m.chatContainer, 120))
	if !strings.Contains(rendered, "Usage: /import <path.jsonl>") {
		t.Fatalf("expected usage error, got:\n%s", rendered)
	}
}

// debugProbeTerminal satisfies the Terminal surface handleDebugCommand needs.
type debugProbeTerminal struct {
	tui.Terminal
	columns int
	rows    int
}

func (t *debugProbeTerminal) Columns() int { return t.columns }
func (t *debugProbeTerminal) Rows() int    { return t.rows }

// debugProbeTUI adds the Render/Terminal surface the stub lacks so the debug
// frame snapshot can run against fixed lines.
type debugProbeTUI struct {
	stubRequestRenderTUI
	lines []string
}

func (d *debugProbeTUI) Terminal() tui.Terminal { return &debugProbeTerminal{columns: 80, rows: 24} }
func (d *debugProbeTUI) Render(width int) []string {
	return append([]string(nil), d.lines...)
}

func TestDebugCommandWritesDebugLog(t *testing.T) {
	agentDir := t.TempDir()
	t.Setenv(codingagent.EnvAgentDir, agentDir)

	theme.SetGlobalTheme(newSubmitTestTheme(t))
	session := createInteractiveTestSession(t, &fixtureResourceLoader{}, nil)
	m := newSeamTestMode(session, nil)
	m.editor = components.NewCustomEditor(&stubRequestRenderTUI{}, theme.GetEditorTheme(), core.CreateKeybindingsManager(""), nil)
	probe := &debugProbeTUI{lines: []string{"hello 世界", ""}}
	m.ui = probe

	m.processSubmitText(context.Background(), "/debug")

	if !inputGateEmpty(m) {
		t.Fatal("/debug leaked to submission")
	}
	logPath := codingagent.GetDebugLogPath()
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("debug log not written: %v", err)
	}
	text := string(data)
	for _, want := range []string{
		"Debug output at ",
		"Terminal: 80x24",
		"=== All rendered lines with visible widths ===",
		`[0] (w=10) "hello 世界"`,
		"[1] (w=0) \"\"",
		"=== Agent messages (JSONL) ===",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("debug log missing %q;\n%s", want, text)
		}
	}
	rendered := normalizeRenderedOutput(renderAllChildren(m.chatContainer, 120))
	if !strings.Contains(rendered, "✓ Debug log written") {
		t.Fatalf("expected debug confirmation, got:\n%s", rendered)
	}
	_ = filepath.Base(logPath)
}

// createStreamingTestSession builds a seam session whose streamFn blocks on
// release until the test lets it through, then completes normally (the shared
// createInteractiveTestSession streamFn returns a never-ending empty stream,
// which no abort can unblock — pig's event iteration is a plain range).
func createStreamingTestSession(t *testing.T, release chan struct{}) *core.AgentSession {
	agentInstance, err := agent.NewAgent(agent.AgentOptions{
		StreamFn: func(_ context.Context, _ *types.Model, _ *types.TranscriptContext, _ *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
			<-release
			stream := types.NewAssistantMessageEventStream()
			message := types.AssistantMessage{
				Role: types.RoleAssistant, Content: []types.Content{types.NewTextContent("ok")},
				API: "openai-completions", Provider: "testprov", Model: "m1",
				Usage: types.Usage{Cost: types.Cost{}}, StopReason: types.StopReasonStop, Timestamp: 0,
			}
			stream.Push(types.AssistantMessageEvent{Type: types.EventStart, Partial: &message})
			stream.Push(types.AssistantMessageEvent{Type: types.EventDone, Reason: types.StopReasonStop, Message: &message})
			stream.End()
			return stream
		},
		InitialState: &agent.AgentState{SystemPrompt: "Test"},
	})
	if err != nil {
		t.Fatal(err)
	}

	sessionManager, err := core.InMemorySession(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	settingsManager := core.InMemorySettingsManager(core.Settings{}, nil)
	emptyModelsPath := ""
	runtime := core.CreateModelRuntime(context.Background(), &core.CreateModelRuntimeOptions{
		Credentials:       core.NewInMemoryAuthStorageWithCredentials(nil),
		ModelsPath:        &emptyModelsPath,
		ModelsStore:       &ai.InMemoryModelsStore{},
		AllowModelNetwork: false,
	})
	session := core.NewAgentSession(core.AgentSessionConfig{
		Agent:           agentInstance,
		SessionManager:  sessionManager,
		SettingsManager: settingsManager,
		Cwd:             sessionManager.GetCwd(),
		ModelRuntime:    runtime,
		ResourceLoader:  &fixtureResourceLoader{},
	})
	t.Cleanup(session.Dispose)
	return session
}

// TestReloadCommandWhileStreamingShowsWaitWarning locks the /reload routing
// regression: /reload used to fall through to the model as a plain message
// because its submit branch was missing. A streaming session trips the
// handler's first guard, keeping the test on the stub-safe path while still
// proving the branch intercepts the input.
func TestReloadCommandWhileStreamingShowsWaitWarning(t *testing.T) {
	theme.SetGlobalTheme(newSubmitTestTheme(t))
	release := make(chan struct{})
	session := createStreamingTestSession(t, release)
	registerTestProvider(t, session, "testprov", []core.ProviderConfigInputModel{{
		ID: "m1", Name: "M1", API: "openai-completions", BaseURL: "http://localhost:0",
	}})
	model, ok := session.GetModelRuntime().GetModel("testprov", "m1")
	if !ok {
		t.Fatal("test model not registered")
	}
	if err := session.SetModel(context.Background(), &model, nil); err != nil {
		t.Fatalf("SetModel: %v", err)
	}

	runErr := make(chan error, 1)
	go func() { runErr <- session.Prompt(context.Background(), "hi", nil) }()

	deadline := time.Now().Add(10 * time.Second)
	for !session.IsStreaming() {
		if time.Now().After(deadline) {
			t.Fatal("session never started streaming")
		}
		time.Sleep(5 * time.Millisecond)
	}

	m := newSubmitTestMode(t)
	m.runtimeHost = core.NewAgentSessionRuntime(session, nil, nil, nil, "")
	m.processSubmitText(context.Background(), "/reload")

	if !inputGateEmpty(m) {
		t.Fatal("/reload leaked to submission (regression: it used to reach the model as a plain message)")
	}
	rendered := normalizeRenderedOutput(renderAllChildren(m.chatContainer, 120))
	if !strings.Contains(rendered, "Wait for the current response to finish before reloading.") {
		t.Fatalf("expected reload wait warning, got:\n%s", rendered)
	}

	close(release)
	select {
	case <-runErr:
	case <-time.After(10 * time.Second):
		t.Fatal("prompt did not finish after release")
	}
}
