// render_text_output_test.go — regression lock for GetTextOutput's
// dual-form content matching
// (docs/fix/2026-09-11-tool-execution-live-output-empty.md).
//
// The builtin tools build result content blocks as *pointers*
// (asTextContent → &types.TextContent, read.go → &types.ImageContent)
// while the JSONL/LLM decoders produce values. GetTextOutput used to match
// only the value forms, so every live tool card rendered with the "Took"
// footer but an empty output body — resume worked because decoded transcript
// entries carry value blocks.
package tools

import (
	"strings"
	"testing"

	"github.com/ZoroSpace/pig/ai/types"
)

func TestGetTextOutputHandlesPointerBlocks(t *testing.T) {
	const probe = "LIVE OUTPUT PROBE"

	// Live path: pointer text block (asTextContent).
	got := GetTextOutput(&ToolRenderResult{
		Content: []types.Content{&types.TextContent{Kind: types.KindText, Text: probe}},
	}, false)
	if !strings.Contains(got, probe) {
		t.Fatalf("pointer text block dropped: %q", got)
	}

	// Resume path: value text block (decoders).
	got = GetTextOutput(&ToolRenderResult{
		Content: []types.Content{types.TextContent{Kind: types.KindText, Text: probe}},
	}, false)
	if !strings.Contains(got, probe) {
		t.Fatalf("value text block dropped: %q", got)
	}
}

func TestGetTextOutputHandlesPointerImageBlocks(t *testing.T) {
	img := func() types.Content {
		return &types.ImageContent{Kind: types.KindImage, Data: "aGk=", MimeType: "image/png"}
	}
	got := GetTextOutput(&ToolRenderResult{Content: []types.Content{img()}}, false)
	if !strings.Contains(got, "image/png") {
		t.Fatalf("pointer image block dropped: %q", got)
	}

	value := types.ImageContent{Kind: types.KindImage, Data: "aGk=", MimeType: "image/png"}
	got = GetTextOutput(&ToolRenderResult{Content: []types.Content{value}}, false)
	if !strings.Contains(got, "image/png") {
		t.Fatalf("value image block dropped: %q", got)
	}
}
