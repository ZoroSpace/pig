// pi: openai-completions-reasoning-details.test.ts — reasoning_details
// streaming + replay tests (v0.85.1; includes the delta-merge behavior).
// pi mocks the openai SDK; pig serves chunk sequences from an httptest SSE
// server and asserts on the captured request payloads.
package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/ai/utils"
)

// mustJSONDetail decodes a reasoning detail literal so numbers unmarshal as
// float64 exactly like the streamed JSON.
func mustJSONDetail(t *testing.T, literal string) map[string]any {
	t.Helper()
	var detail map[string]any
	if err := json.Unmarshal([]byte(literal), &detail); err != nil {
		t.Fatalf("bad detail literal: %v", err)
	}
	return detail
}

func mustJSONDetails(t *testing.T, literals ...string) []any {
	t.Helper()
	var out []any
	for _, literal := range literals {
		out = append(out, mustJSONDetail(t, literal))
	}
	return out
}

func mustJSONMarshal(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

// reasoningDetailsTestModel mirrors the pi test model (openrouter provider,
// openai-completions api).
func reasoningDetailsTestModel(t *testing.T, serverURL string) *types.Model {
	t.Helper()
	return &types.Model{
		ID:            "google/gemini-test",
		Name:          "Gemini Test",
		API:           types.ApiOpenAICompletions,
		Provider:      "openrouter",
		BaseURL:       serverURL,
		Reasoning:     true,
		ContextWindow: 100_000,
		MaxTokens:     4096,
	}
}

func reasoningDetailsChunk(t *testing.T, delta string, finishReason string) string {
	t.Helper()
	return "data: " + `{"id":"chatcmpl-test","model":"google/gemini-test","choices":[{"index":0,"delta":` + delta + `,"finish_reason":` + finishReason + `}]}` + "\n\n"
}

// reasoningDetailsServer serves a fixed SSE payload and captures every
// request payload (first call streams, second call replays).
func reasoningDetailsServer(t *testing.T, sse string) (*httptest.Server, *[]map[string]any) {
	t.Helper()
	return newMockSSEServer(t, sse)
}

func runReasoningDetailsStream(t *testing.T, srv *httptest.Server, messages []types.Message) types.AssistantMessage {
	t.Helper()
	model := reasoningDetailsTestModel(t, srv.URL)
	var adapter OpenAICompletions
	opts := types.SimpleStreamOptions{StreamOptions: keyedStreamOptions("test")}
	_, result := collectEvents(t, adapter.StreamSimple(context.Background(), model, utils.NormalizeContext(&types.Context{
		Messages: messages,
		Tools: []types.Tool{
			{Name: "read", Description: "Read a file", Parameters: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`)},
		},
	}), &opts))
	return result
}

// getAssistantPayload finds the assistant message in a captured request body
// (pi: getAssistantPayload).
func getAssistantPayload(t *testing.T, payload map[string]any) map[string]any {
	t.Helper()
	messages, ok := payload["messages"].([]any)
	if !ok {
		t.Fatalf("payload has no messages array: %v", payload)
	}
	for _, message := range messages {
		if m, ok := message.(map[string]any); ok && m["role"] == "assistant" {
			return m
		}
	}
	t.Fatalf("no assistant message in payload: %v", payload)
	return nil
}

func findThinkingBlock(t *testing.T, message types.AssistantMessage) types.ThinkingContent {
	t.Helper()
	for _, block := range message.Content {
		if thinking, ok := block.(types.ThinkingContent); ok {
			return thinking
		}
	}
	t.Fatalf("no thinking block in content: %v", message.Content)
	return types.ThinkingContent{}
}

func findToolCallBlock(t *testing.T, message types.AssistantMessage) types.ToolCall {
	t.Helper()
	for _, block := range message.Content {
		if toolCall, ok := block.(types.ToolCall); ok {
			return toolCall
		}
	}
	t.Fatalf("no toolCall block in content: %v", message.Content)
	return types.ToolCall{}
}

// assertReplayedReasoningDetails runs the replay request and compares the
// captured assistant payload's reasoning_details against the expected
// sequence.
func assertReplayedReasoningDetails(t *testing.T, srv *httptest.Server, captured *[]map[string]any, assistantMessage types.AssistantMessage, expectedDetails []any) {
	t.Helper()
	_ = runReasoningDetailsStream(t, srv, []types.Message{assistantMessage})
	if len(*captured) != 2 {
		t.Fatalf("expected 2 requests, got %d", len(*captured))
	}
	payload := getAssistantPayload(t, (*captured)[1])
	details, ok := payload["reasoning_details"].([]any)
	if !ok {
		t.Fatalf("replay payload has no reasoning_details: %v", payload)
	}
	expected := mustJSONMarshal(t, expectedDetails)
	got := mustJSONMarshal(t, details)
	if got != expected {
		t.Fatalf("replayed reasoning_details = %s, want %s", got, expected)
	}
}

// reasoningToolCallDelta is the read-tool call chunk shared by the tests.
func reasoningToolCallDelta(t *testing.T) string {
	t.Helper()
	return reasoningDetailsChunk(t, `{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"read","arguments":"{\"path\":\"README.md\"}"}}]}`, "null")
}

func TestReasoningDetailsPreservedInThinkingSignature(t *testing.T) {
	const encryptedDetail = `{"type":"reasoning.encrypted","id":"call_1","data":"encrypted-signature"}`
	body := "" +
		reasoningDetailsChunk(t, `{"reasoning_details":[`+encryptedDetail+`]}`, "null") +
		reasoningToolCallDelta(t) +
		reasoningDetailsChunk(t, `{}`, `"tool_calls"`)
	srv, captured := reasoningDetailsServer(t, body)

	assistantMessage := runReasoningDetailsStream(t, srv, nil)

	expectedDetails := mustJSONDetails(t, encryptedDetail)
	thinking := findThinkingBlock(t, assistantMessage)
	if thinking.Thinking != "" || thinking.ThinkingSignature != mustJSONMarshal(t, expectedDetails) {
		t.Fatalf("thinking = %+v, want empty thinking with signature %s", thinking, mustJSONMarshal(t, expectedDetails))
	}
	toolCall := findToolCallBlock(t, assistantMessage)
	if toolCall.ID != "call_1" || toolCall.Name != "read" {
		t.Fatalf("toolCall = %+v", toolCall)
	}
	if toolCall.Arguments["path"] != "README.md" {
		t.Fatalf("toolCall arguments = %v", toolCall.Arguments)
	}

	// Replay: the reasoning_details sequence goes back in its original order.
	assertReplayedReasoningDetails(t, srv, captured, assistantMessage, expectedDetails)
}

func TestReasoningDetailsFallsBackToEncryptedToolCallSignatures(t *testing.T) {
	const encryptedDetail = `{"type":"reasoning.encrypted","id":"call_1","data":"encrypted-signature"}`
	body := "" +
		reasoningDetailsChunk(t, `{"reasoning_details":[`+encryptedDetail+`]}`, "null") +
		reasoningToolCallDelta(t) +
		reasoningDetailsChunk(t, `{}`, `"tool_calls"`)
	srv, captured := reasoningDetailsServer(t, body)

	assistantMessage := runReasoningDetailsStream(t, srv, nil)

	// Strip the thinking block and move the detail onto the tool call's
	// thought signature (older stored assistant messages).
	var content []types.Content
	for _, block := range assistantMessage.Content {
		if _, ok := block.(types.ThinkingContent); !ok {
			content = append(content, block)
		}
	}
	toolCall := findToolCallBlock(t, assistantMessage)
	toolCall.ThoughtSignature = encryptedDetail
	content = append(content, toolCall)
	assistantMessage.Content = content

	assertReplayedReasoningDetails(t, srv, captured, assistantMessage, mustJSONDetails(t, encryptedDetail))
}

func TestReasoningDetailsPreservesSignedTextAndSummaryInOriginalSequence(t *testing.T) {
	const signedTextDetail = `{"type":"reasoning.text","text":"I should call the read tool.","signature":"sha256:signed-text","id":"reasoning-text-1","format":"anthropic-claude-v1","index":0}`
	const encryptedDetail = `{"type":"reasoning.encrypted","id":"call_1","data":"encrypted-signature"}`
	const summaryDetail = `{"type":"reasoning.summary","summary":"Decided to inspect the requested file.","id":"reasoning-summary-1","format":"anthropic-claude-v1","index":1}`
	signedText := mustJSONDetail(t, signedTextDetail)
	body := "" +
		reasoningDetailsChunk(t, `{"reasoning":`+mustJSONMarshal(t, signedText["text"].(string))+`,"reasoning_details":[`+signedTextDetail+`]}`, "null") +
		reasoningDetailsChunk(t, `{"reasoning_details":[`+encryptedDetail+`,`+summaryDetail+`]}`, "null") +
		reasoningToolCallDelta(t) +
		reasoningDetailsChunk(t, `{}`, `"tool_calls"`)
	srv, captured := reasoningDetailsServer(t, body)

	assistantMessage := runReasoningDetailsStream(t, srv, nil)

	expectedDetails := mustJSONDetails(t, signedTextDetail, encryptedDetail, summaryDetail)
	thinking := findThinkingBlock(t, assistantMessage)
	if thinking.Thinking != signedText["text"].(string) {
		t.Fatalf("thinking = %q, want %q", thinking.Thinking, signedText["text"].(string))
	}
	if thinking.ThinkingSignature != mustJSONMarshal(t, expectedDetails) {
		t.Fatalf("thinkingSignature = %s, want %s", thinking.ThinkingSignature, mustJSONMarshal(t, expectedDetails))
	}

	_ = runReasoningDetailsStream(t, srv, []types.Message{assistantMessage})
	if len(*captured) != 2 {
		t.Fatalf("expected 2 requests, got %d", len(*captured))
	}
	payload := getAssistantPayload(t, (*captured)[1])
	details, ok := payload["reasoning_details"].([]any)
	if !ok {
		t.Fatalf("replay payload has no reasoning_details: %v", payload)
	}
	if _, present := payload["reasoning"]; present {
		t.Fatalf("replay payload must not carry a reasoning field: %v", payload)
	}
	if got := mustJSONMarshal(t, details); got != mustJSONMarshal(t, expectedDetails) {
		t.Fatalf("replayed reasoning_details = %s, want %s", got, mustJSONMarshal(t, expectedDetails))
	}
}

// Regression coverage for the v0.85.1 delta-merge change: OpenRouter streams
// reasoning_details as deltas; consecutive text/summary deltas merge into
// logical entries while encrypted entries stay discrete.
func TestReasoningDetailsMergesConsecutiveTextAndSummaryDeltas(t *testing.T) {
	const encryptedDetail = `{"type":"reasoning.encrypted","id":"call_1","data":"encrypted-signature"}`
	body := "" +
		reasoningDetailsChunk(t, `{"reasoning_details":[{"type":"reasoning.text","text":"The","index":0}]}`, "null") +
		reasoningDetailsChunk(t, `{"reasoning_details":[{"type":"reasoning.text","text":" user wants the time.","signature":"sha256:text-signature","format":"openai-responses-v1","index":0}]}`, "null") +
		reasoningDetailsChunk(t, `{"reasoning_details":[{"type":"reasoning.summary","summary":"Looked","index":0}]}`, "null") +
		reasoningDetailsChunk(t, `{"reasoning_details":[{"type":"reasoning.summary","summary":" up time.","format":"openai-responses-v1","index":0}]}`, "null") +
		reasoningDetailsChunk(t, `{"reasoning_details":[`+encryptedDetail+`]}`, "null") +
		reasoningDetailsChunk(t, `{"reasoning_details":[{"type":"reasoning.summary","summary":"After encrypted block.","format":"openai-responses-v1","index":0}]}`, "null") +
		reasoningToolCallDelta(t) +
		reasoningDetailsChunk(t, `{}`, `"tool_calls"`)
	srv, captured := reasoningDetailsServer(t, body)

	assistantMessage := runReasoningDetailsStream(t, srv, nil)

	expectedDetails := mustJSONDetails(t,
		`{"type":"reasoning.text","text":"The user wants the time.","index":0,"signature":"sha256:text-signature","format":"openai-responses-v1"}`,
		`{"type":"reasoning.summary","summary":"Looked up time.","index":0,"format":"openai-responses-v1"}`,
		encryptedDetail,
		`{"type":"reasoning.summary","summary":"After encrypted block.","format":"openai-responses-v1","index":0}`,
	)
	thinking := findThinkingBlock(t, assistantMessage)
	if thinking.Thinking != "" {
		t.Fatalf("thinking = %q, want empty", thinking.Thinking)
	}
	if thinking.ThinkingSignature != mustJSONMarshal(t, expectedDetails) {
		t.Fatalf("thinkingSignature = %s, want %s", thinking.ThinkingSignature, mustJSONMarshal(t, expectedDetails))
	}

	assertReplayedReasoningDetails(t, srv, captured, assistantMessage, expectedDetails)
}

// pi: openai-completions-vllm-priority.test.ts — compat.vllmPriority becomes
// the top-level priority request field.
func TestVllmPrioritySentAsTopLevelRequestField(t *testing.T) {
	srv, captured := newMockSSEServer(t, reasoningDetailsChunk(t, `{}`, `"stop"`))
	model := reasoningDetailsTestModel(t, srv.URL)
	priority := 10.0
	model.Compat = &types.OpenAICompletionsCompat{VllmPriority: &priority}
	var adapter OpenAICompletions
	opts := types.SimpleStreamOptions{StreamOptions: keyedStreamOptions("test-key")}
	_, _ = collectEvents(t, adapter.StreamSimple(context.Background(), model, utils.NormalizeContext(&types.Context{
		SystemPrompt: "sys",
		Messages: []types.Message{
			types.UserMessage{Role: types.RoleUser, Content: types.UserContent{String: "hi"}, Timestamp: 1},
		},
	}), &opts))

	if len(*captured) != 1 {
		t.Fatalf("expected 1 request, got %d", len(*captured))
	}
	got, ok := (*captured)[0]["priority"].(float64)
	if !ok || got != 10 {
		t.Fatalf("priority = %v, want 10", (*captured)[0]["priority"])
	}
}

func TestVllmPriorityOmittedWhenNotSet(t *testing.T) {
	srv, captured := newMockSSEServer(t, reasoningDetailsChunk(t, `{}`, `"stop"`))
	model := reasoningDetailsTestModel(t, srv.URL)
	var adapter OpenAICompletions
	opts := types.SimpleStreamOptions{StreamOptions: keyedStreamOptions("test-key")}
	_, _ = collectEvents(t, adapter.StreamSimple(context.Background(), model, utils.NormalizeContext(&types.Context{
		SystemPrompt: "sys",
		Messages: []types.Message{
			types.UserMessage{Role: types.RoleUser, Content: types.UserContent{String: "hi"}, Timestamp: 1},
		},
	}), &opts))

	if len(*captured) != 1 {
		t.Fatalf("expected 1 request, got %d", len(*captured))
	}
	if _, present := (*captured)[0]["priority"]; present {
		t.Fatalf("priority must be omitted when vllmPriority is not set: %v", (*captured)[0]["priority"])
	}
}
