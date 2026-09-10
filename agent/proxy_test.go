// pi: proxy.test.ts — streamProxy tests (v0.85.1; includes the regression
// tests for pi issue #8996). pi stubs global fetch; pig points ProxyURL at
// an httptest server.
package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ZoroSpace/pig/ai/types"
)

var proxyTestModel = types.Model{
	ID:        "gpt-5.4",
	Name:      "GPT-5.4",
	API:       types.Api("openai-responses"),
	Provider:  "openai",
	BaseURL:   "https://api.openai.com/v1",
	Reasoning: true,
}

func proxyTestUsage() types.Usage {
	return types.Usage{
		Cost: types.Cost{},
	}
}

// newProxySSEServer serves the given SSE payload and does not inspect the
// request.
func newProxySSEServer(t *testing.T, sse string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(sse))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// proxySSE serializes proxy events into the SSE wire shape the server sends
// (data: <json>\n\n), matching pi's `data: ${JSON.stringify(event)}\n\n`.
func proxySSE(t *testing.T, events ...ProxyAssistantMessageEvent) string {
	t.Helper()
	var b strings.Builder
	for _, event := range events {
		payload, err := json.Marshal(event)
		if err != nil {
			t.Fatalf("marshal proxy event: %v", err)
		}
		b.WriteString("data: ")
		b.WriteString(string(payload))
		b.WriteString("\n\n")
	}
	return b.String()
}

func collectProxyEvents(t *testing.T, s *types.AssistantMessageEventStream) ([]types.AssistantMessageEvent, types.AssistantMessage) {
	t.Helper()
	var events []types.AssistantMessageEvent
	for event := range s.Events() {
		events = append(events, event)
	}
	return events, s.Result()
}

func TestStreamProxyPreservesToolCallMetadataFromToolcallEnd(t *testing.T) {
	usage := proxyTestUsage()
	body := proxySSE(t,
		ProxyAssistantMessageEvent{Type: "start"},
		ProxyAssistantMessageEvent{Type: "toolcall_start", ContentIndex: 0, ID: "call_test|fc_test", ToolName: "lookup"},
		ProxyAssistantMessageEvent{Type: "toolcall_delta", ContentIndex: 0, Delta: `{"value":"hello"}`},
		ProxyAssistantMessageEvent{Type: "toolcall_end", ContentIndex: 0, ToolCall: &types.ToolCall{
			Kind:      types.KindToolCall,
			ID:        "call_test|fc_test",
			Name:      "lookup",
			Arguments: map[string]any{"value": "hello"},
			Namespace: "dynamic_tools",
		}},
		ProxyAssistantMessageEvent{Type: "done", Reason: types.StopReasonToolUse, Usage: usage},
	)
	srv := newProxySSEServer(t, body)

	stream := StreamProxy(context.Background(), &proxyTestModel, &types.Context{}, &ProxyStreamOptions{
		AuthToken: "test-token",
		ProxyURL:  srv.URL,
	})
	events, result := collectProxyEvents(t, stream)

	var endEvent *types.AssistantMessageEvent
	for i := range events {
		if events[i].Type == types.EventToolCallEnd {
			endEvent = &events[i]
		}
	}
	if endEvent == nil {
		t.Fatalf("no toolcall_end event in %v", events)
	}
	if endEvent.ToolCall == nil || endEvent.ToolCall.Namespace != "dynamic_tools" {
		t.Fatalf("toolcall_end toolCall = %+v, want namespace dynamic_tools", endEvent.ToolCall)
	}
	if len(result.Content) == 0 {
		t.Fatalf("result has no content")
	}
	toolCall, ok := result.Content[0].(types.ToolCall)
	if !ok {
		t.Fatalf("result.content[0] = %T, want ToolCall", result.Content[0])
	}
	if toolCall.Arguments["value"] != "hello" || toolCall.Namespace != "dynamic_tools" {
		t.Fatalf("result toolCall = %+v", toolCall)
	}
	if result.StopReason != types.StopReasonToolUse {
		t.Fatalf("stop reason = %q, want toolUse", result.StopReason)
	}
}

// Regression tests for https://github.com/earendil-works/pi/issues/8996

func TestStreamProxyProcessesTerminalMetadataWhenNotNewlineTerminated(t *testing.T) {
	usage := proxyTestUsage()
	start := `data: {"type":"start"}` + "\n\n"
	donePayload, err := json.Marshal(ProxyAssistantMessageEvent{
		Type:                  "done",
		Reason:                types.StopReasonStop,
		Usage:                 usage,
		ProviderThinkingLevel: "high",
	})
	if err != nil {
		t.Fatal(err)
	}
	// The done event is deliberately not newline-terminated.
	body := start + "data: " + string(donePayload)
	srv := newProxySSEServer(t, body)

	stream := StreamProxy(context.Background(), &proxyTestModel, &types.Context{}, &ProxyStreamOptions{
		AuthToken: "test-token",
		ProxyURL:  srv.URL,
	})
	events, result := collectProxyEvents(t, stream)

	var eventTypes []types.AssistantMessageEventType
	for _, event := range events {
		eventTypes = append(eventTypes, event.Type)
	}
	if len(eventTypes) != 2 || eventTypes[0] != types.EventStart || eventTypes[1] != types.EventDone {
		t.Fatalf("events = %v, want [start done]", eventTypes)
	}
	if result.StopReason != types.StopReasonStop {
		t.Fatalf("stop reason = %q, want stop", result.StopReason)
	}
	if result.ProviderThinkingLevel != "high" {
		t.Fatalf("providerThinkingLevel = %q, want high", result.ProviderThinkingLevel)
	}
}

func TestStreamProxyEmitsErrorWhenStreamEndsWithoutTerminalEvent(t *testing.T) {
	body := `data: {"type":"start"}` + "\n\n"
	srv := newProxySSEServer(t, body)

	stream := StreamProxy(context.Background(), &proxyTestModel, &types.Context{}, &ProxyStreamOptions{
		AuthToken: "test-token",
		ProxyURL:  srv.URL,
	})
	events, result := collectProxyEvents(t, stream)

	var eventTypes []types.AssistantMessageEventType
	for _, event := range events {
		eventTypes = append(eventTypes, event.Type)
	}
	if len(eventTypes) != 2 || eventTypes[0] != types.EventStart || eventTypes[1] != types.EventError {
		t.Fatalf("events = %v, want [start error]", eventTypes)
	}
	if result.StopReason != types.StopReasonError {
		t.Fatalf("stop reason = %q, want error", result.StopReason)
	}
	if !strings.Contains(result.ErrorMessage, "Connection closed by proxy server") {
		t.Fatalf("errorMessage = %q, want it to contain \"Connection closed by proxy server\"", result.ErrorMessage)
	}
}
