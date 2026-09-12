// pi: test/openai-codex-stream.test.ts.
//
// Tests for the ChatGPT Codex Responses adapter: SSE transport, WebSocket
// transport with pooled cached connections, zstd request compression, retry
// semantics, and the SSE fallback ladder. pi stubs globalThis.fetch and the
// global WebSocket constructor; pig injects options.Fetch and overrides the
// codexDialWebSocket seam variable (see openai_codex_responses.go header).
// Codex-specific options (toolChoice / reasoningEffort / serviceTier) go
// through the typed streamOpenAICodexResponses entry point, matching pi's
// typed stream calls.
package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"

	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/ai/utils"
)

const codexTestURL = "https://chatgpt.com/backend-api/codex/responses"

// codexMockToken builds `aaa.<payload>.bbb` (pi: mockToken).
func codexMockToken(accountID string) string {
	payload := base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf(
		`{"%s":{"chatgpt_account_id":%q}}`, codexJWTClaimPath, accountID)))
	return "aaa." + payload + ".bbb"
}

// codexTestModel is the gpt-5.1-codex fixture model (pi: the inline model
// literal).
func codexTestModel() *types.Model {
	return &types.Model{
		ID:            "gpt-5.1-codex",
		Name:          "GPT-5.1 Codex",
		API:           types.ApiOpenAICodexResponses,
		Provider:      "openai-codex",
		BaseURL:       "https://chatgpt.com/backend-api",
		Reasoning:     true,
		Input:         []types.ModelInput{types.ModelInputText},
		Cost:          types.ModelCost{},
		ContextWindow: 400000,
		MaxTokens:     128000,
	}
}

// codexUserMessage is the standard user fixture message.
func codexUserMessage(text string, timestamp int64) types.UserMessage {
	return types.UserMessage{
		Role:      types.RoleUser,
		Content:   types.UserContent{Parts: []types.Content{types.TextContent{Kind: types.KindText, Text: text}}},
		Timestamp: timestamp,
	}
}

// codexHelloSSE is the standard completed-payload fixture (pi:
// buildSSEPayload with status "completed").
func codexHelloSSE() string {
	return codexSSEFixture("completed", false, nil)
}

// codexSSEFixture is pi's buildSSEPayload: one text message plus a terminal
// response event.
func codexSSEFixture(status string, includeDone bool, endTurn any) string {
	terminalType := "response.completed"
	incompleteDetails := any(nil)
	if status == "incomplete" {
		terminalType = "response.incomplete"
		incompleteDetails = map[string]any{"reason": "max_output_tokens"}
	}
	events := []string{
		codexSSEData(map[string]any{
			"type": "response.output_item.added",
			"item": map[string]any{"type": "message", "id": "msg_1", "role": "assistant", "status": "in_progress", "content": []any{}},
		}),
		codexSSEData(map[string]any{"type": "response.content_part.added", "part": map[string]any{"type": "output_text", "text": ""}}),
		codexSSEData(map[string]any{"type": "response.output_text.delta", "delta": "Hello"}),
		codexSSEData(map[string]any{
			"type": "response.output_item.done",
			"item": map[string]any{
				"type": "message", "id": "msg_1", "role": "assistant", "status": "completed",
				"content": []any{map[string]any{"type": "output_text", "text": "Hello"}},
			},
		}),
		codexSSEData(map[string]any{
			"type": terminalType,
			"response": map[string]any{
				"status":             status,
				"end_turn":           endTurn,
				"incomplete_details": incompleteDetails,
				"usage": map[string]any{
					"input_tokens": 5, "output_tokens": 3, "total_tokens": 8,
					"input_tokens_details": map[string]any{"cached_tokens": 0},
				},
			},
		}),
	}
	if includeDone {
		events = append(events, "data: [DONE]")
	}
	return strings.Join(events, "\n\n") + "\n\n"
}

// codexSSEData renders one `data:` line.
func codexSSEData(event map[string]any) string {
	data, _ := json.Marshal(event)
	return "data: " + string(data)
}

// codexSSEResponse wraps an SSE payload in an http.Response.
func codexSSEResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: 200,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// codexJSONResponse wraps a JSON payload in an http.Response.
func codexJSONResponse(status int, body string, header http.Header) *http.Response {
	if header == nil {
		header = http.Header{"Content-Type": []string{"application/json"}}
	}
	return &http.Response{
		StatusCode: status,
		Header:     header,
		Body:       io.NopCloser(strings.NewReader(body)),
		Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
	}
}

// codexReqHeader reads a header by exact name (the adapter assigns
// non-canonical keys directly onto http.Header).
func codexReqHeader(req *http.Request, name string) string {
	if values, ok := req.Header[name]; ok && len(values) > 0 {
		return values[0]
	}
	return req.Header.Get(name)
}

// codexDecodeRequestBody decodes the captured request body, decompressing
// zstd bodies (pi: decodeCodexRequestBody).
func codexDecodeRequestBody(t *testing.T, req *http.Request) map[string]any {
	t.Helper()
	raw, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("read request body: %v", err)
	}
	if codexReqHeader(req, "content-encoding") == "zstd" {
		reader, err := zstd.NewReader(bytes.NewReader(raw))
		if err != nil {
			t.Fatalf("zstd reader: %v", err)
		}
		defer reader.Close()
		raw, err = io.ReadAll(reader.IOReadCloser())
		if err != nil {
			t.Fatalf("zstd decompress: %v", err)
		}
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode request body %q: %v", raw, err)
	}
	return body
}

// codexResetWSState is pi's afterEach cleanup.
func codexResetWSState() {
	CloseOpenAICodexWebSocketSessions()
	ResetOpenAICodexWebSocketDebugStats()
}

// codexResultText extracts the joined text blocks of a result message.
func codexResultText(message types.AssistantMessage) string {
	var out []string
	for _, block := range message.Content {
		if text, ok := block.(types.TextContent); ok {
			out = append(out, text.Text)
		}
	}
	return strings.Join(out, "")
}

func codexContains(list []string, target string) bool {
	for _, item := range list {
		if item == target {
			return true
		}
	}
	return false
}

func codexCount(list []string, target string) int {
	count := 0
	for _, item := range list {
		if item == target {
			count++
		}
	}
	return count
}

func codexStringPtr(s string) *string { return &s }
func codexBoolPtr(b bool) *bool       { return &b }
func codexInt64Ptr(v int64) *int64    { return &v }
func codexIntPtr(v int) *int          { return &v }

// codexOnPayload adapts an OnPayload callback expecting a map payload (the
// promoted option's signature carries `any`).
func codexOnPayload(capture func(payload map[string]any)) func(payload any, model *types.Model) any {
	return func(payload any, model *types.Model) any {
		if body, ok := payload.(map[string]any); ok {
			capture(body)
		}
		return nil
	}
}

// =============================================================================
// WebSocket mock (pi: the per-test MockWebSocket classes)
// =============================================================================

// codexMockWSConn is the in-memory WebSocket used across the transport tests.
type codexMockWSConn struct {
	mu          sync.Mutex
	inbox       chan string
	readErr     error
	closeCount  int
	reusableVal bool
	onSend      func(conn *codexMockWSConn, text string)
}

func newCodexMockWSConn() *codexMockWSConn {
	return &codexMockWSConn{inbox: make(chan string, 64), reusableVal: true}
}

func (m *codexMockWSConn) Send(text string) error {
	if m.onSend != nil {
		m.onSend(m, text)
	}
	return nil
}

func (m *codexMockWSConn) Read(ctx context.Context, idleTimeout time.Duration) (string, error) {
	m.mu.Lock()
	readErr := m.readErr
	m.mu.Unlock()
	if readErr != nil {
		return "", readErr
	}
	var idle <-chan time.Time
	if idleTimeout > 0 {
		timer := time.NewTimer(idleTimeout)
		defer timer.Stop()
		idle = timer.C
	}
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-idle:
		return "", errCodexWSIdleTimeout
	case message := <-m.inbox:
		return message, nil
	}
}

func (m *codexMockWSConn) Close(code int, reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closeCount++
	m.reusableVal = false
}

func (m *codexMockWSConn) Reusable() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.reusableVal
}

func (m *codexMockWSConn) dispatch(events ...map[string]any) {
	for _, event := range events {
		data, _ := json.Marshal(event)
		m.inbox <- string(data)
	}
}

func (m *codexMockWSConn) failWith(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.readErr = err
}

func (m *codexMockWSConn) closes() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.closeCount
}

// codexWSDialerStub overrides the dial seam and records dials (pi:
// vi.stubGlobal("WebSocket", MockWebSocket) + capturedWebSocketHeaders).
type codexWSDialerStub struct {
	mu      sync.Mutex
	URLs    []string
	Headers []http.Header
	Conns   []*codexMockWSConn
	// Behavior returns the conn for the nth dial (1-based).
	Behavior func(call int, requestURL string, headers http.Header) (*codexMockWSConn, error)
}

func stubCodexDialer(t *testing.T, behavior func(call int, requestURL string, headers http.Header) (*codexMockWSConn, error)) *codexWSDialerStub {
	t.Helper()
	stub := &codexWSDialerStub{Behavior: behavior}
	prev := codexDialWebSocket
	codexDialWebSocket = func(ctx context.Context, requestURL string, headers http.Header) (codexWSConn, error) {
		stub.mu.Lock()
		stub.URLs = append(stub.URLs, requestURL)
		stub.Headers = append(stub.Headers, headers)
		stub.mu.Unlock()
		if stub.Behavior == nil {
			return nil, errors.New("unexpected codex websocket dial")
		}
		conn, err := stub.Behavior(len(stub.URLs), requestURL, headers)
		if err != nil {
			return nil, err
		}
		stub.mu.Lock()
		stub.Conns = append(stub.Conns, conn)
		stub.mu.Unlock()
		return conn, nil
	}
	t.Cleanup(func() { codexDialWebSocket = prev })
	return stub
}

func (s *codexWSDialerStub) dialCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.URLs)
}

// stubCodexSleepRecorder replaces the retry sleep with a recorder (pi:
// vi.useFakeTimers + setTimeout spy).
func stubCodexSleepRecorder(t *testing.T) *[]time.Duration {
	t.Helper()
	delays := &[]time.Duration{}
	prev := codexSleep
	codexSleep = func(ctx context.Context, d time.Duration) error {
		*delays = append(*delays, d)
		if ctx.Err() != nil {
			return errCodexAborted
		}
		return nil
	}
	t.Cleanup(func() { codexSleep = prev })
	return delays
}

// codexHelloCompletionEvents are the standard completion events the mock
// sockets answer sends with (pi: the MockWebSocket send bodies).
func codexHelloCompletionEvents(responseID string) []map[string]any {
	return []map[string]any{
		{"type": "response.created", "response": map[string]any{"id": responseID}},
		{"type": "response.output_item.added", "item": map[string]any{"type": "message", "id": "msg_1", "role": "assistant", "status": "in_progress", "content": []any{}}},
		{"type": "response.content_part.added", "part": map[string]any{"type": "output_text", "text": ""}},
		{"type": "response.output_text.delta", "delta": "Hello"},
		{"type": "response.output_item.done", "item": map[string]any{
			"type": "message", "id": "msg_1", "role": "assistant", "status": "completed",
			"content": []any{map[string]any{"type": "output_text", "text": "Hello"}},
		}},
		{"type": "response.completed", "response": map[string]any{
			"id": responseID, "status": "completed", "end_turn": false,
			"usage": map[string]any{"input_tokens": 5, "output_tokens": 3, "total_tokens": 8, "input_tokens_details": map[string]any{"cached_tokens": 0}},
		}},
	}
}

// =============================================================================
// SSE transport
// =============================================================================

func TestOpenAICodexStreamStreamsSSEResponses(t *testing.T) {
	codexResetWSState()
	token := codexMockToken("acc_test")
	var gotHeaders http.Header
	model := codexTestModel()
	llmContext := &types.Context{
		SystemPrompt: "You are a helpful assistant.",
		Messages:     []types.Message{codexUserMessage("Say hello", time.Now().UnixMilli())},
	}
	options := &types.StreamOptions{Transport: types.TransportSSE}
	options.APIKey = token
	options.Fetch = func(req *http.Request) (*http.Response, error) {
		if req.URL.String() != codexTestURL {
			return nil, fmt.Errorf("unexpected URL: %s", req.URL)
		}
		gotHeaders = req.Header
		return codexSSEResponse(codexHelloSSE()), nil
	}
	stream := OpenAICodexResponses{}.Stream(context.Background(), model, llmContext, options)

	sawTextDelta := false
	sawDone := false
	for event := range stream.Events() {
		if event.Type == types.EventTextDelta {
			sawTextDelta = true
		}
		if event.Type == types.EventDone {
			sawDone = true
			if codexResultText(*event.Message) != "Hello" {
				t.Fatalf("done message text = %q", codexResultText(*event.Message))
			}
		}
	}
	if !sawTextDelta || !sawDone {
		t.Fatalf("sawTextDelta=%v sawDone=%v", sawTextDelta, sawDone)
	}
	if got := codexReqHeader(reqFromHeader(gotHeaders), "Authorization"); got != "Bearer "+token {
		t.Fatalf("Authorization = %q", got)
	}
	if got := codexReqHeader(reqFromHeader(gotHeaders), "chatgpt-account-id"); got != "acc_test" {
		t.Fatalf("chatgpt-account-id = %q", got)
	}
	if got := codexReqHeader(reqFromHeader(gotHeaders), "OpenAI-Beta"); got != "responses=experimental" {
		t.Fatalf("OpenAI-Beta = %q", got)
	}
	if got := codexReqHeader(reqFromHeader(gotHeaders), "originator"); got != "pi" {
		t.Fatalf("originator = %q", got)
	}
	if got := codexReqHeader(reqFromHeader(gotHeaders), "User-Agent"); got != utils.GetPiUserAgent() {
		t.Fatalf("User-Agent = %q", got)
	}
	if got := codexReqHeader(reqFromHeader(gotHeaders), "accept"); got != "text/event-stream" {
		t.Fatalf("accept = %q", got)
	}
	if _, ok := gotHeaders["x-api-key"]; ok {
		t.Fatal("x-api-key should not be sent")
	}
}

// reqFromHeader wraps a captured header map so codexReqHeader can consult it
// (nil-safe).
func reqFromHeader(h http.Header) *http.Request {
	req := &http.Request{Header: http.Header{}}
	for name, values := range h {
		req.Header[name] = values
	}
	return req
}

func TestOpenAICodexStreamTerminalEventWithoutTrailingBlankLine(t *testing.T) {
	codexResetWSState()
	token := codexMockToken("acc_test")
	sse := strings.TrimRight(codexHelloSSE(), "\n")
	model := codexTestModel()
	llmContext := &types.Context{
		SystemPrompt: "You are a helpful assistant.",
		Messages:     []types.Message{codexUserMessage("Say hello", time.Now().UnixMilli())},
	}
	options := &types.StreamOptions{Transport: types.TransportSSE}
	options.APIKey = token
	options.Fetch = func(req *http.Request) (*http.Response, error) {
		return codexSSEResponse(sse), nil
	}
	result := OpenAICodexResponses{}.Stream(context.Background(), model, llmContext, options).Result()

	if result.StopReason != types.StopReasonStop {
		t.Fatalf("stopReason = %q (error: %q)", result.StopReason, result.ErrorMessage)
	}
	if codexResultText(result) != "Hello" {
		t.Fatalf("text = %q", codexResultText(result))
	}
}

func TestOpenAICodexStreamCompletesWhenSSEBodyStaysOpen(t *testing.T) {
	codexResetWSState()
	token := codexMockToken("acc_test")
	sse := codexSSEFixture("completed", true, false)
	model := codexTestModel()
	llmContext := &types.Context{
		SystemPrompt: "You are a helpful assistant.",
		Messages:     []types.Message{codexUserMessage("Say hello", time.Now().UnixMilli())},
	}
	done := make(chan types.AssistantMessage, 1)
	go func() {
		options := &types.StreamOptions{Transport: types.TransportSSE}
		options.APIKey = token
		options.Fetch = func(req *http.Request) (*http.Response, error) {
			pr, pw := io.Pipe()
			go func() {
				pw.Write([]byte(sse))
				// The body stays open; the terminal event must end the stream.
			}()
			resp := codexSSEResponse("")
			resp.Body = pr
			return resp, nil
		}
		result := OpenAICodexResponses{}.Stream(context.Background(), model, llmContext, options).Result()
		done <- result
	}()
	select {
	case result := <-done:
		if codexResultText(result) != "Hello" {
			t.Fatalf("text = %q", codexResultText(result))
		}
		if result.StopReason != types.StopReasonStop {
			t.Fatalf("stopReason = %q", result.StopReason)
		}
		if result.EndTurn == nil || *result.EndTurn {
			t.Fatalf("endTurn = %v, want false", result.EndTurn)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for completed SSE stream")
	}
}

func TestOpenAICodexStreamMapsIncompleteToLength(t *testing.T) {
	codexResetWSState()
	token := codexMockToken("acc_test")
	sse := codexSSEFixture("incomplete", true, nil)
	model := codexTestModel()
	llmContext := &types.Context{
		SystemPrompt: "You are a helpful assistant.",
		Messages:     []types.Message{codexUserMessage("Say hello", time.Now().UnixMilli())},
	}
	options := &types.StreamOptions{Transport: types.TransportSSE}
	options.APIKey = token
	options.Fetch = func(req *http.Request) (*http.Response, error) {
		return codexSSEResponse(sse), nil
	}
	result := OpenAICodexResponses{}.Stream(context.Background(), model, llmContext, options).Result()

	if codexResultText(result) != "Hello" {
		t.Fatalf("text = %q", codexResultText(result))
	}
	if result.StopReason != types.StopReasonLength {
		t.Fatalf("stopReason = %q", result.StopReason)
	}
}

func TestOpenAICodexStreamHeadersTimeout(t *testing.T) {
	codexResetWSState()
	token := codexMockToken("acc_test")
	fetchCalls := 0
	model := codexTestModel()
	llmContext := &types.Context{
		SystemPrompt: "You are a helpful assistant.",
		Messages:     []types.Message{codexUserMessage("Say hello", time.Now().UnixMilli())},
	}
	options := &types.StreamOptions{Transport: types.TransportSSE}
	options.APIKey = token
	options.TimeoutMs = 10
	options.Fetch = func(req *http.Request) (*http.Response, error) {
		fetchCalls++
		<-req.Context().Done()
		return nil, req.Context().Err()
	}
	result := OpenAICodexResponses{}.Stream(context.Background(), model, llmContext, options).Result()

	if fetchCalls != 1 {
		t.Fatalf("fetch calls = %d", fetchCalls)
	}
	if result.StopReason != types.StopReasonError {
		t.Fatalf("stopReason = %q", result.StopReason)
	}
	if result.ErrorMessage != "Codex SSE response headers timed out after 10ms" {
		t.Fatalf("errorMessage = %q", result.ErrorMessage)
	}
}

// codexCloseProbeBody records Close calls (pi: the ReadableStream cancel
// callback flag).
type codexCloseProbeBody struct {
	io.ReadCloser
	onClose func()
}

func (b *codexCloseProbeBody) Close() error {
	b.onClose()
	return b.ReadCloser.Close()
}

func TestOpenAICodexStreamAbortsSSEBodyReads(t *testing.T) {
	codexResetWSState()
	token := codexMockToken("acc_test")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	bodyClosed := &atomic.Bool{}

	pr, pw := io.Pipe()
	resp := codexSSEResponse("")
	resp.Body = &codexCloseProbeBody{ReadCloser: pr, onClose: func() { bodyClosed.Store(true) }}
	go func() {
		chunkOne := strings.Join([]string{
			codexSSEData(map[string]any{"type": "response.output_item.added", "item": map[string]any{"type": "message", "id": "msg_1", "role": "assistant", "status": "in_progress", "content": []any{}}}),
			codexSSEData(map[string]any{"type": "response.content_part.added", "part": map[string]any{"type": "output_text", "text": ""}}),
			codexSSEData(map[string]any{"type": "response.output_text.delta", "delta": "one"}),
		}, "\n\n") + "\n\n"
		pw.Write([]byte(chunkOne))
		select {
		case <-time.After(30 * time.Second):
			// Abort failed to cut the read; deliver the rest anyway so the
			// text_delta:two assertion fires.
			chunkTwo := strings.Join([]string{
				codexSSEData(map[string]any{"type": "response.output_text.delta", "delta": "two"}),
				codexSSEData(map[string]any{"type": "response.output_item.done", "item": map[string]any{
					"type": "message", "id": "msg_1", "role": "assistant", "status": "completed",
					"content": []any{map[string]any{"type": "output_text", "text": "onetwo"}},
				}}),
				codexSSEData(map[string]any{"type": "response.completed", "response": map[string]any{
					"status": "completed",
					"usage":  map[string]any{"input_tokens": 5, "output_tokens": 3, "total_tokens": 8, "input_tokens_details": map[string]any{"cached_tokens": 0}},
				}}),
			}, "\n\n") + "\n\n"
			pw.Write([]byte(chunkTwo))
			pw.Close()
		case <-ctx.Done():
			pw.CloseWithError(io.EOF)
		}
	}()

	model := codexTestModel()
	llmContext := &types.Context{
		SystemPrompt: "You are a helpful assistant.",
		Messages:     []types.Message{codexUserMessage("Say hello", time.Now().UnixMilli())},
	}
	var eventNames []string
	options := &types.StreamOptions{Transport: types.TransportSSE}
	options.APIKey = token
	options.Fetch = func(req *http.Request) (*http.Response, error) {
		return resp, nil
	}
	stream := OpenAICodexResponses{}.Stream(ctx, model, llmContext, options)
	for event := range stream.Events() {
		if event.Type == types.EventTextDelta {
			eventNames = append(eventNames, "text_delta:"+event.Delta)
			if event.Delta == "one" {
				cancel()
			}
		} else {
			eventNames = append(eventNames, string(event.Type))
		}
	}
	result := stream.Result()

	if result.StopReason != types.StopReasonAborted {
		t.Fatalf("stopReason = %q", result.StopReason)
	}
	if result.ErrorMessage != "Request was aborted" {
		t.Fatalf("errorMessage = %q", result.ErrorMessage)
	}
	if !codexContains(eventNames, "text_delta:one") {
		t.Fatalf("events missing text_delta:one: %v", eventNames)
	}
	if codexContains(eventNames, "text_delta:two") {
		t.Fatalf("events should not contain text_delta:two: %v", eventNames)
	}
	if !bodyClosed.Load() {
		t.Fatal("response body was not closed after abort")
	}
}

// =============================================================================
// Session affinity headers
// =============================================================================

func TestOpenAICodexStreamSessionHeadersAndPromptCacheKey(t *testing.T) {
	codexResetWSState()
	token := codexMockToken("acc_test")
	sessionID := "test-session-123"
	model := codexTestModel()
	llmContext := &types.Context{
		SystemPrompt: "You are a helpful assistant.",
		Messages:     []types.Message{codexUserMessage("Say hello", time.Now().UnixMilli())},
	}
	options := &types.StreamOptions{}
	options.APIKey = token
	options.SessionID = sessionID
	options.Transport = types.TransportSSE
	options.Fetch = func(req *http.Request) (*http.Response, error) {
		if got := codexReqHeader(req, "session-id"); got != sessionID {
			t.Errorf("session-id = %q", got)
		}
		if _, ok := req.Header["session_id"]; ok {
			t.Error("session_id header should not be sent")
		}
		if got := codexReqHeader(req, "x-client-request-id"); got != sessionID {
			t.Errorf("x-client-request-id = %q", got)
		}
		body := codexDecodeRequestBody(t, req)
		if body["prompt_cache_key"] != sessionID {
			t.Errorf("prompt_cache_key = %v", body["prompt_cache_key"])
		}
		return codexSSEResponse(codexHelloSSE()), nil
	}
	OpenAICodexResponses{}.Stream(context.Background(), model, llmContext, options).Result()
}

func TestOpenAICodexStreamOmitsCacheAffinityWhenRetentionNone(t *testing.T) {
	codexResetWSState()
	token := codexMockToken("acc_test")
	var sawSessionID, sawClientRequestID, sawPromptCacheKey bool
	model := codexTestModel()
	llmContext := &types.Context{
		SystemPrompt: "You are a helpful assistant.",
		Messages:     []types.Message{codexUserMessage("Say hello", time.Now().UnixMilli())},
	}
	options := &types.StreamOptions{}
	options.APIKey = token
	options.CacheRetention = types.CacheRetentionNone
	options.SessionID = "one-off-summary"
	options.Transport = types.TransportSSE
	options.Fetch = func(req *http.Request) (*http.Response, error) {
		if _, ok := req.Header["session-id"]; ok {
			sawSessionID = true
		}
		if _, ok := req.Header["x-client-request-id"]; ok {
			sawClientRequestID = true
		}
		body := codexDecodeRequestBody(t, req)
		if _, ok := body["prompt_cache_key"]; ok {
			sawPromptCacheKey = true
		}
		return codexSSEResponse(codexHelloSSE()), nil
	}
	OpenAICodexResponses{}.Stream(context.Background(), model, llmContext, options).Result()
	if sawSessionID || sawClientRequestID || sawPromptCacheKey {
		t.Fatalf("cache affinity should be omitted (sessionID=%v clientRequestID=%v promptCacheKey=%v)",
			sawSessionID, sawClientRequestID, sawPromptCacheKey)
	}
}

func TestOpenAICodexStreamClampsPromptCacheKey(t *testing.T) {
	codexResetWSState()
	token := codexMockToken("acc_test")
	sessionID := strings.Repeat("x", 67)
	want := strings.Repeat("x", 64)
	var gotKey any
	model := codexTestModel()
	llmContext := &types.Context{
		SystemPrompt: "You are a helpful assistant.",
		Messages:     []types.Message{codexUserMessage("Say hello", time.Now().UnixMilli())},
	}
	options := &types.StreamOptions{}
	options.APIKey = token
	options.SessionID = sessionID
	options.Transport = types.TransportSSE
	options.OnPayload = codexOnPayload(func(payload map[string]any) {
		gotKey = payload["prompt_cache_key"]
	})
	options.Fetch = func(req *http.Request) (*http.Response, error) {
		return codexSSEResponse(codexHelloSSE()), nil
	}
	OpenAICodexResponses{}.Stream(context.Background(), model, llmContext, options).Result()
	if gotKey != want {
		t.Fatalf("prompt_cache_key = %v, want %q", gotKey, want)
	}
}

func TestOpenAICodexStreamClampsSessionIDHeader(t *testing.T) {
	codexResetWSState()
	token := codexMockToken("acc_test")
	sessionID := strings.Repeat("x", 67)
	want := strings.Repeat("x", 64)
	model := codexTestModel()
	llmContext := &types.Context{
		SystemPrompt: "You are a helpful assistant.",
		Messages:     []types.Message{codexUserMessage("Say hello", time.Now().UnixMilli())},
	}
	options := &types.StreamOptions{}
	options.APIKey = token
	options.SessionID = sessionID
	options.Transport = types.TransportSSE
	options.Fetch = func(req *http.Request) (*http.Response, error) {
		if got := codexReqHeader(req, "session-id"); got != want {
			t.Errorf("session-id = %q", got)
		}
		if got := codexReqHeader(req, "x-client-request-id"); got != want {
			t.Errorf("x-client-request-id = %q", got)
		}
		return codexSSEResponse(codexHelloSSE()), nil
	}
	OpenAICodexResponses{}.Stream(context.Background(), model, llmContext, options).Result()
}

// =============================================================================
// Reasoning / tools / service tier
// =============================================================================

func TestOpenAICodexStreamSimplePreservesXhigh(t *testing.T) {
	codexResetWSState()
	token := codexMockToken("acc_test")
	var requestedReasoning any
	model := codexTestModel()
	model.ID = "gpt-5.5"
	model.Name = "GPT-5.5"
	model.ThinkingLevelMap = types.ThinkingLevelMap{
		types.ModelThinkingXHigh: codexStringPtr("xhigh"),
	}
	llmContext := &types.Context{
		SystemPrompt: "You are a helpful assistant.",
		Messages:     []types.Message{codexUserMessage("Say hello", time.Now().UnixMilli())},
	}
	base := types.StreamOptions{}
	base.Transport = types.TransportSSE
	base.APIKey = token
	base.Fetch = func(req *http.Request) (*http.Response, error) {
		body := codexDecodeRequestBody(t, req)
		requestedReasoning = body["reasoning"]
		return codexSSEResponse(codexHelloSSE()), nil
	}
	OpenAICodexResponses{}.StreamSimple(context.Background(), model, llmContext, &types.SimpleStreamOptions{
		StreamOptions: base,
		Reasoning:     types.ThinkingLevelXHigh,
	}).Result()
	want := map[string]any{"effort": "xhigh", "summary": "auto"}
	if fmt.Sprint(requestedReasoning) != fmt.Sprint(want) {
		t.Fatalf("reasoning = %v, want %v", requestedReasoning, want)
	}
}

func TestOpenAICodexStreamToolChoiceRequired(t *testing.T) {
	codexResetWSState()
	token := codexMockToken("acc_test")
	var requestedToolChoice any
	model := codexTestModel()
	model.ID = "gpt-5.5"
	model.Name = "GPT-5.5"
	llmContext := &types.Context{
		Messages: []types.Message{codexUserMessage("Do not call ping. Respond with text instead.", time.Now().UnixMilli())},
		Tools: []types.Tool{{
			Name:        "ping",
			Description: "Ping",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"value":{"type":"string"}}}`),
		}},
	}
	base := types.StreamOptions{}
	base.Transport = types.TransportSSE
	base.APIKey = token
	base.Fetch = func(req *http.Request) (*http.Response, error) {
		body := codexDecodeRequestBody(t, req)
		requestedToolChoice = body["tool_choice"]
		return codexSSEResponse(codexHelloSSE()), nil
	}
	streamOpenAICodexResponses(context.Background(), model, llmContext, &OpenAICodexResponsesOptions{
		StreamOptions: base,
		ToolChoice:    "required",
	}).Result()
	if requestedToolChoice != "required" {
		t.Fatalf("tool_choice = %v, want required", requestedToolChoice)
	}
}

func TestOpenAICodexStreamStrictModeAndConstrainedSampling(t *testing.T) {
	codexResetWSState()
	token := codexMockToken("acc_test")
	var requestedTools any
	model := codexTestModel()
	model.ID = "gpt-5.5"
	model.Name = "GPT-5.5"
	llmContext := &types.Context{
		Messages: []types.Message{codexUserMessage("Use a tool", time.Now().UnixMilli())},
		Tools: []types.Tool{
			{
				Name:                "optional",
				Description:         "Optional constrained sampling",
				Parameters:          json.RawMessage(`{"type":"object","properties":{"value":{"type":"string"}}}`),
				ConstrainedSampling: false,
			},
			{
				Name:        "strict",
				Description: "Strict constrained sampling",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"value":{"type":"string"}},"additionalProperties":false}`),
				ConstrainedSampling: map[string]any{
					"type":   "json_schema",
					"strict": "prefer",
				},
			},
		},
	}
	options := &types.StreamOptions{}
	options.APIKey = token
	options.Transport = types.TransportSSE
	options.OnPayload = codexOnPayload(func(payload map[string]any) {
		requestedTools = payload["tools"]
	})
	options.Fetch = func(req *http.Request) (*http.Response, error) {
		return codexSSEResponse(codexHelloSSE()), nil
	}
	OpenAICodexResponses{}.Stream(context.Background(), model, llmContext, options).Result()

	tools, ok := requestedTools.([]any)
	if !ok || len(tools) != 2 {
		t.Fatalf("tools = %v", requestedTools)
	}
	first, _ := tools[0].(map[string]any)
	second, _ := tools[1].(map[string]any)
	if first["type"] != "function" || first["name"] != "optional" {
		t.Fatalf("first tool = %v", first)
	}
	if value, present := first["strict"]; !present || value != nil {
		t.Fatalf("first strict = %#v, want present null", first["strict"])
	}
	if second["type"] != "function" || second["name"] != "strict" || second["strict"] != true {
		t.Fatalf("second tool = %v", second)
	}
}

func TestOpenAICodexStreamClampsMinimalToLow(t *testing.T) {
	codexResetWSState()
	token := codexMockToken("acc_test")
	for _, modelID := range []string{"gpt-5.3-codex", "gpt-5.4", "gpt-5.5"} {
		t.Run(modelID, func(t *testing.T) {
			var requestedReasoning any
			model := codexTestModel()
			model.ID = modelID
			model.Name = modelID
			model.ThinkingLevelMap = types.ThinkingLevelMap{
				types.ModelThinkingMinimal: codexStringPtr("low"),
			}
			llmContext := &types.Context{
				SystemPrompt: "You are a helpful assistant.",
				Messages:     []types.Message{codexUserMessage("Say hello", time.Now().UnixMilli())},
			}
			base := types.StreamOptions{}
			base.Transport = types.TransportSSE
			base.APIKey = token
			base.Fetch = func(req *http.Request) (*http.Response, error) {
				body := codexDecodeRequestBody(t, req)
				requestedReasoning = body["reasoning"]
				return codexSSEResponse(codexHelloSSE()), nil
			}
			streamOpenAICodexResponses(context.Background(), model, llmContext, &OpenAICodexResponsesOptions{
				StreamOptions:   base,
				ReasoningEffort: types.ModelThinkingMinimal,
			}).Result()
			want := map[string]any{"effort": "low", "summary": "auto"}
			if fmt.Sprint(requestedReasoning) != fmt.Sprint(want) {
				t.Fatalf("reasoning = %v, want %v", requestedReasoning, want)
			}
		})
	}
}

func TestOpenAICodexStreamServiceTierPricing(t *testing.T) {
	codexResetWSState()
	token := codexMockToken("acc_test")
	cases := []struct {
		modelID     string
		serviceTier string
		multiplier  float64
	}{
		{"gpt-5.1-codex", "flex", 0.5},
		{"gpt-5.1-codex", "priority", 2},
		{"gpt-5.5", "flex", 0.5},
		{"gpt-5.5", "priority", 2.5},
	}
	for _, tc := range cases {
		t.Run(tc.modelID+"-"+tc.serviceTier, func(t *testing.T) {
			model := codexTestModel()
			model.ID = tc.modelID
			model.Name = tc.modelID
			model.Cost = types.ModelCost{ModelCostRates: types.ModelCostRates{Input: 1, Output: 2, CacheRead: 0, CacheWrite: 0}}
			llmContext := &types.Context{
				SystemPrompt: "You are a helpful assistant.",
				Messages:     []types.Message{codexUserMessage("Say hello", time.Now().UnixMilli())},
			}
			base := types.StreamOptions{}
			base.Transport = types.TransportSSE
			base.APIKey = token
			base.Fetch = func(req *http.Request) (*http.Response, error) {
				sse := strings.Join([]string{
					codexSSEData(map[string]any{"type": "response.output_item.added", "item": map[string]any{"type": "message", "id": "msg_1", "role": "assistant", "status": "in_progress", "content": []any{}}}),
					codexSSEData(map[string]any{"type": "response.content_part.added", "part": map[string]any{"type": "output_text", "text": ""}}),
					codexSSEData(map[string]any{"type": "response.output_text.delta", "delta": "Hello"}),
					codexSSEData(map[string]any{"type": "response.output_item.done", "item": map[string]any{
						"type": "message", "id": "msg_1", "role": "assistant", "status": "completed",
						"content": []any{map[string]any{"type": "output_text", "text": "Hello"}},
					}}),
					codexSSEData(map[string]any{"type": "response.completed", "response": map[string]any{
						"status":       "completed",
						"service_tier": "default",
						"usage": map[string]any{
							"input_tokens": 1000000, "output_tokens": 1000000, "total_tokens": 2000000,
							"input_tokens_details": map[string]any{"cached_tokens": 0},
						},
					}}),
				}, "\n\n") + "\n\n"
				return codexSSEResponse(sse), nil
			}
			result := streamOpenAICodexResponses(context.Background(), model, llmContext, &OpenAICodexResponsesOptions{
				StreamOptions: base,
				ServiceTier:   tc.serviceTier,
			}).Result()

			if result.Usage.Cost.Input != 1*tc.multiplier {
				t.Fatalf("cost.input = %v, want %v", result.Usage.Cost.Input, 1*tc.multiplier)
			}
			if result.Usage.Cost.Output != 2*tc.multiplier {
				t.Fatalf("cost.output = %v, want %v", result.Usage.Cost.Output, 2*tc.multiplier)
			}
			if result.Usage.Cost.Total != 3*tc.multiplier {
				t.Fatalf("cost.total = %v, want %v", result.Usage.Cost.Total, 3*tc.multiplier)
			}
		})
	}
}

func TestOpenAICodexStreamNoSessionHeadersWithoutSessionID(t *testing.T) {
	codexResetWSState()
	token := codexMockToken("acc_test")
	model := codexTestModel()
	llmContext := &types.Context{
		SystemPrompt: "You are a helpful assistant.",
		Messages:     []types.Message{codexUserMessage("Say hello", time.Now().UnixMilli())},
	}
	options := &types.StreamOptions{}
	options.APIKey = token
	options.Transport = types.TransportSSE
	options.Fetch = func(req *http.Request) (*http.Response, error) {
		for _, name := range []string{"session-id", "session_id", "x-client-request-id"} {
			if _, ok := req.Header[name]; ok {
				t.Errorf("%s should not be set without a session id", name)
			}
		}
		return codexSSEResponse(codexHelloSSE()), nil
	}
	OpenAICodexResponses{}.Stream(context.Background(), model, llmContext, options).Result()
}

// =============================================================================
// WebSocket transport
// =============================================================================

func TestOpenAICodexStreamSimpleAutoTransportCachedWebsocket(t *testing.T) {
	codexResetWSState()
	token := codexMockToken("acc_test")
	var sentBodies []map[string]any
	var capturedHeaders atomic.Value // http.Header
	fetchCalls := 0

	conn := newCodexMockWSConn()
	conn.onSend = func(conn *codexMockWSConn, text string) {
		var body map[string]any
		_ = json.Unmarshal([]byte(text), &body)
		sentBodies = append(sentBodies, body)
		conn.dispatch(codexHelloCompletionEvents("resp_1")...)
	}
	stubCodexDialer(t, func(call int, requestURL string, headers http.Header) (*codexMockWSConn, error) {
		capturedHeaders.Store(headers)
		return conn, nil
	})

	model := codexTestModel()
	llmContext := &types.Context{
		SystemPrompt: "You are a helpful assistant.",
		Messages:     []types.Message{codexUserMessage("Say hello", 1)},
	}
	base := types.StreamOptions{}
	base.APIKey = token
	base.SessionID = "session-auto"
	base.Transport = types.TransportAuto
	base.Fetch = func(req *http.Request) (*http.Response, error) {
		fetchCalls++
		return codexJSONResponse(500, "unexpected fetch", nil), nil
	}
	result := OpenAICodexResponses{}.StreamSimple(context.Background(), model, llmContext, &types.SimpleStreamOptions{
		StreamOptions: base,
	}).Result()

	headers := capturedHeaders.Load().(http.Header)
	if len(sentBodies) != 1 {
		t.Fatalf("sent bodies = %d", len(sentBodies))
	}
	if got := codexReqHeader(reqFromHeader(headers), "session-id"); got != "session-auto" {
		t.Fatalf("session-id = %q", got)
	}
	if _, ok := headers["session_id"]; ok {
		t.Fatal("session_id header should not be sent")
	}
	if got := codexReqHeader(reqFromHeader(headers), "x-client-request-id"); got != "session-auto" {
		t.Fatalf("x-client-request-id = %q", got)
	}
	if fetchCalls != 0 {
		t.Fatalf("fetch calls = %d", fetchCalls)
	}
	stats := GetOpenAICodexWebSocketDebugStats("session-auto")
	if stats == nil {
		t.Fatal("missing debug stats")
	}
	if stats.CachedContextRequests != 1 || stats.FullContextRequests != 1 {
		t.Fatalf("stats = %+v", stats)
	}
	if result.EndTurn == nil || *result.EndTurn {
		t.Fatalf("endTurn = %v", result.EndTurn)
	}
}

func TestOpenAICodexStreamScopesCachedWebsocketsToAccount(t *testing.T) {
	codexResetWSState()
	// Regression for #7284: rotating accounts must not reuse a socket
	// authenticated by another account.
	var responseID atomic.Int64
	fetchCalls := 0
	stub := stubCodexDialer(t, func(call int, requestURL string, headers http.Header) (*codexMockWSConn, error) {
		conn := newCodexMockWSConn()
		conn.onSend = func(conn *codexMockWSConn, text string) {
			id := fmt.Sprintf("resp_%d", responseID.Add(1))
			conn.dispatch([]map[string]any{{
				"type": "response.completed",
				"response": map[string]any{
					"id": id, "status": "completed",
					"usage": map[string]any{"input_tokens": 1, "output_tokens": 1, "total_tokens": 2},
				},
			}}...)
		}
		return conn, nil
	})

	model := codexTestModel()
	llmContext := &types.Context{SystemPrompt: "", Messages: []types.Message{}}
	newOptions := func(apiKey string) *types.StreamOptions {
		options := &types.StreamOptions{SessionID: "shared-session", Transport: types.TransportWebsocketCached}
		options.APIKey = apiKey
		options.Fetch = func(req *http.Request) (*http.Response, error) {
			fetchCalls++
			return codexJSONResponse(500, "unexpected fetch", nil), nil
		}
		return options
	}

	OpenAICodexResponses{}.Stream(context.Background(), model, llmContext, newOptions(codexMockToken("account-a"))).Result()
	OpenAICodexResponses{}.Stream(context.Background(), model, llmContext, newOptions(codexMockToken("account-b"))).Result()
	OpenAICodexResponses{}.Stream(context.Background(), model, llmContext, newOptions(codexMockToken("account-a"))).Result()

	if stub.dialCount() != 2 {
		t.Fatalf("dials = %d (third stream must reuse the cached connection)", stub.dialCount())
	}
	if got := []string{
		codexReqHeader(reqFromHeader(stub.Headers[0]), "chatgpt-account-id"),
		codexReqHeader(reqFromHeader(stub.Headers[1]), "chatgpt-account-id"),
	}; fmt.Sprint(got) != fmt.Sprint([]string{"account-a", "account-b"}) {
		t.Fatalf("account ids = %v", got)
	}
	if got := []string{
		stub.Headers[0].Get("Authorization"),
		stub.Headers[1].Get("Authorization"),
	}; got[0] != "Bearer "+codexMockToken("account-a") || got[1] != "Bearer "+codexMockToken("account-b") {
		t.Fatalf("authorization headers = %v", got)
	}
	if fetchCalls != 0 {
		t.Fatalf("fetch calls = %d", fetchCalls)
	}
	stats := GetOpenAICodexWebSocketDebugStats("shared-session")
	if stats == nil || stats.ConnectionsCreated != 2 || stats.ConnectionsReused != 1 {
		t.Fatalf("stats = %+v", stats)
	}
}

func TestOpenAICodexStreamClosesOneShotWebsocketsWhenRetentionNone(t *testing.T) {
	codexResetWSState()
	token := codexMockToken("acc_test")
	var sentBodies []map[string]any
	var conns []*codexMockWSConn
	fetchCalls := 0

	stubCodexDialer(t, func(call int, requestURL string, headers http.Header) (*codexMockWSConn, error) {
		conn := newCodexMockWSConn()
		conn.onSend = func(conn *codexMockWSConn, text string) {
			var body map[string]any
			_ = json.Unmarshal([]byte(text), &body)
			sentBodies = append(sentBodies, body)
			conn.dispatch([]map[string]any{{
				"type": "response.completed",
				"response": map[string]any{
					"id": fmt.Sprintf("resp_%d", call), "status": "completed",
					"usage": map[string]any{"input_tokens": 5, "output_tokens": 3, "total_tokens": 8},
				},
			}}...)
		}
		conns = append(conns, conn)
		return conn, nil
	})

	model := codexTestModel()
	llmContext := &types.Context{
		SystemPrompt: "You are a helpful assistant.",
		Messages:     []types.Message{codexUserMessage("Say hello", 1)},
	}
	newOptions := func() *types.StreamOptions {
		options := &types.StreamOptions{}
		options.APIKey = token
		options.CacheRetention = types.CacheRetentionNone
		options.SessionID = "one-off-summary"
		options.Fetch = func(req *http.Request) (*http.Response, error) {
			fetchCalls++
			return codexJSONResponse(500, "unexpected fetch", nil), nil
		}
		return options
	}

	OpenAICodexResponses{}.Stream(context.Background(), model, llmContext, newOptions()).Result()
	OpenAICodexResponses{}.Stream(context.Background(), model, llmContext, newOptions()).Result()

	if len(conns) != 2 {
		t.Fatalf("connections = %d", len(conns))
	}
	for i, conn := range conns {
		if conn.closes() == 0 {
			t.Fatalf("connection %d was not closed", i)
		}
	}
	if len(sentBodies) != 2 {
		t.Fatalf("sent bodies = %d", len(sentBodies))
	}
	for _, body := range sentBodies {
		if _, ok := body["prompt_cache_key"]; ok {
			t.Fatalf("prompt_cache_key should be omitted: %v", body["prompt_cache_key"])
		}
	}
	if GetOpenAICodexWebSocketDebugStats("one-off-summary") != nil {
		t.Fatal("stats should be undefined without a cache session")
	}
	if fetchCalls != 0 {
		t.Fatalf("fetch calls = %d", fetchCalls)
	}
}

func TestOpenAICodexStreamFallsBackToSSEOnConnectTimeout(t *testing.T) {
	codexResetWSState()
	token := codexMockToken("acc_test")
	fetchCalls := 0
	stubCodexDialer(t, func(call int, requestURL string, headers http.Header) (*codexMockWSConn, error) {
		// Never opens; the connect timeout must fire first.
		<-time.After(200 * time.Millisecond)
		return nil, errors.New("dial should have timed out first")
	})

	model := codexTestModel()
	llmContext := &types.Context{
		SystemPrompt: "You are a helpful assistant.",
		Messages:     []types.Message{codexUserMessage("Say hello", 1)},
	}
	options := &types.StreamOptions{Transport: types.TransportAuto}
	options.APIKey = token
	options.SessionID = "ws-connect-timeout"
	options.TimeoutMs = 300000
	options.WebsocketConnectTimeoutMs = codexInt64Ptr(50)
	options.Fetch = func(req *http.Request) (*http.Response, error) {
		fetchCalls++
		return codexSSEResponse(codexHelloSSE()), nil
	}
	result := OpenAICodexResponses{}.Stream(context.Background(), model, llmContext, options).Result()

	if codexResultText(result) != "Hello" {
		t.Fatalf("text = %q", codexResultText(result))
	}
	if fetchCalls != 1 {
		t.Fatalf("fetch calls = %d", fetchCalls)
	}
	stats := GetOpenAICodexWebSocketDebugStats("ws-connect-timeout")
	if stats == nil || stats.WebSocketFailures != 1 || stats.SSEFallbacks != 1 {
		t.Fatalf("stats = %+v", stats)
	}
	if stats.WebsocketFallbackActive == nil || !*stats.WebsocketFallbackActive {
		t.Fatalf("fallback active = %v", stats.WebsocketFallbackActive)
	}
	if stats.LastWebSocketError == nil || *stats.LastWebSocketError != "WebSocket connect timeout after 50ms" {
		t.Fatalf("last error = %v", stats.LastWebSocketError)
	}
}

func TestOpenAICodexStreamReconnectsOnConnectionLimit(t *testing.T) {
	codexResetWSState()
	token := codexMockToken("acc_test")
	fetchCalls := 0
	stub := stubCodexDialer(t, func(call int, requestURL string, headers http.Header) (*codexMockWSConn, error) {
		conn := newCodexMockWSConn()
		limitReached := call == 1
		conn.onSend = func(conn *codexMockWSConn, text string) {
			if limitReached {
				conn.dispatch([]map[string]any{{
					"type":  "error",
					"error": map[string]any{"code": "websocket_connection_limit_reached"},
				}}...)
				return
			}
			conn.dispatch([]map[string]any{{
				"type": "response.completed",
				"response": map[string]any{
					"id": "resp_1", "status": "completed",
					"usage": map[string]any{"input_tokens": 5, "output_tokens": 3, "total_tokens": 8},
				},
			}}...)
		}
		return conn, nil
	})

	model := codexTestModel()
	options := &types.StreamOptions{}
	options.APIKey = token
	options.Fetch = func(req *http.Request) (*http.Response, error) {
		fetchCalls++
		return nil, errors.New("fetch should not be called")
	}
	result := OpenAICodexResponses{}.Stream(context.Background(), model, &types.Context{SystemPrompt: "", Messages: []types.Message{}}, options).Result()

	if result.StopReason != types.StopReasonStop {
		t.Fatalf("stopReason = %q (%q)", result.StopReason, result.ErrorMessage)
	}
	if fetchCalls != 0 {
		t.Fatalf("fetch calls = %d", fetchCalls)
	}
	if stub.dialCount() != 2 {
		t.Fatalf("dials = %d", stub.dialCount())
	}
}

func TestOpenAICodexStreamFallsBackToSSEWhenIdleBeforeFirstEvent(t *testing.T) {
	codexResetWSState()
	token := codexMockToken("acc_test")
	fetchCalls := 0
	var sentBodies []map[string]any
	stubCodexDialer(t, func(call int, requestURL string, headers http.Header) (*codexMockWSConn, error) {
		conn := newCodexMockWSConn()
		conn.onSend = func(conn *codexMockWSConn, text string) {
			var body map[string]any
			_ = json.Unmarshal([]byte(text), &body)
			sentBodies = append(sentBodies, body)
			// No response events: the idle timeout must fire.
		}
		return conn, nil
	})

	model := codexTestModel()
	llmContext := &types.Context{
		SystemPrompt: "You are a helpful assistant.",
		Messages:     []types.Message{codexUserMessage("Say hello", 1)},
	}
	options := &types.StreamOptions{Transport: types.TransportAuto}
	options.APIKey = token
	options.SessionID = "ws-idle-before-start"
	options.TimeoutMs = 50
	options.Fetch = func(req *http.Request) (*http.Response, error) {
		fetchCalls++
		return codexSSEResponse(codexHelloSSE()), nil
	}
	result := OpenAICodexResponses{}.Stream(context.Background(), model, llmContext, options).Result()

	if codexResultText(result) != "Hello" {
		t.Fatalf("text = %q", codexResultText(result))
	}
	if fetchCalls != 1 {
		t.Fatalf("fetch calls = %d", fetchCalls)
	}
	if len(sentBodies) != 1 {
		t.Fatalf("sent bodies = %d", len(sentBodies))
	}
	stats := GetOpenAICodexWebSocketDebugStats("ws-idle-before-start")
	if stats == nil || stats.WebSocketFailures != 1 || stats.SSEFallbacks != 1 {
		t.Fatalf("stats = %+v", stats)
	}
	if stats.WebsocketFallbackActive == nil || !*stats.WebsocketFallbackActive {
		t.Fatalf("fallback active = %v", stats.WebsocketFallbackActive)
	}
}

func TestOpenAICodexStreamErrorsWhenIdleAfterStreamStarted(t *testing.T) {
	codexResetWSState()
	token := codexMockToken("acc_test")
	fetchCalls := 0
	stubCodexDialer(t, func(call int, requestURL string, headers http.Header) (*codexMockWSConn, error) {
		conn := newCodexMockWSConn()
		conn.onSend = func(conn *codexMockWSConn, text string) {
			conn.dispatch([]map[string]any{{
				"type": "response.output_item.added",
				"item": map[string]any{"type": "message", "id": "msg_1", "role": "assistant", "status": "in_progress", "content": []any{}},
			}}...)
			// Then nothing: the idle timeout fires after the stream started.
		}
		return conn, nil
	})

	model := codexTestModel()
	llmContext := &types.Context{
		SystemPrompt: "You are a helpful assistant.",
		Messages:     []types.Message{codexUserMessage("Say hello", 1)},
	}
	options := &types.StreamOptions{Transport: types.TransportAuto}
	options.APIKey = token
	options.TimeoutMs = 50
	options.Fetch = func(req *http.Request) (*http.Response, error) {
		fetchCalls++
		return codexJSONResponse(500, "unexpected fetch", nil), nil
	}
	result := OpenAICodexResponses{}.Stream(context.Background(), model, llmContext, options).Result()

	if result.StopReason != types.StopReasonError {
		t.Fatalf("stopReason = %q", result.StopReason)
	}
	if result.ErrorMessage != "WebSocket idle timeout after 50ms" {
		t.Fatalf("errorMessage = %q", result.ErrorMessage)
	}
	if fetchCalls != 0 {
		t.Fatalf("fetch calls = %d", fetchCalls)
	}
}

func TestOpenAICodexStreamFreshCachedWebsocketBeforeAgeLimit(t *testing.T) {
	codexResetWSState()
	token := codexMockToken("acc_test")
	var sentConnectionIDs []int
	stubCodexDialer(t, func(call int, requestURL string, headers http.Header) (*codexMockWSConn, error) {
		conn := newCodexMockWSConn()
		conn.onSend = func(conn *codexMockWSConn, text string) {
			sentConnectionIDs = append(sentConnectionIDs, call)
			conn.dispatch([]map[string]any{{
				"type": "response.completed",
				"response": map[string]any{
					"id": fmt.Sprintf("resp_%d", call), "status": "completed",
					"usage": map[string]any{"input_tokens": 5, "output_tokens": 3, "total_tokens": 8},
				},
			}}...)
		}
		return conn, nil
	})

	model := codexTestModel()
	sessionID := "aged-ws-session"
	firstContext := &types.Context{
		SystemPrompt: "You are a helpful assistant.",
		Messages:     []types.Message{codexUserMessage("Say hello", 1)},
	}
	firstOptions := &types.StreamOptions{}
	firstOptions.APIKey = token
	firstOptions.SessionID = sessionID
	firstOptions.Transport = types.TransportWebsocketCached
	first := OpenAICodexResponses{}.Stream(context.Background(), model, firstContext, firstOptions).Result()

	// Rewind the pooled connection's creation time past the 55-minute age
	// limit (pi: vi.setSystemTime(+56min)).
	codexWSStateMu.Lock()
	for _, accountEntries := range codexWSSessionCache[sessionID] {
		accountEntries.createdAt = accountEntries.createdAt.Add(-56 * time.Minute)
	}
	codexWSStateMu.Unlock()

	secondContext := &types.Context{
		SystemPrompt: "You are a helpful assistant.",
		Messages:     []types.Message{codexUserMessage("Say hello", 1), first, codexUserMessage("Now finish", 2)},
	}
	secondOptions := &types.StreamOptions{}
	secondOptions.APIKey = token
	secondOptions.SessionID = sessionID
	secondOptions.Transport = types.TransportWebsocketCached
	OpenAICodexResponses{}.Stream(context.Background(), model, secondContext, secondOptions).Result()

	if len(sentConnectionIDs) != 2 || sentConnectionIDs[0] != 1 || sentConnectionIDs[1] != 2 {
		t.Fatalf("sent connection ids = %v", sentConnectionIDs)
	}
	stats := GetOpenAICodexWebSocketDebugStats(sessionID)
	if stats == nil || stats.ConnectionsCreated != 2 || stats.ConnectionsReused != 0 {
		t.Fatalf("stats = %+v", stats)
	}
}

func TestOpenAICodexStreamSendsOnlyInputDeltasInCachedMode(t *testing.T) {
	codexResetWSState()
	token := codexMockToken("acc_test")
	var sentBodies []map[string]any
	stubCodexDialer(t, func(call int, requestURL string, headers http.Header) (*codexMockWSConn, error) {
		conn := newCodexMockWSConn()
		conn.onSend = func(conn *codexMockWSConn, text string) {
			var body map[string]any
			_ = json.Unmarshal([]byte(text), &body)
			sentBodies = append(sentBodies, body)
			responseID := fmt.Sprintf("resp_%d", len(sentBodies))
			if len(sentBodies) == 1 {
				conn.dispatch([]map[string]any{
					{"type": "response.created", "response": map[string]any{"id": responseID}},
					{"type": "response.output_item.added", "item": map[string]any{
						"type": "custom_tool_call", "id": "ctc_1", "call_id": "call_1", "name": "sample_tool", "input": "",
					}},
					{"type": "response.custom_tool_call_input.delta", "item_id": "ctc_1", "delta": "abc"},
					{"type": "response.custom_tool_call_input.done", "item_id": "ctc_1", "input": "abc"},
					{"type": "response.output_item.done", "item": map[string]any{
						"type": "custom_tool_call", "id": "ctc_1", "call_id": "call_1", "name": "sample_tool", "input": "abc",
					}},
					{"type": "response.completed", "response": map[string]any{
						"id": responseID, "status": "completed",
						"usage": map[string]any{"input_tokens": 5, "output_tokens": 3, "total_tokens": 8, "input_tokens_details": map[string]any{"cached_tokens": 0}},
					}},
				}...)
				return
			}
			conn.dispatch([]map[string]any{
				{"type": "response.created", "response": map[string]any{"id": responseID}},
				{"type": "response.completed", "response": map[string]any{
					"id": responseID, "status": "completed",
					"usage": map[string]any{"input_tokens": 5, "output_tokens": 3, "total_tokens": 8, "input_tokens_details": map[string]any{"cached_tokens": 0}},
				}},
			}...)
		}
		return conn, nil
	})

	model := codexTestModel()
	model.Compat = &types.OpenAIResponsesCompat{SupportsOpenAIGrammarTools: codexBoolPtr(true)}
	firstContext := &types.Context{
		SystemPrompt: "You are a helpful assistant.",
		Messages:     []types.Message{codexUserMessage("Use the tool", 1)},
		Tools: []types.Tool{{
			Name:        "sample_tool",
			Description: "Sample tool",
			// TypeBox's Type.Object adds required + additionalProperties:false.
			Parameters: json.RawMessage(`{"type":"object","properties":{"payload":{"type":"string"}},"required":["payload"],"additionalProperties":false}`),
			ConstrainedSampling: map[string]any{
				"type":     "grammar",
				"variants": map[string]any{"openai_lark": "start: /[a-z]+/"},
			},
		}},
	}
	firstOptions := &types.StreamOptions{}
	firstOptions.APIKey = token
	firstOptions.SessionID = "session-1"
	firstOptions.Transport = types.TransportWebsocketCached
	first := OpenAICodexResponses{}.Stream(context.Background(), model, firstContext, firstOptions).Result()

	secondContext := &types.Context{
		SystemPrompt: firstContext.SystemPrompt,
		Messages: []types.Message{
			codexUserMessage("Use the tool", 1),
			first,
			types.ToolResultMessage{
				Role:       types.RoleToolResult,
				ToolCallID: "call_1|ctc_1",
				ToolName:   "sample_tool",
				Content:    []types.Content{types.TextContent{Kind: types.KindText, Text: "real result"}},
				IsError:    false,
				Timestamp:  2,
			},
			codexUserMessage("Now finish", 3),
		},
		Tools: firstContext.Tools,
	}
	secondOptions := &types.StreamOptions{}
	secondOptions.APIKey = token
	secondOptions.SessionID = "session-1"
	secondOptions.Transport = types.TransportWebsocketCached
	OpenAICodexResponses{}.Stream(context.Background(), model, secondContext, secondOptions).Result()

	if len(sentBodies) != 2 {
		t.Fatalf("sent bodies = %d", len(sentBodies))
	}
	firstBody := sentBodies[0]
	secondBody := sentBodies[1]
	if firstBody["store"] != false {
		t.Fatalf("first store = %v", firstBody["store"])
	}
	if _, ok := firstBody["previous_response_id"]; ok {
		t.Fatal("first previous_response_id should be absent")
	}
	firstInput := fmt.Sprint(firstBody["input"])
	wantFirstInput := fmt.Sprint([]any{map[string]any{
		"role":    "user",
		"content": []any{map[string]any{"type": "input_text", "text": "Use the tool"}},
	}})
	if firstInput != wantFirstInput {
		t.Fatalf("first input = %s", firstInput)
	}
	if secondBody["store"] != false {
		t.Fatalf("second store = %v", secondBody["store"])
	}
	if secondBody["previous_response_id"] != "resp_1" {
		t.Fatalf("second previous_response_id = %v", secondBody["previous_response_id"])
	}
	secondInput := fmt.Sprint(secondBody["input"])
	wantSecondInput := fmt.Sprint([]any{
		map[string]any{"type": "custom_tool_call_output", "call_id": "call_1", "output": "real result"},
		map[string]any{"role": "user", "content": []any{map[string]any{"type": "input_text", "text": "Now finish"}}},
	})
	if secondInput != wantSecondInput {
		t.Fatalf("second input = %s", secondInput)
	}
	stats := GetOpenAICodexWebSocketDebugStats("session-1")
	if stats == nil {
		t.Fatal("missing stats")
	}
	if stats.Requests != 2 || stats.ConnectionsCreated != 1 || stats.ConnectionsReused != 1 ||
		stats.CachedContextRequests != 2 || stats.StoreTrueRequests != 0 ||
		stats.FullContextRequests != 1 || stats.DeltaRequests != 1 {
		t.Fatalf("stats = %+v", stats)
	}
	if stats.LastDeltaInputItems == nil || *stats.LastDeltaInputItems != 2 {
		t.Fatalf("lastDeltaInputItems = %v", stats.LastDeltaInputItems)
	}
	if stats.LastPreviousResponseID == nil || *stats.LastPreviousResponseID != "resp_1" {
		t.Fatalf("lastPreviousResponseId = %v", stats.LastPreviousResponseID)
	}
}

func TestOpenAICodexStreamMissingContinuationRecovery(t *testing.T) {
	codexResetWSState()
	token := codexMockToken("acc_test")
	for _, recoveryTransport := range []string{"websocket", "sse"} {
		t.Run(recoveryTransport, func(t *testing.T) {
			codexResetWSState()
			sessionID := "missing-continuation-" + recoveryTransport
			fetchCalls := 0
			type sentBody struct {
				connectionID int
				body         map[string]any
			}
			var sentBodies []sentBody

			stub := stubCodexDialer(t, func(call int, requestURL string, headers http.Header) (*codexMockWSConn, error) {
				conn := newCodexMockWSConn()
				conn.onSend = func(conn *codexMockWSConn, text string) {
					var body map[string]any
					_ = json.Unmarshal([]byte(text), &body)
					sentBodies = append(sentBodies, sentBody{connectionID: call, body: body})
					switch {
					case len(sentBodies) == 2:
						conn.dispatch(
							map[string]any{
								"type":      "codex.rate_limits",
								"plan_type": "plus",
								"rate_limits": map[string]any{
									"allowed": true, "limit_reached": false,
									"primary":   map[string]any{"used_percent": 7, "window_minutes": 10080, "reset_after_seconds": 556112, "reset_at": 1785269351},
									"secondary": nil,
								},
								"code_review_rate_limits": nil,
								"additional_rate_limits":  nil,
								"credits":                 map[string]any{"has_credits": false, "unlimited": false, "balance": "0"},
								"promo":                   nil,
							},
							map[string]any{
								"type":   "error",
								"status": 400,
								"error": map[string]any{
									"code":    "previous_response_not_found",
									"message": "Previous response with id 'resp_1' not found.",
									"param":   "previous_response_id",
								},
							},
						)
					case len(sentBodies) == 3 && recoveryTransport == "sse":
						conn.failWith(errors.New("retry websocket failed"))
					default:
						responseID, messageID, text := "resp_1", "msg_1", "Hello"
						if len(sentBodies) == 3 {
							responseID, messageID, text = "resp_2", "msg_2", "Recovered"
						}
						conn.dispatch(
							map[string]any{"type": "response.created", "response": map[string]any{"id": responseID}},
							map[string]any{"type": "response.output_item.added", "output_index": 0, "item": map[string]any{
								"type": "message", "id": messageID, "role": "assistant", "status": "in_progress", "content": []any{},
							}},
							map[string]any{"type": "response.output_item.done", "output_index": 0, "item": map[string]any{
								"type": "message", "id": messageID, "role": "assistant", "status": "completed",
								"content": []any{map[string]any{"type": "output_text", "text": text}},
							}},
							map[string]any{"type": "response.completed", "response": map[string]any{
								"id": responseID, "status": "completed",
								"usage": map[string]any{"input_tokens": 5, "output_tokens": 3, "total_tokens": 8},
							}},
						)
					}
				}
				return conn, nil
			})

			model := codexTestModel()
			firstContext := &types.Context{
				SystemPrompt: "You are a helpful assistant.",
				Messages:     []types.Message{codexUserMessage("Say hello", 1)},
			}
			firstOptions := &types.StreamOptions{}
			firstOptions.APIKey = token
			firstOptions.SessionID = sessionID
			firstOptions.Transport = types.TransportWebsocketCached
			first := OpenAICodexResponses{}.Stream(context.Background(), model, firstContext, firstOptions).Result()

			secondContext := &types.Context{
				SystemPrompt: "You are a helpful assistant.",
				Messages:     []types.Message{codexUserMessage("Say hello", 1), first, codexUserMessage("Now finish", 2)},
			}
			var eventTypes []string
			secondOptions := &types.StreamOptions{}
			secondOptions.APIKey = token
			secondOptions.SessionID = sessionID
			secondOptions.Transport = types.TransportWebsocketCached
			secondOptions.Fetch = func(req *http.Request) (*http.Response, error) {
				fetchCalls++
				return codexSSEResponse(codexHelloSSE()), nil
			}
			stream := OpenAICodexResponses{}.Stream(context.Background(), model, secondContext, secondOptions)
			for event := range stream.Events() {
				eventTypes = append(eventTypes, string(event.Type))
			}
			second := stream.Result()

			if second.StopReason != types.StopReasonStop {
				t.Fatalf("stopReason = %q (%q)", second.StopReason, second.ErrorMessage)
			}
			wantText := "Recovered"
			if recoveryTransport == "sse" {
				wantText = "Hello"
			}
			if codexResultText(second) != wantText {
				t.Fatalf("text = %q, want %q", codexResultText(second), wantText)
			}
			if codexCount(eventTypes, "start") != 1 {
				t.Fatalf("start events = %d (%v)", codexCount(eventTypes, "start"), eventTypes)
			}
			if codexContains(eventTypes, "error") {
				t.Fatalf("events should not contain error: %v", eventTypes)
			}
			if stub.dialCount() != 2 {
				t.Fatalf("connections = %d", stub.dialCount())
			}
			if len(sentBodies) != 3 {
				t.Fatalf("sent bodies = %d", len(sentBodies))
			}
			if sentBodies[0].connectionID != 1 || sentBodies[1].connectionID != 1 || sentBodies[2].connectionID != 2 {
				t.Fatalf("connection ids = %d %d %d", sentBodies[0].connectionID, sentBodies[1].connectionID, sentBodies[2].connectionID)
			}
			if sentBodies[1].body["previous_response_id"] != "resp_1" {
				t.Fatalf("second previous_response_id = %v", sentBodies[1].body["previous_response_id"])
			}
			wantSecondInput := fmt.Sprint([]any{map[string]any{
				"role": "user", "content": []any{map[string]any{"type": "input_text", "text": "Now finish"}},
			}})
			if fmt.Sprint(sentBodies[1].body["input"]) != wantSecondInput {
				t.Fatalf("second input = %s", sentBodies[1].body["input"])
			}
			if _, ok := sentBodies[2].body["previous_response_id"]; ok {
				t.Fatal("third previous_response_id should be absent")
			}
			thirdInput, _ := sentBodies[2].body["input"].([]any)
			if len(thirdInput) != 3 {
				t.Fatalf("third input length = %d", len(thirdInput))
			}
			last, _ := thirdInput[len(thirdInput)-1].(map[string]any)
			wantLast := map[string]any{"role": "user", "content": []any{map[string]any{"type": "input_text", "text": "Now finish"}}}
			if fmt.Sprint(last) != fmt.Sprint(wantLast) {
				t.Fatalf("last input item = %v", last)
			}
			wantFetchCalls := 0
			if recoveryTransport == "sse" {
				wantFetchCalls = 1
			}
			if fetchCalls != wantFetchCalls {
				t.Fatalf("fetch calls = %d, want %d", fetchCalls, wantFetchCalls)
			}
			stats := GetOpenAICodexWebSocketDebugStats(sessionID)
			if stats == nil {
				t.Fatal("missing stats")
			}
			if stats.Requests != 3 || stats.ConnectionsCreated != 2 || stats.ConnectionsReused != 1 ||
				stats.FullContextRequests != 2 || stats.DeltaRequests != 1 {
				t.Fatalf("stats = %+v", stats)
			}
			wantFailures, wantFallbacks := 0, 0
			if recoveryTransport == "sse" {
				wantFailures, wantFallbacks = 1, 1
			}
			if stats.WebSocketFailures != wantFailures || stats.SSEFallbacks != wantFallbacks {
				t.Fatalf("failures/fallbacks = %d/%d", stats.WebSocketFailures, stats.SSEFallbacks)
			}
		})
	}
}

// =============================================================================
// Retry semantics
// =============================================================================

func TestOpenAICodexStreamRetryAfterVariants(t *testing.T) {
	codexResetWSState()
	token := codexMockToken("acc_test")
	cases := []struct {
		name        string
		makeHeaders func() http.Header
		expected    time.Duration
		tolerance   time.Duration
	}{
		{
			name:        "retry-after-ms",
			makeHeaders: func() http.Header { return http.Header{"Retry-After-Ms": []string{"1500"}} },
			expected:    1500 * time.Millisecond,
		},
		{
			name:        "retry-after seconds",
			makeHeaders: func() http.Header { return http.Header{"Retry-After": []string{"60"}} },
			expected:    60_000 * time.Millisecond,
		},
		{
			name: "retry-after HTTP date",
			makeHeaders: func() http.Header {
				return http.Header{"Retry-After": []string{time.Now().UTC().Add(45 * time.Second).Format(http.TimeFormat)}}
			},
			expected:  45_000 * time.Millisecond,
			tolerance: time.Second,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			codexResetWSState()
			delays := stubCodexSleepRecorder(t)
			var codexRequests int
			model := codexTestModel()
			llmContext := &types.Context{
				SystemPrompt: "You are a helpful assistant.",
				Messages:     []types.Message{codexUserMessage("Say hello", time.Now().UnixMilli())},
			}
			base := types.StreamOptions{}
			base.Transport = types.TransportSSE
			base.APIKey = token
			base.MaxRetries = codexIntPtr(1)
			base.Fetch = func(req *http.Request) (*http.Response, error) {
				codexRequests++
				if codexRequests == 1 {
					headers := http.Header{"Content-Type": []string{"application/json"}}
					for key, values := range tc.makeHeaders() {
						headers[key] = values
					}
					return codexJSONResponse(429, `{"error":{"code":"rate_limit_exceeded","message":"rate limited"}}`, headers), nil
				}
				return codexSSEResponse(codexHelloSSE()), nil
			}
			result := streamOpenAICodexResponses(context.Background(), model, llmContext, &OpenAICodexResponsesOptions{
				StreamOptions: base,
			}).Result()

			if codexRequests != 2 {
				t.Fatalf("codex requests = %d", codexRequests)
			}
			if codexResultText(result) != "Hello" {
				t.Fatalf("text = %q", codexResultText(result))
			}
			if len(*delays) != 1 {
				t.Fatalf("delays = %v, want one entry", *delays)
			}
			delay := (*delays)[0]
			if delay < tc.expected-tc.tolerance || delay > tc.expected+tc.tolerance {
				t.Fatalf("delay = %v, want %v (±%v)", delay, tc.expected, tc.tolerance)
			}
		})
	}
}

func TestOpenAICodexStreamRetryDelayExceeded(t *testing.T) {
	codexResetWSState()
	token := codexMockToken("acc_test")
	for _, status := range []int{429, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var fetchCalls int
			model := codexTestModel()
			llmContext := &types.Context{
				SystemPrompt: "You are a helpful assistant.",
				Messages:     []types.Message{codexUserMessage("Say hello", time.Now().UnixMilli())},
			}
			base := types.StreamOptions{}
			base.Transport = types.TransportSSE
			base.APIKey = token
			base.MaxRetries = codexIntPtr(3)
			base.MaxRetryDelayMs = codexInt64Ptr(1000)
			base.Fetch = func(req *http.Request) (*http.Response, error) {
				fetchCalls++
				return codexJSONResponse(status, `{"error":{"code":"temporarily_unavailable","message":"retry later"}}`,
					http.Header{"Content-Type": []string{"application/json"}, "Retry-After": []string{"2"}}), nil
			}
			result := streamOpenAICodexResponses(context.Background(), model, llmContext, &OpenAICodexResponsesOptions{
				StreamOptions: base,
			}).Result()

			if result.StopReason != types.StopReasonError {
				t.Fatalf("stopReason = %q", result.StopReason)
			}
			if result.ErrorMessage != "Server requested 2s retry delay (max: 1s)" {
				t.Fatalf("errorMessage = %q", result.ErrorMessage)
			}
			if fetchCalls != 1 {
				t.Fatalf("fetch calls = %d", fetchCalls)
			}
		})
	}
}

func TestOpenAICodexStreamExponentialBackoff(t *testing.T) {
	codexResetWSState()
	token := codexMockToken("acc_test")
	delays := stubCodexSleepRecorder(t)
	var codexRequests int
	model := codexTestModel()
	llmContext := &types.Context{
		SystemPrompt: "You are a helpful assistant.",
		Messages:     []types.Message{codexUserMessage("Say hello", time.Now().UnixMilli())},
	}
	base := types.StreamOptions{}
	base.Transport = types.TransportSSE
	base.APIKey = token
	base.MaxRetries = codexIntPtr(3)
	base.Fetch = func(req *http.Request) (*http.Response, error) {
		codexRequests++
		if codexRequests <= 3 {
			return codexJSONResponse(429, `{"error":{"code":"rate_limit_exceeded","message":"rate limited"}}`, nil), nil
		}
		return codexSSEResponse(codexHelloSSE()), nil
	}
	result := streamOpenAICodexResponses(context.Background(), model, llmContext, &OpenAICodexResponsesOptions{
		StreamOptions: base,
	}).Result()

	if codexResultText(result) != "Hello" {
		t.Fatalf("text = %q", codexResultText(result))
	}
	if codexRequests != 4 {
		t.Fatalf("codex requests = %d", codexRequests)
	}
	wantDelays := []time.Duration{1000 * time.Millisecond, 2000 * time.Millisecond, 4000 * time.Millisecond}
	if fmt.Sprint(*delays) != fmt.Sprint(wantDelays) {
		t.Fatalf("delays = %v, want %v", *delays, wantDelays)
	}
}

// =============================================================================
// Request compression
// =============================================================================

func TestOpenAICodexStreamZstdCompressesRequestBodies(t *testing.T) {
	codexResetWSState()
	token := codexMockToken("acc_test")
	var capturedEncoding string
	var capturedBody []byte
	model := codexTestModel()
	largeText := strings.Repeat("compress me ", 400)

	run := func(userText string) {
		capturedEncoding = ""
		capturedBody = nil
		llmContext := &types.Context{
			SystemPrompt: "You are a helpful assistant.",
			Messages:     []types.Message{codexUserMessage(userText, 1)},
		}
		options := &types.StreamOptions{}
		options.APIKey = token
		options.Transport = types.TransportSSE
		options.Fetch = func(req *http.Request) (*http.Response, error) {
			capturedEncoding = codexReqHeader(req, "content-encoding")
			capturedBody, _ = io.ReadAll(req.Body)
			return codexSSEResponse(codexHelloSSE()), nil
		}
		OpenAICodexResponses{}.Stream(context.Background(), model, llmContext, options).Result()
	}

	run(largeText)
	if capturedEncoding != "zstd" {
		t.Fatalf("content-encoding = %q", capturedEncoding)
	}
	if len(capturedBody) == 0 {
		t.Fatal("captured body is empty")
	}
	reader, err := zstd.NewReader(bytes.NewReader(capturedBody))
	if err != nil {
		t.Fatalf("zstd reader: %v", err)
	}
	defer reader.Close()
	decoded, err := io.ReadAll(reader.IOReadCloser())
	if err != nil {
		t.Fatalf("zstd decompress: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(decoded, &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	input, _ := body["input"].([]any)
	first, _ := input[0].(map[string]any)
	content, _ := first["content"].([]any)
	part, _ := content[0].(map[string]any)
	if part["text"] != largeText {
		t.Fatalf("roundtrip text mismatch: got %d chars, want %d", len(fmt.Sprint(part["text"])), len(largeText))
	}

	run("hi")
	if capturedEncoding != "zstd" {
		t.Fatalf("small body content-encoding = %q", capturedEncoding)
	}
	if len(capturedBody) == 0 {
		t.Fatal("small captured body is empty")
	}
}
