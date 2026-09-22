// Tests for the pig-specific tool-result image elision at the LLM context
// boundary (agent/harness/image_elision.go). Locks the policy: images in
// tool results are elided once the model has produced an assistant response
// after them; fresh tool results, user-message images, and the input slice
// are untouched.
package harness

import (
	"strings"
	"testing"

	"github.com/ZoroSpace/pig/ai/types"
)

const elisionTestImageBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8DwHwAFBQIAX8jx0gAAAABJRU5ErkJggg=="

func elisionTestImage() types.ImageContent {
	return types.ImageContent{Kind: types.KindImage, Data: elisionTestImageBase64, MimeType: "image/png"}
}

func elisionToolResult(id string, image types.ImageContent) types.ToolResultMessage {
	return types.ToolResultMessage{
		Role:       types.RoleToolResult,
		ToolCallID: id,
		ToolName:   "read",
		Content: []types.Content{
			types.NewTextContent("Read image file [image/png]"),
			image,
		},
	}
}

// TestConvertToLlmElidesImagesTheModelAlreadyRespondedTo locks the core
// policy end-to-end through ConvertToLlm.
func TestConvertToLlmElidesImagesTheModelAlreadyRespondedTo(t *testing.T) {
	messages := []types.Message{
		types.UserMessage{Role: types.RoleUser, Content: types.UserContent{Parts: []types.Content{types.NewTextContent("look at this")}}},
		types.AssistantMessage{Role: types.RoleAssistant, Content: []types.Content{}},
		elisionToolResult("t1", elisionTestImage()), // responded to → elided
		types.AssistantMessage{Role: types.RoleAssistant, Content: []types.Content{}},
		elisionToolResult("t2", elisionTestImage()), // fresh → kept
		types.UserMessage{Role: types.RoleUser, Content: types.UserContent{Parts: []types.Content{types.NewTextContent("thanks")}}},
	}

	converted := ConvertToLlm(messages)

	toolResult, ok := converted[2].(types.ToolResultMessage)
	if !ok {
		t.Fatalf("expected ToolResultMessage, got %T", converted[2])
	}
	if len(toolResult.Content) != 2 {
		t.Fatalf("expected text + note preserved, got %v", toolResult.Content)
	}
	if text, ok := toolResult.Content[0].(types.TextContent); !ok || text.Text != "Read image file [image/png]" {
		t.Fatalf("expected surrounding text preserved, got %v", toolResult.Content[0])
	}
	note, ok := toolResult.Content[1].(types.TextContent)
	if !ok || !strings.Contains(note.Text, "[Image elided from context") {
		t.Fatalf("expected elision note, got %v", toolResult.Content[1])
	}

	freshResult, ok := converted[4].(types.ToolResultMessage)
	if !ok {
		t.Fatalf("expected ToolResultMessage, got %T", converted[4])
	}
	if freshResult.Content[1].ContentKind() != types.KindImage {
		t.Fatalf("expected fresh image kept, got %v", freshResult.Content[1])
	}
}

// TestConvertToLlmKeepsImagesWhenNoAssistantRespondedYet: without an
// assistant message after a tool result, its image must be sent.
func TestConvertToLlmKeepsImagesWhenNoAssistantRespondedYet(t *testing.T) {
	messages := []types.Message{
		types.UserMessage{Role: types.RoleUser, Content: types.UserContent{Parts: []types.Content{types.NewTextContent("hi")}}},
		elisionToolResult("t1", elisionTestImage()),
	}

	converted := ConvertToLlm(messages)
	toolResult, ok := converted[1].(types.ToolResultMessage)
	if !ok {
		t.Fatalf("expected ToolResultMessage, got %T", converted[1])
	}
	if toolResult.Content[1].ContentKind() != types.KindImage {
		t.Fatalf("expected image kept before any assistant response, got %v", toolResult.Content[1])
	}
}

// TestConvertToLlmNeverElidesUserMessageImages: clipboard pastes are not
// re-readable from disk and must ride along.
func TestConvertToLlmNeverElidesUserMessageImages(t *testing.T) {
	messages := []types.Message{
		types.UserMessage{Role: types.RoleUser, Content: types.UserContent{Parts: []types.Content{
			types.NewTextContent("pasted"),
			elisionTestImage(),
		}}},
		types.AssistantMessage{Role: types.RoleAssistant, Content: []types.Content{}},
	}

	converted := ConvertToLlm(messages)
	user, ok := converted[0].(types.UserMessage)
	if !ok {
		t.Fatalf("expected UserMessage, got %T", converted[0])
	}
	if user.Content.Parts[1].ContentKind() != types.KindImage {
		t.Fatalf("expected user image untouched, got %v", user.Content.Parts[1])
	}
}

// TestConvertToLlmElisionDoesNotMutateInput: session entries share these
// message values; the converted slice must be copy-on-write.
func TestConvertToLlmElisionDoesNotMutateInput(t *testing.T) {
	image := elisionTestImage()
	messages := []types.Message{
		elisionToolResult("t1", image),
		types.AssistantMessage{Role: types.RoleAssistant, Content: []types.Content{}},
	}

	converted := ConvertToLlm(messages)
	original := messages[0].(types.ToolResultMessage)
	if original.Content[1].ContentKind() != types.KindImage || original.Content[1].(types.ImageContent).Data != elisionTestImageBase64 {
		t.Fatal("input message was mutated")
	}
	if image.Data != elisionTestImageBase64 {
		t.Fatal("shared image block was mutated")
	}
	if converted[0].(types.ToolResultMessage).Content[1].ContentKind() != types.KindText {
		t.Fatal("expected elided copy in output")
	}
}

// TestElideRespondedToolResultImagesDedupesConsecutiveNotes matches pi's
// blockImages placeholder dedupe style.
func TestElideRespondedToolResultImagesDedupesConsecutiveNotes(t *testing.T) {
	messages := []types.Message{
		types.ToolResultMessage{
			Role:    types.RoleToolResult,
			Content: []types.Content{elisionTestImage(), elisionTestImage(), types.NewTextContent("tail")},
		},
		types.AssistantMessage{Role: types.RoleAssistant, Content: []types.Content{}},
	}

	elided := ElideRespondedToolResultImages(messages)
	result := elided[0].(types.ToolResultMessage)
	if len(result.Content) != 2 {
		t.Fatalf("expected note + tail, got %v", result.Content)
	}
	if _, ok := result.Content[0].(types.TextContent); !ok {
		t.Fatalf("expected leading note, got %v", result.Content[0])
	}
}

// TestElideRespondedToolResultImagesHandlesPointerMessages: sdk.go's filter
// family asserts pointer message forms; elision must handle them too.
func TestElideRespondedToolResultImagesHandlesPointerMessages(t *testing.T) {
	messages := []types.Message{
		&types.ToolResultMessage{Role: types.RoleToolResult, Content: []types.Content{elisionTestImage()}},
		&types.AssistantMessage{Role: types.RoleAssistant, Content: []types.Content{}},
	}

	elided := ElideRespondedToolResultImages(messages)
	result, ok := elided[0].(*types.ToolResultMessage)
	if !ok {
		t.Fatalf("expected *ToolResultMessage, got %T", elided[0])
	}
	if result.Content[0].ContentKind() != types.KindText {
		t.Fatalf("expected pointer-form elision, got %v", result.Content[0])
	}
}

// TestElideRespondedToolResultImagesReturnsOriginalWhenNothingToElide: the
// no-op path must return the input slice unchanged (identity preserved for
// cheap reuse).
func TestElideRespondedToolResultImagesReturnsOriginalWhenNothingToElide(t *testing.T) {
	messages := []types.Message{
		elisionToolResult("t1", elisionTestImage()), // fresh, nothing before last assistant
	}

	elided := ElideRespondedToolResultImages(messages)
	if len(elided) != 1 || elided[0].(types.ToolResultMessage).Content[1].ContentKind() != types.KindImage {
		t.Fatalf("expected untouched result, got %v", elided)
	}
}
