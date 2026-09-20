// pi: test/google-raw-stop-reason.test.ts.
//
// pi mocks the @google/genai SDK at module level; pig overrides the
// googleGenerateContentStream seam with canned chunks (and asserts the client
// config the SDK constructor would have received). The Google Vertex cases
// (SAFETY error path, MAX_TOKENS/STOP with tool calls over vertex) run
// against the googleVertexGenerateContentStream seam (C4).
package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/ai/utils"
)

// googleChunkSlice is a canned chunk source (pi: the mocked SDK's async
// generator).
type googleChunkSlice struct {
	chunks []*googleGenerateContentResponse
	index  int
}

func (s *googleChunkSlice) Next() (*googleGenerateContentResponse, error) {
	if s.index >= len(s.chunks) {
		return nil, io.EOF
	}
	chunk := s.chunks[s.index]
	s.index++
	return chunk, nil
}

func (s *googleChunkSlice) Close() {}

// googleSeamState captures what the transport received (pi:
// googleGenAiMock.constructorCalls).
type googleSeamState struct {
	constructorCalls []*googleGenAIClient
	chunks           []*googleGenerateContentResponse
}

func stubGoogleSeam(t *testing.T, state *googleSeamState) {
	t.Helper()
	previous := googleGenerateContentStream
	googleGenerateContentStream = func(ctx context.Context, client *googleGenAIClient, params *googleGenerateContentParams) (googleChunkSource, error) {
		state.constructorCalls = append(state.constructorCalls, client)
		return &googleChunkSlice{chunks: state.chunks}, nil
	}
	t.Cleanup(func() { googleGenerateContentStream = previous })
}

func googleStopReasonChunk(finishReason string, includeFunctionCall bool) *googleGenerateContentResponse {
	chunk := &googleGenerateContentResponse{
		ResponseID: "google-response-id",
		Candidates: []googleCandidate{{FinishReason: finishReason}},
		UsageMetadata: &googleUsageMetadata{
			PromptTokenCount: 1, CandidatesTokenCount: 0, TotalTokenCount: 1,
		},
	}
	if includeFunctionCall {
		chunk.Candidates[0].Content = &googleContent{
			Parts: []googlePart{{FunctionCall: &googleFunctionCall{
				ID: "call-1", Name: "echo", Args: map[string]any{"value": "truncated"},
			}}},
		}
	}
	return chunk
}

func googleStopReasonContext() *types.TranscriptContext {
	return utils.NormalizeContext(&types.Context{
		Messages: []types.Message{
			types.UserMessage{Role: types.RoleUser, Content: types.UserContent{String: "hello"}, Timestamp: time.Now().UnixMilli()},
		},
	})
}

func googleCatalogModel(provider ai.ProviderID, id string) *types.Model {
	base, ok := ai.MODELS[provider][id]
	if !ok {
		return nil
	}
	return &base
}

func TestGoogleRawStopReasonPreservesMalformedFunctionCall(t *testing.T) {
	state := &googleSeamState{chunks: []*googleGenerateContentResponse{
		googleStopReasonChunk("MALFORMED_FUNCTION_CALL", false),
	}}
	stubGoogleSeam(t, state)

	model := googleCatalogModel(ai.ProviderGoogle, "gemini-2.5-flash")
	if model == nil {
		t.Fatal("gemini-2.5-flash missing from the catalog")
	}
	streamOptions := &types.StreamOptions{}
	streamOptions.APIKey = "test-api-key"
	stream := GoogleGenerativeAI{}.Stream(context.Background(), model, googleStopReasonContext(), streamOptions)
	_, result := collectResponsesEvents(t, stream)

	if result.StopReason != types.StopReasonError {
		t.Errorf("stopReason = %q, want error", result.StopReason)
	}
	if result.RawStopReason != "MALFORMED_FUNCTION_CALL" {
		t.Errorf("rawStopReason = %q, want MALFORMED_FUNCTION_CALL", result.RawStopReason)
	}
	if result.ErrorMessage != "Provider stopped with: MALFORMED_FUNCTION_CALL" {
		t.Errorf("errorMessage = %q", result.ErrorMessage)
	}
}

// TestGoogleRawStopReasonPreservesVertexSafety covers the vertex half (pi:
// streamGoogleVertex with SAFETY); C4.
func TestGoogleRawStopReasonPreservesVertexSafety(t *testing.T) {
	state := &vertexSeamState{chunks: []*googleGenerateContentResponse{
		googleStopReasonChunk("SAFETY", false),
	}}
	stubVertexSeam(t, state)

	model := vertexCatalogModel(t, "gemini-3-flash-preview")
	options := &GoogleVertexOptions{Project: "test-project", Location: "us-central1"}
	stream := streamGoogleVertex(context.Background(), model, googleStopReasonContext(), options)
	_, result := collectResponsesEvents(t, stream)

	if result.StopReason != types.StopReasonError {
		t.Errorf("stopReason = %q, want error", result.StopReason)
	}
	if result.RawStopReason != "SAFETY" {
		t.Errorf("rawStopReason = %q, want SAFETY", result.RawStopReason)
	}
	if result.ErrorMessage != "Provider stopped with: SAFETY" {
		t.Errorf("errorMessage = %q", result.ErrorMessage)
	}
}

func TestGoogleRawStopReasonMaxTokensWithToolCallIsLength(t *testing.T) {
	state := &googleSeamState{chunks: []*googleGenerateContentResponse{
		googleStopReasonChunk("MAX_TOKENS", true),
	}}
	stubGoogleSeam(t, state)

	model := googleCatalogModel(ai.ProviderGoogle, "gemini-2.5-flash")
	streamOptions := &types.StreamOptions{}
	streamOptions.APIKey = "test-api-key"
	stream := GoogleGenerativeAI{}.Stream(context.Background(), model, googleStopReasonContext(), streamOptions)
	_, result := collectResponsesEvents(t, stream)

	if result.StopReason != types.StopReasonLength {
		t.Errorf("stopReason = %q, want length", result.StopReason)
	}
	if result.RawStopReason != "MAX_TOKENS" {
		t.Errorf("rawStopReason = %q, want MAX_TOKENS", result.RawStopReason)
	}
	hasToolCall := false
	for _, block := range result.Content {
		if _, ok := block.(types.ToolCall); ok {
			hasToolCall = true
		}
	}
	if !hasToolCall {
		t.Error("expected a tool call block")
	}
}

func TestGoogleRawStopReasonStopWithToolCallIsToolUse(t *testing.T) {
	state := &googleSeamState{chunks: []*googleGenerateContentResponse{
		googleStopReasonChunk("STOP", true),
	}}
	stubGoogleSeam(t, state)

	model := googleCatalogModel(ai.ProviderGoogle, "gemini-2.5-flash")
	streamOptions := &types.StreamOptions{}
	streamOptions.APIKey = "test-api-key"
	stream := GoogleGenerativeAI{}.Stream(context.Background(), model, googleStopReasonContext(), streamOptions)
	_, result := collectResponsesEvents(t, stream)

	if result.StopReason != types.StopReasonToolUse {
		t.Errorf("stopReason = %q, want toolUse", result.StopReason)
	}
	if result.RawStopReason != "STOP" {
		t.Errorf("rawStopReason = %q, want STOP", result.RawStopReason)
	}
}

// TestGoogleRawStopReasonVertexMaxTokensWithToolCallIsLength covers the
// vertex half of pi's it.each(adapters) MAX_TOKENS case (C4).
func TestGoogleRawStopReasonVertexMaxTokensWithToolCallIsLength(t *testing.T) {
	state := &vertexSeamState{chunks: []*googleGenerateContentResponse{
		googleStopReasonChunk("MAX_TOKENS", true),
	}}
	stubVertexSeam(t, state)

	model := vertexCatalogModel(t, "gemini-3-flash-preview")
	options := &GoogleVertexOptions{Project: "test-project", Location: "us-central1"}
	stream := streamGoogleVertex(context.Background(), model, googleStopReasonContext(), options)
	_, result := collectResponsesEvents(t, stream)

	if result.StopReason != types.StopReasonLength {
		t.Errorf("stopReason = %q, want length", result.StopReason)
	}
	if result.RawStopReason != "MAX_TOKENS" {
		t.Errorf("rawStopReason = %q, want MAX_TOKENS", result.RawStopReason)
	}
	hasToolCall := false
	for _, block := range result.Content {
		if _, ok := block.(types.ToolCall); ok {
			hasToolCall = true
		}
	}
	if !hasToolCall {
		t.Error("expected a tool call block")
	}
}

// TestGoogleRawStopReasonVertexStopWithToolCallIsToolUse covers the vertex
// half of pi's it.each(adapters) STOP case (C4).
func TestGoogleRawStopReasonVertexStopWithToolCallIsToolUse(t *testing.T) {
	state := &vertexSeamState{chunks: []*googleGenerateContentResponse{
		googleStopReasonChunk("STOP", true),
	}}
	stubVertexSeam(t, state)

	model := vertexCatalogModel(t, "gemini-3-flash-preview")
	options := &GoogleVertexOptions{Project: "test-project", Location: "us-central1"}
	stream := streamGoogleVertex(context.Background(), model, googleStopReasonContext(), options)
	_, result := collectResponsesEvents(t, stream)

	if result.StopReason != types.StopReasonToolUse {
		t.Errorf("stopReason = %q, want toolUse", result.StopReason)
	}
	if result.RawStopReason != "STOP" {
		t.Errorf("rawStopReason = %q, want STOP", result.RawStopReason)
	}
	hasToolCall := false
	for _, block := range result.Content {
		if _, ok := block.(types.ToolCall); ok {
			hasToolCall = true
		}
	}
	if !hasToolCall {
		t.Error("expected a tool call block")
	}
}

// captureGoogleHeaders drives a stream and reports the headers the client
// would send (pi: captureGoogleHeaders via the mocked constructor's
// httpOptions.headers).
func captureGoogleHeaders(t *testing.T, headers types.ProviderHeaders) map[string]string {
	t.Helper()
	state := &googleSeamState{chunks: []*googleGenerateContentResponse{
		googleStopReasonChunk("STOP", false),
	}}
	stubGoogleSeam(t, state)

	model := googleCatalogModel(ai.ProviderGoogle, "gemini-2.5-flash")
	options := &types.StreamOptions{}
	options.APIKey = "test-api-key"
	options.Headers = headers
	stream := GoogleGenerativeAI{}.Stream(context.Background(), model, googleStopReasonContext(), options)
	_, result := collectResponsesEvents(t, stream)
	if result.StopReason != types.StopReasonStop {
		t.Fatalf("stream errored: %s", result.ErrorMessage)
	}
	if len(state.constructorCalls) != 1 {
		t.Fatalf("constructor calls = %d, want 1", len(state.constructorCalls))
	}
	return state.constructorCalls[0].headers
}

func TestGoogleGenerativeAIUserAgentDefault(t *testing.T) {
	headers := captureGoogleHeaders(t, nil)
	if headers["User-Agent"] != utils.GetPiUserAgent() {
		t.Errorf("User-Agent = %q, want %q", headers["User-Agent"], utils.GetPiUserAgent())
	}
}

func TestGoogleGenerativeAIUserAgentOverride(t *testing.T) {
	custom := "custom-agent"
	headers := captureGoogleHeaders(t, types.ProviderHeaders{"User-Agent": &custom})
	if headers["User-Agent"] != "custom-agent" {
		t.Errorf("User-Agent = %q, want custom-agent", headers["User-Agent"])
	}
}

// TestGoogleStreamRejectsCustomFetch pins pi's custom-fetch guard.
func TestGoogleStreamRejectsCustomFetch(t *testing.T) {
	model := googleCatalogModel(ai.ProviderGoogle, "gemini-2.5-flash")
	options := &types.StreamOptions{}
	options.APIKey = "test-api-key"
	options.Fetch = func(request *http.Request) (*http.Response, error) {
		return nil, errors.New("unused")
	}
	stream := GoogleGenerativeAI{}.Stream(context.Background(), model, googleStopReasonContext(), options)
	_, result := collectResponsesEvents(t, stream)
	if result.StopReason != types.StopReasonError ||
		result.ErrorMessage != "Custom fetch is not supported by the Google Generative AI adapter" {
		t.Errorf("result = %s / %q", result.StopReason, result.ErrorMessage)
	}
}
