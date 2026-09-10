package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/types"
)

func mustDeepSeekModel(t *testing.T) *types.Model {
	t.Helper()
	models, ok := ai.MODELS[ai.ProviderDeepSeek]
	if !ok {
		t.Fatal("deepseek not in catalog")
	}
	model, ok := models["deepseek-v4-flash"]
	if !ok {
		t.Fatal("deepseek-chat not in catalog")
	}
	return &model
}

// keyedStreamOptions builds StreamOptions with an API key set (APIKey lives on
// the embedded ProviderRequestOptions).
func keyedStreamOptions(key string) types.StreamOptions {
	opts := types.StreamOptions{}
	opts.APIKey = key
	return opts
}

// newMockSSEServer serves the given SSE payload and captures the request body.
func newMockSSEServer(t *testing.T, sse string) (*httptest.Server, *[]map[string]any) {
	t.Helper()
	var captured []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var params map[string]any
		if err := json.Unmarshal(body, &params); err != nil {
			t.Errorf("invalid request body: %v", err)
		}
		captured = append(captured, params)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, sse)
	}))
	t.Cleanup(srv.Close)
	return srv, &captured
}

func collectEvents(t *testing.T, s *types.AssistantMessageEventStream) ([]types.AssistantMessageEvent, types.AssistantMessage) {
	t.Helper()
	var events []types.AssistantMessageEvent
	for event := range s.Events() {
		events = append(events, event)
	}
	return events, s.Result()
}

func TestDeepSeekStreamSimpleText(t *testing.T) {
	model := mustDeepSeekModel(t)
	sse := "" +
		"data: {\"id\":\"chatcmpl-1\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"deepseek-chat\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\"},\"finish_reason\":null}]}\n\n" +
		"data: {\"id\":\"chatcmpl-1\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"deepseek-chat\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Hel\"},\"finish_reason\":null}]}\n\n" +
		"data: {\"id\":\"chatcmpl-1\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"deepseek-chat\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"lo \"},\"finish_reason\":null}]}\n\n" +
		"data: {\"id\":\"chatcmpl-1\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"deepseek-chat\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"world\"},\"finish_reason\":\"stop\"}]}\n\n" +
		"data: {\"id\":\"chatcmpl-1\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"deepseek-chat\",\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":8,\"prompt_cache_hit_tokens\":4,\"completion_tokens_details\":{\"reasoning_tokens\":2}}}\n\n" +
		"data: [DONE]\n\n"
	srv, captured := newMockSSEServer(t, sse)
	model.BaseURL = srv.URL

	var adapter OpenAICompletions
	stream := adapter.StreamSimple(context.Background(), model, &types.Context{
		Messages: []types.Message{
			types.UserMessage{Role: types.RoleUser, Content: types.UserContent{String: "hi"}, Timestamp: 1},
		},
	}, &types.SimpleStreamOptions{
		StreamOptions: keyedStreamOptions("test-key"),
		Reasoning:     types.ThinkingLevelHigh,
	})
	events, result := collectEvents(t, stream)

	if result.StopReason != types.StopReasonStop {
		t.Fatalf("stop reason = %q, want stop", result.StopReason)
	}
	if got := result.Text(); got != "Hello world" {
		t.Fatalf("text = %q, want %q", got, "Hello world")
	}
	if result.Usage.Input != 6 || result.Usage.Output != 8 || result.Usage.CacheRead != 4 || result.Usage.Reasoning == nil || *result.Usage.Reasoning != 2 {
		t.Fatalf("usage = %+v", result.Usage)
	}
	if result.ResponseID != "chatcmpl-1" {
		t.Fatalf("response id = %q", result.ResponseID)
	}
	if len(events) < 5 {
		t.Fatalf("expected at least start + text events, got %d: %v", len(events), events)
	}
	if events[0].Type != types.EventStart {
		t.Fatalf("first event = %q, want start", events[0].Type)
	}
	var textDeltaSeen bool
	for _, e := range events {
		if e.Type == types.EventTextDelta {
			textDeltaSeen = true
		}
	}
	if !textDeltaSeen {
		t.Fatalf("no text_delta events: %v", events)
	}

	if len(*captured) != 1 {
		t.Fatalf("expected 1 request, got %d", len(*captured))
	}
	params := (*captured)[0]
	if params["model"] != "deepseek-v4-flash" {
		t.Fatalf("model = %v", params["model"])
	}
	if params["stream"] != true {
		t.Fatalf("stream = %v", params["stream"])
	}
	// DeepSeek thinking format: thinking enabled + max_tokens field.
	if params["thinking"] == nil {
		t.Fatalf("thinking param missing: %v", params)
	}
	if _, ok := params["max_tokens"]; !ok {
		t.Fatalf("deepseek should use max_tokens: %v", params)
	}
	if params["stream_options"] == nil {
		t.Fatalf("stream_options missing: %v", params)
	}
	// deepseek sets prompt_cache_retention? no — short retention, no openai url.
	if _, ok := params["prompt_cache_key"]; ok {
		t.Fatalf("prompt_cache_key should be absent for deepseek base url: %v", params)
	}
}

func TestDeepSeekStreamWithReasoning(t *testing.T) {
	model := mustDeepSeekModel(t)
	sse := "" +
		"data: {\"id\":\"chatcmpl-r\",\"model\":\"deepseek-chat\",\"choices\":[{\"index\":0,\"delta\":{\"reasoning_content\":\"thinking\"},\"finish_reason\":null}]}\n\n" +
		"data: {\"id\":\"chatcmpl-r\",\"model\":\"deepseek-chat\",\"choices\":[{\"index\":0,\"delta\":{\"reasoning_content\":\" hard\"},\"finish_reason\":null}]}\n\n" +
		"data: {\"id\":\"chatcmpl-r\",\"model\":\"deepseek-chat\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Answer\"},\"finish_reason\":\"stop\"}]}\n\n" +
		"data: {\"id\":\"chatcmpl-r\",\"model\":\"deepseek-chat\",\"choices\":[],\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":4,\"prompt_cache_hit_tokens\":0}}\n\n" +
		"data: [DONE]\n\n"
	srv, _ := newMockSSEServer(t, sse)
	model.BaseURL = srv.URL

	var adapter OpenAICompletions
	stream := adapter.StreamSimple(context.Background(), model, &types.Context{
		Messages: []types.Message{
			types.UserMessage{Role: types.RoleUser, Content: types.UserContent{String: "q"}, Timestamp: 1},
		},
	}, &types.SimpleStreamOptions{
		StreamOptions: keyedStreamOptions("test-key"),
		Reasoning:     types.ThinkingLevelHigh,
	})
	_, result := collectEvents(t, stream)

	if result.Text() != "Answer" {
		t.Fatalf("text = %q", result.Text())
	}
	// Thinking block should carry the accumulated reasoning.
	var thinking string
	for _, c := range result.Content {
		if tc, ok := c.(types.ThinkingContent); ok {
			thinking += tc.Thinking
		}
	}
	if thinking != "thinking hard" {
		t.Fatalf("thinking = %q, want %q", thinking, "thinking hard")
	}
	if result.StopReason != types.StopReasonStop {
		t.Fatalf("stop reason = %q", result.StopReason)
	}
}

func TestDeepSeekStreamToolCalls(t *testing.T) {
	model := mustDeepSeekModel(t)
	sse := "" +
		"data: {\"id\":\"chatcmpl-t\",\"model\":\"deepseek-chat\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"get_weather\",\"arguments\":\"\"}}]},\"finish_reason\":null}]}\n\n" +
		"data: {\"id\":\"chatcmpl-t\",\"model\":\"deepseek-chat\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"{\\\"city\\\":\\\"beiji\"}}]},\"finish_reason\":null}]}\n\n" +
		"data: {\"id\":\"chatcmpl-t\",\"model\":\"deepseek-chat\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"ng\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n" +
		"data: {\"id\":\"chatcmpl-t\",\"model\":\"deepseek-chat\",\"choices\":[],\"usage\":{\"prompt_tokens\":20,\"completion_tokens\":5,\"prompt_cache_hit_tokens\":0}}\n\n" +
		"data: [DONE]\n\n"
	srv, captured := newMockSSEServer(t, sse)
	model.BaseURL = srv.URL

	var adapter OpenAICompletions
	opts := keyedStreamOptions("test-key")
	stream := adapter.Stream(context.Background(), model, &types.Context{
		Messages: []types.Message{
			types.UserMessage{Role: types.RoleUser, Content: types.UserContent{String: "weather in beijing"}, Timestamp: 1},
		},
		Tools: []types.Tool{
			{
				Name:        "get_weather",
				Description: "weather",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}`),
			},
		},
	}, &opts)
	_, result := collectEvents(t, stream)

	if result.StopReason != types.StopReasonToolUse {
		t.Fatalf("stop reason = %q, want toolUse", result.StopReason)
	}
	calls := result.ToolCalls()
	if len(calls) != 1 {
		t.Fatalf("tool calls = %v", calls)
	}
	if calls[0].Name != "get_weather" || calls[0].ID != "call_1" {
		t.Fatalf("tool call = %+v", calls[0])
	}
	if calls[0].Arguments["city"] != "beijing" {
		t.Fatalf("arguments = %v", calls[0].Arguments)
	}
	// Request must carry the tool.
	params := (*captured)[0]
	tools, ok := params["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools = %v", params["tools"])
	}
}

func TestDeepSeekErrorSurfacesInStream(t *testing.T) {
	model := mustDeepSeekModel(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"error":{"message":"Invalid API key","type":"authentication_error"}}`)
	}))
	t.Cleanup(srv.Close)
	model.BaseURL = srv.URL

	var adapter OpenAICompletions
	stream := adapter.StreamSimple(context.Background(), model, &types.Context{
		Messages: []types.Message{
			types.UserMessage{Role: types.RoleUser, Content: types.UserContent{String: "hi"}, Timestamp: 1},
		},
	}, &types.SimpleStreamOptions{
		StreamOptions: keyedStreamOptions("bad-key"),
	})
	_, result := collectEvents(t, stream)
	if result.StopReason != types.StopReasonError {
		t.Fatalf("stop reason = %q, want error", result.StopReason)
	}
	if !strings.Contains(result.ErrorMessage, "401") {
		t.Fatalf("error message = %q, want status 401", result.ErrorMessage)
	}
}

func TestDeepSeekMissingAPIKey(t *testing.T) {
	model := mustDeepSeekModel(t)
	var adapter OpenAICompletions
	stream := adapter.StreamSimple(context.Background(), model, &types.Context{
		Messages: []types.Message{
			types.UserMessage{Role: types.RoleUser, Content: types.UserContent{String: "hi"}, Timestamp: 1},
		},
	}, &types.SimpleStreamOptions{})
	_, result := collectEvents(t, stream)
	if result.StopReason != types.StopReasonError {
		t.Fatalf("stop reason = %q, want error", result.StopReason)
	}
	if !strings.Contains(result.ErrorMessage, "No API key") {
		t.Fatalf("error message = %q", result.ErrorMessage)
	}
}

func TestBuildParamsDeepSeekReasoningOff(t *testing.T) {
	model := mustDeepSeekModel(t)
	srv, captured := newMockSSEServer(t, "data: {\"id\":\"x\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	model.BaseURL = srv.URL

	var adapter OpenAICompletions
	stream := adapter.StreamSimple(context.Background(), model, &types.Context{
		Messages: []types.Message{
			types.UserMessage{Role: types.RoleUser, Content: types.UserContent{String: "hi"}, Timestamp: 1},
		},
	}, &types.SimpleStreamOptions{
		StreamOptions: keyedStreamOptions("test-key"),
	})
	_, result := collectEvents(t, stream)
	if result.StopReason != types.StopReasonStop {
		t.Fatalf("stop reason = %q", result.StopReason)
	}
	params := (*captured)[0]
	// Reasoning off + deepseek: thinking disabled (the catalog model's off
	// level is absent → not nulled, so pi sends {type:"disabled"}).
	thinking, ok := params["thinking"].(map[string]any)
	if !ok || thinking["type"] != "disabled" {
		t.Fatalf("thinking = %v, want disabled", params["thinking"])
	}
	if _, ok := params["reasoning_effort"]; ok {
		t.Fatalf("reasoning_effort should be absent with reasoning off: %v", params)
	}
}

// TestStreamSimpleAbort asserts that cancelling the caller context surfaces
// as StopReason aborted with the abort error message (pi: options.signal
// aborting the in-flight SDK request mid-stream → stopReason "aborted").
func TestStreamSimpleAbort(t *testing.T) {
	model := mustDeepSeekModel(t)
	// Slow server: first chunk arrives immediately, the finish chunk only
	// after a long delay — the caller aborts in between.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, "data: {\"id\":\"x\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"partial\"}}]}\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		select {
		case <-r.Context().Done():
		case <-time.After(10 * time.Second):
			io.WriteString(w, "data: {\"id\":\"x\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
		}
	}))
	t.Cleanup(srv.Close)
	model.BaseURL = srv.URL

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	var adapter OpenAICompletions
	stream := adapter.StreamSimple(ctx, model, &types.Context{
		Messages: []types.Message{
			types.UserMessage{Role: types.RoleUser, Content: types.UserContent{String: "hi"}, Timestamp: 1},
		},
	}, &types.SimpleStreamOptions{
		StreamOptions: keyedStreamOptions("test-key"),
	})
	events, result := collectEvents(t, stream)

	if result.StopReason != types.StopReasonAborted {
		t.Fatalf("stop reason = %q, want aborted", result.StopReason)
	}
	if result.ErrorMessage != "Request aborted" {
		t.Fatalf("error message = %q, want %q", result.ErrorMessage, "Request aborted")
	}
	var sawPartial bool
	for _, e := range events {
		if e.Type == types.EventTextDelta && strings.Contains(e.Delta, "partial") {
			sawPartial = true
		}
	}
	if !sawPartial {
		t.Fatalf("expected text deltas before the abort, got %v", events)
	}
}
