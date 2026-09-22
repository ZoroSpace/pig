// Regression tests for the image process pipeline (pig-specific; pi-side
// behavior is locked by tool-result-images.test.ts adaptations in
// tool_result_images_test.go). Covers the Go-native resize/transcode
// implementation that replaced the photon placeholder:
// docs/fix/2026-09-22-image-tool-413-oversize-and-history-bloat.md.
package utils

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"

	"github.com/ZoroSpace/pig/ai/types"
	xbmp "golang.org/x/image/bmp"
)

// makeTestImage builds a w×h RGBA image with position-dependent colors
// (non-uniform so codecs produce realistic payloads).
func makeTestImage(w int, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, color.RGBA{
				R: uint8(x % 251),
				G: uint8(y % 241),
				B: uint8((x + y) % 253),
				A: 255,
			})
		}
	}
	return img
}

func makeTestPNGBase64(t *testing.T, w int, h int) string {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, makeTestImage(w, h)); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

func makeTestJPEG(t *testing.T, w int, h int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, makeTestImage(w, h), &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func mustDecodeImage(t *testing.T, data []byte) image.Image {
	t.Helper()
	decoded, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return decoded
}

func TestNormalizeToolResultImagesResizesOversizedImagesWithDimensionNote(t *testing.T) {
	// pi resizes a 2400x4800 PNG and appends a dimension note; pig now does
	// the same via Go-native codecs (the former placeholder passed the block
	// through unchanged).
	content := []types.Content{imageBlock(makeTestPNGBase64(t, 2400, 4800), "image/png")}

	normalized, err := NormalizeToolResultImages(content, nil)
	if err != nil {
		t.Fatal(err)
	}
	image, hint, ok := imageBlockWithHint(normalized)
	if !ok {
		t.Fatalf("expected image block + hint, got %v", normalized)
	}
	if image.MimeType != "image/png" {
		t.Fatalf("expected image/png, got %s", image.MimeType)
	}
	if !strings.Contains(hint, "[Image: original 2400x4800, displayed at ") {
		t.Fatalf("expected dimension note, got %q", hint)
	}
	decoded := mustDecodeImage(t, mustDecodeBase64ForTest(t, image.Data))
	if bounds := decoded.Bounds(); bounds.Dx() > 2000 || bounds.Dy() > 2000 {
		t.Fatalf("expected resized image within 2000x2000, got %dx%d", bounds.Dx(), bounds.Dy())
	}
}

// imageBlockWithHint extracts the image block and the appended hint text
// (NormalizeToolResultImages emits hints as a trailing text block).
func imageBlockWithHint(content []types.Content) (types.ImageContent, string, bool) {
	var image types.ImageContent
	hasImage := false
	hint := ""
	for _, block := range content {
		switch b := block.(type) {
		case types.ImageContent:
			image, hasImage = b, true
		case types.TextContent:
			if strings.HasPrefix(b.Text, "[Image:") || strings.HasPrefix(b.Text, "[Image converted") {
				hint = b.Text
			}
		case *types.ImageContent:
			image, hasImage = *b, true
		}
	}
	return image, hint, hasImage
}

func TestProcessImageResizesOversizedJpegWithDimensionNote(t *testing.T) {
	input := makeTestJPEG(t, 2736, 3648)

	result := ProcessImage(input, "image/jpeg", &ProcessImageOptions{AutoResizeImages: boolPtr(true)})
	if !result.OK {
		t.Fatalf("expected OK, got message %q", result.Message)
	}
	if result.MimeType != "image/jpeg" {
		t.Fatalf("expected image/jpeg, got %s", result.MimeType)
	}
	if len(result.Hints) != 1 || !strings.Contains(result.Hints[0], "[Image: original 2736x3648, displayed at 1500x2000. Multiply coordinates by 1.82") {
		t.Fatalf("expected dimension note, got %v", result.Hints)
	}
	decoded := mustDecodeImage(t, mustDecodeBase64ForTest(t, result.Data))
	bounds := decoded.Bounds()
	if bounds.Dx() != 1500 || bounds.Dy() != 2000 {
		t.Fatalf("expected 1500x2000, got %dx%d", bounds.Dx(), bounds.Dy())
	}
	got, err := base64.StdEncoding.DecodeString(result.Data)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) >= len(input) {
		t.Fatalf("expected resized payload smaller than %d bytes, got %d", len(input), len(got))
	}
}

func TestProcessImagePassesThroughImagesAlreadyWithinLimits(t *testing.T) {
	input := makeTestJPEG(t, 800, 600)

	result := ProcessImage(input, "image/jpeg", &ProcessImageOptions{AutoResizeImages: boolPtr(true)})
	if !result.OK {
		t.Fatalf("expected OK, got message %q", result.Message)
	}
	if len(result.Hints) != 0 {
		t.Fatalf("expected no hints for in-limit image, got %v", result.Hints)
	}
	if result.Data != base64.StdEncoding.EncodeToString(input) {
		t.Fatal("expected byte-identical pass-through without re-encode")
	}
}

func TestProcessImageConvertsBmpToPng(t *testing.T) {
	var bmp bytes.Buffer
	if err := xbmp.Encode(&bmp, makeTestImage(64, 48)); err != nil {
		t.Fatal(err)
	}

	result := ProcessImage(bmp.Bytes(), "image/bmp", &ProcessImageOptions{AutoResizeImages: boolPtr(true)})
	if !result.OK {
		t.Fatalf("expected BMP→PNG conversion, got message %q", result.Message)
	}
	if result.MimeType != "image/png" {
		t.Fatalf("expected image/png, got %s", result.MimeType)
	}
	if len(result.Hints) != 1 || result.Hints[0] != "[Image converted from image/bmp to image/png.]" {
		t.Fatalf("expected conversion hint, got %v", result.Hints)
	}
	if _, _, err := image.Decode(bytes.NewReader(mustDecodeBase64ForTest(t, result.Data))); err != nil {
		t.Fatal(err)
	}
}

func TestProcessImageAnimatedGifPassesThroughUnchanged(t *testing.T) {
	var buf bytes.Buffer
	err := gif.EncodeAll(&buf, &gif.GIF{
		Image: []*image.Paletted{
			makeTestPaletted(t, 32, 32, 0),
			makeTestPaletted(t, 32, 32, 1),
		},
		Delay: []int{10, 10},
	})
	if err != nil {
		t.Fatal(err)
	}
	input := buf.Bytes()

	result := ProcessImage(input, "image/gif", &ProcessImageOptions{AutoResizeImages: boolPtr(true)})
	if !result.OK {
		t.Fatalf("expected OK, got message %q", result.Message)
	}
	if result.Data != base64.StdEncoding.EncodeToString(input) {
		t.Fatal("expected animated GIF pass-through unchanged")
	}
	if len(result.Hints) != 0 {
		t.Fatalf("expected no hints, got %v", result.Hints)
	}
}

func TestProcessImageOmitsWhenMaxEncodedBytesBudgetUnreachable(t *testing.T) {
	input := makeTestJPEG(t, 2736, 3648)
	budget := 64

	result := ProcessImage(input, "image/jpeg", &ProcessImageOptions{
		AutoResizeImages: boolPtr(true),
		ResizeOptions:    &ImageResizeOptions{MaxEncodedBytes: &budget},
	})
	if result.OK {
		t.Fatal("expected not-OK when the MaxEncodedBytes budget is unreachable")
	}
	if result.Message != "[Image omitted: could not be resized below the inline image size limit.]" {
		t.Fatalf("unexpected message %q", result.Message)
	}
}

func TestProcessImageUndecodableBytesPassThrough(t *testing.T) {
	input := []byte("not really a jpeg but the sniffer claimed image/jpeg")

	result := ProcessImage(input, "image/jpeg", &ProcessImageOptions{AutoResizeImages: boolPtr(true)})
	if !result.OK {
		t.Fatalf("expected OK pass-through, got message %q", result.Message)
	}
	if result.Data != base64.StdEncoding.EncodeToString(input) {
		t.Fatal("expected undecodable bytes passed through unchanged")
	}
}

func makeTestPaletted(t *testing.T, w int, h int, phase int) *image.Paletted {
	t.Helper()
	palette := color.Palette{color.RGBA{R: 255, A: 255}, color.RGBA{G: 255, A: 255}}
	img := image.NewPaletted(image.Rect(0, 0, w, h), palette)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if (x+y+phase)%2 == 0 {
				img.SetColorIndex(x, y, 1)
			}
		}
	}
	return img
}

func mustDecodeBase64ForTest(t *testing.T, data string) []byte {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func boolPtr(v bool) *bool {
	return &v
}
