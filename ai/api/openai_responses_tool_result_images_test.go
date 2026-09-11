// pi: test/openai-responses-tool-result-images.test.ts and
// test/openai-responses-reasoning-replay-e2e.test.ts.
//
// The pi tool-result-images test is a live-API probe (skipIf no provider
// key) asserting the function_call_output payload shape; the payload-shape
// assertions run here as conversion unit tests (the shape is
// convertResponsesMessages behavior, not provider behavior), while the live
// round-trip is covered by the DeepSeek smoke test
// (TestResponsesDeepSeekSmokeToolResultImages). The reasoning-replay e2e
// keeps pi's skip gates (needs OPENAI/ANTHROPIC keys).
package api

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/ZoroSpace/pig/ai/types"
)

// responsesToolResultImagesContext builds the two-turn context with the
// red-circle tool result (pi: verifyToolResultImagesStayInFunctionCallOutput;
// the red-circle.png fixture is inlined as a tiny valid PNG).
const redCircleBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="

func TestResponsesToolResultImagesStayInFunctionCallOutput(t *testing.T) {
	model := responsesTestModel(t, "openai", "gpt-5-mini") // input includes image
	toolText := "A red circle with a diameter of 100 pixels."

	toolResult := types.ToolResultMessage{
		Role:       types.RoleToolResult,
		ToolCallID: "call_test|fc_test",
		ToolName:   "get_circle_with_description",
		Content: []types.Content{
			types.NewTextContent(toolText),
			types.NewImageContent(redCircleBase64, "image/png"),
		},
		IsError:   false,
		Timestamp: 1,
	}
	assistant := types.AssistantMessage{
		Role: types.RoleAssistant,
		Content: []types.Content{types.ToolCall{
			Kind: types.KindToolCall, ID: "call_test|fc_test", Name: "get_circle_with_description",
			Arguments: map[string]any{},
		}},
		API: model.API, Provider: model.Provider, Model: model.ID,
		StopReason: types.StopReasonToolUse, Timestamp: 0,
	}
	llmContext := &types.Context{
		SystemPrompt: "You are a helpful assistant that always uses the provided tool when asked.",
		Messages: []types.Message{
			responsesUserMessage("Call get_circle_with_description, then describe both the tool text and the image."),
			assistant,
			toolResult,
		},
	}

	input := ConvertResponsesMessages(model, llmContext, openAIResponsesToolCallProviders, nil)
	functionCallOutputIndex := -1
	for i, item := range input {
		if m, ok := item.(map[string]any); ok && m["type"] == "function_call_output" {
			functionCallOutputIndex = i
			break
		}
	}
	if functionCallOutputIndex < 0 {
		t.Fatal("expected function_call_output item")
	}
	output := input[functionCallOutputIndex].(map[string]any)["output"]
	parts, ok := output.([]map[string]any)
	if !ok {
		t.Fatalf("function_call_output.output = %T (%v), want content array", output, output)
	}

	var textItem map[string]any
	var imageItem map[string]any
	for _, part := range parts {
		if part["type"] == "input_text" {
			textItem = part
		}
		if part["type"] == "input_image" {
			imageItem = part
		}
	}
	if textItem == nil || imageItem == nil {
		t.Fatalf("expected input_text and input_image in function_call_output, got %v", parts)
	}
	if !strings.Contains(textItem["text"].(string), toolText) {
		t.Fatalf("text = %v", textItem["text"])
	}
	if !strings.HasPrefix(imageItem["image_url"].(string), "data:image/png;base64,") {
		t.Fatalf("image_url = %v", imageItem["image_url"])
	}

	// No user messages may follow the function_call_output (pi: the tool
	// result carries the images so the follow-up turn stays stateless).
	for _, item := range input[functionCallOutputIndex+1:] {
		if m, ok := item.(map[string]any); ok && m["role"] == "user" {
			t.Fatalf("unexpected user message after function_call_output: %v", item)
		}
	}
}

// TestOpenAIResponsesReasoningReplayE2E ports the three live scenarios from
// openai-responses-reasoning-replay-e2e.test.ts. pi gates on
// OPENAI_API_KEY/ANTHROPIC_API_KEY; pig keeps the same gates (set
// OPENAI_API_KEY and optionally ANTHROPIC_API_KEY to run against the real
// APIs). The DeepSeek responses endpoint cannot produce encrypted_content,
// so the encrypted replay path stays mock-only (PLAN §0.2).
func TestOpenAIResponsesReasoningReplayE2E(t *testing.T) {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" || apiKey == "xxx" {
		t.Skip("OPENAI_API_KEY not set")
	}
	model := responsesTestModel(t, "openai", "gpt-5-mini")

	testTool := types.Tool{
		Name:        "double_number",
		Description: "Doubles a number and returns the result",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"value":{"type":"number","description":"A number to double"}},"required":["value"]}`),
	}
	complete := func(t *testing.T, model *types.Model, llmContext *types.Context, effort string) *types.AssistantMessage {
		t.Helper()
		opts := &OpenAIResponsesOptions{}
		opts.APIKey = apiKey
		opts.ReasoningEffort = types.ModelThinkingLevel(effort)
		stream := streamOpenAIResponses(context.Background(), model, llmContext, opts)
		_, result := collectResponsesEvents(t, stream)
		return &result
	}

	t.Run("skips reasoning-only history after an aborted turn", func(t *testing.T) {
		userMessage := responsesUserMessage("Use the double_number tool to double 21.")
		assistantResponse := complete(t, model, &types.Context{
			SystemPrompt: "You are a helpful assistant. Use the tool.",
			Messages:     []types.Message{userMessage},
			Tools:        []types.Tool{testTool},
		}, "high")

		var thinkingBlock *types.ThinkingContent
		for _, block := range assistantResponse.Content {
			if tb, ok := block.(types.ThinkingContent); ok && tb.ThinkingSignature != "" {
				thinkingBlock = &tb
				break
			}
		}
		if thinkingBlock == nil {
			t.Skip("Missing thinking signature from OpenAI Responses")
		}

		corruptedAssistant := *assistantResponse
		corruptedAssistant.Content = []types.Content{*thinkingBlock}
		corruptedAssistant.StopReason = types.StopReasonAborted

		followUp := responsesUserMessage("Say hello to confirm you can continue.")
		response := complete(t, model, &types.Context{
			SystemPrompt: "You are a helpful assistant.",
			Messages:     []types.Message{userMessage, &corruptedAssistant, followUp},
			Tools:        []types.Tool{testTool},
		}, "high")

		// The key assertion: no 400 error from orphaned reasoning item
		if response.StopReason == types.StopReasonError {
			t.Fatalf("response errored: %s", response.ErrorMessage)
		}
		if response.ErrorMessage != "" {
			t.Fatalf("errorMessage = %q", response.ErrorMessage)
		}
		if len(response.Content) == 0 {
			t.Fatal("expected non-empty response")
		}
	})

	t.Run("handles same-provider different-model handoff with tool calls", func(t *testing.T) {
		modelB := responsesTestModel(t, "openai", "gpt-5.5")
		userMessage := responsesUserMessage("Use the double_number tool to double 21.")
		assistantResponse := complete(t, model, &types.Context{
			SystemPrompt: "You are a helpful assistant. Always use the tool when asked.",
			Messages:     []types.Message{userMessage},
			Tools:        []types.Tool{testTool},
		}, "high")

		var toolCallBlock *types.ToolCall
		for _, block := range assistantResponse.Content {
			if tc, ok := block.(types.ToolCall); ok {
				toolCallBlock = &tc
				break
			}
		}
		if toolCallBlock == nil {
			t.Skip("Missing tool call from OpenAI Responses - model did not use the tool")
		}

		toolResult := types.ToolResultMessage{
			Role: types.RoleToolResult, ToolCallID: toolCallBlock.ID, ToolName: toolCallBlock.Name,
			Content: []types.Content{types.NewTextContent("42")}, IsError: false, Timestamp: 1,
		}
		followUp := responsesUserMessage("What was the result? Answer with just the number.")

		response := complete(t, modelB, &types.Context{
			SystemPrompt: "You are a helpful assistant. Answer concisely.",
			Messages:     []types.Message{userMessage, assistantResponse, &toolResult, followUp},
			Tools:        []types.Tool{testTool},
		}, "high")

		if response.StopReason == types.StopReasonError {
			t.Fatalf("response errored: %s", response.ErrorMessage)
		}
		if response.ErrorMessage != "" {
			t.Fatalf("errorMessage = %q", response.ErrorMessage)
		}
		if len(response.Content) == 0 {
			t.Fatal("expected non-empty response")
		}
		// Verify the model understood the context
		var text strings.Builder
		for _, block := range response.Content {
			if tb, ok := block.(types.TextContent); ok {
				text.WriteString(tb.Text)
			}
		}
		if !strings.Contains(text.String(), "42") {
			t.Fatalf("response text = %q, want it to mention 42", text.String())
		}
	})
}
