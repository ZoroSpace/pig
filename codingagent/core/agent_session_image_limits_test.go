package core

// agent_session_image_limits_test.go — pi v0.87.0 suite tests (f5c946480,
// #9631): prompt image normalization uses the model selected by
// before_agent_start (agent-session-prompt.test.ts), and tool result image
// normalization receives both the image settings and the current model
// profile (agent-session-tool-result-images.test.ts).

import (
	"context"
	"encoding/base64"
	"reflect"
	"testing"

	"github.com/samber/lo"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/codingagent/utils"
)

const tinyPNGBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8DwHwAFBQIAX8jx0gAAAABJRU5ErkJggg=="

// stubProcessImage replaces the ProcessImage entry used by prompt image
// normalization for the duration of the test (pi: vi.mock of
// image-process.ts).
func stubProcessImage(t *testing.T, stub func(data []byte, mimeType string, options *utils.ProcessImageOptions) utils.ProcessImageResult) {
	t.Helper()
	previous := processImageFn
	processImageFn = stub
	t.Cleanup(func() { processImageFn = previous })
}

// stubNormalizeToolResultImages replaces the tool result image normalization
// entry (pi: vi.mock of tool-result-images.ts).
func stubNormalizeToolResultImages(t *testing.T, stub func(content []types.Content, options *utils.NormalizeToolResultImagesOptions) ([]types.Content, error)) {
	t.Helper()
	previous := normalizeToolResultImagesFn
	normalizeToolResultImagesFn = stub
	t.Cleanup(func() { normalizeToolResultImagesFn = previous })
}

// TestPromptNormalizationUsesBeforeAgentStartSelectedModel pins pi's
// "uses the model selected by before_agent_start for image normalization":
// images are normalized after the extension hooks, with the resize profile
// of the model the hooks selected.
func TestPromptNormalizationUsesBeforeAgentStartSelectedModel(t *testing.T) {
	harness := createFauxHarness(t, &fauxHarnessOptions{
		modelID:    "wide",
		extensions: []*Extension{{Path: "model-switch", Handlers: map[string][]ExtensionHandler{}}},
	})

	strictModel := *harness.model
	strictModel.ID = "strict"
	resizeOptions := &types.ModelImageResizeOptions{MaxWidth: 1000, MaxHeight: 1000, MaxBytes: 500000, JpegQuality: 70}
	strictModel.InputLimits = &types.ModelInputLimits{Images: &types.ModelImageInputLimits{Resize: resizeOptions}}

	handler := func(ctx context.Context, extCtx *ExtensionContext, event ExtensionEvent) (any, error) {
		if err := harness.session.SetModel(ctx, &strictModel, nil); err != nil {
			return nil, err
		}
		return nil, nil
	}
	harness.session.extensionRunner.extensions[0].Handlers["before_agent_start"] = []ExtensionHandler{handler}

	var capturedOptions *utils.ProcessImageOptions
	stubProcessImage(t, func(data []byte, mimeType string, options *utils.ProcessImageOptions) utils.ProcessImageResult {
		capturedOptions = options
		return utils.ProcessImageResult{OK: true, Data: base64.StdEncoding.EncodeToString([]byte("normalized")), MimeType: mimeType}
	})
	harness.setResponses(fauxAssistantMessage("done"))

	err := harness.session.Prompt(context.Background(), "inspect", &PromptOptions{
		Images: []types.ImageContent{{Kind: types.KindImage, Data: tinyPNGBase64, MimeType: "image/png"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	harness.session.Agent.WaitForIdle()

	if id := harness.session.Model().ID; id != "strict" {
		t.Fatalf("model = %q, want strict", id)
	}
	if capturedOptions == nil {
		t.Fatal("processImage was not called")
	}
	if capturedOptions.AutoResizeImages == nil || !*capturedOptions.AutoResizeImages {
		t.Fatalf("autoResizeImages = %+v, want true", capturedOptions.AutoResizeImages)
	}
	wantResize := &utils.ImageResizeOptions{MaxWidth: lo.ToPtr(1000), MaxHeight: lo.ToPtr(1000), MaxBytes: lo.ToPtr(500000), JpegQuality: lo.ToPtr(70)}
	if !reflect.DeepEqual(capturedOptions.ResizeOptions, wantResize) {
		t.Fatalf("resizeOptions = %+v, want %+v", capturedOptions.ResizeOptions, wantResize)
	}

	var userContent types.UserContent
	for _, entry := range harness.sessionManager.GetEntries() {
		if messageEntry, ok := entry.(*SessionMessageEntry); ok {
			if userMessage, ok := messageEntry.Message.(types.UserMessage); ok {
				userContent = userMessage.Content
			}
		}
	}
	sawNormalizedImage := false
	for _, block := range userContent.Parts {
		if image, ok := block.(types.ImageContent); ok &&
			image.Data == base64.StdEncoding.EncodeToString([]byte("normalized")) && image.MimeType == "image/png" {
			sawNormalizedImage = true
		}
	}
	if !sawNormalizedImage {
		t.Fatalf("user message carries no normalized image: %+v", userContent)
	}
}

// TestToolResultNormalizationUsesModelResizeProfile pins pi's "passes image
// settings and the current model profile to tool result normalization".
func TestToolResultNormalizationUsesModelResizeProfile(t *testing.T) {
	screenshotTool := agent.AgentTool{
		Tool: types.Tool{
			Name:        "screenshot",
			Description: "Return a screenshot",
			Parameters:  rawJSON(`{"type":"object","properties":{}}`),
		},
		Label: "Screenshot",
		Execute: func(ctx context.Context, toolCallID string, params any, onUpdate agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
			return agent.AgentToolResult{
				Content: []types.Content{
					types.NewTextContent("captured"),
					types.ImageContent{Kind: types.KindImage, Data: tinyPNGBase64, MimeType: "image/png"},
				},
				Details: map[string]any{},
			}, nil
		},
	}
	harness := createFauxHarness(t, &fauxHarnessOptions{
		tools:    []agent.AgentTool{screenshotTool},
		settings: Settings{"images": map[string]any{"autoResize": false}},
	})
	resizeOptions := &types.ModelImageResizeOptions{MaxWidth: 1200, MaxHeight: 1000, MaxBytes: 500000, JpegQuality: 70}
	harness.model.InputLimits = &types.ModelInputLimits{Images: &types.ModelImageInputLimits{Resize: resizeOptions}}

	var capturedOptions *utils.NormalizeToolResultImagesOptions
	stubNormalizeToolResultImages(t, func(content []types.Content, options *utils.NormalizeToolResultImagesOptions) ([]types.Content, error) {
		capturedOptions = options
		return content, nil
	})
	harness.setResponses(
		fauxAssistantMessage(fauxToolCall("screenshot", map[string]any{}), func(m *types.AssistantMessage) { m.StopReason = types.StopReasonToolUse }),
		fauxAssistantMessage("done"),
	)

	err := harness.session.Prompt(context.Background(), "take a screenshot", nil)
	if err != nil {
		t.Fatal(err)
	}
	harness.session.Agent.WaitForIdle()

	if capturedOptions == nil {
		t.Fatal("normalizeToolResultImages was not called")
	}
	if capturedOptions.AutoResizeImages == nil || *capturedOptions.AutoResizeImages {
		t.Fatalf("autoResizeImages = %+v, want false", capturedOptions.AutoResizeImages)
	}
	if capturedOptions.ResizeOptions == nil || capturedOptions.ResizeOptions != resizeOptions {
		t.Fatalf("resizeOptions = %+v, want the model profile", capturedOptions.ResizeOptions)
	}
}
