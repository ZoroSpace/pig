// pi: test/google-shared-image-tool-result-routing.test.ts.
package api

import (
	"testing"
	"time"

	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/ai/utils"
)

func googleRoutingModel(id string) *types.Model {
	return &types.Model{
		ID:            id,
		Name:          id,
		API:           "google-generative-ai",
		Provider:      "google",
		BaseURL:       "https://example.com",
		Reasoning:     true,
		Input:         []types.ModelInput{types.ModelInputText, types.ModelInputImage},
		Cost:          types.ModelCost{},
		ContextWindow: 128000,
		MaxTokens:     8192,
	}
}

func googleRoutingContext(model *types.Model) *types.TranscriptContext {
	now := time.Now().UnixMilli()
	return utils.NormalizeContext(&types.Context{
		Messages: []types.Message{
			types.UserMessage{Role: types.RoleUser, Content: types.UserContent{String: "read the files"}, Timestamp: now},
			types.AssistantMessage{
				Role: types.RoleAssistant,
				Content: []types.Content{
					types.NewToolCall("call_a", "read", map[string]any{"path": "a.txt"}),
					types.NewToolCall("call_img", "read", map[string]any{"path": "image.png"}),
					types.NewToolCall("call_b", "read", map[string]any{"path": "b.txt"}),
				},
				API:        types.Api(model.API),
				Provider:   model.Provider,
				Model:      model.ID,
				StopReason: types.StopReasonToolUse,
				Timestamp:  now,
			},
			types.ToolResultMessage{
				Role:       types.RoleToolResult,
				ToolCallID: "call_a",
				ToolName:   "read",
				Content:    []types.Content{types.NewTextContent("alpha text")},
				Timestamp:  now,
			},
			types.ToolResultMessage{
				Role:       types.RoleToolResult,
				ToolCallID: "call_img",
				ToolName:   "read",
				Content:    []types.Content{types.NewImageContent("abc", "image/png")},
				Timestamp:  now,
			},
			types.ToolResultMessage{
				Role:       types.RoleToolResult,
				ToolCallID: "call_b",
				ToolName:   "read",
				Content:    []types.Content{types.NewTextContent("beta text")},
				Timestamp:  now,
			},
		},
	})
}

func TestGoogleImageToolResultRoutingSeparateTurnForGemini2x(t *testing.T) {
	model := googleRoutingModel("gemini-2.5-flash")
	contents := ConvertGoogleMessages(model, googleRoutingContext(model))

	if len(contents) != 5 {
		t.Fatalf("contents length = %d, want 5", len(contents))
	}
	for _, part := range contents[2].Parts {
		if part.FunctionResponse == nil {
			t.Error("contents[2] should be all function responses")
		}
	}
	if contents[3].Parts[0].Text == nil || *contents[3].Parts[0].Text != "Tool result image:" {
		t.Errorf("contents[3].parts[0] = %+v, want the image marker text", contents[3].Parts[0])
	}
	if contents[3].Parts[1].InlineData == nil {
		t.Error("contents[3].parts[1] should carry the inline image")
	}
	if contents[4].Parts[0].FunctionResponse == nil {
		t.Error("contents[4].parts[0] should be a function response")
	}
}

func TestGoogleImageToolResultRoutingNestedForGemini3(t *testing.T) {
	model := googleRoutingModel("gemini-3-pro-preview")
	contents := ConvertGoogleMessages(model, googleRoutingContext(model))

	if len(contents) != 3 {
		t.Fatalf("contents length = %d, want 3", len(contents))
	}
	toolResultTurn := contents[2]
	if len(toolResultTurn.Parts) != 3 {
		t.Fatalf("tool result turn parts = %d, want 3", len(toolResultTurn.Parts))
	}
	imageResponse := toolResultTurn.Parts[1].FunctionResponse
	if imageResponse == nil {
		t.Fatal("parts[1] should be a function response")
	}
	if len(imageResponse.Parts) != 1 {
		t.Fatalf("functionResponse.parts = %d, want 1", len(imageResponse.Parts))
	}
	if imageResponse.Parts[0].InlineData == nil {
		t.Error("nested part should carry the inline image")
	}
}

// TestGoogleWhitespaceOnlyToolResultTextIsNoText ports pi #8720 for Google:
// whitespace-only tool output counts as no text, so a result with an image
// gets the image placeholder and one without images becomes an empty output.
func TestGoogleWhitespaceOnlyToolResultTextIsNoText(t *testing.T) {
	model := googleRoutingModel("gemini-3-pro-preview")
	llmContext := googleRoutingContext(model)
	for i, message := range llmContext.Messages {
		toolResult, ok := message.(types.ToolResultMessage)
		if !ok {
			continue
		}
		switch toolResult.ToolCallID {
		case "call_a":
			toolResult.Content = []types.Content{types.NewTextContent("\r\n")}
		case "call_img":
			toolResult.Content = append([]types.Content{types.NewTextContent(" \n")}, toolResult.Content...)
		}
		llmContext.Messages[i] = toolResult
	}

	contents := ConvertGoogleMessages(model, llmContext)
	responses := make([]any, 0, 3)
	for _, part := range contents[2].Parts {
		if part.FunctionResponse != nil {
			responses = append(responses, part.FunctionResponse.Response["output"])
		}
	}
	if len(responses) != 3 {
		t.Fatalf("responses = %#v, want 3", responses)
	}
	if responses[0] != "" {
		t.Errorf("whitespace-only text output = %v, want empty string", responses[0])
	}
	if responses[1] != "(see attached image)" {
		t.Errorf("whitespace text with image output = %v, want (see attached image)", responses[1])
	}
	if responses[2] != "beta text" {
		t.Errorf("text output = %v, want beta text", responses[2])
	}
}
