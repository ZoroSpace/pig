// Ported from pi's test/print-mode.test.ts, adapted to pig: the fakes are
// real AgentSession instances with a stub streamFn (pi uses object-literal
// session fakes, which Go's concrete types cannot express), and the
// session_shutdown emission is observed through the Dispose call on the fake
// runtime host — pig's no-extension runner cannot register shutdown handlers
// until Phase 7.
package modes

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/auth"
	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/codingagent/core"
)

func loToPtrMode(v string) *string { return &v }

// printTestModelRuntime builds a runtime with a stored openai API key so the
// session's auth pre-flight passes (pi's fakes skip the auth check).
func printTestModelRuntime(t *testing.T) *core.ModelRuntime {
	t.Helper()
	storage := core.NewInMemoryAuthStorageWithCredentials(map[string]string{"openai": "test-key"})
	if _, err := storage.Modify(context.Background(), "openai", func(current auth.Credential) (auth.Credential, error) {
		return &auth.ApiKeyCredential{Key: "test-key"}, nil
	}); err != nil {
		t.Fatal(err)
	}
	runtime := core.CreateModelRuntime(context.Background(), &core.CreateModelRuntimeOptions{
		Credentials: storage,
		ModelsPath:  loToPtrMode(""),
	})
	if err := runtime.RegisterProvider("openai", &core.ProviderConfigInput{
		API: ai.ApiOpenAICompletions,
		Models: []core.ProviderConfigInputModel{{
			ID:            "gpt-4o-mini",
			Name:          "GPT-4o mini",
			API:           ai.ApiOpenAICompletions,
			BaseURL:       "http://localhost:0",
			Input:         []ai.ModelInput{ai.ModelInputText},
			ContextWindow: 128000,
			MaxTokens:     4096,
		}},
	}); err != nil {
		t.Fatal(err)
	}
	return runtime
}

// printTestResourceLoader is the empty ResourceLoader (pi's fakes return
// empty resource results).
type printTestResourceLoader struct{}

func (l *printTestResourceLoader) GetExtensions() *core.LoadExtensionsResult {
	return &core.LoadExtensionsResult{Extensions: []*core.Extension{}, Runtime: core.CreateExtensionRuntime()}
}
func (l *printTestResourceLoader) GetSkills() ([]core.Skill, []core.ResourceDiagnostic) {
	return []core.Skill{}, []core.ResourceDiagnostic{}
}
func (l *printTestResourceLoader) GetPrompts() ([]core.PromptTemplate, []core.ResourceDiagnostic) {
	return []core.PromptTemplate{}, []core.ResourceDiagnostic{}
}
func (l *printTestResourceLoader) GetThemes() ([]any, []core.ResourceDiagnostic) { return nil, nil }
func (l *printTestResourceLoader) GetAgentsFiles() []core.AgentsFile {
	return []core.AgentsFile{}
}
func (l *printTestResourceLoader) GetSystemPrompt() (string, bool)                { return "", false }
func (l *printTestResourceLoader) GetSystemPromptSource() (string, bool)          { return "", false }
func (l *printTestResourceLoader) GetAppendSystemPrompt() []string                { return []string{} }
func (l *printTestResourceLoader) GetAppendSystemPromptSources() []string         { return []string{} }
func (l *printTestResourceLoader) ExtendResources(core.ResourceExtensionPaths)    {}
func (l *printTestResourceLoader) Reload(*core.ResourceLoaderReloadOptions) error { return nil }

// printTestSession builds a real session whose stream function replies with
// the given assistant message (pi: createRuntimeHost's fake session).
func printTestSession(t *testing.T, reply *types.AssistantMessage, capturedPrompts *[]string) *core.AgentSession {
	t.Helper()
	streamFn := func(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
		if capturedPrompts != nil {
			for _, message := range llmContext.Messages {
				user, ok := message.(*types.UserMessage)
				if !ok {
					if value, isValue := message.(types.UserMessage); isValue {
						user = &value
					}
				}
				if user == nil {
					continue
				}
				if user.Content.String != "" {
					*capturedPrompts = append(*capturedPrompts, user.Content.String)
				} else if len(user.Content.Parts) > 0 {
					switch text := user.Content.Parts[0].(type) {
					case *types.TextContent:
						*capturedPrompts = append(*capturedPrompts, text.Text)
					case types.TextContent:
						*capturedPrompts = append(*capturedPrompts, text.Text)
					}
				}
			}
		}
		stream := ai.NewAssistantMessageEventStream()
		if reply.StopReason == types.StopReasonError {
			stream.Push(types.AssistantMessageEvent{Type: types.EventStart, Partial: reply})
			stream.Push(types.AssistantMessageEvent{Type: types.EventError, Reason: types.StopReasonError, Message: reply})
		} else {
			stream.Push(types.AssistantMessageEvent{Type: types.EventStart, Partial: reply})
			stream.Push(types.AssistantMessageEvent{Type: types.EventDone, Reason: types.StopReasonStop, Message: reply})
		}
		stream.End()
		return stream
	}
	agentInstance, err := agent.NewAgent(agent.AgentOptions{
		StreamFn: streamFn,
		InitialState: &agent.AgentState{
			Model: &types.Model{
				ID:            "gpt-4o-mini",
				Name:          "GPT-4o mini",
				API:           ai.ApiOpenAICompletions,
				Provider:      "openai",
				BaseURL:       "http://localhost:0",
				Input:         []ai.ModelInput{ai.ModelInputText},
				ContextWindow: 128000,
				MaxTokens:     4096,
			},
			Tools: []agent.AgentTool{},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	sessionManager, err := core.InMemorySession(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	return core.NewAgentSession(core.AgentSessionConfig{
		Agent:           agentInstance,
		SessionManager:  sessionManager,
		SettingsManager: core.InMemorySettingsManager(core.Settings{}, nil),
		Cwd:             sessionManager.GetCwd(),
		ResourceLoader:  &printTestResourceLoader{},
		ModelRuntime:    printTestModelRuntime(t),
	})
}

// fakeRuntimeHost is pi's FakeRuntimeHost over a real session.
type fakeRuntimeHost struct {
	session      *core.AgentSession
	disposeErr   error
	disposeCalls int
	rebindSet    bool
}

func (h *fakeRuntimeHost) Session() *core.AgentSession { return h.session }
func (h *fakeRuntimeHost) NewSession(ctx context.Context, options *core.RuntimeNewSessionOptions) (bool, error) {
	return false, nil
}
func (h *fakeRuntimeHost) Fork(ctx context.Context, entryID string, options *core.ForkOptions) (*core.ForkResult, error) {
	return &core.ForkResult{}, nil
}
func (h *fakeRuntimeHost) SwitchSession(ctx context.Context, sessionPath string, options *core.SwitchSessionOptions) (bool, error) {
	return false, nil
}
func (h *fakeRuntimeHost) Dispose() error {
	h.disposeCalls++
	return h.disposeErr
}
func (h *fakeRuntimeHost) SetRebindSession(rebindSession func(session *core.AgentSession) error) {
	h.rebindSet = true
}

// captureStdout swaps os.Stdout for a pipe and returns a read function.
func capturePrintStdout(t *testing.T) func() string {
	t.Helper()
	original := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = writer
	done := make(chan string, 1)
	go func() {
		data, _ := io.ReadAll(reader)
		done <- string(data)
	}()
	return func() string {
		writer.Close()
		out := <-done
		os.Stdout = original
		return out
	}
}

func TestRunPrintMode(t *testing.T) {
	t.Run("emits session shutdown in text mode", func(t *testing.T) {
		reply := &types.AssistantMessage{
			Role:       types.RoleAssistant,
			Content:    []types.Content{&types.TextContent{Kind: types.KindText, Text: "done"}},
			API:        ai.ApiOpenAICompletions,
			Provider:   "openai",
			Model:      "gpt-4o-mini",
			StopReason: types.StopReasonStop,
		}
		var capturedPrompts []string
		session := printTestSession(t, reply, &capturedPrompts)
		host := &fakeRuntimeHost{session: session}

		restore := capturePrintStdout(t)
		exitCode := RunPrintMode(context.Background(), host, PrintModeOptions{
			Mode:           "text",
			InitialMessage: "Say done",
			InitialImages:  []types.ImageContent{{Kind: types.KindImage, MimeType: "image/png", Data: "abc"}},
		})
		output := restore()

		if exitCode != 0 {
			t.Fatalf("exitCode = %d, want 0", exitCode)
		}
		if len(capturedPrompts) != 1 || capturedPrompts[0] != "Say done" {
			t.Fatalf("prompts = %v, want [Say done]", capturedPrompts)
		}
		// pi asserts session_shutdown via the extension runner; pig observes
		// the teardown through the host Dispose call.
		if host.disposeCalls != 1 {
			t.Fatalf("disposeCalls = %d, want 1", host.disposeCalls)
		}
		if !host.rebindSet {
			t.Fatal("expected setRebindSession to be called")
		}
		if !strings.Contains(output, "done") {
			t.Fatalf("output = %q, want it to contain the assistant text", output)
		}
	})

	t.Run("emits session shutdown in json mode", func(t *testing.T) {
		reply := &types.AssistantMessage{
			Role:       types.RoleAssistant,
			Content:    []types.Content{&types.TextContent{Kind: types.KindText, Text: "done"}},
			API:        ai.ApiOpenAICompletions,
			Provider:   "openai",
			Model:      "gpt-4o-mini",
			StopReason: types.StopReasonStop,
		}
		var capturedPrompts []string
		session := printTestSession(t, reply, &capturedPrompts)
		host := &fakeRuntimeHost{session: session}

		exitCode := RunPrintMode(context.Background(), host, PrintModeOptions{
			Mode:     "json",
			Messages: []string{"hello"},
		})

		if exitCode != 0 {
			t.Fatalf("exitCode = %d, want 0", exitCode)
		}
		if len(capturedPrompts) != 1 || capturedPrompts[0] != "hello" {
			t.Fatalf("prompts = %v, want [hello]", capturedPrompts)
		}
		if host.disposeCalls != 1 {
			t.Fatalf("disposeCalls = %d, want 1", host.disposeCalls)
		}
	})

	t.Run("emits session shutdown and returns non-zero on assistant error", func(t *testing.T) {
		reply := &types.AssistantMessage{
			Role:         types.RoleAssistant,
			Content:      []types.Content{},
			API:          ai.ApiOpenAICompletions,
			Provider:     "openai",
			Model:        "gpt-4o-mini",
			StopReason:   types.StopReasonError,
			ErrorMessage: "provider failure",
		}
		session := printTestSession(t, reply, nil)
		// pi's fake session presets state.messages; pig seeds the agent state
		// directly (no prompt is sent in this case).
		session.Agent.State().Messages = []agent.AgentMessage{reply}
		host := &fakeRuntimeHost{session: session}

		exitCode := RunPrintMode(context.Background(), host, PrintModeOptions{Mode: "text"})

		if exitCode != 1 {
			t.Fatalf("exitCode = %d, want 1", exitCode)
		}
		if host.disposeCalls != 1 {
			t.Fatalf("disposeCalls = %d, want 1", host.disposeCalls)
		}
	})
}
