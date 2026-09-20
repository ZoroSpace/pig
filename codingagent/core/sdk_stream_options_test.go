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
	"time"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/auth"
	"github.com/ZoroSpace/pig/ai/types"
	aiutils "github.com/ZoroSpace/pig/ai/utils"
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

	stream := result.Session.Agent.StreamFunction(context.Background(), model, aiutils.NormalizeContext(&types.Context{Messages: []types.Message{}}), requestOptions)
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

// --- v0.86.0 cache-warming additions (pi: test/sdk-stream-options.test.ts) ---

// sdkCreateCacheWarmingSession assembles a session with cache warming in
// idle mode, a prompt-cache model, and a capturing provider (pi:
// createCacheWarmingSession). populate seeds the session manager before
// assembly.
func sdkCreateCacheWarmingSession(t *testing.T, populate func(manager *SessionManager, model *types.Model)) (*AgentSession, func() int, func()) {
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

	model := &types.Model{
		ID:            "warming-model",
		Name:          "Warming Model",
		API:           ai.ApiOpenAICompletions,
		Provider:      "warming-provider",
		BaseURL:       "https://warming.invalid/v1",
		Reasoning:     false,
		Input:         []ai.ModelInput{ai.ModelInputText},
		Cost:          ai.ModelCost{ModelCostRates: ai.ModelCostRates{Input: 10, Output: 50, CacheRead: 0.25, CacheWrite: 12.5}},
		ContextWindow: 128000,
		MaxTokens:     4096,
		PromptCache:   ai.ModelPromptCache{ai.CacheRetentionShort: 300},
	}

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

	providerCalls := 0
	if err := modelRuntime.RegisterProvider(string(model.Provider), &ProviderConfigInput{
		API: model.API,
		StreamSimple: func(ctx context.Context, streamModel *types.Model, llmContext *types.TranscriptContext, providerOptions *ai.SimpleStreamOptions) *types.AssistantMessageEventStream {
			providerCalls++
			stream := ai.NewAssistantMessageEventStream()
			message := &types.AssistantMessage{
				Role:     types.RoleAssistant,
				Content:  []types.Content{types.NewTextContent("ok")},
				API:      model.API,
				Provider: model.Provider,
				Model:    model.ID,
				Usage: types.Usage{
					CacheRead:   100_000,
					TotalTokens: 100_000,
					Cost:        types.Cost{},
				},
				StopReason: types.StopReasonStop,
			}
			stream.Push(types.AssistantMessageEvent{Type: types.EventStart, Partial: message})
			stream.Push(types.AssistantMessageEvent{Type: types.EventDone, Message: message})
			stream.End()
			return stream
		},
		Models: []ProviderConfigInputModel{{
			ID: model.ID, Name: model.Name, API: model.API, Reasoning: model.Reasoning,
			Input: model.Input, Cost: model.Cost, ContextWindow: model.ContextWindow, MaxTokens: model.MaxTokens,
			BaseURL: model.BaseURL,
		}},
	}); err != nil {
		t.Fatal(err)
	}

	sessionManager, err := InMemorySession(cwd, nil)
	if err != nil {
		t.Fatal(err)
	}
	if populate != nil {
		populate(sessionManager, model)
	}

	result, err := CreateAgentSession(context.Background(), &CreateAgentSessionOptions{
		Cwd:             cwd,
		AgentDir:        agentDir,
		Model:           model,
		ModelRuntime:    modelRuntime,
		SettingsManager: InMemorySettingsManager(Settings{"cacheWarming": "idle"}, nil),
		SessionManager:  sessionManager,
	})
	if err != nil {
		t.Fatal(err)
	}

	cleanup := func() {
		result.Session.Dispose()
		modelRuntime.UnregisterProvider(string(model.Provider))
	}
	return result.Session, func() int { return providerCalls }, cleanup
}

// TestCreateAgentSessionSchedulesCacheWarmingAfterRequest ports "schedules
// cache warming after a completed session request".
func TestCreateAgentSessionSchedulesCacheWarmingAfterRequest(t *testing.T) {
	session, providerCalls, cleanup := sdkCreateCacheWarmingSession(t, nil)
	defer cleanup()

	if err := session.Prompt(context.Background(), "test", nil); err != nil {
		t.Fatal(err)
	}
	if providerCalls() != 1 {
		t.Fatalf("provider calls = %d, want 1", providerCalls())
	}
	status, ok := session.CacheWarmingStatus()
	if !ok {
		t.Fatal("cache warming status unavailable")
	}
	if status.NextWarmAt <= time.Now().UnixMilli() {
		t.Fatalf("nextWarmAt = %d, want in the future", status.NextWarmAt)
	}

	// Equivalent shallow copies remain current, but removing the request
	// prefix does not.
	session.Agent.State().Messages = append([]agent.AgentMessage{}, session.Agent.State().Messages...)
	status, _ = session.CacheWarmingStatus()
	if status.NextWarmAt <= time.Now().UnixMilli() {
		t.Fatalf("nextWarmAt after shallow copy = %d, want in the future", status.NextWarmAt)
	}
	session.Agent.State().Messages = session.Agent.State().Messages[1:]
	status, _ = session.CacheWarmingStatus()
	if status.Reason != "conversation context changed" {
		t.Fatalf("reason = %q, want %q", status.Reason, "conversation context changed")
	}
}

// TestCreateAgentSessionWaitsForNextRequestInsteadOfRestoringWarming ports
// "waits for the next request instead of restoring cache warming".
func TestCreateAgentSessionWaitsForNextRequestInsteadOfRestoringWarming(t *testing.T) {
	var storedModel *types.Model
	session, providerCalls, cleanup := sdkCreateCacheWarmingSession(t, func(manager *SessionManager, model *types.Model) {
		storedModel = model
		_, _ = manager.AppendModelChange(string(model.Provider), model.ID)
		_, _ = manager.AppendThinkingLevelChange("off")
		_, _ = manager.AppendMessage(types.UserMessage{
			Role:      types.RoleUser,
			Content:   types.UserContent{String: "test"},
			Timestamp: time.Now().UnixMilli() - 60_000,
		})
		assistant := types.AssistantMessage{
			Role:       types.RoleAssistant,
			Content:    []types.Content{types.NewTextContent("ok")},
			API:        model.API,
			Provider:   model.Provider,
			Model:      model.ID,
			Usage:      types.Usage{CacheRead: 100_000, TotalTokens: 100_000, Cost: types.Cost{}},
			StopReason: types.StopReasonStop,
			Timestamp:  time.Now().UnixMilli() - 59_000,
		}
		_, _ = manager.AppendMessage(assistant)
		_, _ = manager.AppendUsage("cache_warm", string(model.Provider), model.ID, &assistant.Usage, "")
	})
	defer cleanup()

	if providerCalls() != 0 {
		t.Fatalf("provider calls = %d, want 0", providerCalls())
	}
	status, ok := session.CacheWarmingStatus()
	if !ok {
		t.Fatal("cache warming status unavailable")
	}
	if status.State != "inactive" || status.Reason != "waiting for first request" {
		t.Fatalf("status = %+v, want inactive/waiting for first request (model %s)", status, storedModel.ID)
	}
}
