package providers

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/ai/utils"
)

// TestDeepSeekProviderEndToEnd drives the full provider path — CreateProvider
// → StreamSimple → openai-completions adapter — against a mock SSE server via
// a Fetch override, without touching the network.
func TestDeepSeekProviderEndToEnd(t *testing.T) {
	sse := "" +
		"data: {\"id\":\"chatcmpl-e2e\",\"model\":\"deepseek-flash\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"42\"},\"finish_reason\":null}]}\n\n" +
		"data: {\"id\":\"chatcmpl-e2e\",\"model\":\"deepseek-flash\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\" is the answer\"},\"finish_reason\":\"stop\"}]}\n\n" +
		"data: {\"id\":\"chatcmpl-e2e\",\"model\":\"deepseek-flash\",\"choices\":[],\"usage\":{\"prompt_tokens\":7,\"completion_tokens\":3,\"prompt_cache_hit_tokens\":2}}\n\n" +
		"data: [DONE]\n\n"

	var lastPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastPath = r.URL.Path
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, sse)
	}))
	t.Cleanup(srv.Close)

	provider := DeepSeekProvider()
	model, ok := GetBuiltinModel(ai.ProviderDeepSeek, "deepseek-flash")
	if !ok {
		t.Fatal("deepseek-flash not in catalog")
	}
	fetch := func(req *http.Request) (*http.Response, error) {
		rewritten := req.Clone(req.Context())
		u, err := url.Parse(srv.URL)
		if err != nil {
			return nil, err
		}
		// Keep the original path (/chat/completions), only swap host.
		rewritten.URL.Scheme = u.Scheme
		rewritten.URL.Host = u.Host
		return http.DefaultClient.Do(rewritten)
	}

	opts := types.SimpleStreamOptions{}
	opts.APIKey = "test-key"
	opts.Fetch = fetch

	stream := provider.StreamSimple(context.Background(), &model, utils.NormalizeContext(&types.Context{
		Messages: []types.Message{
			types.UserMessage{Role: types.RoleUser, Content: types.UserContent{String: "what is 6*7"}, Timestamp: 1},
		},
	}), &opts)

	result := stream.Result()
	if result.StopReason != types.StopReasonStop {
		t.Fatalf("stop reason = %q, want stop (error: %s)", result.StopReason, result.ErrorMessage)
	}
	if got := result.Text(); got != "42 is the answer" {
		t.Fatalf("text = %q", got)
	}
	if result.Provider != ai.ProviderDeepSeek || result.Model != "deepseek-flash" {
		t.Fatalf("message identity = %s/%s", result.Provider, result.Model)
	}
	if lastPath != "/chat/completions" {
		t.Fatalf("request path = %q, want /chat/completions", lastPath)
	}
}

// Real-API test for the DeepSeek provider (README: 验证与恢复).
//
// Run with:
//
//	DEEPSEEK_API_KEY=sk-xxx go test -run TestDeepSeekRealAPI -v
//
// The key is read from DEEPSEEK_API_KEY. When it is unset or still the "xxx"
// placeholder, the test skips instead of failing.

func TestDeepSeekRealAPI(t *testing.T) {
	apiKey := os.Getenv("DEEPSEEK_API_KEY")
	if apiKey == "" || apiKey == "xxx" {
		t.Skip("DEEPSEEK_API_KEY not set (or still the 'xxx' placeholder); skipping real-API test")
	}

	model, ok := GetBuiltinModel(ai.ProviderDeepSeek, "deepseek-flash")
	if !ok {
		t.Fatal("deepseek-flash not in catalog")
	}

	opts := types.SimpleStreamOptions{}
	opts.APIKey = apiKey
	opts.TimeoutMs = 120_000
	opts.Reasoning = types.ThinkingLevelHigh
	opts.Transport = types.TransportSSE

	stream := DeepSeekProvider().StreamSimple(context.Background(), &model, utils.NormalizeContext(&types.Context{
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
