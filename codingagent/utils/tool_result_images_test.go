// pi: test/tool-result-images.test.ts — normalizeToolResultImages behavior.
// Photon-dependent expectations are adapted to the Phase 8 placeholder
// (resizeImage no-ops, convertImageBytesToPng fails), see the
// image_process.go header.
package utils

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/ZoroSpace/pig/ai/types"
)

const tinyPngBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8DwHwAFBQIAX8jx0gAAAABJRU5ErkJggg=="

func imageBlock(b64 string, mimeType string) types.Content {
	return types.ImageContent{Kind: types.KindImage, Data: b64, MimeType: mimeType}
}

func TestNormalizeToolResultImagesReturnsTheOriginalArrayWhenThereAreNoImageBlocks(t *testing.T) {
	content := []types.Content{types.NewTextContent("no images here")}

	normalized, err := NormalizeToolResultImages(content, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(normalized) != 1 || normalized[0].ContentKind() != types.KindText {
		t.Fatalf("expected unchanged content, got %v", normalized)
	}
}

func TestNormalizeToolResultImagesReturnsTheOriginalArrayWhenImagesAreAlreadyWithinLimits(t *testing.T) {
	content := []types.Content{
		types.NewTextContent("screenshot"),
		imageBlock(tinyPngBase64, "image/png"),
	}

	normalized, err := NormalizeToolResultImages(content, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(normalized) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(normalized))
	}
	if _, ok := normalized[1].(types.ImageContent); !ok {
		t.Fatalf("expected image block, got %T", normalized[1])
	}
}

func TestNormalizeToolResultImagesLeavesOversizedImagesAloneUntilPhotonArrives(t *testing.T) {
	// pi resizes a 2400x4800 PNG and appends a dimension note. pig's
	// resizeImage is a Phase 8 no-op, so the block passes through unchanged
	// (declared deviation, core/tools batch).
	content := []types.Content{imageBlock(tinyPngBase64, "image/png")}

	normalized, err := NormalizeToolResultImages(content, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(normalized) != 1 {
		t.Fatalf("expected 1 block, got %d", len(normalized))
	}
}

func TestNormalizeToolResultImagesLeavesOversizedImagesAloneWhenAutoResizeIsDisabled(t *testing.T) {
	disabled := false
	content := []types.Content{imageBlock(tinyPngBase64, "image/png")}

	normalized, err := NormalizeToolResultImages(content, &NormalizeToolResultImagesOptions{AutoResizeImages: &disabled})
	if err != nil {
		t.Fatal(err)
	}
	if len(normalized) != 1 {
		t.Fatalf("expected 1 block, got %d", len(normalized))
	}
}

func TestNormalizeToolResultImagesKeepsUndecodableImagesInsteadOfDroppingToolOutput(t *testing.T) {
	content := []types.Content{imageBlock("bm90LWFuLWltYWdl", "image/png")}

	normalized, err := NormalizeToolResultImages(content, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(normalized) != 1 {
		t.Fatalf("expected 1 block, got %d", len(normalized))
	}
	if image, ok := normalized[0].(types.ImageContent); !ok || image.Data != "bm90LWFuLWltYWdl" {
		t.Fatalf("expected original block preserved, got %v", normalized[0])
	}
}

func TestNormalizeToolResultImagesPreservesSurroundingTextBlocksAndTheirOrder(t *testing.T) {
	if _, err := base64.StdEncoding.DecodeString(tinyPngBase64); err != nil {
		t.Fatal(err)
	}
	content := []types.Content{
		types.NewTextContent("before"),
		imageBlock(tinyPngBase64, "image/png"),
		types.NewTextContent("after"),
	}

	normalized, err := NormalizeToolResultImages(content, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(normalized) != 3 {
		t.Fatalf("expected 3 blocks, got %d", len(normalized))
	}
	if normalized[0].ContentKind() != types.KindText || normalized[2].ContentKind() != types.KindText {
		t.Fatalf("expected text bookends, got %v", normalized)
	}
	if text, ok := normalized[0].(types.TextContent); !ok || text.Text != "before" {
		t.Fatalf("expected 'before' text preserved, got %v", normalized[0])
	}
	if text, ok := normalized[2].(types.TextContent); !ok || !strings.EqualFold(text.Text, "after") {
		t.Fatalf("expected 'after' text preserved, got %v", normalized[2])
	}
}
