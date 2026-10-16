// pi: test/anthropic-strict-tool-fallback.test.ts. Regression tests for
// earendil-works/pi#10409: a 400 invalid_request_error naming a strict-tool
// capacity limit retries once with preferred strict tools downgraded, while
// required strict tools and the caller's transcript stay untouched.
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/samber/lo"

	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/ai/utils"
)

const strictGrammarError = "The compiled grammar is too large, which would cause performance issues. Simplify your tool schemas or reduce the number of strict tools."

func strictFallbackModel() *types.Model {
	return anthropicTestModel("claude-haiku-4-5", types.ProviderAnthropic, &types.AnthropicMessagesCompat{
		SupportsStrictTools: lo.ToPtr(true),
	})
}

func strictTool(name string, strict string) types.Tool {
	tool := types.Tool{
		Name:        name,
		Description: "Look up a value",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"value":{"type":"string"}},"required":["value"]}`),
	}
	if strict != "" {
		tool.ConstrainedSampling = map[string]any{"type": "json_schema", "strict": strict}
	}
	return tool
}

func strictToolContext(tools []types.Tool) *types.TranscriptContext {
	return utils.NormalizeContext(&types.Context{
		Messages: []types.Message{types.UserMessage{Role: types.RoleUser, Content: types.UserContent{String: "Hello"}, Timestamp: 1}},
		Tools:    tools,
	})
}

func strictErrorResponse(message string, status int, errType string) *http.Response {
	body := mustJSON(map[string]any{"type": "error", "error": map[string]any{"type": errType, "message": message}})
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func strictSuccessResponse(req *http.Request) *http.Response {
	var b strings.Builder
	for _, e := range minimalAnthropicEvents {
		fmt.Fprintf(&b, "event: %s\ndata: %s\n\n", e.event, e.data)
	}
	return &http.Response{
		StatusCode: 200,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(b.String())),
		Request:    req,
	}
}

// strictFallbackHarness records every request payload and delegates the
// response to respond (pi: harness()).
func strictFallbackHarness(respond func(attempt int) *http.Response) (*[]map[string]any, *AnthropicOptions) {
	payloads := &[]map[string]any{}
	options := &AnthropicOptions{}
	options.APIKey = "test-key"
	options.MaxRetries = lo.ToPtr(0)
	options.Fetch = func(req *http.Request) (*http.Response, error) {
		bodyBytes, _ := io.ReadAll(req.Body)
		var parsed map[string]any
		_ = json.Unmarshal(bodyBytes, &parsed)
		*payloads = append(*payloads, parsed)
		return respond(len(*payloads)), nil
	}
	return payloads, options
}

func toolStrict(payload map[string]any, index int) (any, bool) {
	tools, ok := payload["tools"].([]any)
	if !ok || index >= len(tools) {
		return nil, false
	}
	tool, ok := tools[index].(map[string]any)
	if !ok {
		return nil, false
	}
	strict, present := tool["strict"]
	return strict, present
}

func TestAnthropicStrictToolFallbackRetriesAndPreservesRequired(t *testing.T) {
	for _, message := range []string{
		strictGrammarError,
		"Too many strict tools (32). The maximum number of strict tools supported is 20. Try reducing the number of tools marked as strict.",
		"Schemas contains too many parameters with union types (480 parameters with type arrays or anyOf). This causes exponential compilation cost. Reduce the number of nullable or union-typed parameters (limit: 16 parameters with unions).",
	} {
		message := message
		t.Run(message[:24], func(t *testing.T) {
			transcript := strictToolContext([]types.Tool{
				strictTool("preferred", "prefer"),
				strictTool("required", "require"),
				strictTool("ordinary", ""),
			})
			original, _ := json.Marshal(transcript)
			payloads, options := strictFallbackHarness(func(attempt int) *http.Response {
				if attempt == 1 {
					return strictErrorResponse(message, 400, "invalid_request_error")
				}
				return strictSuccessResponse(nil)
			})
			onPayloadCalls := 0
			options.OnPayload = func(payload any, model *types.Model) any {
				onPayloadCalls++
				if m, ok := payload.(map[string]any); ok {
					m["inference_geo"] = "us"
					m["stream"] = false
				}
				return nil
			}
			onResponseCalls := 0
			options.OnResponse = func(resp types.ProviderResponse, model *types.Model) {
				onResponseCalls++
			}

			result := runAnthropicStream(t, strictFallbackModel(), transcript, options)

			if result.StopReason != types.StopReasonStop {
				t.Fatalf("stopReason = %v, want stop", result.StopReason)
			}
			if len(*payloads) != 2 {
				t.Fatalf("payloads = %d, want 2", len(*payloads))
			}
			if onPayloadCalls != 2 {
				t.Errorf("onPayload calls = %d, want 2", onPayloadCalls)
			}
			if onResponseCalls != 1 {
				t.Errorf("onResponse calls = %d, want 1", onResponseCalls)
			}
			if strict, _ := toolStrict((*payloads)[0], 0); strict != true {
				t.Errorf("first preferred strict = %v, want true", strict)
			}
			if strict, present := toolStrict((*payloads)[1], 0); present {
				t.Errorf("second preferred strict = %v, want absent", strict)
			}
			if strict, _ := toolStrict((*payloads)[1], 1); strict != true {
				t.Errorf("second required strict = %v, want true", strict)
			}
			if _, present := toolStrict((*payloads)[1], 2); present {
				t.Errorf("second ordinary strict should be absent")
			}
			for i, payload := range *payloads {
				if payload["stream"] != true {
					t.Errorf("payload[%d].stream = %v, want true (params forced over onPayload)", i, payload["stream"])
				}
			}
			if (*payloads)[1]["inference_geo"] != "us" {
				t.Errorf("second payload missing onPayload mutation")
			}
			after, _ := json.Marshal(transcript)
			if string(after) != string(original) {
				t.Errorf("transcript mutated:\nbefore %s\nafter  %s", original, after)
			}
			if len(result.Diagnostics) == 0 || result.Diagnostics[0].Type != "anthropic_strict_tools_fallback" {
				t.Fatalf("diagnostics = %+v, want anthropic_strict_tools_fallback", result.Diagnostics)
			}
			if reason := result.Diagnostics[0].Details["reason"]; reason != "strict_tool_limits_exceeded" {
				t.Errorf("diagnostic reason = %v, want strict_tool_limits_exceeded", reason)
			}
		})
	}
}

func TestAnthropicStrictToolFallbackDoesNotRetryNonPreferredSets(t *testing.T) {
	cases := map[string][]types.Tool{
		"required":                   {strictTool("lookup", "require")},
		"ordinary":                   {strictTool("lookup", "")},
		"locally unsupported prefer": {{Name: "lookup", Description: "Look up a value", Parameters: json.RawMessage(`{"type":"object","properties":{"value":{"type":"number","minimum":1}}}`), ConstrainedSampling: map[string]any{"type": "json_schema", "strict": "prefer"}}},
	}
	for name, tools := range cases {
		tools := tools
		t.Run(name, func(t *testing.T) {
			payloads, options := strictFallbackHarness(func(attempt int) *http.Response {
				return strictErrorResponse(strictGrammarError, 400, "invalid_request_error")
			})
			result := runAnthropicStream(t, strictFallbackModel(), strictToolContext(tools), options)
			if result.StopReason != types.StopReasonError {
				t.Fatalf("stopReason = %v, want error", result.StopReason)
			}
			if !strings.Contains(result.ErrorMessage, strictGrammarError) {
				t.Errorf("errorMessage = %q", result.ErrorMessage)
			}
			if len(*payloads) != 1 {
				t.Errorf("payloads = %d, want 1", len(*payloads))
			}
		})
	}
}

func TestAnthropicStrictToolFallbackDoesNotDowngradeOnOtherErrors(t *testing.T) {
	cases := map[string]struct {
		message string
		status  int
		errType string
	}{
		"unrelated 400":     {"Invalid tool schema", 400, "invalid_request_error"},
		"wrong error type":  {strictGrammarError, 400, "authentication_error"},
		"wrong HTTP status": {strictGrammarError, 500, "invalid_request_error"},
	}
	for name, c := range cases {
		c := c
		t.Run(name, func(t *testing.T) {
			payloads, options := strictFallbackHarness(func(attempt int) *http.Response {
				return strictErrorResponse(c.message, c.status, c.errType)
			})
			result := runAnthropicStream(t, strictFallbackModel(), strictToolContext([]types.Tool{strictTool("lookup", "prefer")}), options)
			if result.StopReason != types.StopReasonError {
				t.Fatalf("stopReason = %v, want error", result.StopReason)
			}
			if len(*payloads) != 1 {
				t.Errorf("payloads = %d, want 1", len(*payloads))
			}
		})
	}
}

func TestAnthropicStrictToolFallbackSurfacesSecondRejection(t *testing.T) {
	payloads, options := strictFallbackHarness(func(attempt int) *http.Response {
		if attempt == 1 {
			return strictErrorResponse(strictGrammarError, 400, "invalid_request_error")
		}
		return strictErrorResponse("Required grammar is still too large", 400, "invalid_request_error")
	})
	result := runAnthropicStream(t, strictFallbackModel(), strictToolContext([]types.Tool{
		strictTool("lookup", "prefer"),
		strictTool("required", "require"),
	}), options)
	if result.StopReason != types.StopReasonError {
		t.Fatalf("stopReason = %v, want error", result.StopReason)
	}
	if !strings.Contains(result.ErrorMessage, "Required grammar is still too large") {
		t.Errorf("errorMessage = %q", result.ErrorMessage)
	}
	if len(*payloads) != 2 {
		t.Fatalf("payloads = %d, want 2", len(*payloads))
	}
	if strict, _ := toolStrict((*payloads)[1], 1); strict != true {
		t.Errorf("second attempt required strict = %v, want true", strict)
	}
}

func TestAnthropicStrictToolFallbackHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	payloads := &[]map[string]any{}
	options := &AnthropicOptions{}
	options.APIKey = "test-key"
	options.MaxRetries = lo.ToPtr(0)
	options.Fetch = func(req *http.Request) (*http.Response, error) {
		bodyBytes, _ := io.ReadAll(req.Body)
		var parsed map[string]any
		_ = json.Unmarshal(bodyBytes, &parsed)
		*payloads = append(*payloads, parsed)
		cancel()
		return strictErrorResponse(strictGrammarError, 400, "invalid_request_error"), nil
	}
	stream := streamAnthropicMessages(ctx, strictFallbackModel(), strictToolContext([]types.Tool{strictTool("lookup", "prefer")}), options)
	result := stream.Result()
	if result.StopReason != types.StopReasonAborted {
		t.Errorf("stopReason = %v, want aborted", result.StopReason)
	}
	if len(*payloads) != 1 {
		t.Errorf("payloads = %d, want 1", len(*payloads))
	}
}

func TestAnthropicStrictToolFallbackDoesNotRetrySSEError(t *testing.T) {
	sseError := fmt.Sprintf("event: error\ndata: %s\n\n", mustJSON(map[string]any{
		"type":  "error",
		"error": map[string]any{"type": "invalid_request_error", "message": strictGrammarError},
	}))
	payloads, options := strictFallbackHarness(func(attempt int) *http.Response {
		return &http.Response{
			StatusCode: 200,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader(sseError)),
		}
	})
	result := runAnthropicStream(t, strictFallbackModel(), strictToolContext([]types.Tool{strictTool("lookup", "prefer")}), options)
	if result.StopReason != types.StopReasonError {
		t.Fatalf("stopReason = %v, want error", result.StopReason)
	}
	if len(*payloads) != 1 {
		t.Errorf("payloads = %d, want 1", len(*payloads))
	}
}

func TestAnthropicStrictToolFallbackDowngradesInlineAdditions(t *testing.T) {
	model := strictFallbackModel()
	compat := model.Compat.(*types.AnthropicMessagesCompat)
	compat.SupportsMidConvoSystemMessages = lo.ToPtr(true)
	compat.SupportsMidConvoToolChanges = lo.ToPtr(true)

	transcript := strictToolContext([]types.Tool{strictTool("lookup", "require")})
	transcript.Messages = append(transcript.Messages,
		types.SystemMessage{Role: types.RoleSystem, ToolsAdded: []types.Tool{strictTool("lookup", "prefer")}, Timestamp: 2},
		types.SystemMessage{Role: types.RoleSystem, ToolsAdded: []types.Tool{strictTool("lookup", "require")}, Timestamp: 3},
	)

	payloads, options := strictFallbackHarness(func(attempt int) *http.Response {
		if attempt == 1 {
			return strictErrorResponse(strictGrammarError, 400, "invalid_request_error")
		}
		return strictSuccessResponse(nil)
	})
	options.APIKey = "sk-ant-oat-test"

	result := runAnthropicStream(t, model, transcript, options)
	if result.StopReason != types.StopReasonStop {
		t.Fatalf("stopReason = %v, want stop", result.StopReason)
	}
	if len(*payloads) != 2 {
		t.Fatalf("payloads = %d, want 2", len(*payloads))
	}
	firstTools, _ := json.Marshal((*payloads)[0]["tools"])
	secondTools, _ := json.Marshal((*payloads)[1]["tools"])
	if string(firstTools) != string(secondTools) {
		t.Errorf("request-level tools changed:\nfirst  %s\nsecond %s", firstTools, secondTools)
	}
	additions := func(payload map[string]any) []any {
		var out []any
		messages, _ := payload["messages"].([]any)
		for _, message := range messages {
			m, _ := message.(map[string]any)
			blocks, _ := m["content"].([]any)
			for _, block := range blocks {
				b, _ := block.(map[string]any)
				if b["type"] != "tool_addition" {
					continue
				}
				tool, _ := b["tool"].(map[string]any)
				definition, _ := tool["definition"].(map[string]any)
				strict, present := definition["strict"]
				if !present {
					strict = nil
				}
				out = append(out, strict)
			}
		}
		return out
	}
	gotFirst, _ := json.Marshal(additions((*payloads)[0]))
	gotSecond, _ := json.Marshal(additions((*payloads)[1]))
	if string(gotFirst) != "[true,true]" {
		t.Errorf("first-attempt additions = %s, want [true,true]", gotFirst)
	}
	if string(gotSecond) != "[null,true]" {
		t.Errorf("second-attempt additions = %s, want [null,true]", gotSecond)
	}
}

func TestAnthropicStrictToolFallbackDoesNotLeakAcrossRequests(t *testing.T) {
	payloads, options := strictFallbackHarness(func(attempt int) *http.Response {
		if attempt == 1 {
			return strictErrorResponse(strictGrammarError, 400, "invalid_request_error")
		}
		return strictSuccessResponse(nil)
	})
	transcript := strictToolContext([]types.Tool{strictTool("lookup", "prefer")})
	runAnthropicStream(t, strictFallbackModel(), transcript, options)
	runAnthropicStream(t, strictFallbackModel(), transcript, options)
	strictFlags := make([]any, 0, len(*payloads))
	for i := range *payloads {
		strict, present := toolStrict((*payloads)[i], 0)
		if !present {
			strict = nil
		}
		strictFlags = append(strictFlags, strict)
	}
	want := []any{true, nil, true}
	got, _ := json.Marshal(strictFlags)
	expected, _ := json.Marshal(want)
	if string(got) != string(expected) {
		t.Errorf("strict flags across requests = %s, want %s", got, expected)
	}
}
