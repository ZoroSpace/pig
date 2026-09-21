// cerebras_strict_test.go — pi: test/cache-retention.test.ts v0.86.1 addition
// (#9804): cerebras models reject the strict field on function tools, so
// supportsStrictMode=false and the request body carries no "strict" key (pi
// runs the cerebras catalog models against the live endpoint; pig uses the
// stub SSE server).
package providers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/api"
	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/ai/utils"
)

func TestCerebrasStrictModeOmitted(t *testing.T) {
	for _, modelID := range []string{"gpt-oss-120b", "qwen-3.8-27b"} {
		t.Run(modelID, func(t *testing.T) {
			model, ok := GetBuiltinModel("cerebras", modelID)
			if !ok {
				t.Fatalf("cerebras model %q missing from catalog", modelID)
			}
			compat, cast := model.Compat.(*ai.OpenAICompletionsCompat)
			if !cast || compat == nil || compat.SupportsStrictMode == nil || *compat.SupportsStrictMode {
				t.Fatalf("cerebras %s supportsStrictMode should be false, got %+v", modelID, model.Compat)
			}
			assertNoStrictField(t, captureBodyWithTools(t, model, strictTestTools()))
		})
	}

	// Runtime detectCompat path: a bare model without catalog compat resolves
	// strict mode from provider/baseUrl.
	t.Run("runtime-detect-compat", func(t *testing.T) {
		model := bareModel("cerebras", "https://api.cerebras.ai/v1")
		assertNoStrictField(t, captureBodyWithTools(t, model, strictTestTools()))
	})
}

func strictTestTools() []types.Tool {
	return []types.Tool{
		{
			Name:                "t1",
			Description:         "strict tool",
			Parameters:          json.RawMessage(`{"type":"object","properties":{"x":{"type":"string"}},"required":["x"]}`),
			ConstrainedSampling: map[string]any{"type": "json_schema"},
		},
		{
			Name:        "t2",
			Description: "non-strict tool",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"y":{"type":"string"}},"required":["y"]}`),
		},
	}
}

// assertNoStrictField checks every function tool omits the strict key.
func assertNoStrictField(t *testing.T, captured map[string]any) {
	t.Helper()
	rawTools, ok := captured["tools"].([]any)
	if !ok || len(rawTools) != 2 {
		t.Fatalf("tools = %v, want 2 function tools", captured["tools"])
	}
	for _, rawTool := range rawTools {
		tool := rawTool.(map[string]any)
		fn := tool["function"].(map[string]any)
		if _, present := fn["strict"]; present {
			t.Fatalf("cerebras request must not carry a strict field: %v", fn)
		}
	}
}

// captureBodyWithTools runs one StreamSimple with toolsAdded on the leading
// system message and returns the captured request body.
func captureBodyWithTools(t *testing.T, model types.Model, tools []types.Tool) map[string]any {
	t.Helper()
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &captured)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, "data: {\"id\":\"x\",\"model\":\"test-model\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(srv.Close)
	model.BaseURL = srv.URL

	opts := types.SimpleStreamOptions{}
	opts.APIKey = "test-key"
	var adapter api.OpenAICompletions
	stream := adapter.StreamSimple(context.Background(), &model, utils.NormalizeContext(&types.Context{
		SystemPrompt: "test",
		Messages: []types.Message{
			types.SystemMessage{Role: types.RoleSystem, ToolsAdded: tools, Timestamp: 0},
			types.UserMessage{Role: types.RoleUser, Content: types.UserContent{String: "hello"}, Timestamp: 1},
		},
	}), &opts)
	result := stream.Result()
	if result.StopReason == types.StopReasonError {
		t.Fatalf("stream error: %s", result.ErrorMessage)
	}
	if captured == nil {
		t.Fatal("no request captured")
	}
	return captured
}
