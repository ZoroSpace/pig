// pi: src/harness/tools/read.ts — the read tool.
//
// Segment deviations:
//   - params decode into ReadToolInput via JSON round-trip (pi: Static of
//     the TypeBox schema); numbers are *float64 (TS number) and nil = unset.
//   - TextDecoder().decode's lenient UTF-8 → strings.ToValidUTF8 with U+FFFD
//     replacement (substitute counts may differ for exotic invalid input;
//     not behavior-locked).
//   - The injected imageProcessor is synchronous (pi: async callback).
//   - The satisfies-annotated content arrays are []types.Content with
//     pointer block structs.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ZoroSpace/pig/agent"
	"github.com/ZoroSpace/pig/agent/harness"
	"github.com/ZoroSpace/pig/agent/harness/utils"
	"github.com/ZoroSpace/pig/ai/types"
)

// readSchemaJSON is the read tool's parameter schema (pi: readSchema via
// TypeBox).
const readSchemaJSON = `{
  "type": "object",
  "properties": {
    "path": {
      "type": "string",
      "description": "Path to the file to read (relative or absolute)"
    },
    "offset": {
      "type": "number",
      "description": "Line number to start reading from (1-indexed)"
    },
    "limit": {
      "type": "number",
      "description": "Maximum number of lines to read"
    }
  },
  "required": ["path"]
}`

// ReadToolInput is the read tool's parameter shape (pi: ReadToolInput).
type ReadToolInput struct {
	Path   string   `json:"path"`
	Offset *float64 `json:"offset,omitempty"`
	Limit  *float64 `json:"limit,omitempty"`
}

// ReadToolDetails carries the truncation outcome (pi: ReadToolDetails).
type ReadToolDetails struct {
	Truncation *utils.TruncationResult `json:"truncation,omitempty"`
}

// ReadImageProcessorOptions carry the processor flags (pi: the options
// argument of ReadImageProcessor).
type ReadImageProcessorOptions struct {
	// AutoResizeImages is whether the processor should resize images.
	AutoResizeImages bool
}

// ReadImageProcessorResult is the outcome of an injected image processor
// (pi: ReadImageProcessorResult union → single struct; OK discriminates the
// success payload from Message).
type ReadImageProcessorResult struct {
	// OK mirrors the { ok: true } union member.
	OK bool
	// Data is the processed image payload (base64 when applicable).
	Data string
	// MimeType is the processed image's mime type.
	MimeType string
	// Hints are extra model-visible lines appended to the text block.
	Hints []string
	// Message is the failure message of the { ok: false } member.
	Message string
}

// ReadImageProcessor converts/resizes images before they are returned (pi:
// ReadImageProcessor; the async callback collapses to sync).
type ReadImageProcessor func(bytes []byte, mimeType string, options ReadImageProcessorOptions) ReadImageProcessorResult

// ReadToolOptions parameterize the read tool (pi: ReadToolOptions).
type ReadToolOptions struct {
	// AutoResizeImages is passed to an injected image processor. Pointer:
	// TS optional; nil = pi's true default.
	AutoResizeImages *bool
	// ImageProcessor is an optional image conversion/resizing
	// implementation.
	ImageProcessor ReadImageProcessor
}

// CreateReadTool builds the read tool (pi: createReadTool).
func CreateReadTool(options *ReadToolOptions) *harness.AgentHarnessTool {
	return &harness.AgentHarnessTool{
		Name:        "read",
		Label:       "read",
		Description: fmt.Sprintf("Read the contents of a file. Supports text files and images (jpg, png, gif, webp, bmp). Images are sent as attachments. For text files, output is truncated to %d lines or %dKB (whichever is hit first). Use offset/limit for large files. When you need the full file, continue with offset until complete.", utils.DefaultMaxLines, utils.DefaultMaxBytes/1024),
		Parameters:  json.RawMessage(readSchemaJSON),
		Execute: func(ctx context.Context, toolCallID string, params any, onUpdate agent.AgentToolUpdateCallback, context any) (agent.AgentToolResult, error) {
			input, err := decodeParams[ReadToolInput](params)
			if err != nil {
				return agent.AgentToolResult{}, err
			}
			executionContext, err := executionContextOf(context)
			if err != nil {
				return agent.AgentToolResult{}, err
			}
			env := executionContext.Env

			absolutePath, err := ResolveReadToolPath(ctx, env, input.Path)
			if err != nil {
				return agent.AgentToolResult{}, err
			}
			bytes, err := env.ReadBinaryFile(ctx, absolutePath)
			if err != nil {
				return agent.AgentToolResult{}, err
			}
			mimeType := DetectSupportedImageMimeType(bytes)
			if mimeType != "" {
				if options != nil && options.ImageProcessor != nil {
					autoResize := true
					if options.AutoResizeImages != nil {
						autoResize = *options.AutoResizeImages
					}
					processed := options.ImageProcessor(bytes, mimeType, ReadImageProcessorOptions{AutoResizeImages: autoResize})
					if !processed.OK {
						return agent.AgentToolResult{
							Content: []types.Content{
								&types.TextContent{Kind: types.KindText, Text: fmt.Sprintf("Read image file [%s]\n%s", mimeType, processed.Message)},
							},
						}, nil
					}
					hints := ""
					if len(processed.Hints) > 0 {
						hints = "\n" + strings.Join(processed.Hints, "\n")
					}
					return agent.AgentToolResult{
						Content: []types.Content{
							&types.TextContent{Kind: types.KindText, Text: fmt.Sprintf("Read image file [%s]%s", processed.MimeType, hints)},
							&types.ImageContent{Kind: types.KindImage, Data: processed.Data, MimeType: processed.MimeType},
						},
					}, nil
				}
				if mimeType == "image/bmp" {
					return agent.AgentToolResult{
						Content: []types.Content{
							&types.TextContent{Kind: types.KindText, Text: "Read image file [image/bmp]\n[Image omitted: configure an imageProcessor to convert BMP images.]"},
						},
					}, nil
				}
				return agent.AgentToolResult{
					Content: []types.Content{
						&types.TextContent{Kind: types.KindText, Text: fmt.Sprintf("Read image file [%s]", mimeType)},
						&types.ImageContent{Kind: types.KindImage, Data: EncodeBase64(bytes), MimeType: mimeType},
					},
				}, nil
			}

			textContent := strings.ToValidUTF8(string(bytes), "\uFFFD")
			allLines := strings.Split(textContent, "\n")
			totalFileLines := len(allLines)
			startLine := 0
			if input.Offset != nil && *input.Offset != 0 {
				startLine = int(*input.Offset) - 1
				if startLine < 0 {
					startLine = 0
				}
			}
			startLineDisplay := startLine + 1
			if startLine >= len(allLines) {
				return agent.AgentToolResult{}, fmt.Errorf("Offset %s is beyond end of file (%d lines total)", formatNumber(input.Offset), len(allLines))
			}

			var selectedContent string
			userLimitedLines := -1
			if input.Limit != nil {
				endLine := startLine + int(*input.Limit)
				if endLine > len(allLines) {
					endLine = len(allLines)
				}
				selectedContent = strings.Join(allLines[startLine:endLine], "\n")
				userLimitedLines = endLine - startLine
			} else {
				selectedContent = strings.Join(allLines[startLine:], "\n")
			}

			truncation := utils.TruncateHead(selectedContent, nil)
			var outputText string
			var details *ReadToolDetails
			if truncation.FirstLineExceedsLimit {
				firstLineSize := utils.FormatSize(int64(len(allLines[startLine])))
				outputText = fmt.Sprintf("[Line %d is %s, exceeds %s limit. Use bash: sed -n '%dp' %s | head -c %d]",
					startLineDisplay, firstLineSize, utils.FormatSize(utils.DefaultMaxBytes), startLineDisplay, input.Path, utils.DefaultMaxBytes)
				details = &ReadToolDetails{Truncation: &truncation}
			} else if truncation.Truncated {
				endLineDisplay := startLineDisplay + truncation.OutputLines - 1
				nextOffset := endLineDisplay + 1
				outputText = truncation.Content
				if truncation.TruncatedBy == utils.TruncatedByLines {
					outputText += fmt.Sprintf("\n\n[Showing lines %d-%d of %d. Use offset=%d to continue.]",
						startLineDisplay, endLineDisplay, totalFileLines, nextOffset)
				} else {
					outputText += fmt.Sprintf("\n\n[Showing lines %d-%d of %d (%s limit). Use offset=%d to continue.]",
						startLineDisplay, endLineDisplay, totalFileLines, utils.FormatSize(utils.DefaultMaxBytes), nextOffset)
				}
				details = &ReadToolDetails{Truncation: &truncation}
			} else if userLimitedLines != -1 && startLine+userLimitedLines < len(allLines) {
				remaining := len(allLines) - (startLine + userLimitedLines)
				nextOffset := startLine + userLimitedLines + 1
				outputText = fmt.Sprintf("%s\n\n[%d more lines in file. Use offset=%d to continue.]", truncation.Content, remaining, nextOffset)
			} else {
				outputText = truncation.Content
			}

			var detailsValue any
			if details != nil {
				detailsValue = details
			}
			return agent.AgentToolResult{
				Content: []types.Content{
					&types.TextContent{Kind: types.KindText, Text: outputText},
				},
				Details: detailsValue,
			}, nil
		},
	}
}

// formatNumber renders an optional TS number the way `${offset}` would.
func formatNumber(value *float64) string {
	if value == nil {
		return "undefined"
	}
	return strconvFormatFloat(*value)
}

// strconvFormatFloat formats a TS-style number (shortest round-trip).
func strconvFormatFloat(f float64) string {
	return fmt.Sprintf("%v", f)
}
