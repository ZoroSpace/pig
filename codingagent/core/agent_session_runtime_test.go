// Ported from pi's test/agent-session-runtime-events.test.ts, adapted to the
// no-extension path: the four pi cases observe lifecycle events through
// extension factories, which arrive with the extension batch (Phase 7). The
// portable subset — rebind/beforeSessionInvalidate ordering and the
// new-session runtime replacement — is covered here; the event-sequence
// assertions (session_before_switch/session_before_fork/session_shutdown
// payloads and cancellation) are skipped until extensions can register
// handlers.
package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/types"
	"github.com/samber/lo"
)

func runtimeEventsModel() *types.Model {
	return &types.Model{
		ID:            "gpt-4o-mini",
		Name:          "GPT-4o mini",
		API:           ai.ApiOpenAICompletions,
		Provider:      "openai",
		BaseURL:       "http://localhost:0",
		Input:         []ai.ModelInput{ai.ModelInputText},
		ContextWindow: 128000,
		MaxTokens:     4096,
	}
}

func runtimeEventsStreamFn(reply string) agent.StreamFn {
	return func(ctx context.Context, model *types.Model, llmContext *types.TranscriptContext, options *types.SimpleStreamOptions) *types.AssistantMessageEventStream {
		message := &types.AssistantMessage{
			Role:       types.RoleAssistant,
			Content:    []types.Content{types.NewTextContent(reply)},
			API:        model.API,
			Provider:   model.Provider,
			Model:      model.ID,
			Usage:      types.Usage{Cost: types.Cost{}},
			StopReason: types.StopReasonStop,
			Timestamp:  0,
		}
		stream := types.NewAssistantMessageEventStream()
		stream.Push(types.AssistantMessageEvent{Type: types.EventStart, Partial: message})
		stream.Push(types.AssistantMessageEvent{Type: types.EventDone, Reason: types.StopReasonStop, Message: message})
		stream.End()
		return stream
	}
}

// newRuntimeHostForTest builds one runtime host over the model-runtime stub
// (pi: createRuntimeForTest / registerFauxProvider).
func newRuntimeHostForTest(t *testing.T, cwd string, sessionManager *SessionManager) *AgentSessionRuntime {
	t.Helper()
	agentDir := filepath.Join(t.TempDir(), "agent")
	modelRuntime := CreateModelRuntime(context.Background(), &CreateModelRuntimeOptions{
		Credentials: NewInMemoryAuthStorageWithCredentials(map[string]string{"openai": "test-key"}),
		ModelsPath:  lo.ToPtr(""),
	})
	if err := modelRuntime.RegisterProvider("openai", &ProviderConfigInput{
		API: ai.ApiOpenAICompletions,
		Models: []ProviderConfigInputModel{{
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
	createRuntime := CreateAgentSessionRuntimeFactory(func(options CreateAgentSessionRuntimeFactoryOptions) (*CreateAgentSessionRuntimeResult, error) {
		services, err := CreateAgentSessionServices(context.Background(), CreateAgentSessionServicesOptions{
			Cwd:          options.Cwd,
			AgentDir:     options.AgentDir,
			ModelRuntime: modelRuntime,
			ResourceLoaderOptions: &DefaultResourceLoaderOptions{
				NoSkills:          true,
				NoPromptTemplates: true,
				NoThemes:          true,
				NoContextFiles:    true,
			},
		})
		if err != nil {
			return nil, err
		}
		result, err := CreateAgentSessionFromServices(context.Background(), CreateAgentSessionFromServicesOptions{
			Services:          services,
			SessionManager:    options.SessionManager,
			SessionStartEvent: options.SessionStartEvent,
			Model:             runtimeEventsModel(),
		})
		if err != nil {
			return nil, err
		}
		// Swap in the stub stream function (pi: registerFauxProvider).
		result.Session.Agent.StreamFunction = runtimeEventsStreamFn("reply")
		return &CreateAgentSessionRuntimeResult{
			CreateAgentSessionResult: *result,
			Services:                 services,
			Diagnostics:              services.Diagnostics,
		}, nil
	})
	runtimeHost, err := CreateAgentSessionRuntime(createRuntime, CreateAgentSessionRuntimeFactoryOptions{
		Cwd:            cwd,
		AgentDir:       agentDir,
		SessionManager: sessionManager,
	})
	if err != nil {
		t.Fatal(err)
	}
	return runtimeHost
}

// TestAgentSessionRuntimeSessionReplacement exercises the runtime replacement
// seams (pi: suite/agent-session-runtime.test.ts).
func TestAgentSessionRuntimeSessionReplacement(t *testing.T) {
	t.Run("runs beforeSessionInvalidate before rebindSession on replacement", func(t *testing.T) {
		cwd := t.TempDir()
		sessionManager, err := CreateSessionManager(cwd, "", nil)
		if err != nil {
			t.Fatal(err)
		}
		runtimeHost := newRuntimeHostForTest(t, cwd, sessionManager)
		defer runtimeHost.Dispose()

		var phases []string
		oldSession := runtimeHost.Session()
		runtimeHost.SetBeforeSessionInvalidate(func() {
			phases = append(phases, "beforeSessionInvalidate")
		})
		runtimeHost.SetRebindSession(func(session *AgentSession) error {
			phases = append(phases, "rebindSession")
			return nil
		})

		cancelled, err := runtimeHost.NewSession(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		if cancelled {
			t.Fatal("newSession was cancelled")
		}

		// pi's sequence is [session_shutdown, beforeSessionInvalidate,
		// rebindSession]; the shutdown entry needs extension handlers and is
		// deferred with Phase 7.
		if strings.Join(phases, ",") != "beforeSessionInvalidate,rebindSession" {
			t.Fatalf("phases = %v", phases)
		}
		if runtimeHost.Session() == oldSession {
			t.Fatal("expected the session to be replaced")
		}
		if oldSession == nil {
			t.Fatal("old session missing")
		}
	})

	t.Run("new session replaces the session file", func(t *testing.T) {
		cwd := t.TempDir()
		sessionManager, err := CreateSessionManager(cwd, "", nil)
		if err != nil {
			t.Fatal(err)
		}
		runtimeHost := newRuntimeHostForTest(t, cwd, sessionManager)
		defer runtimeHost.Dispose()

		ctx := context.Background()
		if err := runtimeHost.Session().Prompt(ctx, "hello", nil); err != nil {
			t.Fatal(err)
		}
		originalSessionFile := runtimeHost.Session().SessionFile()
		if originalSessionFile == "" {
			t.Fatal("expected a persisted session file")
		}

		cancelled, err := runtimeHost.NewSession(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if cancelled {
			t.Fatal("newSession was cancelled")
		}
		secondSessionFile := runtimeHost.Session().SessionFile()
		if secondSessionFile == "" {
			t.Fatal("expected the replacement session to be persisted")
		}
		if secondSessionFile == originalSessionFile {
			t.Fatal("expected a new session file")
		}
	})
}

func TestAgentSessionRuntimeImportPreservesSameNameSession(t *testing.T) {
	cwd := t.TempDir()
	sessionManager, err := CreateSessionManager(cwd, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	runtimeHost := newRuntimeHostForTest(t, cwd, sessionManager)
	defer runtimeHost.Dispose()

	sessionDir := runtimeHost.Session().SessionManager.GetSessionDir()
	importDir := filepath.Join(cwd, "import")
	filename := "collision.jsonl"
	storedPath := filepath.Join(sessionDir, filename)
	importPath := filepath.Join(importDir, filename)

	storedSession := mustJSONLines(map[string]any{
		"type": "session", "version": 3, "id": "stored",
		"timestamp": "2025-01-01T00:00:00Z", "cwd": cwd,
	}) + "\n"
	importedSession := mustJSONLines(map[string]any{
		"type": "session", "version": 3, "id": "imported",
		"timestamp": "2025-01-01T00:00:00Z", "cwd": cwd,
	}) + "\n"

	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(importDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(storedPath, []byte(storedSession), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(importPath, []byte(importedSession), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := runtimeHost.ImportFromJsonl(context.Background(), importPath, nil); err != nil {
		t.Fatal(err)
	}

	stored, err := os.ReadFile(storedPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(stored) != storedSession {
		t.Fatalf("stored session was overwritten: %q", string(stored))
	}
	activeFile := runtimeHost.Session().SessionFile()
	if activeFile == storedPath {
		t.Fatal("expected the imported session to get a unique file")
	}
	active, err := os.ReadFile(activeFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(active), `"id":"imported"`) {
		t.Fatalf("active session = %q", string(active))
	}
}
