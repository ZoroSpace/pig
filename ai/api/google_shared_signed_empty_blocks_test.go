// pi: test/google-shared-signed-empty-blocks.test.ts.
package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ZoroSpace/pig/ai/types"
)

// Gemini can attach thoughtSignature to a response part whose visible text is
// empty (e.g. a thought burst preceding a function call) and requires the
// signature echoed back on the next request. Dropping such blocks while
// rebuilding history silently breaks the reasoning chain: the model then
// intermittently ends a mid-task turn with a thought-only STOP and no tool
// call. These tests pin the rule: an empty text/thinking block is skipped only
// when it is UNSIGNED.
func googleSignedModel(id string) *types.Model {
	return &types.Model{
		ID:            id,
		Name:          id,
		API:           "google-generative-ai",
		Provider:      "google",
		BaseURL:       "https://example.com",
		Reasoning:     true,
		Input:         []types.ModelInput{types.ModelInputText},
		Cost:          types.ModelCost{},
		ContextWindow: 128000,
		MaxTokens:     8192,
	}
}

const googleValidSignature = "AAAAAAAAAAAAAAAAAAAAAA=="

func googleSignedContext(model *types.Model, content []types.Content) *types.Context {
	now := time.Now().UnixMilli()
	return &types.Context{
		Messages: []types.Message{
			types.UserMessage{Role: types.RoleUser, Content: types.UserContent{String: "Hi"}, Timestamp: now},
			types.AssistantMessage{
				Role:       types.RoleAssistant,
				Content:    content,
				API:        types.Api(model.API),
				Provider:   model.Provider,
				Model:      model.ID,
				StopReason: types.StopReasonToolUse,
				Timestamp:  now,
			},
		},
	}
}

// signedParts collects the model-turn parts carrying the valid signature.
func googleSignedParts(t *testing.T, model *types.Model, content []types.Content) []googlePart {
	t.Helper()
	contents := ConvertGoogleMessages(model, googleSignedContext(model, content))
	var modelTurn *googleContent
	for i := range contents {
		if contents[i].Role == "model" {
			modelTurn = &contents[i]
		}
	}
	if modelTurn == nil {
		t.Fatal("no model turn in contents")
	}
	var signed []googlePart
	for _, part := range modelTurn.Parts {
		if part.ThoughtSignature == googleValidSignature {
			signed = append(signed, part)
		}
	}
	return signed
}

func TestGoogleSignedEmptyThinkingBlockKept(t *testing.T) {
	model := googleSignedModel("gemini-3-pro-preview")
	signed := googleSignedParts(t, model, []types.Content{
		types.ThinkingContent{Kind: types.KindThinking, ThinkingSignature: googleValidSignature},
		types.NewToolCall("call_1", "bash", map[string]any{"command": "ls"}),
	})
	if len(signed) != 1 {
		t.Fatalf("signed parts = %d, want 1", len(signed))
	}
	if !signed[0].Thought {
		t.Error("signed part should be a thinking part")
	}
}

func TestGoogleSignedEmptyTextBlockKept(t *testing.T) {
	model := googleSignedModel("gemini-3-pro-preview")
	signed := googleSignedParts(t, model, []types.Content{
		types.TextContent{Kind: types.KindText, TextSignature: googleValidSignature},
		types.NewToolCall("call_1", "bash", map[string]any{"command": "ls"}),
	})
	if len(signed) != 1 {
		t.Fatalf("signed parts = %d, want 1", len(signed))
	}
}

func TestGoogleUnsignedEmptyBlocksDropped(t *testing.T) {
	model := googleSignedModel("gemini-3-pro-preview")
	contents := ConvertGoogleMessages(model, googleSignedContext(model, []types.Content{
		types.ThinkingContent{Kind: types.KindThinking},
		types.NewTextContent("   "),
		types.NewToolCall("call_1", "bash", map[string]any{"command": "ls"}),
	}))
	var modelTurn *googleContent
	for i := range contents {
		if contents[i].Role == "model" {
			modelTurn = &contents[i]
		}
	}
	if len(modelTurn.Parts) != 1 {
		t.Fatalf("model parts = %d, want 1", len(modelTurn.Parts))
	}
	if modelTurn.Parts[0].FunctionCall == nil {
		t.Error("the surviving part should be the function call")
	}
}

func TestGoogleSignedEmptyBlocksFromOtherModelDropped(t *testing.T) {
	model := googleSignedModel("gemini-3-pro-preview")
	contents := ConvertGoogleMessages(googleSignedModel("other-model"), googleSignedContext(model, []types.Content{
		types.ThinkingContent{Kind: types.KindThinking, ThinkingSignature: googleValidSignature},
		types.TextContent{Kind: types.KindText, TextSignature: googleValidSignature},
		types.NewToolCall("call_1", "bash", map[string]any{"command": "ls"}),
	}))
	var modelTurn *googleContent
	for i := range contents {
		if contents[i].Role == "model" {
			modelTurn = &contents[i]
		}
	}
	if len(modelTurn.Parts) != 1 {
		t.Fatalf("model parts = %d, want 1", len(modelTurn.Parts))
	}
	if modelTurn.Parts[0].FunctionCall == nil {
		t.Error("the surviving part should be the function call")
	}
	serialized, err := json.Marshal(contents)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(serialized), googleValidSignature) {
		t.Error("the foreign signature should not survive")
	}
}
