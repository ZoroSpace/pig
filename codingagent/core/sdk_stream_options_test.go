// Ported from pi's test/sdk-stream-options.test.ts and
// test/sdk-openrouter-attribution.test.ts, adapted to pig:
//   - the openai-codex-responses API cases use openai-completions (pig has no
//     codex adapter; Phase 2 decision), and the assertions carry over since
//     both exercise the same sdk option forwarding;
//   - the before_provider_headers hook case needs extension factories
//     (Phase 7) and is skipped with an explicit declaration;
//   - model-registry construction goes through CreateModelRuntime +
//     RegisterProvider (pi's createModelRegistry/getModelRuntime test utils).
package core

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/auth"
	"github.com/ZoroSpace/pig/ai/types"
	"github.com/samber/lo"
)

type sdkStreamCapture struct {
	options *ai.SimpleStreamOptions
}

// sdkCaptureStreamOptions wires a registered provider that captures the
// stream options the sdk assembles (pi: captureStreamOptions).
func sdkCaptureStreamOptions(t *testing.T, settings Settings, requestOptions *types.SimpleStreamOptions, providerHeaders map[string]string, requestHeaders map[string]string, sessionID string) (*ai.SimpleStreamOptions, func()) {
	return sdkCaptureStreamOptionsWithModel(t, nil, settings, requestOptions, providerHeaders, requestHeaders, sessionID)
}

// sdkCaptureStreamOptionsWithModel is the full form; a nil model uses the
// capture-provider default (pi: createModel).
func sdkCaptureStreamOptionsWithModel(t *testing.T, model *types.Model, settings Settings, requestOptions *types.SimpleStreamOptions, providerHeaders map[string]string, requestHeaders map[string]string, sessionID string) (*ai.SimpleStreamOptions, func()) {
	t.Helper()

	tempDir := t.TempDir()
	cwd := filepath.Join(tempDir, "project")
	agentDir := filepath.Join(tempDir, "agent")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}

	if model == nil {
		model = &types.Model{
			ID:            "capture-model",
			Name:          "Capture Model",
			API:           ai.ApiOpenAICompletions,
			Provider:      "capture-provider",
			BaseURL:       "https://capture.invalid/v1",
			Reasoning:     false,
			Input:         []ai.ModelInput{ai.ModelInputText},
			Cost:          ai.ModelCost{},
			ContextWindow: 128000,
			MaxTokens:     4096,
			Headers:       map[string]string{"x-model": "model"},
		}
	}

	settingsManager := InMemorySettingsManager(settings, nil)

	authStorage := NewInMemoryAuthStorage(authStorageData{})
	if _, err := authStorage.Modify(context.Background(), string(model.Provider), func(current auth.Credential) (auth.Credential, error) {
		return &auth.ApiKeyCredential{Key: "test-api-key"}, nil
	}); err != nil {
		t.Fatal(err)
	}
	modelRuntime := CreateModelRuntime(context.Background(), &CreateModelRuntimeOptions{
		Credentials: authStorage,
		ModelsPath:  lo.ToPtr(""),
	})

	capture := &sdkStreamCapture{}
	if err := modelRuntime.RegisterProvider(string(model.Provider), &ProviderConfigInput{
		API:     model.API,
		Headers: providerHeaders,
		StreamSimple: func(ctx context.Context, streamModel *types.Model, llmContext *types.TranscriptContext, providerOptions *ai.SimpleStreamOptions) *types.AssistantMessageEventStream {
			capture.options = providerOptions
			stream := ai.NewAssistantMessageEventStream()
			message := &types.AssistantMessage{
				Role:       types.RoleAssistant,
				Content:    []types.Content{},
				API:        model.API,
				Provider:   "capture-provider",
				Model:      "capture-model",
				Usage:      types.Usage{},
				StopReason: types.StopReasonStop,
			}
			stream.Push(types.AssistantMessageEvent{Type: types.EventStart, Message: message})
			stream.Push(types.AssistantMessageEvent{Type: types.EventDone, Message: message})
			stream.End()
			return stream
		},
		Models: []ProviderConfigInputModel{{
			ID:            model.ID,
			Name:          model.Name,
			API:           model.API,
			Reasoning:     model.Reasoning,
			Input:         model.Input,
			Cost:          model.Cost,
			ContextWindow: model.ContextWindow,
			MaxTokens:     model.MaxTokens,
			BaseURL:       model.BaseURL,
		}},
	}); err != nil {
		t.Fatal(err)
	}

	sessionManager, err := InMemorySession(cwd, nil)
	if err != nil {
		t.Fatal(err)
	}
	if sessionID != "" {
		id := sessionID
		if _, err := sessionManager.NewSession(&NewSessionOptions{ID: &id}); err != nil {
			t.Fatal(err)
		}
	}

	result, err := CreateAgentSession(context.Background(), &CreateAgentSessionOptions{
		Cwd:             cwd,
		AgentDir:        agentDir,
		Model:           model,
		ModelRuntime:    modelRuntime,
		SettingsManager: settingsManager,
		SessionManager:  sessionManager,
	})
	if err != nil {
		t.Fatal(err)
	}

	stream := result.Session.Agent.StreamFunction(context.Background(), model, &types.Context{Messages: []types.Message{}}, requestOptions)
	finalMessage := stream.Result()
	_ = finalMessage

	cleanup := func() {
		result.Session.Dispose()
		modelRuntime.UnregisterProvider(string(model.Provider))
	}
	return capture.options, cleanup
}

func TestCreateAgentSessionStreamOptions(t *testing.T) {
	t.Run("forwards httpIdleTimeoutMs as timeoutMs", func(t *testing.T) {
		options, cleanup := sdkCaptureStreamOptions(t, Settings{"httpIdleTimeoutMs": int64(1234)}, nil, nil, nil, "")
		defer cleanup()
		if options == nil {
			t.Fatal("stream options were not captured")
		}
		if options.TimeoutMs != 1234 {
			t.Fatalf("timeoutMs = %d, want 1234", options.TimeoutMs)
		}
	})

	t.Run("defaults timeoutMs from httpIdleTimeoutMs for all providers", func(t *testing.T) {
		// pi uses the codex api here and the completions api in the previous
		// case; pig runs both through the completions adapter.
		options, cleanup := sdkCaptureStreamOptions(t, Settings{"httpIdleTimeoutMs": int64(1234)}, nil, nil, nil, "")
		defer cleanup()
		if options == nil {
			t.Fatal("stream options were not captured")
		}
		if options.TimeoutMs != 1234 {
			t.Fatalf("timeoutMs = %d, want 1234", options.TimeoutMs)
		}
	})

	t.Run("lets request timeoutMs override httpIdleTimeoutMs", func(t *testing.T) {
		options, cleanup := sdkCaptureStreamOptions(t, Settings{"httpIdleTimeoutMs": int64(1234)}, &types.SimpleStreamOptions{StreamOptions: types.StreamOptions{ProviderRequestOptions: types.ProviderRequestOptions{TimeoutMs: 0}}}, nil, nil, "")
		defer cleanup()
		if options == nil {
			t.Fatal("stream options were not captured")
		}
		if options.TimeoutMs != 0 {
			t.Fatalf("timeoutMs = %d, want 0", options.TimeoutMs)
		}
	})

	t.Run("forwards websocketConnectTimeoutMs from settings", func(t *testing.T) {
		options, cleanup := sdkCaptureStreamOptions(t, Settings{"websocketConnectTimeoutMs": int64(1234)}, nil, nil, nil, "")
		defer cleanup()
		if options == nil {
			t.Fatal("stream options were not captured")
		}
		if options.WebsocketConnectTimeoutMs == nil || *options.WebsocketConnectTimeoutMs != 1234 {
			t.Fatalf("websocketConnectTimeoutMs = %v, want 1234", options.WebsocketConnectTimeoutMs)
		}
	})

	t.Run("lets request websocketConnectTimeoutMs override settings", func(t *testing.T) {
		zero := int64(0)
		options, cleanup := sdkCaptureStreamOptions(t, Settings{"websocketConnectTimeoutMs": int64(1234)}, &types.SimpleStreamOptions{StreamOptions: types.StreamOptions{WebsocketConnectTimeoutMs: &zero}}, nil, nil, "")
		defer cleanup()
		if options == nil {
			t.Fatal("stream options were not captured")
		}
		if options.WebsocketConnectTimeoutMs == nil || *options.WebsocketConnectTimeoutMs != 0 {
			t.Fatalf("websocketConnectTimeoutMs = %v, want 0", options.WebsocketConnectTimeoutMs)
		}
	})

	t.Run("forwards provider retry settings", func(t *testing.T) {
		options, cleanup := sdkCaptureStreamOptions(t, Settings{
			"retry": map[string]any{
				"provider": map[string]any{
					"maxRetries":      int64(2),
					"maxRetryDelayMs": int64(3000),
				},
			},
		}, nil, nil, nil, "")
		defer cleanup()
		if options == nil {
			t.Fatal("stream options were not captured")
		}
		if options.MaxRetries == nil || *options.MaxRetries != 2 {
			t.Fatalf("maxRetries = %v, want 2", options.MaxRetries)
		}
		if options.MaxRetryDelayMs == nil || *options.MaxRetryDelayMs != 3000 {
			t.Fatalf("maxRetryDelayMs = %v, want 3000", options.MaxRetryDelayMs)
		}
	})

	t.Run("runs before_provider_headers on assembled headers without forwarding the transform", func(t *testing.T) {
		// Deferred with the extension batch (Phase 7): the case requires an
		// extension factory registering a before_provider_headers handler.
		t.Skip("extension factories arrive with the extension batch (Phase 7)")
	})
}
