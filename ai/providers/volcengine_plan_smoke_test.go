//go:build smoke

// volcengine-plan real-API smoke: excluded from the default `go test` run
// (it needs network + a live key). Mirrors the ai/api *_smoke_test.go
// convention.

package providers

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/ai/utils"
)

// TestVolcenginePlanRealAPI is the real-API smoke (README: 验证与恢复).
// Run with:
//
//	VOLCENGINE_API_KEY=ark-xxx go test -tags=smoke ./ai/providers/ -run TestVolcenginePlanRealAPI -v
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

	stream := VolcenginePlanProvider().StreamSimple(context.Background(), &model, utils.NormalizeContext(&types.Context{
		Messages: []types.Message{
			types.UserMessage{
				Role:      types.RoleUser,
				Content:   types.UserContent{String: "用一句话介绍你自己，然后算一下 6*7 等于多少。"},
				Timestamp: time.Now().UnixMilli(),
			},
		},
	}), &opts)

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
