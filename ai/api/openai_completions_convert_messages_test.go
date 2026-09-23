// pi: test/openai-completions-tool-result-images.test.ts — the
// convertMessages half (S9 ports the 1b6ddca87 empty-text-part regression;
// the tool-result image batching describes are an existing first-port gap
// declared in the S9 handoff). convertMessages is exercised directly like
// pi; the compat resolution goes through the adapter's getCompat.
package api

import (
	"testing"

	"github.com/ZoroSpace/pig/ai"
	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/ai/utils"
)

// TestOmitsEmptyTextPartsFromMultimodalUserMessages ports the regression for
// pi #9797: image-only user prompts must not carry empty text parts
// (OpenAI-compatible providers reject them).
func TestOmitsEmptyTextPartsFromMultimodalUserMessages(t *testing.T) {
	base, ok := ai.MODELS[ai.ProviderOpenAI]["gpt-4o-mini"]
	if !ok {
		t.Fatal("gpt-4o-mini not in catalog")
	}
	model := base
	model.API = ai.ApiOpenAICompletions
	model.Input = []ai.ModelInput{ai.ModelInputText, ai.ModelInputImage}

	llmContext := utils.NormalizeContext(&types.Context{
		Messages: []types.Message{
			types.UserMessage{
				Role: types.RoleUser,
				Content: types.UserContent{Parts: []types.Content{
					types.NewTextContent(""),
					types.ImageContent{Data: "ZmFrZQ==", MimeType: "image/png"},
				}},
				Timestamp: 1,
			},
		},
	})

	params := convertMessages(&model, llmContext, getCompat(&model), &ConvertCompletionsMessagesOptions{})
	if len(params) != 1 {
		t.Fatalf("params = %d messages, want 1", len(params))
	}
	msg := params[0]
	if msg["role"] != "user" {
		t.Fatalf("role = %v, want user", msg["role"])
	}
	content, ok := msg["content"].([]any)
	if !ok {
		t.Fatalf("content = %#v, want a part array", msg["content"])
	}
	if len(content) != 1 {
		t.Fatalf("content parts = %#v, want only the image part", content)
	}
	part, ok := content[0].(map[string]any)
	if !ok || part["type"] != "image_url" {
		t.Fatalf("first part = %#v, want image_url", content[0])
	}
	imageURL, ok := part["image_url"].(map[string]any)
	if !ok || imageURL["url"] != "data:image/png;base64,ZmFrZQ==" {
		t.Fatalf("image_url = %#v", part["image_url"])
	}
}
