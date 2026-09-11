// DeepSeek Responses-compat endpoint smoke (pi: none — pig-only verification
// channel, see docs/handoffs/PLAN-ANTHROPIC-RESPONSES.md §0.2). Real API,
// deepseek-flash, small max_tokens. Run with:
//
//	DEEPSEEK_API_KEY=sk-... go test -tags=smoke ./ai/api/ -run TestResponsesDeepSeekSmoke -v
//
// The key is read from the environment and never written to disk.
//go:build smoke

package api

import (
	"context"
	"os"
	"testing"

	"github.com/samber/lo"

	"github.com/ZoroSpace/pig/ai/types"
)

func deepseekResponsesSmokeModel(t *testing.T) *types.Model {
	t.Helper()
	if key := os.Getenv("DEEPSEEK_API_KEY"); key == "" || key == "xxx" {
		t.Skip("DEEPSEEK_API_KEY not set")
	}
	return &types.Model{
		ID:            "deepseek-flash",
		Name:          "DeepSeek Flash",
		API:           types.ApiOpenAIResponses,
		Provider:      types.ProviderID("deepseek-responses"),
		BaseURL:       "https://api.deepseek.com",
		Reasoning:     true,
		Input:         []types.ModelInput{types.ModelInputText, types.ModelInputImage},
		Cost:          types.ModelCost{},
		ContextWindow: 128000,
		MaxTokens:     8192,
	}
}

func TestResponsesDeepSeekSmokeText(t *testing.T) {
	model := deepseekResponsesSmokeModel(t)
	options := &OpenAIResponsesOptions{}
	options.APIKey = os.Getenv("DEEPSEEK_API_KEY")
	options.MaxTokens = lo.ToPtr(int64(256))
	llmContext := &types.Context{
		SystemPrompt: "You are a helpful assistant. Reply exactly as requested.",
		Messages:     []types.Message{responsesUserMessage("Reply with exactly: responses smoke ok")},
	}
	stream := streamOpenAIResponses(context.Background(), model, llmContext, options)
	_, result := collectResponsesEvents(t, stream)
	if result.StopReason != types.StopReasonStop {
		t.Fatalf("stopReason = %v (%s), want stop", result.StopReason, result.ErrorMessage)
	}
	if len(result.Content) == 0 {
		t.Fatal("empty response content")
	}
	t.Logf("text: %v usage: %+v", result.Content[0], result.Usage)
}

func TestResponsesDeepSeekSmokeReasoning(t *testing.T) {
	model := deepseekResponsesSmokeModel(t)
	options := &OpenAIResponsesOptions{}
	options.APIKey = os.Getenv("DEEPSEEK_API_KEY")
	options.MaxTokens = lo.ToPtr(int64(512))
	options.ReasoningEffort = "low"
	llmContext := &types.Context{
		SystemPrompt: "You are a helpful assistant. Think briefly, then answer.",
		Messages:     []types.Message{responsesUserMessage("What is 2+2? Answer with the number only.")},
	}
	stream := streamOpenAIResponses(context.Background(), model, llmContext, options)
	_, result := collectResponsesEvents(t, stream)
	if result.StopReason != types.StopReasonStop {
		t.Fatalf("stopReason = %v (%s), want stop", result.StopReason, result.ErrorMessage)
	}
	hasThinking := false
	for _, block := range result.Content {
		if block.ContentKind() == types.KindThinking {
			hasThinking = true
		}
	}
	if !hasThinking {
		t.Fatalf("no thinking block in %d content entries", len(result.Content))
	}
	t.Logf("blocks: %d usage: %+v", len(result.Content), result.Usage)
}

func TestResponsesDeepSeekSmokeToolCall(t *testing.T) {
	model := deepseekResponsesSmokeModel(t)
	options := &OpenAIResponsesOptions{}
	options.APIKey = os.Getenv("DEEPSEEK_API_KEY")
	options.MaxTokens = lo.ToPtr(int64(256))
	options.ToolChoice = "auto"
	llmContext := &types.Context{
		SystemPrompt: "You are a helpful assistant. Always use the provided tool.",
		Messages:     []types.Message{responsesUserMessage("Use the double_number tool on 21.")},
		Tools: []types.Tool{
			{
				Name:        "double_number",
				Description: "Doubles a number and returns the result",
				Parameters:  mustJSONRaw(`{"type":"object","properties":{"value":{"type":"number","description":"A number to double"}},"required":["value"]}`),
			},
		},
	}
	stream := streamOpenAIResponses(context.Background(), model, llmContext, options)
	_, result := collectResponsesEvents(t, stream)
	if result.StopReason != types.StopReasonToolUse {
		t.Fatalf("stopReason = %v (%s), want toolUse", result.StopReason, result.ErrorMessage)
	}
	for _, block := range result.Content {
		if toolCall, ok := block.(types.ToolCall); ok {
			if toolCall.Name != "double_number" {
				t.Fatalf("tool name = %q", toolCall.Name)
			}
			t.Logf("toolCall: id=%s args=%v", toolCall.ID, toolCall.Arguments)
			return
		}
	}
	t.Fatal("no tool call block")
}

// TestResponsesDeepSeekSmokeToolResultImages verifies the two-turn flow with
// an image-bearing tool result (pi: openai-responses-tool-result-images
// live probe).
func TestResponsesDeepSeekSmokeToolResultImages(t *testing.T) {
	model := deepseekResponsesSmokeModel(t)
	options := &OpenAIResponsesOptions{}
	options.APIKey = os.Getenv("DEEPSEEK_API_KEY")
	options.MaxTokens = lo.ToPtr(int64(256))
	toolText := "A red circle with a diameter of 100 pixels."
	llmContext := &types.Context{
		SystemPrompt: "You are a helpful assistant that always uses the provided tool when asked.",
		Messages: []types.Message{
			responsesUserMessage("Call get_circle, then describe the tool text and the image. Mention the color."),
		},
		Tools: []types.Tool{
			{
				Name:        "get_circle",
				Description: "Returns a red circle image with a short text description.",
				Parameters:  mustJSONRaw(`{"type":"object","properties":{},"required":[]}`),
			},
		},
	}
	first := streamOpenAIResponses(context.Background(), model, llmContext, options)
	firstResult := first.Result()
	if firstResult.StopReason != types.StopReasonToolUse {
		t.Fatalf("first turn stopReason = %v (%s), want toolUse", firstResult.StopReason, firstResult.ErrorMessage)
	}
	var toolCall types.ToolCall
	found := false
	for _, block := range firstResult.Content {
		if tc, ok := block.(types.ToolCall); ok {
			toolCall = tc
			found = true
		}
	}
	if !found {
		t.Fatal("no tool call in first turn")
	}

	llmContext.Messages = append(llmContext.Messages,
		firstResult,
		types.ToolResultMessage{
			Role: types.RoleToolResult, ToolCallID: toolCall.ID, ToolName: toolCall.Name,
			Content: []types.Content{
				types.NewTextContent(toolText),
				types.NewImageContent(redCircleBase64, "image/png"),
			},
			IsError:   false,
			Timestamp: 1,
		},
		responsesUserMessage("Now describe the color in one word."),
	)

	var captured map[string]any
	capturedOnPayload := func(payload any, model *types.Model) any {
		if m, ok := payload.(map[string]any); ok {
			captured = m
		}
		return nil
	}
	options.OnPayload = capturedOnPayload
	second := streamOpenAIResponses(context.Background(), model, llmContext, options)
	_, secondResult := collectResponsesEvents(t, second)
	if secondResult.StopReason == types.StopReasonError {
		t.Fatalf("second turn errored: %s", secondResult.ErrorMessage)
	}
	if secondResult.ErrorMessage != "" {
		t.Fatalf("errorMessage = %q", secondResult.ErrorMessage)
	}

	// The function_call_output must carry the image inline (pi: the payload
	// assertions of verifyToolResultImagesStayInFunctionCallOutput). The
	// captured payload is the live params map: output parts are
	// []map[string]any, unlike the JSON-decoded []any.
	input, _ := captured["input"].([]any)
	foundImageOutput := false
	for _, item := range input {
		m, ok := item.(map[string]any)
		if !ok || m["type"] != "function_call_output" {
			continue
		}
		var parts []map[string]any
		switch output := m["output"].(type) {
		case []map[string]any:
			parts = output
		case []any:
			for _, p := range output {
				if pm, ok := p.(map[string]any); ok {
					parts = append(parts, pm)
				}
			}
		}
		for _, part := range parts {
			if part["type"] == "input_image" {
				foundImageOutput = true
			}
		}
	}
	if !foundImageOutput {
		t.Fatal("function_call_output did not carry the tool-result image")
	}
	t.Logf("second turn: %d blocks", len(secondResult.Content))
}
