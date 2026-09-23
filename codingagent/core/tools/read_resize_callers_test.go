package tools

// read_resize_callers_test.go — pi: test/image-resize-callers.test.ts
// (f5c946480, #9631): the read tool honors the model's resize profile from
// the execution context and degrades to text-only output when the resize
// pipeline cannot produce a safe image.

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/samber/lo"

	"github.com/ZoroSpace/pig/ai/types"
	"github.com/ZoroSpace/pig/codingagent/utils"
)

const tinyPNGBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8DwHwAFBQIAX8jx0gAAAABJRU5ErkJggg=="

// fakeToolContext is a minimal ToolExecuteContext for tests.
type fakeToolContext struct {
	cwd   string
	model *types.Model
}

func (f *fakeToolContext) GetModel() *types.Model   { return f.model }
func (f *fakeToolContext) GetSessionID() string     { return "test-session" }
func (f *fakeToolContext) GetSessionFile() *string  { return nil }
func (f *fakeToolContext) GetThinkingLevel() string { return "" }
func (f *fakeToolContext) GetCwd() string           { return f.cwd }

// stubResizeImage replaces the resize pipeline for the duration of the test
// (pi: vi.mock of the image-resize module). The stub receives the input
// bytes, mime type, and the options ProcessImage resolved.
func stubResizeImage(t *testing.T, stub func(inputBytes []byte, mimeType string, options *utils.ImageResizeOptions) *utils.ResizedImage) {
	t.Helper()
	previous := utils.ResizeImageFn
	utils.ResizeImageFn = stub
	t.Cleanup(func() { utils.ResizeImageFn = previous })
}

// writeTinyPNG writes the 1x1 test PNG into dir and returns its path.
func writeTinyPNG(t *testing.T, dir string) string {
	t.Helper()
	data, err := base64.StdEncoding.DecodeString(tinyPNGBase64)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "test.png")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestReadToolTextOnlyWhenResizeCannotProduceSafeImage pins pi's
// "read tool returns text-only output when auto-resize cannot produce a safe
// image": the omitted note replaces the image attachment.
func TestReadToolTextOnlyWhenResizeCannotProduceSafeImage(t *testing.T) {
	dir := newTestDir(t)
	imagePath := writeTinyPNG(t, dir)
	stubResizeImage(t, func([]byte, string, *utils.ImageResizeOptions) *utils.ResizedImage {
		return nil
	})

	readTool := CreateReadTool(cwdForTest(), nil)
	result, err := readTool.ExecuteWithToolContext(t.Context(), "test-read-image", map[string]any{"path": imagePath}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Content) != 1 {
		t.Fatalf("content length = %d, want 1 text block", len(result.Content))
	}
	if !strings.Contains(textOf(result), "Image omitted") {
		t.Fatalf("output = %q, want an Image omitted note", textOf(result))
	}
}

// TestReadToolPassesModelResizeProfile pins pi's "passes the current model
// resize profile to the read tool": ctx.model.inputLimits.images.resize
// reaches the resize pipeline.
func TestReadToolPassesModelResizeProfile(t *testing.T) {
	dir := newTestDir(t)
	imagePath := writeTinyPNG(t, dir)
	var capturedOptions *utils.ImageResizeOptions
	stubResizeImage(t, func(_ []byte, _ string, options *utils.ImageResizeOptions) *utils.ResizedImage {
		capturedOptions = options
		return nil
	})

	model := &types.Model{
		ID:       "vision-model",
		Name:     "Vision model",
		API:      types.Api("test"),
		Provider: "test",
		BaseURL:  "https://example.com",
		Input:    []types.ModelInput{types.ModelInputText, types.ModelInputImage},
		InputLimits: &types.ModelInputLimits{
			Images: &types.ModelImageInputLimits{
				Resize: &types.ModelImageResizeOptions{
					MaxWidth:    1234,
					MaxHeight:   1000,
					MaxBytes:    500000,
					JpegQuality: 70,
				},
			},
		},
	}
	ctx := &fakeToolContext{cwd: dir, model: model}

	tool := CreateReadTool(dir, nil)
	if _, err := tool.ExecuteWithToolContext(t.Context(), "test-read-model-profile", map[string]any{"path": imagePath}, nil, ctx); err != nil {
		t.Fatal(err)
	}
	want := &utils.ImageResizeOptions{MaxWidth: lo.ToPtr(1234), MaxHeight: lo.ToPtr(1000), MaxBytes: lo.ToPtr(500000), JpegQuality: lo.ToPtr(70)}
	if !reflect.DeepEqual(capturedOptions, want) {
		t.Fatalf("resize options = %+v, want %+v", capturedOptions, want)
	}
}
