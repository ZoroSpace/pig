package providers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ZoroSpace/pig/ai/types"
)

// TestVolcenginePlanModelsTable locks the hand-written model table against
// the extension's registry/fallback list (registry/models.json, generated
// from `arkcli plans model-list --plan coding-plan` + `arkcli models get`).
// Note the registry quirk: glm-5-3-flash has display name glm-5.3-flash.
func TestVolcenginePlanModelsTable(t *testing.T) {
	models := VolcenginePlanProvider().GetModels()
	if len(models) != 9 {
		t.Fatalf("GetModels() length = %d, want 9", len(models))
	}
	want := []struct {
		id            string
		name          string
		input         []types.ModelInput
		contextWindow int64
		maxTokens     int64
	}{
		{"doubao-seed-evolving", "doubao-seed-evolving", []types.ModelInput{types.ModelInputText, types.ModelInputImage}, 1024000, 256000},
		{"doubao-seed-2-1-turbo", "doubao-seed-2-1-turbo", []types.ModelInput{types.ModelInputText, types.ModelInputImage}, 256000, 256000},
		{"doubao-seed-2.0-lite", "doubao-seed-2.0-lite", []types.ModelInput{types.ModelInputText, types.ModelInputImage}, 256000, 128000},
		{"glm-5-3-flash", "glm-5.3-flash", []types.ModelInput{types.ModelInputText, types.ModelInputImage}, 1048576, 128000},
		{"glm-5.3", "glm-5.3", []types.ModelInput{types.ModelInputText}, 1048576, 128000},
		{"deepseek-v4-pro", "deepseek-v4-pro", []types.ModelInput{types.ModelInputText}, 1024000, 384000},
		{"deepseek-v4-flash", "deepseek-v4-flash", []types.ModelInput{types.ModelInputText}, 1024000, 384000},
		{"kimi-k2.7-code", "kimi-k2.7-code", []types.ModelInput{types.ModelInputText, types.ModelInputImage}, 256000, 32000},
		{"minimax-m3", "minimax-m3", []types.ModelInput{types.ModelInputText, types.ModelInputImage}, 512000, 128000},
	}
	byID := map[string]types.Model{}
	for _, m := range models {
		byID[m.ID] = m
		if m.API != types.ApiOpenAICompletions {
			t.Errorf("%s: api = %q, want openai-completions", m.ID, m.API)
		}
		if m.Provider != types.ProviderVolcenginePlan {
			t.Errorf("%s: provider = %q, want volcengine-plan", m.ID, m.Provider)
		}
		if m.BaseURL != "https://ark.cn-beijing.volces.com/api/coding/v3" {
			t.Errorf("%s: baseUrl = %q", m.ID, m.BaseURL)
		}
		if !m.Reasoning {
			t.Errorf("%s: reasoning = false, want true", m.ID)
		}
		if m.Cost.Input != 0 || m.Cost.Output != 0 || m.Cost.CacheRead != 0 || m.Cost.CacheWrite != 0 {
			t.Errorf("%s: cost = %+v, want all-zero (subscription quota)", m.ID, m.Cost.ModelCostRates)
		}
		compat, ok := m.Compat.(*types.OpenAICompletionsCompat)
		if !ok || compat == nil {
			t.Fatalf("%s: compat missing or wrong type", m.ID)
		}
		if compat.SupportsDeveloperRole == nil || *compat.SupportsDeveloperRole {
			t.Errorf("%s: compat.supportsDeveloperRole = %v, want false", m.ID, compat.SupportsDeveloperRole)
		}
		if compat.MaxTokensField == nil || *compat.MaxTokensField != types.MaxTokensPlain {
			t.Errorf("%s: compat.maxTokensField = %v, want max_tokens", m.ID, compat.MaxTokensField)
		}
		if compat.SupportsReasoningEffort == nil || *compat.SupportsReasoningEffort {
			t.Errorf("%s: compat.supportsReasoningEffort = %v, want false (ark ignores/suppresses it)", m.ID, compat.SupportsReasoningEffort)
		}
		if compat.ThinkingFormat == nil || *compat.ThinkingFormat != types.ThinkingFormatVolcengine {
			t.Errorf("%s: compat.thinkingFormat = %v, want volcengine", m.ID, compat.ThinkingFormat)
		}
		// glm-5-3-flash, glm-5.3 and kimi-k2.7-code reject thinking.type
		// "disabled" (HTTP 400), so off must be marked unsupported.
		offUnsupported := m.ID == "glm-5-3-flash" || m.ID == "glm-5.3" || m.ID == "kimi-k2.7-code"
		offValue, offPresent := m.ThinkingLevelMap[types.ModelThinkingOff]
		if offUnsupported && (!offPresent || offValue != nil) {
			t.Errorf("%s: ThinkingLevelMap.off = %v (present=%v), want nil (cannot disable thinking)", m.ID, offValue, offPresent)
		}
		if !offUnsupported && offPresent && offValue == nil {
			t.Errorf("%s: ThinkingLevelMap.off = nil, want supported (disabled accepted by ark)", m.ID)
		}
	}
	for _, w := range want {
		m, ok := byID[w.id]
		if !ok {
			t.Fatalf("model %q missing", w.id)
		}
		if m.Name != w.name {
			t.Errorf("%s: name = %q, want %q", w.id, m.Name, w.name)
		}
		if len(m.Input) != len(w.input) || m.Input[0] != w.input[0] {
			t.Errorf("%s: input = %v, want %v", w.id, m.Input, w.input)
		}
		if m.ContextWindow != w.contextWindow {
			t.Errorf("%s: contextWindow = %d, want %d", w.id, m.ContextWindow, w.contextWindow)
		}
		if m.MaxTokens != w.maxTokens {
			t.Errorf("%s: maxTokens = %d, want %d", w.id, m.MaxTokens, w.maxTokens)
		}
		delete(byID, w.id)
	}
	if len(byID) != 0 {
		t.Fatalf("unexpected extra models: %v", byID)
	}
}

// TestVolcenginePlanRequestShape drives the full provider path against a mock
// SSE server and locks the request-body compat surface the extension's
// model.compat implies: max_tokens (not max_completion_tokens), system role
// kept as "system" (supportsDeveloperRole=false), no developer role rewrite.
// Other auto-detected fields (store:false, reasoning_effort) match pi's
// detectCompat behavior for an unknown provider — pi sends the same today.
func TestVolcenginePlanRequestShape(t *testing.T) {
	var captured map[string]any
	var lastPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &captured)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, "data: {\"id\":\"x\",\"model\":\"deepseek-v4-flash\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(srv.Close)

	provider := VolcenginePlanProvider()
	var model types.Model
	for _, m := range provider.GetModels() {
		if m.ID == "deepseek-v4-flash" {
			model = m
			break
		}
	}
	if model.ID == "" {
		t.Fatal("deepseek-v4-flash not in volcengine-plan models")
	}
	model.BaseURL = srv.URL

	opts := types.SimpleStreamOptions{}
	opts.APIKey = "test-key"
	opts.Reasoning = types.ThinkingLevelMedium

	stream := provider.StreamSimple(context.Background(), &model, &types.Context{
		SystemPrompt: "You are helpful.",
		Messages: []types.Message{
			types.UserMessage{Role: types.RoleUser, Content: types.UserContent{String: "hi"}, Timestamp: 1},
		},
	}, &opts)
	result := stream.Result()
	if result.StopReason == types.StopReasonError {
		t.Fatalf("stream error: %s", result.ErrorMessage)
	}

	if lastPath != "/chat/completions" {
		t.Fatalf("request path = %q, want /chat/completions", lastPath)
	}
	if got := captured["model"]; got != "deepseek-v4-flash" {
		t.Fatalf("model = %v", captured["model"])
	}
	if _, ok := captured["max_tokens"]; !ok {
		t.Fatalf("max_tokens missing: %v", captured)
	}
	if _, ok := captured["max_completion_tokens"]; ok {
		t.Fatalf("max_completion_tokens must be absent: %v", captured)
	}
	messages, ok := captured["messages"].([]any)
	if !ok || len(messages) != 2 {
		t.Fatalf("messages = %v, want 2 entries", captured["messages"])
	}
	first, _ := messages[0].(map[string]any)
	if first["role"] != "system" {
		t.Fatalf("system role = %v, want \"system\" (supportsDeveloperRole=false)", first["role"])
	}
}

// TestVolcenginePlanThinkingEncoding locks the volcengine thinking format
// against the live-probed ark behavior (2026-09-10):
//   - level set -> thinking: {type: enabled, clear_thinking: false}, and
//     never reasoning_effort (it suppresses GLM thinking and is otherwise
//     ignored)
//   - off on a model that accepts "disabled" (deepseek-v4-flash) ->
//     thinking: {type: disabled}
//   - off on a model that rejects "disabled" (glm-5-3-flash) -> no thinking
//     param at all (ark hybrid falls back to its own default)
func TestVolcenginePlanThinkingEncoding(t *testing.T) {
	capture := func(t *testing.T, modelID string, level types.ThinkingLevel) map[string]any {
		t.Helper()
		var captured map[string]any
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &captured)
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			io.WriteString(w, "data: {\"id\":\"x\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
		}))
		t.Cleanup(srv.Close)

		var model types.Model
		for _, m := range VolcenginePlanProvider().GetModels() {
			if m.ID == modelID {
				model = m
				break
			}
		}
		if model.ID == "" {
			t.Fatalf("%s not in volcengine-plan models", modelID)
		}
		model.BaseURL = srv.URL

		opts := types.SimpleStreamOptions{}
		opts.APIKey = "test-key"
		opts.Reasoning = level
		stream := VolcenginePlanProvider().StreamSimple(context.Background(), &model, &types.Context{
			Messages: []types.Message{
				types.UserMessage{Role: types.RoleUser, Content: types.UserContent{String: "hi"}, Timestamp: 1},
			},
		}, &opts)
		result := stream.Result()
		if result.StopReason == types.StopReasonError {
			t.Fatalf("stream error: %s", result.ErrorMessage)
		}
		return captured
	}

	t.Run("level high sends enabled without reasoning_effort", func(t *testing.T) {
		params := capture(t, "deepseek-v4-flash", types.ThinkingLevelHigh)
		thinking, ok := params["thinking"].(map[string]any)
		if !ok || thinking["type"] != "enabled" {
			t.Fatalf("thinking = %v, want enabled", params["thinking"])
		}
		if _, present := thinking["clear_thinking"]; !present {
			t.Fatalf("thinking.clear_thinking missing: %v", thinking)
		}
		if _, present := params["reasoning_effort"]; present {
			t.Fatalf("reasoning_effort must never be sent: %v", params)
		}
	})
	t.Run("off on disabled-capable model sends disabled", func(t *testing.T) {
		params := capture(t, "deepseek-v4-flash", "")
		thinking, ok := params["thinking"].(map[string]any)
		if !ok || thinking["type"] != "disabled" {
			t.Fatalf("thinking = %v, want disabled", params["thinking"])
		}
	})
	t.Run("off on reject-disabled model omits thinking param", func(t *testing.T) {
		params := capture(t, "glm-5-3-flash", "")
		if _, present := params["thinking"]; present {
			t.Fatalf("thinking must be absent (ark 400s on disabled): %v", params)
		}
	})
	t.Run("level high on reject-disabled model still sends enabled", func(t *testing.T) {
		params := capture(t, "glm-5-3-flash", types.ThinkingLevelHigh)
		thinking, ok := params["thinking"].(map[string]any)
		if !ok || thinking["type"] != "enabled" {
			t.Fatalf("thinking = %v, want enabled", params["thinking"])
		}
	})
}

// TestVolcenginePlanRealAPI is the real-API smoke (README: 验证与恢复).
// Run with:
//
//	VOLCENGINE_API_KEY=ark-xxx go test -run TestVolcenginePlanRealAPI -v
//
// The key is read from VOLCENGINE_API_KEY; when unset, it falls back to the
// "volcengine-plan" api_key entry in ~/.pi/agent/auth.json (local file, never
// logged). When neither is available the test skips instead of failing.
// VOLCENGINE_SMOKE_MODEL overrides the model (default deepseek-v4-flash).
func TestVolcenginePlanRealAPI(t *testing.T) {
	apiKey := os.Getenv("VOLCENGINE_API_KEY")
	if apiKey == "" {
		if key, err := readPiAuthVolcengineKey(); err == nil && key != "" {
			apiKey = key
		}
	}
	if apiKey == "" {
		t.Skip("no VOLCENGINE_API_KEY (env) and no volcengine-plan entry in ~/.pi/agent/auth.json; skipping real-API test")
	}

	modelID := os.Getenv("VOLCENGINE_SMOKE_MODEL")
	if modelID == "" {
		modelID = "deepseek-v4-flash"
	}
	var model types.Model
	for _, m := range VolcenginePlanProvider().GetModels() {
		if m.ID == modelID {
			model = m
			break
		}
	}
	if model.ID == "" {
		t.Fatalf("%s not in volcengine-plan models", modelID)
	}

	opts := types.SimpleStreamOptions{}
	opts.APIKey = apiKey
	opts.TimeoutMs = 120_000
	opts.Reasoning = types.ThinkingLevelHigh
	opts.Transport = types.TransportSSE

	stream := VolcenginePlanProvider().StreamSimple(context.Background(), &model, &types.Context{
		Messages: []types.Message{
			types.UserMessage{
				Role:      types.RoleUser,
				Content:   types.UserContent{String: "用一句话介绍你自己，然后算一下 6*7 等于多少。"},
				Timestamp: time.Now().UnixMilli(),
			},
		},
	}, &opts)

	start := time.Now()
	eventCount := 0
	var thinking strings.Builder
	var text strings.Builder
	for event := range stream.Events() {
		eventCount++
		switch event.Type {
		case types.EventStart:
			t.Log("== start ==")
		case types.EventTextDelta:
			text.WriteString(event.Delta)
		case types.EventThinkingDelta:
			thinking.WriteString(event.Delta)
		case types.EventTextEnd:
			t.Log("")
		case types.EventError:
			t.Errorf("stream error: %s", event.Message.ErrorMessage)
		}
	}
	result := stream.Result()
	elapsed := time.Since(start)

	t.Logf("== done in %s, events=%d ==", elapsed, eventCount)
	t.Logf("thinking=%q", thinking.String())
	t.Logf("stopReason=%s model=%s", result.StopReason, result.Model)
	t.Logf("text=%q", result.Text())
	t.Logf("usage: in=%d out=%d cacheRead=%d cost=$%.5f",
		result.Usage.Input, result.Usage.Output, result.Usage.CacheRead, result.Usage.Cost.Total)

	if result.StopReason == types.StopReasonError || result.StopReason == types.StopReasonAborted {
		t.Fatalf("stream failed: %s", result.ErrorMessage)
	}
	if result.Text() == "" {
		t.Fatal("empty response text")
	}
}

// readPiAuthVolcengineKey reads the volcengine-plan api_key from the local pi
// auth file, if present (convenience for real-API smoke runs; never logged).
func readPiAuthVolcengineKey() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(filepath.Join(home, ".pi", "agent", "auth.json"))
	if err != nil {
		return "", err
	}
	var auth struct {
		VolcenginePlan struct {
			Type string `json:"type"`
			Key  string `json:"key"`
		} `json:"volcengine-plan"`
	}
	if err := json.Unmarshal(data, &auth); err != nil {
		return "", err
	}
	if auth.VolcenginePlan.Type != "api_key" {
		return "", nil
	}
	return auth.VolcenginePlan.Key, nil
}
