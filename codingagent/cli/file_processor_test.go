package cli

// file_processor_test.go — pi: test/image-resize-callers.test.ts (f5c946480,
// #9631): the @file processor omits image attachments when auto-resize
// cannot produce a safe image, and can defer resizing file attachments until
// prompt dispatch (autoResizeImages=false leaves the bytes untouched;
// AgentSession resizes them after the model is selected).

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ZoroSpace/pig/codingagent/utils"
	"github.com/samber/lo"
)

const tinyPNGBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8DwHwAFBQIAX8jx0gAAAABJRU5ErkJggg=="

// stubResizeImage replaces the resize pipeline for the duration of the test
// (pi: vi.mock of the image-resize module).
func stubResizeImage(t *testing.T, stub func(inputBytes []byte, mimeType string, options *utils.ImageResizeOptions) *utils.ResizedImage) {
	t.Helper()
	previous := utils.ResizeImageFn
	utils.ResizeImageFn = stub
	t.Cleanup(func() { utils.ResizeImageFn = previous })
}

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

func newTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "image-resize-callers-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

// TestFileProcessorOmitsImageWhenResizeCannotProduceSafeImage pins pi's
// "file processor omits image attachments when auto-resize cannot produce a
// safe image".
func TestFileProcessorOmitsImageWhenResizeCannotProduceSafeImage(t *testing.T) {
	dir := newTempDir(t)
	imagePath := writeTinyPNG(t, dir)
	stubResizeImage(t, func([]byte, string, *utils.ImageResizeOptions) *utils.ResizedImage {
		return nil
	})

	result := ProcessFileArguments([]string{imagePath}, nil)
	if len(result.Images) != 0 {
		t.Fatalf("images = %d, want 0", len(result.Images))
	}
	if !strings.Contains(result.Text, "Image omitted") {
		t.Fatalf("text = %q, want an Image omitted note", result.Text)
	}
}

// TestFileProcessorCanDeferResizingUntilPromptDispatch pins pi's "can defer
// resizing file attachments until prompt dispatch": with autoResizeImages
// disabled the pipeline is not invoked and the raw attachment survives.
func TestFileProcessorCanDeferResizingUntilPromptDispatch(t *testing.T) {
	dir := newTempDir(t)
	imagePath := writeTinyPNG(t, dir)
	called := false
	stubResizeImage(t, func([]byte, string, *utils.ImageResizeOptions) *utils.ResizedImage {
		called = true
		return nil
	})

	result := ProcessFileArguments([]string{imagePath}, &ProcessFileOptions{AutoResizeImages: lo.ToPtr(false)})
	if len(result.Images) != 1 {
		t.Fatalf("images = %d, want 1", len(result.Images))
	}
	if called {
		t.Fatal("resize pipeline must not run when resizing is deferred")
	}
}
