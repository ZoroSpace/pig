// Ported from pi's test/suite/regressions/5868-rpc-unknown-command-id.test.ts
// and the non-extension subset of test/rpc-prompt-response-semantics.test.ts.
//
// The in-process harness mirrors print_mode_test.go: real AgentSession
// instances with a stub streamFn, an injected rpcIO (writer + reader), and a
// fake runtime host. Extension-driven cases (command handling, input
// interception) are skipped — the extension system is not ported.
package rpc

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/auth"
	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/codingagent/core"
)

func loToPtrRpc[T any](v T) *T { return &v }

// rpcTestResourceLoader is the empty ResourceLoader (pi's fakes return empty
// resource results).
type rpcTestResourceLoader struct{}

func (l *rpcTestResourceLoader) GetExtensions() *core.LoadExtensionsResult {
	return &core.LoadExtensionsResult{Extensions: []*core.Extension{}, Runtime: core.CreateExtensionRuntime()}
}
func (l *rpcTestResourceLoader) GetSkills() ([]core.Skill, []core.ResourceDiagnostic) {
	return []core.Skill{}, []core.ResourceDiagnostic{}
}
func (l *rpcTestResourceLoader) GetPrompts() ([]core.PromptTemplate, []core.ResourceDiagnostic) {
	return []core.PromptTemplate{}, []core.ResourceDiagnostic{}
}
func (l *rpcTestResourceLoader) GetThemes() ([]any, []core.ResourceDiagnostic) { return nil, nil }
func (l *rpcTestResourceLoader) GetAgentsFiles() []core.AgentsFile {
	return []core.AgentsFile{}
}
func (l *rpcTestResourceLoader) GetSystemPrompt() (string, bool)                { return "", false }
func (l *rpcTestResourceLoader) GetSystemPromptSource() (string, bool)          { return "", false }
func (l *rpcTestResourceLoader) GetAppendSystemPrompt() []string                { return []string{} }
func (l *rpcTestResourceLoader) GetAppendSystemPromptSources() []string         { return []string{} }
func (l *rpcTestResourceLoader) ExtendResources(core.ResourceExtensionPaths)    {}
func (l *rpcTestResourceLoader) Reload(*core.ResourceLoaderReloadOptions) error { return nil }

// rpcTestModelRuntime registers openai (key stored when withAuth) and a
// keyless fake-provider for the auth-failure case.
func rpcTestModelRuntime(t *testing.T, withAuth bool) *core.ModelRuntime {
	t.Helper()
	storage := core.NewInMemoryAuthStorageWithCredentials(nil)
	if withAuth {
		if _, err := storage.Modify(context.Background(), "openai", func(current auth.Credential) (auth.Credential, error) {
			return &auth.ApiKeyCredential{Key: "test-key"}, nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	runtime := core.CreateModelRuntime(context.Background(), &core.CreateModelRuntimeOptions{
		Credentials: storage,
		ModelsPath:  loToPtrRpc(""),
	})
	models := []core.ProviderConfigInputModel{
		{
			ID:            "gpt-4o-mini",
			Name:          "GPT-4o mini",
			API:           ai.ApiOpenAICompletions,
			BaseURL:       "http://localhost:0",
			Input:         []ai.ModelInput{ai.ModelInputText},
			ContextWindow: 128000,
			MaxTokens:     4096,
		},
	}
	if err := runtime.RegisterProvider("openai", &core.ProviderConfigInput{
		API:    ai.ApiOpenAICompletions,
		Models: models,
	}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.RegisterProvider("fake-provider", &core.ProviderConfigInput{
		API: ai.ApiOpenAICompletions,
		Models: []core.ProviderConfigInputModel{{
			ID:            "fake-model",
			Name:          "Fake Model",
			API:           ai.ApiOpenAICompletions,
			BaseURL:       "https://example.invalid",
			Input:         []ai.ModelInput{},
			ContextWindow: 0,
			MaxTokens:     0,
		}},
	}); err != nil {
		t.Fatal(err)
	}
	return runtime
}

func rpcTestAssistantMessage(text string) *types.AssistantMessage {
	return &types.AssistantMessage{
		Role:       types.RoleAssistant,
		Content:    []types.Content{types.NewTextContent(text)},
		API:        ai.ApiOpenAICompletions,
		Provider:   "openai",
		Model:      "gpt-4o-mini",
		StopReason: types.StopReasonStop,
	}
}

// rpcTestSession builds a real session whose stream function replies after
// responseDelay (pi: createRuntimeHost's fake session).
func rpcTestSession(t *testing.T, model *types.Model, withAuth bool, responseDelay time.Duration) *core.AgentSession {
	t.Helper()
	streamFn := func(ctx context.Context, callModel *types.Model, llmContext *types.TranscriptContext, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
		stream := ai.NewAssistantMessageEventStream()
		go func() {
			stream.Push(types.AssistantMessageEvent{Type: types.EventStart, Partial: rpcTestAssistantMessage("")})
			if responseDelay > 0 {
				time.Sleep(responseDelay)
			}
			stream.Push(types.AssistantMessageEvent{Type: types.EventDone, Reason: types.StopReasonStop, Message: rpcTestAssistantMessage("done")})
			stream.End()
		}()
		return stream
	}
	agentInstance, err := agent.NewAgent(agent.AgentOptions{
		StreamFn: streamFn,
		InitialState: &agent.AgentState{
			Model: model,
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
		ResourceLoader:  &rpcTestResourceLoader{},
		ModelRuntime:    rpcTestModelRuntime(t, withAuth),
	})
}

// rpcTestRuntimeHost is the fake runtime host (pi: the createRuntimeHost
// object literal).
type rpcTestRuntimeHost struct {
	session    *core.AgentSession
	disposed   bool
	rebindSeen bool
}

func (h *rpcTestRuntimeHost) Session() *core.AgentSession { return h.session }
func (h *rpcTestRuntimeHost) NewSession(ctx context.Context, options *core.RuntimeNewSessionOptions) (bool, error) {
	return true, nil
}
func (h *rpcTestRuntimeHost) Fork(ctx context.Context, entryID string, options *core.ForkOptions) (*core.ForkResult, error) {
	return &core.ForkResult{Cancelled: true}, nil
}
func (h *rpcTestRuntimeHost) SwitchSession(ctx context.Context, sessionPath string, options *core.SwitchSessionOptions) (bool, error) {
	return true, nil
}
func (h *rpcTestRuntimeHost) Dispose() error                                  { h.disposed = true; return nil }
func (h *rpcTestRuntimeHost) SetRebindSession(func(*core.AgentSession) error) { h.rebindSeen = true }

// rpcTestHarness drives runRpcMode in-process with injected IO.
type rpcTestHarness struct {
	t      *testing.T
	host   *rpcTestRuntimeHost
	out    *syncOutput
	writer *io.PipeWriter
	done   chan int
}

type syncOutput struct {
	mu    sync.Mutex
	lines []string
}

func (o *syncOutput) write(s string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.lines = append(o.lines, s)
}

func (o *syncOutput) snapshot() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]string(nil), o.lines...)
}

func startRpcTest(t *testing.T, session *core.AgentSession) *rpcTestHarness {
	t.Helper()
	reader, writer := io.Pipe()
	out := &syncOutput{}
	host := &rpcTestRuntimeHost{session: session}
	done := make(chan int, 1)
	go func() {
		done <- runRpcMode(context.Background(), host, rpcIO{
			write:            out.write,
			waitBackpressure: func() {},
			flush:            func() {},
			input:            reader,
		})
	}()
	h := &rpcTestHarness{t: t, host: host, out: out, writer: writer, done: done}
	t.Cleanup(func() { h.stop() })
	return h
}

func (h *rpcTestHarness) sendLine(line string) {
	h.t.Helper()
	if _, err := h.writer.Write([]byte(line + "\n")); err != nil {
		h.t.Fatal(err)
	}
}

func (h *rpcTestHarness) stop() {
	_ = h.writer.Close()
	select {
	case <-h.done:
	case <-time.After(5 * time.Second):
		h.t.Error("runRpcMode did not return after input EOF")
	}
}

// parsed returns all emitted records parsed as JSON objects.
func (h *rpcTestHarness) parsed() []map[string]any {
	var records []map[string]any
	for _, chunk := range h.out.snapshot() {
		for _, line := range splitLines(chunk) {
			if line == "" {
				continue
			}
			var record map[string]any
			if err := json.Unmarshal([]byte(line), &record); err != nil {
				continue
			}
			records = append(records, record)
		}
	}
	return records
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

func (h *rpcTestHarness) waitFor(desc string, predicate func() bool) {
	h.t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if predicate() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	h.t.Fatalf("timed out waiting for %s; output = %v", desc, h.parsed())
}

func promptResponses(records []map[string]any, id string) []map[string]any {
	var out []map[string]any
	for _, record := range records {
		if record["id"] == id && record["type"] == "response" && record["command"] == "prompt" {
			out = append(out, record)
		}
	}
	return out
}

func TestRpcUnknownCommandPreservesID(t *testing.T) {
	session := rpcTestSession(t, &types.Model{ID: "gpt-4o-mini", Provider: "openai", API: ai.ApiOpenAICompletions}, true, 0)
	h := startRpcTest(t, session)

	h.sendLine(`{"id":"test","type":"foobar"}`)

	h.waitFor("unknown command response", func() bool {
		for _, record := range h.parsed() {
			if record["id"] == "test" && record["type"] == "response" && record["command"] == "foobar" {
				if record["success"] == false && record["error"] == "Unknown command: foobar" {
					return true
				}
			}
		}
		return false
	})
}

func TestRpcPromptResponseSemantics(t *testing.T) {
	t.Run("emits one failure response when prompt preflight rejects", func(t *testing.T) {
		session := rpcTestSession(t, &types.Model{ID: "fake-model", Provider: "fake-provider", API: ai.ApiOpenAICompletions, BaseURL: "https://example.invalid"}, false, 0)
		h := startRpcTest(t, session)

		h.sendLine(`{"id":"b1","type":"prompt","message":"Hello"}`)

		h.waitFor("failed prompt response", func() bool {
			responses := promptResponses(h.parsed(), "b1")
			if len(responses) != 1 {
				return false
			}
			message, _ := responses[0]["error"].(string)
			return responses[0]["success"] == false && strings.Contains(message, "No API key found for fake-provider")
		})
	})

	t.Run("emits one started response when prompt preflight succeeds", func(t *testing.T) {
		session := rpcTestSession(t, &types.Model{ID: "gpt-4o-mini", Provider: "openai", API: ai.ApiOpenAICompletions, BaseURL: "http://localhost:0"}, true, 0)
		h := startRpcTest(t, session)

		h.sendLine(`{"id":"b2","type":"prompt","message":"Hello"}`)

		h.waitFor("started prompt response", func() bool {
			responses := promptResponses(h.parsed(), "b2")
			if len(responses) != 1 {
				return false
			}
			data, _ := responses[0]["data"].(map[string]any)
			return responses[0]["success"] == true && data != nil && data["disposition"] == "started"
		})
	})

	t.Run("emits one queued response when prompt is queued during streaming", func(t *testing.T) {
		session := rpcTestSession(t, &types.Model{ID: "gpt-4o-mini", Provider: "openai", API: ai.ApiOpenAICompletions, BaseURL: "http://localhost:0"}, true, 150*time.Millisecond)
		h := startRpcTest(t, session)

		h.sendLine(`{"id":"b3-start","type":"prompt","message":"Start"}`)
		h.waitFor("first prompt response", func() bool {
			return len(promptResponses(h.parsed(), "b3-start")) == 1
		})

		h.sendLine(`{"id":"b3","type":"prompt","message":"Queue this","streamingBehavior":"followUp"}`)
		h.waitFor("queued prompt response", func() bool {
			responses := promptResponses(h.parsed(), "b3")
			if len(responses) != 1 {
				return false
			}
			data, _ := responses[0]["data"].(map[string]any)
			return responses[0]["success"] == true && data != nil && data["disposition"] == "queued"
		})
	})
}
