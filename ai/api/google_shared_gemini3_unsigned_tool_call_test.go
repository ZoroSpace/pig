// pi: test/google-shared-gemini3-unsigned-tool-call.test.ts.
//
// The vertex-typed model cases run against the shared converter only (pi's
// makeGemini3Model is generic over both Google API types); the vertex adapter
// itself is not needed.
package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ZoroSpace/pig/ai/types"
)

func googleGemini3Model(api, provider, id string) *types.Model {
	if id == "" {
		id = "gemini-3-pro-preview"
	}
	return &types.Model{
		ID:            id,
		Name:          "Gemini 3 Pro Preview",
		API:           types.Api(api),
		Provider:      types.ProviderID(provider),
		BaseURL:       "https://example.com",
		Reasoning:     true,
		Input:         []types.ModelInput{types.ModelInputText},
		Cost:          types.ModelCost{},
		ContextWindow: 128000,
		MaxTokens:     8192,
	}
}

func googleGemini3Context(model *types.Model, thoughtSignature string) *types.Context {
	now := time.Now().UnixMilli()
	assistantContent := []types.Content{
		types.NewToolCall("call_1", "bash", map[string]any{"command": "echo hi"}),
		types.NewToolCall("call_2", "bash", map[string]any{"command": "ls -la"}),
	}
	if thoughtSignature != "" {
		if call, ok := assistantContent[0].(types.ToolCall); ok {
			call.ThoughtSignature = thoughtSignature
			assistantContent[0] = call
		}
	}
	return &types.Context{
		Messages: []types.Message{
			types.UserMessage{Role: types.RoleUser, Content: types.UserContent{String: "Hi"}, Timestamp: now},
			types.AssistantMessage{
				Role:       types.RoleAssistant,
				Content:    assistantContent,
				API:        model.API,
				Provider:   model.Provider,
				Model:      model.ID,
				StopReason: types.StopReasonToolUse,
				Timestamp:  now,
			},
			types.ToolResultMessage{
				Role:       types.RoleToolResult,
				ToolCallID: "call_1",
				ToolName:   "bash",
				Content:    []types.Content{types.NewTextContent("hi")},
				Timestamp:  now,
			},
			types.ToolResultMessage{
				Role:       types.RoleToolResult,
				ToolCallID: "call_2",
				ToolName:   "bash",
				Content:    []types.Content{types.NewTextContent("files")},
				Timestamp:  now,
			},
		},
	}
}

// googleFlattenParts returns every part across all contents.
func googleFlattenParts(contents []googleContent) []googlePart {
	var parts []googlePart
	for _, content := range contents {
		parts = append(parts, content.Parts...)
	}
	return parts
}

func TestGoogleGemini3PreservesToolCallIDs(t *testing.T) {
	models := []*types.Model{
		googleGemini3Model("google-generative-ai", "google", ""),
		googleGemini3Model("google-generative-ai", "google", "gemini-3.6-flash"),
		googleGemini3Model("google-vertex", "google-vertex", ""),
	}
	for _, model := range models {
		contents := ConvertGoogleMessages(model, googleGemini3Context(model, ""))
		var callIDs, responseIDs []string
		for _, part := range googleFlattenParts(contents) {
			if part.FunctionCall != nil && part.FunctionCall.ID != "" {
				callIDs = append(callIDs, part.FunctionCall.ID)
			}
			if part.FunctionResponse != nil && part.FunctionResponse.ID != "" {
				responseIDs = append(responseIDs, part.FunctionResponse.ID)
			}
		}
		if len(callIDs) != 2 || callIDs[0] != "call_1" || callIDs[1] != "call_2" {
			t.Errorf("%s/%s call ids = %v, want [call_1 call_2]", model.Provider, model.ID, callIDs)
		}
		if len(responseIDs) != 2 || responseIDs[0] != "call_1" || responseIDs[1] != "call_2" {
			t.Errorf("%s/%s response ids = %v, want [call_1 call_2]", model.Provider, model.ID, responseIDs)
		}
	}
}

func TestGoogleGemini3NoSkipValidatorForUnsignedGenAIToolCalls(t *testing.T) {
	model := googleGemini3Model("google-generative-ai", "google", "")
	// The history carries a different model id, so signatures would be
	// unusable — none are present in the first place.
	contents := ConvertGoogleMessages(model, googleGemini3Context(googleGemini3Model("google-generative-ai", "google", "other-model"), ""))

	var modelTurn *googleContent
	for i := range contents {
		if contents[i].Role == "model" {
			modelTurn = &contents[i]
		}
	}
	if modelTurn == nil {
		t.Fatal("no model turn")
	}
	var functionCallParts []googlePart
	for _, part := range modelTurn.Parts {
		if part.FunctionCall != nil {
			functionCallParts = append(functionCallParts, part)
		}
	}
	if len(functionCallParts) != 2 {
		t.Fatalf("function call parts = %d, want 2", len(functionCallParts))
	}
	if functionCallParts[0].ThoughtSignature != "" || functionCallParts[1].ThoughtSignature != "" {
		t.Error("unsigned tool calls should not grow signatures")
	}
	serialized, err := json.Marshal(contents)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(serialized), "skip_thought_signature_validator") {
		t.Error("skip_thought_signature_validator should not appear")
	}
	for _, part := range modelTurn.Parts {
		if part.Text != nil && strings.Contains(*part.Text, "Historical context") {
			t.Error("no historical-context text should be synthesized")
		}
	}
}

func TestGoogleGemini3NoSkipValidatorForUnsignedVertexToolCalls(t *testing.T) {
	model := googleGemini3Model("google-vertex", "google-vertex", "")
	contents := ConvertGoogleMessages(model, googleGemini3Context(model, ""))
	var modelTurn *googleContent
	for i := range contents {
		if contents[i].Role == "model" {
			modelTurn = &contents[i]
		}
	}
	var functionCallParts []googlePart
	for _, part := range modelTurn.Parts {
		if part.FunctionCall != nil {
			functionCallParts = append(functionCallParts, part)
		}
	}
	if len(functionCallParts) != 2 {
		t.Fatalf("function call parts = %d, want 2", len(functionCallParts))
	}
	if functionCallParts[0].ThoughtSignature != "" || functionCallParts[1].ThoughtSignature != "" {
		t.Error("unsigned tool calls should not grow signatures")
	}
	serialized, err := json.Marshal(contents)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(serialized), "skip_thought_signature_validator") {
		t.Error("skip_thought_signature_validator should not appear")
	}
}

func TestGoogleGemini3PreservesValidThoughtSignatureSameModel(t *testing.T) {
	model := googleGemini3Model("google-generative-ai", "google", "")
	validSig := "AAAAAAAAAAAAAAAAAAAAAA=="
	contents := ConvertGoogleMessages(model, googleGemini3Context(model, validSig))
	var modelTurn *googleContent
	for i := range contents {
		if contents[i].Role == "model" {
			modelTurn = &contents[i]
		}
	}
	var functionCallParts []googlePart
	for _, part := range modelTurn.Parts {
		if part.FunctionCall != nil {
			functionCallParts = append(functionCallParts, part)
		}
	}
	if len(functionCallParts) != 2 {
		t.Fatalf("function call parts = %d, want 2", len(functionCallParts))
	}
	if functionCallParts[0].ThoughtSignature != validSig {
		t.Errorf("first signature = %q, want %q", functionCallParts[0].ThoughtSignature, validSig)
	}
	if functionCallParts[1].ThoughtSignature != "" {
		t.Errorf("second signature = %q, want empty", functionCallParts[1].ThoughtSignature)
	}
}

func TestGoogleGemini3NoSignatureForNonGemini3Models(t *testing.T) {
	model := googleGemini3Model("google-generative-ai", "google", "gemini-2.5-flash")
	contents := ConvertGoogleMessages(model, googleGemini3Context(googleGemini3Model("google-generative-ai", "google", "other-model"), ""))
	var modelTurn *googleContent
	for i := range contents {
		if contents[i].Role == "model" {
			modelTurn = &contents[i]
		}
	}
	var functionCallParts, functionResponseParts []googlePart
	for _, part := range modelTurn.Parts {
		if part.FunctionCall != nil {
			functionCallParts = append(functionCallParts, part)
		}
	}
	for _, part := range googleFlattenParts(contents) {
		if part.FunctionResponse != nil {
			functionResponseParts = append(functionResponseParts, part)
		}
	}
	if len(functionCallParts) != 2 {
		t.Fatalf("function call parts = %d, want 2", len(functionCallParts))
	}
	for _, part := range functionCallParts {
		if part.FunctionCall.ID != "" {
			t.Errorf("non-gemini-3 call should have no id: %q", part.FunctionCall.ID)
		}
		if part.ThoughtSignature != "" {
			t.Errorf("non-gemini-3 call should have no signature: %q", part.ThoughtSignature)
		}
	}
	if len(functionResponseParts) != 2 {
		t.Fatalf("function response parts = %d, want 2", len(functionResponseParts))
	}
	for _, part := range functionResponseParts {
		if part.FunctionResponse.ID != "" {
			t.Errorf("non-gemini-3 response should have no id: %q", part.FunctionResponse.ID)
		}
	}
}

func TestGoogleRequiresToolCallId(t *testing.T) {
	cases := []struct {
		expected bool
		modelID  string
	}{
		{false, "gemini-2.5-flash"},
		{true, "gemini-3.6-flash"},
		{true, "claude-sonnet-4-5"},
		{true, "gpt-oss-120b"},
	}
	for _, c := range cases {
		if got := RequiresToolCallId(c.modelID); got != c.expected {
			t.Errorf("RequiresToolCallId(%q) = %v, want %v", c.modelID, got, c.expected)
		}
	}
}
